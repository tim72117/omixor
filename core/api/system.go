package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// systemRoutes 組織的系統設定(功能開關;說明見 api.go 檔頭;規則在 service/system.go,清單在 model/features.go)
func (a *API) systemRoutes(mux *http.ServeMux) {
	p := "/api/orgs/{org}/system"
	mux.HandleFunc("GET "+p, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, pr *model.Principal) {
		x, err := a.Svc.GetSystemSettings(pr, r.PathValue("org"))
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+p, a.authed(func(w http.ResponseWriter, r *http.Request, pr *model.Principal) {
		var in service.SystemSettingsInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.SaveSystemSettings(pr, r.PathValue("org"), in)
		reply(w, x, err)
	}))
}
