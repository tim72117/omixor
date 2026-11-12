package omixor

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// loginLimiter 登入端點的頻率限制(公開、不用登入,每次都會問 evoleap 並寫操作紀錄):
// 每個來源 IP 每分鐘最多 perIP 次,整個服務每分鐘最多 total 次。只在記憶體(服務只有一台)。
type loginLimiter struct {
	mu     sync.Mutex
	perIP  int
	total  int
	window time.Time
	count  map[string]int
	all    int
}

func newLoginLimiter(perIP, total int) *loginLimiter {
	return &loginLimiter{perIP: perIP, total: total, count: map[string]int{}}
}

func (l *loginLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= time.Minute {
		l.window, l.count, l.all = now, map[string]int{}, 0
	}
	if l.count[ip] >= l.perIP || l.all >= l.total {
		return false
	}
	l.count[ip]++
	l.all++
	return true
}

// clientIP Cloud Run 前面有 Google 的前端:真正的來源是 X-Forwarded-For 的第一個
func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}
