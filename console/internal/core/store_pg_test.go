package core

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/store"
	"omixor-core/storetest"
)

// 需要 PostgreSQL:走 storetest(設 OMIXOR_TEST_DATABASE_URL 用該伺服器,沒設用內建 PG);每個測試各有獨立的資料庫,結束後還原回收。

var testKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// consoleSchema 在範本資料庫補上 console 自己的表(omixor_documents、omixor_secrets…,見 ensurePGSchema),回收還原才不會因結構不同而被丟掉
func consoleSchema(url string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	return ensurePGSchema(ctx, c)
}

func withDB(t *testing.T) string {
	t.Helper()
	url := storetest.URLWith(t, "console", consoleSchema)
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	// pool_max_conns 只有 pgxpool 認得,直接 pgx.Connect 的測試用不帶它的 url
	if err := OpenDB(url+sep+"pool_max_conns=4", testKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // 先於 storetest 的還原回收執行(Cleanup 後進先出)
		CloseDB()
		Vault = NewMemoryVault()
	})
	return url
}

func TestPGDocuments(t *testing.T) {
	withDB(t)
	p := filepath.Join(DataRoot(), "orgs", "x", "issues.json")
	if Exists(p) {
		t.Fatal("一開始不該存在")
	}
	if err := WriteJSON(p, []map[string]any{{"id": "ISS-1", "title": "中文 / slash"}}); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := ReadJSON(p, &got); err != nil || got[0]["title"] != "中文 / slash" {
		t.Fatalf("%v %v", got, err)
	}
	// 不存在要回 fs.ErrNotExist(各功能靠這個判斷用預設值)
	if _, err := ReadData(filepath.Join(DataRoot(), "nope.json")); !IsNotExist(err) {
		t.Fatal(err)
	}
	// 改名
	if err := RenameData(p, p+".unreadable-1"); err != nil || Exists(p) || !Exists(p+".unreadable-1") {
		t.Fatal("改名失敗")
	}
	// 整個資料夾刪除:前綴要以 / 分界,orgs/xy 不能被當成 orgs/x 刪掉
	_ = WriteJSON(filepath.Join(DataRoot(), "orgs", "x", "agents.json"), map[string]any{})
	_ = WriteJSON(filepath.Join(DataRoot(), "orgs", "xy", "agents.json"), map[string]any{})
	if err := RemoveDataDir(filepath.Join(DataRoot(), "orgs", "x")); err != nil {
		t.Fatal(err)
	}
	if DataDirExists(filepath.Join(DataRoot(), "orgs", "x")) {
		t.Fatal("刪資料夾失敗")
	}
	if !DataDirExists(filepath.Join(DataRoot(), "orgs", "xy")) {
		t.Fatal("orgs/xy 被誤刪")
	}
	// 不是 JSON 的不收
	if err := WriteData(filepath.Join(DataRoot(), "bad.json"), []byte("{")); err == nil {
		t.Fatal("壞掉的 JSON 不該存進去")
	}
}

func TestPGSecretsEncrypted(t *testing.T) {
	url := withDB(t)
	if err := Vault.Set("github:token", []byte("ghs_secret")); err != nil {
		t.Fatal(err)
	}
	if b, ok := Vault.Get("github:token"); !ok || string(b) != "ghs_secret" {
		t.Fatal("讀不回來")
	}
	// 資料庫裡不能看到明文
	ctx := context.Background()
	c, _ := pgx.Connect(ctx, url)
	defer c.Close(ctx)
	var ct []byte
	_ = c.QueryRow(ctx, "select ciphertext from omixor_secrets where account = 'github:token'").Scan(&ct)
	if strings.Contains(string(ct), "ghs_secret") || len(ct) == 0 {
		t.Fatal("密文裡有明文")
	}
	// 密文搬到別的帳號底下解不開(帳號是 AAD)
	_, _ = c.Exec(ctx, "insert into omixor_secrets (account, nonce, ciphertext) select 'evil:token', nonce, ciphertext from omixor_secrets where account = 'github:token'")
	if _, ok := Vault.Get("evil:token"); ok {
		t.Fatal("搬過去的密文不該解得開")
	}
	// 換了金鑰解不開
	pgKey = []byte("ffffffffffffffffffffffffffffffff")
	if _, ok := Vault.Get("github:token"); ok {
		t.Fatal("錯的金鑰不該解得開")
	}
	pgKey, _ = base64.StdEncoding.DecodeString(testKey)
	_ = Vault.Delete("github:token")
	if _, ok := Vault.Get("github:token"); ok {
		t.Fatal("刪不掉")
	}
	if OpenDB(url, "short") == nil {
		t.Fatal("金鑰長度不對應該失敗")
	}
}

func TestPGSessions(t *testing.T) {
	withDB(t)
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := SessionPut("tok-1", map[string]any{"id": "alice"}, exp); err != nil {
		t.Fatal(err)
	}
	var u map[string]any
	got, ok := SessionGet("tok-1", &u)
	if !ok || u["id"] != "alice" || !got.Equal(exp) {
		t.Fatalf("%v %v %v", ok, u, got)
	}
	SessionDelete("tok-1")
	if _, ok := SessionGet("tok-1", &u); ok {
		t.Fatal("刪掉後還在")
	}
	_ = SessionPut("tok-2", map[string]any{"id": "bob"}, exp)
	SessionClear()
	if _, ok := SessionGet("tok-2", &u); ok {
		t.Fatal("清除後還在")
	}
}

func TestPGMockStateAndActivityPersist(t *testing.T) {
	withDB(t)
	a := New()
	a.Start()
	a.Mu.Lock()
	alert := Str(a.C["alerts"][0], "id")
	a.AcknowledgeAlert(alert, Person("alice"))
	n := len(a.C["activity"])
	a.Mu.Unlock()
	a.Changed("alerts")
	a.SaveNow()
	time.Sleep(300 * time.Millisecond) // 動態是背景寫入

	// 重開:告警狀態與動態都還在
	b := New()
	b.Mu.Lock()
	_, it := b.Find("alerts", alert)
	state := Str(it, "state")
	first := b.C["activity"][0]
	m := len(b.C["activity"])
	b.Mu.Unlock()
	if state != "acknowledged" {
		t.Fatalf("告警狀態沒存:%s", state)
	}
	if m != n || !strings.Contains(Str(first, "text"), "確認告警") {
		t.Fatalf("動態沒存:%d/%d %v", m, n, first)
	}
	// 歷史查詢
	h := ActivityHistory(b, time.Now().Add(time.Minute), 5)
	if len(h) != 5 || Str(h[0], "id") != Str(first, "id") {
		t.Fatalf("歷史:%d", len(h))
	}
}

func TestPGOrgsAndImport(t *testing.T) {
	withDB(t)
	a := New()
	a.Start()
	a.Mu.Lock()
	org := a.CreateOrg("Lab", "", "", "blank")
	a.SwitchOrg(org.ID)
	dir := a.DataDir()
	a.Mu.Unlock()
	// 新組織的初始資料在核心:組織本身,與(空白範本)存好的文件櫃結構;不再有 agents.json 之類的資料夾文件
	if _, err := localCore().Svc.Store.GetOrg(org.ID); err != nil {
		t.Fatalf("新組織沒建在核心:%v", err)
	}
	be, berr := agentBackend()
	if berr != nil {
		t.Fatal(berr)
	}
	if cab, err := be.GetCabinet(org.ID); err != nil || !cab.LayoutSaved {
		t.Fatalf("新組織的文件櫃結構沒存進核心:%+v %v", cab, err)
	}
	if Exists(filepath.Join(dir, "agents.json")) {
		t.Fatal("角色已經在核心的表,不該再寫成資料夾文件")
	}
	// 重開還記得目前的組織
	b := New()
	b.Start()
	b.Mu.Lock()
	cur := b.CurrentOrgID()
	b.SwitchOrg(DefaultOrgID)
	err := b.DeleteOrg(org.ID)
	b.Mu.Unlock()
	if cur != org.ID || err != nil {
		t.Fatalf("%s %v", cur, err)
	}
	if DataDirExists(filepath.Join(DataRoot(), "orgs", org.ID)) {
		t.Fatal("刪除後資料夾還在")
	}

	// 匯入 Swift 版格式的資料夾
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "orgs", "default"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "orgs.json"), []byte(`{"current":"default","orgs":[{"id":"default","name":"evoleap","symbol":"graduationcap","color":"teal","note":"","createdAt":"2026-11-10T00:00:00Z"}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(src, "orgs", "default", "issues.json"), []byte(`[{"id":"ISS-9","title":"匯入的","body":"","status":"open","priority":"high","labels":[],"source":"","link":"","createdBy":"mcp","createdAt":"2026-11-10T00:00:00Z","updatedAt":"2026-11-10T00:00:00Z","comments":[]}]`), 0o644)
	_ = os.WriteFile(filepath.Join(src, "orgs", "default", "cabinet.unreadable-1.json"), []byte(`x`), 0o644)
	done, err := ImportDir(src)
	if err != nil || len(done) != 2 {
		t.Fatalf("%v %v", done, err)
	}
	c := New()
	c.Start()
	c.Mu.Lock()
	defer c.Mu.Unlock()
	// Issue 匯入到 issues 表(不放 omixor_documents)
	if issues := c.Issues(); len(issues) != 1 || issues[0].Key != "ISS-9" || issues[0].Priority != "high" {
		t.Fatalf("匯入的 Issue 讀不到:%v", issues)
	}
	if Exists(filepath.Join(c.DataDir(), "issues.json")) {
		t.Fatal("Issue 不該再存成文件")
	}
}

// 關閉資料庫(CloseDB)會停掉背景計時器並等它結束;計時器不會再碰已關閉的連線池,pgWriteState 在連線池是 nil 時回錯誤而不是當機
func TestPGTimerStopsOnCloseDB(t *testing.T) {
	withDB(t)
	a := New()
	// New 會把種子寫進去;先刪掉 sprint 那一列,才分得出「是計時器寫的」
	if _, err := pgExec(context.Background(), `delete from omixor_state where name = 'sprint'`); err != nil {
		t.Fatal(err)
	}
	startPGTimer(a, 5*time.Millisecond) // 很短的間隔,讓計時器在關閉前後都有機會跑
	pgSt.mu.Lock()
	pgSt.dirty["sprint"] = true
	pgSt.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		_ = pgQueryRow(context.Background(), `select count(*) from omixor_state where name = 'sprint'`).Scan(&n)
		if n == 1 {
			break // 關閉前計時器確實寫過一次資料
		}
		if time.Now().After(deadline) {
			t.Fatal("計時器沒有把 dirty 的 sprint 寫進資料庫")
		}
		time.Sleep(5 * time.Millisecond)
	}
	CloseDB() // 會等計時器結束
	pgTimersMu.Lock()
	left := len(pgTimers)
	pgTimersMu.Unlock()
	if left != 0 {
		t.Fatalf("計時器沒清掉:%d", left)
	}
	pgSt.mu.Lock()
	pgSt.dirty["sprint"] = true
	pgSt.mu.Unlock()
	time.Sleep(30 * time.Millisecond) // 關閉後若計時器還在,saveDirty 會碰 nil 的連線池
	if err := pgWriteState(map[string][]byte{"x": []byte(`{}`)}); !errors.Is(err, errPGClosed) {
		t.Fatalf("關閉後寫入要回 errPGClosed,實際 %v", err)
	}
	sessionSweep() // 關閉後不能當機
	pgSt.mu.Lock()
	pgSt.dirty = map[string]bool{"sprint": true}
	pgSt.mu.Unlock()
	a.saveDirty() // 同上;寫入失敗要把名稱標回 dirty,下一輪才會重試
	pgSt.mu.Lock()
	again := pgSt.dirty["sprint"]
	pgSt.dirty = map[string]bool{}
	pgSt.mu.Unlock()
	if !again {
		t.Fatal("寫入失敗後 sprint 應該重新標成 dirty")
	}
}

// CloseDB 前啟動的背景寫入(動態牆、範例資料)不會在關閉後碰到 nil 的連線池;CloseDB 進行中不再接新的背景寫入
func TestPGBackgroundWritesSafeAcrossCloseDB(t *testing.T) {
	withDB(t)
	a := New()
	a.Start()
	for i := 0; i < 20; i++ {
		a.Mu.Lock()
		a.Log(Person("tim"), "building.2", "背景寫入 "+time.Now().String(), "settings") // 內部 pgGo(activityInsert)
		a.Mu.Unlock()
	}
	pgGo(func() { time.Sleep(50 * time.Millisecond) }) // 一個慢的背景工作:CloseDB 要等它
	t0 := time.Now()
	CloseDB()
	if time.Since(t0) < 40*time.Millisecond {
		t.Fatal("CloseDB 沒有等背景寫入")
	}
	// 關閉後(連線池是 nil)才跑的背景寫入:回 errPGClosed、不當機
	pgGo(func() { activityInsert(map[string]any{"id": "y", "date": ISO(time.Now())}) })
	time.Sleep(20 * time.Millisecond)
	activityInsert(map[string]any{"id": "x", "date": ISO(time.Now())}) // pgPool 是 nil:回錯誤、不當機
	if _, err := pgExec(context.Background(), "select 1"); !errors.Is(err, errPGClosed) {
		t.Fatalf("%v", err)
	}
	if err := pgQueryRow(context.Background(), "select 1").Scan(new(int)); !errors.Is(err, errPGClosed) {
		t.Fatalf("%v", err)
	}
}

// 舊版(ops_ 前綴)的資料庫:啟動時表、索引、序號、主鍵改成 omixor_ 前綴,資料保留;再啟動一次是 no-op;新舊同時存在報錯、不覆蓋
func TestOpenDBRenamesLegacyOpsTables(t *testing.T) {
	url := storetest.URLWith(t, "console", consoleSchema) // 範本已經是新名字;先倒著改回舊名,模擬舊版建出來的資料庫
	ctx := context.Background()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	exec := func(sql string) {
		t.Helper()
		if _, err := c.Exec(ctx, sql); err != nil {
			t.Fatalf("%s:%v", sql, err)
		}
	}
	for _, r := range pgLegacyRenames { // 表在前、索引與序號在後,倒著改時索引要先改(主鍵索引依附在表上,表改名不影響它)
		if r.kind != "table" {
			exec("alter " + r.kind + " " + r.new + " rename to " + r.old)
		}
	}
	for _, r := range pgLegacyRenames {
		if r.kind == "table" {
			exec("alter table " + r.new + " rename to " + r.old)
		}
	}
	exec(`insert into ops_documents (path, data) values ('legacy.json', '{"a":1}')`)
	exec(`insert into ops_activity (id, at, item) values ('act1', now(), '{}')`)

	open := func() error {
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		if err := OpenDB(url+sep+"pool_max_conns=4", testKey); err != nil {
			return err
		}
		CloseDB()
		Vault = NewMemoryVault()
		return nil
	}
	if err := open(); err != nil {
		t.Fatal(err)
	}
	count := func(sql string) (n int) {
		t.Helper()
		if err := c.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatalf("%s:%v", sql, err)
		}
		return n
	}
	if n := count(`select count(*) from pg_class where relnamespace = current_schema()::regnamespace and relname like 'ops\_%'`); n != 0 {
		t.Errorf("改名後不該有 ops_ 開頭的表、索引、序號:%d", n)
	}
	if n := count(`select count(*) from pg_constraint where connamespace = current_schema()::regnamespace and conname like 'ops\_%'`); n != 0 {
		t.Errorf("改名後不該有 ops_ 開頭的約束:%d", n)
	}
	if count(`select count(*) from omixor_documents where path = 'legacy.json'`) != 1 || count(`select count(*) from omixor_activity where id = 'act1'`) != 1 {
		t.Error("資料要保留")
	}
	exec(`insert into omixor_activity (id, at, item) values ('act2', now(), '{}')`) // bigserial 序號改名後仍可用
	// 冪等:再啟動一次
	if err := open(); err != nil {
		t.Fatalf("再啟動一次不該失敗:%v", err)
	}
	exec(`create table ops_files (path text)`) // 改名後又跑過舊版程式建出空的舊表:不覆蓋、報清楚的錯
	if err := open(); err == nil || !strings.Contains(err.Error(), "both exist") {
		t.Fatalf("新舊同時存在要報錯:%v", err)
	}
	exec(`drop table ops_files`)
}

// console 的表登記給核心 schema_check 後不算遺留表:全新資料庫 ok,登記的名單等於實際建出的 console 表
func TestEnsurePGSchemaRegistersConsoleTables(t *testing.T) {
	url := withDB(t)
	ctx := context.Background()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if err := ensurePGSchema(ctx, c); err != nil {
		t.Fatal(err)
	}
	p, err := store.AttachMigratedPG(url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rep, err := p.CheckSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.OrphanTables) != 0 {
		t.Fatalf("本機資料庫應 ok 且沒有遺留表:%+v", rep)
	}
	// 實際建出的表 = 不在核心 golden 裡的 omixor_ 表;用「拿掉登記後的遺留表」反推太繞,直接查資料庫,與核心的表(omixor_ 開頭且屬 golden)分開
	rows, err := c.Query(ctx, `select table_name from information_schema.tables where table_schema = current_schema() and table_type = 'BASE TABLE' and table_name = any($1) order by 1`, pgTableNames(pgSchema))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	want := []string{"omixor_activity", "omixor_documents", "omixor_files", "omixor_secrets", "omixor_sessions", "omixor_state"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(sortedCopy(pgTableNames(pgSchema)), want) {
		t.Errorf("console 表與登記名單不一致:db=%v 名單=%v", got, pgTableNames(pgSchema))
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
