package model_test

import (
	"net/url"
	"reflect"
	"testing"
	"time"

	"omixor-core/model"
)

func qt(min int) model.Time {
	return model.Time{Time: time.Date(2026, 11, 1, 0, min, 0, 0, time.UTC)}
}

func ids(l []model.Issue) []string {
	out := []string{}
	for _, i := range l {
		out = append(out, i.ID)
	}
	return out
}

func qIssue(id, status, prio string, upd int, mod ...func(*model.Issue)) model.Issue {
	i := model.Issue{ID: id, Title: id, Status: status, Priority: prio, Labels: []string{}, Comments: []model.Comment{}, UpdatedAt: qt(upd)}
	for _, m := range mod {
		m(&i)
	}
	return i
}

func TestIssueSearch(t *testing.T) {
	a := qIssue("ISS-1", "open", "normal", 1, func(i *model.Issue) {
		i.Title, i.Body, i.Labels = "登入壞了", "Safari 白畫面", []string{"Bug", "前端"}
		i.Comments = []model.Comment{{Text: "iOS 也會 Crash"}}
		i.Repo = "github:acme/web"
	})
	b := qIssue("ISS-12", "closed", "low", 2, func(i *model.Issue) { i.Title = "改版首頁" })
	list := []model.Issue{a, b}
	cases := []struct {
		q    string
		want []string
	}{
		{"", []string{"ISS-1", "ISS-12"}},
		{"   ", []string{"ISS-1", "ISS-12"}},
		{"登入", []string{"ISS-1"}},              // 標題
		{"safari", []string{"ISS-1"}},          // 內文,不分大小寫
		{"CRASH", []string{"ISS-1"}},           // 留言
		{"bug", []string{"ISS-1"}},             // 標籤
		{"iss-12", []string{"ISS-12"}},         // 編號
		{"iss-1", []string{"ISS-1", "ISS-12"}}, // 編號是部分比對
		{"safari bug", []string{"ISS-1"}},      // 多字 AND,可以跨欄位
		{"safari 改版", []string{}},              // 每個字都要有
		{"白畫面\tcrash\n前端", []string{"ISS-1"}},  // 任何空白都算分隔
		{"acme", []string{}},                   // 倉庫不在搜尋範圍(用 repo 篩選)
		{"壞了 safari", []string{"ISS-1"}},       // 順序無關
		{"了 S", []string{"ISS-1"}},             // 子字串
		{"登入壞了safari", []string{}},             // 不會把不同欄位接起來比對
	}
	for _, c := range cases {
		got := ids(model.FilterIssues(list, model.IssueQuery{Q: c.q}))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("搜尋 %q = %v,要 %v", c.q, got, c.want)
		}
	}
}

func TestFilterIssues(t *testing.T) {
	list := []model.Issue{
		qIssue("ISS-1", "open", "urgent", 1, func(i *model.Issue) { i.Labels = []string{"Bug"}; i.Repo = "acme/web" }),
		qIssue("ISS-2", "open", "low", 2, func(i *model.Issue) { i.Assignee = "eng"; i.Repo = "gitlab:grp/api" }),
		qIssue("ISS-3", "closed", "urgent", 3, func(i *model.Issue) { i.Labels = []string{"bug", "ui"}; i.Assignee = "Eng" }),
	}
	cases := []struct {
		name string
		q    model.IssueQuery
		want []string
	}{
		{"零值 = 全部、照原順序", model.IssueQuery{}, []string{"ISS-1", "ISS-2", "ISS-3"}},
		{"status open", model.IssueQuery{Status: "open"}, []string{"ISS-1", "ISS-2"}},
		{"status closed", model.IssueQuery{Status: "closed"}, []string{"ISS-3"}},
		{"status all", model.IssueQuery{Status: "all"}, []string{"ISS-1", "ISS-2", "ISS-3"}},
		{"priority", model.IssueQuery{Priority: "urgent"}, []string{"ISS-1", "ISS-3"}},
		{"label 不分大小寫、完全相同", model.IssueQuery{Label: "BUG"}, []string{"ISS-1", "ISS-3"}},
		{"label 不是部分比對", model.IssueQuery{Label: "bu"}, []string{}},
		{"repo 前綴一邊沒寫", model.IssueQuery{Repo: "github:ACME/web"}, []string{"ISS-1"}},
		{"repo gitlab", model.IssueQuery{Repo: "grp/api"}, []string{"ISS-2"}},
		{"repo 未標記", model.IssueQuery{Repo: model.IssueFilterNone}, []string{"ISS-3"}},
		{"assignee 不分大小寫", model.IssueQuery{Assignee: "ENG"}, []string{"ISS-2", "ISS-3"}},
		{"assignee 未指派", model.IssueQuery{Assignee: model.IssueFilterNone}, []string{"ISS-1"}},
		{"條件 AND", model.IssueQuery{Status: "open", Label: "bug", Priority: "urgent"}, []string{"ISS-1"}},
	}
	for _, c := range cases {
		if got := ids(model.FilterIssues(list, c.q)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:%v,要 %v", c.name, got, c.want)
		}
	}
	if model.FilterIssues(nil, model.IssueQuery{}) == nil {
		t.Error("沒有結果時要回空切片,不是 nil(JSON 是 [])")
	}
}

// 人類接受者:assignee 字串是空的,只有 assigneeActor;用 id、handle、email、name 篩得到,(none) 篩不到
func TestFilterIssuesHumanAssignee(t *testing.T) {
	human := &model.ActorView{ID: "act_h1", Kind: "user", Handle: "tim@example.com", Email: "tim@example.com", Name: "Tim"}
	list := []model.Issue{
		qIssue("ISS-1", "open", "normal", 1),
		qIssue("ISS-2", "open", "normal", 2, func(i *model.Issue) { i.Assignee = "eng" }),
		qIssue("ISS-3", "open", "normal", 3, func(i *model.Issue) { i.AssigneeActor = human }),
	}
	for _, a := range []string{"act_h1", " TIM@example.com ", "Tim"} {
		if got := ids(model.FilterIssues(list, model.IssueQuery{Assignee: a})); !reflect.DeepEqual(got, []string{"ISS-3"}) {
			t.Errorf("assignee %q = %v,要 [ISS-3]", a, got)
		}
	}
	if got := ids(model.FilterIssues(list, model.IssueQuery{Assignee: model.IssueFilterNone})); !reflect.DeepEqual(got, []string{"ISS-1"}) {
		t.Errorf("(none) = %v,要 [ISS-1]", got)
	}
	if got := ids(model.FilterIssues(list, model.IssueQuery{Assignee: "eng"})); !reflect.DeepEqual(got, []string{"ISS-2"}) {
		t.Errorf("角色指派的舊行為 = %v,要 [ISS-2]", got)
	}
}

func TestSortIssues(t *testing.T) {
	list := []model.Issue{
		qIssue("ISS-10", "open", "normal", 5),
		qIssue("ISS-2", "open", "normal", 5),
		qIssue("ISS-3", "closed", "urgent", 1),
		qIssue("ISS-4", "open", "high", 9),
		qIssue("ISS-5", "open", "high", 10),
		qIssue("X-1", "open", "low", 5),
		qIssue("ABC", "open", "low", 5),
		qIssue("ISS-6", "open", "weird", 7), // 不認得的優先度同 normal
	}
	cases := map[string][]string{
		"":                      {"ISS-10", "ISS-2", "ISS-3", "ISS-4", "ISS-5", "X-1", "ABC", "ISS-6"},
		model.IssueSortPriority: {"ISS-3", "ISS-5", "ISS-4", "ISS-6", "ISS-2", "ISS-10", "ABC", "X-1"},
		model.IssueSortStatus:   {"ISS-5", "ISS-4", "ISS-6", "ISS-2", "ISS-10", "ABC", "X-1", "ISS-3"},
		model.IssueSortUpdated:  {"ISS-5", "ISS-4", "ISS-6", "ISS-2", "ISS-10", "ABC", "X-1", "ISS-3"},
	}
	for by, want := range cases {
		before := ids(list)
		if got := ids(model.SortIssues(list, by)); !reflect.DeepEqual(got, want) {
			t.Errorf("sort %q = %v,要 %v", by, got, want)
		}
		if !reflect.DeepEqual(ids(list), before) {
			t.Fatal("SortIssues 不能改原本的切片")
		}
	}
	// QueryIssues = 篩選 + 排序
	got := ids(model.QueryIssues(list, model.IssueQuery{Status: "open", Priority: "high", Sort: "priority"}))
	if !reflect.DeepEqual(got, []string{"ISS-5", "ISS-4"}) {
		t.Errorf("QueryIssues = %v", got)
	}
}

func TestIssueQueryValidateAndValues(t *testing.T) {
	for _, q := range []model.IssueQuery{{}, {Status: "all"}, {Status: "open", Priority: "low", Sort: "status"}} {
		if e := q.Validate(); e != "" {
			t.Errorf("%+v 應該合格:%s", q, e)
		}
	}
	for _, q := range []model.IssueQuery{{Status: "x"}, {Priority: "p1"}, {Sort: "id"}} {
		if q.Validate() == "" {
			t.Errorf("%+v 應該不合格", q)
		}
	}
	q := model.IssueQuery{Q: "登入 bug", Status: "open", Priority: "high", Label: "前端", Repo: "github:a/b", Assignee: model.IssueFilterNone, Sort: "priority"}
	if back := model.ParseIssueQuery(q.Values()); !reflect.DeepEqual(back, q) {
		t.Errorf("Values ↔ ParseIssueQuery 不一致:%+v", back)
	}
	if v := (model.IssueQuery{}).Values(); len(v) != 0 {
		t.Errorf("零值不放參數:%v", v)
	}
	if got := model.ParseIssueQuery(url.Values{"q": {"a"}, "other": {"x"}}); !reflect.DeepEqual(got, model.IssueQuery{Q: "a"}) {
		t.Errorf("ParseIssueQuery = %+v", got)
	}
}

func TestUsedLabels(t *testing.T) {
	list := []model.Issue{
		qIssue("ISS-1", "open", "normal", 1, func(i *model.Issue) { i.Labels = []string{"ui", "Bug", " "} }),
		qIssue("ISS-2", "open", "normal", 1, func(i *model.Issue) { i.Labels = []string{"bug", "API", "前端"} }),
	}
	if got := model.UsedLabels(list); !reflect.DeepEqual(got, []string{"API", "Bug", "ui", "前端"}) {
		t.Errorf("UsedLabels = %v", got)
	}
	if got := model.UsedLabels(nil); got == nil || len(got) != 0 {
		t.Errorf("沒有標籤要回空切片:%v", got)
	}
}

func TestIssueBoard(t *testing.T) {
	todo := qIssue("ISS-1", "open", "low", 1)
	todo2 := qIssue("ISS-2", "open", "urgent", 1)
	doing := qIssue("ISS-3", "open", "normal", 1, func(i *model.Issue) { i.Assignee = "eng"; i.Labels = []string{"bug"} })
	done := qIssue("ISS-4", "closed", "high", 1, func(i *model.Issue) { i.Assignee = "eng" })
	done2 := qIssue("ISS-5", "closed", "high", 3)
	all := []model.Issue{todo, todo2, doing, done, done2}

	if model.IssueBoardColumn(todo) != model.BoardTodo || model.IssueBoardColumn(doing) != model.BoardDoing ||
		model.IssueBoardColumn(done) != model.BoardDone || model.IssueBoardColumn(done2) != model.BoardDone {
		t.Fatal("分欄不對")
	}
	cols := model.BoardColumns(all)
	type colSum struct {
		Key, Title, Drop string
		IDs              []string
	}
	got := []colSum{}
	for _, c := range cols {
		d := "<nil>"
		if c.DropStatus != nil {
			d = *c.DropStatus
		}
		got = append(got, colSum{c.Key, c.Title, d, ids(c.Issues)})
	}
	want := []colSum{
		{"todo", "待處理", "open", []string{"ISS-2", "ISS-1"}},
		{"doing", "進行中", "open", []string{"ISS-3"}},
		{"done", "已完成", "closed", []string{"ISS-5", "ISS-4"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BoardColumns = %+v", got)
	}
	empty := model.BoardColumns(nil)
	if len(empty) != 3 || empty[0].Issues == nil || len(empty[2].Issues) != 0 {
		t.Errorf("沒有 Issue 也要三欄、每欄空陣列:%+v", empty)
	}

	// 拖曳:拖到已完成 = 關閉;從已完成拖出 = 重新打開;待處理 ↔ 進行中、同一欄 不動作
	str := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	drops := []struct {
		i    model.Issue
		col  string
		want string
	}{
		{todo, model.BoardDone, "closed"},
		{doing, model.BoardDone, "closed"},
		{done, model.BoardTodo, "open"},
		{done, model.BoardDoing, "open"},
		{todo, model.BoardDoing, "<nil>"},
		{doing, model.BoardTodo, "<nil>"},
		{todo, model.BoardTodo, "<nil>"},
		{done, model.BoardDone, "<nil>"},
		{todo, "nope", "<nil>"},
	}
	for _, d := range drops {
		if g := str(model.BoardDrop(d.i, d.col)); g != d.want {
			t.Errorf("%s 拖到 %s = %s,要 %s", d.i.ID, d.col, g, d.want)
		}
	}

	// BuildIssueBoard:不看 Status 與 Sort;Labels 與 Total 是篩選前的
	b := model.BuildIssueBoard(all, model.IssueQuery{Priority: "high", Status: "open", Sort: "updated"})
	if b.Total != 5 || b.Matched != 2 || len(b.Columns[2].Issues) != 2 || len(b.Columns[0].Issues) != 0 {
		t.Errorf("BuildIssueBoard = %+v", b)
	}
	if !reflect.DeepEqual(b.Labels, []string{"bug"}) {
		t.Errorf("Labels = %v", b.Labels)
	}
}
