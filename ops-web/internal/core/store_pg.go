package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgreSQL 存放處(設了 OPS_DATABASE_URL 才用)。
//
//	ops_documents  原本的 JSON 檔(orgs.json、orgs/<組織>/issues.json…),path 是相對資料夾的路徑
//	ops_secrets    連線 token、client secret、LDAP 服務帳號密碼;用 OPS_SECRET_KEY 以 AES-256-GCM 加密
//	ops_sessions   網頁登入的 session(只存 token 的 SHA-256)
//	ops_activity   動態牆(全部歷史)
//	ops_state      範例資料的變更(告警、事件、工作項目…;Swift 版是重開就重置)
//	ops_issues     Issue(與 ops_issue_comments;見 store_pg_issues.go,不放在 ops_documents)
//
// 這個版本只支援一台 ops-web 連同一個資料庫(畫面即時通知與記憶體裡的狀態不跨主機)。

var (
	pgPool *pgxpool.Pool
	// 密鑰加密用的金鑰(32 bytes)
	pgKey []byte
)

// DB 目前的資料庫連線;沒用資料庫時是 nil
func DB() *pgxpool.Pool { return pgPool }

const pgSchema = `
create table if not exists ops_documents (
	path text primary key,
	data jsonb not null,
	updated_at timestamptz not null default now()
);
create table if not exists ops_secrets (
	account text primary key,
	nonce bytea not null,
	ciphertext bytea not null,
	updated_at timestamptz not null default now()
);
create table if not exists ops_sessions (
	token_hash text primary key,
	user_json jsonb not null,
	expires_at timestamptz not null,
	created_at timestamptz not null default now()
);
create index if not exists ops_sessions_expires on ops_sessions (expires_at);
create table if not exists ops_activity (
	seq bigserial primary key,
	id text not null unique,
	at timestamptz not null,
	item jsonb not null
);
create index if not exists ops_activity_at on ops_activity (at desc);
create table if not exists ops_state (
	name text primary key,
	data jsonb not null,
	updated_at timestamptz not null default now()
);
create table if not exists ops_files (
	path text primary key,
	data bytea not null,
	created_at timestamptz not null default now()
);
`

// pgExtraSchema 其他檔案登記的表(例如 store_pg_issues.go 的 ops_issues)
var pgExtraSchema []string

// OpenDB 連上資料庫、建表,並把存放處與密鑰都改到資料庫。key 是 base64 的 32 bytes(OPS_SECRET_KEY)。
func OpenDB(url, key string) error {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil || len(k) != 32 {
		return errors.New("OPS_SECRET_KEY 要是 base64 的 32 bytes,可以用 openssl rand -base64 32 產生")
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
	for _, sc := range append([]string{pgSchema}, pgExtraSchema...) {
		if _, err := pool.Exec(ctx, sc); err != nil {
			pool.Close()
			return fmt.Errorf("建表失敗:%w", err)
		}
	}
	pgPool, pgKey = pool, k
	Store = pgBackend{}
	Vault = pgVault{}
	return nil
}

// CloseDB 測試用
func CloseDB() {
	if pgPool != nil {
		pgPool.Close()
	}
	pgPool, pgKey = nil, nil
	Store = fileBackend{}
}

func pgCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

// MARK: 文件

type pgBackend struct{}

func (pgBackend) Name() string { return "postgres" }

func (pgBackend) Read(key string) ([]byte, error) {
	ctx, cancel := pgCtx()
	defer cancel()
	var b []byte
	err := pgPool.QueryRow(ctx, `select data::text from ops_documents where path = $1`, key).Scan(&b)
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
	_, err := pgPool.Exec(ctx, `insert into ops_documents (path, data, updated_at) values ($1, $2::jsonb, now())
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
	_ = pgPool.QueryRow(ctx, `select count(*) from ops_documents where path = $1`, key).Scan(&n)
	return n > 0
}

func (pgBackend) Rename(oldKey, newKey string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgPool.Exec(ctx, `update ops_documents set path = $2, updated_at = now() where path = $1`, oldKey, newKey)
	return err
}

func (pgBackend) MoveDir(oldPrefix, newPrefix string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	old := strings.TrimSuffix(oldPrefix, "/") + "/"
	nw := strings.TrimSuffix(newPrefix, "/") + "/"
	_, err := pgPool.Exec(ctx, `update ops_documents set path = $2 || substr(path, length($1) + 1), updated_at = now()
		where starts_with(path, $1)`, old, nw)
	return err
}

func (pgBackend) DirExists(prefix string) bool {
	ctx, cancel := pgCtx()
	defer cancel()
	var n int
	_ = pgPool.QueryRow(ctx, `select count(*) from ops_documents where starts_with(path, $1)`, strings.TrimSuffix(prefix, "/")+"/").Scan(&n)
	return n > 0
}

// ImportDir 把資料夾裡的 JSON 檔匯入資料庫(已存在的會覆蓋);回傳匯入的路徑
func ImportDir(dir string) ([]string, error) {
	if pgPool == nil {
		return nil, errors.New("沒有連資料庫")
	}
	var done []string
	err := walkJSON(dir, func(rel string, data []byte) error {
		if !json.Valid(data) {
			log.Printf("略過 %s:不是有效的 JSON", rel)
			return nil
		}
		// 組織的 Issue 匯入 ops_issues 表(已存在的同樣覆蓋)
		if parts := strings.Split(rel, "/"); len(parts) == 3 && parts[0] == "orgs" && parts[2] == "issues.json" {
			var list []OpsIssue
			if err := json.Unmarshal(data, &list); err != nil {
				log.Printf("略過 %s:不是 Issue 清單", rel)
				return nil
			}
			if _, err := PGImportIssues(parts[1], list, true); err != nil {
				return fmt.Errorf("%s:%w", rel, err)
			}
			done = append(done, rel)
			return nil
		}
		if err := (pgBackend{}).Write(rel, data); err != nil {
			return err
		}
		done = append(done, rel)
		return nil
	})
	return done, err
}

func walkJSON(dir string, f func(rel string, data []byte) error) error {
	root := os.DirFS(dir)
	return fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == "deleted-orgs" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".json") || strings.Contains(p, ".unreadable-") {
			return nil
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		return f(p, b)
	})
}

// MARK: 密鑰(AES-256-GCM)

type pgVault struct{}

func (pgVault) Get(account string) ([]byte, bool) {
	ctx, cancel := pgCtx()
	defer cancel()
	var nonce, ct []byte
	if err := pgPool.QueryRow(ctx, `select nonce, ciphertext from ops_secrets where account = $1`, account).Scan(&nonce, &ct); err != nil {
		return nil, false
	}
	pt, err := secretOpen(account, nonce, ct)
	if err != nil {
		log.Printf("密鑰 %s 解不開(OPS_SECRET_KEY 換過了嗎?):%v", account, err)
		return nil, false
	}
	return pt, true
}

func (pgVault) Set(account string, data []byte) error {
	nonce, ct, err := secretSeal(account, data)
	if err != nil {
		return err
	}
	ctx, cancel := pgCtx()
	defer cancel()
	_, err = pgPool.Exec(ctx, `insert into ops_secrets (account, nonce, ciphertext, updated_at) values ($1, $2, $3, now())
		on conflict (account) do update set nonce = excluded.nonce, ciphertext = excluded.ciphertext, updated_at = now()`, account, nonce, ct)
	return err
}

func (pgVault) Delete(account string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgPool.Exec(ctx, `delete from ops_secrets where account = $1`, account)
	return err
}

// 帳號名稱當作附加資料(AAD):密文搬到別的帳號底下會解不開
func secretSeal(account string, pt []byte) (nonce, ct []byte, err error) {
	gcm, err := secretGCM()
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, pt, []byte(account)), nil
}

func secretOpen(account string, nonce, ct []byte) ([]byte, error) {
	gcm, err := secretGCM()
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ct, []byte(account))
}

func secretGCM() (cipher.AEAD, error) {
	if len(pgKey) != 32 {
		return nil, errors.New("沒有密鑰加密金鑰")
	}
	b, err := aes.NewCipher(pgKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// MARK: session

func tokenHash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// SessionPut / SessionGet / SessionDelete / SessionClear:auth 用(沒有資料庫時不會呼叫)
func SessionPut(tok string, user any, expires time.Time) error {
	ctx, cancel := pgCtx()
	defer cancel()
	_, err := pgPool.Exec(ctx, `insert into ops_sessions (token_hash, user_json, expires_at) values ($1, $2, $3)
		on conflict (token_hash) do update set user_json = excluded.user_json, expires_at = excluded.expires_at`,
		tokenHash(tok), string(Marshal(user)), expires)
	return err
}

func SessionGet(tok string, user any) (time.Time, bool) {
	ctx, cancel := pgCtx()
	defer cancel()
	var b []byte
	var exp time.Time
	if err := pgPool.QueryRow(ctx, `select user_json::text, expires_at from ops_sessions where token_hash = $1`, tokenHash(tok)).Scan(&b, &exp); err != nil {
		return time.Time{}, false
	}
	if json.Unmarshal(b, user) != nil {
		return time.Time{}, false
	}
	return exp, true
}

func SessionDelete(tok string) {
	ctx, cancel := pgCtx()
	defer cancel()
	_, _ = pgPool.Exec(ctx, `delete from ops_sessions where token_hash = $1`, tokenHash(tok))
}

// SessionClear 刪掉全部 session(認證設定改了,所有人要重新登入);順便清過期的
func SessionClear() {
	ctx, cancel := pgCtx()
	defer cancel()
	_, _ = pgPool.Exec(ctx, `delete from ops_sessions`)
}

func sessionSweep() {
	ctx, cancel := pgCtx()
	defer cancel()
	_, _ = pgPool.Exec(ctx, `delete from ops_sessions where expires_at < now()`)
}

// MARK: 動態牆與範例資料的變更

const activityKeep = 1000

// mockCollections 範例資料裡要存進 ops_state 的集合(動態牆另外存)
var mockCollections = []string{"people", "services", "servers", "repos", "pull_requests", "work_items", "pipeline_runs",
	"deployments", "alerts", "alert_rules", "logs", "dashboards", "incidents", "on_call", "automations",
	"automation_runs", "approvals", "integrations"}

type pgState struct {
	mu    sync.Mutex
	dirty map[string]bool
}

var pgSt = &pgState{dirty: map[string]bool{}}

// loadPersisted 啟動時:有存過的範例資料與動態牆就用存的,沒有就把種子寫進去(呼叫時不用拿鎖,App 還沒開始用)
func (a *App) loadPersisted() {
	ctx, cancel := pgCtx()
	defer cancel()
	rows, err := pgPool.Query(ctx, `select name, data::text from ops_state`)
	if err != nil {
		log.Printf("讀範例資料失敗:%v", err)
		return
	}
	found := map[string]bool{}
	for rows.Next() {
		var name string
		var b []byte
		if rows.Scan(&name, &b) != nil {
			continue
		}
		var v any
		if json.Unmarshal(b, &v) != nil {
			continue
		}
		found[name] = true
		if name == "sprint" {
			a.Sprint, _ = v.(map[string]any)
			continue
		}
		list, _ := v.([]any)
		items := make([]map[string]any, 0, len(list))
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				items = append(items, m)
			}
		}
		a.C[name] = items
	}
	rows.Close()
	for _, c := range append(mockCollections, "sprint") {
		if !found[c] {
			pgSt.dirty[c] = true
		}
	}

	// 動態牆:最新的 activityKeep 筆
	arows, err := pgPool.Query(ctx, `select item::text from ops_activity where coalesce(item->>'org', '') = '' order by at desc, seq desc limit $1`, activityKeep)
	if err == nil {
		var items []map[string]any
		for arows.Next() {
			var b []byte
			var m map[string]any
			if arows.Scan(&b) == nil && json.Unmarshal(b, &m) == nil {
				items = append(items, m)
			}
		}
		arows.Close()
		if len(items) > 0 {
			a.C["activity"] = items
		} else {
			// 第一次:把種子的動態寫進去(舊的在前)
			seed := a.C["activity"]
			for i := len(seed) - 1; i >= 0; i-- {
				activityInsert(seed[i])
			}
		}
	}
	a.saveDirty()
}

func activityInsert(item map[string]any) {
	id, _ := item["id"].(string)
	at, err := ParseTime(fmt.Sprint(item["date"]))
	if err != nil {
		at = time.Now()
	}
	ctx, cancel := pgCtx()
	defer cancel()
	if _, err := pgPool.Exec(ctx, `insert into ops_activity (id, at, item) values ($1, $2, $3::jsonb) on conflict (id) do nothing`,
		id, at, string(Marshal(item))); err != nil {
		log.Printf("存動態失敗:%v", err)
	}
}

// markDirty Changed 的主題是範例資料集合時,標記要存
func markDirty(topics []string) {
	if pgPool == nil {
		return
	}
	pgSt.mu.Lock()
	for _, t := range topics {
		if t == "*" {
			for _, c := range mockCollections {
				pgSt.dirty[c] = true
			}
			pgSt.dirty["sprint"] = true
			continue
		}
		for _, c := range append(mockCollections, "sprint") {
			if c == t {
				pgSt.dirty[c] = true
			}
		}
	}
	pgSt.mu.Unlock()
}

// saveDirty 把有變的範例資料集合存起來(拿鎖複製,放鎖再寫)。
// 先拿 a.Mu 再拿 pgSt.mu(和切換組織時的 flushMockAsync 同順序),名稱依目前組織加前綴。
func (a *App) saveDirty() {
	a.Mu.Lock()
	pgSt.mu.Lock()
	names := make([]string, 0, len(pgSt.dirty))
	for n := range pgSt.dirty {
		names = append(names, n)
	}
	pgSt.dirty = map[string]bool{}
	pgSt.mu.Unlock()
	org := a.mockOrg()
	data := map[string][]byte{}
	for _, n := range names {
		if n == "sprint" {
			data[mockStateKey(org, n)] = Marshal(a.Sprint)
		} else {
			data[mockStateKey(org, n)] = Marshal(a.C[n])
		}
	}
	a.Mu.Unlock()
	pgWriteState(data)
}

// pgWriteState 寫入 ops_state(key 已含組織前綴)
func pgWriteState(data map[string][]byte) {
	if len(data) == 0 {
		return
	}
	ctx, cancel := pgCtx()
	defer cancel()
	for n, b := range data {
		if _, err := pgPool.Exec(ctx, `insert into ops_state (name, data, updated_at) values ($1, $2::jsonb, now())
			on conflict (name) do update set data = excluded.data, updated_at = now()`, n, string(b)); err != nil {
			log.Printf("存範例資料 %s 失敗:%v", n, err)
		}
	}
}

// ActivityHistory 動態牆歷史(before 之前、最多 limit 筆);沒有資料庫時從記憶體拿
func ActivityHistory(a *App, before time.Time, limit int) []map[string]any {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if pgPool == nil {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		var out []map[string]any
		for _, it := range a.C["activity"] {
			if t, err := ParseTime(fmt.Sprint(it["date"])); err == nil && t.Before(before) {
				out = append(out, it)
				if len(out) >= limit {
					break
				}
			}
		}
		return out
	}
	a.Mu.Lock()
	org := activityOrg(a.mockOrg())
	a.Mu.Unlock()
	ctx, cancel := pgCtx()
	defer cancel()
	rows, err := pgPool.Query(ctx, `select item::text from ops_activity where at < $1 and coalesce(item->>'org', '') = $3 order by at desc, seq desc limit $2`, before, limit, org)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var b []byte
		var m map[string]any
		if rows.Scan(&b) == nil && json.Unmarshal(b, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func init() {
	// 每 10 秒存一次有變的範例資料、清過期 session
	OnStart(func(a *App) {
		if pgPool == nil || os.Getenv("OPS_NO_TIMERS") != "" {
			return
		}
		go func() {
			t := time.NewTicker(10 * time.Second)
			n := 0
			for range t.C {
				a.saveDirty()
				if n++; n%60 == 0 {
					sessionSweep()
				}
			}
		}()
	})
	RegisterAction("activity_history", func(a *App, args map[string]any) (any, error) {
		before := time.Now().Add(time.Minute)
		if s, ok := args["before"].(string); ok && s != "" {
			if t, err := ParseTime(s); err == nil {
				before = t
			}
		}
		limit := 100
		if n, ok := args["limit"].(float64); ok {
			limit = int(n)
		}
		return ActivityHistory(a, before, limit), nil
	})
}

// SaveNow 立刻存(結束程式前、測試用)
func (a *App) SaveNow() {
	if pgPool != nil {
		a.saveDirty()
	}
}
