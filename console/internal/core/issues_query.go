package core

import "omixor-core/model"

// Issue 分頁的列表與看板:篩選、排序、搜尋、分欄規則只在核心(omixor-core/model 的 issue_query.go),
// 這裡拿快取的 a.Issues()(不連網)套核心的函式,畫面只顯示結果。
//
//	POST /api/action/issue_query  {q?, status?, priority?, label?, repo?, assignee?, project?, sort?}
//	     → {issues, total, counts:{open, closed, all}}(counts、total 是全部 Issue 的,不受篩選影響)
//	POST /api/action/issue_board  {q?, priority?, label?, repo?, assignee?, project?}
//	     → 同核心 GET /api/orgs/{org}/issues/board:{columns:[{key,title,dropStatus,issues}], labels, total, matched}
//
// project 參數:"" = 全部、"-" = 只看組織層級(沒放專案)、PRJ-n = 那個專案;counts、total 以這個範圍計算。
// 參數意義同核心 GET /api/orgs/{org}/issues 的 query 參數(repo、assignee 給 "(none)" = 沒標記/沒指派的)。
// 畫面在收到 "issues" 通知時重新呼叫。

// IssueQueryResult issue_query 的回應
type IssueQueryResult struct {
	Issues []model.Issue    `json:"issues"`
	Total  int              `json:"total"`
	Counts map[string]int   `json:"counts"`
	Query  model.IssueQuery `json:"query"`
}

func issueQueryArgs(args map[string]any) model.IssueQuery {
	s := func(k string) string { v, _ := args[k].(string); return v }
	return model.IssueQuery{Q: s("q"), Status: s("status"), Priority: s("priority"), Label: s("label"),
		Repo: s("repo"), Assignee: s("assignee"), Project: s("project"), Sort: s("sort")}
}

// QueryIssues 目前組織快取的 Issue 依條件篩選、排序(呼叫時要拿著 Mu)
func (a *App) QueryIssues(q model.IssueQuery, hidden ...*Hidden) (IssueQueryResult, error) {
	if e := q.Validate(); e != "" {
		return IssueQueryResult{}, UserError{e}
	}
	all := visibleIssues(a, hidden)
	if q.Project != "" { // counts、total 以專案範圍為準(其他條件不影響)
		all = model.FilterIssues(all, model.IssueQuery{Project: q.Project})
	}
	counts := map[string]int{"all": len(all)}
	for _, st := range model.IssueStatuses {
		counts[st] = len(model.FilterIssues(all, model.IssueQuery{Status: st}))
	}
	return IssueQueryResult{Issues: model.QueryIssues(all, q), Total: len(all), Counts: counts, Query: q}, nil
}

// IssueBoard 目前組織快取的 Issue 分成看板三欄(呼叫時要拿著 Mu)
func (a *App) IssueBoard(q model.IssueQuery, hidden ...*Hidden) (model.IssueBoard, error) {
	q.Status, q.Sort = "", ""
	if e := q.Validate(); e != "" {
		return model.IssueBoard{}, UserError{e}
	}
	return model.BuildIssueBoard(visibleIssues(a, hidden), q), nil
}

// visibleIssues 快取的 Issue 拿掉呼叫者看不到的(要在算 total、counts、labels 之前)
func visibleIssues(a *App, hidden []*Hidden) []model.Issue {
	if len(hidden) == 0 {
		return a.Issues()
	}
	return hidden[0].Issues(a.Issues())
}

func init() {
	RegisterAction("issue_query", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return a.QueryIssues(issueQueryArgs(args), HiddenOf(args))
	})
	RegisterAction("issue_board", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return a.IssueBoard(issueQueryArgs(args), HiddenOf(args))
	})
}
