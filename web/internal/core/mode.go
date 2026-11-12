package core

import (
	"fmt"
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

// 預設的 omixor server(和 omixor CLI、Swift app 相同)
const defaultRemoteServer = "https://omixor-issues-340121279179.asia-east1.run.app"

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

// serverOrgID 內部的組織 id 換成伺服器上的組織 id
func serverOrgID(id string) string { return strings.TrimPrefix(id, RemoteOrgPrefix) }

func normalizeServer(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" {
		s = envServer()
	}
	if s == "" {
		s = defaultRemoteServer
	}
	if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
		return "", UserError{"伺服器網址要以 https:// 開頭"}
	}
	return s, nil
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
func remoteLogin(server string) (*model.User, error) {
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
	vaultSetTokens(remoteTokenScope, issueRemoteTokenID, &TokenSet{AccessToken: res.Token, ExpiresAt: exp, TokenType: strPtr("Bearer")})
	return &res.User, nil
}

// ---- 遠端組織清單 ----

func remoteOrgsPath() string { return filepath.Join(DataRoot(), "orgs-remote.json") }

func toRemoteOrganizations(list []model.Org) []Organization {
	out := make([]Organization, 0, len(list))
	for _, o := range list {
		name := o.Name
		if name == "" {
			name = o.ID
		}
		out = append(out, Organization{ID: RemoteOrgPrefix + o.ID, Name: name, Symbol: "building.2", Color: "blue",
			Note: remoteRoleNote(o.MyRole), CreatedAt: Time{o.CreatedAt.Time}})
	}
	return out
}

func remoteRoleNote(role string) string {
	switch role {
	case "admin":
		return "管理員"
	case "member":
		return "成員"
	}
	return ""
}

// fetchRemoteOrgs 從伺服器讀我的組織(連網;不要拿著 Mu 呼叫)
func fetchRemoteOrgs(server string) ([]Organization, error) {
	list, err := remoteClient(server).ListOrgs()
	if err != nil {
		return nil, err
	}
	return toRemoteOrganizations(list), nil
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
func (a *App) SetMode(mode, server string) error {
	switch mode {
	case ModeLocal:
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if currentMode().Mode == ModeLocal {
			return nil
		}
		a.enterMode(modeFile{Mode: ModeLocal}, nil)
		return nil
	case ModeRemote:
		srv, err := normalizeServer(server)
		if err != nil {
			return err
		}
		u, err := remoteLogin(srv)
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
	}
	vaultSetTokens(remoteTokenScope, issueRemoteTokenID, nil)
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
	// 還沒切到遠端時,輸入框預設的網址
	DefaultServer string `json:"defaultServer"`
}

func (a *App) accountView() AccountView {
	m := currentMode()
	v := AccountView{Mode: m.Mode, Server: m.Server, LocalStorage: "files", DefaultServer: defaultRemoteServer}
	if s := envServer(); s != "" {
		v.DefaultServer = s
	}
	if DB() != nil {
		v.LocalStorage = "postgres"
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
		if err := a.SetMode(mode, server); err != nil {
			return nil, err
		}
		return View2(a, "account"), nil
	})
	RegisterAction("remote_logout", func(a *App, args map[string]any) (any, error) {
		err := a.RemoteLogout()
		return View2(a, "account"), err
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
func (a *App) remoteCreateOrg(name string, sw bool) (any, error) {
	if name == "" {
		return nil, UserError{"要給組織名稱"}
	}
	srv := IssueRemoteURL()
	id := a.remoteNewOrgID(name)
	if _, err := remoteClient(srv).CreateOrg(id, name); err != nil {
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
		if o.ID == RemoteOrgPrefix+id {
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
