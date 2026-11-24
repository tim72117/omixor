package mcp_test

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
)

// MCP Apps 接線的守門測試(人類端點才帶畫面,哪些工具帶畫面只由 apps.go 決定)。

var uiTools = []string{"flowchart_render", "issue_items", "issue_decisions"}

// uiToolsOf tools/list 裡帶 _meta.ui.resourceUri 的工具名稱(排序)
func uiToolsOf(t *testing.T, h http.Handler) []string {
	t.Helper()
	out := []string{}
	for _, x := range rpcResult(t, h, "tools/list", map[string]any{})["tools"].([]any) {
		tool := x.(map[string]any)
		if meta, _ := tool["_meta"].(map[string]any); meta["ui"] != nil {
			out = append(out, tool["name"].(string))
		}
	}
	slices.Sort(out)
	return out
}

func resourceURIs(t *testing.T, h http.Handler) []string {
	t.Helper()
	out := []string{}
	for _, x := range rpcResult(t, h, "resources/list", map[string]any{})["resources"].([]any) {
		out = append(out, x.(map[string]any)["uri"].(string))
	}
	slices.Sort(out)
	return out
}

func TestHumanEndpointsServeApps(t *testing.T) {
	wantTools := slices.Clone(uiTools)
	slices.Sort(wantTools)
	wantURIs := []string{mcp.UIDecisionsURI, mcp.UIFlowchartURI, mcp.UIItemsURI}
	slices.Sort(wantURIs)
	for name, h := range uiEndpoints(t) {
		t.Run(name, func(t *testing.T) {
			if got := uiToolsOf(t, h); !slices.Equal(got, wantTools) {
				t.Errorf("tools/list 帶畫面的工具 = %v,要剛好是 %v", got, wantTools)
			}
			if got := resourceURIs(t, h); !slices.Equal(got, wantURIs) {
				t.Errorf("resources/list = %v,要是 %v", got, wantURIs)
			}
		})
	}
}

// 角色端點列得出這三個工具,但它是無介面的 LLM 用:不帶畫面、不宣告資源
func TestAgentEndpointHasNoApps(t *testing.T) {
	svc, boss := newAgentSvc(t)
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "solo", Title: "做事"})
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "solo", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	auth := "Bearer solo:" + res.Credential.Secret
	listed := listTools(t, role, auth)
	for _, n := range uiTools {
		if !slices.Contains(listed, n) {
			t.Fatalf("角色端點應該列 %s(這個測試的前提):%v", n, listed)
		}
	}
	w := rpcPost(role, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, auth)
	if strings.Contains(w.Body.String(), `"_meta":{"ui"`) || strings.Contains(w.Body.String(), "resourceUri") {
		t.Errorf("角色端點不該帶畫面:%s", w.Body.String())
	}
	w = rpcPost(role, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "resources/list"}, auth)
	if strings.Contains(w.Body.String(), "ui://") {
		t.Errorf("角色端點不該列 ui:// 資源:%s", w.Body.String())
	}
}

// 執行者端點的白名單裡沒有帶畫面的工具,所以不需要接線;白名單日後加了,這裡要失敗提醒決定是否接 WithApps
func TestWorkerEndpointExposesNoUITools(t *testing.T) {
	for _, n := range uiTools {
		if mcp.WorkerToolAllow[n] {
			t.Errorf("執行者端點開放了 %s:要決定它是否該帶 MCP Apps 畫面(WithApps)", n)
		}
	}
}

// 靜態守門:WithUIMeta / UIResources 只在 apps.go 與測試出現,端點一律經 WithApps,不會有端點漏接或各接各的
func TestAppsWiredOnlyThroughWithApps(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if f == "apps.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(b), "\n") {
			if l := strings.TrimSpace(line); strings.HasPrefix(l, "//") {
				continue
			}
			if strings.Contains(line, "WithUIMeta(") || strings.Contains(line, "UIResources(") {
				t.Errorf("%s:%d 請改用 WithApps(Hooks{...}):%s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// WithApps 不改掉端點自己的 Defs 與 Resources:Defs 先套、再加畫面;已設的 Resources 保留
func TestWithAppsComposes(t *testing.T) {
	h := mcp.WithApps(mcp.Hooks{Defs: func(defs []any) []any { return defs[:1] }, Resources: []mcp.Resource{{URI: "x://y"}}})
	defs := h.Defs([]any{map[string]any{"name": "flowchart_render"}, map[string]any{"name": "issue_items"}})
	if len(defs) != 1 || defs[0].(map[string]any)["_meta"] == nil {
		t.Errorf("先套原本的 Defs 再加 _meta:%v", defs)
	}
	if len(h.Resources) != 1 || h.Resources[0].URI != "x://y" {
		t.Errorf("已設的 Resources 不該被蓋掉:%v", h.Resources)
	}
}

func newAgentSvc(t *testing.T) (*service.Service, *model.Principal) {
	t.Helper()
	u := newUni(t)
	boss := u.boss
	if _, err := u.svc.SaveAgent(boss, "lab", model.Agent{ID: "solo", Name: "獨行", Active: true}); err != nil {
		t.Fatal(err)
	}
	return u.svc, boss
}
