package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// 人類端點(管理端點)不接受「以角色身分」的 agent 參數:署名一律是呼叫者;角色端點照舊(憑證強制)。

func identityEnv(t *testing.T) (svc *service.Service, boss, amy *model.Principal, admin, member http.Handler) {
	t.Helper()
	st := storetest.New(t)
	svc = &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.SaveUser(model.User{ID: "u2", Email: "amy@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u2", Email: "amy@x", Role: "member", AddedAt: now})
	boss = &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	amy = &model.Principal{User: model.User{ID: "u2", Email: "amy@x", Role: "member"}}
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長"}, {ID: "pm", Name: "產品經理"}} {
		g.Active = true
		if _, err := svc.SaveAgent(boss, "lab", g); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SaveCabinetLayout(boss, "lab", "", []model.CabinetSection{{ID: "docs", Label: "文件"}}); err != nil {
		t.Fatal(err)
	}
	endpoint := func(p *model.Principal) http.Handler {
		return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: p}, "lab", nil
		}})
	}
	return svc, boss, amy, endpoint(boss), endpoint(amy)
}

// 呼叫帶 agent 的身分假冒型工具:全部回 400 固定句子;而且什麼都沒寫入
func TestAdminEndpointRejectsIdentityAgent(t *testing.T) {
	_, _, _, admin, member := identityEnv(t)
	calls := map[string]map[string]any{
		"issue_create":        {"title": "x"},
		"issue_update":        {"id": "ISS-1", "comment": "x"},
		"docs":                {},
		"doc_save":            {"title": "x", "section": "docs"},
		"cabinet":             {},
		"cabinet_layout_save": {"add_sections": []any{map[string]any{"label": "新"}}},
		"channels":            {},
		"channel_create":      {"name": "x"},
		"channel_messages":    {"channel": "CH-1"},
		"channel_post":        {"channel": "CH-1", "text": "x"},
		"flowchart_render":    {"spec": "flowchart TD\n A-->B", "save": map[string]any{"section": "docs", "title": "x"}},
		"wireframe_render":    {"spec": map[string]any{"device": "mobile", "root": map[string]any{"type": "text", "text": "x"}}, "save": map[string]any{"section": "docs", "title": "x"}},
		"agent_task_create":   {"assign_to": "pm", "title": "x"},
	}
	for _, h := range []http.Handler{admin, member} {
		for tool, args := range calls {
			a := map[string]any{"agent": "cto"}
			for k, v := range args {
				a[k] = v
			}
			fails(t, h, "", tool, a, mcp.IdentityAgentError)
		}
	}
	// 沒寫入:沒有 Issue、文件、任務、頻道,文件櫃結構沒變
	if r := ok(t, admin, "", "issues", map[string]any{"status": "all"}); r["total"].(float64) != 0 {
		t.Fatal("Issue 不能寫入", r)
	}
	if r := ok(t, admin, "", "docs", map[string]any{}); r["total"].(float64) != 0 {
		t.Fatal("文件不能寫入", r)
	}
	if r := ok(t, admin, "", "agent_tasks", map[string]any{"status": "all"}); r["total"].(float64) != 0 {
		t.Fatal("任務不能寫入", r)
	}
	if r := ok(t, admin, "", "channels", map[string]any{}); len(r["channels"].([]any)) != 0 {
		t.Fatal("頻道不能寫入", r)
	}
	if c := ok(t, admin, "", "cabinet", map[string]any{}); len(c["sections"].([]any)) != 1 {
		t.Fatal("文件櫃結構不能改", c)
	}
	// 空字串的 agent 等於沒帶
	ok(t, admin, "", "issue_create", map[string]any{"title": "ok", "agent": ""})
	// agent_task_delegate 的委派者只能由角色憑證決定:人類端點沒有這個工具
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "agent_task_delegate", "arguments": map[string]any{"to": "pm", "title": "x", "agent": "cto"}}})
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "Unknown tool") {
		t.Fatal("人類端點不該有 agent_task_delegate:", w.Body.String())
	}
}

// 署名一律是呼叫者;Issue 指派走組織鏈檢查,from 是呼叫者(不能再用 agent 繞過)
func TestAdminEndpointIssueAuthorAndAssignChain(t *testing.T) {
	svc, boss, _, admin, member := identityEnv(t)
	i := ok(t, member, "", "issue_create", map[string]any{"title": "a", "return": "full"})
	if i["createdBy"] != "amy@x" {
		t.Fatalf("署名要是呼叫者:%v", i["createdBy"])
	}
	if _, err := svc.SaveChain(boss, "lab", []model.ChainEdge{{From: "user:boss@x", To: "agent:cto", Label: "目標"}}); err != nil {
		t.Fatal(err)
	}
	// 一般成員 amy 沒有 user:amy@x → agent:pm 的邊:被鏈擋下;帶 agent 一樣不能繞過(400,先於鏈檢查)
	if _, e := call(t, member, "", "issue_create", map[string]any{"title": "b", "assignee": "pm"}); !strings.Contains(e, "組織鏈上沒有") {
		t.Fatalf("要被組織鏈擋下:%q", e)
	}
	fails(t, member, "", "issue_create", map[string]any{"title": "b", "assignee": "pm", "agent": "cto"}, mcp.IdentityAgentError)
	fails(t, member, "", "issue_update", map[string]any{"id": "ISS-1", "assignee": "pm", "agent": "cto"}, mcp.IdentityAgentError)
	// 管理員不受限
	ok(t, admin, "", "issue_create", map[string]any{"title": "c", "assignee": "cto"})
}

// tools/list:不再列身分假冒型的 agent 參數;對象選擇型(agent_context、agent_task_claim)照舊
func TestSpecHasNoIdentityAgentParam(t *testing.T) {
	seen := 0
	for _, d := range mcp.CoreToolDefs() {
		m := d.(map[string]any)
		name := m["name"].(string)
		sch := m["inputSchema"].(map[string]any)
		_, has := sch["properties"].(map[string]any)["agent"]
		if mcp.IdentityAgentTools[name] && has {
			t.Errorf("%s 還列著 agent 參數", name)
		}
		if mcp.IdentityAgentTools[name] {
			req, _ := sch["required"].([]string)
			for _, r := range req {
				if r == "agent" {
					t.Errorf("%s 的 required 還有 agent", name)
				}
			}
		}
		if name == "agent_context" || name == "agent_task_claim" {
			seen++
			if !has {
				t.Errorf("%s 是對象選擇型,要保留 agent", name)
			}
		}
	}
	if seen != 2 {
		t.Fatal("少了對象選擇型工具")
	}
	// 經過 HTTP 的 tools/list 與 initialize 的說明也不教人帶 agent
	_, _, _, admin, _ := identityEnv(t)
	for _, method := range []string{"tools/list", "initialize"} {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method})
		req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, req)
		if strings.Contains(w.Body.String(), "帶 agent") || strings.Contains(w.Body.String(), "填角色 id 或名稱,") {
			t.Fatal(method, "的說明還教人帶 agent")
		}
	}
}

// 角色端點行為不變:署名是憑證的角色;帶別的角色被擋;帶自己的角色等於沒帶
func TestRoleEndpointIdentityUnchanged(t *testing.T) {
	svc, boss, _, _, _ := identityEnv(t)
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	if _, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "cto", Title: "做事"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "cto"})
	if err != nil || res.Credential == nil {
		t.Fatal(res, err)
	}
	auth := "Bearer cto:" + res.Credential.Secret
	i := ok(t, role, auth, "issue_create", map[string]any{"title": "角色開的", "return": "full"})
	if i["createdBy"] != "研發長" {
		t.Fatal(i)
	}
	fails(t, role, auth, "issue_create", map[string]any{"title": "冒充", "agent": "pm"}, "The agent endpoint can only act as its own identity; another agent cannot be specified")
	ok(t, role, auth, "issue_create", map[string]any{"title": "帶自己", "agent": "cto"})
	if r := ok(t, role, auth, "issues", map[string]any{}); r["total"].(float64) != 2 {
		t.Fatal(r)
	}
}

func listToolDefs(t *testing.T, h http.Handler, auth string) map[string]map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err, w.Body.String())
	}
	out := map[string]map[string]any{}
	for _, d := range resp.Result.Tools {
		out[d["name"].(string)] = d
	}
	return out
}

func hasAgentProp(d map[string]any) bool {
	sch, _ := d["inputSchema"].(map[string]any)
	props, _ := sch["properties"].(map[string]any)
	_, has := props["agent"]
	return has
}

// 管理端點(人類)的 tools/list 不列身分假冒型的 agent 參數、不列 agent_task_delegate;對象選擇型的 agent 照舊
func TestAdminEndpointListHidesIdentityAgent(t *testing.T) {
	_, _, _, admin, _ := identityEnv(t)
	tools := listToolDefs(t, admin, "")
	seen := 0
	for name, d := range tools {
		if mcp.IdentityAgentTools[name] && hasAgentProp(d) {
			t.Errorf("人類端點的 %s 還列著 agent 參數", name)
		}
		if name == "agent_context" || name == "agent_task_claim" {
			seen++
			if !hasAgentProp(d) {
				t.Errorf("%s 是對象選擇型,要保留 agent", name)
			}
		}
	}
	if seen != 2 {
		t.Fatal("少了對象選擇型工具", seen)
	}
	if _, ok := tools["agent_task_delegate"]; ok {
		t.Error("人類端點不該列 agent_task_delegate")
	}
	if _, ok := tools["issue_create"]; !ok {
		t.Fatal("issue_create 應該在")
	}
}

// 角色端點的 tools/list 不過濾:身分由憑證決定,agent_task_delegate 在(有下游時)
func TestRoleEndpointListKeepsIdentityAgent(t *testing.T) {
	svc, boss, _, _, _ := identityEnv(t)
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	if _, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "cto", Title: "做事"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "cto"})
	if err != nil || res.Credential == nil {
		t.Fatal(res, err)
	}
	tools := listToolDefs(t, role, "Bearer cto:"+res.Credential.Secret)
	d, ok := tools["issue_create"]
	if !ok {
		t.Fatal("角色端點要有 issue_create")
	}
	// 角色端點不過濾:規格與核心定義完全一樣
	for _, c := range mcp.CoreToolDefs() {
		if m := c.(map[string]any); m["name"] == "issue_create" {
			x, _ := json.Marshal(m)
			y, _ := json.Marshal(d)
			if string(x) != string(y) {
				t.Errorf("角色端點的 issue_create 規格被改了 %s vs %s", x, y)
			}
		}
	}
	if !hasAgentProp(tools["agent_context"]) {
		t.Error("agent_context 要保留 agent")
	}
}

// required 是 []any(外部 JSON 規格解出來的型別)也要拿掉 agent;不改原本的
func TestWithoutIdentityAgentRequiredAny(t *testing.T) {
	var defs []any
	src := `[{"name":"doc_save","description":"x 以角色身分工作時帶 agent,草稿會署名該角色。","inputSchema":{"type":"object","properties":{"agent":{"type":"string"},"title":{"type":"string"}},"required":["agent","title"]}},
{"name":"agent_context","inputSchema":{"type":"object","properties":{"agent":{"type":"string"}},"required":["agent"]}}]`
	if err := json.Unmarshal([]byte(src), &defs); err != nil {
		t.Fatal(err)
	}
	out := mcp.WithoutIdentityAgent(defs)
	sch := out[0].(map[string]any)["inputSchema"].(map[string]any)
	req, isAny := sch["required"].([]any)
	if !isAny || len(req) != 1 || req[0] != "title" {
		t.Fatalf("required 要剩 title:%#v", sch["required"])
	}
	if _, has := sch["properties"].(map[string]any)["agent"]; has {
		t.Fatal("properties 還有 agent")
	}
	// 原本的沒被改
	orig := defs[0].(map[string]any)["inputSchema"].(map[string]any)
	if len(orig["required"].([]any)) != 2 {
		t.Fatal("原規格被改了")
	}
	// 對象選擇型不動
	if req := out[1].(map[string]any)["inputSchema"].(map[string]any)["required"].([]any); len(req) != 1 {
		t.Fatal("agent_context 不該動", req)
	}
}

func TestRejectAgentArg(t *testing.T) {
	for _, args := range []map[string]any{{"agent": 5}, {"agent": true}, {"agent": "pm"}, {"agent": " pm "}} {
		if te, ok := mcp.RejectAgentArg(args).(mcp.ToolError); !ok || te.Status != 400 || te.Message != mcp.IdentityAgentError {
			t.Errorf("%v 要 400", args)
		}
	}
	for _, args := range []map[string]any{nil, {}, {"agent": ""}, {"agent": "  "}} {
		if err := mcp.RejectAgentArg(args); err != nil {
			t.Errorf("%v 要放行:%v", args, err)
		}
	}
}
