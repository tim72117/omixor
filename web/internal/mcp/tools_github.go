package mcp

import (
	"fmt"
	"strings"

	"omixor-web/internal/core"
)

// GitHub 工具,逐行對照 Swift MCPHandler.swift 的 ghStore、ghRepo、githubSummary、githubRuns、githubRun、githubPull。只讀。

// ghReady 同 Swift 的 ghStore():要有連線且已登入
func ghReady(c *Call) error {
	if !core.GHLoggedIn(c.App) {
		return ToolError{Message: "omixor 還沒登入 GitHub。請人在 omixor 的 開發進度 › GitHub 設定並登入"}
	}
	return nil
}

func ghRepoArg(c *Call) (string, error) {
	if r, ok := c.Args.String("repo"); ok {
		n, ok := core.GHNormalizeRepo(r)
		if !ok {
			return "", ToolError{Message: "repo 要寫成 owner/name"}
		}
		return n, nil
	}
	c.App.Mu.Lock()
	repos := core.GHSettingsNow(c.App).Repos
	c.App.Mu.Unlock()
	if len(repos) != 1 {
		if len(repos) == 0 {
			return "", ToolError{Message: "還沒追蹤任何 repo,請給 repo(owner/name)"}
		}
		return "", ToolError{Message: "請給 repo,目前追蹤:" + strings.Join(repos, "、")}
	}
	return repos[0], nil
}

func ghError(err error) error { return ToolError{Message: "GitHub 回應錯誤:" + err.Error()} }

func init() {
	Register("github", func(c *Call) (any, error) {
		if err := ghReady(c); err != nil {
			return nil, err
		}
		a := c.App
		a.Mu.Lock()
		fetched := core.GHSnapshotNow(a).FetchedAt != nil
		a.Mu.Unlock()
		if b, _ := c.Args.Bool("refresh"); b || !fetched {
			core.GHRefresh(a)
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		snap := core.GHSnapshotNow(a)
		if snap.Error != nil {
			return nil, ToolError{Message: *snap.Error}
		}
		var only *string
		if r, ok := c.Args.String("repo"); ok {
			n, ok := core.GHNormalizeRepo(r)
			if !ok {
				return nil, ToolError{Message: "repo 要寫成 owner/name"}
			}
			only = &n
		}
		limit := 10
		if n, ok := c.Args.Int("limit"); ok {
			limit = n
		}
		limit = max(1, limit)
		settings := core.GHSettingsNow(a)
		repos := []any{}
		for _, r := range settings.Repos {
			if only != nil && !strings.EqualFold(r, *only) {
				continue
			}
			var info any
			for _, x := range snap.Repos {
				if x.ID == r {
					info = core.Object(x)
					break
				}
			}
			var errText any
			if e, ok := snap.Errors[r]; ok {
				errText = e
			}
			pulls := []any{}
			for _, p := range snap.Pulls {
				if p.Repo == r {
					pulls = append(pulls, core.Object(p))
				}
			}
			recent := []any{}
			failed := 0
			for _, run := range snap.Runs {
				if run.Repo != r {
					continue
				}
				if len(recent) < limit {
					recent = append(recent, core.GHRunJSON(run))
				}
				if run.IsFailure() {
					failed++
				}
			}
			repos = append(repos, map[string]any{"repo": r, "info": info, "error": errText, "openPulls": pulls,
				"recentRuns": recent, "failedRuns": failed})
		}
		var account, fetchedAt any
		if conn, ok := core.GHConnection(a); ok {
			if st := connAccount(a, conn.ID); st != nil {
				account = *st
			}
		}
		if snap.FetchedAt != nil {
			fetchedAt = core.ISO(snap.FetchedAt.Time)
		}
		return map[string]any{"account": account, "fetchedAt": fetchedAt, "repos": repos}, nil
	})

	Register("github_runs", func(c *Call) (any, error) {
		if err := ghReady(c); err != nil {
			return nil, err
		}
		repo, err := ghRepoArg(c)
		if err != nil {
			return nil, err
		}
		limit := 20
		if n, ok := c.Args.Int("limit"); ok {
			limit = n
		}
		q := []string{fmt.Sprintf("per_page=%d", min(100, max(1, limit)))}
		if b, ok := c.Args.String("branch"); ok {
			q = append(q, "branch="+alnumEscape(b))
		}
		if s, ok := c.Args.String("status"); ok {
			q = append(q, "status="+s)
		}
		obj, err := core.GHGet(c.App, "/repos/"+core.EscapePath(repo)+"/actions/runs?"+strings.Join(q, "&"))
		if err != nil {
			return nil, ghError(err)
		}
		m, _ := obj.(map[string]any)
		runs := []any{}
		if list, ok := m["workflow_runs"].([]any); ok {
			for _, x := range list {
				d, ok := x.(map[string]any)
				if !ok {
					runs = []any{}
					break
				}
				runs = append(runs, core.GHRunJSON(core.GHParseRun(d, repo)))
			}
		}
		total, ok := m["total_count"]
		if !ok {
			total = nil
		}
		return map[string]any{"repo": repo, "total": total, "runs": runs}, nil
	})

	Register("github_run", func(c *Call) (any, error) {
		if err := ghReady(c); err != nil {
			return nil, err
		}
		repo, err := ghRepoArg(c)
		if err != nil {
			return nil, err
		}
		id, ok := c.Args.Int("id")
		if !ok {
			return nil, ToolError{Message: "要給 id"}
		}
		obj, err := core.GHGet(c.App, fmt.Sprintf("/repos/%s/actions/runs/%d", core.EscapePath(repo), id))
		if err != nil {
			return nil, ghError(err)
		}
		d, ok := obj.(map[string]any)
		if !ok {
			return nil, ToolError{Message: "執行資料格式不對"}
		}
		run := core.GHParseRun(d, repo)
		jobs, err := core.GHJobs(c.App, repo, id)
		if err != nil {
			return nil, ghError(err)
		}
		out := core.GHRunJSON(run)
		jobsJSON := []any{}
		for _, j := range jobs {
			jobsJSON = append(jobsJSON, core.Object(j))
		}
		out["jobs"] = jobsJSON
		bad := func(status string, conclusion *string) bool {
			if status != "completed" {
				return false
			}
			switch derefOr(conclusion) {
			case "success", "skipped", "cancelled":
				return false
			}
			return true
		}
		failed := []core.GHJob{}
		steps := []any{}
		for _, j := range jobs {
			if !bad(j.Status, j.Conclusion) {
				continue
			}
			failed = append(failed, j)
			for _, s := range j.Steps {
				if bad(s.Status, s.Conclusion) {
					steps = append(steps, j.Name+" › "+s.Name)
				}
			}
		}
		out["failedSteps"] = steps
		if b, _ := c.Args.Bool("log"); b {
			lines := 80
			if n, ok := c.Args.Int("lines"); ok {
				lines = n
			}
			lines = min(400, max(10, lines))
			logs := []any{}
			for i, j := range failed {
				if i >= 3 {
					break
				}
				tail, err := core.GHJobLogTail(c.App, repo, j.ID, lines)
				if err != nil {
					logs = append(logs, map[string]any{"job": j.Name, "error": err.Error()})
				} else {
					logs = append(logs, map[string]any{"job": j.Name, "tail": tail})
				}
			}
			out["logs"] = logs
		}
		return out, nil
	})

	Register("github_pull", func(c *Call) (any, error) {
		if err := ghReady(c); err != nil {
			return nil, err
		}
		repo, err := ghRepoArg(c)
		if err != nil {
			return nil, err
		}
		n, ok := c.Args.Int("number")
		if !ok {
			return nil, ToolError{Message: "要給 number"}
		}
		d, err := core.GHPullDetailGet(c.App, repo, n)
		if err != nil {
			return nil, ghError(err)
		}
		return core.Object(d), nil
	})
}

func derefOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// alnumEscape 英數字以外都編碼(同 Swift 的 addingPercentEncoding(withAllowedCharacters: .alphanumerics))
func alnumEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			b.WriteByte(ch)
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

// connAccount 連線狀態裡的帳號名稱(拿著鎖)
func connAccount(a *core.App, id string) *string {
	if st := core.ConnStateOf(a, id); st != nil {
		return st.Account
	}
	return nil
}
