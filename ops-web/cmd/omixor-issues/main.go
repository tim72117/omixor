// omixor-issues:omixor 的 Issue 服務(部署在 Cloud Run,資料存 Cloud SQL 的 omixor 資料庫),Issue 唯一的資料來源。
// 授權由 omixor 自己管(internal/omixor):用 evoleap 登入證明身分,換 omixor 自己的 token;
// 使用者名單、角色、token 與操作紀錄都在 omixor 的資料庫。資料維護(匯入、匯出、比對)也走這裡的 API,不直連資料庫。
//
//	GET  /health        健康檢查(不用登入;Cloud Run 保留 /healthz,不能用)
//	     /api/…         見 internal/omixor/omixor.go
//
// 環境變數:
//
//	PORT                           Cloud Run 給的埠(預設 8080)
//	OMIXOR_ADDR                    監聽位址(預設 :$PORT;本機測試可以設 127.0.0.1:<埠>)
//	OPS_DATABASE_URL               PostgreSQL 網址(Cloud Run 上來自 Secret Manager 的 OMIXOR_DATABASE_URL)
//	OMIXOR_ADMIN_BOOTSTRAP_EMAIL   第一位管理員的 email(使用者表是空的時才用)
//	OMIXOR_TOKEN_DAYS              token 有效天數(預設 90;0 是不到期)
//	EVOLEAP_URL                    evoleap 網址(預設 https://evoleap.io)
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"opsweb/internal/core"
	"opsweb/internal/omixor"
	"opsweb/internal/web"
)

func main() {
	url := os.Getenv("OPS_DATABASE_URL")
	if url == "" {
		log.Fatal("要設 OPS_DATABASE_URL")
	}
	if os.Getenv("OPS_DATA_DIR") == "" {
		dir, _ := os.MkdirTemp("", "omixor-issues")
		os.Setenv("OPS_DATA_DIR", dir)
	}
	os.Setenv("OPS_NO_TIMERS", "1")
	if err := core.OpenIssueDB(url); err != nil {
		log.Fatalf("PostgreSQL:%v", err)
	}
	store, err := omixor.NewPGStore()
	if err != nil {
		log.Fatalf("建表失敗:%v", err)
	}
	app := core.New()
	app.Mu.Lock()
	app.LoadOrgData()
	app.Mu.Unlock()

	days := 90
	if v := os.Getenv("OMIXOR_TOKEN_DAYS"); v != "" {
		days, _ = strconv.Atoi(v)
	}
	srv := &omixor.Server{
		Store: store, Issues: omixor.PGIssues{}, IssueAPI: web.IssueAPI(app),
		Evoleap: envOr("EVOLEAP_URL", "https://evoleap.io"), Bootstrap: os.Getenv("OMIXOR_ADMIN_BOOTSTRAP_EMAIL"),
		TTL: time.Duration(days) * 24 * time.Hour,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	mux.Handle("/api/", srv.Handler())

	addr := envOr("OMIXOR_ADDR", ":"+envOr("PORT", "8080"))
	log.Printf("omixor-issues 在 %s", addr)
	hs := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(hs.ListenAndServe())
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
