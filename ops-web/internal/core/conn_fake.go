package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// FakeNet 測試用的假網路:所有連線請求(不管哪個網域)都送到本機的 httptest 伺服器(https 走 TLS 版),
// handler 依 r.Host 與路徑假裝成 GitHub、Google、evoleap…。同 Swift 測試的 StubProtocol。
type FakeNet struct {
	mu      sync.Mutex
	Log     []FakeRequest
	Handler func(r *http.Request, body []byte) (int, any)

	plain, tls *httptest.Server
	saved      http.RoundTripper
	strict     bool
}

type FakeRequest struct {
	Method string
	URL    string // scheme://host/path?query
	Body   []byte
	Auth   string
}

// NewFakeNet 換掉 ConnTransport;測試結束呼叫 Close。https 預設不驗證假伺服器的憑證
func NewFakeNet(h func(r *http.Request, body []byte) (int, any)) *FakeNet {
	return newFakeNet(h, nil)
}

// NewFakeNetTLS 假的 https 伺服器用 cert(例如測試裡產生的自簽憑證),而且照連線的 TLS 設定真的驗證憑證:
// 沒指定 CA 檔就用系統的憑證(自簽的會失敗),指定了 CA 檔或 Insecure 才連得上
func NewFakeNetTLS(h func(r *http.Request, body []byte) (int, any), cert tls.Certificate) *FakeNet {
	return newFakeNet(h, &cert)
}

func newFakeNet(h func(r *http.Request, body []byte) (int, any), cert *tls.Certificate) *FakeNet {
	f := &FakeNet{Handler: h, saved: ConnTransport, strict: cert != nil}
	serve := func(scheme string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.Log = append(f.Log, FakeRequest{Method: r.Method, URL: scheme + "://" + r.Host + r.URL.RequestURI(), Body: body, Auth: r.Header.Get("Authorization")})
			handler := f.Handler
			f.mu.Unlock()
			status, obj := 404, any(map[string]any{"error": "no handler"})
			if handler != nil {
				status, obj = handler(r, body)
			}
			if hb, ok := obj.(WithHeaders); ok {
				for k, v := range hb.Header {
					w.Header().Set(k, v)
				}
				obj = hb.Body
			}
			if loc, ok := obj.(Redirect); ok {
				http.Redirect(w, r, string(loc), status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if s, ok := obj.(RawBody); ok {
				_, _ = w.Write([]byte(s))
				return
			}
			b, _ := json.Marshal(obj)
			_, _ = w.Write(b)
		}
	}
	f.plain = httptest.NewServer(serve("http"))
	f.tls = httptest.NewUnstartedServer(serve("https"))
	if cert != nil {
		f.tls.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}}
	}
	f.tls.StartTLS()
	ConnTransport = &fakeTransport{f: f}
	return f
}

// WithHeaders handler 回傳這個型別時加上回應標頭(例如 GitLab 分頁的 X-Next-Page)
type WithHeaders struct {
	Header map[string]string
	Body   any
}

type fakeTransport struct {
	f   *FakeNet
	cfg *tls.Config
}

func (t *fakeTransport) WithTLS(cfg *tls.Config) http.RoundTripper {
	return &fakeTransport{f: t.f, cfg: cfg}
}

func (t *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	dial := func(addr string) func(ctx context.Context, network, _ string) (net.Conn, error) {
		return func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}
	}
	if r.URL.Scheme != "https" {
		return (&http.Transport{DialContext: dial(t.f.plain.Listener.Addr().String()), DisableKeepAlives: true}).RoundTrip(r)
	}
	cfg := &tls.Config{InsecureSkipVerify: true}
	if t.f.strict {
		cfg = &tls.Config{}
		if t.cfg != nil {
			cfg = t.cfg.Clone()
		}
	}
	return (&http.Transport{DialContext: dial(t.f.tls.Listener.Addr().String()), TLSClientConfig: cfg, DisableKeepAlives: true}).RoundTrip(r)
}

// Redirect handler 回傳這個型別時送 3xx 轉址
type Redirect string

// RawBody handler 回傳這個型別時原樣送出(不是 JSON 的內容)
type RawBody string

func (f *FakeNet) Close() {
	ConnTransport = f.saved
	f.plain.Close()
	f.tls.Close()
}

// Requests 目前為止的請求(副本)
func (f *FakeNet) Requests() []FakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeRequest(nil), f.Log...)
}

// Find 最後一個網址包含 s 的請求
func (f *FakeNet) Find(s string) (FakeRequest, bool) {
	reqs := f.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if strings.Contains(reqs[i].URL, s) {
			return reqs[i], true
		}
	}
	return FakeRequest{}, false
}
