package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"omixor-core/config"
	"omixor-core/store"
)

// PostgreSQL 存放處(設了 OMIXOR_DATABASE_URL 才用;本機模式一定要有,組織、Issue 等資料都在核心的表)。
//
//	omixor_documents  這台機器的設定 JSON 檔(orgs.json 的目前組織、connections.json…),path 是相對資料夾的路徑
//	omixor_secrets    連線 token、client secret、LDAP 服務帳號密碼;用 OMIXOR_SECRET_KEY 以 AES-256-GCM 加密
//	omixor_sessions   網頁登入的 session(只存 token 的 SHA-256)
//	omixor_activity   動態牆(全部歷史)
//	omixor_state      範例資料的變更(告警、事件、工作項目…;Swift 版是重開就重置)
//	omixor_issues     Issue(與 omixor_issue_comments;見 store_pg_issues.go,不放在 omixor_documents)
//
// 這個版本只支援一台 omixor-web 連同一個資料庫(畫面即時通知與記憶體裡的狀態不跨主機)。

var (
	// pgPoolPtr 目前的連線池;用 atomic 讀寫(CloseDB 設 nil 時,別的 goroutine 可能正在讀)。要用請走 pgPool() 與 pgExec / pgQuery / pgQueryRow
	pgPoolPtr atomic.Pointer[pgxpool.Pool]
	// 密鑰加密用的金鑰(32 bytes)
	pgKey []byte
)

// pgPool 目前的連線池;沒開資料庫或已關閉是 nil
func pgPool() *pgxpool.Pool { return pgPoolPtr.Load() }

// pgExec、pgQuery、pgQueryRow 同 pool 的同名方法,但連線池是 nil(沒開或已 CloseDB)時回 errPGClosed 而不是當機
func pgExec(ctx context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	p := pgPool()
	if p == nil {
		return pgconn.CommandTag{}, errPGClosed
	}
	return p.Exec(ctx, q, args...)
}

func pgQuery(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
	p := pgPool()
	if p == nil {
		return nil, errPGClosed
	}
	return p.Query(ctx, q, args...)
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

func pgQueryRow(ctx context.Context, q string, args ...any) pgx.Row {
	p := pgPool()
	if p == nil {
		return errRow{errPGClosed}
	}
	return p.QueryRow(ctx, q, args...)
}

// 背景寫入(動態牆、範例資料、刪組織時清資料)用 pgGo 開,CloseDB 關連線池前會等它們做完(有上限),不會在關閉後碰到 nil 的連線池。
var pgBg struct {
	sync.Mutex
	closing bool
	wg      sync.WaitGroup
}

// pgGo 在背景跑 f 並登記;CloseDB 進行中(或已關閉)不再接新的,直接略過
func pgGo(f func()) {
	pgBg.Lock()
	if pgBg.closing {
		pgBg.Unlock()
		return
	}
	pgBg.wg.Add(1)
	pgBg.Unlock()
	go func() {
		defer pgBg.wg.Done()
		f()
	}()
}

// waitPGBackground 等登記的背景寫入結束,最多 d;回傳是否都結束了
func waitPGBackground(d time.Duration) bool {
	done := make(chan struct{})
	go func() { pgBg.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// errPGClosed 資料庫已關閉(CloseDB 之後)還要寫
var errPGClosed = errors.New("資料庫連線已關閉")

// DB 目前的資料庫連線;沒用資料庫時是 nil
func DB() *pgxpool.Pool { return pgPool() }

const pgSchema = `
create table if not exists omixor_documents (
	path text primary key,
	data jsonb not null,
	updated_at timestamptz not null default now()
);
create table if not exists omixor_secrets (
	account text primary key,
	nonce bytea not null,
	ciphertext bytea not null,
	updated_at timestamptz not null default now()
);
create table if not exists omixor_sessions (
	token_hash text primary key,
	user_json jsonb not null,
	expires_at timestamptz not null,
	created_at timestamptz not null default now()
);
create index if not exists omixor_sessions_expires on omixor_sessions (expires_at);
create table if not exists omixor_activity (
	seq bigserial primary key,
	id text not null unique,
	at timestamptz not null,
	item jsonb not null
);
create index if not exists omixor_activity_at on omixor_activity (at desc);
create table if not exists omixor_state (
	name text primary key,
	data jsonb not null,
	updated_at timestamptz not null default now()
);
create table if not exists omixor_files (
	path text primary key,
	data bytea not null,
	created_at timestamptz not null default now()
);
`

// pgExtraSchema 其他檔案登記的表(Issue 的表由核心 core/store/pg.go 建,不在這裡)
var pgExtraSchema []string

// pgLegacyRenames 舊版(ops_ 前綴)的資料表與其索引、序號、主鍵,改成 omixor_ 前綴的清單。
// kind 是 alter 的關鍵字;主鍵、唯一約束背後的索引用 index(改索引名會連約束名一起改),bigserial 的序號用 sequence。
// 新名字要和下面 pgSchema 全新建出來的一樣。
var pgLegacyRenames = []struct{ kind, old, new string }{
	{"table", "ops_documents", "omixor_documents"},
	{"table", "ops_secrets", "omixor_secrets"},
	{"table", "ops_sessions", "omixor_sessions"},
	{"table", "ops_activity", "omixor_activity"},
	{"table", "ops_state", "omixor_state"},
	{"table", "ops_files", "omixor_files"},
	{"index", "ops_documents_pkey", "omixor_documents_pkey"},
	{"index", "ops_secrets_pkey", "omixor_secrets_pkey"},
	{"index", "ops_sessions_pkey", "omixor_sessions_pkey"},
	{"index", "ops_sessions_expires", "omixor_sessions_expires"},
	{"index", "ops_activity_pkey", "omixor_activity_pkey"},
	{"index", "ops_activity_id_key", "omixor_activity_id_key"},
	{"index", "ops_activity_at", "omixor_activity_at"},
	{"sequence", "ops_activity_seq_seq", "omixor_activity_seq_seq"},
	{"index", "ops_state_pkey", "omixor_state_pkey"},
	{"index", "ops_files_pkey", "omixor_files_pkey"},
}

// pgRenameLegacySQL 把 pgLegacyRenames 組成一個 DO 區塊(一句 = 一個交易,要嘛全改要嘛都不改):
// 舊名存在且新名不存在才改;舊名不存在(全新資料庫、已經改過)略過;新舊同時存在就報錯,不覆蓋(例如改名後又跑過舊版程式)。冪等
var pgRenameLegacySQL = func() string {
	var b strings.Builder
	b.WriteString("do $rename$ begin\n")
	for _, r := range pgLegacyRenames {
		fmt.Fprintf(&b, `  if to_regclass('%[2]s') is not null then
    if to_regclass('%[3]s') is not null then raise exception 'cannot rename %[2]s to %[3]s: both exist (resolve manually)'; end if;
    alter %[1]s %[2]s rename to %[3]s;
  end if;
`, r.kind, r.old, r.new)
	}
	b.WriteString("end $rename$;")
	return b.String()
}()

// pgExecer 連線池與單一連線都有的 Exec
type pgExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// createTableRe 從建表語句取出表名(登記給核心 schema_check 的名單由建表語句派生,不會與實際建的表脫節)
var createTableRe = regexp.MustCompile(`(?i)create table if not exists (\w+)`)

// pgTableNames 這些建表語句建出的表名
func pgTableNames(schemas ...string) []string {
	var names []string
	for _, sc := range schemas {
		for _, m := range createTableRe.FindAllStringSubmatch(sc, -1) {
			names = append(names, m[1])
		}
	}
	return names
}

// ensurePGSchema 先把舊版 ops_ 開頭的表改名(有舊表才動),再 create if not exists 建缺的表
func ensurePGSchema(ctx context.Context, q pgExecer) error {
	if _, err := q.Exec(ctx, pgRenameLegacySQL); err != nil {
		return fmt.Errorf("舊資料表改名失敗:%w", err)
	}
	schemas := append([]string{pgSchema}, pgExtraSchema...)
	for _, sc := range schemas {
		if _, err := q.Exec(ctx, sc); err != nil {
			return fmt.Errorf("建表失敗:%w", err)
		}
	}
	// 這些表與核心共用資料庫,告訴核心的 schema_check 它們不是遺留表
	store.RegisterExtraTables(pgTableNames(schemas...)...)
	return nil
}

// OpenDB 連上資料庫、建表,並把存放處與密鑰都改到資料庫。key 是 32 bytes 的金鑰(OMIXOR_SECRET_KEY),格式同 config.DecodeKey(hex 或 base64)。
func OpenDB(url, key string) error {
	k, err := config.DecodeKey(key)
	if err != nil {
		return errors.New("OMIXOR_SECRET_KEY 要是 32 bytes 的金鑰(64 個 hex 字元或 base64),可以用 openssl rand -base64 32 產生")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("資料庫網址格式不對:%w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("連不到資料庫:%w", err)
	}
	if err := ensurePGSchema(ctx, pool); err != nil {
		pool.Close()
		return err
	}
	pgKey = k
	pgPoolPtr.Store(pool)
	Store = pgBackend{}
	Vault = pgVault{}
	return nil
}

// CloseDB 測試用(正式程式不呼叫)。順序:停計時器並等它們結束 → 不再接新的背景寫入並等已登記的做完(最多 10 秒)→ 關連線池。
// 不可以在持有 a.Mu 時呼叫(背景寫入與計時器的最後一輪可能要拿 a.Mu)。
func CloseDB() {
	stopPGTimers() // 先停背景計時器(它們會用連線池),再關連線池
	pgBg.Lock()
	pgBg.closing = true
	pgBg.Unlock()
	if !waitPGBackground(10 * time.Second) {
		log.Printf("CloseDB:背景寫入 10 秒內沒結束,仍關閉連線池")
	}
	if p := pgPoolPtr.Swap(nil); p != nil {
		p.Close()
	}
	pgKey = nil
	Store = fileBackend{}
	pgBg.Lock()
	pgBg.closing = false
	pgBg.Unlock()
}

func pgCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

// MARK: 文件

type pgBackend struct{}

func (pgBackend) Read(key string) ([]byte, error) {
	ctx, cancel := pgCtx()
	defer cancel()
	var b []byte
	err := pgQueryRow(ctx, `select data::text from omixor_documents where path = $1`, key).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fs.ErrNotExist
	}
	return b, err
}

func (pgBackend) Write(key string, data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("%s 不是有效的 JSON", key)
	}
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgExec(ctx, `insert into omixor_documents (path, data, updated_at) values ($1, $2::jsonb, now())
		on conflict (path) do update set data = excluded.data, updated_at = now()`, key, string(data))
	if err != nil {
		log.Printf("存 %s 失敗:%v", key, err)
	}
	return err
}

func (pgBackend) Exists(key string) bool {
	ctx, cancel := pgCtx()
	defer cancel()
	var n int
	_ = pgQueryRow(ctx, `select count(*) from omixor_documents where path = $1`, key).Scan(&n)
	return n > 0
}

func (pgBackend) Rename(oldKey, newKey string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgExec(ctx, `update omixor_documents set path = $2, updated_at = now() where path = $1`, oldKey, newKey)
	return err
}

func (pgBackend) RemoveDir(prefix string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgExec(ctx, `delete from omixor_documents where starts_with(path, $1)`, strings.TrimSuffix(prefix, "/")+"/")
	return err
}

func (pgBackend) DirExists(prefix string) bool {
	ctx, cancel := pgCtx()
	defer cancel()
	var n int
	_ = pgQueryRow(ctx, `select count(*) from omixor_documents where starts_with(path, $1)`, strings.TrimSuffix(prefix, "/")+"/").Scan(&n)
	return n > 0
}
