package store

// ---- 資料庫裡所有「誰」的欄位(純資料清單;ISS-36 / 雲端 DOC-11) ----
//
// 資料裡「誰」(作者、建立者、指派對象、頻道成員…)原本都是自由字串,格式不一(純角色 id、user:<email>、不帶前綴的 email、
// mcp / routine / chain、本機身分…;規範化規則在 model/principal.go:PrincipalNode、PrincipalKind、AssigneeNode、ActorNode)。
// 身分登記層(omixor_principals + <欄位>_ref 產生欄位 + 外鍵)不部署,改為直接進 actor 欄位;這份清單記錄全部的「誰」欄位(舊字串的寬鬆解析器與外鍵清單用)。
// 注意:IdentityFields / ActorColumns 描述的是舊字串寫法與最終結構欄位的對應;最終結構裡存在的是 ActorColumn.Target 欄位
// (InPlace 同名;Rename、Copy 是新欄位名),舊的 Column(from_node、to_node、actor_kind、actor_id、user_id)不存在。欄位契約見 omixor 文件櫃 DOC-34《core/store/ACTOR_CONTRACT.md》。
//
// 不含 omixor_server_features.updated_by(表沒有 org;服務層仍會寫它)。

// IdentityForm 欄位裡存的值是哪一種寫法
type IdentityForm string

const (
	// IdentityFree 自由字串:agent:<id>、user:<email>、含 @ 的 email、本機身分名稱…(空字串 = 沒有)
	IdentityFree IdentityForm = "free"
	// IdentityAgentID 純角色 id(沒有 agent: 前綴;omixor_issues.assignee)
	IdentityAgentID IdentityForm = "agentID"
	// IdentityActor 事件時間軸的執行者:Column 是 actor_id,種類在 KindColumn(actor_kind:ai / person / system)
	IdentityActor IdentityForm = "actor"
)

// IdentityField 一個「誰」的欄位
type IdentityField struct {
	Table      string
	Column     string
	Form       IdentityForm
	KindColumn string // 只有 IdentityActor 有:搭配的種類欄位
}

// IdentityFields 所有「誰」的欄位(19 個,14 張表)
var IdentityFields = []IdentityField{
	{"omixor_issues", "created_by", IdentityFree, ""},
	{"omixor_issues", "assignee", IdentityAgentID, ""},
	{"omixor_issue_comments", "author", IdentityFree, ""},
	{"omixor_docs", "author", IdentityFree, ""},
	{"omixor_agent_tasks", "created_by", IdentityFree, ""},
	{"omixor_agent_tasks", "claimed_by", IdentityFree, ""},
	{"omixor_agent_task_notes", "author", IdentityFree, ""},
	{"omixor_channels", "created_by", IdentityFree, ""},
	{"omixor_channel_members", "member", IdentityFree, ""},
	{"omixor_channel_messages", "author", IdentityFree, ""},
	{"omixor_channel_mentions", "member", IdentityFree, ""},
	{"omixor_channel_reads", "member", IdentityFree, ""},
	{"omixor_org_chain_edges", "from_node", IdentityFree, ""},
	{"omixor_org_chain_edges", "to_node", IdentityFree, ""},
	{"omixor_incidents", "commander", IdentityFree, ""},
	{"omixor_incidents", "created_by", IdentityFree, ""},
	{"omixor_incident_events", "actor_id", IdentityActor, "actor_kind"},
	{"omixor_org_integrations", "updated_by", IdentityFree, ""},
	{"omixor_org_system", "updated_by", IdentityFree, ""},
}

// ---- actor 欄位的逐欄描述(ISS-36 C-1;雲端 DOC-13 §1.4)----

// IdentityUserID 欄位裡存的是 omixor_users.id(u_…)或角色假使用者 agent:<id>(omixor_audit.user_id;DOC-13 §1.4 #22);空字串 = 沒有
const IdentityUserID IdentityForm = "userID"

// ActorColMode 一個欄位的舊寫法怎麼對應到最終結構的欄位
type ActorColMode string

const (
	// ActorColInPlace 原欄位原地換成 actor id(欄位名不變)
	ActorColInPlace ActorColMode = "inPlace"
	// ActorColRename 原地換成 actor id 之後欄位改名(Target;from_node → from_actor)
	ActorColRename ActorColMode = "rename"
	// ActorColCopy 舊欄位保留(這個版本不 drop),結果寫進新欄位 Target(事件時間軸的 actor、稽核的 actor)
	ActorColCopy ActorColMode = "copy"
)

// ActorColumn 一個「誰」的欄位(IdentityFields 的擴充:最終欄位名、可否 NULL、外鍵)
type ActorColumn struct {
	IdentityField
	Mode ActorColMode
	// Target 轉換後的欄位名(InPlace = 原欄位名;Rename、Copy = 新名字)
	Target string
	// Nullable 轉換後可為 NULL(舊值空字串 → NULL);false = 維持 NOT NULL(空字串 → sys_legacy_unknown)
	Nullable bool
	// FK 外鍵名稱(references omixor_actors(id) on delete restrict)
	FK string
}

// ActorColumns 22 個存 actor id 的欄位(IdentityFields 的 19 個 + omixor_issues.deleted_by、omixor_server_features.updated_by、omixor_audit.user_id)
var ActorColumns = []ActorColumn{
	{IdentityField{"omixor_issues", "created_by", IdentityFree, ""}, ActorColInPlace, "created_by", false, "fk_omixor_issues_created_by"},
	{IdentityField{"omixor_issues", "assignee", IdentityAgentID, ""}, ActorColInPlace, "assignee", true, "fk_omixor_issues_assignee"},
	{IdentityField{"omixor_issues", "deleted_by", IdentityFree, ""}, ActorColInPlace, "deleted_by", true, "fk_omixor_issues_deleted_by"},
	{IdentityField{"omixor_issue_comments", "author", IdentityFree, ""}, ActorColInPlace, "author", false, "fk_omixor_issue_comments_author"},
	{IdentityField{"omixor_docs", "author", IdentityFree, ""}, ActorColInPlace, "author", true, "fk_omixor_docs_author"},
	{IdentityField{"omixor_agent_tasks", "created_by", IdentityFree, ""}, ActorColInPlace, "created_by", true, "fk_omixor_agent_tasks_created_by"},
	{IdentityField{"omixor_agent_tasks", "claimed_by", IdentityFree, ""}, ActorColInPlace, "claimed_by", true, "fk_omixor_agent_tasks_claimed_by"},
	{IdentityField{"omixor_agent_task_notes", "author", IdentityFree, ""}, ActorColInPlace, "author", true, "fk_omixor_agent_task_notes_author"},
	{IdentityField{"omixor_channels", "created_by", IdentityFree, ""}, ActorColInPlace, "created_by", true, "fk_omixor_channels_created_by"},
	{IdentityField{"omixor_channel_members", "member", IdentityFree, ""}, ActorColInPlace, "member", false, "fk_omixor_channel_members_member"},
	{IdentityField{"omixor_channel_messages", "author", IdentityFree, ""}, ActorColInPlace, "author", false, "fk_omixor_channel_messages_author"},
	{IdentityField{"omixor_channel_mentions", "member", IdentityFree, ""}, ActorColInPlace, "member", false, "fk_omixor_channel_mentions_member"},
	{IdentityField{"omixor_channel_reads", "member", IdentityFree, ""}, ActorColInPlace, "member", false, "fk_omixor_channel_reads_member"},
	{IdentityField{"omixor_org_chain_edges", "from_node", IdentityFree, ""}, ActorColRename, "from_actor", false, "fk_omixor_org_chain_edges_from_actor"},
	{IdentityField{"omixor_org_chain_edges", "to_node", IdentityFree, ""}, ActorColRename, "to_actor", false, "fk_omixor_org_chain_edges_to_actor"},
	{IdentityField{"omixor_incidents", "commander", IdentityFree, ""}, ActorColInPlace, "commander", true, "fk_omixor_incidents_commander"},
	{IdentityField{"omixor_incidents", "created_by", IdentityFree, ""}, ActorColInPlace, "created_by", true, "fk_omixor_incidents_created_by"},
	{IdentityField{"omixor_incident_events", "actor_id", IdentityActor, "actor_kind"}, ActorColCopy, "actor", true, "fk_omixor_incident_events_actor"},
	{IdentityField{"omixor_org_integrations", "updated_by", IdentityFree, ""}, ActorColInPlace, "updated_by", true, "fk_omixor_org_integrations_updated_by"},
	{IdentityField{"omixor_org_system", "updated_by", IdentityFree, ""}, ActorColInPlace, "updated_by", true, "fk_omixor_org_system_updated_by"},
	{IdentityField{"omixor_server_features", "updated_by", IdentityFree, ""}, ActorColInPlace, "updated_by", true, "fk_omixor_server_features_updated_by"},
	{IdentityField{"omixor_audit", "user_id", IdentityUserID, ""}, ActorColCopy, "actor", true, "fk_omixor_audit_actor"},
}

// opts 這個欄位的寬鬆解析選項
func (a ActorColumn) opts() LegacyOpts {
	return LegacyOpts{Form: a.Form, NotNull: !a.Nullable}
}

// Key 欄位的識別字串(表.欄位,來源欄位名)
func (a ActorColumn) Key() string { return a.Table + "." + a.Column }

// NewTableActorField ISS-36 之後新增的表裡的「誰」欄位:新表直接以最終結構建立(actor id、有外鍵),不經過上面的轉換引擎(ActorColumns 只描述
// 「舊字串 → actor id」的欄位);外鍵在 fkSpecs(pg_fk.go)的一般關聯,不算 actor 外鍵。
// 測試用它確認每個欄位都有外鍵;新增這類表時登記在這裡。
type NewTableActorField struct {
	Table, Column string
	Nullable      bool   // 沒有時是 NULL(程式裡是空字串)
	FK            string // 外鍵名稱(references omixor_actors(id))
}

// NewTableActorFields 見 NewTableActorField
var NewTableActorFields = []NewTableActorField{
	{"omixor_project_members", "actor_id", false, "fk_project_members_actor"},
	{"omixor_decisions", "created_by", false, "fk_decisions_created_by"},
	{"omixor_decisions", "decided_by", true, "fk_decisions_decided_by"},
	{"omixor_issue_items", "assignee", true, "fk_issue_items_assignee"},
	{"omixor_issue_items", "claimed_by", true, "fk_issue_items_claimed_by"},
	{"omixor_issue_items", "created_by", false, "fk_issue_items_created_by"},
}
