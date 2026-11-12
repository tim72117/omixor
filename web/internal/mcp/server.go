package mcp

import (
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	coremcp "omixor-core/mcp"
	"omixor-web/internal/core"
)

// 傳輸(Streamable HTTP 的最小實作,同 Swift 版 MCPServer)在 omixor-core/mcp/http.go。

const (
	DefaultPort = coremcp.DefaultPort
	MaxBody     = coremcp.MaxBody
)

// ConfiguredPort:環境變數 OPS_MCP_PORT,否則 3765
func ConfiguredPort() int { return coremcp.ConfiguredPort() }

// HTTPHandler 回傳處理 MCP 的 http.Handler
func HTTPHandler(h *Handler) http.Handler { return coremcp.HTTPHandler(h.Handle) }

// IsLocalOrigin 同 Swift:host 是 localhost、127.0.0.1、::1
func IsLocalOrigin(origin string) bool { return coremcp.IsLocalOrigin(origin) }

// Start 在 127.0.0.1:port 啟動 MCP 伺服器,狀態寫進 app.MCP
func Start(app *core.App, port int) {
	app.Mu.Lock()
	app.MCP.State, app.MCP.Port, app.MCP.Error = "starting", port, ""
	app.Mu.Unlock()
	// 只聽 127.0.0.1(同 Swift)。在 Docker 裡要設 OPS_MCP_ADDR=0.0.0.0,並把連接埠只發布到主機的 127.0.0.1
	host := "127.0.0.1"
	if h := os.Getenv("OPS_MCP_ADDR"); h != "" {
		host = h
	}
	ln, err := net.Listen("tcp4", host+":"+strconv.Itoa(port))
	if err != nil {
		msg := err.Error()
		if errors.Is(err, syscall.EADDRINUSE) {
			msg = "連接埠已被占用"
		}
		app.Mu.Lock()
		app.MCP.State, app.MCP.Error = "failed", msg
		app.Mu.Unlock()
		app.Changed("mcp")
		return
	}
	h := &Handler{App: app}
	mux := http.NewServeMux()
	mux.Handle("/mcp/agent", AgentHTTPHandler(h)) // 角色端點(核心的 mcp.AgentEndpoint):Bearer 憑證、白名單工具、文件櫃工具
	mux.Handle("/mcp/worker", WorkerHTTPHandler(h)) // 執行者端點(核心的 mcp.WorkerEndpoint):只有 dispatch_ 四個工具
	mux.Handle("/", HTTPHandler(h))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	srv.SetKeepAlivesEnabled(false)
	app.Mu.Lock()
	app.MCP.State = "listening"
	app.Mu.Unlock()
	app.Changed("mcp")
	go func() {
		err := srv.Serve(ln)
		app.Mu.Lock()
		app.MCP.State = "stopped"
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.MCP.State, app.MCP.Error = "failed", err.Error()
		}
		app.Mu.Unlock()
		app.Changed("mcp")
	}()
}
