package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

func issueIDs(l []model.Issue) string {
	out := []string{}
	for _, i := range l {
		out = append(out, i.ID)
	}
	return strings.Join(out, ",")
}

// GET …/issues 的 query 參數與 GET …/issues/board:規則在 model(issue_query.go),這裡確認 API、client、backend 接得起來
func TestIssueQueryAndBoardAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	mk := func(in service.IssueInput) *model.Issue {
		i, err := boss.CreateIssue("lab", in)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	mk(service.IssueInput{Title: "登入壞了", Body: "Safari 白畫面", Labels: []string{"Bug"}, Priority: "low", Repo: "github:acme/web"})
	mk(service.IssueInput{Title: "首頁改版", Priority: "urgent", Labels: []string{"ui"}})
	mk(service.IssueInput{Title: "API 逾時", Priority: "high", Repo: "acme/api"})
	closed := "closed"
	if _, err := boss.UpdateIssue("lab", "ISS-3", service.IssuePatch{Status: &closed}); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.AddIssueComment("lab", "ISS-2", service.CommentInput{Text: "手機版也要"}); err != nil {
		t.Fatal(err)
	}

	// 不帶參數:同舊的(全部、新的在前)
	all, err := boss.ListIssues("lab")
	if err != nil || issueIDs(all) != "ISS-3,ISS-2,ISS-1" {
		t.Fatalf("%s %v", issueIDs(all), err)
	}
	if l, _ := boss.QueryIssues("lab", model.IssueQuery{}); issueIDs(l) != issueIDs(all) {
		t.Fatal("零值的 QueryIssues 要和 ListIssues 一樣", issueIDs(l))
	}
	// 舊的 ?repo= 照舊
	if l, _ := boss.ListIssuesByRepo("lab", "ACME/web"); issueIDs(l) != "ISS-1" {
		t.Fatal(issueIDs(l))
	}
	cases := []struct {
		q    model.IssueQuery
		want string
	}{
		{model.IssueQuery{Q: "safari 登入"}, "ISS-1"},
		{model.IssueQuery{Q: "手機"}, "ISS-2"}, // 留言
		{model.IssueQuery{Q: "iss-3"}, "ISS-3"},
		{model.IssueQuery{Status: "open", Sort: "priority"}, "ISS-2,ISS-1"},
		{model.IssueQuery{Sort: "status"}, "ISS-2,ISS-1,ISS-3"},
		{model.IssueQuery{Sort: "priority"}, "ISS-2,ISS-3,ISS-1"},
		{model.IssueQuery{Label: "bug"}, "ISS-1"},
		{model.IssueQuery{Repo: model.IssueFilterNone}, "ISS-2"},
		{model.IssueQuery{Repo: "github:acme/api", Priority: "high"}, "ISS-3"},
		{model.IssueQuery{Assignee: model.IssueFilterNone, Status: "closed"}, "ISS-3"},
	}
	for _, c := range cases {
		l, err := boss.QueryIssues("lab", c.q)
		if err != nil || issueIDs(l) != c.want {
			t.Errorf("%+v = %s %v,要 %s", c.q, issueIDs(l), err, c.want)
		}
	}
	if l, err := boss.QueryIssues("lab", model.IssueQuery{Q: "沒有這個字"}); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("沒有符合的要回 [](不是 null):%v %v", l, err)
	}
	for _, bad := range []model.IssueQuery{{Status: "x"}, {Priority: "p0"}, {Sort: "id"}} {
		if _, err := boss.QueryIssues("lab", bad); status(err) != 400 {
			t.Errorf("%+v 要 400:%v", bad, err)
		}
	}

	// 看板
	b, err := boss.IssueBoard("lab", model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Columns) != 3 || b.Total != 3 || b.Matched != 3 || strings.Join(b.Labels, ",") != "Bug,ui" {
		t.Fatalf("%+v", b)
	}
	got := []string{}
	for _, c := range b.Columns {
		got = append(got, c.Key+":"+c.Title+":"+*c.DropStatus+":"+issueIDs(c.Issues))
	}
	if strings.Join(got, " ") != "todo:待處理:open:ISS-2,ISS-1 doing:進行中:open: done:已完成:closed:ISS-3" {
		t.Fatal(got)
	}
	b, _ = boss.IssueBoard("lab", model.IssueQuery{Label: "UI", Status: "closed"}) // 看板不看 status
	if b.Matched != 1 || issueIDs(b.Columns[0].Issues) != "ISS-2" || b.Columns[1].Issues == nil {
		t.Fatalf("%+v", b)
	}
	if _, err := boss.IssueBoard("lab", model.IssueQuery{Priority: "x"}); status(err) != 400 {
		t.Fatal(err)
	}
	// 原始 JSON 形狀(dropStatus、空欄是 [])
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/orgs/lab/issues/board?q=沒有", nil)
	req.Header.Set("Authorization", "Bearer "+boss.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if s := string(raw); !strings.Contains(s, `"key":"todo","title":"待處理","dropStatus":"open","issues":[]`) || !strings.Contains(s, `"labels":["Bug","ui"]`) {
		t.Fatal(s)
	}
	// 沒有權限的人
	if _, err := e.login2(boss, "evo-random").IssueBoard("lab", model.IssueQuery{}); status(err) != 403 {
		t.Fatal(err)
	}

	// backend.Remote 走同一組端點
	rem := backend.NewRemote(boss)
	if l, err := rem.QueryIssues("lab", model.IssueQuery{Q: "safari"}); err != nil || issueIDs(l) != "ISS-1" {
		t.Fatal(issueIDs(l), err)
	}
	if rb, err := rem.IssueBoard("lab", model.IssueQuery{}); err != nil || len(rb.Columns) != 3 || issueIDs(rb.Columns[2].Issues) != "ISS-3" {
		t.Fatal(rb, err)
	}
	if _, err := rem.QueryIssues("lab", model.IssueQuery{Sort: "x"}); backend.Code(err) != 400 {
		t.Fatal(err)
	}
}
