package mcp

import (
	"net/http"
	"sync"

	"omixor-core/backend"
	coremcp "omixor-core/mcp"
	"omixor-core/service"
)

// 執行者端點 /mcp/worker:核心的 mcp.WorkerEndpoint(白名單只有 dispatch_queue、dispatch_claim、dispatch_status、dispatch_release)。
// 只聽 127.0.0.1,和 /mcp 一樣沒有額外驗證;後端是 web 目前的核心(appBackend → AgentOps → agentBackend):
//   - 本機模式:backend.Local + 本機核心,身分是本機 Principal(ClaimedBy = ops-web),credential.endpoint 是 http://127.0.0.1:<MCP 埠>/mcp/agent
//   - 遠端模式:backend.Remote 連雲端,身分是雲端帳號(ClaimedBy = 帳號 email),credential.endpoint 是 <雲端>/mcp/agent。
//     注意:本機的 /mcp/agent 在遠端模式一律 401,領到的憑證要連雲端的 /mcp/agent(endpoint 已指向雲端)。
// 組織是 web 目前選的組織。

var (
	workerOnce sync.Once
	workerH    http.Handler
)

// WorkerHTTPHandler 處理 /mcp/worker
func WorkerHTTPHandler(h *Handler) http.Handler {
	workerOnce.Do(func() {
		a := h.App
		workerH = coremcp.WorkerEndpoint(coremcp.WorkerEndpointConfig{
			Resolve: func(*http.Request) (backend.Backend, string, error) {
				a.Mu.Lock()
				org := a.CurrentOrgID()
				a.Mu.Unlock()
				if org == "" {
					return nil, "", &service.Error{Code: 404, Msg: "目前沒有選定的組織"}
				}
				return appBackend{a: a}, org, nil
			},
			AgentEndpointFn: func() string {
				a.Mu.Lock()
				defer a.Mu.Unlock()
				return a.AgentEndpointURL()
			},
		})
	})
	return workerH
}

