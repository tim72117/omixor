package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// Local 核心的 service 跑在同一個程式裡,以 Principal 的身分呼叫(權限照 service 的規則)
type Local struct {
	Svc *service.Service
	P   *model.Principal
	// EnsureOrg 為 true 時,碰到 Issue 與附件操作前,組織還沒建就補建
	// (omixor-web 的組織是自己管的,本機沒有另外建組織的步驟)
	EnsureOrg bool
}

var _ Backend = (*Local)(nil)

func (l *Local) ensureOrg(org string) {
	if !l.EnsureOrg {
		return
	}
	if _, err := l.Svc.Store.GetOrg(org); err != nil {
		_ = l.Svc.Store.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: model.Now()})
	}
}

func (l *Local) ListOrgs() ([]model.Org, error) { return l.Svc.ListOrgs(l.P) }

func (l *Local) CreateOrg(in model.Org) (*model.Org, error) { return l.Svc.CreateOrgWith(l.P, in) }

// UpdateOrg 修改名稱、圖示、顏色、備註(id 不能改),回傳修改後的內容(同伺服器的 PUT /api/orgs/{org})
func (l *Local) UpdateOrg(in model.Org) (*model.Org, error) {
	if err := l.Svc.UpdateOrg(l.P, in); err != nil {
		return nil, err
	}
	o, err := l.Svc.Store.GetOrg(in.ID)
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (l *Local) DeleteOrg(id string) error { return l.Svc.DeleteOrg(l.P, id) }

func (l *Local) ListMembers(org string) ([]model.Member, error) {
	return l.Svc.ListMembers(l.P, org)
}

func (l *Local) SaveMember(org, email, role string) (*model.Member, error) {
	return l.Svc.SaveMember(l.P, org, email, role)
}

func (l *Local) RemoveMember(org, email string) error { return l.Svc.RemoveMember(l.P, org, email) }

func (l *Local) ListIssues(org string) ([]model.Issue, error) {
	l.ensureOrg(org)
	return l.Svc.ListIssues(l.P, org)
}

func (l *Local) GetIssue(org, id string) (*model.Issue, error) {
	l.ensureOrg(org)
	return l.Svc.GetIssue(l.P, org, id)
}

func (l *Local) CreateIssue(org string, in service.IssueInput) (*model.Issue, error) {
	l.ensureOrg(org)
	return l.Svc.CreateIssue(l.P, org, in)
}

func (l *Local) UpdateIssue(org, id string, p service.IssuePatch) (*model.Issue, error) {
	l.ensureOrg(org)
	return l.Svc.UpdateIssue(l.P, org, id, p)
}

func (l *Local) DeleteIssue(org, id string) error {
	l.ensureOrg(org)
	return l.Svc.DeleteIssue(l.P, org, id)
}

func (l *Local) SaveAttachment(org string, data []byte) (string, error) {
	l.ensureOrg(org)
	return l.Svc.SaveAttachment(l.P, org, data)
}

func (l *Local) GetAttachment(org, id string) ([]byte, string, error) {
	l.ensureOrg(org)
	return l.Svc.GetAttachment(l.P, org, id)
}
