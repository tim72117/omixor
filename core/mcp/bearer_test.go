package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBearerToken(t *testing.T) {
	cases := []struct {
		h    string
		want string
		ok   bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"Bearer  abc  ", "abc", true},
		{"Bearerabc", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Bearer    ", "", false},
		{"Basic abc", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/mcp", nil)
		if c.h != "" {
			r.Header.Set("Authorization", c.h)
		}
		got, ok := BearerToken(r)
		if got != c.want || ok != c.ok {
			t.Errorf("%q:得到 (%q,%v),要 (%q,%v)", c.h, got, ok, c.want, c.ok)
		}
	}
}

func TestTokenEqual(t *testing.T) {
	if !TokenEqual("omxm_abc", "omxm_abc") {
		t.Error("相同的要相等")
	}
	for _, p := range [][2]string{{"omxm_abc", "omxm_abd"}, {"omxm_abc", ""}, {"", "omxm_abc"}, {"", ""}, {"omxm_abc", "omxm_abcd"}} {
		if TokenEqual(p[0], p[1]) {
			t.Errorf("%q 與 %q 不該相等", p[0], p[1])
		}
	}
}

func TestFailLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := &FailLimiter{Max: 3, Window: time.Minute, Now: func() time.Time { return now }}
	for i := 1; i <= 3; i++ {
		if l.Blocked("a") {
			t.Fatalf("第 %d 次前不該擋", i)
		}
		l.Failed("a")
	}
	if l.Blocked("a") {
		t.Fatal("剛好 Max 次還不擋(超過才擋)")
	}
	l.Failed("a")
	if !l.Blocked("a") {
		t.Fatal("超過 Max 要擋")
	}
	if l.Blocked("b") {
		t.Fatal("別的來源不受影響")
	}
	now = now.Add(61 * time.Second)
	if l.Blocked("a") {
		t.Fatal("視窗過了要解除")
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
}

func TestRequireBearer(t *testing.T) {
	lim := &FailLimiter{Max: 5}
	h := RequireBearer("omixor", lim, func(tok string) bool { return TokenEqual(tok, "omxm_secret-token-123456") }, okHandler())
	do := func(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r.RemoteAddr = "127.0.0.1:5555"
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	good := map[string]string{"Authorization": "Bearer omxm_secret-token-123456"}
	if w := do("POST", "/mcp", good); w.Code != 204 {
		t.Fatalf("正確權杖要放行:%d", w.Code)
	}
	if w := do("POST", "/mcp", map[string]string{"Authorization": "bearer omxm_secret-token-123456"}); w.Code != 204 {
		t.Fatalf("bearer 小寫要放行:%d", w.Code)
	}
	bad := []map[string]string{
		nil,
		{"Authorization": "Bearer "},
		{"Authorization": "Bearer omxm_wrong"},
		{"Authorization": "Bearer omx_secret-token-123456"},
		{"Authorization": "Beareromxm_secret-token-123456"},
		{"Authorization": "Basic omxm_secret-token-123456"},
	}
	for _, m := range []string{"POST", "GET", "DELETE", "OPTIONS", "PUT"} {
		for _, p := range []string{"/mcp", "/mcp/worker", "/", "/anything"} {
			lim.fails = nil // 這個迴圈只看 401,不要被限流影響
			for _, hdr := range bad {
				w := do(m, p, hdr)
				if w.Code != 401 {
					t.Errorf("%s %s %v:要 401,得到 %d", m, p, hdr, w.Code)
				}
				if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="omixor"` {
					t.Errorf("WWW-Authenticate:%q", got)
				}
				if strings.Contains(w.Body.String(), "omxm_") {
					t.Errorf("回應不該含權杖:%s", w.Body.String())
				}
			}
		}
	}
	// 失敗太多次:429(只擋錯誤的嘗試;正確權杖照樣放行)
	lim.fails = nil
	for i := 0; i < 6; i++ {
		do("POST", "/mcp", nil)
	}
	if w := do("POST", "/mcp", nil); w.Code != 429 {
		t.Fatalf("要 429:%d", w.Code)
	}
	if w := do("POST", "/mcp", map[string]string{"Authorization": "Bearer omxm_wrong"}); w.Code != 429 {
		t.Fatalf("被擋的來源再送錯誤權杖仍 429:%d", w.Code)
	}
	if w := do("POST", "/mcp", good); w.Code != 204 {
		t.Fatalf("被擋的來源帶正確權杖要放行:%d", w.Code)
	}
}

func TestRequireBearerOrigin(t *testing.T) {
	lim := &FailLimiter{}
	h := RequireBearer("omixor", lim, func(string) bool { return true }, okHandler())
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Authorization", "Bearer whatever-long-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("外部 Origin 要 403:%d", w.Code)
	}
	r = httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Origin", "http://localhost:5191")
	r.Header.Set("Authorization", "Bearer whatever-long-token")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("本機 Origin 帶權杖放行:%d", w.Code)
	}
}

// 別人灌錯誤嘗試把來源擋住時,帶正確權杖的請求不受影響(限流只拖慢錯誤)
func TestRequireBearerCorrectTokenNeverLimited(t *testing.T) {
	lim := &FailLimiter{Max: 2}
	h := RequireBearer("omixor", lim, func(tok string) bool { return TokenEqual(tok, "omxm_secret-token-123456") }, okHandler())
	do := func(auth string) int {
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 50; i++ {
		do("Bearer bad")
		if got := do("Bearer omxm_secret-token-123456"); got != 204 {
			t.Fatalf("第 %d 輪:正確權杖要放行,得到 %d", i, got)
		}
	}
	if got := do("Bearer bad"); got != 429 {
		t.Fatalf("錯誤的要 429:%d", got)
	}
	if lim.fails["127.0.0.1"].n > 60 {
		t.Error("正確權杖不該計失敗")
	}
}

func TestFailLimiterBounded(t *testing.T) {
	now := time.Unix(1000, 0)
	l := &FailLimiter{Max: 3, MaxKeys: 5, Now: func() time.Time { return now }}
	for i := 0; i < 4; i++ {
		l.Failed("victim")
	}
	if !l.Blocked("victim") {
		t.Fatal("victim 該被擋")
	}
	// 其他來源灌進來:不能整張清空(victim 是最舊的會先被淘汰,但每次只淘汰一個,大小不超過上限)
	for i := 0; i < 3; i++ {
		now = now.Add(time.Second)
		l.Failed("k" + string(rune('a'+i)))
	}
	if len(l.fails) != 4 || !l.Blocked("victim") {
		t.Fatalf("未滿上限不該淘汰:len=%d", len(l.fails))
	}
	for i := 0; i < 100; i++ {
		now = now.Add(time.Second)
		l.Failed("x" + string(rune(1000+i)))
		if len(l.fails) > 5 {
			t.Fatalf("超過上限:%d", len(l.fails))
		}
	}
	// 最近的來源仍在,不是整張被清空
	if len(l.fails) != 5 {
		t.Fatalf("要維持滿載而不是被清空:%d", len(l.fails))
	}
	if l.fails["x"+string(rune(1000+99))] == nil {
		t.Fatal("最新的來源要在")
	}
}

func TestHostAllowed(t *testing.T) {
	ok := []struct{ h, extra string }{
		{"localhost", ""}, {"localhost:3765", ""}, {"LOCALHOST:3765", ""}, {"127.0.0.1", ""}, {"127.0.0.1:3765", ""},
		{"[::1]", ""}, {"[::1]:3765", ""}, {"::1", ""}, {"localhost.:3765", ""},
		{"10.0.0.5:3765", "10.0.0.5"}, {"mcp.lan", "mcp.lan"}, {"[fd00::5]:3765", "fd00::5"},
		{"localhost:3765", "0.0.0.0"},
	}
	for _, c := range ok {
		if !HostAllowed(c.h, c.extra) {
			t.Errorf("%q(extra=%q)該放行", c.h, c.extra)
		}
	}
	bad := []struct{ h, extra string }{
		{"", ""}, {"evil.example", ""}, {"evil.example:3765", ""}, {"localhost.evil.example", ""}, {"127.0.0.1.evil.example:3765", ""},
		{"localhost:abc", ""}, {"localhost:99999", ""}, {"localhost:80:80", ""}, {"0.0.0.0:3765", "0.0.0.0"}, {"10.0.0.5", ""},
		{"10.0.0.6:3765", "10.0.0.5"}, {"evil.example", "::"},
	}
	for _, c := range bad {
		if HostAllowed(c.h, c.extra) {
			t.Errorf("%q(extra=%q)不該放行", c.h, c.extra)
		}
	}
}

func TestHostGuardDoesNotCountFailures(t *testing.T) {
	lim := &FailLimiter{Max: 2}
	h := HostGuard("", RequireBearer("omixor", lim, func(string) bool { return false }, okHandler()))
	for i := 0; i < 20; i++ {
		r := httptest.NewRequest("POST", "http://evil.example/mcp", nil)
		r.RemoteAddr = "127.0.0.1:1"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("Host 不符要 403:%d", w.Code)
		}
	}
	if len(lim.fails) != 0 {
		t.Fatal("Host 不符不計驗證失敗")
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:3765/mcp", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("本機 Host 沒權杖要 401:%d", w.Code)
	}
}
