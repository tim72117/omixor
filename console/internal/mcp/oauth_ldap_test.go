package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"

	"omixor-core/ldapauth"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// console 的 MCP 入口用 LDAP 帳號授權端到端(假 LDAP):認證設定沒啟用就沒有;啟用後登入 → 確認頁 → token,
// 寫入署名是 ldap:<帳號> 的本人,RequiredGroup 照擋

var ldapTxn = regexp.MustCompile(`name="txn" value="([^"]+)"`)

func TestConsoleLDAPOAuth(t *testing.T) {
	f := authtest.Start(t)
	st := storetest.NewWithOrg(t, "o")
	svc := &service.Service{Store: st, OAuthRegPerIP: 1000, OAuthRegTotal: 1000, LoginPerIP: 1000, LoginTotal: 1000}
	app := iaApp(t)
	au := auth.New(&auth.MemorySecrets{PW: "svc-pw"})
	cfg := oauthConfig(t, oPublic)
	r := &oauthRig{t: t, h: newOAuthHandler(app, svc, cfg, au.LDAPSettings), app: app, svc: svc}

	w := r.do("POST", "/oauth/register", `{"client_name":"Claude","redirect_uris":["`+oCallback+`"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`, "Content-Type", "application/json")
	client := r.json(w)["client_id"].(string)
	sum := sha256.Sum256([]byte(oVerifier))
	query := url.Values{"client_id": {client}, "redirect_uri": {oCallback}, "response_type": {"code"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "state": {"st"}, "resource": {oPublic + "/mcp"}, "scope": {"mcp:read mcp:write"}}
	cookie := ""
	page := func() string {
		w := r.do("GET", "/oauth/authorize?"+query.Encode(), "")
		for _, c := range w.Result().Cookies() {
			cookie = c.Name + "=" + c.Value
		}
		return w.Body.String()
	}
	if strings.Contains(page(), "ldap_password") {
		t.Fatal("認證設定沒啟用 LDAP 時不該出現")
	}
	if err := au.Save(auth.Config{Mode: "ldap", Admins: []string{"alice"}, EmailDomain: "corp.test",
		LDAP: auth.LDAP{URL: "ldap://" + f.Addr, BindDN: authtest.SvcDN, BaseDN: authtest.Base, RequiredGroup: authtest.UsersDN}}, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = au.Save(auth.Config{Mode: "off"}, nil) })
	post := func(path string, v url.Values) (int, string, string) {
		w := r.do("POST", path, v.Encode(), "Content-Type", "application/x-www-form-urlencoded", "Cookie", cookie, "Origin", "null", "Sec-Fetch-Site", "same-origin")
		return w.Code, w.Body.String(), w.Header().Get("Location")
	}
	login := func(user, pw string) (int, string) {
		m := oCSRF.FindStringSubmatch(page())
		v := url.Values{}
		for k, x := range query {
			v[k] = x
		}
		v.Set("csrf", m[1])
		v.Set("ldap_username", user)
		v.Set("ldap_password", pw)
		v.Set("grant", "write")
		code, body, _ := post("/oauth/authorize", v)
		return code, body
	}
	// carol 密碼對但不在允許的群組
	if code, body := login("carol", "carol-pw"); code != 403 || !strings.Contains(body, "不在允許使用") {
		t.Fatalf("carol:%d %s", code, body)
	}
	// bob 沒有 mail,用網域補;是一般使用者
	code, body := login("bob", "bob-pw")
	m := ldapTxn.FindStringSubmatch(body)
	if code != 200 || m == nil || !strings.Contains(body, "bob@corp.test") {
		t.Fatalf("bob:%d %s", code, body)
	}
	bob, err := st.UserByEvoleapID("ldap:bob")
	if err != nil || bob.Role != "member" || core.IsLocalUser(*bob) {
		t.Fatalf("bob 的核心使用者:%v %+v", err, bob)
	}
	if err := st.SaveMember(model.Member{Org: "o", UserID: bob.ID, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	code, body, loc := post("/oauth/ldap/confirm", url.Values{"txn": {m[1]}, "action": {"allow"}})
	back, _ := url.Parse(loc)
	if code != 303 || back.Query().Get("code") == "" {
		t.Fatalf("確認:%d %s %s", code, loc, body)
	}
	w = r.do("POST", "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {back.Query().Get("code")},
		"redirect_uri": {oCallback}, "code_verifier": {oVerifier}, "resource": {oPublic + "/mcp"}}.Encode(), "Content-Type", "application/x-www-form-urlencoded")
	access, _ := r.json(w)["access_token"].(string)
	if access == "" {
		t.Fatal(w.Body.String())
	}
	res := r.rpc(access, "tools/call", map[string]any{"name": "issue_create", "arguments": map[string]any{"org": "o", "title": "LDAP 授權寫的"}})
	if b, _ := json.Marshal(res); !strings.Contains(string(b), "ISS-1") {
		t.Fatalf("issue_create:%s", b)
	}
	res = r.rpc(access, "tools/call", map[string]any{"name": "issues", "arguments": map[string]any{"org": "o", "id": "ISS-1"}})
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "bob@corp.test") {
		t.Fatalf("署名要是本人:%s", b)
	}
	// 執行者授權走 LDAP:確認頁寫明是任務執行者,換到的 scope 只有 mcp:worker
	query.Set("resource", oPublic+"/mcp/worker/o")
	query.Set("scope", "mcp:worker")
	code, body = login("bob", "bob-pw")
	if m = ldapTxn.FindStringSubmatch(body); code != 200 || m == nil || !strings.Contains(body, "任務執行者") {
		t.Fatalf("worker 確認頁:%d %s", code, body)
	}
	_, _, loc = post("/oauth/ldap/confirm", url.Values{"txn": {m[1]}, "action": {"allow"}})
	back, _ = url.Parse(loc)
	w = r.do("POST", "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {back.Query().Get("code")},
		"redirect_uri": {oCallback}, "code_verifier": {oVerifier}, "resource": {oPublic + "/mcp/worker/o"}}.Encode(), "Content-Type", "application/x-www-form-urlencoded")
	if got := r.json(w)["scope"]; got != "mcp:worker" {
		t.Fatalf("worker scope:%v %s", got, w.Body.String())
	}
	query.Set("resource", oPublic+"/mcp")
	query.Set("scope", "mcp:read mcp:write")
	if code, body := login("alice", "alice-pw"); code != 200 || !strings.Contains(body, "alice@example.com") {
		t.Fatalf("alice:%d %s", code, body)
	}
	// 網頁管理員名單不把人升成核心的伺服器管理員(降不下來)
	if u, err := st.UserByEvoleapID("ldap:alice"); err != nil || u.Role != "member" {
		t.Fatalf("名單上的人在核心仍是一般使用者:%v %+v", err, u)
	}
}

// console 的目錄設定:服務帳號搜尋可以重新確認帳號(含 RequiredGroup),直接組 DN 的查不了
func TestConsoleLDAPRecheck(t *testing.T) {
	f := authtest.Start(t)
	iaApp(t)
	au := auth.New(&auth.MemorySecrets{PW: "svc-pw"})
	cfg := auth.Config{Mode: "ldap", Admins: []string{"alice"}, LDAP: auth.LDAP{URL: "ldap://" + f.Addr, BindDN: authtest.SvcDN, BaseDN: authtest.Base, RequiredGroup: authtest.UsersDN}}
	if err := au.Save(cfg, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = au.Save(auth.Config{Mode: "off"}, nil) })
	check := func() service.DirectoryRechecker {
		set, ok := au.LDAPSettings()
		if !ok || len(set.Admins) != 0 {
			t.Fatalf("不帶管理員名單:%+v", set)
		}
		return set.Dir.(service.DirectoryRechecker)
	}
	r := check()
	if !r.CanRecheck() {
		t.Fatal("服務帳號搜尋要能重查")
	}
	if _, err := r.Recheck("bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Recheck("carol"); !errors.Is(err, ldapauth.ErrNotInGroup) {
		t.Fatalf("carol:%v", err)
	}
	if _, err := r.Recheck("nobody"); !errors.Is(err, ldapauth.ErrNoEntry) {
		t.Fatalf("nobody:%v", err)
	}
	cfg.LDAP.UserDNTemplate = "uid=%s,ou=people," + authtest.Base
	if err := au.Save(cfg, nil); err != nil {
		t.Fatal(err)
	}
	if check().CanRecheck() {
		t.Fatal("範本模式查不了")
	}
}
