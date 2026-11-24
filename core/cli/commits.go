package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// omixor commits sync:把「目前這個 git repo 的新 commit」讀進 omixor(ISS-69,設計見 docs/design-commits.md、用法見 docs/commits.md)。
// 只送 commit metadata(sha、訊息、作者、時間、分支、父 commit),不送 diff 與檔案。
//
//	omixor commits sync [--repo github:owner/name] [--org default] [--source cli|ci] [--dry-run] [--json]
//
// 起點規則(phase 1 刻意不補舊歷史,2026-11-24 決定,沒有 backfill 旗標):
//
//	伺服器沒看過這個 repo(沒有同步位置)      只送目前的 HEAD 一筆當基準點,之後只送新的
//	有同步位置、它在本機而且是 HEAD 的祖先    送 <同步位置>..HEAD(舊的在前),每批最多 100 筆;沒有新的 = up to date
//	有同步位置、但本機沒有或不是祖先            不猜:只送 HEAD 當新的基準點,stderr 警告(改寫過歷史、或這個 clone 沒有那筆)
//	任何情況一次最多 500 筆(最新的 500 筆),多的略過並警告
const (
	commitsMaxPerRun = 500
	commitsBatch     = model.CommitMaxBatch
)

// commitsStderr 警告寫的地方(測試可換)
var commitsStderr io.Writer = os.Stderr

type commitsOpts struct {
	Repo   string // --repo;空 = 從 git remote 推導
	Org    string
	Source string // cli、ci;空 = 依環境變數 CI
	DryRun bool
	JSON   bool
	Dir    string // git 的工作目錄;空 = 目前目錄
}

// commitsSummary --json 的輸出
type commitsSummary struct {
	Repo      string                 `json:"repo"`
	Baseline  bool                   `json:"baseline"`
	UpToDate  bool                   `json:"upToDate,omitempty"`
	DryRun    bool                   `json:"dryRun,omitempty"`
	Sent      int                    `json:"sent"`
	Created   int                    `json:"created"`
	Linked    []service.CommitLinked `json:"linked"`
	Unmatched []string               `json:"unmatched"`
	Skipped   int                    `json:"skipped,omitempty"`
	Commits   []string               `json:"commits,omitempty"` // dry-run:會送的 sha(舊的在前)

	baselineSHA string
}

func commitsCmd(cl *client.Client, out io.Writer, sub string, o commitsOpts) error {
	if sub != "sync" {
		return errors.New("usage: omixor commits sync [--repo github:owner/name] [--org default] [--source cli|ci] [--dry-run] [--json]")
	}
	return commitsSync(cl, out, commitsStderr, o)
}

// ---- repo 推導 ----

var scpLike = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]+):(.+)$`)

// parseRepoURL git remote 網址 → omixor repo key:github.com → github:owner/name、host 含 gitlab → gitlab:group/sub/project、其他 → owner/name
// 支援 https://、ssh://、git://、scp 式(git@host:owner/name.git);去 .git、轉小寫。認不出來回 false
func parseRepoURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		host, path = strings.ToLower(u.Hostname()), u.Path
	} else if m := scpLike.FindStringSubmatch(raw); m != nil {
		host, path = strings.ToLower(m[1]), m[2]
	} else {
		return "", false
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if host == "" || strings.Count(path, "/") < 1 {
		return "", false
	}
	prefix := ""
	switch {
	case host == "github.com" || host == "www.github.com":
		prefix = "github:"
	case strings.Contains(host, "gitlab"):
		prefix = "gitlab:"
	}
	return model.CommitRepoKey(prefix + path)
}

// ---- git ----

type gitErr struct{ msg string }

func (e *gitErr) Error() string { return e.msg }

// git 在 dir 跑 git(參數是 slice,不經 shell);回 stdout
func git(dir string, args ...string) ([]byte, int, error) {
	full := append([]string{}, args...)
	if dir != "" {
		full = append([]string{"-C", dir}, full...)
	}
	cmd := exec.Command("git", full...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	err := cmd.Run()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
			return so.Bytes(), code, &gitErr{fmt.Sprintf("git %s failed: %s", args[0], strings.TrimSpace(se.String()))}
		}
		return nil, code, &gitErr{"cannot run git: " + err.Error()}
	}
	return so.Bytes(), 0, nil
}

func gitStr(dir string, args ...string) (string, error) {
	b, _, err := git(dir, args...)
	return strings.TrimSpace(string(b)), err
}

var reFullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func detectRepo(dir string) (string, error) {
	remotes, err := gitStr(dir, "remote")
	if err != nil {
		return "", fmt.Errorf("%v (is this a git repository? pass --repo to set the repo explicitly)", err)
	}
	var names []string
	for _, n := range strings.Fields(remotes) {
		if n == "origin" {
			names = append([]string{n}, names...)
		} else {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "", errors.New("cannot determine the repo: no git remote; pass --repo (for example --repo github:owner/name)")
	}
	u, err := gitStr(dir, "remote", "get-url", names[0])
	if err != nil {
		return "", err
	}
	key, ok := parseRepoURL(u)
	if !ok {
		return "", fmt.Errorf("cannot determine the repo from remote %q; pass --repo (for example --repo github:owner/name)", names[0])
	}
	return key, nil
}

const logFormat = "%H%x1f%P%x1f%an%x1f%aI%x1f%cI%x1f%s%x1f%b%x1e"

func parseLog(b []byte, branch string) ([]service.CommitInput, error) {
	var res []service.CommitInput
	for _, rec := range strings.Split(string(b), "\x1e") {
		rec = strings.Trim(rec, "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 7)
		if len(f) < 7 {
			return nil, fmt.Errorf("unexpected git log output: %q", rec)
		}
		if !reFullSHA.MatchString(f[0]) {
			return nil, fmt.Errorf("unexpected git log output (sha %q)", f[0])
		}
		c := service.CommitInput{SHA: f[0], Subject: f[5], Body: strings.TrimSpace(f[6]), AuthorName: f[2], Branch: branch}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		if t, err := time.Parse(time.RFC3339, f[3]); err == nil {
			mt := model.Time{Time: t}
			c.AuthoredAt = &mt
		}
		if t, err := time.Parse(time.RFC3339, f[4]); err == nil {
			mt := model.Time{Time: t}
			c.CommittedAt = &mt
		}
		res = append(res, c)
	}
	return res, nil
}

// readCommits 讀 git log(新的在前,最多 limit 筆)後反轉成舊的在前
func readCommits(dir, branch string, limit int, rev ...string) ([]service.CommitInput, error) {
	args := []string{"log", "--no-color", "--no-show-signature", "--encoding=UTF-8", "--topo-order", fmt.Sprintf("--max-count=%d", limit), "--format=" + logFormat}
	args = append(args, rev...)
	b, _, err := git(dir, args...)
	if err != nil {
		return nil, err
	}
	l, err := parseLog(b, branch)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
		l[i], l[j] = l[j], l[i]
	}
	return l, nil
}

func short(sha string) string {
	if len(sha) > model.CommitShortSHA {
		return sha[:model.CommitShortSHA]
	}
	return sha
}

// ---- sync ----

func commitsSync(cl *client.Client, out, errw io.Writer, o commitsOpts) error {
	src := o.Source
	if src == "" {
		src = model.CommitSourceCLI
		if os.Getenv("CI") == "true" {
			src = model.CommitSourceCI
		}
	}
	if src != model.CommitSourceCLI && src != model.CommitSourceCI {
		return fmt.Errorf("--source must be cli or ci, got %q", src)
	}
	repo := o.Repo
	if repo != "" {
		k, ok := model.CommitRepoKey(repo)
		if !ok {
			return fmt.Errorf("invalid --repo %q (use owner/name, github:owner/name or gitlab:group/sub/project)", repo)
		}
		repo = k
	} else {
		r, err := detectRepo(o.Dir)
		if err != nil {
			return err
		}
		repo = r
	}
	headSHA, err := gitStr(o.Dir, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil || !reFullSHA.MatchString(headSHA) {
		return errors.New("git: this repository has no commits yet (HEAD does not exist)")
	}
	// 目前分支(detached HEAD 是空字串)
	branch := ""
	if b, _, err := git(o.Dir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		branch = strings.TrimSpace(string(b))
	}

	cur, err := cl.GetRepoSync(o.Org, repo)
	if err != nil {
		return commitsServerErr(err)
	}
	sum := commitsSummary{Repo: repo, DryRun: o.DryRun, Linked: []service.CommitLinked{}, Unmatched: []string{}}
	var toSend []service.CommitInput
	cursor := strings.ToLower(strings.TrimSpace(cur.LastSHA))
	baseline := func() error {
		l, err := readCommits(o.Dir, branch, 1, headSHA)
		if err != nil {
			return err
		}
		toSend, sum.Baseline = l, true
		if len(l) > 0 {
			sum.baselineSHA = l[0].SHA
		}
		return nil
	}
	switch {
	case cursor == "":
		if err := baseline(); err != nil {
			return err
		}
	case !reFullSHA.MatchString(cursor) || !gitHasAncestor(o.Dir, cursor, headSHA):
		fmt.Fprintf(errw, "warning: the server's sync position %s is not in this clone or is not an ancestor of HEAD (history was rewritten, or this is a different clone); sending only HEAD as a new baseline, no backfill\n", short(cursor))
		if err := baseline(); err != nil {
			return err
		}
	default:
		total, err := gitStr(o.Dir, "rev-list", "--count", cursor+".."+headSHA)
		if err != nil {
			return err
		}
		var n int
		fmt.Sscanf(total, "%d", &n)
		if n > commitsMaxPerRun {
			sum.Skipped = n - commitsMaxPerRun
			fmt.Fprintf(errw, "warning: %d new commits; sending only the newest %d, skipped %d older ones (no backfill)\n", n, commitsMaxPerRun, sum.Skipped)
		}
		if toSend, err = readCommits(o.Dir, branch, commitsMaxPerRun, cursor+".."+headSHA); err != nil {
			return err
		}
	}
	if len(toSend) == 0 {
		sum.UpToDate = true
		return commitsPrint(out, o, sum, nil)
	}
	for _, c := range toSend {
		sum.Commits = append(sum.Commits, c.SHA)
	}
	if o.DryRun {
		return commitsPrint(out, o, sum, toSend)
	}
	for i := 0; i < len(toSend); i += commitsBatch {
		end := min(i+commitsBatch, len(toSend))
		res, err := cl.ReportCommits(o.Org, service.CommitReport{Repo: repo, Source: src, Commits: toSend[i:end]})
		if err != nil {
			if sum.Sent > 0 {
				fmt.Fprintf(errw, "warning: sent %d of %d commits before the error; run again to continue\n", sum.Sent, len(toSend))
			}
			return commitsServerErr(err)
		}
		sum.Sent += len(toSend[i:end])
		sum.Created += res.Created
		sum.Linked = append(sum.Linked, res.Linked...)
		for _, u := range res.Unmatched {
			if !containsStr(sum.Unmatched, u) {
				sum.Unmatched = append(sum.Unmatched, u)
			}
		}
	}
	sum.Commits = nil
	return commitsPrint(out, o, sum, nil)
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// gitHasAncestor cursor 在本機而且是 head 的祖先(或就是 head)
func gitHasAncestor(dir, cursor, head string) bool {
	if _, _, err := git(dir, "cat-file", "-e", cursor+"^{commit}"); err != nil {
		return false
	}
	_, code, err := git(dir, "merge-base", "--is-ancestor", cursor, head)
	return err == nil && code == 0
}

func commitsServerErr(err error) error {
	var e *client.Error
	if errors.As(err, &e) && e.Status == 429 {
		hint := ""
		if m, ok := e.Data["retryMinutes"]; ok {
			hint = fmt.Sprintf(" (retry in about %v minutes)", m)
		}
		return fmt.Errorf("rate limited by the server (429): %s%s", e.Msg, hint)
	}
	return err
}

func commitsPrint(out io.Writer, o commitsOpts, s commitsSummary, plan []service.CommitInput) error {
	if o.JSON {
		printJSON(out, s)
		return nil
	}
	fmt.Fprintf(out, "repo: %s\n", s.Repo)
	if s.UpToDate {
		fmt.Fprintln(out, "up to date")
		return nil
	}
	if o.DryRun {
		fmt.Fprintf(out, "dry run: would send %d commit(s) in %d batch(es); nothing was sent\n", len(plan), (len(plan)+commitsBatch-1)/commitsBatch)
		for _, c := range plan {
			fmt.Fprintf(out, "  %s %s\n", short(c.SHA), c.Subject)
		}
		if s.Baseline {
			fmt.Fprintf(out, "baseline would be set at %s; future runs send only new commits\n", short(plan[0].SHA))
		}
		return nil
	}
	fmt.Fprintf(out, "sent: %d, created: %d, linked: %d\n", s.Sent, s.Created, len(s.Linked))
	for _, l := range s.Linked {
		fmt.Fprintf(out, "  %s %s -> %s\n", l.SHA, l.Kind, l.Issue)
	}
	if len(s.Unmatched) > 0 {
		fmt.Fprintf(out, "unmatched issue keys: %s\n", strings.Join(s.Unmatched, ", "))
	}
	if s.Baseline {
		fmt.Fprintf(out, "baseline set at %s; future runs send only new commits\n", short(s.baselineSHA))
	}
	return nil
}
