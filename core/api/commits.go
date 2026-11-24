package api

import (
	"net/http"
	"strconv"

	"omixor-core/model"
	"omixor-core/service"
)

// commitRoutes commits 與 Issue 的連結(ISS-69 phase 1)的路由。規則(驗證、遮罩、引用解析、可見性)在 service/commits.go。
// 全部只收 omx_ 使用者 token([a]);角色憑證 401/403。
//
//	POST   /api/orgs/{org}/commits                  {repo, source?, commits:[{sha, subject, body?, authorName?, authoredAt?, committedAt?, parents?, branch?, url?}]}
//	                                                → {reported, created, linked:[{issue, sha, kind}], unmatched:[]}(一次一個 repo、最多 100 筆;冪等)
//	GET    /api/orgs/{org}/commits?repo=&sha=&issue=&query=&limit=   → {total, commits:[…, sha7, issues:[{issue, kind}]]}(新的在前;sha 是至少 7 碼的前綴;limit 預設 20、最多 200)
//	GET    /api/orgs/{org}/commits/sync?repo=       → {repo, lastSha, lastAt?, lastSource?}(這個 repo 最近回報到哪;lastSha 空字串 = 還沒有)
//	GET    /api/orgs/{org}/issues/{id}/commits?limit=   → {issue, total, commits:[{sha, sha7, subject, kind, origin, repo, url, …}]}(最近連結的在前;limit 預設 10)
//	POST   /api/orgs/{org}/issues/{id}/commits      {repo, sha, kind?(mentions|fixes|found_in|manual,預設 manual), note?} 手動連結(沒回報過的 sha 當裸參照)→ 連結
//	DELETE /api/orgs/{org}/issues/{id}/commits      {repo, sha} 取消連結
func (a *API) commitRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/orgs/{org}/commits", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.CommitReport
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		out, err := a.Svc.ReportCommits(p, r.PathValue("org"), in)
		reply(w, out, err)
	}))
	mux.HandleFunc("GET /api/orgs/{org}/commits", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		out, err := a.Svc.QueryCommits(p, r.PathValue("org"), service.CommitQueryInput{
			Repo: q.Get("repo"), SHA: q.Get("sha"), Issue: q.Get("issue"), Query: q.Get("query"), Limit: limit})
		reply(w, out, err)
	}))
	mux.HandleFunc("GET /api/orgs/{org}/commits/sync", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		out, err := a.Svc.GetRepoSync(p, r.PathValue("org"), r.URL.Query().Get("repo"))
		reply(w, out, err)
	}))
	mux.HandleFunc("GET /api/orgs/{org}/issues/{id}/commits", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		out, err := a.Svc.ListIssueCommits(p, r.PathValue("org"), r.PathValue("id"), limit)
		reply(w, out, err)
	}))
	mux.HandleFunc("POST /api/orgs/{org}/issues/{id}/commits", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.CommitLinkInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		out, err := a.Svc.LinkIssueCommit(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, out, err)
	}))
	mux.HandleFunc("DELETE /api/orgs/{org}/issues/{id}/commits", a.authed(func(w http.ResponseWriter, r *http.Request, p *model.Principal) {
		var in service.CommitUnlinkInput
		if err := read(r, &in); err != nil {
			bad(w, err)
			return
		}
		err := a.Svc.UnlinkIssueCommit(p, r.PathValue("org"), r.PathValue("id"), in)
		reply(w, map[string]bool{"ok": err == nil}, err)
	}))
}
