package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 角色端點 tools/list 回傳的工具名稱
func listTools(t *testing.T, h http.Handler, auth string) []string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{}})
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", auth)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(w.Body.String())
	}
	out := []string{}
	for _, x := range resp.Result.Tools {
		out = append(out, x.Name)
	}
	return out
}

// 角色端點只列角色目前用得到的工具:文件權限、組織鏈下游、頻道成員決定;改了設定下一個請求就生效。
// agent_context 的 tools 列出能用與不能用(含原因);API 的 AgentTools 同一份。
func TestAgentToolsFollowRoleSettings(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	for _, g := range []model.Agent{{ID: "solo", Name: "獨行"}, {ID: "pm", Name: "產品經理"}} {
		g.Active = true
		if _, err := svc.SaveAgent(boss, "lab", g); err != nil {
			t.Fatal(err)
		}
	}
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "solo", Title: "做事"})
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "solo", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	auth := "Bearer solo:" + res.Credential.Secret

	// 什麼都沒設:只有任務與 Issue
	got := listTools(t, role, auth)
	want := []string{"agent_context", "agent_tasks", "agent_task_report", "issues", "issue_create", "issue_update"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatal(got)
	}
	// 舊名稱(ops_ 開頭)還能呼叫,只是不列在 tools/list
	if old := ok(t, role, auth, "ops_agent_context", map[string]any{}); old["agent"] == nil {
		t.Fatal(old)
	}
	ctx := ok(t, role, auth, "agent_context", map[string]any{})
	tools := ctx["tools"].(map[string]any)
	if n := len(tools["available"].([]any)); n != 6 {
		t.Fatal(tools)
	}
	un := tools["unavailable"].([]any)
	if len(un) != 7 || un[0].(map[string]any)["name"] != "agent_task_delegate" || un[0].(map[string]any)["reason"] == "" {
		t.Fatal(un)
	}

	// 開放:唯讀文件、組織鏈下游、加進頻道
	g, _ := st.GetAgent("lab", "solo")
	g.DocAccess = map[string]string{"docs": "read"}
	_ = st.SaveAgent("lab", *g)
	if _, err := svc.SaveChain(boss, "lab", []model.ChainEdge{{From: "agent:solo", To: "agent:pm"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChannel(boss, "lab", service.ChannelInput{Name: "討論", Members: []string{"agent:solo"}}); err != nil {
		t.Fatal(err)
	}
	got = listTools(t, role, auth)
	for _, n := range []string{"cabinet", "docs", "agent_task_delegate", "channels", "channel_messages", "channel_post"} {
		if !slices.Contains(got, n) {
			t.Fatal(n, got)
		}
	}
	if slices.Contains(got, "doc_save") { // 唯讀:不能寫
		t.Fatal(got)
	}
	l, err := svc.AgentTools(boss, "lab", "solo")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range l {
		if x.Available == (x.Name == "doc_save") || (x.Name == "doc_save" && x.Reason == "") {
			t.Fatal(x)
		}
	}
}

// 登記表(model.CoreTools)與工具規格一致:每個規格都有登記、每個登記都有規格,標題取自登記表;
// 執行者端點的工具標成 Worker,其他至少開放給一個端點
func TestCoreToolRegistry(t *testing.T) {
	defs := map[string]map[string]any{}
	for _, d := range append(mcp.CoreToolDefs(), mcp.WorkerToolDefs()...) {
		m := d.(map[string]any)
		defs[m["name"].(string)] = m
	}
	for _, ct := range model.CoreTools {
		d, ok := defs[ct.Name]
		if !ok {
			t.Fatalf("%s 有登記但沒有規格", ct.Name)
		}
		if d["title"] != ct.Title {
			t.Fatalf("%s 標題 %v ≠ 登記表 %s", ct.Name, d["title"], ct.Title)
		}
		if !ct.Admin && !ct.Agent && !ct.Worker {
			t.Fatalf("%s 沒有開放給任何端點", ct.Name)
		}
		delete(defs, ct.Name)
	}
	if len(defs) > 0 {
		t.Fatalf("有規格但沒登記:%v", defs)
	}
	if !mcp.AdminToolAllow["issues"] || mcp.AdminToolAllow["agent_task_delegate"] || !mcp.AgentToolAllow["agent_task_delegate"] || mcp.AgentToolAllow["chain_save"] {
		t.Fatal(mcp.AdminToolAllow, mcp.AgentToolAllow)
	}
}
