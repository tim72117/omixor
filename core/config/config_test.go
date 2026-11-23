package config

import (
	"fmt"
	"strings"
	"testing"
)

func mapEnv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadLegacyAndDefaults(t *testing.T) {
	c := LoadFrom(mapEnv(map[string]string{"OPS_WEB_PORT": "6000"}))
	if c.Int(WebPort) != 6000 || !c.IsSet(WebPort) {
		t.Fatal("新名稱沒設要讀舊名稱", c.Int(WebPort))
	}
	c = LoadFrom(mapEnv(map[string]string{"OPS_WEB_PORT": "6000", "OMIXOR_WEB_PORT": "7000"}))
	if c.Int(WebPort) != 7000 {
		t.Fatal("新名稱優先")
	}
	if len(c.warn) != 1 || strings.Contains(c.warn[0], "6000") {
		t.Fatal("新舊名稱值不同要提醒(且不印值)", c.warn)
	}
	c = LoadFrom(mapEnv(nil))
	if c.Int(WebPort) != 5191 || c.Int(MCPPort) != 3765 || c.Int(TokenDays) != 90 || c.Str(MCPAddr) != "127.0.0.1" || c.Str(EvoleapURL) != "https://evoleap.io" {
		t.Fatal("預設值不對")
	}
	if c.Bool(NoTimers) || c.IsSet(NoTimers) || c.Str(DatabaseURL) != "" {
		t.Fatal("沒設的要是零值")
	}
}

func TestTypedAccessorsLenient(t *testing.T) {
	c := LoadFrom(mapEnv(map[string]string{"OMIXOR_WEB_PORT": "abc", "OMIXOR_MCP_PORT": "70000", "OMIXOR_NO_TIMERS": "banana", "OMIXOR_AGENT_CRED_HOURS": "1.5"}))
	if c.Int(WebPort) != 5191 || c.Int(MCPPort) != 3765 {
		t.Fatal("型別錯或超出範圍要回預設(同舊行為)")
	}
	if c.Bool(NoTimers) {
		t.Fatal("布林型別錯當作沒設")
	}
	if c.Float(AgentCredHours) != 1.5 {
		t.Fatal("小數")
	}
	for in, want := range map[string]bool{"1": true, "TRUE": true, "yes": true, "on": true, "0": false, "off": false, "no": false} {
		if got := LoadFrom(mapEnv(map[string]string{"OMIXOR_NO_TIMERS": in})).Bool(NoTimers); got != want {
			t.Errorf("Bool(%q)=%v", in, got)
		}
	}
}

func TestDurationKind(t *testing.T) {
	v := &Var{Name: "X", Kind: Duration, Default: "5s"}
	byName["X"] = v // 臨時登記(Config 只認已定義的設定)
	defer delete(byName, "X")
	c := &Config{raw: map[string]string{"X": "2m"}}
	if c.Duration(v) != 120e9 {
		t.Fatal("2m")
	}
	if v.check("zzz") == nil || v.check("1m") != nil {
		t.Fatal("Duration 檢查")
	}
	if (&Config{raw: map[string]string{}}).Duration(v) != 5e9 {
		t.Fatal("預設")
	}
}

func TestValidate(t *testing.T) {
	err := LoadFrom(mapEnv(map[string]string{
		"OMIXOR_DATABASE_URL": "mysql://u:pw-secret-123@h/db", "OMIXOR_TOKEN_DAYS": "x", "PORT": "99999",
		"OMIXOR_PUBLIC_URL": "omixor.io", "OMIXOR_LDAP_BIND_DN": "cn=x",
	})).Validate(Server)
	if err == nil {
		t.Fatal("應該有錯")
	}
	msg := err.Error()
	for _, want := range []string{"OMIXOR_DATABASE_URL", "OMIXOR_TOKEN_DAYS 要是整數", "PORT 要在 1 到 65535", "OMIXOR_PUBLIC_URL 要是網址", "沒設 OMIXOR_LDAP_URL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("錯誤訊息缺 %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "pw-secret-123") {
		t.Fatal("錯誤訊息洩漏機密")
	}
	// 必填
	err = LoadFrom(mapEnv(nil)).Validate(Server)
	if err == nil || !strings.Contains(err.Error(), "OMIXOR_DATABASE_URL 沒設(必填)") {
		t.Fatal("server 要必填資料庫網址", err)
	}
	if err := LoadFrom(mapEnv(nil)).Validate(Web); err != nil {
		t.Fatal("web 沒有必填", err)
	}
	// 只驗證該程式用到的:local 不管 PORT 的錯
	if err := LoadFrom(mapEnv(map[string]string{"PORT": "x"})).Validate(Local); err != nil {
		t.Fatal(err)
	}
	// 互斥
	err = LoadFrom(mapEnv(map[string]string{"OMIXOR_WEB_ADDR": "0.0.0.0:5191", "OMIXOR_WEB_PORT": "6000"})).Validate(Web)
	if err == nil || !strings.Contains(err.Error(), "連接埠不一樣") {
		t.Fatal("ADDR 與 PORT 衝突", err)
	}
	if err := LoadFrom(mapEnv(map[string]string{"OMIXOR_WEB_ADDR": "0.0.0.0:5191", "OMIXOR_WEB_PORT": "5191"})).Validate(Web); err != nil {
		t.Fatal(err)
	}
	if err := LoadFrom(mapEnv(map[string]string{"OMIXOR_AUTH": "on"})).Validate(Web); err != nil {
		t.Fatal("OMIXOR_AUTH 不認得的值只警告、不失敗", err)
	}
	good := map[string]string{"OMIXOR_DATABASE_URL": "postgres://u:p@h/db", "OMIXOR_LDAP_URL": "ldaps://ad:636", "OMIXOR_LDAP_BIND_DN": "cn=x"}
	if err := LoadFrom(mapEnv(good)).Validate(Server); err != nil {
		t.Fatal(err)
	}
}

func TestSecretsMasked(t *testing.T) {
	const pw, key, tok = "pw-secret-123", "key-secret-456", "tok-secret-789"
	c := LoadFrom(mapEnv(map[string]string{
		"OMIXOR_DATABASE_URL": "postgres://omixor:" + pw + "@db/omixor", "OMIXOR_SECRET_KEY": key, "OMIXOR_API_TOKEN": tok,
		"OMIXOR_LDAP_BIND_PASSWORD": "ldap-secret-000", "OMIXOR_LOCAL_TOKEN": "omx_local-secret", "OMIXOR_SERVER_TOKEN": "srv-secret-111",
		"OMIXOR_WEB_PORT": "5191",
	}))
	outs := []string{c.String(), c.GoString(), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprintf("%s", c), c.LogValue().String()}
	for _, o := range outs {
		for _, s := range []string{pw, key, tok, "ldap-secret-000", "omx_local-secret", "srv-secret-111"} {
			if strings.Contains(o, s) {
				t.Fatalf("輸出洩漏機密 %q:\n%s", s, o)
			}
		}
	}
	if !strings.Contains(c.String(), "postgres://omixor:***@db/omixor") || !strings.Contains(c.String(), "OMIXOR_WEB_PORT=5191") {
		t.Fatal("網址只遮密碼、非機密照印\n" + c.String())
	}
	// Redact:任何文字裡的機密都被遮掉
	got := c.Redact("連線失敗 postgres://omixor:" + pw + "@db/omixor key=" + key + " 密碼 " + pw)
	if strings.Contains(got, pw) || strings.Contains(got, key) {
		t.Fatal("Redact 沒遮乾淨", got)
	}
	// 型別錯誤的機密不能出現在驗證錯誤裡
	bad := LoadFrom(mapEnv(map[string]string{"OMIXOR_DATABASE_URL": "http://u:" + pw + "@h/db"}))
	if err := bad.Validate(Server); err == nil || strings.Contains(err.Error(), pw) {
		t.Fatal("驗證錯誤要有、且不含機密", err)
	}
	// 取值仍是原值
	if c.Str(SecretKey) != key || c.Str(DatabaseURL) == "" {
		t.Fatal("取值不能被遮蔽")
	}
	if !strings.Contains(RedactURL("postgres://a:b@c/d"), "***") || strings.Contains(RedactURL("postgres://a:b@c/d"), ":b@") {
		t.Fatal("RedactURL")
	}
}

func TestWithAndGetenv(t *testing.T) {
	c := LoadFrom(mapEnv(map[string]string{"OPS_LDAP_BIND_PASSWORD": "x-secret"}))
	if c.Getenv("OMIXOR_LDAP_BIND_PASSWORD") != "x-secret" || c.Getenv("OMIXOR_LDAP_TIMEOUT_SECONDS") != "10" {
		t.Fatal("Getenv 含舊名稱與預設")
	}
	d := c.With(PublicURL, "http://127.0.0.1:1")
	if d.Str(PublicURL) != "http://127.0.0.1:1" || c.IsSet(PublicURL) {
		t.Fatal("With 不能改到原本的")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("未定義的名稱要 panic")
		}
	}()
	c.Getenv("NOPE")
}

func TestDefinitions(t *testing.T) {
	for _, v := range All {
		if v.Default != "" {
			if err := v.check(v.Default); err != nil {
				t.Errorf("%s 的預設值自己就不合法:%v", v.Name, err)
			}
		}
		if v.Secret && v.Default != "" {
			t.Errorf("%s 是機密,不該有預設值", v.Name)
		}
		for _, p := range v.RequiredFor {
			if !v.usedBy(p) {
				t.Errorf("%s 必填的程式 %s 不在 Programs 裡", v.Name, p)
			}
		}
		if !strings.HasPrefix(v.Name, "OMIXOR_") && v.Name != "PORT" && v.Name != "EVOLEAP_URL" && v.Name != "K_SERVICE" && v.Name != "PGCTLTIMEOUT" {
			t.Errorf("%s 沒有 OMIXOR_ 前綴(例外要在這裡列出)", v.Name)
		}
		for _, l := range v.Legacy {
			if !strings.HasPrefix(l, "OPS_") {
				t.Errorf("%s 的舊名稱 %s 不是 OPS_ 開頭", v.Name, l)
			}
		}
	}
	for _, s := range []string{"OMIXOR_DATABASE_URL", "OMIXOR_SECRET_KEY", "OMIXOR_SERVER_TOKEN", "OMIXOR_API_TOKEN", "OMIXOR_LDAP_BIND_PASSWORD", "OMIXOR_LOCAL_TOKEN"} {
		if v, _ := Lookup(s); v == nil || !v.Secret {
			t.Errorf("%s 要標機密", s)
		}
	}
	// 測試/開發用變數不能同時是正式設定
	for _, d := range DevVars {
		if _, ok := Lookup(d.Name); ok {
			t.Errorf("%s 同時在 DevVars 與正式設定", d.Name)
		}
	}
}

func TestUsage(t *testing.T) {
	u := Usage(Local)
	for _, want := range []string{"OMIXOR_DATA_DIR", "OMIXOR_LOCAL_TOKEN", "機密", "OMIXOR_DATABASE_URL", "舊名稱:OPS_DATABASE_URL", "docs/configuration.md"} {
		if !strings.Contains(u, want) {
			t.Errorf("Usage(Local) 缺 %q", want)
		}
	}
	if strings.Contains(u, "OMIXOR_WEB_PORT") {
		t.Error("Usage(Local) 不該列 web 的變數")
	}
}
