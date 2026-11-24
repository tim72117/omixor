package service_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// OAuth 組織管理員授權(mcp:admin):授予條件(客戶端要求、帳號層級、使用者是某組織的管理員)、scope 解析、refresh 只能縮不能升、仍不是伺服器管理員。

const adminScope = "mcp:read mcp:write mcp:admin"

func (e *oauthEnv) loginGrant(cl *model.OAuthClient, requested, grant string) (*service.OAuthTokenResponse, error) {
	e.t.Helper()
	code, err := e.svc.ApproveOAuthGrant(e.checkScope(cl, requested), oaChallenge(oaVerifier), e.pair(), "2.2.2.2", grant)
	if err != nil {
		return nil, err
	}
	return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{oaAccount}}, "2.2.2.2")
}

func TestOAuthAdminGrantRules(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()

	// 要求 mcp:admin 也算要求寫入;AdminRequested 只在帳號層級
	chk := e.checkScope(cl, "mcp:admin")
	if !chk.AdminRequested || !chk.WriteRequested {
		t.Fatalf("%+v", chk)
	}
	if c := e.checkScope(cl, rwScope); c.AdminRequested {
		t.Fatal("沒要求 admin")
	}
	org := e.check(cl, "o") // 組織層級的授權請求
	if org.AdminRequested {
		t.Fatal("組織層級不支援 admin")
	}

	// 沒要求 mcp:admin 卻送 admin = 偽造:invalid_scope,配對碼不消耗
	pc := e.pair()
	if _, err := e.svc.ApproveOAuthGrant(e.checkScope(cl, rwScope), oaChallenge(oaVerifier), pc, "2.2.2.2", service.OAuthGrantAdmin); oaCode(err) != "invalid_scope" {
		t.Fatal(err)
	}
	if _, err := e.svc.ApproveOAuthGrant(org, oaChallenge(oaVerifier), pc, "2.2.2.2", service.OAuthGrantAdmin); oaCode(err) != "invalid_scope" {
		t.Fatal("組織層級授權要不到 admin:", err)
	}

	// u1 目前只是成員:選 admin 被拒(access_denied、not_org_admin);選讀寫、唯讀仍可
	if _, err := e.loginGrant(cl, adminScope, service.OAuthGrantAdmin); err != service.ErrOAuthNotOrgAdmin {
		t.Fatal("非組織管理員不能授予 admin:", err)
	}
	if tok, err := e.loginGrant(cl, adminScope, service.OAuthGrantWrite); err != nil || tok.Scope != rwScope {
		t.Fatal(err, tok)
	}
	if tok, err := e.loginGrant(cl, adminScope, service.OAuthGrantRead); err != nil || tok.Scope != rScope {
		t.Fatal(err, tok)
	}

	// 升為組織管理員:授予 mcp:read mcp:write mcp:admin;身分是 OAuth 寫入 + 管理員,不是伺服器管理員
	_ = e.svc.Store.SaveMember(model.Member{Org: "o", UserID: "u1", Role: "admin"})
	tok, err := e.loginGrant(cl, adminScope+" offline_access", service.OAuthGrantAdmin)
	if err != nil || tok.Scope != adminScope {
		t.Fatal(err, tok)
	}
	p, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaAccount)
	if err != nil || p.ReadOnly || !p.OAuthWrite() || !p.OAuthAdminGrant() || p.ServerAdmin() {
		t.Fatal(err, p)
	}
	// 伺服器管理員 + admin 授權:ServerAdmin 仍是 false
	p.User.Role = "admin"
	if p.ServerAdmin() {
		t.Fatal("OAuth 管理員授權不是伺服器管理員")
	}
	// 舊的 bool 介面:讀寫仍然只是 mcp:write,沒有 admin
	if code, err := e.svc.ApproveOAuthScope(e.checkScope(cl, adminScope), oaChallenge(oaVerifier), e.pair(), "2.2.2.2", true); err != nil || code == "" {
		t.Fatal(err)
	}
	// 沒選 admin 就沒有 admin
	tok2, err := e.loginGrant(cl, adminScope, service.OAuthGrantWrite)
	if err != nil || tok2.Scope != rwScope {
		t.Fatal(err, tok2)
	}
	if p2, err := e.svc.AuthenticateOAuth(tok2.AccessToken, oaAccount); err != nil || p2.OAuthAdminGrant() {
		t.Fatal(err, p2)
	}

	// refresh:沿用 admin;可以縮成讀寫或唯讀(之後升不回去);讀寫的 refresh 要不到 admin
	r1, err := e.refreshScope(cl, tok.RefreshToken, "")
	if err != nil || r1.Scope != adminScope {
		t.Fatal(err, r1)
	}
	r2, err := e.refreshScope(cl, r1.RefreshToken, "mcp:read mcp:write")
	if err != nil || r2.Scope != rwScope {
		t.Fatal("縮成讀寫:", err, r2)
	}
	if _, err := e.refreshScope(cl, r2.RefreshToken, adminScope); oaCode(err) != "invalid_scope" {
		t.Fatal("縮小後不能升回 admin:", err)
	}
	if _, err := e.refreshScope(cl, tok2.RefreshToken, "mcp:admin"); oaCode(err) != "invalid_scope" {
		t.Fatal("讀寫的 refresh 不能升到 admin:", err)
	}
}
