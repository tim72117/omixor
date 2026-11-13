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

// 訊息頻道工具:管理端點建立、發、讀(讀了算已讀);角色端點的 agent_context mentions、只能碰自己的頻道、不能建
func TestChannelTools(t *testing.T) {
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
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長"}, {ID: "pm", Name: "產品經理"}} {
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

	c := ok(t, member, "", "channel_create", map[string]any{"name": "首頁改版", "topic": "下週上線", "members": []any{"研發長"}})
	if c["id"] != "CH-1" || len(c["members"].([]any)) != 2 || c["members"].([]any)[1].(map[string]any)["name"] != "研發長" {
		t.Fatal(c)
	}
	fails(t, member, "", "channel_post", map[string]any{"channel": "首頁改版", "text": "  "}, "訊息不能空白")
	failsWith(t, member, "", "channel_post", map[string]any{"channel": "沒有這個", "text": "x"}, "找不到頻道 沒有這個")
	p := ok(t, member, "", "channel_post", map[string]any{"channel": "首頁改版", "text": "@研發長 請排優先度,@產品經理 不是成員"})
	if p["author"] != "user:amy@x" || p["authorName"] != "amy@x" || len(p["mentions"].([]any)) != 1 {
		t.Fatal(p)
	}
	// 管理員不是成員:看得到、讀得到,但不能發
	if l := ok(t, admin, "", "channels", map[string]any{}); len(l["channels"].([]any)) != 1 {
		t.Fatal(l)
	}
	failsWith(t, admin, "", "channel_post", map[string]any{"channel": "CH-1", "text": "x"}, "不是頻道 CH-1 的成員")

	// 角色端點
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "cto", Title: "規劃"})
	res, err := svc.ClaimTask(amy, "lab", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	auth := "Bearer cto:" + res.Credential.Secret
	ctx := ok(t, role, auth, "agent_context", map[string]any{})
	mn := ctx["mentions"].(map[string]any)
	msgs := mn["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["authorName"] != "amy@x" || msgs[0].(map[string]any)["channelName"] != "首頁改版" {
		t.Fatal(mn)
	}
	if chs := mn["channels"].([]any); len(chs) != 1 || chs[0].(map[string]any)["unread"].(float64) != 1 {
		t.Fatal(mn)
	}
	if l := ok(t, role, auth, "channels", map[string]any{}); l["channels"].([]any)[0].(map[string]any)["unread"].(float64) != 1 {
		t.Fatal(l)
	}
	// 讀了算已讀
	r := ok(t, role, auth, "channel_messages", map[string]any{"channel": "CH-1"})
	if len(r["messages"].([]any)) != 1 || r["channel"].(map[string]any)["topic"] != "下週上線" {
		t.Fatal(r)
	}
	ctx = ok(t, role, auth, "agent_context", map[string]any{})
	if mn := ctx["mentions"].(map[string]any); len(mn["messages"].([]any)) != 0 || mn["channels"].([]any)[0].(map[string]any)["unread"].(float64) != 0 {
		t.Fatal("讀過了", mn)
	}
	reply := ok(t, role, auth, "channel_post", map[string]any{"channel": "CH-1", "text": "好,@amy@x 週五前給你"})
	if reply["author"] != "agent:cto" || reply["authorName"] != "研發長" {
		t.Fatal(reply)
	}
	fails(t, role, auth, "channel_post", map[string]any{"channel": "CH-1", "text": "x", "agent": "pm"}, "角色端點只能以自己的身分操作,不能指定其他角色")
	if mcp.AgentToolAllow["channel_create"] || !mcp.AdminToolAllow["channel_create"] {
		t.Fatal("角色端點沒有 channel_create,管理端點有")
	}
	ok(t, member, "", "channel_create", map[string]any{"name": "私下"})
	failsWith(t, role, auth, "channel_messages", map[string]any{"channel": "CH-2"}, "找不到頻道 CH-2")

	// amy 讀:since
	r = ok(t, member, "", "channel_messages", map[string]any{"channel": "CH-1", "since": "M-1"})
	if ms := r["messages"].([]any); len(ms) != 1 || ms[0].(map[string]any)["id"] != "M-2" {
		t.Fatal(r)
	}
}

// failsWith 錯誤訊息包含 want
func failsWith(t *testing.T, h http.Handler, auth, tool string, args map[string]any, want string) {
	t.Helper()
	if _, e := call(t, h, auth, tool, args); !strings.Contains(e, want) {
		t.Fatalf("%s:想要包含「%s」,得到「%s」", tool, want, e)
	}
}
