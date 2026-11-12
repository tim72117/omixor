// Package server 啟動 omixor server(核心 + HTTP API)。cmd/omixor-server 與 ops-web 的 cmd/omixor-issues 都呼叫它。
//
// 環境變數:
//
//	PORT                           Cloud Run 給的埠(預設 8080)
//	OMIXOR_ADDR                    監聽位址(預設 :$PORT;本機測試可以設 127.0.0.1:<埠>)
//	OPS_DATABASE_URL               PostgreSQL 網址(Cloud Run 上來自 Secret Manager 的 OMIXOR_DATABASE_URL)
//	OMIXOR_ADMIN_BOOTSTRAP_EMAIL   第一位伺服器管理員的 email(使用者表是空的時才用)
//	OMIXOR_TOKEN_DAYS              token 有效天數(預設 90;0 是不到期)
//	EVOLEAP_URL                    evoleap 網址(預設 https://evoleap.io)
package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"omixor-core/api"
	"omixor-core/service"
	"omixor-core/store"
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
	mux.Handle("/api/", api.New(svc).Handler())
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
	if err := svc.EnsureOrgsForIssues(); err != nil {
		log.Fatalf("補建組織失敗:%v", err)
	}
	addr := env("OMIXOR_ADDR", ":"+env("PORT", "8080"))
	log.Printf("omixor server 在 %s", addr)
	hs := &http.Server{Addr: addr, Handler: Handler(svc), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(hs.ListenAndServe())
}
