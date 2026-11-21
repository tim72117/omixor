package service_test

import (
	"slices"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

func repoKeys(l []model.Issue) []string {
	out := []string{}
	for _, i := range l {
		out = append(out, i.Key)
	}
	slices.Sort(out)
	return out
}

func prKeys(is ...*model.Issue) []string {
	out := []string{}
	for _, i := range is {
		out = append(out, i.Key)
	}
	slices.Sort(out)
	return out
}

// 連結、取消連結:組織管理員才能做(成員 403、外人 403);成員看得到 Project.Repos;格式、空值 400;專案不存在 404;
// 不分大小寫、前綴可省、重複連結成功不重複;取消沒連過的 404
func TestProjectRepoLinkAuthzAndNormalize(t *testing.T) {
	svc, root, boss, alice, _ := issueEnv(t, storetest.New(t))
	app := mkProject(t, svc, boss, "App", "APP")
	out := &model.Principal{User: model.User{ID: "u9", Email: "out@x", Role: "member"}}

	if _, err := svc.LinkProjectRepo(alice, "o1", app.ID, "acme/app"); code(err) != 403 {
		t.Fatal("一般成員不能連結", err)
	}
	if _, err := svc.LinkProjectRepo(out, "o1", app.ID, "acme/app"); code(err) == 0 {
		t.Fatal("外人不能連結", err)
	}
	if _, err := svc.LinkProjectRepo(boss, "o1", app.ID, "  "); code(err) != 400 {
		t.Fatal("repo 不能空", err)
	}
	if _, err := svc.LinkProjectRepo(boss, "o1", app.ID, "bad repo!"); code(err) != 400 {
		t.Fatal("repo 格式", err)
	}
	if _, err := svc.LinkProjectRepo(boss, "o1", "PRJ-99", "acme/app"); code(err) != 404 {
		t.Fatal("專案不存在", err)
	}
	x, err := svc.LinkProjectRepo(boss, "o1", "prj-1", " Acme/App ")
	if err != nil || !slices.Equal(x.Repos, []string{"Acme/App"}) {
		t.Fatal(x, err)
	}
	for _, same := range []string{"acme/app", "github:ACME/APP"} {
		if x, err = svc.LinkProjectRepo(root, "o1", app.ID, same); err != nil || len(x.Repos) != 1 {
			t.Fatal("重複連結成功但不新增", same, x, err)
		}
	}
	if _, err := svc.LinkProjectRepo(boss, "o1", app.ID, "gitlab:g/sub/p"); err != nil {
		t.Fatal(err)
	}
	// 成員看得到
	g, err := svc.GetProject(alice, "o1", app.ID)
	if err != nil || !slices.Equal(g.Repos, []string{"Acme/App", "gitlab:g/sub/p"}) {
		t.Fatal(g, err)
	}
	l, err := svc.ListProjects(alice, "o1")
	if err != nil || len(l) != 1 || len(l[0].Repos) != 2 {
		t.Fatal(l, err)
	}
	links, err := svc.ListProjectRepos(alice, "o1", "")
	if err != nil || len(links) != 2 {
		t.Fatal(links, err)
	}
	if one, err := svc.ListProjectRepos(alice, "o1", "PRJ-1"); err != nil || len(one) != 2 {
		t.Fatal(one, err)
	}
	if _, err := svc.ListProjectRepos(alice, "o1", "PRJ-9"); code(err) != 404 {
		t.Fatal(err)
	}
	// 改專案名稱、建立專案的回傳也帶 repos
	u, err := svc.UpdateProject(boss, "o1", app.ID, service.ProjectPatch{Name: sp("App2")})
	if err != nil || len(u.Repos) != 2 {
		t.Fatal(u, err)
	}
	if n := mkProject(t, svc, boss, "新", ""); n.Repos == nil || len(n.Repos) != 0 {
		t.Fatal("新專案 repos 是空陣列", n.Repos)
	}
	// 取消
	if _, err := svc.UnlinkProjectRepo(alice, "o1", app.ID, "acme/app"); code(err) != 403 {
		t.Fatal("一般成員不能取消", err)
	}
	if x, err = svc.UnlinkProjectRepo(boss, "o1", app.ID, "GITHUB:acme/app"); err != nil || !slices.Equal(x.Repos, []string{"gitlab:g/sub/p"}) {
		t.Fatal(x, err)
	}
	if _, err := svc.UnlinkProjectRepo(boss, "o1", app.ID, "acme/app"); code(err) != 404 {
		t.Fatal("沒連過", err)
	}
	// 刪專案:連結跟著沒了
	if err := svc.DeleteProject(boss, "o1", app.ID); err != nil {
		t.Fatal(err)
	}
	if links, _ := svc.ListProjectRepos(alice, "o1", ""); len(links) != 0 {
		t.Fatal("刪專案連結跟著刪", links)
	}
}

// 建立 Issue 的預設專案:repo 只連到一個專案(有代號)→ 放進去;沒給 project 才預設,明確給了(含空字串)就照給的;
// 沒連、連多個、專案沒代號、沒填 repo 都是組織層級;既有 Issue 不會被搬動
func TestIssueCreateDefaultProjectFromRepo(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	app := mkProject(t, svc, boss, "App", "APP")
	web := mkProject(t, svc, boss, "Web", "WEB")
	bare := mkProject(t, svc, boss, "無代號", "")
	mk := func(repo string, project *string) *model.Issue {
		t.Helper()
		i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Repo: repo, Project: project})
		if err != nil {
			t.Fatal(repo, err)
		}
		return i
	}
	before := mk("acme/app", nil) // 還沒連結:組織層級
	if before.Project != "" {
		t.Fatal("沒連結不預設", before.Project)
	}
	for _, c := range []struct{ p, repo string }{{app.ID, "acme/app"}, {web.ID, "acme/web"}, {bare.ID, "acme/bare"}, {app.ID, "acme/shared"}, {web.ID, "acme/shared"}} {
		if _, err := svc.LinkProjectRepo(boss, "o1", c.p, c.repo); err != nil {
			t.Fatal(c, err)
		}
	}
	if got := mk("Acme/App", nil); got.Project != app.ID || got.Key != "APP-1" { // 不分大小寫
		t.Fatal("只連一個專案要預設", got.Project, got.Key)
	}
	if got := mk("github:acme/web", nil); got.Project != web.ID { // 前綴可省
		t.Fatal(got.Project)
	}
	if got := mk("acme/shared", nil); got.Project != "" || got.Key[:4] != "ISS-" {
		t.Fatal("連到多個專案不猜", got.Project)
	}
	if got := mk("acme/bare", nil); got.Project != "" {
		t.Fatal("專案沒代號不預設", got.Project)
	}
	if got := mk("", nil); got.Project != "" {
		t.Fatal("沒填 repo", got.Project)
	}
	if got := mk("other/none", nil); got.Project != "" {
		t.Fatal("沒連任何專案 = 組織共用", got.Project)
	}
	if got := mk("acme/app", sp("")); got.Project != "" || got.Key[:4] != "ISS-" { // 明確不分專案
		t.Fatal("空字串 = 不分專案", got.Project)
	}
	if got := mk("acme/app", sp(web.ID)); got.Project != web.ID { // 明確覆寫
		t.Fatal("明確指定專案", got.Project)
	}
	if _, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Repo: "acme/app", Project: sp("PRJ-99")}); code(err) != 404 {
		t.Fatal("明確指定不存在的專案照樣 404,不退回預設", err)
	}
	// 既有 Issue 不自動歸類
	if cur, err := svc.GetIssue(alice, "o1", before.ID); err != nil || cur.Project != "" {
		t.Fatal("既有 Issue 不搬", cur, err)
	}
	// 取消連結之後不再預設
	if _, err := svc.UnlinkProjectRepo(boss, "o1", app.ID, "acme/app"); err != nil {
		t.Fatal(err)
	}
	if got := mk("acme/app", nil); got.Project != "" {
		t.Fatal("取消連結後不預設", got.Project)
	}
}

// 依 repo 篩選考慮連結:Issue 自己的 repo 相符,或沒標 repo、所屬專案連到該 repo;標了別的 repo 的不算;看板同規則
func TestIssueQueryRepoWithProjectLinks(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	app := mkProject(t, svc, boss, "App", "APP")
	other := mkProject(t, svc, boss, "Other", "OTH")
	mk := func(title, repo string, project *string) *model.Issue {
		t.Helper()
		i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: title, Repo: repo, Project: project})
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	tagged := mk("標 repo、組織層級", "acme/app", sp(""))
	inProj := mk("專案內沒標 repo", "", sp(app.ID))
	inProjOther := mk("專案內標別的 repo", "acme/else", sp(app.ID))
	inOther := mk("別的專案沒標 repo", "", sp(other.ID))
	org := mk("組織層級沒標 repo", "", sp(""))
	q := func(repo string) []string {
		t.Helper()
		l, err := svc.QueryIssues(alice, "o1", model.IssueQuery{Repo: repo})
		if err != nil {
			t.Fatal(err)
		}
		return repoKeys(l)
	}
	if got := q("acme/app"); !slices.Equal(got, prKeys(tagged)) {
		t.Fatal("沒連結:只看 Issue 自己的 repo", got)
	}
	if _, err := svc.LinkProjectRepo(boss, "o1", app.ID, "github:acme/app"); err != nil {
		t.Fatal(err)
	}
	if got, want := q("ACME/app"), prKeys(tagged, inProj); !slices.Equal(got, want) {
		t.Fatal("連結後專案內沒標 repo 的也算", got, want)
	}
	if got := q("gitlab:acme/app"); !slices.Equal(got, prKeys(tagged)) { // 前綴兩邊都寫且不同:不算同一個 repo(沿用 RepoMatch)
		t.Fatal("前綴不同不相符", got)
	}
	if got := q("acme/else"); !slices.Equal(got, prKeys(inProjOther)) {
		t.Fatal("標了別的 repo 的不受連結影響", got)
	}
	if got := q(model.IssueFilterNone); !slices.Equal(got, prKeys(inProj, inOther, org)) {
		t.Fatal("(none) = 沒標 repo 的,不看連結", got)
	}
	// 同時依專案篩
	l, err := svc.QueryIssues(alice, "o1", model.IssueQuery{Repo: "acme/app", Project: model.IssueProjectNone})
	if err != nil || !slices.Equal(repoKeys(l), prKeys(tagged)) {
		t.Fatal(repoKeys(l), err)
	}
	// 看板
	b, err := svc.IssueBoard(alice, "o1", model.IssueQuery{Repo: "acme/app"})
	if err != nil || b.Matched != 2 || b.Total != 5 {
		t.Fatal(b, err)
	}
	// 取消連結後回到只看自己的 repo
	if _, err := svc.UnlinkProjectRepo(boss, "o1", app.ID, "acme/app"); err != nil {
		t.Fatal(err)
	}
	if got := q("acme/app"); !slices.Equal(got, prKeys(tagged)) {
		t.Fatal(got)
	}
}

// 專案代號的改法沿用第 1 階段:專案還沒有 Issue 才能改(連結不影響)
func TestProjectKeyLockUnaffectedByRepoLinks(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	app := mkProject(t, svc, boss, "App", "APP")
	if _, err := svc.LinkProjectRepo(boss, "o1", app.ID, "acme/app"); err != nil {
		t.Fatal(err)
	}
	if x, err := svc.UpdateProject(boss, "o1", app.ID, service.ProjectPatch{Key: sp("APX")}); err != nil || x.Key != "APX" || len(x.Repos) != 1 {
		t.Fatal("還沒有 Issue 可以改代號", x, err)
	}
	if _, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Repo: "acme/app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateProject(boss, "o1", app.ID, service.ProjectPatch{Key: sp("APY")}); code(err) != 409 {
		t.Fatal("已有 Issue 不能改代號", err)
	}
}
