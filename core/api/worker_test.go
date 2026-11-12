package api_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

func sp(s string) *string { return &s }

// 執行者(領取者本人)不用角色憑證,以角色身分回報與留言
func TestWorkerReportAndComment(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"alice@example.com", "bob@example.com"} {
		if _, err := boss.SaveUser(m, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := boss.SaveMember("o1", m, "member"); err != nil {
			t.Fatal(err)
		}
	}
	alice, bob := e.login("evo-alice"), e.login("evo-bob")
	if _, err := boss.SaveAgent("o1", model.Agent{ID: "programmer", Name: "程式設計師", Active: true}); err != nil {
		t.Fatal(err)
	}
	iss, err := boss.CreateIssue("o1", service.IssueInput{Title: "要做的事"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := boss.AssignTask("o1", service.TaskInput{AgentID: "programmer", Title: "處理", Links: []string{iss.ID}})
	if err != nil {
		t.Fatal(err)
	}

	// 還沒領取:不能回報
	if _, err := alice.WorkerReport("o1", task.ID, service.TaskReport{Note: sp("x")}); status(err) != 409 {
		t.Fatal("沒領取不能回報", err)
	}
	if _, err := alice.ClaimTask("o1", service.ClaimInput{AgentID: "programmer", TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	// 不是領取者:不行
	if _, err := bob.WorkerComment("o1", task.ID, iss.ID, "我來留言"); status(err) != 403 {
		t.Fatal("非領取者不能留言", err)
	}
	if _, err := bob.WorkerReport("o1", task.ID, service.TaskReport{Status: sp("done")}); status(err) != 403 {
		t.Fatal("非領取者不能回報", err)
	}
	// 空白留言
	if _, err := alice.WorkerComment("o1", task.ID, iss.ID, "  "); status(err) != 400 {
		t.Fatal("空白留言", err)
	}
	// 領取者:留言署名是角色
	got, err := alice.WorkerComment("o1", task.ID, iss.ID, "做完了,見 PR")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got.Comments); n != 1 || got.Comments[0].Author != model.AgentPrefix+"programmer" || got.Comments[0].Text != "做完了,見 PR" {
		t.Fatalf("%+v", got.Comments)
	}
	// 進度回報(續約),備註署名角色
	tk, err := alice.WorkerReport("o1", task.ID, service.TaskReport{Status: sp("inProgress"), Note: sp("進行中")})
	if err != nil || tk.Status != "inProgress" || tk.LockedUntil == nil {
		t.Fatal(tk, err)
	}
	if last := tk.Notes[len(tk.Notes)-1]; last.Author != model.AgentPrefix+"programmer" {
		t.Fatalf("%+v", last)
	}
	// canceled 不開放(同角色憑證)
	if _, err := alice.WorkerReport("o1", task.ID, service.TaskReport{Status: sp("canceled")}); status(err) != 400 {
		t.Fatal("角色不能取消", err)
	}
	// done:結案;之後不能再回報或留言
	tk, err = alice.WorkerReport("o1", task.ID, service.TaskReport{Status: sp("done"), Result: sp("完成")})
	if err != nil || tk.Status != "done" || tk.Result != "完成" {
		t.Fatal(tk, err)
	}
	if _, err := alice.WorkerComment("o1", task.ID, iss.ID, "再留一則"); status(err) != 409 || !strings.Contains(err.Error(), "不是進行中") {
		t.Fatal("結案後不能留言", err)
	}
}
