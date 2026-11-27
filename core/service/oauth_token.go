package service

// oauth_token.go token 端點:授權碼換 token(PKCE)、refresh 輪替

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

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

// oauthIssue 由上一代(授權碼或被輪替的 refresh)簽發新的 access 與 refresh;scope 一律沿用上一代的(只能相同或更窄,不會升級)
func (s *Service) oauthIssue(t model.OAuthToken, scope string, now time.Time) (access, refresh model.OAuthToken, resp *OAuthTokenResponse) {
	scope = normalizeScope(scope)
	a, r := RandID(oauthPrefixAccess, 32), RandID(oauthPrefixRefresh, 32)
	access = model.OAuthToken{ID: RandID("oa_", 9), Kind: model.OAuthKindAccess, Hash: oauthHash(model.OAuthKindAccess, a), ClientID: t.ClientID, UserID: t.UserID,
		Org: t.Org, Scope: scope, Resource: t.Resource, FamilyID: t.FamilyID, CreatedAt: now, ExpiresAt: now.Add(OAuthAccessTTL)}
	refresh = model.OAuthToken{ID: RandID("oa_", 9), Kind: model.OAuthKindRefresh, Hash: oauthHash(model.OAuthKindRefresh, r), ClientID: t.ClientID, UserID: t.UserID,
		Org: t.Org, Scope: scope, Resource: t.Resource, FamilyID: t.FamilyID, CreatedAt: now, ExpiresAt: now.Add(OAuthRefreshTTL)}
	resp = &OAuthTokenResponse{AccessToken: a, TokenType: "Bearer", ExpiresIn: int(OAuthAccessTTL / time.Second), RefreshToken: r, Scope: scope}
	return
}

// refreshScope refresh 請求的 scope 參數對照原本授權的 scope(granted):沒帶 = 沿用原本的;帶了就不能超出原本授權(超出或不認得 → ok=false,invalid_scope);
// 只帶 mcp:read 而原本是讀寫 = 縮小成唯讀(之後這一族都是唯讀,不能再升回去)
func refreshScope(want, granted string) (string, bool) {
	granted = normalizeScope(granted)
	fields := strings.Fields(want)
	if len(fields) == 0 {
		return granted, true
	}
	if granted == model.OAuthScopeWorker { // 執行者授權:只能原樣續期
		for _, x := range fields {
			if x != model.OAuthScopeWorker && x != model.OAuthScopeOffline {
				return "", false
			}
		}
		return granted, true
	}
	asked, level := false, 0
	for _, x := range fields {
		switch x {
		case model.OAuthScopeOffline:
		case model.OAuthScopeRead:
			asked = true
		case model.OAuthScopeWrite:
			asked = true
			if scopeLevel(granted) < 1 {
				return "", false
			}
			level = max(level, 1)
		case model.OAuthScopeAdmin:
			asked = true
			if scopeLevel(granted) < 2 {
				return "", false
			}
			level = 2
		default:
			return "", false
		}
	}
	if !asked { // 只帶 offline_access:沿用
		return granted, true
	}
	// 帶了什麼就縮到那一級(最高的那個);write 隱含 read、admin 隱含 write
	return scopeOfGrant([]string{OAuthGrantRead, OAuthGrantWrite, OAuthGrantAdmin}[level]), true
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
	cl, err := s.oauthStore().OAuthClientByID(r.ClientID)
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
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindCode, oauthHash(model.OAuthKindCode, r.Code))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, oerr(400, "invalid_grant", "code 無效或已過期")
		}
		return nil, err
	}
	now := s.now()
	if t.UsedAt != nil { // 重用:撤銷這個 code 換出來的 token
		_ = s.oauthStore().RevokeOAuthFamily(t.FamilyID, now)
		return nil, oerr(400, "invalid_grant", "code 已經用過")
	}
	// 不論後面驗證成敗,code 都只能用一次(驗證失敗也作廢,擋住猜 verifier)
	burn := func(e *OAuthError) (*OAuthTokenResponse, error) {
		_ = s.oauthStore().ExchangeOAuthToken(t.ID, now, nil)
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
	access, refresh, resp := s.oauthIssue(*t, t.Scope, now) // 授權碼的 scope 就是使用者同意的,換 token 時不能改
	capOAuthExpiry(&access, &refresh, resp, s.ldapCodeDeadline(u, now), now)
	if err := s.oauthStore().ExchangeOAuthToken(t.ID, now, []model.OAuthToken{access, refresh}); err != nil {
		if errors.Is(err, store.ErrStale) { // 同時有另一個請求先換走了
			_ = s.oauthStore().RevokeOAuthFamily(t.FamilyID, now)
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
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindRefresh, oauthHash(model.OAuthKindRefresh, r.RefreshToken))
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
		_ = s.oauthStore().RevokeOAuthFamily(t.FamilyID, now)
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
	newScope, ok := refreshScope(r.Scope, t.Scope)
	if !ok {
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
	deadline, err := s.ldapRefresh(u, t, now) // 目錄身分:每次續期都要向目錄確認(見 oauth_ldap.go)
	if err != nil {
		return nil, err
	}
	access, refresh, resp := s.oauthIssue(*t, newScope, now)
	capOAuthExpiry(&access, &refresh, resp, deadline, now)
	if err := s.oauthStore().ExchangeOAuthToken(t.ID, now, []model.OAuthToken{access, refresh}); err != nil {
		if errors.Is(err, store.ErrStale) { // 並行的另一個請求先輪替了:算重用
			obs.Result = "reuse_detected"
			_ = s.oauthStore().RevokeOAuthFamily(t.FamilyID, now)
			return nil, oerr(400, "invalid_grant", "refresh_token 已經用過,這組授權已全部撤銷,請重新連線")
		}
		return nil, err
	}
	obs.Result = "refreshed"
	return resp, nil
}
