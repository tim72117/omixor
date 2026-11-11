// chat：常駐一個 claude 程序，一行一個問題連續對話，每輪顯示耗時。
//
//	go run ./cmd/chat -model haiku -effort low
//	printf '記住 42\n剛才的數字加 1\n' | go run ./cmd/chat -model haiku
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"claudego/claude"
)

func main() {
	o := claude.Options{NoTools: true, NoPersist: true}
	flag.StringVar(&o.Model, "model", "", "模型：opus、sonnet、haiku 或完整 id")
	flag.StringVar(&o.Effort, "effort", "", "推論強度：low、medium、high、xhigh、max")
	flag.StringVar(&o.SystemPrompt, "system", "簡短回答，使用繁體中文。", "系統提示（取代 Claude Code 預設的）")
	flag.BoolVar(&o.Lean, "lean", true, "不載入設定檔、slash 指令與 MCP")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	t0 := time.Now()
	s, err := claude.Start(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "錯誤：", err)
		os.Exit(1)
	}
	defer s.Close()
	fmt.Fprintf(os.Stderr, "[啟動] %.2fs\n", time.Since(t0).Seconds())

	in := bufio.NewScanner(os.Stdin)
	for turn := 1; ; {
		fmt.Fprint(os.Stderr, "> ")
		if !in.Scan() {
			break
		}
		q := strings.TrimSpace(in.Text())
		if q == "" {
			continue
		}
		t := time.Now()
		res, err := s.Send(ctx, q, func(ev claude.Event) {
			if a, ok := ev.(*claude.Assistant); ok && a.Text() != "" {
				fmt.Println(a.Text())
			}
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "錯誤：", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[第 %d 輪] %.2fs  輸入 %d（快取讀 %d）  輸出 %d token\n",
			turn, time.Since(t).Seconds(), res.Usage.InputTokens, res.Usage.CacheReadInputTokens, res.Usage.OutputTokens)
		turn++
	}
	fmt.Fprintln(os.Stderr)
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "讀取輸入：", err)
	}
}
