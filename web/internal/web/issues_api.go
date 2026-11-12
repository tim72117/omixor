package web

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"omixor-web/internal/core"
)

// Issue 的資料 API:給 Swift 版 Ops 設了「Issue 伺服器」時用(Issue 改存在這台 omixor-web,omixor-web 再存 PostgreSQL 或檔案)。
// 回傳原始的 Issue(createdBy 等是作者字串,不是顯示名稱),和 issues.json 的格式一樣。
//
//	GET  /api/issues/{org}                全部 Issue(新的在前)
//	POST /api/issues/{org}                建立 {title, body, priority, labels, source, link, author} → Issue
//	POST /api/issues/{org}/{id}           修改 {author, title, body, status, priority, labels, source, link, comments}
//	                                      (給的欄位整個取代;狀態變了會記關閉時間,同 ops_issue_update)→ Issue
//	POST /api/issues/{org}/{id}/delete    刪除
//
// {org} 是組織 id;是 omixor-web 目前的組織時,變更會出現在畫面與動態牆,其他組織只改資料。
// 作者:啟用 LDAP 時用登入者;用 API token(OPS_API_TOKEN,Authorization: Bearer …)或本機模式時用 body 的 author
// (person id、"mcp" 或 "agent:<角色 id>";沒給是本機使用者)。

// apiToken 有設 OPS_API_TOKEN 且請求帶了相同的 Bearer token
func apiToken(r *http.Request) bool {
	want := os.Getenv("OPS_API_TOKEN")
	if len(want) < 16 {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(want)) == 1
}

func issueAPIAuthor(r *http.Request, app *core.App, body map[string]any) string {
	if u, ok := ctxUser(r); ok {
		return u.ID
	}
	if s, ok := body["author"].(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	app.Mu.Lock()
	defer app.Mu.Unlock()
	return app.Me
}

func registerIssueAPI(mux *http.ServeMux, app *core.App) {
	org := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		o := r.PathValue("org")
		if !core.ValidOrgID(o) {
			writeJSON(w, 400, map[string]any{"error": "組織 id 不對:" + o})
			return "", false
		}
		return o, true
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	strs := func(m map[string]any, k string) ([]string, bool) {
		v, ok := m[k].([]any)
		if !ok {
			return nil, false
		}
		out := []string{}
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	}

	mux.HandleFunc("GET /api/issues/{org}", func(w http.ResponseWriter, r *http.Request) {
		o, ok := org(w, r)
		if !ok {
			return
		}
		app.Mu.Lock()
		var list []core.OpsIssue
		app.WithOrgIssues(o, func() { list = append([]core.OpsIssue{}, app.Issues()...) })
		app.Mu.Unlock()
		writeJSON(w, 200, map[string]any{"result": list})
	})

	mux.HandleFunc("POST /api/issues/{org}", func(w http.ResponseWriter, r *http.Request) {
		o, ok := org(w, r)
		if !ok {
			return
		}
		in, err := readArgs(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		title := strings.TrimSpace(str(in, "title"))
		if title == "" {
			writeJSON(w, 400, map[string]any{"error": "要給 title"})
			return
		}
		priority := str(in, "priority")
		if priority != "" && !slices.Contains(core.IssuePriorities, priority) {
			writeJSON(w, 400, map[string]any{"error": "priority 必須是 " + strings.Join(core.IssuePriorities, "、") + " 之一"})
			return
		}
		labels, _ := strs(in, "labels")
		author := issueAPIAuthor(r, app, in)
		app.Mu.Lock()
		var issue core.OpsIssue
		app.WithOrgIssues(o, func() {
			issue = app.CreateIssue(str(in, "title"), str(in, "body"), priority, labels, str(in, "source"), str(in, "link"), author)
		})
		app.Mu.Unlock()
		writeJSON(w, 200, map[string]any{"result": issue})
	})

	mux.HandleFunc("POST /api/issues/{org}/{id}", func(w http.ResponseWriter, r *http.Request) {
		o, ok := org(w, r)
		if !ok {
			return
		}
		in, err := readArgs(r)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if p, ok := in["priority"]; ok && !slices.Contains(core.IssuePriorities, fmt.Sprint(p)) {
			writeJSON(w, 400, map[string]any{"error": "priority 必須是 " + strings.Join(core.IssuePriorities, "、") + " 之一"})
			return
		}
		if st, ok := in["status"]; ok && !slices.Contains(core.IssueStatuses, fmt.Sprint(st)) {
			writeJSON(w, 400, map[string]any{"error": "status 必須是 open、closed 之一"})
			return
		}
		var patch core.OpsIssue
		if err := core.ReadInto(in, &patch); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if _, ok := in["title"]; ok && strings.TrimSpace(patch.Title) == "" {
			writeJSON(w, 400, map[string]any{"error": "標題不能是空的"})
			return
		}
		author := issueAPIAuthor(r, app, in)
		id := r.PathValue("id")
		app.Mu.Lock()
		var updated *core.OpsIssue
		app.WithOrgIssues(o, func() {
			updated = app.UpdateIssue(id, author, func(i *core.OpsIssue) {
				if _, ok := in["title"]; ok {
					i.Title = patch.Title
				}
				if _, ok := in["body"]; ok {
					i.Body = patch.Body
				}
				if _, ok := in["status"]; ok {
					i.Status = patch.Status
				}
				if _, ok := in["priority"]; ok {
					i.Priority = patch.Priority
				}
				if _, ok := in["labels"]; ok {
					i.Labels = patch.Labels
				}
				if _, ok := in["source"]; ok {
					i.Source = patch.Source
				}
				if _, ok := in["link"]; ok {
					i.Link = patch.Link
				}
				if _, ok := in["comments"]; ok {
					i.Comments = patch.Comments
				}
			})
		})
		app.Mu.Unlock()
		if updated == nil {
			writeJSON(w, 404, map[string]any{"error": "找不到 Issue " + id})
			return
		}
		writeJSON(w, 200, map[string]any{"result": updated})
	})

	mux.HandleFunc("POST /api/issues/{org}/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		o, ok := org(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		app.Mu.Lock()
		app.WithOrgIssues(o, func() { app.DeleteIssue(id) })
		app.Mu.Unlock()
		writeJSON(w, 200, map[string]any{"result": true})
	})
}

// IssueAPI 只有 /api/issues/… 的 handler(omixor-issues 服務用;認證由外層負責)。
// 請求的是目前組織時先重讀資料庫(多個用戶端同時寫也看得到彼此的變更);其他組織 WithOrgIssues 本來就每次重讀。
func IssueAPI(app *core.App) http.Handler {
	mux := http.NewServeMux()
	registerIssueAPI(mux, app)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/issues/"), "/")
		app.Mu.Lock()
		if org == app.CurrentOrgID() {
			app.ReloadIssues()
		}
		app.Mu.Unlock()
		mux.ServeHTTP(w, r)
	})
}
