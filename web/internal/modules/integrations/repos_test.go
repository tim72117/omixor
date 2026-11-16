package integrations

import (
	"encoding/json"
	"testing"
	"time"

	"omixor-web/internal/core"
)

// view「code_repos」:GitHub 與 GitLab 追蹤中的 repo 一起列(GitHub 在前),附開著的 PR/MR 數與預設分支最近一次建置;
// 還沒讀到或讀不到的 repo 也列出來(loaded = false、附錯誤)。沒設定連線時兩個平台都是 configured = false
func TestCodeReposView(t *testing.T) {
	a := connTestApp(t)
	str := func(s string) *string { return &s }
	at := func(min int) *core.Time { return core.TimePtr(time.Date(2026, 11, 1, 10, min, 0, 0, time.UTC)) }
	a.Mu.Lock()
	s := stateOf(a)
	s.GH.Repos = []string{"acme/api", "acme/missing", "acme/later"}
	s.GHSnap = GHSnapshot{
		Repos: []GHRepo{{ID: "acme/api", DefaultBranch: "main", Description: "API", IsPrivate: true, URL: "https://github.com/acme/api"}},
		Pulls: []GHPull{
			{Repo: "acme/api", Number: 1, Checks: &GHChecks{Failure: 1, Total: 2}},
			{Repo: "acme/api", Number: 2},
		},
		Runs: []GHRun{ // 新到舊
			{ID: 30, Repo: "acme/api", Branch: "feature", Status: "completed", Conclusion: str("failure"), Workflow: "CI", CreatedAt: at(30)},
			{ID: 20, Repo: "acme/api", Branch: "main", Status: "completed", Conclusion: str("success"), Workflow: "CI", CreatedAt: at(20)},
			{ID: 10, Repo: "acme/api", Branch: "main", Status: "completed", Conclusion: str("failure"), Workflow: "CI", CreatedAt: at(10)},
		},
		Errors:    map[string]string{"acme/missing": "找不到"},
		FetchedAt: at(31),
	}
	s.GL.Projects = []string{"42"}
	s.GLSnap = GLSnapshot{
		Projects: []GLProject{{Key: "42", Path: "team/web", DefaultBranch: "develop", Visibility: "internal", URL: "https://git.example/team/web"}},
		MRs:      []GLMergeRequest{{Project: "42", IID: 5, Pipeline: &GLPipelineRef{ID: 9, Status: "failed"}}},
		Pipelines: []GLPipeline{
			{Project: "42", ID: 101, IID: 7, Ref: "develop", Status: "running", CreatedAt: at(40)},
			{Project: "42", ID: 100, IID: 6, Ref: "develop", Status: "failed", CreatedAt: at(35)},
		},
		Errors: map[string]string{},
	}
	a.Mu.Unlock()

	v, ok := core.View(a, "code_repos")
	if !ok {
		t.Fatal("沒有 view code_repos")
	}
	b, _ := json.Marshal(v)
	var out CodeReposView
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.GitHub.Configured || out.GitHub.LoggedIn || out.GitLab.Configured || out.GitHub.Tracked != 3 || out.GitLab.Tracked != 1 {
		t.Fatalf("平台狀態:%s", b)
	}
	if len(out.Repos) != 4 || out.Repos[0].Key != "acme/api" || out.Repos[3].Platform != "gitlab" {
		t.Fatalf("清單:%s", b)
	}
	api := out.Repos[0]
	if api.Platform != "github" || !api.Loaded || api.Visibility != "private" || api.OpenPulls != 2 || api.FailingPulls != 1 || api.FailedRuns != 2 {
		t.Fatalf("acme/api:%+v", api)
	}
	if api.LastBuild == nil || api.LastBuild.ID != 20 || api.LastBuild.State != "success" || api.LastBuild.Label != "成功" {
		t.Fatalf("預設分支最近一次建置要是 #20:%+v", api.LastBuild)
	}
	if m := out.Repos[1]; m.Loaded || m.Error != "找不到" || m.LastBuild != nil || m.URL != "https://github.com/acme/missing" {
		t.Fatalf("讀不到的 repo:%+v", m)
	}
	if l := out.Repos[2]; l.Loaded || l.Error != "" {
		t.Fatalf("還沒讀到的 repo:%+v", l)
	}
	gl := out.Repos[3]
	if gl.Key != "42" || gl.Name != "team/web" || gl.Visibility != "internal" || gl.OpenPulls != 1 || gl.FailingPulls != 1 || gl.FailedRuns != 1 {
		t.Fatalf("GitLab:%+v", gl)
	}
	if gl.LastBuild == nil || gl.LastBuild.ID != 101 || gl.LastBuild.State != "running" || gl.LastBuild.Title != "Pipeline #7" {
		t.Fatalf("GitLab 最近一次建置:%+v", gl.LastBuild)
	}

	// 兩個平台都沒登入:重新整理不讀(不把「還沒登入」寫進快照),回傳同 view
	r, err := core.Action(a, "code_repos_refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rv, ok := r.(CodeReposView); !ok || len(rv.Repos) != 4 || rv.GitHub.Error != "" || rv.GitLab.Error != "" {
		t.Fatalf("%+v", r)
	}
}

func TestBuildStates(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		r    GHRun
		want string
	}{
		{GHRun{Status: "in_progress"}, "running"},
		{GHRun{Status: "completed", Conclusion: str("timed_out")}, "failure"},
		{GHRun{Status: "completed", Conclusion: str("success")}, "success"},
		{GHRun{Status: "completed", Conclusion: str("cancelled")}, "canceled"},
		{GHRun{Status: "completed", Conclusion: str("neutral")}, "other"},
	}
	for _, c := range cases {
		if got := ghBuildState(c.r); got != c.want {
			t.Fatalf("%+v:%s", c.r, got)
		}
	}
	for s, want := range map[string]string{"success": "success", "failed": "failure", "running": "running", "pending": "running", "canceled": "canceled", "manual": "other"} {
		if got := glBuildState(s); got != want {
			t.Fatalf("%s:%s", s, got)
		}
	}
}
