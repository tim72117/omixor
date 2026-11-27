package mcp

import (
	"net/http"

	coremcp "omixor-core/mcp"
)

// 傳輸(Streamable HTTP 的最小實作,同 Swift 版 MCPServer)在 omixor-core/mcp/http.go。
// console 沒有自己的 MCP 連接埠:MCP 走畫面連接埠的入口(entry.go)。這裡的 HTTPHandler 是不分身分的完整工具集,
// 只給測試與比對工具(tools/conformance-mcp)用,正式的程式不掛它。

const MaxBody = coremcp.MaxBody

// HTTPHandler 回傳處理 MCP 的 http.Handler
func HTTPHandler(h *Handler) http.Handler { return coremcp.HTTPHandler(h.Handle) }

// IsLocalOrigin 同 Swift:host 是 localhost、127.0.0.1、::1
func IsLocalOrigin(origin string) bool { return coremcp.IsLocalOrigin(origin) }
