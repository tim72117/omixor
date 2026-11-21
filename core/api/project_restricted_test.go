package api_test

import (
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// REST(client → API → service):受限專案。非成員在每個路由都看不到(列表沒有、單筆 404 且訊息與不存在相同),
// 成員管理的路由權限(組織管理員、專案管理員、一般成員、外部組織),非受限專案照舊
func TestRestrictedProjectRoutes(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	for _, o := range []string{"lab", "other"} {
		if _, err := boss.CreateOrg(o, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []string{"alice@example.com", "bob@example.com"} {
		if _, err := boss.SaveMember("lab", m, "member"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := boss.SaveMember("other", "random@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	alice, bob, carol := e.login("evo-alice"), e.login("evo-bob"), e.login2(boss, "evo-random")
	rboss := backend.NewRemote(boss)
	p, err := rboss.CreateProject("lab", service.ProjectInput{Name: "機密", Key: "SEC", Restricted: true})
	if err != nil || !p.Restricted || p.MyRole != "admin" {
		t.Fatal(p, err)
	}
	if _, err := rboss.CreateProject("lab", service.ProjectInput{Name: "公開", Key: "APP"}); err != nil {
		t.Fatal(err)
	}
	// 成員管理路由
	m, err := boss.SetProjectMember("lab", "prj-1", service.ProjectMemberInput{Actor: "bob@example.com", Role: "member"})
	if err != nil || m.Node != "user:bob@example.com" || m.Role != "member" || m.Actor == nil {
		t.Fatal(m, err)
	}
	if _, err := bob.SetProjectMember("lab", "PRJ-1", service.ProjectMemberInput{Actor: "alice@example.com"}); status(err) != 403 {
		t.Fatal("一般成員不能管成員:403", err)
	}
	if _, err := alice.SetProjectMember("lab", "PRJ-1", service.ProjectMemberInput{Actor: "alice@example.com"}); status(err) != 404 {
		t.Fatal("非成員:專案看不到 404", err)
	}
	if _, err := carol.SetProjectMember("lab", "PRJ-1", service.ProjectMemberInput{Actor: "random@example.com"}); status(err) != 403 {
		t.Fatal("外部組織:403", err)
	}
	if _, err := boss.SetProjectMember("lab", "PRJ-1", service.ProjectMemberInput{Actor: "random@example.com"}); status(err) != 400 {
		t.Fatal("外部組織的人不能加:400", err)
	}
	if l, err := bob.ListProjectMembers("lab", "PRJ-1"); err != nil || len(l) != 1 {
		t.Fatal(l, err)
	}
	if _, err := alice.ListProjectMembers("lab", "PRJ-1"); status(err) != 404 {
		t.Fatal(err)
	}
	// 專案的資料
	sec, err := boss.CreateIssue("lab", service.IssueInput{Title: "機密標題", Body: "機密內文", Project: &p.ID})
	if err != nil || sec.Key != "SEC-1" {
		t.Fatal(sec, err)
	}
	doc, err := boss.CreateDoc("lab", service.DocInput{Project: p.ID, Title: "機密文件", Section: "docs", Body: "秘密"})
	if err != nil {
		t.Fatal(err)
	}
	pub := "PRJ-2"
	if _, err := boss.CreateIssue("lab", service.IssueInput{Title: "公開標題", Project: &pub}); err != nil {
		t.Fatal(err)
	}

	gone := func(what string, err error, ref string, refs ...string) {
		t.Helper()
		if status(err) != 404 {
			t.Fatalf("%s:要 404,實際 %v", what, err)
		}
		for _, r := range refs {
			if strings.Contains(err.Error(), r) {
				t.Fatalf("%s:訊息帶出不該帶的 %s:%v", what, r, err)
			}
		}
	}
	// 非成員 alice:每個路由
	if l, err := alice.ListIssues("lab"); err != nil || len(l) != 1 || l[0].Title != "公開標題" {
		t.Fatal("GET issues", l, err)
	}
	if l, err := alice.QueryIssues("lab", model.IssueQuery{Q: "機密"}); err != nil || len(l) != 0 {
		t.Fatal("GET issues?q", l, err)
	}
	if l, err := alice.QueryIssues("lab", model.IssueQuery{Project: "PRJ-1"}); err != nil || len(l) != 0 {
		t.Fatal("GET issues?project", l, err)
	}
	if b, err := alice.IssueBoard("lab", model.IssueQuery{}); err != nil || b.Total != 1 {
		t.Fatal("GET issues/board 的 total 不含隱藏的", b, err)
	}
	for _, ref := range []string{"SEC-1", "sec-1", sec.ID} {
		_, err = alice.GetIssue("lab", ref)
		gone("GET issues/"+ref, err, ref)
	}
	_, err = alice.UpdateIssue("lab", "SEC-1", service.IssuePatch{Title: &p.ID})
	gone("POST issues/{id}", err, "SEC-1", sec.ID)
	_, err = alice.AddIssueComment("lab", "SEC-1", service.CommentInput{Text: "偷留言"})
	gone("POST issues/{id}/comments", err, "SEC-1", sec.ID)
	gone("POST issues/{id}/delete", alice.DeleteIssue("lab", "SEC-1"), "SEC-1", sec.ID)
	_, err = alice.CreateIssue("lab", service.IssueInput{Title: "塞", Project: &p.ID})
	gone("POST issues(project)", err, "PRJ-1")
	if l, err := alice.ListProjects("lab"); err != nil || len(l) != 1 || l[0].ID != "PRJ-2" {
		t.Fatal("GET projects", l, err)
	}
	_, err = alice.GetProject("lab", "PRJ-1")
	gone("GET projects/{id}", err, "PRJ-1")
	_, err = alice.GetProjectCabinet("lab", "PRJ-1")
	gone("GET cabinet?project", err, "PRJ-1")
	if l, err := alice.ListDocs("lab", ""); err != nil || len(l) != 0 {
		t.Fatal("GET docs 未分專案", l, err)
	}
	_, err = alice.ListDocs("lab", "PRJ-1")
	gone("GET docs?project", err, "PRJ-1")
	_, err = alice.GetDoc("lab", doc.ID)
	gone("GET docs/{id}", err, doc.ID)
	_, err = alice.ReadDoc("lab", doc.ID, service.DocRead{})
	gone("POST docs/{id}/read", err, doc.ID)
	_, err = alice.UpdateDoc("lab", doc.ID, service.DocPatch{Title: &p.ID})
	gone("POST docs/{id}", err, doc.ID)
	gone("POST docs/{id}/delete", alice.DeleteDoc("lab", doc.ID), doc.ID)
	_, err = alice.CreateDoc("lab", service.DocInput{Project: p.ID, Title: "塞", Section: "docs"})
	gone("POST docs(project)", err, "PRJ-1")
	if l, err := alice.ListProjectRepos("lab", ""); err != nil || len(l) != 0 {
		t.Fatal(l, err)
	}
	_, err = alice.ListProjectRepos("lab", "PRJ-1")
	gone("GET projects/{id}/repos", err, "PRJ-1")

	// 成員 bob:都看得到
	if l, err := bob.ListIssues("lab"); err != nil || len(l) != 2 {
		t.Fatal(l, err)
	}
	if i, err := bob.GetIssue("lab", "SEC-1"); err != nil || i.Title != "機密標題" {
		t.Fatal(i, err)
	}
	if d, err := bob.GetDoc("lab", doc.ID); err != nil || d.Title != "機密文件" {
		t.Fatal(d, err)
	}
	// 外部組織:整個組織 403(不是 404:看不到組織本身,和受限專案無關)
	if _, err := carol.ListIssues("lab"); status(err) != 403 {
		t.Fatal(err)
	}
	// 移除成員後立刻看不到
	if err := boss.RemoveProjectMember("lab", "PRJ-1", "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	_, err = bob.GetIssue("lab", "SEC-1")
	gone("移除成員後 GET issues/{id}", err, "SEC-1")
	// 改成公開
	pub2 := false
	if _, err := rboss.UpdateProject("lab", "PRJ-1", service.ProjectPatch{Restricted: &pub2}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.GetIssue("lab", "SEC-1"); err != nil {
		t.Fatal("改成公開後看得到", err)
	}
}
