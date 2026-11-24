package mcp

import (
	"omixor-core/tools/wireframe"
)

// 自建工具外掛的試作:wireframe_render 用元件與版面描述畫出線框圖(SVG),可順便存到文件櫃。
// 渲染是純函式(omixor-core/tools/wireframe),存文件走同一個 Server 上的 docs / doc_save 工具,
// 所以文件權限(角色的 docAccess、角色端點強制的憑證角色)和直接呼叫 doc_save 完全一樣。
//
// 文件內文的格式:一段 ```wireframe 程式碼區塊放 JSON 描述(可再編輯的資料來源),畫面顯示時再渲染成圖。

const wireframeDesc = "Draws a wireframe of a UI/UX screen from a component and layout description (JSON) and returns SVG. It can also save to the cabinet (save): the document body is a ```wireframe code block holding the JSON description, and the cabinet renders it as a diagram; edit the JSON later to change the diagram. " +
	"Description: {title?, device: desktop|tablet|mobile|{w,h}, root: <node>}; a node is {type, children?, attributes...}, and children may hold plain strings directly (= text). " +
	"Layouts: col (vertical), row (horizontal), stack (overlay; a child may set place: top-left|center|bottom-right...), grid (columns, children use span), card (bordered, title), section (title). " +
	"Layout attributes: gap, padding (a number or [vertical,horizontal]), width/height (a number, \"fill\" to share the remaining space, \"50%\"), align (start|center|end|stretch), justify (start|center|end|between), border, fill. " +
	"Components: header/navbar (title, items, logo, back, menu, avatar), footer, sidebar (title, items, active), heading (text, level 1~3), text (text, size, muted, bold; without text a placeholder line is drawn), link, " +
	"button (text, variant primary|secondary|ghost, size sm|md|lg), input (label, placeholder, value, kind: password), textarea, select, checkbox / radio (label, checked), toggle (label, on), " +
	"image (a box with a cross, label, ratio), avatar (size, label), icon, list (items or count; avatar, chevron), table (columns, rows as a row count or [[...]]), tabs (items, active), chart (kind bar|line|pie, title), badge, divider, spacer (size; without it, it expands to fill), modal (overlay: title, width, children). " +
	"An unrecognized type is drawn as a dashed box and listed in warnings. When content exceeds the device height, the canvas grows automatically. " +
	"Example: {\"device\":\"mobile\",\"root\":{\"type\":\"col\",\"children\":[{\"type\":\"header\",\"title\":\"Log in\"},{\"type\":\"col\",\"padding\":24,\"gap\":16,\"children\":[{\"type\":\"input\",\"label\":\"Email\"},{\"type\":\"input\",\"label\":\"Password\",\"kind\":\"password\"},{\"type\":\"button\",\"text\":\"Log in\",\"width\":\"fill\"}]}]}}. " +
	"When doc is given without spec, the first ```wireframe block of that document is read and redrawn. When saving a document, the caller's document permissions apply (on the agent endpoint, the credential's agent)."

// WireframeToolDefs 線框圖工具的規格
func WireframeToolDefs() []any { return []any{wireframeDef()} }

func wireframeDef() map[string]any {
	return coreDef("wireframe_render", wireframeDesc, false, obj(map[string]any{
		"spec": map[string]any{
			"description": "Wireframe description: an object, or a JSON string ({title?, device, root})",
			"anyOf":       []any{map[string]any{"type": "object"}, map[string]any{"type": "string"}},
		},
		"doc": pStr("Document id: when spec is not given, the first ```wireframe block of this document is read and redrawn"),
		"svg": pBool("Return the SVG source, default true; set false to save context when you only want to save the document"),
		"save": map[string]any{
			"type":        "object",
			"description": "Saves to the cabinet: without id, creates a new document (section or tab and title are required; note is explanatory text placed before the block); with id, updates the first ```wireframe block of that document (appended at the end if there is none) and leaves the rest of the body untouched",
			"properties": map[string]any{
				"id":      pStr("Id of the document to update"),
				"section": pStr("Section id or name (when creating)"),
				"tab":     pStr("Tab URL or name (when creating)"),
				"title":   pStr("Title (required when creating; when given on update, the title is changed)"),
				"note":    pStr("Explanation placed before the diagram when creating (Markdown)"),
				"tags":    pStrs("Tags"),
			},
			"additionalProperties": false,
		},
	}))
}

// RegisterWireframeTools 登記 wireframe_render。規格裡沒有(例如 omixor-web 的 /mcp 用 Swift 匯出的規格)時連規格一起加上。
// 執行環境(Call.Ctx)只有存文件、讀文件時才用到(交給同一個 Server 的 docs、doc_save)。
func RegisterWireframeTools(s *Server) {
	fn := func(c *Call) (any, error) { return wireframeRender(s, c) }
	if s.InSpec("wireframe_render") {
		s.Register("wireframe_render", fn)
		return
	}
	s.Add(Tool{Def: wireframeDef(), Fn: fn})
}

func wireframeRender(s *Server, c *Call) (any, error) {
	return renderDiagram(s, c, diagramKind{
		noun: "wireframe", lang: wireframe.Lang,
		render: func(spec any) (int, int, []string, string, error) {
			r, err := wireframe.Render(spec)
			if err != nil {
				return 0, 0, nil, "", err
			}
			return r.Width, r.Height, r.Warnings, r.SVG, nil
		},
		blocks: wireframe.Blocks, upsert: wireframe.Upsert, fence: wireframe.Fence, source: wireframe.Pretty,
		first: func(md string) (string, string, bool) {
			b, ok := wireframe.FirstBlock(md)
			return b, wireframe.Lang, ok
		},
		fenceLike: func(_, src string) string { return wireframe.Fence(src) },
		others:    flowchartBlocks,
	})
}
