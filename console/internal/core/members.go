package core

import (
	"strings"

	"omixor-core/model"
)

// 組織成員:接核心的成員功能(角色 admin、member)。
// 以本機使用者(l.P,伺服器管理員)呼叫核心的 service(網頁登入者的寫入用本人身分,見 caller.go),錯誤訊息原樣傳給畫面。

// MembersView 成員視圖
type MembersView struct {
	Org     string         `json:"org"`
	CanEdit bool           `json:"canEdit"` // 讀得到核心就是 true;網頁登入者能不能改由寫入時的身分決定
	Members []model.Member `json:"members"`
	Error   string         `json:"error,omitempty"`
}

func validMemberRole(role string) bool { return role == "admin" || role == "member" }

// listMembers 讀目前組織的成員(視圖在 Mu 下執行,不另外鎖)
func (a *App) membersView() MembersView {
	org := a.CurrentOrgID()
	v := MembersView{Org: org, Members: []model.Member{}}
	if org == "" {
		v.Error = "還沒有組織"
		return v
	}
	l := localCore()
	if l == nil {
		v.Error = errNoDB.Error()
		return v
	}
	ensureLocalOrg(l, org)
	list, err := l.Svc.ListMembers(l.P, org)
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
		l, lerr := localCoreFor(CallerOf(args), org)
		if lerr != nil {
			return nil, lerr
		}
		ensureLocalOrg(l, org)
		m, err = l.Svc.SaveMember(l.P, org, email, role)
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
		l, lerr := localCoreFor(CallerOf(args), org)
		if lerr != nil {
			return nil, lerr
		}
		ensureLocalOrg(l, org)
		if err := l.Svc.RemoveMember(l.P, org, email); err != nil {
			return nil, err
		}
		a.Changed("members")
		return nil, nil
	})
}
