package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"omixor-core/httperr"
)

// 連線:omixor 要呼叫的外部服務(網址 + 認證方式)。同 Swift 版 Connections/ConnectionModels.swift。
// 設定存在目前組織的 connections.json(格式和 Swift 版一樣),token、client secret 存在鑰匙圈(conn_vault.go)。

// AuthKind:oauth、approval、bearer、none
const (
	AuthOAuth    = "oauth"
	AuthApproval = "approval"
	AuthBearer   = "bearer"
	AuthNone     = "none"
	// AuthBasic 鑰匙圈存「帳號:token」,每次請求帶 Authorization: Basic(例如 Jira Cloud 的 email + API token)
	AuthBasic = "basic"
)

var AuthKinds = []string{AuthOAuth, AuthApproval, AuthBearer, AuthBasic, AuthNone}

// OAuthSettings 同 Swift(欄位依字母排序)
type OAuthSettings struct {
	AuthorizationEndpoint string `json:"authorizationEndpoint"`
	ClientID              string `json:"clientId"`
	ExtraParams           string `json:"extraParams"`
	HasClientSecret       bool   `json:"hasClientSecret"`
	Issuer                string `json:"issuer"`
	RegisteredPort        *int   `json:"registeredPort,omitempty"`
	RegistrationEndpoint  string `json:"registrationEndpoint"`
	Resource              string `json:"resource"`
	Scopes                string `json:"scopes"`
	SendIDToken           bool   `json:"sendIdToken"`
	TokenEndpoint         string `json:"tokenEndpoint"`
}

func DefaultOAuthSettings() OAuthSettings {
	return OAuthSettings{Scopes: "openid email offline_access"}
}

type ApprovalSettings struct {
	ApprovePath  string `json:"approvePath"`
	ConsoleURL   string `json:"consoleURL"`
	ExchangePath string `json:"exchangePath"`
	LogoutPath   string `json:"logoutPath"`
	StartPath    string `json:"startPath"`
}

func DefaultApprovalSettings() ApprovalSettings {
	return ApprovalSettings{StartPath: "/api/cli-auth/start", ApprovePath: "/login/cli?id={id}",
		ExchangePath: "/api/cli-auth/exchange", LogoutPath: "/api/cli-auth/logout"}
}

// Connection 同 Swift 的 Connection
type Connection struct {
	Approval ApprovalSettings `json:"approval"`
	Auth     string           `json:"auth"`
	BaseURL  string           `json:"baseURL"`
	// CACert 自簽憑證的 CA(PEM 檔路徑);空白表示用系統的憑證。Go 版才有,Swift 版讀檔時會忽略
	CACert string `json:"caCert,omitempty"`
	ID     string `json:"id"`
	// Insecure 不驗證 TLS 憑證(只給測試環境);Go 版才有,Swift 版讀檔時會忽略
	Insecure     bool          `json:"insecure,omitempty"`
	IsProduction bool          `json:"isProduction"`
	Name         string        `json:"name"`
	Note         string        `json:"note"`
	OAuth        OAuthSettings `json:"oauth"`
	TestPath     string        `json:"testPath"`
}

// NewConnection 帶預設值的連線(同 Swift 的成員初始化)
func NewConnection(id, name, baseURL string) Connection {
	return Connection{ID: id, Name: name, BaseURL: baseURL, Auth: AuthOAuth,
		OAuth: DefaultOAuthSettings(), Approval: DefaultApprovalSettings()}
}

// UnmarshalJSON 寬鬆解碼:缺欄位用預設值
func (c *Connection) UnmarshalJSON(b []byte) error {
	type plain Connection
	p := plain(NewConnection("", "", ""))
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*c = Connection(p)
	if !isAuthKind(c.Auth) {
		c.Auth = AuthOAuth
	}
	return nil
}

func (o *OAuthSettings) UnmarshalJSON(b []byte) error {
	type plain OAuthSettings
	p := plain(DefaultOAuthSettings())
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*o = OAuthSettings(p)
	return nil
}

func (s *ApprovalSettings) UnmarshalJSON(b []byte) error {
	type plain ApprovalSettings
	p := plain(DefaultApprovalSettings())
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*s = ApprovalSettings(p)
	return nil
}

func isAuthKind(s string) bool {
	for _, k := range AuthKinds {
		if k == s {
			return true
		}
	}
	return false
}

// Origin scheme://host[:port];網址解析不了時回傳原字串
func (c Connection) Origin() string { return urlOrigin(c.BaseURL) }

func urlOrigin(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return s
	}
	return u.Scheme + "://" + u.Host
}

// TrimmedBase 去掉結尾的 /
func (c Connection) TrimmedBase() string { return strings.TrimSuffix(c.BaseURL, "/") }

// AllowsHost 連線的 token 可以送到哪些主機:連線網址本身,或同一個上層網域(www.googleapis.com → *.googleapis.com)
func (c Connection) AllowsHost(host string) bool {
	u, err := url.Parse(c.BaseURL)
	if host == "" || err != nil || u.Hostname() == "" {
		return false
	}
	h, base := strings.ToLower(host), strings.ToLower(u.Hostname())
	if h == base {
		return true
	}
	parts := []string{}
	for _, p := range strings.Split(base, ".") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) < 3 {
		return false
	}
	return strings.HasSuffix(h, "."+strings.Join(parts[1:], "."))
}

// TokenSet 存在鑰匙圈的登入結果(JSON 格式同 Swift:日期是 ISO 8601)
type TokenSet struct {
	AccessToken  string  `json:"accessToken"`
	ExpiresAt    *Time   `json:"expiresAt,omitempty"`
	IDToken      *string `json:"idToken,omitempty"`
	RefreshToken *string `json:"refreshToken,omitempty"`
	TokenType    *string `json:"tokenType,omitempty"`
}

func (t TokenSet) IsExpiring() bool {
	return t.ExpiresAt != nil && time.Until(t.ExpiresAt.Time) < 60*time.Second
}

// ConnectionState 畫面上顯示的連線狀態
type ConnectionState struct {
	Account  *string `json:"account,omitempty"`
	Busy     bool    `json:"busy"`
	Error    *string `json:"error,omitempty"`
	LastTest *string `json:"lastTest,omitempty"`
}

// ConnError 同 Swift 的 ConnectionError:Error() 等於 Swift 的 localizedDescription
type ConnError struct {
	Status  int
	Message string
}

func (e ConnError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("HTTP %d:%s", e.Status, e.Message)
	}
	return e.Message
}

func (e ConnError) IsUnauthorized() bool { return e.Status == 401 }

func connErr(msg string) error { return ConnError{Message: msg} }

// AsConnError 取出 ConnError
func AsConnError(err error) (ConnError, bool) {
	e, ok := err.(ConnError)
	return e, ok
}

func strPtr(s string) *string { return &s }

// HTTPError 實作 httperr.Public:連線(GitHub、GitLab、GCP、Jira、OAuth…)的錯誤說明是刻意給使用者 / agent 看的
// (HTTP 狀態與對方回的訊息、憑證怎麼處理、連不上哪台主機),不用文字比對猜它含不含內部細節;狀態碼 0 = 讓出口決定
func (e ConnError) HTTPError() (int, map[string]any) { return 0, nil }

// netFailure 連不上(DNS、拒絕連線、逾時…)轉成 ConnError:「連不上 主機:原因」。
// 原因取 url.Error 裡面那層(不含請求網址,所以查詢參數裡的 token 不會出現)
func netFailure(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	host := ""
	if u, perr := url.Parse(ue.URL); perr == nil {
		host = u.Host
	}
	msg := "連不上"
	if host != "" {
		msg += " " + host
	}
	return ConnError{Message: msg + ":" + ue.Err.Error()}
}

// netReason 連不上的原因文字(netFailure 的 ConnError 去掉「連不上 主機:」前綴不處理,整句就是原因)
func netReason(err error) string {
	if e, ok := AsConnError(err); ok {
		return e.Message
	}
	return httperr.Classify(err).Msg
}
