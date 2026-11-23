package service

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"omixor-core/model"
	"omixor-core/store"
)

// OAuth:讓 Claude 自訂 connector 用 OAuth 2.1(授權碼 + PKCE S256、動態註冊 DCR、refresh 輪替)連組織的 MCP 端點。
//
//   - 身分證明:使用者在已登入的 omixor(手機配對)產生一次性配對碼,在同意畫面輸入;配對碼的規則同 ClaimPairCode(只存雜湊、5 分鐘、用一次),
//     但這裡「不發登入 token」,只用它確認「這個人是誰」,然後簽發授權碼
//   - 授權永遠是唯讀:scope 只有 mcp:read;access token 只能連該組織的 MCP 端點(resource 綁定),只能用登記為唯讀的工具(model.CoreTool.ReadOnly);
//     REST(/api/*)一律拒絕(Authenticate 看到 omo_ 回 403)
//   - 存放:omixor_oauth_clients、omixor_oauth_tokens(獨立的表,不碰 omixor_tokens);code、access、refresh、csrf 都只存雜湊
//   - 這一層不知道對外網址:resource 的字串比對與「resource → 組織」由 core/oauth 的 HTTP 層做,這裡收到的 org 已經解出來
const (
	OAuthAccessTTL  = 600 * time.Second
	OAuthRefreshTTL = 30 * 24 * time.Hour
	OAuthCodeTTL    = 60 * time.Second
	OAuthCSRFTTL    = 15 * time.Minute
	// OAuthMaxClients 動態註冊的用戶端上限
	OAuthMaxClients = 200
	// OAuthMaxFailures、OAuthLockTime 配對碼輸錯累計(全伺服器共用,同 LDAP / 配對的全域限制)超過就鎖
	OAuthMaxFailures = 10
	OAuthLockTime    = 10 * time.Minute

	oauthPrefixAccess  = model.OAuthTokenPrefix
	oauthPrefixRefresh = "omr_"
	oauthPrefixCode    = "omc_"
)

// OAuthError OAuth 的錯誤(RFC 6749):Code 是 error 欄位的值,Status 是 HTTP 狀態碼;
// NoRedirect = 授權請求的 client_id 或 redirect_uri 不可信,不能把錯誤導回 redirect_uri,要直接顯示在頁面上
type OAuthError struct {
	Status     int
	Code       string
	Desc       string
	NoRedirect bool
	// Reason 觀測用的細分原因(只進 log,不外露):locked / disabled / not_member
	Reason string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Desc }

func oerr(status int, code, desc string) *OAuthError {
	return &OAuthError{Status: status, Code: code, Desc: desc}
}

// 固定視窗的頻率限制(同 allowLogin:每個來源、整個服務各一個上限,視窗 1 分鐘;只有放行的請求才佔名額,所以 map 不會無限長)
type rateWindow struct {
	mu     sync.Mutex
	window time.Time
	per    map[string]int
	all    int
}

func (r *rateWindow) allow(now time.Time, key string, perMax, totalMax int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.per == nil || now.Sub(r.window) >= time.Minute {
		r.window, r.per, r.all = now, map[string]int{}, 0
	}
	if r.per[key] >= perMax || r.all >= totalMax {
		return false
	}
	r.per[key]++
	r.all++
	return true
}

type oauthState struct {
	reg, page rateWindow
	mu        sync.Mutex
	fails     int
	failsFrom time.Time
	lockedTo  time.Time
	purgedAt  time.Time
}

func (s *Service) oauthLocked() (bool, time.Duration) {
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	if now := s.now(); now.Before(s.oauth.lockedTo) {
		return true, s.oauth.lockedTo.Sub(now)
	}
	return false, 0
}

// oauthFailed 記一次配對碼錯誤;累計到 OAuthMaxFailures 就鎖 OAuthLockTime(全域)
func (s *Service) oauthFailed() {
	s.oauth.mu.Lock()
	defer s.oauth.mu.Unlock()
	now := s.now()
	if s.oauth.fails == 0 || now.Sub(s.oauth.failsFrom) >= OAuthLockTime {
		s.oauth.fails, s.oauth.failsFrom = 0, now
	}
	s.oauth.fails++
	if s.oauth.fails >= OAuthMaxFailures {
		s.oauth.lockedTo = now.Add(OAuthLockTime)
		s.oauth.fails = 0
	}
}

func oauthHash(kind, raw string) []byte { return HashToken("oauth:" + kind + ":" + raw) }

// ---------------- 動態註冊 ----------------

var claudeCallbacks = []string{"https://claude.ai/api/mcp/auth_callback", "https://claude.com/api/mcp/auth_callback"}

// OAuthRedirectAllowed 註冊時允許的 redirect_uri:Claude 的兩個 callback(完全相同),或本機 http://127.0.0.1|localhost[:任意埠]/callback
func OAuthRedirectAllowed(raw string) bool {
	for _, c := range claudeCallbacks {
		if raw == c {
			return true
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Opaque != "" || u.Path != "/callback" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	if h := u.Hostname(); h != "127.0.0.1" && h != "localhost" {
		return false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return false
		}
	}
	return u.String() == raw
}

// OAuthClientInput POST /oauth/register 的內容(RFC 7591;用不到的欄位忽略)
type OAuthClientInput struct {
	ClientName              any `json:"client_name"`
	RedirectURIs            any `json:"redirect_uris"`
	TokenEndpointAuthMethod any `json:"token_endpoint_auth_method"`
	GrantTypes              any `json:"grant_types"`
	ResponseTypes           any `json:"response_types"`
	Scope                   any `json:"scope"`
}

func stringList(v any) ([]string, bool) {
	l, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(l))
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// cleanClientName 對方自己申報的名稱:去掉控制字元與方向控制字元、壓縮空白、最長 64 字
func cleanClientName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError, unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			sb.WriteRune(' ')
		default:
			sb.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(sb.String()), " ")
	if r := []rune(out); len(r) > 64 {
		out = string(r[:64])
	}
	if out == "" {
		return "(未命名)"
	}
	return out
}

// RegisterOAuthClient 動態註冊(不用登入;每個來源與全服務都有頻率限制,用戶端總數有上限)
func (s *Service) RegisterOAuthClient(in OAuthClientInput, ip string) (*model.OAuthClient, error) {
	per, total := s.OAuthRegPerIP, s.OAuthRegTotal
	if per == 0 {
		per = 5
	}
	if total == 0 {
		total = 30
	}
	if !s.oauth.reg.allow(s.now(), ip, per, total) {
		return nil, oerr(429, "temporarily_unavailable", "註冊太頻繁,請稍後再試")
	}
	uris, ok := stringList(in.RedirectURIs)
	if !ok || len(uris) == 0 || len(uris) > 5 {
		return nil, oerr(400, "invalid_redirect_uri", "redirect_uris 不在允許清單")
	}
	for _, u := range uris {
		if !OAuthRedirectAllowed(u) {
			return nil, oerr(400, "invalid_redirect_uri", "redirect_uris 不在允許清單")
		}
	}
	if in.TokenEndpointAuthMethod != nil && in.TokenEndpointAuthMethod != "none" {
		return nil, oerr(400, "invalid_client_metadata", "只支援 token_endpoint_auth_method=none")
	}
	grants := []string{"authorization_code", "refresh_token"}
	if in.GrantTypes != nil {
		g, ok := stringList(in.GrantTypes)
		if !ok || len(g) == 0 {
			return nil, oerr(400, "invalid_client_metadata", "grant_types 不支援")
		}
		for _, x := range g {
			if x != "authorization_code" && x != "refresh_token" {
				return nil, oerr(400, "invalid_client_metadata", "grant_types 不支援")
			}
		}
		grants = g
	}
	if in.ResponseTypes != nil {
		r, ok := stringList(in.ResponseTypes)
		if !ok || len(r) != 1 || r[0] != "code" {
			return nil, oerr(400, "invalid_client_metadata", "response_types 只支援 code")
		}
	}
	name := ""
	if in.ClientName != nil {
		n, ok := in.ClientName.(string)
		if !ok {
			return nil, oerr(400, "invalid_client_metadata", "client_name 要是字串")
		}
		name = n
	}
	if in.Scope != nil {
		if _, ok := in.Scope.(string); !ok {
			return nil, oerr(400, "invalid_client_metadata", "scope 要是字串")
		}
	}
	cl := model.OAuthClient{ID: RandID("oc_", 16), Name: cleanClientName(name), RedirectURIs: uris, GrantTypes: grants, CreatedAt: s.now().Truncate(time.Second)}
	if err := s.Store.CreateOAuthClient(cl, OAuthMaxClients); err != nil {
		if errors.Is(err, store.ErrOAuthFull) {
			return nil, oerr(429, "temporarily_unavailable", "已註冊的用戶端太多,請稍後再試")
		}
		return nil, err
	}
	return &cl, nil
}

// ---------------- 授權(同意畫面) ----------------

var pkceChallengeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var pkceVerifierRE = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// OAuthAuthRequest GET / POST /oauth/authorize 的參數(Org 是 HTTP 層從 resource 解出來的組織,resource 不合法時是空字串)
type OAuthAuthRequest struct {
	ClientID, RedirectURI, ResponseType string
	CodeChallenge, CodeChallengeMethod  string
	State, Scope                        string
	Resources                           []string
	Org                                 string
}

// OAuthAuthChecked 驗證過的授權請求
type OAuthAuthChecked struct {
	Client      *model.OAuthClient
	RedirectURI string
	Resource    string
	Org         string
	Scope       string // 實際授予的(永遠是 mcp:read)
}

// CheckOAuthAuthorize 驗證授權請求;client_id 或 redirect_uri 不可信的錯誤帶 NoRedirect
func (s *Service) CheckOAuthAuthorize(r OAuthAuthRequest) (*OAuthAuthChecked, error) {
	bad := func(code, desc string) error { return oerr(400, code, desc) }
	cl, err := s.Store.OAuthClientByID(r.ClientID)
	if err != nil {
		if r.ClientID == "" || errors.Is(err, store.ErrNotFound) {
			return nil, &OAuthError{Status: 400, Code: "invalid_request", Desc: "未知的 client_id", NoRedirect: true}
		}
		return nil, err
	}
	okURI := false
	for _, u := range cl.RedirectURIs {
		if u == r.RedirectURI && r.RedirectURI != "" {
			okURI = true
		}
	}
	if !okURI {
		return nil, &OAuthError{Status: 400, Code: "invalid_request", Desc: "redirect_uri 與註冊的不符", NoRedirect: true}
	}
	if r.ResponseType != "code" {
		return nil, bad("unsupported_response_type", "只支援 response_type=code")
	}
	if r.CodeChallenge == "" {
		return nil, bad("invalid_request", "必須帶 PKCE code_challenge")
	}
	if r.CodeChallengeMethod != "S256" {
		return nil, bad("invalid_request", "code_challenge_method 必須是 S256")
	}
	if !pkceChallengeRE.MatchString(r.CodeChallenge) {
		return nil, bad("invalid_request", "code_challenge 格式不對")
	}
	if len(r.Resources) != 1 || r.Org == "" {
		return nil, bad("invalid_target", "resource 必須是這個伺服器的組織 MCP 網址")
	}
	return &OAuthAuthChecked{Client: cl, RedirectURI: r.RedirectURI, Resource: r.Resources[0], Org: r.Org, Scope: model.OAuthScopeRead}, nil
}

// purgeOAuth 清掉過期超過一天的列(最多每分鐘一次)
func (s *Service) purgeOAuth() {
	now := s.now()
	s.oauth.mu.Lock()
	due := now.Sub(s.oauth.purgedAt) >= time.Minute
	if due {
		s.oauth.purgedAt = now
	}
	s.oauth.mu.Unlock()
	if due {
		_, _ = s.Store.PurgeOAuthTokens(now.Add(-24 * time.Hour))
	}
}

// NewOAuthCSRF 同意畫面這一次載入的 CSRF token(只存雜湊、15 分鐘、用一次);GET 有頻率限制(每個來源、整個服務)
func (s *Service) NewOAuthCSRF(clientID, ip string) (string, error) {
	if !s.oauth.page.allow(s.now(), ip, 30, 300) {
		return "", oerr(429, "temporarily_unavailable", "太頻繁,請稍後再試")
	}
	s.purgeOAuth()
	raw := RandID("", 24)
	now := s.now()
	err := s.Store.PutOAuthToken(model.OAuthToken{ID: RandID("oa_", 9), Kind: model.OAuthKindCSRF, Hash: oauthHash(model.OAuthKindCSRF, raw),
		ClientID: clientID, CreatedAt: now, ExpiresAt: now.Add(OAuthCSRFTTL)})
	return raw, err
}

// VerifyOAuthCSRF 驗證並用掉 CSRF token(要是同一個 client 的、沒過期、沒用過)
func (s *Service) VerifyOAuthCSRF(raw, clientID string) bool {
	if raw == "" {
		return false
	}
	t, err := s.Store.OAuthTokenByHash(model.OAuthKindCSRF, oauthHash(model.OAuthKindCSRF, raw))
	if err != nil || t.ClientID != clientID {
		return false
	}
	return s.Store.ExchangeOAuthToken(t.ID, s.now(), nil) == nil
}

// ErrOAuthPairInvalid 配對碼不對(無效、過期、用過、格式不對、帳號停用以外的一切,同一句話,不洩漏碼存不存在)
var ErrOAuthPairInvalid = oerr(403, "access_denied", "配對碼無效、已過期或已用過,請在 omixor 重新產生")

// OAuthLockedError 全域鎖定中
func oauthLockedErr(left time.Duration) error {
	e := oerr(429, "temporarily_unavailable", "配對碼輸入錯誤次數過多,已暫時鎖定 "+strconv.Itoa(int(left.Minutes())+1)+" 分鐘內不能再試")
	e.Reason = "locked"
	return e
}

// oerrR 帶觀測原因的 OAuthError
func oerrR(status int, code, desc, reason string) *OAuthError {
	e := oerr(status, code, desc)
	e.Reason = reason
	return e
}

// ApproveOAuth 同意畫面送出:用配對碼證明身分,成功就簽發授權碼(60 秒、用一次),回傳授權碼原文。
// 配對碼用過就失效(成功才消耗;和 ClaimPairCode 同一套規則)。失敗一律 ErrOAuthPairInvalid(頻率限制、鎖定另外回 429)
func (s *Service) ApproveOAuth(c *OAuthAuthChecked, challenge, pairCode, ip string) (string, error) {
	if locked, left := s.oauthLocked(); locked {
		return "", oauthLockedErr(left)
	}
	pt, u, err := s.redeemPair(pairCode, ip)
	if err != nil {
		if Code(err) == 429 {
			return "", oerrR(429, "temporarily_unavailable", "嘗試太頻繁,請一分鐘後再試", "locked")
		}
		if Code(err) == 401 {
			s.oauthFailed()
			return "", ErrOAuthPairInvalid
		}
		return "", err
	}
	p := &model.Principal{User: *u, Via: model.ViaOAuth}
	if u.Disabled {
		s.Audit(p, "oauth.denied", c.Org, map[string]any{"reason": "disabled", "client": c.Client.ID})
		return "", oerrR(403, "access_denied", "這個帳號已停用", "disabled")
	}
	if !p.ServerAdmin() {
		if _, err := s.Store.MemberRole(c.Org, u.ID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.Audit(p, "oauth.denied", c.Org, map[string]any{"reason": "not-member", "client": c.Client.ID})
				return "", oerrR(403, "access_denied", "你不是組織 "+c.Org+" 的成員,不能授權", "not_member")
			}
			return "", err
		}
	} else if _, err := s.Store.GetOrg(c.Org); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", oerr(403, "access_denied", "找不到組織 "+c.Org)
		}
		return "", err
	}
	raw := RandID(oauthPrefixCode, 32)
	now := s.now()
	err = s.Store.PutOAuthToken(model.OAuthToken{ID: RandID("oa_", 9), Kind: model.OAuthKindCode, Hash: oauthHash(model.OAuthKindCode, raw),
		ClientID: c.Client.ID, UserID: u.ID, Org: c.Org, Scope: c.Scope, Resource: c.Resource, RedirectURI: c.RedirectURI, CodeChallenge: challenge,
		FamilyID: RandID("of_", 12), CreatedAt: now, ExpiresAt: now.Add(OAuthCodeTTL)})
	if err != nil {
		return "", err
	}
	s.Audit(p, "oauth.authorize", c.Org, map[string]any{"client": c.Client.ID, "clientName": c.Client.Name, "pair": pt.ID})
	return raw, nil
}

// ---------------- token 端點 ----------------

// OAuthTokenResponse RFC 6749 的 token 回應
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// OAuthTokenRequest POST /oauth/token 的參數(application/x-www-form-urlencoded)
type OAuthTokenRequest struct {
	GrantType, ClientID, Code, RedirectURI, CodeVerifier, RefreshToken, Scope string
	Resources                                                                 []string
}

func pkceOK(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

func (s *Service) oauthIssue(t model.OAuthToken, now time.Time) (access, refresh model.OAuthToken, resp *OAuthTokenResponse) {
	a, r := RandID(oauthPrefixAccess, 32), RandID(oauthPrefixRefresh, 32)
	access = model.OAuthToken{ID: RandID("oa_", 9), Kind: model.OAuthKindAccess, Hash: oauthHash(model.OAuthKindAccess, a), ClientID: t.ClientID, UserID: t.UserID,
		Org: t.Org, Scope: model.OAuthScopeRead, Resource: t.Resource, FamilyID: t.FamilyID, CreatedAt: now, ExpiresAt: now.Add(OAuthAccessTTL)}
	refresh = model.OAuthToken{ID: RandID("oa_", 9), Kind: model.OAuthKindRefresh, Hash: oauthHash(model.OAuthKindRefresh, r), ClientID: t.ClientID, UserID: t.UserID,
		Org: t.Org, Scope: model.OAuthScopeRead, Resource: t.Resource, FamilyID: t.FamilyID, CreatedAt: now, ExpiresAt: now.Add(OAuthRefreshTTL)}
	resp = &OAuthTokenResponse{AccessToken: a, TokenType: "Bearer", ExpiresIn: int(OAuthAccessTTL / time.Second), RefreshToken: r, Scope: model.OAuthScopeRead}
	return
}

func scopeSubset(want string) bool {
	for _, x := range strings.Fields(want) {
		if x != model.OAuthScopeRead && x != model.OAuthScopeOffline {
			return false
		}
	}
	return true
}

func resourceMismatch(sent []string, bound string) bool {
	if len(sent) == 0 {
		return false
	}
	return len(sent) != 1 || sent[0] != bound
}

// OAuthToken 換 token:authorization_code(PKCE)或 refresh_token(輪替;用過的 refresh 再出現 = 整個家族撤銷)
func (s *Service) OAuthToken(r OAuthTokenRequest, ip string) (*OAuthTokenResponse, error) {
	resp, _, err := s.OAuthTokenObserved(r, ip)
	return resp, err
}

// OAuthTokenObs token 端點的觀測資訊(只進 log):Result = issued / refreshed / reuse_detected / error;
// AccessRemainingS = refresh 當下,上一個 access token 還剩幾秒(未知或已過期 0)
type OAuthTokenObs struct {
	Result           string
	AccessRemainingS int
}

// OAuthTokenObserved 同 OAuthToken,另回傳觀測資訊
func (s *Service) OAuthTokenObserved(r OAuthTokenRequest, ip string) (*OAuthTokenResponse, OAuthTokenObs, error) {
	obs := OAuthTokenObs{Result: "error"}
	resp, err := s.oauthToken(r, &obs)
	if err != nil && obs.Result != "reuse_detected" {
		obs.Result = "error"
	}
	return resp, obs, err
}

func (s *Service) oauthToken(r OAuthTokenRequest, obs *OAuthTokenObs) (*OAuthTokenResponse, error) {
	cl, err := s.Store.OAuthClientByID(r.ClientID)
	if err != nil {
		if r.ClientID == "" || errors.Is(err, store.ErrNotFound) {
			return nil, oerr(401, "invalid_client", "未知的 client_id")
		}
		return nil, err
	}
	switch r.GrantType {
	case "authorization_code":
		resp, err := s.oauthCodeGrant(cl, r)
		if err == nil {
			obs.Result = "issued"
		}
		return resp, err
	case "refresh_token":
		return s.oauthRefreshGrant(cl, r, obs)
	}
	return nil, oerr(400, "unsupported_grant_type", "只支援 authorization_code 與 refresh_token")
}

func (s *Service) oauthCodeGrant(cl *model.OAuthClient, r OAuthTokenRequest) (*OAuthTokenResponse, error) {
	if r.Code == "" {
		return nil, oerr(400, "invalid_request", "缺少 code")
	}
	t, err := s.Store.OAuthTokenByHash(model.OAuthKindCode, oauthHash(model.OAuthKindCode, r.Code))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, oerr(400, "invalid_grant", "code 無效或已過期")
		}
		return nil, err
	}
	now := s.now()
	if t.UsedAt != nil { // 重用:撤銷這個 code 換出來的 token
		_ = s.Store.RevokeOAuthFamily(t.FamilyID, now)
		return nil, oerr(400, "invalid_grant", "code 已經用過")
	}
	// 不論後面驗證成敗,code 都只能用一次(驗證失敗也作廢,擋住猜 verifier)
	burn := func(e *OAuthError) (*OAuthTokenResponse, error) {
		_ = s.Store.ExchangeOAuthToken(t.ID, now, nil)
		return nil, e
	}
	if t.RevokedAt != nil || !now.Before(t.ExpiresAt) {
		return burn(oerr(400, "invalid_grant", "code 無效或已過期"))
	}
	if t.ClientID != cl.ID {
		return burn(oerr(400, "invalid_grant", "code 不是發給這個用戶端的"))
	}
	if r.RedirectURI != t.RedirectURI {
		return burn(oerr(400, "invalid_grant", "redirect_uri 與授權請求的不同"))
	}
	if r.CodeVerifier == "" {
		return burn(oerr(400, "invalid_request", "缺少 code_verifier"))
	}
	if !pkceVerifierRE.MatchString(r.CodeVerifier) || !pkceOK(r.CodeVerifier, t.CodeChallenge) {
		return burn(oerr(400, "invalid_grant", "PKCE 驗證失敗"))
	}
	if resourceMismatch(r.Resources, t.Resource) {
		return burn(oerr(400, "invalid_target", "resource 與授權請求的不同"))
	}
	u, err := s.Store.UserByID(t.UserID)
	if err != nil || u.Disabled {
		return burn(oerr(400, "invalid_grant", "帳號無法使用"))
	}
	access, refresh, resp := s.oauthIssue(*t, now)
	if err := s.Store.ExchangeOAuthToken(t.ID, now, []model.OAuthToken{access, refresh}); err != nil {
		if errors.Is(err, store.ErrStale) { // 同時有另一個請求先換走了
			_ = s.Store.RevokeOAuthFamily(t.FamilyID, now)
			return nil, oerr(400, "invalid_grant", "code 已經用過")
		}
		return nil, err
	}
	return resp, nil
}

func (s *Service) oauthRefreshGrant(cl *model.OAuthClient, r OAuthTokenRequest, obs *OAuthTokenObs) (*OAuthTokenResponse, error) {
	if r.RefreshToken == "" {
		return nil, oerr(400, "invalid_request", "缺少 refresh_token")
	}
	t, err := s.Store.OAuthTokenByHash(model.OAuthKindRefresh, oauthHash(model.OAuthKindRefresh, r.RefreshToken))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, oerr(400, "invalid_grant", "refresh_token 無效")
		}
		return nil, err
	}
	now := s.now()
	if t.ClientID != cl.ID {
		return nil, oerr(400, "invalid_grant", "refresh_token 不是發給這個用戶端的")
	}
	if t.UsedAt != nil { // 已輪替掉的再被拿來用:可能被偷了,整個家族撤銷
		_ = s.Store.RevokeOAuthFamily(t.FamilyID, now)
		obs.Result = "reuse_detected"
		s.Audit(&model.Principal{User: model.User{ID: t.UserID}, Via: model.ViaOAuth}, "oauth.refresh_reuse", t.Org, map[string]any{"client": cl.ID})
		return nil, oerr(400, "invalid_grant", "refresh_token 已經用過,這組授權已全部撤銷,請重新連線")
	}
	if t.RevokedAt != nil || !now.Before(t.ExpiresAt) {
		return nil, oerr(400, "invalid_grant", "refresh_token 無效或已過期")
	}
	if resourceMismatch(r.Resources, t.Resource) {
		return nil, oerr(400, "invalid_target", "resource 與授權的不同")
	}
	if !scopeSubset(r.Scope) {
		return nil, oerr(400, "invalid_scope", "scope 超出原本授權")
	}
	u, err := s.Store.UserByID(t.UserID)
	if err != nil || u.Disabled {
		return nil, oerr(400, "invalid_grant", "帳號無法使用")
	}
	// 上一個 access token 和這個 refresh 同時簽發(oauthIssue 用同一個 now),所以到期 = refresh 建立時間 + OAuthAccessTTL
	if left := t.CreatedAt.Add(OAuthAccessTTL).Sub(now); left > 0 {
		obs.AccessRemainingS = int(left / time.Second)
	}
	access, refresh, resp := s.oauthIssue(*t, now)
	if err := s.Store.ExchangeOAuthToken(t.ID, now, []model.OAuthToken{access, refresh}); err != nil {
		if errors.Is(err, store.ErrStale) { // 並行的另一個請求先輪替了:算重用
			obs.Result = "reuse_detected"
			_ = s.Store.RevokeOAuthFamily(t.FamilyID, now)
			return nil, oerr(400, "invalid_grant", "refresh_token 已經用過,這組授權已全部撤銷,請重新連線")
		}
		return nil, err
	}
	obs.Result = "refreshed"
	return resp, nil
}

// ---------------- 驗證 access token ----------------

// ErrOAuthInvalidToken access token 無效、過期、撤銷、不是這個資源的(MCP 端點回 401 invalid_token)
var ErrOAuthInvalidToken = fail(401, "OAuth 的 access token 無效、已過期或不屬於這個端點")

// AuthenticateOAuth 驗證 access token(omo_…):要綁定 resource(這個端點的網址)、scope 含 mcp:read、沒過期沒撤銷、帳號沒停用。
// 回傳的身分是 ReadOnly(MCP 端點只開放唯讀工具)
func (s *Service) AuthenticateOAuth(raw, resource string) (*model.Principal, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, oauthPrefixAccess) || resource == "" {
		return nil, ErrOAuthInvalidToken
	}
	t, err := s.Store.OAuthTokenByHash(model.OAuthKindAccess, oauthHash(model.OAuthKindAccess, raw))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrOAuthInvalidToken
		}
		return nil, err
	}
	now := s.now()
	if t.RevokedAt != nil || !now.Before(t.ExpiresAt) || t.Resource != resource || !hasScope(t.Scope, model.OAuthScopeRead) {
		return nil, ErrOAuthInvalidToken
	}
	u, err := s.Store.UserByID(t.UserID)
	if err != nil {
		return nil, ErrOAuthInvalidToken
	}
	if u.Disabled {
		return nil, fail(403, "這個帳號已停用")
	}
	actor, err := s.userActorRef(u)
	if err != nil {
		return nil, err
	}
	return &model.Principal{User: *u, Token: model.Token{ID: t.ID, UserID: u.ID, Device: "OAuth", Email: u.Email, CreatedAt: model.Time{Time: t.CreatedAt}},
		Actor: actor, Via: model.ViaOAuth, ReadOnly: true}, nil
}

// OAuthAccessState 觀測用:access token 目前的狀態(valid / missing / invalid / expired / revoked / wrong_resource),不寫入、不回身分
func (s *Service) OAuthAccessState(raw, resource string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "missing"
	}
	if !strings.HasPrefix(raw, oauthPrefixAccess) {
		return "invalid"
	}
	t, err := s.Store.OAuthTokenByHash(model.OAuthKindAccess, oauthHash(model.OAuthKindAccess, raw))
	if err != nil {
		return "invalid"
	}
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case !s.now().Before(t.ExpiresAt):
		return "expired"
	case t.Resource != resource:
		return "wrong_resource"
	case !hasScope(t.Scope, model.OAuthScopeRead):
		return "invalid"
	}
	return "valid"
}

// CleanOAuthClientName 對外用的 client_name 清理(同註冊時的規則)
func CleanOAuthClientName(v any) string {
	s, _ := v.(string)
	return cleanClientName(s)
}

func hasScope(scope, want string) bool {
	for _, x := range strings.Fields(scope) {
		if x == want {
			return true
		}
	}
	return false
}
