package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"unicode"

	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
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
	st := storetest.New(t)
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

	// 什麼都沒設:只有任務、Issue 與不需要設定的工具(線框圖、流程圖)
	got := listTools(t, role, auth)
	want := []string{"agent_context", "agent_tasks", "agent_task_report", "issues", "issue_create", "issue_update", "issue_accept", "issue_decision_add", "issue_decisions", "issue_item_add", "issue_items", "issue_item_report", "wireframe_render", "flowchart_render"}
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
	if n := len(tools["available"].([]any)); n != 14 {
		t.Fatal(tools)
	}
	un := tools["unavailable"].([]any)
	if len(un) != 8 || un[0].(map[string]any)["name"] != "agent_task_delegate" {
		t.Fatal(un)
	}
	// 沒設定時四種原因代碼都出現;LLM 看的欄位沒有中文,per-call 的 note 已移到 instructions
	if _, has := tools["note"]; has {
		t.Fatal("tools.note 應已移除", tools)
	}
	if codes := unavailableCodes(t, tools); !slices.Equal(codes, []string{"doc_read_missing", "doc_write_missing", "no_downstream", "not_channel_member"}) {
		t.Fatal(codes)
	}
	assertNoCJKExceptLabel(t, tools)
	assertInstructionsExplainUnavailable(t, role, auth)

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
	for _, n := range []string{"projects", "cabinet", "docs", "agent_task_delegate", "channels", "channel_messages", "channel_post"} {
		if !slices.Contains(got, n) {
			t.Fatal(n, got)
		}
	}
	if slices.Contains(got, "doc_save") { // 唯讀:不能寫
		t.Fatal(got)
	}
	// 唯讀文件 + 有下游 + 在頻道:只剩寫入原因
	tools = ok(t, role, auth, "agent_context", map[string]any{})["tools"].(map[string]any)
	if codes := unavailableCodes(t, tools); !slices.Equal(codes, []string{"doc_write_missing"}) {
		t.Fatal(codes)
	}
	assertNoCJKExceptLabel(t, tools)
	l, err := svc.AgentTools(boss, "lab", "solo")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range l {
		if x.Available == (x.Name == "doc_save") || (x.Name == "doc_save" && (x.Reason == "" || x.ReasonCode != "doc_write_missing")) {
			t.Fatal(x)
		}
	}
}

// unavailableCodes tools.unavailable 出現的原因代碼(排序、去重);順便確認每個都有英文說明
func unavailableCodes(t *testing.T, tools map[string]any) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, u := range tools["unavailable"].([]any) {
		m := u.(map[string]any)
		code, _ := m["reason"].(string)
		if msg, _ := m["reason_message"].(string); code == "" || msg == "" {
			t.Fatal(m)
		}
		seen[code] = true
	}
	out := []string{}
	for c := range seen {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

// assertNoCJKExceptLabel tools 區塊除了 label(給人看的名稱)以外不含中日韓文字
func assertNoCJKExceptLabel(t *testing.T, tools map[string]any) {
	t.Helper()
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			for _, r := range x {
				if unicode.Is(unicode.Han, r) {
					t.Fatalf("tools 區塊有中文:%q", x)
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				if k != "label" {
					walk(e)
				}
			}
		}
	}
	walk(tools)
}

// assertInstructionsExplainUnavailable 角色端點 initialize 的 instructions 說明 unavailable 工具的處理方式(取代每次 agent_context 都帶的 note)
func assertInstructionsExplainUnavailable(t *testing.T, h http.Handler, auth string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	for _, want := range []string{"tools.unavailable", "only a human can change", "do not retry them or invent workarounds", "agent_task_report status=blocked"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("instructions 缺 %q:%s", want, rec.Body.String())
		}
	}
}
