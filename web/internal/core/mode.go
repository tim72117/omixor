package core

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omixor-core/client"
	"omixor-core/model"
)

// 兩種資料來源,完全切開:
//
//	本機:組織、Issue 等都存在本機(檔案或 PostgreSQL),權限用本機的 LDAP / 資料庫授權
//	遠端:用 evoleap 授權登入 omixor server;組織清單、成員權限、Issue 都來自伺服器
//
// 目前的模式存在資料夾的 mode.json。遠端組織的 id 一律加 "~" 前綴(本機組織 id 不會有 "~"),
// 所以兩邊的資料夾(orgs/~xxx)、鑰匙圈、範例資料都不會撞名;組織清單另存 orgs-remote.json。
// 環境變數 OMIXOR_SERVER:沒有 mode.json 時,設了就以遠端模式啟動,並當作預設的伺服器網址。

const (
	ModeLocal  = "local"
	ModeRemote = "remote"

	// RemoteOrgPrefix 遠端組織的 id 前綴
	RemoteOrgPrefix = "~"
	// 遠端 omixor token 存在鑰匙圈的位置:不分組織,全部遠端組織共用同一個登入
	remoteTokenScope = "remote/"
)

type modeFile struct {
	Mode   string `json:"mode"`
	Server string `json:"server,omitempty"`
}

var (
	modeMu  sync.Mutex
	modeCur modeFile
	modeKey string // 讀進來的是哪個資料夾 + 環境變數(測試換資料夾時要重讀)
)

func modePath() string { return filepath.Join(DataRoot(), "mode.json") }

func envServer() string {
	for _, k := range []string{"OMIXOR_SERVER", "OPS_ISSUE_SERVER"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return ""
}

// ---- 啟動設定:可以連哪些遠端 omixor ----
//
// 沒有設定就沒有遠端,只能用本機(本機登入)。設定來源(合併,id 或網址重複的只留第一個):
//
//	環境變數 OMIXOR_SERVERS:JSON 陣列,或指向 JSON 檔的路徑
//	資料夾裡的 servers.json(和 OPS_DATA_DIR 同一個資料夾,直接讀檔,不經資料庫)
//	環境變數 OMIXOR_SERVER:單一網址(舊的設法)
//
// 格式:[{"id":"prod","name":"正式站","url":"https://…","auth":["evoleap"]}];auth 是可用的授權方式,沒寫就是 ["evoleap"]。

// AuthOption 一種授權方式
type AuthOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ServerConf 設定裡的一個遠端 omixor
type ServerConf struct {
	ID   string       `json:"id"`
	Name string       `json:"name"`
	URL  string       `json:"url"`
	Auth []AuthOption `json:"auth"`
}

// 支援的授權方式(之後有新的就加在這裡,並在 remoteLogin 實作)
var authLabels = map[string]string{"evoleap": "evoleap 帳號"}

type serverFileEntry struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	URL  string   `json:"url"`
	Auth []string `json:"auth"`
}

func parseServers(b []byte) []serverFileEntry {
	var l []serverFileEntry
	if json.Unmarshal(b, &l) != nil {
		return nil
	}
	return l
}

// ConfiguredServers 啟動設定裡的遠端 omixor(沒設定回傳空陣列)
func ConfiguredServers() []ServerConf {
	var raw []serverFileEntry
	if v := strings.TrimSpace(os.Getenv("OMIXOR_SERVERS")); v != "" {
		if strings.HasPrefix(v, "[") {
			raw = append(raw, parseServers([]byte(v))...)
		} else if b, err := os.ReadFile(v); err == nil {
			raw = append(raw, parseServers(b)...)
		}
	}
	if b, err := os.ReadFile(filepath.Join(DataRoot(), "servers.json")); err == nil {
		raw = append(raw, parseServers(b)...)
	}
	if u := envServer(); u != "" {
		raw = append(raw, serverFileEntry{ID: "default", URL: u})
	}
	out := []ServerConf{}
	seenID, seenURL := map[string]bool{}, map[string]bool{}
	for _, e := range raw {
		u := strings.TrimRight(strings.TrimSpace(e.URL), "/")
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			continue
		}
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = u
		}
		if seenID[id] || seenURL[u] {
			continue
		}
		seenID[id], seenURL[u] = true, true
		name := strings.TrimSpace(e.Name)
		if name == "" {
			name = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
		}
		methods := e.Auth
		if len(methods) == 0 {
			methods = []string{"evoleap"}
		}
		sc := ServerConf{ID: id, Name: name, URL: u, Auth: []AuthOption{}}
		for _, m := range methods {
			label := authLabels[m]
			if label == "" {
				label = m
			}
			sc.Auth = append(sc.Auth, AuthOption{ID: m, Label: label})
		}
		out = append(out, sc)
	}
	return out
}

// findServer 用 id 或網址找設定裡的遠端
func findServer(idOrURL string) (ServerConf, bool) {
	idOrURL = strings.TrimRight(strings.TrimSpace(idOrURL), "/")
	for _, s := range ConfiguredServers() {
		if s.ID == idOrURL || s.URL == idOrURL {
			return s, true
		}
	}
	return ServerConf{}, false
}

// 這次執行有沒有選過連接對象(有設定遠端時,啟動後要先選;登出會重設)
var (
	gateMu     sync.Mutex
	gateChosen bool
)

func gateOpen() bool {
	gateMu.Lock()
	defer gateMu.Unlock()
	return len(ConfiguredServers()) > 0 && !gateChosen
}

func setGateChosen(v bool) {
	gateMu.Lock()
	gateChosen = v
	gateMu.Unlock()
}

func currentMode() modeFile {
	modeMu.Lock()
	defer modeMu.Unlock()
	key := DataRoot() + "|" + envServer()
	if key != modeKey {
		modeKey = key
		var m modeFile
		if err := ReadJSON(modePath(), &m); err != nil || (m.Mode != ModeLocal && m.Mode != ModeRemote) {
			m = modeFile{Mode: ModeLocal}
			if s := envServer(); s != "" {
				m = modeFile{Mode: ModeRemote, Server: s}
			}
		}
		if m.Mode == ModeRemote && m.Server == "" {
			m.Server = envServer()
		}
		// 上次連的遠端已經不在啟動設定裡:回本機
		if m.Mode == ModeRemote {
			if _, ok := findServer(m.Server); !ok {
				m = modeFile{Mode: ModeLocal}
			}
		}
		modeCur = m
	}
	return modeCur
}

func setMode(m modeFile) {
	modeMu.Lock()
	defer modeMu.Unlock()
	modeKey = DataRoot() + "|" + envServer()
	modeCur = m
	_ = WriteJSON(modePath(), m)
}

// Mode 目前的模式:"local" 或 "remote"
func Mode() string { return currentMode().Mode }

// IsRemote 目前是不是遠端模式
func IsRemote() bool { return Mode() == ModeRemote }

// IssueRemoteURL 遠端模式下 omixor server 的網址(本機模式回傳 "")
func IssueRemoteURL() string {
	if m := currentMode(); m.Mode == ModeRemote {
		return m.Server
	}
	return ""
}

// IsRemoteOrg 這個組織 id 是不是遠端組織
func IsRemoteOrg(id string) bool { return strings.HasPrefix(id, RemoteOrgPrefix) }

// serverOrgID 內部的組織 id 換成伺服器上的組織 id:"~<伺服器>~<組織>" → "<組織>"(舊格式 "~<組織>" 也接受)
func serverOrgID(id string) string {
	rest := strings.TrimPrefix(id, RemoteOrgPrefix)
	if i := strings.Index(rest, RemoteOrgPrefix); i >= 0 {
		return rest[i+1:]
	}
	return rest
}

// serverKey 遠端 omixor 在本機的名稱(組織 id 前綴、快取檔名、鑰匙圈範圍都用它,讓不同伺服器的資料分開):
// 啟動設定裡的 id;不在設定裡就用網址的 host(:port)。只留英數字與 . _ -
func serverKey(server string) string {
	k := ""
	if sc, ok := findServer(server); ok {
		k = sc.ID
	} else if u, err := url.Parse(strings.TrimSpace(server)); err == nil && u.Host != "" {
		k = u.Host
	} else {
		k = server
	}
	var b strings.Builder
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}

// remoteOrgPrefixFor 這台伺服器的組織在本機的 id 前綴:"~<伺服器>~"
func remoteOrgPrefixFor(server string) string { return RemoteOrgPrefix + serverKey(server) + RemoteOrgPrefix }

// remoteTokenScopeFor 這台伺服器的 omixor token 在鑰匙圈的範圍(每台伺服器各一把)
func remoteTokenScopeFor(server string) string { return remoteTokenScope + serverKey(server) + "/" }

// isLegacyServer 舊版只支援一個遠端,舊格式的快取(orgs/~<組織>、鑰匙圈的 remote/)屬於設定裡的第一個遠端
func isLegacyServer(server string) bool {
	l := ConfiguredServers()
	return len(l) > 0 && serverKey(server) == serverKey(l[0].URL)
}

// remoteToken 這台伺服器存著的 omixor token;第一個遠端沒有時沿用舊格式的(並搬到新的範圍,舊的不刪)
func remoteToken(server string) *TokenSet {
	if t := vaultTokens(remoteTokenScopeFor(server), issueRemoteTokenID); t != nil && t.AccessToken != "" {
		return t
	}
	if isLegacyServer(server) {
		if t := vaultTokens(remoteTokenScope, issueRemoteTokenID); t != nil && t.AccessToken != "" {
			vaultSetTokens(remoteTokenScopeFor(server), issueRemoteTokenID, t)
			return t
		}
	}
	return nil
}

func setRemoteToken(server string, t *TokenSet) { vaultSetTokens(remoteTokenScopeFor(server), issueRemoteTokenID, t) }

// legacyOrgFiles 遠端組織資料夾裡會有的檔案(搬舊格式資料夾用)
var legacyOrgFiles = []string{"connections.json", "github.json", "gcp.json", "gitlab.json", "facebook.json", "jira.json"}

var legacyOrgCopied sync.Map

// copyLegacyRemoteOrg 第一個遠端的組織:新格式的資料夾還沒有的檔案,從舊格式 orgs/~<組織> 複製過來(舊的不刪;每個組織只做一次)
func copyLegacyRemoteOrg(server, id string) {
	if !isLegacyServer(server) {
		return
	}
	if _, done := legacyOrgCopied.LoadOrStore(id, true); done {
		return
	}
	old := filepath.Join(DataRoot(), "orgs", RemoteOrgPrefix+serverOrgID(id))
	dir := OrgDir(id)
	for _, f := range legacyOrgFiles {
		src, dst := filepath.Join(old, f), filepath.Join(dir, f)
		if Exists(dst) || !Exists(src) {
			continue
		}
		if b, err := ReadData(src); err == nil {
			_ = WriteData(dst, b)
		}
	}
}

// resolveServer 要連的遠端:只能是啟動設定裡有的(id 或網址);沒指定且只設了一個就用它
func resolveServer(idOrURL string) (ServerConf, error) {
	list := ConfiguredServers()
	if len(list) == 0 {
		return ServerConf{}, UserError{"沒有設定遠端 omixor:設環境變數 OMIXOR_SERVERS 或在資料夾放 servers.json,現在只能用本機"}
	}
	if strings.TrimSpace(idOrURL) == "" && len(list) == 1 {
		return list[0], nil
	}
	if s, ok := findServer(idOrURL); ok {
		return s, nil
	}
	return ServerConf{}, UserError{"這個 omixor server 不在啟動設定裡"}
}

// ---- 遠端帳號(快取,畫面用;不在拿著鎖時連網) ----

var (
	remoteMeMu  sync.Mutex
	remoteMeVal *model.User
	remoteMeErr string
)

func setRemoteMe(u *model.User, err error) {
	remoteMeMu.Lock()
	defer remoteMeMu.Unlock()
	remoteMeVal = u
	remoteMeErr = ""
	if err != nil {
		remoteMeErr = err.Error()
	}
}

func getRemoteMe() (*model.User, string) {
	remoteMeMu.Lock()
	defer remoteMeMu.Unlock()
	return remoteMeVal, remoteMeErr
}

func remoteClient(server string) *client.Client {
	return (&remoteBackend{base: server}).client("")
}

// remoteWhoAmI 向伺服器問「我是誰」(401 時 client 會自動用 evoleap 換新的 token 再試一次)
func remoteWhoAmI(server string) (*model.User, error) {
	me, err := remoteClient(server).Me()
	if err != nil {
		return nil, err
	}
	return &me.User, nil
}

// remoteLogin 用 evoleap 的瀏覽器核准登入,換一個 omixor token 存起來。
// 已經登入就直接回傳;會開瀏覽器等核准,所以呼叫時不能拿著 Mu
func remoteLogin(server, method string) (*model.User, error) {
	if method != "" && method != "evoleap" {
		return nil, UserError{"還不支援授權方式「" + method + "」"}
	}
	if u, err := remoteWhoAmI(server); err == nil {
		return u, nil
	}
	// 先試手上已有的 evoleap 連線(舊流程);沒有就走瀏覽器核准
	if _, err := issueRemoteExchange(server, "", ""); err == nil {
		if u, err := remoteWhoAmI(server); err == nil {
			return u, nil
		}
	}
	evoBase := strings.TrimRight(os.Getenv("OMIXOR_EVOLEAP"), "/")
	if evoBase == "" {
		evoBase = DefaultConnections()[0].BaseURL
	}
	evoToken, err := EvoleapLogin(evoBase, os.Getenv("OMIXOR_EVOLEAP_CONSOLE"))
	if err != nil {
		return nil, fmt.Errorf("evoleap 登入失敗:%v", err)
	}
	defer EvoleapLogout(evoBase, evoToken)
	c := client.New(server)
	c.HTTP.Timeout = 15 * time.Second
	res, err := c.LoginWithEvoleap(evoToken, clientLabel()+" 網頁版")
	if err != nil {
		if client.Status(err) == 0 {
			return nil, err
		}
		return nil, fmt.Errorf("omixor 拒絕登入:%v", err)
	}
	var exp *Time
	if res.ExpiresAt != nil {
		exp = &Time{res.ExpiresAt.Time}
	}
	setRemoteToken(server, &TokenSet{AccessToken: res.Token, ExpiresAt: exp, TokenType: strPtr("Bearer")})
	return &res.User, nil
}

// ---- 遠端組織清單 ----

// remoteOrgsPath 目前連的伺服器的組織清單快取(每台伺服器各一份)
func remoteOrgsPath() string {
	return filepath.Join(DataRoot(), "orgs-remote-"+serverKey(IssueRemoteURL())+".json")
}

func toRemoteOrganizations(server string, list []model.Org) []Organization {
	out := make([]Organization, 0, len(list))
	prefix := remoteOrgPrefixFor(server)
	for _, o := range list {
		copyLegacyRemoteOrg(server, prefix+o.ID)
		out = append(out, fromCoreOrg(o, prefix))
	}
	return out
}

// fetchRemoteOrgs 從伺服器讀我的組織(連網;不要拿著 Mu 呼叫)
func fetchRemoteOrgs(server string) ([]Organization, error) {
	list, err := remoteClient(server).ListOrgs()
	if err != nil {
		return nil, err
	}
	return toRemoteOrganizations(server, list), nil
}

// applyRemoteOrgs 把伺服器的組織清單換進目前的索引(目前的組織還在就不動,不在就選第一個)並存起來(拿著 Mu)
func (a *App) applyRemoteOrgs(orgs []Organization) bool {
	s := a.orgs()
	cur := s.Index.Current
	found := false
	for _, o := range orgs {
		found = found || o.ID == cur
	}
	if !found {
		cur = ""
		if len(orgs) > 0 {
			cur = orgs[0].ID
		}
	}
	changed := cur != s.Index.Current || string(Marshal(s.Index.Orgs)) != string(Marshal(orgs))
	s.Index = OrgIndex{Current: cur, Orgs: orgs}
	if changed {
		a.saveOrgs()
	}
	return changed
}

// SetMode 切換模式。切到遠端時若還沒登入,會先開瀏覽器用 evoleap 登入;登入失敗就留在原本的模式。
// 呼叫時不能拿著 Mu(會連網、等登入)。
func (a *App) SetMode(mode, server string) error { return a.SetModeAuth(mode, server, "") }

// SetModeAuth 同 SetMode,server 是設定裡的 id(或網址),auth 是授權方式(空的用該 server 的第一種)。
// 成功就算選過連接對象了(畫面不再顯示選擇畫面)
func (a *App) SetModeAuth(mode, server, auth string) error {
	switch mode {
	case ModeLocal:
		a.Mu.Lock()
		defer a.Mu.Unlock()
		setGateChosen(true)
		if currentMode().Mode == ModeLocal {
			return nil
		}
		a.enterMode(modeFile{Mode: ModeLocal}, nil)
		return nil
	case ModeRemote:
		sc, err := resolveServer(server)
		if err != nil {
			return err
		}
		srv := sc.URL
		if auth == "" {
			auth = sc.Auth[0].ID
		}
		u, err := remoteLogin(srv, auth)
		if err != nil {
			return err
		}
		setRemoteMe(u, nil)
		orgs, err := fetchRemoteOrgs(srv)
		if err != nil {
			return fmt.Errorf("讀不到組織清單:%v", err)
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		setGateChosen(true)
		a.enterMode(modeFile{Mode: ModeRemote, Server: srv}, orgs)
		return nil
	}
	return UserError{"模式只有 local 或 remote"}
}

// enterMode 換掉模式、組織索引,讓各功能重讀資料(拿著 Mu)
func (a *App) enterMode(m modeFile, remoteOrgs []Organization) {
	setMode(m)
	delete(a.Ext, "orgs")
	delete(a.Ext, "issues")
	a.orgs() // 依新模式讀索引(遠端是上次存的清單)
	if m.Mode == ModeRemote {
		a.applyRemoteOrgs(remoteOrgs)
	}
	a.LoadOrgData()
	a.Nav.FocusID = ""
	if m.Mode == ModeRemote {
		a.Log(a.Actor(), "cloud", "切換到遠端模式("+m.Server+")", "settings")
	} else {
		a.Log(a.Actor(), "internaldrive", "切換到本機模式", "settings")
	}
	go a.Changed("*")
}

// RemoteLogout 登出遠端:撤銷 token、清掉存著的登入,並切回本機模式(呼叫時不能拿著 Mu)
func (a *App) RemoteLogout() error {
	if srv := IssueRemoteURL(); srv != "" {
		_ = remoteClient(srv).Logout() // 連不上也照樣清掉本機的
		setRemoteToken(srv, nil)
		if isLegacyServer(srv) {
			vaultSetTokens(remoteTokenScope, issueRemoteTokenID, nil) // 舊格式的也清掉,免得又被沿用
		}
	}
	setRemoteMe(nil, nil)
	return a.SetMode(ModeLocal, "")
}

// ---- 畫面 ----

type AccountView struct {
	Mode   string `json:"mode"`
	Server string `json:"server"`
	// 本機
	LocalStorage string `json:"localStorage"` // "postgres" 或 "files"
	// 遠端
	LoggedIn bool   `json:"loggedIn"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Role     string `json:"role,omitempty"`
	Error    string `json:"error,omitempty"`
	// 啟動設定裡的遠端 omixor(空的就只能用本機);ServerID 是目前連的是哪一個
	Servers  []ServerConf `json:"servers"`
	ServerID string       `json:"serverId"`
	// Gate 有設定遠端、而且這次執行還沒選過連接對象:畫面要先顯示「選擇連接對象」
	Gate bool `json:"gate"`
}

func (a *App) accountView() AccountView {
	m := currentMode()
	v := AccountView{Mode: m.Mode, Server: m.Server, LocalStorage: "files", Servers: ConfiguredServers(), Gate: gateOpen()}
	if sc, ok := findServer(m.Server); ok && m.Mode == ModeRemote {
		v.ServerID = sc.ID
	}
	if DB() != nil {
		v.LocalStorage = "postgres"
	}
	if m.Mode == ModeLocal && DB() == nil {
		v.Error = errNoDB.Error()
	}
	if m.Mode == ModeRemote {
		u, errText := getRemoteMe()
		if u != nil {
			v.LoggedIn, v.Email, v.Name, v.Role = true, u.Email, u.Name, u.Role
		}
		v.Error = errText
		if e := a.issueSt().remoteErr; e != "" && v.Error == "" { // 畫面函式已拿著 Mu
			v.Error = e
		}
	}
	return v
}

func init() {
	RegisterView("account", func(a *App) any { return a.accountView() })
	RegisterAction("mode_set", func(a *App, args map[string]any) (any, error) {
		mode, _ := args["mode"].(string)
		server, _ := args["server"].(string)
		auth, _ := args["auth"].(string)
		if err := a.SetModeAuth(mode, server, auth); err != nil {
			return nil, err
		}
		a.Changed("account") // 選過連接對象了:畫面重新讀帳號狀態
		return View2(a, "account"), nil
	})
	RegisterAction("remote_logout", func(a *App, args map[string]any) (any, error) {
		err := a.RemoteLogout()
		return View2(a, "account"), err
	})
	// 登出後回到「選擇連接對象」
	RegisterAction("gate_reset", func(a *App, args map[string]any) (any, error) {
		setGateChosen(false)
		a.Changed("account")
		return View2(a, "account"), nil
	})

	// 遠端模式:啟動時問一次我是誰;之後每 30 秒更新帳號與組織清單(連網都在鎖外)
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for {
				if srv := IssueRemoteURL(); srv != "" {
					u, err := remoteWhoAmI(srv)
					setRemoteMe(u, err)
					if err == nil {
						if orgs, err := fetchRemoteOrgs(srv); err == nil {
							a.Mu.Lock()
							changed := IsRemote() && a.applyRemoteOrgs(orgs)
							if changed {
								a.LoadOrgData()
							}
							a.Mu.Unlock()
							if changed {
								a.Changed("*")
							} else {
								a.Changed("account")
							}
						}
					}
				}
				time.Sleep(30 * time.Second)
			}
		}()
	})
}

// remoteCreateOrg 在伺服器建立組織(要伺服器管理員);建好後更新清單,sw 時順便切過去。呼叫時不能拿著 Mu
func (a *App) remoteCreateOrg(name, symbol, color string, sw bool) (any, error) {
	if name == "" {
		return nil, UserError{"要給組織名稱"}
	}
	srv := IssueRemoteURL()
	id := a.remoteNewOrgID(name)
	if _, err := remoteClient(srv).CreateOrgWith(model.Org{ID: id, Name: name, Symbol: symbol, Color: color}); err != nil {
		return nil, fmt.Errorf("伺服器沒有建立組織:%v", err)
	}
	orgs, err := fetchRemoteOrgs(srv)
	if err != nil {
		return nil, err
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.applyRemoteOrgs(orgs)
	var org Organization
	for _, o := range orgs {
		if o.ID == remoteOrgPrefixFor(srv)+id {
			org = o
		}
	}
	if sw && org.ID != "" {
		a.SwitchOrg(org.ID)
	}
	go a.Changed("orgs")
	return org, nil
}

// remoteNewOrgID 由名稱產生伺服器上的組織 id(英數字與 -;和目前清單重複時加 -2、-3…)
func (a *App) remoteNewOrgID(name string) string {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	root := a.NewOrgID(name)
	taken := map[string]bool{}
	for _, o := range a.orgs().Index.Orgs {
		taken[serverOrgID(o.ID)] = true
	}
	id, n := root, 2
	for taken[id] {
		id = fmt.Sprintf("%s-%d", root, n)
		n++
	}
	return id
}

// View2 在鎖外取畫面資料(動作用)
func View2(a *App, name string) any { v, _ := View(a, name); return v }

// remoteUpdateOrg 改遠端組織的名稱、圖示、顏色、備註(寫到 omixor server;要伺服器管理員)。呼叫時不能拿著 Mu
func (a *App) remoteUpdateOrg(org Organization) (any, error) {
	srv := IssueRemoteURL()
	if _, err := remoteClient(srv).UpdateOrg(toCoreOrg(org)); err != nil {
		return nil, fmt.Errorf("伺服器沒有改組織:%v", err)
	}
	orgs, err := fetchRemoteOrgs(srv)
	if err != nil {
		return nil, err
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.applyRemoteOrgs(orgs)
	go a.Changed("orgs")
	for _, o := range orgs {
		if o.ID == org.ID {
			return o, nil
		}
	}
	return org, nil
}
