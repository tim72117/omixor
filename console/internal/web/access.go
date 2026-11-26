package web

import (
	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
)

// 啟用登入時,誰可以用哪些 action(POST /api/action/…)與工具(POST /api/tool/…)。
//
// web 呼叫核心時不是用登入者的身分:本機模式用 localPrincipal(伺服器管理員),遠端模式大家共用 web 登入 omixor server 的權杖,
// 所以核心那邊的權限擋不住 web 的一般使用者,要在 web 端先檢查。
//
// 預設拒絕:每個 action、工具都要列在「一般成員」或「管理員」其中一張表;沒列到的只有 web 管理員能用
// (auth.User.Admin;沒啟用登入時是本機使用者,視為管理員)。新增 action、工具時要在這裡分類,
// 沒分類或表裡有不存在的名稱時 TestEveryActionAndToolIsClassified 會失敗。
// 這裡只管畫面的 HTTP API;本機 MCP 連接埠(3765)、對外 MCP、角色端點不經過這裡。
//
// 分類原則:一般成員保留日常操作(看、Issue、子項與待決定、文件、頻道、事件、派任務與回報);
// 管理員的是:核心要組織(或伺服器)管理員的寫入、web 共用的設定與憑證、有對外效果的發佈、
// 會在這台主機執行程式或發出角色憑證的、部署與核准類。

// memberActions 一般成員可以用的 action
var memberActions = nameSet(
	// 畫面操作
	"navigate", "org_switch", "system_reload",
	// Issue、事件
	"issue_delete", "issue_query", "issue_board", "issue_resolve", "incident_note",
	// 頻道
	"channel_create", "channel_update", "channel_members", "channel_messages", "channel_post", "channel_read",
	// 文件櫃與繪圖
	"cabinet_query", "cabinet_doc_get", "cabinet_doc_delete", "cabinet_doc_html", "diagram_svg", "wireframe_svg",
	// 角色提示詞、助理、動態牆
	"agent_prompt", "assistant_briefing", "assistant_ask", "activity_history",
	// 開發進度與流程的日常操作(範例資料)
	"pull_request_set", "pull_request_join_review", "work_item_assign", "pipeline_ai_fix",
	// 用 web 共用憑證「讀」整合資料
	"gcp_refresh", "gcp_revisions", "gcp_executions", "gcp_logs", "gcp_metrics",
	"github_refresh", "github_jobs", "github_job_log", "github_pull",
	"gitlab_refresh", "gitlab_jobs", "gitlab_job_log", "gitlab_mr", "code_repos_refresh",
	"facebook_refresh", "facebook_insights", "site_visitors",
)

// adminActions 管理員才能用的 action → 403 訊息裡的「只有管理員可以…」
var adminActions = map[string]string{
	// 功能開關:組織層(核心:組織管理員)、系統層(核心:伺服器管理員)
	"system_save":          "改組織的功能開關",
	"server_features_save": "改這台伺服器開放的功能",
	// 虛擬角色與任務(核心:組織管理員)
	"agent_upsert":        "新增或修改虛擬角色",
	"agent_delete":        "刪除虛擬角色",
	"agent_task_transfer": "轉移角色的任務",
	"agent_task_cancel":   "取消角色任務",
	"agent_task_requeue":  "重新排入角色任務",
	"agent_task_reply":    "回覆需要協助的角色任務",
	"agent_routine_run":   "手動執行角色的例行工作",
	"chain_save":          "改組織鏈",
	// 文件櫃版面與專案(核心:組織管理員)
	"cabinet_layout_save": "改文件櫃的分類",
	"project_create":      "新增專案",
	"project_update":      "改專案",
	"project_delete":      "刪除專案",
	"project_repo_link":   "連結專案與 repo",
	"project_repo_unlink": "取消專案與 repo 的連結",
	// 專案成員:核心是組織管理員或專案管理員,但 web 共用同一個核心身分、分不出是哪個專案管理員,所以限 web 管理員
	"project_members":       "看專案成員",
	"project_member_set":    "加入或改專案成員",
	"project_member_remove": "移除專案成員",
	// Issue 垃圾桶(核心:組織管理員;移到垃圾桶 issue_delete 是一般成員可以做的)
	"issue_trash":   "看 Issue 垃圾桶",
	"issue_restore": "從垃圾桶還原 Issue",
	"issue_purge":   "永久刪除 Issue",
	// 整合設定(核心:組織管理員)
	"github_add_repo":          "改 GitHub 追蹤的 repo",
	"github_remove_repo":       "改 GitHub 追蹤的 repo",
	"github_ensure_connection": "改 GitHub 連線",
	"gitlab_add_project":       "改 GitLab 追蹤的專案",
	"gitlab_remove_project":    "改 GitLab 追蹤的專案",
	"gitlab_settings":          "改 GitLab 伺服器設定",
	"gitlab_ensure_connection": "改 GitLab 連線",
	// 成員與組織(核心:組織管理員 / 伺服器管理員;建立組織時套用樣板也在這裡)
	"member_save":       "改組織成員",
	"member_remove":     "移除組織成員",
	"user_disable":      "停用或啟用使用者",
	"user_oauth_revoke": "撤銷使用者的 MCP 授權",
	"org_create":        "建立組織",
	"org_update":        "改組織資料",
	"org_delete":        "刪除組織",
	// web 伺服器共用的設定與憑證
	"mode_set":              "改連接對象",
	"remote_logout":         "登出 omixor server",
	"gate_reset":            "改連接對象",
	"connection_upsert":     "改連線設定",
	"connection_delete":     "刪除連線",
	"connection_secret":     "改連線的密鑰",
	"connection_bearer":     "改連線的權杖",
	"connection_login":      "登入連線",
	"connection_logout":     "登出連線",
	"connection_test":       "測試連線",
	"connection_discover":   "探索連線端點",
	"gcp_settings":          "改 GCP 設定",
	"gcp_ensure_connection": "改 GCP 連線",
	"jira_setup":            "改 Jira 連線",
	"jira_projects":         "從 Jira 匯入",
	"jira_import":           "從 Jira 匯入",
	"facebook_app":          "改 Facebook App 設定",
	"facebook_login":        "登入 Facebook",
	"facebook_token":        "改 Facebook 權杖",
	"facebook_disconnect":   "中斷 Facebook 連線",
	"voice_settings":        "改旁白設定",
	// 自動執行(用這台的 Claude Code 跑任務);執行日誌有指令輸出
	"agent_runner_save":  "改自動執行設定",
	"agent_runner_start": "手動啟動待領取的任務",
	"agent_runner_stop":  "停止自動執行中的任務",
	"agent_runner_log":   "看自動執行的日誌",
	// 有對外效果的發佈(用 web 共用的憑證)
	"facebook_ensure_connection": "改 Facebook 連線",
	"facebook_managed":           "改管理中的 Facebook 專頁",
	"facebook_draft_new":         "新增 Facebook 貼文草稿",
	"facebook_draft_update":      "修改 Facebook 貼文草稿",
	"facebook_draft_delete":      "刪除 Facebook 貼文草稿",
	"facebook_publish":           "發佈 Facebook 貼文",
	"facebook_cancel_scheduled":  "取消 Facebook 排程貼文",
	"voice_publish":              "發佈旁白",
	"voice_cancel":               "取消旁白批次",
	"voice_retry":                "重新檢查旁白批次",
	"voice_discard":              "移除旁白批次",
	// 核准、部署、伺服器與自動化設定
	"approval_decide":       "核准或退回請求",
	"deployment_approve":    "核准部署",
	"deployment_rollback":   "回滾部署",
	"deployment_promote":    "推進部署",
	"pipeline_approve_gate": "通過流程的核准關卡",
	"server_reboot":         "重新啟動伺服器",
	"automation_autonomy":   "改自動化的自主程度",
	"automation_toggle":     "開關自動化",
	"integration_set":       "改整合狀態",
	"alert_rule_toggle":     "開關告警規則",
	// console 的 MCP 連接埠權杖:查看完整權杖、換新
	"mcp_token_reveal": "查看 MCP 權杖",
	"mcp_token_rotate": "換新 MCP 權杖",
}

// memberTools 一般成員可以透過 /api/tool 用的工具(以本機身分執行,組織一律是目前的組織)
var memberTools = nameSet(
	// 看
	"overview", "list", "get", "navigate", "server", "orgs",
	// Issue、子項、待決定、commit
	"issues", "issue_create", "issue_update", "issue_accept", "report_problem",
	"issue_decision_add", "issue_decisions", "issue_decision_answer", "issue_decision_to_issue", "issue_decision_withdraw", "issue_decision_reopen",
	"issue_item_add", "issue_items", "issue_item_update", "issue_item_claim", "issue_item_report", "issue_item_promote",
	"commits", "commits_report", "issue_commit_link", "issue_commit_unlink",
	// 派任務與回報
	"agents", "agent_tasks", "agent_task_create", "agent_task_report",
	// 文件櫃、組織鏈(唯讀)、繪圖
	"projects", "cabinet", "docs", "doc_save", "chain", "wireframe_render", "flowchart_render",
	// 頻道、事件
	"channels", "channel_create", "channel_messages", "channel_post", "incidents", "incident_declare", "incident_update",
	// 監控與流程的日常操作、動態牆、虛擬辦公室
	"alert_update", "work_item_create", "work_item_move", "pipeline_update", "activity_post", "office_move", "office_state",
	// 用 web 共用憑證「讀」整合資料
	"voice_jobs", "site_visitors", "github", "github_runs", "github_run", "github_pull", "gcp", "gcp_logs", "gcp_metrics",
	"fb_pages", "fb_posts", "fb_insights", "fb_drafts",
)

// adminTools 管理員才能透過 /api/tool 用的工具 → 403 訊息裡的「只有管理員可以…」
var adminTools = map[string]string{
	// 核心要組織管理員
	"agent_upsert":        "新增或修改虛擬角色",
	"agent_task_transfer": "轉移角色的任務",
	"chain_save":          "改組織鏈",
	"cabinet_layout_save": "改文件櫃的分類",
	"project_save":        "新增或修改專案",
	"schema_check":        "檢核資料庫結構",
	// 角色身分與憑證
	"agent_context":       "看角色能讀取的資訊",
	"agent_task_claim":    "領取角色任務(會發角色憑證)",
	"agent_task_delegate": "依組織鏈委派任務",
	// 對外效果、在這台主機執行程式
	"voice_submit":  "送出旁白生成",
	"fb_draft_save": "新增或修改 Facebook 貼文草稿",
	// 給外部系統推資料進來的
	"alert_create":     "推送告警",
	"log_append":       "寫入紀錄",
	"request_approval": "建立核准請求",
}

func nameSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// unclassified 沒分類的動作、工具給非管理員的說明
const unclassified = "使用這個功能"

// actionNeedsAdmin 這個 action 要不要管理員。不存在的名稱不擋(交給後面回 404)
func actionNeedsAdmin(name string) (what string, need bool) {
	if memberActions[name] {
		return "", false
	}
	if what, ok := adminActions[name]; ok {
		return what, true
	}
	return unclassified, core.HasAction(name)
}

// toolNeedsAdmin 這個工具要不要管理員。不存在的名稱不擋(交給後面回 404)
func toolNeedsAdmin(name string) (what string, need bool) {
	if memberTools[name] {
		return "", false
	}
	if what, ok := adminTools[name]; ok {
		return what, true
	}
	return unclassified, mcp.HasTool(name)
}

// stripCredential 非管理員的工具結果不帶角色憑證(issue_item_claim 領角色的子項時會發一次性憑證;畫面本來就不用它)
func stripCredential(res any) {
	if m, ok := res.(map[string]any); ok {
		delete(m, "credential")
	}
}
