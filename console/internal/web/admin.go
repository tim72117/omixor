package web

import (
	"net/http"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
)

// isAdmin 這個請求的登入者是不是 web 的管理員(沒啟用登入時是本機使用者,視為管理員)
func isAdmin(r *http.Request, app *core.App, au *auth.Service) bool {
	u, ok := currentUser(r, app, au)
	return ok && u.Admin
}

// denyNonAdmin 不是管理員就回 403 並回傳 true
func denyNonAdmin(w http.ResponseWriter, r *http.Request, app *core.App, au *auth.Service, what string) bool {
	if isAdmin(r, app, au) {
		return false
	}
	writeJSON(w, 403, map[string]any{"error": "只有管理員可以" + what + ";你登入的帳號不是 web 的管理員", "forbidden": true})
	return true
}

// readOnlyNotAdmin 非管理員看功能開關時的說明
const readOnlyNotAdmin = "你登入的帳號不是 web 的管理員,只能看目前的設定;要改請找管理員。"

// forViewer 依登入者調整給畫面的資料:功能開關(view「system」與 system_reload / system_save / server_features_save 的結果)
// 非管理員兩層都不能改(組織層在最上層,系統層在 server);帳號(view「account」)非管理員不顯示「選擇連接對象」
func forViewer(name string, out any, admin bool) any {
	if admin {
		return out
	}
	if name == "account" { // 解除 gate 的 mode_set 只有管理員能呼叫;非管理員直接用目前的連接對象,不然會卡在選擇畫面
		if m, ok := out.(map[string]any); ok {
			m["gate"] = false
		}
		return out
	}
	if name == "members" { // 成員視圖的 canEdit(畫面也拿它決定要不要顯示垃圾桶等管理員按鈕)要和 isAdmin 一致
		if v, ok := out.(core.MembersView); ok {
			v.CanEdit = false
			return v
		}
		return out
	}
	if name != "system" {
		return out
	}
	if m, ok := out.(map[string]any); ok {
		m["canEdit"] = false
		m["readOnlyReason"] = readOnlyNotAdmin
		if sv, ok := m["server"].(map[string]any); ok {
			sv["canEdit"] = false
			sv["readOnlyReason"] = readOnlyNotAdmin
		}
	}
	return out
}

// actionView 這個 action 回傳的是哪個 view(要依登入者調整)
var actionView = map[string]string{"system_reload": "system", "system_save": "system", "server_features_save": "system"}
