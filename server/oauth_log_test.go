package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"testing"
	"time"
)

// 觀測 log 的去識別守門測試:走完 register → authorize(錯的配對碼、拒絕、成功)→ token → refresh(含重用)→ MCP,
// 已知的機密(配對碼、授權碼、code_verifier、CSRF、state、refresh / access token、tool 參數)一個字都不能出現在 log,預期的非機密欄位要出現
func TestOAuthEventLogRedaction(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	o := newOAHTTP(t)
	client := o.register()
	csrf, _ := o.consent(client)

	// 錯的配對碼(wrong)、錯的 CSRF(bad_csrf)、拒絕(denied;要先換新的 csrf)
	wrongPair := "ZZZZ-9999"
	if w := o.approve(client, csrf, wrongPair, "allow"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := o.approve(client, "bad-csrf-SECRETVALUE", wrongPair, "allow"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	csrf2, _ := o.consent(client)
	if w := o.approve(client, csrf2, "", "deny"); w.Code != 303 {
		t.Fatal(w.Code)
	}

	// 成功授權
	csrf3, _ := o.consent(client)
	pair := o.pair()
	w := o.approve(client, csrf3, pair, "allow")
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	code := loc.Query().Get("code")

	// basic 認證被拒、code 換 token、refresh、refresh 重用、用新 token 打 MCP
	if w := o.do("POST", "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {code}}.Encode(),
		"Content-Type", "application/x-www-form-urlencoded", "Authorization", "Basic c2VjcmV0OnNlY3JldA=="); w.Code != 401 {
		t.Fatal(w.Code)
	}
	tw := o.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {code}, "redirect_uri": {tCallback}, "code_verifier": {tVerifier}, "resource": {tResource}})
	if tw.Code != 200 {
		t.Fatal(tw.Code, tw.Body.String())
	}
	m := jsonBody(t, tw)
	access, refresh := m["access_token"].(string), m["refresh_token"].(string)
	*o.now = o.now.Add(400 * time.Second)
	rw := o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}, "resource": {tResource}})
	if rw.Code != 200 {
		t.Fatal(rw.Code, rw.Body.String())
	}
	access2 := jsonBody(t, rw)["access_token"].(string)
	if w := o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	o.do("POST", "/mcp/org/o", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "Content-Type", "application/json") // 沒 token → 401,token_state=missing
	_ = access2
	secretArg := "TOPSECRET-ARG-VALUE"
	mcpPost := func(tok string, params map[string]any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
		o.do("POST", "/mcp/org/o", string(b), "Content-Type", "application/json", "Authorization", "Bearer "+tok)
	}
	mcpPost(access, map[string]any{"name": "issues", "arguments": map[string]any{"q": secretArg}}) // 已經因為 refresh 重用被撤銷 → revoked
	mcpPost("omo_neverissuedtoken1234567890", map[string]any{"name": "issues"})                    // invalid
	_ = o.rpc("omx_mac", "tools/list", nil)                                                        // omx_ 不記
	lines := logLines(t, buf.String())
	all := buf.String()

	for _, secret := range []string{pair, wrongPair, code, tVerifier, csrf, csrf2, csrf3, "bad-csrf-SECRETVALUE", "st-1", refresh, access, access2, secretArg, "c2VjcmV0OnNlY3JldA==", challenge()} {
		if strings.Contains(all, secret) {
			t.Errorf("log 含機密 %q", secret)
		}
	}
	if strings.Contains(all, "127.0.0.1") || strings.Contains(all, "192.0.2.1") {
		t.Error("log 含原始 IP")
	}
	if strings.Contains(all, "omx_mac") || strings.Contains(all, "Bearer") {
		t.Error("log 含標頭值")
	}

	find := func(path, key, val string) map[string]any {
		for _, l := range lines {
			if l["path"] == path && l[key] == val {
				return l
			}
		}
		t.Fatalf("找不到 %s %s=%s\n%s", path, key, val, all)
		return nil
	}
	for _, l := range lines {
		for _, k := range []string{"ts", "method", "path", "status", "duration_ms", "content_type", "user_agent", "ip_hash", "header_names"} {
			if _, ok := l[k]; !ok {
				t.Errorf("缺 %s:%v", k, l)
			}
		}
		if h, _ := l["ip_hash"].(string); len(h) != 8 {
			t.Errorf("ip_hash:%v", l["ip_hash"])
		}
	}
	reg := find("/oauth/register", "client_id", client)
	if reg["client_name"] != "Claude" || reg["token_endpoint_auth_method"] != "none" || reg["redirect_uris"].([]any)[0] != tCallback {
		t.Error(reg)
	}
	get := find("/oauth/authorize", "code_challenge_method", "S256")
	if get["resource_present"] != true || get["resource"] != tResource || get["code_challenge_present"] != true || get["state_present"] != true || get["scope"] != "mcp:read offline_access" {
		t.Error(get)
	}
	for _, res := range []string{"wrong", "bad_csrf", "denied", "ok"} {
		if p := find("/oauth/authorize", "pairing_result", res); p["txn_valid"] == nil || p["client_id"] != client {
			t.Error(p)
		}
	}
	if x := find("/oauth/authorize", "pairing_result", "bad_csrf"); x["txn_valid"] != false {
		t.Error(x)
	}
	if x := find("/oauth/token", "client_auth_method", "basic"); x["result"] != "error" || x["error"] != "invalid_client" {
		t.Error(x)
	}
	iss := find("/oauth/token", "result", "issued")
	if iss["grant_type"] != "authorization_code" || iss["code_present"] != true || iss["code_verifier_present"] != true || iss["resource"] != tResource ||
		iss["body_format"] != "form" || iss["client_auth_method"] != "none" || iss["expires_in"] != float64(600) {
		t.Error(iss)
	}
	ref := find("/oauth/token", "result", "refreshed")
	if ref["refresh_token_present"] != true || ref["access_remaining_s_at_refresh"] != float64(200) {
		t.Error(ref)
	}
	if x := find("/oauth/token", "result", "reuse_detected"); x["error"] != "invalid_grant" {
		t.Error(x)
	}
	if x := find("/mcp/org/o", "token_state", "revoked"); x["rpc"] != "tools/call:issues" {
		t.Error(x)
	}
	find("/mcp/org/o", "token_state", "invalid")
	find("/mcp/org/o", "token_state", "missing")
	for _, l := range lines {
		if l["path"] == "/mcp/org/o" && l["token_state"] == nil {
			t.Error("MCP 事件缺 token_state", l)
		}
	}
}

func logLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(s), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			continue // 其他非事件的 log 行
		}
		out = append(out, m)
	}
	return out
}

// 同一個 access token 的 valid / read_only_denied / wrong_resource / expired 狀態,以及 resources/read 的 uri,tool 參數不進 log
func TestOAuthEventLogMCPStates(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	o := newOAHTTP(t)
	access, _, _ := o.login()
	post := func(path, tok, method string, params any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		o.do("POST", path, string(b), "Content-Type", "application/json", "Authorization", "Bearer "+tok)
	}
	post("/mcp/org/o", access, "tools/call", map[string]any{"name": "issues", "arguments": map[string]any{"q": "ARGSECRET1"}})
	post("/mcp/org/o", access, "tools/call", map[string]any{"name": "issue_create", "arguments": map[string]any{"title": "ARGSECRET2"}})
	post("/mcp/org/o", access, "resources/read", map[string]any{"uri": "ui://omixor/flowchart"})
	post("/mcp/org/other", access, "tools/list", nil)
	*o.now = o.now.Add(700 * time.Second)
	post("/mcp/org/o", access, "tools/list", nil)

	all := buf.String()
	if strings.Contains(all, "ARGSECRET") || strings.Contains(all, access) {
		t.Error("log 含 tool 參數或 token")
	}
	got := map[string]bool{}
	for _, l := range logLines(t, all) {
		if l["path"] != "/mcp/org/o" && l["path"] != "/mcp/org/other" {
			continue
		}
		key := l["token_state"].(string) + "|" + l["rpc"].(string)
		if u, ok := l["uri"]; ok {
			key += "|" + u.(string)
		}
		got[key] = true
	}
	for _, want := range []string{"valid|tools/call:issues", "read_only_denied|tools/call:issue_create", "valid|resources/read|ui://omixor/flowchart",
		"wrong_resource|tools/list", "expired|tools/list"} {
		if !got[want] {
			t.Errorf("缺 %q;有 %v", want, got)
		}
	}
}
