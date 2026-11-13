package core

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"omixor-core/service"
)

// GitLab(公司自架):用「GitLab」連線(GitLab OAuth + PKCE,或個人存取權杖 read_api)讀追蹤中專案的
// 開著的 MR(含 head pipeline、核准狀態)與最近的 pipelines、jobs、失敗 job 的紀錄。只讀,v4 API。
// Swift 版沒有這個功能,也沒有 MCP 工具(spec/tools.json 不變),只給 Web 版畫面用。
// 追蹤哪些專案與 GitLab 網址是組織的設定,存在核心(見 integrations.go);CA 憑證檔、不驗證憑證只存在這台的連線上(connections.json),
// 這台的 gitlab.json 只再寫用哪一個連線。
// token 只送連線網址那一台主機:API 一律用相對路徑,分頁的下一頁網址也要同一台主機才跟。

const GLConnectionID = "gitlab"

// GLSettings gitlab.json
type GLSettings struct {
	Connection string   `json:"connection"`
	Projects   []string `json:"projects"`
}

func DefaultGLSettings() GLSettings {
	return GLSettings{Connection: GLConnectionID, Projects: []string{}}
}

func (s *GLSettings) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = DefaultGLSettings()
	if v, ok := raw["projects"]; ok {
		var p []string
		if json.Unmarshal(v, &p) == nil && p != nil {
			s.Projects = p
		}
	}
	if v, ok := raw["connection"]; ok {
		var c string
		if json.Unmarshal(v, &c) == nil && c != "" {
			s.Connection = c
		}
	}
	return nil
}

type GLProject struct {
	DefaultBranch  string `json:"defaultBranch"`
	Description    string `json:"description"`
	ID             int    `json:"id"`
	Key            string `json:"key"` // 追蹤時寫的路徑或 id
	LastActivityAt *Time  `json:"lastActivityAt,omitempty"`
	Name           string `json:"name"`
	Path           string `json:"path"` // path_with_namespace
	URL            string `json:"url"`
	Visibility     string `json:"visibility"`
}

type GLPipelineRef struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	URL    string `json:"url"`
}

type GLApproval struct {
	Approved   bool     `json:"approved"`
	ApprovedBy []string `json:"approvedBy"`
	Left       int      `json:"left"`
	Required   int      `json:"required"`
}

type GLMergeRequest struct {
	Approval     *GLApproval    `json:"approval,omitempty"`
	Author       string         `json:"author"`
	AuthorName   string         `json:"authorName"`
	CreatedAt    *Time          `json:"createdAt,omitempty"`
	Draft        bool           `json:"draft"`
	HasConflicts bool           `json:"hasConflicts"`
	ID           int            `json:"id"`
	IID          int            `json:"iid"`
	Labels       []string       `json:"labels"`
	MergeStatus  string         `json:"mergeStatus"`
	Pipeline     *GLPipelineRef `json:"pipeline,omitempty"`
	Project      string         `json:"project"`
	Reviewers    []string       `json:"reviewers"`
	SHA          string         `json:"sha"`
	SourceBranch string         `json:"sourceBranch"`
	TargetBranch string         `json:"targetBranch"`
	Title        string         `json:"title"`
	UpdatedAt    *Time          `json:"updatedAt,omitempty"`
	URL          string         `json:"url"`
}

type GLPipeline struct {
	CreatedAt  *Time  `json:"createdAt,omitempty"`
	Duration   *int   `json:"duration,omitempty"`
	FinishedAt *Time  `json:"finishedAt,omitempty"`
	ID         int    `json:"id"`
	IID        int    `json:"iid"`
	Project    string `json:"project"`
	Ref        string `json:"ref"`
	SHA        string `json:"sha"`
	Source     string `json:"source"`
	StartedAt  *Time  `json:"startedAt,omitempty"`
	Status     string `json:"status"`
	UpdatedAt  *Time  `json:"updatedAt,omitempty"`
	URL        string `json:"url"`
	User       string `json:"user"`
	UserName   string `json:"userName"`
}

type GLJob struct {
	AllowFailure  bool     `json:"allowFailure"`
	CreatedAt     *Time    `json:"createdAt,omitempty"`
	Duration      *float64 `json:"duration,omitempty"`
	FailureReason string   `json:"failureReason"`
	FinishedAt    *Time    `json:"finishedAt,omitempty"`
	ID            int      `json:"id"`
	Name          string   `json:"name"`
	Stage         string   `json:"stage"`
	StartedAt     *Time    `json:"startedAt,omitempty"`
	Status        string   `json:"status"`
	URL           string   `json:"url"`
}

type GLFile struct {
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	NewPath   string `json:"newPath"`
	OldPath   string `json:"oldPath"`
	Status    string `json:"status"` // added、deleted、renamed、modified
}

type GLMRDetail struct {
	Additions    int            `json:"additions"`
	ChangesCount string         `json:"changesCount"`
	Deletions    int            `json:"deletions"`
	Description  string         `json:"description"`
	Files        []GLFile       `json:"files"`
	MR           GLMergeRequest `json:"mr"`
}

type GLSnapshot struct {
	Busy      bool              `json:"busy"`
	Error     *string           `json:"error"`
	Errors    map[string]string `json:"errors"`
	FetchedAt *Time             `json:"fetchedAt"`
	MRs       []GLMergeRequest  `json:"mrs"`
	Pipelines []GLPipeline      `json:"pipelines"`
	Projects  []GLProject       `json:"projects"`
}

func emptyGLSnapshot() GLSnapshot {
	return GLSnapshot{Errors: map[string]string{}, MRs: []GLMergeRequest{}, Pipelines: []GLPipeline{}, Projects: []GLProject{}}
}

// MARK: 狀態文字

var glStatusLabels = map[string]string{
	"created": "已建立", "waiting_for_resource": "等待資源", "preparing": "準備中", "pending": "排隊中", "running": "執行中",
	"success": "成功", "failed": "失敗", "canceled": "已取消", "canceling": "取消中", "skipped": "略過", "manual": "手動",
	"scheduled": "排程",
}

func GLStatusLabel(s string) string {
	if l, ok := glStatusLabels[s]; ok {
		return l
	}
	return s
}

var glSourceLabels = map[string]string{
	"push": "push", "web": "手動", "trigger": "觸發", "schedule": "排程", "api": "API", "external": "外部",
	"pipeline": "上游 pipeline", "parent_pipeline": "上層 pipeline", "chat": "聊天", "webide": "Web IDE",
	"merge_request_event": "MR", "external_pull_request_event": "外部 PR",
}

func GLSourceLabel(s string) string {
	if l, ok := glSourceLabels[s]; ok {
		return l
	}
	return s
}

func GLIsRunning(status string) bool {
	switch status {
	case "created", "waiting_for_resource", "preparing", "pending", "running", "canceling":
		return true
	}
	return false
}

// MARK: 網址與專案

// GLNormalizeBaseURL 去頭尾空白、結尾的 / 與 /api/v4;必須是 http(s)://主機
func GLNormalizeBaseURL(s string) (string, error) {
	t := strings.TrimSpace(s)
	t = strings.TrimSuffix(t, "/")
	t = strings.TrimSuffix(t, "/api/v4")
	t = strings.TrimSuffix(t, "/")
	u, err := url.Parse(t)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", connErr("GitLab 網址要寫成 https://gitlab.example.com")
	}
	return t, nil
}

var glPathSeg = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

// GLNormalizeProject 專案:數字 id、group/subgroup/project,或專案網址(https://gitlab…/group/project、git@…:group/project.git)
func GLNormalizeProject(s, baseURL string) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "", false
	}
	if n, err := strconv.Atoi(t); err == nil {
		if n <= 0 {
			return "", false
		}
		return strconv.Itoa(n), true
	}
	if baseURL != "" && strings.HasPrefix(strings.ToLower(t), strings.ToLower(baseURL)+"/") {
		t = t[len(baseURL)+1:]
	} else if u, err := url.Parse(t); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		t = strings.TrimPrefix(u.Path, "/")
	} else if strings.HasPrefix(t, "git@") {
		if _, rest, ok := strings.Cut(t, ":"); ok {
			t = rest
		}
	}
	if i := strings.Index(t, "/-/"); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSuffix(strings.Trim(t, "/"), ".git")
	parts := strings.Split(t, "/")
	if len(parts) < 2 {
		return "", false
	}
	for _, p := range parts {
		if !glPathSeg.MatchString(p) {
			return "", false
		}
	}
	return t, true
}

// glProjectPath API 路徑:/api/v4/projects/<id 或 URL 編碼的路徑>
func glProjectPath(key string) string { return "/api/v4/projects/" + percentEncode(key, "-._~") }

// GLPresetConnection 預設的 GitLab 連線:OAuth(在自架 GitLab 的 Applications 建應用程式),也可以改貼個人存取權杖
func GLPresetConnection(base string) Connection {
	c := NewConnection(GLConnectionID, "GitLab", base)
	c.TestPath = "/api/v4/user"
	c.Note = "開發進度 › GitLab 讀 MR 與 Pipelines(只讀)"
	c.OAuth = OAuthSettings{Issuer: base, Scopes: "read_api read_user openid"}
	return c
}

// MARK: 解析

func glTime(v any) *Time { return GCPDate(v) }

func glUser(v any) (string, string) {
	m := jMap(v)
	return jStr(m["username"]), jStr(m["name"])
}

func GLParseProject(d map[string]any, key string) GLProject {
	return GLProject{ID: jInt(d["id"]), Key: key, Name: jStr(d["name"]), Path: jStr(d["path_with_namespace"]),
		Description: jStr(d["description"]), DefaultBranch: jStr(d["default_branch"]), URL: jStr(d["web_url"]),
		LastActivityAt: glTime(d["last_activity_at"]), Visibility: jStr(d["visibility"])}
}

func GLParseMR(d map[string]any, key string) GLMergeRequest {
	user, name := glUser(d["author"])
	labels, reviewers := []string{}, []string{}
	if arr, ok := d["labels"].([]any); ok {
		for _, l := range arr {
			if s, ok := l.(string); ok {
				labels = append(labels, s)
			} else if m, ok := l.(map[string]any); ok { // with_labels_details=true
				labels = append(labels, jStr(m["name"]))
			}
		}
	}
	for _, r := range jMaps(d["reviewers"]) {
		reviewers = append(reviewers, jStr(r["username"]))
	}
	draft := jBool(d["draft"]) || jBool(d["work_in_progress"])
	status := jStr(d["detailed_merge_status"])
	if status == "" {
		status = jStr(d["merge_status"])
	}
	mr := GLMergeRequest{Project: key, ID: jInt(d["id"]), IID: jInt(d["iid"]), Title: jStr(d["title"]), Author: user, AuthorName: name,
		SourceBranch: jStr(d["source_branch"]), TargetBranch: jStr(d["target_branch"]), Draft: draft, Labels: labels, Reviewers: reviewers,
		CreatedAt: glTime(d["created_at"]), UpdatedAt: glTime(d["updated_at"]), URL: jStr(d["web_url"]), SHA: jStr(d["sha"]),
		MergeStatus: status, HasConflicts: jBool(d["has_conflicts"])}
	if p, ok := d["head_pipeline"].(map[string]any); ok {
		mr.Pipeline = &GLPipelineRef{ID: jInt(p["id"]), Status: jStr(p["status"]), URL: jStr(p["web_url"])}
	}
	return mr
}

func GLParseApproval(d map[string]any) GLApproval {
	a := GLApproval{Approved: jBool(d["approved"]), Required: jInt(d["approvals_required"]), Left: jInt(d["approvals_left"]), ApprovedBy: []string{}}
	for _, x := range jMaps(d["approved_by"]) {
		if u, _ := glUser(x["user"]); u != "" {
			a.ApprovedBy = append(a.ApprovedBy, u)
		}
	}
	return a
}

func GLParsePipeline(d map[string]any, key string) GLPipeline {
	user, name := glUser(d["user"])
	p := GLPipeline{Project: key, ID: jInt(d["id"]), IID: jInt(d["iid"]), Status: jStr(d["status"]), Ref: jStr(d["ref"]), SHA: jStr(d["sha"]),
		Source: jStr(d["source"]), User: user, UserName: name, CreatedAt: glTime(d["created_at"]), UpdatedAt: glTime(d["updated_at"]),
		StartedAt: glTime(d["started_at"]), FinishedAt: glTime(d["finished_at"]), URL: jStr(d["web_url"])}
	if n, ok := jNum(d["duration"]); ok {
		v := int(n)
		p.Duration = &v
	}
	return p
}

func GLParseJob(d map[string]any) GLJob {
	j := GLJob{ID: jInt(d["id"]), Name: jStr(d["name"]), Stage: jStr(d["stage"]), Status: jStr(d["status"]),
		CreatedAt: glTime(d["created_at"]), StartedAt: glTime(d["started_at"]), FinishedAt: glTime(d["finished_at"]),
		URL: jStr(d["web_url"]), AllowFailure: jBool(d["allow_failure"]), FailureReason: jStr(d["failure_reason"])}
	if n, ok := jNum(d["duration"]); ok {
		j.Duration = &n
	}
	return j
}

// GLParseDiff 一個檔案的變更;+/- 行數從 diff 內容算
func GLParseDiff(d map[string]any) GLFile {
	f := GLFile{OldPath: jStr(d["old_path"]), NewPath: jStr(d["new_path"]), Status: "modified"}
	switch {
	case jBool(d["new_file"]):
		f.Status = "added"
	case jBool(d["deleted_file"]):
		f.Status = "deleted"
	case jBool(d["renamed_file"]):
		f.Status = "renamed"
	}
	for _, line := range strings.Split(jStr(d["diff"]), "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			f.Additions++
		case strings.HasPrefix(line, "-"):
			f.Deletions++
		}
	}
	return f
}

var (
	ansiRe    = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	sectionRe = regexp.MustCompile(`section_(start|end):\d+:[A-Za-z0-9_.\-]+(\[[^\]]*\])?`)
)

// GLCleanTrace 拿掉 ANSI 顏色、GitLab 的 section 標記;\r 覆寫的進度列只留最後一段
func GLCleanTrace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = sectionRe.ReplaceAllString(l, "")
		if j := strings.LastIndex(strings.TrimRight(l, "\r"), "\r"); j >= 0 {
			l = l[j+1:]
		}
		l = ansiRe.ReplaceAllString(l, "")
		lines[i] = strings.TrimRight(l, "\r")
	}
	return strings.Join(lines, "\n")
}

// MARK: - 狀態(拿著鎖)

func (a *App) glFile() string { return filepath.Join(a.DataDir(), "gitlab.json") }

func glLoad(a *App) {
	s := a.conns()
	s.GL = DefaultGLSettings()
	if b, err := ReadData(a.glFile()); err == nil {
		var g GLSettings
		if json.Unmarshal(b, &g) == nil {
			s.GL = g
		}
	}
	s.GLSnap = emptyGLSnapshot()
	// 網址與追蹤的專案以組織(核心)的為準:先顯示這台檔案裡的,背景從核心讀(見 integrations.go)
	if BackgroundAccountCheck {
		go GLSyncSettings(a)
	}
}

// glSave 只寫這台的 connection(projects 是組織的設定,存在核心;檔案裡原本的 projects 保留,見 integrations.go)
func glSave(a *App) { glSaveConnection(a) }

func GLSettingsNow(a *App) GLSettings {
	g := a.conns().GL
	g.Projects = append([]string{}, g.Projects...)
	return g
}

func GLSnapshotNow(a *App) GLSnapshot { return a.conns().GLSnap }

func GLConnection(a *App) (Connection, bool) { return ConnFind(a, a.conns().GL.Connection) }

// GLLoggedIn 有連線、有網址、有 token(會讀鑰匙圈,不能拿著鎖)
func GLLoggedIn(a *App) bool {
	a.Mu.Lock()
	c, ok := GLConnection(a)
	a.Mu.Unlock()
	return ok && c.BaseURL != "" && ConnHasToken(a, c)
}

// GLEnsureConnection 建立預設的 GitLab 連線(沒有才建);base 可以是空的,之後用 GLSetServer 設
func GLEnsureConnection(a *App, base string) (Connection, error) {
	a.Mu.Lock()
	c, ok := GLConnection(a)
	a.Mu.Unlock()
	if ok {
		return c, nil
	}
	if base != "" {
		b, err := GLNormalizeBaseURL(base)
		if err != nil {
			return Connection{}, err
		}
		base = b
	}
	c = GLPresetConnection(base)
	ConnUpsert(a, c)
	a.Mu.Lock()
	s := a.conns()
	if s.GL.Connection != c.ID {
		s.GL.Connection = c.ID
		glSave(a)
	}
	a.Mu.Unlock()
	a.Changed("gitlab")
	return c, nil
}

// GLSetServer 設定 GitLab 網址、CA 憑證檔、不驗證憑證。網址是組織的設定(先存核心,要組織管理員);
// CA 憑證檔、不驗證憑證只存在這台的連線。網址換了舊的登入作廢(ConnUpsert 會清 token)
func GLSetServer(a *App, base *string, caCert *string, insecure *bool) (Connection, error) {
	if base != nil {
		b, err := GLNormalizeBaseURL(*base)
		if err != nil {
			return Connection{}, err
		}
		// 畫面每次都會帶網址:沒換就不送核心(只改 CA 憑證檔、不驗證憑證不用組織管理員)
		a.Mu.Lock()
		cur, ok := GLConnection(a)
		a.Mu.Unlock()
		if !ok || cur.BaseURL != b {
			x, err := glSaveCore(a, service.GitLabSettingsInput{BaseURL: &b})
			if err != nil {
				return Connection{}, err
			}
			b = x.BaseURL
		}
		base = &b
	}
	return glSetLocalServer(a, base, caCert, insecure)
}

// glSetLocalServer 只改這台的連線(網址、CA 憑證檔、不驗證憑證)
func glSetLocalServer(a *App, base *string, caCert *string, insecure *bool) (Connection, error) {
	c, err := GLEnsureConnection(a, "")
	if err != nil {
		return c, err
	}
	n := c
	if base != nil {
		b, err := GLNormalizeBaseURL(*base)
		if err != nil {
			return c, err
		}
		if b != c.BaseURL {
			n.BaseURL = b
			if c.OAuth.Issuer == "" || c.OAuth.Issuer == c.BaseURL {
				n.OAuth.Issuer = b
			}
			// 端點要重新探索
			n.OAuth.AuthorizationEndpoint, n.OAuth.TokenEndpoint, n.OAuth.RegistrationEndpoint, n.OAuth.RegisteredPort = "", "", "", nil
		}
	}
	if caCert != nil {
		p := strings.TrimSpace(*caCert)
		if p != "" {
			if _, _, err := LoadCAPool(p); err != nil {
				return c, err
			}
		}
		n.CACert = p
	}
	if insecure != nil {
		n.Insecure = *insecure
	}
	ConnUpsert(a, n)
	a.Mu.Lock()
	a.conns().GLSnap = emptyGLSnapshot()
	a.Mu.Unlock()
	a.Changed("gitlab")
	return n, nil
}

// GLAddProject 追蹤專案(存到組織的設定,要組織管理員;見 integrations.go)
func GLAddProject(a *App, p string) error {
	a.Mu.Lock()
	base := ""
	if c, ok := GLConnection(a); ok {
		base = c.BaseURL
	}
	a.Mu.Unlock()
	key, ok := GLNormalizeProject(p, base)
	if !ok {
		return connErr("專案要寫成 group/project(可以有子群組)、專案 id,或專案網址")
	}
	_, err := glSaveCore(a, service.GitLabSettingsInput{Add: []string{key}})
	return err
}

// GLRemoveProject 不再追蹤(存到組織的設定);成功後把畫面上這個專案的資料拿掉
func GLRemoveProject(a *App, key string) error {
	if _, err := glSaveCore(a, service.GitLabSettingsInput{Remove: []string{key}}); err != nil {
		return err
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	s := a.conns()
	snap := &s.GLSnap
	projects := []GLProject{}
	for _, p := range snap.Projects {
		if p.Key != key {
			projects = append(projects, p)
		}
	}
	mrs := []GLMergeRequest{}
	for _, m := range snap.MRs {
		if m.Project != key {
			mrs = append(mrs, m)
		}
	}
	pls := []GLPipeline{}
	for _, p := range snap.Pipelines {
		if p.Project != key {
			pls = append(pls, p)
		}
	}
	snap.Projects, snap.MRs, snap.Pipelines = projects, mrs, pls
	delete(snap.Errors, key)
	go a.Changed("gitlab")
	return nil
}

// MARK: 讀 API(不能拿著鎖)

func glConn(a *App) (Connection, error) {
	a.Mu.Lock()
	c, ok := GLConnection(a)
	a.Mu.Unlock()
	if !ok {
		return c, connErr("還沒設定 GitLab 連線(開發進度 › GitLab)")
	}
	if c.BaseURL == "" {
		return c, connErr("還沒設定 GitLab 網址")
	}
	return c, nil
}

// GLGet GET 一個 v4 API 路徑(/api/v4/…),回傳 JSON
func GLGet(a *App, path string) (any, error) {
	c, err := glConn(a)
	if err != nil {
		return nil, err
	}
	return ConnRequest(a, c.ID, "GET", path, nil)
}

// GLGetAll 讀全部分頁(GitLab 的 X-Next-Page;沒有時看 Link rel="next",同一台主機才跟),最多 maxPages 頁
func GLGetAll(a *App, path string, maxPages int) ([]any, error) {
	c, err := glConn(a)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(c.BaseURL)
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	if !strings.Contains(path, "per_page=") {
		path += sep + "per_page=100"
		sep = "&"
	}
	out := []any{}
	next := path
	for page := 0; page < maxPages && next != ""; page++ {
		r, err := connCheck(ConnRequestRaw(a, c.ID, "GET", next, nil))
		if err != nil {
			return nil, err
		}
		arr, ok := r.JSON().([]any)
		if !ok {
			return nil, connErr("GitLab 回應格式不對(應該是陣列):" + path)
		}
		out = append(out, arr...)
		next = ""
		if np := strings.TrimSpace(r.Header.Get("X-Next-Page")); np != "" {
			if _, err := strconv.Atoi(np); err == nil {
				next = setQuery(path, "page", np)
			}
		} else if link := linkNext(r.Header.Get("Link")); link != "" {
			if u, err := url.Parse(link); err == nil && strings.EqualFold(u.Host, base.Host) && u.Scheme == base.Scheme {
				// 只取路徑與參數,接在連線網址後面(token 不會跟著換主機)
				p := u.EscapedPath()
				if bp := strings.TrimSuffix(base.EscapedPath(), "/"); bp != "" {
					p = strings.TrimPrefix(p, bp)
				}
				next = p + "?" + u.RawQuery
			}
		}
	}
	return out, nil
}

func setQuery(path, k, v string) string {
	p, q, _ := strings.Cut(path, "?")
	parts := []string{}
	for _, kv := range strings.Split(q, "&") {
		if kv != "" && !strings.HasPrefix(kv, k+"=") {
			parts = append(parts, kv)
		}
	}
	parts = append(parts, k+"="+v)
	return p + "?" + strings.Join(parts, "&")
}

var linkRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="?next"?`)

func linkNext(h string) string {
	if m := linkRe.FindStringSubmatch(h); m != nil {
		return m[1]
	}
	return ""
}

func glText(a *App, path string) (string, error) {
	c, err := glConn(a)
	if err != nil {
		return "", err
	}
	r, err := connCheck(ConnRequestRaw(a, c.ID, "GET", path, nil))
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(r.Data), "�"), nil
}

// parallel 同時最多 n 個
func parallel(count, n int, f func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, n)
	for i := 0; i < count; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			f(i)
		}(i)
	}
	wg.Wait()
}

// GLFetchProject 專案資料、開著的 MR(含 head pipeline 與核准)、最近 20 個 pipelines(含觸發者)
func GLFetchProject(a *App, key string) (GLProject, []GLMergeRequest, []GLPipeline, error) {
	p := glProjectPath(key)
	obj, err := GLGet(a, p)
	if err != nil {
		return GLProject{}, nil, nil, err
	}
	d, ok := obj.(map[string]any)
	if !ok {
		return GLProject{}, nil, nil, connErr("專案資料格式不對")
	}
	project := GLParseProject(d, key)
	raw, err := GLGetAll(a, p+"/merge_requests?state=opened&order_by=updated_at&sort=desc", 5)
	if err != nil {
		return GLProject{}, nil, nil, err
	}
	mrs := []GLMergeRequest{}
	for _, x := range raw {
		if m, ok := x.(map[string]any); ok {
			mrs = append(mrs, GLParseMR(m, key))
		}
	}
	// 每個 MR 的 head pipeline(詳情才有)與核准狀態;讀不到就略過
	n := min(len(mrs), 50)
	parallel(n, 6, func(i int) {
		mp := fmt.Sprintf("%s/merge_requests/%d", p, mrs[i].IID)
		if o, err := GLGet(a, mp); err == nil {
			if m, ok := o.(map[string]any); ok {
				if pl, ok := m["head_pipeline"].(map[string]any); ok {
					mrs[i].Pipeline = &GLPipelineRef{ID: jInt(pl["id"]), Status: jStr(pl["status"]), URL: jStr(pl["web_url"])}
				}
			}
		}
		if o, err := GLGet(a, mp+"/approvals"); err == nil {
			if m, ok := o.(map[string]any); ok {
				ap := GLParseApproval(m)
				mrs[i].Approval = &ap
			}
		}
	})
	praw, err := GLGet(a, p+"/pipelines?per_page=20&order_by=id&sort=desc")
	if err != nil {
		return GLProject{}, nil, nil, err
	}
	pls := []GLPipeline{}
	for _, m := range jMaps(praw) {
		pls = append(pls, GLParsePipeline(m, key))
	}
	// 清單沒有觸發者與時間長度,讀詳情補上
	parallel(len(pls), 6, func(i int) {
		if o, err := GLGet(a, fmt.Sprintf("%s/pipelines/%d", p, pls[i].ID)); err == nil {
			if m, ok := o.(map[string]any); ok {
				pls[i] = GLParsePipeline(m, key)
			}
		}
	})
	return project, mrs, pls, nil
}

// GLRefresh 重新讀全部追蹤中的專案
func GLRefresh(a *App) {
	GLSyncSettings(a) // 網址與追蹤的專案以組織(核心)的為準,每輪先讀一次
	a.Mu.Lock()
	s := a.conns()
	if s.GLSnap.Busy {
		a.Mu.Unlock()
		return
	}
	s.GLSnap.Busy = true
	s.GLSnap.Error = nil
	keys := append([]string{}, s.GL.Projects...)
	a.Mu.Unlock()
	a.Changed("gitlab")
	defer func() {
		a.Mu.Lock()
		a.conns().GLSnap.Busy = false
		a.Mu.Unlock()
		a.Changed("gitlab")
	}()
	setErr := func(msg string) {
		a.Mu.Lock()
		a.conns().GLSnap.Error = &msg
		a.Mu.Unlock()
	}
	if _, err := glConn(a); err != nil {
		setErr(err.Error())
		return
	}
	if !GLLoggedIn(a) {
		setErr("還沒登入 GitLab")
		return
	}
	projects, mrs, pls := []GLProject{}, []GLMergeRequest{}, []GLPipeline{}
	errs := map[string]string{}
	for _, k := range keys {
		p, m, pl, err := GLFetchProject(a, k)
		if err == nil {
			projects = append(projects, p)
			mrs = append(mrs, m...)
			pls = append(pls, pl...)
			continue
		}
		e, ok := AsConnError(err)
		switch {
		case ok && e.IsUnauthorized():
			setErr(e.Message)
			return
		case ok && e.Status == 403:
			errs[k] = "沒有權限(token 要有 read_api,帳號也要能看到這個專案)"
		case ok && e.Status == 404:
			errs[k] = "找不到(確認專案路徑或 id;私有專案要登入的帳號有權限)"
		default:
			errs[k] = err.Error()
		}
	}
	sort.SliceStable(mrs, func(i, j int) bool { return timeOrPast(mrs[i].UpdatedAt).After(timeOrPast(mrs[j].UpdatedAt)) })
	sort.SliceStable(pls, func(i, j int) bool { return timeOrPast(pls[i].CreatedAt).After(timeOrPast(pls[j].CreatedAt)) })
	a.Mu.Lock()
	a.conns().GLSnap = GLSnapshot{Projects: projects, MRs: mrs, Pipelines: pls, FetchedAt: timePtr(time.Now()), Busy: true, Errors: errs}
	a.Mu.Unlock()
}

// GLJobs pipeline 的 jobs(依建立順序)與 stage 順序
func GLJobs(a *App, key string, pipeline int) ([]GLJob, []string, error) {
	raw, err := GLGetAll(a, fmt.Sprintf("%s/pipelines/%d/jobs", glProjectPath(key), pipeline), 10)
	if err != nil {
		return nil, nil, err
	}
	jobs := []GLJob{}
	for _, x := range raw {
		if m, ok := x.(map[string]any); ok {
			jobs = append(jobs, GLParseJob(m))
		}
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	stages := []string{}
	seen := map[string]bool{}
	for _, j := range jobs {
		if !seen[j.Stage] {
			seen[j.Stage] = true
			stages = append(stages, j.Stage)
		}
	}
	return jobs, stages, nil
}

// GLJobTraceTail job 紀錄的最後幾行(拿掉顏色碼)
func GLJobTraceTail(a *App, key string, job, lines int) (string, error) {
	text, err := glText(a, fmt.Sprintf("%s/jobs/%d/trace", glProjectPath(key), job))
	if err != nil {
		return "", err
	}
	all := strings.Split(strings.TrimRight(GLCleanTrace(text), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}

// GLMRDetailGet MR 的說明、head pipeline、核准與變更的檔案
func GLMRDetailGet(a *App, key string, iid int) (GLMRDetail, error) {
	mp := fmt.Sprintf("%s/merge_requests/%d", glProjectPath(key), iid)
	obj, err := GLGet(a, mp)
	if err != nil {
		return GLMRDetail{}, err
	}
	d, ok := obj.(map[string]any)
	if !ok {
		return GLMRDetail{}, connErr("MR 資料格式不對")
	}
	mr := GLParseMR(d, key)
	if o, err := GLGet(a, mp+"/approvals"); err == nil {
		if m, ok := o.(map[string]any); ok {
			ap := GLParseApproval(m)
			mr.Approval = &ap
		}
	}
	var diffs []map[string]any
	if raw, err := GLGetAll(a, mp+"/diffs", 10); err == nil {
		for _, x := range raw {
			if m, ok := x.(map[string]any); ok {
				diffs = append(diffs, m)
			}
		}
	} else if e, ok := AsConnError(err); ok && e.Status == 404 { // GitLab 15.7 以前沒有 /diffs
		o, err := GLGet(a, mp+"/changes")
		if err != nil {
			return GLMRDetail{}, err
		}
		diffs = jMaps(jMap(o)["changes"])
	} else {
		return GLMRDetail{}, err
	}
	out := GLMRDetail{MR: mr, Description: jStr(d["description"]), Files: []GLFile{}}
	if s, ok := d["changes_count"].(string); ok {
		out.ChangesCount = s
	}
	for _, x := range diffs {
		f := GLParseDiff(x)
		out.Additions += f.Additions
		out.Deletions += f.Deletions
		out.Files = append(out.Files, f)
	}
	if out.ChangesCount == "" {
		out.ChangesCount = strconv.Itoa(len(out.Files))
	}
	return out, nil
}

// MARK: - 計時器、畫面

func glPipelineJSON(p GLPipeline) map[string]any {
	o := Object(p).(map[string]any)
	o["statusLabel"] = GLStatusLabel(p.Status)
	o["sourceLabel"] = GLSourceLabel(p.Source)
	o["isRunning"] = GLIsRunning(p.Status)
	o["isFailure"] = p.Status == "failed"
	return o
}

func init() {
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for range time.Tick(300 * time.Second) {
				a.Mu.Lock()
				busy := a.conns().GLSnap.Busy
				a.Mu.Unlock()
				if !busy && GLLoggedIn(a) {
					GLRefresh(a)
				}
			}
		}()
	})

	RegisterView("gitlab", func(a *App) any {
		s := a.conns()
		snap := Object(s.GLSnap).(map[string]any)
		pls := []any{}
		for _, p := range s.GLSnap.Pipelines {
			pls = append(pls, glPipelineJSON(p))
		}
		snap["pipelines"] = pls
		mrs := []any{}
		for _, m := range s.GLSnap.MRs {
			o := Object(m).(map[string]any)
			if m.Pipeline != nil {
				o["pipelineLabel"] = GLStatusLabel(m.Pipeline.Status)
			}
			mrs = append(mrs, o)
		}
		snap["mrs"] = mrs
		out := map[string]any{"settings": s.GL, "snapshot": snap, "connection": nil, "loggedIn": false, "state": nil,
			"settingsError": GLSettingsError(a), "configured": false, "baseURL": "", "caCert": "", "insecure": false, "callback": "http://127.0.0.1/callback",
			"scopes": "read_api read_user openid", "applicationsURL": "", "tokenURL": ""}
		if c, ok := GLConnection(a); ok {
			out["connection"] = c
			out["loggedIn"] = c.BaseURL != "" && connHasTokenCached(a, c)
			out["state"] = connStateCopy(a, c.ID)
			out["configured"] = c.BaseURL != ""
			out["baseURL"], out["caCert"], out["insecure"] = c.BaseURL, c.CACert, c.Insecure
			if c.BaseURL != "" {
				out["applicationsURL"] = c.BaseURL + "/-/user_settings/applications"
				out["tokenURL"] = c.BaseURL + "/-/user_settings/personal_access_tokens?name=omixor&scopes=read_api"
			}
		}
		return out
	})

	RegisterAction("gitlab_ensure_connection", func(a *App, args map[string]any) (any, error) {
		base, _ := args["baseURL"].(string)
		return GLEnsureConnection(a, base)
	})
	RegisterAction("gitlab_settings", func(a *App, args map[string]any) (any, error) {
		var base, ca *string
		var insecure *bool
		if v, ok := args["baseURL"].(string); ok {
			base = &v
		}
		if v, ok := args["caCert"].(string); ok {
			ca = &v
		}
		if v, ok := args["insecure"].(bool); ok {
			insecure = &v
		}
		return GLSetServer(a, base, ca, insecure)
	})
	RegisterAction("gitlab_add_project", func(a *App, args map[string]any) (any, error) {
		p, _ := args["project"].(string)
		if f, ok := args["project"].(float64); ok {
			p = strconv.Itoa(int(f))
		}
		if err := GLAddProject(a, p); err != nil {
			return nil, err
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GLSettingsNow(a), nil
	})
	RegisterAction("gitlab_remove_project", func(a *App, args map[string]any) (any, error) {
		p, _ := args["project"].(string)
		if err := GLRemoveProject(a, p); err != nil {
			return nil, err
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GLSettingsNow(a), nil
	})
	RegisterAction("gitlab_refresh", func(a *App, args map[string]any) (any, error) {
		GLRefresh(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return GLSnapshotNow(a), nil
	})
	intArg := func(args map[string]any, k string) (int, bool) {
		switch x := args[k].(type) {
		case float64:
			return int(x), true
		case string:
			n, err := strconv.Atoi(strings.TrimSpace(x))
			return n, err == nil
		}
		return 0, false
	}
	projectArg := func(args map[string]any) (string, error) {
		p, _ := args["project"].(string)
		if f, ok := args["project"].(float64); ok {
			p = strconv.Itoa(int(f))
		}
		if strings.TrimSpace(p) == "" {
			return "", UserError{"要給 project"}
		}
		return strings.TrimSpace(p), nil
	}
	RegisterAction("gitlab_jobs", func(a *App, args map[string]any) (any, error) {
		p, err := projectArg(args)
		if err != nil {
			return nil, err
		}
		id, ok := intArg(args, "pipeline")
		if !ok {
			return nil, UserError{"要給 pipeline"}
		}
		jobs, stages, err := GLJobs(a, p, id)
		if err != nil {
			return nil, err
		}
		list := []any{}
		for _, j := range jobs {
			o := Object(j).(map[string]any)
			o["statusLabel"] = GLStatusLabel(j.Status)
			o["isFailure"] = j.Status == "failed" && !j.AllowFailure
			list = append(list, o)
		}
		return map[string]any{"jobs": list, "stages": stages}, nil
	})
	RegisterAction("gitlab_job_log", func(a *App, args map[string]any) (any, error) {
		p, err := projectArg(args)
		if err != nil {
			return nil, err
		}
		id, ok := intArg(args, "job")
		if !ok {
			return nil, UserError{"要給 job"}
		}
		lines, ok := intArg(args, "lines")
		if !ok {
			lines = 120
		}
		tail, err := GLJobTraceTail(a, p, id, min(2000, max(10, lines)))
		if err != nil {
			return nil, err
		}
		return map[string]any{"tail": tail, "text": tail}, nil
	})
	RegisterAction("gitlab_mr", func(a *App, args map[string]any) (any, error) {
		p, err := projectArg(args)
		if err != nil {
			return nil, err
		}
		iid, ok := intArg(args, "iid")
		if !ok {
			return nil, UserError{"要給 iid"}
		}
		d, err := GLMRDetailGet(a, p, iid)
		if err != nil {
			return nil, err
		}
		o := Object(d).(map[string]any)
		if d.MR.Pipeline != nil {
			o["mr"].(map[string]any)["pipelineLabel"] = GLStatusLabel(d.MR.Pipeline.Status)
		}
		return o, nil
	})
}
