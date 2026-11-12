// Package mcp 是 omixor-web 的 MCP 伺服器。
// 協定層(JSON-RPC、HTTP 傳輸)與組織、Issue 工具在 omixor-core/mcp;這裡負責:
//   - 載入規格(spec/ 裡從 Swift 版匯出的 tools.json、initialize.json,必須和 Swift 版一模一樣)
//   - 把核心的組織、Issue 工具接上 web 的本機狀態(backend.go)
//   - 其餘依賴 web 自己 internal/core 的工具(tools_*.go)
//
// 工具的行為、錯誤訊息、輸出欄位照 Swift 版的 MCPHandler.swift。
package mcp

import (
	"encoding/json"
	coremcp "omixor-core/mcp"
	"omixor-core/version"
	"omixor-web/internal/core"
	"omixor-web/spec"
)

var SupportedVersions = coremcp.SupportedVersions

// Call 一次工具呼叫
type Call struct {
	App  *core.App
	Args Args
	// 誰做的:MCP 呼叫是 core.MCPActor;畫面透過 /api/tool 呼叫時是目前使用者
	Actor core.Actor
}

type ToolFunc func(c *Call) (any, error)

// callCtx 傳給核心協定層的 Call.Ctx;同時是核心工具的資料來源(coremcp.Env)
type callCtx struct {
	App   *core.App
	Actor core.Actor
	// locked 現在是不是拿著 App.Mu(Env 的 Lock/Unlock 設定):Issue 工具會上鎖,核心的角色工具不上鎖,
	// 同一個 callCtx 的方法兩種情況都會被呼叫,要靠它知道能不能再鎖(見 tools_core.go 的 withMu)
	locked bool
}

// initializeWithVersion initialize 規格(與 Swift 共用,不改檔)的 serverInfo.version 換成實際版本
func initializeWithVersion(b []byte) []byte {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return b
	}
	info, _ := m["serverInfo"].(map[string]any)
	if info == nil {
		return b
	}
	info["version"] = version.Info().Version
	out, err := json.Marshal(m)
	if err != nil {
		return b
	}
	return out
}

var srv = mustServer()

func mustServer() *coremcp.Server {
	s, err := coremcp.NewServer(spec.ToolsJSON, initializeWithVersion(spec.InitializeJSON))
	if err != nil {
		panic(err)
	}
	s.Marshal = core.Marshal
	return s
}

// Register 登記 web 自己的工具實作(各 tools_*.go 在 init() 呼叫)。名稱必須在 spec/tools.json 裡。
func Register(name string, f ToolFunc) {
	rawTools[name] = f
	srv.Register(name, func(c *coremcp.Call) (any, error) {
		x := c.Ctx.(*callCtx)
		return f(&Call{App: x.App, Args: c.Args, Actor: x.Actor})
	})
}

// rawTools 已登記的 web 工具實作(角色端點 /mcp/agent 把文件櫃工具掛到核心的端點上時用)
var rawTools = map[string]ToolFunc{}

// ToolNames 全部工具名稱(依 tools/list 的順序)
func ToolNames() []string { return srv.ToolNames() }

// ToolDefs tools/list 的內容
func ToolDefs() []any { return srv.ToolDefs() }

// Missing 還沒實作的工具(測試用)
func Missing() []string { return srv.Missing() }

// Handler 處理 JSON-RPC
type Handler struct{ App *core.App }

// Handle 處理一則(或一批)訊息;回傳 nil 表示只有通知,不用回應
func (h *Handler) Handle(data []byte) []byte {
	a := h.App
	return srv.Handle(data, coremcp.Hooks{
		Ctx: &callCtx{App: a, Actor: core.MCPActor},
		Begin: func() func() {
			a.Mu.Lock()
			a.MCP.Requests++
			now := core.Now()
			a.MCP.LastRequestAt = &now
			a.Mu.Unlock()
			return func() { a.Changed("mcp") }
		},
		Client: func(name string) {
			a.Mu.Lock()
			a.MCP.LastClient = name
			a.Mu.Unlock()
		},
		Tool: func(name string) {
			a.Mu.Lock()
			a.MCP.LastTool = name
			a.Mu.Unlock()
		},
	})
}

// Run 執行工具(MCP 與畫面共用)。panic 會轉成錯誤,不會讓伺服器掛掉。
func Run(a *core.App, name string, args map[string]any, actor core.Actor) (any, error) {
	return srv.Run(name, args, &callCtx{App: a, Actor: actor})
}
