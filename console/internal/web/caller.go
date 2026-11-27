package web

import (
	"context"
	"net/http"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
)

// callerOf 這個請求的登入者(給核心寫入署名與判斷權限用,見 core/caller.go);nil = 系統身分:
// 沒啟用登入、API token(沒有登入者)
func callerOf(r *http.Request, au *auth.Service) *core.Caller {
	if au == nil {
		return nil
	}
	if c, ok := r.Context().Value(callerKey{}).(*core.Caller); ok { // membershipGate 建好的
		return c
	}
	u, ok := ctxUser(r)
	if !ok {
		return nil
	}
	return newCaller(au, u)
}

func contextWithCaller(ctx context.Context, c *core.Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// newCaller 目錄設定和 MCP 的 OAuth 授權同一份(au.LDAPSettings),兩邊才會對到同一位核心使用者;沒啟用 LDAP 是 nil
func newCaller(au *auth.Service, u auth.User) *core.Caller {
	set, ok := au.LDAPSettings()
	if !ok {
		return nil
	}
	return core.NewCaller(u.ID, u.Name, u.Email, u.Admin, set)
}
