package mcp

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"omixor-core/config"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/oauth"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 和 omixor server 相同的單一 MCP 入口(OAuth),讓遠端 MCP 用戶端(Claude Code、claude.ai 等)連到這台 console。掛在哪裡:
//   - 有設 OMIXOR_PUBLIC_URL、沒設 OMIXOR_OAUTH_ADDR:掛在畫面連接埠(同 server 的同埠作法,見 WebEntry)
//   - 有設 OMIXOR_OAUTH_ADDR:掛在那個獨立連接埠(舊作法,行為不變,見 StartOAuth)
//   - 都沒設:沒有
//
// 只服務:/oauth/*、/.well-known/*,以及四種 MCP 端點 /mcp、/mcp/org/{org}、/mcp/agent、/mcp/worker/{org}(core/oauth 的 MCPMount,和 server/server.go 同一份)。
// console 自己的 MCP 連接埠(3765,Bearer omxm_)完全不變;這個入口不收 omxm_ 權杖,也沒有配對碼(server 的 /api/* 一概沒有)。
// 同意畫面證明身分:evoleap、GitLab,以及 console 認證設定啟用 LDAP 時的 LDAP 帳號密碼(對應核心使用者 ldap:<帳號>)。
// 授權、使用者、組織與工具全是核心的(core/oauth、core/service、core/mcp):這裡只接 console 的核心 service(同一個實例,使用者與組織共用)
// 與畫面快取。console 才有的工具(gcp、github、fb_、voice_、server、範例資料…)不是核心登記的,所以不會出現在這裡(見 TestConsoleToolsNotOnRemotePort)。
// 本機使用者(omixor-console,伺服器管理員)只能在本機使用:不管用什麼 token,在這個入口一律當認證失敗。

// LDAPSource 目前的 LDAP 登入設定與是否啟用(console 的認證設定可以在執行中改)
type LDAPSource = func() (service.LDAPSettings, bool)

// webEntryProblem 設了 OMIXOR_PUBLIC_URL、要在畫面連接埠掛入口時哪裡不合(空字串 = 可以掛)。
// 這個值以前在沒有 OMIXOR_OAUTH_ADDR 時沒有作用,所以條件不合只警告、不啟用,不擋啟動
func webEntryProblem(cfg *config.Config, hasDB, remote bool) string {
	if p := config.WebPublicURLProblem(cfg); p != "" {
		return p
	}
	switch {
	case remote:
		return "遠端模式沒有 MCP 入口:授權與使用者在那台 omixor server"
	case !hasDB:
		return "需要 PostgreSQL(OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY)"
	}
	return ""
}

// useWebEntry 入口要不要掛在畫面連接埠
func useWebEntry(cfg *config.Config) bool {
	return !cfg.IsSet(config.OAuthAddr) && cfg.IsSet(config.PublicURL) && webEntryProblem(cfg, core.DB() != nil, core.IsRemote()) == ""
}

// checkOAuthRuntime 執行期才知道的條件(設定檔層級的在 config.webOAuthRule):要連 PostgreSQL、不是遠端模式
func checkOAuthRuntime(hasDB, remote bool) error {
	switch {
	case remote:
		return errors.New("OMIXOR_OAUTH_ADDR 不能用在遠端模式:授權與使用者在那台 omixor server,請改在 server 設 OMIXOR_PUBLIC_URL")
	case !hasDB:
		return errors.New("OMIXOR_OAUTH_ADDR 需要 PostgreSQL:請設 OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY")
	}
	return nil
}

// PrepareOAuth 要開 MCP 入口時,在建核心(core.New)之前呼叫:檢查條件、讓核心的 service 套上 evoleap 與 GitLab 登入設定。
// OMIXOR_OAUTH_ADDR 條件不合回錯誤(拒絕啟動);只設 OMIXOR_PUBLIC_URL 而條件不合只警告。兩個都沒設什麼都不做
func PrepareOAuth(cfg *config.Config) error {
	separate := cfg.IsSet(config.OAuthAddr)
	switch {
	case separate:
		if err := checkOAuthRuntime(core.DB() != nil, core.IsRemote()); err != nil {
			return err
		}
	case cfg.IsSet(config.PublicURL):
		if p := webEntryProblem(cfg, core.DB() != nil, core.IsRemote()); p != "" {
			log.Printf("警告:設了 OMIXOR_PUBLIC_URL,但畫面連接埠不啟用 MCP 入口:%s", p)
			return nil
		}
	default:
		return nil
	}
	gitlab, err := consoleGitLab(cfg)
	if err != nil {
		if separate {
			return err
		}
		log.Printf("警告:%v;MCP 授權頁不提供 GitLab 登入", err)
		gitlab = nil
	}
	core.ConfigureLocalService(func(svc *service.Service) {
		oauth.ConfigureService(svc, cfg)
		svc.GitLab = gitlab
	})
	return nil
}

// WebEntry 掛在畫面連接埠的 MCP 入口;不該掛(沒設 OMIXOR_PUBLIC_URL、改用 OMIXOR_OAUTH_ADDR、條件不合)回 nil
func WebEntry(app *core.App, cfg *config.Config, ldap LDAPSource) http.Handler {
	if !useWebEntry(cfg) {
		return nil
	}
	if core.AgentService() == nil {
		log.Printf("警告:核心還沒準備好(PostgreSQL 連不上?),MCP 入口暫時回 503")
	}
	warnNoLogin(cfg, ldap)
	public := publicURL(cfg)
	log.Printf("MCP 入口:%s/mcp(掛在畫面連接埠;反向代理要轉發 /mcp、/oauth/、/.well-known/)", public)
	return newOAuthPort(core.AgentService, func(svc *service.Service) http.Handler { return newOAuthHandler(app, svc, cfg, ldap) })
}

// warnNoLogin 授權頁一種登入方式都沒有時提醒(LDAP 之後在認證設定啟用就會出現,不必重啟)
func warnNoLogin(cfg *config.Config, ldap LDAPSource) {
	ok := false
	if ldap != nil {
		_, ok = ldap()
	}
	if !ok && !cfg.Bool(config.OAuthEvoleapLogin) && !cfg.IsSet(config.GitLabURL) {
		log.Printf("警告:MCP 授權頁目前沒有任何登入方式(evoleap 已關、沒設 GitLab、認證設定沒啟用 LDAP)")
	}
}

// consoleGitLab 解析 OMIXOR_GITLAB_*(規則與 omixor server 相同,見 oauth.ConfigureGitLab);沒設回 nil。
// console 沒有核心的 LDAP 登入,所以不支援併 LDAP 帳號
func consoleGitLab(cfg *config.Config) (*service.GitLabLogin, error) {
	probe := &service.Service{}
	if err := oauth.ConfigureGitLab(probe, cfg.Getenv, publicURL(cfg)); err != nil {
		msg := "GitLab 設定:" + cfg.Redact(err.Error())
		if cfg.Bool(config.GitLabLinkLDAP) {
			msg += "(console 不支援 OMIXOR_GITLAB_LINK_LDAP,請拿掉)"
		}
		return nil, errors.New(msg)
	}
	return probe.GitLab, nil
}

// StartOAuth 在 OMIXOR_OAUTH_ADDR 開對外 MCP 連接埠(沒設不做事);開不了回傳錯誤(明確啟用卻開不了,不悄悄略過)
func StartOAuth(app *core.App, cfg *config.Config, ldap LDAPSource) error {
	if !cfg.IsSet(config.OAuthAddr) {
		return nil
	}
	if core.AgentService() == nil {
		return errors.New("OMIXOR_OAUTH_ADDR:核心沒有準備好(PostgreSQL 連不上?)")
	}
	addr := cfg.Str(config.OAuthAddr)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("對外 MCP 連接埠 %s 開不了:%w", addr, err)
	}
	warnNoLogin(cfg, ldap)
	srv := &http.Server{Handler: newOAuthPort(core.AgentService, func(svc *service.Service) http.Handler { return newOAuthHandler(app, svc, cfg, ldap) }), ReadHeaderTimeout: 10 * time.Second}
	go func() { log.Printf("對外 MCP 連接埠異常停止:%v", srv.Serve(ln)) }()
	log.Printf("對外 MCP:%s(對外 %s/mcp;登入回呼 %s/oauth/evoleap/callback,啟用 GitLab 時另有 /oauth/gitlab/callback)", addr, publicURL(cfg), publicURL(cfg))
	return nil
}

// oauthPort 讓對外 MCP 連接埠永遠用 console 目前的核心 service:資料庫連線池換掉時 console 會重建本機核心(見 core.AgentService),
// 舊的 service 連著已關閉的連線。核心的 oauth.Handler、EvoleapProvider、MCPMount 都直接持有 *service.Service,
// 逐請求解析要改動共用型別,所以這裡改成偵測重建:service 指標變了就用新的重建整個 handler(只在換連線池時發生;
// handler 裡只有速率限制等記憶體狀態會歸零,授權、token 都在資料庫)
type oauthPort struct {
	current func() *service.Service
	build   func(*service.Service) http.Handler
	mu      sync.Mutex
	svc     *service.Service
	h       http.Handler
}

func newOAuthPort(current func() *service.Service, build func(*service.Service) http.Handler) *oauthPort {
	return &oauthPort{current: current, build: build}
}

func (p *oauthPort) handler() http.Handler {
	svc := p.current()
	if svc == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if svc != p.svc {
		p.svc, p.h = svc, p.build(svc)
	}
	return p.h
}

func (p *oauthPort) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := p.handler()
	if h == nil { // 核心暫時不可用(連線池重建中或資料庫斷線)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

func publicURL(cfg *config.Config) string { return strings.TrimRight(cfg.Str(config.PublicURL), "/") }

// newOAuthHandler 對外 MCP 連接埠的路由。整個連接埠先過 Host 白名單(對外網址的主機名稱與本機名稱;不符 403,擋 DNS rebinding)
func newOAuthHandler(app *core.App, svc *service.Service, cfg *config.Config, ldap ...LDAPSource) http.Handler {
	public := publicURL(cfg)
	// 同意畫面證明身分是 evoleap、GitLab 與 LDAP:配對碼是從 console 這邊產生的,兌換出來可能是本機管理員 omixor-console,所以不提供(見 omixor 文件櫃 DOC-35《docs/authorization.md》)
	providers := oauth.LoginProviders(svc, public)
	if len(ldap) > 0 && ldap[0] != nil {
		svc.SetLDAPSource(ldap[0]) // token 端點續期時也要向目前的目錄確認
		providers = append(providers, &oauth.LDAPProvider{Svc: svc, Public: public, Settings: ldap[0]})
	}
	oa := oauth.NewWith(oauth.Config{Svc: svc, Public: public, Providers: providers})
	mux := http.NewServeMux()
	oa.Mount(mux)
	act := &activity{a: app, writes: isWriteTool}
	(&oauth.MCPMount{
		Svc: svc, Public: public, OAuth: oa, AgentEndpoint: public + "/mcp/agent",
		Begin: act.Begin, Tool: act.Tool,
		Reject: func(p *model.Principal) bool { return core.IsLocalUser(p.User) },
		// 和本機連接埠的 /mcp/agent 共用同一個 service:失敗次數分開計,遠端猜錯憑證(反向代理後面來源常是 127.0.0.1)不會把本機的角色擋掉
		AgentSource: remoteAgentSource,
		AgentDone: func(role coremcp.Role, tool string, err error) {
			app.RoleCallDone(role.Org, role.TaskID, tool, err)
		},
	}).Mount(mux)
	host := ""
	if u, err := url.Parse(public); err == nil {
		host = u.Hostname()
	}
	return coremcp.HostGuard(host, mux)
}

// remoteAgentSource 對外連接埠角色端點「驗證失敗次數」的來源鍵:"remote:" + 聚合後的位址(IPv6 取 /64,換位址不能繞過限制)
func remoteAgentSource(r *http.Request) string {
	return "remote:" + service.LimitKey(service.ClientIP(r))
}

// isWriteTool 不是唯讀登記的工具就當會寫入(寫入後要重讀 console 的快取;這個連接埠的工具都是核心的,見 coremcp.ReadOnlyTools)
func isWriteTool(name string) bool { return !coremcp.ReadOnlyTools[name] }
