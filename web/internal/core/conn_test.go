package core

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 連線測試,照 Swift 的 ConnectionTests.swift 一個一個搬

func connTestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	t.Setenv("OPS_NO_TIMERS", "1")
	Vault = NewMemoryVault()
	saved := OpenBrowser
	t.Cleanup(func() { OpenBrowser = saved })
	a := New()
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	return a
}

func formOf(body []byte) map[string]string {
	out := map[string]string{}
	v, _ := url.ParseQuery(string(body))
	for k := range v {
		out[k] = v.Get(k)
	}
	return out
}

// browserApproves 模擬瀏覽器:使用者按同意後,把瀏覽器導回 redirect_uri?code=…&state=…
func browserApproves(t *testing.T, f *FakeNet, code, param string, extra map[string]string, opened *string) {
	var mu sync.Mutex
	OpenBrowser = func(u string) {
		mu.Lock()
		if opened != nil {
			*opened = u
		}
		mu.Unlock()
		parsed, _ := url.Parse(u)
		q := parsed.Query()
		redirect := q.Get("redirect_uri")
		if redirect == "" { // 瀏覽器核准:回呼網址在 start 時就登記了
			if r, ok := f.Find("/api/cli-auth/start"); ok {
				var o map[string]any
				_ = json.Unmarshal(r.Body, &o)
				redirect, _ = o["redirect_uri"].(string)
			}
		}
		value := q.Get(param)
		if value == "" {
			value = extra[param]
		}
		go func() {
			cb, _ := url.Parse(redirect)
			cb.RawQuery = url.Values{"code": {code}, param: {value}}.Encode()
			resp, err := http.Get(cb.String())
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
}

// MARK: - 純函式

func TestPKCEMatchesRFC7636Vector(t *testing.T) {
	if got := pkceChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge = %s", got)
	}
	if n := len(randomString(48)); n < 43 {
		t.Fatalf("verifier 太短:%d", n)
	}
}

func TestMetadataURLs(t *testing.T) {
	got := oauthMetadataURLs("https://accounts.google.com")
	want := []string{"https://accounts.google.com/.well-known/openid-configuration", "https://accounts.google.com/.well-known/oauth-authorization-server"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v", got)
	}
	tenant := oauthMetadataURLs("https://auth.example.com/realms/ops/")
	if tenant[0] != "https://auth.example.com/realms/ops/.well-known/openid-configuration" {
		t.Fatalf("first = %s", tenant[0])
	}
	if !contains(tenant, "https://auth.example.com/.well-known/oauth-authorization-server/realms/ops") {
		t.Fatalf("缺 RFC 8414 路徑:%v", tenant)
	}
}

func TestAuthorizationURL(t *testing.T) {
	m := OAuthMetadata{Issuer: "i", AuthorizationEndpoint: "https://a.example/authorize?x=1", TokenEndpoint: "t", Resource: "https://api.example"}
	u, err := oauthAuthorizationURL(m, "cid", "http://127.0.0.1:5000/callback", "openid, email", "st", "ch", "access_type=offline&prompt=consent")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := url.Parse(u)
	q := p.Query()
	for k, v := range map[string]string{"x": "1", "response_type": "code", "code_challenge_method": "S256", "scope": "openid email",
		"resource": "https://api.example", "access_type": "offline", "prompt": "consent", "redirect_uri": "http://127.0.0.1:5000/callback"} {
		if q.Get(k) != v {
			t.Errorf("%s = %q,要 %q(%s)", k, q.Get(k), v, u)
		}
	}
}

func TestAccountLabelAndJWT(t *testing.T) {
	if s, _ := accountLabel(map[string]any{"user": map[string]any{"email": "tim@example.com", "id": "1"}}); s != "tim@example.com" {
		t.Fatal(s)
	}
	if s, _ := accountLabel(map[string]any{"login": "octocat"}); s != "octocat" {
		t.Fatal(s)
	}
	payload := base64url(Marshal(map[string]any{"email": "a@b.c", "sub": "1"}))
	if c := jwtClaims("h." + payload + ".s"); c["email"] != "a@b.c" {
		t.Fatalf("%v", c)
	}
}

func TestDefaultsSeededAndPersisted(t *testing.T) {
	a := connTestApp(t)
	a.Mu.Lock()
	ids := []string{}
	for _, c := range a.conns().List {
		ids = append(ids, c.ID)
	}
	id := ConnNewID(a, "Payments API")
	a.Mu.Unlock()
	if strings.Join(ids, ",") != "evoleap,evoleap-local" {
		t.Fatalf("預設連線:%v", ids)
	}
	if id != "payments-api" {
		t.Fatalf("id = %s", id)
	}
	c := NewConnection(id, "Payments API", "https://pay.example")
	c.Auth = AuthBearer
	c.TestPath = "/me"
	ConnUpsert(a, c)

	a2 := New()
	a2.Mu.Lock()
	a2.LoadOrgData()
	list := a2.conns().List
	a2.Mu.Unlock()
	if list[len(list)-1].TestPath != "/me" {
		t.Fatalf("沒存檔:%+v", list[len(list)-1])
	}
	// 檔案格式和 Swift 一樣:沒有 registeredPort、欄位齊全
	b, _ := os.ReadFile(filepath.Join(DataRoot(), "orgs", DefaultOrgID, "connections.json"))
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	oauth := raw[0]["oauth"].(map[string]any)
	if _, ok := oauth["registeredPort"]; ok {
		t.Fatal("registeredPort 是 nil 時不該寫出來")
	}
	if raw[1]["approval"].(map[string]any)["consoleURL"] != "http://localhost:5190" || oauth["scopes"] != "openid email offline_access" {
		t.Fatalf("%s", b)
	}
}

func TestSwiftConnectionsFileDecodes(t *testing.T) {
	// Swift 寫的檔案(prettyPrinted 的 " : ")也能讀;缺欄位用預設值
	var list []Connection
	err := json.Unmarshal([]byte(`[{"auth" : "bearer", "baseURL" : "https://x.example", "id" : "x", "name" : "X",
		"oauth" : {"registeredPort" : 5123, "clientId" : "c"}}]`), &list)
	if err != nil {
		t.Fatal(err)
	}
	c := list[0]
	if c.Auth != AuthBearer || *c.OAuth.RegisteredPort != 5123 || c.OAuth.Scopes != "openid email offline_access" || c.Approval.StartPath != "/api/cli-auth/start" {
		t.Fatalf("%+v", c)
	}
}

// MARK: - OAuth 全流程(只給網址)

func TestOAuthDiscoveryRegistrationLoginRefreshAndRequest(t *testing.T) {
	a := connTestApp(t)
	var mu sync.Mutex
	tokenCalls := 0
	var fail []string
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		u := "https://" + r.Host + r.URL.RequestURI()
		form := formOf(body)
		switch u {
		case "https://api.example/.well-known/oauth-protected-resource":
			return 200, map[string]any{"resource": "https://api.example", "authorization_servers": []string{"https://auth.example"}}
		case "https://auth.example/.well-known/openid-configuration":
			return 200, map[string]any{"issuer": "https://auth.example", "authorization_endpoint": "https://auth.example/authorize",
				"token_endpoint": "https://auth.example/token", "registration_endpoint": "https://auth.example/register"}
		case "https://auth.example/register":
			return 201, map[string]any{"client_id": "dyn-123"}
		case "https://auth.example/token":
			mu.Lock()
			defer mu.Unlock()
			tokenCalls++
			if form["client_id"] != "dyn-123" || form["resource"] != "https://api.example" {
				fail = append(fail, "token 表單:"+string(body))
			}
			if form["grant_type"] == "authorization_code" {
				if form["code"] != "the-code" || form["code_verifier"] == "" {
					fail = append(fail, "授權碼表單:"+string(body))
				}
				return 200, map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 1, "token_type": "Bearer"}
			}
			if form["refresh_token"] != "rt-1" {
				fail = append(fail, "refresh 表單:"+string(body))
			}
			return 200, map[string]any{"access_token": "at-2", "expires_in": 3600}
		case "https://api.example/me":
			auth := r.Header.Get("Authorization")
			if auth == "Bearer at-1" || auth == "Bearer at-2" {
				return 200, map[string]any{"email": "tim@example.com"}
			}
			return 401, map[string]any{"error": "unauthorized"}
		}
		return 404, map[string]any{"error": "not found: " + u}
	})
	defer f.Close()
	c := NewConnection("api", "API", "https://api.example")
	c.TestPath = "/me"
	ConnUpsert(a, c)
	browserApproves(t, f, "the-code", "state", nil, nil)

	ConnLogin(a, "api")
	a.Mu.Lock()
	st := connStateCopy(a, "api")
	got, _ := ConnFind(a, "api")
	a.Mu.Unlock()
	if st.Error != nil {
		t.Fatalf("登入失敗:%s", *st.Error)
	}
	if derefStr(st.Account) != "tim@example.com" {
		t.Fatalf("account = %v", st.Account)
	}
	if got.OAuth.ClientID != "dyn-123" || got.OAuth.Issuer != "https://auth.example" || got.OAuth.RegisteredPort == nil {
		t.Fatalf("動態註冊拿到的 client_id 要存回設定:%+v", got.OAuth)
	}
	if tk := vaultTokens("", "api"); tk == nil || derefStr(tk.RefreshToken) != "rt-1" {
		t.Fatalf("token:%+v", tk)
	}
	// 登入寫一筆動態
	a.Mu.Lock()
	logText := a.C["activity"][0]["text"]
	a.Mu.Unlock()
	if logText != "omixor 登入「API」(tim@example.com)" {
		t.Fatalf("動態:%v", logText)
	}

	// access token 1 秒後過期(60 秒內就算快過期):下一次請求自動換新
	me, err := ConnRequest(a, "api", "GET", "/me", nil)
	if err != nil || me.(map[string]any)["email"] != "tim@example.com" {
		t.Fatalf("%v %v", me, err)
	}
	tk := vaultTokens("", "api")
	if tk.AccessToken != "at-2" || derefStr(tk.RefreshToken) != "rt-1" {
		t.Fatalf("沒給新的 refresh token 時沿用舊的:%+v", tk)
	}
	mu.Lock()
	if tokenCalls != 2 || len(fail) > 0 {
		t.Fatalf("tokenCalls=%d fail=%v", tokenCalls, fail)
	}
	mu.Unlock()

	ConnLogout(a, "api")
	if vaultTokens("", "api") != nil {
		t.Fatal("登出後 token 還在")
	}
	if _, err := ConnRequest(a, "api", "GET", "/me", nil); err == nil {
		t.Fatal("登出後應該不能呼叫")
	} else if err.Error() != "HTTP 401:還沒登入「API」" {
		t.Fatalf("錯誤訊息:%s", err)
	}
}

func TestOAuth401RefreshesAndRetries(t *testing.T) {
	a := connTestApp(t)
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		switch r.URL.Path {
		case "/token":
			if formOf(body)["refresh_token"] != "rt" {
				return 400, map[string]any{"error": "invalid_grant"}
			}
			return 200, map[string]any{"access_token": "fresh", "expires_in": 3600}
		case "/data":
			if r.Header.Get("Authorization") == "Bearer fresh" {
				return 200, map[string]any{"ok": true}
			}
			return 401, map[string]any{"error": "revoked"}
		}
		return 404, nil
	})
	defer f.Close()
	c := NewConnection("svc", "Svc", "https://svc.example")
	c.OAuth.AuthorizationEndpoint = "https://svc.example/authorize"
	c.OAuth.TokenEndpoint = "https://svc.example/token"
	c.OAuth.ClientID = "cid"
	ConnUpsert(a, c)
	vaultSetTokens("", "svc", &TokenSet{AccessToken: "revoked", RefreshToken: strPtr("rt")})
	out, err := ConnRequest(a, "svc", "GET", "/data", nil)
	if err != nil || out.(map[string]any)["ok"] != true {
		t.Fatalf("%v %v", out, err)
	}
	if n := len(f.Requests()); n != 3 {
		t.Fatalf("要 3 個請求(401、換 token、重試),實際 %d", n)
	}
	if vaultTokens("", "svc").AccessToken != "fresh" {
		t.Fatal("新的 token 要存回鑰匙圈")
	}
	// refresh token 也失效:回 401 登入已過期
	vaultSetTokens("", "svc", &TokenSet{AccessToken: "revoked", RefreshToken: strPtr("bad")})
	_, err = ConnRequest(a, "svc", "GET", "/data", nil)
	if e, ok := AsConnError(err); !ok || e.Status != 401 || e.Message != "登入已過期,請重新登入(invalid_grant)" {
		t.Fatalf("%v", err)
	}
}

func TestOAuthWithoutRegistrationNeedsClientId(t *testing.T) {
	a := connTestApp(t)
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		if r.Host == "auth.example" && r.URL.Path == "/.well-known/openid-configuration" {
			return 200, map[string]any{"issuer": "https://auth.example", "authorization_endpoint": "https://auth.example/a", "token_endpoint": "https://auth.example/t"}
		}
		return 404, map[string]any{}
	})
	defer f.Close()
	c := NewConnection("g", "G", "https://svc.example")
	c.OAuth.Issuer = "https://auth.example"
	ConnUpsert(a, c)
	ConnLogin(a, "g")
	a.Mu.Lock()
	st := connStateCopy(a, "g")
	a.Mu.Unlock()
	if st == nil || !strings.Contains(derefStr(st.Error), "client_id") || st.Busy {
		t.Fatalf("%+v", st)
	}
}

func TestDiscoverOrderAndState(t *testing.T) {
	a := connTestApp(t)
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		if r.URL.Path == "/.well-known/oauth-authorization-server/realms/ops" {
			return 200, map[string]any{"issuer": "https://id.example/realms/ops", "authorization_endpoint": "https://id.example/a", "token_endpoint": "https://id.example/t"}
		}
		return 404, map[string]any{}
	})
	defer f.Close()
	c := NewConnection("kc", "KC", "https://app.example/api")
	c.OAuth.Issuer = "https://id.example/realms/ops"
	ConnUpsert(a, c)
	ConnDiscover(a, "kc")
	urls := []string{}
	for _, r := range f.Requests() {
		urls = append(urls, r.URL)
	}
	want := []string{"https://id.example/realms/ops/.well-known/openid-configuration", "https://id.example/.well-known/oauth-authorization-server/realms/ops"}
	if strings.Join(urls, "|") != strings.Join(want, "|") {
		t.Fatalf("探索順序:%v", urls)
	}
	a.Mu.Lock()
	st := connStateCopy(a, "kc")
	got, _ := ConnFind(a, "kc")
	a.Mu.Unlock()
	if derefStr(st.LastTest) != "找到授權伺服器 https://id.example/realms/ops(不支援自動註冊,需要 client_id)" || got.OAuth.TokenEndpoint != "https://id.example/t" {
		t.Fatalf("%+v %+v", st, got.OAuth)
	}

	// 沒有 issuer:先問受保護資源(RFC 9728),找不到就用網址本身
	f.Handler = func(r *http.Request, body []byte) (int, any) { return 404, map[string]any{} }
	n := len(f.Requests())
	c2 := NewConnection("p", "P", "https://res.example/mcp")
	ConnUpsert(a, c2)
	ConnDiscover(a, "p")
	urls = []string{}
	for _, r := range f.Requests()[n:] {
		urls = append(urls, r.URL)
	}
	want = []string{"https://res.example/.well-known/oauth-protected-resource/mcp", "https://res.example/.well-known/oauth-protected-resource",
		"https://res.example/.well-known/openid-configuration", "https://res.example/.well-known/oauth-authorization-server"}
	if strings.Join(urls, "|") != strings.Join(want, "|") {
		t.Fatalf("探索順序:%v", urls)
	}
	a.Mu.Lock()
	st = connStateCopy(a, "p")
	a.Mu.Unlock()
	if derefStr(st.Error) != "在 https://res.example 找不到 OAuth 設定(/.well-known/openid-configuration 或 oauth-authorization-server)。請手動填授權與 Token 端點" {
		t.Fatalf("%v", derefStr(st.Error))
	}
}

// MARK: - 瀏覽器核准(evoleap)

func TestApprovalFlowLikeEvoleap(t *testing.T) {
	a := connTestApp(t)
	var bad []string
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		var o map[string]any
		_ = json.Unmarshal(body, &o)
		switch r.URL.Path {
		case "/api/cli-auth/start":
			if s, _ := o["redirect_uri"].(string); !strings.HasPrefix(s, "http://127.0.0.1:") {
				bad = append(bad, "redirect_uri:"+s)
			}
			return 200, map[string]any{"id": "req-1"}
		case "/api/cli-auth/exchange":
			if o["id"] == "req-1" && o["code"] == "c-9" {
				return 200, map[string]any{"token": "cli-token"}
			}
			return 400, map[string]any{"error": map[string]any{"message": "bad"}}
		case "/api/me":
			if r.Header.Get("Authorization") == "Bearer cli-token" {
				return 200, map[string]any{"user": map[string]any{"email": "admin@evoleap.io"}}
			}
			return 200, map[string]any{"user": nil}
		case "/api/cli-auth/logout":
			return 200, map[string]any{"ok": true}
		}
		return 404, map[string]any{}
	})
	defer f.Close()
	var opened string
	browserApproves(t, f, "c-9", "id", map[string]string{"id": "req-1"}, &opened)

	ConnLogin(a, "evoleap-local")
	a.Mu.Lock()
	st := connStateCopy(a, "evoleap-local")
	a.Mu.Unlock()
	if st.Error != nil || derefStr(st.Account) != "admin@evoleap.io" || len(bad) > 0 {
		t.Fatalf("%+v %v %v", st, derefStr(st.Error), bad)
	}
	if opened != "http://localhost:5190/login/cli?id=req-1" {
		t.Fatalf("核准頁:%s", opened)
	}
	if vaultTokens("", "evoleap-local").AccessToken != "cli-token" {
		t.Fatal("token 沒存")
	}
	ConnLogout(a, "evoleap-local")
	r, ok := f.Find("/api/cli-auth/logout")
	if !ok || r.Auth != "Bearer cli-token" || r.Method != "POST" {
		t.Fatalf("登出要通知伺服器:%+v", r)
	}
}

func TestLoopbackRejectsWrongStateAndReportsError(t *testing.T) {
	cb, err := startLoopback(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cb.stop()
	cb.expect("state", "good")
	get := func(q string) int {
		resp, err := http.Get(cb.RedirectURI() + "?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if s := get("code=x&state=bad"); s != 400 {
		t.Fatalf("state 不符要擋:%d", s)
	}
	resp, _ := http.Get("http://127.0.0.1:" + itoa(cb.Port) + "/other")
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
	resp.Body.Close()
	if s := get("state=good&error=access_denied&error_description=no"); s != 400 {
		t.Fatal(s)
	}
	_, err = cb.waitForCode(time.Second)
	if err == nil || err.Error() != "授權被拒絕:access_denied:no" {
		t.Fatalf("%v", err)
	}
	// 逾時
	cb2, _ := startLoopback(nil)
	defer cb2.stop()
	if _, err := cb2.waitForCode(10 * time.Millisecond); err == nil || !strings.HasPrefix(err.Error(), "等太久了") {
		t.Fatalf("%v", err)
	}
}

func TestRedirectToOtherHostDropsAuthorization(t *testing.T) {
	a := connTestApp(t)
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		switch r.Host {
		case "api.github.com":
			if r.URL.Path == "/same" {
				return 302, Redirect("https://api.github.com/landed")
			}
			if r.URL.Path == "/landed" {
				return 200, RawBody("same host")
			}
			return 302, Redirect("https://pipelines.actions.githubusercontent.com/log?sig=x")
		}
		return 200, RawBody("line1\nline2")
	})
	defer f.Close()
	ConnUpsert(a, GHPresetConnection())
	ConnSetBearer(a, "github", "secret")
	r, err := ConnRequestRaw(a, "github", "GET", "/repos/a/b/actions/jobs/1/logs", nil)
	if err != nil || string(r.Data) != "line1\nline2" {
		t.Fatalf("%v %v", r, err)
	}
	if x, _ := f.Find("githubusercontent"); x.Auth != "" {
		t.Fatalf("轉到別的網站不能帶 token:%q", x.Auth)
	}
	if _, err := ConnRequestRaw(a, "github", "GET", "/same", nil); err != nil {
		t.Fatal(err)
	}
	if x, _ := f.Find("/landed"); x.Auth != "Bearer secret" {
		t.Fatalf("同一個網站要保留:%q", x.Auth)
	}
}

func TestTokenOnlyGoesToSameDomain(t *testing.T) {
	c := GCPPresetConnection()
	if c.Auth != AuthOAuth || !strings.Contains(c.OAuth.Scopes, "cloud-platform.read-only") || strings.Contains(c.OAuth.Scopes, "auth/cloud-platform ") {
		t.Fatalf("%+v", c.OAuth)
	}
	for host, want := range map[string]bool{"run.googleapis.com": true, "www.googleapis.com": true, "evil.com": false,
		"googleapis.com.evil.com": false, "xgoogleapis.com": false, "RUN.GOOGLEAPIS.COM": true, "": false} {
		if c.AllowsHost(host) != want {
			t.Errorf("AllowsHost(%q) 要 %v", host, want)
		}
	}
	if NewConnection("x", "x", "https://example.com").AllowsHost("api.example.com") {
		t.Error("兩段的網域不能放寬到子網域")
	}
	a := connTestApp(t)
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) { return 200, map[string]any{} })
	defer f.Close()
	ConnUpsert(a, c)
	ConnSetBearer(a, "gcp", "ya29.test")
	_, err := ConnRequest(a, "gcp", "GET", "https://evil.com/steal", nil)
	if err == nil || err.Error() != "這個連線不能呼叫 evil.com" {
		t.Fatalf("%v", err)
	}
	if _, ok := f.Find("evil.com"); ok {
		t.Fatal("不該送出")
	}
	if _, err := ConnRequest(a, "gcp", "GET", "https://run.googleapis.com/v2/x", nil); err != nil {
		t.Fatal(err)
	}
	if x, _ := f.Find("run.googleapis.com"); x.Auth != "Bearer ya29.test" {
		t.Fatal(x.Auth)
	}
}

func TestErrorMessagesAndCheck(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{500, `{"error":{"message":"boom"}}`, "HTTP 500:boom"},
		{400, `{"error":"invalid_grant","error_description":"expired"}`, "HTTP 400:expired"},
		{403, `{"message":"Forbidden!"}`, "HTTP 403:Forbidden!"},
		{400, `{"error":"bad"}`, "HTTP 400:bad"},
		{502, `upstream down`, "HTTP 502:upstream down"},
		{404, ``, "HTTP 404:not found"},
		{401, `{"message":"Bad credentials"}`, "HTTP 401:登入已失效,請重新登入"},
	}
	for _, c := range cases {
		_, err := connCheck(&ConnResponse{Status: c.status, Data: []byte(c.body)}, nil)
		if err == nil || err.Error() != c.want {
			t.Errorf("%d %s → %v,要 %s", c.status, c.body, err, c.want)
		}
	}
	if EscapePath("tim72117/evo leap#?") != "tim72117/evo%20leap%23%3F" {
		t.Error(EscapePath("tim72117/evo leap#?"))
	}
	if FormEncode(map[string]string{"b": "a b", "a": "x/y=z"}) != "a=x%2Fy%3Dz&b=a%20b" {
		t.Error(FormEncode(map[string]string{"b": "a b", "a": "x/y=z"}))
	}
}

func TestParseTokensGitHubError(t *testing.T) {
	_, err := oauthParseTokens(map[string]any{"error": "bad_verification_code", "error_description": "The code passed is incorrect or expired."}, nil)
	if err == nil || err.Error() != "The code passed is incorrect or expired." {
		t.Fatal(err)
	}
	_, err = oauthParseTokens(map[string]any{"foo": 1}, nil)
	if err == nil || err.Error() != "Token 回應裡沒有 access_token" {
		t.Fatal(err)
	}
	tk, err := oauthParseTokens(map[string]any{"access_token": "a", "expires_in": "120"}, &TokenSet{AccessToken: "old", RefreshToken: strPtr("r")})
	if err != nil || derefStr(tk.RefreshToken) != "r" || tk.ExpiresAt == nil || tk.IsExpiring() {
		t.Fatalf("%+v %v", tk, err)
	}
}

func TestUpsertClearsTokenWhenAuthChanges(t *testing.T) {
	a := connTestApp(t)
	c := NewConnection("b", "B", "https://b.example")
	c.Auth = AuthBearer
	ConnUpsert(a, c)
	ConnSetBearer(a, "b", "  tok \n")
	if vaultTokens("", "b").AccessToken != "tok" {
		t.Fatal("token 要去頭尾空白")
	}
	c.Note = "只改備註"
	ConnUpsert(a, c)
	if vaultTokens("", "b") == nil {
		t.Fatal("只改備註不該清掉 token")
	}
	c.BaseURL = "https://other.example"
	ConnUpsert(a, c)
	if vaultTokens("", "b") != nil {
		t.Fatal("網址換了要清掉 token")
	}
	ConnSetClientSecret(a, "b", strPtr("s3"))
	a.Mu.Lock()
	got, _ := ConnFind(a, "b")
	a.Mu.Unlock()
	if !got.OAuth.HasClientSecret {
		t.Fatal("hasClientSecret")
	}
	ConnDelete(a, "b")
	if _, ok := vaultSecret("", "b"); ok {
		t.Fatal("刪除連線要清掉 secret")
	}
}

func TestKeychainVaultQuoting(t *testing.T) {
	if secQuote(`a "b" \c`) != `"a \"b\" \\c"` {
		t.Fatal(secQuote(`a "b" \c`))
	}
	if os.Getenv("OPS_KEYCHAIN_TEST") != "1" {
		t.Skip("OPS_KEYCHAIN_TEST=1 才用暫存的鑰匙圈檔案測 /usr/bin/security")
	}
	// 暫存的鑰匙圈檔案(不在搜尋清單裡,不會碰到登入鑰匙圈)
	kc := filepath.Join(t.TempDir(), "ops-test.keychain")
	if out, err := exec.Command("/usr/bin/security", "create-keychain", "-p", "ops-test", kc).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	defer exec.Command("/usr/bin/security", "delete-keychain", kc).Run()
	v := &KeychainVault{Service: "com.tim72117.ops-web.tests", Keychain: kc}
	acct := `org "x"/conn:token`
	if _, ok := v.Get(acct); ok {
		t.Fatal("一開始不該有")
	}
	data := Marshal(TokenSet{AccessToken: "a b \"c\"", RefreshToken: strPtr("r")})
	if err := v.Set(acct, data); err != nil {
		t.Fatal(err)
	}
	if err := v.Set(acct, data); err != nil { // 已存在時更新
		t.Fatal(err)
	}
	got, ok := v.Get(acct)
	if !ok || string(got) != string(data) {
		t.Fatalf("讀回 %q", got)
	}
	if err := v.Delete(acct); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete(acct); err != nil {
		t.Fatal("刪不存在的不算錯", err)
	}
	if _, ok := v.Get(acct); ok {
		t.Fatal("刪掉了還在")
	}
}

func TestOrgScopesAndHooks(t *testing.T) {
	a := connTestApp(t)
	mem := Vault.(*MemoryVault)
	c := NewConnection("b", "B", "https://b.example")
	c.Auth = AuthBearer
	ConnUpsert(a, c)
	ConnSetBearer(a, "b", "default-token")
	a.Mu.Lock()
	blank := a.CreateOrg("Side", "", "", "blank")
	copyOrg := a.CreateOrg("Copy", "", "", "copyCurrent")
	a.SwitchOrg(blank.ID)
	n := len(a.conns().List)
	scope := a.conns().Scope
	a.Mu.Unlock()
	if n != 0 || scope != "side/" {
		t.Fatalf("空白組織:%d 個連線、前綴 %q", n, scope)
	}
	a.Mu.Lock()
	a.SwitchOrg(copyOrg.ID)
	_, ok := ConnFind(a, "b")
	a.Mu.Unlock()
	if !ok {
		t.Fatal("複製組織要帶連線")
	}
	if ConnHasToken(a, c) {
		t.Fatal("複製組織不帶登入(鑰匙圈前綴不同)")
	}
	ConnSetBearer(a, "b", "copy-token")
	if !contains(mem.Accounts(), "copy/b:token") {
		t.Fatalf("帳號名稱:%v", mem.Accounts())
	}
	a.Mu.Lock()
	a.SwitchOrg(DefaultOrgID)
	// 不呼叫 DeleteOrg(會把資料夾搬到真的垃圾桶),直接跑刪除鉤子
	for _, h := range orgDeleteHooks {
		h(a, copyOrg.ID, filepath.Join(DataRoot(), "orgs", copyOrg.ID))
	}
	a.Mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for contains(mem.Accounts(), "copy/b:token") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if contains(mem.Accounts(), "copy/b:token") || !contains(mem.Accounts(), "b:token") {
		t.Fatalf("刪組織只清該組織的鑰匙圈:%v", mem.Accounts())
	}
}

func TestConnectionViewsAndActions(t *testing.T) {
	a := connTestApp(t)
	f := NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		if r.URL.Path == "/user" && r.Header.Get("Authorization") == "Bearer gho_x" {
			return 200, map[string]any{"login": "octo"}
		}
		return 401, map[string]any{"message": "Bad credentials"}
	})
	defer f.Close()
	view := func(name string) map[string]any {
		v, ok := View(a, name)
		if !ok {
			t.Fatalf("沒有 view %s", name)
		}
		return v.(map[string]any)
	}
	if v := view("connections"); len(v["connections"].([]any)) != 2 {
		t.Fatalf("%v", v)
	}
	gh := view("github")
	if gh["connection"] != nil || gh["loggedIn"] != false || gh["settings"].(map[string]any)["connection"] != "github" {
		t.Fatalf("%v", gh)
	}
	if _, err := Action(a, "github_ensure_connection", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "connection_bearer", map[string]any{"id": "github", "token": "gho_x"}); err != nil {
		t.Fatal(err)
	}
	gh = view("github")
	if gh["loggedIn"] != true || gh["state"].(map[string]any)["account"] != "octo" {
		t.Fatalf("%v", gh)
	}
	if _, err := Action(a, "github_add_repo", map[string]any{"repo": "x"}); err == nil {
		t.Fatal("格式錯誤要擋")
	}
	if out, err := Action(a, "connection_upsert", map[string]any{"name": "Payments API", "baseURL": "https://pay.example", "auth": "bearer"}); err != nil || out.(Connection).ID != "payments-api" {
		t.Fatalf("%v %v", out, err)
	}
	if _, err := Action(a, "connection_login", map[string]any{"id": "nope"}); err == nil || err.Error() != "找不到連線 nope" {
		t.Fatal(err)
	}
	if _, err := Action(a, "gcp_settings", map[string]any{"project": " p1 ", "services": "a, b"}); err != nil {
		t.Fatal(err)
	}
	g := view("gcp")
	if g["configured"] != true || g["settings"].(map[string]any)["project"] != "p1" || len(g["settings"].(map[string]any)["services"].([]any)) != 2 {
		t.Fatalf("%v", g)
	}
	if _, err := Action(a, "site_visitors", map[string]any{"range": "2y"}); err == nil {
		t.Fatal("range 要檢查")
	}
	if _, err := Action(a, "connection_delete", map[string]any{"id": "payments-api"}); err != nil {
		t.Fatal(err)
	}
	if v := view("connections"); len(v["connections"].([]any)) != 3 {
		t.Fatalf("%v", v)
	}
}
