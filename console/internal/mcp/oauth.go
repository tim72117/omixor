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
	"omixor-core/oauth"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// OAuth 專用連接埠(OMIXOR_OAUTH_ADDR):讓 claude.ai 等遠端 MCP 用戶端用 evoleap 登入、OAuth 連到這台 console。
//
// 只服務三組路徑:/oauth/*、/.well-known/*、/mcp(統一入口)。console 自己的 MCP 連接埠(Bearer omxm_)與畫面連接埠完全不變,
// 這個連接埠上也沒有 /mcp/org/{org}、/mcp/agent、/mcp/worker 與 omxm_ 權杖;對外網路(反向代理、Tailscale Funnel)只轉發這一個連接埠。
// 授權、使用者、組織與工具白名單全是核心的(core/oauth、core/service、core/mcp):這裡只接 console 的核心 service(同一個實例,使用者與組織共用)
// 與畫面快取。console 才有的工具(gcp、github、fb_、voice_、server、範例資料…)不在核心白名單內,所以不會出現在這裡(見 TestConsoleToolsNotOverOAuth)。

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

// PrepareOAuth 設了 OMIXOR_OAUTH_ADDR 時,在建核心(core.New)之前呼叫:檢查條件、讓核心的 service 套上 evoleap 登入設定。
// 沒設 OMIXOR_OAUTH_ADDR 什麼都不做
func PrepareOAuth(cfg *config.Config) error {
	if !cfg.IsSet(config.OAuthAddr) {
		return nil
	}
	if err := checkOAuthRuntime(core.DB() != nil, core.IsRemote()); err != nil {
		return err
	}
	core.ConfigureLocalService(func(svc *service.Service) { oauth.ConfigureService(svc, cfg) })
	return nil
}

// StartOAuth 在 OMIXOR_OAUTH_ADDR 開 OAuth 連接埠(沒設不做事);開不了回傳錯誤(明確啟用卻開不了,不悄悄略過)
func StartOAuth(app *core.App, cfg *config.Config) error {
	if !cfg.IsSet(config.OAuthAddr) {
		return nil
	}
	if core.AgentService() == nil {
		return errors.New("OMIXOR_OAUTH_ADDR:核心沒有準備好(PostgreSQL 連不上?)")
	}
	addr := cfg.Str(config.OAuthAddr)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("OAuth 的連接埠 %s 開不了:%w", addr, err)
	}
	srv := &http.Server{Handler: newOAuthPort(core.AgentService, func(svc *service.Service) http.Handler { return newOAuthHandler(app, svc, cfg) }), ReadHeaderTimeout: 10 * time.Second}
	go func() { log.Printf("OAuth 連接埠異常停止:%v", srv.Serve(ln)) }()
	log.Printf("OAuth:%s(對外 %s/mcp;evoleap 要登記回呼 %s/oauth/evoleap/callback)", addr, publicURL(cfg), publicURL(cfg))
	return nil
}

// oauthPort 讓 OAuth 連接埠永遠用 console 目前的核心 service:資料庫連線池換掉時 console 會重建本機核心(見 core.AgentService),
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

// newOAuthHandler OAuth 連接埠的路由。整個連接埠先過 Host 白名單(對外網址的主機名稱與本機名稱;不符 403,擋 DNS rebinding)
func newOAuthHandler(app *core.App, svc *service.Service, cfg *config.Config) http.Handler {
	public := publicURL(cfg)
	// 證明身分只有 evoleap:配對碼是 console 自己產生的,兌換出來會是本機管理員 omixor-console,所以不提供(見 docs/authorization.md)
	oa := oauth.NewWith(oauth.Config{Svc: svc, Public: public, Providers: []oauth.IdentityProvider{&oauth.EvoleapProvider{Svc: svc, Public: public}}})
	mux := http.NewServeMux()
	oa.Mount(mux)
	act := &activity{a: app, writes: isWriteTool}
	// OAuthOnly:這個連接埠對外公開,只收 OAuth access token,不收長效的 omx_ 使用者 token
	(&oauth.MCPMount{Svc: svc, Public: public, OAuth: oa, OAuthOnly: true, Begin: act.Begin, Tool: act.Tool}).MountUnified(mux)
	host := ""
	if u, err := url.Parse(public); err == nil {
		host = u.Hostname()
	}
	return coremcp.HostGuard(host, mux)
}

// isWriteTool 不是唯讀登記的工具就當會寫入(寫入後要重讀 console 的快取;OAuth 讀寫授權的工具都是核心的,見 coremcp.ReadOnlyTools)
func isWriteTool(name string) bool { return !coremcp.ReadOnlyTools[name] }
