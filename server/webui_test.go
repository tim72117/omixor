package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omixor-core/config"
	"omixor-core/model"
	"omixor-core/service"
)

// /console 畫面與瀏覽器登入(evoleap 是 httptest 假伺服器,不連真的服務)

const (
	tWebHost       = "omixor.example.test"
	tSessionCookie = "__Host-" + webSessionCookie
	tLoginCookie   = "__Host-" + webLoginCookie
)

func testUIDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": "<!doctype html>CONSOLE-INDEX", "assets/app-abc.js": "console.log(1)"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// newWebUI 兩個組織:o(boss 是組織管理員、amy 是成員)、o2(只有 zed);boss 也是伺服器管理員但不是 o2 的成員
func newWebUI(t *testing.T) (*oaHTTP, *fakeEvoleap) {
	t.Helper()
	o := newOAHTTP(t)
	f := newFakeEvoleap(t)
	o.svc.Evoleap, o.svc.EvoleapConsole = f.srv.URL, tConsole
	st := o.svc.Store
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateOrg(model.Org{ID: "o2", Name: "o2", CreatedAt: model.Now()}))
	boss, err := st.UserByID("u1")
	must(err)
	boss.Role = "admin"
	must(st.SaveUser(*boss))
	must(st.SaveUser(model.User{ID: "u2", Email: "amy@x", Role: "member", CreatedAt: model.Now()}))
	must(st.SaveMember(model.Member{Org: "o", UserID: "u2", Role: "member"}))
	must(st.SaveUser(model.User{ID: "u3", Email: "zed@x", Role: "member", CreatedAt: model.Now()}))
	must(st.SaveMember(model.Member{Org: "o2", UserID: "u3", Role: "admin"}))
	o.h = HandlerWith(o.svc, config.LoadFrom(envMap(map[string]string{"OMIXOR_PUBLIC_URL": "https://" + tWebHost, "OMIXOR_UI_DIR": testUIDir(t)}), config.Server))
	return o, f
}

func respCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// webLogin 走完 evoleap 轉址;回傳 callback 的回應
func webLogin(t *testing.T, o *oaHTTP, f *fakeEvoleap, evoID, email string) *httptest.ResponseRecorder {
	t.Helper()
	f.mu.Lock()
	f.meUser = map[string]any{"id": evoID, "email": email, "name": email, "google": true}
	f.mu.Unlock()
	w := o.do("GET", "/api/auth/web/evoleap/start", "")
	b := respCookie(w, tLoginCookie)
	if w.Code != 303 || b == nil || b.Path != "/" || !b.HttpOnly || !b.Secure || b.SameSite != http.SameSiteLaxMode || !strings.HasPrefix(w.Header().Get("Location"), tConsole+"/login/cli?id=") {
		t.Fatalf("start:%d %v %+v", w.Code, w.Header(), b)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || loc.Query().Get("id") == b.Value {
		t.Fatalf("綁定值要和網址上的 id 不同:%s", loc)
	}
	return o.do("GET", "/api/auth/web/evoleap/callback?id="+loc.Query().Get("id")+"&code=c1", "", "Cookie", tLoginCookie+"="+b.Value)
}

func webSessionFor(t *testing.T, o *oaHTTP, f *fakeEvoleap, evoID, email string) string {
	t.Helper()
	w := webLogin(t, o, f, evoID, email)
	c := respCookie(w, tSessionCookie)
	if w.Code != 303 || w.Header().Get("Location") != "/console/" || c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("callback:%d %s %+v", w.Code, w.Header().Get("Location"), c)
	}
	return c.Value
}

// browser 像畫面一樣呼叫:帶 cookie;寫入帶 CSRF 標頭與同源 Origin
func browser(o *oaHTTP, tok, method, path, body string) *httptest.ResponseRecorder {
	hdr := []string{"Cookie", tSessionCookie + "=" + tok}
	if method != "GET" {
		hdr = append(hdr, "X-Omixor-CSRF", "1", "Origin", "http://example.com")
	}
	return o.do(method, path, body, hdr...)
}

func wantCode(t *testing.T, w *httptest.ResponseRecorder, code int, what string) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("%s:要 %d,得到 %d %s", what, code, w.Code, w.Body.String())
	}
}

func TestWebUIDisabled(t *testing.T) {
	o := newOAHTTP(t)
	for _, p := range []string{"/console", "/console/", "/api/auth/web/status", "/api/auth/web/evoleap/start"} {
		wantCode(t, o.do("GET", p, ""), 404, p)
	}
	res, err := o.svc.IssueWebSession("u1", "boss@x", webSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{webSessionCookie, tSessionCookie} {
		wantCode(t, o.do("GET", "/api/orgs", "", "Cookie", name+"="+res.Token), 401, "沒啟用畫面時不收 cookie")
	}
	// 畫面關掉後,之前發的 web session 也不能當 Bearer
	wantCode(t, o.do("GET", "/api/orgs", "", "Authorization", "Bearer "+res.Token), 403, "web token 當 Bearer")
	wantCode(t, o.do("GET", "/api/admin/users", "", "Authorization", "Bearer "+res.Token), 403, "web token 當 Bearer 管理")
	wantCode(t, o.do("POST", "/api/auth/pair", "", "Authorization", "Bearer "+res.Token), 403, "web token 當 Bearer 配對")
	wantCode(t, o.do("GET", "/api/orgs", "", "Authorization", "Bearer omx_mac"), 200, "一般 token 照舊")
}

func TestWebUIStatic(t *testing.T) {
	o, _ := newWebUI(t)
	wantCode(t, o.do("GET", "/console", ""), 308, "/console")
	for _, p := range []string{"/console/", "/console/deep/link", "/console/index.html"} {
		w := o.do("GET", p, "")
		wantCode(t, w, 200, p)
		h := w.Header()
		if !strings.Contains(w.Body.String(), "CONSOLE-INDEX") || h.Get("Cache-Control") != "no-cache" || h.Get("X-Frame-Options") != "DENY" ||
			!strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") || h.Get("Strict-Transport-Security") == "" {
			t.Fatalf("%s:%v %s", p, h, w.Body.String())
		}
	}
	w := o.do("GET", "/console/assets/app-abc.js", "")
	wantCode(t, w, 200, "asset")
	if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Fatal(w.Header())
	}
	for _, p := range []string{"/console/assets/nope.js", "/console/assets/nope", "/console/x.map"} {
		wantCode(t, o.do("GET", p, ""), 404, p)
	}
}

func TestWebLoginAndLogout(t *testing.T) {
	o, f := newWebUI(t)
	st := jsonBody(t, o.do("GET", "/api/auth/web/status", ""))
	if st["mode"] != "server" || st["loggedIn"] != false {
		t.Fatal(st)
	}
	// 不在名單上:不建帳號、不發 session
	w := webLogin(t, o, f, "ev-9", "stranger@x")
	if w.Header().Get("Location") != "/console/?login_error=denied" || respCookie(w, tSessionCookie) != nil {
		t.Fatal(w.Header())
	}
	// 沒有綁定 cookie(別人的瀏覽器開回呼網址)
	w = o.do("GET", "/api/auth/web/evoleap/callback?id=abc&code=c", "")
	if w.Header().Get("Location") != "/console/?login_error=expired" || respCookie(w, tSessionCookie) != nil {
		t.Fatal(w.Header())
	}
	tok := webSessionFor(t, o, f, "ev-2", "amy@x")
	st = jsonBody(t, browser(o, tok, "GET", "/api/auth/web/status", ""))
	if st["loggedIn"] != true || st["user"].(map[string]any)["email"] != "amy@x" {
		t.Fatal(st)
	}
	if _, _, logouts := f.counts(); logouts != 2 {
		t.Fatalf("evoleap token 要撤銷:%d", logouts)
	}
	// CSRF
	c := tSessionCookie + "=" + tok
	wantCode(t, o.do("POST", "/api/orgs/o/issues", `{"title":"x"}`, "Cookie", c), 403, "缺 CSRF 標頭")
	wantCode(t, o.do("POST", "/api/orgs/o/issues", `{"title":"x"}`, "Cookie", c, "X-Omixor-CSRF", "1", "Origin", "https://evil.test"), 403, "別的來源")
	wantCode(t, o.do("POST", "/api/auth/web/logout", "", "Cookie", c), 403, "登出缺 CSRF")
	wantCode(t, browser(o, tok, "GET", "/api/orgs", ""), 200, "還在登入中")
	// token 不能當 Bearer,也不能連 MCP
	wantCode(t, o.do("GET", "/api/orgs", "", "Authorization", "Bearer "+tok), 403, "web token 當 Bearer")
	wantCode(t, o.do("POST", "/api/auth/pair", "", "Authorization", "Bearer "+tok), 403, "web token 當 Bearer 配對")
	if w = o.do("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "Authorization", "Bearer "+tok); w.Code != 401 && w.Code != 403 {
		t.Fatalf("MCP:%d", w.Code)
	}
	// 使用者自己的 token(CLI)不受影響
	wantCode(t, o.do("GET", "/api/orgs", "", "Authorization", "Bearer omx_mac"), 200, "Bearer")
	wantCode(t, o.do("POST", "/api/auth/pair", "", "Authorization", "Bearer omx_mac"), 200, "Bearer 配對照舊")
	// 停用:下一個請求就失效
	u, _ := o.svc.Store.UserByID("u2")
	u.Disabled = true
	_ = o.svc.Store.SaveUser(*u)
	wantCode(t, browser(o, tok, "GET", "/api/orgs", ""), 403, "停用")
	u.Disabled = false
	_ = o.svc.Store.SaveUser(*u)
	wantCode(t, browser(o, tok, "POST", "/api/auth/web/logout", ""), 200, "登出")
	wantCode(t, browser(o, tok, "GET", "/api/orgs", ""), 401, "登出後")
}

func TestWebSessionLimitsAndIsolation(t *testing.T) {
	o, f := newWebUI(t)
	boss := webSessionFor(t, o, f, "ev-1", "boss@x")
	amy := webSessionFor(t, o, f, "ev-2", "amy@x")
	zed := webSessionFor(t, o, f, "ev-3", "zed@x")

	// 預設拒絕:配對、伺服器管理、組織與成員、系統設定、角色憑證
	for _, c := range [][3]string{
		{"POST", "/api/auth/pair", ""}, {"GET", "/api/admin/users", ""}, {"GET", "/api/admin/tokens", ""},
		{"POST", "/api/admin/users", `{"email":"n@x","role":"admin"}`}, {"GET", "/api/admin/audit", ""},
		{"POST", "/api/orgs", `{"id":"o3"}`}, {"POST", "/api/orgs/o/delete", ""}, {"POST", "/api/orgs/o/members", `{"email":"n@x","role":"admin"}`},
		{"POST", "/api/system/features", `{}`}, {"GET", "/api/system/schema-check", ""}, {"POST", "/api/orgs/o/system", `{}`},
		{"POST", "/api/orgs/o/agent-tasks/claim", `{"agentId":"a"}`}, {"POST", "/api/orgs/o/agents", `{"name":"a"}`},
		{"GET", "/api/issues/o", ""}, {"GET", "/api/orgs/o/issues/export", ""},
	} {
		w := browser(o, boss, c[0], c[1], c[2])
		// GET /issues/export 和 GET /issues/{id} 同樣式比對時以較精確的 export 為準,一樣要擋
		wantCode(t, w, 403, c[0]+" "+c[1])
	}
	// 伺服器管理員在瀏覽器裡沒有跨組織權限
	var orgs struct{ Result []model.Org }
	_ = json.Unmarshal(browser(o, boss, "GET", "/api/orgs", "").Body.Bytes(), &orgs)
	if len(orgs.Result) != 1 || orgs.Result[0].ID != "o" {
		t.Fatalf("boss 的組織:%+v", orgs.Result)
	}
	wantCode(t, browser(o, boss, "GET", "/api/orgs/o2/issues", ""), 403, "boss 看 o2")

	admin, err := o.svc.Authenticate("omx_mac")
	if err != nil {
		t.Fatal(err)
	}
	prj, err := o.svc.CreateProject(admin, "o", service.ProjectInput{Name: "secret", Key: "SEC", Restricted: true})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := o.svc.CreateIssue(admin, "o", service.IssueInput{Title: "HIDDEN-TITLE", Project: &prj.ID})
	if err != nil {
		t.Fatal(err)
	}
	// amy:寫入署本人;受限專案在清單、看板、單筆都不存在;別的組織不能讀寫
	w := browser(o, amy, "POST", "/api/orgs/o/issues", `{"title":"amy 開的","author":"boss@x"}`)
	wantCode(t, w, 200, "amy 建立")
	if !strings.Contains(w.Body.String(), `"createdBy":"amy@x"`) {
		t.Fatal(w.Body.String())
	}
	for _, p := range []string{"/api/orgs/o/issues", "/api/orgs/o/issues/board", "/api/orgs/o/projects"} {
		w = browser(o, amy, "GET", p, "")
		wantCode(t, w, 200, p)
		if s := w.Body.String(); strings.Contains(s, "HIDDEN-TITLE") || strings.Contains(s, "secret") {
			t.Fatalf("%s 洩漏:%s", p, s)
		}
	}
	for _, p := range []string{"", "/items"} {
		wantCode(t, browser(o, amy, "GET", "/api/orgs/o/issues/"+hidden.Key+p, ""), 404, "受限單筆"+p)
	}
	wantCode(t, browser(o, amy, "POST", "/api/orgs/o/issues/"+hidden.Key+"/comments", `{"text":"x"}`), 404, "受限留言")
	for _, p := range []string{"/api/orgs/o/issues", "/api/orgs/o/actors", "/api/orgs/o/agents", "/api/orgs/o/decisions"} {
		wantCode(t, browser(o, zed, "GET", p, ""), 403, "zed 讀 "+p)
	}
	wantCode(t, browser(o, zed, "POST", "/api/orgs/o/issues", `{"title":"x"}`), 403, "zed 寫")

	// 附件圖片:組織成員拿得到,別組織與沒登入拿不到
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	w = browser(o, amy, "POST", "/api/orgs/o/attachments", `{"data":"`+png+`"}`)
	wantCode(t, w, 200, "上傳")
	id := jsonBody(t, w)["result"].(map[string]any)["id"].(string)
	w = browser(o, amy, "GET", "/api/web/orgs/o/attachments/"+id, "")
	wantCode(t, w, 200, "圖片")
	if w.Header().Get("Content-Type") != "image/png" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(w.Header())
	}
	wantCode(t, browser(o, zed, "GET", "/api/web/orgs/o/attachments/"+id, ""), 403, "別組織的圖片")
	wantCode(t, o.do("GET", "/api/web/orgs/o/attachments/"+id, ""), 401, "沒登入的圖片")

	// 子項領取:回應不帶角色憑證
	if _, err := o.svc.SaveAgent(admin, "o", model.Agent{ID: "bot", Name: "bot", Active: true}); err != nil {
		t.Fatal(err)
	}
	iss, err := o.svc.CreateIssue(admin, "o", service.IssueInput{Title: "有子項"})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"一", "二"} {
		if _, err := o.svc.AddItem(admin, "o", iss.Key, service.ItemInput{Title: title, Assignee: "agent:bot"}); err != nil {
			t.Fatal(err)
		}
	}
	w = browser(o, boss, "POST", "/api/orgs/o/issues/"+iss.Key+"/items/"+iss.Key+".1/claim", `{}`)
	wantCode(t, w, 200, "領取")
	if s := w.Body.String(); strings.Contains(s, "credential") || strings.Contains(s, "secret") || !strings.Contains(s, `"item"`) {
		t.Fatalf("憑證要拿掉:%s", s)
	}
	w = o.do("POST", "/api/orgs/o/issues/"+iss.Key+"/items/"+iss.Key+".2/claim", `{}`, "Authorization", "Bearer omx_mac")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "credential") {
		t.Fatalf("Bearer 領取照舊:%d %s", w.Code, w.Body.String())
	}
}

// 綁定密鑰由伺服器產生並記錄:自己湊一對 cookie 與 id、用網址上的 id 當 cookie、別人的密鑰、重放都不收
func TestWebLoginBinding(t *testing.T) {
	o, f := newWebUI(t)
	f.mu.Lock()
	f.meUser = map[string]any{"id": "ev-2", "email": "amy@x", "name": "amy", "google": true}
	f.mu.Unlock()
	start := func() (id, bind string) {
		w := o.do("GET", "/api/auth/web/evoleap/start", "")
		loc, _ := url.Parse(w.Header().Get("Location"))
		return loc.Query().Get("id"), respCookie(w, tLoginCookie).Value
	}
	callback := func(id, cookie, bind string) *httptest.ResponseRecorder {
		return o.do("GET", "/api/auth/web/evoleap/callback?id="+id+"&code=c1", "", "Cookie", cookie+"="+bind)
	}
	id, bind := start()
	_, other := start()
	for what, w := range map[string]*httptest.ResponseRecorder{
		"自己湊的一對":       callback("forged", tLoginCookie, "forged"),
		"id 當 cookie":  callback(id, tLoginCookie, id),
		"另一次登入的密鑰":     callback(id, tLoginCookie, other),
		"沒有前綴的 cookie": callback(id, webLoginCookie, bind),
	} {
		if w.Header().Get("Location") != "/console/?login_error=expired" || respCookie(w, tSessionCookie) != nil || respCookie(w, tLoginCookie) != nil {
			t.Fatalf("%s:%v", what, w.Header())
		}
	}
	if _, exchanges, _ := f.counts(); exchanges != 0 {
		t.Fatalf("對不上的回呼不能換 token:%d", exchanges)
	}
	// 上面的嘗試沒有弄壞進行中的登入
	w := callback(id, tLoginCookie, bind)
	if respCookie(w, tSessionCookie) == nil || respCookie(w, tLoginCookie) == nil {
		t.Fatal(w.Header())
	}
	if w = callback(id, tLoginCookie, bind); respCookie(w, tSessionCookie) != nil {
		t.Fatal("重放")
	}
}

// 本機 http 開發:cookie 沒有前綴(瀏覽器不收非 Secure 的 __Host-),路徑照舊
func TestWebCookieNames(t *testing.T) {
	u := &webUI{public: "http://127.0.0.1:8080"}
	if c := u.sessionCookie("v", 1); c.Name != webSessionCookie || c.Path != "/api" || c.Secure {
		t.Fatalf("%+v", c)
	}
	if c := u.loginCookie("v", 1); c.Name != webLoginCookie || c.Path != "/api/auth/web" || c.Secure {
		t.Fatalf("%+v", c)
	}
	u.public = "https://" + tWebHost
	for _, c := range []*http.Cookie{u.sessionCookie("v", 1), u.loginCookie("v", 1)} {
		if !strings.HasPrefix(c.Name, "__Host-") || c.Path != "/" || !c.Secure || c.Domain != "" {
			t.Fatalf("%+v", c)
		}
	}
}
