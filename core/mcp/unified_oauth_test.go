package mcp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
)

// 統一入口收 OAuth 的 omo_(帳號層級、唯讀):只開放 ReadOnlyTools,每次呼叫仍要 org 且是即時成員;omx_ 行為不變

func (u *uni) readOnlyEndpoint() http.Handler {
	p := *u.boss
	p.ReadOnly, p.Via = true, model.ViaOAuth
	return mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{Resolve: func(*http.Request) (backend.Backend, error) {
		return &backend.Local{Svc: u.svc, P: &p}, nil
	}})
}

func TestUnifiedReadOnlyOnlyReadTools(t *testing.T) {
	u := newUni(t)
	ro := u.readOnlyEndpoint()
	// 讀取工具可用(帶 org)
	if r := callRes(t, ro, "issues", map[string]any{"org": "lab"}); r.isErr || r.meta["omixor/org"] != "lab" {
		t.Fatal(r)
	}
	if r := callRes(t, ro, "orgs", map[string]any{}); r.isErr || !strings.Contains(r.all(), "lab2") {
		t.Fatal(r)
	}
	// 寫入類工具:ReadOnlyMessage(就算 org 合法、而且 boss 是 lab 的管理員),而且沒有寫入
	for _, tool := range []string{"issue_create", "issue_update", "doc_save", "agent_upsert", "agent_task_claim", "channel_post", "issue_accept", "project_save", "channel_messages"} {
		r := callRes(t, ro, tool, map[string]any{"org": "lab", "title": "唯讀不該寫入"})
		if !r.isErr || !strings.Contains(r.all(), mcp.ReadOnlyMessage) {
			t.Fatalf("%s 要被唯讀擋掉:%v", tool, r)
		}
	}
	if r := callRes(t, u.unified, "issues", map[string]any{"org": "lab"}); r.isErr || strings.Contains(r.all(), "唯讀不該寫入") {
		t.Fatal("唯讀連線不能建立 Issue", r)
	}
	// flowchart_render 只能畫,不能存
	if r := callRes(t, ro, "flowchart_render", map[string]any{"org": "lab", "save": true, "mermaid": "graph TD; A-->B"}); !r.isErr || !strings.Contains(r.all(), mcp.ReadOnlyMessage) {
		t.Fatal(r)
	}
	// 仍然每次都要 org;非成員的組織被拒(同一句,不洩漏)
	r := callRes(t, ro, "issues", map[string]any{})
	if !r.isErr || !strings.Contains(r.all(), "org") {
		t.Fatal(r)
	}
	for _, org := range []string{"other", "nope"} {
		r := callRes(t, ro, "issues", map[string]any{"org": org})
		if !r.isErr || !strings.Contains(r.all(), "You do not have access to organization "+org) || strings.Contains(r.all(), "別人的組織") {
			t.Fatalf("%s:%v", org, r)
		}
	}
	// 成員資格是即時的:退出 lab2 後,同一個唯讀連線立刻看不到 lab2
	if r := callRes(t, ro, "issues", map[string]any{"org": "lab2"}); r.isErr {
		t.Fatal(r)
	}
	if err := u.svc.Store.RemoveMember("lab2", "u1"); err != nil {
		t.Fatal(err)
	}
	if r := callRes(t, ro, "issues", map[string]any{"org": "lab2"}); !r.isErr {
		t.Fatal("退出組織後要即時失效", r)
	}
}

func TestUnifiedReadOnlyToolList(t *testing.T) {
	u := newUni(t)
	list := func(h http.Handler) map[string]bool {
		w := rpcPost(h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, "")
		var m struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || w.Code != 200 {
			t.Fatal(w.Code, err)
		}
		out := map[string]bool{}
		for _, x := range m.Result.Tools {
			out[x.Name] = true
		}
		return out
	}
	ro, rw := list(u.readOnlyEndpoint()), list(u.unified)
	if len(ro) == 0 || len(ro) >= len(rw) {
		t.Fatal(len(ro), len(rw))
	}
	for n := range ro {
		if !mcp.ReadOnlyTools[n] {
			t.Errorf("唯讀連線列出了非唯讀工具 %s", n)
		}
	}
	if !rw["issue_create"] || ro["issue_create"] {
		t.Fatal("omx_ 看得到寫入工具,omo_ 看不到")
	}
}

// omx_(沒有 ReadOnly 的連線)的行為不變:寫入照常
func TestUnifiedWriteUnchangedForUserToken(t *testing.T) {
	u := newUni(t)
	if r := callRes(t, u.unified, "issue_create", map[string]any{"org": "lab", "title": "寫入"}); r.isErr {
		t.Fatal(r)
	}
}

func TestUnifiedUnauthorizedChallenge(t *testing.T) {
	deny := func(rm string) http.Handler {
		return mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{ResourceMetadata: rm,
			Resolve: func(*http.Request) (backend.Backend, error) { return nil, http.ErrNoCookie }})
	}
	const rm = "https://omixor.test/.well-known/oauth-protected-resource/mcp"
	h := deny(rm)
	// 沒帶 token:401,resource_metadata,沒有 invalid_token;說明是英文
	w := rpcPost(h, rpcReq(1, "orgs", nil), "")
	want := `Bearer realm="omixor", resource_metadata="` + rm + `"`
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != want || w.Body.String() != mcp.UnifiedUnauthorizedMessage ||
		!strings.Contains(w.Body.String(), "omx_ user token or an OAuth access token") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	// omx_ 被拒:同上(不加 invalid_token)
	if w := rpcPost(h, rpcReq(1, "orgs", nil), "Bearer omx_x"); w.Code != 401 || w.Header().Get("WWW-Authenticate") != want {
		t.Fatal(w.Code, w.Header())
	}
	// omo_ 被拒:加 error="invalid_token"
	w = rpcPost(h, rpcReq(1, "orgs", nil), "Bearer omo_expired")
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != want+`, error="invalid_token"` {
		t.Fatal(w.Code, w.Header())
	}
	// 沒設 ResourceMetadata(沒有 OAuth):不帶
	if w := rpcPost(deny(""), rpcReq(1, "orgs", nil), ""); w.Header().Get("WWW-Authenticate") != `Bearer realm="omixor"` {
		t.Fatal(w.Header())
	}
}
