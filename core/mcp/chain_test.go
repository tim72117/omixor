package mcp_test

import (
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 組織鏈工具:管理端點 ops_chain / ops_chain_save、角色端點 ops_agent_task_delegate 與 ops_agent_context 的上下游、
// 執行者端點 dispatch_delegate 與 dispatch_claim 的 nextSteps
func TestChainTools(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.SaveUser(model.User{ID: "u2", Email: "amy@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u2", Email: "amy@x", Role: "member", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	amy := &model.Principal{User: model.User{ID: "u2", Email: "amy@x", Role: "member"}}
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長"}, {ID: "pm", Name: "產品經理"}, {ID: "designer", Name: "設計師"}} {
		g.Active = true
		if _, err := svc.SaveAgent(boss, "lab", g); err != nil {
			t.Fatal(err)
		}
	}
	endpoint := func(p *model.Principal) http.Handler {
		return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: p}, "lab", nil
		}})
	}
	admin, member := endpoint(boss), endpoint(amy)

	fails(t, member, "", "ops_chain_save", map[string]any{"edges": []any{}}, "要組織 lab 的管理員才能做")
	fails(t, admin, "", "ops_chain_save", map[string]any{"edges": []any{map[string]any{"from": "agent:cto", "to": "agent:cto"}}}, "第 1 條邊 研發長 → 研發長 指向自己")
	ok(t, admin, "", "ops_chain_save", map[string]any{"edges": []any{
		map[string]any{"from": "user:boss@x", "to": "agent:cto", "label": "目標"},
		map[string]any{"from": "agent:cto", "to": "agent:pm"},
	}})
	c := ok(t, admin, "", "ops_chain_save", map[string]any{"add": []any{map[string]any{"from": "agent:cto", "to": "agent:designer", "label": "設計"}},
		"remove": []any{map[string]any{"from": "agent:cto", "to": "agent:pm"}}})
	if es := c["edges"].([]any); len(es) != 2 || es[1].(map[string]any)["toName"] != "設計師" {
		t.Fatal(c)
	}
	fails(t, admin, "", "ops_chain_save", map[string]any{"add": []any{map[string]any{"from": "agent:designer", "to": "user:boss@x"}}}, "組織鏈不能有循環:boss@x → 研發長 → 設計師 → boss@x")
	ok(t, admin, "", "ops_chain_save", map[string]any{"add": []any{map[string]any{"from": "agent:cto", "to": "agent:pm"}}})
	if r := ok(t, member, "", "ops_chain", map[string]any{}); len(r["edges"].([]any)) != 3 || r["updatedAt"] == nil {
		t.Fatal(r)
	}

	// 角色端點:委派、context 的上下游
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "cto", Title: "規劃", Links: []string{"ISS-1"}})
	res, err := svc.ClaimTask(amy, "lab", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	auth := "Bearer cto:" + res.Credential.Secret
	ctx := ok(t, role, auth, "ops_agent_context", map[string]any{})
	ch := ctx["chain"].(map[string]any)
	if up := ch["upstream"].([]any); len(up) != 1 || up[0].(map[string]any)["name"] != "boss@x" || up[0].(map[string]any)["label"] != "目標" {
		t.Fatal(ch)
	}
	if down := ch["downstream"].([]any); len(down) != 2 {
		t.Fatal(ch)
	}
	d := ok(t, role, auth, "ops_agent_task_delegate", map[string]any{"to": "設計師", "title": "畫首頁", "instructions": "簡潔", "task": "T-999"})
	if d["agentId"] != "designer" || d["createdBy"] != "研發長" || d["instructions"] != "【設計】\n\n簡潔" || len(d["links"].([]any)) != 2 {
		t.Fatal(d)
	}
	fails(t, role, auth, "ops_agent_task_delegate", map[string]any{"to": "pm", "title": "x", "agent": "designer"}, "角色端點只能以自己的身分操作,不能指定其他角色")

	// 執行者端點
	worker := mcp.WorkerEndpoint(mcp.WorkerEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: svc, P: amy}, "lab", nil
	}})
	t2, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "cto", Title: "第二件"})
	cl := ok(t, worker, "", "dispatch_claim", map[string]any{"id": t2.ID})
	if ns := cl["nextSteps"].(string); !strings.Contains(ns, "dispatch_delegate") || !strings.Contains(ns, "設計師(designer,設計)") {
		t.Fatal(ns)
	}
	code := cl["credential"].(map[string]any)["code"].(string)
	if _, e := call(t, worker, "", "dispatch_delegate", map[string]any{"id": t2.ID, "to": "pm", "title": "x"}); !strings.Contains(e, "code") {
		t.Fatal("要 code", e)
	}
	w := ok(t, worker, "", "dispatch_delegate", map[string]any{"id": t2.ID, "code": code, "to": "pm", "title": "寫規格"})
	if w["task"].(map[string]any)["agentId"] != "pm" {
		t.Fatal(w)
	}
	fails(t, worker, "", "dispatch_delegate", map[string]any{"id": t2.ID, "code": code, "to": "cto", "title": "x"}, "不能委派給自己(研發長)")
}
