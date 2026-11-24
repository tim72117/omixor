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

// MountUnified 只掛統一入口:組織端點不存在(宿主不開放時,例如 console 的 OAuth 連接埠)
func TestMountUnifiedOnly(t *testing.T) {
	e := newProviderEnv(t)
	mux := http.NewServeMux()
	(&MCPMount{Svc: e.svc, Public: pPublic}).MountUnified(mux)
	for path, want := range map[string]int{"/mcp": 401, "/mcp/org/o": 404} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{}`)))
		if w.Code != want {
			t.Errorf("%s:要 %d,實際 %d", path, want, w.Code)
		}
	}
}

// OAuthOnly:omx_ 使用者 token 在統一入口和沒帶 token 一樣 401(不透露 token 是否有效);預設(false)照收
func TestMCPMountOAuthOnly(t *testing.T) {
	e := newProviderEnv(t)
	_ = e.svc.Store.CreateToken(model.Token{ID: "t1", UserID: e.user.ID, Device: "x", CreatedAt: model.Now(), Hash: service.HashToken("omx_t1")})
	const rpc = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	post := func(m *MCPMount, auth string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		m.MountUnified(mux)
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(rpc))
		r.Header.Set("Content-Type", "application/json")
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	open := &MCPMount{Svc: e.svc, Public: pPublic}
	if w := post(open, "Bearer omx_t1"); w.Code != 200 {
		t.Fatalf("預設要收 omx_:%d", w.Code)
	}
	only := &MCPMount{Svc: e.svc, Public: pPublic, OAuthOnly: true}
	user, none := post(only, "Bearer omx_t1"), post(only, "")
	if user.Code != 401 || user.Code != none.Code || user.Header().Get("WWW-Authenticate") != none.Header().Get("WWW-Authenticate") || user.Body.String() != none.Body.String() {
		t.Fatalf("OAuthOnly 的 omx_ 要和沒帶 token 一樣:%d / %d", user.Code, none.Code)
	}
}
