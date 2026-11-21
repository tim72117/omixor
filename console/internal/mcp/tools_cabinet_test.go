package mcp

import (
	"testing"

	"omixor-web/internal/core"
)

// 文件署名一律是呼叫者(MCP 用戶端或畫面的使用者);不能以角色身分讀寫(agent 參數見 identity_test.go)
func TestCabinetToolsAuthors(t *testing.T) {
	a := iaApp(t)
	d := iaRun(t, a, core.MCPActor, "doc_save", map[string]any{"title": "L0", "tab": "L0", "body": "你好"})
	if d["location"] != "課程 › L0 AI 工具介紹" || d["chars"].(float64) != 2 || d["author"] != core.LocalIdentity {
		t.Fatal(d)
	}
	d = iaRun(t, a, core.MCPActor, "docs", map[string]any{"id": "doc-1"})
	if d["body"] != "你好" {
		t.Fatal(d)
	}
	c := iaRun(t, a, core.MCPActor, "cabinet", map[string]any{})
	if c["organization"] != "evoleap" || len(c["sections"].([]any)) < 2 {
		t.Fatal(c)
	}
	iaRun(t, a, core.Person("tim"), "doc_save", map[string]any{"title": "人寫的", "section": "overview"})
	a.Mu.Lock()
	by := a.FindDoc("DOC-2").Author
	a.Mu.Unlock()
	if by != core.LocalIdentity {
		t.Fatal(by)
	}
}
