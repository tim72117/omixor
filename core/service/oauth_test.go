package service_test

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

const (
	oaCallback = "https://claude.ai/api/mcp/auth_callback"
	oaVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ0123456789" // 54 字元,合法的 PKCE verifier
	oaResource = "https://omixor.test/mcp/org/o"
)

func oaChallenge(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type oauthEnv struct {
	t   *testing.T
	svc *service.Service
	now *time.Time
	p   *model.Principal // 已登入的使用者 u1(組織 o 的成員)
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	st := storetest.NewWithOrg(t, "o")
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	svc := &service.Service{Store: st, TTL: 90 * 24 * time.Hour, LoginPerIP: 1000, LoginTotal: 1000, OAuthRegPerIP: 1000, OAuthRegTotal: 1000, Now: func() time.Time { return now }}
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: model.Now()})
	_ = st.SaveMember(model.Member{Org: "o", UserID: "u1", Role: "member"})
	_ = st.CreateToken(model.Token{ID: "t_mac", UserID: "u1", Device: "Mac", CreatedAt: model.Now(), Hash: service.HashToken("omx_mac")})
	p, err := svc.Authenticate("omx_mac")
	if err != nil {
		t.Fatal(err)
	}
	return &oauthEnv{t: t, svc: svc, now: &now, p: p}
}

func (e *oauthEnv) client() *model.OAuthClient {
	e.t.Helper()
	cl, err := e.svc.RegisterOAuthClient(service.OAuthClientInput{ClientName: "Claude", RedirectURIs: []any{oaCallback}}, "1.1.1.1")
	if err != nil {
		e.t.Fatal(err)
	}
	return cl
}

func (e *oauthEnv) check(cl *model.OAuthClient, org string) *service.OAuthAuthChecked {
	e.t.Helper()
	c, err := e.svc.CheckOAuthAuthorize(service.OAuthAuthRequest{ClientID: cl.ID, RedirectURI: oaCallback, ResponseType: "code",
		CodeChallenge: oaChallenge(oaVerifier), CodeChallengeMethod: "S256", Resources: []string{oaResource}, Org: org})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *oauthEnv) pair() string {
	e.t.Helper()
	pc, err := e.svc.CreatePairCode(e.p)
	if err != nil {
		e.t.Fatal(err)
	}
	return pc.Code
}

// authorize 走完「配對碼 → 授權碼」,回傳授權碼
func (e *oauthEnv) authorize(cl *model.OAuthClient) string {
	e.t.Helper()
	code, err := e.svc.ApproveOAuth(e.check(cl, "o"), oaChallenge(oaVerifier), e.pair(), "2.2.2.2")
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}

func (e *oauthEnv) exchange(cl *model.OAuthClient, code string) (*service.OAuthTokenResponse, error) {
	return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{oaResource}}, "2.2.2.2")
}

func oaCode(err error) string {
	var oe *service.OAuthError
	if errors.As(err, &oe) {
		return oe.Code
	}
	if err == nil {
		return ""
	}
	return "other:" + err.Error()
}

// 配對碼 → 授權碼 → token;access token 只能 MCP(resource 綁定)、唯讀身分;REST 的 Authenticate 一律 403
func TestOAuthPairCodeToToken(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	if cl.Name != "Claude" || !strings.HasPrefix(cl.ID, "oc_") {
		t.Fatal(cl)
	}
	pc := e.pair()
	chk := e.check(cl, "o")
	csrf, err := e.svc.NewOAuthCSRF(cl.ID, "2.2.2.2")
	if err != nil || !e.svc.VerifyOAuthCSRF(csrf, cl.ID) {
		t.Fatal("CSRF 要通過一次", err)
	}
	if e.svc.VerifyOAuthCSRF(csrf, cl.ID) {
		t.Fatal("CSRF 只能用一次")
	}
	code, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), strings.ToLower(pc[:4])+"-"+pc[4:], "2.2.2.2") // 不分大小寫、可帶 -
	if err != nil || !strings.HasPrefix(code, "omc_") {
		t.Fatal(err, code)
	}
	// 配對碼成功就用掉:再用同一個碼(包括換 omixor token)都失敗
	if _, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), pc, "2.2.2.2"); err != service.ErrOAuthPairInvalid {
		t.Fatal("配對碼只能用一次", err)
	}
	if _, err := e.svc.ClaimPairCode(pc, "x", "2.2.2.2"); service.Code(err) != 401 {
		t.Fatal("配對碼用過了不能再換 token", err)
	}
	tok, err := e.exchange(cl, code)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.AccessToken, "omo_") || tok.TokenType != "Bearer" || tok.ExpiresIn != 600 || tok.Scope != "mcp:read" || tok.RefreshToken == "" {
		t.Fatalf("%+v", tok)
	}
	p, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaResource)
	if err != nil || !p.ReadOnly || p.Via != model.ViaOAuth || p.User.ID != "u1" {
		t.Fatal(err, p)
	}
	// REST / 一般 Authenticate:OAuth token 一律 403
	if _, err := e.svc.Authenticate(tok.AccessToken); service.Code(err) != 403 {
		t.Fatal("REST 要 403", err)
	}
	// 授權沒有發登入 token:除了 Mac 那把,沒有任何 omx_ token(配對碼那列已撤銷)
	list, err := e.svc.Store.ListTokens(*e.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID != "t_mac" && !service.IsPairToken(x) {
			t.Fatal("OAuth 授權不能發登入 token", x)
		}
	}
}

func TestOAuthPKCEAndGrantChecks(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	other := e.client()

	// PKCE 不符
	code := e.authorize(cl)
	_, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: strings.Repeat("x", 43)}, "")
	if oaCode(err) != "invalid_grant" {
		t.Fatal("PKCE 不符要 invalid_grant", err)
	}
	if _, err := e.exchange(cl, code); oaCode(err) != "invalid_grant" {
		t.Fatal("驗證失敗的 code 也作廢", err)
	}

	// 缺 verifier
	code = e.authorize(cl)
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback}, ""); oaCode(err) != "invalid_request" {
		t.Fatal(err)
	}

	// redirect_uri 不符
	code = e.authorize(cl)
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code,
		RedirectURI: "https://claude.com/api/mcp/auth_callback", CodeVerifier: oaVerifier}, ""); oaCode(err) != "invalid_grant" {
		t.Fatal("redirect_uri 不符", err)
	}

	// 別的 client 拿來換
	code = e.authorize(cl)
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: other.ID, Code: code, RedirectURI: oaCallback, CodeVerifier: oaVerifier}, ""); oaCode(err) != "invalid_grant" {
		t.Fatal("client 不符", err)
	}

	// resource 不符
	code = e.authorize(cl)
	_, err = e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: code, RedirectURI: oaCallback,
		CodeVerifier: oaVerifier, Resources: []string{"https://omixor.test/mcp/org/other"}}, "")
	if oaCode(err) != "invalid_target" {
		t.Fatal("resource 不符要 invalid_target", err)
	}

	// 未知 client、未知 grant_type、未知 code
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: "nope"}, ""); oaCode(err) != "invalid_client" {
		t.Fatal(err)
	}
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "password", ClientID: cl.ID}, ""); oaCode(err) != "unsupported_grant_type" {
		t.Fatal(err)
	}
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: cl.ID, Code: "omc_nope", RedirectURI: oaCallback, CodeVerifier: oaVerifier}, ""); oaCode(err) != "invalid_grant" {
		t.Fatal(err)
	}

	// 過期的 code(60 秒)
	code = e.authorize(cl)
	*e.now = e.now.Add(61 * time.Second)
	if _, err := e.exchange(cl, code); oaCode(err) != "invalid_grant" {
		t.Fatal("code 過期", err)
	}
}

// code 重用:第二次 invalid_grant,而且第一次換出來的 token 一併撤銷
func TestOAuthCodeReuseRevokes(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	code := e.authorize(cl)
	tok, err := e.exchange(cl, code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.exchange(cl, code); oaCode(err) != "invalid_grant" {
		t.Fatal("code 只能用一次", err)
	}
	if _, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaResource); err == nil {
		t.Fatal("code 重用要撤銷已簽發的 access token")
	}
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "refresh_token", ClientID: cl.ID, RefreshToken: tok.RefreshToken}, ""); oaCode(err) != "invalid_grant" {
		t.Fatal("refresh 也要撤銷", err)
	}
}

// refresh 輪替:換出新的一組,舊的 refresh 失效;舊的再出現 → invalid_grant 並撤銷整個家族(包含最新的 refresh 與 access)
func TestOAuthRefreshRotationAndReuse(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	tok1, err := e.exchange(cl, e.authorize(cl))
	if err != nil {
		t.Fatal(err)
	}
	refresh := func(rt string, res ...string) (*service.OAuthTokenResponse, error) {
		return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "refresh_token", ClientID: cl.ID, RefreshToken: rt, Resources: res}, "")
	}
	tok2, err := refresh(tok1.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if tok2.RefreshToken == tok1.RefreshToken || tok2.AccessToken == tok1.AccessToken || tok2.Scope != "mcp:read" || tok2.ExpiresIn != 600 {
		t.Fatal("要換新的", tok2)
	}
	if _, err := e.svc.AuthenticateOAuth(tok2.AccessToken, oaResource); err != nil {
		t.Fatal(err)
	}
	// resource 不符:invalid_target,且不輪替
	if _, err := refresh(tok2.RefreshToken, "https://omixor.test/mcp/org/zzz"); oaCode(err) != "invalid_target" {
		t.Fatal(err)
	}
	// 帶合法的 resource 可以
	tok3, err := refresh(tok2.RefreshToken, oaResource)
	if err != nil {
		t.Fatal(err)
	}
	// 舊的 refresh(tok1)重用:invalid_grant + 整個家族撤銷
	if _, err := refresh(tok1.RefreshToken); oaCode(err) != "invalid_grant" {
		t.Fatal("重用要 invalid_grant", err)
	}
	if _, err := refresh(tok3.RefreshToken); oaCode(err) != "invalid_grant" {
		t.Fatal("家族撤銷後最新的 refresh 也不能用", err)
	}
	if _, err := e.svc.AuthenticateOAuth(tok3.AccessToken, oaResource); err == nil {
		t.Fatal("家族撤銷後 access token 也失效")
	}
}

func TestOAuthExpiryResourceAndDisabled(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	tok, err := e.exchange(cl, e.authorize(cl))
	if err != nil {
		t.Fatal(err)
	}
	// 資源不對(別的組織的 MCP 網址、空)
	for _, res := range []string{"https://omixor.test/mcp/org/other", "https://omixor.test/mcp/org/o/", "", "https://omixor.test"} {
		if _, err := e.svc.AuthenticateOAuth(tok.AccessToken, res); err != service.ErrOAuthInvalidToken {
			t.Fatalf("resource %q 要被拒:%v", res, err)
		}
	}
	// 不是 access token(refresh、code、亂的)
	for _, x := range []string{tok.RefreshToken, "omo_nope", "omx_mac", ""} {
		if _, err := e.svc.AuthenticateOAuth(x, oaResource); err == nil {
			t.Fatalf("%q 不能當 access token", x)
		}
	}
	// 帳號停用:403(已有的 token 也停)
	u, _ := e.svc.Store.UserByID("u1")
	u.Disabled = true
	_ = e.svc.Store.SaveUser(*u)
	if _, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaResource); service.Code(err) != 403 {
		t.Fatal("停用的帳號要 403", err)
	}
	u.Disabled = false
	_ = e.svc.Store.SaveUser(*u)
	// 10 分鐘後過期
	*e.now = e.now.Add(599 * time.Second)
	if _, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaResource); err != nil {
		t.Fatal("599 秒還沒過期", err)
	}
	*e.now = e.now.Add(2 * time.Second)
	if _, err := e.svc.AuthenticateOAuth(tok.AccessToken, oaResource); err != service.ErrOAuthInvalidToken {
		t.Fatal("過期要 invalid", err)
	}
	// refresh 還能用(30 天內);過了 30 天不行
	if _, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "refresh_token", ClientID: cl.ID, RefreshToken: tok.RefreshToken}, ""); err != nil {
		t.Fatal(err)
	}
}

// 授權的把關:非成員、停用、錯碼(同一句話)、鎖定、全域頻率
func TestOAuthApproveGuards(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	// 不是組織成員:不發 code(配對碼已用掉)
	_ = e.svc.Store.RemoveMember("o", "u1")
	_, err := e.svc.ApproveOAuth(e.check(cl, "o"), oaChallenge(oaVerifier), e.pair(), "")
	if oaCode(err) != "access_denied" {
		t.Fatal("非成員要拒絕", err)
	}
	_ = e.svc.Store.SaveMember(model.Member{Org: "o", UserID: "u1", Role: "member"})
	// 帳號停用
	u, _ := e.svc.Store.UserByID("u1")
	u.Disabled = true
	_ = e.svc.Store.SaveUser(*u)
	if _, err := e.svc.ApproveOAuth(e.check(cl, "o"), oaChallenge(oaVerifier), e.pair(), ""); oaCode(err) != "access_denied" {
		t.Fatal("停用要拒絕", err)
	}
	u.Disabled = false
	_ = e.svc.Store.SaveUser(*u)

	// 錯誤的、格式不對的、過期的:同一個錯誤物件(不洩漏原因)
	chk := e.check(cl, "o")
	good := e.pair()
	*e.now = e.now.Add(service.PairTTL + time.Second)
	for _, c := range []string{"ABCD-2345", "zz", "", good} {
		if _, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), c, ""); err != service.ErrOAuthPairInvalid {
			t.Fatalf("%q:要同一句錯誤,得到 %v", c, err)
		}
	}
	// 累計到上限:全域鎖定,連正確的碼也先擋(429);鎖過了才恢復
	for i := 0; i < service.OAuthMaxFailures; i++ {
		_, _ = e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), "ABCD-2345", "")
	}
	var oe *service.OAuthError
	if _, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), e.pair(), ""); !errors.As(err, &oe) || oe.Status != 429 {
		t.Fatal("鎖定中要 429", err)
	}
	*e.now = e.now.Add(service.OAuthLockTime + time.Second)
	if _, err := e.svc.ApproveOAuth(chk, oaChallenge(oaVerifier), e.pair(), ""); err != nil {
		t.Fatal("鎖定結束要恢復", err)
	}
}

func TestOAuthAuthorizeRequestChecks(t *testing.T) {
	e := newOAuthEnv(t)
	cl := e.client()
	base := service.OAuthAuthRequest{ClientID: cl.ID, RedirectURI: oaCallback, ResponseType: "code", CodeChallenge: oaChallenge(oaVerifier),
		CodeChallengeMethod: "S256", Resources: []string{oaResource}, Org: "o"}
	if _, err := e.svc.CheckOAuthAuthorize(base); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		mod        func(r *service.OAuthAuthRequest)
		code       string
		noRedirect bool
	}{
		{"未知 client", func(r *service.OAuthAuthRequest) { r.ClientID = "nope" }, "invalid_request", true},
		{"redirect 不符", func(r *service.OAuthAuthRequest) { r.RedirectURI = "https://evil.example/cb" }, "invalid_request", true},
		{"redirect 空", func(r *service.OAuthAuthRequest) { r.RedirectURI = "" }, "invalid_request", true},
		{"response_type", func(r *service.OAuthAuthRequest) { r.ResponseType = "token" }, "unsupported_response_type", false},
		{"沒有 PKCE", func(r *service.OAuthAuthRequest) { r.CodeChallenge = "" }, "invalid_request", false},
		{"plain", func(r *service.OAuthAuthRequest) { r.CodeChallengeMethod = "plain" }, "invalid_request", false},
		{"challenge 格式", func(r *service.OAuthAuthRequest) { r.CodeChallenge = "short" }, "invalid_request", false},
		{"沒有 resource", func(r *service.OAuthAuthRequest) { r.Resources, r.Org = nil, "" }, "invalid_target", false},
		{"多個 resource", func(r *service.OAuthAuthRequest) { r.Resources = []string{oaResource, oaResource} }, "invalid_target", false},
		{"resource 不是組織 MCP", func(r *service.OAuthAuthRequest) { r.Org = "" }, "invalid_target", false},
	}
	for _, c := range cases {
		r := base
		c.mod(&r)
		_, err := e.svc.CheckOAuthAuthorize(r)
		var oe *service.OAuthError
		if !errors.As(err, &oe) || oe.Code != c.code || oe.NoRedirect != c.noRedirect {
			t.Errorf("%s:%v", c.name, err)
		}
	}
}

func TestOAuthRegistration(t *testing.T) {
	e := newOAuthEnv(t)
	reg := func(in service.OAuthClientInput) error {
		_, err := e.svc.RegisterOAuthClient(in, "9.9.9.9")
		return err
	}
	ok := func(uris ...string) service.OAuthClientInput {
		l := []any{}
		for _, u := range uris {
			l = append(l, u)
		}
		return service.OAuthClientInput{RedirectURIs: l}
	}
	for _, u := range []string{oaCallback, "https://claude.com/api/mcp/auth_callback", "http://127.0.0.1/callback", "http://localhost/callback", "http://127.0.0.1:8123/callback", "http://localhost:65535/callback"} {
		if err := reg(ok(u)); err != nil {
			t.Errorf("%s 要允許:%v", u, err)
		}
	}
	for _, u := range []string{"https://claude.ai/api/mcp/auth_callback/", "https://claude.ai/api/mcp/auth_callback?x=1", "http://claude.ai/api/mcp/auth_callback",
		"https://evil.example/api/mcp/auth_callback", "https://claude.ai.evil.example/api/mcp/auth_callback", "http://127.0.0.1:99999/callback", "http://127.0.0.1:0/callback",
		"http://127.0.0.1/callback?a=b", "http://127.0.0.1/other", "http://[::1]/callback", "https://127.0.0.1/callback", "http://evil@127.0.0.1/callback",
		"http://127.0.0.1.evil.example/callback", "javascript:alert(1)", "http://localhost:08080/callback", ""} {
		if err := reg(ok(u)); oaCode(err) != "invalid_redirect_uri" {
			t.Errorf("%q 要拒絕:%v", u, err)
		}
	}
	// 一個不合格就整個拒絕;空、太多、型別不對
	if err := reg(ok(oaCallback, "https://evil.example/cb")); oaCode(err) != "invalid_redirect_uri" {
		t.Error("有一個不合格就拒絕", err)
	}
	if reg(service.OAuthClientInput{}) == nil || reg(ok()) == nil || reg(service.OAuthClientInput{RedirectURIs: "x"}) == nil || reg(ok(oaCallback, oaCallback, oaCallback, oaCallback, oaCallback, oaCallback)) == nil {
		t.Error("redirect_uris 空、型別不對、太多都要拒絕")
	}
	in := ok(oaCallback)
	in.TokenEndpointAuthMethod = "client_secret_basic"
	if oaCode(reg(in)) != "invalid_client_metadata" {
		t.Error("只收 none")
	}
	in.TokenEndpointAuthMethod = "none"
	in.GrantTypes = []any{"authorization_code", "refresh_token"}
	in.ResponseTypes = []any{"code"}
	if err := reg(in); err != nil {
		t.Error(err)
	}
	in.GrantTypes = []any{"password"}
	if oaCode(reg(in)) != "invalid_client_metadata" {
		t.Error("grant_types ⊆ {authorization_code, refresh_token}")
	}
	in.GrantTypes = nil
	in.ResponseTypes = []any{"token"}
	if oaCode(reg(in)) != "invalid_client_metadata" {
		t.Error("response_types 只收 code")
	}
	// 名稱清理:控制字元、方向字元、長度
	in = ok(oaCallback)
	in.ClientName = "  Cl‮au\x00de\n\tApp  " + strings.Repeat("長", 100)
	cl, err := e.svc.RegisterOAuthClient(in, "9.9.9.9")
	if err != nil || len([]rune(cl.Name)) > 64 || strings.ContainsAny(cl.Name, "‮\x00\n\t") || !strings.HasPrefix(cl.Name, "Cl au de App") {
		t.Fatalf("名稱要清理:%q %v", cl.Name, err)
	}
	in.ClientName = "   "
	if cl, _ := e.svc.RegisterOAuthClient(in, "9.9.9.9"); cl == nil || cl.Name != "(未命名)" {
		t.Error("空名稱")
	}
	in.ClientName = 5
	if oaCode(reg(in)) != "invalid_client_metadata" {
		t.Error("名稱要是字串")
	}
}

func TestOAuthRegistrationRateLimit(t *testing.T) {
	e := newOAuthEnv(t)
	e.svc.OAuthRegPerIP, e.svc.OAuthRegTotal = 2, 3
	in := service.OAuthClientInput{RedirectURIs: []any{oaCallback}}
	n429 := func(ip string) bool {
		_, err := e.svc.RegisterOAuthClient(in, ip)
		var oe *service.OAuthError
		return errors.As(err, &oe) && oe.Status == 429
	}
	if n429("a") || n429("a") || !n429("a") {
		t.Fatal("同一個來源第 3 次要 429")
	}
	if n429("b") { // 全域第 3 個
		t.Fatal("別的來源還有名額")
	}
	if !n429("c") { // 全域 3 個用完(偽造 X-Forwarded-For 換來源也躲不掉)
		t.Fatal("全域上限要擋")
	}
	*e.now = e.now.Add(61 * time.Second)
	if n429("c") {
		t.Fatal("下一分鐘恢復")
	}
}
