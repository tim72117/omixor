package service_test

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"omixor-core/ldapauth"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// recheckDir 可以重新確認的假目錄:recheck 非 nil 就回那個錯誤
type recheckDir struct {
	fakeDirectory
	can     bool
	recheck error
}

func (d *recheckDir) CanRecheck() bool { return d.can }
func (d *recheckDir) Recheck(a string) (*ldapauth.Identity, error) {
	return &ldapauth.Identity{ID: a}, d.recheck
}

const (
	lPublic   = "https://omixor.test"
	lCallback = "https://claude.ai/api/mcp/auth_callback"
	lVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ0123456789"
)

type ldapOAuthEnv struct {
	t     *testing.T
	svc   *service.Service
	st    store.Store
	dir   *recheckDir
	now   time.Time
	user  model.User
	cid   string
	admin *model.Principal
}

func newLDAPOAuthEnv(t *testing.T, can bool) *ldapOAuthEnv {
	st := storetest.New(t)
	e := &ldapOAuthEnv{t: t, st: st, dir: &recheckDir{can: can}, now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	e.svc = &service.Service{Store: st, LDAP: e.dir, OAuthRegPerIP: 1000, OAuthRegTotal: 1000, Now: func() time.Time { return e.now }}
	e.user = model.User{ID: "u_amy", Email: "amy@corp.test", EvoleapID: "ldap:amy", Role: "member", CreatedAt: model.Now()}
	boss := model.User{ID: "u_boss", Email: "boss@corp.test", Role: "admin", CreatedAt: model.Now()}
	for _, u := range []model.User{e.user, boss} {
		if err := st.SaveUser(u); err != nil {
			t.Fatal(err)
		}
	}
	e.admin = &model.Principal{User: boss, Via: model.ViaLocal}
	cl, err := e.svc.RegisterOAuthClient(service.OAuthClientInput{ClientName: "c", RedirectURIs: []any{lCallback}}, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	e.cid = cl.ID
	return e
}

func (e *ldapOAuthEnv) connect() *service.OAuthTokenResponse {
	e.t.Helper()
	sum := sha256.Sum256([]byte(lVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	chk, err := e.svc.CheckOAuthAuthorize(service.OAuthAuthRequest{ClientID: e.cid, RedirectURI: lCallback, ResponseType: "code",
		CodeChallenge: challenge, CodeChallengeMethod: "S256", Resources: []string{lPublic + "/mcp"}, Account: true})
	if err != nil {
		e.t.Fatal(err)
	}
	code, err := e.svc.ApproveOAuthIdentity(chk, challenge, service.OAuthGrantRead, &e.user, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "authorization_code", ClientID: e.cid, Code: code, RedirectURI: lCallback, CodeVerifier: lVerifier}, "1.1.1.1")
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *ldapOAuthEnv) refresh(tok string) (*service.OAuthTokenResponse, error) {
	return e.svc.OAuthToken(service.OAuthTokenRequest{GrantType: "refresh_token", ClientID: e.cid, RefreshToken: tok}, "1.1.1.1")
}

func oauthCode(err error) string {
	var oe *service.OAuthError
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

func TestOAuthRefreshRechecksDirectory(t *testing.T) {
	e := newLDAPOAuthEnv(t, true)
	r := e.connect()
	r, err := e.refresh(r.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	// 目錄故障、LDAP 沒啟用:503,不撤銷、不輪替,恢復後同一個 refresh token 還能用
	e.dir.recheck = errors.New("dial timeout")
	if _, err := e.refresh(r.RefreshToken); oauthCode(err) != "temporarily_unavailable" {
		t.Fatalf("目錄故障:%v", err)
	}
	e.svc.SetLDAPSource(func() (service.LDAPSettings, bool) { return service.LDAPSettings{}, false })
	if _, err := e.refresh(r.RefreshToken); oauthCode(err) != "temporarily_unavailable" {
		t.Fatalf("LDAP 沒啟用:%v", err)
	}
	e.svc.SetLDAPSource(nil)
	e.dir.recheck = nil
	r, err = e.refresh(r.RefreshToken)
	if err != nil {
		t.Fatalf("恢復後要能續期:%v", err)
	}
	// 確定不能登入:撤銷整組(access 也立刻失效),帳號恢復也要重新授權
	for _, why := range []error{ldapauth.ErrNoEntry, ldapauth.ErrAccountDisabled, ldapauth.ErrNotInGroup} {
		r = e.connect()
		e.dir.recheck = why
		if _, err := e.refresh(r.RefreshToken); oauthCode(err) != "invalid_grant" {
			t.Fatalf("%v:%v", why, err)
		}
		if _, err := e.svc.AuthenticateOAuth(r.AccessToken, lPublic+"/mcp"); err == nil {
			t.Fatalf("%v:access token 要失效", why)
		}
		e.dir.recheck = nil
		if _, err := e.refresh(r.RefreshToken); err == nil {
			t.Fatal("撤銷後不能復活")
		}
	}
}

func TestOAuthRefreshUncheckableDirectoryIsCapped(t *testing.T) {
	e := newLDAPOAuthEnv(t, false)
	r := e.connect()
	start := e.now
	var err error
	for e.now.Sub(start) < 11*time.Hour {
		e.now = e.now.Add(time.Hour)
		if r, err = e.refresh(r.RefreshToken); err != nil {
			t.Fatalf("%v 時要能續期:%v", e.now.Sub(start), err)
		}
	}
	e.now = start.Add(service.DefaultUncheckedLDAPAge + time.Second)
	if _, err := e.refresh(r.RefreshToken); oauthCode(err) != "invalid_grant" {
		t.Fatalf("超過上限要重新登入:%v", err)
	}
}

func TestAdminRevokeAndDisableOAuth(t *testing.T) {
	e := newLDAPOAuthEnv(t, true)
	r := e.connect()
	member := &model.Principal{User: e.user, Via: model.ViaLocal}
	if _, err := e.svc.RevokeUserOAuth(member, e.user.Email); err == nil {
		t.Fatal("一般使用者不能撤銷")
	}
	if n, err := e.svc.RevokeUserOAuth(e.admin, " AMY@corp.test "); err != nil || n == 0 {
		t.Fatalf("撤銷:%d %v", n, err)
	}
	if _, err := e.refresh(r.RefreshToken); err == nil {
		t.Fatal("撤銷後不能續期")
	}
	r = e.connect()
	if err := e.svc.SetUserDisabled(e.admin, e.user.Email, true); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetUserDisabled(e.admin, e.user.Email, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.refresh(r.RefreshToken); err == nil {
		t.Fatal("停用時撤銷的授權,重新啟用不能復活")
	}
	if err := e.svc.SetUserDisabled(e.admin, "nobody@corp.test", true); service.Code(err) != 404 {
		t.Fatalf("不能建立新使用者:%v", err)
	}
}
