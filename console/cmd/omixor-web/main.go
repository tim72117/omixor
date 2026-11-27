// omixor-web:omixor console(Go + React)。環境變數集中定義在 omixor-core/config(說明見 docs/configuration.md,-h 也會列出),舊名稱 OPS_* 仍相容。
//
//	MCP:畫面連接埠的 /mcp、/oauth/*(和 omixor server 同一套;OAuth 授權,啟用 LDAP 時可用 LDAP 帳號;需要 PostgreSQL)。
//	      有 OMIXOR_PUBLIC_URL 時對外服務;沒有時只收這台電腦直接連進來的(http://127.0.0.1:<畫面連接埠>/mcp),見 internal/mcp/entry.go
//	對外 MCP:設了 OMIXOR_OAUTH_ADDR(例如 127.0.0.1:8771)才開,端點與認證同 omixor server(/mcp、/mcp/org/{org}、/mcp/agent、/mcp/worker/{org};OAuth 登入或 omx_ token);見 console/README.md
//	REST API:畫面連接埠另有和 omixor server 同一套的 /api/orgs…(開放範圍見 internal/web/coreapi.go)
//	畫面:http://127.0.0.1:5191(OMIXOR_WEB_PORT 改連接埠;OMIXOR_WEB_ADDR 改位址,例如 0.0.0.0:5191,必須先啟用 LDAP 登入)
//	資料:OMIXOR_CONSOLE_DATA_DIR,預設 ~/Library/Application Support/omixor-console(舊的 OpsWeb 會自動搬過來);
//	      設了 OMIXOR_DATABASE_URL(與 OMIXOR_SECRET_KEY)就改存 PostgreSQL
//
//	omixor-web leave-remote  以前設定成遠端模式(已移除)的安裝:確認改用自己的資料庫(不刪任何資料),之後才啟動得了
//	omixor-web import <資料夾>   把資料夾裡的 JSON 檔(例如 macOS App 的 ~/Library/Application Support/Ops)匯入資料庫
//	omixor-web import-issues [-org <組織>] [-overwrite] <資料夾或 issues.json>
//	                         只匯入 Issue 到資料庫的 issues 表(只要 OMIXOR_DATABASE_URL)。資料夾是匯入 orgs/<組織>/issues.json;
//	                         可以重跑:已經有的 id 略過(-overwrite 改成用檔案覆蓋)
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
	"omixor-web/internal/web"
	webui "omixor-web/ui"

	"omixor-core/config"
	"omixor-core/service"
)

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		fmt.Println("用法:omixor-web [leave-remote | import <資料夾> | import-issues [-org <組織>] [-overwrite] <資料夾或 issues.json>]")
		fmt.Println()
		fmt.Print(config.Usage(config.Web))
		return
	}
	// 設定集中載入並驗證一次(有問題一次列完後結束);之後不再直接讀環境變數
	cfg := config.MustLoad(config.Web)
	// 本機使用者(omixor-console)要在建核心 store(啟動遷移)之前告訴遷移:舊資料裡的 mcp、tim、ops-web、omixor-console 才會都對到它的 actor
	core.ConfigureLocalIdentity()
	if len(os.Args) >= 2 && os.Args[1] == "import-issues" {
		runImportIssues(os.Args[2:], cfg)
		return
	}
	if url := cfg.Str(config.DatabaseURL); url != "" {
		if err := core.OpenDB(url, cfg.Str(config.SecretKey)); err != nil {
			log.Fatalf("PostgreSQL:%v", cfg.Redact(err.Error()))
		}
		log.Printf("資料:PostgreSQL")
	}
	if len(os.Args) >= 2 && os.Args[1] == "leave-remote" {
		if err := core.LeaveRemoteMode(); err != nil {
			log.Fatal(err)
		}
		log.Print("已改用這台 console 自己的資料,可以啟動了(以前遠端模式的設定與登入都沒有刪)")
		return
	}
	if core.DB() == nil && len(os.Args) < 2 {
		log.Printf("提醒:console 要連 PostgreSQL(OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY),沒有的話組織與 Issue 不能用;本機開發可以用 docker compose -f docker-compose.dev.yml up -d 開一個")
	}
	if len(os.Args) >= 2 && os.Args[1] == "import" {
		runImport()
		return
	}
	// 以前設定成遠端模式(已移除)的安裝不能悄悄開出另一份看板,見 core/remote_removed.go
	if err := core.CheckRemovedRemoteMode(); err != nil {
		log.Fatal(err)
	}
	if cfg.IsSet(config.ServerURL) {
		log.Print("提醒:omixor-web 不再使用 OMIXOR_SERVER(遠端模式已移除),可以拿掉")
	}
	// 對外 MCP 連接埠(OMIXOR_OAUTH_ADDR)明確啟用卻條件不合就不啟動:要在建核心之前,核心的 service 才會帶上登入設定
	if err := mcp.PrepareOAuth(cfg); err != nil {
		log.Fatal(err)
	}

	app := core.New()
	// 自動執行要連的角色端點(畫面連接埠上的 /mcp/agent):背景工作啟動前就要設好
	app.AgentLocalEndpoint = mcp.LocalBaseURL(cfg) + "/mcp/agent"
	app.Start()

	// 登入的每來源頻率限制(網頁與 MCP 授權同一份)在反向代理後面要照信任的代理層數取來源位址
	service.SetTrustedProxyHops(cfg.ProxyHops())
	au := auth.New(auth.VaultSecrets{})
	if err := mcp.StartOAuth(app, cfg, au.LDAPSettings); err != nil {
		log.Fatal(err)
	}
	for _, w := range au.Config().Warnings() {
		log.Printf("警告:%s", w)
	}
	if au.Enabled() {
		for _, w := range core.UpgradeLoginWarnings(au.Config().AdminList()) {
			log.Printf("提醒:%s", w)
		}
	}
	addr := web.ConfiguredAddr()
	// 在 Docker 裡要聽 0.0.0.0,但連接埠只發布到主機的 127.0.0.1 時,設 OMIXOR_WEB_LOCAL_ONLY_PUBLISH=1
	if !web.IsLoopbackAddr(addr) && !au.Enabled() && !cfg.Bool(config.WebLocalOnlyPublish) {
		log.Fatalf("畫面要聽 %s(不只本機)時,一定要先啟用 LDAP 登入:先只聽本機位址開,在 設定 › 認證 設好,再改 OMIXOR_WEB_ADDR", addr)
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		log.Fatalf("畫面的連接埠 %s 開不了:%v(用 OMIXOR_WEB_PORT 換一個)", addr, err)
	}
	url := "http://" + addr
	if !web.IsLoopbackAddr(addr) {
		log.Printf("畫面開放給網路上的其他電腦,建議放在 HTTPS 反向代理後面(cookie 才會加上 Secure)。允許的主機名稱:OMIXOR_WEB_HOSTS")
	}
	log.Printf("畫面:%s", url)
	if core.DB() == nil {
		log.Printf("資料:%s", core.DataRoot())
	}
	// 結束前把範例資料的變更存起來
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		app.SaveNow()
		os.Exit(0)
	}()
	if !cfg.Bool(config.NoBrowser) {
		go func() { time.Sleep(300 * time.Millisecond); _ = core.OpenURLCommand(url).Start() }()
	}
	handler := web.Handler(app, webui.Dist(), au)
	// MCP 入口和畫面同一個連接埠(哪些請求收得到依部署情況,見 mcp.ScreenEntry)
	handler = web.WithMCPEntry(mcp.ScreenEntry(app, cfg, au.LDAPSettings), handler)
	handler = web.WarnUntrustedProxy(handler, cfg.ProxyHops())
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}

func runImport() {
	if core.DB() == nil {
		log.Fatal("要先設 OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY")
	}
	if len(os.Args) < 3 {
		log.Fatal("用法:omixor-web import <資料夾>")
	}
	done, err := core.ImportDir(os.Args[2])
	for _, p := range done {
		log.Printf("匯入 %s", p)
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("完成,共 %d 個檔案。鑰匙圈裡的登入沒辦法搬,連線要在畫面上重新登入", len(done))
}

func runImportIssues(args []string, cfg *config.Config) {
	fs := flag.NewFlagSet("import-issues", flag.ExitOnError)
	org := fs.String("org", "", "組織 id(給檔案時預設 default;給資料夾時只匯入這個組織)")
	overwrite := fs.Bool("overwrite", false, "資料庫已經有的 id 用檔案內容覆蓋(預設略過)")
	_ = fs.Parse(args)
	url := cfg.Str(config.DatabaseURL)
	if url == "" || fs.NArg() != 1 {
		log.Fatal("用法:OMIXOR_DATABASE_URL=… omixor-web import-issues [-org <組織>] [-overwrite] <資料夾或 issues.json>")
	}
	if err := core.OpenIssueDB(url); err != nil {
		log.Fatalf("PostgreSQL:%v", cfg.Redact(err.Error()))
	}
	res, err := core.ImportIssuesPath(fs.Arg(0), *org, *overwrite)
	for _, r := range res {
		log.Printf("組織 %s:新增 %d、覆蓋 %d、略過 %d", r.Org, r.Inserted, r.Updated, r.Skipped)
	}
	if err != nil {
		log.Fatal(err)
	}
}
