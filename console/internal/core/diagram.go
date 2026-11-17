package core

import (
	"strings"

	"omixor-core/tools/flowchart"
	"omixor-core/tools/wireframe"
)

// 文件櫃顯示圖表區塊用(```wireframe、```flowchart、flowchart 語法的 ```mermaid):畫面把區塊內容送來,
// 這裡用核心的渲染器畫成 SVG 回傳。渲染是純函式,本機與遠端模式都在這台 omixor-web 上算,不用打雲端。
// 畫面一律用 <img src="data:image/svg+xml,…"> 顯示(img 裡的 SVG 不會執行腳本),不要直接塞進 DOM。
// 描述有錯時回 {error},不當成 HTTP 錯誤(畫面在圖的位置顯示錯誤訊息)。
//
// diagram_svg 參數:kind(wireframe | flowchart;mermaid 視同 flowchart)、spec(區塊內容)。
// 舊的 wireframe_svg(只有 spec)保留相容。

func init() {
	RegisterAction("diagram_svg", func(_ *App, args map[string]any) (any, error) {
		kind, _ := args["kind"].(string)
		return renderDiagram(kind, args["spec"]), nil
	})
}

func renderDiagram(kind string, spec any) map[string]any {
	var svg string
	var w, h int
	var warns []string
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "wireframe":
		r, err := wireframe.Render(spec)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		svg, w, h, warns = r.SVG, r.Width, r.Height, r.Warnings
	case "flowchart", "mermaid":
		r, err := flowchart.Render(spec)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		svg, w, h, warns = r.SVG, r.Width, r.Height, r.Warnings
	default:
		return map[string]any{"error": "不支援的圖表種類「" + kind + "」(只有 wireframe、flowchart)"}
	}
	return map[string]any{"svg": svg, "width": w, "height": h, "warnings": warns}
}
