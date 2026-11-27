package web

import (
	"strings"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"

	"omixor-core/model"
)

// 管理員名單上、對不上核心使用者的帳號:只能改認證設定,改好就能正常用;停用的、不在名單上的照樣擋
func TestAdminRepairSession(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	f := authtest.Start(t)
	au := auth.New(&auth.MemorySecrets{})
	c, app := newServer(t, au)
	// 升級前的狀態:管理員先用 email 被預先加入、沒開 trustMail,登入設定直接寫在檔案裡
	svc := core.AgentService()
	if err := svc.Store.SaveUser(model.User{ID: "u_pre", Email: "alice@example.com", Role: "member", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	cfg := ldapCfg(f)
	pw := "svc-pw"
	if err := au.Save(cfg, &pw); err != nil {
		t.Fatal(err)
	}
	app.Mu.Lock()
	org := app.CurrentOrgID()
	app.Mu.Unlock()

	alice := &client{t: t, srv: c.srv, c: newJarClient()}
	code, out := alice.do("POST", "/api/auth/web/login", map[string]any{"username": "alice", "password": "alice-pw"}, nil)
	if msg, _ := out["denied"].(string); code != 200 || out["repair"] != true || !strings.Contains(msg, "trustMail") {
		t.Fatal(code, out)
	}
	_, st := alice.do("GET", "/api/auth/web/status", nil, nil)
	if st["loggedIn"] != false || st["repair"] != true || st["denied"] == nil || st["user"] == nil {
		t.Fatal(st)
	}
	for _, x := range [][2]string{
		{"GET", "/api/view/orgs"}, {"GET", "/api/view/issues"}, {"GET", "/api/issues/" + org}, {"GET", "/api/events"},
		{"POST", "/api/tool/issue_create"}, {"POST", "/api/action/member_save"}, {"POST", "/api/action/system_org_set"}, {"GET", "/api/office/state"},
	} {
		var body any
		if x[0] == "POST" {
			body = map[string]any{"title": "x", "email": "a@b.c"}
		}
		if code, out := alice.do(x[0], x[1], body, nil); code != 401 {
			t.Fatal(x, "要 401", code, out)
		}
	}
	if code, out := alice.do("GET", "/api/auth/config", nil, nil); code != 200 {
		t.Fatal(code, out)
	}
	off := cfg
	off.Mode = "off"
	if code, _ := alice.do("POST", "/api/auth/config", map[string]any{"config": off}, nil); code != 403 || !au.Enabled() {
		t.Fatal("修復登入不能關掉登入", code)
	}
	cfg.TrustMail = true
	if _, out := alice.do("POST", "/api/auth/config", map[string]any{"config": cfg, "testUsername": "alice", "testPassword": "alice-pw"}, nil); out["error"] != nil {
		t.Fatal(out)
	}
	code, out = alice.do("POST", "/api/auth/web/login", map[string]any{"username": "alice", "password": "alice-pw"}, nil)
	if code != 200 || out["repair"] != nil {
		t.Fatal("修好後正常登入", code, out)
	}
	if code, _ := alice.do("GET", "/api/view/issues", nil, nil); code != 200 {
		t.Fatal(code)
	}

	// 停用的管理員:照樣擋
	u, err := svc.Store.UserByEvoleapID("ldap:alice")
	if err != nil {
		t.Fatal(err)
	}
	u.Disabled = true
	if err := svc.Store.SaveUser(*u); err != nil {
		t.Fatal(err)
	}
	if code, out := alice.do("GET", "/api/auth/config", nil, nil); code != 401 {
		t.Fatal("停用的管理員", code, out)
	}
	if code, out := alice.do("POST", "/api/auth/web/login", map[string]any{"username": "alice", "password": "alice-pw"}, nil); code != 403 {
		t.Fatal("停用的管理員不能登入", code, out)
	}
	if w := strings.Join(core.UpgradeLoginWarnings([]string{"alice"}), "\n"); !strings.Contains(w, "管理員 alice") {
		t.Fatal(w)
	}
}
