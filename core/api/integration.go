package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// integrationRoutes 組織的 GitHub、GitLab 設定(說明見 api.go 檔頭;規則在 service/integration.go)。
// 只有非機密設定:token 留在各裝置,讀 PR、Actions(GitLab 的 MR、Pipeline)也在各裝置,不經過這裡
func (a *API) integrationRoutes(mux *http.ServeMux) {
	gh := "/api/orgs/{org}/integrations/github"
	mux.HandleFunc("GET "+gh, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		x, err := a.Svc.GetGitHubSettings(p, r.PathValue("org"))
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+gh, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.GitHubSettingsInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.SaveGitHubSettings(p, r.PathValue("org"), in)
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+gh+"/import", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.GitHubImport
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.ImportGitHubSettings(p, r.PathValue("org"), in)
		reply(w, x, err)
	}))

	gl := "/api/orgs/{org}/integrations/gitlab"
	mux.HandleFunc("GET "+gl, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		x, err := a.Svc.GetGitLabSettings(p, r.PathValue("org"))
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+gl, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.GitLabSettingsInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.SaveGitLabSettings(p, r.PathValue("org"), in)
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+gl+"/import", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.GitLabImport
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.ImportGitLabSettings(p, r.PathValue("org"), in)
		reply(w, x, err)
	}))
}
