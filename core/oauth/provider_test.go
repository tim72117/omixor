package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// provider 契約:假的 IdentityProvider 回 Done / Redirect / Retry / 錯誤,同意畫面的 POST 要走對應的回應;預設 provider 組合的同意畫面內容不變

const (
	pPublic   = "https://omixor.test"
	pCallback = "https://claude.ai/api/mcp/auth_callback"
	pVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ0123456789"
)

var pCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

type fakeProvider struct {
	start    func(r *http.Request, t Txn) (Outcome, error)
	callback http.Handler
	last     Txn
}

func (p *fakeProvider) ID() string    { return "fake" }
func (p *fakeProvider) Label() string { return "Fake" }
func (p *fakeProvider) Fields() []Field {
	return []Field{{Name: "secret", Kind: "password", Label: "Secret"}}
}
func (p *fakeProvider) FormActions() []string { return []string{"action=allow"} }
func (p *fakeProvider) Present() Presentation {
	return Presentation{Button: "Go", SoloButton: "Allow", Summary: "Use fake"}
}
func (p *fakeProvider) Callback() http.Handler { return p.callback }
func (p *fakeProvider) Start(w http.ResponseWriter, r *http.Request, t Txn) (Outcome, error) {
	p.last = t
	return p.start(r, t)
}

type providerEnv struct {
	t    *testing.T
	svc  *service.Service
	h    http.Handler
	cid  string
	user *model.User
	// cookie 同意畫面的綁定 cookie(像瀏覽器一樣帶回去)
	cookie string
}

func newProviderEnv(t *testing.T, ps ...IdentityProvider) *providerEnv {
	t.Helper()
	st := storetest.New(t)
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	svc := &service.Service{Store: st, OAuthRegPerIP: 1000, OAuthRegTotal: 1000, Now: func() time.Time { return now }}
	u := model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: model.Now()}
	if err := st.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	e := &providerEnv{t: t, svc: svc, user: &u}
	if ps != nil {
		mux := http.NewServeMux()
		NewWith(Config{Svc: svc, Public: pPublic, Providers: ps}).Mount(mux)
		e.h = mux
	}
	cl, err := svc.RegisterOAuthClient(service.OAuthClientInput{ClientName: "Claude", RedirectURIs: []any{pCallback},
		GrantTypes: []any{"authorization_code"}, ResponseTypes: []any{"code"}, TokenEndpointAuthMethod: "none"}, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	e.cid = cl.ID
	return e
}

func (e *providerEnv) serve(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if e.cookie != "" {
		r.Header.Set("Cookie", e.cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == consentCookie {
			e.cookie = c.Name + "=" + c.Value
		}
	}
	return w
}

func (e *providerEnv) authForm(csrf string, extra url.Values) url.Values {
	sum := sha256.Sum256([]byte(pVerifier))
	f := url.Values{"csrf": {csrf}, "client_id": {e.cid}, "redirect_uri": {pCallback}, "response_type": {"code"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "state": {"st-1"},
		"resource": {pPublic + "/mcp"}, "scope": {"mcp:read"}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

// consent 載入同意畫面,回傳 csrf 與回應
func (e *providerEnv) consent() (string, *httptest.ResponseRecorder) {
	e.t.Helper()
	q := e.authForm("", nil)
	q.Del("csrf")
	w := e.serve(e.h, "GET", "/oauth/authorize?"+q.Encode(), "")
	m := pCSRF.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil {
		e.t.Fatalf("同意畫面:%d %s", w.Code, w.Body.String())
	}
	return m[1], w
}

func (e *providerEnv) submit(extra url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	csrf, _ := e.consent()
	return e.serve(e.h, "POST", "/oauth/authorize", e.authForm(csrf, extra).Encode())
}

func TestProviderDone(t *testing.T) {
	fp := &fakeProvider{}
	e := newProviderEnv(t, fp)
	fp.start = func(r *http.Request, tx Txn) (Outcome, error) {
		return Outcome{Done: &Identity{User: e.user, Extra: map[string]any{"via": "fake"}}}, nil
	}
	w := e.submit(url.Values{"action": {"allow"}, "secret": {"x"}, "grant": {"read"}})
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || loc == nil || !strings.HasPrefix(w.Header().Get("Location"), pCallback+"?") || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st-1" {
		t.Fatalf("Done 要簽發授權碼並導回:%d %s", w.Code, w.Header().Get("Location"))
	}
	if fp.last.Grant != service.OAuthGrantRead || fp.last.Form.Get("secret") != "x" || fp.last.Checked == nil || fp.last.Req.State != "st-1" {
		t.Fatalf("Txn:%+v", fp.last)
	}
}

func TestProviderRedirectAndCallback(t *testing.T) {
	cb := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	fp := &fakeProvider{callback: cb}
	e := newProviderEnv(t, fp)
	fp.start = func(r *http.Request, tx Txn) (Outcome, error) {
		return Outcome{Redirect: "https://login.example/go", Cookie: &http.Cookie{Name: "fake_c", Value: "v", Path: "/oauth"}}, nil
	}
	w := e.submit(url.Values{"action": {"allow"}, "secret": {"x"}})
	if w.Code != 303 || w.Header().Get("Location") != "https://login.example/go" || !strings.Contains(w.Header().Get("Set-Cookie"), "fake_c=v") {
		t.Fatalf("Redirect:%d %v", w.Code, w.Header())
	}
	// 有 Callback 的 provider 掛在 /oauth/<id>/callback
	if w := e.serve(e.h, "GET", "/oauth/fake/callback", ""); w.Code != 204 {
		t.Fatalf("callback 沒掛上:%d", w.Code)
	}
}

func TestProviderRetryAndError(t *testing.T) {
	fp := &fakeProvider{}
	e := newProviderEnv(t, fp)
	fp.start = func(r *http.Request, tx Txn) (Outcome, error) {
		return Outcome{Retry: "try again", RetryStatus: 401}, nil
	}
	w := e.submit(url.Values{"action": {"allow"}, "secret": {"x"}})
	if w.Code != 401 || !strings.Contains(w.Body.String(), `<p class="err">try again</p>`) || pCSRF.FindString(w.Body.String()) == "" {
		t.Fatalf("Retry 要用指定狀態碼、顯示訊息並發新的 CSRF:%d %s", w.Code, w.Body.String())
	}
	// 沒給狀態碼 = 403
	fp.start = func(r *http.Request, tx Txn) (Outcome, error) { return Outcome{Retry: "again"}, nil }
	if w := e.submit(url.Values{"action": {"allow"}}); w.Code != 403 {
		t.Fatalf("預設 403:%d", w.Code)
	}
	// 錯誤:OAuthError 照狀態碼與訊息顯示錯誤頁;其他錯誤 500
	fp.start = func(r *http.Request, tx Txn) (Outcome, error) {
		return Outcome{}, &service.OAuthError{Status: 503, Code: "temporarily_unavailable", Desc: "down"}
	}
	if w := e.submit(url.Values{"action": {"allow"}}); w.Code != 503 || !strings.Contains(w.Body.String(), "down") {
		t.Fatalf("OAuthError:%d %s", w.Code, w.Body.String())
	}
	fp.start = func(r *http.Request, tx Txn) (Outcome, error) { return Outcome{}, http.ErrAbortHandler }
	if w := e.submit(url.Values{"action": {"allow"}}); w.Code != 500 {
		t.Fatalf("一般錯誤:%d", w.Code)
	}
}

// 偽造的授權等級(客戶端沒要求 mcp:write 卻送 grant=write)在 provider 被呼叫之前就擋掉
func TestProviderNotCalledForForgedGrant(t *testing.T) {
	fp := &fakeProvider{}
	e := newProviderEnv(t, fp)
	called := false
	fp.start = func(r *http.Request, tx Txn) (Outcome, error) { called = true; return Outcome{}, nil }
	if w := e.submit(url.Values{"action": {"allow"}, "grant": {"write"}}); w.Code != 400 || called {
		t.Fatalf("要 400 且不呼叫 provider:%d called=%v", w.Code, called)
	}
	// 拒絕:不呼叫 provider,導回 access_denied
	if w := e.submit(url.Values{"action": {"deny"}}); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "error=access_denied") || called {
		t.Fatalf("deny:%d %s called=%v", w.Code, w.Header().Get("Location"), called)
	}
}

// 預設的 provider 組合(evoleap + 配對碼)的同意畫面:有 evoleap 時是主要按鈕、配對碼收在 details;沒有時配對碼直接顯示並自動取得焦點
func TestDefaultProvidersConsentPage(t *testing.T) {
	e := newProviderEnv(t)
	mux := http.NewServeMux()
	e.svc.Evoleap, e.svc.EvoleapConsole = "https://evo.test", "https://console.evo.test"
	NewWith(Config{Svc: e.svc, Public: pPublic}).Mount(mux)
	e.h = mux
	_, w := e.consent()
	page := w.Body.String()
	for _, want := range []string{
		`<button class="p wide" type="submit" name="via" value="evoleap">用 evoleap 登入並允許</button>`,
		`<p class="note">會前往 evoleap 登入(Google),完成後自動回到這裡並授權;不需要配對碼。</p>`,
		`<details><summary>改用配對碼</summary>`,
		`name="pairing_code"`, `maxlength="16"`, `placeholder="XXXX-XXXX"`, `autocapitalize="characters"`,
		`<button class="p" type="submit" name="action" value="allow">用配對碼允許</button>`,
		`<button type="submit" name="action" value="deny">拒絕</button>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("缺少 %s\n%s", want, page)
		}
	}
	if strings.Contains(page, "autofocus") || !strings.HasSuffix(w.Header().Get("Content-Security-Policy"), "form-action 'self' https://claude.ai https://console.evo.test") {
		t.Errorf("有 evoleap 時不自動對焦,CSP 要多允許核准頁:%s", w.Header().Get("Content-Security-Policy"))
	}
	// 輸錯配對碼後重新顯示:配對碼區預設展開
	if w := e.submit(url.Values{"action": {"allow"}, "pairing_code": {"AAAA-AAAA"}}); w.Code != 403 || !strings.Contains(w.Body.String(), "<details open>") {
		t.Errorf("配對碼錯:%d", w.Code)
	}

	// 沒有 evoleap:只有配對碼,直接顯示、自動對焦、按鈕是「允許」
	e.svc.Evoleap = ""
	_, w = e.consent()
	page = w.Body.String()
	for _, want := range []string{`autofocus`, `<button class="p" type="submit" name="action" value="allow">允許</button>`} {
		if !strings.Contains(page, want) {
			t.Errorf("缺少 %s\n%s", want, page)
		}
	}
	if strings.Contains(page, "evoleap") || strings.Contains(page, "<details") || !strings.HasSuffix(w.Header().Get("Content-Security-Policy"), "form-action 'self' https://claude.ai") {
		t.Errorf("沒有 evoleap 不該出現相關內容:%s %s", page, w.Header().Get("Content-Security-Policy"))
	}
}
