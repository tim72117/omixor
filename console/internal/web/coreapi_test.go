package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"

	"omixor-core/api"
	"omixor-core/model"
	"omixor-core/service"
)

var csrf = map[string]string{api.CSRFHeader: "1"}

func anonymous(t *testing.T, c *client) *client { return &client{t: t, srv: c.srv, c: newJarClient()} }

func wantCode(t *testing.T, who *client, method, path string, body any, hdr map[string]string, want int) map[string]any {
	t.Helper()
	code, out := who.do(method, path, body, hdr)
	if code != want {
		t.Errorf("%s %s:%d %v,要 %d", method, path, code, out, want)
	}
	return out
}

func webToken(t *testing.T, c *client) string {
	t.Helper()
	u, _ := url.Parse(c.srv.URL + "/api/orgs")
	for _, ck := range c.c.Jar.Cookies(u) {
		if ck.Name == webCookie {
			return ck.Value
		}
	}
	t.Fatal("沒有 " + webCookie)
	return ""
}

// 啟用 LDAP 時,同一個連接埠的 core/api 套用和畫面一樣的登入限制,寫入署名本人,開放範圍照 coreapi.go
func TestCoreAPILoginRestrictions(t *testing.T) {
	e := newCallerEnv(t, func(c *auth.Config) { c.LDAP.RequiredGroup = "" })
	alice := e.login("alice") // 管理員帳號名單
	e.denied("bob", "還沒有加入任何組織")
	bobUser := e.userOf("bob")
	e.ok(alice, "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "member"})
	bob := e.login("bob")
	issues := "/api/orgs/" + e.org + "/issues"

	anon := anonymous(t, e.c)
	wantCode(t, anon, "GET", "/api/orgs", nil, nil, 401)
	// 不該開放的登入端點(guard 之後也不在 core 的路徑裡)
	for _, p := range []string{"/api/auth/ldap", "/api/auth/evoleap", "/api/auth/pair", "/api/auth/pair/claim", "/api/auth/gitlab/start"} {
		if code, _ := anon.do("POST", p, map[string]any{"username": "alice", "password": "alice-pw"}, nil); code == 200 {
			t.Error(p, "不該能用")
		}
		if code, _ := alice.do("POST", p, map[string]any{}, csrf); code != 404 && code != 405 {
			t.Error(p, "登入的人也不該有", code)
		}
	}

	wantCode(t, bob, "GET", "/api/orgs", nil, nil, 200)
	wantCode(t, bob, "GET", "/api/auth/me", nil, nil, 200)
	wantCode(t, bob, "POST", issues, map[string]any{"title": "沒帶 CSRF"}, nil, 403)
	wantCode(t, bob, "POST", issues, map[string]any{"title": "跨站"}, map[string]string{api.CSRFHeader: "1", "Origin": "https://evil.example"}, 403)
	wantCode(t, bob, "POST", issues, map[string]any{"title": "bob 用 REST 開的"}, csrf, 200)
	if by := e.creator("bob 用 REST 開的"); by != bobUser.ActorID {
		t.Errorf("要署名 bob:%s", by)
	}
	// 一般成員:管理類寫入、發角色憑證、伺服器管理都不行
	for _, p := range []string{"/api/orgs/" + e.org + "/members", "/api/orgs/" + e.org + "/projects", "/api/orgs/" + e.org + "/agent-tasks/claim-next", "/api/system/features"} {
		wantCode(t, bob, "POST", p, map[string]any{}, csrf, 403)
	}
	wantCode(t, bob, "GET", "/api/admin/users", nil, nil, 403)
	wantCode(t, alice, "GET", "/api/admin/users", nil, nil, 200)
	wantCode(t, alice, "POST", "/api/orgs/"+e.org+"/projects", map[string]any{"name": "REST 專案"}, csrf, 200)
	// 誰都不行的
	wantCode(t, alice, "POST", "/api/orgs", map[string]any{"id": "x-org", "name": "x"}, csrf, 403)
	wantCode(t, alice, "POST", "/api/orgs/"+e.org+"/delete", map[string]any{}, csrf, 403)
	wantCode(t, alice, "POST", "/api/admin/users", map[string]any{"email": bobUser.Email, "role": "admin"}, csrf, 403)

	// 非本組織:照核心的權限
	svc := core.AgentService()
	if err := svc.Store.CreateOrg(model.Org{ID: "elsewhere", Name: "別的", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, bob, "GET", "/api/orgs/elsewhere/issues", nil, nil, 403)

	// Bearer:同一套限制;管理員名單上的人用 Bearer 不升成管理員
	bearer := func(userID string) map[string]string {
		raw := service.RandID(service.TokenPrefix, 32)
		if err := svc.Store.CreateToken(model.Token{ID: service.RandID("t_", 9), UserID: userID, Device: "CLI", CreatedAt: model.Now(), Hash: service.HashToken(raw)}); err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + raw}
	}
	wantCode(t, anon, "GET", "/api/orgs", nil, bearer(bobUser.ID), 200)
	wantCode(t, anon, "GET", "/api/admin/users", nil, bearer(e.userOf("alice").ID), 403)
	wantCode(t, anon, "GET", "/api/orgs", nil, bearer(core.LocalAPIPrincipal().User.ID), 403) // 本機使用者
	if err := svc.Store.SaveUser(model.User{ID: "u_other", Email: "other@example.com", Role: "admin", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, anon, "GET", "/api/orgs", nil, bearer("u_other"), 403) // 不是 LDAP 帳號
	e.denied("carol", "還沒有加入任何組織")
	wantCode(t, anon, "GET", "/api/orgs", nil, bearer(e.userOf("carol").ID), 403) // 不是任何組織成員

	// 移出組織立即生效
	e.ok(alice, "/api/action/member_remove", map[string]any{"email": bobUser.Email})
	wantCode(t, bob, "GET", "/api/orgs", nil, nil, 403)
	e.ok(alice, "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "member"})
	wantCode(t, bob, "GET", "/api/orgs", nil, nil, 200)

	// core 的登出撤銷這個 web session
	tok := webToken(t, bob)
	// 被 CSRF 檢查擋下的登出:不清 cookie、session 照舊有效
	wantCode(t, bob, "POST", "/api/auth/logout", map[string]any{}, nil, 403)
	if webToken(t, bob) != tok {
		t.Error("被擋下的登出清掉了 cookie")
	}
	wantCode(t, bob, "GET", "/api/orgs", nil, nil, 200)
	wantCode(t, bob, "POST", "/api/auth/logout", map[string]any{}, csrf, 200)
	if _, err := svc.WebSession(tok); err == nil {
		t.Error("登出後 token 還能用")
	}
	wantCode(t, bob, "GET", "/api/view/issues", nil, nil, 401)

	// 改認證設定:所有 web session 失效
	bob = e.login("bob")
	tok = webToken(t, bob)
	e.ok(alice, "/api/auth/config", map[string]any{"config": e.cfg, "testUsername": "alice", "testPassword": "alice-pw"})
	wantCode(t, bob, "GET", "/api/view/issues", nil, nil, 401)
	wantCode(t, alice, "GET", "/api/orgs", nil, nil, 401)
	if _, err := svc.WebSession(tok); err == nil {
		t.Error("改設定後 token 還能用")
	}
}

// 非管理員開放的寫入路由都要真的存在(打錯字會變成默默擋掉)
func TestCoreMemberWritesExist(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	for pattern := range coreMemberWrites {
		method, path, _ := strings.Cut(pattern, " ")
		path = strings.NewReplacer("{org}", e.org, "{id}", "zz", "{itemId}", "1").Replace(path)
		if path == "/api/auth/logout" {
			continue
		}
		req, _ := http.NewRequest(method, e.c.srv.URL+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(api.CSRFHeader, "1")
		resp, err := alice.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(b), "404 page not found") || resp.StatusCode == 405 {
			t.Errorf("%s 不是 core 的路由:%d %s", pattern, resp.StatusCode, b)
		}
	}
}

// 沒啟用登入:本機名稱的 Host 用本機使用者身分;其他主機名稱、Bearer 都不行
func TestCoreAPILocalMode(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	c, _ := newServer(t, auth.New(&auth.MemorySecrets{}))
	wantCode(t, c, "GET", "/api/auth/me", nil, nil, 200)
	wantCode(t, c, "POST", "/api/orgs/nope/issues", map[string]any{"title": "x"}, nil, 403) // 沒帶 CSRF
	wantCode(t, c, "GET", "/api/auth/me", nil, map[string]string{"Authorization": "Bearer omx_whatever"}, 401)
	t.Setenv("OMIXOR_WEB_HOSTS", "ops.corp.example")
	wantCode(t, c, "GET", "/api/auth/me", nil, map[string]string{"Host": "ops.corp.example:5191"}, 401)
	wantCode(t, c, "GET", "/api/auth/me", nil, map[string]string{"Host": "evil.example"}, 403)
}

// 升級前的登入(ops_session)在有核心時不再認得;管理員修復登入不能用 core/api
func TestOldAndRepairSessionsHaveNoAccess(t *testing.T) {
	e := newCallerEnv(t, nil)
	old, err := e.au.StartSession(auth.User{ID: "alice"}, false)
	if err != nil {
		t.Fatal(err)
	}
	repair, err := e.au.StartSession(auth.User{ID: "alice"}, true)
	if err != nil {
		t.Fatal(err)
	}
	x := anonymous(t, e.c)
	wantCode(t, x, "GET", "/api/view/issues", nil, map[string]string{"Cookie": cookieName + "=" + old}, 401)
	wantCode(t, x, "GET", "/api/orgs", nil, map[string]string{"Cookie": cookieName + "=" + repair}, 401)
	// 帳號對得上核心使用者了,修復登入就不能再用
	e.login("alice")
	wantCode(t, x, "GET", "/api/view/issues", nil, map[string]string{"Cookie": cookieName + "=" + repair}, 401)
	wantCode(t, x, "GET", "/api/auth/config", nil, map[string]string{"Cookie": cookieName + "=" + repair}, 401)
}

// web session 照 sessionHours 到期
func TestWebSessionExpiry(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	p, err := core.AgentService().WebSession(webToken(t, alice))
	if err != nil || p.Token.ExpiresAt == nil {
		t.Fatal(err)
	}
	if d := time.Until(p.Token.ExpiresAt.Time); d > 12*time.Hour+time.Minute || d < 11*time.Hour {
		t.Fatal("到期時間", d)
	}
	if p.Token.Device != service.WebDevicePrefix+"alice" {
		t.Fatal(p.Token.Device)
	}
}
