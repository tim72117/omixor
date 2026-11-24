package service_test

import (
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// 帳號層級授權(resource = <public>/mcp,統一入口):token 不綁組織(Org 空字串),resource 綁定仍然嚴格

const oaAccount = "https://omixor.test/mcp"

func (e *oauthEnv) checkAccount(cl *model.OAuthClient) (*service.OAuthAuthChecked, error) {
	return e.svc.CheckOAuthAuthorize(service.OAuthAuthRequest{ClientID: cl.ID, RedirectURI: oaCallback, ResponseType: "code",
		CodeChallenge: oaChallenge(oaVerifier), CodeChallengeMethod: "S256", Resources: []string{oaAccount}, Account: true})
}

// accountLogin 走完帳號層級授權(配對碼 → 授權碼 → token)
func (e *oauthEnv) accountLogin(cl *model.OAuthClient) *service.OAuthTokenResponse {
	e.t.Helper()
	chk, err := e.checkAccount(cl)
	if err != nil {
		e.t.Fatal(err)
	}
	if !chk.Account || chk.Org != "" || chk.Resource != oaAccount {
		e.t.Fatalf("%+v", chk)
	}
	code, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), e.pair(), "2.2.2.2")
	if err != nil {
		e.t.Fatal(err)
	}
	tok, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{oaAccount}}, "2.2.2.2")
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func TestOAuthAccountLevelIssueAndBinding(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	acct := e.accountLogin(cl)
	// 用在統一入口(/mcp):成功,唯讀身分
	p, err := e.svc.AuthenticateOAuth(acct.AccessToken, oaAccount)
	if err != nil || !p.ReadOnly || p.Via != model.ViaOAuth || p.User.ID != "u1" {
		t.Fatal(err, p)
	}
	// 存下來的 token 列 Org 是空字串(不代表「所有組織」;組織存取在統一入口以即時成員資格決定)
	row, err := e.svc.Store.OAuthTokenByHash(model.OAuthKindAccess, service.HashToken("oauth:access:"+acct.AccessToken))
	if err != nil || row.Org != "" || row.Resource != oaAccount || row.Scope != model.OAuthScopeRead {
		t.Fatal(err, row)
	}
	// 用在組織端點:失敗
	for _, res := range []string{oaResource, "https://omixor.test/mcp/org/other", "https://omixor.test/mcp/", "https://omixor.test/mcp/foo", "https://omixor.test", "https://evil.example/mcp", ""} {
		if _, err := e.svc.AuthenticateOAuth(acct.AccessToken, res); err != service.ErrOAuthInvalidToken {
			t.Fatalf("帳號層級 token 不能用在 %q:%v", res, err)
		}
	}
	// 組織 token 用在統一入口:失敗(用在自己的組織端點仍成功)
	org, err := e.exchange(cl, e.authorize(cl))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthenticateOAuth(org.AccessToken, oaAccount); err != service.ErrOAuthInvalidToken {
		t.Fatal("組織 token 不能用在 /mcp", err)
	}
	if _, err := e.svc.AuthenticateOAuth(org.AccessToken, oaResource); err != nil {
		t.Fatal(err)
	}
	// REST 仍一律 403
	if _, err := e.svc.Authenticate(acct.AccessToken); service.Code(err) != 403 {
		t.Fatal("REST 要 403", err)
	}
	// 觀測用的狀態
	if s := e.svc.OAuthAccessState(acct.AccessToken, oaAccount); s != "valid" {
		t.Fatal(s)
	}
	if s := e.svc.OAuthAccessState(acct.AccessToken, oaResource); s != "wrong_resource" {
		t.Fatal(s)
	}
	if s := e.svc.OAuthAccessState(org.AccessToken, oaAccount); s != "wrong_resource" {
		t.Fatal(s)
	}
}

// 帳號層級授權不需要是任何組織的成員(身分由配對碼證明);組織授權仍然要是成員
func TestOAuthAccountApproveNeedsNoMembership(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	_ = e.svc.Store.SaveUser(model.User{ID: "u2", Email: "loner@x", Role: "member", CreatedAt: model.Now()})
	_ = e.svc.Store.CreateToken(model.Token{ID: "t_u2", UserID: "u2", Device: "Mac", CreatedAt: model.Now(), Hash: service.HashToken("omx_u2")})
	p2, err := e.svc.Authenticate("omx_u2")
	if err != nil {
		t.Fatal(err)
	}
	pair := func() string {
		pc, err := e.svc.CreatePairCode(p2)
		if err != nil {
			t.Fatal(err)
		}
		return pc.Code
	}
	// 組織授權:u2 不是 o 的成員,拒絕
	if _, err := e.svc.ApproveOAuth(e.check(cl, "o"), oaChallenge(oaVerifier), pair(), "2.2.2.2"); oaCode(err) != "access_denied" {
		t.Fatal(err)
	}
	// 帳號層級:可以授權;但之後查組織仍由即時成員資格決定,token 本身不給任何組織
	chk, err := e.checkAccount(cl)
	if err != nil {
		t.Fatal(err)
	}
	code, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), pair(), "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback, CodeVerifier: oaVerifier}, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaAccount)
	if err != nil || p.User.ID != "u2" {
		t.Fatal(err, p)
	}
	orgs, err := e.svc.ListOrgs(p)
	if err != nil || len(orgs) != 0 {
		t.Fatal("非成員看不到任何組織", orgs, err)
	}
	// 配對碼錯:一樣 ErrOAuthPairInvalid;配對碼只能用一次
	if _, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), "ZZZZ-ZZZZ", "2.2.2.2"); err != service.ErrOAuthPairInvalid {
		t.Fatal(err)
	}
}

func TestOAuthAccountExpiryRevokeRefresh(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	tok := e.accountLogin(cl)
	// refresh 保持 resource:換出來的 access 仍綁 /mcp、Org 仍是空
	refresh := func(rt string, res ...string) (*service.OAuthTokenResponse, error) {
		return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "refresh_token", ClientID: cl.ID, RefreshToken: rt, Resources: res}, "")
	}
	// resource 帶別的(組織端點、不認得的):invalid_target,不輪替
	for _, res := range []string{oaResource, "https://omixor.test/mcp/foo", "https://evil.example/mcp"} {
		if _, err := refresh(tok.RefreshToken, res); oaCode(err) != "invalid_target" {
			t.Fatalf("refresh resource %q:%v", res, err)
		}
	}
	tok2, err := refresh(tok.RefreshToken, oaAccount)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthenticateOAuth(tok2.AccessToken, oaAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthenticateOAuth(tok2.AccessToken, oaResource); err != service.ErrOAuthInvalidToken {
		t.Fatal("refresh 後仍不能用在組織端點", err)
	}
	row, err := e.svc.Store.OAuthTokenByHash(model.OAuthKindAccess, service.HashToken("oauth:access:"+tok2.AccessToken))
	if err != nil || row.Org != "" || row.Resource != oaAccount {
		t.Fatal(err, row)
	}
	// 過期
	*e.now = e.now.Add(service.OAuthAccessTTL + time.Second)
	if _, err := e.svc.AuthenticateOAuth(tok2.AccessToken, oaAccount); err != service.ErrOAuthInvalidToken {
		t.Fatal("過期要被拒", err)
	}
	// 撤銷(舊 refresh 重用 → 整個家族撤銷)
	tok3, err := refresh(tok2.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refresh(tok2.RefreshToken); oaCode(err) != "invalid_grant" {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthenticateOAuth(tok3.AccessToken, oaAccount); err != service.ErrOAuthInvalidToken {
		t.Fatal("撤銷後要被拒", err)
	}
	// 帳號停用:403
	tok4 := e.accountLogin(cl)
	u, _ := e.svc.Store.UserByID("u1")
	u.Disabled = true
	_ = e.svc.Store.SaveUser(*u)
	if _, err := e.svc.AuthenticateOAuth(tok4.AccessToken, oaAccount); service.Code(err) != 403 {
		t.Fatal(err)
	}
}

// 授權請求的 resource 把關:只認 /mcp(Account)或組織 MCP 網址(Org);其他一律 invalid_target
func TestOAuthAccountCheckInvalidTarget(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	base := service.OAuthAuthRequest{ClientID: cl.ID, RedirectURI: oaCallback, ResponseType: "code", CodeChallenge: oaChallenge(oaVerifier), CodeChallengeMethod: "S256"}
	cases := map[string]func(r *service.OAuthAuthRequest){
		"不認得的 resource(HTTP 層沒解出 Org、也不是 Account)": func(r *service.OAuthAuthRequest) { r.Resources = []string{"https://omixor.test/mcp/foo"} },
		"別的主機":                func(r *service.OAuthAuthRequest) { r.Resources = []string{"https://evil.example/mcp"} },
		"沒有 resource":         func(r *service.OAuthAuthRequest) { r.Account = true },
		"多個 resource":         func(r *service.OAuthAuthRequest) { r.Resources, r.Account = []string{oaAccount, oaAccount}, true },
		"Org 與 Account 不能同時設": func(r *service.OAuthAuthRequest) { r.Resources, r.Account, r.Org = []string{oaAccount}, true, "o" },
		"Org 與 Account 都沒設":   func(r *service.OAuthAuthRequest) { r.Resources = []string{oaAccount} },
	}
	for name, mod := range cases {
		r := base
		mod(&r)
		if _, err := e.svc.CheckOAuthAuthorize(r); oaCode(err) != "invalid_target" {
			t.Errorf("%s:%v", name, err)
		}
	}
	// 授權碼換 token 時帶不同的 resource:invalid_target
	chk, _ := e.checkAccount(cl)
	code, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), e.pair(), "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{oaResource}}, ""); oaCode(err) != "invalid_target" {
		t.Fatal(err)
	}
}
