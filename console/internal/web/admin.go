package web

import (
	"net/http"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
)

// 管理員才能做的寫入。
//
// web 呼叫核心時不是用登入者的身分:本機模式用 localPrincipal(伺服器管理員),遠端模式大家共用 web 登入 omixor server 的權杖,
// 所以核心那邊的「要組織管理員」擋不住 web 的一般使用者。這裡在 web 端先檢查登入者是 web 的管理員(auth.User.Admin;
// 沒啟用登入時是本機使用者,視為管理員),不是就回 403。
//
// 列進來的有兩類:
//  1. 核心要組織(或伺服器)管理員才能做的寫入:功能開關(組織層、系統層)、角色與任務的管理、組織鏈、文件櫃版面、GitHub/GitLab 追蹤設定、成員、組織
//  2. web 這台伺服器所有人共用的設定與憑證:連接對象、連線(含權杖與密鑰)、GCP / Jira / Facebook / 旁白設定、
//     自動執行(用這台的 Claude Code 跑任務)
//
// 一般成員可以做的(核心本來就允許成員做)不列:Issue、事件(宣告、更新、留言)、文件、頻道、派任務、回報、切換自己看的組織等。
var adminActions = map[string]string{
	// 功能開關:組織層(核心:組織管理員)、系統層(核心:伺服器管理員)
	"system_save":          "改組織的功能開關",
	"server_features_save": "改這台伺服器開放的功能",
	// 虛擬角色與任務(核心:組織管理員)
	"agent_upsert":       "新增或修改虛擬角色",
	"agent_delete":       "刪除虛擬角色",
	"agent_task_cancel":  "取消角色任務",
	"agent_task_requeue": "重新排入角色任務",
	"agent_routine_run":  "手動執行角色的例行工作",
	"chain_save":         "改組織鏈",
	// 文件櫃版面與專案(核心:組織管理員)
	"cabinet_layout_save": "改文件櫃的分類",
	"project_create":      "新增專案",
	"project_update":      "改專案",
	"project_delete":      "刪除專案",
	// 整合設定(核心:組織管理員)
	"github_add_repo":       "改 GitHub 追蹤的 repo",
	"github_remove_repo":    "改 GitHub 追蹤的 repo",
	"gitlab_add_project":    "改 GitLab 追蹤的專案",
	"gitlab_remove_project": "改 GitLab 追蹤的專案",
	"gitlab_settings":       "改 GitLab 伺服器設定",
	// 成員與組織(核心:組織管理員 / 伺服器管理員;建立組織時套用樣板也在這裡)
	"member_save":   "改組織成員",
	"member_remove": "移除組織成員",
	"org_create":    "建立組織",
	"org_update":    "改組織資料",
	"org_delete":    "刪除組織",
	// web 伺服器共用的設定與憑證
	"mode_set":            "改連接對象",
	"remote_logout":       "登出 omixor server",
	"gate_reset":          "改連接對象",
	"connection_upsert":   "改連線設定",
	"connection_delete":   "刪除連線",
	"connection_secret":   "改連線的密鑰",
	"connection_bearer":   "改連線的權杖",
	"connection_login":    "登入連線",
	"connection_logout":   "登出連線",
	"gcp_settings":        "改 GCP 設定",
	"jira_setup":          "改 Jira 連線",
	"facebook_app":        "改 Facebook App 設定",
	"facebook_login":      "登入 Facebook",
	"facebook_token":      "改 Facebook 權杖",
	"facebook_disconnect": "中斷 Facebook 連線",
	"voice_settings":      "改旁白設定",
	"agent_runner_save":   "改自動執行設定",
	"agent_runner_stop":   "停止自動執行中的任務",
}

// adminTools 透過 POST /api/tool 執行、核心要組織管理員的 MCP 工具
var adminTools = map[string]string{
	"agent_upsert":        "新增或修改虛擬角色",
	"chain_save":          "改組織鏈",
	"cabinet_layout_save": "改文件櫃的分類",
	"project_save":        "新增或修改專案",
}

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
// 非管理員兩層都不能改(組織層在最上層,系統層在 server)
func forViewer(name string, out any, admin bool) any {
	if admin || name != "system" {
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
