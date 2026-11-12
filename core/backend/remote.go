package backend

import (
	"errors"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// Remote 連線上的 omixor server(用 core/client)
type Remote struct {
	// Client 每次操作時取得 client(token 與換 token 的方式由呼叫的程式決定,可以隨時間改變)
	Client func() *client.Client
	// OrgID 把呼叫端的組織 id 換成伺服器上的 id(例如 omixor-web 的遠端組織有內部前綴);nil 是原樣
	OrgID func(org string) string
}

var _ Backend = (*Remote)(nil)

// NewRemote 固定用同一個 client
func NewRemote(c *client.Client) *Remote { return &Remote{Client: func() *client.Client { return c }} }

func (r *Remote) org(org string) string {
	if r.OrgID != nil {
		return r.OrgID(org)
	}
	return org
}

// wrap 伺服器回的錯誤轉成 *service.Error,和 Local 一致;連不上等(Status 0)原樣回傳
func wrap(err error) error {
	var ce *client.Error
	if errors.As(err, &ce) && ce.Status > 0 {
		return &service.Error{Code: ce.Status, Msg: ce.Msg}
	}
	return err
}

func (r *Remote) ListOrgs() ([]model.Org, error) {
	l, err := r.Client().ListOrgs()
	return l, wrap(err)
}

func (r *Remote) CreateOrg(in model.Org) (*model.Org, error) {
	o, err := r.Client().CreateOrgWith(in)
	if err != nil {
		return nil, wrap(err)
	}
	return o, nil
}

func (r *Remote) UpdateOrg(in model.Org) (*model.Org, error) {
	in.ID = r.org(in.ID)
	o, err := r.Client().UpdateOrg(in)
	if err != nil {
		return nil, wrap(err)
	}
	return o, nil
}

func (r *Remote) DeleteOrg(id string) error { return wrap(r.Client().DeleteOrg(r.org(id))) }

func (r *Remote) ListMembers(org string) ([]model.Member, error) {
	l, err := r.Client().ListMembers(r.org(org))
	return l, wrap(err)
}

func (r *Remote) SaveMember(org, email, role string) (*model.Member, error) {
	m, err := r.Client().SaveMember(r.org(org), email, role)
	if err != nil {
		return nil, wrap(err)
	}
	return m, nil
}

func (r *Remote) RemoveMember(org, email string) error {
	return wrap(r.Client().RemoveMember(r.org(org), email))
}

func (r *Remote) ListIssues(org string) ([]model.Issue, error) {
	l, err := r.Client().ListIssues(r.org(org))
	return l, wrap(err)
}

func (r *Remote) GetIssue(org, id string) (*model.Issue, error) {
	i, err := r.Client().GetIssue(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return i, nil
}

func (r *Remote) CreateIssue(org string, in service.IssueInput) (*model.Issue, error) {
	i, err := r.Client().CreateIssue(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return i, nil
}

func (r *Remote) UpdateIssue(org, id string, p service.IssuePatch) (*model.Issue, error) {
	i, err := r.Client().UpdateIssue(r.org(org), id, p)
	if err != nil {
		return nil, wrap(err)
	}
	return i, nil
}

func (r *Remote) DeleteIssue(org, id string) error {
	return wrap(r.Client().DeleteIssue(r.org(org), id))
}

func (r *Remote) SaveAttachment(org string, data []byte) (string, error) {
	id, err := r.Client().SaveAttachment(r.org(org), data)
	return id, wrap(err)
}

func (r *Remote) GetAttachment(org, id string) ([]byte, string, error) {
	b, t, err := r.Client().GetAttachment(r.org(org), id)
	return b, t, wrap(err)
}
