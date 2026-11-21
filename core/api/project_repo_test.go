package api_test

import (
	"slices"
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 專案與 repo 的連結路由:透過 client 與 backend.Remote。成員可看、組織管理員連結與取消;
// 建立 Issue 沒給 project 依連結預設、給空字串不分專案(JSON 有沒有 project 欄位要分得出來);依 repo 篩選考慮連結
func TestProjectRepoRoutes(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("lab", "bob@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	bob := e.login("evo-bob")
	rb, rboss := backend.NewRemote(bob), backend.NewRemote(boss)
	p, err := rboss.CreateProject("lab", service.ProjectInput{Name: "官網", Key: "WEB"})
	if err != nil || p.ID != "PRJ-1" || p.Repos == nil || len(p.Repos) != 0 {
		t.Fatal(p, err)
	}

	if _, err := rb.LinkProjectRepo("lab", p.ID, "acme/web"); backend.Code(err) != 403 {
		t.Fatal("成員不能連結", err)
	}
	if _, err := rboss.LinkProjectRepo("lab", "PRJ-9", "acme/web"); backend.Code(err) != 404 {
		t.Fatal("專案不存在", err)
	}
	if _, err := rboss.LinkProjectRepo("lab", p.ID, "bad repo"); backend.Code(err) != 400 {
		t.Fatal("repo 格式", err)
	}
	x, err := rboss.LinkProjectRepo("lab", "prj-1", "Acme/Web")
	if err != nil || !slices.Equal(x.Repos, []string{"Acme/Web"}) {
		t.Fatal(x, err)
	}
	if x, err = rboss.LinkProjectRepo("lab", p.ID, "github:acme/web"); err != nil || len(x.Repos) != 1 {
		t.Fatal("重複連結", x, err)
	}
	// 成員看得到
	if l, err := rb.ListProjects("lab"); err != nil || len(l) != 1 || !slices.Equal(l[0].Repos, []string{"Acme/Web"}) {
		t.Fatal(l, err)
	}
	if l, err := bob.ListProjectRepos("lab", ""); err != nil || len(l) != 1 || l[0].Project != p.ID || l[0].Repo != "Acme/Web" {
		t.Fatal(l, err)
	}
	if l, err := bob.ListProjectRepos("lab", "PRJ-1"); err != nil || len(l) != 1 {
		t.Fatal(l, err)
	}
	if _, err := bob.ListProjectRepos("lab", "PRJ-9"); backend.Code(err) != 404 {
		t.Fatal(err)
	}

	// 建立 Issue:沒給 project → 預設;給空字串 → 不分專案
	in := service.IssueInput{Title: "t", Repo: "acme/WEB"}
	i, err := bob.CreateIssue("lab", in)
	if err != nil || i.Project != p.ID || i.Key != "WEB-1" {
		t.Fatal("依連結預設", i, err)
	}
	empty := ""
	in.Project = &empty
	if i, err = bob.CreateIssue("lab", in); err != nil || i.Project != "" || i.Key != "ISS-1" {
		t.Fatal("空字串 = 不分專案", i, err)
	}
	// 篩選:repo=acme/web → 自己標的都算(兩張);專案內沒標 repo 的 Issue 也算
	in.Repo, in.Project = "", &p.ID
	untagged, err := bob.CreateIssue("lab", in)
	if err != nil || untagged.Project != p.ID {
		t.Fatal(untagged, err)
	}
	l, err := bob.QueryIssues("lab", model.IssueQuery{Repo: "acme/web"})
	if err != nil || len(l) != 3 {
		t.Fatal("連結的專案裡沒標 repo 的 Issue 也算", len(l), err)
	}

	// 取消
	if _, err := rb.UnlinkProjectRepo("lab", p.ID, "acme/web"); backend.Code(err) != 403 {
		t.Fatal("成員不能取消", err)
	}
	if x, err = rboss.UnlinkProjectRepo("lab", p.ID, "ACME/web"); err != nil || len(x.Repos) != 0 {
		t.Fatal(x, err)
	}
	if _, err := rboss.UnlinkProjectRepo("lab", p.ID, "acme/web"); backend.Code(err) != 404 {
		t.Fatal("沒連過", err)
	}
	if l, err = bob.QueryIssues("lab", model.IssueQuery{Repo: "acme/web"}); err != nil || len(l) != 2 {
		t.Fatal("取消後只看自己標的", len(l), err)
	}
}
