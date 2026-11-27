package mcp

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// 共用的 HTTP 輔助:
//   - BearerToken 取出 Authorization: Bearer <權杖>(Bearer 不分大小寫,後面要有空白)
//   - HostGuard 本機名稱的 Host 白名單(防 DNS rebinding;不符回 403)

// BearerToken 取出 Authorization 標頭裡的 Bearer 權杖;格式不對回傳 ("", false)
func BearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(auth[len(prefix):])
	return tok, tok != ""
}

// RemoteHost 連線來源位址(只看 RemoteAddr,不信任 X-Forwarded-For)
func RemoteHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// HostAllowed Host 標頭(可含連接埠)是不是本機名稱(localhost、127.0.0.1、[::1]),或是 extra(例如對外網址的主機名稱;空字串或 0.0.0.0、:: 不算)
func HostAllowed(hostHeader, extra string) bool {
	h := hostHeader
	if host, port, err := net.SplitHostPort(hostHeader); err == nil {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return false
		}
		h = host
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	} else if strings.Contains(h, ":") && net.ParseIP(h) == nil {
		return false // 有冒號卻不是「主機:埠」也不是 IPv6 位址
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	extra = strings.ToLower(strings.Trim(strings.TrimSpace(extra), "[]"))
	switch extra {
	case "", "0.0.0.0", "::":
		return false
	}
	return h == extra
}

// HostGuard Host 白名單:不符回 403;extra 見 HostAllowed
func HostGuard(extra string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !HostAllowed(r.Host, extra) {
			w.Header().Set("Connection", "close")
			text(w, 403, "Forbidden host")
			return
		}
		next.ServeHTTP(w, r)
	})
}
