package web

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"
)

// 失敗要回真的 HTTP 狀態碼,內容 {error, …} 不變(以前這幾個端點失敗也回 200)
func TestFailingEndpointsReturnRealStatus(t *testing.T) {
	captureWebLog(t)
	os.Remove(core.DataRoot() + "/auth.json") // 前面的測試可能留下啟用中的 LDAP 設定
	t.Cleanup(func() { os.Remove(core.DataRoot() + "/auth.json") })
	bob, alice, _ := adminTestClients(t)
	anon := &client{t: t, srv: bob.srv, c: newJarClient()}

	// POST /api/auth/login:帳密不對 401
	code, out := anon.do("POST", "/api/auth/login", map[string]any{"username": "bob", "password": "錯的"}, nil)
	if code != 401 || out["error"] != "帳號或密碼錯誤" {
		t.Errorf("login:%d %v", code, out)
	}

	// POST /api/auth/test:輸入有問題 400;連不上 LDAP 502,而且診斷訊息還在
	f := authtest.Start(t)
	code, out = alice.do("POST", "/api/auth/test", map[string]any{"config": ldapCfg(f), "username": "bob", "password": "錯的"}, nil)
	if code != 400 || out["error"] == nil {
		t.Errorf("auth/test 帳密不對:%d %v", code, out)
	}
	down := auth.Config{Mode: "ldap", Admins: []string{"alice"}, LDAP: auth.LDAP{URL: "ldap://127.0.0.1:1", BindDN: authtest.SvcDN, BaseDN: authtest.Base, UserFilter: "(uid=%s)"}}
	code, out = alice.do("POST", "/api/auth/test", map[string]any{"config": down, "bindPassword": "x", "username": "bob", "password": "pw"}, nil)
	if code != 502 || !strings.Contains(out["error"].(string), "連不到 LDAP") {
		t.Errorf("auth/test 連不上:%d %v", code, out)
	}

	// POST /api/auth/config:測試帳號登入失敗(設定沒存)400;連不上 502
	code, out = alice.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": "svc-pw", "testUsername": "alice", "testPassword": "錯的"}, nil)
	if code != 400 || !strings.Contains(out["error"].(string), "設定沒有存") {
		t.Errorf("auth/config 測試帳號失敗:%d %v", code, out)
	}
	code, out = alice.do("POST", "/api/auth/config", map[string]any{"config": down, "bindPassword": "x", "testUsername": "bob", "testPassword": "pw"}, nil)
	if code != 502 {
		t.Errorf("auth/config 連不上:%d %v", code, out)
	}
	// 測試帳號不是管理員:設定沒存 400
	code, out = alice.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": "svc-pw", "testUsername": "bob", "testPassword": "bob-pw"}, nil)
	if code != 400 || !strings.Contains(out["error"].(string), "不在管理員帳號名單") {
		t.Errorf("auth/config 測試帳號不是管理員:%d %v", code, out)
	}

	// POST /api/tool/{name} 與 /api/action/{name}:給人看的說明 400、StatusError 用它自己的、內部錯誤 500
	code, out = alice.do("POST", "/api/tool/zz_no_such_tool", map[string]any{}, nil)
	if code != 404 || out["error"] == nil {
		t.Errorf("tool 不存在的工具:%d %v", code, out)
	}
	if code, out := alice.do("POST", "/api/action/zz_no_such_action", map[string]any{}, nil); code != 404 || out["error"] == nil {
		t.Errorf("action 不存在的動作要和 tool 一致 404:%d %v", code, out)
	}
	for name, want := range map[string]int{"zz_err_user": 400, "zz_err_status": 409, "zz_err_pg": 500, "zz_err_wrapped_service": 409} {
		if code, out := alice.do("POST", "/api/action/"+name, map[string]any{}, nil); code != want || out["error"] == nil {
			t.Errorf("action %s:要 %d,得到 %d %v", name, want, code, out)
		}
	}

	// POST /api/attachments:空檔案、不是圖片 400;太大 413;內容仍是 {error}
	if code, out := alice.do("POST", "/api/attachments", map[string]any{"data": ""}, nil); code != 400 || out["error"] == nil {
		t.Errorf("附件空檔案:%d %v", code, out)
	}
	if code, out := alice.do("POST", "/api/attachments", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("<svg/>"))}, nil); code != 400 || out["error"] == nil {
		t.Errorf("附件不是圖片:%d %v", code, out)
	}
	big := base64.StdEncoding.EncodeToString(make([]byte, core.MaxAttachment+1))
	if code, out := alice.do("POST", "/api/attachments", map[string]any{"data": big}, nil); code != 413 || out["error"] == nil {
		t.Errorf("附件太大:%d %v", code, out)
	}
}
