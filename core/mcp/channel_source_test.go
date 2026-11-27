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

// WorkerTasks 只靠 dispatch_queue:帶 token、依角色與狀態篩選;工具錯誤與連不到要變成 error
func TestWorkerTasksSource(t *testing.T) {
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
		text := `{"total":2,"tasks":[{"id":"T-7","title":"做事","agent":{"id":"dev","name":"工程師"},"status":"queued","priority":"high","updatedAt":"2026-11-18T03:00:00Z"},` +
			`{"id":"T-8","agent":{"id":"dev"},"status":"blocked"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}})
	}))
	defer srv.Close()

	m := &mcp.WorkerTasks{URL: srv.URL, Token: "tok"}
	ags, err := m.ListAgents("")
	if err != nil || len(ags) != 1 || ags[0].ID != "dev" || !ags[0].Active {
		t.Fatalf("ListAgents:%+v %v", ags, err)
	}
	ts, err := m.ListAgentTasks("", service.TaskFilter{AgentID: "dev", Status: "queued"})
	if err != nil || len(ts) != 1 || ts[0].ID != "T-7" || ts[0].AgentID != "dev" || ts[0].UpdatedAt.IsZero() {
		t.Fatalf("ListAgentTasks:%+v %v", ts, err)
	}
	if !strings.HasPrefix(calls[1], "dispatch_queue") || !strings.Contains(calls[1], `"agent":"dev"`) {
		t.Fatalf("dispatch_queue 的參數:%s", calls[1])
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"沒有權限"}],"isError":true}}`))
	}))
	defer srv2.Close()
	if _, err := (&mcp.WorkerTasks{URL: srv2.URL}).ListAgents(""); err == nil || !strings.Contains(err.Error(), "沒有權限") {
		t.Fatalf("工具錯誤要回 error:%v", err)
	}
	if _, err := (&mcp.WorkerTasks{URL: "http://127.0.0.1:1/mcp"}).ListAgents(""); err == nil {
		t.Fatal("連不到要回 error")
	}
}

// TokenFunc 每次請求前取權杖(優先於 Token),換新權杖後不用重開;讀不到(錯誤或空字串)就不送請求,下一輪恢復後照常送
func TestWorkerTasksTokenFunc(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"tasks":[]}`}}}})
	}))
	defer srv.Close()
	tok := "first"
	var tokErr error
	m := &mcp.WorkerTasks{URL: srv.URL, Token: "ignored", TokenFunc: func() (string, error) { return tok, tokErr }}
	if _, err := m.ListAgents(""); err != nil {
		t.Fatal(err)
	}
	tok = "second"
	_, _ = m.ListAgents("")
	tok = ""
	if _, err := m.ListAgents(""); err == nil {
		t.Fatal("空權杖要回錯誤")
	}
	tok, tokErr = "third", errors.New("檔案讀不到")
	if _, err := m.ListAgents(""); err == nil {
		t.Fatal("讀權杖出錯要回錯誤")
	}
	if len(got) != 2 {
		t.Fatalf("讀不到權杖時不能送請求(含沒帶 Authorization 的):%q", got)
	}
	tok, tokErr = "third", nil
	if _, err := m.ListAgents(""); err != nil {
		t.Fatalf("下一輪恢復要照常送:%v", err)
	}
	if len(got) != 3 || got[0] != "Bearer first" || got[1] != "Bearer second" || got[2] != "Bearer third" {
		t.Fatalf("Authorization 標頭:%q", got)
	}
}
