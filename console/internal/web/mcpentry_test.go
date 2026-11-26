package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// MCP 入口的路徑交給 entry,畫面自己的路徑(含 /api)照舊
func TestWithMCPEntryRouting(t *testing.T) {
	status := func(code int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
	}
	h := WithMCPEntry(status(201), status(202))
	for path, want := range map[string]int{
		"/mcp": 201, "/mcp/org/o": 201, "/mcp/agent": 201, "/oauth/authorize": 201, "/oauth/ldap/confirm": 201,
		"/.well-known/oauth-protected-resource/mcp": 201, "/.well-known/oauth-authorization-server/oauth": 201,
		"/": 202, "/api/tool/list": 202, "/mcpx": 202, "/oauth": 202, "/office/": 202,
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Errorf("%s:%d,要 %d", path, w.Code, want)
		}
	}
}
