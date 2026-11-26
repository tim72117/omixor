package service

import (
	"errors"
	"log"
	"strings"
	"time"

	"omixor-core/ldapauth"
	"omixor-core/model"
	"omixor-core/store"
)

// 目錄身分(ldap:<帳號>)的 OAuth 授權不能只靠 refresh token 一直續下去:目錄那邊停用、移出群組之後要失效。
//   - 能用服務帳號查:每次 refresh 重新確認。確定不能登入(不存在、停用、不在群組)→ 撤銷這組授權;
//     目錄故障、設定錯、LDAP 登入沒啟用 → 這次回 503,不撤銷、不輪替(用戶端稍後可以再試),也不發新 token
//   - 查不了(只用 DN 範本 bind):refresh 的到期時間固定在授權當下起算的上限(預設 12 小時),不再往後延,到期要重新輸入密碼
// 存取權杖本身 10 分鐘到期,所以最晚約 10 分鐘生效

// DirectoryRechecker 目錄可以選擇性地實作:不用密碼重新確認帳號(ldapauth.Authenticator 有)
type DirectoryRechecker interface {
	CanRecheck() bool
	Recheck(account string) (*ldapauth.Identity, error)
}

// DefaultUncheckedLDAPAge 查不了目錄時,一次授權最長可以用多久
const DefaultUncheckedLDAPAge = 12 * time.Hour

// SetLDAPSource 宿主的目錄設定可以在執行中改(console):token 端點重新確認帳號時現取。回 false = 目前沒啟用
func (s *Service) SetLDAPSource(f func() (LDAPSettings, bool)) { s.ldapSource.Store(&f) }

func (s *Service) currentLDAP() (LDAPSettings, bool) {
	if f := s.ldapSource.Load(); f != nil && *f != nil {
		set, ok := (*f)()
		return set, ok && set.Dir != nil
	}
	set := s.LDAPSettings()
	return set, set.Dir != nil
}

func rechecker(set LDAPSettings) (DirectoryRechecker, bool) {
	r, ok := set.Dir.(DirectoryRechecker)
	return r, ok && r.CanRecheck()
}

func uncheckedAge(set LDAPSettings) time.Duration {
	if set.UncheckedMaxAge > 0 {
		return set.UncheckedMaxAge
	}
	return DefaultUncheckedLDAPAge
}

// ldapCodeDeadline 授權碼換 token 時:查不了目錄的 LDAP 身分,這組授權的最後期限;其他回零值(照常)
func (s *Service) ldapCodeDeadline(u *model.User, now time.Time) time.Time {
	if !strings.HasPrefix(u.EvoleapID, LDAPIDPrefix) {
		return time.Time{}
	}
	set, ok := s.currentLDAP()
	if !ok {
		return time.Time{}
	}
	if _, can := rechecker(set); can {
		return time.Time{}
	}
	return now.Add(uncheckedAge(set))
}

// ldapRefresh refresh 前的確認。deadline 非零 = 新的 token 不能超過它;err 非 nil = 這次不發
func (s *Service) ldapRefresh(u *model.User, t *model.OAuthToken, now time.Time) (deadline time.Time, err error) {
	account, isLDAP := strings.CutPrefix(u.EvoleapID, LDAPIDPrefix)
	if !isLDAP {
		return time.Time{}, nil
	}
	unavailable := oerr(503, "temporarily_unavailable", "現在無法向 LDAP 確認這個帳號,請稍後再試")
	set, ok := s.currentLDAP()
	if !ok {
		log.Printf("OAuth refresh:LDAP 登入沒有啟用,不能確認帳號 %s,暫不續期", account)
		return time.Time{}, unavailable
	}
	r, can := rechecker(set)
	if !can {
		deadline = t.CreatedAt.Add(uncheckedAge(set))
		if t.ExpiresAt.Before(deadline) {
			deadline = t.ExpiresAt
		}
		if !now.Before(deadline) {
			return time.Time{}, oerr(400, "invalid_grant", "授權已到期,請重新連線並輸入 LDAP 帳號密碼")
		}
		return deadline, nil
	}
	_, rerr := r.Recheck(account)
	if rerr == nil {
		return time.Time{}, nil
	}
	reason := ""
	switch {
	case errors.Is(rerr, ldapauth.ErrNoEntry):
		reason = "no-entry"
	case errors.Is(rerr, ldapauth.ErrAccountDisabled):
		reason = "directory-disabled"
	case errors.Is(rerr, ldapauth.ErrNotInGroup):
		reason = "not-in-group"
	default:
		log.Printf("OAuth refresh:向 LDAP 確認帳號 %s 失敗:%v", account, rerr)
		return time.Time{}, unavailable
	}
	if err := s.oauthStore().RevokeOAuthFamily(t.FamilyID, now); err != nil {
		return time.Time{}, err
	}
	s.Audit(&model.Principal{User: *u, Via: model.ViaOAuth}, "oauth.revoked", t.Org, map[string]any{"via": "ldap", "username": account, "reason": reason})
	return time.Time{}, oerr(400, "invalid_grant", "這個 LDAP 帳號已經不能登入,授權已撤銷")
}

// capOAuthExpiry 把新簽的 token 限制在 deadline 之前
func capOAuthExpiry(access, refresh *model.OAuthToken, resp *OAuthTokenResponse, deadline, now time.Time) {
	if deadline.IsZero() {
		return
	}
	refresh.ExpiresAt = deadline
	if access.ExpiresAt.After(deadline) {
		access.ExpiresAt = deadline
		resp.ExpiresIn = int(deadline.Sub(now) / time.Second)
	}
}

// RevokeUserOAuth 撤銷某位使用者全部的 OAuth 授權(伺服器管理員);回傳撤銷了幾列。停用帳號另外用 SaveUser
func (s *Service) RevokeUserOAuth(p *model.Principal, email string) (int, error) {
	if err := needServerAdmin(p); err != nil {
		return 0, err
	}
	u, err := s.userByEmailOr404(email)
	if err != nil {
		return 0, err
	}
	n, err := s.oauthStore().RevokeOAuthUser(u.ID, s.now())
	if err != nil {
		return 0, err
	}
	s.Audit(p, "oauth.revoked", "", map[string]any{"email": u.Email, "reason": "admin", "count": n})
	return n, nil
}

// SetUserDisabled 停用或啟用已經存在的使用者(伺服器管理員;不會建立新使用者)。停用時一併撤銷 OAuth 授權,重新啟用也不會復活
func (s *Service) SetUserDisabled(p *model.Principal, email string, disabled bool) error {
	if err := needServerAdmin(p); err != nil {
		return err
	}
	u, err := s.userByEmailOr404(email)
	if err != nil {
		return err
	}
	if _, err := s.SaveUser(p, u.Email, nil, &disabled); err != nil {
		return err
	}
	if disabled {
		_, err = s.RevokeUserOAuth(p, u.Email)
	}
	return err
}

func (s *Service) userByEmailOr404(email string) (*model.User, error) {
	u, err := s.Store.UserByEmail(strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "沒有這位使用者:%s", email)
	}
	return u, err
}
