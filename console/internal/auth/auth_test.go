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
		RequiredGroup: authtest.UsersDN,
	}, Admins: []string{"Alice"}}
}

func newSvc(pw string) *Service { return New(&MemorySecrets{PW: pw}) }

// signIn 驗證帳密後建 console 自己的 session(session 的行為;web 的一般登入改用核心的 web session,見 web 套件的測試)
func signIn(s *Service, user, pw, ip string) (string, User, error) {
	u, err := s.Verify(user, pw, ip)
	if err != nil {
		return "", User{}, err
	}
	tok, err := s.StartSession(u, false)
	return tok, u, err
}

// MARK: 測試

func TestSearchBindLoginWithGroups(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("svc-pw")
	if err := s.Save(searchCfg(f), nil); err != nil {
		t.Fatal(err)
	}
	tok, u, err := signIn(s, "alice", "alice-pw", "127.0.0.1")
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
	_, u, err = signIn(s, "bob", "bob-pw", "127.0.0.1")
	if err != nil || u.Admin {
		t.Fatalf("bob:%+v %v", u, err)
	}
	// carol 不在允許的群組
	if _, _, err := signIn(s, "carol", "carol-pw", "127.0.0.1"); err == nil || !strings.Contains(err.Error(), "不在允許使用") {
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
	s.failures.Window = 3 * time.Second
	for _, c := range []struct{ u, p string }{{"alice", "wrong"}, {"nobody", "x"}, {"alice", ""}, {"", "x"}} {
		if _, _, err := signIn(s, c.u, c.p, "10.0.0.1"); err != ErrBadLogin {
			t.Fatalf("%q/%q 應該是帳密錯誤,得到 %v", c.u, c.p, err)
		}
	}
	// 空密碼一律擋掉(LDAP 的空密碼 bind 會被當成匿名成功),連 bind 都不會送
	f.Mu.Lock()
	n := len(f.Binds)
	f.Mu.Unlock()
	_, _, _ = signIn(s, "bob", "", "10.0.0.9")
	f.Mu.Lock()
	if len(f.Binds) != n {
		t.Fatal("空密碼不該送出 bind")
	}
	f.Mu.Unlock()
	// 同一帳號失敗 5 次就鎖 10 分鐘(換來源也一樣;和 MCP 授權共用同一份計數)
	for i := 0; i < 5; i++ {
		_, _, _ = signIn(s, "bob", "nope", "10.0.0.2")
	}
	for _, u := range []string{"bob", "BOB", `CORP\bob`, "bob@corp.test"} {
		if _, _, err := signIn(s, u, "bob-pw", "10.0.0.3"); err == nil || !strings.Contains(err.Error(), "太多次") {
			t.Fatalf("%q 應該被鎖:%v", u, err)
		}
	}
	if set, _ := s.LDAPSettings(); set.Failures != &s.failures {
		t.Fatal("MCP 授權要用同一份計數")
	}
	time.Sleep(s.failures.Window) // 測試把鎖定時間縮短了(核心的時鐘換不掉)
	if _, _, err := signIn(s, "bob", "bob-pw", "10.0.0.2"); err != nil {
		t.Fatalf("10 分鐘後應該可以登入:%v", err)
	}
}

func TestFilterInjectionIsEscaped(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("svc-pw")
	_ = s.Save(searchCfg(f), nil)
	// 想用 * 或 ) 改寫篩選來配對任何人:要被跳脫
	for _, u := range []string{"*", "alice)(uid=*", "al*"} {
		if _, _, err := signIn(s, u, "alice-pw", "127.0.0.1"); err != ErrBadLogin {
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
	cfg := Config{Mode: "ldap", Admins: []string{"alice"}, LDAP: LDAP{URL: "ldap://" + f.Addr, UserDNTemplate: "uid=%s,ou=people," + authtest.Base}}
	if err := s.Save(cfg, nil); err != nil {
		t.Fatal(err)
	}
	_, u, err := signIn(s, "alice", "alice-pw", "127.0.0.1")
	if err != nil || u.Name != "Alice Chen" || !u.Admin {
		t.Fatalf("%+v %v", u, err)
	}
	if _, _, err := signIn(s, "alice", "x", "127.0.0.1"); err != ErrBadLogin {
		t.Fatal(err)
	}
	// 帳號裡的 DN 特殊字元要跳脫,不能變成別的 DN
	if _, _, err := signIn(s, "alice,ou=people", "alice-pw", "127.0.0.1"); err != ErrBadLogin {
		t.Fatal(err)
	}
}

func TestSessionExpiry(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("svc-pw")
	cfg := searchCfg(f)
	cfg.SessionHours = 1
	_ = s.Save(cfg, nil)
	tok, _, err := signIn(s, "alice", "alice-pw", "127.0.0.1")
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
	_, _, err := signIn(s, "alice", "pw", "10.1.1.1")
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
