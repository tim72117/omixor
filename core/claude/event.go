package claude

import (
	"encoding/json"
	"strings"
)

// Event 是 stream-json 的一行：*Init、*Assistant、*User、*RateLimit、*Result 或 *Other。
type Event interface{ event() }

// Init 是 session 開始時的設定：實際用的模型、工具與權限模式。
type Init struct {
	SessionID      string   `json:"session_id"`
	Model          string   `json:"model"`
	Cwd            string   `json:"cwd"`
	Tools          []string `json:"tools"`
	PermissionMode string   `json:"permissionMode"`
	// MCPServers 這次載入的 MCP 伺服器與連線狀態(connected、failed、needs-auth…)
	MCPServers []MCPServer `json:"mcp_servers"`
}

// MCPServer 是 Init 裡的一個 MCP 伺服器。
type MCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Assistant 是模型的一則回覆（可能含文字、思考、工具呼叫）。
type Assistant struct {
	SessionID string `json:"session_id"`
	Message   struct {
		Model   string  `json:"model"`
		Content []Block `json:"content"`
	} `json:"message"`
}

// Block 是回覆裡的一個區塊；Type 為 text、thinking、tool_use（Assistant 裡），或 tool_result（User 裡）。
type Block struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"` // tool_use 的 id
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	// tool_result：對應的 tool_use id、是否出錯、內容（字串或 [{type:text,text}] 陣列，用 ResultText 取文字）
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// ResultText 取出 tool_result 區塊的文字（內容是字串或文字區塊陣列；其他型別的區塊略過）。
func (b Block) ResultText() string {
	if len(b.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(b.Content, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		if p.Type == "text" || p.Type == "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// User 是回給模型的訊息，帶工具執行結果（tool_result 區塊）。
type User struct {
	SessionID string `json:"session_id"`
	Message   struct {
		// Content 可能是字串或區塊陣列；用 Blocks 取
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// Blocks 回傳 tool_result 等區塊（內容是純字串時回 nil）。
func (u *User) Blocks() []Block {
	var bs []Block
	if json.Unmarshal(u.Message.Content, &bs) != nil {
		return nil
	}
	return bs
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
func (*User) event()      {}
func (*RateLimit) event() {}
func (*Result) event()    {}
func (*Other) event()     {}
