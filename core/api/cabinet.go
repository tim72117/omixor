package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// cabinetRoutes 文件櫃的路由(說明見 api.go 檔頭)
func (a *API) cabinetRoutes(mux *http.ServeMux) {
	cb := "/api/orgs/{org}/cabinet"
	mux.HandleFunc("GET "+cb, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		c, err := a.Svc.GetCabinet(p, r.PathValue("org"))
		reply(w, c, err)
	}))
	mux.HandleFunc("POST "+cb+"/layout", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			Layout []model.CabinetSection `json:"layout"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		l, err := a.Svc.SaveCabinetLayout(p, r.PathValue("org"), in.Layout)
		reply(w, l, err)
	}))
	mux.HandleFunc("POST "+cb+"/import", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.CabinetImport
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.ImportCabinet(p, r.PathValue("org"), in)
		reply(w, res, err)
	}))

	dc := "/api/orgs/{org}/docs"
	mux.HandleFunc("GET "+dc, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		c, err := a.Svc.GetCabinet(p, r.PathValue("org"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		reply(w, c.Docs, nil)
	}))
	mux.HandleFunc("POST "+dc, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.DocInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		d, err := a.Svc.CreateDoc(p, r.PathValue("org"), in)
		reply(w, d, err)
	}))
	mux.HandleFunc("GET "+dc+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		d, err := a.Svc.GetDoc(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, d, err)
	}))
	mux.HandleFunc("POST "+dc+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.DocPatch
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		d, err := a.Svc.UpdateDoc(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, d, err)
	}))
	mux.HandleFunc("POST "+dc+"/{id}/delete", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		reply(w, true, a.Svc.DeleteDoc(p, r.PathValue("org"), r.PathValue("id")))
	}))
}
