package core

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// GitHub:用「GitHub」連線(網頁 OAuth,也可以改貼個人存取權杖)讀追蹤中 repo 的 PR、檢查與 Actions。同 Swift 的 GitHub/GitHub.swift。
// 只讀不寫:Ops 不會開 PR、留言、重跑 workflow。追蹤哪些 repo 存在目前組織的 github.json。

const GHConnectionID = "github"

// GHSettings github.json
type GHSettings struct {
	Connection string   `json:"connection"`
	Repos      []string `json:"repos"`
}

func DefaultGHSettings() GHSettings { return GHSettings{Repos: []string{}, Connection: GHConnectionID} }

// UnmarshalJSON 寬鬆解碼(同 Swift 的 init(from:):欄位壞掉就用預設值)
func (s *GHSettings) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = DefaultGHSettings()
	if v, ok := raw["repos"]; ok {
		var r []string
		if json.Unmarshal(v, &r) == nil && r != nil {
			s.Repos = r
		}
	}
	if v, ok := raw["connection"]; ok {
		var c string
		if json.Unmarshal(v, &c) == nil {
			s.Connection = c
		}
	}
	return nil
}

type GHRepo struct {
	DefaultBranch string `json:"defaultBranch"`
	Description   string `json:"description"`
	ID            string `json:"id"`
	IsPrivate     bool   `json:"isPrivate"`
	OpenIssues    int    `json:"openIssues"`
	PushedAt      *Time  `json:"pushedAt,omitempty"`
	Stars         int    `json:"stars"`
	URL           string `json:"url"`
}

type GHChecks struct {
	Failure int `json:"failure"`
	Pending int `json:"pending"`
	Skipped int `json:"skipped"`
	Success int `json:"success"`
	Total   int `json:"total"`
}

func (c GHChecks) State() string {
	switch {
	case c.Failure > 0:
		return "failure"
	case c.Pending > 0:
		return "pending"
	case c.Total == 0:
		return "none"
	}
	return "success"
}

func (c GHChecks) Label() string {
	switch c.State() {
	case "failure":
		return fmt.Sprintf("%d 個失敗", c.Failure)
	case "pending":
		return fmt.Sprintf("%d 個執行中", c.Pending)
	case "success":
		return fmt.Sprintf("%d 個通過", c.Success)
	}
	return "沒有檢查"
}

type GHPull struct {
	Author    string    `json:"author"`
	Base      string    `json:"base"`
	Branch    string    `json:"branch"`
	Checks    *GHChecks `json:"checks,omitempty"`
	CreatedAt *Time     `json:"createdAt,omitempty"`
	Draft     bool      `json:"draft"`
	HeadSHA   string    `json:"headSHA"`
	Labels    []string  `json:"labels"`
	Number    int       `json:"number"`
	Repo      string    `json:"repo"`
	Reviewers []string  `json:"reviewers"`
	Title     string    `json:"title"`
	UpdatedAt *Time     `json:"updatedAt,omitempty"`
	URL       string    `json:"url"`
}

type GHRun struct {
	Actor      string  `json:"actor"`
	Attempt    int     `json:"attempt"`
	Branch     string  `json:"branch"`
	Conclusion *string `json:"conclusion,omitempty"`
	CreatedAt  *Time   `json:"createdAt,omitempty"`
	Event      string  `json:"event"`
	ID         int     `json:"id"`
	Repo       string  `json:"repo"`
	RunNumber  int     `json:"runNumber"`
	SHA        string  `json:"sha"`
	StartedAt  *Time   `json:"startedAt,omitempty"`
	Status     string  `json:"status"`
	Title      string  `json:"title"`
	UpdatedAt  *Time   `json:"updatedAt,omitempty"`
	URL        string  `json:"url"`
	Workflow   string  `json:"workflow"`
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (r GHRun) IsFailure() bool {
	switch derefStr(r.Conclusion) {
	case "failure", "timed_out", "startup_failure":
		return true
	}
	return false
}

func (r GHRun) IsRunning() bool { return r.Status != "completed" }

// Duration 秒;還在跑或缺時間時 ok=false
func (r GHRun) Duration() (float64, bool) {
	if r.IsRunning() || r.UpdatedAt == nil {
		return 0, false
	}
	s := r.StartedAt
	if s == nil {
		s = r.CreatedAt
	}
	if s == nil {
		return 0, false
	}
	return r.UpdatedAt.Sub(s.Time).Seconds(), true
}

func (r GHRun) StateLabel() string {
	if r.IsRunning() {
		if r.Status == "queued" || r.Status == "waiting" || r.Status == "pending" {
			return "排隊中"
		}
		return "執行中"
	}
	switch derefStr(r.Conclusion) {
	case "success":
		return "成功"
	case "failure":
		return "失敗"
	case "cancelled":
		return "已取消"
	case "skipped":
		return "略過"
	case "timed_out":
		return "逾時"
	case "action_required":
		return "待處理"
	}
	if r.Conclusion != nil {
		return *r.Conclusion
	}
	return r.Status
}

type GHStep struct {
	Conclusion *string `json:"conclusion,omitempty"`
	Name       string  `json:"name"`
	Number     int     `json:"number"`
	Status     string  `json:"status"`
}

type GHJob struct {
	CompletedAt *Time    `json:"completedAt,omitempty"`
	Conclusion  *string  `json:"conclusion,omitempty"`
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	StartedAt   *Time    `json:"startedAt,omitempty"`
	Status      string   `json:"status"`
	Steps       []GHStep `json:"steps"`
	URL         string   `json:"url"`
}

type GHFile struct {
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Filename  string `json:"filename"`
	Status    string `json:"status"`
}

type GHPullDetail struct {
	Additions      int      `json:"additions"`
	Body           string   `json:"body"`
	ChangedFiles   int      `json:"changedFiles"`
	Deletions      int      `json:"deletions"`
	Files          []GHFile `json:"files"`
	MergeableState string   `json:"mergeableState"`
	Pull           GHPull   `json:"pull"`
}

// GHSnapshot 畫面上的 GitHub 狀態
type GHSnapshot struct {
	Busy      bool              `json:"busy"`
	Error     *string           `json:"error"`
	Errors    map[string]string `json:"errors"`
	FetchedAt *Time             `json:"fetchedAt"`
	Pulls     []GHPull          `json:"pulls"`
	Repos     []GHRepo          `json:"repos"`
	Runs      []GHRun           `json:"runs"`
}

func emptyGHSnapshot() GHSnapshot {
	return GHSnapshot{Errors: map[string]string{}, Pulls: []GHPull{}, Repos: []GHRepo{}, Runs: []GHRun{}}
}

// GHPresetConnection 預設的 GitHub 連線:網頁 OAuth。GitHub 不支援自動註冊,要先建 OAuth App 拿 client ID 與 secret
func GHPresetConnection() Connection {
	c := NewConnection(GHConnectionID, "GitHub", "https://api.github.com")
	c.TestPath = "/user"
	c.Note = "開發進度 › GitHub 讀 PR 與 Actions(只讀)"
	c.OAuth = OAuthSettings{Issuer: "https://github.com", AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		TokenEndpoint: "https://github.com/login/oauth/access_token", Scopes: "read:user"}
	return c
}

// GHRegisterURL 開 GitHub 的「新增 OAuth App」頁,先填好名稱與網址
func GHRegisterURL() string {
	q := []string{"oauth_application[name]=Ops", "oauth_application[url]=http://127.0.0.1", "oauth_application[callback_url]=http://127.0.0.1/callback"}
	for i, s := range q {
		k, v, _ := strings.Cut(s, "=")
		q[i] = url.QueryEscape(k) + "=" + v
	}
	return "https://github.com/settings/applications/new?" + strings.Join(q, "&")
}

// GHNormalizeRepo owner/name;也接受 https://github.com/owner/name(.git)
func GHNormalizeRepo(s string) (string, bool) {
	t := strings.TrimSpace(s)
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/", "git@github.com:"} {
		if strings.HasPrefix(t, p) {
			t = t[len(p):]
		}
	}
	t = strings.TrimSuffix(t, ".git")
	t = strings.TrimSuffix(t, "/")
	parts := []string{}
	for _, p := range strings.Split(t, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) != 2 {
		return "", false
	}
	for _, p := range parts {
		for _, r := range p {
			if !(unicode.IsLetter(r) || unicode.IsNumber(r) || strings.ContainsRune("-_.", r)) {
				return "", false
			}
		}
	}
	return strings.Join(parts, "/"), true
}

// MARK: 解析 GitHub 的 JSON

func ghDate(v any) *Time {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	t, err := ParseTime(s)
	if err != nil {
		return nil
	}
	return timePtr(t.Truncate(time.Millisecond))
}

func ghLogin(v any) string { return jStr(jMap(v)["login"]) }

func GHParseRepo(d map[string]any) GHRepo {
	return GHRepo{ID: jStr(d["full_name"]), Description: jStr(d["description"]), DefaultBranch: jStr(d["default_branch"]),
		IsPrivate: jBool(d["private"]), OpenIssues: jInt(d["open_issues_count"]), Stars: jInt(d["stargazers_count"]),
		PushedAt: ghDate(d["pushed_at"]), URL: jStr(d["html_url"])}
}

func GHParsePull(d map[string]any, repo string) GHPull {
	head, base := jMap(d["head"]), jMap(d["base"])
	labels, reviewers := []string{}, []string{}
	for _, l := range jMaps(d["labels"]) {
		labels = append(labels, jStr(l["name"]))
	}
	for _, r := range jMaps(d["requested_reviewers"]) {
		reviewers = append(reviewers, jStr(r["login"]))
	}
	return GHPull{Repo: repo, Number: jInt(d["number"]), Title: jStr(d["title"]), Author: ghLogin(d["user"]),
		Branch: jStr(head["ref"]), Base: jStr(base["ref"]), Draft: jBool(d["draft"]), Labels: labels, Reviewers: reviewers,
		HeadSHA: jStr(head["sha"]), CreatedAt: ghDate(d["created_at"]), UpdatedAt: ghDate(d["updated_at"]), URL: jStr(d["html_url"])}
}

func GHParseChecks(obj any) GHChecks {
	var c GHChecks
	for _, r := range jMaps(jMap(obj)["check_runs"]) {
		c.Total++
		if jStr(r["status"]) != "completed" {
			c.Pending++
			continue
		}
		switch jStr(r["conclusion"]) {
		case "success", "neutral":
			c.Success++
		case "skipped", "cancelled", "stale":
			c.Skipped++
		default:
			c.Failure++
		}
	}
	return c
}

func GHParseRun(d map[string]any, repo string) GHRun {
	title := jStr(d["display_title"])
	if title == "" {
		title = jStr(jMap(d["head_commit"])["message"])
	}
	return GHRun{ID: jInt(d["id"]), Repo: repo, Workflow: jStr(d["name"]), Title: title, Branch: jStr(d["head_branch"]),
		Event: jStr(d["event"]), Status: jStr(d["status"]), Conclusion: optString(d["conclusion"]), SHA: jStr(d["head_sha"]),
		Actor: ghLogin(d["actor"]), RunNumber: jInt(d["run_number"]), Attempt: max(1, jInt(d["run_attempt"])),
		CreatedAt: ghDate(d["created_at"]), StartedAt: ghDate(d["run_started_at"]), UpdatedAt: ghDate(d["updated_at"]),
		URL: jStr(d["html_url"])}
}

func GHParseJob(d map[string]any) GHJob {
	steps := []GHStep{}
	for _, s := range jMaps(d["steps"]) {
		steps = append(steps, GHStep{Number: jInt(s["number"]), Name: jStr(s["name"]), Status: jStr(s["status"]), Conclusion: optString(s["conclusion"])})
	}
	return GHJob{ID: jInt(d["id"]), Name: jStr(d["name"]), Status: jStr(d["status"]), Conclusion: optString(d["conclusion"]),
		StartedAt: ghDate(d["started_at"]), CompletedAt: ghDate(d["completed_at"]), URL: jStr(d["html_url"]), Steps: steps}
}

func GHParseFile(d map[string]any) GHFile {
	return GHFile{Filename: jStr(d["filename"]), Status: jStr(d["status"]), Additions: jInt(d["additions"]), Deletions: jInt(d["deletions"])}
}

// MARK: - 狀態(拿著鎖)

func (a *App) ghFile() string { return filepath.Join(a.DataDir(), "github.json") }

func ghLoad(a *App) {
	s := a.conns()
	s.GH = DefaultGHSettings()
	if b, err := ReadData(a.ghFile()); err == nil {
		var g GHSettings
		if json.Unmarshal(b, &g) == nil {
			s.GH = g
		}
	}
	s.GHSnap = emptyGHSnapshot()
}

func ghSave(a *App) { _ = WriteJSON(a.ghFile(), a.conns().GH) }

// GHSettingsNow 目前的設定(拿著鎖)
func GHSettingsNow(a *App) GHSettings {
	g := a.conns().GH
	g.Repos = append([]string{}, g.Repos...)
	return g
}

// GHSnapshotNow 目前的快照(拿著鎖)
func GHSnapshotNow(a *App) GHSnapshot { return a.conns().GHSnap }

// GHConnection 目前用的連線(拿著鎖)
func GHConnection(a *App) (Connection, bool) { return ConnFind(a, a.conns().GH.Connection) }

// ghLoggedIn 有連線且有 token(會讀鑰匙圈,不能拿著鎖)
func ghLoggedIn(a *App) bool {
	a.Mu.Lock()
	c, ok := GHConnection(a)
	a.Mu.Unlock()
	return ok && ConnHasToken(a, c)
}

// GHLoggedIn 同 Swift 的 GitHubStore.isLoggedIn(不能拿著鎖)
func GHLoggedIn(a *App) bool { return ghLoggedIn(a) }

// GHEnsureConnection 建立預設的 GitHub 連線(沒有才建)
func GHEnsureConnection(a *App) Connection {
	a.Mu.Lock()
	if c, ok := GHConnection(a); ok {
		a.Mu.Unlock()
		return c
	}
	a.Mu.Unlock()
	c := GHPresetConnection()
	ConnUpsert(a, c)
	a.Mu.Lock()
	s := a.conns()
	if s.GH.Connection != c.ID {
		s.GH.Connection = c.ID
		ghSave(a)
	}
	a.Mu.Unlock()
	a.Changed("github")
	return c
}

// GHAddRepo 追蹤 repo
func GHAddRepo(a *App, repo string) error {
	r, ok := GHNormalizeRepo(repo)
	if !ok {
		return connErr("repo 要寫成 owner/name,例如 tim72117/evoleap")
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.conns()
	for _, x := range s.GH.Repos {
		if strings.EqualFold(x, r) {
			return nil
		}
	}
	s.GH.Repos = append(s.GH.Repos, r)
	ghSave(a)
	go a.Changed("github")
	return nil
}

func GHRemoveRepo(a *App, r string) {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.conns()
	s.GH.Repos = filterStrings(s.GH.Repos, func(x string) bool { return x != r })
	snap := &s.GHSnap
	repos := snap.Repos[:0:0]
	for _, x := range snap.Repos {
		if x.ID != r {
			repos = append(repos, x)
		}
	}
	snap.Repos = repos
	pulls := []GHPull{}
	for _, x := range snap.Pulls {
		if x.Repo != r {
			pulls = append(pulls, x)
		}
	}
	snap.Pulls = pulls
	runs := []GHRun{}
	for _, x := range snap.Runs {
		if x.Repo != r {
			runs = append(runs, x)
		}
	}
	snap.Runs = runs
	delete(snap.Errors, r)
	ghSave(a)
	go a.Changed("github")
}

func filterStrings(in []string, keep func(string) bool) []string {
	out := []string{}
	for _, s := range in {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

// MARK: 讀 API(不能拿著鎖)

// GHGet GET 一個 GitHub API 路徑
func GHGet(a *App, path string) (any, error) {
	a.Mu.Lock()
	_, ok := GHConnection(a)
	id := a.conns().GH.Connection
	a.Mu.Unlock()
	if !ok {
		return nil, connErr("還沒設定 GitHub 連線(開發進度 › GitHub)")
	}
	return ConnRequest(a, id, "GET", path, nil)
}

func ghGetText(a *App, path string) (string, error) {
	a.Mu.Lock()
	id := a.conns().GH.Connection
	a.Mu.Unlock()
	r, err := connCheck(ConnRequestRaw(a, id, "GET", path, nil))
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(r.Data), "�"), nil
}

func ghRepoPath(r string) string { return "/repos/" + EscapePath(r) }

// GHRefresh 重新讀全部追蹤中的 repo
func GHRefresh(a *App) {
	a.Mu.Lock()
	s := a.conns()
	if s.GHSnap.Busy {
		a.Mu.Unlock()
		return
	}
	s.GHSnap.Busy = true
	s.GHSnap.Error = nil
	repos := append([]string{}, s.GH.Repos...)
	a.Mu.Unlock()
	a.Changed("github")
	defer func() {
		a.Mu.Lock()
		a.conns().GHSnap.Busy = false
		a.Mu.Unlock()
		a.Changed("github")
	}()
	setErr := func(msg string) {
		a.Mu.Lock()
		a.conns().GHSnap.Error = &msg
		a.Mu.Unlock()
	}
	if !ghLoggedIn(a) {
		setErr("還沒登入 GitHub")
		return
	}
	outRepos, pulls, runs := []GHRepo{}, []GHPull{}, []GHRun{}
	errs := map[string]string{}
	for _, r := range repos {
		repo, ps, rs, err := GHFetchRepo(a, r)
		if err == nil {
			outRepos = append(outRepos, repo)
			pulls = append(pulls, ps...)
			runs = append(runs, rs...)
			continue
		}
		if e, ok := AsConnError(err); ok && e.IsUnauthorized() {
			setErr(e.Message)
			return
		} else if ok && e.Status == 404 {
			errs[r] = "找不到(私有 repo 要在 GitHub 連線的 scopes 加 repo,或 OAuth App 沒有權限)"
		} else {
			errs[r] = err.Error()
		}
	}
	sort.SliceStable(pulls, func(i, j int) bool { return timeOrPast(pulls[i].UpdatedAt).After(timeOrPast(pulls[j].UpdatedAt)) })
	sort.SliceStable(runs, func(i, j int) bool { return timeOrPast(runs[i].CreatedAt).After(timeOrPast(runs[j].CreatedAt)) })
	a.Mu.Lock()
	a.conns().GHSnap = GHSnapshot{Repos: outRepos, Pulls: pulls, Runs: runs, FetchedAt: timePtr(time.Now()), Busy: true, Errors: errs}
	a.Mu.Unlock()
}

func timeOrPast(t *Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time
}

// GHFetchRepo 一個 repo 的資料、開著的 PR(含檢查)與最近的 Actions
func GHFetchRepo(a *App, r string) (GHRepo, []GHPull, []GHRun, error) {
	p := ghRepoPath(r)
	obj, err := GHGet(a, p)
	if err != nil {
		return GHRepo{}, nil, nil, err
	}
	rd, ok := obj.(map[string]any)
	if !ok {
		return GHRepo{}, nil, nil, connErr("repo 資料格式不對")
	}
	repo := GHParseRepo(rd)
	obj, err = GHGet(a, p+"/pulls?state=open&per_page=50")
	if err != nil {
		return GHRepo{}, nil, nil, err
	}
	pulls := []GHPull{}
	for _, d := range jMaps(obj) {
		pulls = append(pulls, GHParsePull(d, r))
	}
	for i := range pulls {
		if pulls[i].HeadSHA == "" {
			continue
		}
		if o, err := GHGet(a, p+"/commits/"+pulls[i].HeadSHA+"/check-runs?per_page=100"); err == nil {
			c := GHParseChecks(o)
			pulls[i].Checks = &c
		}
	}
	obj, err = GHGet(a, p+"/actions/runs?per_page=30")
	if err != nil {
		return GHRepo{}, nil, nil, err
	}
	runs := []GHRun{}
	for _, d := range jMaps(jMap(obj)["workflow_runs"]) {
		runs = append(runs, GHParseRun(d, r))
	}
	return repo, pulls, runs, nil
}

func GHJobs(a *App, repo string, run int) ([]GHJob, error) {
	obj, err := GHGet(a, fmt.Sprintf("%s/actions/runs/%d/jobs?per_page=50", ghRepoPath(repo), run))
	if err != nil {
		return nil, err
	}
	jobs := []GHJob{}
	for _, d := range jMaps(jMap(obj)["jobs"]) {
		jobs = append(jobs, GHParseJob(d))
	}
	return jobs, nil
}

// GHJobLogTail 失敗工作的紀錄最後幾行(GitHub 會轉址到暫時的下載網址,轉址時不帶 token)
func GHJobLogTail(a *App, repo string, job, lines int) (string, error) {
	text, err := ghGetText(a, fmt.Sprintf("%s/actions/jobs/%d/logs", ghRepoPath(repo), job))
	if err != nil {
		return "", err
	}
	all := strings.Split(text, "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}

func GHPullDetailGet(a *App, repo string, number int) (GHPullDetail, error) {
	p := fmt.Sprintf("%s/pulls/%d", ghRepoPath(repo), number)
	obj, err := GHGet(a, p)
	if err != nil {
		return GHPullDetail{}, err
	}
	d, ok := obj.(map[string]any)
	if !ok {
		return GHPullDetail{}, connErr("PR 資料格式不對")
	}
	pull := GHParsePull(d, repo)
	if pull.HeadSHA != "" {
		if o, err := GHGet(a, ghRepoPath(repo)+"/commits/"+pull.HeadSHA+"/check-runs?per_page=100"); err == nil {
			c := GHParseChecks(o)
			pull.Checks = &c
		}
	}
	obj, err = GHGet(a, p+"/files?per_page=100")
	if err != nil {
		return GHPullDetail{}, err
	}
	files := []GHFile{}
	for _, f := range jMaps(obj) {
		files = append(files, GHParseFile(f))
	}
	changed := len(files)
	if n, ok := jNum(d["changed_files"]); ok {
		changed = int(n)
	}
	return GHPullDetail{Pull: pull, Body: jStr(d["body"]), Additions: jInt(d["additions"]), Deletions: jInt(d["deletions"]),
		ChangedFiles: changed, MergeableState: jStr(d["mergeable_state"]), Files: files}, nil
}

// GHRunJSON 同 Swift MCP 的 runJSON:加上 state 與 durationSeconds
func GHRunJSON(r GHRun) map[string]any {
	o, _ := Object(r).(map[string]any)
	o["state"] = r.StateLabel()
	if d, ok := r.Duration(); ok {
		o["durationSeconds"] = int(d)
	}
	return o
}

// MARK: - 計時器、畫面

func init() {
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for range time.Tick(300 * time.Second) {
				a.Mu.Lock()
				busy := a.conns().GHSnap.Busy
				a.Mu.Unlock()
				if !busy && ghLoggedIn(a) {
					GHRefresh(a)
				}
			}
		}()
	})

	RegisterView("github", func(a *App) any {
		s := a.conns()
		snap := Object(s.GHSnap).(map[string]any)
		// 畫面方便用:Swift 的計算屬性(狀態文字、時間長度、檢查摘要)
		runs := []any{}
		for _, r := range s.GHSnap.Runs {
			o := GHRunJSON(r)
			o["isFailure"] = r.IsFailure()
			o["isRunning"] = r.IsRunning()
			runs = append(runs, o)
		}
		snap["runs"] = runs
		pulls := []any{}
		for _, p := range s.GHSnap.Pulls {
			o := Object(p).(map[string]any)
			if p.Checks != nil {
				o["checksState"] = p.Checks.State()
				o["checksLabel"] = p.Checks.Label()
			}
			pulls = append(pulls, o)
		}
		snap["pulls"] = pulls
		out := map[string]any{"settings": s.GH, "snapshot": snap, "connection": nil, "loggedIn": false, "state": nil,
			"registerURL": GHRegisterURL(), "homepage": "http://127.0.0.1", "callback": "http://127.0.0.1/callback"}
		if c, ok := GHConnection(a); ok {
			out["connection"] = c
			out["loggedIn"] = connHasTokenCached(a, c)
			out["state"] = connStateCopy(a, c.ID)
		}
		return out
	})

	RegisterAction("github_ensure_connection", func(a *App, args map[string]any) (any, error) {
		return GHEnsureConnection(a), nil
	})
	RegisterAction("github_add_repo", func(a *App, args map[string]any) (any, error) {
		r, _ := args["repo"].(string)
		if err := GHAddRepo(a, r); err != nil {
			return nil, err
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GHSettingsNow(a), nil
	})
	RegisterAction("github_remove_repo", func(a *App, args map[string]any) (any, error) {
		r, _ := args["repo"].(string)
		GHRemoveRepo(a, r)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GHSettingsNow(a), nil
	})
	RegisterAction("github_refresh", func(a *App, args map[string]any) (any, error) {
		GHRefresh(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GHSnapshotNow(a), nil
	})
	argInt := func(args map[string]any, k string) (int, bool) {
		switch x := args[k].(type) {
		case float64:
			return int(x), true
		case string:
			var n int
			_, err := fmt.Sscan(x, &n)
			return n, err == nil
		}
		return 0, false
	}
	repoArg := func(args map[string]any) (string, error) {
		r, _ := args["repo"].(string)
		n, ok := GHNormalizeRepo(r)
		if !ok {
			return "", UserError{"repo 要寫成 owner/name"}
		}
		return n, nil
	}
	RegisterAction("github_jobs", func(a *App, args map[string]any) (any, error) {
		repo, err := repoArg(args)
		if err != nil {
			return nil, err
		}
		run, ok := argInt(args, "run")
		if !ok {
			return nil, UserError{"要給 run"}
		}
		return GHJobs(a, repo, run)
	})
	RegisterAction("github_job_log", func(a *App, args map[string]any) (any, error) {
		repo, err := repoArg(args)
		if err != nil {
			return nil, err
		}
		job, ok := argInt(args, "job")
		if !ok {
			return nil, UserError{"要給 job"}
		}
		lines, ok := argInt(args, "lines")
		if !ok {
			lines = 120
		}
		tail, err := GHJobLogTail(a, repo, job, lines)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tail": tail, "text": tail}, nil
	})
	RegisterAction("github_pull", func(a *App, args map[string]any) (any, error) {
		repo, err := repoArg(args)
		if err != nil {
			return nil, err
		}
		n, ok := argInt(args, "number")
		if !ok {
			return nil, UserError{"要給 number"}
		}
		return GHPullDetailGet(a, repo, n)
	})
}
