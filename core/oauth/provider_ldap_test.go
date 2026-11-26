package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"omixor-core/ldapauth"
)

// 同意畫面的 LDAP 帳號密碼登入:確認頁、失敗鎖定、CSRF 與跨站、沒啟用時畫面和分派都不變

type ldapDir struct{ calls int }

func (d *ldapDir) Authenticate(u, p string) (*ldapauth.Identity, error) {
	d.calls++
	if u == "amy" && p == "pw-amy" {
		return &ldapauth.Identity{ID: "amy", Name: "Amy", Email: "amy@corp.test"}, nil
	}
	return nil, ldapauth.ErrInvalidCredentials
}

var txnRE = regexp.MustCompile(`name="txn" value="([^"]+)"`)

func newLDAPEnv(t *testing.T, dir *ldapDir) *providerEnv {
	e := newProviderEnv(t)
	if dir != nil {
		e.svc.LDAP = dir
	}
	e.svc.LoginPerIP, e.svc.LoginTotal = 1000, 1000
	mux := http.NewServeMux()
	New(e.svc, pPublic).Mount(mux)
	e.h = mux
	return e
}

func (e *providerEnv) ldapLogin(user, pw string) *httptest.ResponseRecorder {
	return e.submit(url.Values{"ldap_username": {user}, "ldap_password": {pw}, "via": {"ldap"}})
}

func TestLDAPProviderConfirmFlow(t *testing.T) {
	dir := &ldapDir{}
	e := newLDAPEnv(t, dir)
	_, page := e.consent()
	for _, want := range []string{`name="ldap_username"`, `type="password" name="ldap_password"`, `value="ldap"`, `name="pairing_code"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("同意畫面缺少 %s", want)
		}
	}
	if w := e.ldapLogin("amy", "nope"); w.Code != 401 || !strings.Contains(w.Body.String(), "帳號或密碼錯誤") || strings.Contains(w.Body.String(), "nope") {
		t.Fatalf("密碼錯:%d %s", w.Code, w.Body.String())
	}
	w := e.ldapLogin(" Amy ", "pw-amy")
	m := txnRE.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "amy@corp.test") ||
		!strings.Contains(w.Body.String(), `action="/oauth/ldap/confirm"`) || strings.Contains(w.Body.String(), "pw-amy") {
		t.Fatalf("要先顯示確認頁:%d %s", w.Code, w.Body.String())
	}
	u, err := e.svc.Store.UserByEvoleapID("ldap:amy")
	if err != nil || u.Email != "amy@corp.test" {
		t.Fatalf("要對應核心使用者 ldap:amy:%v %+v", err, u)
	}
	body := url.Values{"txn": {m[1]}, "action": {"allow"}}.Encode()
	// 跨站送出、沒有綁定 cookie 都不簽發,也不作廢交易
	r := httptest.NewRequest("POST", "/oauth/ldap/confirm", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", e.cookie)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatalf("跨站:%d", rec.Code)
	}
	saved := e.cookie
	e.cookie = ""
	if w := e.serve(e.h, "POST", "/oauth/ldap/confirm", body); w.Code != 400 {
		t.Fatalf("沒有 cookie:%d %s", w.Code, w.Header().Get("Location"))
	}
	e.cookie = saved
	w = e.serve(e.h, "POST", "/oauth/ldap/confirm", body)
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st-1" {
		t.Fatalf("允許:%d %s %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	if w := e.serve(e.h, "POST", "/oauth/ldap/confirm", body); w.Code != 400 {
		t.Fatalf("交易只能用一次:%d", w.Code)
	}
	// 拒絕
	m = txnRE.FindStringSubmatch(e.ldapLogin("amy", "pw-amy").Body.String())
	w = e.serve(e.h, "POST", "/oauth/ldap/confirm", url.Values{"txn": {m[1]}, "action": {"deny"}}.Encode())
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "error=access_denied") {
		t.Fatalf("拒絕:%d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestLDAPProviderCSRFAndLockout(t *testing.T) {
	dir := &ldapDir{}
	e := newLDAPEnv(t, dir)
	e.consent()
	// CSRF 不對:不碰目錄
	form := e.authForm("forged", url.Values{"ldap_username": {"amy"}, "ldap_password": {"pw-amy"}, "via": {"ldap"}})
	if w := e.serve(e.h, "POST", "/oauth/authorize", form.Encode()); w.Code != 403 || dir.calls != 0 {
		t.Fatalf("CSRF:%d calls=%d", w.Code, dir.calls)
	}
	for i := 0; i < 5; i++ {
		e.ldapLogin("amy", "nope")
	}
	w := e.ldapLogin("amy", "pw-amy")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || dir.calls != 5 {
		t.Fatalf("要鎖定且不再問目錄:%d calls=%d %s", w.Code, dir.calls, w.Body.String())
	}
}

func TestLDAPProviderDisabledChangesNothing(t *testing.T) {
	e := newLDAPEnv(t, nil)
	_, page := e.consent()
	if s := page.Body.String(); strings.Contains(s, "ldap") || !strings.Contains(s, `<div class="row"><button type="submit" name="action" value="deny">拒絕</button><button class="p" type="submit" name="action" value="allow">允許</button></div>`) {
		t.Fatalf("沒啟用 LDAP 的同意畫面要照舊:%s", s)
	}
	// 沒有任何欄位與按鈕的送出,照舊交給配對碼
	if w := e.submit(url.Values{}); !strings.Contains(w.Body.String(), "配對碼") || w.Code != 403 {
		t.Fatalf("要照舊當配對碼:%d %s", w.Code, w.Body.String())
	}
	if w := e.serve(e.h, "POST", "/oauth/ldap/confirm", "txn=x&action=allow"); w.Code != 404 {
		t.Fatalf("確認路由:%d", w.Code)
	}
}
