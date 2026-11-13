package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

const testTok = "omx_test-token-0123456789abcdefghij"

func localReq(t *testing.T, h http.Handler, method, path, tok string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// 啟動:本機使用者、預設組織、這次的 token 可以用;重開換 token 後舊的作廢,資料還在
func TestLocalOpenAndReopen(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok, User: "Tim"})
	if err != nil {
		t.Fatal(err)
	}
	h := l.Handler()
	code, out := localReq(t, h, "GET", "/api/orgs", testTok, nil)
	if code != 200 || !strings.Contains(mustJSON(out), `"id":"default"`) || !strings.Contains(mustJSON(out), `"name":"evoleap"`) {
		t.Fatal(code, out)
	}
	code, out = localReq(t, h, "POST", "/api/orgs/default/issues", testTok, map[string]any{"title": "x"})
	if code != 200 {
		t.Fatal(code, out)
	}
	if code, _ := localReq(t, h, "GET", "/api/orgs", "omx_wrong-token-xxxxxxxxxxxxxxxxxx", nil); code != 401 {
		t.Fatal("錯的 token 要 401", code)
	}
	tok2 := testTok + "2"
	l2, err := OpenLocal(LocalConfig{DataDir: dir, Token: tok2, User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	h2 := l2.Handler()
	if code, _ := localReq(t, h2, "GET", "/api/orgs", testTok, nil); code != 401 {
		t.Fatal("上一次的 token 要作廢", code)
	}
	code, out = localReq(t, h2, "GET", "/api/orgs/default/issues", tok2, nil)
	if code != 200 || !strings.Contains(mustJSON(out), `"title":"x"`) {
		t.Fatal("重開後資料要在", code, out)
	}
	// 頻道的作者是 user:tim(和 App 原本的本機資料一致)
	code, out = localReq(t, h2, "POST", "/api/orgs/default/channels", tok2, map[string]any{"name": "c"})
	if code != 200 || !strings.Contains(mustJSON(out), `"createdBy":"user:tim"`) {
		t.Fatal(code, out)
	}
	// MCP 管理端點
	code, out = localReq(t, h2, "POST", "/mcp/org/default", tok2, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if code != 200 || !strings.Contains(mustJSON(out), "issue_create") {
		t.Fatal("MCP", code, out)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// 匯入 App 的本機資料:只匯入一次;頻道與組織鏈的 user 節點補成組織成員
func TestLocalImport(t *testing.T) {
	l, err := OpenLocal(LocalConfig{DataDir: t.TempDir(), Token: testTok, User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	h := l.Handler()
	if code, out := localReq(t, h, "POST", "/local/orgs", testTok, map[string]any{"orgs": []map[string]any{
		{"id": "default", "name": "evoleap", "symbol": "graduationcap"}, {"id": "acme", "name": "Acme"}}}); code != 200 {
		t.Fatal(code, out)
	}
	at := model.Now()
	tab := "/x"
	in := AppImport{
		Issues: &[]model.Issue{{ID: "ISS-3", Title: "舊的", CreatedBy: "tim", CreatedAt: at, UpdatedAt: at, Status: "open", Priority: "high"}},
		Agents: &service.AgentImport{Agents: []model.Agent{{ID: "dev", Name: "工程師", Active: true}},
			Tasks: []model.AgentTask{{ID: "T-2", AgentID: "dev", Title: "做事", Status: "queued"}}},
		CabinetLayout: &[]model.CabinetSection{{ID: "docs", Label: "文件", Tabs: []model.CabinetTab{{Path: "/x", Label: "X"}}}},
		Docs:          &[]model.CabinetDoc{{ID: "DOC-4", Title: "d", Section: "docs", Tab: &tab, Author: "tim", CreatedAt: at, UpdatedAt: at}},
		Chain:         &model.OrgChain{Edges: []model.ChainEdge{{From: "user:tim", To: "agent:dev"}, {From: "user:alice", To: "agent:dev", Label: "目標"}}},
		Channels: &AppChannels{
			Channels: []model.Channel{{ID: "CH-1", Name: "一般", Members: []string{"user:tim", "agent:dev"}, CreatedBy: "user:tim", CreatedAt: at, UpdatedAt: at}},
			Messages: []model.ChannelMessage{{ID: "M-1", ChannelID: "CH-1", Author: "user:tim", Text: "hi @dev", Mentions: []string{"agent:dev"}, At: at},
				{ID: "M-2", ChannelID: "CH-1", Author: "agent:dev", Text: "ok", At: at}},
			Reads: []model.ChannelRead{{ChannelID: "CH-1", Member: "user:tim", LastRead: 1, At: at}},
		},
	}
	code, out := localReq(t, h, "POST", "/local/import/default", testTok, in)
	if code != 200 {
		t.Fatal(code, out)
	}
	res, _ := out["result"].(map[string]any)
	for _, k := range []string{"issues", "agents", "cabinet", "chain", "channels"} {
		if sec, _ := res[k].(map[string]any); sec["status"] != "done" {
			t.Fatal(k, mustJSON(out))
		}
	}
	// 再匯入一次:全部略過
	_, out = localReq(t, h, "POST", "/local/import/default", testTok, in)
	if strings.Contains(mustJSON(out), `"done"`) {
		t.Fatal("第二次不能再匯入", mustJSON(out))
	}
	for path, want := range map[string]string{
		"/api/orgs/default/issues":                 `"id":"ISS-3"`,
		"/api/orgs/default/agent-tasks":            `"id":"T-2"`,
		"/api/orgs/default/docs":                   `"id":"DOC-4"`,
		"/api/orgs/default/chain":                  `"from":"user:alice"`,
		"/api/orgs/default/channels":               `"unread":1`,
		"/api/orgs/default/channels/CH-1/messages": `"id":"M-2"`,
	} {
		code, out := localReq(t, h, "GET", path, testTok, nil)
		if code != 200 || !strings.Contains(mustJSON(out), want) {
			t.Fatal(path, code, mustJSON(out))
		}
	}
	// 新的組織也建好了,本機使用者是管理員
	if code, out := localReq(t, h, "GET", "/api/orgs/acme/members", testTok, nil); code != 200 || !strings.Contains(mustJSON(out), `"role":"admin"`) {
		t.Fatal(code, out)
	}
	// 沒有登入不能用 /local
	if code, _ := localReq(t, h, "POST", "/local/orgs", "", map[string]any{}); code != 401 {
		t.Fatal(code)
	}
}

func TestLoopbackOnly(t *testing.T) {
	for _, a := range []string{"127.0.0.1:0", "localhost:1", "[::1]:2"} {
		if loopbackAddr(a) != nil {
			t.Fatal(a)
		}
	}
	for _, a := range []string{":8080", "0.0.0.0:1", "192.168.1.2:3"} {
		if loopbackAddr(a) == nil {
			t.Fatal(a)
		}
	}
}

func TestLockDir(t *testing.T) {
	dir := t.TempDir()
	f, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := lockDir(dir); err == nil {
		t.Fatal("第二個要被擋")
	}
}
