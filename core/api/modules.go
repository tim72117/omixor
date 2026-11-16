package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/module"
)

// moduleRoutes 選用模組(core/module)的路由:這次編譯有註冊的模組才有,沒編進來的模組路由不存在(404)。
//
//	GET  /api/modules                          這台伺服器編進來的選用模組 [{id, name, description?, features}](任何登入的使用者;角色憑證也可以)
//	                                           用戶端可以用它決定顯示哪些設定頁;功能本身照 /api/orgs/{org}/system 的清單
func (a *API) moduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/modules", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		reply(w, module.Infos(), nil)
	}))
	kit := module.API{
		Svc:           a.Svc,
		Authed:        func(h module.Handler) http.HandlerFunc { return a.authed(handler(h)) },
		AuthedOrAgent: func(h module.Handler) http.HandlerFunc { return a.authedOrAgent(handler(h)) },
		Reply:         reply,
		Read:          read,
		Bad:           bad,
	}
	for _, m := range module.All() {
		if m.Routes != nil {
			m.Routes(mux, kit)
		}
	}
}
