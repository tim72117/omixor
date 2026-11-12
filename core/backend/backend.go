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

	// 成員
	ListMembers(org string) ([]model.Member, error)
	SaveMember(org, email, role string) (*model.Member, error)
	RemoveMember(org, email string) error

	// Issue
	ListIssues(org string) ([]model.Issue, error)
	GetIssue(org, id string) (*model.Issue, error)
	CreateIssue(org string, in service.IssueInput) (*model.Issue, error)
	UpdateIssue(org, id string, p service.IssuePatch) (*model.Issue, error)
	DeleteIssue(org, id string) error

	// 虛擬角色與任務(權限同 service/agents.go:管理角色與派任務要組織管理員)。實作在 agents.go
	ListAgents(org string) ([]model.Agent, error)
	GetAgent(org, id string) (*model.Agent, error) // id 或名稱
	SaveAgent(org string, in model.Agent) (*model.Agent, error)
	DeleteAgent(org, id string) error
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

	// 文件櫃(權限同 service/cabinet.go:成員讀寫文件、組織管理員改結構、角色身分依 docAccess)。實作在 cabinet.go
	// GetCabinet 結構與文件(新的在前);組織沒存過結構時是空白結構(LayoutSaved = false)
	GetCabinet(org string) (*model.Cabinet, error)
	SaveCabinetLayout(org string, layout []model.CabinetSection) ([]model.CabinetSection, error)
	// ImportCabinet 第一次搬資料用(組織管理員)
	ImportCabinet(org string, in service.CabinetImport) (*service.CabinetImportResult, error)
	GetDoc(org, id string) (*model.CabinetDoc, error) // 不分大小寫
	CreateDoc(org string, in service.DocInput) (*model.CabinetDoc, error)
	UpdateDoc(org, id string, p service.DocPatch) (*model.CabinetDoc, error)
	DeleteDoc(org, id string) error

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

func (Unsupported) ListOrgs() ([]model.Org, error)             { return nil, errUnsupported }
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
func (Unsupported) CreateIssue(string, service.IssueInput) (*model.Issue, error) {
	return nil, errUnsupported
}
func (Unsupported) UpdateIssue(string, string, service.IssuePatch) (*model.Issue, error) {
	return nil, errUnsupported
}
func (Unsupported) DeleteIssue(string, string) error              { return errUnsupported }
func (Unsupported) SaveAttachment(string, []byte) (string, error) { return "", errUnsupported }
func (Unsupported) GetAttachment(string, string) ([]byte, string, error) {
	return nil, "", errUnsupported
}
