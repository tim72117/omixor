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

// forViewer 依登入者調整給畫面的資料(out 是 core.Object 轉過的 map,不是原本的 struct):
//   - 功能開關(view「system」與 system_reload / system_save / server_features_save 的結果):非管理員兩層都不能改
//     (組織層在最上層,系統層在 server)
//   - 成員、組織鏈(view「members」「org_chain」)的 canEdit:改成員、組織鏈與看垃圾桶要 web 管理員(access.go)
func forViewer(name string, out any, admin bool) any {
	m, ok := out.(map[string]any)
	if admin || !ok {
		return out
	}
	switch name {
	case "members", "org_chain":
		m["canEdit"] = false
	case "system":
		m["canEdit"] = false
		m["readOnlyReason"] = readOnlyNotAdmin
		if sv, ok := m["server"].(map[string]any); ok {
			sv["canEdit"] = false
			sv["readOnlyReason"] = readOnlyNotAdmin
		}
	}
	return out
}

// markOrgAdmin 非 web 管理員的 members view 另外標出他是不是組織管理員(orgAdmin):放回別人接受的 Issue、
// 撤回與重新開啟待決定是核心照組織角色判斷的(不在 web 管理員名單上的組織管理員也可以)
func markOrgAdmin(out any, who *core.Caller) {
	if m, ok := out.(map[string]any); ok {
		org, _ := m["org"].(string)
		m["orgAdmin"] = who.OrgAdmin(org)
	}
}

// actionView 這個 action 回傳的是哪個 view(要依登入者調整)
var actionView = map[string]string{"system_reload": "system", "system_save": "system", "server_features_save": "system"}
