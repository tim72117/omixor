// Package store 是核心的存放介面。正式用 PostgreSQL(pg.go),測試用記憶體(mem.go)。
// 這一層只存取資料,不判斷權限(權限在 service)。
package store

import (
	"errors"
	"time"

	"omixor-core/model"
)

var ErrNotFound = errors.New("not found")

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
	// IssueOrgs 有 Issue 的組織 id(啟動時補建組織用)
	IssueOrgs() ([]string, error)
}
