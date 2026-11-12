package mcp

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"opsweb/internal/core"
)

// GitHub:假的 api.github.com(httptest),照 Swift 的 GitHubTests.swift 一個一個搬

func extTestApp(t *testing.T) *core.App {
	t.Helper()
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	t.Setenv("OPS_NO_TIMERS", "1")
	core.Vault = core.NewMemoryVault()
	a := core.New()
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	return a
}

// extCall 走完整的 JSON-RPC(同 Swift 測試的 tool()):回傳 (解析後的結果或錯誤文字, isError)
func extCall(t *testing.T, a *core.App, name string, args map[string]any) (any, bool) {
	t.Helper()
	h := &Handler{App: a}
	msg := core.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(h.Handle(msg), &resp); err != nil || len(resp.Result.Content) == 0 {
		t.Fatalf("回應格式不對:%v", err)
	}
	text := resp.Result.Content[0].Text
	if resp.Result.IsError {
		return text, true
	}
	var out any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("結果不是 JSON:%s", text)
	}
	return out, false
}

func extOK(t *testing.T, a *core.App, name string, args map[string]any) map[string]any {
	t.Helper()
	out, isErr := extCall(t, a, name, args)
	if isErr {
		t.Fatalf("%s 失敗:%v", name, out)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("%s 回傳不是物件:%v", name, out)
	}
	return m
}

func extErr(t *testing.T, a *core.App, name string, args map[string]any) string {
	t.Helper()
	out, isErr := extCall(t, a, name, args)
	if !isErr {
		t.Fatalf("%s 應該失敗,卻回傳 %v", name, out)
	}
	return out.(string)
}

const ghTestRepo = "tim72117/evoleap"

func fakeGitHub(r *http.Request, body []byte) (int, any) {
	if r.Header.Get("Authorization") != "Bearer gho_test" {
		return 401, map[string]any{"message": "Bad credentials"}
	}
	base := "/repos/" + ghTestRepo
	run7 := map[string]any{"id": 7, "name": "Test", "display_title": "壞掉的測試", "head_branch": "main", "event": "push",
		"status": "completed", "conclusion": "failure", "head_sha": "deadbee", "run_number": 9, "run_attempt": 2,
		"created_at": "2026-11-10T16:00:00Z", "updated_at": "2026-11-10T16:03:00Z", "actor": map[string]any{"login": "tim72117"},
		"html_url": "https://github.com/x/7"}
	switch r.URL.Path {
	case "/user":
		return 200, map[string]any{"login": "tim72117", "email": nil}
	case base:
		return 200, map[string]any{"full_name": ghTestRepo, "description": "evoleap", "default_branch": "main", "private": false,
			"open_issues_count": 1, "stargazers_count": 3, "pushed_at": "2026-11-10T17:16:49Z", "html_url": "https://github.com/" + ghTestRepo}
	case base + "/pulls":
		return 200, []any{map[string]any{"number": 12, "title": "兒童模式入口", "draft": false, "user": map[string]any{"login": "tim72117"},
			"head": map[string]any{"ref": "feat/kids", "sha": "abc123"}, "base": map[string]any{"ref": "main"},
			"labels": []any{map[string]any{"name": "kids"}}, "requested_reviewers": []any{},
			"created_at": "2026-11-09T10:00:00Z", "updated_at": "2026-11-10T10:00:00Z", "html_url": "https://github.com/" + ghTestRepo + "/pull/12"}}
	case base + "/pulls/12":
		return 200, map[string]any{"number": 12, "title": "兒童模式入口", "draft": false, "user": map[string]any{"login": "tim72117"},
			"head": map[string]any{"ref": "feat/kids", "sha": "abc123"}, "base": map[string]any{"ref": "main"}, "labels": []any{},
			"body": "## 改了什麼\n- 入口", "additions": 40, "deletions": 2, "changed_files": 1,
			"mergeable_state": "clean", "html_url": "https://github.com/" + ghTestRepo + "/pull/12"}
	case base + "/pulls/12/files":
		return 200, []any{map[string]any{"filename": "web/src/routes.tsx", "status": "modified", "additions": 40, "deletions": 2}}
	case base + "/commits/abc123/check-runs":
		return 200, map[string]any{"total_count": 3, "check_runs": []any{
			map[string]any{"status": "completed", "conclusion": "success"},
			map[string]any{"status": "completed", "conclusion": "failure"},
			map[string]any{"status": "in_progress", "conclusion": nil},
		}}
	case base + "/actions/runs":
		runs := []any{
			map[string]any{"id": 42863301165, "name": "Deploy to Cloud Run", "display_title": "v0.35.0:兒童模式", "head_branch": "v0.35.0",
				"event": "push", "status": "completed", "conclusion": "success", "head_sha": "f8d723e", "run_number": 46,
				"run_attempt": 1, "created_at": "2026-11-10T17:16:49Z", "run_started_at": "2026-11-10T17:16:49Z",
				"updated_at": "2026-11-10T17:26:40Z", "actor": map[string]any{"login": "tim72117"}, "html_url": "https://github.com/x/1"},
			run7,
		}
		if r.URL.Query().Get("status") == "failure" {
			runs = runs[1:]
		}
		return 200, map[string]any{"total_count": 132, "workflow_runs": runs}
	case base + "/actions/runs/7":
		return 200, run7
	case base + "/actions/runs/7/jobs":
		return 200, map[string]any{"jobs": []any{map[string]any{"id": 99, "name": "test / test", "status": "completed", "conclusion": "failure",
			"started_at": "2026-11-10T16:00:05Z", "completed_at": "2026-11-10T16:02:55Z", "html_url": "https://github.com/x/7/job/99",
			"steps": []any{
				map[string]any{"number": 1, "name": "Set up job", "status": "completed", "conclusion": "success"},
				map[string]any{"number": 2, "name": "go test ./...", "status": "completed", "conclusion": "failure"},
			}}}}
	case base + "/actions/jobs/99/logs":
		// GitHub 轉址到暫時的下載網址
		return 302, core.Redirect("https://pipelines.actions.githubusercontent.com/log/99")
	}
	return 404, map[string]any{"message": "Not Found"}
}

func fakeGitHubWithLogs(r *http.Request, body []byte) (int, any) {
	if r.Host == "pipelines.actions.githubusercontent.com" {
		if r.Header.Get("Authorization") != "" {
			return 403, map[string]any{"message": "token 不該送到這裡"}
		}
		lines := []string{}
		for i := 1; i <= 100; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i))
		}
		return 200, core.RawBody(strings.Join(lines, "\n"))
	}
	return fakeGitHub(r, body)
}

func ghLogin(t *testing.T, a *core.App) {
	core.GHEnsureConnection(a)
	core.ConnSetBearer(a, core.GHConnectionID, "gho_test")
	if err := core.GHAddRepo(a, "https://github.com/tim72117/evoleap.git"); err != nil {
		t.Fatal(err)
	}
	core.ConnRefreshAccount(a, core.GHConnectionID)
}

func TestGHNormalizeRepo(t *testing.T) {
	for in, want := range map[string]string{"tim72117/evoleap": "tim72117/evoleap", " https://github.com/tim72117/evoleap.git ": "tim72117/evoleap",
		"git@github.com:tim72117/evoleap.git": "tim72117/evoleap", "evoleap": "", "a/b/c": "", "a/b?x=1": ""} {
		got, ok := core.GHNormalizeRepo(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q → %q %v,要 %q", in, got, ok, want)
		}
	}
}

func TestGHPresetConnectionIsWebOAuth(t *testing.T) {
	c := core.GHPresetConnection()
	if c.Auth != core.AuthOAuth || c.BaseURL != "https://api.github.com" ||
		c.OAuth.AuthorizationEndpoint != "https://github.com/login/oauth/authorize" || c.OAuth.TokenEndpoint != "https://github.com/login/oauth/access_token" {
		t.Fatalf("%+v", c)
	}
	if u := core.GHRegisterURL(); !strings.Contains(u, "callback_url%5D=http://127.0.0.1/callback") {
		t.Fatal(u)
	}
}

func TestGHRefreshReadsPullsChecksAndRuns(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGitHub)
	defer f.Close()
	ghLogin(t, a)
	if !core.GHLoggedIn(a) {
		t.Fatal("要已登入")
	}
	core.GHRefresh(a)
	a.Mu.Lock()
	snap := core.GHSnapshotNow(a)
	a.Mu.Unlock()
	if snap.Error != nil || snap.Busy {
		t.Fatalf("%+v", snap)
	}
	if snap.Repos[0].DefaultBranch != "main" {
		t.Fatal(snap.Repos)
	}
	pr := snap.Pulls[0]
	if pr.Number != 12 || pr.Branch != "feat/kids" || strings.Join(pr.Labels, ",") != "kids" {
		t.Fatalf("%+v", pr)
	}
	if *pr.Checks != (core.GHChecks{Total: 3, Success: 1, Failure: 1, Pending: 1}) || pr.Checks.State() != "failure" {
		t.Fatalf("%+v", pr.Checks)
	}
	if len(snap.Runs) != 2 || snap.Runs[0].ID != 42863301165 || snap.Runs[1].ID != 7 {
		t.Fatalf("%+v", snap.Runs)
	}
	ok := snap.Runs[0]
	if d, has := ok.Duration(); ok.StateLabel() != "成功" || !has || d < 590 || d > 592 {
		t.Fatalf("%s %v", ok.StateLabel(), d)
	}
	if !snap.Runs[1].IsFailure() {
		t.Fatal("run 7 要算失敗")
	}
	// 追蹤的 repo 存在目前組織的 github.json
	b, _ := os.ReadFile(filepath.Join(core.DataRoot(), "orgs", core.DefaultOrgID, "github.json"))
	var saved core.GHSettings
	if json.Unmarshal(b, &saved) != nil || strings.Join(saved.Repos, ",") != ghTestRepo || saved.Connection != "github" {
		t.Fatalf("%s", b)
	}
	// 只送 GET
	for _, r := range f.Requests() {
		if r.Method != "GET" {
			t.Fatalf("只讀:%s %s", r.Method, r.URL)
		}
	}
}

func TestGHBadTokenShowsError(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGitHub)
	defer f.Close()
	ghLogin(t, a)
	core.ConnSetBearer(a, core.GHConnectionID, "wrong")
	core.GHRefresh(a)
	a.Mu.Lock()
	snap := core.GHSnapshotNow(a)
	a.Mu.Unlock()
	if snap.Error == nil || *snap.Error != "登入已失效,請重新登入" || len(snap.Runs) != 0 {
		t.Fatalf("%+v", snap)
	}
}

func TestGHMissingRepoIsReportedPerRepo(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGitHub)
	defer f.Close()
	ghLogin(t, a)
	if err := core.GHAddRepo(a, "someone/private-thing"); err != nil {
		t.Fatal(err)
	}
	core.GHRefresh(a)
	a.Mu.Lock()
	snap := core.GHSnapshotNow(a)
	a.Mu.Unlock()
	if snap.Error != nil || snap.Errors["someone/private-thing"] != "找不到(私有 repo 要在 GitHub 連線的 scopes 加 repo,或 OAuth App 沒有權限)" || len(snap.Repos) != 1 {
		t.Fatalf("%+v", snap)
	}
	if err := core.GHAddRepo(a, "evoleap"); err == nil || err.Error() != "repo 要寫成 owner/name,例如 tim72117/evoleap" {
		t.Fatal(err)
	}
	core.GHRemoveRepo(a, "someone/private-thing")
	a.Mu.Lock()
	_, still := core.GHSnapshotNow(a).Errors["someone/private-thing"]
	n := len(core.GHSettingsNow(a).Repos)
	a.Mu.Unlock()
	if still || n != 1 {
		t.Fatal("移除 repo 要一起清掉錯誤")
	}
}

func TestGHMCPToolsAreReadOnlyAndWork(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGitHubWithLogs)
	defer f.Close()
	n := 0
	for _, name := range ToolNames() {
		if strings.HasPrefix(name, "ops_github") {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("GitHub 工具 %d 個", n)
	}
	// 還沒登入:告訴角色要請人登入
	want := "omixor 還沒登入 GitHub。請人在 omixor 的 開發進度 › GitHub 設定並登入"
	for _, name := range []string{"ops_github", "ops_github_runs", "ops_github_run", "ops_github_pull"} {
		if msg := extErr(t, a, name, map[string]any{"id": 7, "number": 12}); msg != want {
			t.Fatalf("%s:%s", name, msg)
		}
	}

	ghLogin(t, a)
	sum := extOK(t, a, "ops_github", map[string]any{"refresh": true})
	repo := sum["repos"].([]any)[0].(map[string]any)
	if repo["failedRuns"] != 1.0 || len(repo["openPulls"].([]any)) != 1 || sum["account"] != "tim72117" || repo["error"] != nil {
		t.Fatalf("%v", sum)
	}
	if recent := repo["recentRuns"].([]any)[0].(map[string]any); recent["state"] != "成功" || recent["durationSeconds"] != 591.0 {
		t.Fatalf("%v", recent)
	}
	if lim := extOK(t, a, "ops_github", map[string]any{"limit": 0}); len(lim["repos"].([]any)[0].(map[string]any)["recentRuns"].([]any)) != 1 {
		t.Fatal("limit 最少 1")
	}
	if msg := extErr(t, a, "ops_github", map[string]any{"repo": "x"}); msg != "repo 要寫成 owner/name" {
		t.Fatal(msg)
	}

	runs := extOK(t, a, "ops_github_runs", map[string]any{"status": "failure", "branch": "feat/kids"})
	if list := runs["runs"].([]any); len(list) != 1 || list[0].(map[string]any)["id"] != 7.0 || runs["total"] != 132.0 || runs["repo"] != ghTestRepo {
		t.Fatalf("%v", runs)
	}
	if r, _ := f.Find("/actions/runs?"); !strings.Contains(r.URL, "per_page=20&branch=feat%2Fkids&status=failure") {
		t.Fatal(r.URL)
	}

	run := extOK(t, a, "ops_github_run", map[string]any{"id": 7, "log": true, "lines": 5})
	if fs := run["failedSteps"].([]any); len(fs) != 1 || fs[0] != "test / test › go test ./..." || run["state"] != "失敗" {
		t.Fatalf("%v", run)
	}
	logs := run["logs"].([]any)[0].(map[string]any)
	if logs["job"] != "test / test" || !strings.HasPrefix(logs["tail"].(string), "line 91\n") || !strings.HasSuffix(logs["tail"].(string), "line 100") {
		t.Fatalf("紀錄最少 10 行:%v", logs)
	}
	if msg := extErr(t, a, "ops_github_run", map[string]any{}); msg != "要給 id" {
		t.Fatal(msg)
	}

	pull := extOK(t, a, "ops_github_pull", map[string]any{"number": 12})
	if pull["changedFiles"] != 1.0 || pull["files"].([]any)[0].(map[string]any)["filename"] != "web/src/routes.tsx" ||
		pull["pull"].(map[string]any)["checks"].(map[string]any)["failure"] != 1.0 {
		t.Fatalf("%v", pull)
	}
	if msg := extErr(t, a, "ops_github_pull", map[string]any{}); msg != "要給 number" {
		t.Fatal(msg)
	}
	if msg := extErr(t, a, "ops_github_pull", map[string]any{"number": 99}); msg != "GitHub 回應錯誤:HTTP 404:Not Found" {
		t.Fatal(msg)
	}

	// 沒追蹤的 repo 也可以直接查,格式錯誤會擋
	if msg := extErr(t, a, "ops_github_runs", map[string]any{"repo": "not a repo"}); msg != "repo 要寫成 owner/name" {
		t.Fatal(msg)
	}
	_ = core.GHAddRepo(a, "a/b")
	if msg := extErr(t, a, "ops_github_runs", map[string]any{}); msg != "請給 repo,目前追蹤:tim72117/evoleap、a/b" {
		t.Fatal(msg)
	}
	// 只送 GET
	for _, r := range f.Requests() {
		if r.Method != "GET" {
			t.Fatalf("只讀:%s %s", r.Method, r.URL)
		}
	}
}

func TestGHNoRepoTracked(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGitHub)
	defer f.Close()
	core.GHEnsureConnection(a)
	core.ConnSetBearer(a, core.GHConnectionID, "gho_test")
	if msg := extErr(t, a, "ops_github_runs", map[string]any{}); msg != "還沒追蹤任何 repo,請給 repo(owner/name)" {
		t.Fatal(msg)
	}
	sum := extOK(t, a, "ops_github", nil)
	if len(sum["repos"].([]any)) != 0 || sum["fetchedAt"] == nil {
		t.Fatalf("%v", sum)
	}
}

func TestGHOrgsKeepSeparateRepos(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(fakeGitHub)
	defer f.Close()
	ghLogin(t, a)
	repos := func() string {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return strings.Join(core.GHSettingsNow(a).Repos, ",")
	}
	a.Mu.Lock()
	other := a.CreateOrg("Side", "", "", "blank")
	a.SwitchOrg(other.ID)
	a.Mu.Unlock()
	if repos() != "" {
		t.Fatal(repos())
	}
	a.Mu.Lock()
	a.SwitchOrg(core.DefaultOrgID)
	a.Mu.Unlock()
	if repos() != ghTestRepo {
		t.Fatal(repos())
	}
	a.Mu.Lock()
	cp := a.CreateOrg("Copy", "", "", "copyCurrent")
	a.SwitchOrg(cp.ID)
	a.Mu.Unlock()
	if repos() != ghTestRepo {
		t.Fatal(repos())
	}
}

// 條件請求:第二輪帶 If-None-Match,GitHub 回 304 就沿用上次的內容(不扣額度),結果和第一輪一樣
func TestGHRefreshUsesETags(t *testing.T) {
	a := extTestApp(t)
	var mu sync.Mutex
	sent, notModified := 0, 0
	f := core.NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		if r.Host != "api.github.com" || r.URL.Path == "/user" {
			return fakeGitHub(r, body)
		}
		etag := fmt.Sprintf(`W/"%x"`, sha1.Sum([]byte(r.URL.RequestURI())))
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("If-None-Match") != "" {
			sent++
		}
		if r.Header.Get("If-None-Match") == etag {
			notModified++
			return 304, core.WithHeaders{Header: map[string]string{"ETag": etag}, Body: core.RawBody("")}
		}
		status, obj := fakeGitHub(r, body)
		return status, core.WithHeaders{Header: map[string]string{"ETag": etag}, Body: obj}
	})
	defer f.Close()
	ghLogin(t, a)
	core.GHRefresh(a)
	a.Mu.Lock()
	first := core.GHSnapshotNow(a)
	a.Mu.Unlock()
	if sent != 0 || len(first.Pulls) != 1 || len(first.Runs) != 2 {
		t.Fatalf("第一輪不該帶 If-None-Match:%d %+v", sent, first)
	}
	n := len(f.Requests())
	core.GHRefresh(a)
	a.Mu.Lock()
	second := core.GHSnapshotNow(a)
	a.Mu.Unlock()
	if notModified == 0 || notModified != sent || notModified != len(f.Requests())-n {
		t.Fatalf("第二輪每個請求都要是 304:sent %d, 304 %d, 請求 %d", sent, notModified, len(f.Requests())-n)
	}
	if second.Error != nil || len(second.Errors) != 0 || len(second.Pulls) != 1 || len(second.Runs) != 2 ||
		*second.Pulls[0].Checks != *first.Pulls[0].Checks || second.Repos[0].DefaultBranch != "main" {
		t.Fatalf("304 要沿用上次的內容:%+v", second)
	}
	// 換組織後快取清掉:回到原組織第一次不帶 If-None-Match
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	sent = 0
	core.GHRefresh(a)
	if sent != 0 {
		t.Fatalf("重新讀組織資料後不該沿用快取:%d", sent)
	}
}
