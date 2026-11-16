// Package mcp 是 omixor-web 的 MCP 伺服器。
// 協定層(JSON-RPC、HTTP 傳輸)與組織、Issue 工具在 omixor-core/mcp;這裡負責:
//   - 組 tools/list 的規格:核心工具的定義一律取自核心(coremcp.CoreToolDefs,與 /mcp/org 同一份,見 webSpec),
//     web 自己的工具(範例資料、語音、網站訪客、GitHub、GCP、Facebook、server)的定義在 spec/tools.json(只放 web 自己的工具)
//   - 把核心的組織、Issue 工具接上 web 的本機狀態(tools_core.go)
//   - 其餘依賴 web 自己 internal/core 的工具(tools_*.go)
//
// 工具的行為、錯誤訊息、輸出欄位照 Swift 版的 MCPHandler.swift。
package mcp

import (
	"encoding/json"
	"fmt"

	coremcp "omixor-core/mcp"
	"omixor-core/model"
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

// webExtraCoreTools 管理端點開放的核心工具(model.CoreTools 的 Admin)以外,web 的 /mcp 也開放的核心工具。
// 規格一樣取自核心。agent_task_delegate:web 的 /mcp 一直都有(核心實作,帶 agent 代角色委派;同 Swift App 的 /mcp 也有這個工具)
var webExtraCoreTools = map[string]bool{"agent_task_delegate": true}

// isCoreToolName 是不是核心的工具名稱(登記表 model.CoreTools,或核心規格裡有的)。web 自己的工具不能用這些名稱
func isCoreToolName(name string) bool {
	if _, ok := model.CoreToolByName(name); ok {
		return true
	}
	for _, d := range append(coremcp.CoreToolDefs(), coremcp.WorkerToolDefs()...) {
		if d.(map[string]any)["name"] == name {
			return true
		}
	}
	return false
}

// coreToolDefs web 的 /mcp 開放的核心工具定義:coremcp.CoreToolDefs 裡 Admin 的(加上 webExtraCoreTools),順序照核心
func coreToolDefs() []any {
	out := []any{}
	for _, d := range coremcp.CoreToolDefs() {
		n := d.(map[string]any)["name"].(string)
		if coremcp.AdminToolAllow[n] || webExtraCoreTools[n] {
			out = append(out, d)
		}
	}
	return out
}

// webToolDefs web 自己的工具定義(spec/tools.json)。和核心撞名的不收(以核心為準;測試 TestWebToolsDoNotShadowCore 會擋下)
func webToolDefs() ([]any, error) {
	var all []any
	if err := json.Unmarshal(spec.ToolsJSON, &all); err != nil {
		return nil, fmt.Errorf("spec/tools.json 讀不出來:%w", err)
	}
	out := []any{}
	for _, d := range all {
		m, ok := d.(map[string]any)
		n, _ := m["name"].(string)
		if !ok || n == "" {
			return nil, fmt.Errorf("spec/tools.json 有沒有名稱的工具")
		}
		if isCoreToolName(n) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// webSpec tools/list 的規格:web 自己的工具 + 核心工具
func webSpec() ([]byte, error) {
	own, err := webToolDefs()
	if err != nil {
		return nil, err
	}
	return json.Marshal(append(own, coreToolDefs()...))
}

var srv = mustServer()

func mustServer() *coremcp.Server {
	tools, err := webSpec()
	if err != nil {
		panic(err)
	}
	s, err := coremcp.NewServer(tools, initializeWithVersion(spec.InitializeJSON))
	if err != nil {
		panic(err)
	}
	s.Marshal = core.Marshal
	// 規格裡屬於選用模組的工具(例如 github*,模組 internal/modules/integrations):
	// 沒編進模組時不列在 tools/list,呼叫回「這台伺服器沒有編進這個工具」
	s.HideMissing = true
	return s
}

// Register 登記 web 自己的工具實作(各 tools_*.go 在 init() 呼叫)。名稱必須在 spec/tools.json 裡(而且不是核心工具的名稱)。
func Register(name string, f ToolFunc) {
	srv.Register(name, func(c *coremcp.Call) (any, error) {
		x := c.Ctx.(*callCtx)
		return f(&Call{App: x.App, Args: c.Args, Actor: x.Actor})
	})
}

// ToolNames 全部工具名稱(依 tools/list 的順序)
func ToolNames() []string { return srv.ToolNames() }

// ToolDefs tools/list 的內容
func ToolDefs() []any { return srv.ToolDefs() }

// Missing 還沒實作的工具(測試用;選用模組沒編進來時,它的工具也在這裡)
func Missing() []string { return srv.Missing() }

// Handler 處理 JSON-RPC
type Handler struct{ App *core.App }

// Handle 處理一則(或一批)訊息;回傳 nil 表示只有通知,不用回應
func (h *Handler) Handle(data []byte) []byte {
	a := h.App
	// 範例資料工具依功能開關過濾(tools/list 不列、tools/call 回「這個功能已關閉」),見 feature_gate.go
	return h.handleMessages(data, coremcp.Hooks{
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
