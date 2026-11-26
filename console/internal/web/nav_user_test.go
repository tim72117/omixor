package web

import (
	"reflect"
	"strings"
	"testing"

	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
)

// 啟用 LDAP 時換頁只影響自己:畫面的 action、/api/tool 都只改呼叫者(在那個組織)的導覽狀態;
// 沒有登入者的連線(MCP 3765)移不動任何人的畫面,回應裡說明
func TestNavigateIsPerUser(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	joinOrg(t, e.app, e.au, "bob")
	bob := e.login("bob")
	jia := e.org
	nav := func(who *client, org string) (string, string) {
		t.Helper()
		code, v := who.do("GET", "/api/view/nav", nil, at(org))
		if code != 200 {
			t.Fatal(code, v)
		}
		f, _ := v["focusId"].(string)
		s, _ := v["section"].(string)
		return s + "/" + f, v["env"].(string)
	}
	want := func(who *client, org, page, env, what string) {
		t.Helper()
		if p, en := nav(who, org); p != page || en != env {
			t.Fatalf("%s:%s %s(要 %s %s)", what, p, en, page, env)
		}
	}
	want(alice, jia, "overview/", "production", "起始")

	e.ok(alice, "/api/action/navigate", map[string]any{"section": "development", "focusId": "ISS-1", "env": "staging"})
	want(alice, jia, "development/ISS-1", "staging", "alice 換頁")
	want(bob, jia, "overview/", "production", "bob 不動")

	e.ok(bob, "/api/tool/navigate", map[string]any{"section": "cabinet", "focus_id": "DOC-1"})
	want(bob, jia, "cabinet/DOC-1", "production", "bob 用工具換頁")
	want(alice, jia, "development/ISS-1", "staging", "alice 不動")

	// overview 工具的 ui 是自己的
	r := e.ok(bob, "/api/tool/overview", map[string]any{})
	if ui, _ := r["ui"].(map[string]any); ui["section"] != "cabinet" {
		t.Fatal(r["ui"])
	}

	// 同一人在另一個組織是另一份,焦點不會帶過去
	beta := e.ok(alice, "/api/action/org_create", map[string]any{"name": "Beta"})["id"].(string)
	want(alice, beta, "overview/", "production", "別的組織")
	if code, out := alice.do("POST", "/api/action/navigate", map[string]any{"section": "cabinet"}, at(beta)); code != 200 || out["error"] != nil {
		t.Fatal(code, out)
	}
	if code, out := alice.do("POST", "/api/tool/navigate", map[string]any{"env": "development"}, at(beta)); code != 200 || out["error"] != nil {
		t.Fatal(code, out)
	}
	want(alice, beta, "cabinet/", "development", "Beta 換頁")
	want(alice, jia, "development/ISS-1", "staging", "原組織不動")

	// 系統身分(3765 用的是同一個 Run):不移動任何人
	out, err := mcp.Run(e.app, "navigate", map[string]any{"section": "incidents"}, core.MCPActor)
	if err != nil || !strings.Contains(core2s(core.Object(out)), "no one's screen was moved") {
		t.Fatal(err, core2s(core.Object(out)))
	}
	want(alice, jia, "development/ISS-1", "staging", "3765 之後的 alice")
	want(bob, jia, "cabinet/DOC-1", "production", "3765 之後的 bob")

	// 去關掉的頁面:自己拿到提示,別人的畫面沒有提示
	_, before := bob.do("GET", "/api/view/system", nil, nil)
	e.ok(alice, "/api/action/system_save", map[string]any{"features": map[string]any{"cabinet": false}})
	res := e.ok(alice, "/api/action/navigate", map[string]any{"section": "cabinet"})
	if res["section"] != "overview" || res["notice"] == nil {
		t.Fatal(res)
	}
	_, after := bob.do("GET", "/api/view/system", nil, nil)
	if !reflect.DeepEqual(before["notice"], after["notice"]) {
		t.Fatal("別人不該看到提示", before["notice"], after["notice"])
	}
	want(bob, jia, "cabinet/DOC-1", "production", "bob 最後")
}
