// Package backend 是 omixor 核心操作(組織、成員、Issue、附件)的共同介面。
// 兩個實作行為與錯誤一致:
//
//	Local   包核心的 service.Service 與一個 Principal(同一個程式裡跑核心)
//	Remote  包 core/client(連線上的 omixor server)
//
// MCP 工具(core/mcp)與 omixor-web 只依賴這個介面,不管核心在本機還是雲端。
//
// 錯誤:伺服器端的錯誤(找不到、沒有權限、參數不對…)一律是 *service.Error,用 Code 取得狀態碼;
// 連不上伺服器等不是伺服器回的錯誤是一般 error(Code 回 0)。
package backend

import (
	"errors"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// Backend 核心的操作。org 是組織 id。
type Backend interface {
	// 組織
	ListOrgs() ([]model.Org, error)
	CreateOrg(in model.Org) (*model.Org, error)
	UpdateOrg(in model.Org) (*model.Org, error)
	DeleteOrg(id string) error

	// SchemaCheck 資料庫結構檢核(伺服器管理員,唯讀)
	SchemaCheck() (*model.SchemaReport, error)

	// 成員
	ListMembers(org string) ([]model.Member, error)
	SaveMember(org, email, role string) (*model.Member, error)
	RemoveMember(org, email string) error

	// Issue
	ListIssues(org string) ([]model.Issue, error)
	// QueryIssues 依條件篩選、排序(規則見 model.QueryIssues;零值同 ListIssues)
	QueryIssues(org string, q model.IssueQuery) ([]model.Issue, error)
	// IssueBoard 看板三欄(規則見 model.BuildIssueBoard)
	IssueBoard(org string, q model.IssueQuery) (*model.IssueBoard, error)
	GetIssue(org, id string) (*model.Issue, error)
	CreateIssue(org string, in service.IssueInput) (*model.Issue, error)
	UpdateIssue(org, id string, p service.IssuePatch) (*model.Issue, error)
	// AddIssueComment 新增一則留言(只附加這一則,不會蓋掉同時進來的留言);author 只有伺服器管理員的有效
	AddIssueComment(org, id, text, author string) (*model.Issue, error)
	// AcceptIssue 接受 Issue(呼叫者成為負責人、Issue 進行中;規則見 service/issue_accept.go),ReleaseIssue 放回(取消負責人、退回待處理)
	AcceptIssue(org, id string) (*model.Issue, error)
	ReleaseIssue(org, id string) (*model.Issue, error)
	// DeleteIssue 丟進垃圾桶(軟刪除);RestoreIssue 拿回來、PurgeIssue 永久刪除(組織管理員)。垃圾桶列表用 QueryIssues 帶 Deleted
	DeleteIssue(org, id string) error
	RestoreIssue(org, id string) (*model.Issue, error)
	PurgeIssue(org, id string) error

	// 虛擬角色與任務(權限同 service/agents.go:管理角色與派任務要組織管理員)。實作在 agents.go
	ListAgents(org string) ([]model.Agent, error)
	GetAgent(org, id string) (*model.Agent, error) // id 或名稱
	SaveAgent(org string, in model.Agent) (*model.Agent, error)
	DeleteAgent(org, id string) error
	// DeleteAgentTo 刪角色並先把名下任務轉給 transferTo(同一個交易);名下有任務而 transferTo 空 = 409(Error.Data 有各狀態的任務數)
	DeleteAgentTo(org, id, transferTo string) error
	// TransferAgentTasks 不刪角色的單獨轉移(組織管理員;scope all 全部 | open 只轉待領取與需要協助,空 = open)
	TransferAgentTasks(org string, in service.AgentTransferInput) (*service.AgentTransferResult, error)
	ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error)
	GetAgentTask(org, id string) (*model.AgentTask, error)
	AssignTask(org string, in service.TaskInput) (*model.AgentTask, error)
	// ClaimTask 領取任務並發一次性憑證;沒有可領的任務時回傳的 Task 是 nil
	ClaimTask(org string, in service.ClaimInput) (*service.ClaimResult, error)
	ReportTask(org, id string, in service.TaskReport) (*model.AgentTask, error)
	CancelTask(org, id string) (*model.AgentTask, error)
	RequeueTask(org, id string) (*model.AgentTask, error)
	// ClaimNext 從全部啟用中角色的待領取任務挑優先度最高(同優先度最舊)的一筆領走;agentID 非空就限定該角色。沒有可領的任務時回傳的 Task 是 nil
	ClaimNext(org, agentID string) (*service.ClaimResult, error)
	// ReleaseTask 放棄領取(進行中 → 待領取,作廢憑證、解鎖、留備註);只有領取者本人或組織管理員可以
	ReleaseTask(org, id, reason string) (*model.AgentTask, error)
	// WorkerReport、WorkerComment 領取者本人(人,不是角色憑證)以任務角色的身分回報任務、在 Issue 留言;
	// 任務要是進行中、租約還沒到期、而且是呼叫者領取的
	// code 是領取時拿到的 6 位數驗證碼(確認是這次領取的任務與角色)
	WorkerReport(org, id, code string, in service.TaskReport) (*model.AgentTask, error)
	WorkerComment(org, taskID, code, issueID, comment string) (*model.Issue, error)
	// Whoami 呼叫者的署名,和領取任務時寫進 ClaimedBy 的值一致(email 優先,否則名稱);不知道回空字串
	Whoami() string
	// RunRoutine 立刻替角色排一筆例行任務(組織管理員);author 是署名(只有伺服器管理員的有效)
	RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error)

	// 組織鏈(權限同 service/chain.go:成員讀、組織管理員改;依鏈委派要角色身分,或伺服器管理員帶 From)。實作在 chain.go
	GetChain(org string) (*model.OrgChain, error)
	SaveChain(org string, edges []model.ChainEdge) (*model.OrgChain, error)
	DelegateTask(org string, in service.DelegateInput) (*model.AgentTask, error)
	// WorkerDelegate 領取者本人以任務角色的身分依組織鏈委派(要領取驗證碼 code)
	WorkerDelegate(org, taskID, code string, in service.DelegateInput) (*model.AgentTask, error)

	// 文件櫃(權限同 service/cabinet.go:成員讀寫文件、組織管理員改結構、角色身分依 docAccess)。實作在 cabinet.go
	// GetCabinet 結構與文件(新的在前);組織沒存過結構時是空白結構(LayoutSaved = false)
	GetCabinet(org string) (*model.Cabinet, error)
	SaveCabinetLayout(org string, layout []model.CabinetSection, expectedVersion *int64) ([]model.CabinetSection, error)
	// ImportCabinet 第一次搬資料用(組織管理員)
	ImportCabinet(org string, in service.CabinetImport) (*service.CabinetImportResult, error)
	// GetProjectCabinet、SaveProjectCabinetLayout 指定專案(空 = 未分專案,同 GetCabinet、SaveCabinetLayout;不存在的專案 404)
	GetProjectCabinet(org, project string) (*model.Cabinet, error)
	SaveProjectCabinetLayout(org, project string, layout []model.CabinetSection, expectedVersion *int64) ([]model.CabinetSection, error)
	GetDoc(org, id string) (*model.CabinetDoc, error) // 不分大小寫
	// CreateDoc in.Project 是放進的專案(空 = 未分專案);UpdateDoc p.Project 有給就是搬到那個專案
	CreateDoc(org string, in service.DocInput) (*model.CabinetDoc, error)
	UpdateDoc(org, id string, p service.DocPatch) (*model.CabinetDoc, error)
	// ReadDoc、EditDoc 分段讀與局部修改(見 core/model/docsections.go)
	ReadDoc(org, id string, in service.DocRead) (*service.DocView, error)
	EditDoc(org, id string, in service.DocEdit) (*service.DocEditResult, error)
	DeleteDoc(org, id string) error

	// 專案(組織底下的一層,目前只有文件櫃分專案;權限同 service/project.go:成員看、組織管理員新增修改刪除)。實作在 project.go
	ListProjects(org string) ([]model.Project, error)
	CreateProject(org string, in service.ProjectInput) (*model.Project, error)
	UpdateProject(org, id string, p service.ProjectPatch) (*model.Project, error)
	// DeleteProject 專案裡還有文件時回 409
	DeleteProject(org, id string) error
	// LinkProjectRepo、UnlinkProjectRepo 專案與 repo 的連結(組織管理員;只用於預設值與篩選,見 model.ProjectRepo),回傳更新後的專案(含 Repos)。
	// ListProjectRepos 專案連結的 repo(project 空 = 整個組織)
	LinkProjectRepo(org, project, repo string) (*model.Project, error)
	UnlinkProjectRepo(org, project, repo string) (*model.Project, error)
	ListProjectRepos(org, project string) ([]model.ProjectRepo, error)
	// 專案成員(組織管理員或專案管理員管理;受限專案只有成員與組織管理員看得到,見 service/project_access.go)
	ListProjectMembers(org, project string) ([]model.ProjectMember, error)
	SetProjectMember(org, project string, in service.ProjectMemberInput) (*model.ProjectMember, error)
	RemoveProjectMember(org, project, actor string) error

	// 訊息頻道(權限同 service/channel.go:成員建頻道、看自己是成員的頻道,組織管理員看全部;角色身分只能看與發自己是成員的頻道)。
	// as 只有伺服器管理員給的算數(以角色或別人的身分)。實作在 channel.go
	ListChannels(org string, f service.ChannelFilter) ([]model.ChannelSummary, error)
	GetChannel(org, id, as string) (*model.ChannelSummary, error)
	CreateChannel(org string, in service.ChannelInput) (*model.ChannelSummary, error)
	UpdateChannel(org, id string, in service.ChannelPatch) (*model.ChannelSummary, error)
	ChangeChannelMembers(org, id string, in service.ChannelMembersInput) (*model.ChannelSummary, error)
	ListChannelMessages(org, id string, in service.MessageListInput) (*service.MessagePage, error)
	PostChannelMessage(org, id string, in service.PostInput) (*model.ChannelMessage, error)
	MarkChannelRead(org, id string, in service.ReadInput) (*model.ChannelRead, error)
	// ChannelInbox 提到 who(me、agent:<id>、user:<email>)的未讀訊息(新的在前最多 20 則)與它所在頻道的未讀數
	ChannelInbox(org, who string) (*service.ChannelInbox, error)

	// 事件(權限同 service/incident.go:成員看、宣告、更新;匯入要組織管理員)。實作在 incident.go
	ListIncidents(org string) ([]model.Incident, error)
	GetIncident(org, id string) (*model.Incident, error) // 不分大小寫
	DeclareIncident(org string, in service.IncidentInput) (*model.Incident, error)
	UpdateIncident(org, id string, in service.IncidentPatch) (*model.Incident, error)
	ImportIncidents(org string, list []model.Incident) (*model.ImportResult, error)

	// 待決定 / 需要協助(權限與可見性同 service/decision.go:成員看、提出;角色身分可以看與提出、撤回自己提的,不能處理;有來源 Issue 的決定依該 Issue 的專案可見性篩選;
	// 署名一律是呼叫者本人)。實作在 decision.go
	ListDecisions(org string, q service.DecisionListQuery) ([]model.Decision, error)
	GetDecision(org, id string) (*model.Decision, error) // 不分大小寫
	AddDecision(org string, in service.DecisionInput) (*model.Decision, error)
	AnswerDecision(org, id string, in service.DecisionAnswer) (*model.Decision, error)
	WithdrawDecision(org, id string) (*model.Decision, error)
	ReopenDecision(org, id string) (*model.Decision, error)
	DecisionToIssue(org, id string, in service.DecisionToIssueInput) (*service.DecisionIssueResult, error)
	DecisionSummaries(org string) (map[string]model.DecisionSummary, error)

	// Issue 子項(issue_item;權限與可見性同 service/issue_items.go:繼承所屬 Issue;角色身分可以看、新增、回報自己綁定任務的子項)。
	// issue 是 Issue 編號(key、id),item 是子項 key、序號或 id。實作在 issue_items.go
	ListIssueItems(org, issue string, q service.ItemListQuery) ([]model.IssueItem, error)
	GetIssueItem(org, issue, item string) (*model.IssueItem, error)
	AddIssueItem(org, issue string, in service.ItemInput) (*model.IssueItem, error)
	UpdateIssueItem(org, issue, item string, in service.ItemPatch) (*model.IssueItem, error)
	DeleteIssueItem(org, issue, item string) error
	ClaimIssueItem(org, issue, item string, in service.ItemClaimInput) (*service.ItemClaimResult, error)
	ReleaseIssueItem(org, issue, item string) (*model.IssueItem, error)
	ReportIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error)
	BlockIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error)
	UnblockIssueItem(org, issue, item string) (*model.IssueItem, error)
	CancelIssueItem(org, issue, item string) (*model.IssueItem, error)
	PromoteIssueItem(org, issue, item string, in service.ItemPromoteInput) (*service.ItemPromoteResult, error)

	// 組織的 GitHub、GitLab 設定搬到選用模組 core/modules/integrations(它自己的 Backend 介面與 Local / Remote 實作,見 module.go)

	// 組織的系統設定(功能開關;權限同 service/system.go:成員讀、組織管理員改)。實作在 system.go
	GetSystemSettings(org string) (*service.SystemSettingsView, error)
	SaveSystemSettings(org string, in service.SystemSettingsInput) (*service.SystemSettingsView, error)
	// 系統層的功能設定(整台伺服器一份;任何登入的使用者讀、伺服器管理員改)。實作在 system.go
	GetServerFeatures() (*service.ServerFeaturesView, error)
	SaveServerFeatures(in service.SystemSettingsInput) (*service.ServerFeaturesView, error)

	// 附件(圖片)
	SaveAttachment(org string, data []byte) (id string, err error)
	GetAttachment(org, id string) (data []byte, mime string, err error)
}

// Code 錯誤的 HTTP 狀態碼:伺服器端的錯誤是它的狀態碼;nil 是 0;連不上等其他錯誤是 0
func Code(err error) int {
	var e *service.Error
	if errors.As(err, &e) {
		return e.Code
	}
	var ce *client.Error
	if errors.As(err, &ce) {
		return ce.Status
	}
	return 0
}

// NotFound 是不是 404
func NotFound(err error) bool { return Code(err) == 404 }

// Unsupported 沒實作的操作一律回錯誤。給只實作一部分操作的 Backend 嵌入用。
type Unsupported struct{}

var errUnsupported = errors.New("這個模式不支援這個操作")

// Unsupported 錯誤是 Unsupported 的預設實作回的(這個模式沒有實作這個操作)
func IsUnsupported(err error) bool { return errors.Is(err, errUnsupported) }

func (Unsupported) ListOrgs() ([]model.Org, error)             { return nil, errUnsupported }
func (Unsupported) SchemaCheck() (*model.SchemaReport, error)  { return nil, errUnsupported }
func (Unsupported) CreateOrg(model.Org) (*model.Org, error)    { return nil, errUnsupported }
func (Unsupported) UpdateOrg(model.Org) (*model.Org, error)    { return nil, errUnsupported }
func (Unsupported) DeleteOrg(string) error                     { return errUnsupported }
func (Unsupported) ListMembers(string) ([]model.Member, error) { return nil, errUnsupported }
func (Unsupported) SaveMember(string, string, string) (*model.Member, error) {
	return nil, errUnsupported
}
func (Unsupported) RemoveMember(string, string) error             { return errUnsupported }
func (Unsupported) ListIssues(string) ([]model.Issue, error)      { return nil, errUnsupported }
func (Unsupported) GetIssue(string, string) (*model.Issue, error) { return nil, errUnsupported }
func (Unsupported) QueryIssues(string, model.IssueQuery) ([]model.Issue, error) {
	return nil, errUnsupported
}
func (Unsupported) IssueBoard(string, model.IssueQuery) (*model.IssueBoard, error) {
	return nil, errUnsupported
}
func (Unsupported) CreateIssue(string, service.IssueInput) (*model.Issue, error) {
	return nil, errUnsupported
}
func (Unsupported) UpdateIssue(string, string, service.IssuePatch) (*model.Issue, error) {
	return nil, errUnsupported
}
func (Unsupported) AcceptIssue(string, string) (*model.Issue, error)  { return nil, errUnsupported }
func (Unsupported) ReleaseIssue(string, string) (*model.Issue, error) { return nil, errUnsupported }
func (Unsupported) AddIssueComment(string, string, string, string) (*model.Issue, error) {
	return nil, errUnsupported
}
func (Unsupported) DeleteIssue(string, string) error { return errUnsupported }
func (Unsupported) RestoreIssue(string, string) (*model.Issue, error) {
	return nil, errUnsupported
}
func (Unsupported) PurgeIssue(string, string) error               { return errUnsupported }
func (Unsupported) SaveAttachment(string, []byte) (string, error) { return "", errUnsupported }
func (Unsupported) GetAttachment(string, string) ([]byte, string, error) {
	return nil, "", errUnsupported
}
