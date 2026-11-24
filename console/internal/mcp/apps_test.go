package mcp

import (
	"encoding/json"
	"slices"
	"testing"

	coremcp "omixor-core/mcp"
	"omixor-web/internal/core"
)

// 本機 /mcp 也要帶 MCP Apps 畫面(和雲端同一個 coremcp.WithApps):tools/list 的 _meta.ui 掛在功能開關過濾後留下的工具上,resources/list 列三個 ui:// 畫面
func TestConsoleMCPServesApps(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	t.Setenv("OMIXOR_NO_TIMERS", "1")
	h := &Handler{App: core.New()}
	rpc := func(method string) json.RawMessage {
		var r struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(h.Handle(core.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method})), &r); err != nil || r.Result == nil {
			t.Fatalf("%s:%v", method, err)
		}
		return r.Result
	}

	var tools struct {
		Tools []struct {
			Name string         `json:"name"`
			Meta map[string]any `json:"_meta"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(rpc("tools/list"), &tools)
	withUI, listed := []string{}, []string{}
	for _, tl := range tools.Tools {
		listed = append(listed, tl.Name)
		if tl.Meta["ui"] != nil {
			withUI = append(withUI, tl.Name)
		}
	}
	slices.Sort(withUI)
	if want := []string{"flowchart_render", "issue_decisions", "issue_items"}; !slices.Equal(withUI, want) {
		t.Errorf("帶畫面的工具 = %v,要是 %v", withUI, want)
	}
	if slices.Contains(listed, "alert_create") { // 功能開關仍然生效
		t.Error("功能關著的 alert_create 不該出現")
	}

	var res struct {
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
	}
	_ = json.Unmarshal(rpc("resources/list"), &res)
	uris := []string{}
	for _, r := range res.Resources {
		uris = append(uris, r.URI)
	}
	slices.Sort(uris)
	if want := []string{coremcp.UIDecisionsURI, coremcp.UIFlowchartURI, coremcp.UIItemsURI}; !slices.Equal(uris, want) {
		t.Errorf("resources/list = %v,要是 %v", uris, want)
	}
}
