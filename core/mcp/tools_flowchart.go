package mcp

import (
	"slices"

	"omixor-core/tools/flowchart"
)

// flowchart_render:把流程描述(JSON 或 Mermaid flowchart)自動排版畫成流程圖(SVG),可順便存到文件櫃。
// 排版與渲染是純函式(omixor-core/tools/flowchart);讀寫文件和 wireframe_render 共用 renderDiagram(tools_diagram.go)。
//
// 文件內文的格式:一段 ```flowchart 程式碼區塊放原始描述(JSON 或 Mermaid 原文,渲染時自動判斷),畫面顯示時再畫成圖。
// 文件裡 flowchart 語法的 ```mermaid 區塊也算流程圖區塊(doc 重畫、save 更新都會用到)。

const flowchartDesc = "畫流程圖:給節點與連線,自動分層排版(有環時回頭的線從側邊繞)、正交折線、箭頭與標籤,回傳 SVG。可以順便存到文件櫃(save):文件內文是一段 ```flowchart 區塊放原始描述,文件櫃顯示時畫成圖。" +
	"spec 兩種格式擇一:" +
	"(1) Mermaid flowchart 文字(建議):第一行 flowchart TD|LR|BT|RL(或 graph);節點 A[處理]、A([開始/結束])、A{判斷}、A[(資料庫)]、A[/輸入輸出/]、A[[子流程]]、A(圓角)、A((圓))、A{{準備}};" +
	"連線 -->、---(無箭頭)、-.->(虛線)、==>(粗線)、<-->(雙向)、-->|標籤|、-- 標籤 -->;可串接 A --> B --> C、A & B --> C;subgraph id [標題] … end 畫群組框;%% 註解;A:::highlight 或 class A highlight 強調。" +
	"未宣告的節點自動建立。classDef、style、click、linkStyle 會忽略並列在 warnings。" +
	"(2) JSON:{title?, direction: TB|LR|BT|RL, nodes: [{id, label, shape?: process|decision|start|end|io|subprocess|database|note|round|circle|hexagon, group?, style?: highlight|muted}], " +
	"edges: [{from, to, label?, style?: solid|dashed|thick, arrow?: end|none|both}], groups?: [{id, label, parent?}]}。" +
	"上限:節點 300、邊 600、描述 256KB。" +
	"範例:flowchart TD\\n  A([開始]) --> B[/輸入帳密/] --> C{驗證通過?}\\n  C -->|是| D([進入首頁])\\n  C -->|否| E[失敗次數+1] --> B。" +
	"不給 spec 而給 doc 時,讀那份文件的第一個 ```flowchart(或 flowchart 語法的 ```mermaid)區塊重畫。以角色身分工作時帶 agent;存文件時只能寫到該角色可寫的位置。"

// FlowchartToolDefs 流程圖工具的規格
func FlowchartToolDefs() []any { return []any{flowchartDef()} }

func flowchartDef() map[string]any {
	return coreDef("flowchart_render", flowchartDesc, false, obj(map[string]any{
		"spec": map[string]any{
			"description": "流程圖描述:Mermaid flowchart 文字,或 JSON(物件或字串 {direction, nodes, edges, groups?});外面包了 ```flowchart / ```mermaid 區塊也可以",
			"anyOf":       []any{map[string]any{"type": "object"}, map[string]any{"type": "string"}},
		},
		"doc":   pStr("文件 id:不給 spec 時讀這份文件裡第一個 ```flowchart(或 flowchart 語法的 ```mermaid)區塊重畫"),
		"agent": pStr("以虛擬角色身分工作時填角色 id 或名稱(讀寫文件依該角色的文件權限)"),
		"svg":   pBool("回傳 SVG 原始碼,預設 true;只想存文件時可設 false 省上下文"),
		"save":  diagramSaveParam(flowchart.Lang),
	}))
}

// RegisterFlowchartTools 登記 flowchart_render。規格裡沒有(例如 omixor-web 的 /mcp 用 Swift 匯出的規格)時連規格一起加上。
func RegisterFlowchartTools(s *Server) {
	fn := func(c *Call) (any, error) { return flowchartRender(s, c) }
	if slices.Contains(s.ToolNames(), "flowchart_render") {
		s.Register("flowchart_render", fn)
		return
	}
	s.Add(Tool{Def: flowchartDef(), Fn: fn})
}

func flowchartRender(s *Server, c *Call) (any, error) {
	return renderDiagram(s, c, diagramKind{
		noun: "流程圖", lang: flowchart.Lang,
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
