package store

import (
	"errors"
	"strings"
	"testing"

	"omixor-core/model"
)

// commits(ISS-69)的 store 層測試:冪等、verified 只升不降、裸參照、連結規則、查詢、cascade、結構。

func sha(c byte) string { return strings.Repeat(string(c), 40) }

func commitFixture(t *testing.T) (*PG, string, string, string) {
	t.Helper()
	st := testPG(t)
	newOrg(t, st, "o", "o2")
	at := mt("2026-01-02T03:04:05Z")
	alice := lid(t, st, "o", "user:a")
	i1, err := st.CreateIssue("o", model.Issue{Title: "一", Status: "open", Priority: "normal", CreatedBy: alice, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	return st.(*PG), alice, i1.ID, "o"
}

func mkCommit(repo string, c byte, subject string) model.Commit {
	at := mt("2026-01-02T03:04:05Z")
	return model.Commit{Repo: repo, SHA: sha(c), Subject: subject, Body: "body " + subject, AuthorName: "A", AuthoredAt: &at, Source: model.CommitSourceCLI,
		Parents: []string{sha('0')}, ReportedAt: at}
}

func TestCommitUpsertIdempotentAndNeverDowngradesVerified(t *testing.T) {
	p, alice, _, org := commitFixture(t)
	c := mkCommit("a/b", 'a', "first")
	c.ReportedBy = alice
	n, err := p.UpsertCommits(org, []model.Commit{c, mkCommit("a/b", 'b', "second")})
	chk(t, err)
	if n != 2 {
		t.Fatal("created", n)
	}
	n, err = p.UpsertCommits(org, []model.Commit{c})
	chk(t, err)
	if n != 0 {
		t.Fatal("resend must not create", n)
	}
	list, total, err := p.QueryCommits(org, CommitQuery{Repo: "a/b"})
	chk(t, err)
	if total != 2 || len(list) != 2 {
		t.Fatal(total, list)
	}
	// verified 只升不降
	v := c
	v.Verified = true
	_, err = p.UpsertCommits(org, []model.Commit{v})
	chk(t, err)
	_, err = p.UpsertCommits(org, []model.Commit{c})
	chk(t, err)
	got, err := p.GetCommit(org, "a/b", sha('a'))
	chk(t, err)
	if !got.Verified || got.Subject != "first" || got.ReportedBy != alice || len(got.Parents) != 1 || got.AuthoredAt == nil {
		t.Fatalf("%+v", got)
	}
	// 另一個組織互不可見
	if _, err := p.GetCommit("o2", "a/b", sha('a')); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// 同一 sha 在不同 repo 是不同列
	n, err = p.UpsertCommits(org, []model.Commit{mkCommit("x/y", 'a', "other repo")})
	chk(t, err)
	if n != 1 {
		t.Fatal(n)
	}
	// sha 格式由資料庫約束把關
	bad := mkCommit("a/b", 'c', "bad")
	bad.SHA = "ABC"
	if _, err := p.UpsertCommits(org, []model.Commit{bad}); err == nil {
		t.Fatal("expected check violation")
	}
}

func TestCommitBareRefFilledByRealReport(t *testing.T) {
	p, _, _, org := commitFixture(t)
	chk(t, p.EnsureCommitRef(org, model.Commit{Repo: "a/b", SHA: sha('d'), Source: model.CommitSourceManual}))
	got, err := p.GetCommit(org, "a/b", sha('d'))
	chk(t, err)
	if got.Subject != "" || got.Source != "manual" {
		t.Fatalf("%+v", got)
	}
	n, err := p.UpsertCommits(org, []model.Commit{mkCommit("a/b", 'd', "real")})
	chk(t, err)
	if n != 0 { // 補齊裸參照不算新建
		t.Fatal(n)
	}
	got, err = p.GetCommit(org, "a/b", sha('d'))
	chk(t, err)
	if got.Subject != "real" || got.Source != "cli" || got.Body == "" {
		t.Fatalf("%+v", got)
	}
	// 裸參照不會蓋掉已有的內容
	chk(t, p.EnsureCommitRef(org, model.Commit{Repo: "a/b", SHA: sha('d'), Source: model.CommitSourceManual}))
	got, _ = p.GetCommit(org, "a/b", sha('d'))
	if got.Subject != "real" {
		t.Fatal(got.Subject)
	}
}

func TestIssueCommitLinkRules(t *testing.T) {
	p, alice, iss, org := commitFixture(t)
	_, err := p.UpsertCommits(org, []model.Commit{mkCommit("a/b", 'a', "one"), mkCommit("a/b", 'b', "two")})
	chk(t, err)
	link := func(c byte, kind, origin, note string) bool {
		t.Helper()
		ch, err := p.LinkIssueCommit(org, model.IssueCommit{Issue: iss, Repo: "a/b", SHA: sha(c), Kind: kind, Origin: origin, LinkedBy: alice, Note: note})
		chk(t, err)
		return ch
	}
	if !link('a', "mentions", "auto", "") {
		t.Fatal("first link")
	}
	if link('a', "mentions", "auto", "") {
		t.Fatal("repeat is a no-op")
	}
	if !link('a', "fixes", "auto", "") { // auto mentions -> fixes 升級
		t.Fatal("upgrade")
	}
	if link('a', "mentions", "auto", "") { // 不降級
		t.Fatal("no downgrade")
	}
	if !link('a', "found_in", "manual", "seen here") { // 手動覆蓋
		t.Fatal("manual override")
	}
	if link('a', "fixes", "auto", "") { // 自動不蓋手動
		t.Fatal("auto must not overwrite manual")
	}
	if link('a', "found_in", "manual", "seen here") {
		t.Fatal("identical manual is a no-op")
	}
	link('b', "mentions", "auto", "")
	views, total, err := p.ListIssueCommits(org, iss, 1)
	chk(t, err)
	if total != 2 || len(views) != 1 {
		t.Fatal(total, views)
	}
	views, _, err = p.ListIssueCommits(org, iss, 0)
	chk(t, err)
	byKind := map[string]string{}
	for _, v := range views {
		byKind[v.SHA7] = v.Kind + "/" + v.Origin + "/" + v.Note
	}
	if byKind[sha('a')[:7]] != "found_in/manual/seen here" || len(views) != 2 || views[0].Subject == "" || views[0].IssueID != iss {
		t.Fatal(byKind, views)
	}
	links, err := p.ListCommitLinks(org, "a/b", []string{sha('a'), sha('b'), sha('c')})
	chk(t, err)
	if len(links[sha('a')]) != 1 || len(links[sha('b')]) != 1 || len(links[sha('c')]) != 0 {
		t.Fatal(links)
	}
	// 查詢:依 Issue
	list, total, err := p.QueryCommits(org, CommitQuery{Issue: iss})
	chk(t, err)
	if total != 2 || len(list) != 2 {
		t.Fatal(total)
	}
	// 解除
	chk(t, p.UnlinkIssueCommit(org, iss, "a/b", sha('a')))
	if err := p.UnlinkIssueCommit(org, iss, "a/b", sha('a')); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// commit 不存在、Issue 不存在 → ErrBadRef
	if _, err := p.LinkIssueCommit(org, model.IssueCommit{Issue: iss, Repo: "a/b", SHA: sha('f'), Kind: "mentions", Origin: "auto"}); !errors.Is(err, ErrBadRef) {
		t.Fatal(err)
	}
	if _, err := p.LinkIssueCommit(org, model.IssueCommit{Issue: "nope", Repo: "a/b", SHA: sha('b'), Kind: "mentions", Origin: "auto"}); !errors.Is(err, ErrBadRef) {
		t.Fatal(err)
	}
	// 種類由資料庫約束把關
	if _, err := p.LinkIssueCommit(org, model.IssueCommit{Issue: iss, Repo: "a/b", SHA: sha('b'), Kind: "weird", Origin: "auto"}); err == nil {
		t.Fatal("expected check violation")
	}
}

func TestQueryCommitsFilters(t *testing.T) {
	p, _, _, org := commitFixture(t)
	a := mkCommit("a/b", 'a', "Fix login bug")
	b := mkCommit("a/b", 'b', "Add 100%_done feature")
	b.Body = "touches the parser"
	_, err := p.UpsertCommits(org, []model.Commit{a, b, mkCommit("x/y", 'c', "other")})
	chk(t, err)
	check := func(q CommitQuery, want int) {
		t.Helper()
		l, total, err := p.QueryCommits(org, q)
		chk(t, err)
		if total != want || len(l) != want {
			t.Fatalf("%+v: total %d len %d want %d", q, total, len(l), want)
		}
	}
	check(CommitQuery{}, 3)
	check(CommitQuery{Repo: "a/b"}, 2)
	check(CommitQuery{SHAPrefix: sha('a')[:7]}, 1)
	check(CommitQuery{Query: "LOGIN"}, 1)
	check(CommitQuery{Query: "parser"}, 1) // body
	check(CommitQuery{Query: "100%_done"}, 1)
	check(CommitQuery{Query: "%"}, 1) // 當純文字,不是萬用字元
	check(CommitQuery{Query: "fix feature"}, 0)
	l, total, err := p.QueryCommits(org, CommitQuery{Limit: 1})
	chk(t, err)
	if total != 3 || len(l) != 1 {
		t.Fatal(total, len(l))
	}
	if _, _, err := p.QueryCommits("o2", CommitQuery{}); err != nil {
		t.Fatal(err)
	}
	if l, total, _ := p.QueryCommits("o2", CommitQuery{}); total != 0 || len(l) != 0 {
		t.Fatal("cross-org leak")
	}
}

func TestRepoSyncRoundTrip(t *testing.T) {
	p, _, _, org := commitFixture(t)
	if _, err := p.GetRepoSync(org, "a/b"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	chk(t, p.PutRepoSync(org, model.RepoSync{Repo: "a/b", LastSHA: sha('a'), LastSource: "cli"}))
	chk(t, p.PutRepoSync(org, model.RepoSync{Repo: "a/b", LastSHA: sha('b'), LastSource: "ci"}))
	s, err := p.GetRepoSync(org, "a/b")
	chk(t, err)
	if s.LastSHA != sha('b') || s.LastSource != "ci" || s.LastAt.IsZero() {
		t.Fatalf("%+v", s)
	}
	if _, err := p.GetRepoSync("o2", "a/b"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCommitsCascadeOnIssuePurgeAndOrgDelete(t *testing.T) {
	p, alice, iss, org := commitFixture(t)
	_, err := p.UpsertCommits(org, []model.Commit{mkCommit("a/b", 'a', "one")})
	chk(t, err)
	_, err = p.LinkIssueCommit(org, model.IssueCommit{Issue: iss, Repo: "a/b", SHA: sha('a'), Kind: "fixes", Origin: "auto", LinkedBy: alice})
	chk(t, err)
	chk(t, p.PutRepoSync(org, model.RepoSync{Repo: "a/b", LastSHA: sha('a')}))
	// 永久刪除 Issue:連結跟著刪,commit 還在
	pgExec(t, p, `delete from omixor_issues where org = 'o' and id = '`+iss+`'`)
	if n := pgCount(t, p, `select count(*) from omixor_issue_commits where org = 'o'`); n != 0 {
		t.Fatal("links must cascade", n)
	}
	if _, err := p.GetCommit(org, "a/b", sha('a')); err != nil {
		t.Fatal(err)
	}
	// 刪組織:全部清掉
	chk(t, p.DeleteOrg("o"))
	for _, tb := range []string{"omixor_commits", "omixor_issue_commits", "omixor_repo_sync"} {
		if n := pgCount(t, p, `select count(*) from `+tb+` where org = 'o'`); n != 0 {
			t.Fatal(tb, n)
		}
	}
}

// 結構:三張表在、版本 2 已記錄、重複套用步驟是 no-op
func TestCommitsSchemaStep(t *testing.T) {
	p, _, _, _ := commitFixture(t)
	for _, tb := range []string{"omixor_commits", "omixor_issue_commits", "omixor_repo_sync"} {
		if n := pgCount(t, p, `select count(*) from information_schema.tables where table_schema = current_schema() and table_name = $1`, tb); n != 1 {
			t.Fatal("missing table", tb)
		}
	}
	if n := pgCount(t, p, `select count(*) from omixor_schema_version where version = 2 and name = 'commits'`); n != 1 {
		t.Fatal("step 2 not recorded")
	}
	c, cancel := ctx()
	defer cancel()
	if _, err := p.pool.Exec(c, commitsSchemaSQL); err != nil { // 冪等:再執行一次不失敗
		t.Fatal(err)
	}
}

// FindCommitRepos 與 IssueCommitStats:repo 解析與連結上限用的查詢
func TestFindCommitReposAndIssueCommitStats(t *testing.T) {
	p, alice, iss, org := commitFixture(t)
	_, err := p.UpsertCommits(org, []model.Commit{mkCommit("github:acme/app", 'a', "one"), mkCommit("gitlab:acme/app", 'a', "one"), mkCommit("acme/app", 'b', "two"), mkCommit("github:other/x", 'c', "x")})
	chk(t, err)
	got, err := p.FindCommitRepos(org, "acme/app")
	chk(t, err)
	if strings.Join(got, ",") != "acme/app,github:acme/app,gitlab:acme/app" {
		t.Fatal(got)
	}
	if got, _ = p.FindCommitRepos(org, "nope/x"); len(got) != 0 {
		t.Fatal(got)
	}
	if got, _ = p.FindCommitRepos("o2", "acme/app"); len(got) != 0 {
		t.Fatal("跨組織", got)
	}
	// 連結數與裸參照數
	chk(t, p.EnsureCommitRef(org, model.Commit{Repo: "github:acme/app", SHA: sha('d'), Source: model.CommitSourceManual}))
	for _, c := range []byte{'a', 'd'} {
		_, err := p.LinkIssueCommit(org, model.IssueCommit{Issue: iss, Repo: "github:acme/app", SHA: sha(c), Kind: "mentions", Origin: "auto", LinkedBy: alice})
		chk(t, err)
	}
	st, err := p.IssueCommitStats(org, iss, "github:acme/app", sha('d'))
	chk(t, err)
	if st.Total != 2 || st.Bare != 1 || !st.Linked {
		t.Fatalf("%+v", st)
	}
	if st, _ = p.IssueCommitStats(org, iss, "gitlab:acme/app", sha('a')); st.Linked || st.Total != 2 {
		t.Fatalf("同 sha 不同平台是另一條連結:%+v", st)
	}
	if st, _ = p.IssueCommitStats(org, "ISSX", "x", sha('a')); st.Total != 0 || st.Linked {
		t.Fatalf("%+v", st)
	}
}
