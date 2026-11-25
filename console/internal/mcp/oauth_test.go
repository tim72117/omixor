package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"omixor-web/internal/core"

	"omixor-core/config"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// console 的對外 MCP 連接埠(OAuth 部分)端到端;四種端點、omx_ 與本機使用者見 remote_port_test.go。假的 evoleap(httptest)+ 測試用 PostgreSQL,不連真的服務

const (
	oPublic   = "https://console.test"
	oCallback = "https://claude.ai/api/mcp/auth_callback"
	oVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ0123456789"
)

var oCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// fakeEvoleap 只實作 service 會呼叫的四個端點;me 是 /api/me 目前回的使用者(換人登入時改它)
type fakeEvoleap struct {
	srv *httptest.Server
	me  atomic.Value // map[string]any
	n   atomic.Int32
}

func newFakeEvoleap(t *testing.T) *fakeEvoleap {
	f := &fakeEvoleap{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/cli-auth/start", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "cli-" + string(rune('a'+f.n.Add(1)))})
	})
	mux.HandleFunc("POST /api/cli-auth/exchange", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "evotok"})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"user": f.me.Load()})
	})
	mux.HandleFunc("POST /api/cli-auth/logout", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{}")) })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEvoleap) loginAs(id, email string) {
	f.me.Store(map[string]any{"id": id, "email": email, "name": email, "google": true})
}

// oauthRig console 的對外 MCP 連接埠(newOAuthHandler)加上假 evoleap 與核心 store;組織 o(boss 是成員)、p(other 是成員)
type oauthRig struct {
	t   *testing.T
	h   http.Handler
	app *core.App
	svc *service.Service
	evo *fakeEvoleap
}

func oauthConfig(t *testing.T, public string) *config.Config {
	env := map[string]string{"OMIXOR_OAUTH_ADDR": "127.0.0.1:0", "OMIXOR_PUBLIC_URL": public}
	cfg := config.LoadFrom(func(k string) string { return env[k] }, config.Web)
	if err := cfg.Validate(config.Web); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newOAuthRig(t *testing.T) *oauthRig {
	t.Helper()
	st := storetest.NewWithOrg(t, "o", "p")
	evo := newFakeEvoleap(t)
	svc := &service.Service{Store: st, OAuthRegPerIP: 1000, OAuthRegTotal: 1000, Evoleap: evo.srv.URL}
	for _, u := range []model.User{{ID: "u_boss", Email: "boss@x", Role: "member"}, {ID: "u_other", Email: "other@x", Role: "member"}} {
		u.CreatedAt = model.Now()
		if err := st.SaveUser(u); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.SaveMember(model.Member{Org: "o", UserID: "u_boss", Role: "member"})
	_ = st.SaveMember(model.Member{Org: "p", UserID: "u_other", Role: "member"})
	app := iaApp(t)
	return &oauthRig{t: t, h: newOAuthHandler(app, svc, oauthConfig(t, oPublic)), app: app, svc: svc, evo: evo}
}

func (r *oauthRig) do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, oPublic+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w
}

func (r *oauthRig) json(w *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		r.t.Fatalf("不是 JSON(%d):%s", w.Code, w.Body.String())
	}
	return m
}

// connect 走完整個授權(註冊用戶端 → 同意畫面 → evoleap 登入 → 回呼 → token),回傳 /mcp 的 access token。
// grant 是同意畫面選的等級(read / write),scope 是用戶端要求的
func (r *oauthRig) connect(evoID, email, scope, grant string) string {
	r.t.Helper()
	return r.connectTo("/mcp", evoID, email, scope, grant)
}

// connectTo 同 connect,access token 綁定 path 這個資源(/mcp 或 /mcp/org/{org})
func (r *oauthRig) connectTo(path, evoID, email, scope, grant string) string {
	t := r.t
	t.Helper()
	r.evo.loginAs(evoID, email)
	w := r.do("POST", "/oauth/register", `{"client_name":"Claude","redirect_uris":["`+oCallback+`"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`,
		"Content-Type", "application/json")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	client := r.json(w)["client_id"].(string)
	sum := sha256.Sum256([]byte(oVerifier))
	form := url.Values{"client_id": {client}, "redirect_uri": {oCallback}, "response_type": {"code"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "state": {"st"}, "resource": {oPublic + path}, "scope": {scope}}
	page := r.do("GET", "/oauth/authorize?"+form.Encode(), "")
	m := oCSRF.FindStringSubmatch(page.Body.String())
	if page.Code != 200 || m == nil {
		t.Fatal(page.Code, page.Body.String())
	}
	// 同意畫面只有 evoleap 登入,沒有配對碼
	if b := page.Body.String(); !strings.Contains(b, "用 evoleap 登入並允許") || strings.Contains(b, "pairing_code") {
		t.Fatal("同意畫面要只有 evoleap:", b)
	}
	form.Set("csrf", m[1])
	form.Set("via", "evoleap")
	form.Set("grant", grant)
	consent := ""
	for _, c := range page.Result().Cookies() {
		if c.Name == "omixor_oauth_consent" {
			consent = c.Name + "=" + c.Value
		}
	}
	// 像瀏覽器一樣:帶同意畫面的 cookie,Origin 是對外網址
	w = r.do("POST", "/oauth/authorize", form.Encode(), "Content-Type", "application/x-www-form-urlencoded", "Cookie", consent, "Origin", oPublic, "Sec-Fetch-Site", "same-origin")
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	login, _ := url.Parse(w.Header().Get("Location"))
	var cookie string
	for _, c := range w.Result().Cookies() {
		if c.Name == "omixor_oauth_evo" {
			cookie = c.Name + "=" + c.Value
		}
	}
	w = r.do("GET", "/oauth/evoleap/callback?"+url.Values{"id": {login.Query().Get("id")}, "code": {"c"}}.Encode(), "", "Cookie", cookie)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	back, _ := url.Parse(w.Header().Get("Location"))
	code := back.Query().Get("code")
	if code == "" {
		t.Fatalf("沒拿到授權碼:%s", back)
	}
	tok := url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {code}, "redirect_uri": {oCallback}, "code_verifier": {oVerifier}, "resource": {oPublic + path}}
	w = r.do("POST", "/oauth/token", tok.Encode(), "Content-Type", "application/x-www-form-urlencoded")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return r.json(w)["access_token"].(string)
}

func (r *oauthRig) rpc(access, method string, params any) map[string]any {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	w := r.do("POST", "/mcp", string(b), "Content-Type", "application/json", "Authorization", "Bearer "+access)
	if w.Code != 200 {
		r.t.Fatal(w.Code, w.Body.String())
	}
	return r.json(w)
}

func (r *oauthRig) toolNames(access string) map[string]bool {
	out := map[string]bool{}
	for _, d := range r.rpc(access, "tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any) {
		out[d.(map[string]any)["name"].(string)] = true
	}
	return out
}

// call 呼叫工具:回傳是不是錯誤與文字
func (r *oauthRig) call(access, tool string, args map[string]any) (isErr bool, text string) {
	res := r.rpc(access, "tools/call", map[string]any{"name": tool, "arguments": args})["result"].(map[string]any)
	isErr, _ = res["isError"].(bool)
	b, _ := json.Marshal(res["content"])
	return isErr, string(b)
}

// 授權 → /mcp:唯讀授權只看到核心的唯讀白名單;讀寫授權多出協作寫入;console 的工具(gcp、fb_…)兩者都沒有
func TestConsoleOAuthToolsFollowGrant(t *testing.T) {
	r := newOAuthRig(t)
	read := r.toolNames(r.connect("ev-boss", "boss@x", "mcp:read", "read"))
	if len(read) == 0 {
		t.Fatal("tools/list 空的")
	}
	for n := range read {
		if !coremcp.ReadOnlyTools[n] {
			t.Errorf("唯讀授權不該有 %s", n)
		}
	}
	write := r.toolNames(r.connect("ev-boss", "boss@x", "mcp:read mcp:write", "write"))
	for n := range write {
		if !coremcp.ReadOnlyTools[n] && !coremcp.OAuthWriteTools[n] {
			t.Errorf("讀寫授權不該有 %s", n)
		}
	}
	if !write["issue_create"] || read["issue_create"] {
		t.Fatal("issue_create 只有讀寫授權才有")
	}
	for _, n := range []string{"gcp", "gcp_logs", "github", "fb_pages", "voice_submit", "server", "overview", "list"} {
		if read[n] || write[n] {
			t.Errorf("console 的工具 %s 不該出現在對外 MCP 連接埠", n)
		}
	}
}

// 讀寫授權寫入的署名是 evoleap 對上的真實使用者(usr 的 actor),不是本機使用者 omixor-console 或 mcp;唯讀授權不能寫;非成員的組織被拒;請求計入 MCP 狀態
func TestConsoleOAuthWriteIsSignedByRealUser(t *testing.T) {
	r := newOAuthRig(t)
	access := r.connect("ev-boss", "boss@x", "mcp:read mcp:write", "write")
	isErr, text := r.call(access, "issue_create", map[string]any{"org": "o", "title": "from claude.ai"})
	if isErr {
		t.Fatal(text)
	}
	list, err := r.svc.Store.ListIssues("o")
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	u, err := r.svc.Store.UserByActorID(list[0].CreatedBy)
	if err != nil || u.ID != "u_boss" || u.EvoleapID != "ev-boss" {
		t.Fatalf("署名要是 evoleap 對上的 u_boss,實際 %+v(%v)", u, err)
	}
	if local, err := r.svc.Store.UserByEvoleapID("local:" + core.LocalIdentity); err == nil && local.ActorID == list[0].CreatedBy {
		t.Fatal("不可以署名成本機使用者")
	}
	// boss 不是組織 p 的成員
	if isErr, _ := r.call(access, "issue_create", map[string]any{"org": "p", "title": "no"}); !isErr {
		t.Fatal("非成員的組織要被拒")
	}
	// 唯讀授權不能寫
	ro := r.connect("ev-boss", "boss@x", "mcp:read", "read")
	if isErr, text := r.call(ro, "issue_create", map[string]any{"org": "o", "title": "ro"}); !isErr || !strings.Contains(text, coremcp.ReadOnlyMessage) {
		t.Fatal("唯讀授權要被拒:", text)
	}
	// 另一個人(只是 p 的成員)在 o 不能寫
	other := r.connect("ev-other", "other@x", "mcp:read mcp:write", "write")
	if isErr, _ := r.call(other, "issue_create", map[string]any{"org": "o", "title": "no"}); !isErr {
		t.Fatal("非成員不能寫 o")
	}
	if n, _ := r.svc.Store.ListIssues("o"); len(n) != 1 {
		t.Fatalf("只該有 boss 建的那一筆:%d", len(n))
	}
	r.app.Mu.Lock()
	reqs, last := r.app.MCP.Requests, r.app.MCP.LastTool
	r.app.Mu.Unlock()
	if reqs == 0 || last != "issue_create" {
		t.Fatal("對外 MCP 連接埠的請求要計入 MCP 狀態:", reqs, last)
	}
}

// 這個連接埠只服務 /oauth、/.well-known 與四種 MCP 端點:其他路徑(含 server 的 /api/*)一律 404,Host 不是對外網址或本機 403
func TestConsoleRemotePortOnlyExposesOAuthAndMCP(t *testing.T) {
	r := newOAuthRig(t)
	for _, p := range []string{"/mcp/worker", "/mcp/org", "/mcp/x", "/api/orgs", "/api/version", "/api/auth/pair", "/api/auth/evoleap",
		"/api/auth/ldap", "/api/auth/gitlab", "/api/auth/gitlab/start", "/api/auth/status", "/api/tool/gcp", "/", "/health", "/office/"} {
		if w := r.do("POST", p, `{}`, "Authorization", "Bearer omx_x"); w.Code != 404 {
			t.Errorf("%s 不該存在,實際 %d", p, w.Code)
		}
	}
	for _, p := range []string{"/mcp", "/mcp/org/o"} {
		w := r.do("GET", "/.well-known/oauth-protected-resource"+p, "")
		if w.Code != 200 || r.json(w)["resource"] != oPublic+p {
			t.Fatal(p, w.Code, w.Body.String())
		}
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource/mcp", "/mcp", "/mcp/org/o", "/mcp/agent", "/mcp/worker/o"} {
		req := httptest.NewRequest("POST", "http://evil.example"+p, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		r.h.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("%s:Host 不符要 403,實際 %d", p, rec.Code)
		}
	}
	req := httptest.NewRequest("GET", "http://127.0.0.1:8771/.well-known/oauth-protected-resource/mcp", nil)
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("本機名稱要放行,實際 %d", rec.Code)
	}
}

// 啟動條件:沒設 OMIXOR_OAUTH_ADDR 什麼都不檢查;設了要有 PostgreSQL、不能是遠端模式
func TestCheckOAuthRuntime(t *testing.T) {
	if err := checkOAuthRuntime(true, false); err != nil {
		t.Fatal(err)
	}
	if err := checkOAuthRuntime(false, false); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatal(err)
	}
	if err := checkOAuthRuntime(true, true); err == nil || !strings.Contains(err.Error(), "遠端模式") {
		t.Fatal(err)
	}
	if err := PrepareOAuth(config.LoadFrom(func(string) string { return "" }, config.Web)); err != nil {
		t.Fatal("沒設 OMIXOR_OAUTH_ADDR 不該檢查:", err)
	}
}

// 守門:console 自己的工具(不是核心登記的)不可以進核心任何一種端點的白名單,否則對外 MCP 連接埠會把 gcp、fb_、voice_ 等開放給遠端
func TestConsoleToolsNotOnRemotePort(t *testing.T) {
	n := 0
	for _, name := range ToolNames() {
		if isCoreToolName(name) {
			continue
		}
		n++
		if coremcp.ReadOnlyTools[name] || coremcp.OAuthWriteTools[name] || coremcp.OAuthAdminTools[name] ||
			coremcp.AdminToolAllow[name] || coremcp.AgentToolAllow[name] || coremcp.WorkerToolAllow[name] {
			t.Errorf("console 的工具 %s 在核心的端點白名單裡", name)
		}
	}
	if n == 0 {
		t.Fatal("沒有找到 console 自己的工具,測試失效")
	}
}

// 核心 service 被重建(連線池換掉)後,對外 MCP 連接埠改用新的 service;沒換就不重建
func TestOAuthPortFollowsServiceRebuild(t *testing.T) {
	a, b := &service.Service{}, &service.Service{}
	cur := a
	var built []*service.Service
	p := newOAuthPort(func() *service.Service { return cur }, func(s *service.Service) http.Handler {
		built = append(built, s)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	})
	serve := func() int {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		return w.Code
	}
	if serve() != 204 || serve() != 204 || len(built) != 1 || built[0] != a {
		t.Fatalf("同一個 service 只建一次:%v", built)
	}
	cur = b
	if serve() != 204 || len(built) != 2 || built[1] != b {
		t.Fatalf("service 換了要重建:%v", built)
	}
	cur = nil
	if serve() != 503 {
		t.Fatal("核心不可用要 503")
	}
}

// console 的對外掛載同樣拒絕跨站送出,CSRF 綁同意畫面的 cookie(核心 core/oauth/browser.go)
func TestConsoleOAuthConsentRejectsCrossSite(t *testing.T) {
	r := newOAuthRig(t)
	w := r.do("POST", "/oauth/authorize", "via=evoleap", "Content-Type", "application/x-www-form-urlencoded", "Origin", "https://evil.test")
	if w.Code != 403 || w.Header().Get("Location") != "" {
		t.Fatal(w.Code, w.Body.String())
	}
}
