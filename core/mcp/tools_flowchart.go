package mcp

import (
	"omixor-core/tools/flowchart"
)

// flowchart_render:把流程描述(JSON 或 Mermaid flowchart)自動排版畫成流程圖(SVG),可順便存到文件櫃。
// 排版與渲染是純函式(omixor-core/tools/flowchart);讀寫文件和 wireframe_render 共用 renderDiagram(tools_diagram.go)。
//
// 文件內文的格式:一段 ```flowchart 程式碼區塊放原始描述(JSON 或 Mermaid 原文,渲染時自動判斷),畫面顯示時再畫成圖。
// 文件裡 flowchart 語法的 ```mermaid 區塊也算流程圖區塊(doc 重畫、save 更新都會用到)。

const flowchartDesc = "Draws a flowchart: give nodes and edges and it lays them out in layers automatically (back edges of a cycle are routed around the side), with orthogonal polylines, arrows and labels, and returns SVG. It can also save to the cabinet (save): the document body is a ```flowchart block holding the original description, and the cabinet renders it as a diagram. " +
	"spec takes one of two formats: " +
	"(1) Mermaid flowchart text (recommended): the first line is flowchart TD|LR|BT|RL (or graph); nodes A[process], A([start/end]), A{decision}, A[(database)], A[/input-output/], A[[subprocess]], A(rounded), A((circle)), A{{prepare}}; " +
	"edges -->, --- (no arrow), -.-> (dashed), ==> (thick), <--> (bidirectional), -->|label|, -- label -->; edges can be chained: A --> B --> C, A & B --> C; subgraph id [title] ... end draws a group box; %% comment; A:::highlight or class A highlight emphasizes a node. " +
	"Undeclared nodes are created automatically. classDef, style, click and linkStyle are ignored and listed in warnings. " +
	"(2) JSON: {title?, direction: TB|LR|BT|RL, nodes: [{id, label, shape?: process|decision|start|end|io|subprocess|database|note|round|circle|hexagon, group?, style?: highlight|muted}], " +
	"edges: [{from, to, label?, style?: solid|dashed|thick, arrow?: end|none|both}], groups?: [{id, label, parent?}]}. " +
	"Limits: 300 nodes, 600 edges, 256KB description. " +
	"Example: flowchart TD\\n  A([Start]) --> B[/Enter credentials/] --> C{Verified?}\\n  C -->|Yes| D([Go to home page])\\n  C -->|No| E[Failure count +1] --> B. " +
	"When doc is given without spec, the first ```flowchart block (or ```mermaid block in flowchart syntax) of that document is read and redrawn. When saving a document, the caller's document permissions apply (on the agent endpoint, the credential's agent)."

// FlowchartToolDefs 流程圖工具的規格
func FlowchartToolDefs() []any { return []any{flowchartDef()} }

func flowchartDef() map[string]any {
	return coreDef("flowchart_render", flowchartDesc, false, obj(map[string]any{
		"spec": map[string]any{
			"description": "Flowchart description: Mermaid flowchart text, or JSON (an object or a string {direction, nodes, edges, groups?}); it may also be wrapped in a ```flowchart / ```mermaid block",
			"anyOf":       []any{map[string]any{"type": "object"}, map[string]any{"type": "string"}},
		},
		"doc":  pStr("Document id: when spec is not given, the first ```flowchart block (or ```mermaid block in flowchart syntax) of this document is read and redrawn"),
		"svg":  pBool("Return the SVG source, default true; set false to save context when you only want to save the document"),
		"save": diagramSaveParam(flowchart.Lang),
	}))
}

// RegisterFlowchartTools 登記 flowchart_render。規格裡沒有(例如 omixor-web 的 /mcp 用 Swift 匯出的規格)時連規格一起加上。
func RegisterFlowchartTools(s *Server) {
	fn := func(c *Call) (any, error) { return flowchartRender(s, c) }
	if s.InSpec("flowchart_render") {
		s.Register("flowchart_render", fn)
		return
	}
	s.Add(Tool{Def: flowchartDef(), Fn: fn})
}

func flowchartRender(s *Server, c *Call) (any, error) {
	return renderDiagram(s, c, diagramKind{
		noun: "flowchart", lang: flowchart.Lang,
		render: func(spec any) (int, int, []string, string, error) {
			r, err := flowchart.Render(spec)
			if err != nil {
				return 0, 0, nil, "", err
			}
			return r.Width, r.Height, r.Warnings, r.SVG, nil
		},
		blocks: flowchart.Blocks, upsert: flowchart.Upsert, fence: flowchart.Fence, source: flowchart.Source,
		first: flowchart.FirstBlock, fenceLike: flowchart.FenceLike, others: wireframeBlocks,
	})
}
