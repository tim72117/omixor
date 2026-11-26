package web

import "strings"

// 啟用 LDAP 時,每個 /api 請求和「組織」的關係(見 membership.go)。沒列到的 view、action、工具、路徑一律是
// scopeSystem(只能在系統工作組織用),新增功能忘了分類時只會少開放、不會跨組織外洩。

type orgScopeKind int

const (
	// scopeSystem 資料在整台一份的狀態裡(整合、範例資料、旁白、助理、自動執行、MCP 權杖以外的設定…):
	// 請求的組織是系統工作組織才能用
	scopeSystem orgScopeKind = iota
	// scopeOrg 核心資料:照請求帶的組織(X-Omixor-Org)
	scopeOrg
	// scopeHost 和組織無關(主機層設定、組織管理、導覽)
	scopeHost
	// scopePath /api/issues/{org}…:組織在路徑上
	scopePath
	// scopeAttachment GET /api/attachments/{id}:圖片請求帶不了組織,在呼叫者能讀的組織裡找
	scopeAttachment
	// scopeOffice 3D 虛擬辦公室(整台一份,檢視器分頁帶不了組織):要能讀系統工作組織
	scopeOffice
)

var orgViews = nameSet("orgs", "account", "badges", "system", "issues", "issue_repos", "cabinet", "projects", "members",
	"org_chain", "channels", "agents", "incidents", "nav")

var hostViews = nameSet("mcp", "me")

var orgActions = nameSet(
	"issue_delete", "issue_query", "issue_board", "issue_resolve", "issue_trash", "issue_restore", "issue_purge", "incident_note",
	"channel_create", "channel_update", "channel_members", "channel_messages", "channel_post", "channel_read",
	"cabinet_query", "cabinet_doc_get", "cabinet_doc_delete", "cabinet_doc_html", "cabinet_layout_save",
	"project_create", "project_update", "project_delete", "project_repo_link", "project_repo_unlink",
	"project_members", "project_member_set", "project_member_remove",
	"member_save", "member_remove", "chain_save",
	"agent_upsert", "agent_delete", "agent_task_transfer", "agent_task_cancel", "agent_task_requeue", "agent_task_reply",
	"agent_routine_run", "agent_prompt",
	"system_save", "system_reload", "navigate",
)

var hostActions = nameSet(
	"org_switch", "org_create", "org_update", "org_delete", "system_org_set", "diagram_svg", "wireframe_svg",
	"server_features_save", "user_disable", "user_oauth_revoke", "mcp_token_reveal", "mcp_token_rotate",
	"mode_set", "remote_logout", "gate_reset",
)

var orgTools = nameSet(
	"orgs", "issues", "issue_create", "issue_update", "issue_accept", "report_problem",
	"issue_decision_add", "issue_decisions", "issue_decision_answer", "issue_decision_to_issue", "issue_decision_withdraw", "issue_decision_reopen",
	"issue_item_add", "issue_items", "issue_item_update", "issue_item_claim", "issue_item_report", "issue_item_promote",
	"commits", "commits_report", "issue_commit_link", "issue_commit_unlink",
	"agents", "agent_tasks", "agent_task_create", "agent_task_report", "agent_upsert", "agent_task_transfer",
	"agent_context", "agent_task_claim", "agent_task_delegate",
	"projects", "project_save", "cabinet", "docs", "doc_save", "cabinet_layout_save", "chain", "chain_save",
	"wireframe_render", "flowchart_render",
	"channels", "channel_create", "channel_messages", "channel_post", "incidents", "incident_declare", "incident_update",
	"navigate",
)

var hostTools = nameSet("schema_check")

func routeScope(method, path string) orgScopeKind {
	byName := func(name string, org, host map[string]bool) orgScopeKind {
		switch {
		case org[name]:
			return scopeOrg
		case host[name]:
			return scopeHost
		}
		return scopeSystem
	}
	if name, ok := strings.CutPrefix(path, "/api/view/"); ok {
		return byName(name, orgViews, hostViews)
	}
	if name, ok := strings.CutPrefix(path, "/api/action/"); ok {
		return byName(name, orgActions, hostActions)
	}
	if name, ok := strings.CutPrefix(path, "/api/tool/"); ok {
		return byName(name, orgTools, hostTools)
	}
	switch {
	case path == "/api/views", path == "/api/events", strings.HasPrefix(path, "/api/auth/"):
		return scopeHost
	case path == "/api/attachments" && method == "POST":
		return scopeOrg
	case strings.HasPrefix(path, "/api/attachments/"):
		return scopeAttachment
	case strings.HasPrefix(path, "/api/issues/"):
		return scopePath
	case strings.HasPrefix(path, "/api/office/"):
		return scopeOffice
	}
	return scopeSystem
}
