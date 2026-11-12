package api

import (
	"net/http"
	"strings"

	"omixor-core/model"
	"omixor-core/service"
)

// chainRoutes 組織鏈與依鏈委派的路由(說明見 api.go 檔頭)
func (a *API) chainRoutes(mux *http.ServeMux) {
	ch := "/api/orgs/{org}/chain"
	mux.HandleFunc("GET "+ch, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		c, err := a.Svc.GetChain(p, r.PathValue("org"))
		reply(w, c, err)
	}))
	mux.HandleFunc("POST "+ch, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			Edges []model.ChainEdge `json:"edges"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		c, err := a.Svc.SaveChain(p, r.PathValue("org"), in.Edges)
		reply(w, c, err)
	}))
	// 角色委派:Authorization 是角色憑證(Bearer <agentId>:<secret>);伺服器管理員也可以用 omixor token 帶 from(、taskId)
	mux.HandleFunc("POST /api/orgs/{org}/agent-tasks/delegate", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.DelegateInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		t, err := a.Svc.DelegateTask(p, r.PathValue("org"), in)
		reply(w, t, err)
	}))
	// 執行者(領取者本人)以任務角色的身分委派:{code, to, title, instructions, priority}
	mux.HandleFunc("POST /api/orgs/{org}/agent-tasks/{id}/worker/delegate", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.WorkerDelegateInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		t, err := a.Svc.WorkerDelegate(p, r.PathValue("org"), r.PathValue("id"), in.Code, in.DelegateInput)
		reply(w, t, err)
	}))
}

// authedOrAgent 同 authed,但也接受角色憑證(Bearer <agentId>:<secret>,規則同角色端點:失敗一律 401,太多次 429)
func (a *API) authedOrAgent(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if !strings.HasPrefix(tok, service.TokenPrefix) {
			if id, secret, ok := strings.Cut(tok, ":"); ok {
				p, err := a.Svc.AuthenticateAgent(id, secret, service.ClientIP(r))
				if err != nil {
					reply(w, nil, err)
					return
				}
				h(w, r, p)
				return
			}
		}
		a.authed(h)(w, r)
	}
}
