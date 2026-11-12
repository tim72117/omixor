// ask：用訂閱額度問 Claude 一個問題，並控制模型與推論強度。
//
//	go run ./cmd/ask -model sonnet -effort low "用一句話解釋 DNS"
//	go run ./cmd/ask -resume <session-id> "再舉一個例子"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"omixor-core/claude"
)

func main() {
	var o claude.Options
	var tools, allowed string
	var verbose bool
	flag.StringVar(&o.Model, "model", "", "模型：opus、sonnet、haiku 或完整 id")
	flag.StringVar(&o.Effort, "effort", "", "推論強度：low、medium、high、xhigh、max")
	flag.StringVar(&o.SystemPrompt, "system", "", "取代預設的系統提示")
	flag.StringVar(&o.AppendSystemPrompt, "append-system", "", "接在預設系統提示之後")
	flag.StringVar(&tools, "tools", "", "可用的工具，逗號分隔；留空表示不給工具")
	flag.StringVar(&allowed, "allow", "", "不用詢問就能執行的工具，逗號分隔")
	flag.StringVar(&o.PermissionMode, "permission", "", "權限模式：default、acceptEdits、plan、auto")
	flag.StringVar(&o.JSONSchema, "schema", "", "結構化輸出的 JSON Schema")
	flag.StringVar(&o.Resume, "resume", "", "接續的 session id")
	flag.StringVar(&o.Dir, "dir", "", "工作目錄")
	flag.BoolVar(&o.Lean, "lean", false, "不載入設定檔、slash 指令與 MCP（建議搭配 -system）")
	flag.BoolVar(&verbose, "v", false, "顯示模型、思考與額度")
	timeout := flag.Duration("timeout", 10*time.Minute, "逾時")
	flag.Parse()

	prompt := strings.Join(flag.Args(), " ")
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "用法：ask [參數] <問題>")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if tools == "" {
		o.NoTools = true
	} else {
		o.Tools = strings.Split(tools, ",")
	}
	if allowed != "" {
		o.AllowedTools = strings.Split(allowed, ",")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	res, err := claude.Run(ctx, prompt, o, func(ev claude.Event) {
		switch e := ev.(type) {
		case *claude.Init:
			if verbose {
				fmt.Fprintf(os.Stderr, "[模型] %s  [工具] %d 個\n", e.Model, len(e.Tools))
			}
		case *claude.Assistant:
			for _, b := range e.Message.Content {
				switch {
				case b.Type == "text":
					fmt.Println(b.Text)
				case b.Type == "thinking" && verbose && b.Thinking != "":
					fmt.Fprintf(os.Stderr, "[思考] %s\n", b.Thinking)
				case b.Type == "tool_use" && b.Name != "StructuredOutput":
					fmt.Fprintf(os.Stderr, "[工具] %s %s\n", b.Name, b.Input)
				}
			}
		case *claude.RateLimit:
			if verbose {
				for name, w := range e.Info.Windows {
					fmt.Fprintf(os.Stderr, "[額度] %s 已用 %.0f%%，%s 重置\n", name, w.Utilization*100, time.Unix(w.ResetsAt, 0).Format("01/02 15:04"))
				}
			}
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "錯誤：", err)
		os.Exit(1)
	}
	if len(res.StructuredOutput) > 0 {
		fmt.Println(string(res.StructuredOutput))
	}
	fmt.Fprintf(os.Stderr, "[session] %s  [輪數] %d  [耗時] %.1fs  [輸出 token] %d\n",
		res.SessionID, res.NumTurns, float64(res.DurationMs)/1000, res.Usage.OutputTokens)
}
