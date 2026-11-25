// Package config 是 omixor 設定(環境變數)的集中定義、載入與驗證。
//
//   - vars.go 定義每一個設定:名稱、舊名稱(OPS_*)、型別、預設值、說明、是否機密、屬於哪些程式。
//   - 各程式啟動時 Load(程式) 讀一次並驗證,錯誤用中文一次列完;之後只用回傳的 *Config,不再讀環境變數。
//   - 機密(密碼、token、金鑰、資料庫網址裡的密碼)不會出現在 String()、日誌與錯誤訊息裡。
//   - -h 的環境變數說明(Usage)與 docs/configuration.md(Markdown)都由同一份定義產生。
//
// 程式碼不要直接呼叫 os.Getenv(守門測試會檢查);底層的新舊名稱相容在 omixor-core/env。
package config

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"omixor-core/env"
)

// Kind 設定值的型別
type Kind int

const (
	String   Kind = iota // 任意字串
	Int                  // 整數(可有 Range)
	Float                // 小數
	Bool                 // 1/true/yes/on 或 0/false/no/off
	Duration             // 時間長度,例如 30s、5m
	URL                  // 網址(可限制 Schemes)
	Path                 // 檔案或資料夾路徑
	Enum                 // 只能是 Choices 其中之一(不分大小寫,取值時一律小寫)
	Conn                 // 資料庫連線:網址形式(有 ://,檢查 Schemes)或 key=value 形式
	Flag                 // 旗標:有設(非空)就算開,任何值都一樣(含 0、false);Bool() 回傳有沒有設
	Gate                 // 安全閘門:只有精確的 1 才算開,其他值一律當沒設;Bool() 只認 1
)

func (k Kind) String() string {
	return [...]string{"字串", "整數", "小數", "布林", "時間長度", "網址", "路徑", "選項", "網址或 key=value", "旗標(有設就算)", "閘門(只認 1)"}[k]
}

// Program 用到設定的程式
type Program string

const (
	Server Program = "server" // omixor-server
	Local  Program = "local"  // omixor-local
	Web    Program = "web"    // omixor-web(console)
	CLI    Program = "cli"    // omixor
	MCP    Program = "mcp"    // omixor-mcp
)

// Programs 所有程式(文件與 -h 的順序)
var Programs = []Program{Server, Local, Web, CLI, MCP}

// Binary 程式的執行檔名稱
func (p Program) Binary() string {
	switch p {
	case CLI:
		return "omixor"
	}
	return "omixor-" + string(p)
}

func progs(p ...Program) []Program { return p }

// Var 一個設定的定義
type Var struct {
	Name        string    // 環境變數名稱
	Legacy      []string  // 舊名稱(新名稱沒設才讀,讀到記一次 log)
	Kind        Kind      // 型別
	Default     string    // 預設值(可執行的,沒設時用它)
	DefaultDoc  string    // 預設值的文字說明(預設是算出來的或依程式而定時用;有 Default 時不用寫)
	Doc         string    // 說明(繁體中文)
	Secret      bool      // 機密:不印出值
	Group       string    // 文件裡的群組
	Programs    []Program // 哪些程式會讀
	RequiredFor []Program // 哪些程式必填
	Choices     []string  // Kind==Enum 的選項
	Schemes     []string  // Kind==URL 時允許的 scheme(空 = 不限)
	Range       *[2]int   // Kind==Int 時的範圍(含兩端;上限 math.MaxInt = 不設上限)
	Soft        bool      // 型別不對只在啟動時警告、不讓程式起不來(對應原版「不認得的值一律忽略」的設定)
}

// All 所有正式設定,依定義順序
var All []*Var

var byName = map[string]*Var{}

func add(v *Var) *Var {
	for _, n := range append([]string{v.Name}, v.Legacy...) {
		if _, dup := byName[n]; dup {
			panic("config: 名稱重複 " + n)
		}
		byName[n] = v
	}
	if v.Doc == "" || v.Group == "" || len(v.Programs) == 0 {
		panic("config: " + v.Name + " 缺說明、群組或程式")
	}
	All = append(All, v)
	return v
}

// Lookup 依名稱找設定(只認新名稱與舊名稱)
func Lookup(name string) (*Var, bool) { v, ok := byName[name]; return v, ok }

func (v *Var) usedBy(p Program) bool {
	for _, x := range v.Programs {
		if x == p {
			return true
		}
	}
	return false
}

func (v *Var) requiredFor(p Program) bool {
	for _, x := range v.RequiredFor {
		if x == p {
			return true
		}
	}
	return false
}

// check 檢查值的型別;錯誤訊息不含機密值
func (v *Var) check(raw string) error {
	s := strings.TrimSpace(raw)
	show := func() string { // 錯誤訊息裡的值:機密不印
		if v.Secret {
			return "(值已隱藏)"
		}
		return fmt.Sprintf("(收到 %q)", raw)
	}
	switch v.Kind {
	case Int:
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("%s 要是整數%s", v.Name, show())
		}
		if v.Range != nil && (n < v.Range[0] || n > v.Range[1]) {
			return fmt.Errorf("%s 要在 %d 到 %d 之間%s", v.Name, v.Range[0], v.Range[1], show())
		}
	case Float:
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return fmt.Errorf("%s 要是數字%s", v.Name, show())
		}
	case Bool:
		if _, ok := parseBool(s); !ok {
			return fmt.Errorf("%s 要是 1/true/yes/on 或 0/false/no/off%s", v.Name, show())
		}
	case Duration:
		if _, err := time.ParseDuration(s); err != nil {
			return fmt.Errorf("%s 要是時間長度,例如 30s、5m%s", v.Name, show())
		}
	case URL:
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "" || !strings.Contains(s, "://") {
			return fmt.Errorf("%s 要是網址(含 scheme,例如 https://主機)%s", v.Name, show())
		}
		if len(v.Schemes) > 0 && !contains(v.Schemes, strings.ToLower(u.Scheme)) {
			return fmt.Errorf("%s 的網址要用 %s 開頭%s", v.Name, strings.Join(v.Schemes, "、"), show())
		}
	case Enum:
		if !contains(v.Choices, strings.ToLower(s)) {
			return fmt.Errorf("%s 只能是 %s%s", v.Name, strings.Join(v.Choices, "、"), show())
		}
	case Conn:
		if i := strings.Index(s, "://"); i > 0 {
			if len(v.Schemes) > 0 && !contains(v.Schemes, strings.ToLower(s[:i])) {
				return fmt.Errorf("%s 的網址要用 %s 開頭%s", v.Name, strings.Join(v.Schemes, "、"), show())
			}
		} else if !strings.Contains(s, "=") {
			return fmt.Errorf("%s 要是網址(postgres://…)或 key=value 形式(host=… user=… dbname=…)%s", v.Name, show())
		}
	case Path:
		if s == "" {
			return fmt.Errorf("%s 不能只有空白", v.Name)
		}
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off", "":
		return false, true
	}
	return false, false
}

// Config 載入後的設定。值唯讀;要換值用 With(複製一份)
type Config struct {
	raw  map[string]string // 名稱 → 讀到的值(只有有設的)
	warn []string
}

// ProcessEnv 讀行程環境變數(config 以外的地方需要「一個取值函式」時用,例如本機模式的 getenv 注入)
// 只能讀已定義的設定名稱(含舊名稱)與系統變數(SystemVars),其他名稱 panic(程式錯誤),不是任意讀環境變數的後門。
func ProcessEnv(name string) string {
	if _, ok := byName[name]; !ok && !contains(SystemVars, name) {
		panic("config: ProcessEnv 只能讀已定義的設定名稱:" + name)
	}
	return os.Getenv(name)
}

// LoadFrom 從 lookup(name → 值)載入全部設定(不管哪個程式用);不驗證,驗證用 Validate(只驗證該程式用到的)。
// 新名稱沒設時讀舊名稱,讀到記一次 log。(only 參數保留相容,已不影響載入的範圍)
func LoadFrom(lookup func(string) string, only ...Program) *Config {
	c := &Config{raw: map[string]string{}}
	for _, v := range All {
		val, ok := env.LookupFunc(lookup, v.Name, v.Legacy...)
		if !ok {
			continue
		}
		c.raw[v.Name] = val
		if lookup(v.Name) != "" {
			for _, old := range v.Legacy {
				if o := lookup(old); o != "" && o != val {
					c.warn = append(c.warn, fmt.Sprintf("%s 與舊名稱 %s 都有設而且值不同,使用 %s", v.Name, old, v.Name))
				}
			}
		}
	}
	return c
}

// Load 程式啟動時呼叫:讀行程環境變數、驗證(型別錯誤、必填缺漏、互斥組合),有問題回傳中文錯誤(一次列完);
// 沒問題回傳設定並設成全域(Current)。新舊名稱都有設而值不同時記 log 提醒
func Load(prog Program) (*Config, error) {
	c := LoadFrom(os.Getenv)
	for _, w := range c.warn {
		log.Print("設定提醒:" + w)
	}
	for _, w := range c.softWarnings(prog) {
		log.Print("設定提醒:" + w)
	}
	if err := c.Validate(prog); err != nil {
		return nil, err
	}
	Set(c)
	return c, nil
}

// MustLoad 同 Load,失敗時印錯誤並結束程式(給 main 用)
func MustLoad(prog Program) *Config {
	c, err := Load(prog)
	if err != nil {
		log.Fatalf("%s 啟動失敗:%v", prog.Binary(), err)
	}
	return c
}

// ---- 全域設定 ----

var (
	curMu sync.RWMutex
	cur   *Config
)

// Set 設定全域設定(main 啟動時由 Load 做;測試要換設定時也可以自己呼叫,結束後 Set(nil))
func Set(c *Config) { curMu.Lock(); cur = c; curMu.Unlock() }

// Current 全域設定。main 用 Load 設過就回傳那一份;沒設過(測試、函式庫直接被呼叫)就依目前的環境變數
// 即時載入全部設定,型別不對的值當作沒設(寬鬆,不驗證)
func Current() *Config {
	curMu.RLock()
	c := cur
	curMu.RUnlock()
	if c != nil {
		return c
	}
	return LoadFrom(os.Getenv)
}

// softWarnings prog 用到、型別不對但標了 Soft 的設定:只警告(值會被忽略),不讓啟動失敗
func (c *Config) softWarnings(prog Program) []string {
	var out []string
	for _, v := range All {
		if raw, ok := c.raw[v.Name]; ok && v.Soft && v.usedBy(prog) {
			if err := v.check(raw); err != nil {
				out = append(out, err.Error()+";這個值會被忽略")
			}
		}
	}
	return out
}

// ---- 取值 ----

// known 確認 v 是 vars.go 定義的設定;程式寫錯設定(自己 new 一個 Var、拼錯名稱)直接 panic,不會靜默拿到預設值
func (c *Config) known(v *Var) {
	if byName[v.Name] != v {
		panic("config: 未定義的設定 " + v.Name)
	}
}

// IsSet 有沒有設(非空)
func (c *Config) IsSet(v *Var) bool { c.known(v); _, ok := c.raw[v.Name]; return ok }

// Str 字串值;沒設回預設值
func (c *Config) Str(v *Var) string {
	c.known(v)
	if s, ok := c.raw[v.Name]; ok {
		if v.Kind == Enum {
			return strings.ToLower(strings.TrimSpace(s))
		}
		return s
	}
	return v.Default
}

// Int 整數值;沒設、型別不對或超出範圍時回預設值(驗證在 Validate)
func (c *Config) Int(v *Var) int {
	c.known(v)
	if s, ok := c.raw[v.Name]; ok && v.check(s) == nil {
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		return n
	}
	n, _ := strconv.Atoi(v.Default)
	return n
}

// Float 小數值;沒設或型別不對回預設值
func (c *Config) Float(v *Var) float64 {
	c.known(v)
	if s, ok := c.raw[v.Name]; ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f
		}
	}
	f, _ := strconv.ParseFloat(v.Default, 64)
	return f
}

// Bool 布林值;沒設或型別不對回預設值(沒給預設就是 false)
//
// Flag 型別:有設(非空)就是 true;Gate 型別:只有精確的 "1" 是 true。
func (c *Config) Bool(v *Var) bool {
	c.known(v)
	switch v.Kind {
	case Flag:
		_, ok := c.raw[v.Name]
		return ok
	case Gate:
		return c.raw[v.Name] == "1"
	}
	if s, ok := c.raw[v.Name]; ok {
		if b, ok := parseBool(strings.TrimSpace(s)); ok {
			return b
		}
	}
	b, _ := parseBool(v.Default)
	return b
}

// Duration 時間長度;沒設或型別不對回預設值
func (c *Config) Duration(v *Var) time.Duration {
	c.known(v)
	if s, ok := c.raw[v.Name]; ok {
		if d, err := time.ParseDuration(strings.TrimSpace(s)); err == nil {
			return d
		}
	}
	d, _ := time.ParseDuration(v.Default)
	return d
}

// Getenv 依名稱取值(含舊名稱與預設值),給還在用「取值函式」介面的程式(例如 ldapauth.FromEnv);
// 不是已定義的設定名稱會 panic(程式錯誤)
func (c *Config) Getenv(name string) string {
	v, ok := byName[name]
	if !ok {
		panic("config: 未定義的設定 " + name)
	}
	return c.Str(v)
}

// With 回傳一份複製,並把 v 設成 val(程式內部覆寫用,例如本機模式啟動後填上自己的網址)
func (c *Config) With(v *Var, val string) *Config {
	c.known(v)
	n := &Config{raw: make(map[string]string, len(c.raw)+1), warn: c.warn}
	for k, x := range c.raw {
		n.raw[k] = x
	}
	if val == "" {
		delete(n.raw, v.Name)
	} else {
		n.raw[v.Name] = val
	}
	return n
}

// ---- 驗證 ----

// Error 設定有問題(一次列完)
type Error struct{ Problems []string }

func (e *Error) Error() string {
	if len(e.Problems) == 1 {
		return "設定有問題:" + e.Problems[0]
	}
	return fmt.Sprintf("設定有 %d 個問題:\n  - %s", len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// rule 跨欄位的規則(必填、互斥、相依);回傳錯誤訊息不得含機密值
type rule struct {
	prog Program
	fn   func(c *Config) error
}

var rules = []rule{
	{Server, func(c *Config) error {
		// 設了 LDAP 的其他項目卻沒設 OMIXOR_LDAP_URL:LDAP 不會啟用,多半是漏設
		if c.IsSet(LDAPURL) {
			return nil
		}
		var set []string
		for _, v := range All {
			if v.Group == GroupLDAP && v != LDAPURL && v != LDAPBindPassword && c.IsSet(v) {
				set = append(set, v.Name)
			}
		}
		if len(set) > 0 {
			return fmt.Errorf("設了 %s,但沒設 OMIXOR_LDAP_URL,LDAP 登入不會啟用", strings.Join(set, "、"))
		}
		return nil
	}},
	{Server, func(c *Config) error {
		if c.IsSet(GitLabURL) && !c.IsSet(PublicURL) {
			return fmt.Errorf("設了 OMIXOR_GITLAB_URL,也要設 OMIXOR_PUBLIC_URL(GitLab 的回呼網址由它推導)")
		}
		return gitLabURLMissing(c)
	}},
	{Web, func(c *Config) error {
		// 兩個都設而連接埠不一樣:OMIXOR_WEB_ADDR 優先,OMIXOR_WEB_PORT 會被忽略,多半是設錯
		if c.IsSet(WebAddr) && c.IsSet(WebPort) {
			if i := strings.LastIndex(c.Str(WebAddr), ":"); i >= 0 && c.Str(WebAddr)[i+1:] != strconv.Itoa(c.Int(WebPort)) {
				return fmt.Errorf("OMIXOR_WEB_ADDR 與 OMIXOR_WEB_PORT 的連接埠不一樣;只設其中一個(OMIXOR_WEB_ADDR 優先)")
			}
		}
		return nil
	}},
	{Web, webOAuthRule},
	{Web, func(c *Config) error {
		// console 只有對外 MCP 連接埠的同意畫面用 GitLab 登入:沒開那個連接埠就不檢查
		if !c.IsSet(OAuthAddr) {
			return nil
		}
		return gitLabURLMissing(c)
	}},
}

// gitLabURLMissing 設了 GitLab 的其他項目卻沒設 OMIXOR_GITLAB_URL:GitLab 登入不會啟用,多半是漏設
func gitLabURLMissing(c *Config) error {
	if c.IsSet(GitLabURL) {
		return nil
	}
	var set []string
	for _, v := range All {
		if v.Group == GroupGitLab && v != GitLabURL && c.IsSet(v) {
			set = append(set, v.Name)
		}
	}
	if len(set) > 0 {
		return fmt.Errorf("設了 %s,但沒設 OMIXOR_GITLAB_URL,GitLab 登入不會啟用", strings.Join(set, "、"))
	}
	return nil
}

// webOAuthRule 設了 OMIXOR_OAUTH_ADDR(console 的對外 MCP 連接埠)時的相依:要有對外網址(issuer、resource、evoleap 回呼都由它推導)、
// 網址必須是 https(本機位址除外,開發用)且只有網址、evoleap 登入只有在啟用 GitLab 時才能關(console 沒有配對碼)、不能是遠端模式(授權與使用者在那台 server)。
// 需要 PostgreSQL 與否是執行期才知道的事,由 console 啟動時檢查
func webOAuthRule(c *Config) error {
	if !c.IsSet(OAuthAddr) {
		return nil
	}
	const pre = "設了 OMIXOR_OAUTH_ADDR,"
	if !c.IsSet(PublicURL) {
		return fmt.Errorf(pre + "也要設 OMIXOR_PUBLIC_URL(對外的 https 網址,例如 https://console.example.com)")
	}
	u, err := url.Parse(c.Str(PublicURL))
	if err != nil || u.Hostname() == "" {
		return nil // 網址本身的格式問題由 OMIXOR_PUBLIC_URL 的型別檢查回報
	}
	if p := strings.Trim(u.Path, "/"); p != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf(pre + "OMIXOR_PUBLIC_URL 只能是網址(不含路徑、查詢或片段)")
	}
	if u.Scheme != "https" && !IsLoopbackHost(u.Hostname()) {
		return fmt.Errorf(pre + "OMIXOR_PUBLIC_URL 必須是 https(只有 localhost、127.0.0.1、[::1] 可以用 http,開發用)")
	}
	if !c.Bool(OAuthEvoleapLogin) && !c.IsSet(GitLabURL) {
		return fmt.Errorf(pre + "OMIXOR_OAUTH_EVOLEAP_LOGIN 設 false 時要另外啟用 GitLab 登入(OMIXOR_GITLAB_URL):console 的同意畫面沒有配對碼,兩種都沒有就沒人能登入")
	}
	if c.IsSet(ServerURL) {
		return fmt.Errorf(pre + "但 OMIXOR_SERVER 設了(遠端模式):OAuth 要連本機 PostgreSQL 的 console,遠端模式請改在那台 omixor server 設 OMIXOR_PUBLIC_URL")
	}
	return nil
}

// IsLoopbackHost host(不含連接埠)是不是本機名稱:localhost、127.0.0.1、::1
func IsLoopbackHost(host string) bool {
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// ProxyHops 信任的代理層數(service.SetTrustedProxyHops 用):明確設了 OMIXOR_TRUSTED_PROXY_HOPS 就用;
// 沒設時,跑在 Cloud Run(有 K_SERVICE)是 1,否則 0(不信任 X-Forwarded-For)
func (c *Config) ProxyHops() int {
	if c.IsSet(TrustedProxyHops) {
		return c.Int(TrustedProxyHops)
	}
	if c.Str(KService) != "" {
		return 1
	}
	return 0
}

// Validate 檢查 prog 用到的設定:型別、範圍、必填、互斥/相依。問題一次列完(*Error);錯誤訊息不含機密值
func (c *Config) Validate(prog Program) error {
	var problems []string
	for _, v := range All {
		if !v.usedBy(prog) {
			continue
		}
		raw, set := c.raw[v.Name]
		switch {
		case set:
			if err := v.check(raw); err != nil && !v.Soft { // Soft:只警告(Load 記 log),不算問題
				problems = append(problems, err.Error())
			}
		case v.requiredFor(prog):
			problems = append(problems, v.Name+" 沒設(必填):"+v.Doc)
		}
	}
	for _, r := range rules {
		if r.prog == prog {
			if err := r.fn(c); err != nil {
				problems = append(problems, c.Redact(err.Error()))
			}
		}
	}
	if len(problems) > 0 {
		return &Error{Problems: problems}
	}
	return nil
}
