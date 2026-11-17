package mcp

import (
	"net/http"
	"sync"
	"sync/atomic"

	coremcp "omixor-core/mcp"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 角色端點 /mcp/agent:核心的 mcp.AgentEndpoint(驗證、白名單、署名、401/429 規則都在核心;
// 文件櫃工具與 agent_context 的文件櫃欄位也在核心,依憑證角色的 docAccess),這裡只掛上:
//   - 每個請求的統計(MCP 狀態),與處理完後重讀 Issue、角色資料、文件櫃(核心可能多了 Issue、留言、任務狀態、文件)
//
// 憑證在核心的資料庫(本機模式:本機 PostgreSQL)。遠端模式的角色憑證由雲端發、連雲端的 /mcp/agent,這裡一律 401。

// roleEndpoints 每個核心(service)一份角色端點(建立時要組工具表,不要每個請求都建)
var (
	roleMu        sync.Mutex
	roleEndpoints = map[*service.Service]http.Handler{}
)

func roleEndpoint(h *Handler, svc *service.Service) http.Handler {
	roleMu.Lock()
	defer roleMu.Unlock()
	if e := roleEndpoints[svc]; e != nil {
		return e
	}
	a := h.App
	var toolRan atomic.Bool
	e := coremcp.AgentEndpoint(coremcp.AgentEndpointConfig{
		Svc: svc,
		Begin: func() func() {
			a.Mu.Lock()
			a.MCP.Requests++
			now := core.Now()
			a.MCP.LastRequestAt = &now
			a.Mu.Unlock()
			return func() {
				if toolRan.Swap(false) {
					a.RoleCallFinished()
				}
				a.Changed("mcp")
			}
		},
		Tool: func(name string) {
			toolRan.Store(true)
			a.Mu.Lock()
			a.MCP.LastTool = name
			a.Mu.Unlock()
		},
	})
	roleEndpoints[svc] = e
	return e
}

// AgentHTTPHandler 處理 /mcp/agent
func AgentHTTPHandler(h *Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc := core.AgentService()
		if svc == nil { // 遠端模式或沒有資料庫:這裡沒有角色憑證
			w.Header().Set("WWW-Authenticate", `Bearer realm="omixor-agent"`)
			w.Header().Set("Connection", "close")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(401)
			_, _ = w.Write([]byte("Unauthorized"))
			return
		}
		roleEndpoint(h, svc).ServeHTTP(w, r)
	})
}
