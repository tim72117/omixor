package mcp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"omixor-core/mcp"
	"omixor-core/model"
)

// MCP Apps 畫面登記表的守門測試:工具 _meta 指到的 ui:// 都要有資源、資源也都要有工具指到(沒有孤兒、沒有懸空參照);
// 每個端點(管理、統一、OAuth 唯讀 / 讀寫)都要提供資源。這組測試會抓到「畫面檔寫了卻沒接線」(ISS-70)。

func rpcResult(t *testing.T, h http.Handler, method string, params any) map[string]any {
	t.Helper()
	w := rpcPost(h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, "")
	if w.Code != 200 {
		t.Fatalf("%s:HTTP %d %s", method, w.Code, w.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	r, _ := m["result"].(map[string]any)
	if r == nil {
		t.Fatalf("%s 沒有 result:%v", method, m)
	}
	return r
}

func uiEndpoints(t *testing.T) map[string]http.Handler {
	u := newUni(t)
	return map[string]http.Handler{
		"admin":           u.old,
		"unified":         u.unified,
		"oauth-readonly":  u.readOnlyEndpoint(),
		"oauth-write":     u.writeUnified(u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false)),
		"oauth-write-org": u.writeAdmin(u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false), "lab"),
	}
}

func TestUIResourcesRegistry(t *testing.T) {
	for name, h := range uiEndpoints(t) {
		t.Run(name, func(t *testing.T) {
			// initialize 宣告 resources 能力
			init := rpcResult(t, h, "initialize", map[string]any{})
			if init["capabilities"].(map[string]any)["resources"] == nil {
				t.Fatal("initialize 沒有宣告 resources")
			}
			// resources/list
			listed := map[string]bool{}
			for _, x := range rpcResult(t, h, "resources/list", map[string]any{})["resources"].([]any) {
				r := x.(map[string]any)
				if r["mimeType"] != "text/html;profile=mcp-app" {
					t.Errorf("%v mimeType", r)
				}
				listed[r["uri"].(string)] = true
			}
			if !listed[mcp.UIDecisionsURI] || mcp.UIDecisionsURI != "ui://omixor/decisions" {
				t.Fatalf("resources/list 缺 decisions:%v", listed)
			}
			// resources/read:每個都有非空 HTML 與共用區塊
			for uri := range listed {
				c := rpcResult(t, h, "resources/read", map[string]any{"uri": uri})["contents"].([]any)[0].(map[string]any)
				txt, _ := c["text"].(string)
				if strings.TrimSpace(txt) == "" || !strings.Contains(txt, "COMMON-CSS-BEGIN") || !strings.Contains(txt, "COMMON-JS-BEGIN") {
					t.Errorf("%s 內容空白或缺共用區塊", uri)
				}
			}
			// tools/list:_meta 參照 ⇔ 資源
			refs := map[string]bool{}
			for _, x := range rpcResult(t, h, "tools/list", map[string]any{})["tools"].([]any) {
				tool := x.(map[string]any)
				meta, _ := tool["_meta"].(map[string]any)
				if meta == nil {
					continue
				}
				uri, _ := meta["ui"].(map[string]any)["resourceUri"].(string)
				if uri == "" || meta["ui/resourceUri"] != uri {
					t.Errorf("%v:新舊兩個鍵要一致", tool["name"])
				}
				if !listed[uri] {
					t.Errorf("工具 %v 指到不存在的資源 %s", tool["name"], uri)
				}
				refs[uri] = true
				if tool["name"] == "issue_decisions" && uri != mcp.UIDecisionsURI {
					t.Errorf("issue_decisions 指到 %s", uri)
				}
			}
			for uri := range listed {
				if !refs[uri] {
					t.Errorf("資源 %s 沒有任何工具指到(孤兒)", uri)
				}
			}
			if !refs[mcp.UIDecisionsURI] {
				t.Error("issue_decisions 沒有標 ui 資源")
			}
		})
	}
}

// 畫面是唯讀的:不能呼叫任何工具(tools/call)
func TestUIViewsAreReadOnly(t *testing.T) {
	for _, r := range mcp.UIResources() {
		for _, bad := range []string{"tools/call", "issue_decision_answer", "issue_decision_add", "issue_create", "issue_update"} {
			if strings.Contains(r.Text, bad) {
				t.Errorf("%s 含有 %s", r.URI, bad)
			}
		}
	}
}
