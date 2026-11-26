// Package auth 是畫面(web)的登入:LDAP 認證 + session cookie。
//
// MCP 不經過這裡:MCP 和 Swift 版一樣只聽 127.0.0.1、不需要登入(規格要一模一樣)。
//
// 設定存在 <資料夾>/auth.json(全部組織共用);服務帳號密碼存在鑰匙圈(或 OMIXOR_LDAP_BIND_PASSWORD)。
// 設定錯了把自己鎖在外面時:用 OMIXOR_AUTH=off 啟動,就會回到不用登入的本機模式。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omixor-web/internal/core"

	"omixor-core/config"
	"omixor-core/httperr"
	"omixor-core/service"
)

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
	// failures 帳號失敗鎖定:網頁登入與 MCP 授權(LDAPSettings)共用
	failures service.LoginFailures
	// 測試可以換掉
	now func() time.Time
}

type session struct {
	user    User
	expires time.Time
}

var ErrBadLogin = core.UserError{Message: "帳號或密碼錯誤"}

func configPath() string { return filepath.Join(core.DataRoot(), "auth.json") }

// New 讀 auth.json;OMIXOR_AUTH=off 時強制不用登入
func New(secrets Secrets) *Service {
	s := &Service{secrets: secrets, sessions: map[string]*session{}, now: time.Now}
	s.failures.Key = failureKey
	_ = core.ReadJSON(configPath(), &s.cfg)
	if config.Current().Str(config.Auth) == "off" {
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

// LockedError 登入失敗太多次被鎖:429,帶 retryAfter(秒;web 另外放進 Retry-After 標頭)
type LockedError struct{ RetryAfter int }

func (e LockedError) Error() string {
	return fmt.Sprintf("登入失敗太多次,請 %d 分鐘後再試", (e.RetryAfter+59)/60)
}

// HTTPError 實作 httperr.Public:429,data 帶 retryAfter(秒)
func (e LockedError) HTTPError() (int, map[string]any) {
	return 429, map[string]any{"retryAfter": e.RetryAfter}
}

// Login 驗證帳密,成功回傳 session token。失敗的種類(狀態碼由 web 的 authLoginStatus 依型別決定):
//   - 帳號不存在、密碼錯誤:同一個 ErrBadLogin(401,同一句話,不洩漏帳號存不存在)
//   - 被鎖:LockedError(429,帶 Retry-After)
//   - LDAP 連不上、服務帳號錯誤等診斷類失敗:503,固定句子(細節進 log)
//   - 存 session 失敗:500,固定句子(原文進 log)
func (s *Service) Login(username, password, _ string) (string, User, error) {
	username = strings.TrimSpace(username)
	cfg := s.Config()
	if cfg.Mode != "ldap" {
		return "", User{}, core.UserError{Message: "沒有啟用 LDAP 登入"}
	}
	if username == "" || password == "" { // 不會送到目錄,不計次
		return "", User{}, ErrBadLogin
	}
	// 先預扣再送目錄,並行嘗試也不會超過上限
	if wait, ok := s.failures.Reserve(username, s.now()); !ok {
		return "", User{}, LockedError{RetryAfter: max(int(wait.Seconds()), 1)}
	}
	u, err := s.Authenticate(cfg, username, password)
	if err != nil {
		var ue core.UserError
		switch {
		case errors.As(err, &ue) && ue == ErrBadLogin: // 帳密錯:預扣的那次算數
		case errors.As(err, &ue) && ue == errNotInGroup: // 密碼是對的
			s.failures.Succeeded(username)
		default: // 目錄故障、設定問題:不算
			s.failures.Abort(username)
		}
		// 連線、設定類的錯誤:一般使用者只看到固定句子(503),細節(位址、dial tcp、資料庫)進 log;
		// 管理員診斷(測試 LDAP、存設定)走 Authenticate,照樣拿得到 diagError 的細節
		var de diagError
		if errors.As(err, &de) {
			httperr.LogMasked("POST /api/auth/login LDAP", de)
			return "", User{}, core.StatusError{Status: 503, Message: de.user}
		}
		return "", User{}, err
	}
	s.failures.Succeeded(username)
	tok := randomToken()
	exp := s.now().Add(time.Duration(cfg.SessionHours) * time.Hour)
	s.mu.Lock()
	s.sessions[tok] = &session{user: u, expires: exp}
	s.mu.Unlock()
	// 有資料庫時 session 也存進去:重開 omixor-web 不用重新登入
	if core.DB() != nil {
		if err := core.SessionPut(tok, u, exp); err != nil {
			httperr.LogMasked("POST /api/auth/login 存 session", err) // 資料庫錯誤只進 log,不回給(未登入的)呼叫者
			s.mu.Lock()
			delete(s.sessions, tok) // 沒存起來的 session 不留在記憶體(否則這次回 500,token 卻其實可用)
			s.mu.Unlock()
			return "", User{}, core.StatusError{Status: 500, Message: "登入暫時失敗,請稍後再試;若持續發生請聯絡管理員"}
		}
	}
	return tok, u, nil
}

// UserSettings 某份設定(可能還沒存)怎麼把目錄帳號對到核心使用者:email 網域與是否信任目錄的 mail(同 LDAPSettings)
func (s *Service) UserSettings(c Config) service.LDAPSettings {
	return service.LDAPSettings{EmailDomain: c.effectiveEmailDomain(), TrustMail: c.effectiveTrustMail()}
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
	u := ss.user
	u.Admin = s.cfg.IsAdminAccount(u.ID) // 管理員照目前的名單,不用登入當下的快照
	return u, true
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
