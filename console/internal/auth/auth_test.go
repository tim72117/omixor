package auth

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"omixor-core/httperr"
	"omixor-core/storetest"

	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"
	"omixor-web/internal/core/coretest"
)

func TestMain(m *testing.M) {
	os.Setenv("OMIXOR_CONSOLE_DATA_DIR", coretest.DataDir("ops-auth-test"))
	core.Vault = core.NewMemoryVault()
	coretest.SkipIfNoPG()
	core.UseTestStores(coretest.NewStores().Get) // 沒有資料庫時用 PostgreSQL 的測試核心(每個 DataRoot 一份)
	storetest.Main(m)
}

func searchCfg(f *authtest.Fake) Config {
	return Config{Mode: "ldap", LDAP: LDAP{
		URL: "ldap://" + f.Addr, BindDN: authtest.SvcDN, BaseDN: authtest.Base, UserFilter: "(&(objectClass=person)(uid=%s))",
		RequiredGroup: authtest.UsersDN, AdminGroup: authtest.AdminsDN,
	}}
}

func newSvc(pw string) *Service { return New(&MemorySecrets{PW: pw}) }

// MARK: 測試

func TestSearchBindLoginWithGroups(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("svc-pw")
	if err := s.Save(searchCfg(f), nil); err != nil {
		t.Fatal(err)
	}
	tok, u, err := s.Login("alice", "alice-pw", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "alice" || u.Name != "Alice Chen" || u.Email != "alice@example.com" || u.DN != authtest.AliceDN || !u.Admin {
		t.Fatalf("使用者資料不對:%+v", u)
	}
	if got, ok := s.User(tok); !ok || got.ID != "alice" {
		t.Fatal("session 找不到")
	}
	// bob 在使用者群組但不是管理員
	_, u, err = s.Login("bob", "bob-pw", "127.0.0.1")
	if err != nil || u.Admin {
		t.Fatalf("bob:%+v %v", u, err)
	}
	// carol 不在允許的群組
	if _, _, err := s.Login("carol", "carol-pw", "127.0.0.1"); err == nil || !strings.Contains(err.Error(), "不在允許使用") {
		t.Fatalf("carol 應該被擋:%v", err)
	}
	s.Logout(tok)
	if _, ok := s.User(tok); ok {
		t.Fatal("登出後 session 還在")
	}
}

func TestBadPasswordsAndLockout(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("svc-pw")
	_ = s.Save(searchCfg(f), nil)
	for _, c := range []struct{ u, p string }{{"alice", "wrong"}, {"nobody", "x"}, {"alice", ""}, {"", "x"}} {
		if _, _, err := s.Login(c.u, c.p, "10.0.0.1"); err != ErrBadLogin {
			t.Fatalf("%q/%q 應該是帳密錯誤,得到 %v", c.u, c.p, err)
		}
	}
	// 空密碼一律擋掉(LDAP 的空密碼 bind 會被當成匿名成功),連 bind 都不會送
	f.Mu.Lock()
	n := len(f.Binds)
	f.Mu.Unlock()
	_, _, _ = s.Login("bob", "", "10.0.0.9")
	f.Mu.Lock()
	if len(f.Binds) != n {
		t.Fatal("空密碼不該送出 bind")
	}
	f.Mu.Unlock()
	// 同一帳號同一來源失敗 5 次就鎖 5 分鐘
	for i := 0; i < 5; i++ {
		_, _, _ = s.Login("bob", "nope", "10.0.0.2")
	}
	if _, _, err := s.Login("bob", "bob-pw", "10.0.0.2"); err == nil || !strings.Contains(err.Error(), "太多次") {
		t.Fatalf("應該被鎖:%v", err)
	}
	// 別的來源不受影響;時間過了就解鎖
	if _, _, err := s.Login("bob", "bob-pw", "10.0.0.3"); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	if _, _, err := s.Login("bob", "bob-pw", "10.0.0.2"); err != nil {
		t.Fatalf("5 分鐘後應該可以登入:%v", err)
	}
}

func TestFilterInjectionIsEscaped(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("svc-pw")
	_ = s.Save(searchCfg(f), nil)
	// 想用 * 或 ) 改寫篩選來配對任何人:要被跳脫
	for _, u := range []string{"*", "alice)(uid=*", "al*"} {
		if _, _, err := s.Login(u, "alice-pw", "127.0.0.1"); err != ErrBadLogin {
			t.Fatalf("%q 應該登不進去:%v", u, err)
		}
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, fl := range f.Filters {
		if strings.Contains(fl, "uid=*)") || strings.Contains(fl, "(uid=al*") {
			t.Fatalf("篩選沒有跳脫:%s", fl)
		}
	}
}

func TestDirectBindTemplate(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("")
	cfg := Config{Mode: "ldap", LDAP: LDAP{URL: "ldap://" + f.Addr, UserDNTemplate: "uid=%s,ou=people," + authtest.Base}}
	if err := s.Save(cfg, nil); err != nil {
		t.Fatal(err)
	}
	_, u, err := s.Login("alice", "alice-pw", "127.0.0.1")
	if err != nil || u.Name != "Alice Chen" || !u.Admin {
		t.Fatalf("%+v %v", u, err)
	}
	if _, _, err := s.Login("alice", "x", "127.0.0.1"); err != ErrBadLogin {
		t.Fatal(err)
	}
	// 帳號裡的 DN 特殊字元要跳脫,不能變成別的 DN
	if got := fillTemplate("uid=%s,ou=people,dc=x", "a,ou=admins"); got != `uid=a\,ou\=admins,ou=people,dc=x` {
		t.Fatal(got)
	}
}

func TestSessionExpiry(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("svc-pw")
	cfg := searchCfg(f)
	cfg.SessionHours = 1
	_ = s.Save(cfg, nil)
	tok, _, err := s.Login("alice", "alice-pw", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(61 * time.Minute) }
	if _, ok := s.User(tok); ok {
		t.Fatal("過期的 session 還能用")
	}
}

func TestValidateAndSave(t *testing.T) {
	s := newSvc("")
	for _, c := range []Config{
		{Mode: "ldap", LDAP: LDAP{URL: "http://x"}},
		{Mode: "ldap", LDAP: LDAP{URL: "ldap://x:389"}},
		{Mode: "ldap", LDAP: LDAP{URL: "ldap://x:389", UserDNTemplate: "uid=alice"}},
		{Mode: "ldap", LDAP: LDAP{URL: "ldap://x:389", BindDN: authtest.SvcDN, BaseDN: authtest.Base}}, // 沒存服務帳號密碼
	} {
		if err := s.Save(c, nil); err == nil {
			t.Fatalf("應該存不進去:%+v", c)
		}
	}
	// 服務帳號密碼存在 Secrets,不會寫進 auth.json
	pw := "svc-pw"
	if err := s.Save(Config{Mode: "ldap", LDAP: LDAP{URL: "ldap://x:389", BindDN: authtest.SvcDN, BaseDN: authtest.Base}}, &pw); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(configPath())
	if strings.Contains(string(b), "svc-pw") {
		t.Fatal("auth.json 裡有密碼")
	}
	if !s.Config().LDAP.HasBindPassword {
		t.Fatal("hasBindPassword 應該是 true")
	}
	// 重新讀檔一樣
	s2 := New(&MemorySecrets{PW: "svc-pw"})
	if s2.Config().LDAP.BindDN != authtest.SvcDN || !s2.Enabled() {
		t.Fatal("重開後設定不見")
	}
	os.Setenv("OMIXOR_AUTH", "off")
	defer os.Unsetenv("OMIXOR_AUTH")
	if New(&MemorySecrets{}).Enabled() {
		t.Fatal("OMIXOR_AUTH=off 應該關掉登入")
	}
}

func TestUnreachableServer(t *testing.T) {
	s := newSvc("svc-pw")
	cfg := Config{Mode: "ldap", LDAP: LDAP{URL: "ldap://127.0.0.1:1", BindDN: authtest.SvcDN, BaseDN: authtest.Base}}
	_, err := s.AuthenticateWith(cfg, nil, "alice", "pw")
	if err == nil || !strings.Contains(err.Error(), "連不到 LDAP") {
		t.Fatal(err)
	}
}

// 未登入就能打的登入:LDAP 連不上時只回固定句子,不含內網位址與 dial tcp;管理員診斷(AuthenticateWith)仍看得到原因
func TestLoginDoesNotLeakLDAPDetails(t *testing.T) {
	var logged []string
	old := httperr.Logf
	httperr.Logf = func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	t.Cleanup(func() { httperr.Logf = old })
	httperr.ResetThrottle()

	s := newSvc("svc-pw")
	cfg := Config{Mode: "ldap", LDAP: LDAP{URL: "ldap://127.0.0.1:1", BindDN: authtest.SvcDN, BaseDN: authtest.Base, UserFilter: "(uid=%s)"}}
	if err := s.Save(cfg, nil); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Login("alice", "pw", "10.1.1.1")
	if err == nil {
		t.Fatal("要失敗")
	}
	in := httperr.Classify(err)
	for _, w := range []string{"127.0.0.1", "dial tcp", "connect", "refused", ":1"} {
		if strings.Contains(in.Msg, w) {
			t.Errorf("登入回應含內部細節 %q:%s", w, in.Msg)
		}
	}
	if !strings.Contains(in.Msg, "連不到 LDAP") {
		t.Errorf("要說連不到 LDAP:%s", in.Msg)
	}
	if !strings.Contains(strings.Join(logged, "\n"), "127.0.0.1") {
		t.Errorf("原文要進 log:%v", logged)
	}
	// 管理員診斷路徑保留細節
	_, derr := s.AuthenticateWith(cfg, nil, "alice", "pw")
	if d := httperr.ClassifyDetail(derr); d.Masked || !strings.Contains(d.Msg, "127.0.0.1") {
		t.Errorf("診斷要保留細節:%+v", d)
	}
}

// lockedOut 只讀:查詢不同的帳號不會為每個 key 建紀錄;失敗紀錄的 key 數有上限(超過淘汰最舊),不會無限成長
func TestFailsMapBounded(t *testing.T) {
	s := newSvc("")
	base := time.Unix(1_000_000, 0)
	cur := base
	s.now = func() time.Time { return cur }
	for i := 0; i < 20000; i++ {
		if locked, _ := s.lockedOut(fmt.Sprintf("u%d|1.2.3.4", i)); locked {
			t.Fatal("沒失敗過不該鎖")
		}
	}
	if len(s.fails) != 0 {
		t.Fatalf("只查詢不該建紀錄:%d", len(s.fails))
	}
	for i := 0; i < maxFailKeys*3; i++ {
		cur = base.Add(time.Duration(i) * time.Millisecond)
		s.fail(fmt.Sprintf("u%d|1.2.3.4", i))
		if len(s.fails) > maxFailKeys {
			t.Fatalf("超過上限:%d", len(s.fails))
		}
	}
	// 最新的還在,最舊的被淘汰
	if _, ok := s.fails[fmt.Sprintf("u%d|1.2.3.4", maxFailKeys*3-1)]; !ok {
		t.Fatal("最新的失敗紀錄要在")
	}
	if _, ok := s.fails["u0|1.2.3.4"]; ok {
		t.Fatal("最舊的要被淘汰")
	}
	// 過期的紀錄被查詢時順手清掉
	cur = cur.Add(failWindow + time.Minute)
	k := fmt.Sprintf("u%d|1.2.3.4", maxFailKeys*3-1)
	if locked, _ := s.lockedOut(k); locked {
		t.Fatal("過期不該鎖")
	}
	if _, ok := s.fails[k]; ok {
		t.Fatal("過期的紀錄要刪掉")
	}
	// 單一 key 只留最近 maxFails 筆
	for i := 0; i < 50; i++ {
		s.fail("one|x")
	}
	if len(s.fails["one|x"]) != maxFails {
		t.Fatalf("單一 key 的紀錄要有上限:%d", len(s.fails["one|x"]))
	}
	if locked, wait := s.lockedOut("one|x"); !locked || wait <= 0 || wait > failWindow {
		t.Fatalf("要鎖,等待時間在 0 到 5 分鐘:%v %v", locked, wait)
	}
}
