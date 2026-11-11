package core

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// 管理連線設定、登入、帶認證的請求(同 Swift 的 ConnectionStore)。畫面與其他功能(例如旁白發佈)都透過這裡呼叫外部服務:
//
//	core.ConnRequest(a, id, method, path, body)     帶認證呼叫,回傳解析後的 JSON(呼叫時不能拿著 a.Mu)
//	core.ConnRequestRaw(a, id, method, path, body)  同上,回傳原始回應
//	core.ConnFind(a, id) / core.ConnHasToken(a, c)  找連線(拿著鎖)/ 有沒有登入(不能拿著鎖)
//
// 鎖的規則:a.Mu 只保護 connState;鑰匙圈(外部程式)與網路請求都在鎖外。

type connState struct {
	List   []Connection
	States map[string]*ConnectionState
	Scope  string // 鑰匙圈的組織前綴
	Gen    int    // 每次重新讀組織資料加一,背景工作用來判斷是不是過期了

	GH     GHSettings
	GHSnap GHSnapshot

	GCP     GCPSettings
	GCPSnap GCPSnapshot

	GL     GLSettings
	GLSnap GLSnapshot

	VisitorsConn string
	loadedOnce   bool
}

// BackgroundAccountCheck 讀組織資料後在背景確認登入(同 Swift 的 ConnectionStore.load);跑 go test 時預設關掉
var BackgroundAccountCheck = !testing.Testing()

func (a *App) conns() *connState {
	if s, ok := a.Ext["connections"].(*connState); ok {
		return s
	}
	s := &connState{States: map[string]*ConnectionState{}, GH: DefaultGHSettings(), GCP: DefaultGCPSettings(), GL: DefaultGLSettings(),
		GLSnap: emptyGLSnapshot(), VisitorsConn: "evoleap"}
	a.Ext["connections"] = s
	return s
}

func connTopics(a *App) { a.Changed("connections", "github", "gcp", "gitlab") }

func (a *App) connFile() string { return filepath.Join(a.DataDir(), "connections.json") }

// connLoad 讀目前組織的連線(拿著鎖)
func connLoad(a *App) {
	s := a.conns()
	s.Gen++
	s.Scope = orgScope(a.CurrentOrgID())
	s.States = map[string]*ConnectionState{}
	if list, ok := readConnections(a.connFile()); ok {
		s.List = list
	} else {
		s.List = DefaultConnections()
		connSave(a)
	}
}

func readConnections(path string) ([]Connection, bool) {
	b, err := ReadData(path)
	if err != nil {
		return nil, false
	}
	var list []Connection
	if json.Unmarshal(b, &list) != nil || list == nil {
		return nil, false
	}
	return list, true
}

func connSave(a *App) { _ = WriteJSON(a.connFile(), a.conns().List) }

// ConnFind 依 id 找連線(拿著鎖呼叫)
func ConnFind(a *App, id string) (Connection, bool) {
	for _, c := range a.conns().List {
		if c.ID == id {
			return c, true
		}
	}
	return Connection{}, false
}

// ConnScope 目前組織的鑰匙圈前綴(拿著鎖呼叫)
func ConnScope(a *App) string { return a.conns().Scope }

// ConnHasToken 不用認證或鑰匙圈裡有 token(會讀鑰匙圈,不能拿著鎖)
func ConnHasToken(a *App, c Connection) bool {
	if c.Auth == AuthNone {
		return true
	}
	a.Mu.Lock()
	scope := a.conns().Scope
	a.Mu.Unlock()
	return vaultTokens(scope, c.ID) != nil
}

// connHasTokenCached 只看快取(畫面用,拿著鎖呼叫)
func connHasTokenCached(a *App, c Connection) bool {
	if c.Auth == AuthNone {
		return true
	}
	has, _ := vaultPeek(tokenAccount(a.conns().Scope, c.ID))
	return has
}

func connSetState(a *App, id string, change func(*ConnectionState)) {
	a.Mu.Lock()
	s := a.conns()
	st := s.States[id]
	if st == nil {
		st = &ConnectionState{}
		s.States[id] = st
	}
	change(st)
	a.Mu.Unlock()
	connTopics(a)
}

func connStateCopy(a *App, id string) *ConnectionState {
	if st := a.conns().States[id]; st != nil {
		c := *st
		return &c
	}
	return nil
}

// ConnUpsert 新增或更新連線;網址、認證方式或 client_id 換了,舊的 token 不能再用(不能拿著鎖)
func ConnUpsert(a *App, c Connection) {
	a.Mu.Lock()
	s := a.conns()
	clear := false
	found := false
	for i, old := range s.List {
		if old.ID == c.ID {
			found = true
			s.List[i] = c
			if old.BaseURL != c.BaseURL || old.Auth != c.Auth || old.OAuth.ClientID != c.OAuth.ClientID {
				clear = true
				s.States[c.ID] = &ConnectionState{}
			}
			break
		}
	}
	if !found {
		s.List = append(s.List, c)
	}
	connSave(a)
	scope := s.Scope
	a.Mu.Unlock()
	if clear {
		vaultSetTokens(scope, c.ID, nil)
	}
	connTopics(a)
}

// ConnDelete 刪除連線與它在鑰匙圈裡的東西
func ConnDelete(a *App, id string) {
	a.Mu.Lock()
	s := a.conns()
	scope := s.Scope
	kept := s.List[:0]
	for _, c := range s.List {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	s.List = kept
	delete(s.States, id)
	connSave(a)
	a.Mu.Unlock()
	vaultRemoveAll(scope, id)
	connTopics(a)
}

// ConnNewID 由名稱產生 id(拿著鎖呼叫),同 Swift 的 newId(for:)
func ConnNewID(a *App, name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	parts := []string{}
	for _, p := range strings.Split(b.String(), "-") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	base := strings.Join(parts, "-")
	if base == "" {
		base = "conn"
	}
	id, n := base, 2
	for {
		if _, taken := ConnFind(a, id); !taken {
			return id
		}
		id = base + "-" + itoa(n)
		n++
	}
}

func itoa(n int) string { return formatInt(int64(n)) }

// ConnSetClientSecret client secret 存鑰匙圈,設定只記有沒有
func ConnSetClientSecret(a *App, id string, secret *string) {
	if secret != nil && *secret == "" {
		secret = nil
	}
	a.Mu.Lock()
	scope := a.conns().Scope
	a.Mu.Unlock()
	vaultSetSecret(scope, id, secret)
	_, has := vaultSecret(scope, id)
	a.Mu.Lock()
	s := a.conns()
	for i := range s.List {
		if s.List[i].ID == id {
			s.List[i].OAuth.HasClientSecret = has
			connSave(a)
		}
	}
	a.Mu.Unlock()
	connTopics(a)
}

// ConnSetBearer 固定 token(空白表示清掉)。Swift 會在背景確認帳號;Go 版由呼叫的人決定要不要接著 ConnRefreshAccount。
func ConnSetBearer(a *App, id, token string) {
	t := strings.TrimSpace(token)
	a.Mu.Lock()
	scope := a.conns().Scope
	a.Mu.Unlock()
	if t == "" {
		vaultSetTokens(scope, id, nil)
	} else {
		vaultSetTokens(scope, id, &TokenSet{AccessToken: t, TokenType: strPtr("Bearer")})
	}
	connSetState(a, id, func(s *ConnectionState) { *s = ConnectionState{} })
}

// ConnDiscover OAuth:從網址探索端點,填進設定
func ConnDiscover(a *App, id string) {
	a.Mu.Lock()
	c, ok := ConnFind(a, id)
	a.Mu.Unlock()
	if !ok {
		return
	}
	connSetState(a, id, func(s *ConnectionState) { s.Busy = true; s.Error = nil })
	defer connSetState(a, id, func(s *ConnectionState) { s.Busy = false })
	m, err := oauthDiscover(c)
	if err != nil {
		connSetState(a, id, func(s *ConnectionState) { s.Error = strPtr(err.Error()) })
		return
	}
	n := c
	n.OAuth.Issuer = m.Issuer
	n.OAuth.AuthorizationEndpoint = m.AuthorizationEndpoint
	n.OAuth.TokenEndpoint = m.TokenEndpoint
	n.OAuth.RegistrationEndpoint = m.RegistrationEndpoint
	if n.OAuth.Resource == "" {
		n.OAuth.Resource = m.Resource
	}
	ConnUpsert(a, n)
	msg := "找到授權伺服器 " + m.Issuer
	if m.RegistrationEndpoint == "" {
		msg += "(不支援自動註冊,需要 client_id)"
	} else {
		msg += "(支援自動註冊)"
	}
	connSetState(a, id, func(s *ConnectionState) { s.LastTest = strPtr(msg) })
}

// ConnLogin 登入(OAuth 或瀏覽器核准會開瀏覽器、等回呼,完成才回傳)
func ConnLogin(a *App, id string) {
	a.Mu.Lock()
	c, ok := ConnFind(a, id)
	scope := a.conns().Scope
	a.Mu.Unlock()
	if !ok {
		return
	}
	connSetState(a, id, func(s *ConnectionState) { s.Busy = true; s.Error = nil; s.LastTest = nil })
	defer connSetState(a, id, func(s *ConnectionState) { s.Busy = false })
	var err error
	switch c.Auth {
	case AuthOAuth:
		var t *TokenSet
		var settings OAuthSettings
		t, settings, err = oauthLogin(c, scope)
		if err == nil {
			a.Mu.Lock()
			s := a.conns()
			for i := range s.List {
				if s.List[i].ID == id {
					s.List[i].OAuth = settings
					connSave(a)
				}
			}
			a.Mu.Unlock()
			vaultSetTokens(scope, id, t)
		}
	case AuthApproval:
		var t *TokenSet
		t, err = approvalLogin(c)
		if err == nil {
			vaultSetTokens(scope, id, t)
		}
	}
	if err != nil {
		connSetState(a, id, func(s *ConnectionState) { s.Error = strPtr(err.Error()) })
		return
	}
	ConnRefreshAccount(a, id)
	a.Mu.Lock()
	text := "Ops 登入「" + c.Name + "」"
	if st := a.conns().States[id]; st != nil && st.Account != nil {
		text += "(" + *st.Account + ")"
	}
	a.Log(Person(a.Me), "person.badge.key", text, "settings")
	a.Mu.Unlock()
}

// ConnLogout 登出:瀏覽器核准的連線會通知伺服器作廢 token
func ConnLogout(a *App, id string) {
	a.Mu.Lock()
	c, ok := ConnFind(a, id)
	scope := a.conns().Scope
	a.Mu.Unlock()
	if !ok {
		return
	}
	if c.Auth == AuthApproval {
		if t := vaultTokens(scope, id); t != nil {
			approvalLogout(c, t)
		}
	}
	vaultSetTokens(scope, id, nil)
	connSetState(a, id, func(s *ConnectionState) { *s = ConnectionState{} })
}

// ConnRefreshAccount 用測試路徑確認登入還有效,順便取得帳號名稱
func ConnRefreshAccount(a *App, id string) {
	a.Mu.Lock()
	c, ok := ConnFind(a, id)
	scope := a.conns().Scope
	a.Mu.Unlock()
	if !ok {
		return
	}
	if !ConnHasToken(a, c) {
		connSetState(a, id, func(s *ConnectionState) { s.Account = nil })
		return
	}
	var label *string
	if t := vaultTokens(scope, id); t != nil && t.IDToken != nil {
		if claims := jwtClaims(*t.IDToken); claims != nil {
			if l, ok := accountLabel(claims); ok {
				label = &l
			}
		}
	}
	if c.TestPath != "" {
		obj, err := ConnRequest(a, id, "GET", c.TestPath, nil)
		if err == nil {
			if l, ok := accountLabel(obj); ok {
				label = &l
			}
			msg := "測試連線成功(GET " + c.TestPath + ")"
			connSetState(a, id, func(s *ConnectionState) { s.LastTest = strPtr(msg); s.Error = nil })
		} else if e, ok := AsConnError(err); ok && e.IsUnauthorized() {
			connSetState(a, id, func(s *ConnectionState) { s.Account = nil; s.Error = strPtr(e.Message) })
			return
		} else {
			msg := "連不到 " + c.Name + ":" + err.Error()
			connSetState(a, id, func(s *ConnectionState) { s.Error = strPtr(msg) })
		}
	}
	if label == nil {
		if c.Auth == AuthNone {
			label = strPtr("不用登入")
		} else {
			label = strPtr("已登入")
		}
	}
	connSetState(a, id, func(s *ConnectionState) { s.Account = label })
}

// MARK: - 請求

// connBearer 取得要帶的 token;快過期時自動換新
func connBearer(c Connection, scope string) (*string, error) {
	if c.Auth == AuthNone {
		return nil, nil
	}
	t := vaultTokens(scope, c.ID)
	if t == nil {
		return nil, ConnError{Status: 401, Message: "還沒登入「" + c.Name + "」"}
	}
	if c.Auth == AuthOAuth && t.IsExpiring() {
		n, err := oauthRefresh(c, scope, t)
		if err != nil {
			return nil, err
		}
		t = n
		vaultSetTokens(scope, c.ID, t)
	}
	if c.Auth == AuthOAuth && c.OAuth.SendIDToken {
		if t.IDToken == nil {
			return nil, connErr("登入結果沒有 id_token,請在 scopes 加上 openid")
		}
		return t.IDToken, nil
	}
	return &t.AccessToken, nil
}

// ConnRequest 帶認證呼叫連線上的 API(同 Swift 的 ConnectionStore.request)。
// path 以 / 開頭(接在連線網址後面),或是和連線網址同網域的 https:// 完整網址;body 不是 nil 時送 JSON。
// 回傳解析後的 JSON(map[string]any、[]any…,不是 JSON 時是 nil);不是 2xx 時回傳 ConnError(401 是「登入已失效」)。
// 會發網路請求,呼叫時不能拿著 a.Mu。
func ConnRequest(a *App, id, method, path string, body []byte) (any, error) {
	r, err := connCheck(ConnRequestRaw(a, id, method, path, body))
	if err != nil {
		return nil, err
	}
	return r.JSON(), nil
}

// ConnRequestRaw 同 ConnRequest,但回傳原始回應(不檢查狀態碼)
func ConnRequestRaw(a *App, id, method, path string, body []byte) (*ConnResponse, error) {
	a.Mu.Lock()
	c, ok := ConnFind(a, id)
	scope := a.conns().Scope
	a.Mu.Unlock()
	if !ok {
		return nil, connErr("找不到連線 " + id)
	}
	// 完整網址:只允許和連線網址同一個網域(例如 www.googleapis.com 的 token 打 run.googleapis.com),token 不會送到別處
	full := path
	if !strings.HasPrefix(path, "https://") {
		full = c.TrimmedBase() + path
	}
	u, err := url.Parse(full)
	if err != nil {
		return nil, connErr("網址格式錯誤:" + path)
	}
	if !strings.HasPrefix(path, "/") && !c.AllowsHost(u.Hostname()) {
		h := u.Hostname()
		if h == "" {
			h = path
		}
		return nil, connErr("這個連線不能呼叫 " + h)
	}
	headers := map[string]string{}
	tok, err := connBearer(c, scope)
	if err != nil {
		return nil, err
	}
	if tok != nil {
		headers["Authorization"] = "Bearer " + *tok
	}
	if body != nil {
		headers["Content-Type"] = "application/json"
	}
	r, err := connSend(&c, method, full, headers, body)
	if err != nil {
		return nil, err
	}
	// OAuth 的 token 被提早撤銷:換新一次再試
	if r.Status == 401 && c.Auth == AuthOAuth {
		if t := vaultTokens(scope, id); t != nil && t.RefreshToken != nil {
			n, err := oauthRefresh(c, scope, t)
			if err != nil {
				return nil, err
			}
			vaultSetTokens(scope, id, n)
			bearer := n.AccessToken
			if c.OAuth.SendIDToken && n.IDToken != nil {
				bearer = *n.IDToken
			}
			headers["Authorization"] = "Bearer " + bearer
			r, err = connSend(&c, method, full, headers, body)
			if err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}

// MARK: - 組織、背景確認

// connBackground 讀組織資料後:讀鑰匙圈(預熱快取)、確認登入、切換組織時更新 GitHub / GCP
func connBackground(a *App, gen int, refreshServices bool) {
	a.Mu.Lock()
	s := a.conns()
	list := append([]Connection(nil), s.List...)
	a.Mu.Unlock()
	stale := func() bool {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return a.conns().Gen != gen
	}
	with := []string{}
	for _, c := range list {
		if stale() {
			return
		}
		if ConnHasToken(a, c) {
			with = append(with, c.ID)
		}
	}
	connTopics(a)
	for _, id := range with {
		if stale() {
			return
		}
		ConnRefreshAccount(a, id)
	}
	if refreshServices && !stale() {
		if ghLoggedIn(a) {
			GHRefresh(a)
		}
		if gcpLoggedIn(a) && gcpConfigured(a) {
			GCPRefresh(a)
		}
		if GLLoggedIn(a) {
			GLRefresh(a)
		}
	}
}

func init() {
	OnOrgLoad(func(a *App) {
		connLoad(a)
		ghLoad(a)
		gcpLoad(a)
		glLoad(a)
		s := a.conns()
		gen, switching := s.Gen, s.loadedOnce
		s.loadedOnce = true
		if BackgroundAccountCheck {
			go connBackground(a, gen, switching)
		}
	})
	OnOrgCreate(func(a *App, dir, template string) {
		if template == "copyCurrent" {
			s := a.conns()
			_ = WriteJSON(filepath.Join(dir, "connections.json"), s.List)
			_ = WriteJSON(filepath.Join(dir, "github.json"), s.GH)
			_ = WriteJSON(filepath.Join(dir, "gcp.json"), s.GCP)
			_ = WriteJSON(filepath.Join(dir, "gitlab.json"), s.GL)
			return
		}
		_ = WriteJSON(filepath.Join(dir, "connections.json"), []Connection{})
	})
	OnOrgDelete(func(a *App, id, dir string) {
		// 這個組織在鑰匙圈裡的登入一併清掉(鑰匙圈是外部程式,放到鎖外做)
		list, _ := readConnections(filepath.Join(dir, "connections.json"))
		scope := orgScope(id)
		go func() {
			for _, c := range list {
				vaultRemoveAll(scope, c.ID)
			}
		}()
	})

	RegisterView("connections", func(a *App) any {
		s := a.conns()
		states := map[string]any{}
		for id, st := range s.States {
			states[id] = st
		}
		return map[string]any{"connections": s.List, "states": states, "authKinds": AuthKinds}
	})

	connID := func(args map[string]any) (string, error) {
		id, _ := args["id"].(string)
		if id == "" {
			return "", UserError{"要給連線 id"}
		}
		return id, nil
	}
	stateOf := func(a *App, id string) any {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		c, ok := ConnFind(a, id)
		if !ok {
			return nil
		}
		return map[string]any{"connection": c, "state": connStateCopy(a, id)}
	}
	RegisterAction("connection_upsert", func(a *App, args map[string]any) (any, error) {
		var c Connection
		if err := ReadInto(args, &c); err != nil {
			return nil, err
		}
		c.Name = strings.TrimSpace(c.Name)
		c.BaseURL = strings.TrimSpace(c.BaseURL)
		if c.Name == "" || c.BaseURL == "" {
			return nil, UserError{"要給名稱與網址"}
		}
		if c.ID == "" {
			a.Mu.Lock()
			c.ID = ConnNewID(a, c.Name)
			a.Mu.Unlock()
		}
		ConnUpsert(a, c)
		return c, nil
	})
	RegisterAction("connection_delete", func(a *App, args map[string]any) (any, error) {
		id, err := connID(args)
		if err != nil {
			return nil, err
		}
		ConnDelete(a, id)
		return map[string]any{"ok": true}, nil
	})
	for name, f := range map[string]func(*App, string){
		"connection_login": ConnLogin, "connection_logout": ConnLogout,
		"connection_test": ConnRefreshAccount, "connection_discover": ConnDiscover,
	} {
		f := f
		RegisterAction(name, func(a *App, args map[string]any) (any, error) {
			id, err := connID(args)
			if err != nil {
				return nil, err
			}
			a.Mu.Lock()
			_, ok := ConnFind(a, id)
			a.Mu.Unlock()
			if !ok {
				return nil, UserError{"找不到連線 " + id}
			}
			f(a, id)
			return stateOf(a, id), nil
		})
	}
	RegisterAction("connection_secret", func(a *App, args map[string]any) (any, error) {
		id, err := connID(args)
		if err != nil {
			return nil, err
		}
		s, _ := args["secret"].(string)
		s = strings.TrimSpace(s)
		ConnSetClientSecret(a, id, &s)
		return stateOf(a, id), nil
	})
	RegisterAction("connection_bearer", func(a *App, args map[string]any) (any, error) {
		id, err := connID(args)
		if err != nil {
			return nil, err
		}
		t, _ := args["token"].(string)
		ConnSetBearer(a, id, t)
		ConnRefreshAccount(a, id)
		return stateOf(a, id), nil
	})
}

// ConnStateOf 連線狀態的副本(拿著鎖呼叫;沒有狀態時是 nil)
func ConnStateOf(a *App, id string) *ConnectionState { return connStateCopy(a, id) }
