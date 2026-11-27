package core

import (
	"context"
	"time"
)

// ConnectionInfo 目前連線資訊(給 MCP server 用)。只含可以給人看的資訊,不含任何網址、token 或密鑰。
// mode 固定是 "local"(工具規格和 macOS App 共用,欄位保留)。會連資料庫(Ping),呼叫時不要拿著 Mu。
func (a *App) ConnectionInfo() map[string]any {
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
	out := map[string]any{"mode": "local", "local": map[string]any{"database": db}}
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
