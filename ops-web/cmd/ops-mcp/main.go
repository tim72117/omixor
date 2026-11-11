// ops-mcp:把 stdio 上的 MCP 訊息轉給執行中的 Ops(http://127.0.0.1:3765/mcp)。和 Swift 版的 ops-mcp 行為一樣。
// 給只支援 stdio 的 MCP 用戶端用;支援 HTTP 的用戶端(例如 Claude Code)可以直接連。
//
// 一行一則 JSON-RPC 訊息(MCP stdio 規格)。Ops 沒開時,請求會收到 JSON-RPC 錯誤。
// 連線位址:OPS_MCP_URL,或 OPS_MCP_PORT 指定連接埠。
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	endpoint := os.Getenv("OPS_MCP_URL")
	if endpoint == "" {
		port := os.Getenv("OPS_MCP_PORT")
		if port == "" {
			port = "3765"
		}
		endpoint = "http://127.0.0.1:" + port + "/mcp"
	}
	// 不走系統代理
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: nil}}
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
			write(out, errorReply(id, fmt.Sprintf("無法連到 Ops app(%s)。請先開啟 Ops,再重試。", endpoint)))
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
		write(out, errorReply(id, fmt.Sprintf("Ops app 回應 HTTP %d:%s", resp.StatusCode, data)))
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

func logf(format string, a ...any) { fmt.Fprintf(os.Stderr, "[ops-mcp] "+format+"\n", a...) }
