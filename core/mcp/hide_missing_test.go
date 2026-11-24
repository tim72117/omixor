package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// HideMissing:規格有但沒實作的工具(選用模組沒編進來)不列在 tools/list,呼叫回「這台伺服器沒有編進這個工具」
func TestHideMissingTools(t *testing.T) {
	tools := `[{"name":"a","inputSchema":{"type":"object"}},{"name":"b","inputSchema":{"type":"object"}}]`
	s, err := NewServer([]byte(tools), []byte(`{"serverInfo":{"name":"x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	s.Register("a", func(c *Call) (any, error) { return "ok", nil })
	if len(s.ToolNames()) != 2 {
		t.Fatal("沒開 HideMissing 時照規格列", s.ToolNames())
	}
	s.HideMissing = true
	if n := s.ToolNames(); len(n) != 1 || n[0] != "a" || len(s.ToolDefs()) != 1 || !s.InSpec("b") {
		t.Fatal(n)
	}
	out := s.Handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), Hooks{})
	if strings.Contains(string(out), `"b"`) {
		t.Fatalf("tools/list 不該有 b:%s", out)
	}
	out = s.Handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"b"}}`), Hooks{})
	var r struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct{ Text string }
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) != nil || !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "built without") {
		t.Fatalf("%s", out)
	}
}
