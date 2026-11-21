package mcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 共用的 Bearer 權杖驗證輔助(本機 console 的 /mcp、/mcp/worker 用):
//   - BearerToken 取出 Authorization: Bearer <權杖>(Bearer 不分大小寫,後面要有空白)
//   - TokenEqual 常數時間比對(先雜湊成等長再比,不洩漏長度與內容)
//   - FailLimiter 同一來源「驗證失敗」太多次回 429(同角色端點:一分鐘內超過 10 次);只拖慢錯誤的嘗試,帶正確權杖的請求永遠放行
//   - HostGuard 本機連接埠的 Host 白名單(防 DNS rebinding;不符回 403,不算驗證失敗)
//   - RequireBearer 把它們串起來:失敗一律 401(WWW-Authenticate,內容不洩漏權杖存不存在),太多次 429

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

// TokenEqual 常數時間比對兩個權杖(任一邊空字串一律不相等)
func TokenEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// FailLimiter 依來源計驗證失敗次數:Window 內超過 Max 次就擋(Window 到了重算)
type FailLimiter struct {
	Max    int           // 0 = 10
	Window time.Duration // 0 = 1 分鐘
	Now    func() time.Time
	// MaxKeys 同時追蹤的來源上限,0 = 1000;滿了先清掉過期的,還是滿就淘汰最舊的(不整張清空,否則攻擊者灌來源就能洗掉自己的失敗紀錄)
	MaxKeys int

	mu    sync.Mutex
	fails map[string]*failEntry
}

type failEntry struct {
	n     int
	reset time.Time
}

func (l *FailLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *FailLimiter) max() int {
	if l.Max > 0 {
		return l.Max
	}
	return 10
}

func (l *FailLimiter) window() time.Duration {
	if l.Window > 0 {
		return l.Window
	}
	return time.Minute
}

// Blocked 這個來源目前被擋嗎
func (l *FailLimiter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[key]
	return f != nil && l.now().Before(f.reset) && f.n > l.max()
}

// Failed 記一次失敗,回傳這個視窗內第幾次
func (l *FailLimiter) Failed(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.fails == nil {
		l.fails = map[string]*failEntry{}
	}
	f := l.fails[key]
	if f == nil || !now.Before(f.reset) {
		if f == nil {
			l.makeRoom(now)
		}
		f = &failEntry{reset: now.Add(l.window())}
		l.fails[key] = f
	}
	f.n++
	return f.n
}

func (l *FailLimiter) maxKeys() int {
	if l.MaxKeys > 0 {
		return l.MaxKeys
	}
	return 1000
}

// makeRoom 新來源要進來前:滿了就先清過期的,仍滿就淘汰最舊(reset 最早)的;呼叫時要拿著 l.mu
func (l *FailLimiter) makeRoom(now time.Time) {
	if len(l.fails) < l.maxKeys() {
		return
	}
	for k, f := range l.fails {
		if !now.Before(f.reset) {
			delete(l.fails, k)
		}
	}
	for len(l.fails) >= l.maxKeys() {
		oldest, first := "", true
		var at time.Time
		for k, f := range l.fails {
			if first || f.reset.Before(at) {
				oldest, at, first = k, f.reset, false
			}
		}
		delete(l.fails, oldest)
	}
}

// RemoteHost 連線來源位址(只看 RemoteAddr,不信任 X-Forwarded-For:本機連接埠前面沒有代理,信了反而讓人偽造來源躲掉限流)
func RemoteHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// RequireBearer 包住 next:要帶 Authorization: Bearer <權杖> 且 verify 通過才放行。
//   - 先驗證:帶正確權杖的請求永遠放行,不看失敗計數(限流只拖慢錯誤的嘗試,不能被別人灌錯誤擋掉本人)
//   - 沒帶、格式不對、權杖不符:來源已被擋(失敗太多次)回 429,否則計一次失敗回 401(含 WWW-Authenticate: Bearer realm="<realm>",內容不洩漏權杖存不存在)
//   - 帶 Origin 但不是本機來源的請求先回 403(防 DNS rebinding,同 HTTPHandler;不算驗證失敗)
//   - 任何路徑、任何 HTTP 方法(含 OPTIONS)都要驗證,沒有例外
func RequireBearer(realm string, lim *FailLimiter, verify func(token string) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !IsLocalOrigin(origin) {
			w.Header().Set("Connection", "close")
			text(w, 403, "Forbidden origin")
			return
		}
		key := RemoteHost(r)
		if tok, ok := BearerToken(r); ok && verify(tok) {
			next.ServeHTTP(w, r)
			return
		}
		if lim.Blocked(key) {
			w.Header().Set("Connection", "close")
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too many failed attempts", 429)
			return
		}
		lim.Failed(key)
		unauthorized(w, realm)
	})
}

// HostAllowed Host 標頭(可含連接埠)是不是本機名稱(localhost、127.0.0.1、[::1]),或是 extra(例如 OMIXOR_MCP_ADDR 設的非本機位址;空字串或 0.0.0.0、:: 不算)
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

// HostGuard Host 白名單:不符回 403(不算驗證失敗,不碰 FailLimiter);extra 見 HostAllowed
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
