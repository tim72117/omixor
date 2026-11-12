// Package server 啟動 omixor server(omixor-core 的 HTTP API + 健康檢查),引用 omixor-core 的 api、service、store。cmd/omixor-server 呼叫它。
//
// 環境變數:
//
//	PORT                           Cloud Run 給的埠(預設 8080)
//	OMIXOR_ADDR                    監聽位址(預設 :$PORT;本機測試可以設 127.0.0.1:<埠>)
//	OPS_DATABASE_URL               PostgreSQL 網址(Cloud Run 上來自 Secret Manager 的 OMIXOR_DATABASE_URL)
//	OMIXOR_ADMIN_BOOTSTRAP_EMAIL   第一位伺服器管理員的 email(使用者表是空的時才用)
//	OMIXOR_TOKEN_DAYS              token 有效天數(預設 90;0 是不到期)
//	EVOLEAP_URL                    evoleap 網址(預設 https://evoleap.io)
//	OMIXOR_AGENT_CRED_HOURS        角色憑證有效小時數(預設 2)
//	OMIXOR_PUBLIC_URL              對外網址(例如 https://omixor.example.com);設了,領取任務時會附上角色端點網址
//
// 路由:
//
//	/health                        健康檢查
//	/api/…                         核心 HTTP API(core/api)
//	/mcp/agent                     角色端點:Authorization: Bearer <agentId>:<secret>(領取任務時發的憑證),白名單工具(含文件櫃,依角色的 docAccess)
//	/mcp/worker/{org}              執行者專用 MCP(只有 dispatch_queue/claim/status/release):認證同 /mcp/org/{org}
//	/mcp/org/{org}                 管理用 MCP(組織 {org} 的 Issue、角色、任務、文件櫃):Authorization: Bearer omx_…(omixor token),權限同該使用者
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"strconv"
	"time"

	"omixor-core/api"
	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/version"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Handler /health 加上核心 API
func Handler(svc *service.Service) http.Handler {
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
	public := strings.TrimRight(os.Getenv("OMIXOR_PUBLIC_URL"), "/")
	agentURL := ""
	if public != "" {
		agentURL = public + "/mcp/agent"
	}
	mux.Handle("/mcp/agent", mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc}))
	mux.Handle("/mcp/org/{org}", mcp.AdminEndpoint(mcp.AdminEndpointConfig{
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
	return mux
}

func Run() {
	url := os.Getenv("OPS_DATABASE_URL")
	if url == "" {
		log.Fatal("要設 OPS_DATABASE_URL")
	}
	st, err := store.NewPG(url)
	if err != nil {
		log.Fatalf("PostgreSQL:%v", err)
	}
	days, _ := strconv.Atoi(env("OMIXOR_TOKEN_DAYS", "90"))
	svc := &service.Service{Store: st, Evoleap: env("EVOLEAP_URL", "https://evoleap.io"),
		Bootstrap: os.Getenv("OMIXOR_ADMIN_BOOTSTRAP_EMAIL"), TTL: time.Duration(days) * 24 * time.Hour}
	if h, err := strconv.ParseFloat(os.Getenv("OMIXOR_AGENT_CRED_HOURS"), 64); err == nil && h > 0 {
		svc.AgentCredTTL = time.Duration(h * float64(time.Hour))
	}
	if err := svc.EnsureOrgsForIssues(); err != nil {
		log.Fatalf("補建組織失敗:%v", err)
	}
	// 例行任務排程:每 60 秒檢查一次,到時間的替角色排任務
	svc.StartRoutines(time.Minute, nil)
	addr := env("OMIXOR_ADDR", ":"+env("PORT", "8080"))
	log.Printf("omixor server 在 %s", addr)
	hs := &http.Server{Addr: addr, Handler: Handler(svc), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(hs.ListenAndServe())
}
