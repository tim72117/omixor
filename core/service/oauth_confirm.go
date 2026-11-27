package service

import (
	"errors"
	"log"

	"omixor-core/model"
	"omixor-core/store"
)

// 同意畫面「帳號密碼」類登入(LDAP)的確認頁:密碼驗證通過後先顯示「你要用這個帳號授權」,使用者按允許才簽發授權碼。
// 交易和 GitLab 的確認頁一樣存在 omixor_oauth_tokens(kind=csrf,靠雜湊命名空間分開;不動結構):綁瀏覽器 cookie、
// GitLabConfirmTTL(5 分鐘)、只能用一次。密碼不進交易

// ErrOAuthConfirmInvalid 確認頁送出不被接受(不認得、cookie 不對、逾時、重放):同一句話
var ErrOAuthConfirmInvalid = &OAuthError{Status: 400, Code: "invalid_request", NoRedirect: true, Reason: "confirm_invalid",
	Desc: "確認頁已失效(逾時、重複送出,或不是在同一個瀏覽器開始的),請回到 Claude 重新開始連線"}

// OAuthConfirmPage 確認頁要顯示的內容(同 GitLab 確認頁)
type OAuthConfirmPage = OAuthGitLabConfirm

// StartOAuthConfirm 身分(u)已經由呼叫者證明:登記一筆等使用者確認的交易。kind 是登入方式的命名空間(例如 ldap-confirm),
// binding 是瀏覽器綁定 cookie 的值。呼叫前 HTTP 層必須已重驗授權請求與 CSRF
func (s *Service) StartOAuthConfirm(kind string, c *OAuthAuthChecked, challenge, state, grant, binding string, u *model.User) (*OAuthConfirmPage, error) {
	if err := CheckOAuthGrant(c, grant); err != nil {
		return nil, err
	}
	if binding == "" {
		return nil, ErrOAuthConfirmInvalid
	}
	if grant == OAuthGrantAdmin { // 先擋掉,讓同意畫面能停用管理員選項(簽發時還會再查一次)
		ok, err := s.isAnyOrgAdmin(u.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			s.Audit(&model.Principal{User: *u, Via: model.ViaOAuth, ReadOnly: true}, "oauth.denied", c.Org, map[string]any{"reason": "not-org-admin", "client": c.Client.ID})
			return nil, ErrOAuthNotOrgAdmin
		}
	}
	s.purgeOAuth()
	token := RandID("", 24)
	err := s.putGitLabTxn(kind, token, GitLabConfirmTTL, model.OAuthToken{ClientID: c.Client.ID, UserID: u.ID, Org: c.Org, Scope: scopeOfGrant(grant),
		Resource: c.Resource, RedirectURI: c.RedirectURI, CodeChallenge: challenge, FamilyID: gitlabBindHash(binding) + "." + state})
	if err != nil {
		return nil, err
	}
	return &OAuthConfirmPage{Token: token, UserName: u.Name, UserEmail: u.Email, ClientName: c.Client.Name, RedirectURI: c.RedirectURI,
		Org: c.Org, Grant: grant, Scope: scopeOfGrant(grant)}, nil
}

// FinishOAuthConfirm 確認頁送出:allow 才簽發授權碼;extra 寫進稽核 oauth.authorize。
// 回傳的 result 非 nil 代表轉址可信(失敗也可以導回 redirect_uri)。呼叫前 HTTP 層必須已拒絕跨站送出
func (s *Service) FinishOAuthConfirm(kind, token, binding string, allow bool, extra map[string]any) (*OAuthGitLabResult, error) {
	t, state, err := s.boundGitLabTxn(kind, token, binding)
	if err != nil {
		log.Printf("OAuth 確認頁:查交易失敗:%v", err)
		return nil, err
	}
	if t == nil || t.UserID == "" {
		return nil, ErrOAuthConfirmInvalid
	}
	chk, err := s.CheckOAuthAuthorize(OAuthAuthRequest{ClientID: t.ClientID, RedirectURI: t.RedirectURI, ResponseType: "code",
		CodeChallenge: t.CodeChallenge, CodeChallengeMethod: "S256", State: state, Scope: t.Scope, Resources: []string{t.Resource},
		Org: t.Org, Account: t.Org == "", Worker: hasScope(t.Scope, model.OAuthScopeWorker)})
	if err != nil {
		return nil, ErrOAuthConfirmInvalid
	}
	res := &OAuthGitLabResult{RedirectURI: chk.RedirectURI, State: state}
	if !allow {
		return res, oerrR(403, "access_denied", "使用者拒絕授權", "denied")
	}
	u, err := s.Store.UserByID(t.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrOAuthConfirmInvalid
		}
		log.Printf("OAuth 確認頁:讀使用者失敗:%v", err)
		return res, oerr(500, "server_error", "伺服器錯誤,請稍後再試")
	}
	if u.Disabled {
		return res, oerrR(403, "access_denied", "這個帳號已停用", "disabled")
	}
	code, err := s.ApproveOAuthIdentity(chk, t.CodeChallenge, grantOfScope(t.Scope), u, extra)
	if err != nil {
		var oe *OAuthError
		if !errors.As(err, &oe) {
			log.Printf("OAuth 確認頁:簽發授權碼失敗:%v", err)
			err = oerr(500, "server_error", "伺服器錯誤,請稍後再試")
		}
		return res, err
	}
	res.Code = code
	return res, nil
}
