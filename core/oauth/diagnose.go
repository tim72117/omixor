package oauth

import (
	"errors"
	"net/url"
	"strings"

	"omixor-core/config"
)

// OAuth 啟動自我檢查:啟動時把 OAuth 要成功需要的條件逐項檢查,印成一份看得懂的摘要(✓ 正常 / ! 警告 / ✗ 致命),
// 致命的問題讓啟動失敗,而不是等到使用者授權時才看到一句「授權失敗」。
// omixor-server 與 console 的 MCP 入口(獨立的 OAuth 連接埠、或掛在畫面連接埠)共用這一份(Startup);各 provider 自己的條件由它實作 checker(見 EvoleapProvider.Check),
// 「同意畫面到底有哪些登入方式」看全部 provider 才知道,在 loginProblems。
// 設定層級的規則不在這裡重寫:對外網址與 console 的相依用 config 的 WebPublicURLProblem / WebOAuthIssues(和 Validate 同一份)。

// Level 問題的嚴重程度
type Level int

const (
	Info  Level = iota // ✓ 正常或提示(例如要登記的回呼網址)
	Warn               // ! 可以啟動,但很可能出問題
	Fatal              // ✗ 不能啟動
)

// Problem 檢查的一個項目;Fix 是怎麼修(可為空)
type Problem struct {
	Level Level
	Msg   string
	Fix   string
}

func (p Problem) String() string {
	mark := map[Level]string{Info: "✓", Warn: "!", Fatal: "✗"}[p.Level]
	if p.Fix == "" {
		return mark + " " + p.Msg
	}
	return mark + " " + p.Msg + "(" + p.Fix + ")"
}

// checker provider 可以選擇性地實作:回這個 provider 的條件與要登記的回呼網址(public 是對外網址,不含結尾斜線);沒實作 = 沒有條件
type checker interface{ Check(public string) []Problem }

// Env 檢查需要的輸入。HasDB、Remote 是執行期才知道的事(只有 console 的獨立 OAuth 連接埠用)
type Env struct {
	Cfg           *config.Config
	Prog          config.Program // config.Server 或 config.Web
	Providers     []IdentityProvider
	HasDB, Remote bool
}

// separatePort console 用獨立的 OAuth 連接埠(OMIXOR_OAUTH_ADDR):條件不合就不能啟動
func (e Env) separatePort() bool { return e.Prog == config.Web && e.Cfg.IsSet(config.OAuthAddr) }

// enabled 這個程式有沒有啟用 OAuth:設了對外網址(console 是掛在畫面連接埠),或 console 開了獨立連接埠。
// 掛在畫面連接埠的條件(PostgreSQL、非遠端模式)由宿主決定掛不掛,真的掛上才呼叫檢查
func (e Env) enabled() bool { return e.separatePort() || e.Cfg.IsSet(config.PublicURL) }

// Diagnose 逐項檢查,回傳全部項目(含正常的);沒啟用 OAuth 時 console 回 nil、server 回一則提示
func Diagnose(e Env) []Problem {
	if !e.enabled() {
		if e.Prog == config.Web {
			return nil
		}
		return []Problem{{Level: Info, Msg: "OAuth 未啟用(沒設 OMIXOR_PUBLIC_URL)"}}
	}
	public := strings.TrimRight(e.Cfg.Str(config.PublicURL), "/")
	var ps []Problem
	ps = append(ps, publicProblems(e, public)...)
	if e.separatePort() {
		ps = append(ps, webProblems(e)...)
	}
	ps = append(ps, proxyProblems(e, public)...)
	for _, p := range e.Providers {
		if c, ok := p.(checker); ok {
			ps = append(ps, c.Check(public)...)
		}
	}
	return append(ps, loginProblems(e.Providers)...)
}

// loginProblems 同意畫面實際會有哪些登入方式(沒給 provider = 不檢查):一種都沒有、或只剩備援的配對碼要警告。
// 一種都沒有不算致命:console 的 LDAP 在認證設定,執行中啟用就會出現
func loginProblems(providers []IdentityProvider) []Problem {
	if len(providers) == 0 {
		return nil
	}
	var names []string
	onlyPair := true
	for _, p := range providers {
		if !providerEnabled(p) {
			continue
		}
		names = append(names, p.Label())
		if _, pair := p.(*PairProvider); !pair {
			onlyPair = false
		}
	}
	const more = "evoleap、GitLab、LDAP 至少開一種"
	switch {
	case len(names) == 0:
		return []Problem{{Level: Warn, Msg: "MCP 授權頁目前沒有任何登入方式", Fix: more + ";console 在認證設定啟用 LDAP 後不必重啟"}}
	case onlyPair:
		return []Problem{{Level: Warn, Msg: "同意畫面只剩配對碼", Fix: "要用帳號登入就" + more}}
	}
	return []Problem{{Level: Info, Msg: "同意畫面的登入方式:" + strings.Join(names, "、")}}
}

// publicProblems 對外網址:有設、https(本機位址除外)、不含路徑。console 獨立連接埠的問題一律致命(連接埠是對外公開的,由 webProblems 回報);
// 其他只警告(沿用既有部署:server 的 http 對外網址一直能啟動)
func publicProblems(e Env, public string) []Problem {
	if public == "" {
		if e.separatePort() { // 設了 OMIXOR_OAUTH_ADDR 沒設網址:由 WebOAuthIssues 回報(它說得最完整)
			return nil
		}
		return []Problem{{Level: Fatal, Msg: "OMIXOR_PUBLIC_URL 是空的", Fix: "設成對外的 https 網址"}}
	}
	if is := config.WebPublicURLProblem(e.Cfg); is != "" {
		if e.separatePort() { // 一樣的問題 WebOAuthIssues 會以致命回報
			return nil
		}
		return []Problem{{Level: Warn, Msg: is}}
	}
	return []Problem{{Level: Info, Msg: "對外網址 " + public}}
}

// webProblems console 的獨立 OAuth 連接埠才有的條件:設定層級(config.WebOAuthIssues)與執行期(要連 PostgreSQL、不是遠端模式)
func webProblems(e Env) []Problem {
	var ps []Problem
	for _, is := range e.Cfg.WebOAuthIssues() {
		ps = append(ps, Problem{Level: Fatal, Msg: "OMIXOR_OAUTH_ADDR:" + is})
	}
	if e.Remote && !e.Cfg.IsSet(config.ServerURL) { // 設定層級已經回報過遠端模式(OMIXOR_SERVER)
		ps = append(ps, Problem{Level: Fatal, Msg: "OMIXOR_OAUTH_ADDR 不能用在遠端模式:授權與使用者在那台 omixor server",
			Fix: "改在 server 設 OMIXOR_PUBLIC_URL"})
	}
	if !e.HasDB {
		ps = append(ps, Problem{Level: Fatal, Msg: "OMIXOR_OAUTH_ADDR 需要 PostgreSQL", Fix: "設 OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY"})
	}
	return ps
}

// proxyProblems 對外網址是公開的 https 主機、卻沒有信任任何代理層:多半在反向代理後面,每 IP 的頻率限制會把所有人當成代理的位址
func proxyProblems(e Env, public string) []Problem {
	u, err := url.Parse(public)
	if err != nil || u.Scheme != "https" || config.IsLoopbackHost(u.Hostname()) || e.Cfg.ProxyHops() != 0 {
		return nil
	}
	return []Problem{{Level: Warn, Msg: "OMIXOR_TRUSTED_PROXY_HOPS 是 0,但對外網址是公開的 https 主機(多半在反向代理後面):每個 IP 的頻率限制會把所有人當成同一個來源",
		Fix: "直接面對網路就忽略;有代理就設成代理層數(例如 1)"}}
}

// Summary 摘要(多行,第一行是標題)
func Summary(ps []Problem) string {
	lines := []string{"OAuth 啟動檢查:"}
	for _, p := range ps {
		lines = append(lines, "  "+p.String())
	}
	return strings.Join(lines, "\n")
}

// FatalError 把致命項目合成一個錯誤(沒有致命項目回 nil);宿主用它讓啟動失敗
func FatalError(ps []Problem) error {
	var msgs []string
	for _, p := range ps {
		if p.Level == Fatal {
			m := p.Msg
			if p.Fix != "" {
				m += "(" + p.Fix + ")"
			}
			msgs = append(msgs, m)
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return errors.New("OAuth 設定有問題,不能啟動:" + strings.Join(msgs, ";"))
}

// Startup 檢查、把摘要交給 logf、回傳致命問題(nil = 可以啟動)。沒有項目(console 沒啟用 OAuth)什麼都不印
func Startup(e Env, logf func(format string, args ...any)) error {
	ps := Diagnose(e)
	if len(ps) == 0 {
		return nil
	}
	logf("%s", Summary(ps))
	return FatalError(ps)
}
