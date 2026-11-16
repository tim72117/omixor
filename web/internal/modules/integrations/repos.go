package integrations

// 開發進度 › Repositories(功能 development.repos,核心模組 omixor-core/modules/integrations 登記):
// 組織追蹤的 GitHub repo 與 GitLab 專案放在同一張清單(用 platform 標平台),點進去看該 repo 的 PR/MR 與 Actions/Pipeline。
// 原本的 GitHub、GitLab 分頁併進這裡;資料仍是 GHRefresh / GLRefresh 讀好的快照(每 5 分鐘自動更新),這裡只整理成清單。
//
//	view   code_repos          {github, gitlab, repos}:兩個平台的連線狀態與追蹤中 repo 的摘要(見 CodeReposView)
//	action code_repos_refresh  兩個平台(有登入的)同時重新讀,回傳同 view
//
// 單一 repo 的詳情沿用既有的 view 與 action(不另外複製):
//
//	GitHub:view github 的 snapshot.pulls / runs 依 repo 篩;github_jobs、github_job_log、github_pull
//	GitLab:view gitlab 的 snapshot.mrs / pipelines 依 project(追蹤時寫的 key)篩;gitlab_jobs、gitlab_job_log、gitlab_mr
//
// 範例資料的 view「repos」(internal/core 的範例集合)不動:總覽、Pull Requests(範例資料)與 MCP 的範例工具還在用;
// Repositories 分頁不再讀它。

import (
	"strconv"
	"sync"

	"omixor-web/internal/core"
)

// CodeRepoBuild 預設分支最近一次建置(GitHub Actions 的 run、GitLab 的 pipeline)
type CodeRepoBuild struct {
	ID int `json:"id"`
	// State 統一的狀態:success / failure / running / canceled / other(畫面依此決定圖示與顏色)
	State string `json:"state"`
	// Status 平台原本的狀態(GitHub 有結論時是 conclusion,否則 status;GitLab 是 status)
	Status string `json:"status"`
	// Label 中文狀態
	Label string `json:"label"`
	// Title GitHub:workflow 名稱;GitLab:Pipeline #iid
	Title  string     `json:"title"`
	Branch string     `json:"branch"`
	SHA    string     `json:"sha"`
	URL    string     `json:"url"`
	At     *core.Time `json:"at,omitempty"`
}

// CodeRepo 清單的一列
type CodeRepo struct {
	// Platform github / gitlab
	Platform string `json:"platform"`
	// Key GitHub:owner/name;GitLab:追蹤時寫的路徑或 id(gitlab_jobs 等動作的 project 參數、view gitlab 的 project 欄位)
	Key string `json:"key"`
	// Name 顯示名稱(GitHub:owner/name;GitLab:path_with_namespace,還沒讀到時同 key)
	Name          string `json:"name"`
	Description   string `json:"description"`
	URL           string `json:"url"`
	DefaultBranch string `json:"defaultBranch"`
	// Visibility public / private / internal("" = 還沒讀到)
	Visibility string `json:"visibility"`
	// Loaded 這一輪有讀到這個 repo
	Loaded bool `json:"loaded"`
	// Error 讀這個 repo 的錯誤("" = 沒有)
	Error string `json:"error,omitempty"`
	// OpenPulls 開著的 PR / MR 數
	OpenPulls int `json:"openPulls"`
	// FailingPulls 檢查(GitHub)或 head pipeline(GitLab)失敗的 PR / MR 數
	FailingPulls int `json:"failingPulls"`
	// LastBuild 預設分支最近一次建置(沒有時 nil)
	LastBuild *CodeRepoBuild `json:"lastBuild"`
	// FailedRuns 快照裡(最近的執行中)失敗的次數
	FailedRuns int `json:"failedRuns"`
	// ActivityAt 最近的活動(GitHub:pushed_at;GitLab:last_activity_at)
	ActivityAt *core.Time `json:"activityAt,omitempty"`
}

// CodePlatform 一個平台的連線狀態
type CodePlatform struct {
	// Configured 有設定連線(GitLab 還要有伺服器網址)
	Configured bool   `json:"configured"`
	LoggedIn   bool   `json:"loggedIn"`
	Account    string `json:"account,omitempty"`
	// Error 這一輪讀取的錯誤(例如 token 失效)
	Error string `json:"error,omitempty"`
	// SettingsError 讀寫組織設定(核心)的錯誤
	SettingsError string     `json:"settingsError,omitempty"`
	FetchedAt     *core.Time `json:"fetchedAt,omitempty"`
	Busy          bool       `json:"busy"`
	// Tracked 追蹤中的 repo / 專案數
	Tracked int `json:"tracked"`
	// BaseURL GitLab 伺服器網址(GitHub 是 "")
	BaseURL string `json:"baseURL,omitempty"`
	// Insecure GitLab 不驗證憑證(畫面要顯示安全提醒)
	Insecure bool `json:"insecure,omitempty"`
}

// CodeReposView view「code_repos」
type CodeReposView struct {
	GitHub CodePlatform `json:"github"`
	GitLab CodePlatform `json:"gitlab"`
	// Repos GitHub 在前、GitLab 在後,各自照追蹤清單的順序
	Repos []CodeRepo `json:"repos"`
}

func ghBuildState(r GHRun) string {
	switch {
	case r.IsRunning():
		return "running"
	case r.IsFailure():
		return "failure"
	}
	switch derefStr(r.Conclusion) {
	case "success":
		return "success"
	case "cancelled", "skipped":
		return "canceled"
	}
	return "other"
}

func glBuildState(status string) string {
	switch status {
	case "success":
		return "success"
	case "failed":
		return "failure"
	case "canceled", "canceling", "skipped":
		return "canceled"
	case "manual", "scheduled":
		return "other"
	}
	if GLIsRunning(status) {
		return "running"
	}
	return "other"
}

func stateAccount(st *core.ConnectionState) string {
	if st == nil || st.Account == nil {
		return ""
	}
	return *st.Account
}

// codeReposView 整理兩個平台的快照(拿著 Mu)
func codeReposView(a *core.App) CodeReposView {
	s := stateOf(a)
	out := CodeReposView{Repos: []CodeRepo{}}

	// GitHub
	gh := &out.GitHub
	gh.Tracked, gh.Busy, gh.FetchedAt, gh.SettingsError = len(s.GH.Repos), s.GHSnap.Busy, s.GHSnap.FetchedAt, GHSettingsError(a)
	if s.GHSnap.Error != nil {
		gh.Error = *s.GHSnap.Error
	}
	if c, ok := GHConnection(a); ok {
		gh.Configured = true
		gh.LoggedIn = core.ConnHasTokenCached(a, c)
		gh.Account = stateAccount(core.ConnStateCopy(a, c.ID))
	}
	ghRepos := map[string]GHRepo{}
	for _, r := range s.GHSnap.Repos {
		ghRepos[r.ID] = r
	}
	for _, key := range s.GH.Repos {
		row := CodeRepo{Platform: "github", Key: key, Name: key, URL: "https://github.com/" + key, Error: s.GHSnap.Errors[key]}
		if r, ok := ghRepos[key]; ok {
			row.Loaded = true
			row.Description, row.DefaultBranch, row.ActivityAt = r.Description, r.DefaultBranch, r.PushedAt
			if r.URL != "" {
				row.URL = r.URL
			}
			row.Visibility = "public"
			if r.IsPrivate {
				row.Visibility = "private"
			}
		}
		for _, p := range s.GHSnap.Pulls {
			if p.Repo != key {
				continue
			}
			row.OpenPulls++
			if p.Checks != nil && p.Checks.State() == "failure" {
				row.FailingPulls++
			}
		}
		// 執行紀錄已依建立時間新到舊排好:第一個在預設分支上的就是最近一次
		for _, r := range s.GHSnap.Runs {
			if r.Repo != key {
				continue
			}
			if r.IsFailure() {
				row.FailedRuns++
			}
			if row.LastBuild == nil && row.DefaultBranch != "" && r.Branch == row.DefaultBranch {
				status := r.Status
				if r.Conclusion != nil && !r.IsRunning() {
					status = *r.Conclusion
				}
				row.LastBuild = &CodeRepoBuild{ID: r.ID, State: ghBuildState(r), Status: status, Label: r.StateLabel(), Title: r.Workflow,
					Branch: r.Branch, SHA: r.SHA, URL: r.URL, At: r.CreatedAt}
			}
		}
		out.Repos = append(out.Repos, row)
	}

	// GitLab
	gl := &out.GitLab
	gl.Tracked, gl.Busy, gl.FetchedAt, gl.SettingsError = len(s.GL.Projects), s.GLSnap.Busy, s.GLSnap.FetchedAt, GLSettingsError(a)
	if s.GLSnap.Error != nil {
		gl.Error = *s.GLSnap.Error
	}
	if c, ok := GLConnection(a); ok {
		gl.Configured = c.BaseURL != ""
		gl.LoggedIn = c.BaseURL != "" && core.ConnHasTokenCached(a, c)
		gl.Account = stateAccount(core.ConnStateCopy(a, c.ID))
		gl.BaseURL, gl.Insecure = c.BaseURL, c.Insecure
	}
	glProjects := map[string]GLProject{}
	for _, p := range s.GLSnap.Projects {
		glProjects[p.Key] = p
	}
	for _, key := range s.GL.Projects {
		row := CodeRepo{Platform: "gitlab", Key: key, Name: key, Error: s.GLSnap.Errors[key]}
		if p, ok := glProjects[key]; ok {
			row.Loaded = true
			row.Description, row.DefaultBranch, row.URL, row.Visibility, row.ActivityAt = p.Description, p.DefaultBranch, p.URL, p.Visibility, p.LastActivityAt
			if p.Path != "" {
				row.Name = p.Path
			}
		}
		for _, m := range s.GLSnap.MRs {
			if m.Project != key {
				continue
			}
			row.OpenPulls++
			if m.Pipeline != nil && m.Pipeline.Status == "failed" {
				row.FailingPulls++
			}
		}
		for _, p := range s.GLSnap.Pipelines {
			if p.Project != key {
				continue
			}
			if p.Status == "failed" {
				row.FailedRuns++
			}
			if row.LastBuild == nil && row.DefaultBranch != "" && p.Ref == row.DefaultBranch {
				n := p.IID
				if n == 0 {
					n = p.ID
				}
				row.LastBuild = &CodeRepoBuild{ID: p.ID, State: glBuildState(p.Status), Status: p.Status, Label: GLStatusLabel(p.Status),
					Title: "Pipeline #" + strconv.Itoa(n), Branch: p.Ref, SHA: p.SHA, URL: p.URL, At: p.CreatedAt}
			}
		}
		out.Repos = append(out.Repos, row)
	}
	return out
}

func init() {
	core.RegisterView("code_repos", func(a *core.App) any { return codeReposView(a) })
	// 兩個平台(有登入的)同時重新讀;沒登入的不讀(不把「還沒登入」寫進快照的錯誤)
	core.RegisterAction("code_repos_refresh", func(a *core.App, args map[string]any) (any, error) {
		gh, gl := ghLoggedIn(a), GLLoggedIn(a) // 會讀鑰匙圈,不能拿著鎖
		var wg sync.WaitGroup
		if gh {
			wg.Add(1)
			go func() { defer wg.Done(); GHRefresh(a) }()
		}
		if gl {
			wg.Add(1)
			go func() { defer wg.Done(); GLRefresh(a) }()
		}
		wg.Wait()
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return codeReposView(a), nil
	})
}
