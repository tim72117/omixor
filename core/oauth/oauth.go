// Package oauth 是 omixor 的 OAuth 2.1 授權伺服器 HTTP 層:讓 Claude 自訂 connector 登入 MCP 端點,授權永遠是唯讀。
// resource 有兩種:組織 MCP 端點 <public>/mcp/org/{org}(綁單一組織),和統一入口 <public>/mcp(帳號層級:不綁組織,
// 每次工具呼叫由統一入口用即時成員資格決定能用哪些組織;同意畫面不選組織)。
// 同意畫面證明身分的方式由 IdentityProvider 決定(provider.go;NewWith 可以換):預設有兩種,主要是「用 evoleap 登入並允許」
// (瀏覽器登入 evoleap,見下面 /oauth/evoleap/callback;伺服器沒有 evoleap 或被關掉就不顯示),備援是一次性的配對碼(本機伺服器常沒有 evoleap)。
// 證明身分之後的授權碼簽發一律是 service.ApproveOAuthIdentity。
// 規則(證明身分、PKCE、refresh 輪替、唯讀)在 service/oauth.go,存放在 omixor_oauth_*(core/store/pg_oauth.go);這裡只做 HTTP:
//
//	GET  /.well-known/oauth-protected-resource/mcp/org/{org}   RFC 9728:這個組織 MCP 端點的 resource 與授權伺服器(/.well-known/oauth-protected-resource 是沒有路徑的備援)
//	GET  /.well-known/oauth-protected-resource/mcp             RFC 9728:統一入口 /mcp(帳號層級唯讀授權)
//	GET  /.well-known/oauth-authorization-server/oauth         RFC 8414:授權伺服器 metadata(/oauth/.well-known/openid-configuration 同內容)
//	POST /oauth/register                                       RFC 7591 動態註冊(JSON ≤ 16 KB;redirect_uri 只收 Claude 的 callback(含桌面版 claude://claude.ai/mcp-auth-callback/sdk)與本機 /callback)
//	GET  /oauth/authorize                                      同意畫面:主要按鈕「用 evoleap 登入並允許」(有設 evoleap 才有);備援是輸入「配對碼」(在已登入的 omixor 的 手機配對 產生)
//	POST /oauth/authorize                                      送出同意:驗證 CSRF;配對碼成功 303 導回 redirect_uri?code=…&state=…;via=evoleap 則重驗授權請求、
//	                                                           登記一筆登入交易、設綁定瀏覽器的 cookie(omixor_oauth_evo,HttpOnly、SameSite=Lax、Path=/oauth)並 303 導去 evoleap 核准頁
//	GET  /oauth/evoleap/callback                               (有回呼的 provider 掛在 /oauth/<id>/callback;這是 evoleap 的)evoleap 核准後導回(?id=&code=):核對 cookie、交易單一用途(10 分鐘)、向 evoleap 換 token 對上 omixor 使用者後
//	                                                           立刻撤銷 evoleap token,簽發授權碼並 303 導回客戶端的 redirect_uri;任何不符一律同一個通用錯誤頁
//	GET  /oauth/gitlab/callback                                (OMIXOR_GITLAB_URL 有設才可用)GitLab 導回(?code=&state=):核對 cookie 與登入交易(10 分鐘、用一次)、換 token 對上使用者後立刻撤銷 GitLab token,
//	                                                           顯示確認頁,還不簽發授權碼(同意畫面按 via=gitlab 開始;provider_gitlab.go、service/gitlab_login.go)
//	POST /oauth/gitlab/confirm                                 GitLab 回呼後的確認頁送出(允許才簽發授權碼;綁同一個 cookie、5 分鐘、用一次)
//	POST /oauth/ldap/confirm                                   LDAP 帳密通過後的確認頁送出(有啟用 LDAP 才有;同意畫面按 via=ldap;provider_ldap.go)
//	POST /oauth/token                                          authorization_code(PKCE S256)、refresh_token(輪替);只收 application/x-www-form-urlencoded
//	GET  /oauth/consent.css                                    同意畫面的樣式(CSP 不允許行內樣式,所以獨立一個檔)
//
// POST /oauth/authorize 與 /oauth/gitlab/confirm 一律拒絕跨站送出;CSRF token 另外綁定 GET 同意畫面時設的 cookie omixor_oauth_consent(browser.go)。
//
// 對外網址(OMIXOR_PUBLIC_URL)沒設就不掛這些路由(issuer、resource 都需要它)。
package oauth

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"log"

	"omixor-core/model"
	"omixor-core/service"
)

// Handler OAuth 端點
type Handler struct {
	Svc    *service.Service
	Public string // 對外網址(不含結尾斜線),例如 https://omixor.example.com
	// Log 觀測 log 的輸出(見 log.go);nil = 伺服器既有的標準 logger(log.Writer(),不加時間前綴,一行一個 JSON)
	Log *log.Logger
	// Providers 同意畫面證明身分的方式,依序顯示(見 provider.go)
	Providers []IdentityProvider
}

// Config NewWith 的設定
type Config struct {
	Svc    *service.Service
	Public string // OMIXOR_PUBLIC_URL(結尾斜線會去掉)
	// Providers 證明身分的方式;nil = DefaultProviders(雲端的 evoleap 登入加配對碼)
	Providers []IdentityProvider
	Log       *log.Logger
}

// New 建立,證明身分用預設的 evoleap 登入與配對碼;public 是 OMIXOR_PUBLIC_URL(結尾斜線會去掉)
func New(svc *service.Service, public string) *Handler {
	return NewWith(Config{Svc: svc, Public: public})
}

// NewWith 建立,可以指定證明身分的方式
func NewWith(c Config) *Handler {
	h := &Handler{Svc: c.Svc, Public: strings.TrimRight(c.Public, "/"), Log: c.Log, Providers: c.Providers}
	if h.Providers == nil {
		h.Providers = DefaultProviders(h.Svc, h.Public)
	}
	return h
}

// DefaultProviders 雲端與本機伺服器的證明身分方式:evoleap 登入(沒設網址或被關掉時同意畫面不顯示)與配對碼(備援)
// GitLab 登入(有設 OMIXOR_GITLAB_URL 才顯示)排在 evoleap 之後;LDAP 帳號密碼(有設 OMIXOR_LDAP_URL 才顯示)排在配對碼之前
func DefaultProviders(svc *service.Service, public string) []IdentityProvider {
	return append(LoginProviders(svc, public), &LDAPProvider{Svc: svc, Public: public}, &PairProvider{Svc: svc})
}

// LoginProviders 只有外部登入(evoleap、GitLab;各自沒啟用時同意畫面不顯示),不含配對碼:console 的對外 MCP 連接埠用
func LoginProviders(svc *service.Service, public string) []IdentityProvider {
	return []IdentityProvider{&EvoleapProvider{Svc: svc, Public: public}, &GitLabProvider{Svc: svc, Public: public}}
}

// Issuer 授權伺服器識別
func (h *Handler) Issuer() string { return h.Public + "/oauth" }

// MCPURL 組織 MCP 端點的網址(也是 OAuth 的 resource)
func (h *Handler) MCPURL(org string) string { return h.Public + "/mcp/org/" + org }

// UnifiedURL 統一入口的網址(也是帳號層級授權的 OAuth resource)
func (h *Handler) UnifiedURL() string { return h.Public + "/mcp" }

// UnifiedResourceMetadataURL 統一入口的 Protected Resource Metadata 網址
func (h *Handler) UnifiedResourceMetadataURL() string {
	return h.Public + "/.well-known/oauth-protected-resource/mcp"
}

// ResourceMetadataURL 組織 MCP 端點的 Protected Resource Metadata 網址
func (h *Handler) ResourceMetadataURL(org string) string {
	return h.Public + "/.well-known/oauth-protected-resource/mcp/org/" + org
}

// orgOfResource resource(RFC 8707)→ 組織 id;不是這個伺服器的組織 MCP 網址回 ""
func (h *Handler) orgOfResource(resource string) string {
	org, ok := strings.CutPrefix(resource, h.Public+"/mcp/org/")
	if !ok || !model.ValidOrgID(org) {
		return ""
	}
	return org
}

// resourceOf resource(RFC 8707)→ 授權範圍:組織 MCP 網址回組織 id;統一入口網址回 account=true;其他都不認得(org 空、account false)
func (h *Handler) resourceOf(resource string) (org string, account bool) {
	if resource == h.UnifiedURL() {
		return "", true
	}
	return h.orgOfResource(resource), false
}

// Mount 把路由掛到 mux
func (h *Handler) Mount(mux *http.ServeMux) {
	obs := func(f http.HandlerFunc) http.Handler { return h.observe(f, nil, nil) }
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp/org/{org}", obs(h.protectedResource))
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", obs(h.protectedResourceUnified))
	mux.Handle("GET /.well-known/oauth-protected-resource", obs(h.protectedResource))
	mux.Handle("GET /.well-known/oauth-authorization-server/oauth", obs(h.serverMetadata))
	mux.Handle("GET /oauth/.well-known/openid-configuration", obs(h.serverMetadata))
	mux.Handle("POST /oauth/register", obs(h.register))
	mux.Handle("GET /oauth/authorize", obs(h.authorizeGet))
	mux.Handle("POST /oauth/authorize", obs(h.authorizePost))
	for _, p := range h.Providers { // 外部登入的回呼:/oauth/<id>/callback(evoleap 是 /oauth/evoleap/callback)
		if cb := p.Callback(); cb != nil {
			mux.Handle("GET "+callbackURL("", p.ID()), h.observe(cb, nil, nil))
		}
		if c, ok := p.(confirmer); ok { // 回呼後要使用者再按一次允許的 provider:POST /oauth/<id>/confirm
			mux.Handle("POST /oauth/"+p.ID()+"/confirm", h.observe(c.Confirm(), nil, nil))
		}
	}
	mux.Handle("POST /oauth/token", obs(h.token))
	mux.HandleFunc("GET /oauth/consent.css", h.css) // 靜態樣式,不記 log
}

func writeJSON(w http.ResponseWriter, status int, v any, extra ...string) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	for i := 0; i+1 < len(extra); i += 2 {
		w.Header().Set(extra[i], extra[i+1])
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// ---------------- metadata ----------------

func (h *Handler) protectedResourceUnified(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"resource":                 h.UnifiedURL(),
		"authorization_servers":    []string{h.Issuer()},
		"scopes_supported":         []string{model.OAuthScopeRead, model.OAuthScopeWrite, model.OAuthScopeAdmin},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "omixor",
	})
}

func (h *Handler) protectedResource(w http.ResponseWriter, r *http.Request) {
	resource := h.Public // 沒有路徑的備援:沒有組織可以指,只給伺服器本身
	if org := r.PathValue("org"); org != "" {
		if !model.ValidOrgID(org) {
			http.NotFound(w, r)
			return
		}
		resource = h.MCPURL(org)
	}
	writeJSON(w, 200, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{h.Issuer()},
		"scopes_supported":         []string{model.OAuthScopeRead, model.OAuthScopeWrite, model.OAuthScopeAdmin},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "omixor",
	})
}

func (h *Handler) serverMetadata(w http.ResponseWriter, r *http.Request) {
	iss := h.Issuer()
	writeJSON(w, 200, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/authorize",
		"token_endpoint":                        iss + "/token",
		"registration_endpoint":                 iss + "/register",
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{model.OAuthScopeRead, model.OAuthScopeWrite, model.OAuthScopeAdmin, model.OAuthScopeOffline},
	})
}

// ---------------- 錯誤 ----------------

func jsonError(w http.ResponseWriter, err error) {
	var oe *service.OAuthError
	if !errors.As(err, &oe) {
		oe = &service.OAuthError{Status: 500, Code: "server_error", Desc: "伺服器錯誤"}
		_ = service.Code(err) // 內部錯誤不外露細節
	}
	extra := []string{}
	if oe.Status == 429 {
		extra = append(extra, "Retry-After", "60")
	}
	writeJSON(w, oe.Status, map[string]any{"error": oe.Code, "error_description": oe.Desc}, extra...)
}

// ---------------- 動態註冊 ----------------

func mediaType(r *http.Request) string {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt
}

// anyStrings JSON 任意值 → 字串清單(觀測用;非字串清單回 nil)
func anyStrings(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if mediaType(r) != "application/json" {
		writeJSON(w, 415, map[string]any{"error": "invalid_client_metadata", "error_description": "要用 application/json"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeJSON(w, 413, map[string]any{"error": "invalid_client_metadata", "error_description": "內容太大(上限 16 KB)"})
		return
	}
	var in service.OAuthClientInput
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid_client_metadata", "error_description": "內容必須是 JSON 物件"})
		return
	}
	d := detail(r)
	d["client_name"] = service.CleanOAuthClientName(in.ClientName)
	d["redirect_uris"] = anyStrings(in.RedirectURIs)
	d["grant_types"] = anyStrings(in.GrantTypes)
	d["response_types"] = anyStrings(in.ResponseTypes)
	d["token_endpoint_auth_method"], _ = in.TokenEndpointAuthMethod.(string)
	d["scope"], _ = in.Scope.(string)
	cl, err := h.Svc.RegisterOAuthClient(in, service.ClientIP(r))
	if err != nil {
		jsonError(w, err)
		return
	}
	d["client_id"] = cl.ID
	writeJSON(w, 201, map[string]any{
		"client_id": cl.ID, "client_id_issued_at": cl.CreatedAt.Unix(), "client_name": cl.Name,
		"redirect_uris": cl.RedirectURIs, "grant_types": cl.GrantTypes, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// ---------------- token ----------------

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	d := detail(r)
	d["result"] = "error"
	d["body_format"] = "other"
	if mediaType(r) != "application/x-www-form-urlencoded" {
		writeJSON(w, 415, map[string]any{"error": "invalid_request", "error_description": "要用 application/x-www-form-urlencoded"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeJSON(w, 413, map[string]any{"error": "invalid_request", "error_description": "內容太大"})
		return
	}
	f, err := url.ParseQuery(string(body))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid_request", "error_description": "內容格式不對"})
		return
	}
	d["body_format"] = "form"
	d["grant_type"], d["client_id"], d["redirect_uri"] = f.Get("grant_type"), f.Get("client_id"), f.Get("redirect_uri")
	d["resource_present"], d["resource"] = f.Has("resource"), strings.Join(f["resource"], ",")
	d["code_verifier_present"], d["code_present"], d["refresh_token_present"] = f.Get("code_verifier") != "", f.Get("code") != "", f.Get("refresh_token") != ""
	basic := strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "basic ")
	switch {
	case basic:
		d["client_auth_method"] = "basic"
	case f.Has("client_secret"):
		d["client_auth_method"] = "post"
	default:
		d["client_auth_method"] = "none"
	}
	// 公開用戶端(token_endpoint_auth_method=none):帶了密鑰或 Basic 認證都不收
	if basic || f.Has("client_secret") {
		writeJSON(w, 401, map[string]any{"error": "invalid_client", "error_description": "這是公開用戶端(token_endpoint_auth_method=none)"})
		return
	}
	resp, obs, err := h.Svc.OAuthTokenObserved(service.OAuthTokenRequest{
		GrantType: f.Get("grant_type"), ClientID: f.Get("client_id"), Code: f.Get("code"), RedirectURI: f.Get("redirect_uri"),
		CodeVerifier: f.Get("code_verifier"), RefreshToken: f.Get("refresh_token"), Scope: f.Get("scope"), Resources: f["resource"],
	}, service.ClientIP(r))
	d["result"] = obs.Result
	if f.Get("grant_type") == "refresh_token" {
		d["access_remaining_s_at_refresh"] = obs.AccessRemainingS
	}
	if err != nil {
		jsonError(w, err)
		return
	}
	d["expires_in"] = resp.ExpiresIn
	writeJSON(w, 200, resp, "Pragma", "no-cache")
}

func (h *Handler) authRequest(get func(string) string, all func(string) []string) service.OAuthAuthRequest {
	req := service.OAuthAuthRequest{
		ClientID: get("client_id"), RedirectURI: get("redirect_uri"), ResponseType: get("response_type"),
		CodeChallenge: get("code_challenge"), CodeChallengeMethod: get("code_challenge_method"),
		State: get("state"), Scope: get("scope"), Resources: all("resource"),
	}
	if len(req.Resources) == 1 {
		req.Org, req.Account = h.resourceOf(req.Resources[0])
	}
	return req
}

// redirectTo 303 導回 redirect_uri(已經驗證過是註冊的);params 的空值不帶
func redirectTo(w http.ResponseWriter, redirectURI string, kv ...string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect", 500)
		return
	}
	q := u.Query()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	u.RawQuery = q.Encode()
	w.Header().Set("Location", u.String())
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(303)
}

// noteError 把 OAuthError 的原因記進這次請求的觀測欄位:error 是回給用戶端的 RFC 碼,pairing_result 是細分原因,detail 是只進 log 的細節
func noteError(r *http.Request, oe *service.OAuthError) {
	d := detail(r)
	d["error"] = oe.Code
	if oe.Reason != "" {
		d["pairing_result"] = oe.Reason
	}
	if oe.Detail != "" {
		d["detail"] = oe.Detail
	}
}

// authError 授權請求的錯誤:client_id 或 redirect_uri 不可信 → 直接顯示;其他導回 redirect_uri?error=…
func (h *Handler) authError(w http.ResponseWriter, r *http.Request, req service.OAuthAuthRequest, err error) {
	var oe *service.OAuthError
	if !errors.As(err, &oe) {
		renderFatal(w, 500, "伺服器錯誤,請稍後再試")
		return
	}
	noteError(r, oe)
	if oe.NoRedirect {
		renderFatal(w, oe.Status, oe.Desc)
		return
	}
	redirectTo(w, req.RedirectURI, "error", oe.Code, "error_description", oe.Desc, "state", req.State)
}

func (h *Handler) authorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := h.authRequest(q.Get, func(k string) []string { return q[k] })
	d := detail(r)
	d["client_id"], d["redirect_uri"], d["response_type"], d["scope"] = req.ClientID, req.RedirectURI, req.ResponseType, req.Scope
	d["resource_present"], d["resource"] = q.Has("resource"), strings.Join(req.Resources, ",")
	d["code_challenge_method"], d["code_challenge_present"], d["state_present"] = req.CodeChallengeMethod, req.CodeChallenge != "", req.State != ""
	chk, err := h.Svc.CheckOAuthAuthorize(req)
	if err == nil && len(req.State) > 512 {
		err = &service.OAuthError{Status: 400, Code: "invalid_request", Desc: "state 太長"}
	}
	if err != nil {
		h.authError(w, r, req, err)
		return
	}
	csrf, err := h.Svc.NewOAuthCSRF(chk, req.CodeChallenge, service.ClientIP(r), h.ensureBrowserSecret(w, r))
	if err != nil {
		var oe *service.OAuthError
		if errors.As(err, &oe) {
			renderFatal(w, oe.Status, oe.Desc)
		} else {
			renderFatal(w, 500, "伺服器錯誤,請稍後再試")
		}
		return
	}
	h.renderConsent(w, 200, chk, req, csrf, "")
}

func (h *Handler) authorizePost(w http.ResponseWriter, r *http.Request) {
	if crossSite(r, h.Public) { // 在讀任何內容、消耗任何憑證之前
		d := detail(r)
		d["txn_valid"], d["pairing_result"] = false, "cross_site"
		renderFatal(w, 403, "這個請求不是從授權頁送出的,已拒絕")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	if err != nil {
		renderFatal(w, 413, "請求太大")
		return
	}
	f, err := url.ParseQuery(string(body))
	if err != nil {
		renderFatal(w, 400, "請求格式不對")
		return
	}
	req := h.authRequest(f.Get, func(k string) []string { return f[k] })
	d := detail(r)
	d["txn_valid"] = false
	d["client_id"], d["redirect_uri"] = req.ClientID, req.RedirectURI
	chk, err := h.Svc.CheckOAuthAuthorize(req)
	if err == nil && len(req.State) > 512 {
		err = &service.OAuthError{Status: 400, Code: "invalid_request", Desc: "state 太長"}
	}
	if err != nil {
		h.authError(w, r, req, err)
		return
	}
	// CSRF:這一次載入的 token,綁定這一次授權請求(client、resource、redirect_uri、code_challenge、要求的 scope 範圍),只能用一次(驗證失敗不導回,請使用者重新開始)
	if !h.Svc.VerifyOAuthCSRF(f.Get("csrf"), chk, req.CodeChallenge, browserSecret(r)) {
		d["pairing_result"] = "bad_csrf"
		renderFatal(w, 403, "這個頁面已失效(逾時或重複送出),請回到 Claude 重新開始連線")
		return
	}
	d["txn_valid"] = true
	if f.Get("action") == "deny" {
		d["pairing_result"] = "denied"
		redirectTo(w, chk.RedirectURI, "error", "access_denied", "error_description", "使用者拒絕授權", "state", req.State)
		return
	}
	// 只有表單明確送 grant=write 才是讀寫(其他值、沒送都是唯讀);客戶端沒有要求 mcp:write 卻送 grant=write 在這裡就拒絕(invalid_scope,不會消耗任何憑證)
	grant := service.OAuthGrantRead
	switch f.Get("grant") {
	case "write":
		grant = service.OAuthGrantWrite
	case "admin":
		grant = service.OAuthGrantAdmin
	}
	d["grant_write"] = grant != service.OAuthGrantRead
	d["grant"] = grant
	if err := service.CheckOAuthGrant(chk, grant); err != nil {
		h.approveFailed(w, r, chk, req, err, "")
		return
	}
	p := pickProvider(h.Providers, f)
	if p == nil {
		renderFatal(w, 400, "請求格式不對")
		return
	}
	out, err := p.Start(w, r, Txn{Checked: chk, Req: req, Grant: grant, Form: f})
	if err != nil {
		h.approveFailed(w, r, chk, req, err, "")
		return
	}
	switch {
	case out.Confirm != nil:
		h.ensureBrowserSecret(w, r) // 確認頁沿用同一個綁定 cookie,時效重新起算
		renderConfirm(w, p.ID(), out.Confirm)
	case out.Done != nil:
		code, err := h.Svc.ApproveOAuthIdentity(chk, req.CodeChallenge, grant, out.Done.User, out.Done.Extra)
		if err != nil {
			h.approveFailed(w, r, chk, req, err, out.Done.Hint)
			return
		}
		d["pairing_result"] = "ok"
		redirectTo(w, chk.RedirectURI, "code", code, "state", req.State)
	case out.Redirect != "":
		if out.Cookie != nil {
			http.SetCookie(w, out.Cookie)
		}
		w.Header().Set("Location", out.Redirect)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(303)
	default:
		status := out.RetryStatus
		if status == 0 {
			status = 403
		}
		h.retryConsent(w, r, chk, req, status, out.Retry, false)
	}
}

// retryConsent 這次沒過:CSRF 已經用掉,發新的重新顯示同意畫面(太頻繁就放棄,顯示錯誤頁)
func (h *Handler) retryConsent(w http.ResponseWriter, r *http.Request, chk *service.OAuthAuthChecked, req service.OAuthAuthRequest, status int, msg string, adminDenied bool) {
	csrf, err := h.Svc.NewOAuthCSRF(chk, req.CodeChallenge, service.ClientIP(r), h.ensureBrowserSecret(w, r))
	if err != nil {
		renderFatal(w, 429, "太頻繁,請稍後再試")
		return
	}
	h.renderConsent(w, status, chk, req, csrf, msg, adminDenied)
}

// approveFailed 證明身分或簽發授權碼失敗:記觀測欄位,然後重新顯示同意畫面(身分已證明但不是任何組織的管理員:管理員選項停用,hint 接在訊息後面)
// 或顯示錯誤頁(429 帶 Retry-After)。同意畫面與錯誤頁的文字、狀態碼不洩漏細節(一律用 OAuthError 的 Desc)
func (h *Handler) approveFailed(w http.ResponseWriter, r *http.Request, chk *service.OAuthAuthChecked, req service.OAuthAuthRequest, err error, hint string) {
	var oe *service.OAuthError
	if !errors.As(err, &oe) {
		renderFatal(w, 500, "伺服器錯誤,請稍後再試")
		return
	}
	noteError(r, oe)
	if oe.Code == "invalid_scope" && oe.Reason == "" {
		detail(r)["pairing_result"] = "bad_scope"
	}
	if oe == service.ErrOAuthNotOrgAdmin { // 身分已證明、但不是任何組織的管理員:重新顯示同意畫面,管理員選項停用
		msg := oe.Desc
		if hint != "" {
			msg += "。" + hint
		}
		h.retryConsent(w, r, chk, req, 403, msg, true)
		return
	}
	if oe.Status == 429 {
		w.Header().Set("Retry-After", "60")
	}
	renderFatal(w, oe.Status, oe.Desc)
}
