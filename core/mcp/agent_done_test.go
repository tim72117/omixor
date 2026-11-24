package mcp_test

import (
	"testing"

	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// Done 在每次角色端點的工具呼叫處理完後回報:哪個角色、綁定的任務、哪個工具、成功或失敗(被拒絕的也算)
func TestAgentEndpointDoneHook(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: "solo", Name: "獨行", Active: true}); err != nil {
		t.Fatal(err)
	}
	type rec struct {
		role mcp.Role
		tool string
		err  error
	}
	var got []rec
	h := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc, Done: func(r mcp.Role, tool string, err error) { got = append(got, rec{r, tool, err}) }})
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "solo", Title: "做事"})
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "solo", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	auth := "Bearer solo:" + res.Credential.Secret
	ok(t, h, auth, "agent_context", map[string]any{})
	// 回報別人的任務:被 Guard 拒絕,Done 仍收到錯誤
	call(t, h, auth, "agent_task_report", map[string]any{"id": "T-999", "status": "done"})
	if len(got) != 2 || got[0].tool != "agent_context" || got[0].err != nil || got[0].role.TaskID != tk.ID || got[0].role.AgentID != "solo" || got[1].tool != "agent_task_report" || got[1].err == nil {
		t.Fatalf("Done 紀錄不對:%+v", got)
	}
}
