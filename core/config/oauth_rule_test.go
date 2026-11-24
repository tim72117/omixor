package config

import (
	"strings"
	"testing"
)

func webCfg(env map[string]string) *Config {
	return LoadFrom(func(k string) string { return env[k] }, Web)
}

// console 的 OAuth 連接埠(OMIXOR_OAUTH_ADDR)的相依檢查:沒設的時候什麼都不檢查
func TestWebOAuthRule(t *testing.T) {
	const addr = "127.0.0.1:8771"
	cases := []struct {
		name string
		env  map[string]string
		want string // 空 = 通過;否則錯誤要含這段
	}{
		{"沒設 OAuth 連接埠", map[string]string{"OMIXOR_PUBLIC_URL": "http://example.com"}, ""},
		{"缺對外網址", map[string]string{"OMIXOR_OAUTH_ADDR": addr}, "OMIXOR_PUBLIC_URL"},
		{"https 可以", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "https://console.example.com/"}, ""},
		{"https 帶連接埠可以", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "https://console.example.com:8443"}, ""},
		{"公開網址用 http 不行", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "http://console.example.com"}, "https"},
		{"localhost 可以用 http", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "http://localhost:8771"}, ""},
		{"127.0.0.1 可以用 http", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "http://127.0.0.1:8771"}, ""},
		{"[::1] 可以用 http", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "http://[::1]:8771"}, ""},
		{"網址不能帶路徑", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "https://console.example.com/omixor"}, "不含路徑"},
		{"evoleap 登入不能關", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "https://c.example.com", "OMIXOR_OAUTH_EVOLEAP_LOGIN": "false"}, "OMIXOR_OAUTH_EVOLEAP_LOGIN"},
		{"遠端模式不行", map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "https://c.example.com", "OMIXOR_SERVER": "https://omixor.example.com"}, "遠端模式"},
	}
	for _, c := range cases {
		err := webCfg(c.env).Validate(Web)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s:不該有錯,實際 %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s:要有錯誤含 %q,實際 %v", c.name, c.want, err)
		}
	}
}
