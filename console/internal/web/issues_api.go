package web

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"omixor-core/config"
	"omixor-core/model"
	"omixor-core/service"

	"omixor-web/internal/core"
)

// Issue 的資料 API:給 macOS App(Swift 版)設了「Issue 伺服器」時用(Issue 改存在這台 omixor-web,omixor-web 再存 PostgreSQL 或檔案)。
// 回傳原始的 Issue(createdBy 等是作者字串,不是顯示名稱),和 issues.json 的格式一樣。
//
//	GET  /api/issues/{org}                全部 Issue(新的在前)
//	POST /api/issues/{org}                建立 {title, body, priority, labels, source, link, repo?, author} → Issue
//	POST /api/issues/{org}/{id}           修改 {author, title, body, status, priority, labels, source, link, repo, comments}
//	                                      (給的欄位整個取代;狀態變了會記關閉時間,同 issue_update)→ Issue
//	POST /api/issues/{org}/{id}/delete    刪除
//
// {org} 是組織 id;是 omixor-web 目前的組織時,變更會出現在畫面與動態牆,其他組織只改資料。
// 作者:啟用 LDAP 時用登入者;用 API token(OMIXOR_API_TOKEN,Authorization: Bearer …)或本機模式時用 body 的 author
// (person id、"mcp" 或 "agent:<角色 id>";沒給是本機使用者)。

// apiToken 有設 OMIXOR_API_TOKEN 且請求帶了相同的 Bearer token
func apiToken(r *http.Request) bool {
	want := config.Current().Str(config.APIToken)
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

// issueVersionOf 呼叫者讀到的 Issue 版本:body 的 version,或標準的 If-Match(可帶引號、W/ 前綴)。
// 0 = 沒給(null、0、"*"、沒有標頭都算沒給,不檢查);負數、不是整數回錯誤(400)。body 的 version 有給正整數時優先
func issueVersionOf(in map[string]any, ifMatch string) (int, error) {
	if v, ok := in["version"]; ok && v != nil {
		f, isNum := v.(float64)
		if !isNum || f != math.Trunc(f) || f < 0 || f > math.MaxInt32 {
			return 0, errors.New("version 要是 Issue 的版本號(非負整數)")
		}
		if f > 0 {
			return int(f), nil
		}
	}
	m := strings.TrimSpace(ifMatch)
	m = strings.Trim(strings.TrimPrefix(m, "W/"), `"`)
	if m == "" || m == "*" {
		return 0, nil
	}
	n, err := strconv.Atoi(m)
	if err != nil || n < 0 {
		return 0, errors.New("If-Match 要是 Issue 的版本號(非負整數)")
	}
	return n, nil
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
		var list []model.Issue
		app.WithOrgIssues(o, func() { list = append([]model.Issue{}, app.Issues()...) })
		app.Mu.Unlock()
		if set, unk, active := issueAPIRestricted(r, app, o); active { // 登入的非管理員:受限專案的 Issue 拿掉(見 restricted.go)
			kept := []model.Issue{}
			for _, i := range list {
				if i.Project == "" || (!unk && !set[i.Project]) {
					kept = append(kept, i)
				}
			}
			list = kept
		}
		writeJSON(w, 200, map[string]any{"result": list})
	})

	mux.HandleFunc("POST /api/issues/{org}", func(w http.ResponseWriter, r *http.Request) {
		o, ok := org(w, r)
		if !ok {
			return
		}
		in, err := readArgs(r)
		if err != nil {
			badRequest(w, err)
			return
		}
		if set, unk, active := issueAPIRestricted(r, app, o); active && restrictedArg(in, set, unk) {
			notFoundRestricted(w)
			return
		}
		title := strings.TrimSpace(str(in, "title"))
		if title == "" {
			writeJSON(w, 400, map[string]any{"error": "要給 title"})
			return
		}
		priority := str(in, "priority")
		if priority != "" && !slices.Contains(model.IssuePriorities, priority) {
			writeJSON(w, 400, map[string]any{"error": "priority 必須是 " + strings.Join(model.IssuePriorities, "、") + " 之一"})
			return
		}
		labels, _ := strs(in, "labels")
		author := issueAPIAuthor(r, app, in)
		app.Mu.Lock()
		var issue model.Issue
		var failure error
		app.WithOrgIssues(o, func() {
			issue = app.CreateIssueIn(service.IssueInput{Title: str(in, "title"), Body: str(in, "body"), Priority: priority, Labels: labels,
				Source: str(in, "source"), Link: str(in, "link"), Author: author, Repo: str(in, "repo"), Project: projectArg(in)})
			if issue.ID == "" { // 核心沒存(專案不存在、專案沒有代號、伺服器連不上…):帶原因回去,不要回一份沒有 id 的草稿當成功
				failure = app.IssueRemoteErr()
			}
		})
		app.Mu.Unlock()
		if failure != nil {
			writeFail(w, r, failure)
			return
		}
		writeJSON(w, 200, map[string]any{"result": issue})
	})

	mux.HandleFunc("POST /api/issues/{org}/{id}", func(w http.ResponseWriter, r *http.Request) {
		o, ok := org(w, r)
		if !ok {
			return
		}
		in, err := readArgs(r)
		if err != nil {
			badRequest(w, err)
			return
		}
		if p, ok := in["priority"]; ok && !slices.Contains(model.IssuePriorities, fmt.Sprint(p)) {
			writeJSON(w, 400, map[string]any{"error": "priority 必須是 " + strings.Join(model.IssuePriorities, "、") + " 之一"})
			return
		}
		if st, ok := in["status"]; ok && !slices.Contains(model.IssueStatuses, fmt.Sprint(st)) {
			writeJSON(w, 400, map[string]any{"error": "status 必須是 open、closed 之一"})
			return
		}
		var patch model.Issue
		if err := core.ReadInto(in, &patch); err != nil {
			badRequest(w, err)
			return
		}
		// 給的留言沒有 id 或時間時補上(同以前的寬鬆讀法:id 是新的 UUID、時間是現在)
		for k := range patch.Comments {
			if patch.Comments[k].ID == "" {
				patch.Comments[k].ID = core.UUID()
			}
			if patch.Comments[k].At.IsZero() {
				patch.Comments[k].At = model.Time{Time: time.Now().UTC()}
			}
		}
		if _, ok := in["title"]; ok && strings.TrimSpace(patch.Title) == "" {
			writeJSON(w, 400, map[string]any{"error": "標題不能是空的"})
			return
		}
		author := issueAPIAuthor(r, app, in)
		id := r.PathValue("id")
		if issueAPIHidden(r, app, o, id) || issueAPIProjectHidden(r, app, o, in) { // 受限專案的 Issue、搬進受限專案:和不存在一樣
			notFoundRestricted(w)
			return
		}
		// 樂觀鎖:body 的 version,或標準的 If-Match 標頭(可以帶引號);有給而且和核心目前的版本不同,核心回 409(Data 有目前內容與版本)
		version, err := issueVersionOf(in, r.Header.Get("If-Match"))
		if err != nil {
			badRequest(w, err)
			return
		}
		app.Mu.Lock()
		var updated *model.Issue
		var failure error
		app.WithOrgIssues(o, func() {
			// version = 0:呼叫者沒給,不檢查(照舊直接套用);> 0:和核心目前的版本不同就 409
			update := func(id, author string, change func(*model.Issue)) *model.Issue {
				return app.UpdateIssueAtVersion(id, author, "", version, change)
			}
			if _, ok := in["comments"]; ok { // 給了整份留言:照 API 的約定整份取代(保留給的 id 與時間);版本照樣檢查
				update = func(id, author string, change func(*model.Issue)) *model.Issue {
					return app.UpdateIssueReplacingCommentsAtVersion(id, author, version, change)
				}
			}
			updated = update(id, author, func(i *model.Issue) {
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
				if _, ok := in["repo"]; ok {
					i.Repo = strings.TrimSpace(patch.Repo)
				}
				if _, ok := in["project"]; ok { // 搬專案("" = 搬回組織層級);核心發新的 key,舊的留在別名表
					i.Project = strings.TrimSpace(patch.Project)
				}
				if _, ok := in["comments"]; ok {
					i.Comments = patch.Comments
				}
			})
			if updated == nil {
				failure = app.IssueRemoteErr()
			}
		})
		app.Mu.Unlock()
		if updated == nil {
			if failure != nil { // 409 版本衝突(Data:current、currentVersion)、400 專案沒有代號… 原樣帶回去
				writeFail(w, r, failure)
				return
			}
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
		if issueAPIHidden(r, app, o, id) {
			notFoundRestricted(w)
			return
		}
		in, _ := readArgs(r) // 可以沒有 body;有的話 author 用來署名動態牆
		if in == nil {
			in = map[string]any{}
		}
		author := issueAPIAuthor(r, app, in)
		app.Mu.Lock()
		var failure error
		app.WithOrgIssues(o, func() { _, failure = app.TrashIssueAs(core.AuthorActor(author), id) })
		app.Mu.Unlock()
		if failure != nil { // 找不到 404、沒權限 403、連線錯誤… 照核心的狀態碼,不吞錯
			writeFail(w, r, failure)
			return
		}
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

// projectArg 請求裡的 project:有給(含空字串 = 不分專案)回指標,沒給回 nil(核心依 repo 的專案連結預設)
func projectArg(in map[string]any) *string {
	if _, ok := in["project"]; !ok {
		return nil
	}
	v, _ := in["project"].(string)
	return &v
}
