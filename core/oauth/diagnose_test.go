package oauth

import (
	"strings"
	"testing"

	"omixor-core/config"
	"omixor-core/gitlabauth"
	"omixor-core/ldapauth"
	"omixor-core/service"
)

func diagCfg(prog config.Program, env map[string]string) *config.Config {
	return config.LoadFrom(func(k string) string { return env[k] }, prog)
}

// 啟動自我檢查:每個條件的等級與訊息(致命的要回報為致命、FatalError 不為 nil)
func TestDiagnose(t *testing.T) {
	evo := func(url string, off bool) []IdentityProvider {
		return []IdentityProvider{&EvoleapProvider{Svc: &service.Service{Evoleap: url, OAuthEvoleapOff: off}, Public: "x"}, &PairProvider{}}
	}
	const addr = "127.0.0.1:8771"
	proxied := map[string]string{"OMIXOR_PUBLIC_URL": "https://o.example.com", "OMIXOR_TRUSTED_PROXY_HOPS": "1"}
	// hosted:目錄設定由宿主提供(console)
	ldap := func(on, hosted bool) *LDAPProvider {
		set := service.LDAPSettings{}
		if on {
			set.Dir = &ldapauth.Authenticator{}
		}
		if hosted {
			return &LDAPProvider{Settings: func() (service.LDAPSettings, bool) { return set, on }}
		}
		return &LDAPProvider{Svc: &service.Service{LDAP: &ldapauth.Authenticator{}}}
	}
	cases := []struct {
		name  string
		env   Env
		want  Level  // 要出現的最高等級
		match string // 該等級的項目要含這段;空 = 不檢查
		none  string // 整份摘要不能含這段;空 = 不檢查
	}{
		{"server 沒設網址", Env{Cfg: diagCfg(config.Server, nil), Prog: config.Server}, Info, "OAuth 未啟用", ""},
		{"server https 正常", Env{Cfg: diagCfg(config.Server, map[string]string{"OMIXOR_PUBLIC_URL": "https://o.example.com/", "OMIXOR_TRUSTED_PROXY_HOPS": "1"}), Prog: config.Server, Providers: evo("https://evoleap.io", false)},
			Info, "https://o.example.com/oauth/evoleap/callback", "!"},
		{"server http 公開網址只警告", Env{Cfg: diagCfg(config.Server, map[string]string{"OMIXOR_PUBLIC_URL": "http://o.example.com"}), Prog: config.Server}, Warn, "https", ""},
		{"server 網址帶路徑警告", Env{Cfg: diagCfg(config.Server, map[string]string{"OMIXOR_PUBLIC_URL": "https://o.example.com/x", "OMIXOR_TRUSTED_PROXY_HOPS": "1"}), Prog: config.Server}, Warn, "不含路徑", ""},
		{"server evoleap 被關掉", Env{Cfg: diagCfg(config.Server, map[string]string{"OMIXOR_PUBLIC_URL": "https://o.example.com", "OMIXOR_TRUSTED_PROXY_HOPS": "1"}), Prog: config.Server, Providers: evo("https://evoleap.io", true)},
			Warn, "只剩配對碼", ""},
		{"server 沒有 evoleap 網址", Env{Cfg: diagCfg(config.Server, map[string]string{"OMIXOR_PUBLIC_URL": "https://o.example.com", "OMIXOR_TRUSTED_PROXY_HOPS": "1"}), Prog: config.Server, Providers: evo("", false)},
			Warn, "只剩配對碼", ""},
		{"代理層數 0 + 公開 https", Env{Cfg: diagCfg(config.Server, map[string]string{"OMIXOR_PUBLIC_URL": "https://o.example.com"}), Prog: config.Server}, Warn, "OMIXOR_TRUSTED_PROXY_HOPS", ""},
		{"代理層數 0 + 本機位址不警告", Env{Cfg: diagCfg(config.Server, map[string]string{"OMIXOR_PUBLIC_URL": "http://localhost:8080"}), Prog: config.Server}, Info, "", "!"},
		{"server evoleap 關掉但有 LDAP 不警告", Env{Cfg: diagCfg(config.Server, proxied), Prog: config.Server, Providers: append(evo("https://evoleap.io", true), ldap(true, false))},
			Info, "LDAP 帳號密碼登入已啟用", "!"},
		{"web 沒設連接埠也沒有網址", Env{Cfg: diagCfg(config.Web, nil), Prog: config.Web}, Info, "", "對外網址"},
		{"web 畫面連接埠入口", Env{Cfg: diagCfg(config.Web, proxied), Prog: config.Web, Providers: []IdentityProvider{ldap(true, true)}}, Info, "LDAP 帳號密碼登入已啟用", "!"},
		{"web 畫面連接埠入口沒有任何登入方式", Env{Cfg: diagCfg(config.Web, proxied), Prog: config.Web, Providers: []IdentityProvider{ldap(false, true)}}, Warn, "沒有任何登入方式", "OMIXOR_OAUTH_ADDR"},
		{"web 缺對外網址", Env{Cfg: diagCfg(config.Web, map[string]string{"OMIXOR_OAUTH_ADDR": addr}), Prog: config.Web, HasDB: true}, Fatal, "OMIXOR_PUBLIC_URL", ""},
		{"web http 公開網址致命", Env{Cfg: diagCfg(config.Web, map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "http://c.example.com"}), Prog: config.Web, HasDB: true}, Fatal, "https", ""},
		{"web 只用 LDAP 可以關 evoleap", Env{Cfg: diagCfg(config.Web, map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "https://c.example.com", "OMIXOR_OAUTH_EVOLEAP_LOGIN": "false", "OMIXOR_TRUSTED_PROXY_HOPS": "1"}), Prog: config.Web, HasDB: true,
			Providers: append(evo("https://evoleap.io", true)[:1], ldap(true, true))}, Info, "同意畫面的登入方式:LDAP", "!"},
		{"web 沒有 PostgreSQL", Env{Cfg: diagCfg(config.Web, map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "https://c.example.com"}), Prog: config.Web}, Fatal, "PostgreSQL", ""},
		{"web 全部正常", Env{Cfg: diagCfg(config.Web, map[string]string{"OMIXOR_OAUTH_ADDR": addr, "OMIXOR_PUBLIC_URL": "https://c.example.com", "OMIXOR_TRUSTED_PROXY_HOPS": "1"}), Prog: config.Web, HasDB: true, Providers: evo("https://evoleap.io", false)},
			Info, "/oauth/evoleap/callback", "✗"},
	}
	for _, c := range cases {
		ps := Diagnose(c.env)
		top, hit := Info, c.match == ""
		for _, p := range ps {
			top = max(top, p.Level)
			if p.Level == c.want && strings.Contains(p.Msg+p.Fix, c.match) {
				hit = true
			}
		}
		if top != c.want || !hit {
			t.Errorf("%s:要有 %v 等級含 %q,實際最高 %v\n%s", c.name, c.want, c.match, top, Summary(ps))
		}
		if c.none != "" && strings.Contains(Summary(ps), c.none) {
			t.Errorf("%s:摘要不該含 %q\n%s", c.name, c.none, Summary(ps))
		}
		if gotFatal := FatalError(ps) != nil; gotFatal != (c.want == Fatal) {
			t.Errorf("%s:FatalError 與等級不一致(致命 %v)", c.name, gotFatal)
		}
	}
}

func TestStartupPrintsSummary(t *testing.T) {
	var out string
	e := Env{Cfg: diagCfg(config.Server, map[string]string{"OMIXOR_PUBLIC_URL": "https://o.example.com", "OMIXOR_TRUSTED_PROXY_HOPS": "1"}), Prog: config.Server,
		Providers: []IdentityProvider{&EvoleapProvider{Svc: &service.Service{Evoleap: "https://evoleap.io"}}}}
	if err := Startup(e, func(f string, a ...any) { out = strings.TrimSpace(strings.ReplaceAll(f, "%s", a[0].(string))) }); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"OAuth 啟動檢查:", "✓ 對外網址 https://o.example.com", "https://o.example.com/oauth/evoleap/callback"} {
		if !strings.Contains(out, w) {
			t.Errorf("摘要缺 %q:\n%s", w, out)
		}
	}
	if err := Startup(Env{Cfg: diagCfg(config.Web, nil), Prog: config.Web}, func(string, ...any) { t.Fatal("沒啟用不該印") }); err != nil {
		t.Fatal(err)
	}
}

func TestGitLabProviderCheck(t *testing.T) {
	on := (&GitLabProvider{Svc: &service.Service{GitLab: &service.GitLabLogin{Client: &gitlabauth.Client{}}}}).Check("https://omixor.example.com")
	if len(on) != 1 || on[0].Level != Info || !strings.Contains(on[0].Msg, "https://omixor.example.com/oauth/gitlab/callback") {
		t.Fatalf("啟用時要印出要登記的回呼網址:%v", on)
	}
	off := (&GitLabProvider{Svc: &service.Service{}}).Check("https://omixor.example.com")
	if len(off) != 1 || off[0].Level != Info || !strings.Contains(off[0].Msg, "OMIXOR_GITLAB_URL") {
		t.Fatalf("沒啟用只是提示:%v", off)
	}
}
