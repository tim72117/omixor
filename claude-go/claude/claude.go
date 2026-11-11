// Package claude 透過本機的 Claude Code CLI（claude -p）呼叫 Claude，用的是登入 CLI 的訂閱額度，不需要 API key。
//
// 官方的 anthropic-sdk-go 只能用 API key（按量計費），Go 也還沒有官方的 Agent SDK；
// 這個套件做的事和 Agent SDK 一樣：啟動 CLI、讀 stream-json 輸出，把模型、推論強度、工具、權限等參數交給呼叫端控制。
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Options 控制一次推論。零值欄位不會傳給 CLI，沿用 CLI 的預設。
type Options struct {
	// Model 模型別名（opus、sonnet、haiku）或完整 id（claude-opus-5-5）
	Model string
	// Effort 推論強度：low、medium、high、xhigh、max
	Effort string
	// FallbackModel 主要模型忙碌時改用的模型
	FallbackModel string

	// SystemPrompt 取代預設的系統提示；AppendSystemPrompt 接在預設之後
	SystemPrompt       string
	AppendSystemPrompt string

	// Tools 可用的內建工具；NoTools 為 true 時一個都不給（純對話）
	Tools   []string
	NoTools bool
	// AllowedTools 不用詢問就能執行的工具，例如 "Bash(git *)"、"Edit"
	AllowedTools    []string
	DisallowedTools []string
	// PermissionMode default、acceptEdits、plan、auto、bypassPermissions
	PermissionMode string

	// JSONSchema 要求結構化輸出，結果放在 Result.StructuredOutput
	JSONSchema string

	// Resume 接續先前的 session（用 Result.SessionID）；NoPersist 為 true 時不存 session
	Resume    string
	NoPersist bool

	// CustomTools 自訂 tool（用 NewTool 建立），在這個 Go 程序裡執行，不用詢問就能呼叫。
	// 和 NoTools 可以並用：只給自訂 tool、不給內建工具。
	CustomTools []Tool

	// Lean 不載入設定檔、slash 指令與 MCP，啟動較快；搭配 SystemPrompt 可以省下預設約 6,000 token 的系統提示
	Lean bool

	// Dir 工作目錄；Bin 是 CLI 的路徑，預設 "claude"
	Dir string
	Bin string
	// ExtraArgs 其他直接傳給 CLI 的參數
	ExtraArgs []string
}

// Args 組出單次推論要傳給 CLI 的參數（不含執行檔）。
func (o Options) Args(prompt string) []string {
	// prompt 放最後，前面加 -- 以免以 - 開頭的提示被當成參數
	return append(o.flags(), "--", prompt)
}

// SessionArgs 組出常駐程序的參數：問題從 stdin 一行一個送進去。
func (o Options) SessionArgs() []string {
	return append(o.flags(), "--input-format", "stream-json")
}

func (o Options) flags() []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	add := func(flag, v string) {
		if v != "" {
			args = append(args, flag, v)
		}
	}
	add("--model", o.Model)
	add("--effort", o.Effort)
	add("--fallback-model", o.FallbackModel)
	add("--system-prompt", o.SystemPrompt)
	add("--append-system-prompt", o.AppendSystemPrompt)
	if o.NoTools {
		args = append(args, "--tools", "")
	} else {
		add("--tools", strings.Join(o.Tools, ","))
	}
	add("--allowed-tools", strings.Join(o.AllowedTools, ","))
	add("--disallowed-tools", strings.Join(o.DisallowedTools, ","))
	add("--permission-mode", o.PermissionMode)
	add("--json-schema", o.JSONSchema)
	add("--resume", o.Resume)
	if o.NoPersist {
		args = append(args, "--no-session-persistence")
	}
	if o.Lean {
		args = append(args, "--setting-sources", "", "--disable-slash-commands", "--strict-mcp-config")
	}
	return append(args, o.ExtraArgs...)
}

func (o Options) command(ctx context.Context, args []string) (*exec.Cmd, *strings.Builder) {
	bin := o.Bin
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = o.Dir
	cmd.Env = subscriptionEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	return cmd, &stderr
}

// Run 執行一次推論。每讀到一個事件就呼叫 onEvent（可為 nil），最後回傳 result 事件。
// 每次都會啟動一個新的 CLI 程序；要連續問很多次時改用 Start 的常駐程序。
func Run(ctx context.Context, prompt string, o Options, onEvent func(Event)) (*Result, error) {
	o, tools, err := o.withTools()
	if err != nil {
		return nil, err
	}
	defer tools.close()
	cmd, stderr := o.command(ctx, o.Args(prompt))
	bin := cmd.Path
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("啟動 %s：%w", bin, err)
	}

	res, parseErr := Parse(out, onEvent)
	waitErr := cmd.Wait()
	if parseErr != nil {
		return res, parseErr
	}
	if res == nil {
		if waitErr != nil {
			return nil, fmt.Errorf("claude 結束但沒有結果：%w：%s", waitErr, strings.TrimSpace(stderr.String()))
		}
		return nil, errors.New("claude 結束但沒有結果")
	}
	if res.IsError {
		return res, fmt.Errorf("claude 回報錯誤（%s）：%s", res.Subtype, res.Text)
	}
	return res, nil
}

// Parse 讀 stream-json（一行一個 JSON），回傳最後的 result 事件。
func Parse(r io.Reader, onEvent func(Event)) (*Result, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var res *Result
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		ev, err := decode(line)
		if err != nil {
			return res, err
		}
		if r, ok := ev.(*Result); ok {
			res = r
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return res, sc.Err()
}

// subscriptionEnv 移除會讓 CLI 改用 API key 計費的環境變數，確保用的是訂閱。
func subscriptionEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "ANTHROPIC_API_KEY" || k == "ANTHROPIC_AUTH_TOKEN" {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func decode(line []byte) (Event, error) {
	var head struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, fmt.Errorf("看不懂的輸出：%.80s：%w", line, err)
	}
	var ev Event
	switch {
	case head.Type == "system" && head.Subtype == "init":
		ev = &Init{}
	case head.Type == "assistant":
		ev = &Assistant{}
	case head.Type == "rate_limit_event":
		ev = &RateLimit{}
	case head.Type == "result":
		ev = &Result{}
	default:
		return &Other{Type: head.Type, Subtype: head.Subtype, Raw: json.RawMessage(append([]byte(nil), line...))}, nil
	}
	if err := json.Unmarshal(line, ev); err != nil {
		return nil, fmt.Errorf("解析 %s 事件：%w", head.Type, err)
	}
	return ev, nil
}
