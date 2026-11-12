package core

import (
	"strings"

	"omixor-core/model"
)

// 組織成員:接核心的成員功能(角色 admin、member)。
// 本機模式以 localPrincipal(伺服器管理員)呼叫核心的 service;遠端模式用 remoteClient 呼叫 omixor server,
// 權限由伺服器判斷,錯誤訊息原樣傳給畫面。

// MembersView 成員視圖
type MembersView struct {
	Org     string         `json:"org"`
	Remote  bool           `json:"remote"`
	CanEdit bool           `json:"canEdit"` // 本機一定可以;遠端要是這個組織的管理員
	Members []model.Member `json:"members"`
	Error   string         `json:"error,omitempty"`
}

func validMemberRole(role string) bool { return role == "admin" || role == "member" }

// listMembers 讀目前組織的成員(視圖在 Mu 下執行,不另外鎖)
func (a *App) membersView() MembersView {
	org := a.CurrentOrgID()
	v := MembersView{Org: org, Remote: IsRemote(), Members: []model.Member{}}
	if org == "" {
		v.Error = "還沒有組織"
		return v
	}
	if IsRemote() {
		c := remoteClient(IssueRemoteURL())
		list, err := c.ListMembers(serverOrgID(org))
		if err != nil {
			v.Error = err.Error()
			return v
		}
		if list != nil {
			v.Members = list
		}
		if orgs, err := c.ListOrgs(); err == nil {
			for _, o := range orgs {
				if o.ID == serverOrgID(org) {
					v.CanEdit = o.MyRole == "admin"
				}
			}
		}
		return v
	}
	l := localCore()
	if l == nil {
		v.Error = errNoDB.Error()
		return v
	}
	l.ensureOrg(org)
	list, err := l.svc.ListMembers(localPrincipal, org)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	if list != nil {
		v.Members = list
	}
	v.CanEdit = true
	return v
}

// currentOrgForMembers 動作用:短暫拿鎖讀目前的組織 id
func (a *App) currentOrgForMembers() string {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return a.CurrentOrgID()
}

func init() {
	RegisterView("members", func(a *App) any { return a.membersView() })
	RegisterAction("member_save", func(a *App, args map[string]any) (any, error) {
		email, _ := args["email"].(string)
		role, _ := args["role"].(string)
		email = strings.TrimSpace(email)
		if email == "" {
			return nil, UserError{"要給成員的 email"}
		}
		if role == "" {
			role = "member"
		}
		if !validMemberRole(role) {
			return nil, UserError{"角色只能是 admin 或 member"}
		}
		org := a.currentOrgForMembers()
		var m *model.Member
		var err error
		if IsRemote() {
			m, err = remoteClient(IssueRemoteURL()).SaveMember(serverOrgID(org), email, role)
		} else {
			l := localCore()
			if l == nil {
				return nil, errNoDB
			}
			l.ensureOrg(org)
			m, err = l.svc.SaveMember(localPrincipal, org, email, role)
		}
		if err != nil {
			return nil, err
		}
		a.Changed("members")
		return m, nil
	})
	RegisterAction("member_remove", func(a *App, args map[string]any) (any, error) {
		email, _ := args["email"].(string)
		email = strings.TrimSpace(email)
		if email == "" {
			return nil, UserError{"要給成員的 email"}
		}
		org := a.currentOrgForMembers()
		var err error
		if IsRemote() {
			err = remoteClient(IssueRemoteURL()).RemoveMember(serverOrgID(org), email)
		} else {
			l := localCore()
			if l == nil {
				return nil, errNoDB
			}
			l.ensureOrg(org)
			err = l.svc.RemoveMember(localPrincipal, org, email)
		}
		if err != nil {
			return nil, err
		}
		a.Changed("members")
		return nil, nil
	})
}
