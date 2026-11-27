package mcp

import (
	"log"
	"net"
	"net/http"
	"strconv"

	"omixor-core/config"
	coremcp "omixor-core/mcp"
	"omixor-core/oauth"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 畫面連接埠上的 MCP。console 只有這一個 MCP 入口(和 omixor server 同一套 core/oauth 的 MCPMount),依部署情況分四種:
//
//	公開   有合格的 OMIXOR_PUBLIC_URL:完整入口,對外網址就是它
//	本機   沒有(或不合格):完整入口,但只收「同一台電腦直接連進來」的請求,對外網址是 http://127.0.0.1:<畫面連接埠>
//	獨立   設了 OMIXOR_OAUTH_ADDR:完整入口在那個連接埠(StartOAuth),畫面連接埠只剩本機的角色端點 /mcp/agent
//	沒有   沒有 PostgreSQL、畫面只聽非本機的特定位址又沒有 PUBLIC_URL:/mcp 與 /oauth 回 404 說明原因
//
// 本機的角色端點 /mcp/agent(自動執行、同一台電腦上拿角色憑證的程式)在每一種情況都服務同一台電腦的直接連線,
// 失敗次數和遠端的分開計(遠端猜錯憑證不會擋掉本機的角色)。

type entryMode int

const (
	entryNone entryMode = iota
	entryPublic
	entryLoopback
	entrySeparate
)

type entryPlan struct {
	mode entryMode
	// public 入口的對外網址(OAuth 的 issuer 與 resource);獨立連接埠時是那個入口的
	public string
	// localHost、localURL 同一台電腦直接連畫面連接埠用的主機名稱與網址
	localHost, localURL string
	// off 沒有入口的原因(給畫面與 404;不含路徑或機密)
	off string
}

// listenHostPort 畫面實際聽的主機與連接埠換成「同一台電腦要連的」:沒指定、0.0.0.0、本機名稱都是 127.0.0.1
func listenHostPort(cfg *config.Config) (host, port string) {
	host, port = "127.0.0.1", strconv.Itoa(cfg.Int(config.WebPort))
	addr := cfg.Str(config.WebAddr)
	if addr == "" {
		return host, port
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return host, port
	}
	if p != "" {
		port = p
	}
	if ip := net.ParseIP(h); h != "" && h != "localhost" && (ip == nil || !ip.IsUnspecified() && !ip.IsLoopback()) {
		host = h
	}
	return host, port
}

// LocalBaseURL 同一台電腦直接連畫面連接埠的網址(不含結尾斜線)
func LocalBaseURL(cfg *config.Config) string {
	host, port := listenHostPort(cfg)
	return "http://" + net.JoinHostPort(host, port)
}

// webEntryProblem 公開入口(OMIXOR_PUBLIC_URL)哪裡不合(空字串 = 可以掛)。不合只警告、改用本機入口,不擋啟動
func webEntryProblem(cfg *config.Config, hasDB bool) string {
	if p := config.WebPublicURLProblem(cfg); p != "" {
		return p
	}
	return entryUnavailable(hasDB)
}

// entryUnavailable 整個入口(不分公開或本機)不能提供的原因
func entryUnavailable(hasDB bool) string {
	if !hasDB {
		return "MCP 需要 PostgreSQL(OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY)"
	}
	return ""
}

func planEntry(cfg *config.Config, hasDB bool) entryPlan {
	p := entryPlan{localURL: LocalBaseURL(cfg)}
	p.localHost, _ = listenHostPort(cfg)
	if off := entryUnavailable(hasDB); off != "" {
		p.off = off
		return p
	}
	switch {
	case cfg.IsSet(config.OAuthAddr):
		p.mode, p.public = entrySeparate, publicURL(cfg)
	case cfg.IsSet(config.PublicURL) && config.WebPublicURLProblem(cfg) == "":
		p.mode, p.public = entryPublic, publicURL(cfg)
	case config.IsLoopbackHost(p.localHost):
		p.mode, p.public = entryLoopback, p.localURL
	default:
		p.off = "畫面只聽 " + p.localHost + ",沒設 OMIXOR_PUBLIC_URL 時沒有 MCP 入口;設成 https 的對外網址,或讓畫面聽 127.0.0.1 / 0.0.0.0"
	}
	return p
}

// entryConfig 入口用的設定:本機入口把對外網址換成本機網址
func entryConfig(cfg *config.Config, p entryPlan) *config.Config {
	if p.mode == entryLoopback {
		return cfg.With(config.PublicURL, p.public)
	}
	return cfg
}

// ScreenEntry 畫面連接埠上處理 MCP 路徑(/mcp、/mcp/*、/oauth/*、/.well-known/oauth-*)的 handler,一定不是 nil;
// 順便把目前的狀態寫進 app.MCP(畫面顯示、自動執行要連的角色端點)
func ScreenEntry(app *core.App, cfg *config.Config, ldap LDAPSource) http.Handler {
	plan := planEntry(cfg, core.DB() != nil)
	if cfg.IsSet(config.PublicURL) && !cfg.IsSet(config.OAuthAddr) {
		if why := webEntryProblem(cfg, core.DB() != nil); why != "" {
			log.Printf("警告:設了 OMIXOR_PUBLIC_URL,但不能當 MCP 的對外網址:%s", why)
		}
	}
	setStatus(app, plan)

	var full http.Handler
	eff := entryConfig(cfg, plan)
	switch plan.mode {
	case entryPublic:
		log.Printf("MCP 入口:%s/mcp(掛在畫面連接埠;反向代理要轉發 /mcp、/oauth/、/.well-known/)", plan.public)
	case entryLoopback:
		log.Printf("MCP 入口:%s/mcp(只收這台電腦直接連進來的;要讓別台電腦連,設 OMIXOR_PUBLIC_URL)", plan.public)
	case entrySeparate:
		log.Printf("MCP 入口在 OMIXOR_OAUTH_ADDR;畫面連接埠只有本機的角色端點 %s/mcp/agent", plan.localURL)
	default:
		log.Printf("這台 console 沒有 MCP 入口:%s", plan.off)
	}
	if plan.mode == entryPublic || plan.mode == entryLoopback {
		if svc := core.AgentService(); svc == nil {
			log.Printf("警告:核心還沒準備好(PostgreSQL 連不上?),MCP 入口暫時回 503")
			warnNoLogin(eff, ldap) // 沒有 service 跑不了自我檢查,只看設定
		} else if err := selfCheck(svc, eff, ldap); err != nil {
			log.Printf("警告:%v", err)
		}
		if ldap != nil {
			if _, ok := ldap(); !ok {
				log.Printf("提醒:沒啟用 LDAP 登入時,MCP 授權頁沒有本機帳號可用(本機使用者不能授權);要用 MCP 請到 設定 › 認證 啟用 LDAP,或設定 GitLab / evoleap 登入且該帳號是組織成員")
			}
		}
		full = newOAuthPort(core.AgentService, func(svc *service.Service) http.Handler { return newOAuthHandler(app, svc, eff, ldap) })
	}
	return screenRouter(plan, full, AgentHTTPHandler(&Handler{App: app}))
}

func setStatus(app *core.App, plan entryPlan) {
	st := core.MCPStatus{State: "off", Error: plan.off}
	if plan.mode != entryNone {
		st.State, st.URL = "listening", plan.public+"/mcp"
		st.Local = plan.mode == entryLoopback
	}
	app.Mu.Lock()
	app.MCP = st
	app.AgentLocalEndpoint = plan.localURL + "/mcp/agent"
	app.Mu.Unlock()
}

// screenRouter 依 plan 分派;full 是完整入口(公開、本機才有),agent 是本機的角色端點
func screenRouter(plan entryPlan, full, agent http.Handler) http.Handler {
	localAgent := coremcp.HostGuard(plan.localHost, agent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct := directFromThisMachine(r)
		if r.URL.Path == "/mcp/agent" && direct && coremcp.HostAllowed(r.Host, plan.localHost) {
			localAgent.ServeHTTP(w, r)
			return
		}
		switch plan.mode {
		case entryPublic:
			full.ServeHTTP(w, r)
		case entryLoopback:
			if !direct || !coremcp.HostAllowed(r.Host, "") {
				refuse(w, 403, "這個 MCP 入口只收同一台電腦直接連到 "+plan.public+" 的請求;要讓別台電腦或經過代理連線,伺服器要設 OMIXOR_PUBLIC_URL")
				return
			}
			full.ServeHTTP(w, r)
		case entrySeparate:
			refuse(w, 404, "MCP 入口在另一個連接埠:"+plan.public+"/mcp")
		default:
			if r.URL.Path == "/mcp/agent" && direct { // 沒有入口時角色端點照舊回 401(不是 404)
				localAgent.ServeHTTP(w, r)
				return
			}
			refuse(w, 404, plan.off)
		}
	})
}

func refuse(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg))
}

// directFromThisMachine 這個連線是不是同一台電腦直接連進來的:來源是本機回送位址、或和被連的位址相同,
// 而且沒有任何反向代理的轉送標頭(有代理就看不出真正的來源,一律當外部)。
// 限制:同一台電腦上的反向代理如果不加任何轉送標頭、又把 Host 改寫成本機位址,這裡和 Host 檢查都分不出來
// (例如 nginx 只寫 proxy_pass);放在代理後面要設 OMIXOR_PUBLIC_URL,或讓代理保留 Host(見 console/README.md)
func directFromThisMachine(r *http.Request) bool {
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Via"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	ip := net.ParseIP(coremcp.RemoteHost(r))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if h, _, err := net.SplitHostPort(a.String()); err == nil {
			return ip.Equal(net.ParseIP(h))
		}
	}
	return false
}

// selfCheck 印 OAuth 啟動自我檢查摘要(看的登入方式和授權頁是同一份 consoleProviders),回傳致命問題
func selfCheck(svc *service.Service, cfg *config.Config, ldap LDAPSource) error {
	return oauth.Startup(oauthEnv(cfg, consoleProviders(svc, publicURL(cfg), ldap)), log.Printf)
}
