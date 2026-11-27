package core

import "strings"

// 使用者的停用與 MCP(OAuth)授權撤銷(限 web 管理員,見 web/access.go)。畫面之後再做,先給 API:
//
//	POST /api/action/user_oauth_revoke {email}             撤銷這個人全部的 MCP 授權(之後可以重新授權)
//	POST /api/action/user_disable      {email, disabled?}  停用(預設)或啟用;停用後不能登入 MCP,既有授權一併撤銷
//
// 使用者在這台的資料庫。停用的是核心使用者,不影響網頁版登入(那邊看 LDAP)

func userAdminArgs(args map[string]any) (string, error) {
	email, _ := args["email"].(string)
	if email = strings.TrimSpace(email); email == "" {
		return "", UserError{"要給使用者的 email"}
	}
	return email, nil
}

func (a *App) logUserAdmin(args map[string]any, text string) {
	by, _ := args[UserKey].(string)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.Log(Person(by), "lock", text, "settings")
}

func init() {
	RegisterAction("user_oauth_revoke", func(a *App, args map[string]any) (any, error) {
		email, err := userAdminArgs(args)
		if err != nil {
			return nil, err
		}
		l, err := localCoreFor(CallerOf(args), "")
		if err != nil {
			return nil, err
		}
		n, err := l.Svc.RevokeUserOAuth(l.P, email)
		if err != nil {
			return nil, err
		}
		a.logUserAdmin(args, "撤銷了 "+email+" 的 MCP 授權")
		return map[string]any{"revoked": n}, nil
	})
	RegisterAction("user_disable", func(a *App, args map[string]any) (any, error) {
		email, err := userAdminArgs(args)
		if err != nil {
			return nil, err
		}
		l, err := localCoreFor(CallerOf(args), "")
		if err != nil {
			return nil, err
		}
		disabled := true
		if v, ok := args["disabled"].(bool); ok {
			disabled = v
		}
		if err := l.Svc.SetUserDisabled(l.P, email, disabled); err != nil {
			return nil, err
		}
		text := "啟用了使用者 "
		if disabled {
			text = "停用了使用者 "
		}
		a.logUserAdmin(args, text+email)
		return map[string]any{"disabled": disabled}, nil
	})
}
