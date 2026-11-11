package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// 需要 PostgreSQL:scripts/test-pg.sh 會開一個暫時的資料庫並設 OPS_TEST_DATABASE_URL。沒設就略過。
// 每個測試用自己的 schema,互不干擾,結束後刪掉。

var testKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func withDB(t *testing.T) string {
	t.Helper()
	url := os.Getenv("OPS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("沒設 OPS_TEST_DATABASE_URL(用 scripts/test-pg.sh 跑)")
	}
	schema := fmt.Sprintf("t_%d", time.Now().UnixNano())
	ctx := context.Background()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatal(err)
	}
	c.Close(ctx)
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	surl := url + sep + "search_path=" + schema
	if err := OpenDB(surl, testKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		CloseDB()
		Vault = NewMemoryVault()
		c, err := pgx.Connect(ctx, url)
		if err == nil {
			_, _ = c.Exec(ctx, "drop schema "+schema+" cascade")
			c.Close(ctx)
		}
	})
	return surl
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
	// 改名與整個資料夾搬移
	if err := RenameData(p, p+".unreadable-1"); err != nil || Exists(p) || !Exists(p+".unreadable-1") {
		t.Fatal("改名失敗")
	}
	_ = WriteJSON(filepath.Join(DataRoot(), "orgs", "x", "agents.json"), map[string]any{})
	_ = WriteJSON(filepath.Join(DataRoot(), "orgs", "xy", "agents.json"), map[string]any{})
	if err := MoveDataDir(filepath.Join(DataRoot(), "orgs", "x"), filepath.Join(DataRoot(), "deleted-orgs", "x-1")); err != nil {
		t.Fatal(err)
	}
	if DataDirExists(filepath.Join(DataRoot(), "orgs", "x")) || !DataDirExists(filepath.Join(DataRoot(), "deleted-orgs", "x-1")) {
		t.Fatal("搬資料夾失敗")
	}
	// 前綴要以 / 分界:orgs/xy 不能被當成 orgs/x 搬走
	if !DataDirExists(filepath.Join(DataRoot(), "orgs", "xy")) {
		t.Fatal("orgs/xy 被誤搬")
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
	_ = c.QueryRow(ctx, "select ciphertext from ops_secrets where account = 'github:token'").Scan(&ct)
	if strings.Contains(string(ct), "ghs_secret") || len(ct) == 0 {
		t.Fatal("密文裡有明文")
	}
	// 密文搬到別的帳號底下解不開(帳號是 AAD)
	_, _ = c.Exec(ctx, "insert into ops_secrets (account, nonce, ciphertext) select 'evil:token', nonce, ciphertext from ops_secrets where account = 'github:token'")
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
	if !Exists(filepath.Join(dir, "agents.json")) {
		t.Fatal("新組織的初始資料沒寫進資料庫")
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
	var issues []map[string]any
	_ = ReadJSON(filepath.Join(c.DataDir(), "issues.json"), &issues)
	if len(issues) != 1 || issues[0]["id"] != "ISS-9" {
		t.Fatalf("匯入的 Issue 讀不到:%v", issues)
	}
}
