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
	"omixor-core/store"
)

// 呼叫一個工具;回傳 (結果, 是不是錯誤)
func call(t *testing.T, h http.Handler, auth, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("%s:HTTP %d %s", tool, w.Code, w.Body.String())
	}
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Error != nil {
		t.Fatalf("%s:%s", tool, w.Body.String())
	}
	text := resp.Result.Content[0].Text
	if resp.Result.IsError {
		return nil, text
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return out, ""
}

func ok(t *testing.T, h http.Handler, auth, tool string, args map[string]any) map[string]any {
	t.Helper()
	out, e := call(t, h, auth, tool, args)
	if e != "" {
		t.Fatalf("%s:%s", tool, e)
	}
	return out
}

func fails(t *testing.T, h http.Handler, auth, tool string, args map[string]any, want string) {
	t.Helper()
	if _, e := call(t, h, auth, tool, args); e != want {
		t.Fatalf("%s:想要「%s」,得到「%s」", tool, want, e)
	}
}

func TestCabinetToolsAdminAndRole(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	code := "L0"
	if _, err := svc.SaveCabinetLayout(boss, "lab", []model.CabinetSection{
		{ID: "levels", Label: "課程", Path: "/studio/levels", Tabs: []model.CabinetTab{{Path: "/studio/l0", Label: "AI 工具介紹", Code: &code}}},
		{ID: "docs", Label: "文件", Tabs: []model.CabinetTab{{Path: "/docs/m", Label: "行銷手法"}}},
		{ID: "secret", Label: "機密"},
	}); err != nil {
		t.Fatal(err)
	}
	admin := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		AgentEndpoint: "http://x/mcp/agent",
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: boss}, "lab", nil
		},
	})
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})

	// 管理端點:doc_access 的 key 可以用名稱與代號
	g := ok(t, admin, "", "agent_upsert", map[string]any{"id": "writer", "name": "寫手", "doc_access": map[string]any{"L0": "read", "文件": "write"}})
	if acc := g["docAccess"].([]any); len(acc) != 2 {
		t.Fatalf("docAccess 要換成看得懂的範圍:%v", g)
	}
	fails(t, admin, "", "agent_upsert", map[string]any{"id": "writer", "doc_access": map[string]any{"沒有": "read"}}, "文件櫃沒有「沒有」這個區塊或頁籤")
	if a, _ := st.GetAgent("lab", "writer"); a.DocAccess["/studio/l0"] != "read" || a.DocAccess["docs"] != "write" {
		t.Fatalf("%+v", a.DocAccess)
	}

	d := ok(t, admin, "", "doc_save", map[string]any{"title": "L0 腳本", "tab": "L0", "body": "你好"})
	if d["id"] != "DOC-1" || d["location"] != "課程 › L0 AI 工具介紹" || d["chars"].(float64) != 2 || d["author"] != "boss@x" {
		t.Fatal(d)
	}
	ok(t, admin, "", "doc_save", map[string]any{"title": "機密", "section": "機密"})
	fails(t, admin, "", "doc_save", map[string]any{"title": "x", "section": "nope"}, "要指定區塊(levels、docs、secret)")
	fails(t, admin, "", "doc_save", map[string]any{"title": "x", "section": "levels", "agent": "writer"}, "寫手沒有權限寫入「課程」;它可以寫的範圍:文件")
	fails(t, admin, "", "docs", map[string]any{"id": "DOC-2", "agent": "writer"}, "寫手沒有權限讀「機密」的文件")
	if l := ok(t, admin, "", "docs", map[string]any{"query": "腳本"}); l["total"].(float64) != 1 {
		t.Fatal(l)
	}
	if c := ok(t, admin, "", "cabinet", map[string]any{"agent": "writer"}); c["organization"] != "實驗室" || len(c["sections"].([]any)) != 2 {
		t.Fatal(c)
	}
	ctx := ok(t, admin, "", "agent_context", map[string]any{"agent": "writer", "include_bodies": true})
	if docs := ctx["docs"].(map[string]any); docs["total"].(float64) != 1 || docs["docs"].([]any)[0].(map[string]any)["body"] != "你好" {
		t.Fatalf("角色能讀的文件:%v", ctx["docs"])
	}

	// 角色端點:憑證的角色,依 docAccess;署名是角色
	if _, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "writer", Title: "寫文件"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "writer"})
	if err != nil || res.Credential == nil {
		t.Fatal(res, err)
	}
	auth := "Bearer writer:" + res.Credential.Secret
	l := ok(t, role, auth, "docs", map[string]any{})
	if l["total"].(float64) != 1 {
		t.Fatalf("角色只看得到能讀的:%v", l)
	}
	fails(t, role, auth, "docs", map[string]any{"id": "DOC-2"}, "寫手沒有權限讀「機密」的文件")
	fails(t, role, auth, "doc_save", map[string]any{"id": "DOC-1", "title": "改"}, "寫手沒有權限寫入「課程 › L0 AI 工具介紹」;它可以寫的範圍:文件")
	fails(t, role, auth, "doc_save", map[string]any{"title": "x", "section": "docs", "agent": "someone"}, "角色端點只能以自己的身分操作,不能指定其他角色")
	w := ok(t, role, auth, "doc_save", map[string]any{"title": "行銷筆記", "tab": "行銷手法", "body": "社群"})
	if w["author"] != "寫手" || w["tab"] != "/docs/m" {
		t.Fatal(w)
	}
	if dd, _ := st.GetDoc("lab", w["id"].(string)); dd.Author != "agent:writer" {
		t.Fatal(dd.Author)
	}
	ok(t, role, auth, "doc_save", map[string]any{"id": w["id"], "append_body": "補充", "tags": []any{"a"}})
	c := ok(t, role, auth, "cabinet", map[string]any{})
	if secs := c["sections"].([]any); len(secs) != 2 {
		t.Fatalf("角色只列能存取的位置:%v", secs)
	}
	ctx = ok(t, role, auth, "agent_context", map[string]any{})
	if ctx["cabinet"] == nil || ctx["docs"].(map[string]any)["total"].(float64) != 2 {
		t.Fatalf("%v", ctx)
	}
	if !strings.Contains(c["organization"].(string), "實驗室") {
		t.Fatal(c)
	}
}
