package server

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// 唯讀授權請求略過同意畫面(OMIXOR_OAUTH_AUTO_APPROVE):假的 evoleap(httptest),不連真的服務

func newAutoOA(t *testing.T) (*oaHTTP, *fakeEvoleap) {
	o, f := newEvoOA(t)
	o.svc.OAuthAutoApproveOff = false
	return o, f
}

func (o *oaHTTP) authGet(client string, extra ...string) *httptest.ResponseRecorder {
	return o.do("GET", "/oauth/authorize?"+authQuery(client, extra...), "")
}

func TestAutoApproveReadOnlyRedirectsToEvoleap(t *testing.T) {
	o, f := newAutoOA(t)
	client := o.register()
	w := o.authGet(client)
	if w.Code != 303 || strings.Contains(w.Body.String(), "<form") {
		t.Fatal(w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Scheme+"://"+loc.Host != tConsole || loc.Path != "/login/cli" || loc.Query().Get("id") == "" {
		t.Fatal(loc)
	}
	ck := bindCookie(w)
	if ck == nil || !ck.HttpOnly || ck.Path != "/oauth" || !ck.Secure {
		t.Fatalf("%+v", ck)
	}
	if len(f.starts) != 1 || f.starts[0]["name"] != "omixor OAuth: Claude (read-only)" {
		t.Fatal(f.starts)
	}
	// 沒有 cookie / 錯的 cookie 一樣被拒;之後完整走完
	if w := o.callback(loc.Query().Get("id"), "c", nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
	cb := o.callback(loc.Query().Get("id"), "evo-code", ck)
	if cb.Code != 303 || !strings.HasPrefix(cb.Header().Get("Location"), tCallback+"?") {
		t.Fatal(cb.Code, cb.Header().Get("Location"))
	}
	tw := o.exchangeCode(client, cb.Header().Get("Location"), tResource)
	if tw.Code != 200 || jsonBody(t, tw)["scope"] != "mcp:read" {
		t.Fatal(tw.Code, tw.Body.String())
	}
	if w := o.callback(loc.Query().Get("id"), "evo-code", ck); w.Code != 400 {
		t.Fatal("重放要被拒:", w.Code)
	}
	if _, ex, lo := f.counts(); ex != 1 || lo != 1 {
		t.Fatal(ex, lo)
	}
	// 稽核 auto: true
	auto := false
	ents, _ := o.svc.Store.ListAudit(50)
	for _, e := range ents {
		if e.Action == "oauth.authorize" && e.Detail["auto"] == true && e.Detail["via"] == "evoleap" {
			auto = true
		}
	}
	if !auto {
		t.Fatal("稽核要帶 auto:true")
	}
}

func TestAutoApproveNameSanitized(t *testing.T) {
	o, f := newAutoOA(t)
	w := o.do("POST", "/oauth/register", `{"client_name":"Evil\u0007‮  `+strings.Repeat("x", 200)+`","redirect_uris":["`+tCallback+`"],"token_endpoint_auth_method":"none","grant_types":["authorization_code"],"response_types":["code"]}`,
		"Content-Type", "application/json")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := o.authGet(jsonBody(t, w)["client_id"].(string)); w.Code != 303 {
		t.Fatal(w.Code)
	}
	name := f.starts[0]["name"].(string)
	if len([]rune(name)) > 80 || !strings.HasPrefix(name, "omixor OAuth: Evil ") || !strings.HasSuffix(name, " (read-only)") {
		t.Fatal(name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == 0x202e {
			t.Fatalf("控制字元:%q", name)
		}
	}
}

func TestAutoApproveNotUsedWhenWriteRequested(t *testing.T) {
	o, f := newAutoOA(t)
	client := o.register()
	w := o.authGet(client, "resource", tUnified, "scope", rwReq)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="grant"`) || bindCookie(w) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if s, _, _ := f.counts(); s != 0 {
		t.Fatal("不能呼叫 evoleap")
	}
}

func TestAutoApproveSwitchOffRendersPage(t *testing.T) {
	o, f := newAutoOA(t)
	o.svc.OAuthAutoApproveOff = true
	client := o.register()
	w := o.authGet(client)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "用 evoleap 登入並允許") || bindCookie(w) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if s, _, _ := f.counts(); s != 0 {
		t.Fatal("不能呼叫 evoleap")
	}
}

func TestAutoApproveEvoleapUnreachableFallsBack(t *testing.T) {
	o, f := newAutoOA(t)
	client := o.register()
	f.srv.Close()
	w := o.authGet(client)
	if w.Code != 200 || bindCookie(w) != nil || !strings.Contains(w.Body.String(), "改用配對碼") || !strings.Contains(w.Body.String(), `name="csrf"`) ||
		!strings.Contains(w.Body.String(), "<details open>") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAutoApproveNoEvoleapConfigured(t *testing.T) {
	o := newOAHTTP(t)
	client := o.register()
	if w := o.authGet(client); w.Code != 200 || bindCookie(w) != nil {
		t.Fatal(w.Code)
	}
}

func TestAutoApproveInvalidRequestsNeverRedirect(t *testing.T) {
	o, f := newAutoOA(t)
	client := o.register()
	for name, q := range map[string]string{
		"bad redirect_uri": authQuery(client, "redirect_uri", "https://evil.example/cb"),
		"unknown client":   authQuery("nope"),
		"no challenge":     authQuery(client, "code_challenge", ""),
	} {
		w := o.do("GET", "/oauth/authorize?"+q, "")
		loc := w.Header().Get("Location")
		if w.Code == 200 || strings.Contains(loc, "/login/cli") || bindCookie(w) != nil {
			t.Fatal(name, w.Code, loc)
		}
		if name != "no challenge" && w.Code == 303 {
			t.Fatal(name, "不可信的請求不能導向:", loc)
		}
	}
	if s, _, _ := f.counts(); s != 0 {
		t.Fatal("不能呼叫 evoleap")
	}
}
