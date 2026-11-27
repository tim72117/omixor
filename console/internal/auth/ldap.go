package auth

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"omixor-web/internal/core"

	"omixor-core/config"
	"omixor-core/ldapauth"
	"omixor-core/service"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// LDAP 驗證本身在 omixor-core/ldapauth(和 omixor server 同一套實作);這裡只把 auth.json 轉成它的設定,
// 並把錯誤換成畫面用的訊息

const ldapTimeout = 15 * time.Second

// authenticator 依設定建立;bindPassword 非 nil 時用它(還沒存的設定做「測試」)
func (s *Service) authenticator(cfg Config, bindPassword *string) (*ldapauth.Authenticator, error) {
	cfg.normalize()
	l := cfg.LDAP
	c := ldapauth.Config{
		URL: l.URL, StartTLS: l.StartTLS && strings.HasPrefix(strings.ToLower(l.URL), "ldap://"),
		CACert: l.CACert, InsecureSkipVerify: l.InsecureSkipVerify,
		BaseDN: l.BaseDN, UserFilter: l.UserFilter, IDAttr: l.IDAttr, NameAttr: l.NameAttr, MailAttr: l.MailAttr,
		Timeout: ldapTimeout, TemplateReadSelf: true, RawNonDNTemplate: true,
		RequiredGroup: l.RequiredGroup, GroupFilter: l.GroupFilter, GroupBaseDN: l.GroupBaseDN,
	}
	if l.UserDNTemplate != "" { // 有範本就直接 bind,不帶服務帳號
		c.UserDNTemplate = l.UserDNTemplate
	} else {
		pw, _ := s.bindPassword()
		if bindPassword != nil {
			pw = *bindPassword
		}
		if pw == "" {
			return nil, core.UserError{Message: "還沒有服務帳號密碼"}
		}
		c.BindDN, c.BindPassword = l.BindDN, pw
	}
	a, err := ldapauth.New(c)
	if err != nil {
		return nil, diag("LDAP 設定不完整", "LDAP 的設定有問題,請聯絡管理員", err)
	}
	return a, nil
}

// Authenticate 驗證帳密並讀使用者資料(不建立 session)
func (s *Service) Authenticate(cfg Config, username, password string) (User, error) {
	return s.AuthenticateWith(cfg, nil, username, password)
}

// AuthenticateWith 同 Authenticate,但服務帳號密碼用 bindPassword(nil 用已存的)
func (s *Service) AuthenticateWith(cfg Config, bindPassword *string, username, password string) (User, error) {
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return User{}, err
	}
	// 空密碼在 LDAP 是「匿名 bind」,會被當成成功:一律擋掉
	username = strings.TrimSpace(username)
	if !validCredentials(username, password) {
		return User{}, ErrBadLogin
	}
	a, err := s.authenticator(cfg, bindPassword)
	if err != nil {
		return User{}, err
	}
	id, err := a.Authenticate(username, password)
	if err != nil {
		return User{}, translate(err)
	}
	return s.userOf(cfg, id, username), nil
}

func validCredentials(username, password string) bool {
	return username != "" && password != "" && !strings.ContainsRune(username, 0) && len(username) <= 256
}

// userOf 目錄身分 → 登入者;管理員照 cfg 的帳號名單
func (s *Service) userOf(cfg Config, id *ldapauth.Identity, typed string) User {
	u := User{ID: id.Account, Name: id.Name, Email: id.Email, DN: id.DN, Groups: id.Groups}
	if u.Name == "" {
		u.Name = typed
	}
	u.Admin = cfg.IsAdminAccount(u.ID)
	return u
}

var errNotInGroup = core.UserError{Message: "這個帳號不在允許使用 omixor 的群組裡"}

// translate ldapauth 的錯誤 → 畫面的錯誤
func translate(err error) error {
	switch {
	case errors.Is(err, ldapauth.ErrInvalidCredentials):
		return ErrBadLogin
	case errors.Is(err, ldapauth.ErrNotInGroup):
		return errNotInGroup
	case errors.Is(err, ldapauth.ErrCACertPEM):
		return core.UserError{Message: "CA 憑證不是有效的 PEM"}
	}
	var se *ldapauth.StageError
	if !errors.As(err, &se) {
		return diag("LDAP 驗證失敗", "LDAP 驗證失敗,請稍後再試;若持續發生請聯絡管理員", err)
	}
	m := stageMessages[se.Stage]
	return diag(m[0], m[1], se.Cause)
}

// stageMessages 每一步失敗時:給管理員診斷的開頭、給登入者的固定句子
var stageMessages = map[string][2]string{
	ldapauth.StageCACert:      {"讀不到 CA 憑證", "LDAP 的 CA 憑證設定有問題,請聯絡管理員"},
	ldapauth.StageDial:        {"連不到 LDAP 伺服器", "連不到 LDAP 伺服器,請稍後再試;若持續發生請聯絡管理員"},
	ldapauth.StageStartTLS:    {"StartTLS 失敗", "LDAP 伺服器的加密連線失敗,請聯絡管理員"},
	ldapauth.StageServiceBind: {"服務帳號登入 LDAP 失敗,請檢查認證設定", "LDAP 的服務帳號設定有問題,請聯絡管理員"},
	ldapauth.StageSearch:      {"搜尋使用者失敗", "查詢 LDAP 失敗,請稍後再試;若持續發生請聯絡管理員"},
	ldapauth.StageUserBind:    {"LDAP 驗證失敗", "LDAP 驗證失敗,請稍後再試;若持續發生請聯絡管理員"},
}

func (s *Service) bindPassword() (string, bool) {
	if p := config.Current().Str(config.LDAPBindPassword); p != "" {
		return p, true
	}
	if s.secrets == nil {
		return "", false
	}
	return s.secrets.Get()
}

// LDAPSettings 給 MCP 授權(OAuth 同意頁與續期)的目錄登入設定:跟著目前的認證設定,沒啟用 LDAP 回 false。
// 驗證走和網頁登入同一套 ldapauth(含 RequiredGroup),失敗鎖定和網頁登入共用同一份計數。
// 故意不帶管理員名單:核心的使用者分不出管理員是怎麼指定的,名單移除後降不下來,所以名單只決定網頁管理員(每個請求現查),
// 不把任何人升成核心的伺服器管理員
func (s *Service) LDAPSettings() (service.LDAPSettings, bool) {
	cfg := s.Config()
	if cfg.Mode != "ldap" {
		return service.LDAPSettings{}, false
	}
	return service.LDAPSettings{Dir: directory{s, cfg}, EmailDomain: cfg.effectiveEmailDomain(), TrustMail: cfg.effectiveTrustMail(),
		Failures: &s.failures, UncheckedMaxAge: time.Duration(cfg.SessionHours) * time.Hour}, true
}

type directory struct {
	s   *Service
	cfg Config
}

func (d directory) Authenticate(username, password string) (*ldapauth.Identity, error) {
	a, err := d.s.authenticator(d.cfg, nil)
	if err != nil {
		return nil, err
	}
	return a.Authenticate(username, password)
}

// CanRecheck 只有服務帳號搜尋的設定能在不知道密碼時確認帳號(直接用帳號組 DN 的查不了)
func (d directory) CanRecheck() bool { return d.cfg.LDAP.UserDNTemplate == "" }

func (d directory) Recheck(account string) (*ldapauth.Identity, error) {
	a, err := d.s.authenticator(d.cfg, nil)
	if err != nil {
		return nil, err
	}
	return a.Recheck(account)
}

// failureKey 失敗鎖定的計數鍵:同一個目錄帳號的不同寫法(CORP\alice、alice@corp、Alice、全形)算同一個。
// 多網域的目錄裡 bob@a.com 和 bob@b.com 可能是不同人,次數仍然合併(寧可多鎖),清除則依 failureIdentity
func failureKey(user string) string {
	u := failureIdentity(user)
	if i := strings.LastIndex(u, `\`); i >= 0 {
		u = strings.TrimSpace(u[i+1:])
	}
	if i := strings.Index(u, "@"); i > 0 {
		u = strings.TrimSpace(u[:i])
	}
	return u
}

// failureIdentity 寫法鍵:只抹掉大小寫、全半形(Unicode NFKC)與前後空白,網域部分保留;
// 密碼正確只清掉同一個寫法鍵記的失敗次數,用自己的帳號登入清不掉別的寫法(可能是別人)的
func failureIdentity(user string) string {
	// Caser 有狀態,不能跨 goroutine 共用,每次建立
	return strings.TrimSpace(norm.NFKC.String(cases.Fold().String(norm.NFKC.String(user))))
}

// diagError LDAP 連線 / 設定類的錯誤:Error() 是給管理員診斷的完整說明(prefix + 原因,含位址、dial tcp),
// 實作 httperr.Public 讓「測試 LDAP」「存設定」照樣顯示;Login(未登入就能呼叫)不能回這些,
// 會換成 user 這句固定的話、原文進 log。
type diagError struct {
	prefix string // 管理員診斷的開頭
	user   string // 一般使用者(登入)看到的固定句子
	cause  error
}

func diag(prefix, user string, cause error) error {
	return diagError{prefix: prefix, user: user, cause: cause}
}

func (e diagError) Error() string                    { return e.prefix + ":" + e.cause.Error() }
func (e diagError) Unwrap() error                    { return e.cause }
func (e diagError) HTTPError() (int, map[string]any) { return 0, nil }

// StatusOf 管理員診斷(測試 LDAP、存設定)失敗的 HTTP 狀態碼:輸入有問題(設定驗證不過、帳密不對)400;
// 連不上 LDAP 等外部服務 502,逾時 504;其餘(內部錯誤)500
func StatusOf(err error) int {
	var de diagError
	if errors.As(err, &de) {
		var ne net.Error
		if errors.Is(de.cause, context.DeadlineExceeded) || (errors.As(de.cause, &ne) && ne.Timeout()) ||
			strings.Contains(strings.ToLower(de.cause.Error()), "timeout") {
			return 504
		}
		return 502
	}
	var ue core.UserError
	if errors.As(err, &ue) {
		return 400
	}
	return 500
}
