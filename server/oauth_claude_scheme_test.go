package server

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

const tClaudeSDK = "claude://claude.ai/mcp-auth-callback/sdk"

// Claude desktop client: register with the claude:// callback and scope "mcp:read mcp:write offline_access",
// then authorize -> token -> refresh over HTTP. offline_access must never error or appear in a granted scope.
func TestOAuthClaudeSDKCallbackHTTP(t *testing.T) {
	o := newOAHTTP(t)
	reg := func(uri string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]any{"client_name": "Claude", "redirect_uris": []string{uri}, "token_endpoint_auth_method": "none",
			"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "scope": "mcp:read mcp:write offline_access"})
		w := o.do("POST", "/oauth/register", string(b), "Content-Type", "application/json")
		return w.Code, jsonBody(t, w)
	}
	for _, bad := range []string{tClaudeSDK + "/", "claude://claude.ai/mcp-auth-callback", "claude://claude.ai/mcp-auth-callback/sdk/x",
		"claude://evil/mcp-auth-callback/sdk", "CLAUDE://claude.ai/mcp-auth-callback/sdk", "claude://CLAUDE.AI/mcp-auth-callback/sdk", "evil://claude.ai/mcp-auth-callback/sdk"} {
		if code, m := reg(bad); code != 400 || m["error"] != "invalid_redirect_uri" {
			t.Errorf("%q must be rejected: %d %v", bad, code, m)
		}
	}
	code, m := reg(tClaudeSDK)
	if code != 201 {
		t.Fatal(code, m)
	}
	client := m["client_id"].(string)
	if uris, _ := m["redirect_uris"].([]any); len(uris) != 1 || uris[0] != tClaudeSDK {
		t.Fatal(m)
	}
	query := func(redirect, scope string) string {
		return authQuery(client, "redirect_uri", redirect, "resource", tUnified, "scope", scope)
	}
	// Unregistered redirect (e.g. the https callback) is shown on the page, never redirected to.
	if w := o.do("GET", "/oauth/authorize?"+query(tCallback, "mcp:read"), ""); w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatal(w.Code)
	}
	for _, scope := range []string{"mcp:read mcp:write offline_access", "mcp:read offline_access", "offline_access"} {
		w := o.do("GET", "/oauth/authorize?"+query(tClaudeSDK, scope), "")
		if w.Code != 200 {
			t.Fatal(scope, w.Code, w.Body.String())
		}
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "claude://claude.ai") {
			t.Fatal("form-action must allow the claude:// redirect target", csp)
		}
		csrf := csrfRE.FindStringSubmatch(w.Body.String())[1]
		wantWrite := strings.Contains(scope, "mcp:write")
		grant := "read"
		if wantWrite {
			grant = "write"
		}
		f := url.Values{"csrf": {csrf}, "client_id": {client}, "redirect_uri": {tClaudeSDK}, "response_type": {"code"}, "code_challenge": {challenge()},
			"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tUnified}, "scope": {scope}, "pairing_code": {o.pair()}, "action": {"allow"}, "grant": {grant}}
		aw := o.do("POST", "/oauth/authorize", f.Encode(), "Content-Type", "application/x-www-form-urlencoded")
		loc, _ := url.Parse(aw.Header().Get("Location"))
		if aw.Code != 303 || loc.Scheme != "claude" || loc.Host != "claude.ai" || loc.Path != "/mcp-auth-callback/sdk" || loc.Query().Get("state") != "st-1" || !strings.HasPrefix(loc.Query().Get("code"), "omc_") {
			t.Fatal(scope, aw.Code, aw.Header())
		}
		tf := url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {loc.Query().Get("code")}, "redirect_uri": {tClaudeSDK},
			"code_verifier": {tVerifier}, "resource": {tUnified}}
		tw := o.token(tf)
		tm := jsonBody(t, tw)
		want := "mcp:read"
		if wantWrite {
			want = "mcp:read mcp:write"
		}
		if tw.Code != 200 || tm["scope"] != want || strings.Contains(tw.Body.String(), "offline_access") {
			t.Fatal(scope, tw.Code, tw.Body.String())
		}
		// Refresh with offline_access: tolerated, scope unchanged, no escalation from read.
		rw := o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {tm["refresh_token"].(string)}, "scope": {"mcp:read offline_access"}})
		rm := jsonBody(t, rw)
		if rw.Code != 200 || rm["scope"] != "mcp:read" || strings.Contains(rw.Body.String(), "offline_access") {
			t.Fatal(scope, rw.Code, rw.Body.String())
		}
		if !wantWrite {
			xw := o.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {rm["refresh_token"].(string)}, "scope": {"mcp:read mcp:write offline_access"}})
			if xw.Code != 400 || jsonBody(t, xw)["error"] != "invalid_scope" {
				t.Fatal("refresh must not escalate", xw.Code, xw.Body.String())
			}
		}
	}
	// A different client registered with only the https callback cannot use the claude:// redirect.
	other := o.register()
	if w := o.do("GET", "/oauth/authorize?"+authQuery(other, "redirect_uri", tClaudeSDK, "resource", tUnified), ""); w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatal(w.Code)
	}
}
