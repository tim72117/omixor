package core

import (
	"context"
	"time"
)

// ConnectionInfo 目前連線資訊(給 MCP server 用)。只含可以給人看的資訊:
// 遠端 server 只給名稱不給網址,也不含任何 token / 密鑰。會連資料庫(Ping),呼叫時不要拿著 Mu。
func (a *App) ConnectionInfo() map[string]any {
	m := currentMode()
	out := map[string]any{"mode": m.Mode}
	if m.Mode == ModeRemote {
		conn := map[string]any{}
		if sc, ok := findServer(m.Server); ok {
			conn["server"] = sc.Name
			conn["serverId"] = sc.ID
		}
		login := map[string]any{"loggedIn": false}
		if u, errText := getRemoteMe(); u != nil {
			login["loggedIn"] = true
			login["email"] = u.Email
			login["name"] = u.Name
			login["role"] = u.Role
		} else if errText != "" {
			login["error"] = errText
		}
		if t := remoteToken(IssueRemoteURL()); t != nil && t.AccessToken != "" && t.ExpiresAt != nil {
			login["tokenExpiresAt"] = ISO(t.ExpiresAt.Time)
		}
		conn["login"] = login
		out["remote"] = conn
	} else {
		db := map[string]any{"type": "postgres", "connected": false}
		if p := DB(); p != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := p.Ping(ctx)
			cancel()
			db["connected"] = err == nil
			if err != nil {
				db["error"] = err.Error()
			}
		} else {
			db["error"] = errNoDB.Error()
		}
		out["local"] = map[string]any{"database": db}
	}
	a.Mu.Lock()
	cur := a.CurrentOrgID()
	org := map[string]any{"id": cur}
	for _, o := range a.Orgs().Orgs {
		if o.ID == cur {
			org["name"] = o.Name
		}
	}
	a.Mu.Unlock()
	if cur != "" {
		out["org"] = org
	}
	return out
}
