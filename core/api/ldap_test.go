package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"omixor-core/ldapauth"
	"omixor-core/service"
)

type ldapDir struct{}

func (ldapDir) Authenticate(u, p string) (*ldapauth.Identity, error) {
	if u == "amy" && p == "pw" {
		return &ldapauth.Identity{ID: "amy", Name: "Amy", Email: "amy@corp.test"}, nil
	}
	return nil, ldapauth.ErrInvalidCredentials
}

func postJSON(t *testing.T, url, body, bearer string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestLDAPLoginHTTP(t *testing.T) {
	e := newEnv(t)

	// 沒啟用:404,不是 500、也不是空回應
	if code, out := postJSON(t, e.srv.URL+"/api/auth/ldap", `{"username":"amy","password":"pw"}`, ""); code != 404 || !strings.Contains(out["error"].(string), "沒有啟用 LDAP") {
		t.Fatalf("沒啟用:%d %v", code, out)
	}

	e.svc.LDAP = ldapDir{}
	// 密碼錯:401
	if code, _ := postJSON(t, e.srv.URL+"/api/auth/ldap", `{"username":"amy","password":"nope"}`, ""); code != 401 {
		t.Fatalf("密碼錯:%d", code)
	}
	// 格式錯:400
	if code, _ := postJSON(t, e.srv.URL+"/api/auth/ldap", `not json`, ""); code != 400 {
		t.Fatalf("格式錯:%d", code)
	}
	// 成功:回傳格式同 /api/auth/evoleap,token 能打 /api/auth/me
	code, out := postJSON(t, e.srv.URL+"/api/auth/ldap", `{"username":"Amy","password":"pw","device":"CLI"}`, "")
	if code != 200 {
		t.Fatalf("登入:%d %v", code, out)
	}
	tok, _ := out["token"].(string)
	if !strings.HasPrefix(tok, service.TokenPrefix) || out["tokenId"] == nil || out["user"] == nil {
		t.Fatalf("回傳:%v", out)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("/api/auth/me:%v %v", resp, err)
	}
	resp.Body.Close()
}
