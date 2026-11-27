// conformance-mcp:只給 conformance/ 比對用的開發工具,把 console 的完整工具集(不分身分、不用登入)開在 127.0.0.1。
// 正式的 console 沒有這個端點(MCP 只走畫面連接埠的 OAuth 入口)。只用暫存資料夾裡的範本資料:設了資料庫就拒絕啟動。
//
//	go run ./tools/conformance-mcp -port 3790
package main

import (
	"flag"
	"log"
	"net/http"
	"strconv"
	"time"

	"omixor-core/config"
	coremcp "omixor-core/mcp"
	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
)

func main() {
	port := flag.Int("port", 0, "要聽的連接埠(只聽 127.0.0.1)")
	flag.Parse()
	cfg := config.MustLoad(config.Web)
	if *port <= 0 {
		log.Fatal("要給 -port")
	}
	if cfg.Str(config.DatabaseURL) != "" {
		log.Fatal("這個工具不驗證身分,只能用範本資料:不要設 OMIXOR_DATABASE_URL 或 OMIXOR_SERVER")
	}
	core.ConfigureLocalIdentity()
	app := core.New()
	app.Start()
	srv := &http.Server{Addr: "127.0.0.1:" + strconv.Itoa(*port), ReadHeaderTimeout: 10 * time.Second,
		Handler: coremcp.HostGuard("", mcp.HTTPHandler(&mcp.Handler{App: app}))}
	log.Printf("conformance 用的 MCP:http://%s/mcp", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
