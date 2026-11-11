package mcp

import (
	"testing"

	"opsweb/internal/core"
)

func TestCabinetToolsAccess(t *testing.T) {
	a := iaApp(t)
	d := iaRun(t, a, core.MCPActor, "ops_doc_save", map[string]any{"title": "L0", "tab": "L0", "body": "你好"})
	if d["location"] != "課程 › L0 AI 工具介紹" || d["chars"].(float64) != 2 || d["author"] != "MCP 用戶端" {
		t.Fatal(d)
	}
	iaErr(t, a, "ops_doc_save", map[string]any{"title": "x", "section": "levels", "agent": "narrator"},
		"旁白製作沒有權限寫入「課程」;它可以寫的範圍:")
	iaErr(t, a, "ops_doc_save", map[string]any{"title": "x", "section": "docs", "agent": "marketing"},
		"行銷沒有權限寫入「文件」;它可以寫的範圍:軟體與課程行銷手法")
	iaErr(t, a, "ops_docs", map[string]any{"id": "DOC-1", "agent": "triager"}, "Issue 整理員沒有權限讀「課程 › L0 AI 工具介紹」的文件")
	d = iaRun(t, a, core.MCPActor, "ops_docs", map[string]any{"id": "doc-1", "agent": "narrator"})
	if d["body"] != "你好" {
		t.Fatal(d)
	}
	d = iaRun(t, a, core.MCPActor, "ops_doc_save", map[string]any{"title": "寫", "tab": "L2", "agent": "course-dev"})
	if d["author"] != "課程開發" {
		t.Fatal(d)
	}
	c := iaRun(t, a, core.MCPActor, "ops_cabinet", map[string]any{"agent": "triager"})
	secs := c["sections"].([]any)
	if c["organization"] != "evoleap" || len(secs) != 1 || secs[0].(map[string]any)["access"] != "write" {
		t.Fatal(c)
	}
	// 畫面新增的文件署名使用者
	iaRun(t, a, core.Person("tim"), "ops_doc_save", map[string]any{"title": "人寫的", "section": "overview"})
	a.Mu.Lock()
	by := a.FindDoc("DOC-3").Author
	a.Mu.Unlock()
	if by != "tim" {
		t.Fatal(by)
	}
}
