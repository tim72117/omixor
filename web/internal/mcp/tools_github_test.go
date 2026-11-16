package mcp

import (
	"encoding/json"
	"testing"

	"omixor-web/internal/core"
)

// 共用的測試工具(其他工具的測試也用)。GitHub 的 MCP 工具測試搬到選用模組 web/internal/modules/integrations(tools_github_test.go)。

func extTestApp(t *testing.T) *core.App {
	t.Helper()
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	t.Setenv("OPS_NO_TIMERS", "1")
	core.Vault = core.NewMemoryVault()
	a := core.New()
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	return a
}

// extCall 走完整的 JSON-RPC(同 Swift 測試的 tool()):回傳 (解析後的結果或錯誤文字, isError)
func extCall(t *testing.T, a *core.App, name string, args map[string]any) (any, bool) {
	t.Helper()
	h := &Handler{App: a}
	msg := core.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(h.Handle(msg), &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("回應格式不對:%v", err)
	}
	text := resp.Result.Content[0].Text
	if resp.Result.IsError {
		return text, true
	}
	var out any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("結果不是 JSON:%s", text)
	}
	return out, false
}

func extOK(t *testing.T, a *core.App, name string, args map[string]any) map[string]any {
	t.Helper()
	out, isErr := extCall(t, a, name, args)
	if isErr {
		t.Fatalf("%s 失敗:%v", name, out)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("%s 回傳不是物件:%v", name, out)
	}
	return m
}

func extErr(t *testing.T, a *core.App, name string, args map[string]any) string {
	t.Helper()
	out, isErr := extCall(t, a, name, args)
	if !isErr {
		t.Fatalf("%s 應該失敗,卻回傳 %v", name, out)
	}
	return out.(string)
}
