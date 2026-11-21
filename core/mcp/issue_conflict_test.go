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

// issue_update 帶舊 version → 409:工具結果的 text 含 currentVersion(agent 只看得到 text),structuredContent 有 data 與 status
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
	text := resp.Result.Content[0].Text
	if !strings.HasPrefix(text, "Issue 沒有改成:") || !strings.Contains(text, `"currentVersion"`) {
		t.Errorf("text 要含前綴與 currentVersion:%.300s", text)
	}
	sc := resp.Result.StructuredContent
	if sc["status"] != float64(409) || sc["currentVersion"] == nil {
		t.Errorf("structuredContent 要有 status 與 currentVersion:%v", sc)
	}
}
