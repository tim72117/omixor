// Package auth 是畫面(web)的登入:LDAP 認證 + session cookie。
//
// MCP 不經過這裡:MCP 和 Swift 版一樣只聽 127.0.0.1、不需要登入(規格要一模一樣)。
//
// 設定存在 <資料夾>/auth.json(全部組織共用);服務帳號密碼存在鑰匙圈(或 OPS_LDAP_BIND_PASSWORD)。
// 設定錯了把自己鎖在外面時:用 OPS_AUTH=off 啟動,就會回到不用登入的本機模式。
package auth

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"

	"opsweb/internal/core"
)

// Config:auth.json
type Config struct {
	// off:不用登入(只能從本機開);ldap:要用 LDAP 帳號登入
	Mode string `json:"mode"`
	LDAP LDAP   `json:"ldap"`
	// 登入有效幾小時,預設 12
	SessionHours int `json:"sessionHours"`
}

type LDAP struct {
	// ldap://host:389 或 ldaps://host:636
	URL string `json:"url"`
	// ldap:// 時先 StartTLS(建議開)
	StartTLS bool `json:"startTLS"`
	// 自簽憑證的 CA(PEM 檔路徑);空白用系統的
	CACert string `json:"caCert"`
	// 不驗證伺服器憑證(只給測試環境)
	InsecureSkipVerify bool `json:"insecureSkipVerify"`

	// 方式一:直接用使用者身分 bind。%s 換成帳號,例如 uid=%s,ou=people,dc=example,dc=com,
	// 或 Active Directory 的 %s@corp.example.com。有填這個就不用服務帳號。
	UserDNTemplate string `json:"userDNTemplate"`

	// 方式二:先用服務帳號搜尋使用者,再用找到的 DN 驗證密碼
	BindDN string `json:"bindDN"`
	// 服務帳號密碼不寫在這個檔案;這裡只記有沒有存
	HasBindPassword bool   `json:"hasBindPassword"`
	BaseDN          string `json:"baseDN"`
	// %s 換成(跳脫過的)帳號。OpenLDAP:(uid=%s);AD:(sAMAccountName=%s)
	UserFilter string `json:"userFilter"`

	// 屬性:帳號(Ops 裡的使用者 id)、顯示名稱、email
	IDAttr   string `json:"idAttr"`
	NameAttr string `json:"nameAttr"`
	MailAttr string `json:"mailAttr"`

	// 只允許這個群組的成員登入(群組 DN;空白表示所有人)
	RequiredGroup string `json:"requiredGroup"`
	// 這個群組的成員才能改認證設定(空白表示登入的人都可以)
	AdminGroup string `json:"adminGroup"`
	// 找群組的方式:%s 換成使用者 DN。預設 (|(member=%s)(uniqueMember=%s))
	GroupFilter string `json:"groupFilter"`
	// 群組從哪裡找,空白用 BaseDN
	GroupBaseDN string `json:"groupBaseDN"`
}

func (c *Config) normalize() {
	if c.Mode != "ldap" {
		c.Mode = "off"
	}
	if c.SessionHours <= 0 {
		c.SessionHours = 12
	}
	l := &c.LDAP
	if l.UserFilter == "" {
		l.UserFilter = "(uid=%s)"
	}
	if l.IDAttr == "" {
		l.IDAttr = "uid"
	}
	if l.NameAttr == "" {
		l.NameAttr = "cn"
	}
	if l.MailAttr == "" {
		l.MailAttr = "mail"
	}
	if l.GroupFilter == "" {
		l.GroupFilter = "(|(member=%s)(uniqueMember=%s))"
	}
}

// Validate 存檔前檢查
func (c Config) Validate() error {
	if c.Mode != "ldap" {
		return nil
	}
	l := c.LDAP
	u, err := url.Parse(l.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Host == "" {
		return core.UserError{Message: "LDAP 網址要寫成 ldap://主機:389 或 ldaps://主機:636"}
	}
	if l.UserDNTemplate == "" {
		if l.BindDN == "" || l.BaseDN == "" {
			return core.UserError{Message: "沒有填「使用者 DN 範本」時,要填服務帳號 DN 與搜尋起點(Base DN)"}
		}
		if !strings.Contains(l.UserFilter, "%s") {
			return core.UserError{Message: "使用者篩選要有 %s(會換成帳號),例如 (uid=%s)"}
		}
	} else if !strings.Contains(l.UserDNTemplate, "%s") {
		return core.UserError{Message: "使用者 DN 範本要有 %s(會換成帳號)"}
	}
	if (l.RequiredGroup != "" || l.AdminGroup != "") && l.UserDNTemplate == "" && l.BaseDN == "" && l.GroupBaseDN == "" {
		return core.UserError{Message: "要檢查群組時,要填群組的搜尋起點"}
	}
	return nil
}

// User 登入的人
type User struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Email  string   `json:"email,omitempty"`
	DN     string   `json:"dn,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Admin  bool     `json:"admin"`
}

// Secrets 服務帳號密碼的存放(預設鑰匙圈;測試換成記憶體)
type Secrets interface {
	Get() (string, bool)
	Set(password string) error
}

// Service 認證服務
type Service struct {
	mu       sync.Mutex
	cfg      Config
	secrets  Secrets
	sessions map[string]*session
	fails    map[string][]time.Time
	// 測試可以換掉
	now  func() time.Time
	dial func(cfg LDAP) (*ldap.Conn, error)
}

type session struct {
	user    User
	expires time.Time
}

var ErrBadLogin = core.UserError{Message: "帳號或密碼錯誤"}

func configPath() string { return filepath.Join(core.DataRoot(), "auth.json") }

// New 讀 auth.json;OPS_AUTH=off 時強制不用登入
func New(secrets Secrets) *Service {
	s := &Service{secrets: secrets, sessions: map[string]*session{}, fails: map[string][]time.Time{}, now: time.Now}
	s.dial = s.dialLDAP
	_ = core.ReadJSON(configPath(), &s.cfg)
	if os.Getenv("OPS_AUTH") == "off" {
		s.cfg.Mode = "off"
	}
	s.cfg.normalize()
	if secrets != nil {
		_, s.cfg.LDAP.HasBindPassword = secrets.Get()
	}
	return s
}

func (s *Service) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *Service) Enabled() bool { return s.Config().Mode == "ldap" }

// Save 存設定;bindPassword 是 nil 時不改密碼。改了設定,所有人要重新登入。
func (s *Service) Save(c Config, bindPassword *string) error {
	c.normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	if bindPassword != nil && s.secrets != nil {
		if err := s.secrets.Set(*bindPassword); err != nil {
			return fmt.Errorf("服務帳號密碼存不進鑰匙圈:%w", err)
		}
	}
	if s.secrets != nil {
		_, c.LDAP.HasBindPassword = s.secrets.Get()
	}
	if c.Mode == "ldap" && c.LDAP.UserDNTemplate == "" && !c.LDAP.HasBindPassword {
		return core.UserError{Message: "用服務帳號搜尋時,要存服務帳號密碼"}
	}
	if err := core.WriteJSON(configPath(), c); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = c
	s.sessions = map[string]*session{}
	s.mu.Unlock()
	if core.DB() != nil {
		core.SessionClear()
	}
	return nil
}

// MARK: 登入

const (
	maxFails   = 5
	failWindow = 5 * time.Minute
)

// Login 驗證帳密,成功回傳 session token
func (s *Service) Login(username, password, remote string) (string, User, error) {
	username = strings.TrimSpace(username)
	key := strings.ToLower(username) + "|" + remote
	if s.lockedOut(key) {
		return "", User{}, core.UserError{Message: "登入失敗太多次,請 5 分鐘後再試"}
	}
	cfg := s.Config()
	if cfg.Mode != "ldap" {
		return "", User{}, core.UserError{Message: "沒有啟用 LDAP 登入"}
	}
	u, err := s.Authenticate(cfg, username, password)
	if err != nil {
		var ue core.UserError
		if errors.As(err, &ue) && ue == ErrBadLogin {
			s.fail(key)
		}
		return "", User{}, err
	}
	tok := randomToken()
	exp := s.now().Add(time.Duration(cfg.SessionHours) * time.Hour)
	s.mu.Lock()
	s.sessions[tok] = &session{user: u, expires: exp}
	delete(s.fails, key)
	s.mu.Unlock()
	// 有資料庫時 session 也存進去:重開 ops-web 不用重新登入
	if core.DB() != nil {
		if err := core.SessionPut(tok, u, exp); err != nil {
			return "", User{}, core.UserError{Message: "存 session 失敗:" + err.Error()}
		}
	}
	return tok, u, nil
}

func (s *Service) lockedOut(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-failWindow)
	kept := s.fails[key][:0]
	for _, t := range s.fails[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.fails[key] = kept
	return len(kept) >= maxFails
}

func (s *Service) fail(key string) {
	s.mu.Lock()
	s.fails[key] = append(s.fails[key], s.now())
	s.mu.Unlock()
}

// User 由 token 找登入的人(過期回 false)
func (s *Service) User(token string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.sessions[token]
	if !ok && core.DB() != nil && token != "" {
		var u User
		if exp, found := core.SessionGet(token, &u); found {
			ss = &session{user: u, expires: exp}
			s.sessions[token] = ss
			ok = true
		}
	}
	if !ok {
		return User{}, false
	}
	if s.now().After(ss.expires) {
		delete(s.sessions, token)
		if core.DB() != nil {
			go core.SessionDelete(token)
		}
		return User{}, false
	}
	return ss.user, true
}

func (s *Service) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
	if core.DB() != nil {
		core.SessionDelete(token)
	}
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// MARK: LDAP

func (s *Service) dialLDAP(l LDAP) (*ldap.Conn, error) {
	tc := &tls.Config{InsecureSkipVerify: l.InsecureSkipVerify, MinVersion: tls.VersionTLS12}
	if u, err := url.Parse(l.URL); err == nil {
		tc.ServerName = u.Hostname()
	}
	if l.CACert != "" {
		pem, err := os.ReadFile(l.CACert)
		if err != nil {
			return nil, core.UserError{Message: "讀不到 CA 憑證:" + err.Error()}
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, core.UserError{Message: "CA 憑證不是有效的 PEM"}
		}
		tc.RootCAs = pool
	}
	conn, err := ldap.DialURL(l.URL, ldap.DialWithTLSConfig(tc), ldap.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}))
	if err != nil {
		return nil, core.UserError{Message: "連不到 LDAP 伺服器:" + err.Error()}
	}
	conn.SetTimeout(15 * time.Second)
	if l.StartTLS && strings.HasPrefix(strings.ToLower(l.URL), "ldap://") {
		if err := conn.StartTLS(tc); err != nil {
			conn.Close()
			return nil, core.UserError{Message: "StartTLS 失敗:" + err.Error()}
		}
	}
	return conn, nil
}

// Authenticate 驗證帳密並讀使用者資料與群組(不建立 session)
func (s *Service) Authenticate(cfg Config, username, password string) (User, error) {
	return s.AuthenticateWith(cfg, nil, username, password)
}

// AuthenticateWith 同 Authenticate,但服務帳號密碼用 bindPassword(還沒存的設定做「測試」時用;nil 用已存的)
func (s *Service) AuthenticateWith(cfg Config, bindPassword *string, username, password string) (User, error) {
	if err := func() error { c := cfg; c.normalize(); return c.Validate() }(); err != nil {
		return User{}, err
	}
	cfg.normalize()
	l := cfg.LDAP
	// 空密碼在 LDAP 是「匿名 bind」,會被當成成功:一律擋掉
	if username == "" || password == "" {
		return User{}, ErrBadLogin
	}
	if strings.ContainsAny(username, "\x00") || len(username) > 256 {
		return User{}, ErrBadLogin
	}
	conn, err := s.dial(l)
	if err != nil {
		return User{}, err
	}
	defer conn.Close()

	var userDN string
	var entry *ldap.Entry
	attrs := []string{l.IDAttr, l.NameAttr, l.MailAttr, "memberOf"}

	if l.UserDNTemplate != "" {
		// 方式一:直接 bind。帳號放進 DN 要依 DN 規則跳脫(UPN 形式不跳脫 @ 前的內容以外的東西)
		userDN = fillTemplate(l.UserDNTemplate, username)
		if err := conn.Bind(userDN, password); err != nil {
			return User{}, bindError(err)
		}
		// 讀自己的資料:DN 形式直接讀那一筆;UPN 形式在 BaseDN 底下用篩選找
		if strings.Contains(userDN, "=") {
			entry, _ = searchOne(conn, userDN, ldap.ScopeBaseObject, "(objectClass=*)", attrs)
		} else if l.BaseDN != "" {
			entry, _ = searchOne(conn, l.BaseDN, ldap.ScopeWholeSubtree, fmt.Sprintf(l.UserFilter, ldap.EscapeFilter(username)), attrs)
		}
		if entry != nil {
			userDN = entry.DN
		}
	} else {
		// 方式二:服務帳號搜尋 → 用找到的 DN 驗證密碼
		pw, _ := s.bindPassword()
		if bindPassword != nil {
			pw = *bindPassword
		}
		if pw == "" {
			return User{}, core.UserError{Message: "還沒有服務帳號密碼"}
		}
		if err := conn.Bind(l.BindDN, pw); err != nil {
			return User{}, core.UserError{Message: "服務帳號登入 LDAP 失敗,請檢查認證設定:" + err.Error()}
		}
		filter := strings.ReplaceAll(l.UserFilter, "%s", ldap.EscapeFilter(username))
		e, err := searchOne(conn, l.BaseDN, ldap.ScopeWholeSubtree, filter, attrs)
		if err != nil {
			if errors.Is(err, errNotFound) || errors.Is(err, errMany) {
				return User{}, ErrBadLogin
			}
			return User{}, core.UserError{Message: "搜尋使用者失敗:" + err.Error()}
		}
		entry, userDN = e, e.DN
		if err := conn.Bind(userDN, password); err != nil {
			return User{}, bindError(err)
		}
		// 之後查群組用服務帳號(使用者本人可能沒有讀群組的權限)
		if err := conn.Bind(l.BindDN, pw); err != nil {
			return User{}, core.UserError{Message: "服務帳號登入 LDAP 失敗:" + err.Error()}
		}
	}

	u := User{ID: username, Name: username, DN: userDN}
	if entry != nil {
		if v := entry.GetAttributeValue(l.IDAttr); v != "" {
			u.ID = v
		}
		if v := entry.GetAttributeValue(l.NameAttr); v != "" {
			u.Name = v
		}
		u.Email = entry.GetAttributeValue(l.MailAttr)
		u.Groups = entry.GetAttributeValues("memberOf")
	}
	if l.RequiredGroup != "" || l.AdminGroup != "" {
		base := l.GroupBaseDN
		if base == "" {
			base = l.BaseDN
		}
		if base != "" {
			f := strings.ReplaceAll(l.GroupFilter, "%s", ldap.EscapeFilter(userDN))
			if res, err := conn.Search(ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 10, false, f, []string{"dn"}, nil)); err == nil {
				for _, g := range res.Entries {
					u.Groups = append(u.Groups, g.DN)
				}
			}
		}
	}
	if l.RequiredGroup != "" && !inGroup(u.Groups, l.RequiredGroup) {
		return User{}, core.UserError{Message: "這個帳號不在允許使用 Ops 的群組裡"}
	}
	u.Admin = l.AdminGroup == "" || inGroup(u.Groups, l.AdminGroup)
	return u, nil
}

func (s *Service) bindPassword() (string, bool) {
	if p := os.Getenv("OPS_LDAP_BIND_PASSWORD"); p != "" {
		return p, true
	}
	if s.secrets == nil {
		return "", false
	}
	return s.secrets.Get()
}

var (
	errNotFound = errors.New("找不到")
	errMany     = errors.New("找到不只一筆")
)

func searchOne(conn *ldap.Conn, base string, scope int, filter string, attrs []string) (*ldap.Entry, error) {
	res, err := conn.Search(ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases, 2, 10, false, filter, attrs, nil))
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return nil, errNotFound
		}
		if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
			return nil, errMany
		}
		return nil, err
	}
	switch len(res.Entries) {
	case 0:
		return nil, errNotFound
	case 1:
		return res.Entries[0], nil
	}
	return nil, errMany
}

func bindError(err error) error {
	if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
		return ErrBadLogin
	}
	return core.UserError{Message: "LDAP 驗證失敗:" + err.Error()}
}

// fillTemplate:DN 形式(有 =)時依 RFC 4514 跳脫帳號;UPN 形式只擋特殊字元
func fillTemplate(tpl, username string) string {
	if strings.Contains(tpl, "=") {
		return strings.ReplaceAll(tpl, "%s", escapeDN(username))
	}
	return strings.ReplaceAll(tpl, "%s", username)
}

func escapeDN(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case strings.ContainsRune(`,+"\<>;=`, r):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '#' && i == 0, r == ' ' && (i == 0 || i == len(s)-1):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == 0:
			b.WriteString(`\00`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func inGroup(groups []string, want string) bool {
	for _, g := range groups {
		if strings.EqualFold(normalizeDN(g), normalizeDN(want)) {
			return true
		}
	}
	return false
}

func normalizeDN(dn string) string {
	if p, err := ldap.ParseDN(dn); err == nil {
		parts := []string{}
		for _, rdn := range p.RDNs {
			for _, a := range rdn.Attributes {
				parts = append(parts, strings.ToLower(a.Type)+"="+strings.ToLower(a.Value))
			}
		}
		return strings.Join(parts, ",")
	}
	return strings.ToLower(strings.ReplaceAll(dn, " ", ""))
}
