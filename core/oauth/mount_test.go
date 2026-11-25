package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// MCPMount:兩個端點都掛上、認證成功後呼叫 NewBackend、每個帶 id 的請求呼叫 Begin(及它回傳的結束函式);沒有 OAuth 時 omo_ 回 403
func TestMCPMountHooks(t *testing.T) {
	e := newProviderEnv(t)
	_ = e.svc.Store.CreateToken(model.Token{ID: "t1", UserID: e.user.ID, Device: "x", CreatedAt: model.Now(), Hash: service.HashToken("omx_t1")})
	var backends, begun, ended int
	m := &MCPMount{Svc: e.svc, Public: pPublic,
		NewBackend: func(p *model.Principal) backend.Backend { backends++; return &backend.Local{Svc: e.svc, P: p} },
		Begin:      func() func() { begun++; return func() { ended++ } },
	}
	mux := http.NewServeMux()
	m.Mount(mux)
	const rpc = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for _, path := range []string{"/mcp", "/mcp/org/o"} {
		r, _ := http.NewRequest("POST", path, strings.NewReader(rpc))
		r.Header.Set("Authorization", "Bearer omx_t1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s:狀態 %d", path, w.Code)
		}
	}
	if backends != 2 || begun != 2 || ended != 2 {
		t.Fatalf("backends=%d begin=%d end=%d", backends, begun, ended)
	}
	// 沒有 OAuth:omo_ token 一律 403(統一入口帶說明)
	for _, path := range []string{"/mcp", "/mcp/org/o"} {
		r, _ := http.NewRequest("POST", path, strings.NewReader(rpc))
		r.Header.Set("Authorization", "Bearer omo_x")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s:omo_ 沒有 OAuth 要 403,實際 %d", path, w.Code)
		}
	}
}

// 四種端點都掛上;Reject 擋掉的使用者在組織端點、統一入口、執行者端點和無效 token 一樣 401;角色端點只收角色憑證
func TestMCPMountEndpointsAndReject(t *testing.T) {
	e := newProviderEnv(t)
	_ = e.svc.Store.CreateToken(model.Token{ID: "t1", UserID: e.user.ID, Device: "x", CreatedAt: model.Now(), Hash: service.HashToken("omx_t1")})
	const rpc = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	post := func(m *MCPMount, path, auth string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		m.Mount(mux)
		r := httptest.NewRequest("POST", path, strings.NewReader(rpc))
		r.Header.Set("Content-Type", "application/json")
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	open := &MCPMount{Svc: e.svc, Public: pPublic}
	denied := &MCPMount{Svc: e.svc, Public: pPublic, Reject: func(p *model.Principal) bool { return p.User.ID == e.user.ID }}
	for _, path := range []string{"/mcp", "/mcp/org/o", "/mcp/worker/o"} {
		if w := post(open, path, "Bearer omx_t1"); w.Code != 200 {
			t.Errorf("%s:omx_ 要 200,實際 %d", path, w.Code)
		}
		bad, got := post(open, path, "Bearer omx_nope"), post(denied, path, "Bearer omx_t1")
		if bad.Code != 401 || got.Code != 401 || got.Body.String() != bad.Body.String() || got.Header().Get("WWW-Authenticate") != bad.Header().Get("WWW-Authenticate") {
			t.Errorf("%s:被 Reject 的要和無效 token 一樣 401:%d / %d", path, got.Code, bad.Code)
		}
	}
	if w := post(open, "/mcp/agent", "Bearer omx_t1"); w.Code != 401 {
		t.Errorf("角色端點不收使用者 token:%d", w.Code)
	}
}
