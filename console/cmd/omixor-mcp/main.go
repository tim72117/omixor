// omixor-mcp:把 stdio 上的 MCP 訊息轉給執行中的 omixor console(http://127.0.0.1:3765/mcp)。和 Swift 版的 omixor-mcp 行為一樣。
// 給只支援 stdio 的 MCP 用戶端用;支援 HTTP 的用戶端(例如 Claude Code)可以直接連。
//
// 一行一則 JSON-RPC 訊息(MCP stdio 規格)。omixor console 沒開時,請求會收到 JSON-RPC 錯誤。
// 連線位址:OMIXOR_MCP_URL,或 OMIXOR_MCP_PORT 指定連接埠(舊名稱 OPS_MCP_URL、OPS_MCP_PORT 仍相容)。
//
// 驗證:console 的 MCP 連接埠一律要 Authorization: Bearer <權杖>,這支程式(轉接、--channel、--init-worker)都會帶。
// 權杖的來源優先序:環境變數 OMIXOR_CONSOLE_MCP_TOKEN → console 資料夾裡的 mcp_token(console 第一次啟動時產生,權限 0600);
// 兩處都讀不到就明確報錯,不會沒帶權杖硬連。
//
// 任務通知頻道模式(給 Claude Code 的 channels,見 omixor-core/mcp/channel.go):
//
//	omixor-mcp --channel [--agent 角色] [--interval 30s] [--auto]
//
// 不轉接,改成定時讀這台 console 目前組織的待領取任務,有新的就推進 Claude Code 工作階段。
// Claude Code 要用 claude --dangerously-load-development-channels server:<.mcp.json 裡的名稱> 啟動。
//
// 產生執行者資料夾(CLAUDE.md 長期授權 + .mcp.json 註冊頻道與 omixor MCP;範本見 omixor-core/mcp/worker_template.go):
//
//	omixor-mcp --init-worker <資料夾> [--agent 角色] [--no-subagent] [--force]
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
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omixor-core/config"
	coremcp "omixor-core/mcp"
	"omixor-web/internal/core"
	"omixor-web/internal/mcptoken"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Println("用法:omixor-mcp [--channel … | --init-worker <資料夾> …](說明見 console/cmd/omixor-mcp/main.go 開頭)")
		fmt.Println()
		fmt.Print(config.Usage(config.MCP))
		return
	}
	cfg := config.MustLoad(config.MCP)
	endpoint := cfg.Str(config.MCPURL)
	if endpoint == "" {
		port := cfg.Str(config.MCPPort)
		endpoint = "http://127.0.0.1:" + port + "/mcp"
	}
	// 不走系統代理
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: nil}}
	tokens := tokenSource{cfg: cfg, dir: core.DataRoot}
	if len(os.Args) > 1 && os.Args[1] == "--init-worker" {
		if err := initWorker(endpoint, tokens, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--channel" {
		runChannel(endpoint, tokens, os.Args[2:])
		return
	}
	logf("轉接到 %s", endpoint)

	out := bufio.NewWriter(os.Stdout)
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			handle(client, endpoint, tokens, []byte(trimmed), out)
		}
		if err != nil {
			if err != io.EOF {
				logf("讀取失敗:%v", err)
			}
			return
		}
	}
}

// tokenSource 找 console 的 MCP 權杖:環境變數 OMIXOR_CONSOLE_MCP_TOKEN → console 資料夾的 mcp_token。
// 每次都重讀(console 換新權杖後,轉接不用重開就跟得上)。
type tokenSource struct {
	cfg *config.Config
	dir func() string // console 資料夾
}

const tokenHelp = "取得方式:開 omixor console 的 MCP 設定頁(管理員)查看權杖,設成環境變數 OMIXOR_CONSOLE_MCP_TOKEN;或確認 console 資料夾裡有 mcp_token(console 第一次啟動時產生)"

func (t tokenSource) get() (string, error) {
	env, set, err := mcptoken.FromEnv(t.cfg.Str(config.MCPToken))
	if err != nil {
		return "", err
	}
	if set {
		return env, nil
	}
	dir := t.dir()
	tok, err := mcptoken.ReadFile(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("找不到 console 的 MCP 權杖(OMIXOR_CONSOLE_MCP_TOKEN 沒設,%s 也不存在)。%s", mcptoken.Path(dir), tokenHelp)
		}
		return "", fmt.Errorf("讀不到 console 的 MCP 權杖:%v。%s", err, tokenHelp)
	}
	return tok, nil
}

func handle(client *http.Client, endpoint string, tokens tokenSource, body []byte, out *bufio.Writer) {
	var msg map[string]any
	_ = json.Unmarshal(body, &msg)
	id, hasID := msg["id"]
	tok, err := tokens.get()
	if err != nil { // 沒有權杖就不送(不做沒有驗證的連線);有 id 的請求回錯誤讓用戶端看得到原因
		logf("%v", err)
		if hasID {
			write(out, errorReply(id, err.Error()))
		}
		return
	}
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := client.Do(req)
	if err != nil {
		logf("無法連線:%v", err)
		if hasID {
			write(out, errorReply(id, fmt.Sprintf("無法連到 omixor console(%s)。請先開啟 omixor console,再重試。", endpoint)))
		}
		return
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 202 || len(data) == 0 {
		return
	}
	if resp.StatusCode == 200 {
		write(out, data)
		return
	}
	logf("HTTP %d: %s", resp.StatusCode, data)
	if hasID {
		msg := fmt.Sprintf("omixor console 回應 HTTP %d:%s", resp.StatusCode, data)
		if resp.StatusCode == 401 {
			msg = "omixor console 拒絕了 MCP 權杖(HTTP 401):權杖不對或已換新。" + tokenHelp
		}
		write(out, errorReply(id, msg))
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

// runChannel 任務通知頻道:stdout 是 MCP 協定,訊息只寫 stderr
func runChannel(endpoint string, tokens tokenSource, args []string) {
	fs := flag.NewFlagSet("channel", flag.ExitOnError)
	agent := fs.String("agent", "", "只通知這個角色的任務")
	interval := fs.Duration("interval", 30*time.Second, "查詢間隔(最少 5 秒)")
	auto := fs.Bool("auto", false, "指示 Claude 收到就領取處理(仍需使用者在工作階段授權)")
	_ = fs.Parse(args)
	src, err := channelSource(endpoint, tokens)
	if err != nil { // 沒權杖就不開頻道(不做沒有驗證的連線)
		logf("%v", err)
		os.Exit(1)
	}
	// 組織每次查詢前重讀:console 晚開、中途換組織都跟得上
	logf("任務頻道:%s(組織跟著 console 目前的組織)", endpoint)
	lg := log.New(os.Stderr, "[omixor-mcp] ", log.LstdFlags)
	if err := coremcp.RunChannel(context.Background(), os.Stdin, os.Stdout, coremcp.ChannelConfig{
		Tasks: src, OrgFunc: src.CurrentOrg, Server: "本機 omixor console(" + endpoint + ")",
		AgentID: *agent, Interval: *interval, Auto: *auto, Logf: lg.Printf,
	}); err != nil {
		logf("%v", err)
		os.Exit(1)
	}
}

// channelSource 頻道讀任務用的 MCP 用戶端:帶 console 的權杖(每次請求前重讀,換新後跟得上);現在就沒有權杖時回傳錯誤
func channelSource(endpoint string, tokens tokenSource) (*coremcp.MCPTasks, error) {
	if _, err := tokens.get(); err != nil {
		return nil, err
	}
	return &coremcp.MCPTasks{URL: endpoint, TokenFunc: tokens.get}, nil
}

// initWorker 產生執行者資料夾;已有檔案時要 --force 才覆蓋
func initWorker(endpoint string, tokens tokenSource, args []string) error {
	fs := flag.NewFlagSet("init-worker", flag.ContinueOnError)
	agent := fs.String("agent", "", "只接這個角色的任務")
	noSub := fs.Bool("no-subagent", false, "不用子代理,直接在工作階段裡執行")
	force := fs.Bool("force", false, "覆蓋已有的 CLAUDE.md、.mcp.json、.claude/settings.json")
	var repos []string
	fs.Func("repo", "可以讀的公開 repo(https 網址,例如 https://github.com/owner/name.git;可重複給)", func(s string) error {
		repos = append(repos, s)
		return nil
	})
	// 資料夾放最前面或最後面都可以
	var dir string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if dir == "" && fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if dir == "" {
		return fmt.Errorf("用法:omixor-mcp --init-worker <資料夾> [--agent 角色] [--repo <公開 repo 的 https 網址>]... [--no-subagent] [--force]")
	}
	tok, err := tokens.get()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	chArgs := []string{"--channel", "--auto"}
	if *agent != "" {
		chArgs = append(chArgs, "--agent", *agent)
	}
	files, err := coremcp.WorkerFiles(coremcp.WorkerOptions{
		ChannelCommand: filepath.ToSlash(exe), ChannelArgs: chArgs, MCPURL: endpoint, MCPToken: tok,
		Where: "本機 omixor console(" + endpoint + ")", AgentID: *agent, Subagent: !*noSub, Repos: repos,
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
		mode := os.FileMode(0o644)
		if strings.Contains(content, tok) { // 含權杖的檔案(.mcp.json)只有自己能讀
			mode = 0o600
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			return err
		}
		if mode == 0o600 { // 已存在的檔案(--force)WriteFile 不會改權限;Windows 要用 icacls。失敗只警告(會印在 stderr)
			mcptoken.RestrictOrWarn(path)
		}
	}
	abs, _ := filepath.Abs(dir)
	fmt.Printf("已建立執行者資料夾:%s\n啟動:\n  cd \"%s\"\n  %s\n", abs, abs, coremcp.WorkerStartCommand())
	fmt.Printf("注意:%s 含 console 的 MCP 權杖,不要提交到 git、不要分享。\n", filepath.Join(abs, ".mcp.json"))
	if ignored, err := gitignoreTokenFiles(abs); err != nil {
		fmt.Printf("提醒:寫入 .gitignore 失敗(%v),請自己把 .mcp.json 加進去。\n", err)
	} else if ignored {
		fmt.Println("已把 .mcp.json 加進這個資料夾的 .gitignore。")
	}
	return nil
}

// gitignoreTokenFiles 資料夾在 git repo 裡時,把 .mcp.json 加進資料夾的 .gitignore(已經有就不重複);回傳有沒有寫入
func gitignoreTokenFiles(dir string) (bool, error) {
	inRepo := false
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			inRepo = true
			break
		}
		p := filepath.Dir(d)
		if p == d {
			break
		}
		d = p
	}
	if !inRepo {
		return false, nil
	}
	path := filepath.Join(dir, ".gitignore")
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	for _, l := range strings.Split(string(cur), "\n") {
		if strings.TrimSpace(l) == ".mcp.json" || strings.TrimSpace(l) == "/.mcp.json" {
			return false, nil
		}
	}
	add := ".mcp.json\n"
	if len(cur) > 0 && !strings.HasSuffix(string(cur), "\n") {
		add = "\n" + add
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.WriteString(add)
	return err == nil, err
}
