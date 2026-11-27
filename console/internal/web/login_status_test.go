package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/auth/authtest"
	"omixor-web/internal/core"
)

func rawLogin(t *testing.T, base, user, pw string) (int, http.Header, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"username": user, "password": pw})
	resp, err := http.Post(base+"/api/auth/web/login", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, resp.Header, out
}

// 前面的測試可能留下啟用中的 LDAP 設定
func resetAuthJSON(t *testing.T) {
	t.Helper()
	os.Remove(core.DataRoot() + "/auth.json")
	t.Cleanup(func() { os.Remove(core.DataRoot() + "/auth.json") })
}

// 登入失敗的狀態碼:帳號不存在與密碼錯誤是同一個 401、同一句話;被鎖 429 帶 Retry-After;LDAP 連不上 503(固定句子,不含位址)
func TestLoginStatusCodes(t *testing.T) {
	captureWebLog(t)
	resetAuthJSON(t)
	f := authtest.Start(t)
	au := auth.New(&auth.MemorySecrets{})
	c, app := newServer(t, au)
	if code, out := c.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": "svc-pw", "testUsername": "alice", "testPassword": "alice-pw"}, nil); code != 200 {
		t.Fatalf("設定:%d %v", code, out)
	}
	joinOrg(t, app, au, "bob")
	// 帳號不存在 vs 密碼錯誤:同一個 401、同一句話
	c1, _, o1 := rawLogin(t, c.srv.URL, "nobody-here", "x")
	c2, _, o2 := rawLogin(t, c.srv.URL, "bob", "wrong-password")
	if c1 != 401 || c2 != 401 || o1["error"] != o2["error"] || o1["error"] != "帳號或密碼錯誤" {
		t.Fatalf("要同一個 401 與同一句話:%d %v / %d %v", c1, o1, c2, o2)
	}
	// 鎖定:同一帳號同一來源 5 次失敗後 429,帶 Retry-After(正整數秒)
	var code int
	var hdr http.Header
	var out map[string]any
	for i := 0; i < 6; i++ {
		code, hdr, out = rawLogin(t, c.srv.URL, "carol", "wrong")
	}
	if code != 429 || hdr.Get("Retry-After") == "" || hdr.Get("Retry-After") == "0" {
		t.Fatalf("要 429 與 Retry-After:%d %q %v", code, hdr.Get("Retry-After"), out)
	}
	if !strings.Contains(out["error"].(string), "太多次") {
		t.Fatalf("%v", out)
	}
	// 別的帳號不受影響
	if code, _, _ := rawLogin(t, c.srv.URL, "bob", "bob-pw"); code != 200 {
		t.Fatalf("別的帳號要能登入:%d", code)
	}
}

// LDAP 連不上:503,固定句子,不含位址或 dial tcp
func TestLoginLDAPDownIs503(t *testing.T) {
	logs := captureWebLog(t)
	resetAuthJSON(t)
	f, stop, err := authtest.StartServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	au := auth.New(&auth.MemorySecrets{})
	c, _ := newServer(t, au)
	if code, out := c.do("POST", "/api/auth/config", map[string]any{"config": ldapCfg(f), "bindPassword": "svc-pw", "testUsername": "alice", "testPassword": "alice-pw"}, nil); code != 200 {
		t.Fatalf("設定:%d %v", code, out)
	}
	stop() // 之後 LDAP 伺服器沒了
	code, _, out := rawLogin(t, c.srv.URL, "bob", "bob-pw")
	msg, _ := out["error"].(string)
	if code != 503 || msg == "" || strings.Contains(msg, "dial") || strings.Contains(msg, "127.0.0.1") {
		t.Fatalf("要 503 與固定句子:%d %v", code, out)
	}
	if !strings.Contains(logs.String(), "LDAP") {
		t.Errorf("原文要進 log:%s", logs.String())
	}
}

// authLoginStatus 依型別:鎖 429、StatusError 用它自己的、BadLogin 401
func TestAuthLoginStatusByType(t *testing.T) {
	if got := authLoginStatus(auth.LockedError{RetryAfter: 30}); got != 429 {
		t.Errorf("鎖定要 429:%d", got)
	}
	if got := authLoginStatus(core.StatusError{Status: 503, Message: "x"}); got != 503 {
		t.Errorf("503:%d", got)
	}
	if got := authLoginStatus(auth.ErrBadLogin); got != 401 {
		t.Errorf("401:%d", got)
	}
}
