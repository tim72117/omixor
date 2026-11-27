package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omixor-core/api"
	"omixor-core/model"
	"omixor-core/service"
)

// 瀏覽器 session(宿主掛 core/api 時的選項):cookie 認證、CSRF、撤銷與停用;沒設 Session(omixor server)時 cookie 無效
func TestWebSession(t *testing.T) {
	e := newEnv(t)
	u := model.User{ID: "u_web", Email: "web@example.com", Role: "member", CreatedAt: model.Now()}
	if err := e.st.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.IssueWebSession(u.ID, "web", 0); err == nil {
		t.Fatal("web session 一定要有有效時間")
	}
	login := func() string {
		r, err := e.svc.IssueWebSession(u.ID, "web", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if r.ExpiresAt == nil {
			t.Fatal("沒有到期時間")
		}
		return r.Token
	}
	a := api.New(e.svc)
	a.Session = &api.Session{Cookie: "omx_session", NoLoginRoutes: true, NoAgentCredentials: true}
	host := httptest.NewServer(a.Handler())
	t.Cleanup(host.Close)

	call := func(base, method, path, cookie string, hdr map[string]string) int {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader("{}"))
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "omx_session", Value: cookie})
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return resp.StatusCode
	}
	want := func(name string, got, w int) {
		t.Helper()
		if got != w {
			t.Errorf("%s:%d,要 %d", name, got, w)
		}
	}
	tok := login()
	csrf := map[string]string{api.CSRFHeader: "1"}
	want("cookie 讀", call(host.URL, "GET", "/api/auth/me", tok, nil), 200)
	want("沒開 Session 的伺服器不看 cookie", call(e.srv.URL, "GET", "/api/auth/me", tok, nil), 401)
	want("cookie 寫入沒帶 CSRF 標頭", call(host.URL, "POST", "/api/auth/logout", tok, nil), 403)
	want("跨來源", call(host.URL, "POST", "/api/auth/logout", tok, map[string]string{api.CSRFHeader: "1", "Origin": "http://evil.example"}), 403)
	want("沒有憑證", call(host.URL, "GET", "/api/auth/me", "", nil), 401)
	want("角色憑證不收", call(host.URL, "GET", "/api/modules", "", map[string]string{"Authorization": "Bearer a:b"}), 401)
	for _, p := range []string{"/api/auth/ldap", "/api/auth/evoleap", "/api/auth/pair/claim", "/api/auth/gitlab/start"} {
		want("不掛登入端點 "+p, call(host.URL, "POST", p, "", nil), 404)
	}
	// 一般 omx_ token 不能當 cookie
	plain := "omx_plain-token-for-test"
	if err := e.st.CreateToken(model.Token{ID: "t_plain", UserID: u.ID, Device: "CLI", CreatedAt: model.Now(), Hash: service.HashToken(plain)}); err != nil {
		t.Fatal(err)
	}
	want("非 web token 當 cookie", call(host.URL, "GET", "/api/auth/me", plain, nil), 401)
	want("Bearer 不需要 CSRF", call(host.URL, "POST", "/api/auth/logout", "", map[string]string{"Authorization": "Bearer " + plain}), 200)
	want("登出", call(host.URL, "POST", "/api/auth/logout", tok, csrf), 200)
	want("登出後", call(host.URL, "GET", "/api/auth/me", tok, nil), 401)

	// 全部撤銷只撤 web session
	tok, other := login(), login()
	n, err := e.svc.RevokeWebSessions()
	if err != nil || n != 2 {
		t.Fatalf("撤銷 %d %v", n, err)
	}
	want("撤銷後", call(host.URL, "GET", "/api/auth/me", tok, nil), 401)
	want("撤銷後 2", call(host.URL, "GET", "/api/auth/me", other, nil), 401)

	tok = login()
	u.Disabled = true
	if err := e.st.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	want("停用立即生效", call(host.URL, "GET", "/api/auth/me", tok, nil), 403)
}
