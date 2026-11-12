// Package store 是核心的存放介面。正式用 PostgreSQL(pg.go),測試用記憶體(mem.go)。
// 這一層只存取資料,不判斷權限(權限在 service)。
package store

import (
	"errors"
	"time"

	"omixor-core/model"
)

var ErrNotFound = errors.New("not found")

// ErrLocked 領取任務時,任務已被別人鎖住(進行中且 lockedUntil 還沒到)
var ErrLocked = errors.New("locked")

type Store interface {
	// 使用者、token、操作紀錄
	CountUsers() (int, error)
	ListUsers() ([]model.User, error)
	UserByID(id string) (*model.User, error)
	UserByEvoleapID(id string) (*model.User, error)
	UserByEmail(email string) (*model.User, error)
	SaveUser(u model.User) error
	CreateToken(t model.Token) error
	TokenByHash(h []byte, now time.Time) (*model.Token, *model.User, error) // 沒撤銷、沒過期的
	TouchToken(id string, at time.Time) error
	RevokeToken(id string, at time.Time) error
	ListTokens(now time.Time) ([]model.Token, error)
	AddAudit(e model.AuditEntry) error
	ListAudit(limit int) ([]model.AuditEntry, error)

	// 組織與成員
	ListOrgs() ([]model.Org, error)
	GetOrg(id string) (*model.Org, error)
	CreateOrg(o model.Org) error
	// UpdateOrg 更新名稱、圖示、顏色、備註(不存在回 ErrNotFound)
	UpdateOrg(o model.Org) error
	// DeleteOrg 連成員與 Issue 一起刪掉(不能復原)
	DeleteOrg(id string) error
	MemberRole(org, userID string) (string, error) // 不是成員回傳 ErrNotFound
	ListMembers(org string) ([]model.Member, error)
	SaveMember(m model.Member) error
	RemoveMember(org, userID string) error
	OrgsOfUser(userID string) ([]model.Org, error) // MyRole 是成員角色

	// Issue(一個組織一份清單,新的在前)
	ListIssues(org string) ([]model.Issue, error)
	GetIssue(org, id string) (*model.Issue, error)
	// CreateIssue 指定新的 id(ISS-<最大號碼+1>)並放在最前面,回傳存好的
	CreateIssue(org string, i model.Issue) (model.Issue, error)
	// UpdateIssue 整筆取代(含留言;位置不變)
	UpdateIssue(org string, i model.Issue) error
	DeleteIssue(org, id string) error
	// ImportIssues 第一次搬資料用:保留 id、作者與時間;list 是新的在前
	ImportIssues(org string, list []model.Issue) (int, error)
	// 附件(Issue 內文與留言裡的圖片):一個組織一份,id 是內容雜湊;同 id 再存不覆蓋
	PutAttachment(org, id string, data []byte) error
	GetAttachment(org, id string) ([]byte, error) // 沒有回 ErrNotFound
	// IssueOrgs 有 Issue 的組織 id(啟動時補建組織用)
	IssueOrgs() ([]string, error)

	// 虛擬角色、任務與憑證(依組織分;DeleteOrg 一併刪除)。id 比對精確(大小寫要一致),不分大小寫的查找在 service。
	ListAgents(org string) ([]model.Agent, error) // 依建立時間
	GetAgent(org, id string) (*model.Agent, error) // 沒有回 ErrNotFound
	SaveAgent(org string, a model.Agent) error     // 新增或整筆取代
	DeleteAgent(org, id string) error
	ListAgentTasks(org string) ([]model.AgentTask, error) // 新的在前
	GetAgentTask(org, id string) (*model.AgentTask, error)
	// CreateAgentTask 指定新的 id(T-<最大號碼+1>),回傳存好的
	CreateAgentTask(org string, t model.AgentTask) (model.AgentTask, error)
	UpdateAgentTask(org string, t model.AgentTask) error // 整筆取代
	// ClaimAgentTask 領取用的條件式整筆取代:任務目前是進行中且 lockedUntil 在 now 之後(被鎖住)就不改,回傳 ErrLocked
	ClaimAgentTask(org string, t model.AgentTask, now time.Time) error
	// ExtendAgentCredential 把任務憑證的到期時間延到 expires(沒有憑證就什麼都不做)
	ExtendAgentCredential(org, taskID string, expires time.Time) error
	// SaveAgentCredential 存憑證;同一個 (org, taskId) 舊的先刪(同任務只有一組有效憑證)
	SaveAgentCredential(c model.AgentCredential) error
	AgentCredentialByHash(h []byte) (*model.AgentCredential, error)
	// DeleteAgentCredentials 刪任務的憑證(taskID 不空)或某角色的全部憑證(agentID 不空)
	DeleteAgentCredentials(org, taskID, agentID string) error
	// PruneAgentCredentials 清掉到期的憑證
	PruneAgentCredentials(now time.Time) error

	// 文件櫃(依組織分;DeleteOrg 一併刪除)。id 比對精確,不分大小寫的查找在 service。
	// GetCabinetLayout 這個組織存過的結構;沒存過回 ErrNotFound
	GetCabinetLayout(org string) ([]model.CabinetSection, error)
	SaveCabinetLayout(org string, layout []model.CabinetSection) error
	ListDocs(org string) ([]model.CabinetDoc, error) // 新的在前
	GetDoc(org, id string) (*model.CabinetDoc, error)
	// CreateDoc 指定新的 id(DOC-<最大號碼+1>)並放在最前面,回傳存好的
	CreateDoc(org string, d model.CabinetDoc) (model.CabinetDoc, error)
	UpdateDoc(org string, d model.CabinetDoc) error // 整筆取代(位置不變)
	DeleteDoc(org, id string) error
	// ImportDocs 第一次搬資料用:保留 id、作者與時間;list 是新的在前;組織已經有文件就回錯誤
	ImportDocs(org string, list []model.CabinetDoc) (int, error)
}
