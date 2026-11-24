package server

import (
	"bytes"
	"log"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// OAuth 寫入(mcp:write)端到端(HTTP 層):同意畫面的讀寫選項、伺服器端驗證 scope、CSRF 綁定、token、MCP 寫入白名單

const rwReq = "mcp:read mcp:write offline_access"

// consentScope 載入同意畫面(要求 scope、resource),回傳 csrf 與頁面
func (o *oaHTTP) consentScope(client, resource, scope string) (string, *httptest.ResponseRecorder) {
	o.t.Helper()
	w := o.do("GET", "/oauth/authorize?"+authQuery(client, "resource", resource, "scope", scope), "")
	if w.Code != 200 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	m := csrfRE.FindStringSubmatch(w.Body.String())
	if m == nil {
		o.t.Fatal("沒有 csrf", w.Body.String())
	}
	return m[1], w
}

// approveScope 送出同意畫面:scope = 表單裡的隱藏欄位、grant = 使用者選的(空 = 沒有送這個欄位)
func (o *oaHTTP) approveScope(client, csrf, resource, scope, grant string) *httptest.ResponseRecorder {
	f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {resource}, "scope": {scope}, "pairing_code": {o.pair()}, "action": {"allow"}}
	if grant != "" {
		f.Set("grant", grant)
	}
	return o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded")
}

// tokenFor 走完授權並換 token,回傳 token 回應
func (o *oaHTTP) tokenFor(resource, requested, grant string) map[string]any {
	o.t.Helper()
	client := o.register()
	csrf, _ := o.consentScope(client, resource, requested)
	w := o.approveScope(client, csrf, resource, requested, grant)
	if w.Code != 303 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	tw := o.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {loc.Query().Get("code")}, "redirect_uri": {tCallback},
		"code_verifier": {tVerifier}, "resource": {resource}})
	if tw.Code != 200 {
		o.t.Fatal(tw.Code, tw.Body.String())
	}
	m := jsonBody(o.t, tw)
	m["client"] = client
	return m
}

func TestOAuthWriteMetadataScopes(t *testing.T) {
	o := newOAHTTP(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource/mcp/org/o", "/.well-known/oauth-authorization-server/oauth"} {
		b := o.do("GET", path, "").Body.String()
		if !strings.Contains(b, `"mcp:read"`) || !strings.Contains(b, `"mcp:write"`) {
			t.Fatal(path, b)
		}
	}
}

func TestOAuthWriteConsentOptions(t *testing.T) {
	o := newOAHTTP(t)
	client := o.register()
	// 客戶端沒要求 write:沒有讀寫選項
	_, w := o.consentScope(client, tUnified, "mcp:read offline_access")
	if strings.Contains(w.Body.String(), `name="grant"`) || strings.Contains(w.Body.String(), "讀寫") {
		t.Fatal("沒要求 write 不能提供讀寫:", w.Body.String())
	}
	// 沒指定 scope:同上
	if _, w := o.consentScope(client, tUnified, ""); strings.Contains(w.Body.String(), `name="grant"`) {
		t.Fatal("沒指定 scope 不能提供讀寫")
	}
	// 要求 write:兩個選項,預設唯讀,讀寫有醒目說明
	_, w = o.consentScope(client, tUnified, rwReq)
	page := w.Body.String()
	if !strings.Contains(page, `value="read" checked`) || strings.Contains(page, `value="write" checked`) || !strings.Contains(page, `value="write"`) {
		t.Fatal("要有唯讀(預設)與讀寫兩個選項:", page)
	}
	for _, want := range []string{"唯讀(建議)", "讀寫(進階)", "以你的身分修改資料", "存文件到文件櫃", "在頻道發訊息", "伺服器管理員", "撤銷"} {
		if !strings.Contains(page, want) {
			t.Errorf("同意畫面缺少說明 %q", want)
		}
	}
}

func TestOAuthWriteGrantFlowAccount(t *testing.T) {
	o := newOAHTTP(t)
	// 要求 write、使用者選讀寫 → 讀寫 token,能在白名單內寫入、寫到指定組織
	m := o.tokenFor(tUnified, rwReq, "write")
	if m["scope"] != "mcp:read mcp:write" {
		t.Fatal(m)
	}
	access := m["access_token"].(string)
	w := o.mcpPost("/mcp", access, callBody("issue_create", `{"org":"o","title":"經 OAuth 讀寫"}`))
	if w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) || !strings.Contains(w.Body.String(), `"omixor/org":"o"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(o.mcpPost("/mcp", access, callBody("issues", `{"org":"o"}`)).Body.String(), "經 OAuth 讀寫") {
		t.Fatal("Issue 沒寫到組織 o")
	}
	// 白名單外:英文訊息;沒有效果
	w = o.mcpPost("/mcp", access, callBody("agent_upsert", `{"org":"o","id":"evil","name":"x"}`))
	if !strings.Contains(w.Body.String(), `"isError":true`) || !strings.Contains(w.Body.String(), "This tool is not available over OAuth") {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(o.mcpPost("/mcp", access, callBody("agents", `{"org":"o"}`)).Body.String(), "evil") {
		t.Fatal("agent_upsert 不該生效")
	}
	// 非成員的組織
	if w := o.mcpPost("/mcp", access, callBody("issue_create", `{"org":"zzz","title":"x"}`)); !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Body.String())
	}
	// REST 與別的資源:拒絕
	if w := o.do("GET", "/api/orgs", "", "Authorization", "Bearer "+access); w.Code != 403 {
		t.Fatal("REST 要 403", w.Code)
	}
	if w := o.mcpPost("/mcp/org/o", access, callBody("issue_create", `{"title":"x"}`)); w.Code != 401 {
		t.Fatal("帳號層級 token 不能用在組織端點", w.Code)
	}
	// refresh:不能升級;不帶 scope 沿用讀寫
	client := m["client"].(string)
	rt := m["refresh_token"].(string)
	if w := o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {rt}, "scope": {"mcp:read mcp:write admin"}}); w.Code != 400 || jsonBody(t, w)["error"] != "invalid_scope" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {rt}})
	if w.Code != 200 || jsonBody(t, w)["scope"] != "mcp:read mcp:write" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestOAuthWriteGrantFlowOrgEndpoint(t *testing.T) {
	o := newOAHTTP(t)
	m := o.tokenFor(tResource, rwReq, "write")
	access := m["access_token"].(string)
	w := o.mcpPost("/mcp/org/o", access, callBody("issue_create", `{"title":"組織端點讀寫"}`))
	if w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = o.mcpPost("/mcp/org/o", access, callBody("project_save", `{"id":"p1","name":"x"}`))
	if !strings.Contains(w.Body.String(), "This tool is not available over OAuth") {
		t.Fatal(w.Body.String())
	}
	// 統一入口不收組織 token
	if w := o.mcpPost("/mcp", access, callBody("issues", `{"org":"o"}`)); w.Code != 401 {
		t.Fatal("組織 token 不能用在 /mcp", w.Code)
	}
}

// 沒選讀寫(沒送 grant 或送 read)→ 唯讀;就算客戶端要求了 write
func TestOAuthWriteNotChosenStaysReadOnly(t *testing.T) {
	o := newOAHTTP(t)
	for _, grant := range []string{"", "read", "WRITE", "write ", "mcp:write"} {
		m := o.tokenFor(tUnified, rwReq, grant)
		if m["scope"] != "mcp:read" {
			t.Fatalf("grant=%q 要是唯讀:%v", grant, m)
		}
		w := o.mcpPost("/mcp", m["access_token"].(string), callBody("issue_create", `{"org":"o","title":"唯讀"}`))
		if !strings.Contains(w.Body.String(), "此連線是唯讀授權") {
			t.Fatalf("grant=%q:%s", grant, w.Body.String())
		}
	}
}

// 偽造:客戶端沒要求 write 卻送 grant=write → 400、不發授權碼;改隱藏的 scope 欄位 → CSRF 綁定失敗 403
func TestOAuthWriteForgedForm(t *testing.T) {
	o := newOAHTTP(t)
	client := o.register()
	csrf, _ := o.consentScope(client, tUnified, "mcp:read offline_access")
	w := o.approveScope(client, csrf, tUnified, "mcp:read offline_access", "write")
	if w.Code != 400 || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "沒有要求 mcp:write") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	// 改隱藏的 scope(從唯讀的畫面送出,scope 改成要求讀寫):CSRF 綁定的範圍不同 → 403
	client = o.register()
	csrf, _ = o.consentScope(client, tUnified, "mcp:read")
	w = o.approveScope(client, csrf, tUnified, rwReq, "write")
	if w.Code != 403 || w.Header().Get("Location") != "" {
		t.Fatal(w.Code, w.Body.String())
	}
	// 換 resource 也不行(綁定)
	client = o.register()
	csrf, _ = o.consentScope(client, tUnified, rwReq)
	if w := o.approveScope(client, csrf, tResource, rwReq, "write"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	// 拒絕:照舊導回 access_denied,沒有授權碼
	client = o.register()
	csrf, _ = o.consentScope(client, tUnified, rwReq)
	f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tUnified}, "scope": {rwReq}, "grant": {"write"}, "action": {"deny"}}
	w = o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded")
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "error=access_denied") || strings.Contains(w.Header().Get("Location"), "code=") {
		t.Fatal(w.Code, w.Header())
	}
	// 重複送出:CSRF 只能用一次
	client = o.register()
	csrf, _ = o.consentScope(client, tUnified, rwReq)
	if w := o.approveScope(client, csrf, tUnified, rwReq, "write"); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w := o.approveScope(client, csrf, tUnified, rwReq, "write"); w.Code != 403 {
		t.Fatal("重複送出要失效", w.Code)
	}
}

// 觀測 log:write token 呼叫白名單內的工具是 valid,白名單外是 tool_denied(唯讀 token 照舊 read_only_denied)
func TestOAuthWriteEventLog(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	o := newOAHTTP(t)
	access := o.tokenFor(tUnified, rwReq, "write")["access_token"].(string)
	o.mcpPost("/mcp", access, callBody("issue_create", `{"org":"o","title":"ARGSECRET"}`))
	o.mcpPost("/mcp", access, callBody("agent_upsert", `{"org":"o","id":"x","name":"x"}`))
	got := map[string]bool{}
	for _, l := range logLines(t, buf.String()) {
		if l["path"] == "/mcp" && l["rpc"] != nil {
			got[l["token_state"].(string)+"|"+l["rpc"].(string)] = true
		}
		if _, ok := l["_write"]; ok {
			t.Fatal("內部欄位不能進 log", l)
		}
	}
	if !got["valid|tools/call:issue_create"] || !got["tool_denied|tools/call:agent_upsert"] || strings.Contains(buf.String(), "ARGSECRET") || strings.Contains(buf.String(), access) {
		t.Fatal(got)
	}
}
