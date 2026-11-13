package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// mcpRoutes 外部 MCP 閘道的登記(說明見 api.go 檔頭;規則在 service/mcp.go)。
// 只有登記與規則:實際連線外部 MCP 伺服器在 App;任何密鑰(token、標頭值、環境變數值)都不經過這裡
func (a *API) mcpRoutes(mux *http.ServeMux) {
	base := "/api/orgs/{org}/mcp-servers"
	mux.HandleFunc("GET "+base, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		x, err := a.Svc.GetMCP(p, r.PathValue("org"))
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+base, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.MCPServerInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.SaveMCPServer(p, r.PathValue("org"), in)
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/delete", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		err := a.Svc.DeleteMCPServer(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, map[string]any{"deleted": err == nil}, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/tools", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.MCPToolsInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.SaveMCPTools(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, x, err)
	}))
	mux.HandleFunc("POST /api/orgs/{org}/mcp-grants", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.MCPGrantsInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.SaveMCPGrants(p, r.PathValue("org"), in)
		reply(w, map[string]any{"agentId": in.AgentID, "grants": x}, err)
	}))
	mux.HandleFunc("POST /api/orgs/{org}/mcp-tools/resolve", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.MCPResolveInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.ResolveMCPTools(p, r.PathValue("org"), in)
		reply(w, x, err)
	}))
}
