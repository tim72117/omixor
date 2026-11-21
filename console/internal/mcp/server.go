package mcp

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"omixor-core/config"
	coremcp "omixor-core/mcp"
	"omixor-web/internal/core"
)

// 傳輸(Streamable HTTP 的最小實作,同 Swift 版 MCPServer)在 omixor-core/mcp/http.go。

const (
	DefaultPort = coremcp.DefaultPort
	MaxBody     = coremcp.MaxBody
)

// ConfiguredPort:環境變數 OMIXOR_MCP_PORT,否則 3765
func ConfiguredPort() int { return coremcp.ConfiguredPort() }

// HTTPHandler 回傳處理 MCP 的 http.Handler
func HTTPHandler(h *Handler) http.Handler { return coremcp.HTTPHandler(h.Handle) }

// NewTokenGate 回傳要求 console MCP 權杖的包裝(沒帶或不符 401、同一來源「錯誤嘗試」太多次 429;帶正確權杖的永遠放行;權杖用常數時間比對)。
// 每個 gate 有自己的失敗計數;權杖換新後舊的立刻失效(每次請求都讀目前的權杖)。
func NewTokenGate(app *core.App) func(http.Handler) http.Handler {
	lim := &coremcp.FailLimiter{}
	verify := func(tok string) bool { return core.MCPTokenVerify(app, tok, coremcp.TokenEqual) }
	return func(next http.Handler) http.Handler {
		return coremcp.RequireBearer("omixor", lim, verify, next)
	}
}

// newMux console MCP 連接埠的路由:整個連接埠先過 Host 白名單(localhost、127.0.0.1、[::1],OMIXOR_MCP_ADDR 設了非本機位址就加上它;不符 403);
// /mcp/agent 驗角色憑證;其餘一律要 console 的 MCP 權杖
func newMux(app *core.App) http.Handler {
	return coremcp.HostGuard(config.Current().Str(config.MCPAddr), newRoutes(app))
}

func newRoutes(app *core.App) http.Handler {
	h := &Handler{App: app}
	mux := http.NewServeMux()
	mux.Handle("/mcp/agent", AgentHTTPHandler(h)) // 角色端點(核心的 mcp.AgentEndpoint):Bearer 憑證、白名單工具、文件櫃工具
	// 其餘路徑(/mcp、/mcp/worker、其他一律)都要 Authorization: Bearer <console 的 MCP 權杖>,沒有任何免驗證的例外
	gate := NewTokenGate(app)
	mux.Handle("/mcp/worker", gate(WorkerHTTPHandler(h))) // 執行者端點(核心的 mcp.WorkerEndpoint):只有 dispatch_ 四個工具
	mux.Handle("/", gate(HTTPHandler(h)))
	return mux
}

// IsLocalOrigin 同 Swift:host 是 localhost、127.0.0.1、::1
func IsLocalOrigin(origin string) bool { return coremcp.IsLocalOrigin(origin) }

// Start 在 127.0.0.1:port 啟動 MCP 伺服器,狀態寫進 app.MCP
func Start(app *core.App, port int) {
	app.Mu.Lock()
	app.MCP.State, app.MCP.Port, app.MCP.Error = "starting", port, ""
	app.Mu.Unlock()
	// 只聽 127.0.0.1(同 Swift)。在 Docker 裡要設 OMIXOR_MCP_ADDR=0.0.0.0,並把連接埠只發布到主機的 127.0.0.1
	host := "127.0.0.1"
	if h := config.Current().Str(config.MCPAddr); h != "" {
		host = h
	}
	// 權杖先準備好:讀不到也產生不了就不開連接埠(沒有權杖等於沒有驗證)
	if err := core.MCPTokenInit(app); err != nil {
		log.Printf("MCP 權杖準備失敗:%v", err) // 原文只進 log;給畫面(所有登入的人看得到 app.MCP.Error)的是固定句子,不含路徑與資料庫文字
		app.Mu.Lock()
		app.MCP.State, app.MCP.Error = "failed", "MCP 權杖準備失敗,詳情看 console 的啟動紀錄"
		app.Mu.Unlock()
		app.Changed("mcp")
		return
	}
	ln, err := net.Listen("tcp4", host+":"+strconv.Itoa(port))
	if err != nil {
		log.Printf("MCP 連接埠 %s:%d 開不了:%v", host, port, err)
		msg := "連接埠開不了,詳情看 console 的啟動紀錄"
		if errors.Is(err, syscall.EADDRINUSE) {
			msg = "連接埠已被占用"
		}
		app.Mu.Lock()
		app.MCP.State, app.MCP.Error = "failed", msg
		app.Mu.Unlock()
		app.Changed("mcp")
		return
	}
	srv := &http.Server{Handler: newMux(app), ReadHeaderTimeout: 10 * time.Second}
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
			log.Printf("MCP 伺服器異常停止:%v", err)
			app.MCP.State, app.MCP.Error = "failed", "MCP 伺服器異常停止,詳情看 console 的啟動紀錄"
		}
		app.Mu.Unlock()
		app.Changed("mcp")
	}()
}
