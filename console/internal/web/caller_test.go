package web

import (
	"net/http"
	"strings"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"

	"omixor-core/model"
	"omixor-core/service"
)

// joinOrg 先建好帳號的核心使用者並加進目前組織(不是任何組織成員的人不能登入)
func joinOrg(t *testing.T, app *core.App, au *auth.Service, account string) *model.User {
	t.Helper()
	svc := core.AgentService()
	u, err := svc.ResolveDirectoryUserWith(au.UserSettings(au.Config()), service.DirectoryIdentity{ID: account})
	if err != nil {
		t.Fatal(account, err)
	}
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()
	if err := svc.Store.SaveMember(model.Member{Org: org, UserID: u.ID, Email: u.Email, Role: "member", AddedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	return u
}

// callerEnv 一份自己的核心與啟用 LDAP 的伺服器(alice 是 web 管理員,還沒有人登入)
type callerEnv struct {
	t   *testing.T
	app *core.App
	au  *auth.Service
	c   *client
	cfg auth.Config
	org string
}

func newCallerEnv(t *testing.T, change func(*auth.Config)) *callerEnv {
	t.Helper()
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	f := authtest.Start(t)
	au := auth.New(&auth.MemorySecrets{})
	c, app := newServer(t, au)
	cfg := ldapCfg(f)
	if change != nil {
		change(&cfg)
	}
	if _, out := c.do("POST", "/api/auth/config", map[string]any{"config": cfg, "bindPassword": "svc-pw", "testUsername": "alice", "testPassword": "alice-pw"}, nil); out["error"] != nil {
		t.Fatal(out)
	}
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()
	return &callerEnv{t: t, app: app, au: au, c: c, cfg: cfg, org: org}
}

func (e *callerEnv) tryLogin(user string) (*client, int, map[string]any) {
	x := &client{t: e.t, srv: e.c.srv, c: newJarClient()}
	code, out := x.do("POST", "/api/auth/web/login", map[string]any{"username": user, "password": user + "-pw"}, nil)
	return x, code, out
}

func (e *callerEnv) login(user string) *client {
	e.t.Helper()
	x, code, out := e.tryLogin(user)
	if code != 200 || out["error"] != nil {
		e.t.Fatal(user, code, out)
	}
	return x
}

// denied 登入被拒:403、原因包含 want、沒有 session
func (e *callerEnv) denied(user, want string) {
	e.t.Helper()
	x, code, out := e.tryLogin(user)
	msg, _ := out["error"].(string)
	if code != 403 || !strings.Contains(msg, want) {
		e.t.Fatalf("%s 應該被拒(%s):%d %v", user, want, code, out)
	}
	if code, _ := x.do("GET", "/api/view/alerts", nil, nil); code != 401 {
		e.t.Fatal("被拒的登入不能有 session", code)
	}
}

func (e *callerEnv) ok(who *client, path string, body map[string]any) map[string]any {
	e.t.Helper()
	code, out := who.do("POST", path, body, nil)
	if code != 200 || out["error"] != nil {
		e.t.Fatalf("%s:%d %v", path, code, out)
	}
	r, _ := out["result"].(map[string]any)
	return r
}

func (e *callerEnv) userOf(account string) *model.User {
	e.t.Helper()
	u, err := core.AgentService().Store.UserByEvoleapID(service.LDAPIDPrefix + account)
	if err != nil {
		e.t.Fatal(account, err)
	}
	return u
}

// creator Issue 內部資料的建立者(actor id)
func (e *callerEnv) creator(title string) string {
	e.t.Helper()
	list, err := core.AgentService().Store.ListIssues(e.org)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, i := range list {
		if i.Title == title {
			return i.CreatedBy
		}
	}
	e.t.Fatalf("沒有 Issue %s", title)
	return ""
}

// 寫入以登入者本人執行並署名;網頁登入和 OAuth 授權是同一位核心使用者;對話頻道、未讀數、帳號視圖各人分開
func TestWebWritesRunAsLoggedInUser(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	// bob 第一次登入被拒(不是任何組織成員),但使用者建好了,管理員用 email 加進組織後就能登入
	e.denied("bob", "還沒有加入任何組織")
	aliceUser, bobUser := e.userOf("alice"), e.userOf("bob")
	e.ok(alice, "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "member"})
	bob := e.login("bob")

	svc := core.AgentService()
	set, _ := e.au.LDAPSettings()
	if u, err := svc.ProveLDAP(set, "Alice", "alice-pw", "192.0.2.1"); err != nil || u.ID != aliceUser.ID {
		t.Fatalf("OAuth 要是同一位使用者:%v %v", u, err)
	}

	r := e.ok(alice, "/api/issues/"+e.org, map[string]any{"title": "alice 開的", "author": "mcp"})
	e.ok(alice, "/api/issues/"+e.org+"/"+r["id"].(string), map[string]any{"comments": []any{map[string]any{"text": "留言", "author": ""}}})
	if by := e.creator("alice 開的"); by != aliceUser.ActorID {
		t.Fatalf("要署名 alice:%s", by)
	}
	e.ok(alice, "/api/tool/doc_save", map[string]any{"title": "alice 的文件", "section": "docs", "body": "內文"})
	docs, err := svc.Store.ListDocs(e.org)
	if err != nil || len(docs) != 1 || docs[0].Author != aliceUser.ActorID {
		t.Fatalf("文件要署名 alice:%v %+v", err, docs)
	}
	if u := e.userOf("alice"); u.Role == "admin" {
		t.Fatal("不能把 web 管理員寫成核心的伺服器管理員")
	}
	// 請求自己塞 _caller 沒有用
	e.ok(bob, "/api/action/navigate", map[string]any{core.CallerKey: map[string]any{"webAdmin": true}, "section": "overview"})
	e.ok(bob, "/api/tool/issue_create", map[string]any{"title": "bob 工具開的"})
	if by := e.creator("bob 工具開的"); by != bobUser.ActorID {
		t.Fatalf("成員署名本人:%s(%s)", by, bobUser.ActorID)
	}
	r = e.ok(bob, "/api/issues/"+e.org, map[string]any{"title": "bob 的 API"})
	e.ok(bob, "/api/issues/"+e.org+"/"+r["id"].(string)+"/delete", map[string]any{})
	code, out := alice.do("POST", "/api/action/issue_trash", map[string]any{}, nil)
	if code != 200 || !strings.Contains(core2s(out), "bob 的 API") || !strings.Contains(core2s(out), bobUser.Email) {
		t.Fatal("刪除者要是 bob", code, out)
	}

	ch := e.ok(alice, "/api/action/channel_create", map[string]any{"name": "alice 的頻道"})
	e.ok(alice, "/api/action/channel_post", map[string]any{"id": ch["id"], "text": "哈囉"})
	_, v := bob.do("GET", "/api/view/channels", nil, nil)
	if strings.Contains(core2s(v), "alice 的頻道") || !strings.Contains(core2s(v["me"]), bobUser.Email) {
		t.Fatal("bob 的頻道視圖", v)
	}
	if code, _ := bob.do("POST", "/api/action/channel_messages", map[string]any{"id": ch["id"]}, nil); code == 200 {
		t.Fatal("bob 不該讀得到 alice 的頻道")
	}
	e.ok(alice, "/api/action/channel_members", map[string]any{"id": ch["id"], "add": []any{bobUser.Email}})
	_, bv := bob.do("GET", "/api/view/channels", nil, nil)
	_, av := alice.do("GET", "/api/view/channels", nil, nil)
	if !strings.Contains(core2s(bv), "alice 的頻道") || !strings.Contains(core2s(av["me"]), aliceUser.Email) {
		t.Fatal("加入後看得到", bv, av)
	}
	_, bb := bob.do("GET", "/api/view/badges", nil, nil)
	_, ab := alice.do("GET", "/api/view/badges", nil, nil)
	if bb["channels"] != float64(1) || ab["channels"] != nil {
		t.Fatal("未讀徽章", bb, ab)
	}
	if _, acc := bob.do("GET", "/api/view/account", nil, nil); acc["self"] != bobUser.Email {
		t.Fatal("account.self", acc)
	}
	acc := e.ok(bob, "/api/tool/issue_create", map[string]any{"title": "bob 接受的"})
	e.ok(bob, "/api/tool/issue_accept", map[string]any{"id": acc["key"]})
	if got := core2s(issueList(t, bob, e.org)); !strings.Contains(got, bobUser.Email) {
		t.Fatal("接受者要是 bob", got)
	}
}

// 移出所有組織、停用立即失效;核心的伺服器管理員不用加入組織
func TestWebMembershipRequired(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	bobUser := joinOrg(t, e.app, e.au, "bob")
	bob := e.login("bob")

	e.ok(alice, "/api/action/member_remove", map[string]any{"email": bobUser.Email})
	code, out := bob.do("GET", "/api/view/issues", nil, nil)
	if code != 401 || out["login"] != true || !strings.Contains(out["error"].(string), "還沒有加入任何組織") {
		t.Fatal("移出後", code, out)
	}
	if _, st := bob.do("GET", "/api/auth/web/status", nil, nil); st["loggedIn"] != false || st["denied"] == nil {
		t.Fatal("status", st)
	}
	if code, _ := bob.do("POST", "/api/tool/issue_create", map[string]any{"title": "移出後寫"}, nil); code != 401 {
		t.Fatal(code)
	}
	e.denied("bob", "還沒有加入任何組織")
	e.ok(alice, "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "member"})
	if code, _ := bob.do("GET", "/api/view/issues", nil, nil); code != 200 {
		t.Fatal("加回來原本的 session 就能用", code)
	}

	// 核心的伺服器管理員不用加入組織
	e.ok(alice, "/api/action/member_remove", map[string]any{"email": bobUser.Email})
	u := e.userOf("bob")
	u.Role = "admin"
	if err := core.AgentService().Store.SaveUser(*u); err != nil {
		t.Fatal(err)
	}
	if code, _ := bob.do("GET", "/api/view/issues", nil, nil); code != 200 {
		t.Fatal("伺服器管理員", code)
	}
	u.Role = "member"
	if err := core.AgentService().Store.SaveUser(*u); err != nil {
		t.Fatal(err)
	}

	// 停用:session 立即失效、不能再登入
	e.ok(alice, "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "member"})
	e.ok(alice, "/api/action/user_disable", map[string]any{"email": bobUser.Email})
	if code, _ := bob.do("GET", "/api/view/issues", nil, nil); code != 401 {
		t.Fatal("停用後", code)
	}
	e.denied("bob", "停用")
	// web 管理員不能把自己停用
	if code, _ := alice.do("POST", "/api/action/user_disable", map[string]any{"email": e.userOf("alice").Email}, nil); code == 200 {
		t.Fatal("停用自己應該被拒")
	}
	if code, _ := alice.do("GET", "/api/view/issues", nil, nil); code != http.StatusOK {
		t.Fatal(code)
	}
}

// 管理員先用 email 把人加進組織、對方還沒登入過:沒開 trustMail 對不上(說明怎麼辦;移除重加沒有用),開了就能登入,之後關掉照樣可以
func TestWebPreAddedMember(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	e.ok(alice, "/api/action/member_save", map[string]any{"email": "bob@corp.example", "role": "member"})
	e.denied("bob", "trustMail")
	e.ok(alice, "/api/action/member_remove", map[string]any{"email": "bob@corp.example"})
	e.denied("bob", "trustMail")
	e.ok(alice, "/api/action/member_save", map[string]any{"email": "bob@corp.example", "role": "member"})

	save := func(trust bool) {
		cfg := e.cfg
		cfg.TrustMail = trust
		if _, out := alice.do("POST", "/api/auth/config", map[string]any{"config": cfg, "testUsername": "alice", "testPassword": "alice-pw"}, nil); out["error"] != nil {
			t.Fatal(out)
		}
		alice = e.login("alice") // 改設定後全部重新登入
	}
	save(true)
	e.login("bob")
	save(false)
	e.login("bob")

	// email 已經是別的身分在用:trustMail 也對不上
	if err := core.AgentService().Store.SaveUser(model.User{ID: "u_other", EvoleapID: "evo-1", Email: "carol@corp.example", Role: "member", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	cfg := e.cfg
	cfg.LDAP.RequiredGroup, cfg.TrustMail = "", true
	e.cfg = cfg
	save(true)
	e.denied("carol", "另一個帳號")
}

// 存認證設定時,測試帳號依新設定要真的能登入(不然存了就沒人進得來)
func TestWebAuthConfigRejectsUnusableAdmin(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	f := authtest.Start(t)
	au := auth.New(&auth.MemorySecrets{})
	c, _ := newServer(t, au)
	cfg := ldapCfg(f)
	cfg.EmailDomain, cfg.Admins = "", []string{"bob"} // bob 沒有 mail
	_, out := c.do("POST", "/api/auth/config", map[string]any{"config": cfg, "bindPassword": "svc-pw", "testUsername": "bob", "testPassword": "bob-pw"}, nil)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "不能登入") || !strings.Contains(msg, "email") || au.Enabled() {
		t.Fatal(out)
	}
}
