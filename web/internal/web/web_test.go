package web

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "omixor-web-test")
	os.Setenv("OPS_DATA_DIR", dir)
	os.Setenv("OPS_NO_TIMERS", "1")
	// 預設不載入範本;這裡的測試要用範例資料(告警等),所以開著(templates/ 在 repo 最外層)
	os.Setenv("OMIXOR_TEMPLATES", "1")
	core.Vault = core.NewMemoryVault()
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type client struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func newServer(t *testing.T, au *auth.Service) (*client, *core.App) {
	app := core.New()
	app.Start()
	srv := httptest.NewServer(Handler(app, nil, au))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, srv: srv, c: &http.Client{Jar: jar}}, app
}

func (c *client) do(method, path string, body any, hdr map[string]string) (int, map[string]any) {
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func ldapCfg(f *authtest.Fake) auth.Config {
	return auth.Config{Mode: "ldap", LDAP: auth.LDAP{
		URL: "ldap://" + f.Addr, BindDN: authtest.SvcDN, BaseDN: authtest.Base,
		RequiredGroup: authtest.UsersDN, AdminGroup: authtest.AdminsDN,
	}}
}

func TestLocalModeNeedsNoLogin(t *testing.T) {
	os.Remove(core.DataRoot() + "/auth.json")
	c, _ := newServer(t, auth.New(&auth.MemorySecrets{}))
	code, st := c.do("GET", "/api/auth/status", nil, nil)
	if code != 200 || st["mode"] != "off" || st["loggedIn"] != true {
		t.Fatalf("%d %v", code, st)
	}
	if code, _ := c.do("GET", "/api/view/alerts", nil, nil); code != 200 {
		t.Fatal(code)
	}
}

func TestGuards(t *testing.T) {
	c, _ := newServer(t, auth.New(&auth.MemorySecrets{}))
	// 非本機的 Host(DNS rebinding)
	if code, _ := c.do("GET", "/api/view/alerts", nil, map[string]string{"Host": "evil.example"}); code != 403 {
		t.Fatalf("Host 沒擋:%d", code)
	}
	// 跨站 Origin
	if code, _ := c.do("POST", "/api/action/navigate", map[string]any{}, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("Origin 沒擋:%d", code)
	}
	// 表單送的 POST(CSRF)
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/action/navigate", strings.NewReader("section=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, _ := c.c.Do(req)
	if resp.StatusCode != 415 {
		t.Fatalf("表單 POST 沒擋:%d", resp.StatusCode)
	}
	// OPS_WEB_HOSTS 列的主機可以
	os.Setenv("OPS_WEB_HOSTS", "ops.corp.example")
	defer os.Unsetenv("OPS_WEB_HOSTS")
	if code, _ := c.do("GET", "/api/view/alerts", nil, map[string]string{"Host": "ops.corp.example:5191"}); code != 200 {
		t.Fatalf("允許的主機被擋:%d", code)
	}
}

func TestLDAPLoginFlowAndAttribution(t *testing.T) {
	f := authtest.Start(t)
	au := auth.New(&auth.MemorySecrets{})
	c, app := newServer(t, au)

	// 本機模式的管理員啟用 LDAP:測試帳號登不進去或不是管理員時不存
	pw := "svc-pw"
	_, out := c.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": pw, "testUsername": "alice", "testPassword": "wrong"}, nil)
	if !strings.Contains(out["error"].(string), "設定沒有存") || au.Enabled() {
		t.Fatalf("錯的測試帳密不該存:%v", out)
	}
	_, out = c.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": pw, "testUsername": "bob", "testPassword": "bob-pw"}, nil)
	if !strings.Contains(out["error"].(string), "管理員群組") || au.Enabled() {
		t.Fatalf("非管理員的測試帳號不該存:%v", out)
	}
	_, out = c.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": pw, "testUsername": "alice", "testPassword": "alice-pw"}, nil)
	if out["error"] != nil || !au.Enabled() {
		t.Fatalf("應該存成功:%v", out)
	}

	// 現在要登入
	if code, out := c.do("GET", "/api/view/alerts", nil, nil); code != 401 || out["login"] != true {
		t.Fatalf("沒登入應該 401:%d", code)
	}
	if code, _ := c.do("GET", "/api/events", nil, nil); code != 401 {
		t.Fatalf("SSE 也要登入:%d", code)
	}
	_, st := c.do("GET", "/api/auth/status", nil, nil)
	if st["mode"] != "ldap" || st["loggedIn"] != false {
		t.Fatal(st)
	}
	_, out = c.do("POST", "/api/auth/login", map[string]any{"username": "bob", "password": "nope"}, nil)
	if out["error"] != "帳號或密碼錯誤" {
		t.Fatal(out)
	}
	_, out = c.do("POST", "/api/auth/login", map[string]any{"username": "bob", "password": "bob-pw"}, nil)
	if out["error"] != nil {
		t.Fatal(out)
	}
	if code, _ := c.do("GET", "/api/view/alerts", nil, nil); code != 200 {
		t.Fatalf("登入後應該能讀:%d", code)
	}
	// bob 不是管理員:不能看/改認證設定
	if code, _ := c.do("GET", "/api/auth/config", nil, nil); code != 403 {
		t.Fatalf("非管理員不該看到設定:%d", code)
	}

	// 動作以登入者署名(MCP 工具與畫面 Action 都是)
	app.Mu.Lock()
	alert := app.C["alerts"][0]["id"].(string)
	app.Mu.Unlock()
	_, out = c.do("POST", "/api/tool/alert_update", map[string]any{"alert_id": alert, "action": "acknowledge"}, nil)
	if out["error"] != nil {
		t.Fatal(out)
	}
	app.Mu.Lock()
	_, a := app.Find("alerts", alert)
	by := core.ActorFrom(a["acknowledgedBy"])
	app.Mu.Unlock()
	if by.ID != "bob" || by.Kind != "person" {
		t.Fatalf("署名應該是 bob:%+v", by)
	}

	// 登出後又要登入
	c.do("POST", "/api/auth/logout", map[string]any{}, nil)
	if code, _ := c.do("GET", "/api/view/alerts", nil, nil); code != 401 {
		t.Fatalf("登出後應該 401:%d", code)
	}
	// cookie 是 HttpOnly + SameSite=Strict
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/auth/login", strings.NewReader(`{"username":"alice","password":"alice-pw"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	sc := resp.Header.Get("Set-Cookie")
	if !strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "SameSite=Strict") {
		t.Fatalf("cookie 屬性不對:%s", sc)
	}
}
