package web

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"
)

// 啟用 LDAP,回傳已登入 bob(非管理員)的 client 與另一個登入 alice(管理員)的 client
func adminTestClients(t *testing.T) (bob, alice *client, app *core.App) {
	t.Helper()
	f := authtest.Start(t)
	au := auth.New(&auth.MemorySecrets{})
	c, app := newServer(t, au)
	if _, out := c.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": "svc-pw", "testUsername": "alice", "testPassword": "alice-pw"}, nil); out["error"] != nil {
		t.Fatal(out)
	}
	login := func(user, pw string) *client {
		x := &client{t: t, srv: c.srv, c: newJarClient()}
		if _, out := x.do("POST", "/api/auth/login", map[string]any{"username": user, "password": pw}, nil); out["error"] != nil {
			t.Fatal(out)
		}
		return x
	}
	return login("bob", "bob-pw"), login("alice", "alice-pw"), app
}

// 非管理員:功能開關與其他「核心要管理員 / web 共用設定」的寫入一律 403;系統頁唯讀並說明原因
func TestNonAdminCannotWriteAdminSettings(t *testing.T) {
	bob, alice, app := adminTestClients(t)

	for name := range adminActions {
		code, out := bob.do("POST", "/api/action/"+name, map[string]any{}, nil)
		if code != 403 || !strings.Contains(out["error"].(string), "只有管理員") {
			t.Errorf("%s:非管理員應該 403,收到 %d %v", name, code, out)
		}
	}
	for name := range adminTools {
		if code, _ := bob.do("POST", "/api/tool/"+name, map[string]any{}, nil); code != 403 {
			t.Errorf("工具 %s:非管理員應該 403,收到 %d", name, code)
		}
	}
	// 一般成員能做的照常(切換頁面、看資料)
	if code, out := bob.do("POST", "/api/action/navigate", map[string]any{"section": "incidents"}, nil); code != 200 || out["error"] != nil {
		t.Fatalf("navigate:%d %v", code, out)
	}

	// 系統頁:非管理員 canEdit 一定是 false,並說明原因;system_reload 的結果也一樣
	code, v := bob.do("GET", "/api/view/system", nil, nil)
	if code != 200 || v["canEdit"] != false || !strings.Contains(v["readOnlyReason"].(string), "不是 web 的管理員") {
		t.Fatalf("非管理員的系統頁:%d %v", code, v)
	}
	if sv, _ := v["server"].(map[string]any); sv == nil || sv["canEdit"] != false || !strings.Contains(sv["readOnlyReason"].(string), "不是 web 的管理員") {
		t.Fatalf("非管理員的系統層也唯讀:%v", v["server"])
	}
	_, out := bob.do("POST", "/api/action/system_reload", map[string]any{}, nil)
	if r, _ := out["result"].(map[string]any); r == nil || r["canEdit"] != false || r["readOnlyReason"] == nil {
		t.Fatalf("system_reload:%v", out)
	}
	// 管理員:不會被 web 擋(沒有資料庫時是核心的錯誤,不是 403),也沒有唯讀說明
	code, out = alice.do("POST", "/api/action/system_save", map[string]any{"features": map[string]any{"cabinet": false}}, nil)
	if code == 403 {
		t.Fatalf("管理員不應該被擋:%v", out)
	}
	code, out = alice.do("POST", "/api/action/server_features_save", map[string]any{"features": map[string]any{"familyAssistant": true}}, nil)
	if code == 403 {
		t.Fatalf("管理員改系統層不應該被 web 擋:%v", out)
	}
	_, v = alice.do("GET", "/api/view/system", nil, nil)
	if _, ok := v["readOnlyReason"]; ok {
		t.Fatalf("管理員不應該有唯讀說明:%v", v)
	}
	if sv, _ := v["server"].(map[string]any); sv == nil || sv["readOnlyReason"] != nil {
		t.Fatalf("管理員的系統層不應該有唯讀說明:%v", v["server"])
	}

	// 畫面顯示的組織和目前的組織不同:409,提示重新整理
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()
	code, out = alice.do("POST", "/api/action/system_save", map[string]any{"org": org + "-x", "features": map[string]any{"cabinet": false}}, nil)
	if code != 409 || !strings.Contains(out["error"].(string), "重新整理") {
		t.Fatalf("組織不同應該 409:%d %v", code, out)
	}
	if v["org"] != org {
		t.Fatalf("system view 要帶組織:%v", v["org"])
	}
}

// 沒啟用登入(本機模式):本機使用者視為管理員,不擋
func TestLocalModeIsAdmin(t *testing.T) {
	c, _ := newServer(t, nil)
	_, v := c.do("GET", "/api/view/system", nil, nil)
	if _, ok := v["readOnlyReason"]; ok {
		t.Fatalf("本機模式不應該唯讀:%v", v)
	}
	if code, _ := c.do("POST", "/api/action/system_save", map[string]any{"features": map[string]any{"cabinet": false}}, nil); code == 403 {
		t.Fatal("本機模式不應該 403")
	}
}

func newJarClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}
