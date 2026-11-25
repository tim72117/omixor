// Package gitlabauth 用自建 GitLab 的 OAuth2(授權碼 + PKCE S256,scope read_user)問出「你是誰」。
// 只做 HTTP:帳號怎麼對應、能不能登入的規則在 service/gitlab.go。GitLab 的 token 只用來查一次 /api/v4/user,用完立刻撤銷,不保存。
//
// 環境變數(OMIXOR_GITLAB_URL 沒設 = 不啟用;設了但不完整,啟動時就失敗):
//
//	OMIXOR_GITLAB_URL                 GitLab 網址,例如 https://gitlab.example.com(可帶子路徑)
//	OMIXOR_GITLAB_CLIENT_ID           GitLab Application 的 Application ID
//	OMIXOR_GITLAB_CLIENT_SECRET       Application 的 Secret;或用 OMIXOR_GITLAB_CLIENT_SECRET_FILE 指到檔案(Docker secret)
//	OMIXOR_GITLAB_CA_CERT             自簽 CA 的 PEM 檔(加在系統信任清單之外;空白用系統的)
//	OMIXOR_GITLAB_ADMINS、OMIXOR_GITLAB_LINK_LDAP、OMIXOR_GITLAB_TRUST_MAIL  見 server/gitlab.go
package gitlabauth

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// Scope 只要讀自己的資料
	Scope = "read_user"
	// maxResponse GitLab 回應的大小上限
	maxResponse = 1 << 20
	// timeout 每個 HTTP 要求的逾時
	timeout = 10 * time.Second
)

// Config GitLab 的連線設定
type Config struct {
	URL, ClientID, ClientSecret, CACert string
}

// Identity GitLab 帳號掛的外部身分(LDAP 的 extern_uid 是目錄裡的 DN,由 GitLab 從 LDAP 帶入,使用者自己改不了)
type Identity struct {
	Provider  string `json:"provider"`
	ExternUID string `json:"extern_uid"`
}

// User GET /api/v4/user(目前登入者)用到的欄位
type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	State       string     `json:"state"`
	Bot         bool       `json:"bot"`
	ConfirmedAt *string    `json:"confirmed_at"`
	Identities  []Identity `json:"identities"`
}

// Confirmed GitLab 已經確認過這個帳號的 email
func (u *User) Confirmed() bool {
	return u.ConfirmedAt != nil && strings.TrimSpace(*u.ConfirmedAt) != ""
}

// LDAPDNs 掛在這個帳號上的 LDAP 身分(provider 以 ldap 開頭)的 DN
func (u *User) LDAPDNs() []string {
	var out []string
	for _, i := range u.Identities {
		if strings.HasPrefix(strings.ToLower(i.Provider), "ldap") && strings.TrimSpace(i.ExternUID) != "" {
			out = append(out, i.ExternUID)
		}
	}
	return out
}

// Client 連一台 GitLab
type Client struct {
	cfg  Config
	base string
	http *http.Client
}

// FromEnv 從環境變數讀設定。URL 沒設回傳 (nil, nil) = 不啟用;設了但不完整回傳錯誤
func FromEnv(getenv func(string) string) (*Config, error) {
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	if get("OMIXOR_GITLAB_URL") == "" {
		return nil, nil
	}
	c := &Config{URL: get("OMIXOR_GITLAB_URL"), ClientID: get("OMIXOR_GITLAB_CLIENT_ID"),
		ClientSecret: get("OMIXOR_GITLAB_CLIENT_SECRET"), CACert: get("OMIXOR_GITLAB_CA_CERT")}
	if f := get("OMIXOR_GITLAB_CLIENT_SECRET_FILE"); f != "" && c.ClientSecret == "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("讀不到 OMIXOR_GITLAB_CLIENT_SECRET_FILE(%s):%w", f, err)
		}
		c.ClientSecret = strings.TrimSpace(string(b))
	}
	if c.ClientID == "" {
		return nil, errors.New("設了 OMIXOR_GITLAB_URL,要同時設 OMIXOR_GITLAB_CLIENT_ID")
	}
	if c.ClientSecret == "" {
		return nil, errors.New("設了 OMIXOR_GITLAB_URL,要同時給 OMIXOR_GITLAB_CLIENT_SECRET 或 OMIXOR_GITLAB_CLIENT_SECRET_FILE")
	}
	return c, nil
}

// New 用設定建立
func New(c Config) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("OMIXOR_GITLAB_URL 要寫成 https://主機(可帶子路徑,不含帳密、查詢或片段)")
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return nil, errors.New("GitLab 登入缺 client id 或 secret")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if c.CACert != "" {
		pem, err := os.ReadFile(c.CACert)
		if err != nil {
			return nil, fmt.Errorf("讀不到 GitLab 的 CA 憑證:%w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("GitLab 的 CA 憑證檔不是有效的 PEM")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &Client{cfg: c, base: strings.TrimRight(u.String(), "/"), http: &http.Client{Transport: tr, Timeout: timeout,
		// 不跟轉址:避免把 client secret 或 token 送到別的地方
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// URL GitLab 網址(不含結尾斜線)
func (c *Client) URL() string { return c.base }

// Origin GitLab 的來源(scheme://host),同意畫面的 CSP form-action 用
func (c *Client) Origin() string {
	u, err := url.Parse(c.base)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// AuthorizeURL 瀏覽器要去的 GitLab 授權頁
func (c *Client) AuthorizeURL(redirectURI, state, challenge string) string {
	q := url.Values{"client_id": {c.cfg.ClientID}, "redirect_uri": {redirectURI}, "response_type": {"code"}, "scope": {Scope},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return c.base + "/oauth/authorize?" + q.Encode()
}

// do 送出要求並解析 JSON;錯誤訊息只帶狀態碼,不帶回應內容(可能含 token)
func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("連不到 GitLab:%w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("讀不到 GitLab 的回應:%w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitLab 回應 HTTP %d(%s)", resp.StatusCode, req.URL.Path)
	}
	if len(b) > maxResponse {
		return errors.New("GitLab 的回應太大")
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("GitLab 的回應格式不對(%s)", req.URL.Path)
	}
	return nil
}

func (c *Client) postForm(path string, f url.Values, out any) error {
	f.Set("client_id", c.cfg.ClientID)
	f.Set("client_secret", c.cfg.ClientSecret)
	req, err := http.NewRequest("POST", c.base+path, strings.NewReader(f.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, out)
}

// Exchange 授權碼換 access token(用完要 Revoke)
func (c *Client) Exchange(code, redirectURI, verifier string) (string, error) {
	var r struct {
		AccessToken string `json:"access_token"`
	}
	err := c.postForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}, &r)
	if err != nil {
		return "", err
	}
	if r.AccessToken == "" {
		return "", errors.New("GitLab 沒有回傳 token")
	}
	return r.AccessToken, nil
}

// CurrentUser 這個 token 是誰
func (c *Client) CurrentUser(token string) (*User, error) {
	req, err := http.NewRequest("GET", c.base+"/api/v4/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var u User
	if err := c.do(req, &u); err != nil {
		return nil, err
	}
	if u.ID <= 0 {
		return nil, errors.New("GitLab 沒有回傳使用者 id")
	}
	return &u, nil
}

// Revoke 撤銷 token
func (c *Client) Revoke(token string) error {
	return c.postForm("/oauth/revoke", url.Values{"token": {token}}, nil)
}
