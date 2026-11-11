// Package mcp 是 MCP 伺服器:協定(JSON-RPC)、工具與 HTTP 傳輸。
// 規格必須和 Swift 版(ops/Sources/Ops/MCP)一模一樣:initialize 與 tools/list 直接回傳 spec/ 裡從 Swift 版匯出的內容,
// 工具的行為、錯誤訊息、輸出欄位也照 Swift 版的 MCPHandler.swift。
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"opsweb/internal/core"
	"opsweb/spec"
)

var SupportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// ToolError 工具執行失敗:回 isError = true 的結果(不是 JSON-RPC 錯誤),同 Swift 的 MCPToolError
type ToolError struct{ Message string }

func (e ToolError) Error() string { return e.Message }

func Errorf(format string, a ...any) error { return ToolError{fmt.Sprintf(format, a...)} }

// Call 一次工具呼叫
type Call struct {
	App  *core.App
	Args Args
	// 誰做的:MCP 呼叫是 core.MCPActor;畫面透過 /api/tool 呼叫時是目前使用者
	Actor core.Actor
}

type ToolFunc func(c *Call) (any, error)

var registry = map[string]ToolFunc{}

// Register 登記工具實作(各 tools_*.go 在 init() 呼叫)。名稱必須在 spec/tools.json 裡。
func Register(name string, f ToolFunc) {
	if !slices.Contains(ToolNames(), name) {
		panic("spec/tools.json 沒有這個工具:" + name)
	}
	if _, dup := registry[name]; dup {
		panic("工具重複登記:" + name)
	}
	registry[name] = f
}

var (
	toolDefs  []any
	toolNames []string
	initInfo  map[string]any
)

func init() {
	if err := json.Unmarshal(spec.ToolsJSON, &toolDefs); err != nil {
		panic("spec/tools.json 讀不出來:" + err.Error())
	}
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.(map[string]any)["name"].(string))
	}
	if err := json.Unmarshal(spec.InitializeJSON, &initInfo); err != nil {
		panic("spec/initialize.json 讀不出來:" + err.Error())
	}
}

// ToolNames 全部工具名稱(依 tools/list 的順序)
func ToolNames() []string { return toolNames }

// ToolDefs tools/list 的內容
func ToolDefs() []any { return toolDefs }

// Missing 還沒實作的工具(測試用)
func Missing() []string {
	var out []string
	for _, n := range toolNames {
		if registry[n] == nil {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Handler 處理 JSON-RPC
type Handler struct{ App *core.App }

// Handle 處理一則(或一批)訊息;回傳 nil 表示只有通知,不用回應
func (h *Handler) Handle(data []byte) []byte {
	var parsed any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return core.Marshal(errorResponse(nil, -32700, "JSON 解析失敗"))
	}
	parsed = normalizeNumbers(parsed)
	if batch, ok := parsed.([]any); ok {
		var out []any
		for _, m := range batch {
			if r := h.message(m); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return core.Marshal(out)
	}
	if r := h.message(parsed); r != nil {
		return core.Marshal(r)
	}
	return nil
}

func (h *Handler) message(obj any) map[string]any {
	msg, ok := obj.(map[string]any)
	method, hasMethod := msg["method"].(string)
	if !ok || !hasMethod {
		// 用戶端送來的 response,或格式錯誤
		if ok && (msg["result"] != nil || msg["error"] != nil) {
			return nil
		}
		return errorResponse(nil, -32600, "不是有效的 JSON-RPC 請求")
	}
	id, hasID := msg["id"]
	if !hasID {
		return nil // 通知
	}
	params, _ := msg["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	a := h.App
	a.Mu.Lock()
	a.MCP.Requests++
	now := core.Now()
	a.MCP.LastRequestAt = &now
	a.Mu.Unlock()
	defer a.Changed("mcp")

	switch method {
	case "initialize":
		requested, _ := params["protocolVersion"].(string)
		version := SupportedVersions[0]
		if slices.Contains(SupportedVersions, requested) {
			version = requested
		}
		if info, ok := params["clientInfo"].(map[string]any); ok {
			if name, ok := info["name"].(string); ok {
				a.Mu.Lock()
				a.MCP.LastClient = name
				a.Mu.Unlock()
			}
		}
		r := map[string]any{}
		for k, v := range initInfo {
			r[k] = v
		}
		r["protocolVersion"] = version
		return result(id, r)
	case "ping":
		return result(id, map[string]any{})
	case "tools/list":
		return result(id, map[string]any{"tools": toolDefs})
	case "tools/call":
		name, ok := params["name"].(string)
		if !ok {
			return errorResponse(id, -32602, "缺少工具名稱")
		}
		if !slices.Contains(toolNames, name) {
			return errorResponse(id, -32602, "沒有這個工具:"+name)
		}
		args, _ := params["arguments"].(map[string]any)
		a.Mu.Lock()
		a.MCP.LastTool = name
		a.Mu.Unlock()
		out, err := Run(a, name, args, core.MCPActor)
		if err != nil {
			return result(id, map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true})
		}
		return result(id, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(core.Marshal(out))}}, "isError": false})
	case "resources/list":
		return result(id, map[string]any{"resources": []any{}})
	case "prompts/list":
		return result(id, map[string]any{"prompts": []any{}})
	}
	return errorResponse(id, -32601, "不支援的方法:"+method)
}

// Run 執行工具(MCP 與畫面共用)。panic 會轉成錯誤,不會讓伺服器掛掉。
func Run(a *core.App, name string, args map[string]any, actor core.Actor) (out any, err error) {
	f := registry[name]
	if f == nil {
		return nil, ToolError{"這個工具在 Go 版還沒實作:" + name}
	}
	if args == nil {
		args = map[string]any{}
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%v", r)
		}
	}()
	return f(&Call{App: a, Args: Args{Raw: args}, Actor: actor})
}

func result(id any, r map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": r}
}

func errorResponse(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

// normalizeNumbers 把 json.Number 轉成 float64(整數也是 float64,和 encoding/json 預設一致);
// 先用 UseNumber 是為了 id 這種大整數不失真:整數 id 會轉成 int64
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			x[k] = normalizeNumbers(vv)
		}
		return x
	case []any:
		for i, vv := range x {
			x[i] = normalizeNumbers(vv)
		}
		return x
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	}
	return v
}
