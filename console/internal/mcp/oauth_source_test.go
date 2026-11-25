package mcp

import (
	"net/http"
	"testing"
)

// 對外連接埠的失敗限流鍵:IPv6 同一個 /64 要算同一個來源,IPv4 原樣,都帶 "remote:" 前綴(和本機連接埠分開計)
func TestRemoteAgentSource(t *testing.T) {
	key := func(addr string) string {
		return remoteAgentSource(&http.Request{RemoteAddr: addr, Header: http.Header{}})
	}
	if a, b := key("[2001:db8:1:2::1]:1000"), key("[2001:db8:1:2:ffff:ffff:ffff:ffff]:2000"); a != b || a != "remote:2001:db8:1:2::/64" {
		t.Errorf("同一個 /64:%q、%q", a, b)
	}
	if a, b := key("[2001:db8:1:2::1]:1000"), key("[2001:db8:1:3::1]:1000"); a == b {
		t.Errorf("不同 /64 不該同一個鍵:%q", a)
	}
	if got := key("192.0.2.7:1234"); got != "remote:192.0.2.7" {
		t.Errorf("IPv4:%q", got)
	}
}
