package mcp

import (
	_ "embed"
	"maps"

	"omixor-core/model"
)

// MCP Apps(ui:// 的 HTML 畫面)與唯讀授權。掛在管理端點(AdminEndpoint,/mcp/org/{org})。
//
// 畫面:ui/flowchart.html(flowchart_render 的 SVG)、ui/items.html(issue_items 的子項清單)、ui/decisions.html(issue_decisions 的待決定 / 需要協助,唯讀)。
// 每個都是「自包含」的單一 HTML:CSS、JS 全部內嵌,不載入任何外部來源;MCP Apps 的 View 端協定(postMessage 上的 JSON-RPC:
// ui/initialize → ui/notifications/initialized、接 ui/notifications/tool-result 與 host-context-changed、回報 size-changed)直接手寫,
// 不依賴 @modelcontextprotocol/ext-apps 套件。樣式與資料處理以 tools/mcp-apps-pilot/src/(view-common.js、diagram-view.js、items-view.js)與 build.js 的 css 為底,
// 主題用宿主給的 CSS 變數(--color-background-primary…),沒有就退回內建的淺色 / 深色值。這兩個檔是手寫、直接進版控的成品(Go 編譯不需要 node);
// 改它們就直接改 HTML(各檔的「COMMON」區塊要一致,測試 TestUIViewsCommonBlock 檢查)。

//go:embed ui/flowchart.html
var uiFlowchartHTML string

//go:embed ui/items.html
var uiItemsHTML string

//go:embed ui/decisions.html
var uiDecisionsHTML string

// MCP Apps 的 HTML 畫面的 MIME 型別
const UIMimeType = "text/html;profile=mcp-app"

// UI 資源網址
const (
	UIFlowchartURI = "ui://omixor/flowchart"
	UIItemsURI     = "ui://omixor/items"
	UIDecisionsURI = "ui://omixor/decisions"
)

// uiToolResource 宣告了 UI 的工具 → 畫面網址
var uiToolResource = map[string]string{
	"flowchart_render": UIFlowchartURI,
	"issue_items":      UIItemsURI,
	"issue_decisions":  UIDecisionsURI,
}

// UIResources 管理端點提供的 ui:// 資源
func UIResources() []Resource {
	meta := func() map[string]any { return map[string]any{"ui": map[string]any{"prefersBorder": true}} }
	return []Resource{
		{URI: UIFlowchartURI, Name: "flowchart", Title: "Flowchart view", Description: "Flowchart (SVG) from flowchart_render", MimeType: UIMimeType, Text: uiFlowchartHTML, Meta: meta()},
		{URI: UIItemsURI, Name: "items", Title: "Item list view", Description: "Item list from issue_items", MimeType: UIMimeType, Text: uiItemsHTML, Meta: meta()},
		{URI: UIDecisionsURI, Name: "decisions", Title: "Decisions view", Description: "Pending decisions and help requests from issue_decisions (read-only)", MimeType: UIMimeType, Text: uiDecisionsHTML, Meta: meta()},
	}
}

// WithUIMeta tools/list 的規格:flowchart_render、issue_items、issue_decisions 加上 _meta.ui.resourceUri(複製,不改原本的;另帶舊鍵 "ui/resourceUri" 給只認舊寫法的宿主)
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
const ReadOnlyMessage = "This connection is read-only"

// readOnlyGuard 唯讀授權的呼叫檢查:flowchart_render 只能「畫」,不能存文件(save)
func readOnlyGuard(name string, args map[string]any) error {
	if name == "flowchart_render" {
		if v, ok := args["save"]; ok && v != nil && v != false {
			return ToolError{Message: ReadOnlyMessage + ", so it cannot save to the cabinet (save)", Status: 403}
		}
	}
	return nil
}

// OAuthWriteTools OAuth 寫入授權(mcp:write)額外開放的協作寫入工具(明確的白名單,見 model.OAuthWriteTools):可用 = ReadOnlyTools ∪ OAuthWriteTools
var OAuthWriteTools = model.OAuthWriteTools

// OAuthNotAvailableMessage 有 mcp:write 的 OAuth 連線呼叫了白名單外的工具時的訊息(給 LLM 看,英文)
const OAuthNotAvailableMessage = "This tool is not available over OAuth. Use an omx_ user token for it."

// OAuthAdminTools OAuth 組織管理員授權(mcp:admin)再多開放的組織結構工具(見 model.OAuthAdminTools):可用 = ReadOnlyTools ∪ OAuthWriteTools ∪ OAuthAdminTools,
// 而且每次呼叫都要即時是該組織的管理員(統一入口的 CallCtx 檢查;service 層的授權表照舊)
var OAuthAdminTools = model.OAuthAdminTools

// 組織管理員工具被擋的訊息與機器碼(Data["code"];給 LLM 看,英文)
const (
	// OAuthAdminRequiredMessage 讀寫授權(沒有 mcp:admin)呼叫了組織管理員工具
	OAuthAdminRequiredMessage = "This tool requires the organization-admin grant (scope mcp:admin), but this OAuth connection was granted read-write only. Reconnect and choose the organization-admin option (you must be an admin of the organization), or use an omx_ user token."
	// OAuthCodeAdminScopeRequired 上面那種情況的 Data["code"]
	OAuthCodeAdminScopeRequired = "oauth_admin_scope_required"
	// OAuthCodeOrgAdminRequired 有 mcp:admin 授權,但呼叫者目前不是該組織的管理員(例如之後被降級或移出組織)
	OAuthCodeOrgAdminRequired = "org_admin_required"
)

// isReadOnlyBackend 這個連線是唯讀授權(OAuth 的 mcp:read)嗎(Backend 實作 ReadOnly() bool,例如 backend.Local)
func isReadOnlyBackend(bk any) bool {
	b, ok := bk.(interface{ ReadOnly() bool })
	return ok && b.ReadOnly()
}

// isOAuthWriteBackend 這個連線是有 mcp:write 的 OAuth 授權嗎(Backend 實作 OAuthWrite() bool,例如 backend.Local)
func isOAuthWriteBackend(bk any) bool {
	b, ok := bk.(interface{ OAuthWrite() bool })
	return ok && b.OAuthWrite()
}

// isOAuthAdminBackend 這個連線是有 mcp:admin 的 OAuth 授權嗎(Backend 實作 OAuthAdmin() bool,例如 backend.Local;隱含 OAuthWrite)
func isOAuthAdminBackend(bk any) bool {
	b, ok := bk.(interface{ OAuthAdmin() bool })
	return ok && b.OAuthAdmin()
}

// restrictOAuth 依連線的 OAuth scope 套上限制(AdminEndpoint 與 UnifiedEndpoint 共用,兩邊行為一致);不是 OAuth 連線(omx_)原樣回傳:
//   - 唯讀(mcp:read):restrictReadOnly
//   - 讀寫(mcp:write):restrictOAuthWrite
//   - 組織管理員(mcp:admin):只有統一入口(unified)才多開 OAuthAdminTools(restrictOAuthAdmin);組織端點(/mcp/org/{org})把它當讀寫
func restrictOAuth(h Hooks, bk any, unified bool) Hooks {
	switch {
	case isReadOnlyBackend(bk):
		return restrictReadOnly(h)
	case isOAuthAdminBackend(bk) && unified:
		return restrictOAuthAdmin(h)
	case isOAuthWriteBackend(bk):
		h = restrictOAuthWrite(h)
		if !unified { // 組織端點沒有 mcp:admin 這個選項:管理員工具對它就是「OAuth 不開放」,不提示重新授權
			h.Deny = func(string) string { return OAuthNotAvailableMessage }
			h.DenyCode = nil
		}
		return h
	}
	return h
}

// restrictOAuthAdmin 組織管理員授權(mcp:admin)的限制:開放 ReadOnlyTools ∪ OAuthWriteTools ∪ OAuthAdminTools(額外掛上來的工具也不開放);其他工具回 OAuthNotAvailableMessage。
// 「呼叫者是該呼叫 org 的管理員」的即時檢查在統一入口的 CallCtx(orgAdminCheck),這裡只決定哪些工具看得到、呼叫得到
func restrictOAuthAdmin(h Hooks) Hooks {
	orig := h.Allow
	h = restrictOAuthWrite(h)
	h.Allow = func(name string) bool {
		return (ReadOnlyTools[name] || OAuthWriteTools[name] || OAuthAdminTools[name]) && (orig == nil || orig(name))
	}
	return h
}

// restrictReadOnly 唯讀授權(OAuth)的限制:
// 只開放登記為 ReadOnly 的核心工具(額外掛上來的工具也不開放);被擋掉的工具回 ReadOnlyMessage;呼叫前再過 readOnlyGuard(例如 flowchart_render 不能 save)。
// 原本的 Allow / Guard 仍然先生效(兩者都通過才放行)
func restrictReadOnly(h Hooks) Hooks {
	allow, guard := h.Allow, h.Guard
	h.Allow = func(name string) bool { return ReadOnlyTools[name] && (allow == nil || allow(name)) }
	h.Guard = func(name string, args map[string]any) error {
		if guard != nil {
			if err := guard(name, args); err != nil {
				return err
			}
		}
		return readOnlyGuard(name, args)
	}
	h.Deny = func(string) string { return ReadOnlyMessage }
	return h
}

// restrictOAuthWrite 讀寫授權(mcp:write)的限制:開放 ReadOnlyTools ∪ OAuthWriteTools(額外掛上來的工具也不開放);其他工具回 OAuthNotAvailableMessage;
// readOnlyGuard 照舊套用(flowchart_render 仍不能 save:要存文件請用 doc_save)。原本的 Allow / Guard 仍然先生效。
// 身分的權限上限不在這裡:Principal.ServerAdmin 對這個身分是 false,寫入走 service 的授權表
func restrictOAuthWrite(h Hooks) Hooks {
	allow, guard := h.Allow, h.Guard
	h.Allow = func(name string) bool {
		return (ReadOnlyTools[name] || OAuthWriteTools[name]) && (allow == nil || allow(name))
	}
	h.Guard = func(name string, args map[string]any) error {
		if guard != nil {
			if err := guard(name, args); err != nil {
				return err
			}
		}
		return readOnlyGuard(name, args)
	}
	h.Deny = func(name string) string {
		if OAuthAdminTools[name] {
			return OAuthAdminRequiredMessage
		}
		return OAuthNotAvailableMessage
	}
	h.DenyCode = func(name string) string {
		if OAuthAdminTools[name] {
			return OAuthCodeAdminScopeRequired
		}
		return ""
	}
	return h
}
