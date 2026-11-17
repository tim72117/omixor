// omixor-web:omixor console(Go + React)。環境變數的舊名稱 OPS_* 仍相容(見 omixor-core/env)。
//
//	MCP:http://127.0.0.1:3765/mcp(OMIXOR_MCP_PORT 可改),規格和 Swift 版一模一樣
//	畫面:http://127.0.0.1:5191(OMIXOR_WEB_PORT 改連接埠;OMIXOR_WEB_ADDR 改位址,例如 0.0.0.0:5191,必須先啟用 LDAP 登入)
//	資料:OMIXOR_CONSOLE_DATA_DIR,預設 ~/Library/Application Support/omixor-console(舊的 OpsWeb 會自動搬過來);
//	      設了 OMIXOR_DATABASE_URL(與 OMIXOR_SECRET_KEY)就改存 PostgreSQL
//
//	omixor-web import <資料夾>   把資料夾裡的 JSON 檔(例如 macOS App 的 ~/Library/Application Support/Ops)匯入資料庫
//	omixor-web import-issues [-org <組織>] [-overwrite] <資料夾或 issues.json>
//	                         只匯入 Issue 到資料庫的 issues 表(只要 OMIXOR_DATABASE_URL)。資料夾是匯入 orgs/<組織>/issues.json;
//	                         可以重跑:已經有的 id 略過(-overwrite 改成用檔案覆蓋)
//
// macOS App(Swift 版)開著時 3765 會被占用;兩個都要開時,給其中一個設 OMIXOR_MCP_PORT。
package main

import (
	"flag"
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

	"omixor-core/env"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "import-issues" {
		runImportIssues(os.Args[2:])
		return
	}
	if url := env.Get("OMIXOR_DATABASE_URL", "OPS_DATABASE_URL"); url != "" {
		if err := core.OpenDB(url, env.Get("OMIXOR_SECRET_KEY", "OPS_SECRET_KEY")); err != nil {
			log.Fatalf("PostgreSQL:%v", err)
		}
		log.Printf("資料:PostgreSQL")
	}
	if core.DB() == nil && !core.IsRemote() && len(os.Args) < 2 {
		log.Printf("提醒:本機模式要連 PostgreSQL(OMIXOR_DATABASE_URL 與 OMIXOR_SECRET_KEY),沒有的話組織與 Issue 不能用;本機開發可以用 docker compose -f docker-compose.dev.yml up -d 開一個")
	}
	if len(os.Args) >= 2 && os.Args[1] == "import" {
		runImport()
		return
	}

	app := core.New()
	app.Start()

	mcp.Start(app, mcp.ConfiguredPort())
	app.Mu.Lock()
	st := app.MCP
	app.Mu.Unlock()
	if st.State == "failed" {
		log.Printf("MCP 伺服器沒啟動(127.0.0.1:%d):%s。macOS App(Swift 版)開著嗎?可以用 OMIXOR_MCP_PORT 換連接埠", st.Port, st.Error)
	} else {
		log.Printf("MCP:http://127.0.0.1:%d/mcp", st.Port)
	}

	au := auth.New(auth.VaultSecrets{})
	addr := web.ConfiguredAddr()
	// 在 Docker 裡要聽 0.0.0.0,但連接埠只發布到主機的 127.0.0.1 時,設 OMIXOR_WEB_LOCAL_ONLY_PUBLISH=1
	if !web.IsLoopbackAddr(addr) && !au.Enabled() && env.Get("OMIXOR_WEB_LOCAL_ONLY_PUBLISH", "OPS_WEB_LOCAL_ONLY_PUBLISH") != "1" {
		log.Fatalf("畫面要聽 %s(不只本機)時,一定要先啟用 LDAP 登入:先用本機模式開,在 設定 › 認證 設好,再改 OMIXOR_WEB_ADDR", addr)
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
	if env.Get("OMIXOR_NO_BROWSER", "OPS_NO_BROWSER") == "" {
		go func() { time.Sleep(300 * time.Millisecond); _ = core.OpenURLCommand(url).Start() }()
	}
	srv := &http.Server{Handler: web.Handler(app, webui.Dist(), au), ReadHeaderTimeout: 10 * time.Second}
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

func runImportIssues(args []string) {
	fs := flag.NewFlagSet("import-issues", flag.ExitOnError)
	org := fs.String("org", "", "組織 id(給檔案時預設 default;給資料夾時只匯入這個組織)")
	overwrite := fs.Bool("overwrite", false, "資料庫已經有的 id 用檔案內容覆蓋(預設略過)")
	_ = fs.Parse(args)
	url := env.Get("OMIXOR_DATABASE_URL", "OPS_DATABASE_URL")
	if url == "" || fs.NArg() != 1 {
		log.Fatal("用法:OMIXOR_DATABASE_URL=… omixor-web import-issues [-org <組織>] [-overwrite] <資料夾或 issues.json>")
	}
	if err := core.OpenIssueDB(url); err != nil {
		log.Fatalf("PostgreSQL:%v", err)
	}
	res, err := core.ImportIssuesPath(fs.Arg(0), *org, *overwrite)
	for _, r := range res {
		log.Printf("組織 %s:新增 %d、覆蓋 %d、略過 %d", r.Org, r.Inserted, r.Updated, r.Skipped)
	}
	if err != nil {
		log.Fatal(err)
	}
}
