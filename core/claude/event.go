package claude

import (
	"encoding/json"
	"strings"
)

// Event 是 stream-json 的一行：*Init、*Assistant、*RateLimit、*Result 或 *Other。
type Event interface{ event() }

// Init 是 session 開始時的設定：實際用的模型、工具與權限模式。
type Init struct {
	SessionID      string   `json:"session_id"`
	Model          string   `json:"model"`
	Cwd            string   `json:"cwd"`
	Tools          []string `json:"tools"`
	PermissionMode string   `json:"permissionMode"`
}

// Assistant 是模型的一則回覆（可能含文字、思考、工具呼叫）。
type Assistant struct {
	SessionID string `json:"session_id"`
	Message   struct {
		Model   string  `json:"model"`
		Content []Block `json:"content"`
	} `json:"message"`
}

// Block 是回覆裡的一個區塊；Type 為 text、thinking 或 tool_use。
type Block struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// Text 把這則回覆的文字區塊接起來。
func (a *Assistant) Text() string {
	var b strings.Builder
	for _, c := range a.Message.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// RateLimit 是訂閱額度的使用狀況。
type RateLimit struct {
	Info struct {
		Status         string                 `json:"status"`
		RateLimitType  string                 `json:"rateLimitType"`
		ResetsAt       int64                  `json:"resetsAt"`
		IsUsingOverage bool                   `json:"isUsingOverage"`
		Windows        map[string]LimitWindow `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

// LimitWindow 是一個額度區間（five_hour、seven_day）：Utilization 0–1，ResetsAt 是 Unix 秒。
type LimitWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

// Result 是最後一行：整次推論的結果與用量。
type Result struct {
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Text             string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output,omitempty"`
	SessionID        string          `json:"session_id"`
	StopReason       string          `json:"stop_reason"`
	NumTurns         int             `json:"num_turns"`
	DurationMs       int64           `json:"duration_ms"`
	// TotalCostUSD 是換算成 API 價格的估計；用訂閱時不會另外收費
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        Usage   `json:"usage"`
}

// Usage 是 token 用量。
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// Other 是這個套件沒有特別解析的事件，原文放在 Raw。
type Other struct {
	Type    string
	Subtype string
	Raw     json.RawMessage
}

func (*Init) event()      {}
func (*Assistant) event() {}
func (*RateLimit) event() {}
func (*Result) event()    {}
func (*Other) event()     {}
