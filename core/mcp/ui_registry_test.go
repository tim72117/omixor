package mcp_test

import (
	"encoding/json"
	"net/http"
	"regexp"
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

// 畫面能呼叫哪些工具:flowchart、items 唯讀(不含 tools/call);decisions 只能呼叫 issue_decisions 與 issue_decision_answer(重新整理、回覆),
// 不能多開別的工具。權限不在這裡放寬:OAuth 唯讀 / 讀寫的白名單照舊(見 oauth_write_test.go、readonly_test.go)
func TestUIViewToolCalls(t *testing.T) {
	toolName := regexp.MustCompile(`\b(?:issue|doc|channel|agent|incident|report|flowchart|cabinet|project|chain|schema)_[a-z_]+\b`)
	for _, r := range mcp.UIResources() {
		if r.URI != mcp.UIDecisionsURI {
			if strings.Contains(r.Text, "tools/call") {
				t.Errorf("%s 是唯讀畫面,不能呼叫工具", r.URI)
			}
			continue
		}
		if !strings.Contains(r.Text, `request("tools/call"`) {
			t.Error("decisions 畫面要用共用的 request() 發 tools/call")
		}
		if strings.Contains(r.Text, "唯讀:只顯示") || strings.Contains(r.Text, "不呼叫任何工具") {
			t.Error("decisions 畫面的註解還說自己唯讀")
		}
		if !strings.Contains(r.Text, "hostCaps.serverTools") {
			t.Error("decisions 畫面要依宿主能力(hostCapabilities.serverTools)決定是否顯示按鈕")
		}
		got := map[string]bool{}
		for _, n := range toolName.FindAllString(r.Text, -1) {
			got[n] = true
		}
		if len(got) != 2 || !got["issue_decisions"] || !got["issue_decision_answer"] {
			t.Errorf("decisions 畫面提到的工具名稱要剛好是 issue_decisions 與 issue_decision_answer:%v", got)
		}
	}
}

// decisions 畫面呼叫的兩個工具,OAuth 讀寫授權都要能呼叫(畫面按鈕才有用);唯讀授權只能讀(回覆會被擋,畫面顯示唯讀提示)
func TestUIDecisionToolsOAuthScopes(t *testing.T) {
	if !mcp.ReadOnlyTools["issue_decisions"] || mcp.ReadOnlyTools["issue_decision_answer"] {
		t.Error("唯讀授權:issue_decisions 可用、issue_decision_answer 不可用")
	}
	if !mcp.OAuthWriteTools["issue_decision_answer"] {
		t.Error("讀寫授權要開放 issue_decision_answer")
	}
	for _, d := range mcp.WithUIMeta(mcp.DecisionToolDefs()) {
		m := d.(map[string]any)
		meta, _ := m["_meta"].(map[string]any)
		if ui, _ := meta["ui"].(map[string]any); ui != nil && ui["visibility"] != nil {
			t.Errorf("%v 設了 ui.visibility:會讓 app 叫不到(預設含 app)", m["name"])
		}
	}
}
