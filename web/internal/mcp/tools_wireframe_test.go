package mcp

import (
	"strings"
	"testing"

	"omixor-web/internal/core"
)

// /mcp 也有 wireframe_render(spec/tools.json 沒有,由核心加上);存文件走 web 的文件櫃,畫面用 wireframe_svg 動作畫圖
func TestWireframeToolAndAction(t *testing.T) {
	a := iaApp(t)
	spec := `{"device": "mobile", "root": {"type": "col", "children": [{"type": "header", "title": "登入"}, {"type": "button", "text": "送出"}]}}`
	out := iaRun(t, a, core.MCPActor, "wireframe_render", map[string]any{"spec": spec, "save": map[string]any{"section": "overview", "title": "登入草稿"}})
	if out["width"].(float64) != 390 || !strings.HasPrefix(out["svg"].(string), "<svg") || out["doc"] == nil {
		t.Fatal(out)
	}
	id := out["doc"].(map[string]any)["id"].(string)
	a.Mu.Lock()
	body := a.FindDoc(id).Body
	a.Mu.Unlock()
	if !strings.HasPrefix(body, "```wireframe\n{") {
		t.Fatalf("%q", body)
	}
	r, err := core.Action(a, "wireframe_svg", map[string]any{"spec": body})
	if m := r.(map[string]any); err != nil || !strings.Contains(m["svg"].(string), "送出") {
		t.Fatal(r, err)
	}
	r, _ = core.Action(a, "wireframe_svg", map[string]any{"spec": "{"})
	if m := r.(map[string]any); !strings.Contains(m["error"].(string), "不是有效的 JSON") {
		t.Fatal(r)
	}
}
