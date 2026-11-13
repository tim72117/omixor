package mcp

import (
	"slices"

	"omixor-core/tools/wireframe"
)

// 自建工具外掛的試作:wireframe_render 用元件與版面描述畫出線框圖(SVG),可順便存到文件櫃。
// 渲染是純函式(omixor-core/tools/wireframe),存文件走同一個 Server 上的 docs / doc_save 工具,
// 所以文件權限(角色的 docAccess、角色端點強制的憑證角色)和直接呼叫 doc_save 完全一樣。
//
// 文件內文的格式:一段 ```wireframe 程式碼區塊放 JSON 描述(可再編輯的資料來源),畫面顯示時再渲染成圖。

const wireframeDesc = "用元件與版面描述(JSON)畫出 UI/UX 畫面的線框圖,回傳 SVG。可以順便存到文件櫃(save):文件內文是一段 ```wireframe 程式碼區塊放 JSON 描述,文件櫃顯示時畫成圖,之後改 JSON 就能改圖。" +
	"描述:{title?, device: desktop|tablet|mobile|{w,h}, root: <node>};node 是 {type, children?, 屬性…},children 裡可以直接放字串(= text)。" +
	"版面:col(直排)、row(橫排)、stack(疊放,子節點可設 place: top-left|center|bottom-right…)、grid(columns 欄,子節點 span)、card(有框,title)、section(title)。" +
	"版面屬性:gap、padding(數字或 [上下,左右])、width/height(數字、\"fill\" 平分剩餘、\"50%\")、align(start|center|end|stretch)、justify(start|center|end|between)、border、fill。" +
	"元件:header/navbar(title、items、logo、back、menu、avatar)、footer、sidebar(title、items、active)、heading(text、level 1~3)、text(text、size、muted、bold;沒給 text 畫佔位線)、link、" +
	"button(text、variant primary|secondary|ghost、size sm|md|lg)、input(label、placeholder、value、kind: password)、textarea、select、checkbox / radio(label、checked)、toggle(label、on)、" +
	"image(打叉框,label、ratio)、avatar(size、label)、icon、list(items 或 count;avatar、chevron)、table(columns、rows 列數或 [[…]])、tabs(items、active)、chart(kind bar|line|pie、title)、badge、divider、spacer(size;沒給就撐開)、modal(疊層:title、width、children)。" +
	"不認得的 type 畫成虛線框並列在 warnings。內容超出裝置高度時畫布自動加高。" +
	"範例:{\"device\":\"mobile\",\"root\":{\"type\":\"col\",\"children\":[{\"type\":\"header\",\"title\":\"登入\"},{\"type\":\"col\",\"padding\":24,\"gap\":16,\"children\":[{\"type\":\"input\",\"label\":\"Email\"},{\"type\":\"input\",\"label\":\"密碼\",\"kind\":\"password\"},{\"type\":\"button\",\"text\":\"登入\",\"width\":\"fill\"}]}]}}。" +
	"不給 spec 而給 doc 時,讀那份文件的第一個 ```wireframe 區塊重畫。以角色身分工作時帶 agent;存文件時只能寫到該角色可寫的位置。"

// WireframeToolDefs 線框圖工具的規格
func WireframeToolDefs() []any { return []any{wireframeDef()} }

func wireframeDef() map[string]any {
	return coreDef("wireframe_render", wireframeDesc, false, obj(map[string]any{
		"spec": map[string]any{
			"description": "線框圖描述:物件,或 JSON 字串({title?, device, root})",
			"anyOf":       []any{map[string]any{"type": "object"}, map[string]any{"type": "string"}},
		},
		"doc":   pStr("文件 id:不給 spec 時讀這份文件裡第一個 ```wireframe 區塊重畫"),
		"agent": pStr("以虛擬角色身分工作時填角色 id 或名稱(讀寫文件依該角色的文件權限)"),
		"svg":   pBool("回傳 SVG 原始碼,預設 true;只想存文件時可設 false 省上下文"),
		"save": map[string]any{
			"type":        "object",
			"description": "存到文件櫃:不給 id 新增一份(section 或 tab、title 必填,note 是區塊前面的說明文字);給 id 就更新那份文件的第一個 ```wireframe 區塊(沒有就接在最後),其他內文不動",
			"properties": map[string]any{
				"id":      pStr("要更新的文件 id"),
				"section": pStr("區塊 id 或名稱(新增時)"),
				"tab":     pStr("頁籤網址或名稱(新增時)"),
				"title":   pStr("標題(新增時必填;更新時給了就改標題)"),
				"note":    pStr("新增時放在圖前面的說明(Markdown)"),
				"tags":    pStrs("標籤"),
			},
			"additionalProperties": false,
		},
	}))
}

// RegisterWireframeTools 登記 wireframe_render。規格裡沒有(例如 omixor-web 的 /mcp 用 Swift 匯出的規格)時連規格一起加上。
// 執行環境(Call.Ctx)只有存文件、讀文件時才用到(交給同一個 Server 的 docs、doc_save)。
func RegisterWireframeTools(s *Server) {
	fn := func(c *Call) (any, error) { return wireframeRender(s, c) }
	if slices.Contains(s.ToolNames(), "wireframe_render") {
		s.Register("wireframe_render", fn)
		return
	}
	s.Add(Tool{Def: wireframeDef(), Fn: fn})
}

func wireframeRender(s *Server, c *Call) (any, error) {
	return renderDiagram(s, c, diagramKind{
		noun: "線框圖", lang: wireframe.Lang,
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
