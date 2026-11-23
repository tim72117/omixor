// Package server 啟動 omixor server(omixor-core 的 HTTP API + 健康檢查),引用 omixor-core 的 api、service、store。cmd/omixor-server 呼叫它。
//
// 環境變數:
//
//	PORT                           Cloud Run 給的埠(預設 8080)
//	OMIXOR_ADDR                    監聽位址(預設 :$PORT;本機測試可以設 127.0.0.1:<埠>)
//	OMIXOR_DATABASE_URL            PostgreSQL 網址(Cloud Run 上來自 Secret Manager 的 OMIXOR_DATABASE_URL;舊名稱 OPS_DATABASE_URL 仍相容)
//	OMIXOR_ADMIN_BOOTSTRAP_EMAIL   第一位伺服器管理員的 email(使用者表是空的時才用)
//	OMIXOR_TOKEN_DAYS              token 有效天數(預設 90;0 是不到期)
//	EVOLEAP_URL                    evoleap 網址(預設 https://evoleap.io)
//	OMIXOR_AGENT_CRED_HOURS        角色憑證有效小時數(預設 2)
//	OMIXOR_TRUSTED_PROXY_HOPS      信任的代理層數(取來源 IP 用;0 不看 X-Forwarded-For;預設:有 K_SERVICE(Cloud Run)是 1,否則 0)
//	OMIXOR_PUBLIC_URL              對外網址(例如 https://omixor.example.com);設了,領取任務時會附上角色端點網址
//	OMIXOR_SITE_DIR                形象網站的資料夾(映像裡是 /site);設了才提供網站,見 site.go
//	OMIXOR_LDAP_URL …              LDAP / Active Directory 登入(POST /api/auth/ldap):設了 OMIXOR_LDAP_URL 才啟用,設定不完整會直接啟動失敗。
//	                               全部變數見 core/ldapauth;另外兩個在這裡:
//	OMIXOR_LDAP_ADMINS             伺服器管理員的 LDAP 帳號,逗號分隔(小寫比對);第一次通過 LDAP 的其他人自動建成一般使用者
//	OMIXOR_LDAP_EMAIL_DOMAIN       目錄沒有 mail 屬性時,用「帳號@這個網域」當 email
//	OMIXOR_LDAP_TRUST_MAIL         true:管理員先用 email 加進名單、還沒登入過的人,會和目錄 mail 相同的 LDAP 帳號對上(預設 false;
//	                               目錄的 mail 使用者自己改得動時不要開,否則能冒用別人預先加入的身分)
//
// 路由:
//
//	/                              形象網站英文首頁(site/index.html;另有 /pricing、/favicon.svg、/og.png、/robots.txt、/sitemap.xml、/llms.txt;
//	                               文字檔有 gzip;其他找不到的頁面回 site/404.html)
//	/health                        健康檢查
//	/api/…                         核心 HTTP API(core/api)
//	/mcp/agent                     角色端點:Authorization: Bearer <agentId>:<secret>(領取任務時發的憑證),白名單工具(含文件櫃,依角色的 docAccess)
//	/mcp/worker/{org}              執行者專用 MCP(只有 dispatch_queue/claim/status/release):認證同 /mcp/org/{org}
//	/mcp                           統一入口 MCP(第一階段):Authorization: Bearer omx_…;每次工具呼叫用 org 參數指定組織(必填,沒有預設組織),
//	                               每次驗證呼叫者是該組織成員;OAuth 的 omo_ 一律 403(OAuth 請用 /mcp/org/{org})
//	/mcp/org/{org}                 管理用 MCP(組織 {org} 的 Issue、角色、任務、文件櫃):Authorization: Bearer omx_…(omixor token),權限同該使用者;
//	                               或 OAuth 的 access token(omo_…,唯讀,見下面 /oauth;只開放唯讀工具,帶 MCP Apps 畫面 ui://omixor/flowchart、ui://omixor/items)
//	/oauth/…、/.well-known/…       OAuth 2.1 授權伺服器(Claude 自訂 connector 用):/oauth/register、/oauth/authorize、/oauth/token、/oauth/consent.css,
//	                               metadata 在 /.well-known/oauth-protected-resource/mcp/org/{org}、/.well-known/oauth-authorization-server/oauth;
//	                               要設 OMIXOR_PUBLIC_URL 才掛;使用者在同意畫面輸入「手機配對」產生的配對碼證明身分(core/oauth、core/service/oauth.go)
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"omixor-core/api"
	"omixor-core/backend"
	"omixor-core/config"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/oauth"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/version"
)

// Handler /health 加上核心 API(設定用 config.Current();要指定設定用 HandlerWith)
func Handler(svc *service.Service) http.Handler { return HandlerWith(svc, config.Current()) }

// HandlerWith 同 Handler,用指定的設定(OMIXOR_PUBLIC_URL、OMIXOR_SITE_DIR)
func HandlerWith(svc *service.Service, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()
	// Cloud Run 保留 /healthz,不能用
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	// 版本資訊(免登入):{version, commit, buildTime}
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(version.Info())
	})
	mux.Handle("/api/", api.New(svc).Handler())

	// MCP:角色端點與管理端點(協定與工具都在 omixor-core/mcp,這裡只接路由與認證;文件櫃工具是核心內建的,不用另外掛)
	public := strings.TrimRight(cfg.Str(config.PublicURL), "/")
	agentURL := ""
	if public != "" {
		agentURL = public + "/mcp/agent"
	}
	mux.Handle("/mcp/agent", mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc}))
	// OAuth(Claude 自訂 connector 唯讀連 /mcp/org/{org}):需要對外網址(issuer、resource 都由它推導),沒設就不掛
	var oa *oauth.Handler
	if public != "" {
		oa = oauth.New(svc, public)
		oa.Mount(mux)
	}
	adminMCP := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		AgentEndpoint: agentURL,
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			org := r.PathValue("org")
			var p *model.Principal
			var err error
			if oa != nil && strings.HasPrefix(tok, model.OAuthTokenPrefix) {
				// OAuth access token:只收綁定「這個組織的 MCP 網址」的;身分是唯讀(AdminEndpoint 只開放唯讀工具)
				p, err = svc.AuthenticateOAuth(tok, oa.MCPURL(org))
			} else {
				p, err = svc.Authenticate(tok) // omx_ 使用者 token;omo_ 在這裡一律 403(沒有 OAuth 設定時)
			}
			if err != nil {
				return nil, "", err
			}
			return &backend.Local{Svc: svc, P: p}, org, nil
		},
		ResourceMetadata: func(r *http.Request) string {
			if oa == nil || !model.ValidOrgID(r.PathValue("org")) {
				return ""
			}
			return oa.ResourceMetadataURL(r.PathValue("org"))
		},
	})
	if oa != nil {
		mux.Handle("/mcp/org/{org}", oa.ObserveMCP(adminMCP)) // 觀測 log:只記沒帶 token 或帶 OAuth token 的請求(見 core/oauth/log.go)
	} else {
		mux.Handle("/mcp/org/{org}", adminMCP)
	}
	// 統一入口:單一連線,組織由每次呼叫的 org 參數指定(不做預設組織);只收 omx_ 使用者 token,omo_ 在 core/mcp 先擋掉
	mux.Handle("/mcp", mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{
		AgentEndpoint: agentURL,
		Resolve: func(r *http.Request) (backend.Backend, error) {
			tok, _ := mcp.BearerToken(r)
			p, err := svc.Authenticate(tok)
			if err != nil {
				return nil, err
			}
			return &backend.Local{Svc: svc, P: p}, nil
		},
	}))
	// 執行者端點:給領取任務的 LLM 用,只有 dispatch_ 四個工具;認證同管理端點
	mux.Handle("/mcp/worker/{org}", mcp.WorkerEndpoint(mcp.WorkerEndpointConfig{
		AgentEndpoint: agentURL,
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			p, err := svc.Authenticate(tok)
			if err != nil {
				return nil, "", err
			}
			return &backend.Local{Svc: svc, P: p}, r.PathValue("org"), nil
		},
	}))
	// 對外的形象網站(英文版;只有設了 OMIXOR_SITE_DIR 才提供,見 site.go)
	// 有掛上網站時,找不到的頁面回 site/404.html(不影響 /api/、/mcp/、/health 與既有路由)
	if dir := cfg.Str(config.SiteDir); mountSite(mux, dir) {
		return siteNotFound(mux, dir)
	}
	return mux
}

func Run() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		fmt.Println("omixor-server:omixor 的線上服務(沒有旗標,全部用環境變數設定)")
		fmt.Println()
		fmt.Print(config.Usage(config.Server))
		return
	}
	// 設定集中載入並驗證一次(型別、必填、互斥;有問題一次列完);之後不再直接讀環境變數
	cfg := config.MustLoad(config.Server)
	st, err := store.NewPG(cfg.Str(config.DatabaseURL))
	if err != nil {
		log.Fatalf("PostgreSQL:%v", cfg.Redact(err.Error()))
	}
	svc := &service.Service{Store: st, Evoleap: cfg.Str(config.EvoleapURL),
		Bootstrap: cfg.Str(config.AdminBootstrapEmail), TTL: time.Duration(cfg.Int(config.TokenDays)) * 24 * time.Hour}
	if h := cfg.Float(config.AgentCredHours); h > 0 {
		svc.AgentCredTTL = time.Duration(h * float64(time.Hour))
	}
	service.SetTrustedProxyHops(trustedProxyHops(cfg))
	if err := configureLDAP(svc, cfg.Getenv); err != nil {
		log.Fatalf("LDAP 設定:%v", cfg.Redact(err.Error()))
	}
	if err := svc.EnsureOrgsForIssues(); err != nil {
		log.Fatalf("補建組織失敗:%v", err)
	}
	// 例行任務排程:每 60 秒檢查一次,到時間的替角色排任務
	svc.StartRoutines(time.Minute, nil)
	addr := cfg.Str(config.Addr)
	if addr == "" {
		addr = ":" + cfg.Str(config.Port)
	}
	log.Printf("omixor server 在 %s", addr)
	hs := &http.Server{Addr: addr, Handler: HandlerWith(svc, cfg), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(hs.ListenAndServe())
}

// trustedProxyHops 信任的代理層數:明確設了就用;沒設時,跑在 Cloud Run(有 K_SERVICE)是 1,否則 0(不信任 X-Forwarded-For)
func trustedProxyHops(cfg *config.Config) int {
	if cfg.IsSet(config.TrustedProxyHops) {
		return cfg.Int(config.TrustedProxyHops)
	}
	if cfg.Str(config.KService) != "" {
		return 1
	}
	return 0
}
