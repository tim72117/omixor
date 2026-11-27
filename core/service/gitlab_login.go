package service

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// GitLab 登入的兩條 HTTP 流程(帳號對應規則在 gitlab.go):
//
//	A. MCP OAuth 同意畫面(via=gitlab):StartOAuthGitLab → GitLab 授權頁 → CompleteOAuthGitLab(GET /oauth/gitlab/callback,
//	   只證明身分、回確認頁要顯示的內容)→ ConfirmOAuthGitLab(POST /oauth/gitlab/confirm,使用者按允許),
//	   最後和其他方式共用 ApproveOAuthIdentity 簽發授權碼。GitLab 對授權過的應用程式不再詢問,所以一定要有自己的確認頁
//	B. CLI / 客戶端登入:StartGitLabLogin(POST /api/auth/gitlab/start)→ GitLabLoginRedirect(瀏覽器開 /api/auth/gitlab/login,
//	   設綁定 cookie 後導去 GitLab)→ GitLabLoginCallback(GET /api/auth/gitlab/callback,303 導回客戶端的 loopback 帶一次性短效碼)
//	   → LoginWithGitLab(POST /api/auth/gitlab {code, codeVerifier})換 omixor token
//
// 交易存在 omixor_oauth_tokens(不動結構):kind 一律是 csrf(資料表的檢查約束只允許四種),靠雜湊的命名空間分開;只存雜湊、短效、用一次。
// 瀏覽器綁定密鑰放在 HttpOnly cookie(資料庫只存它的雜湊),GitLab 那一段的 PKCE verifier 也由它推導,不用另外存。
// CLI 登入沒有 OAuth 用戶端,但 client_id 是外鍵:用一個內部用戶端(沒有任何 redirect_uri,不能拿來授權)。
const (
	// GitLabTxnTTL 一次 GitLab 登入(從開始到 GitLab 導回)的時限
	GitLabTxnTTL = 10 * time.Minute
	// GitLabCodeTTL 導回 loopback 的一次性碼的時限
	GitLabCodeTTL = 60 * time.Second
	// GitLabConfirmTTL 同意畫面流程:GitLab 登入完成後,在確認頁按下允許的時限
	GitLabConfirmTTL = 5 * time.Minute

	// GitLabConsentCallbackPath、GitLabLoginCallbackPath 要登記在 GitLab Application 的兩個 Redirect URI(接在對外網址後面)
	GitLabConsentCallbackPath = "/oauth/gitlab/callback"
	GitLabLoginCallbackPath   = "/api/auth/gitlab/callback"

	gitlabLoginClientID = "omixor_gitlab_login"

	gitlabKindConsent = "gitlab-consent"
	gitlabKindStart   = "gitlab-start"
	gitlabKindCLI     = "gitlab-cli"
	gitlabKindCode    = "gitlab-code"
	gitlabKindConfirm = "gitlab-confirm"
)

// ErrOAuthGitLabInvalid 同意畫面的 GitLab 回呼不被接受(不認得的 state、cookie 不對、逾時、重放):同一句話
var ErrOAuthGitLabInvalid = &OAuthError{Status: 400, Code: "invalid_request", NoRedirect: true, Reason: "gitlab_invalid",
	Desc: "GitLab 登入已失效(逾時、重複使用,或不是在同一個瀏覽器開始的),請回到 Claude 重新開始連線"}

func gitlabHash(kind, raw string) []byte { return oauthHash(kind, raw) }

func gitlabBindHash(binding string) string {
	return hex.EncodeToString(oauthHash("gitlab-bind", binding))
}

// gitlabVerifier GitLab 那一段的 PKCE verifier(43 字元),由瀏覽器綁定密鑰推導
func gitlabVerifier(binding string) string {
	return base64.RawURLEncoding.EncodeToString(oauthHash("gitlab-pkce", binding))
}

func gitlabAuthorizeURL(g *GitLabLogin, callbackPath, state, binding string) string {
	challenge := base64.RawURLEncoding.EncodeToString(HashToken(gitlabVerifier(binding)))
	return g.Client.AuthorizeURL(g.Public+callbackPath, state, challenge)
}

func (s *Service) putGitLabTxn(kind, raw string, ttl time.Duration, t model.OAuthToken) error {
	now := s.now()
	t.ID, t.Kind, t.Hash = RandID("oa_", 9), model.OAuthKindCSRF, gitlabHash(kind, raw)
	t.CreatedAt, t.ExpiresAt = now, now.Add(ttl)
	return s.oauthStore().PutOAuthToken(t)
}

// liveGitLabTxn 查一筆還能用的交易(沒用過、沒撤銷、沒過期);找不到回 (nil, nil)
func (s *Service) liveGitLabTxn(kind, raw string) (*model.OAuthToken, error) {
	if raw == "" || len(raw) > 200 {
		return nil, nil
	}
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindCSRF, gitlabHash(kind, raw))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.RevokedAt != nil || t.UsedAt != nil || !s.now().Before(t.ExpiresAt) {
		return nil, nil
	}
	return t, nil
}

// useGitLabTxn 標成用過(條件式,並行只有一個成功);已經被用掉回 false
func (s *Service) useGitLabTxn(id string) (bool, error) {
	if err := s.oauthStore().ExchangeOAuthToken(id, s.now(), nil); err != nil {
		if errors.Is(err, store.ErrStale) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// boundGitLabTxn 找 state 對應、而且 cookie 綁定密鑰相符的交易,並標成用過。回傳交易與 FamilyID 裡綁定雜湊後面的部分;
// 任何不符回 (nil, "", nil)。cookie 不對時不作廢交易(不能讓別人拿猜到的 state 把使用者的登入弄壞)
func (s *Service) boundGitLabTxn(kind, state, binding string) (*model.OAuthToken, string, error) {
	if binding == "" || len(binding) > 200 {
		return nil, "", nil
	}
	t, err := s.liveGitLabTxn(kind, state)
	if err != nil || t == nil {
		return nil, "", err
	}
	bind, rest, ok := strings.Cut(t.FamilyID, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(bind), []byte(gitlabBindHash(binding))) != 1 {
		return nil, "", nil
	}
	if used, err := s.useGitLabTxn(t.ID); err != nil || !used {
		return nil, "", err
	}
	return t, rest, nil
}

// ---------------- A. MCP OAuth 同意畫面 ----------------

// StartOAuthGitLab 同意畫面按「用 GitLab 登入並允許」:回傳瀏覽器要去的 GitLab 授權頁與綁定密鑰(放進 HttpOnly cookie)。
// 呼叫前 HTTP 層必須已重驗授權請求與 CSRF。binding 是瀏覽器已經有的綁定密鑰(多個分頁共用,不互相弄壞),沒有或格式不對就產生新的
func (s *Service) StartOAuthGitLab(c *OAuthAuthChecked, challenge, state, grant, binding string) (loginURL, newBinding string, err error) {
	if !s.GitLabEnabled() {
		return "", "", oerr(400, "invalid_request", "這個伺服器沒有啟用 GitLab 登入")
	}
	if err := CheckOAuthGrant(c, grant); err != nil {
		return "", "", err
	}
	s.purgeOAuth()
	glState := RandID("", 24)
	if len(binding) != 32 {
		binding = RandID("", 24)
	}
	err = s.putGitLabTxn(gitlabKindConsent, glState, GitLabTxnTTL, model.OAuthToken{ClientID: c.Client.ID, Org: c.Org, Scope: scopeOfGrant(grant),
		Resource: c.Resource, RedirectURI: c.RedirectURI, CodeChallenge: challenge, FamilyID: gitlabBindHash(binding) + "." + state})
	if err != nil {
		return "", "", err
	}
	return gitlabAuthorizeURL(s.GitLab, GitLabConsentCallbackPath, glState, binding), binding, nil
}

// OAuthGitLabResult 回呼或確認的結果;失敗時 RedirectURI 非空代表這個轉址已經可信(同 OAuthEvoleapResult)
type OAuthGitLabResult struct {
	Code, RedirectURI, State string
	// Confirm 非 nil:身分已證明,要顯示確認頁(還沒有簽發授權碼)
	Confirm *OAuthGitLabConfirm
}

// OAuthGitLabConfirm 確認頁要顯示的內容;Token 是確認表單帶回來的一次性值
type OAuthGitLabConfirm struct {
	Token                   string
	UserName, UserEmail     string
	ClientName, RedirectURI string
	Org                     string // 空 = 帳號層級
	Grant, Scope            string
}

// gitlabConsentRequest 由交易重建並重驗授權請求;失敗時 res 非 nil 代表轉址可信
func (s *Service) gitlabConsentRequest(t *model.OAuthToken, state string) (chk *OAuthAuthChecked, res *OAuthGitLabResult, err error) {
	chk, err = s.CheckOAuthAuthorize(OAuthAuthRequest{ClientID: t.ClientID, RedirectURI: t.RedirectURI, ResponseType: "code",
		CodeChallenge: t.CodeChallenge, CodeChallengeMethod: "S256", State: state, Scope: t.Scope, Resources: []string{t.Resource},
		Org: t.Org, Account: t.Org == "", Worker: hasScope(t.Scope, model.OAuthScopeWorker)})
	if err != nil {
		var oe *OAuthError
		if errors.As(err, &oe) && !oe.NoRedirect {
			return nil, &OAuthGitLabResult{RedirectURI: t.RedirectURI, State: state}, err
		}
		if oe == nil {
			log.Printf("GitLab 登入:重驗授權請求失敗:%v", err)
			return nil, nil, err
		}
		return nil, nil, ErrOAuthGitLabInvalid
	}
	return chk, &OAuthGitLabResult{RedirectURI: chk.RedirectURI, State: state}, nil
}

func grantOfScope(scope string) string {
	if hasScope(scope, model.OAuthScopeWorker) {
		return OAuthGrantWorker
	}
	return []string{OAuthGrantRead, OAuthGrantWrite, OAuthGrantAdmin}[scopeLevel(scope)]
}

// CompleteOAuthGitLab GitLab 導回(GET /oauth/gitlab/callback?code=&state= 或 ?error=&state=);binding 是 cookie 帶的綁定密鑰。
// 只證明身分:成功回 Confirm,授權碼要等 ConfirmOAuthGitLab
func (s *Service) CompleteOAuthGitLab(glState, glCode, glError, binding string) (*OAuthGitLabResult, error) {
	if !s.GitLabEnabled() || len(glCode) > 2000 || (glCode == "" && glError == "") {
		return nil, ErrOAuthGitLabInvalid
	}
	t, state, err := s.boundGitLabTxn(gitlabKindConsent, glState, binding)
	if err != nil {
		log.Printf("GitLab 登入:查登入交易失敗:%v", err)
		return nil, err
	}
	if t == nil {
		return nil, ErrOAuthGitLabInvalid
	}
	chk, res, err := s.gitlabConsentRequest(t, state)
	if err != nil {
		return res, err
	}
	if glError != "" {
		return res, oerrR(403, "access_denied", "GitLab 登入沒有完成,請回到 Claude 重新開始連線", "gitlab_denied")
	}
	u, err := s.authenticateGitLab(glCode, s.GitLab.Public+GitLabConsentCallbackPath, gitlabVerifier(binding))
	if err != nil {
		var e *Error
		switch {
		case errors.As(err, &e) && e.Code == 403:
			return res, oerrR(403, "access_denied", e.Msg, "gitlab_account")
		case errors.As(err, &e):
			return res, oerrR(502, "temporarily_unavailable", e.Msg, "gitlab_unavailable")
		}
		log.Printf("GitLab 登入:對應帳號失敗:%v", err)
		return res, oerr(500, "server_error", "伺服器錯誤,請稍後再試")
	}
	token := RandID("", 24)
	err = s.putGitLabTxn(gitlabKindConfirm, token, GitLabConfirmTTL, model.OAuthToken{ClientID: t.ClientID, UserID: u.ID, Org: t.Org, Scope: t.Scope,
		Resource: t.Resource, RedirectURI: t.RedirectURI, CodeChallenge: t.CodeChallenge, FamilyID: gitlabBindHash(binding) + "." + state})
	if err != nil {
		log.Printf("GitLab 登入:存確認交易失敗:%v", err)
		return res, oerr(500, "server_error", "伺服器錯誤,請稍後再試")
	}
	res.Confirm = &OAuthGitLabConfirm{Token: token, UserName: u.Name, UserEmail: u.Email, ClientName: chk.Client.Name, RedirectURI: chk.RedirectURI,
		Org: chk.Org, Grant: grantOfScope(t.Scope), Scope: t.Scope}
	return res, nil
}

// ConfirmOAuthGitLab 確認頁送出(POST /oauth/gitlab/confirm):綁同一個 cookie、5 分鐘、用一次;allow 才簽發授權碼。
// 呼叫前 HTTP 層必須已拒絕跨站送出
func (s *Service) ConfirmOAuthGitLab(token, binding string, allow bool) (*OAuthGitLabResult, error) {
	if !s.GitLabEnabled() {
		return nil, ErrOAuthGitLabInvalid
	}
	t, state, err := s.boundGitLabTxn(gitlabKindConfirm, token, binding)
	if err != nil {
		log.Printf("GitLab 登入:查確認交易失敗:%v", err)
		return nil, err
	}
	if t == nil || t.UserID == "" {
		return nil, ErrOAuthGitLabInvalid
	}
	chk, res, err := s.gitlabConsentRequest(t, state)
	if err != nil {
		return res, err
	}
	if !allow {
		return res, oerrR(403, "access_denied", "使用者拒絕授權", "denied")
	}
	u, err := s.Store.UserByID(t.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrOAuthGitLabInvalid
		}
		log.Printf("GitLab 登入:讀使用者失敗:%v", err)
		return res, oerr(500, "server_error", "伺服器錯誤,請稍後再試")
	}
	if u.Disabled {
		return res, oerrR(403, "access_denied", "這個帳號已停用", "disabled")
	}
	code, err := s.ApproveOAuthIdentity(chk, t.CodeChallenge, grantOfScope(t.Scope), u, map[string]any{"via": "gitlab"})
	if err != nil {
		var oe *OAuthError
		if !errors.As(err, &oe) {
			log.Printf("GitLab 登入:簽發授權碼失敗:%v", err)
			err = oerr(500, "server_error", "伺服器錯誤,請稍後再試")
		}
		return res, err
	}
	res.Code = code
	return res, nil
}

// ---------------- B. CLI / 客戶端登入 ----------------

// LoopbackRedirectAllowed CLI 登入的導回網址:只收 http 的本機位址(127.0.0.1、localhost、[::1])、要帶連接埠,不能有帳密、查詢、片段
func LoopbackRedirectAllowed(raw string) bool {
	if len(raw) > 300 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "#\\") {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return false
	}
	n, err := strconv.Atoi(u.Port())
	return err == nil && n >= 1 && n <= 65535 && u.String() == raw
}

// 開始登入與換 token 各用自己的視窗(w):一邊被灌爆不影響另一邊,正常登入一次也不會在同一個視窗佔掉兩格
func (s *Service) gitlabLoginAllowed(w *rateWindow, ip string) error {
	if !s.GitLabEnabled() {
		return fail(404, "這台伺服器沒有啟用 GitLab 登入")
	}
	if !s.allowLogin(w, ip) {
		return fail(429, "登入嘗試太頻繁,請一分鐘後再試")
	}
	return nil
}

// ensureGitLabClient CLI 登入交易掛的內部用戶端(被動態註冊的名額淘汰掉也沒關係,下次再建)
func (s *Service) ensureGitLabClient() error {
	_, err := s.oauthStore().OAuthClientByID(gitlabLoginClientID)
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	err = s.oauthStore().CreateOAuthClient(model.OAuthClient{ID: gitlabLoginClientID, Name: "omixor GitLab 登入(內部)", CreatedAt: s.now()}, 0)
	if err != nil {
		if _, again := s.oauthStore().OAuthClientByID(gitlabLoginClientID); again == nil {
			return nil // 另一個請求剛建好
		}
	}
	return err
}

// StartGitLabLogin 開始 CLI / 客戶端登入:redirect 是客戶端的 loopback 網址,codeChallenge 是 PKCE S256,state 原樣帶回。
// 回傳要用瀏覽器開的網址
func (s *Service) StartGitLabLogin(redirect, device, codeChallenge, state, ip string) (string, error) {
	if err := s.gitlabLoginAllowed(&s.logins.gitlab, ip); err != nil {
		return "", err
	}
	if !LoopbackRedirectAllowed(redirect) {
		return "", fail(400, "redirect 只能是 http 的本機網址(http://127.0.0.1:<埠>/…)")
	}
	if !pkceChallengeRE.MatchString(codeChallenge) {
		return "", fail(400, "codeChallenge 要是 PKCE S256 的值")
	}
	if state == "" || len(state) > 512 {
		return "", fail(400, "要給 state(最長 512)")
	}
	if r := []rune(strings.TrimSpace(device)); len(r) > 80 {
		device = string(r[:80])
	}
	s.purgeOAuth()
	if err := s.ensureGitLabClient(); err != nil {
		return "", err
	}
	id := RandID("", 24)
	err := s.putGitLabTxn(gitlabKindStart, id, GitLabTxnTTL, model.OAuthToken{ClientID: gitlabLoginClientID, Resource: device,
		RedirectURI: redirect, CodeChallenge: codeChallenge, FamilyID: "." + state})
	if err != nil {
		return "", err
	}
	return s.GitLab.Public + "/api/auth/gitlab/login?id=" + url.QueryEscape(id), nil
}

// errGitLabLoginInvalid 瀏覽器端的交易不對(不認得、逾時、重複使用、cookie 不符)
var errGitLabLoginInvalid = &Error{Code: 400, Msg: "GitLab 登入已失效(逾時、重複使用,或不是在同一個瀏覽器開始的),請回到終端機或 app 重新開始"}

// GitLabLoginRedirect 瀏覽器開登入網址(只能用一次):回傳 GitLab 授權頁與綁定密鑰(放進 HttpOnly cookie)
func (s *Service) GitLabLoginRedirect(id string) (loginURL, binding string, err error) {
	if !s.GitLabEnabled() {
		return "", "", fail(404, "這台伺服器沒有啟用 GitLab 登入")
	}
	t, err := s.liveGitLabTxn(gitlabKindStart, id)
	if err != nil {
		return "", "", err
	}
	if t == nil {
		return "", "", errGitLabLoginInvalid
	}
	if used, err := s.useGitLabTxn(t.ID); err != nil {
		return "", "", err
	} else if !used {
		return "", "", errGitLabLoginInvalid
	}
	glState, binding := RandID("", 24), RandID("", 24)
	err = s.putGitLabTxn(gitlabKindCLI, glState, GitLabTxnTTL, model.OAuthToken{ClientID: gitlabLoginClientID, Resource: t.Resource,
		RedirectURI: t.RedirectURI, CodeChallenge: t.CodeChallenge, FamilyID: gitlabBindHash(binding) + t.FamilyID})
	if err != nil {
		return "", "", err
	}
	return gitlabAuthorizeURL(s.GitLab, GitLabLoginCallbackPath, glState, binding), binding, nil
}

// GitLabLoginCallback GitLab 導回(GET /api/auth/gitlab/callback):回傳要 303 過去的 loopback 網址
// (成功帶 code、state;身分不能登入等帶 error、error_description、state)。交易本身不對才回 error(不導回)
func (s *Service) GitLabLoginCallback(glState, glCode, glError, binding string) (string, error) {
	if !s.GitLabEnabled() {
		return "", fail(404, "這台伺服器沒有啟用 GitLab 登入")
	}
	if len(glCode) > 2000 || (glCode == "" && glError == "") {
		return "", errGitLabLoginInvalid
	}
	t, state, err := s.boundGitLabTxn(gitlabKindCLI, glState, binding)
	if err != nil {
		return "", err
	}
	if t == nil || !LoopbackRedirectAllowed(t.RedirectURI) {
		return "", errGitLabLoginInvalid
	}
	back := func(kv ...string) (string, error) {
		u, err := url.Parse(t.RedirectURI)
		if err != nil {
			return "", err
		}
		q := url.Values{"state": {state}}
		for i := 0; i+1 < len(kv); i += 2 {
			q.Set(kv[i], kv[i+1])
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	if glError != "" {
		return back("error", "access_denied", "error_description", "GitLab 登入沒有完成")
	}
	user, err := s.authenticateGitLab(glCode, s.GitLab.Public+GitLabLoginCallbackPath, gitlabVerifier(binding))
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			return "", err
		}
		code := "access_denied"
		if e.Code >= 500 {
			code = "temporarily_unavailable"
		}
		return back("error", code, "error_description", e.Msg)
	}
	code := RandID("omg_", 32)
	err = s.putGitLabTxn(gitlabKindCode, code, GitLabCodeTTL, model.OAuthToken{ClientID: gitlabLoginClientID, UserID: user.ID,
		Resource: t.Resource, CodeChallenge: t.CodeChallenge})
	if err != nil {
		return "", err
	}
	return back("code", code)
}

// LoginWithGitLab 用導回 loopback 的一次性碼加 PKCE verifier 換 omixor token(回應同其他登入方式)
func (s *Service) LoginWithGitLab(code, codeVerifier, ip string) (*LoginResult, error) {
	if err := s.gitlabLoginAllowed(&s.logins.gitlabToken, ip); err != nil {
		return nil, err
	}
	invalid := fail(401, "GitLab 登入碼無效、已過期或已用過,請重新開始")
	if code == "" || codeVerifier == "" || len(codeVerifier) > 200 {
		return nil, invalid
	}
	t, err := s.liveGitLabTxn(gitlabKindCode, code)
	if err != nil {
		return nil, err
	}
	// verifier 不對不作廢:只看到碼的人猜不出 verifier,也不能因此弄壞使用者的登入
	if t == nil || t.UserID == "" || !pkceOK(codeVerifier, t.CodeChallenge) {
		return nil, invalid
	}
	if used, err := s.useGitLabTxn(t.ID); err != nil {
		return nil, err
	} else if !used {
		return nil, invalid
	}
	u, err := s.Store.UserByID(t.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, invalid
		}
		return nil, err
	}
	if u.Disabled {
		return nil, fail(403, "這個帳號已停用")
	}
	return s.issueToken(u, t.Resource, s.mt(), "gitlab")
}
