package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// systemRoutes 功能開關的兩層:系統層(整台伺服器)與組織的系統設定(說明見 api.go 檔頭;規則在 service/system.go,清單在 model/features.go)
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
	// 系統層(整台伺服器一份):任何登入的使用者讀,伺服器管理員改
	sp := "/api/system/features"
	mux.HandleFunc("GET "+sp, a.authed(func(w http.ResponseWriter, r *http.Request, pr *model.Principal) {
		x, err := a.Svc.GetServerFeatures(pr)
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+sp, a.authed(func(w http.ResponseWriter, r *http.Request, pr *model.Principal) {
		var in service.SystemSettingsInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.SaveServerFeatures(pr, in)
		reply(w, x, err)
	}))
}
