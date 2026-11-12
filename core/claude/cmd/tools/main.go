// tools：自訂 tool 的範例。tool 在這個 Go 程序裡執行，可以直接讀寫程序內的資料。
//
//	go run ./cmd/tools -model haiku "台北店還有幾件 M 號白 T？不夠 5 件就幫我叫貨補到 5 件"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"

	"omixor-core/claude"
)

// 程序內的庫存，tool 直接讀寫
var (
	mu    sync.Mutex
	stock = map[string]int{"台北/白T/M": 2, "台北/白T/L": 7, "台中/白T/M": 9}
)

type stockIn struct {
	Store string `json:"store" jsonschema:"門市，例如 台北、台中"`
	Item  string `json:"item" jsonschema:"品項，例如 白T"`
	Size  string `json:"size" jsonschema:"尺寸：S、M、L"`
}

type orderIn struct {
	stockIn
	Qty int `json:"qty" jsonschema:"要叫貨的數量，1–50"`
}

func key(in stockIn) string { return in.Store + "/" + in.Item + "/" + strings.ToUpper(in.Size) }

func main() {
	o := claude.Options{NoTools: true, NoPersist: true, Lean: true,
		SystemPrompt: "你是門市助理，用繁體中文簡短回答。庫存與叫貨一律用提供的 tool，不要猜。"}
	flag.StringVar(&o.Model, "model", "", "模型")
	flag.StringVar(&o.Effort, "effort", "low", "推論強度")
	flag.Parse()
	prompt := strings.Join(flag.Args(), " ")
	if prompt == "" {
		prompt = "台北店還有幾件 M 號白 T？不夠 5 件就幫我叫貨補到 5 件"
	}

	o.CustomTools = []claude.Tool{
		claude.NewTool("get_stock", "查詢某門市某品項某尺寸的庫存件數", func(_ context.Context, in stockIn) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			n, ok := stock[key(in)]
			fmt.Fprintf(os.Stderr, "  [Go] get_stock %s → %d\n", key(in), n)
			if !ok {
				return "", fmt.Errorf("沒有 %s 這個品項", key(in))
			}
			return fmt.Sprintf("%d 件", n), nil
		}),
		claude.NewTool("place_order", "向倉庫叫貨，到貨後加進門市庫存", func(_ context.Context, in orderIn) (string, error) {
			if in.Qty < 1 || in.Qty > 50 {
				return "", fmt.Errorf("數量要在 1–50 之間，收到 %d", in.Qty)
			}
			mu.Lock()
			defer mu.Unlock()
			stock[key(in.stockIn)] += in.Qty
			fmt.Fprintf(os.Stderr, "  [Go] place_order %s +%d → %d\n", key(in.stockIn), in.Qty, stock[key(in.stockIn)])
			return fmt.Sprintf("已叫貨 %d 件，目前 %d 件", in.Qty, stock[key(in.stockIn)]), nil
		}),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := claude.Run(ctx, prompt, o, func(ev claude.Event) {
		if a, ok := ev.(*claude.Assistant); ok {
			for _, b := range a.Message.Content {
				switch b.Type {
				case "tool_use":
					fmt.Fprintf(os.Stderr, "[Claude 呼叫] %s %s\n", b.Name, b.Input)
				case "text":
					fmt.Println(b.Text)
				}
			}
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "錯誤：", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[輪數] %d  [耗時] %.1fs  [最後庫存] 台北/白T/M = %d\n", res.NumTurns, float64(res.DurationMs)/1000, stock["台北/白T/M"])
}
