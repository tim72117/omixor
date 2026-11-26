package core

import (
	"context"
	"strings"
	"testing"

	"omixor-core/claude"
	"omixor-core/model"
	"omixor-core/service"
)

// runnerIssueRun 派一筆連到新 Issue 的任務讓 runner 跑(Claude 不自己回報,由 runner 代回報),回傳任務與 Issue 的留言
func runnerIssueRun(t *testing.T, during func(a *App, org string)) (AgentTask, []model.Comment, string) {
	t.Helper()
	f := &fakeRun{}
	a, org, logs := setupRunner(t, f)
	if during != nil {
		f.do = func(context.Context, string) (*claude.Result, error) {
			during(a, org)
			return &claude.Result{Subtype: "success", Text: "已完成:整理好了"}, nil
		}
	}
	saveCfg(t, org, "narrator", AgentRunConfig{AutoRun: true})
	issue := a.CreateIssueFor("要做的", "", "", nil, "", "", "mcp", "", "")
	tk, err := a.AgentOps().AssignTask(org, service.TaskInput{AgentID: "narrator", Title: "代回報", Author: MCPAuthor, Links: []string{issue.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !a.runnerTick(context.Background(), org) {
		t.Fatal("要跑")
	}
	l := localCore()
	got, err := l.Svc.GetIssue(l.P, org, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	return taskNow(t, a, org, tk.ID), got.Comments, logs()
}

// Claude 沒自己回報:runner 以角色身分代回報,完成留言署名角色
func TestRunnerReportsAsRole(t *testing.T) {
	task, comments, logs := runnerIssueRun(t, nil)
	if task.Status != "done" || len(comments) != 1 || comments[0].Author != "agent:narrator" || !strings.Contains(comments[0].Text, "整理好了") {
		t.Fatalf("%s %+v\n%s", task.Status, comments, logs)
	}
}

// 角色執行中被停用:以角色身分回報被拒(409),退回一般回報,結果不能掉(署名是本人)
func TestRunnerReportFallsBackWhenRoleDeactivated(t *testing.T) {
	task, comments, logs := runnerIssueRun(t, func(a *App, org string) {
		g := *a.FindAgentIn(org, "narrator")
		g.Active = false
		if _, err := a.AgentOps().SaveAgent(org, g); err != nil {
			t.Error(err)
		}
	})
	if task.Status != "done" || len(comments) != 1 || strings.HasPrefix(comments[0].Author, "agent:") || !strings.Contains(comments[0].Text, "整理好了") {
		t.Fatalf("%s %+v\n%s", task.Status, comments, logs)
	}
	if !strings.Contains(logs, "改用一般回報") {
		t.Fatalf("要記 log:%s", logs)
	}
}

// 沒有驗證碼(舊 server)或 worker 回報被拒:都退回一般回報
func TestReportForRoleFallback(t *testing.T) {
	a, org, _ := setupRunner(t, &fakeRun{})
	for _, code := range []string{"", "000000x"} {
		tk := assignTask(t, a, org, "narrator", "退回 "+code)
		if _, err := a.AgentOps().ClaimTask(org, service.ClaimInput{AgentID: "narrator", TaskID: tk.ID}); err != nil {
			t.Fatal(err)
		}
		done, result := "done", "結果"
		if err := reportForRole(a.AgentOps(), org, tk.ID, code, service.TaskReport{Status: &done, Result: &result}); err != nil {
			t.Fatal(code, err)
		}
		if got := taskNow(t, a, org, tk.ID); got.Status != "done" || got.Result != result {
			t.Fatalf("%q:%+v", code, got)
		}
	}
}
