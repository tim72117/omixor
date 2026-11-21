package mcp_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/mcp"
	"omixor-core/service"
)

// MCPTasks 透過 MCP 端點讀角色與任務:送 tools/call、帶 token、解析回傳文字;工具錯誤要變成 error
func TestMCPTasksSource(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("要帶 token:%q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		var req struct {
			ID     any `json:"id"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(b, &req)
		args, _ := json.Marshal(req.Params.Arguments)
		calls = append(calls, req.Params.Name+string(args))
		text, isErr := "", false
		switch req.Params.Name {
		case "orgs":
			text = `{"current":"lab","orgs":[]}`
		case "agents":
			text = `{"agents":[{"id":"dev","name":"工程師","active":true},{"id":"old","active":false}]}`
		case "agent_tasks":
			text = `{"total":1,"tasks":[{"id":"T-7","title":"做事","agent":"dev","status":"queued","priority":"high","updatedAt":"2026-11-18T03:00:00Z"}]}`
		default:
			text, isErr = "沒有這個工具", true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr}})
	}))
	defer srv.Close()

	m := &mcp.MCPTasks{URL: srv.URL, Token: "tok"}
	if org, err := m.CurrentOrg(); err != nil || org != "lab" {
		t.Fatalf("CurrentOrg:%q %v", org, err)
	}
	ags, err := m.ListAgents("")
	if err != nil || len(ags) != 2 || !ags[0].Active || ags[1].Active {
		t.Fatalf("ListAgents:%+v %v", ags, err)
	}
	ts, err := m.ListAgentTasks("", service.TaskFilter{AgentID: "dev", Status: "queued"})
	if err != nil || len(ts) != 1 || ts[0].ID != "T-7" || ts[0].AgentID != "dev" || ts[0].UpdatedAt.IsZero() {
		t.Fatalf("ListAgentTasks:%+v %v", ts, err)
	}
	if !strings.Contains(calls[2], `"agent":"dev"`) || !strings.Contains(calls[2], `"status":"queued"`) {
		t.Fatalf("agent_tasks 的參數:%s", calls[2])
	}

	// 連不到、工具錯誤都要回 error(頻道會記錄並下次再試)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"沒有權限"}],"isError":true}}`))
	}))
	defer srv2.Close()
	if _, err := (&mcp.MCPTasks{URL: srv2.URL}).ListAgents(""); err == nil || !strings.Contains(err.Error(), "沒有權限") {
		t.Fatalf("工具錯誤要回 error:%v", err)
	}
	if _, err := (&mcp.MCPTasks{URL: "http://127.0.0.1:1/mcp"}).ListAgents(""); err == nil {
		t.Fatal("連不到要回 error")
	}
}

// agent_tasks 被截斷(total > 回傳筆數)時,改成每個角色各查一次
func TestMCPTasksTruncated(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls = append(calls, req.Params.Name+":"+req.Params.Arguments["agent"])
		text := ""
		switch {
		case req.Params.Name == "agents":
			text = `{"agents":[{"id":"a","active":true},{"id":"b","active":true}]}`
		case req.Params.Arguments["agent"] == "":
			text = `{"total":3,"tasks":[{"id":"T-1","agent":"a","status":"queued"}]}`
		case req.Params.Arguments["agent"] == "a":
			text = `{"total":1,"tasks":[{"id":"T-1","agent":"a","status":"queued"}]}`
		default:
			text = `{"total":2,"tasks":[{"id":"T-2","agent":"b","status":"queued"},{"id":"T-3","agent":"b","status":"queued"}]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}})
	}))
	defer srv.Close()
	l, err := (&mcp.MCPTasks{URL: srv.URL}).ListAgentTasks("", service.TaskFilter{Status: "queued"})
	if err != nil || len(l) != 3 {
		t.Fatalf("要拿到全部 3 筆:%+v %v(呼叫 %v)", l, err, calls)
	}
}

// TokenFunc 每次請求前取權杖(優先於 Token),換新權杖後不用重開;讀不到(錯誤或空字串)就不送請求,下一輪恢復後照常送
func TestMCPTasksTokenFunc(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"current":"lab"}`}}}})
	}))
	defer srv.Close()
	tok := "first"
	var tokErr error
	m := &mcp.MCPTasks{URL: srv.URL, Token: "ignored", TokenFunc: func() (string, error) { return tok, tokErr }}
	if _, err := m.CurrentOrg(); err != nil {
		t.Fatal(err)
	}
	tok = "second"
	_, _ = m.CurrentOrg()
	tok = ""
	if _, err := m.CurrentOrg(); err == nil {
		t.Fatal("空權杖要回錯誤")
	}
	tok, tokErr = "third", errors.New("檔案讀不到")
	if _, err := m.CurrentOrg(); err == nil {
		t.Fatal("讀權杖出錯要回錯誤")
	}
	if len(got) != 2 {
		t.Fatalf("讀不到權杖時不能送請求(含沒帶 Authorization 的):%q", got)
	}
	tok, tokErr = "third", nil
	if _, err := m.CurrentOrg(); err != nil {
		t.Fatalf("下一輪恢復要照常送:%v", err)
	}
	if len(got) != 3 || got[0] != "Bearer first" || got[1] != "Bearer second" || got[2] != "Bearer third" {
		t.Fatalf("Authorization 標頭:%q", got)
	}
}
