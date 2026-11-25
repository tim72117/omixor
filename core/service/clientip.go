// clientip.go 取得請求的來源 IP(給頻率限制、稽核用)。
// X-Forwarded-For 是呼叫者自己能填的,只有「我們信任的代理」加上去的那幾段才可信;所以預設完全不看,要設信任的代理層數才用。

package service

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
)

var trustedProxyHops atomic.Int32

// SetTrustedProxyHops 設定信任的代理層數(伺服器啟動時呼叫一次;負數當 0)。
// 0 = 不看 X-Forwarded-For,用連線的位址;N = 取 X-Forwarded-For 由右數第 N 個(見 ClientIPFrom)
func SetTrustedProxyHops(n int) {
	if n < 0 {
		n = 0
	}
	trustedProxyHops.Store(int32(n))
}

// ClientIP 請求的來源 IP,規則見 ClientIPFrom
func ClientIP(r *http.Request) string {
	return ClientIPFrom(r.RemoteAddr, strings.Join(r.Header.Values("X-Forwarded-For"), ","), int(trustedProxyHops.Load()))
}

// ClientIPFrom 純函式:由連線位址、X-Forwarded-For 與信任的代理層數算出來源 IP。
//   - hops <= 0:完全不看 xff,用 remoteAddr 的主機部分
//   - hops = N > 0:取 xff 由右數第 N 個項目(最右邊是離我們最近的代理加的,左邊的都可能是偽造的)
//   - 項目不足 N 個、或該項目不是合法的 IPv4 / IPv6,一律退回 remoteAddr
func ClientIPFrom(remoteAddr, xff string, hops int) string {
	if hops > 0 && strings.TrimSpace(xff) != "" {
		items := strings.Split(xff, ",")
		if len(items) >= hops {
			if a, ok := parseXFFItem(strings.TrimSpace(items[len(items)-hops])); ok {
				return a.Unmap().String()
			}
		}
	}
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return h
	}
	return remoteAddr
}

// parseXFFItem 解析 X-Forwarded-For 的一個項目:純位址、帶埠號(1.2.3.4:80、[::1]:80)、或方括號的 IPv6([::1])
func parseXFFItem(s string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(s); err == nil {
		return a, true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	if len(s) > 2 && s[0] == '[' && s[len(s)-1] == ']' {
		if a, err := netip.ParseAddr(s[1 : len(s)-1]); err == nil {
			return a, true
		}
	}
	return netip.Addr{}, false
}

// LimitKey 同 limitKey,給需要在鍵上加前綴的宿主用:要先聚合再加前綴(加了前綴的字串解析不出位址,不會聚合)
func LimitKey(ip string) string { return limitKey(ip) }

// limitKey 頻率限制與失敗計數用的鍵:IPv6 取 /64 前綴(一般使用者至少拿到 /64,逐一換位址不能繞過限制),IPv4 原樣,
// IPv4-mapped IPv6 先轉成 IPv4,不是位址的字串原樣回傳。稽核、log、ip_hash 仍用 ClientIP 的真實位址,不要用這個
func limitKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.WithZone("").Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}
