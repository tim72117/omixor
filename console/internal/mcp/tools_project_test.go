package mcp

import (
	"testing"

	"omixor-web/internal/core"
)

// 專案:核心的 MCP 工具(projects、project_save,文件櫃工具帶 project)透過 appBackend 交給 App.CabinetOps
func TestProjectTools(t *testing.T) {
	a := iaApp(t)
	p := iaRun(t, a, core.MCPActor, "project_save", map[string]any{"name": "App"})
	if p["id"] != "PRJ-1" {
		t.Fatal(p)
	}
	iaRun(t, a, core.MCPActor, "cabinet_layout_save", map[string]any{"project": "PRJ-1",
		"add_sections": []any{map[string]any{"id": "spec", "label": "規格"}}})
	d := iaRun(t, a, core.MCPActor, "doc_save", map[string]any{"project": "PRJ-1", "section": "spec", "title": "規格書"})
	id, _ := d["id"].(string)
	c := iaRun(t, a, core.MCPActor, "cabinet", map[string]any{"project": "PRJ-1"})
	if c == nil {
		t.Fatal("cabinet 沒有回傳")
	}
	l := iaRun(t, a, core.MCPActor, "projects", map[string]any{})
	ps := l["projects"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["docs"].(float64) != 1 {
		t.Fatal(l)
	}
	// 搬回未分專案(目標結構要有位置:給 section)
	m := iaRun(t, a, core.MCPActor, "doc_save", map[string]any{"id": id, "project": "", "section": "overview"})
	if m["project"] != nil {
		t.Fatal(m)
	}
	a.Mu.Lock()
	moved := a.FindDoc(id)
	a.Mu.Unlock()
	if moved == nil || moved.Project != "" || moved.Section != "overview" {
		t.Fatal(moved)
	}
	iaRun(t, a, core.MCPActor, "project_save", map[string]any{"id": "PRJ-1", "name": "App 2"})
}
