package api_test

import (
	"strings"
	"testing"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// 組織鏈的 API:讀寫、成員依鏈派工、角色憑證委派、執行者委派、回報給我的任務
func TestChainAPI(t *testing.T) {
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
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長"}, {ID: "designer", Name: "設計師"}} {
		g.Active = true
		if _, err := boss.SaveAgent("o1", g); err != nil {
			t.Fatal(err)
		}
	}
	// 驗證錯誤 400(原文)、成員不能改 403
	if _, err := boss.SaveChain("o1", []model.ChainEdge{{From: "agent:cto", To: "agent:designer"}, {From: "agent:designer", To: "agent:cto"}}); status(err) != 400 || !strings.Contains(err.Error(), "循環") {
		t.Fatal(err)
	}
	if _, err := alice.SaveChain("o1", nil); status(err) != 403 {
		t.Fatal(err)
	}
	c, err := boss.SaveChain("o1", []model.ChainEdge{{From: "user:Alice@example.com", To: "agent:cto", Label: "目標"}, {From: "agent:cto", To: "agent:designer"}})
	if err != nil || len(c.Edges) != 2 || c.Edges[0].From != "user:alice@example.com" {
		t.Fatal(c, err)
	}
	if got, err := bob.GetChain("o1"); err != nil || len(got.Edges) != 2 {
		t.Fatal(got, err)
	}
	// 成員依鏈派工
	goal, err := alice.AssignTask("o1", service.TaskInput{AgentID: "cto", Title: "Q4", Instructions: "成長"})
	if err != nil || !strings.HasPrefix(goal.Instructions, "【目標】") {
		t.Fatal(goal, err)
	}
	if _, err := bob.AssignTask("o1", service.TaskInput{AgentID: "cto", Title: "x"}); status(err) != 403 {
		t.Fatal(err)
	}
	// 角色憑證委派(Bearer <agentId>:<secret>)
	cl, err := bob.ClaimTask("o1", service.ClaimInput{AgentID: "cto", TaskID: goal.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := client.New(e.srv.URL)
	cred := "cto:" + cl.Credential.Secret
	role.Token = func() string { return cred }
	if got, err := role.GetChain("o1"); err != nil || len(got.Edges) != 2 {
		t.Fatal("角色憑證也可以讀組織鏈", got, err)
	}
	d, err := role.DelegateTask("o1", service.DelegateInput{To: "designer", Title: "畫圖"})
	if err != nil || d.CreatedBy != "agent:cto" || d.Links[0] != goal.ID {
		t.Fatal(d, err)
	}
	if _, err := bob.DelegateTask("o1", service.DelegateInput{To: "designer", Title: "x"}); status(err) != 403 {
		t.Fatal("人用 token 不能委派", err)
	}
	bad := client.New(e.srv.URL)
	bad.Token = func() string { return "cto:wrong" }
	if _, err := bad.DelegateTask("o1", service.DelegateInput{To: "designer", Title: "x"}); status(err) != 401 {
		t.Fatal("憑證錯誤 401", err)
	}
	// 執行者委派(要 code)
	if _, err := bob.WorkerDelegate("o1", goal.ID, "", service.DelegateInput{To: "designer", Title: "y"}); status(err) != 400 {
		t.Fatal(err)
	}
	if d2, err := bob.WorkerDelegate("o1", goal.ID, cl.Credential.Code, service.DelegateInput{To: "designer", Title: "y"}); err != nil || d2.CreatedBy != "agent:cto" {
		t.Fatal(d2, err)
	}
	// 研發長回報 done → alice 的「回報給我的任務」
	done := "done"
	if _, err := bob.WorkerReport("o1", goal.ID, cl.Credential.Code, service.TaskReport{Status: &done, Result: sp("完成")}); err != nil {
		t.Fatal(err)
	}
	mine, err := alice.ListAgentTasks("o1", service.TaskFilter{ReportTo: "me"})
	if err != nil || len(mine) != 1 || mine[0].ID != goal.ID {
		t.Fatal(mine, err)
	}
}
