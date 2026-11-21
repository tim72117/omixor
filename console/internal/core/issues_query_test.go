package core

import (
	"encoding/json"
	"strings"
	"testing"

	"omixor-core/model"
)

// issue_query / issue_board:用核心 model 的規則處理快取的 Issue(規則本身的測試在 core/model/issue_query_test.go)
func TestIssueQueryAndBoardActions(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	a.CreateIssueFor("登入壞了", "Safari 白畫面", "low", []string{"Bug"}, "", "", "mcp", "", "github:acme/web")
	b := a.CreateIssueFor("首頁改版", "", "urgent", []string{"ui"}, "", "", "mcp", "", "")
	a.CreateIssueFor("API 逾時", "", "high", nil, "", "", "mcp", "", "")
	a.UpdateIssue("ISS-3", "mcp", func(i *model.Issue) { i.Status = "closed" })
	a.CommentIssue(b.ID, "手機版也要", "mcp")
	a.Mu.Unlock()

	ids := func(l []model.Issue) string {
		out := []string{}
		for _, i := range l {
			out = append(out, i.Key)
		}
		return strings.Join(out, ",")
	}
	run := func(name string, args map[string]any) []byte {
		t.Helper()
		out, err := Action(a, name, args)
		if err != nil {
			t.Fatalf("%s %v:%v", name, args, err)
		}
		j, _ := json.Marshal(out)
		return j
	}

	var r IssueQueryResult
	_ = json.Unmarshal(run("issue_query", map[string]any{"status": "open", "sort": "priority"}), &r)
	if ids(r.Issues) != "ISS-2,ISS-1" || r.Total != 3 || r.Counts["open"] != 2 || r.Counts["closed"] != 1 || r.Counts["all"] != 3 {
		t.Fatalf("%+v", r)
	}
	_ = json.Unmarshal(run("issue_query", map[string]any{"q": "手機", "sort": "status"}), &r)
	if ids(r.Issues) != "ISS-2" {
		t.Fatal("搜尋要含留言:", ids(r.Issues))
	}
	_ = json.Unmarshal(run("issue_query", map[string]any{"repo": "(none)", "sort": "status"}), &r)
	if ids(r.Issues) != "ISS-2,ISS-3" {
		t.Fatal(ids(r.Issues))
	}
	_ = json.Unmarshal(run("issue_query", map[string]any{"q": "沒有"}), &r)
	if r.Issues == nil || len(r.Issues) != 0 {
		t.Fatal("沒有符合的要是 []")
	}
	if _, err := Action(a, "issue_query", map[string]any{"sort": "x"}); err == nil {
		t.Fatal("不認得的 sort 要錯誤")
	}

	var bd model.IssueBoard
	raw := run("issue_board", map[string]any{"label": "bug"})
	_ = json.Unmarshal(raw, &bd)
	if bd.Total != 3 || bd.Matched != 1 || len(bd.Columns) != 3 || ids(bd.Columns[0].Issues) != "ISS-1" || strings.Join(bd.Labels, ",") != "Bug,ui" {
		t.Fatalf("%s", raw)
	}
	if !strings.Contains(string(raw), `"key":"done","title":"已完成","dropStatus":"closed","issues":[]`) {
		t.Fatalf("%s", raw)
	}
}
