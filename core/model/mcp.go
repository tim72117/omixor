package model

import (
	"net/url"
	"regexp"
	"strings"
)

// 外部 MCP 閘道的登記(組織層級;成員讀、組織管理員改,規則在 service/mcp.go)。
//
// core 只記「有哪些外部 MCP 伺服器、怎麼啟動或連線、工具規則、哪個角色能用哪些工具」;
// 實際連線外部 MCP 伺服器的是用戶端(macOS App),App 把工具以 <伺服器 id>__<工具名> 掛到自己的 MCP 端點。
//
// 任何密鑰(token、API key、HTTP 標頭的值、環境變數的值)都不存 core:core 只記「需要哪些密鑰的名稱」
// (EnvNames、HeaderNames),值由各裝置自己存(App 存鑰匙圈),啟動或連線時才帶入。
//
// 工具規則:App 連上伺服器後把讀到的工具清單同步回來(組織管理員),每個工具可以設在閘道開不開(Enabled)、
// 呼叫前要不要人確認(Confirm:寫入、付款、對外發訊這類高風險工具)。還沒同步過的工具照伺服器的 ToolDefault(預設不開)。
// 角色權限(Grants):哪個角色可以用哪些伺服器 / 工具,預設不給;一般成員(使用者自己的 Claude Code 連 App)
// 看得到所有啟用伺服器裡開著的工具。

// FeatureMCPGateway 功能開關的 key(見 features.go;開發版、實驗、預設關)
const FeatureMCPGateway = "mcpGateway"

// 傳輸方式
const (
	MCPTransportStdio = "stdio" // App 啟動子程序,JSON-RPC 走標準輸入輸出
	MCPTransportHTTP  = "http"  // Streamable HTTP(JSON 或 SSE 回應)
)

// 上限
const (
	MaxMCPServers  = 50
	MaxMCPTools    = 300 // 一台伺服器最多記幾個工具規則
	MaxMCPArgs     = 50
	MaxMCPSecrets  = 20 // 一台伺服器最多需要幾個密鑰名稱
	MCPToolSep     = "__"
	maxMCPTextLen  = 2000
	maxMCPShortLen = 200
)

// MCPServerReg 一台登記的外部 MCP 伺服器
type MCPServerReg struct {
	// ID 小寫英數與 -(1–32 字;不能有底線,工具名用 <id>__<工具名> 組合)
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Transport   string `json:"transport"` // stdio、http
	// stdio
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd,omitempty"`
	// EnvNames 需要的環境變數「名稱」(值存在各裝置的鑰匙圈,啟動子程序時帶入,不放命令列)
	EnvNames []string `json:"envNames"`
	// http
	URL string `json:"url,omitempty"`
	// HeaderNames 需要的 HTTP 標頭「名稱」(值存在各裝置的鑰匙圈)
	HeaderNames []string `json:"headerNames"`

	Enabled bool `json:"enabled"`
	// ToolDefault 還沒同步過規則的工具開不開(預設不開)
	ToolDefault bool          `json:"toolDefault"`
	Tools       []MCPToolRule `json:"tools"`
	UpdatedAt   *Time         `json:"updatedAt,omitempty"`
	UpdatedBy   string        `json:"updatedBy,omitempty"`
}

// MCPToolRule 一個工具的規則
type MCPToolRule struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Enabled 在閘道開不開(關的工具不列、不能呼叫)
	Enabled bool `json:"enabled"`
	// Confirm 呼叫前要人在 App 按允許
	Confirm bool `json:"confirm"`
}

// MCPGrant 角色可以用的一台伺服器;Tools 空 = 這台伺服器在閘道開著的全部工具
type MCPGrant struct {
	Server string   `json:"server"`
	Tools  []string `json:"tools"`
}

// OrgMCPConfig 一個組織的外部 MCP 登記(一份文件;store 整份讀寫)
type OrgMCPConfig struct {
	Servers []MCPServerReg `json:"servers"`
	// Grants 角色 id → 可以用的伺服器 / 工具
	Grants    map[string][]MCPGrant `json:"grants"`
	UpdatedAt *Time                 `json:"updatedAt,omitempty"`
	UpdatedBy string                `json:"updatedBy,omitempty"`
}

// Clone 深拷貝
func (c OrgMCPConfig) Clone() OrgMCPConfig {
	out := OrgMCPConfig{Servers: make([]MCPServerReg, 0, len(c.Servers)), Grants: map[string][]MCPGrant{}, UpdatedBy: c.UpdatedBy}
	for _, s := range c.Servers {
		out.Servers = append(out.Servers, s.Clone())
	}
	for k, gs := range c.Grants {
		l := make([]MCPGrant, 0, len(gs))
		for _, g := range gs {
			l = append(l, MCPGrant{Server: g.Server, Tools: append([]string{}, g.Tools...)})
		}
		out.Grants[k] = l
	}
	if c.UpdatedAt != nil {
		t := *c.UpdatedAt
		out.UpdatedAt = &t
	}
	return out
}

// Clone 深拷貝
func (s MCPServerReg) Clone() MCPServerReg {
	s.Args = append([]string{}, s.Args...)
	s.EnvNames = append([]string{}, s.EnvNames...)
	s.HeaderNames = append([]string{}, s.HeaderNames...)
	s.Tools = append([]MCPToolRule{}, s.Tools...)
	if s.UpdatedAt != nil {
		t := *s.UpdatedAt
		s.UpdatedAt = &t
	}
	return s
}

// Server 找伺服器(id 不分大小寫)
func (c OrgMCPConfig) Server(id string) (MCPServerReg, int, bool) {
	for i, s := range c.Servers {
		if strings.EqualFold(s.ID, id) {
			return s, i, true
		}
	}
	return MCPServerReg{}, -1, false
}

// Rule 找工具規則(名稱精確比對)
func (s MCPServerReg) Rule(tool string) (MCPToolRule, bool) {
	for _, r := range s.Tools {
		if r.Name == tool {
			return r, true
		}
	}
	return MCPToolRule{}, false
}

// ToolOn 工具在閘道開不開:有規則照規則,沒有照 ToolDefault
func (s MCPServerReg) ToolOn(tool string) bool {
	if r, ok := s.Rule(tool); ok {
		return r.Enabled
	}
	return s.ToolDefault
}

// ToolConfirm 呼叫前要不要人確認:有規則照規則,沒有就依名稱猜(看起來是寫入、刪除、付款、對外發訊的要確認)
func (s MCPServerReg) ToolConfirm(tool string) bool {
	if r, ok := s.Rule(tool); ok {
		return r.Confirm
	}
	return MCPRiskyTool(tool)
}

// MCPQualifiedName 閘道上的工具名:<伺服器 id>__<工具名>
func MCPQualifiedName(server, tool string) string { return server + MCPToolSep + tool }

// MCPSplitName 拆開閘道上的工具名(伺服器 id 沒有底線,第一個 __ 之前就是伺服器)
func MCPSplitName(q string) (server, tool string, ok bool) {
	server, tool, ok = strings.Cut(q, MCPToolSep)
	return server, tool, ok && server != "" && tool != ""
}

var mcpRiskyWords = []string{"write", "create", "update", "delete", "remove", "send", "post", "publish", "pay", "charge",
	"transfer", "refund", "purchase", "buy", "order", "deploy", "exec", "run", "push", "merge", "move", "upload", "set", "put", "patch", "edit", "insert", "drop", "kill"}

// MCPRiskyTool 依工具名稱猜是不是高風險(寫入、付款、對外發訊);同步工具清單時新工具的 Confirm 預設值。
// 名稱拆成字(底線、-、.、/ 與駝峰),任何一個字是寫入類的動詞就算
func MCPRiskyTool(name string) bool {
	words := []string{}
	cur := []rune{}
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for _, r := range name {
		switch {
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ' ':
			flush()
		case r >= 'A' && r <= 'Z':
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	for _, w := range words {
		for _, x := range mcpRiskyWords {
			if w == x {
				return true
			}
		}
	}
	return false
}

var (
	mcpIDRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	mcpEnvRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	mcpHeaderRe = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_` + "`" + `|~-]{1,64}$`)
	mcpToolRe   = regexp.MustCompile(`^[A-Za-z0-9_.\-/]{1,128}$`)
)

// ValidMCPServerID 伺服器 id 的格式
func ValidMCPServerID(id string) bool { return mcpIDRe.MatchString(id) && !strings.Contains(id, "--") }

// ValidMCPEnvName 環境變數名稱
func ValidMCPEnvName(n string) bool { return mcpEnvRe.MatchString(n) }

// ValidMCPHeaderName HTTP 標頭名稱
func ValidMCPHeaderName(n string) bool { return mcpHeaderRe.MatchString(n) }

// ValidMCPToolName 工具名稱(MCP 規格的工具名)
func ValidMCPToolName(n string) bool { return mcpToolRe.MatchString(n) }

var mcpSecretQueryRe = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|auth|signature|sig|credential)`)

// NormalizeMCPURL http(s) 網址;不能帶帳號密碼,query 不能有像密鑰的參數(密鑰要用標頭,值存在裝置)
func NormalizeMCPURL(raw string) (string, string) {
	t := strings.TrimSpace(raw)
	u, err := url.Parse(t)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return "", "網址要是 http:// 或 https:// 開頭"
	}
	if u.User != nil {
		return "", "網址不能帶帳號密碼(密鑰要用標頭,值存在各裝置)"
	}
	for k := range u.Query() {
		if mcpSecretQueryRe.MatchString(k) {
			return "", "網址的參數 " + k + " 看起來是密鑰;密鑰不存 core,請改用標頭(只登記標頭名稱)"
		}
	}
	if u.Fragment != "" {
		return "", "網址不能有 #"
	}
	return t, ""
}

// ClipMCPText 截斷說明類文字
func ClipMCPText(s string, short bool) string {
	s = strings.TrimSpace(s)
	n := maxMCPTextLen
	if short {
		n = maxMCPShortLen
	}
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
