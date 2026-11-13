package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// templateRoutes 組織樣板(說明見 api.go 檔頭;規則在 service/templates.go,樣板定義在 model/orgtemplates.go)
func (a *API) templateRoutes(mux *http.ServeMux) {
	p := "/api/orgs/{org}/templates"
	mux.HandleFunc("GET "+p, a.authed(func(w http.ResponseWriter, r *http.Request, pr *model.Principal) {
		l, err := a.Svc.ListOrgTemplates(pr, r.PathValue("org"))
		reply(w, l, err)
	}))
	mux.HandleFunc("GET "+p+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, pr *model.Principal) {
		x, err := a.Svc.PreviewOrgTemplate(pr, r.PathValue("org"), r.PathValue("id"), r.URL.Query().Get("scenarios") == "1")
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+p+"/{id}/apply", a.authed(func(w http.ResponseWriter, r *http.Request, pr *model.Principal) {
		var in service.TemplateApplyInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.ApplyOrgTemplate(pr, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, x, err)
	}))
}
