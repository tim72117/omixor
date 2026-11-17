package integrations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"omixor-core/env"
	coreinteg "omixor-core/modules/integrations"
	"omixor-web/internal/core"
)

// GitHub:用「GitHub」連線(網頁 OAuth,也可以改貼個人存取權杖)讀追蹤中 repo 的 PR、檢查與 Actions。同 Swift 的 GitHub/GitHub.swift。
// 只讀不寫:omixor 不會開 PR、留言、重跑 workflow。追蹤哪些 repo 是組織的設定,存在核心(見 integrations.go);
// 這台的 github.json 只再寫用哪一個連線(token 在這台的加密 vault)。

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
	DefaultBranch string     `json:"defaultBranch"`
	Description   string     `json:"description"`
	ID            string     `json:"id"`
	IsPrivate     bool       `json:"isPrivate"`
	OpenIssues    int        `json:"openIssues"`
	PushedAt      *core.Time `json:"pushedAt,omitempty"`
	Stars         int        `json:"stars"`
	URL           string     `json:"url"`
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
	Author    string     `json:"author"`
	Base      string     `json:"base"`
	Branch    string     `json:"branch"`
	Checks    *GHChecks  `json:"checks,omitempty"`
	CreatedAt *core.Time `json:"createdAt,omitempty"`
	Draft     bool       `json:"draft"`
	HeadSHA   string     `json:"headSHA"`
	Labels    []string   `json:"labels"`
	Number    int        `json:"number"`
	Repo      string     `json:"repo"`
	Reviewers []string   `json:"reviewers"`
	Title     string     `json:"title"`
	UpdatedAt *core.Time `json:"updatedAt,omitempty"`
	URL       string     `json:"url"`
}

type GHRun struct {
	Actor      string     `json:"actor"`
	Attempt    int        `json:"attempt"`
	Branch     string     `json:"branch"`
	Conclusion *string    `json:"conclusion,omitempty"`
	CreatedAt  *core.Time `json:"createdAt,omitempty"`
	Event      string     `json:"event"`
	ID         int        `json:"id"`
	Repo       string     `json:"repo"`
	RunNumber  int        `json:"runNumber"`
	SHA        string     `json:"sha"`
	StartedAt  *core.Time `json:"startedAt,omitempty"`
	Status     string     `json:"status"`
	Title      string     `json:"title"`
	UpdatedAt  *core.Time `json:"updatedAt,omitempty"`
	URL        string     `json:"url"`
	Workflow   string     `json:"workflow"`
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
	CompletedAt *core.Time `json:"completedAt,omitempty"`
	Conclusion  *string    `json:"conclusion,omitempty"`
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	StartedAt   *core.Time `json:"startedAt,omitempty"`
	Status      string     `json:"status"`
	Steps       []GHStep   `json:"steps"`
	URL         string     `json:"url"`
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
	FetchedAt *core.Time        `json:"fetchedAt"`
	Pulls     []GHPull          `json:"pulls"`
	Repos     []GHRepo          `json:"repos"`
	Runs      []GHRun           `json:"runs"`
}

func emptyGHSnapshot() GHSnapshot {
	return GHSnapshot{Errors: map[string]string{}, Pulls: []GHPull{}, Repos: []GHRepo{}, Runs: []GHRun{}}
}

// GHPresetConnection 預設的 GitHub 連線:網頁 OAuth。GitHub 不支援自動註冊,要先建 OAuth App 拿 client ID 與 secret
func GHPresetConnection() core.Connection {
	c := core.NewConnection(GHConnectionID, "GitHub", "https://api.github.com")
	c.TestPath = "/user"
	c.Note = "開發進度 › Repositories 讀 PR 與 Actions(只讀)"
	c.OAuth = core.OAuthSettings{Issuer: "https://github.com", AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		TokenEndpoint: "https://github.com/login/oauth/access_token", Scopes: "read:user"}
	return c
}

// GHRegisterURL 開 GitHub 的「新增 OAuth App」頁,先填好名稱與網址
func GHRegisterURL() string {
	q := []string{"oauth_application[name]=omixor", "oauth_application[url]=http://127.0.0.1", "oauth_application[callback_url]=http://127.0.0.1/callback"}
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

func ghDate(v any) *core.Time {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	t, err := core.ParseTime(s)
	if err != nil {
		return nil
	}
	return core.TimePtr(t.Truncate(time.Millisecond))
}

func ghLogin(v any) string { return core.JStr(core.JMap(v)["login"]) }

func GHParseRepo(d map[string]any) GHRepo {
	return GHRepo{ID: core.JStr(d["full_name"]), Description: core.JStr(d["description"]), DefaultBranch: core.JStr(d["default_branch"]),
		IsPrivate: core.JBool(d["private"]), OpenIssues: core.JInt(d["open_issues_count"]), Stars: core.JInt(d["stargazers_count"]),
		PushedAt: ghDate(d["pushed_at"]), URL: core.JStr(d["html_url"])}
}

func GHParsePull(d map[string]any, repo string) GHPull {
	head, base := core.JMap(d["head"]), core.JMap(d["base"])
	labels, reviewers := []string{}, []string{}
	for _, l := range core.JMaps(d["labels"]) {
		labels = append(labels, core.JStr(l["name"]))
	}
	for _, r := range core.JMaps(d["requested_reviewers"]) {
		reviewers = append(reviewers, core.JStr(r["login"]))
	}
	return GHPull{Repo: repo, Number: core.JInt(d["number"]), Title: core.JStr(d["title"]), Author: ghLogin(d["user"]),
		Branch: core.JStr(head["ref"]), Base: core.JStr(base["ref"]), Draft: core.JBool(d["draft"]), Labels: labels, Reviewers: reviewers,
		HeadSHA: core.JStr(head["sha"]), CreatedAt: ghDate(d["created_at"]), UpdatedAt: ghDate(d["updated_at"]), URL: core.JStr(d["html_url"])}
}

func GHParseChecks(obj any) GHChecks {
	var c GHChecks
	for _, r := range core.JMaps(core.JMap(obj)["check_runs"]) {
		c.Total++
		if core.JStr(r["status"]) != "completed" {
			c.Pending++
			continue
		}
		switch core.JStr(r["conclusion"]) {
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
	title := core.JStr(d["display_title"])
	if title == "" {
		title = core.JStr(core.JMap(d["head_commit"])["message"])
	}
	return GHRun{ID: core.JInt(d["id"]), Repo: repo, Workflow: core.JStr(d["name"]), Title: title, Branch: core.JStr(d["head_branch"]),
		Event: core.JStr(d["event"]), Status: core.JStr(d["status"]), Conclusion: core.OptString(d["conclusion"]), SHA: core.JStr(d["head_sha"]),
		Actor: ghLogin(d["actor"]), RunNumber: core.JInt(d["run_number"]), Attempt: max(1, core.JInt(d["run_attempt"])),
		CreatedAt: ghDate(d["created_at"]), StartedAt: ghDate(d["run_started_at"]), UpdatedAt: ghDate(d["updated_at"]),
		URL: core.JStr(d["html_url"])}
}

func GHParseJob(d map[string]any) GHJob {
	steps := []GHStep{}
	for _, s := range core.JMaps(d["steps"]) {
		steps = append(steps, GHStep{Number: core.JInt(s["number"]), Name: core.JStr(s["name"]), Status: core.JStr(s["status"]), Conclusion: core.OptString(s["conclusion"])})
	}
	return GHJob{ID: core.JInt(d["id"]), Name: core.JStr(d["name"]), Status: core.JStr(d["status"]), Conclusion: core.OptString(d["conclusion"]),
		StartedAt: ghDate(d["started_at"]), CompletedAt: ghDate(d["completed_at"]), URL: core.JStr(d["html_url"]), Steps: steps}
}

func GHParseFile(d map[string]any) GHFile {
	return GHFile{Filename: core.JStr(d["filename"]), Status: core.JStr(d["status"]), Additions: core.JInt(d["additions"]), Deletions: core.JInt(d["deletions"])}
}

// MARK: - 狀態(拿著鎖)

func ghFile(a *core.App) string { return filepath.Join(a.DataDir(), "github.json") }

func ghLoad(a *core.App) {
	s := stateOf(a)
	s.GH = DefaultGHSettings()
	if b, err := core.ReadData(ghFile(a)); err == nil {
		var g GHSettings
		if json.Unmarshal(b, &g) == nil {
			s.GH = g
		}
	}
	s.GHSnap = emptyGHSnapshot()
	s.GHETags = nil
	// 追蹤的 repo 以組織(核心)的為準:先顯示這台檔案裡的,背景從核心讀(見 integrations.go)
	if core.BackgroundAccountCheck {
		go GHSyncSettings(a)
	}
}

// ghSave 只寫這台的 connection(repos 是組織的設定,存在核心;檔案裡原本的 repos 保留,見 integrations.go)
func ghSave(a *core.App) { ghSaveConnection(a) }

// GHSettingsNow 目前的設定(拿著鎖)
func GHSettingsNow(a *core.App) GHSettings {
	g := stateOf(a).GH
	g.Repos = append([]string{}, g.Repos...)
	return g
}

// GHSnapshotNow 目前的快照(拿著鎖)
func GHSnapshotNow(a *core.App) GHSnapshot { return stateOf(a).GHSnap }

// GHConnection 目前用的連線(拿著鎖)
func GHConnection(a *core.App) (core.Connection, bool) {
	return core.ConnFind(a, stateOf(a).GH.Connection)
}

// ghLoggedIn 有連線且有 token(會讀鑰匙圈,不能拿著鎖)
func ghLoggedIn(a *core.App) bool {
	a.Mu.Lock()
	c, ok := GHConnection(a)
	a.Mu.Unlock()
	return ok && core.ConnHasToken(a, c)
}

// GHLoggedIn 同 Swift 的 GitHubStore.isLoggedIn(不能拿著鎖)
func GHLoggedIn(a *core.App) bool { return ghLoggedIn(a) }

// GHEnsureConnection 建立預設的 GitHub 連線(沒有才建)
func GHEnsureConnection(a *core.App) core.Connection {
	a.Mu.Lock()
	if c, ok := GHConnection(a); ok {
		a.Mu.Unlock()
		return c
	}
	a.Mu.Unlock()
	c := GHPresetConnection()
	core.ConnUpsert(a, c)
	a.Mu.Lock()
	s := stateOf(a)
	if s.GH.Connection != c.ID {
		s.GH.Connection = c.ID
		ghSave(a)
	}
	a.Mu.Unlock()
	a.Changed("github")
	return c
}

// GHAddRepo 追蹤 repo(存到組織的設定,要組織管理員;見 integrations.go)
func GHAddRepo(a *core.App, repo string) error {
	r, ok := GHNormalizeRepo(repo)
	if !ok {
		return core.ConnErr("repo 要寫成 owner/name,例如 tim72117/evoleap")
	}
	return ghSaveCore(a, coreinteg.GitHubSettingsInput{Add: []string{r}})
}

// GHRemoveRepo 不再追蹤(存到組織的設定);成功後把畫面上這個 repo 的資料拿掉
func GHRemoveRepo(a *core.App, r string) error {
	if err := ghSaveCore(a, coreinteg.GitHubSettingsInput{Remove: []string{r}}); err != nil {
		return err
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := stateOf(a)
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
	go a.Changed("github")
	return nil
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

// ghCached 上次 200 回應的 ETag 與內容
type ghCached struct {
	ETag string
	Data []byte
}

// ghETagLimit 快取最多幾筆;超過就整個清掉重來(每個 repo 每輪約 3 + PR 數筆)
const ghETagLimit = 500

// GHGet GET 一個 GitHub API 路徑。
// 帶 If-None-Match 做條件請求:GitHub 回 304 表示沒變,直接用上次的內容(304 不扣 API 額度)。
func GHGet(a *core.App, path string) (any, error) {
	a.Mu.Lock()
	_, ok := GHConnection(a)
	s := stateOf(a)
	id := s.GH.Connection
	key := id + " " + path
	cached, hit := s.GHETags[key]
	gen := core.ConnGen(a)
	a.Mu.Unlock()
	if !ok {
		return nil, core.ConnErr("還沒設定 GitHub 連線(設定 › 程式碼倉庫)")
	}
	var extra map[string]string
	if hit {
		extra = map[string]string{"If-None-Match": cached.ETag}
	}
	r, err := core.ConnRequestWith(a, id, "GET", path, nil, extra)
	if err != nil {
		return nil, err
	}
	if r.Status == http.StatusNotModified && hit {
		return (&core.ConnResponse{Status: 200, Data: cached.Data}).JSON(), nil
	}
	if r, err = core.ConnCheck(r, nil); err != nil {
		return nil, err
	}
	a.Mu.Lock()
	if core.ConnGen(a) != gen {
		// 讀的時候換了組織:不留快取
	} else if etag := r.Header.Get("ETag"); etag != "" {
		if s.GHETags == nil || len(s.GHETags) >= ghETagLimit {
			s.GHETags = map[string]ghCached{}
		}
		s.GHETags[key] = ghCached{ETag: etag, Data: r.Data}
	} else {
		delete(s.GHETags, key)
	}
	a.Mu.Unlock()
	return r.JSON(), nil
}

func ghGetText(a *core.App, path string) (string, error) {
	a.Mu.Lock()
	id := stateOf(a).GH.Connection
	a.Mu.Unlock()
	r, err := core.ConnCheck(core.ConnRequestRaw(a, id, "GET", path, nil))
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(r.Data), "�"), nil
}

func ghRepoPath(r string) string { return "/repos/" + core.EscapePath(r) }

// GHRefresh 重新讀全部追蹤中的 repo
func GHRefresh(a *core.App) {
	GHSyncSettings(a) // 追蹤的 repo 以組織(核心)的為準,每輪先讀一次
	a.Mu.Lock()
	s := stateOf(a)
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
		stateOf(a).GHSnap.Busy = false
		a.Mu.Unlock()
		a.Changed("github")
	}()
	setErr := func(msg string) {
		a.Mu.Lock()
		stateOf(a).GHSnap.Error = &msg
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
		if e, ok := core.AsConnError(err); ok && e.IsUnauthorized() {
			setErr(e.Message)
			return
		} else if ok && e.Status == 404 {
			errs[r] = "找不到(私有 repo 要在 GitHub 連線的 scopes 加 repo,或 OAuth App 沒有權限)"
		} else {
			errs[r] = err.Error()
		}
	}
	sort.SliceStable(pulls, func(i, j int) bool {
		return core.TimeOrPast(pulls[i].UpdatedAt).After(core.TimeOrPast(pulls[j].UpdatedAt))
	})
	sort.SliceStable(runs, func(i, j int) bool {
		return core.TimeOrPast(runs[i].CreatedAt).After(core.TimeOrPast(runs[j].CreatedAt))
	})
	a.Mu.Lock()
	stateOf(a).GHSnap = GHSnapshot{Repos: outRepos, Pulls: pulls, Runs: runs, FetchedAt: core.TimePtr(time.Now()), Busy: true, Errors: errs}
	a.Mu.Unlock()
}

// GHFetchRepo 一個 repo 的資料、開著的 PR(含檢查)與最近的 Actions
func GHFetchRepo(a *core.App, r string) (GHRepo, []GHPull, []GHRun, error) {
	p := ghRepoPath(r)
	obj, err := GHGet(a, p)
	if err != nil {
		return GHRepo{}, nil, nil, err
	}
	rd, ok := obj.(map[string]any)
	if !ok {
		return GHRepo{}, nil, nil, core.ConnErr("repo 資料格式不對")
	}
	repo := GHParseRepo(rd)
	obj, err = GHGet(a, p+"/pulls?state=open&per_page=50")
	if err != nil {
		return GHRepo{}, nil, nil, err
	}
	pulls := []GHPull{}
	for _, d := range core.JMaps(obj) {
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
	for _, d := range core.JMaps(core.JMap(obj)["workflow_runs"]) {
		runs = append(runs, GHParseRun(d, r))
	}
	return repo, pulls, runs, nil
}

func GHJobs(a *core.App, repo string, run int) ([]GHJob, error) {
	obj, err := GHGet(a, fmt.Sprintf("%s/actions/runs/%d/jobs?per_page=50", ghRepoPath(repo), run))
	if err != nil {
		return nil, err
	}
	jobs := []GHJob{}
	for _, d := range core.JMaps(core.JMap(obj)["jobs"]) {
		jobs = append(jobs, GHParseJob(d))
	}
	return jobs, nil
}

// GHJobLogTail 失敗工作的紀錄最後幾行(GitHub 會轉址到暫時的下載網址,轉址時不帶 token)
func GHJobLogTail(a *core.App, repo string, job, lines int) (string, error) {
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

func GHPullDetailGet(a *core.App, repo string, number int) (GHPullDetail, error) {
	p := fmt.Sprintf("%s/pulls/%d", ghRepoPath(repo), number)
	obj, err := GHGet(a, p)
	if err != nil {
		return GHPullDetail{}, err
	}
	d, ok := obj.(map[string]any)
	if !ok {
		return GHPullDetail{}, core.ConnErr("PR 資料格式不對")
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
	for _, f := range core.JMaps(obj) {
		files = append(files, GHParseFile(f))
	}
	changed := len(files)
	if n, ok := core.JNum(d["changed_files"]); ok {
		changed = int(n)
	}
	return GHPullDetail{Pull: pull, Body: core.JStr(d["body"]), Additions: core.JInt(d["additions"]), Deletions: core.JInt(d["deletions"]),
		ChangedFiles: changed, MergeableState: core.JStr(d["mergeable_state"]), Files: files}, nil
}

// GHRunJSON 同 Swift MCP 的 runJSON:加上 state 與 durationSeconds
func GHRunJSON(r GHRun) map[string]any {
	o, _ := core.Object(r).(map[string]any)
	o["state"] = r.StateLabel()
	if d, ok := r.Duration(); ok {
		o["durationSeconds"] = int(d)
	}
	return o
}

// MARK: - 計時器、畫面

func init() {
	core.OnStart(func(a *core.App) {
		if env.Get("OMIXOR_NO_TIMERS", "OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for range time.Tick(300 * time.Second) {
				a.Mu.Lock()
				busy := stateOf(a).GHSnap.Busy
				a.Mu.Unlock()
				if !busy && ghLoggedIn(a) {
					GHRefresh(a)
				}
			}
		}()
	})

	core.RegisterView("github", func(a *core.App) any {
		s := stateOf(a)
		snap := core.Object(s.GHSnap).(map[string]any)
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
			o := core.Object(p).(map[string]any)
			if p.Checks != nil {
				o["checksState"] = p.Checks.State()
				o["checksLabel"] = p.Checks.Label()
			}
			pulls = append(pulls, o)
		}
		snap["pulls"] = pulls
		out := map[string]any{"settings": s.GH, "snapshot": snap, "connection": nil, "loggedIn": false, "state": nil,
			"settingsError": GHSettingsError(a), "registerURL": GHRegisterURL(), "homepage": "http://127.0.0.1", "callback": "http://127.0.0.1/callback"}
		if c, ok := GHConnection(a); ok {
			out["connection"] = c
			out["loggedIn"] = core.ConnHasTokenCached(a, c)
			out["state"] = core.ConnStateCopy(a, c.ID)
		}
		return out
	})

	core.RegisterAction("github_ensure_connection", func(a *core.App, args map[string]any) (any, error) {
		return GHEnsureConnection(a), nil
	})
	core.RegisterAction("github_add_repo", func(a *core.App, args map[string]any) (any, error) {
		r, _ := args["repo"].(string)
		if err := GHAddRepo(a, r); err != nil {
			return nil, err
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GHSettingsNow(a), nil
	})
	core.RegisterAction("github_remove_repo", func(a *core.App, args map[string]any) (any, error) {
		r, _ := args["repo"].(string)
		if err := GHRemoveRepo(a, r); err != nil {
			return nil, err
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GHSettingsNow(a), nil
	})
	core.RegisterAction("github_refresh", func(a *core.App, args map[string]any) (any, error) {
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
			return "", core.UserError{Message: "repo 要寫成 owner/name"}
		}
		return n, nil
	}
	core.RegisterAction("github_jobs", func(a *core.App, args map[string]any) (any, error) {
		repo, err := repoArg(args)
		if err != nil {
			return nil, err
		}
		run, ok := argInt(args, "run")
		if !ok {
			return nil, core.UserError{Message: "要給 run"}
		}
		return GHJobs(a, repo, run)
	})
	core.RegisterAction("github_job_log", func(a *core.App, args map[string]any) (any, error) {
		repo, err := repoArg(args)
		if err != nil {
			return nil, err
		}
		job, ok := argInt(args, "job")
		if !ok {
			return nil, core.UserError{Message: "要給 job"}
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
	core.RegisterAction("github_pull", func(a *core.App, args map[string]any) (any, error) {
		repo, err := repoArg(args)
		if err != nil {
			return nil, err
		}
		n, ok := argInt(args, "number")
		if !ok {
			return nil, core.UserError{Message: "要給 number"}
		}
		return GHPullDetailGet(a, repo, n)
	})
}
