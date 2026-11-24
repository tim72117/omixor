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
	"omixor-core/service"
)

// issue_update 帶舊 version → 409:structuredContent 是 {error:{code,message,status,data:{currentVersion,current}}},text 的 data 只有 currentVersion 與 truncated
func TestIssueUpdateVersionConflictCarriesData(t *testing.T) {
	_, svc, amy := issueToolsEnv(t)
	h := mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: svc, P: amy}, "lab", nil
	}})
	iss, err := svc.CreateIssue(amy, "lab", service.IssueInput{Title: "t", Body: "開頭"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": "issue_update", "arguments": map[string]any{"id": iss.ID, "title": "新標題", "version": 99}}})
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp struct {
		Result struct {
			Content           []struct{ Text string } `json:"content"`
			IsError           bool                    `json:"isError"`
			StructuredContent map[string]any          `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.Result.IsError {
		t.Fatalf("要是 isError:%s", w.Body.String())
	}
	var sc struct {
		Error struct {
			Code, Message string
			Status        int
			Data          map[string]any
		}
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &sc); err != nil {
		t.Fatalf("text 要是 JSON:%v", err)
	}
	if !strings.HasPrefix(sc.Error.Message, "The issue was not changed: ") || sc.Error.Code != "conflict" || sc.Error.Status != 409 || sc.Error.Data["currentVersion"] == nil {
		t.Errorf("text 要含 message、code、status 與 data.currentVersion:%.300s", resp.Result.Content[0].Text)
	}
	if sc.Error.Data["current"] != nil || sc.Error.Data["truncated"] != true {
		t.Errorf("text 不該重複整份 Issue(current),要帶 truncated:%.300s", resp.Result.Content[0].Text)
	}
	e, _ := resp.Result.StructuredContent["error"].(map[string]any)
	if d, _ := e["data"].(map[string]any); e == nil || e["status"] != float64(409) || d["current"] == nil || d["truncated"] != nil {
		t.Errorf("structuredContent 要有 error.status 與完整的 data.current:%v", resp.Result.StructuredContent)
	}
}
