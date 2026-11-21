package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// cabinetRoutes 文件櫃的路由(說明見 api.go 檔頭)
func (a *API) cabinetRoutes(mux *http.ServeMux) {
	// 專案:?project=<專案 id>(或 body 的 project);沒給 = 未分專案
	cb := "/api/orgs/{org}/cabinet"
	mux.HandleFunc("GET "+cb, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		c, err := a.Svc.GetCabinet(p, r.PathValue("org"), r.URL.Query().Get("project"))
		reply(w, c, err)
	}))
	mux.HandleFunc("POST "+cb+"/layout", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			Project         string                 `json:"project"`
			Layout          []model.CabinetSection `json:"layout"`
			ExpectedVersion *int64                 `json:"expectedVersion"` // 樂觀鎖;不帶 = 無鎖覆蓋(舊客戶端)
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		l, ver, err := a.Svc.SaveCabinetLayoutIf(p, r.PathValue("org"), queryOr(r, "project", in.Project), in.Layout, in.ExpectedVersion)
		if err != nil {
			reply(w, nil, err)
			return
		}
		writeJSON(w, 200, map[string]any{"result": l, "layoutVersion": ver})
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
		c, err := a.Svc.GetCabinet(p, r.PathValue("org"), r.URL.Query().Get("project"))
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
		in.Project = queryOr(r, "project", in.Project)
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
		if r.URL.Query().Has("project") && in.Project == nil { // 搬到別的專案(?project= 空字串 = 未分專案)
			v := r.URL.Query().Get("project")
			in.Project = &v
		}
		d, err := a.Svc.UpdateDoc(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, d, err)
	}))
	// 分段讀:{"outline"?, "section"?, "from"?, "to"?, "grep"?, "context"?};回傳大綱或那一部分與版本(見 service.DocRead)
	mux.HandleFunc("POST "+dc+"/{id}/read", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.DocRead
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		v, err := a.Svc.ReadDoc(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, v, err)
	}))
	// 局部修改:{"baseVersion"?, "edits"? | "replaceSection"+"content" | "appendToSection"+"content"}(見 service.DocEdit)
	mux.HandleFunc("POST "+dc+"/{id}/edit", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.DocEdit
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.EditDoc(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, res, err)
	}))
	mux.HandleFunc("POST "+dc+"/{id}/delete", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		reply(w, true, a.Svc.DeleteDoc(p, r.PathValue("org"), r.PathValue("id")))
	}))
}

// queryOr 網址參數有給(非空)就用它,否則用 body 的值
func queryOr(r *http.Request, key, body string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return body
}

// projectRoutes 專案的路由(說明見 api.go 檔頭)
func (a *API) projectRoutes(mux *http.ServeMux) {
	pj := "/api/orgs/{org}/projects"
	mux.HandleFunc("GET "+pj, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		l, err := a.Svc.ListProjects(p, r.PathValue("org"))
		reply(w, l, err)
	}))
	mux.HandleFunc("POST "+pj, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.ProjectInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.CreateProject(p, r.PathValue("org"), in)
		reply(w, x, err)
	}))
	mux.HandleFunc("GET "+pj+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		x, err := a.Svc.GetProject(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, x, err)
	}))
	mux.HandleFunc("PUT "+pj+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.ProjectPatch
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		x, err := a.Svc.UpdateProject(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, x, err)
	}))
	mux.HandleFunc("POST "+pj+"/{id}/delete", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		reply(w, true, a.Svc.DeleteProject(p, r.PathValue("org"), r.PathValue("id")))
	}))
}
