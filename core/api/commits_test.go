package api_test

import (
	"strings"
	"testing"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// commits API(ISS-69):回報、查詢、同步位置、Issue 的連結清單、手動連結與取消;錯誤碼;角色憑證與外部組織;受限專案隱藏連結。
// 同時走過 core/client 與 backend.Remote 用到的每個方法。

func sha40(c byte) string { return strings.Repeat(string(c), 40) }

func TestCommitsAPI(t *testing.T) {
	e := newEnv(t)
	boss, alice, bob := e.issueOrg(t)
	if _, err := boss.CreateOrg("o2", "o2"); err != nil {
		t.Fatal(err)
	}
	iss, err := alice.CreateIssue("lab", service.IssueInput{Title: "登入壞了"})
	if err != nil {
		t.Fatal(err)
	}
	k := iss.Key

	res, err := alice.ReportCommits("lab", service.CommitReport{Repo: "github:Acme/App", Source: "cli", Commits: []service.CommitInput{
		{SHA: sha40('a'), Subject: "Fixes " + k + " and ISS-77"}, {SHA: sha40('b'), Subject: "other"}}})
	if err != nil || res.Reported != 2 || res.Created != 2 || len(res.Linked) != 1 || res.Linked[0].Issue != k || res.Linked[0].Kind != "fixes" || len(res.Unmatched) != 1 || res.Unmatched[0] != "ISS-77" {
		t.Fatalf("%+v %v", res, err)
	}
	// 冪等
	if res, err = alice.ReportCommits("lab", service.CommitReport{Repo: "github:acme/app", Commits: []service.CommitInput{{SHA: sha40('a'), Subject: "Fixes " + k}}}); err != nil || res.Created != 0 || len(res.Linked) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	// 錯誤碼
	if _, err := alice.ReportCommits("lab", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: "abc", Subject: "x"}}}); status(err) != 400 {
		t.Fatal(err)
	}
	if _, err := alice.ReportCommits("lab", service.CommitReport{Repo: "a/b"}); status(err) != 400 {
		t.Fatal(err)
	}
	if _, err := alice.ReportCommits("o2", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: sha40('a'), Subject: "x"}}}); status(err) != 403 {
		t.Fatal("不是成員 403", err)
	}

	// 查詢與同步位置
	q, err := bob.QueryCommits("lab", service.CommitQueryInput{Repo: "ACME/app", Query: "fixes"})
	if err != nil || q.Total != 1 || q.Commits[0].SHA != sha40('a') || len(q.Commits[0].Issues) != 1 || q.Commits[0].Issues[0].Issue != k || q.Commits[0].URL == "" {
		t.Fatalf("%+v %v", q, err)
	}
	if q, err = bob.QueryCommits("lab", service.CommitQueryInput{Issue: k, Limit: 5}); err != nil || q.Total != 1 {
		t.Fatalf("%+v %v", q, err)
	}
	if _, err := bob.QueryCommits("lab", service.CommitQueryInput{SHA: "abc"}); status(err) != 400 {
		t.Fatal(err)
	}
	s, err := bob.GetRepoSync("lab", "acme/app")
	if err != nil || s.LastSHA == "" || s.LastSource == "" || s.LastAt == nil {
		t.Fatalf("%+v %v", s, err)
	}
	if s, err = bob.GetRepoSync("lab", "never/seen"); err != nil || s.LastSHA != "" {
		t.Fatalf("%+v %v", s, err)
	}

	// Issue 的連結清單、手動連結與取消
	l, err := bob.ListIssueCommits("lab", k, 10)
	if err != nil || l.Total != 1 || l.Issue != k || l.Commits[0].SHA7 != "aaaaaaa" || l.Commits[0].Kind != "fixes" {
		t.Fatalf("%+v %v", l, err)
	}
	v, err := bob.LinkIssueCommit("lab", k, service.CommitLinkInput{Repo: "acme/app", SHA: sha40('c'), Kind: "found_in", Note: "v1"})
	if err != nil || v.Kind != "found_in" || v.Origin != "manual" || v.Issue != k || v.LinkedBy != "bob@example.com" || v.LinkedByActor == nil {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := bob.LinkIssueCommit("lab", "ISS-404", service.CommitLinkInput{Repo: "acme/app", SHA: sha40('c')}); status(err) != 404 {
		t.Fatal(err)
	}
	if err := bob.UnlinkIssueCommit("lab", k, service.CommitUnlinkInput{Repo: "acme/app", SHA: "ccccccc"}); err != nil {
		t.Fatal(err)
	}
	if err := bob.UnlinkIssueCommit("lab", k, service.CommitUnlinkInput{Repo: "acme/app", SHA: sha40('c')}); status(err) != 404 {
		t.Fatal(err)
	}
	if l, _ = bob.ListIssueCommits("lab", k, 0); l.Total != 1 {
		t.Fatal(l)
	}

	// 角色憑證:路由只收 omx_ 使用者 token,一律被拒
	if _, err := boss.SaveAgent("lab", model.Agent{ID: "cto", Name: "研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	tk, err := boss.AssignTask("lab", service.TaskInput{AgentID: "cto", Title: "做事"})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := alice.ClaimTask("lab", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := client.New(e.srv.URL)
	cred := "cto:" + cl.Credential.Secret
	role.Token = func() string { return cred }
	for name, f := range map[string]func() error{
		"report": func() error {
			_, err := role.ReportCommits("lab", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: sha40('d'), Subject: "x"}}})
			return err
		},
		"query": func() error { _, err := role.QueryCommits("lab", service.CommitQueryInput{}); return err },
		"sync":  func() error { _, err := role.GetRepoSync("lab", "acme/app"); return err },
		"list":  func() error { _, err := role.ListIssueCommits("lab", k, 10); return err },
		"link": func() error {
			_, err := role.LinkIssueCommit("lab", k, service.CommitLinkInput{Repo: "a/b", SHA: sha40('d')})
			return err
		},
		"unlink": func() error {
			return role.UnlinkIssueCommit("lab", k, service.CommitUnlinkInput{Repo: "a/b", SHA: sha40('a')})
		},
	} {
		if st := status(f()); st != 401 && st != 403 {
			t.Errorf("角色憑證 %s 要被拒,得到 %d", name, st)
		}
	}
	if q, _ := bob.QueryCommits("lab", service.CommitQueryInput{SHA: "ddddddd"}); q.Total != 0 {
		t.Fatal("被拒的角色憑證不能寫入")
	}
}

// 受限專案:非成員回報提到受限 Issue 不連結;連結清單、依 Issue 查、手動連結對非成員都是 404,訊息和不存在的 Issue 相同;commit 本身看得到
func TestCommitsAPIRestrictedProject(t *testing.T) {
	e := newEnv(t)
	boss, alice, _ := e.issueOrg(t)
	p, err := boss.CreateProject("lab", service.ProjectInput{Name: "機密", Key: "SEC", Restricted: true})
	if err != nil || p.ID == "" {
		t.Fatal(p, err)
	}
	sec, err := boss.CreateIssue("lab", service.IssueInput{Title: "機密事項", Project: &p.ID})
	if err != nil || sec.Key != "SEC-1" {
		t.Fatalf("%+v %v", sec, err)
	}
	res, err := alice.ReportCommits("lab", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: sha40('a'), Subject: "Fixes SEC-1, SEC-42"}}})
	if err != nil || len(res.Linked) != 0 || len(res.Unmatched) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = boss.ReportCommits("lab", service.CommitReport{Repo: "a/b", Commits: []service.CommitInput{{SHA: sha40('b'), Subject: "Fixes SEC-1"}}})
	if err != nil || len(res.Linked) != 1 || res.Linked[0].Issue != "SEC-1" {
		t.Fatalf("%+v %v", res, err)
	}
	_, e1 := alice.ListIssueCommits("lab", "SEC-1", 10)
	_, e2 := alice.ListIssueCommits("lab", "SEC-777", 10)
	if status(e1) != 404 || status(e2) != 404 || e1.Error() != strings.ReplaceAll(e2.Error(), "SEC-777", "SEC-1") {
		t.Fatalf("要和不存在一樣:%v / %v", e1, e2)
	}
	if _, err := alice.QueryCommits("lab", service.CommitQueryInput{Issue: "SEC-1"}); status(err) != 404 {
		t.Fatal(err)
	}
	if _, err := alice.LinkIssueCommit("lab", "SEC-1", service.CommitLinkInput{Repo: "a/b", SHA: sha40('a')}); status(err) != 404 {
		t.Fatal(err)
	}
	q, err := alice.QueryCommits("lab", service.CommitQueryInput{Repo: "a/b"})
	if err != nil || q.Total != 2 {
		t.Fatalf("commit 本身看得到:%+v %v", q, err)
	}
	for _, c := range q.Commits {
		if len(c.Issues) != 0 {
			t.Fatalf("連結要隱藏:%+v", c)
		}
	}
}
