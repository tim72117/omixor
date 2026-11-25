package service

import (
	"crypto/subtle"
	"encoding/hex"

	"omixor-core/model"
)

// 同意畫面的 CSRF token。只靠表單裡的值擋不住「別站自己抓一份同意畫面、再叫受害者的瀏覽器代送」,
// 所以 token 另外綁定瀏覽器:HTTP 層在 GET 同意畫面時設一個 HttpOnly cookie(同一個瀏覽器的多個分頁共用同一個值),
// 這裡把它的雜湊存在 family_id,送出時要帶同一個 cookie。

func oauthBrowserHash(browser string) string {
	return "csrf-browser:" + hex.EncodeToString(oauthHash("csrf-browser", browser))
}

// NewOAuthCSRF 同意畫面這一次載入的 CSRF token(只存雜湊、15 分鐘、用一次);GET 有頻率限制(每個來源、整個服務)。
// token 綁定這一次授權請求(client、resource、redirect_uri、code_challenge、客戶端要求的 scope 範圍)與瀏覽器(browser 是 cookie 的值):
// 送出時任何一項不同都驗證失敗
func (s *Service) NewOAuthCSRF(c *OAuthAuthChecked, challenge, ip, browser string) (string, error) {
	if !s.oauth.page.allow(s.now(), limitKey(ip), 30, 300) {
		return "", oerr(429, "temporarily_unavailable", "太頻繁,請稍後再試")
	}
	s.purgeOAuth()
	raw := RandID("", 24)
	now := s.now()
	err := s.oauthStore().PutOAuthToken(model.OAuthToken{ID: RandID("oa_", 9), Kind: model.OAuthKindCSRF, Hash: oauthHash(model.OAuthKindCSRF, raw),
		ClientID: c.Client.ID, Scope: csrfScope(c), Resource: c.Resource, RedirectURI: c.RedirectURI, CodeChallenge: challenge,
		FamilyID: oauthBrowserHash(browser), CreatedAt: now, ExpiresAt: now.Add(OAuthCSRFTTL)})
	return raw, err
}

// csrfScope CSRF token 綁定的 scope 範圍:客戶端有要求管理員 = 管理員,有要求寫入 = 讀寫,否則唯讀
func csrfScope(c *OAuthAuthChecked) string {
	switch {
	case c.AdminRequested:
		return scopeOfGrant(OAuthGrantAdmin)
	case c.WriteRequested:
		return scopeOfGrant(OAuthGrantWrite)
	}
	return scopeOfGrant(OAuthGrantRead)
}

// VerifyOAuthCSRF 驗證並用掉 CSRF token(要是同一個請求、同一個瀏覽器的;沒過期、沒用過)。瀏覽器不符不消耗 token
func (s *Service) VerifyOAuthCSRF(raw string, c *OAuthAuthChecked, challenge, browser string) bool {
	if raw == "" || browser == "" {
		return false
	}
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindCSRF, oauthHash(model.OAuthKindCSRF, raw))
	if err != nil || t.ClientID != c.Client.ID || t.Resource != c.Resource || t.RedirectURI != c.RedirectURI || t.CodeChallenge != challenge || t.Scope != csrfScope(c) ||
		subtle.ConstantTimeCompare([]byte(t.FamilyID), []byte(oauthBrowserHash(browser))) != 1 {
		return false
	}
	return s.oauthStore().ExchangeOAuthToken(t.ID, s.now(), nil) == nil
}
