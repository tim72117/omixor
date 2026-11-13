// Package mcp 是 omixor 核心的 MCP 協定層:JSON-RPC(initialize / tools/list / tools/call)、HTTP 傳輸、
// 工具登記,以及與組織、Issue 有關的工具(見 tools_orgs.go、tools_issues.go)。
//
// 工具的規格(initialize 回應、tools/list)由使用的程式提供(web 內嵌從 Swift 版匯出的 spec/*.json),
// 協定層只負責原樣回傳。工具操作的資料透過 core/backend 的共同介面(Env.Backend,env.go),
// 所以內建 service(backend.Local)、遠端 client(backend.Remote)或 omixor-web 的本機狀態都能實作。
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

var SupportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// ToolError 工具執行失敗:回 isError = true 的結果(不是 JSON-RPC 錯誤),同 Swift 的 MCPToolError
type ToolError struct{ Message string }

func (e ToolError) Error() string { return e.Message }

func Errorf(format string, a ...any) error { return ToolError{fmt.Sprintf(format, a...)} }

// Call 一次工具呼叫。Ctx 由使用的程式決定(例如 web 放 App 與呼叫者),
// 核心的工具透過 Env 介面從 Ctx 取得 Backend。
type Call struct {
	Args Args
	Ctx  any
}

type ToolFunc func(c *Call) (any, error)

// Hooks 使用的程式掛在協定處理上的回呼(都可以是 nil)
type Hooks struct {
	// Ctx 是 tools/call 傳給工具的 Call.Ctx(MCP 呼叫的身分等)
	Ctx any
	// Begin 每個帶 id 的請求開始時呼叫(統計用);回傳的函式在處理完後呼叫
	Begin func() (end func())
	// Client initialize 帶了 clientInfo.name 時呼叫
	Client func(name string)
	// Tool tools/call 驗證過工具名稱、執行前呼叫
	Tool func(name string)
	// Allow 不是 nil 時只開放它回傳 true 的工具:tools/list 只列這些,
	// tools/call 其他工具回「沒有這個工具」(角色端點的白名單用)
	Allow func(name string) bool
	// Guard tools/call 通過 Allow 之後、執行前呼叫;可以改 args(例如角色端點強制帶上憑證的角色)。
	// 回傳錯誤就不執行,回 isError 結果(角色端點的權限檢查用)
	Guard func(name string, args map[string]any) error
}

// Server 協定處理器:規格 + 工具登記表
type Server struct {
	// Marshal 把工具結果轉成 JSON 文字;預設是精簡輸出、不跳脫 HTML
	Marshal func(v any) []byte

	toolDefs  []any
	toolNames []string
	initInfo  map[string]any
	registry  map[string]ToolFunc
}

// NewServer 用 tools/list 與 initialize 的規格建立
func NewServer(toolsJSON, initializeJSON []byte) (*Server, error) {
	s := &Server{Marshal: marshal, registry: map[string]ToolFunc{}}
	if err := json.Unmarshal(toolsJSON, &s.toolDefs); err != nil {
		return nil, fmt.Errorf("tools 規格讀不出來:%w", err)
	}
	for _, t := range s.toolDefs {
		s.toolNames = append(s.toolNames, t.(map[string]any)["name"].(string))
	}
	if err := json.Unmarshal(initializeJSON, &s.initInfo); err != nil {
		return nil, fmt.Errorf("initialize 規格讀不出來:%w", err)
	}
	return s, nil
}

func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("null")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// Register 登記工具實作。名稱必須在規格(tools/list)裡。
func (s *Server) Register(name string, f ToolFunc) {
	if !slices.Contains(s.toolNames, name) {
		panic("工具規格沒有這個工具:" + name)
	}
	if _, dup := s.registry[name]; dup {
		panic("工具重複登記:" + name)
	}
	s.registry[name] = f
}

// Tool 一個額外的工具(規格加實作):使用的程式(例如 web 的文件櫃)掛到核心的端點上
type Tool struct {
	Def map[string]any // tools/list 的一筆:name、description、inputSchema…
	Fn  ToolFunc
}

// Add 新增規格裡沒有的工具(規格與實作一起);名稱重複會 panic
func (s *Server) Add(t Tool) {
	name, _ := t.Def["name"].(string)
	if name == "" || slices.Contains(s.toolNames, name) {
		panic("工具名稱空白或重複:" + name)
	}
	s.toolDefs = append(s.toolDefs, t.Def)
	s.toolNames = append(s.toolNames, name)
	s.registry[name] = t.Fn
}

// ToolNames 全部工具名稱(依 tools/list 的順序)
func (s *Server) ToolNames() []string { return s.toolNames }

// ToolDefs tools/list 的內容
func (s *Server) ToolDefs() []any { return s.toolDefs }

// Missing 還沒實作的工具(測試用)
func (s *Server) Missing() []string {
	var out []string
	for _, n := range s.toolNames {
		if s.registry[n] == nil {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Run 執行工具(MCP 與畫面共用)。panic 會轉成錯誤,不會讓伺服器掛掉。
func (s *Server) Run(name string, args map[string]any, ctx any) (out any, err error) {
	f := s.registry[name]
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
	return f(&Call{Args: Args{Raw: args}, Ctx: ctx})
}

// Handle 處理一則(或一批)JSON-RPC 訊息;回傳 nil 表示只有通知,不用回應
func (s *Server) Handle(data []byte, h Hooks) []byte {
	var parsed any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return s.Marshal(errorResponse(nil, -32700, "JSON 解析失敗"))
	}
	parsed = normalizeNumbers(parsed)
	if batch, ok := parsed.([]any); ok {
		var out []any
		for _, m := range batch {
			if r := s.message(m, h); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return s.Marshal(out)
	}
	if r := s.message(parsed, h); r != nil {
		return s.Marshal(r)
	}
	return nil
}

func (s *Server) message(obj any, h Hooks) map[string]any {
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
	if h.Begin != nil {
		if end := h.Begin(); end != nil {
			defer end()
		}
	}

	switch method {
	case "initialize":
		requested, _ := params["protocolVersion"].(string)
		version := SupportedVersions[0]
		if slices.Contains(SupportedVersions, requested) {
			version = requested
		}
		if info, ok := params["clientInfo"].(map[string]any); ok {
			if name, ok := info["name"].(string); ok && h.Client != nil {
				h.Client(name)
			}
		}
		r := map[string]any{}
		maps.Copy(r, s.initInfo)
		r["protocolVersion"] = version
		return result(id, r)
	case "ping":
		return result(id, map[string]any{})
	case "tools/list":
		defs := s.toolDefs
		if h.Allow != nil {
			defs = []any{}
			for _, t := range s.toolDefs {
				if h.Allow(t.(map[string]any)["name"].(string)) {
					defs = append(defs, t)
				}
			}
		}
		return result(id, map[string]any{"tools": defs})
	case "tools/call":
		name, ok := params["name"].(string)
		if !ok {
			return errorResponse(id, -32602, "缺少工具名稱")
		}
		name = LegacyToolName(name, s.toolNames)
		if !slices.Contains(s.toolNames, name) || (h.Allow != nil && !h.Allow(name)) {
			return errorResponse(id, -32602, "沒有這個工具:"+name)
		}
		args, _ := params["arguments"].(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		if h.Guard != nil {
			if err := h.Guard(name, args); err != nil {
				return result(id, map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true})
			}
		}
		if h.Tool != nil {
			h.Tool(name)
		}
		out, err := s.Run(name, args, h.Ctx)
		if err != nil {
			return result(id, map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true})
		}
		return result(id, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(s.Marshal(out))}}, "isError": false})
	case "resources/list":
		return result(id, map[string]any{"resources": []any{}})
	case "prompts/list":
		return result(id, map[string]any{"prompts": []any{}})
	}
	return errorResponse(id, -32601, "不支援的方法:"+method)
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

// LegacyToolName 舊的工具名稱(ops_ 開頭,例如 issues)換成現在的名稱(issues):
// 工具改名後過渡用,tools/list 只列新名稱,舊名稱還能呼叫。現在有這個名稱(或沒有對應的新名稱)就原樣回傳。
func LegacyToolName(name string, names []string) string {
	if slices.Contains(names, name) || !strings.HasPrefix(name, "ops_") {
		return name
	}
	if n := strings.TrimPrefix(name, "ops_"); slices.Contains(names, n) {
		return n
	}
	return name
}
