package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type addIn struct {
	A int `json:"a" jsonschema:"第一個數"`
	B int `json:"b" jsonschema:"第二個數"`
}

func TestToolServer(t *testing.T) {
	add := NewTool("add", "兩數相加", func(_ context.Context, in addIn) (string, error) {
		if in.A < 0 {
			return "", errors.New("不收負數")
		}
		return fmt.Sprint(in.A + in.B), nil
	})
	o, ts, err := Options{CustomTools: []Tool{add}}.withTools()
	if err != nil {
		t.Fatal(err)
	}
	defer ts.close()
	if !slices.Contains(o.AllowedTools, "mcp__app") || !slices.Contains(o.ExtraArgs, "--mcp-config") {
		t.Fatalf("參數沒有加上：%+v", o)
	}

	var cfg struct {
		MCPServers map[string]struct{ Type, URL string } `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(ts.config), &cfg); err != nil {
		t.Fatal(err)
	}
	url := cfg.MCPServers["app"].URL
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("網址 %q", url)
	}

	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	list, err := cs.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != "add" {
		t.Fatalf("tools/list：%v %+v", err, list)
	}
	if schema, _ := json.Marshal(list.Tools[0].InputSchema); !strings.Contains(string(schema), "第一個數") {
		t.Fatalf("schema 沒有欄位說明：%s", schema)
	}

	call := func(args map[string]any) (*mcp.CallToolResult, string) {
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "add", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return r, r.Content[0].(*mcp.TextContent).Text
	}
	if r, text := call(map[string]any{"a": 2, "b": 3}); r.IsError || text != "5" {
		t.Fatalf("2+3：%v %q", r.IsError, text)
	}
	if r, text := call(map[string]any{"a": -1, "b": 3}); !r.IsError || text != "不收負數" {
		t.Fatalf("錯誤要交給 Claude：%v %q", r.IsError, text)
	}

	// 猜不到路徑的請求拿不到 tool
	bad := strings.TrimSuffix(url, "/")[:len(url)-10] + "/"
	if _, err := mcp.NewClient(&mcp.Implementation{Name: "x"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: bad, MaxRetries: -1}, nil); err == nil {
		t.Fatal("錯的路徑應該連不上")
	}
}

func TestNoToolServer(t *testing.T) {
	o, ts, err := Options{}.withTools()
	if err != nil || ts != nil || len(o.ExtraArgs) != 0 {
		t.Fatalf("沒有自訂 tool 時不該啟動：%v %v", err, ts)
	}
	ts.close() // nil 也可以呼叫
}
