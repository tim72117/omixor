package store

// ---- 資料庫裡所有「誰」的欄位(給 actor 遷移用的純資料清單;ISS-36 / 雲端 DOC-11) ----
//
// 資料裡「誰」(作者、建立者、指派對象、頻道成員…)原本都是自由字串,格式不一(純角色 id、user:<email>、不帶前綴的 email、
// mcp / routine / chain、本機身分…;規範化規則在 model/principal.go:PrincipalNode、PrincipalKind、AssigneeNode、ActorNode)。
// 身分登記層(omixor_principals + <欄位>_ref 產生欄位 + 外鍵)不部署,改為直接進 actor 欄位;這份清單記錄遷移要處理的全部欄位。
// 注意:IdentityFields / ActorColumns 描述的是「舊結構 → 最終結構」的轉換;最終結構(全新資料庫、轉換後)裡存在的是 ActorColumn.Target 欄位
// (InPlace 同名;Rename、Copy 是新欄位名),舊的 Column(from_node、to_node、actor_kind、actor_id、user_id)不存在。欄位契約見 ACTOR_CONTRACT.md。
//
// 不含 omixor_server_features.updated_by(表沒有 org;RenameIdentity 仍會改它)。

// IdentityForm 欄位裡存的值是哪一種寫法
type IdentityForm string

const (
	// IdentityFree 自由字串:agent:<id>、user:<email>、含 @ 的 email、本機身分名稱…(空字串 = 沒有)
	IdentityFree IdentityForm = "free"
	// IdentityAgentID 純角色 id(沒有 agent: 前綴;ops_issues.assignee)
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
	{"ops_issues", "created_by", IdentityFree, ""},
	{"ops_issues", "assignee", IdentityAgentID, ""},
	{"ops_issue_comments", "author", IdentityFree, ""},
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

// ---- actor 欄位遷移的逐欄描述(ISS-36 C-1;雲端 DOC-13 §1.4)----

// IdentityUserID 欄位裡存的是 omixor_users.id(u_…)或角色假使用者 agent:<id>(omixor_audit.user_id;DOC-13 §1.4 #22);空字串 = 沒有
const IdentityUserID IdentityForm = "userID"

// ActorColMode 一個欄位怎麼轉成 actor id
type ActorColMode string

const (
	// ActorColInPlace 原欄位原地換成 actor id(欄位名不變)
	ActorColInPlace ActorColMode = "inPlace"
	// ActorColRename 原地換成 actor id 之後欄位改名(Target;from_node → from_actor)
	ActorColRename ActorColMode = "rename"
	// ActorColCopy 舊欄位保留(這個版本不 drop),結果寫進新欄位 Target(事件時間軸的 actor、稽核的 actor)
	ActorColCopy ActorColMode = "copy"
)

// ActorColumn 一個「誰」的欄位怎麼遷移(IdentityFields 的擴充:新欄位名、可否 NULL、外鍵、主鍵衝突的保留規則)
type ActorColumn struct {
	IdentityField
	Mode ActorColMode
	// Target 轉換後的欄位名(InPlace = 原欄位名;Rename、Copy = 新名字)
	Target string
	// Nullable 轉換後可為 NULL(舊值空字串 → NULL);false = 維持 NOT NULL(空字串 → sys_legacy_unknown)
	Nullable bool
	// HasOrg 表有 org 欄位(omixor_server_features 沒有,只能對使用者與系統)
	HasOrg bool
	// FK 外鍵名稱(references omixor_actors(id) on delete restrict;欄位轉換完成後由引擎加,全新資料庫在同一次啟動補上)
	FK string
	// KeyCols 主鍵含這個欄位的表:同一個人兩種寫法轉換後會撞主鍵,要先刪重複。KeyCols 是主鍵裡除了 org 與這個欄位以外的欄位;
	// Keep 是留哪一筆的排序(order by 子句,排第一的留下)。空 = 這個欄位不在主鍵裡
	KeyCols []string
	Keep    string
}

// ActorColumns 22 個要轉成 actor id 的欄位(IdentityFields 的 19 個 + ops_issues.deleted_by、omixor_server_features.updated_by、omixor_audit.user_id)
var ActorColumns = []ActorColumn{
	{IdentityField{"ops_issues", "created_by", IdentityFree, ""}, ActorColInPlace, "created_by", false, true, "fk_ops_issues_created_by", nil, ""},
	{IdentityField{"ops_issues", "assignee", IdentityAgentID, ""}, ActorColInPlace, "assignee", true, true, "fk_ops_issues_assignee", nil, ""},
	{IdentityField{"ops_issues", "deleted_by", IdentityFree, ""}, ActorColInPlace, "deleted_by", true, true, "fk_ops_issues_deleted_by", nil, ""},
	{IdentityField{"ops_issue_comments", "author", IdentityFree, ""}, ActorColInPlace, "author", false, true, "fk_ops_issue_comments_author", nil, ""},
	{IdentityField{"omixor_docs", "author", IdentityFree, ""}, ActorColInPlace, "author", true, true, "fk_omixor_docs_author", nil, ""},
	{IdentityField{"omixor_agent_tasks", "created_by", IdentityFree, ""}, ActorColInPlace, "created_by", true, true, "fk_omixor_agent_tasks_created_by", nil, ""},
	{IdentityField{"omixor_agent_tasks", "claimed_by", IdentityFree, ""}, ActorColInPlace, "claimed_by", true, true, "fk_omixor_agent_tasks_claimed_by", nil, ""},
	{IdentityField{"omixor_agent_task_notes", "author", IdentityFree, ""}, ActorColInPlace, "author", true, true, "fk_omixor_agent_task_notes_author", nil, ""},
	{IdentityField{"omixor_channels", "created_by", IdentityFree, ""}, ActorColInPlace, "created_by", true, true, "fk_omixor_channels_created_by", nil, ""},
	{IdentityField{"omixor_channel_members", "member", IdentityFree, ""}, ActorColInPlace, "member", false, true, "fk_omixor_channel_members_member", []string{"channel_id"}, "x.pos, x.ctid"},
	{IdentityField{"omixor_channel_messages", "author", IdentityFree, ""}, ActorColInPlace, "author", false, true, "fk_omixor_channel_messages_author", nil, ""},
	{IdentityField{"omixor_channel_mentions", "member", IdentityFree, ""}, ActorColInPlace, "member", false, true, "fk_omixor_channel_mentions_member", []string{"channel_id", "message_id"}, "x.pos, x.ctid"},
	{IdentityField{"omixor_channel_reads", "member", IdentityFree, ""}, ActorColInPlace, "member", false, true, "fk_omixor_channel_reads_member", []string{"channel_id"}, "x.last_read desc, x.at desc, x.ctid"},
	{IdentityField{"omixor_org_chain_edges", "from_node", IdentityFree, ""}, ActorColRename, "from_actor", false, true, "fk_omixor_org_chain_edges_from_actor", nil, ""},
	{IdentityField{"omixor_org_chain_edges", "to_node", IdentityFree, ""}, ActorColRename, "to_actor", false, true, "fk_omixor_org_chain_edges_to_actor", nil, ""},
	{IdentityField{"omixor_incidents", "commander", IdentityFree, ""}, ActorColInPlace, "commander", true, true, "fk_omixor_incidents_commander", nil, ""},
	{IdentityField{"omixor_incidents", "created_by", IdentityFree, ""}, ActorColInPlace, "created_by", true, true, "fk_omixor_incidents_created_by", nil, ""},
	{IdentityField{"omixor_incident_events", "actor_id", IdentityActor, "actor_kind"}, ActorColCopy, "actor", true, true, "fk_omixor_incident_events_actor", nil, ""},
	{IdentityField{"omixor_org_integrations", "updated_by", IdentityFree, ""}, ActorColInPlace, "updated_by", true, true, "fk_omixor_org_integrations_updated_by", nil, ""},
	{IdentityField{"omixor_org_system", "updated_by", IdentityFree, ""}, ActorColInPlace, "updated_by", true, true, "fk_omixor_org_system_updated_by", nil, ""},
	{IdentityField{"omixor_server_features", "updated_by", IdentityFree, ""}, ActorColInPlace, "updated_by", true, false, "fk_omixor_server_features_updated_by", nil, ""},
	{IdentityField{"omixor_audit", "user_id", IdentityUserID, ""}, ActorColCopy, "actor", true, true, "fk_omixor_audit_actor", nil, ""},
}

// Key 欄位的識別字串(表.欄位,來源欄位名)
func (a ActorColumn) Key() string { return a.Table + "." + a.Column }
