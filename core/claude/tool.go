package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolServerName 是自訂 tool 所在的 MCP 伺服器名稱；Claude 看到的 tool 名稱是 mcp__app__<Name>。
const ToolServerName = "app"

// Tool 是一個自訂 tool，處理函式在你自己的 Go 程序裡執行。用 NewTool 建立。
type Tool struct {
	Name     string
	register func(*mcp.Server)
}

// NewTool 建立自訂 tool。In 是輸入的 struct，欄位的 json tag 是參數名稱，
// jsonschema tag 是參數說明（例如 `json:"city" jsonschema:"城市名稱"`），輸入的 JSON Schema 會自動產生。
// fn 回傳的文字交給 Claude；回傳 error 時 Claude 會看到錯誤訊息，可以自己修正後重試。
func NewTool[In any](name, description string, fn func(ctx context.Context, in In) (string, error)) Tool {
	return Tool{Name: name, register: func(s *mcp.Server) {
		mcp.AddTool(s, &mcp.Tool{Name: name, Description: description},
			func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
				text, err := fn(ctx, in)
				if err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
			})
	}}
}

// toolServer 是只聽 127.0.0.1 的 MCP 伺服器，網址帶隨機路徑，其他本機程式猜不到。
type toolServer struct {
	srv    *http.Server
	config string // 給 --mcp-config 的 JSON
}

func startTools(tools []Tool) (*toolServer, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "claude-go", Version: "0.1.0"}, nil)
	for _, t := range tools {
		if t.register == nil {
			return nil, errors.New("自訂 tool 要用 NewTool 建立：" + t.Name)
		}
		t.register(s)
	}
	secret := make([]byte, 16)
	rand.Read(secret)
	path := "/" + hex.EncodeToString(secret) + "/"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle(path, mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true}))
	ts := &toolServer{srv: &http.Server{Handler: mux}}
	go ts.srv.Serve(ln)

	cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		ToolServerName: map[string]string{"type": "http", "url": "http://" + ln.Addr().String() + path},
	}})
	ts.config = string(cfg)
	return ts, nil
}

func (ts *toolServer) close() {
	if ts != nil {
		ts.srv.Close()
	}
}

// withTools 啟動自訂 tool 的伺服器，回傳加上 --mcp-config 與免詢問設定後的 Options。
func (o Options) withTools() (Options, *toolServer, error) {
	ts, err := startTools(o.CustomTools)
	if err != nil || ts == nil {
		return o, nil, err
	}
	o.ExtraArgs = append(slices.Clone(o.ExtraArgs), "--mcp-config", ts.config)
	o.AllowedTools = append(slices.Clone(o.AllowedTools), "mcp__"+ToolServerName)
	return o, ts, nil
}
