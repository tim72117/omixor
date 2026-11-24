package service_test

import (
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// ISS-54 第三階段:OAuth 寫入(scope mcp:write)。授予的 scope ≤ 客戶端請求 ∩ 使用者明確選擇;scope 在換 token、refresh 都不能升級;
// 寫入身分不是伺服器管理員。

const (
	rScope  = "mcp:read"
	rwScope = "mcp:read mcp:write"
)

func (e *oauthEnv) checkScope(cl *model.OAuthClient, scope string) *service.OAuthAuthChecked {
	e.t.Helper()
	c, err := e.svc.CheckOAuthAuthorize(service.OAuthAuthRequest{ClientID: cl.ID, RedirectURI: oaCallback, ResponseType: "code",
		CodeChallenge: oaChallenge(oaVerifier), CodeChallengeMethod: "S256", Resources: []string{oaAccount}, Account: true, Scope: scope})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// login 走完帳號層級授權:requested = 客戶端要求的 scope、grantWrite = 使用者在同意畫面選了讀寫
func (e *oauthEnv) loginScope(cl *model.OAuthClient, requested string, grantWrite bool) (*service.OAuthTokenResponse, error) {
	e.t.Helper()
	code, err := e.svc.ApproveOAuthScope(e.checkScope(cl, requested), oaChallenge(oaVerifier), e.pair(), "2.2.2.2", grantWrite)
	if err != nil {
		return nil, err
	}
	return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{oaAccount}}, "2.2.2.2")
}

func (e *oauthEnv) refreshScope(cl *model.OAuthClient, rt, scope string) (*service.OAuthTokenResponse, error) {
	return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "refresh_token", ClientID: cl.ID, RefreshToken: rt, Scope: scope}, "")
}

func TestOAuthWriteScopeDefaultsAndGrant(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	// 客戶端沒指定 scope:預設只給 mcp:read;選讀寫也被拒(沒有要求)
	tok, err := e.loginScope(cl, "", false)
	if err != nil || tok.Scope != rScope {
		t.Fatal(err, tok)
	}
	if p, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaAccount); err != nil || !p.ReadOnly || p.OAuthWrite() {
		t.Fatal(err, p)
	}
	// 客戶端要求 write,使用者沒選讀寫(預設唯讀):只給 read
	tok, err = e.loginScope(cl, rwScope, false)
	if err != nil || tok.Scope != rScope {
		t.Fatal("要求了 write 但使用者沒選,只能是唯讀", err, tok)
	}
	// 客戶端要求 write,使用者選讀寫:read + write
	tok, err = e.loginScope(cl, rwScope+" offline_access", true)
	if err != nil || tok.Scope != rwScope {
		t.Fatal(err, tok)
	}
	p, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaAccount)
	if err != nil || p.ReadOnly || !p.OAuthWrite() || p.Via != model.ViaOAuth {
		t.Fatal(err, p)
	}
	row, err := e.svc.Store.OAuthTokenByHash(model.OAuthKindAccess, service.HashToken("oauth:access:"+tok.AccessToken))
	if err != nil || row.Scope != rwScope {
		t.Fatal(err, row)
	}
	// REST 照舊拒絕(write token 也一樣)
	if _, err := e.svc.Authenticate(tok.AccessToken); service.Code(err) != 403 {
		t.Fatal("REST 要 403", err)
	}
	// 不認得的 scope 被忽略,不會變成授予
	tok, err = e.loginScope(cl, "admin mcp:everything", false)
	if err != nil || tok.Scope != rScope {
		t.Fatal(err, tok)
	}
}

// 表單偽造:客戶端沒有要求 mcp:write 卻送讀寫 → 400 invalid_scope,而且配對碼不消耗
func TestOAuthWriteForgedGrantRejected(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	pc := e.pair()
	chk := e.checkScope(cl, rScope)
	if chk.WriteRequested {
		t.Fatal("沒要求 write")
	}
	if _, err := e.svc.ApproveOAuthScope(chk, oaChallenge(oaVerifier), pc, "2.2.2.2", true); oaCode(err) != "invalid_scope" {
		t.Fatal("偽造的讀寫要被拒", err)
	}
	// 配對碼沒被消耗:改回唯讀可以用同一個碼
	if code, err := e.svc.ApproveOAuthScope(chk, oaChallenge(oaVerifier), pc, "2.2.2.2", false); err != nil || code == "" {
		t.Fatal("配對碼不該被偽造請求消耗", err)
	}
	// 舊的 ApproveOAuth(沒有 scope 參數)永遠是唯讀
	chk = e.checkScope(cl, rwScope)
	code, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), e.pair(), "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{oaAccount}, Scope: rwScope}, "")
	if err != nil || tok.Scope != rScope {
		t.Fatal("token 請求帶 scope 也不能升級", err, tok)
	}
}

// 唯讀授權碼換出來的 token 不能寫;refresh 不能升級;窄化後不能再升回去;寫入族 refresh 沿用寫入
func TestOAuthWriteRefreshCannotEscalate(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	ro, err := e.loginScope(cl, rwScope, false)
	if err != nil || ro.Scope != rScope {
		t.Fatal(err, ro)
	}
	for _, sc := range []string{rwScope, "mcp:write", "mcp:read mcp:write offline_access", "admin"} {
		if _, err := e.refreshScope(cl, ro.RefreshToken, sc); oaCode(err) != "invalid_scope" {
			t.Fatalf("唯讀的 refresh 帶 %q 要 invalid_scope:%v", sc, err)
		}
	}
	// 帶同樣或更窄的 scope、沒帶:仍是唯讀
	ro2, err := e.refreshScope(cl, ro.RefreshToken, "mcp:read offline_access")
	if err != nil || ro2.Scope != rScope {
		t.Fatal(err, ro2)
	}
	if p, err := e.svc.AuthenticateOAuth(ro2.AccessToken, oaAccount); err != nil || !p.ReadOnly {
		t.Fatal(err, p)
	}

	rw, err := e.loginScope(cl, rwScope, true)
	if err != nil || rw.Scope != rwScope {
		t.Fatal(err, rw)
	}
	rw2, err := e.refreshScope(cl, rw.RefreshToken, "") // 沒帶 = 沿用
	if err != nil || rw2.Scope != rwScope {
		t.Fatal("寫入族 refresh 沿用 scope", err, rw2)
	}
	if p, err := e.svc.AuthenticateOAuth(rw2.AccessToken, oaAccount); err != nil || !p.OAuthWrite() {
		t.Fatal(err, p)
	}
	rw3, err := e.refreshScope(cl, rw2.RefreshToken, rwScope) // 帶同樣的 = 沿用
	if err != nil || rw3.Scope != rwScope {
		t.Fatal(err, rw3)
	}
	narrow, err := e.refreshScope(cl, rw3.RefreshToken, "mcp:read") // 窄化
	if err != nil || narrow.Scope != rScope {
		t.Fatal("可以縮小成唯讀", err, narrow)
	}
	if p, err := e.svc.AuthenticateOAuth(narrow.AccessToken, oaAccount); err != nil || !p.ReadOnly {
		t.Fatal(err, p)
	}
	if _, err := e.refreshScope(cl, narrow.RefreshToken, rwScope); oaCode(err) != "invalid_scope" {
		t.Fatal("窄化後不能再升回去", err)
	}
}

// write token 的撤銷:refresh 重用 → 整族撤銷;停用帳號 → 403;resource 綁定不變
func TestOAuthWriteRevocationAndBinding(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	rw, err := e.loginScope(cl, rwScope, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range []string{oaResource, "https://omixor.test/mcp/org/other", "https://omixor.test", ""} {
		if _, err := e.svc.AuthenticateOAuth(rw.AccessToken, res); err != service.ErrOAuthInvalidToken {
			t.Fatalf("write token 不能用在 %q:%v", res, err)
		}
	}
	rw2, err := e.refreshScope(cl, rw.RefreshToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.refreshScope(cl, rw.RefreshToken, ""); oaCode(err) != "invalid_grant" {
		t.Fatal("重用要 invalid_grant", err)
	}
	if _, err := e.svc.AuthenticateOAuth(rw2.AccessToken, oaAccount); err == nil {
		t.Fatal("重用偵測要撤銷整個家族(含寫入的 access token)")
	}
	rw3, err := e.loginScope(cl, rwScope, true)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := e.svc.Store.UserByID("u1")
	u.Disabled = true
	_ = e.svc.Store.SaveUser(*u)
	if _, err := e.svc.AuthenticateOAuth(rw3.AccessToken, oaAccount); service.Code(err) != 403 {
		t.Fatal("停用的帳號要 403", err)
	}
	if _, err := e.refreshScope(cl, rw3.RefreshToken, ""); oaCode(err) != "invalid_grant" {
		t.Fatal("停用的帳號不能 refresh", err)
	}
}

// CSRF token 綁定這一次授權請求:換 scope 範圍、challenge、resource 都驗證失敗;一次性
func TestOAuthCSRFBoundToRequest(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	ro, rw := e.checkScope(cl, rScope), e.checkScope(cl, rwScope)
	ch := oaChallenge(oaVerifier)
	mk := func(c *service.OAuthAuthChecked) string {
		x, err := e.svc.NewOAuthCSRF(c, ch, "2.2.2.2")
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	if x := mk(ro); e.svc.VerifyOAuthCSRF(x, rw, ch) {
		t.Fatal("唯讀請求的 CSRF 不能拿去送要求讀寫的請求(隱藏欄位 scope 被改)")
	}
	if x := mk(rw); e.svc.VerifyOAuthCSRF(x, ro, ch) {
		t.Fatal("scope 範圍不同要失敗")
	}
	if x := mk(ro); e.svc.VerifyOAuthCSRF(x, ro, oaChallenge(strings.Repeat("y", 50))) {
		t.Fatal("challenge 不同要失敗")
	}
	org := e.check(cl, "o")
	if x := mk(ro); e.svc.VerifyOAuthCSRF(x, org, ch) {
		t.Fatal("resource 不同要失敗")
	}
	x := mk(rw)
	if !e.svc.VerifyOAuthCSRF(x, rw, ch) || e.svc.VerifyOAuthCSRF(x, rw, ch) {
		t.Fatal("相同請求通過一次,不能重複")
	}
}

// 稽核:oauth.authorize 帶 scope;write 身分的寫入稽核來源是 oauth
func TestOAuthWriteAuditScopeAndVia(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	if _, err := e.loginScope(cl, rwScope, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.loginScope(cl, rwScope, false); err != nil {
		t.Fatal(err)
	}
	admin := &model.Principal{User: model.User{ID: "root", Role: "admin"}}
	_ = e.svc.Store.SaveUser(model.User{ID: "root", Email: "root@x", Role: "admin", CreatedAt: model.Now()})
	list, err := e.svc.ListAudit(admin, 100)
	if err != nil {
		t.Fatal(err)
	}
	var scopes []string
	for _, a := range list {
		if a.Action == "oauth.authorize" {
			s, _ := a.Detail["scope"].(string)
			scopes = append(scopes, s)
			if a.Source != model.ViaOAuth {
				t.Fatal("來源要是 oauth", a)
			}
		}
	}
	if len(scopes) != 2 || !((scopes[0] == rwScope && scopes[1] == rScope) || (scopes[1] == rwScope && scopes[0] == rScope)) {
		t.Fatal(scopes)
	}
	// 寫入:以 write 身分建立 Issue,稽核的 Source = oauth
	tok, err := e.loginScope(cl, rwScope, true)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaAccount)
	if err != nil {
		t.Fatal(err)
	}
	iss, err := e.svc.CreateIssue(p, "o", service.IssueInput{Title: "經 OAuth 寫入"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ = e.svc.ListAudit(admin, 100)
	found := false
	for _, a := range list {
		if strings.HasPrefix(a.Action, "issue.") && a.Source == model.ViaOAuth && strings.Contains(a.Org+" "+iss.ID, "o") {
			found = true
		}
	}
	if !found {
		t.Fatal("OAuth 寫入的稽核要帶 source=oauth", list)
	}
}

// 伺服器管理員的 OAuth 寫入身分以一般成員運作:不是成員的組織進不去、不能管理使用者、不能建立組織、檢核資料庫、不能產生配對碼;唯讀授權照舊
func TestOAuthWriteServerAdminCapped(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	_ = e.svc.Store.SaveUser(model.User{ID: "root", Email: "root@x", Role: "admin", CreatedAt: model.Now()})
	_ = e.svc.Store.CreateToken(model.Token{ID: "t_root", UserID: "root", Device: "Mac", CreatedAt: model.Now(), Hash: service.HashToken("omx_root")})
	rootLogin, err := e.svc.Authenticate("omx_root")
	if err != nil || !rootLogin.ServerAdmin() {
		t.Fatal(err)
	}
	pc, err := e.svc.CreatePairCode(rootLogin)
	if err != nil {
		t.Fatal(err)
	}
	// 組織層級的讀寫授權:root 不是 o 的成員 → 被拒(沒有萬能權限);唯讀授權照舊可以(行為不變)
	if _, err := e.svc.ApproveOAuthScope(e.checkScope2(cl, "o", rwScope), oaChallenge(oaVerifier), pc.Code, "2.2.2.2", true); oaCode(err) != "access_denied" {
		t.Fatal("伺服器管理員不是成員,讀寫授權要被拒", err)
	}
	pc, _ = e.svc.CreatePairCode(rootLogin)
	if _, err := e.svc.ApproveOAuthScope(e.checkScope2(cl, "o", rwScope), oaChallenge(oaVerifier), pc.Code, "2.2.2.2", false); err != nil {
		t.Fatal("唯讀授權的行為不變", err)
	}
	// 帳號層級讀寫授權:成功,但身分不是 ServerAdmin
	pc, _ = e.svc.CreatePairCode(rootLogin)
	code, err := e.svc.ApproveOAuthScope(e.checkScope(cl, rwScope), oaChallenge(oaVerifier), pc.Code, "2.2.2.2", true)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{oaAccount}}, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaAccount)
	if err != nil || p.ServerAdmin() || p.User.Role != "admin" {
		t.Fatal(err, p.ServerAdmin())
	}
	orgs, err := e.svc.ListOrgs(p)
	if err != nil || len(orgs) != 0 {
		t.Fatal("root 不是任何組織的成員,OAuth 寫入身分看不到組織", orgs, err)
	}
	if _, err := e.svc.CreateIssue(p, "o", service.IssueInput{Title: "x"}); service.Code(err) != 403 {
		t.Fatal("非成員組織不能寫入", err)
	}
	if _, err := e.svc.CreateOrgWith(p, model.Org{ID: "evil", Name: "evil"}); service.Code(err) != 403 {
		t.Fatal("不能建立組織", err)
	}
	if _, err := e.svc.SchemaCheck(p); service.Code(err) != 403 {
		t.Fatal("不能做伺服器管理的檢核", err)
	}
	if _, err := e.svc.ListUsers(p); service.Code(err) != 403 {
		t.Fatal("不能管理使用者", err)
	}
	if _, err := e.svc.SaveUser(p, "x@x", nil, nil); service.Code(err) != 403 {
		t.Fatal("不能管理使用者", err)
	}
	if _, err := e.svc.ListTokens(p); service.Code(err) != 403 {
		t.Fatal("不能管理 token", err)
	}
	if _, err := e.svc.CreatePairCode(p); err == nil {
		t.Fatal("OAuth 連線不能建立配對碼")
	}
	// 唯讀授權的伺服器管理員:行為不變(看得到全部組織)
	ro, err := e.loginAs(cl, rootLogin, rScope, false)
	if err != nil {
		t.Fatal(err)
	}
	pr, _ := e.svc.AuthenticateOAuth(ro.AccessToken, oaAccount)
	if orgs, err := e.svc.ListOrgs(pr); err != nil || len(orgs) == 0 || !pr.ReadOnly {
		t.Fatal("唯讀授權不變", orgs, err)
	}
}

func (e *oauthEnv) checkScope2(cl *model.OAuthClient, org, scope string) *service.OAuthAuthChecked {
	e.t.Helper()
	c, err := e.svc.CheckOAuthAuthorize(service.OAuthAuthRequest{ClientID: cl.ID, RedirectURI: oaCallback, ResponseType: "code",
		CodeChallenge: oaChallenge(oaVerifier), CodeChallengeMethod: "S256", Resources: []string{oaResource}, Org: org, Scope: scope})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *oauthEnv) loginAs(cl *model.OAuthClient, who *model.Principal, requested string, grantWrite bool) (*service.OAuthTokenResponse, error) {
	pc, err := e.svc.CreatePairCode(who)
	if err != nil {
		return nil, err
	}
	code, err := e.svc.ApproveOAuthScope(e.checkScope(cl, requested), oaChallenge(oaVerifier), pc.Code, "2.2.2.2", grantWrite)
	if err != nil {
		return nil, err
	}
	return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{oaAccount}}, "")
}

// 寫入仍照授權表:一般成員(member)能建 Issue、不能做組織管理員的事;退出組織後立刻失效
func TestOAuthWriteFollowsMembership(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	tok, err := e.loginScope(cl, rwScope, true)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaAccount)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateIssue(p, "o", service.IssueInput{Title: "成員可以寫"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveMember(p, "o", "someone@x", "member"); service.Code(err) != 403 {
		t.Fatal("一般成員不能管理成員", err)
	}
	if err := e.svc.Store.RemoveMember("o", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateIssue(p, "o", service.IssueInput{Title: "退出後"}); service.Code(err) != 403 {
		t.Fatal("退出組織後要立刻失效", err)
	}
	_ = time.Second
}
