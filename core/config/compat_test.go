package config

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// 與舊版行為相容的測試:資料庫連線兩種形式、旗標與安全閘門語意、OMIXOR_AUTH 寬鬆、LDAP 逾時上限、載入範圍。

func TestDatabaseURLBothForms(t *testing.T) {
	ok := []string{
		"postgres://u:p@h/db",
		"postgresql://u:p@h1:5432,h2:5433/db?target_session_attrs=read-write",
		"postgres://u:p@/db?host=/cloudsql/proj:region:inst",
		"POSTGRES://u:p@h/db",
		"host=/cloudsql/proj:region:inst user=omixor password=secret dbname=omixor",
		"host=h1,h2 port=5432,5433 user=u password='a b' dbname=d sslmode=disable",
	}
	for _, s := range ok {
		if err := DatabaseURL.check(s); err != nil {
			t.Errorf("%q 應該通過:%v", s, err)
		}
	}
	for _, s := range []string{"mysql://u:p@h/db", "http://h/db", "localhost", "omixor"} {
		if err := DatabaseURL.check(s); err == nil {
			t.Errorf("%q 應該被拒", s)
		}
	}
	// 完整載入:key=value 形式不會讓 server 啟動驗證失敗
	c := LoadFrom(mapEnv(map[string]string{"OMIXOR_DATABASE_URL": "host=/cloudsql/p:r:i user=u password=pw-kv-123 dbname=d"}))
	if err := c.Validate(Server); err != nil {
		t.Fatal(err)
	}
}

func TestRedactURLForms(t *testing.T) {
	cases := map[string]string{
		"postgres://u:PW1@h/db":                                   "postgres://u:***@h/db",
		"postgres://u:p%40ss@h1:1,h2:2/db?sslmode=disable":        "postgres://u:***@h1:1,h2:2/db?sslmode=disable",
		"postgres://h/db?user=u&password=QUERYPW&sslmode=disable": "postgres://h/db?user=u&password=***&sslmode=disable",
		"postgres://h/db?sslpassword=SSLPW":                       "postgres://h/db?sslpassword=***",
		"postgres://h/db?sslkey=KEYPW&x=1":                        "postgres://h/db?sslkey=***&x=1",
		"postgres://h/db?PASSWORD=UP":                             "postgres://h/db?PASSWORD=***",
		"postgres://u@h/db":                                       "postgres://u@h/db",
		"postgres://u:a@b@h/db":                                   "postgres://u:***@h/db",
		"host=/s user=u password=PW dbname=d":                     "host=/s user=u password=*** dbname=d",
		`host=h password = 'a b\'c' dbname=d`:                     "host=h password = '***' dbname=d",
		"password=PW":                                             "password=***",
		"host=h sslpassword=SP sslkey=/k":                         "host=h sslpassword=*** sslkey=***",
		"host=h passwordx=keep dbname=d":                          "host=h passwordx=keep dbname=d",
		"postgres://u:p@h/db#password=frag":                       "postgres://u:***@h/db#password=frag",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestConfigNeverPrintsDSNSecrets(t *testing.T) {
	for _, dsn := range []string{
		"postgres://h/db?user=u&password=QUERYPW-1&sslmode=disable",
		"postgres://h/db?sslpassword=SSLPW-2",
		"postgres://u:USERINFOPW-3@h/db",
		"host=/cloudsql/p:r:i user=u password=KVPW-4 dbname=d",
		"host=h password='QUOTED PW-5' dbname=d",
	} {
		c := LoadFrom(mapEnv(map[string]string{"OMIXOR_DATABASE_URL": dsn}))
		var secret string
		for _, k := range []string{"QUERYPW-1", "SSLPW-2", "USERINFOPW-3", "KVPW-4", "QUOTED PW-5"} {
			if strings.Contains(dsn, k) {
				secret = k
			}
		}
		outs := []string{
			c.String(), c.GoString(), c.LogValue().String(),
			fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprintf("%s", c),
			fmt.Sprintf("%v", *c), fmt.Sprintf("%+v", *c), fmt.Sprintf("%#v", *c), fmt.Sprint(*c),
			DatabaseURL.Display(dsn),
			c.Redact("連線失敗:" + dsn),
			c.Redact("dial error for " + secret),
		}
		var b strings.Builder
		slog.New(slog.NewTextHandler(&b, nil)).Info("cfg", "config", c, "value", *c)
		outs = append(outs, b.String())
		for i, o := range outs {
			if strings.Contains(o, secret) {
				t.Errorf("dsn %q 輸出 #%d 洩漏 %q:\n%s", dsn, i, secret, o)
			}
		}
	}
}

func TestValuePrintHidesRaw(t *testing.T) {
	c := LoadFrom(mapEnv(map[string]string{"OMIXOR_SECRET_KEY": "key-secret-456", "OMIXOR_WEB_PORT": "5191"}))
	for _, o := range []string{fmt.Sprintf("%v", *c), fmt.Sprintf("%+v", *c), fmt.Sprintf("%#v", *c), fmt.Sprintf("%d", *c)} {
		if strings.Contains(o, "key-secret-456") || strings.Contains(o, "raw") {
			t.Errorf("以值印出洩漏或列出內部欄位:%s", o)
		}
	}
	if !strings.Contains(fmt.Sprintf("%v", *c), "OMIXOR_WEB_PORT=5191") {
		t.Error("非機密要照印")
	}
}

func TestRedactJSONAndShortSecrets(t *testing.T) {
	servers := `[{"name":"a","url":"https://x","token":"tok-AAAA-1"},{"name":"b","auth":{"password":"pw-BBBB-2"},"secret":"sec-CCCC-3"}]`
	c := LoadFrom(mapEnv(map[string]string{"OMIXOR_SERVERS": servers}))
	for _, leak := range []string{"tok-AAAA-1", "pw-BBBB-2", "sec-CCCC-3"} {
		if got := c.Redact("401 Unauthorized: bad token " + leak + " rejected"); strings.Contains(got, leak) {
			t.Errorf("單獨一個 token 沒被遮:%s", got)
		}
	}
	if got := c.Redact("連 https://x 失敗(name a)"); got != "連 https://x 失敗(name a)" {
		t.Errorf("非機密欄位不該被遮:%s", got)
	}
	if strings.Contains(c.String(), "tok-AAAA-1") {
		t.Error("String 洩漏")
	}
	// 短機密:整字才換
	c = LoadFrom(mapEnv(map[string]string{"OMIXOR_API_TOKEN": "ab", "OMIXOR_SECRET_KEY": "z"}))
	if got := c.Redact("bad ab token; ab, label cab z zebra z"); got != "bad *** token; ***, label cab *** zebra ***" {
		t.Errorf("短機密整字遮蔽不對:%s", got)
	}
	// 沒有機密時不動
	if got := LoadFrom(mapEnv(nil)).Redact("hello"); got != "hello" {
		t.Error(got)
	}
}

func TestNoBrowserIsPresenceFlag(t *testing.T) {
	for _, in := range []string{"1", "0", "false", "no", "banana", "true"} {
		c := LoadFrom(mapEnv(map[string]string{"OMIXOR_NO_BROWSER": in}))
		if !c.Bool(NoBrowser) {
			t.Errorf("OMIXOR_NO_BROWSER=%s 應該算「不開瀏覽器」(有設就不開)", in)
		}
		if err := c.Validate(Web); err != nil {
			t.Errorf("OMIXOR_NO_BROWSER=%s 不該驗證失敗:%v", in, err)
		}
	}
	if LoadFrom(mapEnv(nil)).Bool(NoBrowser) {
		t.Error("沒設要開瀏覽器")
	}
	if !LoadFrom(mapEnv(map[string]string{"OPS_NO_BROWSER": "1"})).Bool(NoBrowser) {
		t.Error("舊名稱")
	}
}

func TestLocalOnlyPublishOnlyExactOne(t *testing.T) {
	for in, want := range map[string]bool{"1": true, "true": false, "yes": false, "on": false, "0": false, " 1": false, "11": false, "": false} {
		c := LoadFrom(mapEnv(map[string]string{"OMIXOR_WEB_LOCAL_ONLY_PUBLISH": in}))
		if got := c.Bool(WebLocalOnlyPublish); got != want {
			t.Errorf("OMIXOR_WEB_LOCAL_ONLY_PUBLISH=%q → %v,要是 %v", in, got, want)
		}
		if err := c.Validate(Web); err != nil {
			t.Errorf("%q 不該驗證失敗:%v", in, err)
		}
	}
	if !LoadFrom(mapEnv(map[string]string{"OPS_WEB_LOCAL_ONLY_PUBLISH": "1"})).Bool(WebLocalOnlyPublish) {
		t.Error("舊名稱")
	}
}

func TestAuthLenient(t *testing.T) {
	for in, want := range map[string]string{"off": "off", "OFF": "off", "Off": "off", " off ": "off", "on": "on", "ON": "on", "x": "x"} {
		c := LoadFrom(mapEnv(map[string]string{"OMIXOR_AUTH": in}))
		if err := c.Validate(Web); err != nil {
			t.Errorf("OMIXOR_AUTH=%q 不該失敗:%v", in, err)
		}
		if got := c.Str(Auth); got != want {
			t.Errorf("OMIXOR_AUTH=%q → %q,要是 %q", in, got, want)
		}
	}
	c := LoadFrom(mapEnv(map[string]string{"OMIXOR_AUTH": "ON"}))
	if w := c.softWarnings(Web); len(w) != 1 || !strings.Contains(w[0], "OMIXOR_AUTH") {
		t.Errorf("認不得的值要警告:%v", w)
	}
	if w := LoadFrom(mapEnv(map[string]string{"OMIXOR_AUTH": "OFF"})).softWarnings(Web); len(w) != 0 {
		t.Errorf("OFF 不該警告:%v", w)
	}
}

func TestLDAPTimeoutRange(t *testing.T) {
	for in, ok := range map[string]bool{"1": true, "3600": true, "7200": true, "86400": true, "0": false, "-1": false, "abc": false, "1.5": false} {
		err := LoadFrom(mapEnv(map[string]string{"OMIXOR_LDAP_URL": "ldaps://h", "OMIXOR_LDAP_BIND_DN": "cn=x", "OMIXOR_LDAP_TIMEOUT_SECONDS": in})).Validate(Server)
		// 這裡只看 TIMEOUT 本身;沒設資料庫網址的必填錯誤略過
		bad := err != nil && strings.Contains(err.Error(), "OMIXOR_LDAP_TIMEOUT_SECONDS")
		if bad == ok {
			t.Errorf("OMIXOR_LDAP_TIMEOUT_SECONDS=%s 驗證結果不對(ok=%v):%v", in, ok, err)
		}
	}
	if got := LoadFrom(mapEnv(map[string]string{"OMIXOR_LDAP_TIMEOUT_SECONDS": "7200"})).Int(LDAPTimeoutSeconds); got != 7200 {
		t.Errorf("7200 → %d", got)
	}
}

func TestLoadReadsAllButValidatesProgram(t *testing.T) {
	// 本程式(local)沒用到的設定:不驗證(壞值不擋啟動),但取得到真實的值,不是靜默的預設值
	c := LoadFrom(mapEnv(map[string]string{"OMIXOR_MCP_URL": "http://x/mcp", "OMIXOR_NO_TIMERS": "1", "PORT": "x"}))
	if c.Str(MCPURL) != "http://x/mcp" || !c.Bool(NoTimers) {
		t.Fatal("LoadFrom 要讀全部設定")
	}
	if err := c.Validate(Local); err != nil {
		t.Fatal("只驗證該程式用到的:", err)
	}
}

func TestUndefinedVarPanics(t *testing.T) {
	c := LoadFrom(mapEnv(nil))
	stray := &Var{Name: "OMIXOR_TYPO", Kind: String}
	for name, f := range map[string]func(){
		"Str": func() { c.Str(stray) }, "Int": func() { c.Int(stray) }, "Bool": func() { c.Bool(stray) },
		"IsSet": func() { c.IsSet(stray) }, "Float": func() { c.Float(stray) }, "Duration": func() { c.Duration(stray) },
		"Getenv": func() { c.Getenv("OMIXOR_TYPO") }, "ProcessEnv": func() { ProcessEnv("OMIXOR_TYPO") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s:未定義的設定要 panic", name)
				}
			}()
			f()
		}()
	}
	// 已定義的名稱(含舊名稱、系統變數)不 panic
	_ = ProcessEnv("OPS_DATABASE_URL")
	_ = ProcessEnv("PATH")
	_ = ProcessEnv(LocalToken.Name)
}
