package core

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 登入時接收瀏覽器導回的臨時伺服器(同 Swift 的 LoopbackCallback):只綁 127.0.0.1,收到一次符合的回呼就結束。
// OAuth 用 state 比對、瀏覽器核准用 id 比對;回呼帶 error 時直接回報錯誤。

// LoginTimeout 等回呼多久(測試可以改短)
var LoginTimeout = 600 * time.Second

type loopback struct {
	ln   net.Listener
	srv  *http.Server
	Port int

	mu          sync.Mutex
	expectName  string
	expectValue string
	done        chan struct{}
	code        string
	err         error
	finished    bool
}

func (l *loopback) RedirectURI() string { return fmt.Sprintf("http://127.0.0.1:%d/callback", l.Port) }

// startLoopback preferredPort 有給就先試那個埠,被占用就改用隨機埠
func startLoopback(preferredPort *int) (*loopback, error) {
	var ln net.Listener
	var err error
	if preferredPort != nil && *preferredPort > 0 {
		ln, err = net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *preferredPort))
	}
	if ln == nil {
		ln, err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
	}
	l := &loopback{ln: ln, Port: ln.Addr().(*net.TCPAddr).Port, done: make(chan struct{})}
	l.srv = &http.Server{Handler: http.HandlerFunc(l.handle), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 16 * 1024}
	go func() { _ = l.srv.Serve(ln) }()
	return l, nil
}

func (l *loopback) expect(name, value string) {
	l.mu.Lock()
	l.expectName, l.expectValue = name, value
	l.mu.Unlock()
}

func (l *loopback) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = l.srv.Shutdown(ctx)
}

func (l *loopback) finish(code string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return
	}
	l.finished = true
	l.code, l.err = code, err
	close(l.done)
}

// waitForCode 等回呼帶來的 code
func (l *loopback) waitForCode(timeout time.Duration) (string, error) {
	select {
	case <-l.done:
	case <-time.After(timeout):
		l.finish("", connErr(fmt.Sprintf("等太久了(%d 分鐘),請重新登入", int(timeout.Minutes()))))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.code, l.err
}

func (l *loopback) handle(w http.ResponseWriter, r *http.Request) {
	q := map[string]string{}
	// 同名參數取第一個
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			q[k] = vs[0]
		}
	}
	if r.URL.Path != "/callback" {
		loopbackRespond(w, 404, "找不到", "這不是登入回呼網址。")
		return
	}
	l.mu.Lock()
	name, value := l.expectName, l.expectValue
	l.mu.Unlock()
	if got, ok := q[name]; !ok || got != value {
		loopbackRespond(w, 400, "登入失敗", "回呼的驗證參數不符,請回到 Ops 重新登入。")
		return
	}
	if e, ok := q["error"]; ok {
		msg := e
		if d, ok := q["error_description"]; ok {
			msg = e + ":" + d
		}
		loopbackRespond(w, 400, "登入失敗", msg)
		l.finish("", connErr("授權被拒絕:"+msg))
		return
	}
	code := q["code"]
	if code == "" {
		loopbackRespond(w, 400, "登入失敗", "回呼沒有帶授權碼。")
		return
	}
	loopbackRespond(w, 200, "登入完成", "Ops 已完成登入,可以關掉這個分頁。")
	l.finish(code, nil)
}

func loopbackRespond(w http.ResponseWriter, status int, title, msg string) {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;").Replace
	html := `<!doctype html><meta charset="utf-8"><title>` + esc(title) + `</title><body style="font:16px -apple-system;padding:48px"><h2>` +
		esc(title) + `</h2><p>` + esc(msg) + `</p>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Connection", "close")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(html))
}
