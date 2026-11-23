package mcp

import (
	_ "embed"
	"maps"

	"omixor-core/model"
)

// MCP Apps(ui:// 的 HTML 畫面)與唯讀授權。掛在管理端點(AdminEndpoint,/mcp/org/{org})。
//
// 畫面:ui/flowchart.html(flowchart_render 的 SVG)、ui/items.html(issue_items 的子項清單)。
// 每個都是「自包含」的單一 HTML:CSS、JS 全部內嵌,不載入任何外部來源;MCP Apps 的 View 端協定(postMessage 上的 JSON-RPC:
// ui/initialize → ui/notifications/initialized、接 ui/notifications/tool-result 與 host-context-changed、回報 size-changed)直接手寫,
// 不依賴 @modelcontextprotocol/ext-apps 套件。樣式與資料處理以 tools/mcp-apps-pilot/src/(view-common.js、diagram-view.js、items-view.js)與 build.js 的 css 為底,
// 主題用宿主給的 CSS 變數(--color-background-primary…),沒有就退回內建的淺色 / 深色值。這兩個檔是手寫、直接進版控的成品(Go 編譯不需要 node);
// 改它們就直接改 HTML(兩個檔的「COMMON」區塊要一致,測試 TestUIViewsCommonBlock 檢查)。

//go:embed ui/flowchart.html
var uiFlowchartHTML string

//go:embed ui/items.html
var uiItemsHTML string

// MCP Apps 的 HTML 畫面的 MIME 型別
const UIMimeType = "text/html;profile=mcp-app"

// UI 資源網址
const (
	UIFlowchartURI = "ui://omixor/flowchart"
	UIItemsURI     = "ui://omixor/items"
)

// uiToolResource 宣告了 UI 的工具 → 畫面網址
var uiToolResource = map[string]string{
	"flowchart_render": UIFlowchartURI,
	"issue_items":      UIItemsURI,
}

// UIResources 管理端點提供的 ui:// 資源
func UIResources() []Resource {
	meta := func() map[string]any { return map[string]any{"ui": map[string]any{"prefersBorder": true}} }
	return []Resource{
		{URI: UIFlowchartURI, Name: "flowchart", Title: "流程圖畫面", Description: "flowchart_render 的流程圖(SVG)", MimeType: UIMimeType, Text: uiFlowchartHTML, Meta: meta()},
		{URI: UIItemsURI, Name: "items", Title: "子項清單畫面", Description: "issue_items 的子項清單", MimeType: UIMimeType, Text: uiItemsHTML, Meta: meta()},
	}
}

// WithUIMeta tools/list 的規格:flowchart_render、issue_items 加上 _meta.ui.resourceUri(複製,不改原本的;另帶舊鍵 "ui/resourceUri" 給只認舊寫法的宿主)
func WithUIMeta(defs []any) []any {
	out := make([]any, 0, len(defs))
	for _, d := range defs {
		m, ok := d.(map[string]any)
		name, _ := m["name"].(string)
		uri := uiToolResource[name]
		if !ok || uri == "" {
			out = append(out, d)
			continue
		}
		cp := map[string]any{}
		maps.Copy(cp, m)
		cp["_meta"] = map[string]any{"ui": map[string]any{"resourceUri": uri}, "ui/resourceUri": uri}
		out = append(out, cp)
	}
	return out
}

// ReadOnlyTools 唯讀授權(OAuth mcp:read)可用的核心工具(登記表 model.CoreTools 的 ReadOnly)
var ReadOnlyTools = model.CoreToolSet(func(t model.CoreTool) bool { return t.ReadOnly })

// ReadOnlyMessage 唯讀授權呼叫了不開放的工具時的訊息
const ReadOnlyMessage = "此連線是唯讀授權"

// readOnlyGuard 唯讀授權的呼叫檢查:flowchart_render 只能「畫」,不能存文件(save)
func readOnlyGuard(name string, args map[string]any) error {
	if name == "flowchart_render" {
		if v, ok := args["save"]; ok && v != nil && v != false {
			return ToolError{Message: ReadOnlyMessage + ",不能存到文件櫃(save)", Status: 403}
		}
	}
	return nil
}
