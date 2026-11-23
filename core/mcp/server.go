// Package mcp 是 omixor 核心的 MCP 協定層:JSON-RPC(initialize / tools/list / tools/call)、HTTP 傳輸、
// 工具登記,以及與組織、Issue 有關的工具(見 tools_orgs.go、tools_issues.go)。
//
// 工具的規格(initialize 回應、tools/list)由使用的程式提供(核心工具的定義在 spec.go 的 CoreToolDefs;web 用它加上自己的 spec/tools.json),
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

	"omixor-core/httperr"
)

var SupportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// ToolError 工具執行失敗:回 isError = true 的結果(不是 JSON-RPC 錯誤),同 Swift 的 MCPToolError
type ToolError struct {
	Message string
	// Status、Data 錯誤原本的 HTTP 狀態碼與額外欄位(例如 409 版本衝突的 currentVersion、刪角色的 taskCounts);
	// 有的話隨結果的 structuredContent 一起回給呼叫端(沒有是 0 / nil)
	Status int
	Data   map[string]any
	// cause 訊息被換成通用訊息時的原文(只進 log,不對外;見 toolFailure)
	cause error
}

func (e ToolError) Error() string { return e.Message }

// HTTPError 實作 httperr.Public:工具錯誤的訊息是刻意給 agent 看的,照給
func (e ToolError) HTTPError() (int, map[string]any) { return e.Status, e.Data }

// Unwrap 讓 errors.Is / As 找得到被遮蔽的原始錯誤(訊息不含它)
func (e ToolError) Unwrap() error { return e.cause }

// Cause 訊息被換成通用訊息時的原文(httperr.Causer:只進 log)
func (e ToolError) Cause() error { return e.cause }

func Errorf(format string, a ...any) error { return ToolError{Message: fmt.Sprintf(format, a...)} }

// toolFailure 工具執行失敗的結果:訊息一律經過 httperr(資料庫錯誤等內部細節換成通用訊息,原文連同工具名稱 name 進 log);
// ToolError 是刻意給 agent 看的,訊息照給(它的 cause 進 log)。
// 有狀態碼或 data(例如 409 的 currentVersion)放進 structuredContent,同時把 data 以一行緊湊 JSON 附在 text 末尾
// (Claude Code 只顯示 content 的 text,agent 才看得到 currentVersion、taskCounts 這些欄位)。
func toolFailure(name string, err error) map[string]any {
	in := httperr.Report("mcp 工具 "+name, err)
	data := map[string]any{}
	for k, v := range in.Data {
		if k != "error" && k != "status" {
			data[k] = v
		}
	}
	text := in.Msg
	if len(data) > 0 {
		text += "\n" + compactData(data)
	}
	res := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": true}
	if in.Status > 0 || len(data) > 0 {
		sc := map[string]any{"error": in.Msg}
		if in.Status > 0 {
			sc["status"] = in.Status
		}
		maps.Copy(sc, data)
		res["structuredContent"] = sc
	}
	return res
}

// Call 一次工具呼叫。Ctx 由使用的程式決定(例如 web 放 App 與呼叫者),
// 核心的工具透過 Env 介面從 Ctx 取得 Backend。
type Call struct {
	Args Args
	Ctx  any
	// hooks 這次請求的 Allow、Guard(由 Handle 帶入);工具在執行中呼叫其他工具時用 RunNested 套用同一組限制
	hooks *Hooks
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
	// CallCtx 不是 nil 時:tools/call 通過 Allow 之後、Guard 之前呼叫,回傳「這一次呼叫」的 Call.Ctx(取代 Ctx),
	// 讓同一個連線(同一批請求)裡每次呼叫各有自己的組織上下文、不共用可被改寫的狀態(統一入口用:組織取自 args["org"])。
	// 可以改 args(例如移除 org);meta 非空時放進結果的 _meta(告訴呼叫者實際用的組織,例如 {"omixor/org": id};不再附額外的 text 區塊);回傳錯誤就以 isError 結果拒絕、不執行
	CallCtx func(name string, args map[string]any) (ctx any, meta map[string]any, err error)
	// Defs 不是 nil 時改寫 tools/list 的規格(Allow 過濾之後;人類端點拿掉身分假冒型的 agent 參數用)
	Defs func(defs []any) []any
	// Deny 不是 nil 時:tools/call 一個「存在、但被 Allow 擋掉」的工具,它回傳非空字串就以 isError 結果回這句話
	// (而不是「沒有這個工具」;唯讀授權用:「此連線是唯讀授權」)
	Deny func(name string) string
	// Resources 這個端點提供的 MCP 資源(MCP Apps 的 ui:// 畫面):有的話 initialize 會宣告 resources 能力,resources/list、resources/read 回它們
	Resources []Resource
}

// Resource 一個 MCP 資源(目前只有 MCP Apps 的 ui:// HTML 畫面)
type Resource struct {
	URI         string
	Name        string
	Title       string
	Description string
	MimeType    string
	Text        string
	Meta        map[string]any // _meta
}

func (r Resource) listing() map[string]any {
	m := map[string]any{"uri": r.URI, "name": r.Name, "title": r.Title, "description": r.Description, "mimeType": r.MimeType}
	if r.Meta != nil {
		m["_meta"] = r.Meta
	}
	return m
}

func (r Resource) content() map[string]any {
	m := map[string]any{"uri": r.URI, "mimeType": r.MimeType, "text": r.Text}
	if r.Meta != nil {
		m["_meta"] = r.Meta
	}
	return m
}

// Server 協定處理器:規格 + 工具登記表
type Server struct {
	// Marshal 把工具結果轉成 JSON 文字;預設是精簡輸出、不跳脫 HTML
	Marshal func(v any) []byte
	// HideMissing 規格裡有、但這次編譯沒有實作的工具(例如選用模組沒編進來,見 omixor-core/module)不列在 tools/list、
	// ToolNames、ToolDefs;呼叫時回「這台伺服器沒有編進這個工具」。規格本身不改(omixor-web 的 spec/tools.json 照舊有這些工具)
	HideMissing bool

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

// InSpec 規格(含 Add 加上的)裡有沒有這個工具(不管有沒有實作)
func (s *Server) InSpec(name string) bool { return slices.Contains(s.toolNames, name) }

// listed 這個工具列不列在 tools/list(HideMissing 時沒實作的不列)
func (s *Server) listed(name string) bool { return !s.HideMissing || s.registry[name] != nil }

// Has 這個名稱有註冊工具嗎(沒有 = 不存在或還沒實作;給 HTTP 出口回 404 用)
func (s *Server) Has(name string) bool { return s.registry[name] != nil }

// ToolNames 全部工具名稱(依 tools/list 的順序;HideMissing 時不含沒實作的)
func (s *Server) ToolNames() []string {
	if !s.HideMissing {
		return s.toolNames
	}
	out := []string{}
	for _, n := range s.toolNames {
		if s.listed(n) {
			out = append(out, n)
		}
	}
	return out
}

// ToolDefs tools/list 的內容(HideMissing 時不含沒實作的)
func (s *Server) ToolDefs() []any {
	if !s.HideMissing {
		return s.toolDefs
	}
	out := []any{}
	for _, t := range s.toolDefs {
		if s.listed(t.(map[string]any)["name"].(string)) {
			out = append(out, t)
		}
	}
	return out
}

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
	return s.run(name, args, ctx, nil)
}

// RunNested 工具在執行中呼叫其他工具(例如繪圖工具存文件呼叫 doc_save):
// 套用同一次請求的 Allow 與 Guard,角色端點的工具開關與強制角色不會被繞過。
// 直接用 Run 呼叫就沒有這些限制(只給畫面、內部用)。
func (s *Server) RunNested(c *Call, name string, args map[string]any) (any, error) {
	if args == nil {
		args = map[string]any{}
	}
	if h := c.hooks; h != nil {
		if h.Allow != nil && !h.Allow(name) {
			return nil, Errorf("這個連線沒有開放 %s(角色設定或端點限制),不能代為呼叫", name)
		}
		if h.Guard != nil {
			if err := h.Guard(name, args); err != nil {
				return nil, err
			}
		}
	}
	return s.run(name, args, c.Ctx, c.hooks)
}

func (s *Server) run(name string, args map[string]any, ctx any, h *Hooks) (out any, err error) {
	f := s.registry[name]
	if f == nil {
		if s.HideMissing {
			return nil, ToolError{Message: "這台伺服器沒有編進 " + name + " 工具(它屬於沒有編進來的選用模組)"}
		}
		return nil, ToolError{Message: "這個工具在 Go 版還沒實作:" + name}
	}
	if args == nil {
		args = map[string]any{}
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%v", r)
		}
	}()
	return f(&Call{Args: Args{Raw: args}, Ctx: ctx, hooks: h})
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
		if len(h.Resources) > 0 { // 宣告 resources 能力(複製一份,不改共用的 initInfo)
			caps := map[string]any{}
			if c, ok := s.initInfo["capabilities"].(map[string]any); ok {
				maps.Copy(caps, c)
			}
			caps["resources"] = map[string]any{"listChanged": false, "subscribe": false}
			r["capabilities"] = caps
		}
		r["protocolVersion"] = version
		return result(id, r)
	case "ping":
		return result(id, map[string]any{})
	case "tools/list":
		defs := s.ToolDefs()
		if h.Allow != nil {
			all := defs
			defs = []any{}
			for _, t := range all {
				if h.Allow(t.(map[string]any)["name"].(string)) {
					defs = append(defs, t)
				}
			}
		}
		if h.Defs != nil {
			defs = h.Defs(defs)
		}
		return result(id, map[string]any{"tools": defs})
	case "tools/call":
		name, ok := params["name"].(string)
		if !ok {
			return errorResponse(id, -32602, "缺少工具名稱")
		}
		name = LegacyToolName(name, s.toolNames)
		if slices.Contains(s.toolNames, name) && h.Allow != nil && !h.Allow(name) && h.Deny != nil {
			if msg := h.Deny(name); msg != "" {
				return result(id, toolFailure(name, ToolError{Message: msg, Status: 403}))
			}
		}
		if !slices.Contains(s.toolNames, name) || (h.Allow != nil && !h.Allow(name)) {
			return errorResponse(id, -32602, "沒有這個工具:"+name)
		}
		args, _ := params["arguments"].(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		callCtx, meta := h.Ctx, map[string]any(nil)
		if h.CallCtx != nil {
			c, n, err := h.CallCtx(name, args)
			if err != nil {
				return result(id, toolFailure(name, err))
			}
			callCtx, meta = c, n
		}
		if h.Guard != nil {
			if err := h.Guard(name, args); err != nil {
				return result(id, toolFailure(name, err))
			}
		}
		if h.Tool != nil {
			h.Tool(name)
		}
		out, err := s.run(name, args, callCtx, &h)
		if err != nil {
			return result(id, toolFailure(name, err))
		}
		content := []any{map[string]any{"type": "text", "text": string(s.Marshal(out))}}
		res := map[string]any{"content": content, "isError": false}
		if len(meta) > 0 {
			res["_meta"] = meta
		}
		return result(id, res)
	case "resources/list":
		list := []any{}
		for _, r := range h.Resources {
			list = append(list, r.listing())
		}
		return result(id, map[string]any{"resources": list})
	case "resources/read":
		uri, _ := params["uri"].(string)
		for _, r := range h.Resources {
			if r.URI == uri {
				return result(id, map[string]any{"contents": []any{r.content()}})
			}
		}
		return errorResponse(id, -32002, "找不到資源:"+uri)
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

// textValueMax 附在 text 末尾的 data 裡,單一欄位的 JSON 最長的長度
const textValueMax = 300

// compactData 把 data 精簡成附在 text 末尾的一行 JSON:純量與短的欄位(currentVersion、taskCounts)留下,
// 太長的(例如 409 版本衝突的 current 是整張 Issue)和 current 不放(完整內容仍在 structuredContent)
func compactData(data map[string]any) string {
	small := map[string]any{}
	for k, v := range data {
		if k == "current" {
			continue
		}
		if b, err := json.Marshal(v); err == nil && len(b) <= textValueMax {
			small[k] = v
		}
	}
	if len(small) == 0 {
		return ""
	}
	b, _ := json.Marshal(small)
	return string(b)
}
