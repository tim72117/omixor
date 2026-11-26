package web

import (
	"net/http"
	"strings"
)

// WithMCPEntry 在畫面連接埠加上 MCP 入口(mcp.WebEntry,和 omixor server 同一套):這些路徑交給 entry,其餘照舊給畫面。
// 不能經過畫面的 guard:OAuth 表單送出的 Origin 是 "null"、用戶端的請求也不是 /api 那套規則;
// entry 自己擋 Host(對外網址與本機名稱)、跨站送出與認證,而且不認畫面的 session cookie
func WithMCPEntry(entry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMCPPath(r.URL.Path) {
			entry.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMCPPath(p string) bool {
	return p == "/mcp" || strings.HasPrefix(p, "/mcp/") || strings.HasPrefix(p, "/oauth/") ||
		strings.HasPrefix(p, "/.well-known/oauth-")
}
