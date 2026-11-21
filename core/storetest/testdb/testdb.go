// Package testdb 是測試用的 PostgreSQL 資料庫供應者(不依賴 store 套件,所以 core/store 自己的測試也能用)。
// 一般測試請用上一層的 storetest.New;這裡是底層:每個測試拿一個只屬於自己的 database。
//
// 資料庫來源:
//   - 有設 OMIXOR_TEST_DATABASE_URL → 用它(帳號要有 CREATEDB 權限);每個測試在同一個伺服器上建自己的 database,測完 drop。
//   - 沒設 → 這個測試程序第一次要 PG 時,起「一個」內建 PostgreSQL 17(embedded-postgres):只聽 127.0.0.1、隨機埠,
//     資料放系統暫存資料夾;PostgreSQL 二進位檔用 library 預設快取(~/.embedded-postgres-go,第一次會下載)。
//     不需要 Docker。程序結束時由 Main 關掉並刪掉資料夾。
//   - go test -short 或 OMIXOR_TEST_NO_EMBEDDED_PG=1(且沒設網址)→ 需要 PG 的測試 t.Skip。
//
// 防呆:OMIXOR_TEST_DATABASE_URL 指到非本機(127.0.0.1、localhost、::1、unix socket 以外)或資料庫名稱不像測試用
// (不是 omixor_ 開頭、也不含 test)時拒絕,要設 OMIXOR_TEST_ALLOW_REMOTE=1 才放行(見 guard.go)。
// OMIXOR_TEST_REQUIRE_PG=1(CI 設):-short 或 OMIXOR_TEST_NO_EMBEDDED_PG=1 造成的略過改成失敗。
//
// 回收與結構:回收只還原資料與序號,不還原結構。release 時比對結構指紋(表、欄位含產生欄位表達式、約束、索引、觸發器,
// 以及結構中繼資料表 structureMetaTables 的資料列),和範本不同的資料庫直接 drop、不回收。
// 所以「需要 DDL」(建表、改約束、改欄位…)的測試請用 testdb.Blank(每次全新、測完 drop),不要用 Template / storetest.New / URL。
//
// 殘留:啟動內建 PG 前會掃系統暫存資料夾的 omixor-testpg-*,清掉超過 1 小時的舊資料夾(行程確實是 postgres 且資料目錄相符才先 pg_ctl stop -m immediate)。
//
// 每個使用測試的套件都要在 TestMain 呼叫 Main(內建 PG 才關得掉;沒呼叫又需要內建 PG 時會直接 Fatal,避免留下孤兒程序):
//
//	func TestMain(m *testing.M) { testdb.Main(m) }
//
// 速度:Template 先在一個範本 database 跑完整遷移(每個程序只一次),之後每個測試用 create database … template …
// 複製(檔案層級複製,不重跑遷移),Blank 則是空的 database(測遷移本身用)。
// 並行:每個測試各有 database,t.Parallel 不會互相干擾。連線上限注意:內建 PG 設 max_connections=500;
// 外部伺服器請留足(每個測試的連線池預設最多 4 條,storetest.New 另外限制)。
// 殘留:程序被殺掉時,範本與測試 database 會留在外部伺服器上(名稱 omixor_tpl_* / omixor_t_*,內含建立時間);
// 下次有人用時會自動清掉超過 6 小時的。
package testdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// URLEnv 外部 PostgreSQL 的連線網址
	URLEnv = "OMIXOR_TEST_DATABASE_URL"
	// NoEmbeddedEnv 設 1:沒有 URLEnv 時不起內建 PG(測試略過)
	NoEmbeddedEnv = "OMIXOR_TEST_NO_EMBEDDED_PG"
	staleAfter    = 6 * time.Hour
)

var errStructureChanged = errors.New("結構和範本不同")

var (
	mainCalled atomic.Bool
	closing    atomic.Bool

	cluster struct {
		once sync.Once
		url  string // 管理用連線網址(能 create database)
		err  error
		skip string // 非空:略過原因

		emb    *embeddedpostgres.EmbeddedPostgres
		dir    string
		embLog bytes.Buffer
	}

	seq atomic.Int64

	// createMu 讓同一程序內的 create database 一次一個(同一個範本同時被複製會互相擋)
	createMu sync.Mutex

	tpls struct {
		sync.Mutex
		m map[string]*tpl
	}
)

type tpl struct {
	once sync.Once
	name string
	fp   string // 範本建好時的結構指紋;回收時比對
	err  error
	mu   sync.Mutex
	free []*slot // 還原好、等著下一個測試的資料庫
	all  []*slot // 這個範本建過的所有資料庫(Shutdown 清)
}

// slot 是一個從範本複製出來的資料庫。測完不 drop,而是清空後回收給下一個測試(省下 create / drop database 與新連線,
// 在 Windows 的內建 PG 上這兩項各要好幾秒)
type slot struct {
	name string
	url  string
	conn *pgx.Conn // 還原用的專屬連線(第一次還原時才開)
}

// Main 放在 TestMain:跑完測試後清掉範本、關掉內建 PG
func Main(m *testing.M) {
	mainCalled.Store(true)
	code := m.Run()
	Shutdown()
	os.Exit(code)
}

// Closing 測試程序正在結束(Shutdown 已開始,資料庫即將或已經被 drop)。
// 測試留下的背景 goroutine(例如每 5 秒重新整理的計時器)在這之後連資料庫失敗(57P01 等)是正常的,不要當成錯誤
func Closing() bool { return closing.Load() }

// Shutdown 清掉這個程序建的範本、關掉內建 PG 並刪資料夾(Main 會呼叫;可重複呼叫)
func Shutdown() {
	closing.Store(true) // 先立旗標,再 drop:背景 goroutine 碰到被 drop 的資料庫時,可以靠 Closing() 判斷是結束中的正常現象
	c := &cluster
	if c.url != "" && c.emb == nil {
		tpls.Lock()
		for _, t := range tpls.m {
			for _, sl := range t.all {
				if sl.conn != nil {
					_ = sl.conn.Close(context.Background())
				}
				dropDB(sl.name)
			}
			if t.name != "" {
				dropDB(t.name)
			}
		}
		tpls.m = nil
		tpls.Unlock()
	}
	if c.emb != nil {
		if err := c.emb.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "testdb:停內建 PostgreSQL 失敗:", err)
		}
		c.emb = nil
	}
	if c.dir != "" {
		_ = os.RemoveAll(c.dir)
		c.dir = ""
	}
}

func get(t testing.TB) string {
	t.Helper()
	c := &cluster
	c.once.Do(start)
	if c.skip != "" {
		t.Skip(c.skip)
	}
	if c.err != nil {
		t.Fatalf("testdb:取得測試用 PostgreSQL 失敗:%v\n%s", c.err, c.embLog.String())
	}
	return c.url
}

func start() {
	c := &cluster
	if u := firstEnv(URLEnv, "OPS_TEST_DATABASE_URL"); u != "" {
		if err := checkTarget(u, os.Getenv(AllowRemoteEnv) == "1"); err != nil {
			c.err = err
			return
		}
		c.url = u
		cleanStale()
		return
	}
	if testing.Short() {
		c.skip = "沒設 " + URLEnv + ",-short 不起內建 PostgreSQL"
	} else if firstEnv(NoEmbeddedEnv, "OPS_TEST_NO_EMBEDDED_PG") == "1" {
		c.skip = "沒設 " + URLEnv + ",且 " + NoEmbeddedEnv + "=1"
	}
	if c.skip != "" {
		if RequirePG() { // CI:略過視同失敗,不讓「沒跑」顯示成通過
			c.err = errors.New(c.skip + ",但 " + RequirePGEnv + "=1 要求一定要跑 PostgreSQL 測試")
			c.skip = ""
		}
		return
	}
	if !mainCalled.Load() {
		c.err = errors.New("沒設 " + URLEnv + " 會起內建 PostgreSQL,需要在 TestMain 呼叫 testdb.Main(m)(或 storetest.Main(m))才關得掉它")
		return
	}
	port, err := freePort()
	if err != nil {
		c.err = err
		return
	}
	sweepOrphans(os.TempDir(), embDirPrefix, orphanAfter, time.Now()) // 以前被殺掉的測試程序留下的內建 PG 與資料夾
	dir, err := os.MkdirTemp("", embDirPrefix)
	if err != nil {
		c.err = err
		return
	}
	c.dir = dir
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V17).
		Port(port).
		Username("postgres").
		Password("test").
		Database("postgres").
		RuntimePath(dir + "/runtime").
		DataPath(dir + "/data").
		StartTimeout(5 * time.Minute). // 第一次要下載、解壓
		Logger(&c.embLog).
		StartParameters(map[string]string{
			"listen_addresses":   "127.0.0.1",
			"max_connections":    "500",
			"fsync":              "off",
			"synchronous_commit": "off",
			"full_page_writes":   "off",
		})
	db := embeddedpostgres.NewDatabase(cfg)
	if err := db.Start(); err != nil {
		c.err = err
		_ = os.RemoveAll(dir)
		c.dir = ""
		return
	}
	c.emb = db
	c.url = fmt.Sprintf("postgres://postgres:test@127.0.0.1:%d/postgres?sslmode=disable", port)
}

// RequirePGEnv 設 1(CI):需要 PostgreSQL 的測試不可以略過(-short、OMIXOR_TEST_NO_EMBEDDED_PG=1 都改成失敗)
const RequirePGEnv = "OMIXOR_TEST_REQUIRE_PG"

// RequirePG 是否設了 OMIXOR_TEST_REQUIRE_PG=1
func RequirePG() bool { return os.Getenv(RequirePGEnv) == "1" }

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func freePort() (uint32, error) {
	for range 20 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		p := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		if p != 5432 && p != 5433 && p != 5440 { // 避開預設與使用者本機常用的 PG
			return uint32(p), nil
		}
	}
	return 0, errors.New("找不到可用的 port")
}

// Admin 回傳管理用的連線網址(連到伺服器上既有的 database);沒有可用的 PG 時 t.Skip。給需要自己建庫的測試用
func Admin(t testing.TB) string { return get(t) }

func withDB(base, db string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.Path = "/" + db
	return u.String()
}

func admin(fn func(ctx context.Context, c *pgx.Conn) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := pgx.Connect(ctx, cluster.url)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	return fn(ctx, c)
}

func dropDB(name string) {
	_ = admin(func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
		return err
	})
}

// createDB 建 database;from 非空就用它當範本(範本不能有連線,剛關連線池時可能還有殘留,重試一下)
func createDB(name, from string) error {
	createMu.Lock()
	defer createMu.Unlock()
	q := "create database " + pgx.Identifier{name}.Sanitize()
	if from != "" {
		q += " template " + pgx.Identifier{from}.Sanitize()
	}
	var err error
	for range 100 {
		err = admin(func(ctx context.Context, c *pgx.Conn) error {
			_, e := c.Exec(ctx, q)
			return e
		})
		var pe *pgconn.PgError
		if err == nil || !errors.As(err, &pe) || pe.Code != "55006" { // 55006 = 還有人連著範本
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

func newName(prefix string) string {
	return fmt.Sprintf("%s_%d_%d_%d", prefix, time.Now().Unix(), os.Getpid(), seq.Add(1))
}

// Blank 回傳一個全新的空 database 的連線網址(測完自動 drop)。測遷移、或需要自己重複 NewPG(模擬重啟)時用
func Blank(t testing.TB) string {
	t.Helper()
	base := get(t)
	name := newName("omixor_t")
	if err := createDB(name, ""); err != nil {
		t.Fatalf("testdb:建 database 失敗:%v", err)
	}
	t.Cleanup(func() { dropDB(name) })
	return withDB(base, name)
}

// Template 回傳一個「已經 build 過」的乾淨 database 的連線網址(測完自動還原、回收)。
// key 區分不同的範本(例如不同 schema 版本);build(url) 在範本 database 上做初始化(跑遷移),每個程序每個 key 只跑一次;
// build 結束時必須已關掉自己開的所有連線。範本裡的表必須是空的(不能有種子資料),因為回收是用 truncate 還原。
// 測試結束時(t.Cleanup)資料庫會被清空並回收給下一個測試;測試自己開的連線都要在那之前關掉(storetest.New 的 Cleanup 順序已保證),
// 還沒關的連線會被強制中斷。
func Template(t testing.TB, key string, build func(url string) error) string {
	t.Helper()
	base := get(t)
	tpls.Lock()
	if tpls.m == nil {
		tpls.m = map[string]*tpl{}
	}
	tp := tpls.m[key]
	if tp == nil {
		tp = &tpl{}
		tpls.m[key] = tp
	}
	tpls.Unlock()
	tp.once.Do(func() {
		name := newName("omixor_tpl_" + sanitize(key))
		if tp.err = createDB(name, ""); tp.err != nil {
			return
		}
		tp.name = name
		u := withDB(base, name)
		if err := build(u); err != nil {
			tp.err = fmt.Errorf("建範本失敗:%w", err)
			return
		}
		if tp.err = checkTemplateEmpty(u); tp.err != nil {
			return
		}
		tp.fp, tp.err = templateFingerprint(u)
	})
	if tp.err != nil {
		t.Fatalf("testdb:%v", tp.err)
	}
	tp.mu.Lock()
	var sl *slot
	if n := len(tp.free); n > 0 {
		sl, tp.free = tp.free[n-1], tp.free[:n-1]
	}
	tp.mu.Unlock()
	if sl == nil {
		name := newName("omixor_t")
		if err := createDB(name, tp.name); err != nil {
			t.Fatalf("testdb:從範本建 database 失敗:%v", err)
		}
		sl = &slot{name: name, url: withDB(base, name)}
		tp.mu.Lock()
		tp.all = append(tp.all, sl)
		tp.mu.Unlock()
	}
	t.Cleanup(func() { tp.release(sl) })
	return sl.url
}

func templateFingerprint(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		return "", err
	}
	defer c.Close(ctx)
	return fingerprint(ctx, c)
}

// checkTemplateEmpty 範本有資料就報錯(結構中繼資料表 structureMetaTables 除外)(回收是清空表,不會還原種子資料)
func checkTemplateEmpty(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	var tables []string
	rows, err := c.Query(ctx, `select format('%I.%I', schemaname, tablename) from pg_tables where schemaname not in ('pg_catalog', 'information_schema')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		tables = append(tables, n)
	}
	rows.Close()
	for _, tb := range tables {
		if isMetaTable(tb) {
			// 登記了固定列條件的表:固定列以外的列不能出現在範本(回收會把它們刪掉,範本卻帶著就不一致)
			if w := metaKeepWhere(strings.TrimPrefix(tb, "public.")); w != "" {
				var extra bool
				if err := c.QueryRow(ctx, "select exists (select 1 from "+tb+" where not ("+metaTableKeep[strings.TrimPrefix(tb, "public.")]+"))").Scan(&extra); err != nil {
					return err
				}
				if extra {
					return fmt.Errorf("範本的表 %s 有固定列(%s)以外的資料;範本要是乾淨的才能回收", tb, metaTableKeep[strings.TrimPrefix(tb, "public.")])
				}
			}
			continue
		}
		var has bool
		if err := c.QueryRow(ctx, "select exists (select 1 from "+tb+")").Scan(&has); err != nil {
			return err
		}
		if has {
			return fmt.Errorf("範本的表 %s 有資料;範本要是空的才能回收(用 truncate 還原)", tb)
		}
	}
	return nil
}

// resetSQL 算出把這個資料庫還原成範本狀態要跑的 SQL:只清「寫過」的表(pg_relation_size > 0)、只歸位「用過」的序號。
// truncate / alter sequence 每動一個表都會新建檔案,在 Windows 上很慢,所以不要每次都清全部 39 張表。
func resetSQL(ctx context.Context, c *pgx.Conn) (string, error) {
	var tables, seqs *string
	if err := c.QueryRow(ctx, `select string_agg(format('%I.%I', schemaname, tablename), ', ') from pg_tables
		where schemaname not in ('pg_catalog', 'information_schema') and pg_relation_size(format('%I.%I', schemaname, tablename)::regclass) > 0
		and not (schemaname = 'public' and tablename = any($1))`, structureMetaTables).Scan(&tables); err != nil {
		return "", err
	}
	if err := c.QueryRow(ctx, `select string_agg(format('alter sequence %I.%I restart;', schemaname, sequencename), ' ') from pg_sequences where last_value is not null`).Scan(&seqs); err != nil {
		return "", err
	}
	var b strings.Builder
	if tables != nil {
		b.WriteString("truncate table " + *tables + " restart identity cascade;")
	}
	if seqs != nil {
		b.WriteString(" " + *seqs)
	}
	// 中繼資料表裡登記了固定列條件的:刪掉固定列以外的(測試建的使用者、角色…);固定列留著。
	// 放在 truncate 之後:參照它的表已經清空,不會被外鍵擋住。每次都跑(表小,沒有符合的列就是空操作)
	for _, t := range structureMetaTables {
		if k := metaTableKeep[t]; k != "" {
			// 這個範本不一定有這張表(例如測試用的範本),所以先確認存在
			q := "public." + pgx.Identifier{t}.Sanitize()
			b.WriteString(" do $$ begin if to_regclass('" + q + "') is not null then delete from " + q + " where not (" + k + "); end if; end $$;")
		}
	}
	return b.String(), nil
}

// release 還原一個用過的資料庫並放回空閒清單;還原失敗(有人還握著鎖、連線壞了…)就 drop 掉、不回收
func (tp *tpl) release(sl *slot) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := func() error {
		if sl.conn == nil || sl.conn.IsClosed() {
			c, err := pgx.Connect(ctx, sl.url)
			if err != nil {
				return err
			}
			sl.conn = c
		}
		// 還沒關的連線(測試漏關)強制中斷,免得擋住 truncate
		if _, err := sl.conn.Exec(ctx, `select pg_terminate_backend(pid) from pg_stat_activity where datname = current_database() and pid <> pg_backend_pid()`); err != nil {
			return err
		}
		if _, err := sl.conn.Exec(ctx, "set lock_timeout = '10s'"); err != nil {
			return err
		}
		q, err := resetSQL(ctx, sl.conn)
		if err != nil {
			return err
		}
		if q != "" {
			if _, err := sl.conn.Exec(ctx, q); err != nil {
				return err
			}
		}
		// 結構(表、欄位、約束、索引、觸發器、中繼資料列)被測試改過就不回收:丟掉這個資料庫,後面的測試才不會拿到殘缺的結構
		fp, err := fingerprint(ctx, sl.conn)
		if err != nil {
			return err
		}
		if fp != tp.fp {
			fmt.Fprintf(os.Stderr, "testdb:%s 的結構和範本不同(測試改了結構?),丟棄不回收;要做 DDL 的測試請用 testdb.Blank\n", sl.name)
			return errStructureChanged
		}
		return nil
	}()
	if err != nil {
		if sl.conn != nil {
			_ = sl.conn.Close(ctx)
			sl.conn = nil
		}
		tp.mu.Lock()
		for i, x := range tp.all {
			if x == sl {
				tp.all = append(tp.all[:i], tp.all[i+1:]...)
				break
			}
		}
		tp.mu.Unlock()
		dropDB(sl.name)
		return
	}
	tp.mu.Lock()
	tp.free = append(tp.free, sl)
	tp.mu.Unlock()
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cleanStale 清掉外部伺服器上以前被殺掉的測試程序留下的 database(名稱倒數第三段是建立時間,超過 staleAfter)
func cleanStale() {
	_ = admin(func(ctx context.Context, c *pgx.Conn) error {
		rows, err := c.Query(ctx, `select datname from pg_database where datname like 'omixor\_t\_%' or datname like 'omixor\_tpl\_%'`)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				return err
			}
			names = append(names, n)
		}
		rows.Close()
		for _, n := range names {
			parts := strings.Split(n, "_")
			if len(parts) < 4 {
				continue
			}
			ts, err := strconv.ParseInt(parts[len(parts)-3], 10, 64)
			if err != nil || time.Since(time.Unix(ts, 0)) < staleAfter {
				continue
			}
			_, _ = c.Exec(ctx, "drop database if exists "+pgx.Identifier{n}.Sanitize()+" with (force)")
		}
		return nil
	})
}
