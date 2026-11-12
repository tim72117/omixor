package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// agentRoutes 虛擬角色與任務的路由(說明見 api.go 檔頭)
func (a *API) agentRoutes(mux *http.ServeMux) {
	ag := "/api/orgs/{org}/agents"
	mux.HandleFunc("GET "+ag, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		l, err := a.Svc.ListAgents(p, r.PathValue("org"))
		reply(w, l, err)
	}))
	mux.HandleFunc("POST "+ag, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		in := model.Agent{Active: true} // 沒給 active 視為啟用
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		g, err := a.Svc.SaveAgent(p, r.PathValue("org"), in)
		reply(w, g, err)
	}))
	mux.HandleFunc("GET "+ag+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		g, err := a.Svc.GetAgent(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, g, err)
	}))
	mux.HandleFunc("POST "+ag+"/{id}/delete", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		reply(w, true, a.Svc.DeleteAgent(p, r.PathValue("org"), r.PathValue("id")))
	}))

	// 立刻排一筆例行任務:body 可帶 {"author": "..."}(署名,只有伺服器管理員的有效)
	mux.HandleFunc("POST "+ag+"/{id}/routines/{rid}/run", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			Author string `json:"author"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		t, err := a.Svc.RunRoutine(p, r.PathValue("org"), r.PathValue("id"), r.PathValue("rid"), in.Author)
		reply(w, t, err)
	}))

	// 第一次搬資料:{"agents": [...], "tasks": [...]}(組織管理員;已有角色或任務回 409)
	mux.HandleFunc("POST /api/orgs/{org}/agents-import", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.AgentImport
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.ImportAgents(p, r.PathValue("org"), in)
		reply(w, res, err)
	}))

	tk := "/api/orgs/{org}/agent-tasks"
	mux.HandleFunc("GET "+tk, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		q := r.URL.Query()
		l, err := a.Svc.ListAgentTasks(p, r.PathValue("org"), service.TaskFilter{AgentID: q.Get("agent"), Status: q.Get("status"), ReportTo: q.Get("reportTo")})
		reply(w, l, err)
	}))
	mux.HandleFunc("POST "+tk, a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.TaskInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		t, err := a.Svc.AssignTask(p, r.PathValue("org"), in)
		reply(w, t, err)
	}))
	mux.HandleFunc("GET "+tk+"/{id}", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		t, err := a.Svc.GetAgentTask(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, t, err)
	}))
	mux.HandleFunc("POST "+tk+"/claim", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.ClaimInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.ClaimTask(p, r.PathValue("org"), in)
		reply(w, res, err)
	}))
	// 領最優先的一筆(全部啟用中角色;body {"agentId": "..."} 可限定角色)
	mux.HandleFunc("POST "+tk+"/claim-next", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			AgentID string `json:"agentId"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.ClaimNext(p, r.PathValue("org"), in.AgentID)
		reply(w, res, err)
	}))
	// 放棄領取:body {"reason": "..."}
	mux.HandleFunc("POST "+tk+"/{id}/release", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			Reason string `json:"reason"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		t, err := a.Svc.ReleaseTask(p, r.PathValue("org"), r.PathValue("id"), in.Reason)
		reply(w, t, err)
	}))
	mux.HandleFunc("POST "+tk+"/{id}/claim", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.ClaimInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		in.TaskID = r.PathValue("id")
		res, err := a.Svc.ClaimTask(p, r.PathValue("org"), in)
		reply(w, res, err)
	}))
	mux.HandleFunc("POST "+tk+"/{id}/report", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.TaskReport
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		t, err := a.Svc.ReportTask(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, t, err)
	}))
	// 執行者(領取者本人)以角色身分回報與留言,不需要角色憑證,但要帶領取驗證碼 code:
	// 回報 {"code", "status"?, "result"?, "note"?, "links"?};留言 {"code", "issueId", "comment"}
	mux.HandleFunc("POST "+tk+"/{id}/worker/report", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.WorkerReportInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		t, err := a.Svc.WorkerReport(p, r.PathValue("org"), r.PathValue("id"), in.Code, in.TaskReport)
		reply(w, t, err)
	}))
	mux.HandleFunc("POST "+tk+"/{id}/worker/comment", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in struct {
			Code    string `json:"code"`
			IssueID string `json:"issueId"`
			Comment string `json:"comment"`
		}
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		i, err := a.Svc.WorkerComment(p, r.PathValue("org"), r.PathValue("id"), in.Code, in.IssueID, in.Comment)
		reply(w, i, err)
	}))
	mux.HandleFunc("POST "+tk+"/{id}/cancel", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		t, err := a.Svc.CancelTask(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, t, err)
	}))
	mux.HandleFunc("POST "+tk+"/{id}/requeue", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		t, err := a.Svc.RequeueTask(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, t, err)
	}))
}
