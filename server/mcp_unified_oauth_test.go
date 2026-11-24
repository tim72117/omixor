package server

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omixor-core/config"
	"omixor-core/service"
)

// 統一入口 /mcp 的 OAuth(帳號層級、唯讀):resource = <public>/mcp

const tUnified = tPublic + "/mcp"

func (o *oaHTTP) mcpPost(path, tok, body string) *httptest.ResponseRecorder {
	hdr := []string{"Content-Type", "application/json"}
	if tok != "" {
		hdr = append(hdr, "Authorization", "Bearer "+tok)
	}
	return o.do("POST", path, body, hdr...)
}

func callBody(tool, args string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
}

// accountLogin 走完帳號層級授權(resource=<public>/mcp),回傳 access、refresh、client、同意畫面 HTML
func (o *oaHTTP) accountLogin() (access, refresh, client, consentHTML string) {
	o.t.Helper()
	client = o.register()
	w := o.do("GET", "/oauth/authorize?"+authQuery(client, "resource", tUnified), "")
	if w.Code != 200 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	consentHTML = w.Body.String()
	csrf := csrfRE.FindStringSubmatch(consentHTML)[1]
	f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tUnified}, "scope": {"mcp:read"}, "pairing_code": {o.pair()}, "action": {"allow"}}
	pw := o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded")
	if pw.Code != 303 {
		o.t.Fatal(pw.Code, pw.Body.String())
	}
	loc, _ := url.Parse(pw.Header().Get("Location"))
	tw := o.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {loc.Query().Get("code")}, "redirect_uri": {tCallback},
		"code_verifier": {tVerifier}, "resource": {tUnified}})
	if tw.Code != 200 {
		o.t.Fatal(tw.Code, tw.Body.String())
	}
	m := jsonBody(o.t, tw)
	return m["access_token"].(string), m["refresh_token"].(string), client, consentHTML
}

func TestUnifiedProtectedResourceMetadata(t *testing.T) {
	o := newOAHTTP(t)
	w := o.do("GET", "/.well-known/oauth-protected-resource/mcp", "")
	m := jsonBody(t, w)
	if w.Code != 200 || m["resource"] != tUnified || m["authorization_servers"].([]any)[0] != tPublic+"/oauth" ||
		m["scopes_supported"].([]any)[0] != "mcp:read" || len(m["scopes_supported"].([]any)) != 3 || m["scopes_supported"].([]any)[1] != "mcp:write" || m["scopes_supported"].([]any)[2] != "mcp:admin" {
		t.Fatal(w.Code, m)
	}
	// 既有的不變:組織路徑、根目錄備援
	if m := jsonBody(t, o.do("GET", "/.well-known/oauth-protected-resource/mcp/org/o", "")); m["resource"] != tResource {
		t.Fatal(m)
	}
	if m := jsonBody(t, o.do("GET", "/.well-known/oauth-protected-resource", "")); m["resource"] != tPublic {
		t.Fatal(m)
	}
	// 沒設對外網址:沒有這個路由
	h := Handler(&service.Service{Store: o.svc.Store})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/mcp", nil))
	if rec.Code == 200 {
		t.Fatal(rec.Code)
	}
}

func TestUnifiedUnauthorizedHeaders(t *testing.T) {
	o := newOAHTTP(t)
	const rm = tPublic + "/.well-known/oauth-protected-resource/mcp"
	want := `Bearer realm="omixor", resource_metadata="` + rm + `"`
	w := o.mcpPost("/mcp", "", `{}`)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != want || !strings.Contains(w.Body.String(), "A bearer token is required") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	if w := o.mcpPost("/mcp", "omx_nope", `{}`); w.Code != 401 || w.Header().Get("WWW-Authenticate") != want {
		t.Fatal(w.Code, w.Header())
	}
	if w := o.mcpPost("/mcp", "omo_nope", `{}`); w.Code != 401 || w.Header().Get("WWW-Authenticate") != want+`, error="invalid_token"` {
		t.Fatal(w.Code, w.Header())
	}
}

func TestUnifiedOAuthAccountFlow(t *testing.T) {
	o := newOAHTTP(t)
	access, refresh, client, page := o.accountLogin()
	// 同意畫面:說明是帳號層級唯讀,沒有「組織」欄位
	if !strings.Contains(page, "你帳號能存取的所有組織") || strings.Contains(page, "<dt>組織</dt>") || !strings.Contains(page, `value="`+tUnified+`"`) {
		t.Fatal(page)
	}
	// 唯讀工具可用,org 必填、結果附組織
	w := o.mcpPost("/mcp", access, callBody("issues", `{"org":"o"}`))
	if w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) || !strings.Contains(w.Body.String(), `"omixor/org":"o"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := o.mcpPost("/mcp", access, callBody("issues", `{}`)); !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Body.String())
	}
	// 非成員的組織:拒絕
	if w := o.mcpPost("/mcp", access, callBody("issues", `{"org":"zzz"}`)); !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Body.String())
	}
	// 寫入:唯讀授權
	w = o.mcpPost("/mcp", access, callBody("issue_create", `{"org":"o","title":"x"}`))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"isError":true`) || !strings.Contains(w.Body.String(), "此連線是唯讀授權") {
		t.Fatal(w.Code, w.Body.String())
	}
	// 帳號層級 token 不能用在組織端點;組織 token 不能用在 /mcp(都是 401 invalid_token)
	if w := o.mcpPost("/mcp/org/o", access, `{}`); w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatal(w.Code, w.Header())
	}
	orgAccess, _, _ := o.login()
	if w := o.mcpPost("/mcp", orgAccess, `{}`); w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatal(w.Code, w.Header())
	}
	if w := o.mcpPost("/mcp/org/o", orgAccess, callBody("issues", `{}`)); w.Code != 200 {
		t.Fatal("組織 token 在組織端點照舊", w.Code)
	}
	// REST 仍拒絕
	if w := o.do("GET", "/api/orgs", "", "Authorization", "Bearer "+access); w.Code != 403 {
		t.Fatal(w.Code)
	}
	// refresh 保持 resource(帶 /mcp 可以,帶組織的 invalid_target)
	rf := o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}, "resource": {tResource}})
	if rf.Code != 400 || jsonBody(t, rf)["error"] != "invalid_target" {
		t.Fatal(rf.Code, rf.Body.String())
	}
	rf = o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}, "resource": {tUnified}})
	if rf.Code != 200 {
		t.Fatal(rf.Code, rf.Body.String())
	}
	if w := o.mcpPost("/mcp", jsonBody(t, rf)["access_token"].(string), callBody("orgs", `{}`)); w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// 過期
	*o.now = o.now.Add(601 * time.Second)
	if w := o.mcpPost("/mcp", access, callBody("orgs", `{}`)); w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatal(w.Code, w.Header())
	}
}

// 不認得的 resource 仍然 invalid_target
func TestUnifiedOAuthInvalidTargetStill(t *testing.T) {
	o := newOAHTTP(t)
	client := o.register()
	for _, res := range []string{tPublic + "/mcp/foo", tPublic + "/mcp/", tPublic + "/mcp/org/", "https://evil.example/mcp", tPublic + "/mcp/agent", tPublic} {
		w := o.do("GET", "/oauth/authorize?"+authQuery(client, "resource", res), "")
		loc, _ := url.Parse(w.Header().Get("Location"))
		if w.Code != 303 || loc.Query().Get("error") != "invalid_target" {
			t.Errorf("%s:%d %s", res, w.Code, w.Header().Get("Location"))
		}
	}
}

// 沒設對外網址(oa == nil):/mcp 仍可用 omx_;omo_ 回 403 並說明 OAuth 沒啟用
func TestUnifiedWithoutOAuth(t *testing.T) {
	o := newOAHTTP(t)
	h := HandlerWith(o.svc, config.LoadFrom(envMap(map[string]string{}), config.Server))
	post := func(tok, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := post("omx_mac", callBody("issues", `{"org":"o"}`)); w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w := post("omo_whatever", `{}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "OAuth is not enabled") {
		t.Fatal(w.Code, w.Body.String())
	}
	// 沒 token:401,沒有 resource_metadata(沒有 OAuth)
	w = post("", `{}`)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != `Bearer realm="omixor"` {
		t.Fatal(w.Code, w.Header())
	}
}

// 觀測:統一入口也記沒帶 token / 帶 omo_ 的請求(resource 用 <public>/mcp 判斷 token_state),omx_ 不記
func TestUnifiedObserveLog(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	o := newOAHTTP(t)
	access, _, _, _ := o.accountLogin()
	buf.Reset()
	o.mcpPost("/mcp", access, callBody("issues", `{"org":"o"}`))
	o.mcpPost("/mcp", access, callBody("issue_create", `{"org":"o","title":"x"}`))
	o.mcpPost("/mcp/org/o", access, `{}`)
	o.mcpPost("/mcp", "omx_mac", callBody("issues", `{"org":"o"}`))
	out := buf.String()
	if strings.Count(out, `"path":"/mcp"`) != 2 || !strings.Contains(out, `"token_state":"valid"`) || !strings.Contains(out, `"token_state":"read_only_denied"`) ||
		!strings.Contains(out, `"token_state":"wrong_resource"`) || strings.Contains(out, access) || strings.Contains(out, "omx_mac") {
		t.Fatal(out)
	}
}
