# claude-go

用 Go 控制 Claude 的推論，走的是 **訂閱額度**（登入 Claude Code CLI 的帳號），不需要 API key。

## 為什麼不是 anthropic-sdk-go

- 官方的 `github.com/anthropics/anthropic-sdk-go` 只能用 API key，按 token 計費，不會用到訂閱。
- Go 還沒有官方的 Claude Agent SDK。
- 這個套件的做法和 Agent SDK 一樣：啟動本機的 `claude -p --output-format stream-json`，逐行解析事件。模型、推論強度、工具和權限等參數都交給呼叫端控制。
- 執行時會移除 `ANTHROPIC_API_KEY` 和 `ANTHROPIC_AUTH_TOKEN`，確保 CLI 用的是訂閱。

前提：已安裝 Claude Code，並用訂閱帳號登入過（`claude` 再輸入 `/login`）。只給自己使用；Anthropic 不允許把訂閱登入提供給第三方產品。

## 指令

```bash
go run ./claude/cmd/ask -v -model sonnet -effort low "用一句話解釋 DNS"
go run ./claude/cmd/ask -model haiku -schema '{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"]}' "3 加 4 等於多少"
go run ./claude/cmd/ask -resume <session-id> "再舉一個例子"
go run ./claude/cmd/ask -tools Read,Grep -dir ../web "src 底下有幾個演員"
```

| 參數 | 作用 |
|---|---|
| `-model` | `opus`、`sonnet`、`haiku` 或完整 id |
| `-effort` | `low`、`medium`、`high`、`xhigh`、`max` |
| `-system` / `-append-system` | 取代預設系統提示，或接在它後面 |
| `-tools` | 可用的工具，留空代表純對話 |
| `-allow` | 不用詢問就能執行的工具，例如 `Bash(git *)` |
| `-permission` | 權限模式 |
| `-schema` | 結構化輸出 |
| `-resume` | 接續先前的 session |
| `-v` | 顯示實際模型、思考摘要、5 小時與 7 天的額度使用率 |

## 常駐程序（連續對話）

`claude.Start` 只啟動一個 `claude` 程序（`--input-format stream-json`），之後每次 `Send` 都是同一段對話的下一輪：

```bash
go run ./claude/cmd/chat -model haiku -effort low
```

```go
s, err := claude.Start(ctx, claude.Options{Model: "haiku", Lean: true, SystemPrompt: "簡短回答", NoTools: true})
defer s.Close()
r1, _ := s.Send(ctx, "記住數字 42", nil)
r2, _ := s.Send(ctx, "剛才的數字加 1", nil) // 43
```

實測（2026-11-09，haiku、low，5 個問題）：

| | 總耗時 | 每輪 | claude 記憶體 | 記得上一輪 |
|---|---|---|---|---|
| 常駐（`chat`） | 11.8 秒 | 1.8–2.8 秒 | 約 217 MB，一個程序 | 會 |
| 每次啟動（`ask -lean`） | 22.1 秒 | 3.6–6.0 秒 | 每次約 219 MB | 不會 |

- 同一個 Session 一次只跑一輪，`Send` 會排隊。要並行就開多個 Session，每個約 200 MB。
- `Send` 的 ctx 取消時會結束程序，因為那一輪的輸出還沒讀完，程序狀態已經對不上。
- 每輪都會帶上前面的對話，輸入 token 逐輪增加。對話很長時，開新的 Session 比較省。

## 自訂 tool

`Options.CustomTools` 放用 `claude.NewTool` 建立的 tool。執行時會在 `127.0.0.1` 開一個 MCP 伺服器，網址帶隨機路徑，再用 `--mcp-config` 讓 `claude` 連上。tool 的處理函式在你自己的 Go 程序裡執行，可以直接讀寫程序內的資料。

```go
type stockIn struct {
	Store string `json:"store" jsonschema:"門市"`
	Size  string `json:"size" jsonschema:"尺寸：S、M、L"`
}

o.CustomTools = []claude.Tool{
	claude.NewTool("get_stock", "查詢庫存件數", func(ctx context.Context, in stockIn) (string, error) {
		return fmt.Sprintf("%d 件", stock[in.Store+in.Size]), nil
	}),
}
```

- 輸入的 JSON Schema 從 struct 自動產生：`json` tag 是參數名稱，`jsonschema` tag 是參數說明。
- 回傳的文字交給 Claude。回傳 error 時，Claude 會看到錯誤訊息，可以自己修正後重試。
- 自訂 tool 不用詢問就能執行，Claude 看到的名稱是 `mcp__app__<名稱>`。搭配 `NoTools: true` 時，就只有自訂 tool，沒有內建工具。
- `Run` 和常駐的 `Start` 都支援。程序結束或 `Close` 時，伺服器也會關掉。

範例：門市庫存。Claude 先查庫存，發現不夠，再叫貨補齊：

```bash
go run ./claude/cmd/tools -model haiku "台北店還有幾件 M 號白 T？不夠 5 件就幫我叫貨補到 5 件"
```

## 套件

```go
res, err := claude.Run(ctx, "解釋 DNS", claude.Options{
	Model: "sonnet", Effort: "low", NoTools: true,
}, func(ev claude.Event) {
	if a, ok := ev.(*claude.Assistant); ok {
		fmt.Print(a.Text())
	}
})
// res.Text、res.SessionID、res.Usage、res.StructuredOutput
```

事件類型：`*Init`（實際模型、工具）、`*Assistant`（文字、思考、工具呼叫）、`*RateLimit`（訂閱額度）、`*Result`（最後結果）、`*Other`（其他，原文在 `Raw`）。

`Result.TotalCostUSD` 是換算成 API 價格的估計值。用訂閱時不會另外收費，實際消耗的是額度。

## 測試

```bash
go test ./...
```
