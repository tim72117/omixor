package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"omixor-core/client"
	"omixor-core/service"
)

func TestParseRepoURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://github.com/Acme/App.git", "github:acme/app"},
		{"https://github.com/acme/app", "github:acme/app"},
		{"https://user:tok@github.com/acme/app.git/", "github:acme/app"},
		{"git@github.com:acme/app.git", "github:acme/app"},
		{"ssh://git@github.com:22/acme/app.git", "github:acme/app"},
		{"git@gitlab.com:group/sub/project.git", "gitlab:group/sub/project"},
		{"ssh://git@gitlab.example.com/group/sub/project.git", "gitlab:group/sub/project"},
		{"https://gitlab.example.com:8443/Group/Sub/Project", "gitlab:group/sub/project"},
		{"https://git.example.com/team/repo.git", "team/repo"},
		{"git@git.example.com:team/repo.git", "team/repo"},
		{"/srv/git/repo.git", ""},
		{"repo", ""},
		{"https://github.com/onlyone", ""},
		{"", ""},
	} {
		got, ok := parseRepoURL(c.in)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("parseRepoURL(%q) = %q,%v; want %q", c.in, got, ok, c.want)
		}
	}
}

// ---- 假 server ----

type fakeSync struct {
	mu      sync.Mutex
	last    string
	status  int // 非 0 = POST 回這個狀態
	reports []service.CommitReport
	getRepo string
}

func (f *fakeSync) server(t *testing.T) *client.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/commits/sync"):
			f.getRepo = r.URL.Query().Get("repo")
			json.NewEncoder(w).Encode(service.CommitSyncView{Repo: f.getRepo, LastSHA: f.last})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/commits"):
			if f.status != 0 {
				w.WriteHeader(f.status)
				fmt.Fprint(w, `{"error":"too many","retryMinutes":17}`)
				return
			}
			var in service.CommitReport
			json.NewDecoder(r.Body).Decode(&in)
			f.reports = append(f.reports, in)
			res := service.CommitReportResult{Reported: len(in.Commits), Created: len(in.Commits),
				Linked: []service.CommitLinked{{Issue: "ISS-1", SHA: in.Commits[0].SHA[:7], Kind: "fixes"}}, Unmatched: []string{"ISS-999"}}
			json.NewEncoder(w).Encode(res)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cl := client.New(srv.URL)
	cl.Token = func() string { return "omx_test" }
	return cl
}

func (f *fakeSync) sent() (shas []string) {
	for _, r := range f.reports {
		for _, c := range r.Commits {
			shas = append(shas, c.SHA)
		}
	}
	return
}

// ---- 暫時的 git repo ----

func needGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR", "GIT_COMMITTER"} {
		t.Setenv(k+"_NAME", "Tester")
		t.Setenv(k+"_EMAIL", "tester@example.com")
	}
	t.Setenv("CI", "")
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	b, err := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "user.name=Tester", "-c", "user.email=tester@example.com"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func newRepo(t *testing.T) string {
	needGit(t)
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "remote", "add", "origin", "git@github.com:Acme/App.git")
	return dir
}

func commit(t *testing.T, dir, msg string) string {
	t.Helper()
	f := filepath.Join(dir, "f.txt")
	old, _ := os.ReadFile(f)
	os.WriteFile(f, append(old, []byte(msg+"\n")...), 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", msg)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

func runSync(t *testing.T, cl *client.Client, o commitsOpts) (stdout, stderr string, err error) {
	t.Helper()
	var so, se bytes.Buffer
	err = commitsSync(cl, &so, &se, o)
	return so.String(), se.String(), err
}

func TestCommitsSyncBaseline(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "one")
	commit(t, dir, "two\n\nfixes ISS-1\nsecond line")
	head := commit(t, dir, "three")
	f := &fakeSync{}
	cl := f.server(t)
	out, _, err := runSync(t, cl, commitsOpts{Org: "default", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.sent(); len(got) != 1 || got[0] != head {
		t.Fatalf("第一次只送 HEAD:%v", got)
	}
	r := f.reports[0]
	if r.Repo != "github:acme/app" || r.Source != "cli" || r.Commits[0].Branch != "main" || r.Commits[0].Subject != "three" || r.Commits[0].AuthorName != "Tester" || len(r.Commits[0].Parents) != 1 || r.Commits[0].AuthoredAt == nil {
		t.Fatalf("%+v", r)
	}
	if f.getRepo != "github:acme/app" {
		t.Fatal(f.getRepo)
	}
	if !strings.Contains(out, "baseline set at "+head[:7]+"; future runs send only new commits") || !strings.Contains(out, "ISS-1") || !strings.Contains(out, "ISS-999") {
		t.Fatal(out)
	}
}

func TestCommitsSyncIncrementalBatches(t *testing.T) {
	dir := newRepo(t)
	base := commit(t, dir, "base")
	var want []string
	for i := 0; i < 230; i++ {
		msg := fmt.Sprintf("c%03d", i)
		if i == 5 {
			msg = "multi\n\nbody line 1\nbody line 2\n\nfixes ISS-3"
		}
		want = append(want, commit(t, dir, msg))
	}
	f := &fakeSync{last: base}
	cl := f.server(t)
	out, _, err := runSync(t, cl, commitsOpts{Org: "default", Dir: dir, Source: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reports) != 3 || len(f.reports[0].Commits) != 100 || len(f.reports[1].Commits) != 100 || len(f.reports[2].Commits) != 30 {
		t.Fatalf("批次:%d", len(f.reports))
	}
	got := f.sent()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatal("要舊的在前、不含 cursor 本身")
	}
	if f.reports[0].Source != "ci" {
		t.Fatal(f.reports[0].Source)
	}
	if c := f.reports[0].Commits[5]; c.Subject != "multi" || c.Body != "body line 1\nbody line 2\n\nfixes ISS-3" {
		t.Fatalf("多行訊息:%+v", c)
	}
	if !strings.Contains(out, "sent: 230") || strings.Contains(out, "baseline") {
		t.Fatal(out)
	}
	if strings.Count(out, "ISS-999") != 1 {
		t.Fatal("unmatched 要去重:" + out)
	}
}

func TestCommitsSyncUpToDate(t *testing.T) {
	dir := newRepo(t)
	head := commit(t, dir, "one")
	f := &fakeSync{last: head}
	out, _, err := runSync(t, f.server(t), commitsOpts{Org: "default", Dir: dir})
	if err != nil || len(f.reports) != 0 || !strings.Contains(out, "up to date") {
		t.Fatal(err, out, len(f.reports))
	}
}

func TestCommitsSyncUnknownCursorFallsBackToHead(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "one")
	old := commit(t, dir, "two")
	gitIn(t, dir, "reset", "-q", "--hard", "HEAD~1") // 改寫歷史:old 之後不在 HEAD 的祖先裡
	head := commit(t, dir, "three")
	for name, cursor := range map[string]string{"不是祖先": old, "本機沒有": strings.Repeat("ab", 20)} {
		f := &fakeSync{last: cursor}
		_, errs, err := runSync(t, f.server(t), commitsOpts{Org: "default", Dir: dir})
		if err != nil {
			t.Fatal(name, err)
		}
		if got := f.sent(); len(got) != 1 || got[0] != head {
			t.Fatal(name, got)
		}
		if !strings.Contains(errs, "warning") || !strings.Contains(errs, "baseline") {
			t.Fatal(name, errs)
		}
	}
}

func TestCommitsSyncCap500(t *testing.T) {
	dir := newRepo(t)
	base := commit(t, dir, "base")
	// 用 commit-tree 快速產生 520 筆
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")
	parent := base
	var all []string
	for i := 0; i < 520; i++ {
		parent = gitIn(t, dir, "commit-tree", tree, "-p", parent, "-m", fmt.Sprintf("n%d", i))
		all = append(all, parent)
	}
	gitIn(t, dir, "update-ref", "refs/heads/main", parent)
	f := &fakeSync{last: base}
	_, errs, err := runSync(t, f.server(t), commitsOpts{Org: "default", Dir: dir, JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	got := f.sent()
	if len(got) != 500 || got[0] != all[20] || got[499] != all[519] {
		t.Fatalf("要送最新的 500、舊的在前:%d", len(got))
	}
	if !strings.Contains(errs, "skipped 20") {
		t.Fatal(errs)
	}
}

func TestCommitsSyncDryRun(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "one")
	f := &fakeSync{}
	out, _, err := runSync(t, f.server(t), commitsOpts{Org: "default", Dir: dir, DryRun: true})
	if err != nil || len(f.reports) != 0 || !strings.Contains(out, "nothing was sent") {
		t.Fatal(err, out)
	}
	out, _, err = runSync(t, f.server(t), commitsOpts{Org: "default", Dir: dir, DryRun: true, JSON: true})
	var j commitsSummary
	if err != nil || json.Unmarshal([]byte(out), &j) != nil || !j.DryRun || !j.Baseline || j.Sent != 0 || len(j.Commits) != 1 {
		t.Fatal(err, out)
	}
}

func TestCommitsSyncJSON(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "one")
	f := &fakeSync{}
	out, _, err := runSync(t, f.server(t), commitsOpts{Org: "default", Dir: dir, JSON: true})
	var j commitsSummary
	if err != nil || json.Unmarshal([]byte(out), &j) != nil || j.Repo != "github:acme/app" || j.Sent != 1 || j.Created != 1 || !j.Baseline || len(j.Linked) != 1 || len(j.Unmatched) != 1 {
		t.Fatal(err, out)
	}
}

func TestCommitsSyncErrors(t *testing.T) {
	needGit(t)
	f := &fakeSync{}
	cl := f.server(t)
	// 不是 git repo
	if _, _, err := runSync(t, cl, commitsOpts{Org: "default", Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "--repo") {
		t.Fatal("非 git 目錄要報錯並提示 --repo:", err)
	}
	// 沒有 remote、沒給 --repo
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, "one")
	if _, _, err := runSync(t, cl, commitsOpts{Org: "default", Dir: dir}); err == nil || !strings.Contains(err.Error(), "--repo") {
		t.Fatal(err)
	}
	// 給 --repo 就能跑;沒有 commit 的 repo 報錯
	if _, _, err := runSync(t, cl, commitsOpts{Org: "default", Dir: dir, Repo: "GitHub:Acme/App"}); err != nil || f.reports[0].Repo != "github:acme/app" {
		t.Fatal(err)
	}
	empty := t.TempDir()
	gitIn(t, empty, "init", "-q", "-b", "main")
	if _, _, err := runSync(t, cl, commitsOpts{Org: "default", Dir: empty, Repo: "a/b"}); err == nil || !strings.Contains(err.Error(), "no commits") {
		t.Fatal(err)
	}
	if _, _, err := runSync(t, cl, commitsOpts{Org: "default", Dir: dir, Repo: "bad repo!"}); err == nil {
		t.Fatal("repo 不合格要報錯")
	}
	if _, _, err := runSync(t, cl, commitsOpts{Org: "default", Dir: dir, Repo: "a/b", Source: "app"}); err == nil {
		t.Fatal("source 只能 cli 或 ci")
	}
}

func TestCommitsSyncRateLimited(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "one")
	f := &fakeSync{status: 429}
	_, _, err := runSync(t, f.server(t), commitsOpts{Org: "default", Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "17 minutes") {
		t.Fatal(err)
	}
	f = &fakeSync{status: 500}
	if _, _, err := runSync(t, f.server(t), commitsOpts{Org: "default", Dir: dir}); err == nil {
		t.Fatal("伺服器錯誤要回錯")
	}
}

// 走 Run:旗標、登入資料、CI 環境變數
func TestCLICommitsSyncRun(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "one")
	f := &fakeSync{}
	cl := f.server(t)
	cfg := t.TempDir()
	t.Setenv("OMIXOR_CONFIG_DIR", cfg)
	t.Setenv("OMIXOR_CREDENTIALS", filepath.Join(cfg, "cred.json"))
	t.Setenv("OMIXOR_SERVER", "")
	_ = saveCred(credentials{Server: cl.Base, Token: "omx_test"})
	t.Chdir(dir)
	t.Setenv("CI", "true")
	var b bytes.Buffer
	if err := Run([]string{"commits", "sync", "--server", cl.Base, "--org", "default", "--json"}, &b); err != nil {
		t.Fatal(err)
	}
	if len(f.reports) != 1 || f.reports[0].Source != "ci" || strings.Contains(b.String(), "omx_test") {
		t.Fatalf("%+v %s", f.reports, b.String())
	}
	if err := Run([]string{"commits", "nope", "--server", cl.Base}, &b); err == nil {
		t.Fatal("未知子指令要報錯")
	}
}
