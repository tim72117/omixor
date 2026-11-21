package api

import (
	"net/http"

	"omixor-core/model"
	"omixor-core/service"
)

// decisionRoutes 待決定 / 需要協助的路由(規則在 service/decision.go)。
// 看、提出、撤回(自己提的)角色憑證也可以;處理(answer)、重新開啟(reopen)、由決定開 Issue(to-issue)只有人(角色憑證 403)。
// 署名一律是呼叫者本人,請求裡沒有「誰」的欄位。有來源 Issue 的決定依該 Issue 的專案可見性篩選(看不到的單筆 404、列表沒有)。
//
//	GET  /api/orgs/{org}/decisions?status=&kind=&issue=&mine=1   列表
//	POST /api/orgs/{org}/decisions                               提出 {kind?, title, body, options?, sourceIssue?, blocks?}
//	GET  /api/orgs/{org}/decisions/summary                       每張來源 Issue(key)的 open / decided 筆數(只算呼叫者看得到的)
//	GET  /api/orgs/{org}/decisions/{id}
//	POST /api/orgs/{org}/decisions/{id}/answer                   {answer, note?, evidence?}
//	POST /api/orgs/{org}/decisions/{id}/withdraw | reopen
//	POST /api/orgs/{org}/decisions/{id}/to-issue                 {priority?, labels?, repo?, project?} → {issue, decision}
func (a *API) decisionRoutes(mux *http.ServeMux) {
	base := "/api/orgs/{org}/decisions"
	mux.HandleFunc("GET "+base, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		q := r.URL.Query()
		l, err := a.Svc.ListDecisions(p, r.PathValue("org"), service.DecisionListQuery{
			Status: q.Get("status"), Kind: q.Get("kind"), Issue: q.Get("issue"), Mine: q.Get("mine") == "1" || q.Get("mine") == "true"})
		reply(w, l, err)
	}))
	mux.HandleFunc("POST "+base, a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.DecisionInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		d, err := a.Svc.AddDecision(p, r.PathValue("org"), in)
		reply(w, d, err)
	}))
	mux.HandleFunc("GET "+base+"/summary", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		m, err := a.Svc.DecisionSummaries(p, r.PathValue("org"))
		reply(w, m, err)
	}))
	mux.HandleFunc("GET "+base+"/{id}", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		d, err := a.Svc.GetDecision(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, d, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/answer", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.DecisionAnswer
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		d, err := a.Svc.AnswerDecision(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, d, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/withdraw", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		d, err := a.Svc.WithdrawDecision(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, d, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/reopen", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		d, err := a.Svc.ReopenDecision(p, r.PathValue("org"), r.PathValue("id"))
		reply(w, d, err)
	}))
	mux.HandleFunc("POST "+base+"/{id}/to-issue", a.authedOrAgent(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.DecisionToIssueInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		i, d, err := a.Svc.DecisionToIssue(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, service.DecisionIssueResult{Issue: i, Decision: d}, err)
	}))
}
