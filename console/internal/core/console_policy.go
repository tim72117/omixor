package core

import "strings"

// 畫面 HTTP API 和 OAuth MCP 入口的專有工具(mcp/oauth_tools.go)共用的規則,兩邊才不會一寬一嚴。

// SetConsoleAdmins 由 web 層告訴 App 誰是網頁管理員(認證設定的管理員帳號名單;每次呼叫都重查)
func (a *App) SetConsoleAdmins(f func(account string) bool) { a.root().consoleAdmins.Store(&f) }

// ConsoleAdmins 見 SetConsoleAdmins;ok = false 表示 web 層還沒接上(要當作什麼都不能用)
func (a *App) ConsoleAdmins() (isAdmin func(account string) bool, ok bool) {
	f := a.root().consoleAdmins.Load()
	if f == nil {
		return nil, false
	}
	return *f, true
}

// SystemOrgLock 系統工作組織的讀寫鎖(見 system_gate.go)
func (a *App) SystemOrgLock() *SystemGate { return &a.root().systemGate }

// 受限專案的輸出篩選(給不是網頁管理員的人;為什麼是輸出層篩選見 web/restricted.go)

// ScrubRestricted 把 JSON 值(Object 轉出來的)裡屬於受限專案的物件拿掉。
// 回傳 (新的值, 這個值本身是不是受限專案的物件)。物件的判斷:project 欄位是受限專案 id(Issue、文件、頻道綁定…);
// 專案物件本身 restricted = true;unknown 時任何有非空 project 欄位的物件、restricted = true 的物件都算
func ScrubRestricted(v any, set map[string]bool, unknown bool) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if p, ok := x["project"].(string); ok && p != "" && (unknown || set[p]) {
			return nil, true
		}
		if b, ok := x["restricted"].(bool); ok && b {
			if id, _ := x["id"].(string); unknown || set[id] {
				return nil, true
			}
		}
		for k, y := range x {
			ny, drop := ScrubRestricted(y, set, unknown)
			if drop {
				delete(x, k) // 欄位值本身是受限的物件(例如 {"project": {...}}):整個欄位拿掉
				continue
			}
			x[k] = ny
		}
		return x, false
	case []any:
		out := make([]any, 0, len(x))
		for _, y := range x {
			ny, drop := ScrubRestricted(y, set, unknown)
			if !drop {
				out = append(out, ny)
			}
		}
		return out, false
	}
	return v, false
}

// RestrictedArg 引數指到受限專案(project、或專案 action 的 id)
func RestrictedArg(args map[string]any, set map[string]bool, unknown bool) bool {
	if p, ok := args["project"].(string); ok && strings.TrimSpace(p) != "" {
		return unknown || set[strings.ToUpper(strings.TrimSpace(p))]
	}
	return false
}
