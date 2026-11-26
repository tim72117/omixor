package mcp_test

import (
	"strings"
	"testing"

	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// 角色端點被 Issue 權限擋下時回英文錯誤(ISS-7;依服務的機器碼換,不讀中文原文)
func TestAgentEndpointIssueLimits(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	for _, id := range []string{"solo", "peer"} {
		if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: id, Name: id, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	other, err := svc.CreateIssue(boss, "lab", service.IssueInput{Title: "別人的"})
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "solo", Title: "做事"})
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "solo", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	h, auth := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc}), "Bearer solo:"+res.Credential.Secret
	failsWith(t, h, auth, "issue_update", map[string]any{"id": other.Key, "priority": "urgent", "status": "closed"}, "Not allowed for an agent: priority, status.")
	failsWith(t, h, auth, "issue_create", map[string]any{"title": "x", "assignee": "peer"}, "Not allowed for an agent: the organization chain has no edge")
	ok(t, h, auth, "issue_update", map[string]any{"id": other.Key, "comment": "看到了"})
	failsWith(t, h, auth, "issue_accept", map[string]any{"id": other.Key}, "Failed: Not allowed for an agent: As an agent you can accept only")

	// 自己任務的子項回報 done:沒有回報過的 commit 要帶角色版的提醒(不叫角色跑 commits_report)
	it, err := svc.AddItem(boss, "lab", other.Key, service.ItemInput{Title: "做", Assignee: "agent:solo"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := svc.ClaimItem(boss, "lab", other.Key, it.Key, service.ItemClaimInput{})
	if err != nil {
		t.Fatal(err)
	}
	d := ok(t, h, "Bearer solo:"+claim.Credential.Secret, "issue_item_report", map[string]any{"id": it.Key, "status": "done", "evidence": map[string]any{"commit": "abcdef1"}})
	if msg, _ := d["message"].(string); !strings.Contains(msg, "Agents cannot report commits") || strings.Contains(msg, "commits_report") {
		t.Fatalf("角色版提醒:%v", d)
	}
}
