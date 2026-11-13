package mcp

import (
	"strings"
	"testing"

	"omixor-web/internal/core"
)

// /mcp 也有 flowchart_render(spec/tools.json 沒有,由核心加上);存文件走 web 的文件櫃,畫面用 diagram_svg 動作畫圖
func TestFlowchartToolAndAction(t *testing.T) {
	a := iaApp(t)
	spec := "flowchart TD\n  A([開始]) --> B{通過?}\n  B -->|是| C[完成]\n  B -->|否| A"
	out := iaRun(t, a, core.MCPActor, "flowchart_render", map[string]any{"spec": spec, "save": map[string]any{"section": "overview", "title": "審核流程"}})
	if !strings.HasPrefix(out["svg"].(string), "<svg") || out["doc"] == nil {
		t.Fatal(out)
	}
	id := out["doc"].(map[string]any)["id"].(string)
	a.Mu.Lock()
	body := a.FindDoc(id).Body
	a.Mu.Unlock()
	if body != "```flowchart\n"+spec+"\n```\n" {
		t.Fatalf("%q", body)
	}
	for _, kind := range []string{"flowchart", "mermaid"} {
		r, err := core.Action(a, "diagram_svg", map[string]any{"kind": kind, "spec": body})
		if m := r.(map[string]any); err != nil || !strings.Contains(m["svg"].(string), "通過?") {
			t.Fatal(r, err)
		}
	}
	r, _ := core.Action(a, "diagram_svg", map[string]any{"kind": "wireframe", "spec": `{"device": "mobile", "root": {"type": "button", "text": "送出"}}`})
	if m := r.(map[string]any); !strings.Contains(m["svg"].(string), "送出") {
		t.Fatal(r)
	}
	r, _ = core.Action(a, "diagram_svg", map[string]any{"kind": "flowchart", "spec": "sequenceDiagram\n  A->>B: x"})
	if m := r.(map[string]any); !strings.Contains(m["error"].(string), "只支援 Mermaid 的 flowchart") {
		t.Fatal(r)
	}
	r, _ = core.Action(a, "diagram_svg", map[string]any{"kind": "pie", "spec": "x"})
	if m := r.(map[string]any); !strings.Contains(m["error"].(string), "不支援的圖表種類") {
		t.Fatal(r)
	}
}
