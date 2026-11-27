// omixor-mcp:給只支援 stdio 的 MCP 用戶端,把訊息轉給 omixor 的 MCP 入口(console 或 omixor server 的 /mcp);
// 另外提供任務通知頻道與執行者資料夾。支援 HTTP 與 OAuth 的用戶端(例如 Claude Code)可以直接連,不需要這支程式。
//
// 認證:OAuth(授權碼 + PKCE,本機回呼)。先登入一次,之後自動更新;30 天沒用要重新登入。
//
//	omixor-mcp login [--url <…/mcp>] [--no-browser] [--new-client]           統一入口(轉接用)
//	omixor-mcp login --worker --org <組織> [--url <…/mcp>]                   執行者端點(任務頻道用)
//	omixor-mcp logout [--worker --org <組織>] [--url <…/mcp>]
//
// 沒有瀏覽器的機器:加 --no-browser,把印出的網址拿到別台的瀏覽器開,允許後把轉到的 127.0.0.1 網址整串貼回來。
// 登入資料存在 <設定資料夾>/mcp/(只有本人可讀寫;設定資料夾同 omixor CLI,OMIXOR_CONFIG_DIR 可改)。
//
// 網址:--url 或 OMIXOR_MCP_URL(結尾是 /mcp);都沒有是本機 console 的 http://127.0.0.1:<OMIXOR_WEB_PORT>/mcp。
// 網址要和伺服器公告的完全相同(有 OMIXOR_PUBLIC_URL 的伺服器要用那個網址)。
//
// 轉接(預設):一行一則 JSON-RPC 訊息(MCP stdio 規格);統一入口沒有「目前組織」,工具呼叫要帶 org。
//
//	omixor-mcp [--url <…/mcp>]
//
// 任務通知頻道(給 Claude Code 的 channels,見 omixor-core/mcp/channel.go):定時讀執行者端點 /mcp/worker/{org} 的待領取任務,
// 有新的就推進 Claude Code 工作階段。Claude Code 要用 claude --dangerously-load-development-channels server:<.mcp.json 裡的名稱> 啟動。
//
//	omixor-mcp --channel --org <組織> [--url <…/mcp>] [--agent 角色] [--interval 30s] [--auto]
//
// 產生執行者資料夾(CLAUDE.md 長期授權 + .mcp.json 註冊頻道與執行者端點;資料夾裡沒有任何 token):
//
//	omixor-mcp --init-worker <資料夾> --org <組織> [--url <…/mcp>] [--agent 角色] [--repo <網址>]… [--no-subagent] [--force]
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"omixor-core/config"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-web/internal/core"
	"omixor-web/internal/mcpauth"
)

const usage = "用法:omixor-mcp [login|logout] … | omixor-mcp [--url …] | --channel --org <組織> … | --init-worker <資料夾> --org <組織> …(說明見 console/README.md)"

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(usage)
		fmt.Println()
		fmt.Print(config.Usage(config.MCP))
		return
	}
	cfg := config.MustLoad(config.MCP)
	app := newCLI(cfg)
	if err := app.run(args); err != nil {
		logf("%v", err)
		os.Exit(1)
	}
}

type cli struct {
	// base 統一入口網址(…/mcp),--url 可以蓋掉
	base  string
	store mcpauth.Store
}

func newCLI(cfg *config.Config) *cli {
	base := cfg.Str(config.MCPURL)
	if base == "" {
		base = "http://127.0.0.1:" + cfg.Str(config.WebPort) + "/mcp"
	}
	return &cli{base: base, store: mcpauth.Store{Dir: filepath.Join(configDir(cfg), "mcp")}}
}

// configDir 和 omixor CLI 同一個設定資料夾
func configDir(cfg *config.Config) string {
	if d := cfg.Str(config.ConfigDir); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "omixor")
	}
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "omixor")
}

func (c *cli) run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		switch args[0] {
		case "login", "logout", "--channel", "--init-worker":
			cmd, args = args[0], args[1:]
		}
	}
	switch cmd {
	case "login":
		return c.login(args)
	case "logout":
		return c.logout(args)
	case "--channel":
		return c.channel(args)
	case "--init-worker":
		return c.initWorker(args)
	}
	return c.bridge(args)
}

// flags 每個子命令共用 --url
func (c *cli) flags(name string) (*flag.FlagSet, *string) {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	return f, f.String("url", c.base, "MCP 入口網址(…/mcp)")
}

// resource 要登入的端點:統一入口,或(worker)執行者端點
func resource(base, org string, worker bool) (string, error) {
	base = strings.TrimRight(base, "/")
	if !worker {
		if org != "" {
			return "", errors.New("--org 只和 --worker 一起用(統一入口每次呼叫自己帶組織)")
		}
		return base, nil
	}
	if !model.ValidOrgID(org) {
		return "", errors.New("要用 --org 指定組織(組織 id,例如 --org lab)")
	}
	if !strings.HasSuffix(base, "/mcp") {
		return "", fmt.Errorf("MCP 入口網址要以 /mcp 結尾:%s", base)
	}
	return base + "/worker/" + org, nil
}

// loginCommand 登入某個端點要執行的指令(錯誤訊息用)
func loginCommand(res string) string {
	if base, org, ok := strings.Cut(res, "/worker/"); ok {
		return "omixor-mcp login --worker --org " + org + " --url " + base
	}
	return "omixor-mcp login --url " + res
}

func (c *cli) client(res string) *mcpauth.Client {
	return &mcpauth.Client{Store: c.store, Resource: res}
}

// tokenFor 目前的 access token;要重新登入時的錯誤帶上指令
func tokenFor(cl *mcpauth.Client, rejected string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tok, err := cl.Renew(ctx, rejected)
	if errors.Is(err, mcpauth.ErrLoginRequired) {
		return "", fmt.Errorf("%v;請執行:%s", err, loginCommand(cl.Resource))
	}
	return tok, err
}

func (c *cli) login(args []string) error {
	f, u := c.flags("login")
	worker := f.Bool("worker", false, "執行者端點(任務頻道用)")
	org := f.String("org", "", "組織(和 --worker 一起用)")
	noBrowser := f.Bool("no-browser", false, "不開瀏覽器,只印網址(可以把轉到的網址貼回來)")
	newClient := f.Bool("new-client", false, "重新向伺服器註冊用戶端")
	if err := f.Parse(args); err != nil {
		return err
	}
	res, err := resource(*u, *org, *worker)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	o := mcpauth.LoginOptions{Out: os.Stderr, Paste: os.Stdin, NewClient: *newClient}
	if !*noBrowser {
		o.Open = func(url string) error { return core.OpenURLCommand(url).Start() }
	}
	return c.client(res).Login(ctx, o)
}

func (c *cli) logout(args []string) error {
	f, u := c.flags("logout")
	worker := f.Bool("worker", false, "執行者端點")
	org := f.String("org", "", "組織(和 --worker 一起用)")
	if err := f.Parse(args); err != nil {
		return err
	}
	res, err := resource(*u, *org, *worker)
	if err != nil {
		return err
	}
	if err := c.client(res).Logout(); err != nil {
		return err
	}
	logf("已清掉 %s 的登入資料", res)
	return nil
}

// ---- 轉接 ----

type bridge struct {
	http     *http.Client
	endpoint string
	token    func(rejected string) (string, error)
}

func (c *cli) bridge(args []string) error {
	f, u := c.flags("omixor-mcp")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() > 0 {
		return errors.New(usage)
	}
	res := strings.TrimRight(*u, "/")
	cl := c.client(res)
	b := &bridge{http: mcpauth.HTTPClient(res, 60*time.Second), endpoint: res, token: func(rejected string) (string, error) { return tokenFor(cl, rejected) }}
	logf("轉接到 %s", res)
	out := bufio.NewWriter(os.Stdout)
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			b.handle([]byte(trimmed), out)
		}
		if err != nil {
			if err != io.EOF {
				logf("讀取失敗:%v", err)
			}
			return nil
		}
	}
}

func (b *bridge) post(body []byte, tok string) (int, []byte, error) {
	req, err := http.NewRequest("POST", b.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := b.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func (b *bridge) handle(body []byte, out *bufio.Writer) {
	var msg map[string]any
	_ = json.Unmarshal(body, &msg)
	id, hasID := msg["id"]
	fail := func(m string) {
		logf("%s", m)
		if hasID {
			write(out, errorReply(id, m))
		}
	}
	tok, err := b.token("")
	if err != nil { // 沒有可用的登入就不送(不做沒有驗證的連線)
		fail(err.Error())
		return
	}
	status, data, err := b.post(body, tok)
	if err == nil && status == 401 { // access token 被拒:換一個再試一次
		if tok, err = b.token(tok); err != nil {
			fail(err.Error())
			return
		}
		status, data, err = b.post(body, tok)
	}
	switch {
	case err != nil:
		logf("無法連線:%v", err)
		fail(fmt.Sprintf("無法連到 omixor(%s)。請確認 console 或 server 已經啟動,再重試。", b.endpoint))
	case status == 202 || len(data) == 0 && status < 300:
	case status == 200:
		write(out, data)
	case status == 401:
		fail("omixor 拒絕了這次登入(HTTP 401)。請執行:" + loginCommand(b.endpoint))
	default:
		fail(fmt.Sprintf("omixor 回應 HTTP %d:%s", status, bytes.TrimSpace(data)))
	}
}

func errorReply(id any, message string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": message}})
	return b
}

// 回應必須是單行
func write(out *bufio.Writer, data []byte) {
	data = bytes.ReplaceAll(bytes.ReplaceAll(data, []byte("\n"), nil), []byte("\r"), nil)
	out.Write(data)
	out.WriteByte('\n')
	out.Flush()
}

func logf(format string, a ...any) { fmt.Fprintf(os.Stderr, "[omixor-mcp] "+format+"\n", a...) }

// ---- 任務通知頻道 ----

// channelSource 頻道讀任務用的來源:執行者端點,token 每次請求前取(快到期就更新);現在就沒有可用的登入時回錯誤
func (c *cli) channelSource(res string) (*coremcp.WorkerTasks, error) {
	cl := c.client(res)
	get := func() (string, error) { return tokenFor(cl, "") }
	if _, err := get(); err != nil {
		return nil, err
	}
	return &coremcp.WorkerTasks{URL: res, TokenFunc: get, HTTP: mcpauth.HTTPClient(res, 20*time.Second)}, nil
}

// channel stdout 是 MCP 協定,訊息只寫 stderr
func (c *cli) channel(args []string) error {
	f, u := c.flags("channel")
	org := f.String("org", "", "要領任務的組織(必填)")
	agent := f.String("agent", "", "只通知這個角色的任務")
	interval := f.Duration("interval", 30*time.Second, "查詢間隔(最少 5 秒)")
	auto := f.Bool("auto", false, "指示 Claude 收到就領取處理(仍需使用者在工作階段授權)")
	if err := f.Parse(args); err != nil {
		return err
	}
	res, err := resource(*u, *org, true)
	if err != nil {
		return err
	}
	src, err := c.channelSource(res) // 沒登入就不開頻道
	if err != nil {
		return err
	}
	logf("任務頻道:%s", res)
	lg := log.New(os.Stderr, "[omixor-mcp] ", log.LstdFlags)
	return coremcp.RunChannel(context.Background(), os.Stdin, os.Stdout, coremcp.ChannelConfig{
		Tasks: src, Org: *org, Server: "omixor(" + res + ")", Worker: true,
		AgentID: *agent, Interval: *interval, Auto: *auto, Logf: lg.Printf,
	})
}

// ---- 執行者資料夾 ----

// initWorker 產生執行者資料夾;已有檔案時要 --force 才覆蓋。不需要先登入,資料夾裡也不放任何 token
func (c *cli) initWorker(args []string) error {
	f, u := c.flags("init-worker")
	org := f.String("org", "", "要領任務的組織(必填)")
	agent := f.String("agent", "", "只接這個角色的任務")
	noSub := f.Bool("no-subagent", false, "不用子代理,直接在工作階段裡執行")
	force := f.Bool("force", false, "覆蓋已有的 CLAUDE.md、.mcp.json、.claude/settings.json")
	var repos []string
	f.Func("repo", "可以讀的公開 repo(https 網址,例如 https://github.com/owner/name.git;可重複給)", func(s string) error {
		repos = append(repos, s)
		return nil
	})
	// 資料夾放最前面或最後面都可以
	var dir string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir, args = args[0], args[1:]
	}
	if err := f.Parse(args); err != nil {
		return err
	}
	if dir == "" && f.NArg() > 0 {
		dir = f.Arg(0)
	}
	if dir == "" {
		return errors.New("用法:omixor-mcp --init-worker <資料夾> --org <組織> [--agent 角色] [--repo <公開 repo 的 https 網址>]... [--no-subagent] [--force]")
	}
	res, err := resource(*u, *org, true)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	base := strings.TrimRight(*u, "/")
	chArgs := []string{"--channel", "--auto", "--org", *org, "--url", base}
	if *agent != "" {
		chArgs = append(chArgs, "--agent", *agent)
	}
	files, err := coremcp.WorkerFiles(coremcp.WorkerOptions{
		ChannelCommand: filepath.ToSlash(exe), ChannelArgs: chArgs, MCPURL: res,
		Where: "omixor(" + res + ")", AgentID: *agent, Subagent: !*noSub, Repos: repos,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if !*force {
		for name := range files {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return fmt.Errorf("%s 已經有 %s;要覆蓋請加 --force", dir, name)
			}
		}
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // .claude/settings.json
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	abs, _ := filepath.Abs(dir)
	fmt.Printf("已建立執行者資料夾:%s\n接著:\n  %s\n  cd \"%s\"\n  %s\n進入 Claude Code 後用 /mcp 授權 omixor 這個伺服器(第一次)。\n",
		abs, loginCommand(res), abs, coremcp.WorkerStartCommand())
	return nil
}
