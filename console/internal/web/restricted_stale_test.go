package web

import (
	"strings"
	"testing"
)

// 專案清單是系統身分的快取:別的路徑(REST、MCP)剛把專案改成受限時,快取裡還是 restricted=false,非成員也不能看到
func TestJustRestrictedProjectHiddenFromStaleList(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	joinOrg(t, e.app, e.au, "bob")
	bob := e.login("bob")
	p := e.ok(alice, "/api/action/project_create", map[string]any{"name": "改受限案", "key": "STL"})["id"].(string)
	if got := rawBody(t, bob, "GET", "/api/view/projects", nil); !strings.Contains(got, "改受限案") {
		t.Fatal("公開專案要看得到", got)
	}
	if code, out := alice.do("PUT", "/api/orgs/"+e.org+"/projects/"+p, map[string]any{"restricted": true},
		map[string]string{"X-Omixor-CSRF": "1", "Content-Type": "application/json"}); code != 200 {
		t.Fatal("改成受限", code, out)
	}
	if got := rawBody(t, bob, "GET", "/api/view/projects", nil); strings.Contains(got, "改受限案") {
		t.Error("剛改成受限的專案從舊快取洩漏", got)
	}
}
