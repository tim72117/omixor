package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/cli"
	"omixor-core/config"
	"omixor-core/evoleap"
	"omixor-core/gitlabauth"
	"omixor-core/ldapauth"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// GitLab 登入(HTTP 層端到端;GitLab 是 httptest 假伺服器、LDAP 目錄是假的)

const (
	glSecret   = "gl-secret"
	glClientID = "gl-app"
)

type fakeGrant struct {
	challenge, redirect string
	user                map[string]any
}

type fakeGitLab struct {
	srv     *httptest.Server
	mu      sync.Mutex
	codes   map[string]fakeGrant
	tokens  map[string]map[string]any
	revoked []string
	n       int
}

func newFakeGitLab(t *testing.T) *fakeGitLab {
	f := &fakeGitLab{codes: map[string]fakeGrant{}, tokens: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		g, ok := f.codes[r.PostForm.Get("code")]
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || r.PostForm.Get("client_id") != glClientID || r.PostForm.Get("client_secret") != glSecret ||
			r.PostForm.Get("redirect_uri") != g.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			w.WriteHeader(400)
			return
		}
		delete(f.codes, r.PostForm.Get("code"))
		tok := "gltok-" + r.PostForm.Get("code")
		f.tokens[tok] = g.user
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "token_type": "bearer"})
	})
	mux.HandleFunc("GET /api/v4/user", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		u, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(u)
	})
	mux.HandleFunc("POST /oauth/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		delete(f.tokens, r.PostForm.Get("token"))
		_, _ = io.WriteString(w, "{}")
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// authorize 模擬使用者在 GitLab 授權頁同意:檢查授權網址,回傳 (code, state)
func (f *fakeGitLab) authorize(t *testing.T, authorizeURL, callbackPath string, user map[string]any) (string, string) {
	t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme+"://"+u.Host != f.srv.URL || u.Path != "/oauth/authorize" || q.Get("scope") != "read_user" || q.Get("code_challenge_method") != "S256" ||
		q.Get("client_id") != glClientID || q.Get("redirect_uri") != tPublic+callbackPath || q.Get("state") == "" {
		t.Fatal("授權網址不對:", authorizeURL)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	code := "code" + string(rune('a'+f.n%26)) + string(rune('a'+f.n/26))
	f.codes[code] = fakeGrant{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), user: user}
	return code, q.Get("state")
}

func glUser(id int, username, email string, extra ...any) map[string]any {
	u := map[string]any{"id": id, "username": username, "name": "GL " + username, "email": email, "state": "active",
		"confirmed_at": "2026-01-01T00:00:00Z", "identities": []any{}}
	for i := 0; i+1 < len(extra); i += 2 {
		u[extra[i].(string)] = extra[i+1]
	}
	return u
}

func ldapIdent(dn string) []any {
	return []any{map[string]any{"provider": "ldapmain", "extern_uid": dn}}
}

type fakeLookup struct {
	entries map[string]ldapauth.Identity // DN → 帳號
	err     error
}

func (f *fakeLookup) Authenticate(u, p string) (*ldapauth.Identity, error) {
	for _, e := range f.entries {
		if e.ID == u && p == "pw" {
			x := e
			return &x, nil
		}
	}
	return nil, ldapauth.ErrInvalidCredentials
}

func (f *fakeLookup) LookupDN(dn string) (*ldapauth.Identity, error) {
	if f.err != nil {
		return nil, f.err
	}
	e, ok := f.entries[dn]
	if !ok {
		return nil, ldapauth.ErrNoEntry
	}
	return &e, nil
}

func newGitLabOA(t *testing.T) (*oaHTTP, *fakeGitLab) {
	o := newOAHTTP(t)
	f := newFakeGitLab(t)
	cl, err := gitlabauth.New(gitlabauth.Config{URL: f.srv.URL, ClientID: glClientID, ClientSecret: glSecret})
	if err != nil {
		t.Fatal(err)
	}
	o.svc.GitLab = &service.GitLabLogin{Client: cl, Public: tPublic}
	return o, f
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.MaxAge >= 0 && c.Value != "" {
			return c
		}
	}
	return nil
}

func (o *oaHTTP) postJSON(path string, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return o.do("POST", path, string(b), "Content-Type", "application/json")
}

const glLoopback = "http://127.0.0.1:45123/callback"

// cliStart 走到「瀏覽器已被導去 GitLab」:回傳 GitLab 授權網址與綁定 cookie
func (o *oaHTTP) cliStart() (string, *http.Cookie) {
	o.t.Helper()
	w := o.postJSON("/api/auth/gitlab/start", map[string]any{"redirect": glLoopback, "device": "我的筆電", "codeChallenge": challenge(), "state": "cli-state"})
	if w.Code != 200 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	login := jsonBody(o.t, w)["url"].(string)
	path, ok := strings.CutPrefix(login, tPublic)
	if !ok || !strings.HasPrefix(path, "/api/auth/gitlab/login?id=") {
		o.t.Fatal("登入網址:", login)
	}
	w = o.do("GET", path, "")
	if w.Code != 303 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	c := cookieNamed(w, "omixor_gitlab_login")
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/api/auth/gitlab" {
		o.t.Fatalf("cookie:%+v", c)
	}
	if again := o.do("GET", path, ""); again.Code != 400 {
		o.t.Fatal("登入網址只能用一次:", again.Code)
	}
	return w.Header().Get("Location"), c
}

func (o *oaHTTP) cliCallback(code, state string, c *http.Cookie) *httptest.ResponseRecorder {
	hdr := []string{}
	if c != nil {
		hdr = []string{"Cookie", c.Name + "=" + c.Value}
	}
	return o.do("GET", "/api/auth/gitlab/callback?"+url.Values{"code": {code}, "state": {state}}.Encode(), "", hdr...)
}

// cliLogin 整個 CLI 流程;回傳最後的回應。導回 loopback 帶 error 時回傳 (nil, error_description)
func (o *oaHTTP) cliLogin(f *fakeGitLab, user map[string]any) (*httptest.ResponseRecorder, string) {
	o.t.Helper()
	au, c := o.cliStart()
	code, state := f.authorize(o.t, au, "/api/auth/gitlab/callback", user)
	w := o.cliCallback(code, state, c)
	if w.Code != 303 {
		o.t.Fatal(w.Code, w.Body.String())
	}
	back, _ := url.Parse(w.Header().Get("Location"))
	if back.Scheme+"://"+back.Host+back.Path != glLoopback || back.Query().Get("state") != "cli-state" {
		o.t.Fatal("導回:", back)
	}
	if e := back.Query().Get("error"); e != "" {
		return nil, back.Query().Get("error_description")
	}
	return o.postJSON("/api/auth/gitlab", map[string]any{"code": back.Query().Get("code"), "codeVerifier": tVerifier}), ""
}

// loggedIn 登入成功並回傳使用者
func (o *oaHTTP) loggedIn(f *fakeGitLab, user map[string]any) model.User {
	o.t.Helper()
	w, msg := o.cliLogin(f, user)
	if w == nil || w.Code != 200 {
		if w != nil {
			msg = w.Body.String()
		}
		o.t.Fatal("登入失敗:", msg)
	}
	var r service.LoginResult
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || !strings.HasPrefix(r.Token, "omx_") {
		o.t.Fatal(w.Body.String(), err)
	}
	return r.User
}

func (o *oaHTTP) refused(f *fakeGitLab, user map[string]any, want string) {
	o.t.Helper()
	w, msg := o.cliLogin(f, user)
	if w != nil || !strings.Contains(msg, want) {
		got := msg
		if w != nil {
			got = w.Body.String()
		}
		o.t.Fatalf("應該被拒(%s),實際:%s", want, got)
	}
}

func (o *oaHTTP) save(u model.User) {
	o.t.Helper()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = model.Now()
	}
	if u.Role == "" {
		u.Role = "member"
	}
	if err := o.svc.Store.SaveUser(u); err != nil {
		o.t.Fatal(err)
	}
}

func TestGitLabCLILogin(t *testing.T) {
	o, f := newGitLabOA(t)
	o.svc.GitLab.Admins = []int64{7}

	u := o.loggedIn(f, glUser(5, "amy", "Amy@Corp.Test"))
	if u.EvoleapID != "gitlab:5" || u.Email != "amy@corp.test" || u.Role != "member" {
		t.Fatalf("使用者:%+v", u)
	}
	if len(f.revoked) != 1 || len(f.tokens) != 0 {
		t.Fatal("GitLab token 要撤銷:", f.revoked)
	}
	// 同一個 id:同一人(username、email 改了也一樣);ADMINS 只升不降
	if u2 := o.loggedIn(f, glUser(5, "amy2", "new@corp.test")); u2.ID != u.ID || u2.Email != "amy@corp.test" {
		t.Fatalf("第二次:%+v", u2)
	}
	boss := o.loggedIn(f, glUser(7, "chief", "chief@corp.test"))
	if boss.Role != "admin" {
		t.Fatal("管理員:", boss.Role)
	}
	o.svc.GitLab.Admins = nil
	if x := o.loggedIn(f, glUser(7, "chief", "chief@corp.test")); x.Role != "admin" {
		t.Fatal("不該自動降級")
	}
	// 稽核 via=gitlab
	log, err := o.svc.Store.ListAudit(50)
	if err != nil {
		t.Fatal(err)
	}
	via := false
	for _, e := range log {
		if e.Action == "login" && e.Detail["via"] == "gitlab" && e.Detail["device"] == "我的筆電" {
			via = true
		}
	}
	if !via {
		t.Fatal("沒有 via=gitlab 的登入稽核")
	}

	// 拒絕的情形
	o.refused(f, glUser(8, "b", "b@corp.test", "state", "blocked"), "不是啟用中")
	o.refused(f, glUser(9, "c", "c@corp.test", "state", "deactivated"), "不是啟用中")
	o.refused(f, glUser(10, "d", "d@corp.test", "confirmed_at", nil), "確認過的 email")
	o.refused(f, glUser(11, "e", "amy@corp.test"), "另一個帳號")
	o.save(model.User{ID: "u_pend", Email: "pend@corp.test"})
	o.refused(f, glUser(12, "p", "pend@corp.test"), "預先加入")
	o.svc.GitLab.TrustMail = true
	if x := o.loggedIn(f, glUser(12, "p", "pend@corp.test")); x.ID != "u_pend" || x.EvoleapID != "gitlab:12" {
		t.Fatalf("TRUST_MAIL 接手:%+v", x)
	}
	dis := model.User{ID: u.ID, EvoleapID: "gitlab:5", Email: "amy@corp.test", Disabled: true}
	o.save(dis)
	o.refused(f, glUser(5, "amy", "amy@corp.test"), "停用")
}

func TestGitLabLinkLDAP(t *testing.T) {
	o, f := newGitLabOA(t)
	dir := &fakeLookup{entries: map[string]ldapauth.Identity{
		"uid=alice,dc=x": {ID: "alice", Name: "Alice", Email: "alice@corp.test"},
		"uid=bob,dc=x":   {ID: "bob", Name: "Bob", Email: "bob@corp.test"},
		"uid=carl,dc=x":  {ID: "carl", Name: "Carl", Email: "carl@corp.test"},
		"uid=dan,dc=x":   {ID: "dan", Name: "Dan", Email: "dan@corp.test"},
	}}
	o.svc.LDAP = dir
	o.svc.GitLab.LinkLDAP = true
	users := func() int {
		n, err := o.svc.Store.CountUsers()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 已經用 LDAP 登入過:併到同一帳號(GitLab 的 username 不影響)
	res, err := o.svc.LoginWithLDAP("alice", "pw", "", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	alice := func(email string) map[string]any {
		return glUser(1, "whatever", email, "identities", ldapIdent("uid=alice,dc=x"))
	}
	if u := o.loggedIn(f, alice("Alice@corp.test")); u.ID != res.User.ID || u.EvoleapID != "ldap:alice" {
		t.Fatalf("併帳號:%+v", u)
	}
	// 還沒有 LDAP 帳號:以 ldap:<帳號> 建立,之後 LDAP 登入是同一個
	u := o.loggedIn(f, glUser(2, "bobby", "bob@corp.test", "identities", ldapIdent("uid=bob,dc=x")))
	if u.EvoleapID != "ldap:bob" {
		t.Fatalf("建立:%+v", u)
	}
	if r, err := o.svc.LoginWithLDAP("bob", "pw", "", "1.1.1.1"); err != nil || r.User.ID != u.ID {
		t.Fatalf("之後 LDAP 登入:%v", err)
	}
	// 帳號名稱取自目錄:自己的身分是 carl,username 叫 alice 也只會是 carl
	if x := o.loggedIn(f, glUser(4, "alice", "carl@corp.test", "identities", ldapIdent("uid=carl,dc=x"))); x.EvoleapID != "ldap:carl" {
		t.Fatalf("帳號名稱要取自目錄:%+v", x)
	}
	// 沒有 ldap 身分的人不受影響(非 ldap 的 provider 也算沒有)
	if x := o.loggedIn(f, glUser(3, "carl", "ext@other.test")); x.EvoleapID != "gitlab:3" {
		t.Fatalf("沒有 ldap 身分:%+v", x)
	}
	o.refused(f, glUser(15, "alice", "alice@corp.test", "identities", []any{map[string]any{"provider": "saml", "extern_uid": "uid=alice,dc=x"}}), "另一個帳號")

	// 以下一律拒絕,而且不建任何帳號
	n := users()
	// 改 GitLab email(既有 ldap: 帳號、或還沒有帳號)
	o.refused(f, alice("evil@corp.test"), "管理員")
	o.refused(f, glUser(5, "dan", "other@corp.test", "identities", ldapIdent("uid=dan,dc=x")), "管理員")
	// 目錄查不到(AD 停用、被篩掉、別的目錄)
	o.refused(f, glUser(13, "x", "x@corp.test", "identities", ldapIdent("uid=gone,dc=x")), "管理員")
	// 沒確認的 email
	o.refused(f, glUser(14, "dan", "dan@corp.test", "confirmed_at", nil, "identities", ldapIdent("uid=dan,dc=x")), "確認過的 email")
	// 目錄壞了
	dir.err = io.ErrUnexpectedEOF
	o.refused(f, glUser(16, "z", "z@corp.test", "identities", ldapIdent("uid=dan,dc=x")), "LDAP 目錄")
	dir.err = nil
	if users() != n {
		t.Fatal("拒絕的情形不能建帳號:", n, users())
	}
	// 改 email 又改回:照常進原本的帳號
	if x := o.loggedIn(f, alice("alice@corp.test")); x.ID != res.User.ID {
		t.Fatalf("改回:%+v", x)
	}

	// 先前已經有 gitlab:<id> 帳號(開 LINK_LDAP 之前建的):沿用,不改併
	o.save(model.User{ID: "u_old", EvoleapID: "gitlab:30", Email: "old@other.test"})
	danOld := glUser(30, "dan", "old@other.test", "identities", ldapIdent("uid=dan,dc=x"))
	if x := o.loggedIn(f, danOld); x.ID != "u_old" {
		t.Fatalf("沿用:%+v", x)
	}
	// 之後目錄查不到:拒絕
	d := dir.entries["uid=dan,dc=x"]
	delete(dir.entries, "uid=dan,dc=x")
	o.refused(f, danOld, "管理員")
	dir.entries["uid=dan,dc=x"] = d
	// 對應的 ldap: 帳號停用:已有 gitlab: 帳號也擋
	o.save(model.User{ID: "u_dan", EvoleapID: "ldap:dan", Email: "dan@corp.test", Disabled: true})
	o.refused(f, danOld, "停用")

	// LDAP 帳號停用:改 email 也不能繞過
	o.save(model.User{ID: res.User.ID, EvoleapID: "ldap:alice", Email: "alice@corp.test", Disabled: true})
	n = users()
	o.refused(f, alice("alice@corp.test"), "停用")
	o.refused(f, alice("fresh@corp.test"), "停用")
	o.refused(f, glUser(17, "x", "fresh2@corp.test", "identities", ldapIdent("uid=alice,dc=x")), "停用")
	if users() != n {
		t.Fatal("停用不能繞過建帳號")
	}
	// 關掉 LINK_LDAP:不併,email 被佔用 403
	o.svc.GitLab.LinkLDAP = false
	o.refused(f, glUser(18, "bob", "bob@corp.test", "identities", ldapIdent("uid=bob,dc=x")), "另一個帳號")
}

func TestGitLabEmailDomains(t *testing.T) {
	o, f := newGitLabOA(t)
	o.svc.GitLab.EmailDomains = []string{"corp.test"}
	if u := o.loggedIn(f, glUser(1, "a", "A@Corp.Test")); u.Email != "a@corp.test" {
		t.Fatal(u)
	}
	for i, bad := range []string{"a@evil.test", "a@sub.corp.test", "a@corp.test.evil.test", "a@xcorp.test"} {
		o.refused(f, glUser(10+i, "b", bad), "網域")
	}
	o.refused(f, glUser(20, "c", "c@corp.test", "confirmed_at", nil), "網域")
	// 已經建立的帳號,email 換到清單外也擋
	o.refused(f, glUser(1, "a", "a@evil.test"), "網域")
}

// 開始登入與換 token 的限流視窗分開
func TestGitLabRateWindowsSeparate(t *testing.T) {
	o, _ := newGitLabOA(t)
	o.svc.LoginPerIP = 1
	start := func() int {
		return o.postJSON("/api/auth/gitlab/start", map[string]any{"redirect": glLoopback, "codeChallenge": challenge(), "state": "s"}).Code
	}
	exchange := func() int {
		return o.postJSON("/api/auth/gitlab", map[string]any{"code": "nope", "codeVerifier": tVerifier}).Code
	}
	if a, b := start(), start(); a != 200 || b != 429 {
		t.Fatal("start:", a, b)
	}
	if a, b := exchange(), exchange(); a != 401 || b != 429 {
		t.Fatal("換 token 要用自己的視窗:", a, b)
	}
}

func TestGitLabCLIProtections(t *testing.T) {
	o, f := newGitLabOA(t)
	start := func(redirect string) int {
		return o.postJSON("/api/auth/gitlab/start", map[string]any{"redirect": redirect, "codeChallenge": challenge(), "state": "s"}).Code
	}
	for _, bad := range []string{"https://127.0.0.1:1/callback", "http://evil.test:80/callback", "http://127.0.0.1.evil.test:1/", "http://127.0.0.1/callback",
		"http://user@127.0.0.1:1/", "http://127.0.0.1:1/cb?x=1", "http://127.0.0.1:1/cb#f", "myapp://cb", "http://[::2]:1/", ""} {
		if c := start(bad); c != 400 {
			t.Errorf("%q 應該被拒:%d", bad, c)
		}
	}
	for _, ok := range []string{"http://localhost:5000/callback", "http://[::1]:5000/callback"} {
		if c := start(ok); c != 200 {
			t.Errorf("%q 應該可以:%d", ok, c)
		}
	}

	au, c := o.cliStart()
	code, state := f.authorize(t, au, "/api/auth/gitlab/callback", glUser(5, "amy", "amy@corp.test"))
	// 沒有 cookie / cookie 不對:拒絕,而且不作廢交易
	if w := o.cliCallback(code, state, nil); w.Code != 400 {
		t.Fatal("沒有 cookie:", w.Code)
	}
	if w := o.cliCallback(code, state, &http.Cookie{Name: c.Name, Value: "other"}); w.Code != 400 {
		t.Fatal("cookie 不對:", w.Code)
	}
	w := o.cliCallback(code, state, c)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	// state 重放
	if again := o.cliCallback(code, state, c); again.Code != 400 {
		t.Fatal("state 重放:", again.Code)
	}
	back, _ := url.Parse(w.Header().Get("Location"))
	one := back.Query().Get("code")
	if r := o.postJSON("/api/auth/gitlab", map[string]any{"code": one, "codeVerifier": strings.Repeat("z", 43)}); r.Code != 401 {
		t.Fatal("verifier 不對:", r.Code)
	}
	if r := o.postJSON("/api/auth/gitlab", map[string]any{"code": one, "codeVerifier": tVerifier}); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := o.postJSON("/api/auth/gitlab", map[string]any{"code": one, "codeVerifier": tVerifier}); r.Code != 401 {
		t.Fatal("碼只能用一次:", r.Code)
	}
	// 碼逾時
	au, c = o.cliStart()
	code, state = f.authorize(t, au, "/api/auth/gitlab/callback", glUser(5, "amy", "amy@corp.test"))
	w = o.cliCallback(code, state, c)
	back, _ = url.Parse(w.Header().Get("Location"))
	*o.now = o.now.Add(2 * time.Minute)
	if r := o.postJSON("/api/auth/gitlab", map[string]any{"code": back.Query().Get("code"), "codeVerifier": tVerifier}); r.Code != 401 {
		t.Fatal("逾時:", r.Code)
	}
	// 交易逾時
	au, c = o.cliStart()
	code, state = f.authorize(t, au, "/api/auth/gitlab/callback", glUser(5, "amy", "amy@corp.test"))
	*o.now = o.now.Add(11 * time.Minute)
	if w := o.cliCallback(code, state, c); w.Code != 400 {
		t.Fatal("交易逾時:", w.Code)
	}
	// 頻率限制
	o.svc.LoginPerIP = 1
	*o.now = o.now.Add(2 * time.Minute)
	if a, b := start("http://127.0.0.1:1/cb"), start("http://127.0.0.1:1/cb"); a != 200 || b != 429 {
		t.Fatal("頻率限制:", a, b)
	}
	// 能力偵測與沒啟用
	if !strings.Contains(o.do("GET", "/api/version", "").Body.String(), `"gitlab_login"`) {
		t.Fatal("版本要宣告 gitlab_login")
	}
	o.svc.GitLab = nil
	if strings.Contains(o.do("GET", "/api/version", "").Body.String(), "gitlab") {
		t.Fatal("沒啟用不該宣告")
	}
	if c := start("http://127.0.0.1:1/cb"); c != 404 {
		t.Fatal("沒啟用:", c)
	}
}

// glConsent 同意畫面流程的測試輔助
type glConsent struct {
	o      *oaHTTP
	f      *fakeGitLab
	client string
}

func (g *glConsent) form(csrf string, extra ...string) url.Values {
	v := url.Values{"csrf": {csrf}, "client_id": {g.client}, "redirect_uri": {tCallback}, "response_type": {"code"}, "code_challenge": {challenge()},
		"code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {tResource}, "scope": {"mcp:read mcp:write"}}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return v
}

func (g *glConsent) post(v url.Values, hdr ...string) *httptest.ResponseRecorder {
	return g.o.do("POST", "/oauth/authorize", v.Encode(), append([]string{"Content-Type", "application/x-www-form-urlencoded"}, hdr...)...)
}

func (g *glConsent) csrf() string {
	c, _ := g.o.consentScope(g.client, tResource, "mcp:read mcp:write")
	return c
}

// start 按「用 GitLab 登入並允許」:回傳 GitLab 授權網址與 cookie
func (g *glConsent) start(grant string, hdr ...string) (string, *http.Cookie) {
	g.o.t.Helper()
	w := g.post(g.form(g.csrf(), "via", "gitlab", "grant", grant), hdr...)
	if w.Code != 303 {
		g.o.t.Fatal(w.Code, w.Body.String())
	}
	c := cookieNamed(w, "omixor_oauth_gitlab")
	if c == nil || !c.HttpOnly || !c.Secure || c.Path != "/oauth" || c.SameSite != http.SameSiteLaxMode {
		g.o.t.Fatalf("cookie:%+v", c)
	}
	return w.Header().Get("Location"), c
}

func (g *glConsent) callback(code, state string, c *http.Cookie, hdr ...string) *httptest.ResponseRecorder {
	if c != nil {
		hdr = append(hdr, "Cookie", c.Name+"="+c.Value)
	}
	return g.o.do("GET", "/oauth/gitlab/callback?"+url.Values{"code": {code}, "state": {state}}.Encode(), "", hdr...)
}

var glTxnRE = regexp.MustCompile(`name="txn" value="([^"]+)"`)

// toConfirm 走到確認頁
func (g *glConsent) toConfirm(grant string, user map[string]any) (string, *http.Cookie, *httptest.ResponseRecorder) {
	g.o.t.Helper()
	au, c := g.start(grant)
	code, state := g.f.authorize(g.o.t, au, "/oauth/gitlab/callback", user)
	w := g.callback(code, state, c)
	m := glTxnRE.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil || w.Header().Get("Location") != "" {
		g.o.t.Fatal("要顯示確認頁:", w.Code, w.Body.String(), w.Header().Get("Location"))
	}
	return m[1], c, w
}

func (g *glConsent) confirm(txn, action string, c *http.Cookie, hdr ...string) *httptest.ResponseRecorder {
	h := append([]string{"Content-Type", "application/x-www-form-urlencoded"}, hdr...)
	if c != nil {
		h = append(h, "Cookie", c.Name+"="+c.Value)
	}
	return g.o.do("POST", "/oauth/gitlab/confirm", url.Values{"txn": {txn}, "action": {action}}.Encode(), h...)
}

func newGLConsent(t *testing.T) *glConsent {
	o, f := newGitLabOA(t)
	// u1(boss@x)是預先建立、還沒對上身分的人,也是組織 o 的管理員
	o.svc.GitLab.TrustMail = true
	return &glConsent{o: o, f: f, client: o.register()}
}

func TestGitLabConsent(t *testing.T) {
	g := newGLConsent(t)
	o, f := g.o, g.f
	_, page := o.consentScope(g.client, tResource, "mcp:read mcp:write")
	if !strings.Contains(page.Body.String(), `name="via" value="gitlab">用 GitLab 登入並允許</button>`) ||
		!strings.Contains(page.Header().Get("Content-Security-Policy"), " "+f.srv.URL) {
		t.Fatal("同意畫面:", page.Header().Get("Content-Security-Policy"), page.Body.String())
	}
	au, c := g.start("write")
	code, state := f.authorize(t, au, "/oauth/gitlab/callback", glUser(42, "boss", "boss@x"))
	if w := g.callback(code, state, nil); w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatal("沒有 cookie:", w.Code)
	}
	w := g.callback(code, state, c)
	m := glTxnRE.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil || w.Header().Get("Location") != "" {
		t.Fatal("回呼只能顯示確認頁,不能簽授權碼:", w.Code, w.Header().Get("Location"))
	}
	for _, want := range []string{"boss@x", "Claude", "claude.ai", "<dt>組織</dt><dd>o</dd>", "讀寫", `value="allow"`, `value="deny"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Error("確認頁缺少", want, w.Body.String())
		}
	}
	if again := g.callback(code, state, c); again.Code != 400 {
		t.Fatal("state 重放:", again.Code)
	}
	txn := m[1]
	// 確認這一步:跨站、沒有 cookie、cookie 不對都拒絕,而且不作廢
	if w := g.confirm(txn, "allow", c, "Origin", "https://evil.test"); w.Code != 403 {
		t.Fatal("跨站 Origin:", w.Code)
	}
	if w := g.confirm(txn, "allow", c, "Origin", "null", "Sec-Fetch-Site", "cross-site"); w.Code != 403 {
		t.Fatal("Sec-Fetch-Site:", w.Code)
	}
	if w := g.confirm(txn, "allow", nil); w.Code != 400 {
		t.Fatal("沒有 cookie:", w.Code)
	}
	if w := g.confirm(txn, "allow", &http.Cookie{Name: c.Name, Value: strings.Repeat("a", 32)}); w.Code != 400 {
		t.Fatal("cookie 不對:", w.Code)
	}
	w = g.confirm(txn, "allow", c, "Origin", "null", "Sec-Fetch-Site", "same-origin")
	back, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || !strings.HasPrefix(back.String(), tCallback) || back.Query().Get("code") == "" || back.Query().Get("state") != "st-1" {
		t.Fatal("允許:", w.Code, back, w.Body.String())
	}
	tok := o.do("POST", "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {g.client}, "code": {back.Query().Get("code")},
		"redirect_uri": {tCallback}, "code_verifier": {tVerifier}, "resource": {tResource}}.Encode(), "Content-Type", "application/x-www-form-urlencoded")
	if tok.Code != 200 || !strings.Contains(tok.Body.String(), "mcp:write") {
		t.Fatal(tok.Code, tok.Body.String())
	}
	if w := g.confirm(txn, "allow", c); w.Code != 400 {
		t.Fatal("確認只能一次:", w.Code)
	}
	// 拒絕
	txn, c, _ = g.toConfirm("read", glUser(42, "boss", "boss@x"))
	w = g.confirm(txn, "deny", c)
	back, _ = url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || back.Query().Get("error") != "access_denied" || back.Query().Get("code") != "" {
		t.Fatal("拒絕:", w.Code, back)
	}
	// 逾時
	txn, c, _ = g.toConfirm("read", glUser(42, "boss", "boss@x"))
	*o.now = o.now.Add(6 * time.Minute)
	if w := g.confirm(txn, "allow", c); w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatal("確認逾時:", w.Code)
	}
	// 兩個分頁:共用綁定 cookie,先開始的那個不會被後開始的弄壞
	au1, c1 := g.start("read")
	au2, c2 := g.start("read", "Cookie", o.consentCookie+"; "+c1.Name+"="+c1.Value)
	if c1.Value != c2.Value {
		t.Fatal("要沿用綁定 cookie")
	}
	for _, au := range []string{au1, au2} {
		code, state = f.authorize(t, au, "/oauth/gitlab/callback", glUser(42, "boss", "boss@x"))
		if w := g.callback(code, state, c2); w.Code != 200 {
			t.Fatal("分頁:", w.Code, w.Body.String())
		}
	}
	// 不能登入的人:回呼直接導回 error=access_denied
	au, c = g.start("read")
	code, state = f.authorize(t, au, "/oauth/gitlab/callback", glUser(43, "x", "x@x", "state", "blocked"))
	w = g.callback(code, state, c)
	back, _ = url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || back.Query().Get("error") != "access_denied" || back.Query().Get("code") != "" {
		t.Fatal("blocked:", w.Code, back)
	}
	// 沒啟用:不顯示,CSP 不含 GitLab
	o.svc.GitLab = nil
	_, page = o.consentScope(g.client, tResource, "mcp:read")
	if strings.Contains(page.Body.String(), "GitLab") || strings.Contains(page.Header().Get("Content-Security-Policy"), f.srv.URL) {
		t.Fatal("沒啟用不該出現")
	}
}

// 同意畫面的送出要同一個瀏覽器、同站(所有 provider 共用:GitLab、evoleap、配對碼)
func TestConsentRejectsCrossSite(t *testing.T) {
	g := newGLConsent(t)
	o := g.o
	w := o.do("GET", "/oauth/authorize?"+authQuery(g.client), "")
	ck := cookieNamed(w, "omixor_oauth_consent")
	if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/oauth" {
		t.Fatalf("同意畫面的 cookie:%+v", ck)
	}
	// 多分頁:帶著 cookie 再開一次,值不變
	again := o.do("GET", "/oauth/authorize?"+authQuery(g.client), "", "Cookie", ck.Name+"="+ck.Value)
	if x := cookieNamed(again, "omixor_oauth_consent"); x == nil || x.Value != ck.Value {
		t.Fatal("要沿用:", x)
	}
	gitlab := func(csrf string) url.Values { return g.form(csrf, "via", "gitlab", "grant", "write") }
	evo := func(csrf string) url.Values { return g.form(csrf, "via", "evoleap", "grant", "write") }
	pair := func(csrf string) url.Values {
		return g.form(csrf, "action", "allow", "grant", "write", "pairing_code", o.pair())
	}
	for name, mk := range map[string]func(string) url.Values{"gitlab": gitlab, "evoleap": evo, "pair": pair} {
		for _, hdr := range [][]string{
			{"Origin", "https://evil.test"},
			{"Origin", "null", "Sec-Fetch-Site", "cross-site"},
			{"Sec-Fetch-Site", "same-site"},
		} {
			if w := g.post(mk(g.csrf()), hdr...); w.Code != 403 || w.Header().Get("Location") != "" {
				t.Error(name, hdr, "要拒絕:", w.Code, w.Header().Get("Location"))
			}
		}
		// 別站自己抓的同意畫面叫受害者代送:受害者的瀏覽器沒有那個 cookie(或是自己的另一個值)
		csrf := g.csrf()
		for _, cookie := range []string{"x=1", "omixor_oauth_consent=" + strings.Repeat("v", 32)} {
			if w := g.post(mk(csrf), "Cookie", cookie); w.Code != 403 || w.Header().Get("Location") != "" {
				t.Error(name, cookie, "要拒絕:", w.Code, w.Header().Get("Location"))
			}
		}
	}
	// 被拒的沒有用掉 CSRF;自己的頁面(Origin 是 "null" 或對外網址)可以送
	for _, hdr := range [][]string{
		{"Origin", "null", "Sec-Fetch-Site", "same-origin"},
		{"Origin", "null"},
		{"Origin", tPublic, "Sec-Fetch-Site", "same-origin"},
	} {
		csrf := g.csrf()
		_ = g.post(pair(csrf), "Origin", "https://evil.test")
		w := g.post(pair(csrf), hdr...)
		if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "code=") {
			t.Error(hdr, "要能送:", w.Code, w.Body.String())
		}
	}
	// 兩個分頁各自的 CSRF 都能用
	a, b := g.csrf(), g.csrf()
	for _, csrf := range []string{a, b} {
		if w := g.post(pair(csrf)); w.Code != 303 {
			t.Error("分頁:", w.Code, w.Body.String())
		}
	}
}

// failCodeStore 簽發授權碼時資料庫出錯
type failCodeStore struct{ store.OAuthStore }

func (s failCodeStore) PutOAuthToken(t model.OAuthToken) error {
	if t.Kind == model.OAuthKindCode {
		return errors.New("disk full")
	}
	return s.OAuthStore.PutOAuthToken(t)
}

// 非預期錯誤換成 500 之前要記 log,而且 log 不含 code、token、secret
func TestGitLabUnexpectedErrorIsLogged(t *testing.T) {
	g := newGLConsent(t)
	txn, c, _ := g.toConfirm("read", glUser(42, "boss", "boss@x"))
	g.o.svc.OAuth = failCodeStore{g.o.svc.Store}
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	w := g.confirm(txn, "allow", c)
	back, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 303 || back.Query().Get("error") != "server_error" || strings.Contains(back.String(), "disk full") {
		t.Fatal(w.Code, back, w.Body.String())
	}
	if !strings.Contains(buf.String(), "disk full") {
		t.Fatal("要記 log:", buf.String())
	}
	for _, secret := range []string{txn, c.Value, glSecret, "gltok-"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatal("log 不能含機密:", secret)
		}
	}
}

// omixor login --gitlab 整個流程(core/cli、core/client 的 loopback 回呼):真的開 HTTP 伺服器,「瀏覽器」是測試自己
func TestGitLabCLICommand(t *testing.T) {
	o, f := newGitLabOA(t)
	srv := httptest.NewServer(o.h)
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("OMIXOR_CONFIG_DIR", dir)
	t.Setenv("OMIXOR_CREDENTIALS", filepath.Join(dir, "cred.json"))
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 10 * time.Second}
	old := evoleap.OpenBrowser
	defer func() { evoleap.OpenBrowser = old }()
	evoleap.OpenBrowser = func(login string) {
		go func() {
			w := o.do("GET", strings.TrimPrefix(login, tPublic), "")
			c := cookieNamed(w, "omixor_gitlab_login")
			if c == nil {
				t.Error("沒有 cookie", w.Code)
				return
			}
			code, state := f.authorize(t, w.Header().Get("Location"), "/api/auth/gitlab/callback", glUser(5, "amy", "amy@corp.test"))
			r, err := browser.Get(o.cliCallback(code, state, c).Header().Get("Location"))
			if err != nil {
				t.Error(err)
				return
			}
			r.Body.Close()
		}()
	}
	var out bytes.Buffer
	if err := cli.Run([]string{"login", "--gitlab", "--server", srv.URL}, &out); err != nil {
		t.Fatal(err, out.String())
	}
	cred, err := os.ReadFile(filepath.Join(dir, "cred.json"))
	if err != nil || !strings.Contains(string(cred), "omx_") || !strings.Contains(out.String(), "amy@corp.test") {
		t.Fatal(err, out.String())
	}
	o.svc.GitLab = nil
	if err := cli.Run([]string{"login", "--gitlab", "--server", srv.URL}, &out); err == nil || !strings.Contains(err.Error(), "沒有啟用") {
		t.Fatal("沒啟用:", err)
	}
}

func TestConfigureGitLab(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(secret, []byte("shh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"OMIXOR_GITLAB_URL": "https://gitlab.example.test", "OMIXOR_GITLAB_CLIENT_ID": "id", "OMIXOR_GITLAB_CLIENT_SECRET_FILE": secret}
	run := func(public string, ldap service.Directory, kv ...string) (*service.Service, error) {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		svc := &service.Service{LDAP: ldap}
		return svc, configureGitLab(svc, func(k string) string { return m[k] }, public)
	}
	svc, err := run(tPublic, nil, "OMIXOR_GITLAB_ADMINS", "3, 4")
	if err != nil || svc.GitLab == nil || len(svc.GitLab.Admins) != 2 || svc.GitLab.LinkLDAP || svc.GitLab.TrustMail {
		t.Fatal(svc.GitLab, err)
	}
	if svc, err := run("", nil, "OMIXOR_GITLAB_URL", ""); err != nil || svc.GitLab != nil {
		t.Fatal("沒設 URL = 不啟用:", err)
	}
	for name, c := range map[string]struct {
		public string
		kv     []string
		want   string
	}{
		"缺 id":      {tPublic, []string{"OMIXOR_GITLAB_CLIENT_ID", ""}, "CLIENT_ID"},
		"缺 secret":  {tPublic, []string{"OMIXOR_GITLAB_CLIENT_SECRET_FILE", ""}, "CLIENT_SECRET"},
		"缺對外網址":     {"", nil, "OMIXOR_PUBLIC_URL"},
		"admin 非數字": {tPublic, []string{"OMIXOR_GITLAB_ADMINS", "alice"}, "數字"},
		"併帳號沒 LDAP": {tPublic, []string{"OMIXOR_GITLAB_LINK_LDAP", "true"}, "LDAP"},
	} {
		if _, err := run(c.public, nil, c.kv...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s:%v", name, err)
		}
	}
	if svc, err := run(tPublic, nil, "OMIXOR_GITLAB_EMAIL_DOMAINS", " @Corp.Test, b.test "); err != nil || strings.Join(svc.GitLab.EmailDomains, ",") != "corp.test,b.test" {
		t.Error("email 網域:", err)
	}
	for _, bad := range []string{"a@corp.test", "*.corp.test", " , "} {
		if _, err := run(tPublic, nil, "OMIXOR_GITLAB_EMAIL_DOMAINS", bad); err == nil || !strings.Contains(err.Error(), "EMAIL_DOMAINS") {
			t.Error("要拒絕:", bad, err)
		}
	}
	if _, err := run(tPublic, &fakeLookup{}, "OMIXOR_GITLAB_LINK_LDAP", "true"); err != nil {
		t.Error("有可查詢的目錄:", err)
	}
	// 設定檢查:只設了其他項目沒設 URL、設了 URL 沒對外網址
	for _, m := range []map[string]string{{"OMIXOR_GITLAB_CLIENT_ID": "x"}, {"OMIXOR_GITLAB_URL": "https://g.test"}} {
		m["OMIXOR_DATABASE_URL"] = "postgres://u@127.0.0.1/x"
		if err := config.LoadFrom(envMap(m), config.Server).Validate(config.Server); err == nil || !strings.Contains(err.Error(), "OMIXOR_GITLAB_URL") {
			t.Error(m, err)
		}
	}
}
