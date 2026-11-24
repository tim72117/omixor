package service_test

import (
	"errors"
	"testing"

	"omixor-core/service"
)

const claudeSDKCallback = "claude://claude.ai/mcp-auth-callback/sdk"

// Claude desktop / Agent SDK custom-scheme callback: exact string only, no prefix or wildcard matching.
func TestOAuthClaudeSDKCallbackExactOnly(t *testing.T) {
	e := newOAuthEnv(t)
	reg := func(u string) error {
		_, err := e.svc.RegisterOAuthClient(service.OAuthClientInput{RedirectURIs: []any{u}}, "1.1.1.1")
		return err
	}
	if err := reg(claudeSDKCallback); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{
		"claude://claude.ai/mcp-auth-callback/sdk/", "claude://claude.ai/mcp-auth-callback", "claude://claude.ai/mcp-auth-callback/other",
		"claude://claude.ai/mcp-auth-callback/sdk/extra", "claude://claude.ai/mcp-auth-callback/sdk?x=1", "claude://claude.ai/mcp-auth-callback/sdk#f",
		"claude://evil/mcp-auth-callback/sdk", "claude://claude.ai.evil/mcp-auth-callback/sdk", "CLAUDE://claude.ai/mcp-auth-callback/sdk",
		"claude://CLAUDE.AI/mcp-auth-callback/sdk", "Claude://claude.ai/mcp-auth-callback/sdk", "claude-evil://claude.ai/mcp-auth-callback/sdk",
		"claude://u@claude.ai/mcp-auth-callback/sdk", "claude:///mcp-auth-callback/sdk", "myapp://claude.ai/mcp-auth-callback/sdk",
		" claude://claude.ai/mcp-auth-callback/sdk", "claude://claude.ai/mcp-auth-callback/SDK",
	} {
		if err := reg(u); oaCode(err) != "invalid_redirect_uri" {
			t.Errorf("%q must be rejected: %v", u, err)
		}
	}
	// One bad URI rejects the whole registration.
	if err := reg2(e, claudeSDKCallback, "claude://evil/x"); oaCode(err) != "invalid_redirect_uri" {
		t.Error(err)
	}
}

func reg2(e *oauthEnv, uris ...string) error {
	l := []any{}
	for _, u := range uris {
		l = append(l, u)
	}
	_, err := e.svc.RegisterOAuthClient(service.OAuthClientInput{RedirectURIs: l}, "1.1.1.1")
	return err
}

// Full authorize -> token -> refresh round trip with the claude:// callback; offline_access is never granted.
func TestOAuthClaudeSDKCallbackRoundTrip(t *testing.T) {
	e := newOAuthEnv(t)
	cl, err := e.svc.RegisterOAuthClient(service.OAuthClientInput{ClientName: "Claude", RedirectURIs: []any{claudeSDKCallback}, Scope: "mcp:read mcp:write offline_access"}, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	authz := func(redirect, scope string) (*service.OAuthAuthChecked, error) {
		return e.svc.CheckOAuthAuthorize(service.OAuthAuthRequest{ClientID: cl.ID, RedirectURI: redirect, ResponseType: "code",
			CodeChallenge: oaChallenge(oaVerifier), CodeChallengeMethod: "S256", Resources: []string{oaAccount}, Account: true, Scope: scope})
	}
	// The registered claude:// URI is used verbatim; any other redirect is untrusted (NoRedirect).
	for _, bad := range []string{oaCallback, claudeSDKCallback + "/", "claude://evil/mcp-auth-callback/sdk", "CLAUDE://claude.ai/mcp-auth-callback/sdk"} {
		_, err := authz(bad, rScope)
		var oe *service.OAuthError
		if oaCode(err) != "invalid_request" || !asOAuth(err, &oe) || !oe.NoRedirect {
			t.Errorf("%q: %v", bad, err)
		}
	}
	exchange := func(code, redirect string) (*service.OAuthTokenResponse, error) {
		return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: redirect,
			CodeVerifier: oaVerifier, Resources: []string{oaAccount}}, "2.2.2.2")
	}
	// Requested read+write+offline_access, user keeps the read-only default: read only, no offline_access, no error.
	c, err := authz(claudeSDKCallback, "mcp:read mcp:write offline_access")
	if err != nil || c.RedirectURI != claudeSDKCallback || !c.WriteRequested {
		t.Fatal(err, c)
	}
	code, err := e.svc.ApproveOAuthScope(c, oaChallenge(oaVerifier), e.pair(), "2.2.2.2", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exchange(code, oaCallback); oaCode(err) != "invalid_grant" { // wrong redirect at token time
		t.Fatal(err)
	}
	code, err = e.svc.ApproveOAuthScope(c, oaChallenge(oaVerifier), e.pair(), "2.2.2.2", false)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := exchange(code, claudeSDKCallback)
	if err != nil || tok.Scope != rScope || tok.RefreshToken == "" {
		t.Fatal(err, tok)
	}
	// Refresh: offline_access tolerated, scope stays read; cannot escalate to write.
	r, err := e.refreshScope(cl, tok.RefreshToken, "mcp:read offline_access")
	if err != nil || r.Scope != rScope {
		t.Fatal(err, r)
	}
	if _, err := e.refreshScope(cl, r.RefreshToken, "mcp:read mcp:write offline_access"); oaCode(err) != "invalid_scope" {
		t.Fatal("refresh must not escalate to write", err)
	}
	r2, err := e.refreshScope(cl, r.RefreshToken, "offline_access")
	if err != nil || r2.Scope != rScope {
		t.Fatal(err, r2)
	}
	// Only offline_access requested at authorize: read only, and write cannot be granted.
	c, err = authz(claudeSDKCallback, "offline_access")
	if err != nil || c.WriteRequested {
		t.Fatal(err, c)
	}
	if _, err := e.svc.ApproveOAuthScope(c, oaChallenge(oaVerifier), e.pair(), "2.2.2.2", true); oaCode(err) != "invalid_scope" {
		t.Fatal("write not requested must not be grantable", err)
	}
	// Read-only requested with offline_access: write still not grantable.
	c, err = authz(claudeSDKCallback, "mcp:read offline_access")
	if err != nil || c.WriteRequested {
		t.Fatal(err, c)
	}
	if _, err := e.svc.ApproveOAuthScope(c, oaChallenge(oaVerifier), e.pair(), "2.2.2.2", true); oaCode(err) != "invalid_scope" {
		t.Fatal(err)
	}
	// Explicit write grant still works and offline_access is not in the scope.
	c, _ = authz(claudeSDKCallback, "mcp:read mcp:write offline_access")
	code, err = e.svc.ApproveOAuthScope(c, oaChallenge(oaVerifier), e.pair(), "2.2.2.2", true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err = exchange(code, claudeSDKCallback)
	if err != nil || tok.Scope != rwScope {
		t.Fatal(err, tok)
	}
}

func asOAuth(err error, oe **service.OAuthError) bool { return errors.As(err, oe) }
