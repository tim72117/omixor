package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"omixor-web/internal/core"
)

// 工具結果的形狀合約(console 的 /mcp,含核心工具與 console 自己的工具):
// 成功或失敗都要有 structuredContent(JSON 物件)、content[0].text 是同一個物件的 JSON;
// 失敗是 isError = true、{"error": {code, message, status}}。
func TestConsoleToolResultContract(t *testing.T) {
	a := extTestApp(t)
	h := &Handler{App: a}

	// tools/list 的每個工具各用幾種參數呼叫,只驗證形狀(多半是參數錯誤的失敗)
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(h.Handle(core.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})), &list); err != nil || len(list.Result.Tools) == 0 {
		t.Fatalf("tools/list 失敗:%v", err)
	}
	var okSeen, errSeen int
	for _, tl := range list.Result.Tools {
		for _, args := range []map[string]any{{}, {"__bogus__": 1}, {"id": "nope", "org": "nope"}} {
			if contractShape(t, tl.Name, contractCall(t, h, tl.Name, args)) {
				errSeen++
			} else {
				okSeen++
			}
		}
	}
	if okSeen == 0 || errSeen == 0 {
		t.Errorf("成功與失敗都要有:ok=%d err=%d", okSeen, errSeen)
	}

	// 代表呼叫:核心工具與 console 自己的工具,成功與失敗各一
	for _, name := range []string{"orgs", "office_state", "fb_drafts"} {
		if contractShape(t, name, contractCall(t, h, name, nil)) {
			t.Errorf("%s 預期成功", name)
		}
	}
	for _, name := range []string{"site_visitors", "voice_submit", "voice_jobs"} {
		if !contractShape(t, name, contractCall(t, h, name, map[string]any{"id": "VJ-9"})) {
			t.Errorf("%s 預期失敗", name)
		}
	}
}

func contractCall(t *testing.T, h *Handler, tool string, args map[string]any) map[string]any {
	t.Helper()
	raw := h.Handle(core.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}}))
	var m struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Result == nil {
		t.Fatalf("%s:不是 tools/call 結果:%s", tool, raw)
	}
	return m.Result
}

// contractShape 驗證一個結果的合約,回傳它是不是失敗
func contractShape(t *testing.T, tool string, res map[string]any) bool {
	t.Helper()
	sc, ok := res["structuredContent"].(map[string]any)
	if !ok || sc == nil {
		t.Errorf("%s:structuredContent 必須是 JSON 物件:%v", tool, res["structuredContent"])
		return false
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Errorf("%s:沒有 content", tool)
		return false
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil || !reflect.DeepEqual(parsed, any(sc)) {
		t.Errorf("%s:content[0].text 要是 structuredContent 的 JSON:%.200s", tool, text)
	}
	if isErr, _ := res["isError"].(bool); !isErr {
		return false
	}
	e, _ := sc["error"].(map[string]any)
	code, _ := e["code"].(string)
	msg, _ := e["message"].(string)
	status, _ := e["status"].(float64)
	if len(sc) != 1 || code == "" || msg == "" || status < 400 || status > 599 {
		t.Errorf("%s:失敗要是 {error:{code,message,status}}:%v", tool, sc)
	}
	return true
}

// console 的 MCP 與 web 層不自己組結果:結果欄位只在核心的 result.go 組裝
func TestConsoleResultsNotAssembledLocally(t *testing.T) {
	for _, dir := range []string{".", "../web"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, _ := os.ReadFile(f)
			for i, line := range strings.Split(string(b), "\n") {
				l := strings.TrimSpace(line)
				if strings.HasPrefix(l, "//") || strings.Contains(l, "`json:\"") {
					continue
				}
				if strings.Contains(l, `"structuredContent"`) || strings.Contains(l, `"isError"`) {
					t.Errorf("%s:%d 結果要用核心的 OKResult / ErrResult / FailureResult:%s", f, i+1, l)
				}
			}
		}
	}
}
