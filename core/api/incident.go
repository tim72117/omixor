package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// incidentRoutes 事件的路由(說明見 api.go 檔頭;規則在 service/incident.go)。角色憑證也可以用(執行者記為該角色)
func (a *API) incidentRoutes(mux *http.ServeMux) {
	base := "/api/orgs/{org}/incidents"
	mux.HandleFunc("GET "+base, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		l, err := a.Svc.ListIncidents(p, r.PathValue("org"))
		reply(w, l, err)
	}))
	mux.HandleFunc("POST "+base, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.IncidentInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		i, err := a.Svc.DeclareIncident(p, r.PathValue("org"), in)
		reply(w, i, err)
	}))
	mux.HandleFunc("POST "+base+"/import", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.IncidentImport
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.ImportIncidents(p, r.PathValue("org"), in.Incidents)
		reply(w, res, err)
	}))
	mux.HandleFunc("GET "+base+"/{id}", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		i, err := a.Svc.GetIncident(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, i, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.IncidentPatch
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		i, err := a.Svc.UpdateIncident(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, i, err)
	}))
}
