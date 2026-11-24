package service

import (
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"omixor-core/evoleap"
	"omixor-core/model"
	"omixor-core/store"
)

// OAuth 同意畫面的 evoleap 登入(瀏覽器登入證明身分;配對碼是備援)。omixor 伺服器扮演 evoleap CLI 的角色(見 core/evoleap):
//
//  1. 同意畫面送出(via=evoleap,CSRF 與整個授權請求照配對碼那條路重驗):StartOAuthEvoleap 向 evoleap 登記回呼網址
//     <對外網址>/oauth/evoleap/callback 拿到 evoleap id,存一列登入交易(只存雜湊、10 分鐘、用一次),回傳瀏覽器要去的 evoleap 核准頁與「瀏覽器綁定密鑰」
//     (HTTP 層放進 HttpOnly cookie)
//  2. 使用者在 evoleap 登入核准後,evoleap 把瀏覽器導回回呼網址(帶 id 與 code):CompleteOAuthEvoleap 先查交易、核對 cookie 的綁定密鑰、
//     把交易標成用過(單一用途,成功失敗都一樣),再向 evoleap 換 token、對上 omixor 使用者(resolveEvoleapUser,規則同 LoginWithEvoleap,
//     但不發 omixor 的登入 token)、立刻撤銷 evoleap token,最後和配對碼共用 approveOAuth 簽發授權碼
//
// 登入交易存在 omixor_oauth_tokens(不動結構,Kind 見 model.OAuthKindEvoleapLogin):
//
//	token_hash   oauth:evoleap:<evoleap id> 的雜湊(用 evoleap id 查)
//	client_id / resource / redirect_uri / code_challenge / org   綁定的授權請求
//	scope        使用者選的授權範圍(mcp:read 或 mcp:read mcp:write)
//	family_id    <瀏覽器綁定密鑰的雜湊(64 個 hex)>.<授權請求的 state>
const (
	OAuthEvoleapTTL  = 10 * time.Minute
	oauthEvoleapName = "omixor OAuth"
	oauthBindLen     = 64 // 綁定密鑰雜湊的 hex 長度
)

// ErrOAuthEvoleapInvalid 回呼不被接受(不認得的 id、cookie 不對或沒有、逾時、重放):同一句話,不洩漏是哪一種
var ErrOAuthEvoleapInvalid = &OAuthError{Status: 400, Code: "invalid_request", NoRedirect: true, Reason: "evoleap_invalid",
	Desc: "evoleap 登入已失效(逾時、重複使用,或不是在同一個瀏覽器開始的),請回到 Claude 重新開始連線"}

// OAuthEvoleapEnabled 同意畫面要不要提供 evoleap 登入:有 evoleap 網址、沒被明確關掉
func (s *Service) OAuthEvoleapEnabled() bool {
	return !s.OAuthEvoleapOff && strings.TrimSpace(s.Evoleap) != ""
}

// OAuthEvoleapConsoleOrigin evoleap 核准頁的來源(scheme://host),同意畫面的 CSP form-action 要允許它(送出表單後 303 導去那裡);沒啟用回 ""
func (s *Service) OAuthEvoleapConsoleOrigin() string {
	if !s.OAuthEvoleapEnabled() {
		return ""
	}
	console := s.EvoleapConsole
	if strings.TrimSpace(console) == "" {
		console = s.Evoleap
	}
	u, err := url.Parse(strings.TrimSpace(console))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func oauthEvoleapHash(id string) []byte { return oauthHash("evoleap", id) }

func oauthBindHash(binding string) string {
	return hex.EncodeToString(oauthHash("evoleap-bind", binding))
}

// StartOAuthEvoleap 開始 evoleap 登入:回傳瀏覽器要導去的 evoleap 核准頁網址與瀏覽器綁定密鑰(要放進 HttpOnly cookie)。
// 呼叫前 HTTP 層必須已重驗授權請求與 CSRF。讀寫的規則同配對碼:grantWrite 但客戶端沒要求 mcp:write = 400 invalid_scope,在呼叫 evoleap 之前就擋
func (s *Service) StartOAuthEvoleap(c *OAuthAuthChecked, challenge, state, callbackURL string, grantWrite bool) (loginURL, binding string, err error) {
	if !s.OAuthEvoleapEnabled() {
		return "", "", oerr(400, "invalid_request", "這個伺服器沒有啟用 evoleap 登入,請改用配對碼")
	}
	if grantWrite && !c.WriteRequested {
		return "", "", oerr(400, "invalid_scope", "這個授權請求沒有要求 mcp:write,不能授予讀寫")
	}
	s.purgeOAuth()
	id, err := evoleap.Start(s.Evoleap, callbackURL, oauthEvoleapName)
	if err != nil {
		return "", "", oerrR(502, "temporarily_unavailable", "暫時無法連到 evoleap,請稍後再試,或改用配對碼", "evoleap_unavailable")
	}
	scope := model.OAuthScopeRead
	if grantWrite {
		scope = model.OAuthScopeRead + " " + model.OAuthScopeWrite
	}
	binding = RandID("", 24)
	now := s.now()
	err = s.Store.PutOAuthToken(model.OAuthToken{ID: RandID("oa_", 9), Kind: model.OAuthKindEvoleapLogin, Hash: oauthEvoleapHash(id),
		ClientID: c.Client.ID, Org: c.Org, Scope: scope, Resource: c.Resource, RedirectURI: c.RedirectURI, CodeChallenge: challenge,
		FamilyID: oauthBindHash(binding) + "." + state, CreatedAt: now, ExpiresAt: now.Add(OAuthEvoleapTTL)})
	if err != nil {
		return "", "", err
	}
	return evoleap.ConsoleLoginURL(s.Evoleap, s.EvoleapConsole, id), binding, nil
}

// OAuthEvoleapResult 回呼成功:要導回客戶端的授權碼與 state。失敗時若 RedirectURI 非空,代表這個轉址已經可信(交易核對過、授權請求重驗過),
// HTTP 層用 error 導回 RedirectURI?error=…&state=…;RedirectURI 空 = 不可信,直接顯示錯誤頁
type OAuthEvoleapResult struct {
	Code, RedirectURI, State string
}

// CompleteOAuthEvoleap evoleap 核准後的回呼(GET /oauth/evoleap/callback?id=&code=):binding 是瀏覽器 cookie 帶的綁定密鑰。
// 交易用過就作廢(單一用途,後面不論成敗);evoleap 的 token 換到之後一律撤銷。不會建立新使用者,除非 LoginWithEvoleap 的規則也會(見 resolveEvoleapUser)
func (s *Service) CompleteOAuthEvoleap(evoID, evoCode, binding string) (*OAuthEvoleapResult, error) {
	if !s.OAuthEvoleapEnabled() || evoID == "" || evoCode == "" || len(evoID) > 200 || len(evoCode) > 2000 || binding == "" {
		return nil, ErrOAuthEvoleapInvalid
	}
	t, err := s.Store.OAuthTokenByHash(model.OAuthKindEvoleapLogin, oauthEvoleapHash(evoID))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrOAuthEvoleapInvalid
		}
		return nil, err
	}
	bindHash, state, ok := strings.Cut(t.FamilyID, ".")
	if !ok || len(bindHash) != oauthBindLen ||
		subtle.ConstantTimeCompare([]byte(bindHash), []byte(oauthBindHash(binding))) != 1 {
		return nil, ErrOAuthEvoleapInvalid // cookie 不對:不作廢交易(不能讓別人用一個猜到的 id 把使用者的登入弄壞)
	}
	now := s.now()
	if t.RevokedAt != nil || t.UsedAt != nil || !now.Before(t.ExpiresAt) {
		return nil, ErrOAuthEvoleapInvalid
	}
	// 先標成用過:條件式(沒用過、沒過期),並行的第二個請求拿到 ErrStale
	if err := s.Store.ExchangeOAuthToken(t.ID, now, nil); err != nil {
		if errors.Is(err, store.ErrStale) {
			return nil, ErrOAuthEvoleapInvalid
		}
		return nil, err
	}
	// 交易核對過:轉址是當初驗證過的。再驗一次整個授權請求(用戶端、redirect_uri 還在不在註冊清單、resource、PKCE 格式、scope)
	grantWrite := hasScope(t.Scope, model.OAuthScopeWrite)
	chk, err := s.CheckOAuthAuthorize(OAuthAuthRequest{ClientID: t.ClientID, RedirectURI: t.RedirectURI, ResponseType: "code",
		CodeChallenge: t.CodeChallenge, CodeChallengeMethod: "S256", State: state, Scope: t.Scope, Resources: []string{t.Resource},
		Org: t.Org, Account: t.Org == ""})
	if err != nil {
		var oe *OAuthError
		if errors.As(err, &oe) && !oe.NoRedirect {
			return &OAuthEvoleapResult{RedirectURI: t.RedirectURI, State: state}, err
		}
		return nil, ErrOAuthEvoleapInvalid
	}
	res := &OAuthEvoleapResult{RedirectURI: chk.RedirectURI, State: state}
	evoTok, err := evoleap.Exchange(s.Evoleap, evoID, evoCode)
	if err != nil {
		return res, oerrR(403, "access_denied", "evoleap 登入沒有完成,請回到 Claude 重新開始連線", "evoleap_exchange")
	}
	defer evoleap.Logout(s.Evoleap, evoTok) // 成功失敗都撤銷:這個 token 只用來問「你是誰」
	code, err := s.approveOAuth(chk, t.CodeChallenge, grantWrite, func() (*model.User, map[string]any, error) {
		u, _, err := s.resolveEvoleapUser(evoTok)
		switch {
		case err == nil:
			return u, map[string]any{"via": "evoleap"}, nil
		case err == errEvoleapDisabled && u != nil:
			s.Audit(&model.Principal{User: *u, Via: model.ViaOAuth, ReadOnly: true}, "oauth.denied", chk.Org, map[string]any{"reason": "disabled", "client": chk.Client.ID, "via": "evoleap"})
			return nil, nil, oerrR(403, "access_denied", "這個帳號已停用", "disabled")
		case Code(err) == 403:
			return nil, nil, oerrR(403, "access_denied", "這個 evoleap 帳號沒有 omixor 的權限,請 omixor 管理員加入", "no_account")
		}
		return nil, nil, oerrR(502, "temporarily_unavailable", "evoleap 驗證身分失敗,請稍後再試", "evoleap_me")
	})
	if err != nil {
		var oe *OAuthError
		if !errors.As(err, &oe) {
			err = oerr(500, "server_error", "伺服器錯誤,請稍後再試")
		}
		return res, err
	}
	res.Code = code
	return res, nil
}
