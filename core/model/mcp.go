package model

import (
	"fmt"
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
// 儲存時會擋看起來像密鑰的值(見 CheckMCPLaunch、NormalizeMCPURL):command、args、cwd、網址裡出現
// 常見密鑰前綴(sk-、ghp_、xoxb-、AKIA…)、Bearer、長隨機字串、--token 這類旗標直接帶值、VAR=值 都回 400。
//
// 密鑰佔位 ${名稱}:要把密鑰當命令列參數傳的伺服器(例如 --api-key <值>),在 args 寫 ${名稱}
// (例如 ["--api-key", "${API_KEY}"] 或 ["--api-key=${API_KEY}"]、["Authorization: Bearer ${TOKEN}"])。
// 名稱要符合環境變數名稱格式([A-Za-z_][A-Za-z0-9_]*)而且要登記在 EnvNames;App 啟動子程序時
// 用該裝置鑰匙圈裡同名密鑰的值代入(同一個值也照常放進子程序的環境變數)。佔位只在 args 有效,
// command、cwd、url 不代入;沒有 $ 跳脫寫法(args 裡不能有字面上的 ${…})。
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
	// UpdatedByActor 最後修改者的 actor 物件(ISS-36 C-3;只在服務層輸出補,存的設定裡沒有)
	UpdatedByActor *ActorView `json:"updatedByActor,omitempty"`
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

// MCPGrant 角色可以用的一台伺服器:All = 這台伺服器在閘道開著的全部工具,否則只有 Tools 列的。
// 舊資料沒有 All、Tools 空代表全部(Normalize 會轉成 All = true)
type MCPGrant struct {
	Server string   `json:"server"`
	All    bool     `json:"all"`
	Tools  []string `json:"tools"`
}

// Normalize 舊資料相容:Tools 空視為 All;All 時 Tools 清空
func (g MCPGrant) Normalize() MCPGrant {
	if g.All || len(g.Tools) == 0 {
		return MCPGrant{Server: g.Server, All: true, Tools: []string{}}
	}
	return MCPGrant{Server: g.Server, Tools: append([]string{}, g.Tools...)}
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
			l = append(l, g.Normalize())
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

// MCPToolKey 工具名比對用的正規化:不分大小寫,- 與 _ 視為相同
// (避免 Approve_Pull_Request、approve-pull-request 這類變體找不到規則而套用預設)
func MCPToolKey(n string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(n)), "-", "_")
}

// MCPSameTool 兩個工具名是不是同一個(見 MCPToolKey)
func MCPSameTool(a, b string) bool { return MCPToolKey(a) == MCPToolKey(b) }

// Rule 找工具規則(不分大小寫、- 與 _ 視為相同;有完全相同的名稱優先)
func (s MCPServerReg) Rule(tool string) (MCPToolRule, bool) {
	for _, r := range s.Tools {
		if r.Name == tool {
			return r, true
		}
	}
	for _, r := range s.Tools {
		if MCPSameTool(r.Name, tool) {
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

// 唯讀動詞:工具名第一個字是這些,而且後面沒有任何寫入類的字,才當成唯讀(不用確認)
var mcpReadVerbs = map[string]bool{"get": true, "list": true, "read": true, "search": true, "query": true, "describe": true,
	"view": true, "show": true, "find": true, "lookup": true, "count": true, "check": true, "inspect": true, "browse": true,
	"head": true, "stat": true, "explain": true, "preview": true, "diff": true, "status": true, "info": true, "whoami": true}

// 寫入類的字:出現在名稱任何位置就不算唯讀(例如 get_and_delete、list_then_send)
// (只放動詞;payment、order、message、status 這類也常當名詞的不放,免得 get_order、list_messages 被當成寫入)
var mcpWriteWords = map[string]bool{"write": true, "create": true, "update": true, "delete": true, "remove": true, "send": true,
	"post": true, "publish": true, "pay": true, "charge": true, "transfer": true, "refund": true, "purchase": true, "buy": true,
	"deploy": true, "exec": true, "execute": true, "run": true, "push": true, "merge": true, "move": true, "upload": true,
	"set": true, "put": true, "patch": true, "edit": true, "insert": true, "drop": true, "kill": true, "add": true,
	"approve": true, "reject": true, "destroy": true, "invite": true, "reply": true, "fetch": true, "call": true,
	"invoke": true, "trigger": true, "restart": true, "cancel": true, "commit": true, "apply": true, "grant": true,
	"revoke": true, "assign": true, "submit": true, "save": true, "modify": true, "rename": true, "install": true,
	"uninstall": true, "eval": true, "click": true, "fill": true, "navigate": true, "download": true, "mint": true,
	"swap": true, "withdraw": true, "deposit": true, "notify": true, "reset": true, "clear": true, "purge": true,
	"terminate": true, "enable": true, "disable": true, "unlock": true, "import": true, "sync": true, "mutate": true,
	"upsert": true, "replace": true, "append": true, "forward": true, "unsubscribe": true, "subscribe": true}

// mcpToolWords 工具名拆成小寫的字(底線、-、.、/、空白與駝峰)
func mcpToolWords(name string) []string {
	words := []string{}
	cur := []rune{}
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	prevLower := false
	for _, r := range name {
		switch {
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ' ' || r == ':':
			flush()
			prevLower = false
		case r >= 'A' && r <= 'Z':
			if prevLower {
				flush()
			}
			cur = append(cur, r)
			prevLower = false
		default:
			cur = append(cur, r)
			prevLower = (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		}
	}
	flush()
	return words
}

// MCPRiskyTool 依工具名稱判斷呼叫前要不要人確認(同步工具清單時新工具的 Confirm 預設值、沒有規則時的預設)。
// 保守:只有明確是唯讀的才不用確認——第一個字是唯讀動詞(get、list、read、search、query、describe、view、show…)
// 而且整個名稱沒有任何寫入類的字;其他(包括 execute_command、fetch、echo 這類看不出來的)一律要確認
func MCPRiskyTool(name string) bool {
	words := mcpToolWords(name)
	if len(words) == 0 || !mcpReadVerbs[words[0]] {
		return true
	}
	for _, w := range words[1:] {
		if mcpWriteWords[w] {
			return true
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

var mcpSecretQueryRe = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|auth|signature|sig|credential|session|cookie|jwt)`)

// NormalizeMCPURL http(s) 網址;不能帶帳號密碼、#,query(& 或 ; 分隔)不能有像密鑰的參數名稱或值,
// path 不能有像密鑰的段落(密鑰要用標頭,只登記標頭名稱,值存在各裝置)
func NormalizeMCPURL(raw string) (string, string) {
	t := strings.TrimSpace(raw)
	u, err := url.Parse(t)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return "", "網址要是 http:// 或 https:// 開頭"
	}
	if u.User != nil || strings.Contains(u.Host, "@") {
		return "", "網址不能帶帳號密碼(密鑰要用標頭,只登記標頭名稱,值由各裝置輸入)"
	}
	if u.Fragment != "" || strings.Contains(t, "#") {
		return "", "網址不能有 #"
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if d, err := url.PathUnescape(seg); err == nil {
			seg = d
		}
		if mcpSecretValue(seg) {
			return "", "網址的路徑裡有看起來像密鑰的段落;" + mcpSecretHint
		}
	}
	for _, kv := range strings.FieldsFunc(u.RawQuery, func(r rune) bool { return r == '&' || r == ';' }) {
		k, v, _ := strings.Cut(kv, "=")
		if d, err := url.QueryUnescape(k); err == nil {
			k = d
		}
		if d, err := url.QueryUnescape(v); err == nil {
			v = d
		}
		if mcpSecretQueryRe.MatchString(k) {
			return "", "網址的參數 " + k + " 看起來是密鑰;" + mcpSecretHint
		}
		if mcpSecretValue(v) || mcpSecretValue(k) {
			return "", "網址的參數 " + ClipMCPText(k, true) + " 的值看起來是密鑰;" + mcpSecretHint
		}
	}
	return t, ""
}

// mcpSecretHint 擋下密鑰時的說明
const mcpSecretHint = "core 不存任何密鑰。請改成登記「密鑰名稱」(stdio 用 envNames、http 用 headerNames),值由各裝置輸入(App 存在鑰匙圈);" +
	"要當命令列參數傳的,在 args 用 ${名稱} 佔位(名稱要登記在 envNames),App 連線時用該裝置的密鑰代入"

// 常見密鑰格式(前綴)
var mcpSecretPrefixRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9])(?:` +
	`sk-[A-Za-z0-9_-]{6,}|(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{6,}|gh[pousr]_[A-Za-z0-9]{6,}|github_pat_[A-Za-z0-9_]{6,}|` +
	`glpat-[A-Za-z0-9_-]{6,}|xox[abposre]-[A-Za-z0-9-]{6,}|xapp-[A-Za-z0-9-]{6,}|(?:AKIA|ASIA)[A-Z0-9]{12,}|AIza[A-Za-z0-9_-]{20,}|` +
	`ya29\.[A-Za-z0-9_-]{10,}|npm_[A-Za-z0-9]{20,}|hf_[A-Za-z0-9]{20,}|shp(?:at|ss|ca|pa)_[A-Za-z0-9]{10,}|` +
	`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})`)

// Bearer / Basic 帶值、私鑰
var mcpSecretAuthRe = regexp.MustCompile(`(?i)(?:\bbearer\s+[^\s$]|\bbasic\s+[A-Za-z0-9+/=]{8,}|-----BEGIN [A-Z ]*PRIVATE KEY)`)

// mcpPlaceholderRe ${名稱} 佔位
var mcpPlaceholderRe = regexp.MustCompile(`\$\{([^}]*)\}`)

// mcpRandomRun 長隨機字串:連續 24 個以上英數,同時有英文和數字,而且字元夠亂
func mcpRandomRun(s string) bool {
	run := []rune{}
	check := func() bool {
		defer func() { run = run[:0] }()
		if len(run) < 24 {
			return false
		}
		letters, digits := 0, 0
		freq := map[rune]int{}
		for _, r := range run {
			if r >= '0' && r <= '9' {
				digits++
			} else {
				letters++
			}
			freq[r]++
		}
		if letters < 2 || digits < 2 {
			return false
		}
		// 不同字元的個數:隨機字串通常很多(24 字以上至少 12 種)
		return len(freq) >= 12
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			run = append(run, r)
			continue
		}
		if check() {
			return true
		}
	}
	return check()
}

// mcpSecretValue 一段文字裡有沒有看起來像密鑰的值(佔位 ${名稱} 不算)
func mcpSecretValue(s string) bool {
	s = mcpPlaceholderRe.ReplaceAllString(s, " ")
	return mcpSecretPrefixRe.MatchString(s) || mcpSecretAuthRe.MatchString(s) || mcpRandomRun(s)
}

// mcpSecretName 名稱(旗標、環境變數、標頭、參數)看起來是放密鑰的:去掉符號後以這些字結尾
func mcpSecretName(n string) bool {
	k := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return -1
	}, n)
	if k == "" {
		return false
	}
	for _, x := range []string{"key", "token", "tokens", "secret", "secrets", "password", "passwd", "pwd", "passphrase", "auth",
		"authorization", "credential", "credentials", "cookie", "apikey", "accesskey", "privatekey", "bearer", "jwt", "signature"} {
		if strings.HasSuffix(k, x) {
			return true
		}
	}
	return false
}

// mcpSafeSecretSlot 放在密鑰旗標或 NAME= 後面的值可以接受:空、只有 ${名稱} 佔位、檔案路徑
func mcpSafeSecretSlot(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "./") || strings.HasPrefix(v, "../") {
		return true
	}
	rest := strings.TrimSpace(mcpPlaceholderRe.ReplaceAllString(v, ""))
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(rest, "Bearer"), "bearer"))
	return rest == "" && mcpPlaceholderRe.MatchString(v)
}

var mcpEnvAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// MCPPlaceholders args 裡用到的 ${名稱}(去重複,照出現順序)
func MCPPlaceholders(args []string) []string {
	out := []string{}
	for _, a := range args {
		for _, m := range mcpPlaceholderRe.FindAllStringSubmatch(a, -1) {
			if !containsStr(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

func containsStr(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

// CheckMCPLaunch stdio 伺服器的啟動設定裡不能有密鑰(回傳空字串 = 沒問題,否則是 400 的說明;說明不含密鑰本身)。
//   - command 只能是程式與固定參數:不能有 VAR=值 的環境指定(環境變數用 envNames)
//   - command、args、cwd 不能有常見密鑰前綴、Bearer、長隨機字串
//   - --token、--api-key、--password、--secret 這類旗標:=值 或下一個參數只能是 ${名稱} 佔位(或檔案路徑)
//   - NAME=值、Name: 值 這類寫法,名稱像密鑰時同上
//   - args 的 ${名稱} 佔位要登記在 envNames(App 用該裝置的密鑰代入);command、cwd 不能有佔位
func CheckMCPLaunch(command string, args []string, cwd string, envNames []string) string {
	fields := strings.Fields(command)
	for i, f := range fields {
		if mcpEnvAssignRe.MatchString(f) {
			return "啟動指令不能用 VAR=值 指定環境變數;" + mcpSecretHint
		}
		if i == 0 && (f == "env" || strings.HasSuffix(f, "/env")) {
			for _, rest := range fields[1:] {
				if strings.Contains(rest, "=") {
					return "啟動指令不能用 env VAR=值 指定環境變數;" + mcpSecretHint
				}
			}
		}
	}
	if mcpPlaceholderRe.MatchString(command) || mcpPlaceholderRe.MatchString(cwd) {
		return "${名稱} 佔位只能用在 args"
	}
	if mcpSecretValue(cwd) {
		return "工作目錄(cwd)裡有看起來像密鑰的值;" + mcpSecretHint
	}
	if why := mcpCheckArgs(fields, "啟動指令"); why != "" {
		return why
	}
	if why := mcpCheckArgs(args, "args"); why != "" {
		return why
	}
	for _, n := range MCPPlaceholders(args) {
		if !ValidMCPEnvName(n) {
			return "args 的佔位 ${" + ClipMCPText(n, true) + "} 名稱格式不對(要是英文、數字、底線,不能數字開頭)"
		}
		found := false
		for _, e := range envNames {
			if e == n {
				found = true
			}
		}
		if !found {
			return "args 的佔位 ${" + n + "} 要先登記在 envNames(App 用各裝置存的同名密鑰代入)"
		}
	}
	return ""
}

// mcpCheckArgs 一串命令列參數裡有沒有密鑰(說明只講第幾個,不帶值)
func mcpCheckArgs(args []string, what string) string {
	for i, a := range args {
		pos := fmt.Sprintf("%s第 %d 個", what, i+1)
		if mcpSecretValue(a) {
			return pos + "看起來是密鑰;" + mcpSecretHint
		}
		t := strings.TrimSpace(a)
		// --name=值 / NAME=值
		if name, val, ok := strings.Cut(t, "="); ok && mcpSecretName(strings.TrimLeft(name, "-")) && !mcpSafeSecretSlot(val) {
			return pos + "把密鑰直接寫在參數裡;" + mcpSecretHint
		}
		// Name: 值(標頭寫法)
		if name, val, ok := strings.Cut(t, ":"); ok && !strings.ContainsAny(name, " /") && mcpSecretName(name) && !mcpSafeSecretSlot(val) {
			return pos + "把密鑰直接寫在參數裡;" + mcpSecretHint
		}
		// --token 值(下一個參數是值;下一個是另一個旗標就不算)
		if strings.HasPrefix(t, "-") && !strings.Contains(t, "=") && mcpSecretName(strings.TrimLeft(t, "-")) && i+1 < len(args) {
			next := strings.TrimSpace(args[i+1])
			if !strings.HasPrefix(next, "-") && !mcpSafeSecretSlot(next) {
				return fmt.Sprintf("%s第 %d 個", what, i+2) + "是旗標 " + ClipMCPText(t, true) + " 的值,看起來是密鑰;" + mcpSecretHint
			}
		}
	}
	return ""
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
