package mcp

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opsweb/internal/core"
)

// 傳輸和 Swift 版的 MCPServer 一樣:Streamable HTTP 的最小實作。
//   - 只聽 127.0.0.1,POST /mcp 收 JSON-RPC、回 application/json;只有通知時回 202
//   - 不開 SSE(GET、DELETE 回 405,Allow: POST),不用 session
//   - 每個請求處理完就關閉連線(Connection: close)
//   - 帶 Origin 的請求只接受本機來源(防 DNS rebinding)
//   - body 最大 4 MB;不接受 chunked(411)

const (
	DefaultPort = 3765
	MaxBody     = 4 << 20
)

// ConfiguredPort:環境變數 OPS_MCP_PORT,否則 3765
func ConfiguredPort() int {
	if p, err := strconv.Atoi(os.Getenv("OPS_MCP_PORT")); err == nil && p > 0 && p < 65536 {
		return p
	}
	return DefaultPort
}

// HTTPHandler 回傳處理 MCP 的 http.Handler
func HTTPHandler(h *Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		if origin := r.Header.Get("Origin"); origin != "" && !IsLocalOrigin(origin) {
			text(w, 403, "Forbidden origin")
			return
		}
		if r.URL.Path != "/mcp" {
			text(w, 404, "Not found. MCP endpoint is /mcp")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(405)
			return
		}
		for _, te := range r.TransferEncoding {
			if strings.EqualFold(te, "chunked") {
				text(w, 411, "Chunked bodies are not supported; send Content-Length")
				return
			}
		}
		if r.ContentLength > MaxBody {
			text(w, 413, "Body too large")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
		if err != nil || len(body) > MaxBody {
			text(w, 413, "Body too large")
			return
		}
		out := h.Handle(body)
		if out == nil {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(out)))
		w.WriteHeader(200)
		_, _ = w.Write(out)
	})
}

func text(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(msg)))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, msg)
}

// IsLocalOrigin 同 Swift:host 是 localhost、127.0.0.1、::1
func IsLocalOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
}

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
	srv := &http.Server{Handler: HTTPHandler(&Handler{App: app}), ReadHeaderTimeout: 10 * time.Second}
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
