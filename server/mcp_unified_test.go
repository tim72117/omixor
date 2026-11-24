package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/service"
)

// 統一入口 /mcp 的路由與認證(走 HandlerWith,和 OAuth 一起掛):omx_ 使用者 token + org 參數;舊的 /mcp/org/{org} 不變
func TestUnifiedMCPRoute(t *testing.T) {
	o := newOAHTTP(t)
	post := func(path, body string, hdr ...string) *httptest.ResponseRecorder {
		return o.do("POST", path, body, append([]string{"Content-Type", "application/json"}, hdr...)...)
	}
	auth := []string{"Authorization", "Bearer omx_mac"}
	call := func(args string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"issues","arguments":` + args + `}}`
	}
	// 沒 token:401
	if w := post("/mcp", call(`{"org":"o"}`)); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	// 錯的 omx_ token:401
	if w := post("/mcp", call(`{"org":"o"}`), "Authorization", "Bearer omx_nope"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	// 亂的 omo_(OAuth 有啟用):401 invalid_token(統一入口收 OAuth,見 mcp_unified_oauth_test.go)
	if w := post("/mcp", call(`{"org":"o"}`), "Authorization", "Bearer omo_whatever"); w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	// 帶 org:成功,結果的 _meta 附 org
	w := post("/mcp", call(`{"org":"o"}`), auth...)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) || !strings.Contains(w.Body.String(), `"_meta":{"omixor/org":"o"}`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// 沒帶 org / 非成員的組織:isError
	for _, args := range []string{`{}`, `{"org":"nope"}`} {
		if w := post("/mcp", call(args), auth...); w.Code != 200 || !strings.Contains(w.Body.String(), `"isError":true`) {
			t.Fatal(args, w.Code, w.Body.String())
		}
	}
	// 規格:issues 有必填 org
	w = post("/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, auth...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"required":["org"`) {
		t.Fatal(w.Code, w.Body.String()[:300])
	}
	// 舊端點:不帶 org 照常;帶不同的 org 拒絕
	if w := post("/mcp/org/o", call(`{}`), auth...); w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := post("/mcp/org/o", call(`{"org":"x"}`), auth...); w.Code != 200 || !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// 沒設 OAuth(沒有對外網址)時一樣有 /mcp
	h := Handler(&service.Service{Store: o.svc.Store})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
}
