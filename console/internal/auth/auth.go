// Package auth 是畫面(web)的登入:認證設定(auth.json)、LDAP 帳密驗證與 session。
// 驗證走核心(service.VerifyDirectory + core/ldapauth);一般登入的 session 是核心的 web session(omx_ token),
// 沒有核心可用時(沒有資料庫)與管理員修復登入才用這裡自己的 session,見 session.go。
//
// MCP 不經過這裡:MCP 在畫面連接埠的 /mcp,用 OAuth 授權(見 mcp/entry.go);同一台電腦的角色端點 /mcp/agent 用角色憑證。
//
// 設定存在 <資料夾>/auth.json(全部組織共用);服務帳號密碼存在鑰匙圈(或 OMIXOR_LDAP_BIND_PASSWORD)。
// 設定錯了把自己鎖在外面時:用 OMIXOR_AUTH=off 啟動,就會回到不用登入的本機模式。
package auth

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omixor-web/internal/core"

	"omixor-core/config"
	"omixor-core/httperr"
	"omixor-core/ldapauth"
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
	// Repair 管理員修復用的登入(對不上核心使用者,只能改認證設定)
	Repair bool `json:"-"`
}

// Secrets 服務帳號密碼的存放(預設鑰匙圈;測試換成記憶體)
type Secrets interface {
	Get() (string, bool)
	Set(password string) error
}

// Service 認證服務
type Service struct {
	mu      sync.Mutex
	cfg     Config
	secrets Secrets
	// sessions 沒有核心可用時與修復登入的 session(token → 內容),見 session.go
	sessions map[string]*session
	// failures 帳號失敗鎖定:網頁登入與 MCP 授權(LDAPSettings)共用
	failures service.LoginFailures
	// rate 網頁登入自己的每來源 / 全部頻率限制(登入成功不佔名額),和 MCP 授權頁分開
	rate service.LoginRate
	// 測試可以換掉
	now func() time.Time
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
	// 先撤銷再存:撤銷不了就不存,舊設定登入的人才不會在新設定下繼續用
	if err := revokeWebSessions(); err != nil {
		httperr.LogMasked("存認證設定:撤銷 web session", err)
		return core.StatusError{Status: 500, Message: "暫時無法讓目前的登入失效,設定沒有存,請稍後再試"}
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
	if err := revokeWebSessions(); err != nil { // 上面撤銷到存檔之間新登入的
		httperr.LogMasked("存認證設定:撤銷 web session", err)
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

// Verify 驗證帳密(不建立 session)。有核心時走 service.VerifyDirectory(帳號失敗鎖定、稽核和 MCP 授權同一份;
// 每來源 / 全部頻率限制是網頁登入自己的一份,只有沒通過的佔名額);
// 沒有資料庫時只做帳號失敗鎖定。失敗的種類(狀態碼由 web 的 authLoginStatus 依型別決定):
//   - 帳號不存在、密碼錯誤:同一個 ErrBadLogin(401,同一句話,不洩漏帳號存不存在)
//   - 被鎖或太頻繁:LockedError(429,帶 Retry-After)
//   - LDAP 連不上、服務帳號錯誤等診斷類失敗:503,固定句子(細節進 log)
func (s *Service) Verify(username, password, ip string) (User, error) {
	username = strings.TrimSpace(username)
	set, ok := s.LDAPSettings()
	if !ok {
		return User{}, core.UserError{Message: "沒有啟用 LDAP 登入"}
	}
	set.KeepCase = true
	set.Rate = &s.rate
	if !validCredentials(username, password) { // 不會送到目錄,不計次
		return User{}, ErrBadLogin
	}
	var id *ldapauth.Identity
	var err error
	if svc := core.AgentService(); svc != nil {
		id, err = svc.VerifyDirectory(set, username, password, ip)
		err = loginError(err)
	} else {
		id, err = s.verifyWithoutCore(set, username, password)
	}
	if err != nil {
		return User{}, err
	}
	return s.userOf(s.Config(), id, username), nil
}

// verifyWithoutCore 沒有核心時:鎖定規則同 service.VerifyDirectory(先預扣,密碼對清掉,目錄故障退回)
func (s *Service) verifyWithoutCore(set service.LDAPSettings, username, password string) (*ldapauth.Identity, error) {
	if wait, ok := s.failures.Reserve(username, s.now()); !ok {
		return nil, LockedError{RetryAfter: max(int(wait.Seconds()), 1)}
	}
	id, err := set.Dir.Authenticate(username, password)
	switch {
	case err == nil:
		s.failures.Succeeded(username)
		return id, nil
	case errors.Is(err, ldapauth.ErrInvalidCredentials):
		return nil, ErrBadLogin
	case errors.Is(err, ldapauth.ErrNotInGroup):
		s.failures.Succeeded(username)
		return nil, errNotInGroup
	}
	s.failures.Abort(username)
	return nil, directoryFailure(err)
}

// loginError service.VerifyDirectory 的錯誤 → 畫面的錯誤
func loginError(err error) error {
	if err == nil {
		return nil
	}
	var de *service.DirectoryError
	if errors.As(err, &de) {
		return directoryFailure(de.Cause)
	}
	var se *service.Error
	if !errors.As(err, &se) {
		return err
	}
	switch se.Code {
	case 401:
		return ErrBadLogin
	case 403:
		return errNotInGroup
	case 429:
		sec, _ := se.Data["retryAfter"].(int)
		return LockedError{RetryAfter: max(sec, 1)}
	}
	return err
}

// directoryFailure 目錄或設定的問題:一般使用者只看到固定句子(503),細節(位址、dial tcp)進 log;
// 管理員診斷(測試 LDAP、存設定)走 Authenticate,照樣拿得到 diagError 的細節
func directoryFailure(cause error) error {
	var de diagError
	var ue core.UserError
	if !errors.As(cause, &de) && !errors.As(cause, &ue) {
		cause = translate(cause)
	}
	if errors.As(cause, &de) {
		httperr.LogMasked("POST /api/auth/web/login LDAP", de)
		return core.StatusError{Status: 503, Message: de.user}
	}
	return cause
}

// UserSettings 某份設定(可能還沒存)怎麼把目錄帳號對到核心使用者:email 網域與是否信任目錄的 mail(同 LDAPSettings)
func (s *Service) UserSettings(c Config) service.LDAPSettings {
	return service.LDAPSettings{EmailDomain: c.effectiveEmailDomain(), TrustMail: c.effectiveTrustMail()}
}
