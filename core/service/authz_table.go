package service

// 操作表(ISS-33 第 1 階段):「每個對外 service 方法 → 操作名 → 範圍 → 所需能力 → 角色憑證可否呼叫 → MCP 工具與 REST 端點」。
//
// 這張表只是「現況的描述」,不是執行授權的地方:service 方法照舊用 orgRole / needOrgAdmin / needServerAdmin / viewer / docViewer /
// channelActor 等輔助函式自己檢查(第 3 階段才會收斂成單一 Authorize,並讓這張表成為唯一來源)。表的內容都是讀實作確認過的;
// 表與實作不一致時,不改行為,把差異登記在 authz_table_test.go 的已知差異白名單與 omixor 文件櫃 DOC-35《docs/authorization.md》「不一致與漏檢」。
//
// 守門測試(authz_table_test.go):
//   (a) 掃 core/service 所有帶 *model.Principal 的公開方法,每個都要出現在某個 Op.Methods,或在 AuthzExempt 寫明原因;
//   (b) AuthzTools 與 model.CoreTools 的 Admin / Agent / Worker / Needs 一致,表的 Tools 欄位指到真的工具;
//   (c) 依表自動產生「未授權被拒」測試:成員(要 OrgAdmin 以上時)、角色憑證(表說不可時)、外部組織成員、外部組織的角色憑證,各試一次,預期 403。
//
// 維護:新增或改名帶 Principal 的公開方法 → 在 AuthzOps 加一筆(或放進 AuthzExempt);新增 MCP 工具 → 在 model/tools.go 與 AuthzTools 各登記一次、
// 並在對應的 Op.Tools 列出;新增 REST 路由 → 把路由字串寫進對應的 Op.Routes。

// AuthzLevel 呼叫者最低要有的身分(不含角色憑證與附加條件)
type AuthzLevel string

const (
	LvAuthenticated AuthzLevel = "authenticated" // 任何登入者(伺服器層,沒有組織)
	LvOrgMember     AuthzLevel = "orgMember"     // 組織成員(伺服器管理員視同成員)
	LvOrgAdmin      AuthzLevel = "orgAdmin"      // 組織管理員(伺服器管理員視同管理員)
	LvServerAdmin   AuthzLevel = "serverAdmin"   // 伺服器管理員
)

// AuthzAgent 角色憑證(task-bound agent credential,Principal.Agent != nil)能不能呼叫
type AuthzAgent string

const (
	AgentNo   AuthzAgent = "no"   // 拒絕(403)
	AgentOrg  AuthzAgent = "org"  // 憑證所屬組織內都可以(跨組織 403)
	AgentSelf AuthzAgent = "self" // 只限自己:自己的角色、自己綁定的任務、自己是成員的頻道
	AgentOnly AuthzAgent = "only" // 只有角色身分可以,人類呼叫者 403
)

// AuthzCap 附加條件(在 Level 之上再檢查的資源層級能力)
type AuthzCap string

const (
	CapChainEdge  AuthzCap = "chainEdge"          // 非管理員的成員:組織鏈上要有 user→agent 的邊(AssignTask)
	CapDownstream AuthzCap = "downstream"         // 角色:委派要鏈上有 agent→agent 的邊
	CapClaimer    AuthzCap = "claimer"            // 任務的領取者本人(或組織管理員)
	CapLeaseCode  AuthzCap = "leaseCode"          // 領取驗證碼與租約(執行者代角色)
	CapDocRead    AuthzCap = "docRead"            // 角色:docAccess 要有讀取權;成員不受限
	CapDocWrite   AuthzCap = "docWrite"           // 角色:docAccess 要有寫入權;成員不受限
	CapChannel    AuthzCap = "channel"            // 頻道成員(組織管理員讀得到全部頻道;角色只限自己是成員的)
	CapFeatureMCP AuthzCap = "featureMCP"         // 功能開關 mcpGateway 要開
	CapSelfOnly   AuthzCap = "selfOnly"           // 只能作用在自己(自己的 token、自己的配對碼)
	CapRestore    AuthzCap = "serverAdminRestore" // 局部:IssuePatch.Restore 只限伺服器管理員
	// 第 3 階段(專案可見性,規則集中在 project_access.go):
	CapProjectView  AuthzCap = "projectView"  // 受限專案(Restricted)的資料:要是專案成員、組織管理員或伺服器管理員;看不到的和不存在一樣(404、列表沒有)
	CapProjectAdmin AuthzCap = "projectAdmin" // 專案管理員:組織管理員,或專案成員角色是 admin(管理專案成員);看不到的專案 404
	// 待決定 / 需要協助:
	CapOwnerOrAdmin AuthzCap = "ownerOrAdmin" // 資源的建立者(withdraw)或原處理人(reopen),或組織管理員;其他成員 403、角色憑證只限自己建立的
)

// AuthzOp 一個操作
type AuthzOp struct {
	ID      string     // 操作名(全域唯一;稽核 action 之後可對應)
	Group   string     // 分組
	Scope   string     // server / org / project / self
	Level   AuthzLevel // 最低身分
	Agent   AuthzAgent // 角色憑證可否呼叫
	Caps    []AuthzCap // 附加條件
	Methods []string   // 對應的 service 方法(Service 的公開方法名)
	Tools   []string   // 對應的 MCP 工具(model.CoreTools 的名稱)
	Routes  []string   // 對應的 REST 路由;[a]=authed(只收 omx_ 使用者 token)、[aa]=authedOrAgent(也收角色憑證)
	Note    string     // 補充(實作上的特例)
}

// AuthzToolMark MCP 工具的曝光標記(必須與 model.CoreTools 完全一致,authz_table_test 驗證)
type AuthzToolMark struct {
	Name                 string
	Admin, Agent, Worker bool
	Needs                string
}

// AuthzExempt 帶 *model.Principal 但不是「需要授權的操作」的公開方法與原因(每筆都要寫原因)
var AuthzExempt = map[string]string{
	"Audit":           "內部寫稽核的 helper,不是操作;呼叫端都已授權",
	"ResolveAuthor":   "署名解析:不讀寫資料;由呼叫端(已授權的寫入方法)使用,伺服器管理員才能代別人署名",
	"ResolveWho":      "署名/對象解析:不讀寫資料,由呼叫端先授權",
	"SelfActor":       "取呼叫者自己的 actor,不讀寫資料",
	"WhoOrSelf":       "署名/對象解析,由呼叫端先授權",
	"ResolveActor":    "actor 字串解析,由呼叫端先授權",
	"RequireViewer":   "授權輔助函式本身(給 core/modules 的選用模組用):等同 viewer",
	"RequireOrgAdmin": "授權輔助函式本身(給 core/modules 的選用模組用):等同 needOrgAdmin",
	"WhoActor":        "署名用的 actor id,不讀寫資料",
}

// AuthzOps 全部操作
var AuthzOps = []AuthzOp{
	// ===== 登入與自己 =====
	{ID: "auth.logout", Group: "登入", Scope: "self", Level: LvAuthenticated, Agent: AgentNo, Caps: []AuthzCap{CapSelfOnly},
		Methods: []string{"Logout"}, Routes: []string{"POST /api/auth/logout [a]"},
		Note: "只作用於呼叫者自己的 token;沒有授權檢查,角色憑證到不了(端點只收 omx_ token)"},
	{ID: "auth.pair.create", Group: "登入", Scope: "self", Level: LvAuthenticated, Agent: AgentNo, Caps: []AuthzCap{CapSelfOnly},
		Methods: []string{"CreatePairCode"}, Routes: []string{"POST /api/auth/pair [a]"},
		Note: "自我檢查:拒絕 nil、角色身分、空 user id、配對碼 token、OAuth 連線(Via=oauth,含 mcp:write);只能替自己產生"},

	// ===== 伺服器層 =====
	{ID: "org.list", Group: "組織與成員", Scope: "server", Level: LvAuthenticated, Agent: AgentNo,
		Methods: []string{"ListOrgs"}, Tools: []string{"orgs"}, Routes: []string{"GET /api/orgs [a]"},
		Note: "只回自己是成員的組織(伺服器管理員全部);沒有拒絕分支,角色憑證在 service 層不會被拒但查不到任何組織"},
	{ID: "org.create", Group: "組織與成員", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"CreateOrg", "CreateOrgWith"}, Routes: []string{"POST /api/orgs [a]"}},
	{ID: "org.update", Group: "組織與成員", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"UpdateOrg"}, Routes: []string{"PUT /api/orgs/{org} [a]"},
		Note: "組織管理員也不能改組織本身(名稱、圖示),只有伺服器管理員"},
	{ID: "org.delete", Group: "組織與成員", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"DeleteOrg"}, Routes: []string{"POST /api/orgs/{org}/delete [a]"}},
	{ID: "user.list", Group: "稽核與使用者", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"ListUsers"}, Routes: []string{"GET /api/admin/users [a]"}},
	{ID: "user.save", Group: "稽核與使用者", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"SaveUser"}, Routes: []string{"POST /api/admin/users [a]"}},
	{ID: "user.disable", Group: "稽核與使用者", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"SetUserDisabled"}, Note: "目前只有 console 的 action user_disable 呼叫;停用時一併撤銷 OAuth 授權"},
	{ID: "user.oauth.revoke", Group: "稽核與使用者", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"RevokeUserOAuth"}, Note: "目前只有 console 的 action user_oauth_revoke 呼叫"},
	{ID: "token.list", Group: "稽核與使用者", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"ListTokens"}, Routes: []string{"GET /api/admin/tokens [a]"}},
	{ID: "token.revoke", Group: "稽核與使用者", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"RevokeToken"}, Routes: []string{"POST /api/admin/tokens/{id}/revoke [a]"}},
	{ID: "schema.check", Group: "稽核與使用者", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"SchemaCheck"}, Tools: []string{"schema_check"}, Routes: []string{"GET /api/system/schema-check [a]"},
		Note: "唯讀、不寫稽核;只查 catalog。Store 沒有實作 SchemaChecker 時 400"},
	{ID: "audit.list", Group: "稽核與使用者", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"ListAudit"}, Routes: []string{"GET /api/admin/audit [a]"}},
	{ID: "server.features.read", Group: "設定與整合", Scope: "server", Level: LvAuthenticated, Agent: AgentNo,
		Methods: []string{"GetServerFeatures"}, Routes: []string{"GET /api/system/features [a]"},
		Note: "任何登入者可讀(canEdit 旗標依是否伺服器管理員);明確拒絕 nil 與角色憑證"},
	{ID: "server.features.save", Group: "設定與整合", Scope: "server", Level: LvServerAdmin, Agent: AgentNo,
		Methods: []string{"SaveServerFeatures"}, Routes: []string{"POST /api/system/features [a]"}},

	// ===== 組織與成員 =====
	{ID: "member.list", Group: "組織與成員", Scope: "org", Level: LvOrgMember, Agent: AgentNo,
		Methods: []string{"ListMembers"}, Routes: []string{"GET /api/orgs/{org}/members [a]"}},
	{ID: "member.save", Group: "組織與成員", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"SaveMember"}, Routes: []string{"POST /api/orgs/{org}/members [a]"},
		Note: "組織管理員只能加入已存在的使用者;email 不存在時要建立使用者,只有伺服器管理員可以(R-9 已修)"},
	{ID: "member.remove", Group: "組織與成員", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"RemoveMember"}, Routes: []string{"POST /api/orgs/{org}/members/remove [a]"}},
	{ID: "actor.list", Group: "組織與成員", Scope: "org", Level: LvOrgMember, Agent: AgentNo,
		Methods: []string{"ListOrgActors"}, Routes: []string{"GET /api/orgs/{org}/actors [a]"}},
	{ID: "attachment.save", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentNo,
		Methods: []string{"SaveAttachment"}, Routes: []string{"POST /api/orgs/{org}/attachments [a]"}},
	{ID: "attachment.get", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentNo,
		Methods: []string{"GetAttachment"}, Routes: []string{"GET /api/orgs/{org}/attachments/{id} [a]"}},

	// ===== Issue =====
	{ID: "issue.read", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ListIssues", "GetIssue", "QueryIssues", "IssueBoard", "ResolveIssueID"}, Tools: []string{"issues"},
		Routes: []string{"GET /api/orgs/{org}/issues [a]", "GET /api/orgs/{org}/issues/board [a]", "GET /api/orgs/{org}/issues/{id} [a]"},
		Note:   "QueryIssues 帶 deleted 時走 ListDeletedIssues(要組織管理員)"},
	{ID: "issue.create", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"CreateIssue"}, Tools: []string{"issue_create", "report_problem"}, Routes: []string{"POST /api/orgs/{org}/issues [a]"},
		Note: "帶 assignee 會替角色排任務;有設定組織鏈時要有邊(成員 user→agent、角色 agent→agent),組織管理員不受限(R-3 已修)"},
	{ID: "issue.update", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapRestore, CapProjectView},
		Methods: []string{"UpdateIssue"}, Tools: []string{"issue_update"}, Routes: []string{"POST /api/orgs/{org}/issues/{id} [a]"},
		Note: "改 assignee 同 issue.create(R-3 已修);IssuePatch.Restore 只限伺服器管理員"},
	{ID: "issue.comment", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"AddIssueComment"}, Tools: []string{"issue_update"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/comments [a]"}},
	{ID: "issue.accept", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"AcceptIssue"}, Tools: []string{"issue_accept"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/accept [aa]"},
		Note: "接受者一律是呼叫者本人(人或角色憑證,規則同 issue.comment);已關閉 409、已被別人接受 409(要先放回或用 issue_update 的 assignee 轉交)、已是自己冪等;沒有組織鏈檢查(只能指派給自己);角色憑證只能接受指派給自己、自己建立、或目前任務連到的 Issue(其他 403)"},
	{ID: "issue.release", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ReleaseIssue"}, Tools: []string{"issue_accept"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/release [aa]"},
		Note: "目前的接受者本人(人或角色憑證)或組織管理員,別人 403;沒有人接受時冪等;MCP 是 issue_accept 的 release=true。不用 CapClaimer(那是任務領取者,且守門測試會把「沒有接受者」的探測當成成員被拒)"},
	{ID: "issue.delete", Group: "Issue", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"DeleteIssue"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/delete [a]"},
		Note: "一般成員即可刪(移進垃圾桶),無作者或管理員限制(R-10);沒有 MCP 工具"},
	{ID: "issue.trash.list", Group: "Issue", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"ListDeletedIssues"}, Note: "只經 QueryIssues(deleted=true)、備份;沒有獨立的 REST 路由"},
	{ID: "issue.restore", Group: "Issue", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"RestoreIssue"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/restore [a]"}},
	{ID: "issue.purge", Group: "Issue", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"PurgeIssue"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/purge [a]"}},
	{ID: "issue.export", Group: "Issue", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"ExportIssues", "ExportIssuesWith"}, Routes: []string{"GET /api/orgs/{org}/issues/export [a]"}},
	{ID: "issue.import", Group: "Issue", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"ImportIssues", "ImportIssuesWith"}, Routes: []string{"POST /api/orgs/{org}/issues/import [a]"}},

	// ===== 角色與任務 =====
	{ID: "agent.read", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentSelf,
		Methods: []string{"ListAgents", "GetAgent", "AgentTools"}, Tools: []string{"agents", "agent_context"},
		Routes: []string{"GET /api/orgs/{org}/agents [a]", "GET /api/orgs/{org}/agents/{id} [a]", "GET /api/orgs/{org}/agents/{id}/tools [a]"},
		Note:   "角色憑證只看得到自己;MCP 的 agents 工具不開給角色端點(agent_context 取代)"},
	{ID: "agent.save", Group: "角色與任務", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"SaveAgent"}, Tools: []string{"agent_upsert"}, Routes: []string{"POST /api/orgs/{org}/agents [a]"}},
	{ID: "agent.delete", Group: "角色與任務", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"DeleteAgent", "DeleteAgentTo"}, Routes: []string{"POST /api/orgs/{org}/agents/{id}/delete [a]"}},
	{ID: "agent.import", Group: "角色與任務", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"ImportAgents"}, Routes: []string{"POST /api/orgs/{org}/agents-import [a]"}},
	{ID: "agent.routine.run", Group: "角色與任務", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"RunRoutine"}, Routes: []string{"POST /api/orgs/{org}/agents/{id}/routines/{rid}/run [a]"}},
	{ID: "agent.task.transfer", Group: "角色與任務", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"TransferAgentTasks"}, Tools: []string{"agent_task_transfer"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/transfer [a]"}},
	{ID: "agent.task.read", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ListAgentTasks", "GetAgentTask"}, Tools: []string{"agent_tasks", "agent_context", "dispatch_queue", "dispatch_status", "dispatch_claim"},
		Routes: []string{"GET /api/orgs/{org}/agent-tasks [a]", "GET /api/orgs/{org}/agent-tasks/{id} [a]"},
		Note:   "角色憑證只查得到回報給自己的任務"},
	{ID: "agent.task.create", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapChainEdge},
		Methods: []string{"AssignTask"}, Tools: []string{"agent_task_create"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks [a]"},
		Note: "組織管理員派工不受限;一般成員需鏈上有 user→agent 的邊"},
	{ID: "agent.task.claim", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ClaimTask", "ClaimNext"}, Tools: []string{"agent_task_claim", "dispatch_claim"},
		Routes: []string{"POST /api/orgs/{org}/agent-tasks/claim [a]", "POST /api/orgs/{org}/agent-tasks/claim-next [a]", "POST /api/orgs/{org}/agent-tasks/{id}/claim [a]"}},
	{ID: "agent.task.release", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapClaimer},
		Methods: []string{"ReleaseTask"}, Tools: []string{"dispatch_release"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/{id}/release [a]"},
		Note: "領取者本人或組織管理員"},
	{ID: "agent.task.report", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapClaimer, CapProjectView},
		Methods: []string{"ReportTask"}, Tools: []string{"agent_task_report"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/{id}/report [a]"},
		Note: "人:領取者本人或組織管理員;角色憑證:只能回報綁定的那一筆,狀態限 inProgress/blocked/done"},
	{ID: "agent.task.cancel", Group: "角色與任務", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"CancelTask"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/{id}/cancel [a]"}},
	{ID: "agent.task.requeue", Group: "角色與任務", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"RequeueTask"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/{id}/requeue [a]"}},
	{ID: "agent.task.reply", Group: "角色與任務", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"ReplyTask"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/{id}/reply [a]"},
		Note: "只有需要協助(blocked)的任務;回覆記成備註並退回待領取"},
	{ID: "agent.task.delegate", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentOnly, Caps: []AuthzCap{CapDownstream},
		Methods: []string{"DelegateTask"}, Tools: []string{"agent_task_delegate"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/delegate [aa]"},
		Note: "角色身分依組織鏈委派;人只有伺服器管理員帶 from 可代委派;一般成員與組織管理員(非伺服器管理員)403"},
	{ID: "agent.task.worker.report", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapClaimer, CapLeaseCode},
		Methods: []string{"WorkerReport"}, Tools: []string{"dispatch_report"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/{id}/worker/report [a]"},
		Note: "領取者本人 + 租約 + 驗證碼,以角色署名;角色憑證不行(notAgent)"},
	{ID: "agent.task.worker.comment", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapClaimer, CapLeaseCode},
		Methods: []string{"WorkerComment"}, Tools: []string{"dispatch_comment"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/{id}/worker/comment [a]"}},
	{ID: "agent.task.worker.delegate", Group: "角色與任務", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapClaimer, CapLeaseCode, CapDownstream},
		Methods: []string{"WorkerDelegate"}, Tools: []string{"dispatch_delegate"}, Routes: []string{"POST /api/orgs/{org}/agent-tasks/{id}/worker/delegate [a]"}},

	// ===== 文件櫃與專案 =====
	{ID: "cabinet.read", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapDocRead, CapProjectView},
		Methods: []string{"GetCabinet"}, Tools: []string{"cabinet", "agent_context"}, Routes: []string{"GET /api/orgs/{org}/cabinet [a]", "GET /api/orgs/{org}/docs [a]"},
		Note: "角色憑證只看得到 docAccess 範圍內的文件;結構本身可讀"},
	{ID: "doc.read", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapDocRead, CapProjectView},
		Methods: []string{"GetDoc", "ReadDoc"}, Tools: []string{"docs"}, Routes: []string{"GET /api/orgs/{org}/docs/{id} [a]", "POST /api/orgs/{org}/docs/{id}/read [a]"}},
	{ID: "doc.create", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapDocWrite, CapProjectView},
		Methods: []string{"CreateDoc"}, Tools: []string{"doc_save"}, Routes: []string{"POST /api/orgs/{org}/docs [a]"}},
	{ID: "doc.update", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapDocRead, CapDocWrite, CapProjectView},
		Methods: []string{"UpdateDoc", "EditDoc"}, Tools: []string{"doc_save"}, Routes: []string{"POST /api/orgs/{org}/docs/{id} [a]", "POST /api/orgs/{org}/docs/{id}/edit [a]"}},
	{ID: "doc.delete", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"DeleteDoc"}, Routes: []string{"POST /api/orgs/{org}/docs/{id}/delete [a]"},
		Note: "一般成員即可刪(R-10);沒有 MCP 工具"},
	{ID: "cabinet.layout.save", Group: "文件櫃", Scope: "project", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"SaveCabinetLayout", "SaveCabinetLayoutIf"}, Tools: []string{"cabinet_layout_save"}, Routes: []string{"POST /api/orgs/{org}/cabinet/layout [a]"}},
	{ID: "cabinet.import", Group: "文件櫃", Scope: "project", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"ImportCabinet"}, Routes: []string{"POST /api/orgs/{org}/cabinet/import [a]"}},
	{ID: "project.read", Group: "文件櫃", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapDocRead, CapProjectView},
		Methods: []string{"ListProjects", "GetProject"}, Tools: []string{"projects", "agent_context"},
		Routes: []string{"GET /api/orgs/{org}/projects [a]", "GET /api/orgs/{org}/projects/{id} [a]"},
		Note:   "角色憑證沒有任何 docAccess 時,列專案是空的、取專案 403(R-8 已修);MCP 另有 NeedDocRead"},
	{ID: "project.create", Group: "文件櫃", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"CreateProject"}, Tools: []string{"project_save"}, Routes: []string{"POST /api/orgs/{org}/projects [a]"}},
	{ID: "project.update", Group: "文件櫃", Scope: "project", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"UpdateProject"}, Tools: []string{"project_save"}, Routes: []string{"PUT /api/orgs/{org}/projects/{id} [a]"}},
	{ID: "project.delete", Group: "文件櫃", Scope: "project", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"DeleteProject"}, Routes: []string{"POST /api/orgs/{org}/projects/{id}/delete [a]"}},
	// ISS-34 第 3 階段:專案成員(受限專案的可見範圍,見 project_access.go、project_members.go)
	{ID: "project.member.list", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ListProjectMembers"}, Routes: []string{"GET /api/orgs/{org}/projects/{id}/members [a]"},
		Note: "看得到專案的組織成員(受限專案要是專案成員或組織管理員;看不到 404);角色憑證 403;人類端點的 MCP projects 工具帶 id 時另外回 members(角色端點不回)"},
	{ID: "project.member.set", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectAdmin},
		Methods: []string{"SetProjectMember"}, Tools: []string{"project_save"}, Routes: []string{"POST /api/orgs/{org}/projects/{id}/members [a]"},
		Note: "組織管理員或專案管理員(成員角色 admin);成員要是組織成員或組織裡的角色;角色憑證 403;看不到的專案 404"},
	{ID: "project.member.remove", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectAdmin},
		Methods: []string{"RemoveProjectMember"}, Tools: []string{"project_save"}, Routes: []string{"POST /api/orgs/{org}/projects/{id}/members/remove [a]"},
		Note: "同 project.member.set;移除角色成員時,它在受限專案還沒領取的任務一併取消"},
	// ISS-34 進行中新增的專案與 repo 連結(以現況為準:service/project_repo.go)
	{ID: "project.repo.list", Group: "文件櫃", Scope: "project", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ListProjectRepos"}, Routes: []string{"GET /api/orgs/{org}/projects/repos [a]", "GET /api/orgs/{org}/projects/{id}/repos [a]"},
		Note: "viewer;路由用 authed,角色憑證只能經 service 層直接呼叫(目前沒有 MCP 工具)"},
	{ID: "project.repo.link", Group: "文件櫃", Scope: "project", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"LinkProjectRepo"}, Routes: []string{"POST /api/orgs/{org}/projects/{id}/repos [a]"}},
	{ID: "project.repo.unlink", Group: "文件櫃", Scope: "project", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"UnlinkProjectRepo"}, Routes: []string{"POST /api/orgs/{org}/projects/{id}/repos/delete [a]"}},

	// ===== 組織鏈 =====
	{ID: "chain.read", Group: "組織鏈", Scope: "org", Level: LvOrgMember, Agent: AgentOrg,
		Methods: []string{"GetChain"}, Tools: []string{"chain", "agent_context"}, Routes: []string{"GET /api/orgs/{org}/chain [aa]"},
		Note: "service 與 REST 對角色憑證開放,MCP 的 chain 工具只開管理端點(agent_context 帶上下游)"},
	{ID: "chain.save", Group: "組織鏈", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"SaveChain"}, Tools: []string{"chain_save"}, Routes: []string{"POST /api/orgs/{org}/chain [a]"}},

	// ===== 頻道 =====
	{ID: "channel.list", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapChannel, CapProjectView},
		Methods: []string{"ListChannels"}, Tools: []string{"channels", "agent_context"}, Routes: []string{"GET /api/orgs/{org}/channels [aa]"},
		Note: "只列看得到的頻道(自己是成員的;組織管理員全部)"},
	{ID: "channel.read", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapChannel, CapProjectView},
		Methods: []string{"GetChannel"}, Tools: []string{"channel_messages"}, Routes: []string{"GET /api/orgs/{org}/channels/{id} [aa]"},
		Note: "看不到的頻道回 404(不洩漏存在)"},
	{ID: "channel.create", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"CreateChannel"}, Tools: []string{"channel_create"}, Routes: []string{"POST /api/orgs/{org}/channels [aa]"}},
	{ID: "channel.update", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapChannel},
		Methods: []string{"UpdateChannel"}, Routes: []string{"POST /api/orgs/{org}/channels/{id} [aa]"}, Note: "頻道成員或組織管理員;角色拒絕"},
	{ID: "channel.members", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapChannel},
		Methods: []string{"ChangeChannelMembers"}, Routes: []string{"POST /api/orgs/{org}/channels/{id}/members [aa]"}, Note: "頻道成員或組織管理員;角色拒絕"},
	{ID: "channel.messages", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapChannel},
		Methods: []string{"ListChannelMessages"}, Tools: []string{"channel_messages"}, Routes: []string{"GET /api/orgs/{org}/channels/{id}/messages [aa]"}},
	{ID: "channel.post", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapChannel},
		Methods: []string{"PostChannelMessage"}, Tools: []string{"channel_post"}, Routes: []string{"POST /api/orgs/{org}/channels/{id}/messages [aa]"},
		Note: "要是頻道成員才能發(組織管理員也一樣);伺服器管理員可用 as 代節點"},
	{ID: "channel.markRead", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapChannel},
		Methods: []string{"MarkChannelRead"}, Tools: []string{"channel_messages"}, Routes: []string{"POST /api/orgs/{org}/channels/{id}/read [aa]"}},
	{ID: "channel.inbox", Group: "訊息頻道", Scope: "org", Level: LvOrgMember, Agent: AgentSelf,
		Methods: []string{"ChannelInbox"}, Tools: []string{"agent_context"}, Routes: []string{"GET /api/orgs/{org}/channels/inbox [aa]"}},

	// ===== 事件 =====
	{ID: "incident.read", Group: "事件", Scope: "org", Level: LvOrgMember, Agent: AgentOrg,
		Methods: []string{"ListIncidents", "GetIncident"}, Tools: []string{"incidents"},
		Routes: []string{"GET /api/orgs/{org}/incidents [aa]", "GET /api/orgs/{org}/incidents/{id} [aa]"},
		Note:   "REST 與 service 對角色憑證開放,MCP 的 incidents 只開管理端點(R-4)"},
	{ID: "incident.declare", Group: "事件", Scope: "org", Level: LvOrgMember, Agent: AgentOrg,
		Methods: []string{"DeclareIncident"}, Tools: []string{"incident_declare"}, Routes: []string{"POST /api/orgs/{org}/incidents [aa]"}, Note: "同 incident.read 的 R-4"},
	{ID: "incident.update", Group: "事件", Scope: "org", Level: LvOrgMember, Agent: AgentOrg,
		Methods: []string{"UpdateIncident"}, Tools: []string{"incident_update"}, Routes: []string{"POST /api/orgs/{org}/incidents/{id} [aa]"}, Note: "同 incident.read 的 R-4"},
	{ID: "incident.import", Group: "事件", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"ImportIncidents"}, Routes: []string{"POST /api/orgs/{org}/incidents/import [a]"}},

	// ===== 待決定 / 需要協助(service/decision.go;有來源 Issue 的決定繼承該 Issue 的專案可見性,看不到的 404、列表沒有) =====
	{ID: "decision.add", Group: "待決定", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"AddDecision"}, Tools: []string{"issue_decision_add"}, Routes: []string{"POST /api/orgs/{org}/decisions [aa]"},
		Note: "建立者署名一律是呼叫者本人(SelfActor,角色憑證是角色),不接受指定;來源 Issue 看不到的專案和不存在一樣 400"},
	{ID: "decision.list", Group: "待決定", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ListDecisions"}, Tools: []string{"issue_decisions"}, Routes: []string{"GET /api/orgs/{org}/decisions [aa]"},
		Note: "只列看得到的(來源 Issue 在看不到的專案的決定不列);mine 對角色憑證回空"},
	{ID: "decision.get", Group: "待決定", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"GetDecision"}, Tools: []string{"issue_decisions"}, Routes: []string{"GET /api/orgs/{org}/decisions/{id} [aa]"},
		Note: "看不到的決定回和不存在一樣的 404"},
	{ID: "decision.summary", Group: "待決定", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"DecisionSummaries"}, Routes: []string{"GET /api/orgs/{org}/decisions/summary [aa]"}, Note: "只算看得到的決定"},
	{ID: "decision.answer", Group: "待決定", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"AnswerDecision"}, Tools: []string{"issue_decision_answer"}, Routes: []string{"POST /api/orgs/{org}/decisions/{id}/answer [aa]"},
		Note: "只有人(組織成員);路由接受角色憑證但 service 層 403;重複回答 409(條件式更新)"},
	{ID: "decision.reopen", Group: "待決定", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapOwnerOrAdmin, CapProjectView},
		Methods: []string{"ReopenDecision"}, Routes: []string{"POST /api/orgs/{org}/decisions/{id}/reopen [aa]"},
		Note: "組織管理員或原處理人;一般成員 403;角色憑證 403;沒有 MCP 工具"},
	{ID: "decision.withdraw", Group: "待決定", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapOwnerOrAdmin, CapProjectView},
		Methods: []string{"WithdrawDecision"}, Routes: []string{"POST /api/orgs/{org}/decisions/{id}/withdraw [aa]"},
		Note: "open 才能撤回;建立者或組織管理員;角色憑證只能撤回自己提的;沒有 MCP 工具"},
	{ID: "decision.to_issue", Group: "待決定", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"DecisionToIssue"}, Tools: []string{"issue_decision_to_issue"}, Routes: []string{"POST /api/orgs/{org}/decisions/{id}/to-issue [aa]"},
		Note: "只有人(組織成員);新 Issue 可指定專案(看不到的專案 404),預設依 repo 的連結規則;已有來源 Issue 或已撤回 409"},

	// ===== Issue 子項(service/issue_items.go;完全繼承所屬 Issue 的專案可見性,看不到的 Issue 底下的子項 404;署名一律是呼叫者本人) =====
	{ID: "issue.item.read", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ListItems", "GetItem"}, Tools: []string{"issue_items"},
		Routes: []string{"GET /api/orgs/{org}/issues/{id}/items [aa]", "GET /api/orgs/{org}/issues/{id}/items/{itemId} [aa]"},
		Note:   "所屬 Issue 看不到(受限專案非成員、角色憑證不是專案成員)和不存在一樣 404;垃圾桶裡的 Issue 當作不存在"},
	{ID: "issue.item.add", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentOrg, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"AddItem"}, Tools: []string{"issue_item_add"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/items [aa]"},
		Note: "角色憑證可以新增(提出下一步),但負責人只能留空或是自己;負責人是受限專案的角色要是專案成員(400)"},
	{ID: "issue.item.update", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"UpdateItem"}, Tools: []string{"issue_item_update"}, Routes: []string{"PATCH /api/orgs/{org}/issues/{id}/items/{itemId} [aa]"},
		Note: "只有人;路由接受角色憑證但 service 層 403;完成與取消的子項 409;被別人持有有效租約時不能換負責人 409"},
	{ID: "issue.item.claim", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ClaimItem", "StartItem"}, Tools: []string{"issue_item_claim"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/items/{itemId}/claim [aa]"},
		Note: "只有人;前置未完成 409;被別人持有有效租約 409;負責人是別的人時只有組織管理員可領;負責人是角色時綁定 agent_task 並回一次性憑證"},
	{ID: "issue.item.release", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapClaimer, CapProjectView},
		Methods: []string{"ReleaseItem"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/items/{itemId}/release [aa]"},
		Note: "領取者本人或組織管理員;角色憑證 403;沒有 MCP 工具(用 issue_item_report status=todo)"},
	{ID: "issue.item.report", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapClaimer, CapProjectView},
		Methods: []string{"ReportItem"}, Tools: []string{"issue_item_report"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/items/{itemId}/report [aa]"},
		Note: "人:沒被別人持有有效租約、或是領取者本人、或組織管理員;角色憑證:只能回報自己綁定任務的那個子項,狀態限 doing/blocked/done"},
	{ID: "issue.item.block", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentSelf, Caps: []AuthzCap{CapClaimer, CapProjectView},
		Methods: []string{"BlockItem"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/items/{itemId}/block [aa]"},
		Note: "同 issue.item.report(status=blocked 的捷徑,要附 blockReason);MCP 用 issue_item_report"},
	{ID: "issue.item.unblock", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"UnblockItem"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/items/{itemId}/unblock [aa]"},
		Note: "只有人(組織成員皆可,通常是做決定的人);角色憑證 403;MCP 用 issue_item_report status=todo"},
	{ID: "issue.item.cancel", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"CancelItem"}, Tools: []string{"issue_item_update"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/items/{itemId}/cancel [aa]"},
		Note: "只有人;已完成 409;綁定的角色任務一併取消;MCP 是 issue_item_update 的 cancel=true"},
	{ID: "issue.item.promote", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"PromoteItem"}, Tools: []string{"issue_item_promote"}, Routes: []string{"POST /api/orgs/{org}/issues/{id}/items/{itemId}/promote [aa]"},
		Note: "只有人;新 Issue 預設放進來源 Issue 的專案(可指定,看不到的專案 404);已提升或已完成 409"},
	{ID: "issue.item.delete", Group: "Issue 子項", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapOwnerOrAdmin, CapProjectView},
		Methods: []string{"DeleteItem"}, Routes: []string{"DELETE /api/orgs/{org}/issues/{id}/items/{itemId} [aa]"},
		Note: "建立者或組織管理員;其他成員 403、角色憑證 403;沒有 MCP 工具(放棄用 cancel)"},

	// ===== commits 與 Issue 的連結(service/commits.go;ISS-69 phase 1:只開放給人,角色憑證一律 403;commit 本身整個組織成員都看得到,連結依 Issue 可見性) =====
	{ID: "commit.report", Group: "Commit", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ReportCommits"}, Tools: []string{"commits_report"}, Routes: []string{"POST /api/orgs/{org}/commits [a]"},
		Note: "一次一個 repo、最多 100 筆;回報者可以說謊,資料標 verified=false;自動連結只連「組織裡存在且呼叫者看得到」的 Issue(受限專案裡看不到的:不連結、也不列在 unmatched);從不修改或關閉 Issue;MCP 寫入工具不在 OAuth 白名單(只收 omx_ 使用者 token)"},
	{ID: "commit.read", Group: "Commit", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"QueryCommits", "GetRepoSync"}, Tools: []string{"commits"},
		Routes: []string{"GET /api/orgs/{org}/commits [a]", "GET /api/orgs/{org}/commits/sync [a]"},
		Note:   "commit 本身組織成員都看得到;每筆附的 Issue 連結只含呼叫者看得到的 Issue;依 issue 查時 Issue 看不到 404"},
	{ID: "issue.commit.read", Group: "Commit", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"ListIssueCommits"}, Routes: []string{"GET /api/orgs/{org}/issues/{id}/commits [a]"},
		Note: "Issue 看不到(受限專案非成員)和不存在一樣 404;MCP 是 issues(id) 回傳的 commits 與 commits_total(角色端點的 issues 不附)"},
	{ID: "issue.commit.link", Group: "Commit", Scope: "org", Level: LvOrgMember, Agent: AgentNo, Caps: []AuthzCap{CapProjectView},
		Methods: []string{"LinkIssueCommit", "UnlinkIssueCommit"}, Tools: []string{"issue_commit_link", "issue_commit_unlink"},
		Routes: []string{"POST /api/orgs/{org}/issues/{id}/commits [a]", "DELETE /api/orgs/{org}/issues/{id}/commits [a]"},
		Note:   "權限同 issue.update(成員、Issue 要看得到);沒回報過的 sha 允許當裸參照;MCP 寫入工具不在 OAuth 白名單"},

	// ===== 設定與整合 =====
	{ID: "mcp.gateway.read", Group: "設定與整合", Scope: "org", Level: LvOrgMember, Agent: AgentSelf,
		Methods: []string{"GetMCP", "ResolveMCPTools"}, Routes: []string{"GET /api/orgs/{org}/mcp-servers [aa]", "POST /api/orgs/{org}/mcp-tools/resolve [aa]"},
		Note: "角色憑證只看得到自己的權限、沒有連線細節"},
	{ID: "mcp.gateway.save", Group: "設定與整合", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo, Caps: []AuthzCap{CapFeatureMCP},
		Methods: []string{"SaveMCPServer", "SaveMCPTools", "SaveMCPGrants", "DeleteMCPServer"},
		Routes:  []string{"POST /api/orgs/{org}/mcp-servers [a]", "POST /api/orgs/{org}/mcp-servers/{id}/delete [a]", "POST /api/orgs/{org}/mcp-servers/{id}/tools [a]", "POST /api/orgs/{org}/mcp-grants [a]"}},
	{ID: "system.settings.read", Group: "設定與整合", Scope: "org", Level: LvOrgMember, Agent: AgentOrg,
		Methods: []string{"GetSystemSettings"}, Routes: []string{"GET /api/orgs/{org}/system [aa]"}},
	{ID: "system.settings.save", Group: "設定與整合", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"SaveSystemSettings"}, Routes: []string{"POST /api/orgs/{org}/system [a]"}},
	{ID: "template.read", Group: "設定與整合", Scope: "org", Level: LvOrgMember, Agent: AgentNo,
		Methods: []string{"ListOrgTemplates", "PreviewOrgTemplate"}, Routes: []string{"GET /api/orgs/{org}/templates [a]", "GET /api/orgs/{org}/templates/{id} [a]"}},
	{ID: "template.apply", Group: "設定與整合", Scope: "org", Level: LvOrgAdmin, Agent: AgentNo,
		Methods: []string{"ApplyOrgTemplate"}, Routes: []string{"POST /api/orgs/{org}/templates/{id}/apply [a]"}},
}

// AuthzTools 每個核心 MCP 工具的曝光標記(與 model.CoreTools 逐欄一致,測試驗證)
var AuthzTools = []AuthzToolMark{
	{Name: "orgs", Admin: true},
	{Name: "schema_check", Admin: true},
	{Name: "issues", Admin: true, Agent: true},
	{Name: "issue_create", Admin: true, Agent: true},
	{Name: "report_problem", Admin: true},
	{Name: "issue_update", Admin: true, Agent: true},
	{Name: "issue_accept", Admin: true, Agent: true},
	{Name: "commits", Admin: true},
	{Name: "commits_report", Admin: true},
	{Name: "issue_commit_link", Admin: true},
	{Name: "issue_commit_unlink", Admin: true},
	{Name: "agents", Admin: true},
	{Name: "agent_context", Admin: true, Agent: true},
	{Name: "agent_tasks", Admin: true, Agent: true},
	{Name: "agent_task_claim", Admin: true},
	{Name: "agent_task_report", Admin: true, Agent: true},
	{Name: "agent_upsert", Admin: true},
	{Name: "agent_task_create", Admin: true},
	{Name: "agent_task_transfer", Admin: true},
	{Name: "agent_task_delegate", Agent: true, Needs: "downstream"},
	{Name: "projects", Admin: true, Agent: true, Needs: "docRead"},
	{Name: "project_save", Admin: true},
	{Name: "cabinet", Admin: true, Agent: true, Needs: "docRead"},
	{Name: "docs", Admin: true, Agent: true, Needs: "docRead"},
	{Name: "doc_save", Admin: true, Agent: true, Needs: "docWrite"},
	{Name: "cabinet_layout_save", Admin: true},
	{Name: "chain", Admin: true},
	{Name: "chain_save", Admin: true},
	{Name: "channels", Admin: true, Agent: true, Needs: "channel"},
	{Name: "channel_create", Admin: true},
	{Name: "channel_messages", Admin: true, Agent: true, Needs: "channel"},
	{Name: "channel_post", Admin: true, Agent: true, Needs: "channel"},
	{Name: "incidents", Admin: true},
	{Name: "incident_declare", Admin: true},
	{Name: "incident_update", Admin: true},
	{Name: "issue_decision_add", Admin: true, Agent: true},
	{Name: "issue_decisions", Admin: true, Agent: true},
	{Name: "issue_decision_answer", Admin: true},
	{Name: "issue_decision_to_issue", Admin: true},
	{Name: "issue_item_add", Admin: true, Agent: true},
	{Name: "issue_items", Admin: true, Agent: true},
	{Name: "issue_item_update", Admin: true},
	{Name: "issue_item_claim", Admin: true},
	{Name: "issue_item_report", Admin: true, Agent: true},
	{Name: "issue_item_promote", Admin: true},
	{Name: "wireframe_render", Admin: true, Agent: true},
	{Name: "flowchart_render", Admin: true, Agent: true},
	{Name: "dispatch_queue", Worker: true},
	{Name: "dispatch_claim", Worker: true},
	{Name: "dispatch_status", Worker: true},
	{Name: "dispatch_report", Worker: true},
	{Name: "dispatch_comment", Worker: true},
	{Name: "dispatch_release", Worker: true},
	{Name: "dispatch_delegate", Worker: true},
}

// AuthzToolsNoOp 沒有對應 service 操作的工具與原因
var AuthzToolsNoOp = map[string]string{
	"wireframe_render": "純渲染,不讀寫資料;save 走巢狀的 docs/doc_save(RunNested 套用同一組 Allow/Guard)",
	"flowchart_render": "純渲染,不讀寫資料;save 走巢狀的 docs/doc_save(RunNested 套用同一組 Allow/Guard)",
}

// AuthzOpByID 依操作名找
func AuthzOpByID(id string) (AuthzOp, bool) {
	for _, o := range AuthzOps {
		if o.ID == id {
			return o, true
		}
	}
	return AuthzOp{}, false
}

// AuthzOpByMethod 依 service 方法名找
func AuthzOpByMethod(method string) (AuthzOp, bool) {
	for _, o := range AuthzOps {
		for _, m := range o.Methods {
			if m == method {
				return o, true
			}
		}
	}
	return AuthzOp{}, false
}
