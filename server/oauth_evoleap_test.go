package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
)

// OAuth 同意畫面的 evoleap 瀏覽器登入(HTTP 層端到端;evoleap 是 httptest 假伺服器,不連真的服務)

const tConsole = "https://console.evoleap.test"

type fakeEvoleap struct {
	srv       *httptest.Server
	mu        sync.Mutex
	starts    []map[string]any // /start 收到的內容
	exchanges int
	logouts   []string // /logout 帶的 token
	nextID    int
	meUser    map[string]any // /api/me 回的 user(nil = 回 {"user":null})
	failEx    bool
}

func newFakeEvoleap(t *testing.T) *fakeEvoleap {
	f := &fakeEvoleap{meUser: map[string]any{"id": "ev-1", "email": "boss@x", "name": "Boss", "google": true}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/cli-auth/start", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.starts = append(f.starts, b)
		f.nextID++
		id := "cli-id-" + string(rune('a'+f.nextID))
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
	})
	mux.HandleFunc("POST /api/cli-auth/exchange", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.exchanges++
		fail := f.failEx
		f.mu.Unlock()
		if fail {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "bad"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "evotok-" + b["id"].(string)})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer evotok-") {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		u := f.meUser
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"user": u})
	})
	mux.HandleFunc("POST /api/cli-auth/logout", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.logouts = append(f.logouts, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEvoleap) counts() (starts, exchanges, logouts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts), f.exchanges, len(f.logouts)
}

// evoOA 一個啟用 evoleap 登入的測試環境
func newEvoOA(t *testing.T) (*oaHTTP, *fakeEvoleap) {
	o := newOAHTTP(t)
	f := newFakeEvoleap(t)
	o.svc.Evoleap = f.srv.URL
	o.svc.EvoleapConsole = tConsole
	return o, f
}

// startEvo 載入同意畫面、按「用 evoleap 登入並允許」:回傳 POST 的回應
func (o *oaHTTP) startEvo(client, resource, scope, grant string) *httptest.ResponseRecorder {
	o.t.Helper()
	csrf, _ := o.consentScope(client, resource, scope)
	f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {resource}, "scope": {scope}, "via": {"evoleap"}}
	if grant != "" {
		f.Set("grant", grant)
	}
	return o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded")
}

func bindCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "omixor_oauth_evo" && c.MaxAge >= 0 && c.Value != "" {
			return c
		}
	}
	return nil
}

func (o *oaHTTP) callback(id, code string, c *http.Cookie) *httptest.ResponseRecorder {
	hdr := []string{}
	if c != nil {
		hdr = append(hdr, "Cookie", c.Name+"="+c.Value)
	}
	return o.do("GET", "/oauth/evoleap/callback?"+url.Values{"id": {id}, "code": {code}}.Encode(), "", hdr...)
}

// evoStarted 開始 evoleap 登入並檢查導向:回傳 evoleap id 與 cookie
func (o *oaHTTP) evoStarted(client, resource, scope, grant string) (string, *http.Cookie) {
	o.t.Helper()
	w := o.startEvo(client, resource, scope, grant)
	if w.Code != 303 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Scheme+"://"+loc.Host != tConsole || loc.Path != "/login/cli" || loc.Query().Get("id") == "" {
		o.t.Fatal("要導去 evoleap 核准頁:", loc)
	}
	c := bindCookie(w)
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/oauth" || c.Secure != true {
		o.t.Fatalf("綁定 cookie 屬性不對:%+v", c)
	}
	return loc.Query().Get("id"), c
}

func (o *oaHTTP) exchangeCode(client, loc, resource string) *httptest.ResponseRecorder {
	u, _ := url.Parse(loc)
	return o.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {u.Query().Get("code")}, "redirect_uri": {tCallback},
		"code_verifier": {tVerifier}, "resource": {resource}})
}

func TestEvoleapConsoleButtonOnlyWhenConfigured(t *testing.T) {
	o := newOAHTTP(t)
	client := o.register()
	_, w := o.consentScope(client, tResource, "mcp:read")
	if strings.Contains(w.Body.String(), "用 evoleap 登入並允許") || strings.Contains(w.Body.String(), `value="evoleap"`) {
		t.Fatal("沒設 evoleap 不能有按鈕")
	}
	if !strings.Contains(w.Body.String(), `name="pairing_code"`) || !strings.HasSuffix(w.Header().Get("Content-Security-Policy"), "form-action 'self' https://claude.ai") {
		t.Fatal("只有配對碼、form-action 不變:", w.Header().Get("Content-Security-Policy"))
	}
	// 沒設 evoleap 時 via=evoleap 的表單不會走 evoleap(當成配對碼,碼是空的 → 被拒)
	csrf, _ := o.consentScope(client, tResource, "mcp:read")
	f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tResource}, "scope": {"mcp:read"}, "via": {"evoleap"}}
	if w := o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded"); w.Code != 400 {
		t.Fatal("沒啟用要 400:", w.Code, w.Body.String())
	}

	o2, _ := newEvoOA(t)
	c2 := o2.register()
	_, w2 := o2.consentScope(c2, tResource, "mcp:read")
	b := w2.Body.String()
	if !strings.Contains(b, "用 evoleap 登入並允許") || !strings.Contains(b, `name="pairing_code"`) || !strings.Contains(b, "<details") {
		t.Fatal("要有 evoleap 按鈕,配對碼收在 details 裡:", b)
	}
	csp := w2.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action 'self' https://claude.ai "+tConsole) || !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatal("CSP:", csp)
	}
	// 明確關掉
	o2.svc.OAuthEvoleapOff = true
	if _, w := o2.consentScope(c2, tResource, "mcp:read"); strings.Contains(w.Body.String(), "用 evoleap 登入並允許") {
		t.Fatal("關掉了不能有按鈕")
	}
}

func TestEvoleapHappyPathRead(t *testing.T) {
	o, f := newEvoOA(t)
	client := o.register()
	id, ck := o.evoStarted(client, tResource, "mcp:read offline_access", "")
	if len(f.starts) != 1 || f.starts[0]["redirect_uri"] != tPublic+"/oauth/evoleap/callback" || f.starts[0]["name"] != "omixor OAuth" {
		t.Fatal("start 內容:", f.starts)
	}
	w := o.callback(id, "evo-code", ck)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, tCallback+"?") {
		t.Fatal(loc)
	}
	if u, _ := url.Parse(loc); u.Query().Get("state") != "st-1" || u.Query().Get("code") == "" {
		t.Fatal(loc)
	}
	if _, ex, lo := f.counts(); ex != 1 || lo != 1 || f.logouts[0] != "evotok-"+id {
		t.Fatal("evoleap token 要撤銷:", ex, f.logouts)
	}
	tw := o.exchangeCode(client, loc, tResource)
	if tw.Code != 200 {
		t.Fatal(tw.Code, tw.Body.String())
	}
	m := jsonBody(t, tw)
	if m["scope"] != "mcp:read" {
		t.Fatal(m)
	}
	// access token 真的能連 MCP(唯讀)
	tools := toolNames(o.rpc(m["access_token"].(string), "tools/list", map[string]any{}))
	if len(tools) == 0 {
		t.Fatal("tools/list 空的")
	}
	// 沒有發 omixor 登入 token:u1 只有最初那一把
	toks, _ := o.svc.Store.ListTokens(*o.now)
	n := 0
	for _, tk := range toks {
		if tk.UserID == "u1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("不應發 omx_ token,現有 %d 把", n)
	}
	// 第一次 evoleap 登入:預先加入的使用者(管理員用 email 加進名單)對上 evoleap id
	if u, err := o.svc.Store.UserByEvoleapID("ev-1"); err != nil || u.ID != "u1" {
		t.Fatal("要對上預先加入的 u1:", u, err)
	}
	// callback 清掉 cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "omixor_oauth_evo" && c.MaxAge >= 0 {
			t.Fatal("callback 要清掉 cookie")
		}
	}
}

func TestEvoleapHappyPathWrite(t *testing.T) {
	o, f := newEvoOA(t)
	client := o.register()
	id, ck := o.evoStarted(client, tUnified, rwReq, "write")
	w := o.callback(id, "evo-code", ck)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	tw := o.exchangeCode(client, w.Header().Get("Location"), tUnified)
	if tw.Code != 200 || jsonBody(t, tw)["scope"] != "mcp:read mcp:write" {
		t.Fatal(tw.Code, tw.Body.String())
	}
	if _, _, lo := f.counts(); lo != 1 {
		t.Fatal("logout 要被呼叫")
	}
	// 沒選 write(預設唯讀)即使客戶端要求了
	id2, ck2 := o.evoStarted(client, tUnified, rwReq, "")
	w2 := o.callback(id2, "evo-code", ck2)
	tw2 := o.exchangeCode(client, w2.Header().Get("Location"), tUnified)
	if tw2.Code != 200 || jsonBody(t, tw2)["scope"] != "mcp:read" {
		t.Fatal(tw2.Code, tw2.Body.String())
	}
}

func TestEvoleapForgedWriteRejectedBeforeEvoleap(t *testing.T) {
	o, f := newEvoOA(t)
	client := o.register()
	w := o.startEvo(client, tUnified, "mcp:read offline_access", "write")
	if w.Code != 400 || bindCookie(w) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if s, _, _ := f.counts(); s != 0 {
		t.Fatal("不能呼叫 evoleap")
	}
}

func TestEvoleapBadCSRF(t *testing.T) {
	o, f := newEvoOA(t)
	client := o.register()
	f1 := url.Values{"csrf": {"nope"}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tResource}, "scope": {"mcp:read"}, "via": {"evoleap"}}
	if w := o.do("POST", "/oauth/authorize", f1.Encode(), "Content-Type", "application/x-www-form-urlencoded"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if s, _, _ := f.counts(); s != 0 {
		t.Fatal("不能呼叫 evoleap")
	}
}

func TestEvoleapCallbackRejections(t *testing.T) {
	o, f := newEvoOA(t)
	client := o.register()
	generic := func(w *httptest.ResponseRecorder, what string) string {
		t.Helper()
		if w.Code != 400 || strings.Contains(w.Header().Get("Location"), "code=") {
			t.Fatal(what, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	id, ck := o.evoStarted(client, tResource, "mcp:read", "")
	a := generic(o.callback(id, "c", nil), "沒有 cookie")
	b := generic(o.callback(id, "c", &http.Cookie{Name: ck.Name, Value: "wrong"}), "cookie 不對")
	c := generic(o.callback("unknown-id", "c", ck), "不認得的 id")
	if a != b || b != c {
		t.Fatal("錯誤要一樣(通用)")
	}
	if _, ex, _ := f.counts(); ex != 0 {
		t.Fatal("cookie 不對不能去換 token")
	}
	// 沒有 code
	generic(o.callback(id, "", ck), "沒有 code")
	// 正確的 cookie 仍然可用(前面的失敗沒有燒掉交易),成功一次後重放被拒
	if w := o.callback(id, "c", ck); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	generic(o.callback(id, "c", ck), "重放")
	if _, ex, lo := f.counts(); ex != 1 || lo != 1 {
		t.Fatal("重放不能再換 token:", ex, lo)
	}
	// 逾時
	id2, ck2 := o.evoStarted(client, tResource, "mcp:read", "")
	*o.now = o.now.Add(11 * time.Minute)
	generic(o.callback(id2, "c", ck2), "逾時")
	if _, ex, _ := f.counts(); ex != 1 {
		t.Fatal("逾時不能去換 token")
	}
}

func TestEvoleapTokenRevokedOnFailures(t *testing.T) {
	o, f := newEvoOA(t)
	client := o.register()
	// 帳號停用
	id, ck := o.evoStarted(client, tResource, "mcp:read", "")
	_ = o.svc.Store.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", EvoleapID: "ev-1", Disabled: true, CreatedAt: model.Now()})
	w := o.callback(id, "c", ck)
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" || loc.Query().Get("state") != "st-1" {
		t.Fatal("停用帳號要導回 error=access_denied:", w.Code, w.Header().Get("Location"))
	}
	if _, _, lo := f.counts(); lo != 1 {
		t.Fatal("停用也要撤銷 evoleap token")
	}
	_ = o.svc.Store.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", EvoleapID: "ev-1", CreatedAt: model.Now()})
	// evoleap 的使用者不在 omixor 名單:不會悄悄建立新使用者
	f.meUser = map[string]any{"id": "ev-9", "email": "stranger@x", "name": "S", "google": true}
	id2, ck2 := o.evoStarted(client, tResource, "mcp:read", "")
	w2 := o.callback(id2, "c", ck2)
	loc2, _ := url.Parse(w2.Header().Get("Location"))
	if w2.Code != 303 || loc2.Query().Get("error") != "access_denied" {
		t.Fatal(w2.Code, w2.Header().Get("Location"))
	}
	if _, err := o.svc.Store.UserByEmail("stranger@x"); err == nil {
		t.Fatal("不能建立新使用者")
	}
	if _, _, lo := f.counts(); lo != 2 {
		t.Fatal("要撤銷:", lo)
	}
	// 不是 Google 登入
	f.meUser = map[string]any{"id": "ev-1", "email": "boss@x", "google": false}
	id3, ck3 := o.evoStarted(client, tResource, "mcp:read", "")
	if w := o.callback(id3, "c", ck3); !strings.Contains(w.Header().Get("Location"), "error=access_denied") {
		t.Fatal(w.Header().Get("Location"))
	}
	if _, _, lo := f.counts(); lo != 3 {
		t.Fatal("要撤銷:", lo)
	}
	// exchange 失敗:沒有 evoleap token 可撤銷,交易照樣作廢
	f.failEx = true
	id4, ck4 := o.evoStarted(client, tResource, "mcp:read", "")
	if w := o.callback(id4, "c", ck4); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "error=access_denied") {
		t.Fatal(w.Code, w.Header().Get("Location"))
	}
	f.failEx = false
	if w := o.callback(id4, "c", ck4); w.Code != 400 {
		t.Fatal("交易要已作廢:", w.Code)
	}
}

func TestEvoleapNotMemberAndStartUnavailable(t *testing.T) {
	o, f := newEvoOA(t)
	client := o.register()
	// 組織層級授權:不是成員 → 導回 access_denied,evoleap token 照撤銷
	_ = o.svc.Store.SaveUser(model.User{ID: "u2", Email: "x@x", Role: "member", EvoleapID: "ev-2", CreatedAt: model.Now()})
	f.meUser = map[string]any{"id": "ev-2", "email": "x@x", "google": true}
	id, ck := o.evoStarted(client, tResource, "mcp:read", "")
	w := o.callback(id, "c", ck)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "error=access_denied") || strings.Contains(w.Header().Get("Location"), "code=") {
		t.Fatal(w.Code, w.Header().Get("Location"))
	}
	if _, _, lo := f.counts(); lo != 1 {
		t.Fatal("要撤銷")
	}
	// 連不到 evoleap(start 失敗):重新顯示同意畫面,可改用配對碼
	f.srv.Close()
	w2 := o.startEvo(client, tResource, "mcp:read", "")
	if w2.Code != 502 || !strings.Contains(w2.Body.String(), "改用配對碼") || bindCookie(w2) != nil {
		t.Fatal(w2.Code, w2.Body.String())
	}
}

func TestEvoleapPairingStillWorks(t *testing.T) {
	o, _ := newEvoOA(t)
	if access, _, _ := o.login(); access == "" {
		t.Fatal("配對碼仍可用")
	}
	// 同意畫面有 evoleap 按鈕時,填了配對碼再按 Enter(via=evoleap 隨表單送出)也走配對碼
	client := o.register()
	csrf, _ := o.consent(client)
	f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tResource}, "scope": {"mcp:read"}, "via": {"evoleap"}, "pairing_code": {o.pair()}}
	if w := o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded"); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "code=") {
		t.Fatal(w.Code, w.Body.String())
	}
}
