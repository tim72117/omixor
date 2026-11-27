package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"omixor-core/config"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// OAuth 唯讀授權的端到端測試(HTTP 層,走 HandlerWith):配對碼 → 同意畫面 → token → MCP 唯讀 / MCP Apps / REST 拒絕

const (
	tPublic   = "https://omixor.test"
	tResource = tPublic + "/mcp/org/o"
	tCallback = "https://claude.ai/api/mcp/auth_callback"
	tVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ0123456789"
)

type oaHTTP struct {
	t   *testing.T
	svc *service.Service
	h   http.Handler
	now *time.Time
	p   *model.Principal
	// consentCookie 像瀏覽器一樣記住同意畫面的綁定 cookie,送出同意時自動帶上
	consentCookie string
}

func newOAHTTP(t *testing.T) *oaHTTP {
	t.Helper()
	st := storetest.NewWithOrg(t, "o")
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	svc := &service.Service{Store: st, TTL: 90 * 24 * time.Hour, LoginPerIP: 1000, LoginTotal: 1000, OAuthRegPerIP: 1000, OAuthRegTotal: 1000,
		Now: func() time.Time { return now }}
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: model.Now()})
	_ = st.SaveMember(model.Member{Org: "o", UserID: "u1", Role: "admin"})
	_ = st.CreateToken(model.Token{ID: "t_mac", UserID: "u1", Device: "Mac", CreatedAt: model.Now(), Hash: service.HashToken("omx_mac")})
	p, err := svc.Authenticate("omx_mac")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.LoadFrom(envMap(map[string]string{"OMIXOR_PUBLIC_URL": tPublic}), config.Server)
	return &oaHTTP{t: t, svc: svc, h: HandlerWith(svc, cfg), now: &now, p: p}
}

func (o *oaHTTP) do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	if strings.HasPrefix(path, "/oauth/authorize") && o.consentCookie != "" && r.Header.Get("Cookie") == "" {
		r.Header.Set("Cookie", o.consentCookie)
	}
	w := httptest.NewRecorder()
	o.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == "omixor_oauth_consent" {
			o.consentCookie = c.Name + "=" + c.Value
		}
	}
	return w
}

func jsonBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("不是 JSON:%s", w.Body.String())
	}
	return m
}

func (o *oaHTTP) register() string {
	o.t.Helper()
	w := o.do("POST", "/oauth/register", `{"client_name":"Claude","redirect_uris":["`+tCallback+`"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`,
		"Content-Type", "application/json")
	if w.Code != 201 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	m := jsonBody(o.t, w)
	if m["token_endpoint_auth_method"] != "none" || m["client_name"] != "Claude" {
		o.t.Fatal(m)
	}
	return m["client_id"].(string)
}

func challenge() string {
	h := sha256.Sum256([]byte(tVerifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func authQuery(client string, extra ...string) string {
	q := url.Values{"client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tResource}, "scope": {"mcp:read offline_access"}}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == "" {
			q.Del(extra[i])
		} else {
			q.Set(extra[i], extra[i+1])
		}
	}
	return q.Encode()
}

var csrfRE = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (o *oaHTTP) consent(client string) (csrf string, w *httptest.ResponseRecorder) {
	o.t.Helper()
	w = o.do("GET", "/oauth/authorize?"+authQuery(client), "")
	if w.Code != 200 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	m := csrfRE.FindStringSubmatch(w.Body.String())
	if m == nil {
		o.t.Fatal("沒有 csrf", w.Body.String())
	}
	return m[1], w
}

func (o *oaHTTP) pair() string {
	o.t.Helper()
	pc, err := o.svc.CreatePairCode(o.p)
	if err != nil {
		o.t.Fatal(err)
	}
	return pc.Code
}

func (o *oaHTTP) approve(client, csrf, pair, action string) *httptest.ResponseRecorder {
	f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tResource}, "scope": {"mcp:read"}, "pairing_code": {pair}, "action": {action}}
	return o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded")
}

func (o *oaHTTP) login() (access, refresh, client string) {
	o.t.Helper()
	client = o.register()
	csrf, _ := o.consent(client)
	w := o.approve(client, csrf, o.pair(), "allow")
	if w.Code != 303 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	code := loc.Query().Get("code")
	tw := o.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {code}, "redirect_uri": {tCallback}, "code_verifier": {tVerifier}, "resource": {tResource}})
	if tw.Code != 200 {
		o.t.Fatal(tw.Code, tw.Body.String())
	}
	m := jsonBody(o.t, tw)
	return m["access_token"].(string), m["refresh_token"].(string), client
}

func (o *oaHTTP) token(f url.Values) *httptest.ResponseRecorder {
	return o.do("POST", "/oauth/token", f.Encode(), "Content-Type", "application/x-www-form-urlencoded")
}

func (o *oaHTTP) rpc(token, method string, params any) map[string]any {
	o.t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	hdr := []string{"Content-Type", "application/json"}
	if token != "" {
		hdr = append(hdr, "Authorization", "Bearer "+token)
	}
	w := o.do("POST", "/mcp/org/o", string(b), hdr...)
	if w.Code != 200 {
		o.t.Fatalf("%s:%d %s", method, w.Code, w.Body.String())
	}
	return jsonBody(o.t, w)
}

func toolNames(m map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, t := range m["result"].(map[string]any)["tools"].([]any) {
		tm := t.(map[string]any)
		out[tm["name"].(string)] = tm
	}
	return out
}

// metadata:RFC 9728 / RFC 8414 的內容(兩個路徑各一);其他組織 id 不合法 404
func TestOAuthMetadata(t *testing.T) {
	o := newOAHTTP(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp/org/o"} {
		w := o.do("GET", path, "")
		m := jsonBody(t, w)
		if w.Code != 200 || m["resource"] != tResource || m["authorization_servers"].([]any)[0] != tPublic+"/oauth" ||
			m["scopes_supported"].([]any)[0] != "mcp:read" || m["bearer_methods_supported"].([]any)[0] != "header" {
			t.Fatal(w.Code, m)
		}
	}
	if w := o.do("GET", "/.well-known/oauth-protected-resource", ""); w.Code != 200 || jsonBody(t, w)["authorization_servers"].([]any)[0] != tPublic+"/oauth" {
		t.Fatal("備援路徑要有", w.Code)
	}
	if w := o.do("GET", "/.well-known/oauth-protected-resource/mcp/org/bad.org", ""); w.Code != 404 {
		t.Fatal("組織 id 不合法要 404", w.Code)
	}
	for _, path := range []string{"/.well-known/oauth-authorization-server/oauth", "/oauth/.well-known/openid-configuration"} {
		w := o.do("GET", path, "")
		m := jsonBody(t, w)
		list := func(k string) string { b, _ := json.Marshal(m[k]); return string(b) }
		if w.Code != 200 || m["issuer"] != tPublic+"/oauth" || m["authorization_endpoint"] != tPublic+"/oauth/authorize" ||
			m["token_endpoint"] != tPublic+"/oauth/token" || m["registration_endpoint"] != tPublic+"/oauth/register" ||
			list("response_types_supported") != `["code"]` || list("grant_types_supported") != `["authorization_code","refresh_token"]` ||
			list("code_challenge_methods_supported") != `["S256"]` || list("token_endpoint_auth_methods_supported") != `["none"]` ||
			list("scopes_supported") != `["mcp:read","mcp:write","mcp:admin","offline_access"]` {
			t.Fatal(path, w.Body.String())
		}
		if _, ok := m["client_id_metadata_document_supported"]; ok {
			t.Fatal("先不要宣告 CIMD")
		}
		if w.Header().Get("Content-Type") != "application/json" {
			t.Fatal(w.Header())
		}
	}
}

// 沒帶 token:401 + resource_metadata;其他端點的 401 照舊(只有 realm)
func TestOAuthUnauthorizedHeader(t *testing.T) {
	o := newOAHTTP(t)
	w := o.do("POST", "/mcp/org/o", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "Content-Type", "application/json")
	want := `Bearer realm="omixor", resource_metadata="` + tPublic + `/.well-known/oauth-protected-resource/mcp/org/o"`
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != want {
		t.Fatal(w.Code, w.Header())
	}
	// 亂的 omx_ token:同樣的 401(不洩漏、也不加 invalid_token,omx_ 行為不變)
	w = o.do("POST", "/mcp/org/o", `{}`, "Authorization", "Bearer omx_nope")
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != want {
		t.Fatal(w.Code, w.Header())
	}
	// 執行者端點:帶自己的 resource_metadata(執行者專用授權)
	w = o.do("POST", "/mcp/worker/o", `{}`, "Content-Type", "application/json")
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != `Bearer realm="omixor", resource_metadata="`+tPublic+`/.well-known/oauth-protected-resource/mcp/worker/o"` {
		t.Fatal(w.Code, w.Header())
	}
	// 角色端點照舊(只有 realm)
	w = o.do("POST", "/mcp/agent", `{}`)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != `Bearer realm="omixor-agent"` {
		t.Fatal(w.Code, w.Header())
	}
}

// 完整流程:註冊 → 同意畫面 → 配對碼 → 授權碼 → token → 唯讀 MCP
func TestOAuthFullFlowAndReadOnlyMCP(t *testing.T) {
	o := newOAHTTP(t)
	client := o.register()

	// 同意畫面:安全標頭、內容(中文、client 名稱、redirect 主機、scope、配對碼欄位)、CSRF
	csrf, w := o.consent(client)
	hd := w.Header()
	csp := hd.Get("Content-Security-Policy")
	if hd.Get("X-Frame-Options") != "DENY" || hd.Get("Referrer-Policy") != "no-referrer" || hd.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(csp, "default-src 'self'; frame-ancestors 'none'; form-action 'self'") {
		t.Fatal(hd)
	}
	if !strings.Contains(csp, "https://claude.ai") { // Chrome 會用 form-action 檢查送出後的導向
		t.Fatal("form-action 要允許導回的 redirect_uri 來源", csp)
	}
	body := w.Body.String()
	for _, s := range []string{"授權連線確認", "Claude", "claude.ai", "mcp:read", "pairing_code", `name="viewport"`, "prefers-color-scheme"} {
		if !strings.Contains(body+o.do("GET", "/oauth/consent.css", "").Body.String(), s) {
			t.Fatal("同意畫面缺", s)
		}
	}
	if strings.Contains(body, "<style") || strings.Contains(body, "<script") {
		t.Fatal("CSP 不允許行內樣式或 script")
	}

	// 錯誤配對碼:重新顯示畫面(新的 CSRF)、不導回、不說原因
	w = o.approve(client, csrf, "ABCD-2345", "allow")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "配對碼無效、已過期或已用過") {
		t.Fatal(w.Code, w.Body.String())
	}
	csrf2 := csrfRE.FindStringSubmatch(w.Body.String())[1]
	if csrf2 == csrf {
		t.Fatal("要發新的 CSRF")
	}
	// 用過的 CSRF 不能再用(即使配對碼是對的),配對碼也不會被消耗
	pc := o.pair()
	if w = o.approve(client, csrf, pc, "allow"); w.Code != 403 || w.Header().Get("Location") != "" {
		t.Fatal("舊 CSRF 要擋", w.Code)
	}
	// 沒有 / 亂的 CSRF
	if w = o.approve(client, "", pc, "allow"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	// 拒絕:導回 error=access_denied(帶 state)
	csrf3 := csrfRE.FindStringSubmatch(o.do("GET", "/oauth/authorize?"+authQuery(client), "").Body.String())[1]
	w = o.approve(client, csrf3, "", "deny")
	if loc := w.Header().Get("Location"); w.Code != 303 || !strings.Contains(loc, "error=access_denied") || !strings.Contains(loc, "state=st-1") {
		t.Fatal(w.Code, loc)
	}
	// 允許:303 → redirect_uri?code=…&state=…
	w = o.approve(client, csrf2, pc, "allow")
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || loc.Host != "claude.ai" || loc.Path != "/api/mcp/auth_callback" || loc.Query().Get("state") != "st-1" || !strings.HasPrefix(loc.Query().Get("code"), "omc_") {
		t.Fatal(w.Code, w.Header())
	}
	code := loc.Query().Get("code")

	// token:JSON 不收 415;PKCE 錯、redirect 錯 invalid_grant;正確 200 + no-store
	if w = o.do("POST", "/oauth/token", `{"grant_type":"authorization_code"}`, "Content-Type", "application/json"); w.Code != 415 {
		t.Fatal("只收 form", w.Code)
	}
	good := url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {code}, "redirect_uri": {tCallback}, "code_verifier": {tVerifier}}
	bad := url.Values{}
	for k, v := range good {
		bad[k] = v
	}
	bad.Set("code_verifier", strings.Repeat("a", 50))
	if w = o.token(bad); w.Code != 400 || jsonBody(t, w)["error"] != "invalid_grant" {
		t.Fatal("PKCE 不符", w.Code, w.Body.String())
	}
	if w = o.token(good); w.Code != 400 { // 驗證失敗那次 code 已作廢
		t.Fatal("失敗後 code 作廢", w.Code, w.Body.String())
	}
	// 重新來一輪拿到正式的 token
	access, refresh, client := o.login()
	// token 回應的格式在 login 裡已經走過;這裡檢查標頭與欄位
	csrf4, _ := o.consent(client)
	loc2, _ := url.Parse(o.approve(client, csrf4, o.pair(), "allow").Header().Get("Location"))
	tw := o.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {loc2.Query().Get("code")}, "redirect_uri": {tCallback}, "code_verifier": {tVerifier}})
	tm := jsonBody(t, tw)
	if tw.Code != 200 || tw.Header().Get("Cache-Control") != "no-store" || tm["token_type"] != "Bearer" || tm["expires_in"] != float64(600) || tm["scope"] != "mcp:read" ||
		!strings.HasPrefix(tm["access_token"].(string), "omo_") || tm["refresh_token"] == "" {
		t.Fatal(tw.Code, tw.Header(), tm)
	}

	// ---- MCP:唯讀 ----
	tools := toolNames(o.rpc(access, "tools/list", map[string]any{}))
	if len(tools) == 0 || tools["issues"] == nil || tools["flowchart_render"] == nil || tools["issue_items"] == nil {
		t.Fatal("唯讀工具要列出", tools)
	}
	for name := range tools {
		if ct, ok := modelTool(name); !ok || !ct.ReadOnly {
			t.Fatalf("tools/list 列出了非唯讀工具 %s", name)
		}
	}
	for _, n := range []string{"issue_create", "issue_update", "doc_save", "channel_post", "channel_messages", "agent_task_claim", "agent_upsert", "chain_save", "project_save",
		"issue_item_add", "issue_item_report", "incident_declare", "cabinet_layout_save", "issue_decision_answer", "wireframe_render", "agent_context"} {
		if tools[n] != nil {
			t.Fatalf("唯讀授權不能列 %s", n)
		}
	}
	// tools/call 其他工具:isError ReadOnlyMessage,而且真的沒執行
	res := o.rpc(access, "tools/call", map[string]any{"name": "issue_create", "arguments": map[string]any{"title": "不該建立"}})["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), mcp.ReadOnlyMessage) {
		t.Fatal(res)
	}
	if list, _ := o.svc.Store.ListIssues("o"); len(list) != 0 {
		t.Fatal("唯讀授權不能建立 Issue", list)
	}
	// 不存在的工具照舊「沒有這個工具」
	if r := o.rpc(access, "tools/call", map[string]any{"name": "nope", "arguments": map[string]any{}}); r["error"] == nil {
		t.Fatal(r)
	}
	// 唯讀工具可以用;flowchart_render 能畫、不能存
	res = o.rpc(access, "tools/call", map[string]any{"name": "issues", "arguments": map[string]any{}})["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatal(res)
	}
	res = o.rpc(access, "tools/call", map[string]any{"name": "flowchart_render", "arguments": map[string]any{"spec": "flowchart TD\n A[開始] --> B[結束]"}})["result"].(map[string]any)
	if res["isError"] == true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "<svg") {
		t.Fatal(res)
	}
	res = o.rpc(access, "tools/call", map[string]any{"name": "flowchart_render", "arguments": map[string]any{"spec": "flowchart TD\n A --> B",
		"save": map[string]any{"title": "x", "section": "s"}}})["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), mcp.ReadOnlyMessage) {
		t.Fatal("flowchart_render 不能存檔", res)
	}

	// ---- MCP Apps ----
	for _, c := range []struct{ tool, uri string }{{"flowchart_render", "ui://omixor/flowchart"}, {"issue_items", "ui://omixor/items"}} {
		meta := tools[c.tool]["_meta"].(map[string]any)
		if meta["ui"].(map[string]any)["resourceUri"] != c.uri {
			t.Fatal(c.tool, meta)
		}
	}
	if tools["issues"]["_meta"] != nil {
		t.Fatal("其他工具不帶 UI")
	}
	list := o.rpc(access, "resources/list", map[string]any{})["result"].(map[string]any)["resources"].([]any)
	if len(list) != len(mcp.UIResources()) { // 登記的畫面全列出(flowchart、items、decisions…),不寫死數量
		t.Fatal(list)
	}
	for _, uri := range []string{"ui://omixor/flowchart", "ui://omixor/items"} {
		c := o.rpc(access, "resources/read", map[string]any{"uri": uri})["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
		if c["uri"] != uri || c["mimeType"] != "text/html;profile=mcp-app" || !strings.Contains(c["text"].(string), "ui/initialize") ||
			c["_meta"].(map[string]any)["ui"].(map[string]any)["prefersBorder"] != true {
			t.Fatal(uri, c["mimeType"], c["_meta"])
		}
		for _, bad := range []string{"https://", "http://", "src=", "@import"} {
			if strings.Contains(c["text"].(string), bad) {
				t.Fatalf("%s 不能引用外部來源(%s)", uri, bad)
			}
		}
	}
	if r := o.rpc(access, "resources/read", map[string]any{"uri": "ui://omixor/nope"}); r["error"] == nil {
		t.Fatal("不存在的資源要錯誤")
	}
	init := o.rpc(access, "initialize", map[string]any{"protocolVersion": "2025-06-18"})["result"].(map[string]any)
	if init["capabilities"].(map[string]any)["resources"] == nil || init["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatal("initialize 要宣告 resources", init)
	}

	// ---- REST:OAuth token 一律 403 ----
	for _, path := range []string{"/api/orgs", "/api/orgs/o/issues", "/api/auth/me", "/api/orgs/o/members"} {
		if w := o.do("GET", path, "", "Authorization", "Bearer "+access); w.Code != 403 {
			t.Fatalf("%s 要 403,得到 %d", path, w.Code)
		}
	}
	if w := o.do("POST", "/api/orgs/o/issues", `{"title":"x"}`, "Authorization", "Bearer "+access, "Content-Type", "application/json"); w.Code != 403 {
		t.Fatal("寫入也 403", w.Code)
	}
	if w := o.do("POST", "/api/auth/pair", ``, "Authorization", "Bearer "+access); w.Code != 403 {
		t.Fatal("不能用 OAuth token 產生配對碼", w.Code)
	}
	// 執行者端點只收執行者專用授權,一般授權的 token 無效
	if w := o.do("POST", "/mcp/worker/o", `{}`, "Authorization", "Bearer "+access); w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatal("worker 端點要 401", w.Code)
	}
	// refresh token 不能當 access token 用
	if w := o.do("POST", "/mcp/org/o", `{}`, "Authorization", "Bearer "+refresh); w.Code != 401 {
		t.Fatal(w.Code)
	}

	// omx_ 使用者 token:不受影響,有寫入工具(也有 UI meta)
	full := toolNames(o.rpc("omx_mac", "tools/list", map[string]any{}))
	if full["issue_create"] == nil || full["doc_save"] == nil || full["flowchart_render"]["_meta"] == nil {
		t.Fatal("omx_ 的工具清單不變")
	}
	if res := o.rpc("omx_mac", "tools/call", map[string]any{"name": "issue_create", "arguments": map[string]any{"title": "我自己建的"}})["result"].(map[string]any); res["isError"] == true {
		t.Fatal(res)
	}
}

func modelTool(name string) (model.CoreTool, bool) { return model.CoreToolByName(name) }

// 過期、他組織的 resource、撤銷:401 invalid_token(帶 resource_metadata)
func TestOAuthInvalidTokenChallenge(t *testing.T) {
	o := newOAHTTP(t)
	access, refresh, client := o.login()
	call := func(path, tok string) *httptest.ResponseRecorder {
		return o.do("POST", path, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
	}
	want := `Bearer realm="omixor", resource_metadata="` + tPublic + `/.well-known/oauth-protected-resource/mcp/org/o", error="invalid_token"`
	if w := call("/mcp/org/o", access); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// 綁 o 的 token 拿去連別的組織 → invalid_token(連 403 都不是)
	_ = o.svc.Store.CreateOrg(model.Org{ID: "p", Name: "P"})
	w := call("/mcp/org/p", access)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != strings.Replace(want, "/org/o", "/org/p", 2) {
		t.Fatal(w.Code, w.Header())
	}
	// 過期
	*o.now = o.now.Add(601 * time.Second)
	w = call("/mcp/org/o", access)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != want {
		t.Fatal("過期要 401 invalid_token", w.Code, w.Header())
	}
	// refresh 換新的就能用
	rf := o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}})
	if rf.Code != 200 {
		t.Fatal(rf.Code, rf.Body.String())
	}
	m := jsonBody(t, rf)
	if w := call("/mcp/org/o", m["access_token"].(string)); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// 舊的 refresh 再用:invalid_grant,家族撤銷,新的 access 也失效
	rf = o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}})
	if rf.Code != 400 || jsonBody(t, rf)["error"] != "invalid_grant" {
		t.Fatal(rf.Code, rf.Body.String())
	}
	if w := call("/mcp/org/o", m["access_token"].(string)); w.Code != 401 {
		t.Fatal("家族撤銷後 access 失效", w.Code)
	}
}

// 授權請求的把關(HTTP):redirect 不符不導回;缺 PKCE 導回 error;resource 不對 invalid_target;未註冊 client
func TestOAuthAuthorizeValidationHTTP(t *testing.T) {
	o := newOAHTTP(t)
	client := o.register()
	if w := o.do("GET", "/oauth/authorize?"+authQuery(client, "redirect_uri", "https://evil.example/cb"), ""); w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatal("redirect_uri 不符不能導回", w.Code)
	}
	if w := o.do("GET", "/oauth/authorize?"+authQuery("nope"), ""); w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatal(w.Code)
	}
	for name, c := range map[string]struct{ k, v, errCode string }{
		"沒有 PKCE":       {"code_challenge", "", "invalid_request"},
		"plain":         {"code_challenge_method", "plain", "invalid_request"},
		"resource 不對":   {"resource", "https://omixor.test/mcp/org/o/x", "invalid_target"},
		"resource 他站":   {"resource", "https://evil.example/mcp/org/o", "invalid_target"},
		"沒有 resource":   {"resource", "", "invalid_target"},
		"response_type": {"response_type", "token", "unsupported_response_type"},
	} {
		w := o.do("GET", "/oauth/authorize?"+authQuery(client, c.k, c.v), "")
		loc, _ := url.Parse(w.Header().Get("Location"))
		if w.Code != 303 || loc.Host != "claude.ai" || loc.Query().Get("error") != c.errCode || loc.Query().Get("state") != "st-1" {
			t.Errorf("%s:%d %s", name, w.Code, w.Header().Get("Location"))
		}
	}
	// 多個 resource
	w := o.do("GET", "/oauth/authorize?"+authQuery(client)+"&resource="+url.QueryEscape(tResource), "")
	if loc, _ := url.Parse(w.Header().Get("Location")); w.Code != 303 || loc.Query().Get("error") != "invalid_target" {
		t.Fatal("多個 resource", w.Code)
	}
	// 使用者不是這個組織的成員:同意後被拒絕(不發 code)
	_ = o.svc.Store.CreateOrg(model.Org{ID: "p", Name: "P"})
	q := strings.Replace(authQuery(client), url.QueryEscape(tResource), url.QueryEscape(tPublic+"/mcp/org/p"), 1)
	cw := o.do("GET", "/oauth/authorize?"+q, "")
	csrf := csrfRE.FindStringSubmatch(cw.Body.String())[1]
	f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "resource": {tPublic + "/mcp/org/p"}, "pairing_code": {o.pair()}, "action": {"allow"}}
	w = o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded")
	if w.Code != 403 || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "不是組織 p 的成員") {
		t.Fatal(w.Code, w.Body.String())
	}
	// 這些路由只有對的方法
	if w := o.do("GET", "/oauth/token", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := o.do("GET", "/oauth/register", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestOAuthRegisterHTTP(t *testing.T) {
	o := newOAHTTP(t)
	post := func(ct, body string) *httptest.ResponseRecorder {
		return o.do("POST", "/oauth/register", body, "Content-Type", ct)
	}
	if w := post("text/plain", `{}`); w.Code != 415 {
		t.Fatal(w.Code)
	}
	big := `{"client_name":"` + strings.Repeat("a", 17<<10) + `","redirect_uris":["` + tCallback + `"]}`
	if w := post("application/json", big); w.Code != 413 {
		t.Fatal("超過 16 KB 要 413", w.Code)
	}
	if w := post("application/json", `[1]`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := post("application/json", `{"redirect_uris":["https://evil.example/cb"]}`); w.Code != 400 || jsonBody(t, w)["error"] != "invalid_redirect_uri" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := post("application/json", `{"redirect_uris":["`+tCallback+`"],"token_endpoint_auth_method":"client_secret_post"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	// token 端點:公開用戶端不收密鑰
	client := o.register()
	f := url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "client_secret": {"x"}, "refresh_token": {"omr_x"}}
	if w := o.token(f); w.Code != 401 || jsonBody(t, w)["error"] != "invalid_client" {
		t.Fatal(w.Code, w.Body.String())
	}
	// 沒設 OMIXOR_PUBLIC_URL:不掛 OAuth 路由
	st := storetest.New(t)
	h := Handler(&service.Service{Store: st})
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/oauth/authorize"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 {
			t.Fatal(path, rec.Code)
		}
	}
	_ = mcp.ReadOnlyMessage
}
