// omixor-mcp:把 stdio 上的 MCP 訊息轉給執行中的 omixor console(http://127.0.0.1:3765/mcp)。和 Swift 版的 omixor-mcp 行為一樣。
// 給只支援 stdio 的 MCP 用戶端用;支援 HTTP 的用戶端(例如 Claude Code)可以直接連。
//
// 一行一則 JSON-RPC 訊息(MCP stdio 規格)。omixor console 沒開時,請求會收到 JSON-RPC 錯誤。
// 連線位址:OMIXOR_MCP_URL,或 OMIXOR_MCP_PORT 指定連接埠(舊名稱 OPS_MCP_URL、OPS_MCP_PORT 仍相容)。
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
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omixor-core/env"
	coremcp "omixor-core/mcp"
)

func main() {
	endpoint := env.Get("OMIXOR_MCP_URL", "OPS_MCP_URL")
	if endpoint == "" {
		port := env.Get("OMIXOR_MCP_PORT", "OPS_MCP_PORT")
		if port == "" {
			port = "3765"
		}
		endpoint = "http://127.0.0.1:" + port + "/mcp"
	}
	// 不走系統代理
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: nil}}
	if len(os.Args) > 1 && os.Args[1] == "--init-worker" {
		if err := initWorker(endpoint, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--channel" {
		runChannel(endpoint, os.Args[2:])
		return
	}
	logf("轉接到 %s", endpoint)

	out := bufio.NewWriter(os.Stdout)
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			handle(client, endpoint, []byte(trimmed), out)
		}
		if err != nil {
			if err != io.EOF {
				logf("讀取失敗:%v", err)
			}
			return
		}
	}
}

func handle(client *http.Client, endpoint string, body []byte, out *bufio.Writer) {
	var msg map[string]any
	_ = json.Unmarshal(body, &msg)
	id, hasID := msg["id"]
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
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
		write(out, errorReply(id, fmt.Sprintf("omixor console 回應 HTTP %d:%s", resp.StatusCode, data)))
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
func runChannel(endpoint string, args []string) {
	fs := flag.NewFlagSet("channel", flag.ExitOnError)
	agent := fs.String("agent", "", "只通知這個角色的任務")
	interval := fs.Duration("interval", 30*time.Second, "查詢間隔(最少 5 秒)")
	auto := fs.Bool("auto", false, "指示 Claude 收到就領取處理(仍需使用者在工作階段授權)")
	_ = fs.Parse(args)
	src := &coremcp.MCPTasks{URL: endpoint}
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

// initWorker 產生執行者資料夾;已有檔案時要 --force 才覆蓋
func initWorker(endpoint string, args []string) error {
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
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	chArgs := []string{"--channel", "--auto"}
	if *agent != "" {
		chArgs = append(chArgs, "--agent", *agent)
	}
	files, err := coremcp.WorkerFiles(coremcp.WorkerOptions{
		ChannelCommand: filepath.ToSlash(exe), ChannelArgs: chArgs, MCPURL: endpoint,
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
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	abs, _ := filepath.Abs(dir)
	fmt.Printf("已建立執行者資料夾:%s\n啟動:\n  cd \"%s\"\n  %s\n", abs, abs, coremcp.WorkerStartCommand())
	return nil
}
