// Package ldapauth 用 LDAP / Active Directory 驗證帳號密碼。伺服器啟動時從環境變數讀設定(FromEnv),
// 不寫進資料庫;驗證通過後由 service.LoginWithLDAP 換 omixor 自己的 token。
//
// 流程(同 GitLab 的 LDAP 設定:服務帳號搜尋使用者,再用找到的 DN 驗證密碼):
//
//  1. 連到 LDAP(ldap://、ldaps://,可先 StartTLS)
//  2. 用服務帳號(BindDN)bind,在 BaseDN 底下用 UserFilter 找這個帳號,要剛好一筆
//     (沒有服務帳號、UserDNTemplate 有填時,直接用帳號組出 DN,不搜尋)
//  3. 用找到的 DN 和使用者輸入的密碼 bind;成功才算通過
//
// 環境變數(OMIXOR_LDAP_URL 沒設 = 不啟用 LDAP 登入):
//
//	OMIXOR_LDAP_URL                  ldap://主機:389 或 ldaps://主機:636
//	OMIXOR_LDAP_START_TLS            true:ldap:// 時先 StartTLS(明文 ldap:// 會把密碼在網路上明碼傳送,建議開)
//	OMIXOR_LDAP_CA_CERT              自簽 CA 的 PEM 檔路徑(空白用系統的)
//	OMIXOR_LDAP_INSECURE_SKIP_VERIFY true:不驗證伺服器憑證(只給測試環境)
//	OMIXOR_LDAP_BIND_DN              服務帳號 DN
//	OMIXOR_LDAP_BIND_PASSWORD        服務帳號密碼;或用 OMIXOR_LDAP_BIND_PASSWORD_FILE 指到檔案(例如 Docker secret /run/secrets/ldap_password)
//	OMIXOR_LDAP_BASE_DN              搜尋使用者的起點
//	OMIXOR_LDAP_USER_FILTER          %s 換成(跳脫過的)帳號,預設 (sAMAccountName=%s);AD 只要啟用中的帳號:(&(sAMAccountName=%s)(userAccountControl=512))
//	OMIXOR_LDAP_USER_DN_TEMPLATE     不用服務帳號時:%s 換成帳號組出 DN,例如 %s@corp.example.com 或 uid=%s,ou=people,dc=example,dc=com
//	OMIXOR_LDAP_ID_ATTR              帳號屬性,預設 sAMAccountName(omixor 裡的使用者身分,小寫)
//	OMIXOR_LDAP_NAME_ATTR            顯示名稱屬性,預設 displayName(沒有時退回 cn)
//	OMIXOR_LDAP_MAIL_ATTR            email 屬性,預設 mail
//	OMIXOR_LDAP_TIMEOUT_SECONDS      連線與每個要求的逾時,預設 10
package ldapauth

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// Config LDAP 設定
type Config struct {
	URL                string
	StartTLS           bool
	CACert             string
	InsecureSkipVerify bool
	BindDN             string
	BindPassword       string
	BaseDN             string
	UserFilter         string
	UserDNTemplate     string
	IDAttr             string
	NameAttr           string
	MailAttr           string
	Timeout            time.Duration
}

// Identity 驗證通過的人
type Identity struct {
	// ID 帳號(小寫),omixor 用它認人
	ID    string
	Name  string
	Email string
	DN    string
}

// ErrInvalidCredentials 帳號不存在、被停用或密碼錯(不分是哪一種,避免被拿來猜帳號)
var ErrInvalidCredentials = errors.New("帳號或密碼錯誤")

// FromEnv 從環境變數讀設定。URL 沒設回傳 (nil, nil) = 不啟用;設了但不完整回傳錯誤(啟動時就該失敗)
func FromEnv(getenv func(string) string) (*Config, error) {
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	if get("OMIXOR_LDAP_URL") == "" {
		return nil, nil
	}
	c := &Config{
		URL:                get("OMIXOR_LDAP_URL"),
		StartTLS:           isTrue(get("OMIXOR_LDAP_START_TLS")),
		CACert:             get("OMIXOR_LDAP_CA_CERT"),
		InsecureSkipVerify: isTrue(get("OMIXOR_LDAP_INSECURE_SKIP_VERIFY")),
		BindDN:             get("OMIXOR_LDAP_BIND_DN"),
		BindPassword:       getenv("OMIXOR_LDAP_BIND_PASSWORD"),
		BaseDN:             get("OMIXOR_LDAP_BASE_DN"),
		UserFilter:         get("OMIXOR_LDAP_USER_FILTER"),
		UserDNTemplate:     get("OMIXOR_LDAP_USER_DN_TEMPLATE"),
		IDAttr:             get("OMIXOR_LDAP_ID_ATTR"),
		NameAttr:           get("OMIXOR_LDAP_NAME_ATTR"),
		MailAttr:           get("OMIXOR_LDAP_MAIL_ATTR"),
		Timeout:            10 * time.Second,
	}
	if f := get("OMIXOR_LDAP_BIND_PASSWORD_FILE"); f != "" && c.BindPassword == "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("讀不到 OMIXOR_LDAP_BIND_PASSWORD_FILE(%s):%w", f, err)
		}
		c.BindPassword = strings.TrimRight(string(b), "\r\n")
	}
	if s := get("OMIXOR_LDAP_TIMEOUT_SECONDS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("OMIXOR_LDAP_TIMEOUT_SECONDS 要是正整數:%q", s)
		}
		c.Timeout = time.Duration(n) * time.Second
	}
	if err := c.fill(); err != nil {
		return nil, err
	}
	return c, nil
}

func isTrue(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// fill 補預設值並檢查
func (c *Config) fill() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Host == "" {
		return fmt.Errorf("OMIXOR_LDAP_URL 要寫成 ldap://主機:389 或 ldaps://主機:636(收到 %q)", c.URL)
	}
	if c.StartTLS && u.Scheme == "ldaps" {
		return errors.New("ldaps:// 已經加密,OMIXOR_LDAP_START_TLS 只用在 ldap://")
	}
	if c.IDAttr == "" {
		c.IDAttr = "sAMAccountName"
	}
	if c.NameAttr == "" {
		c.NameAttr = "displayName"
	}
	if c.MailAttr == "" {
		c.MailAttr = "mail"
	}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}
	switch {
	case c.BindDN != "":
		// 方式二:服務帳號搜尋
		if c.BindPassword == "" {
			return errors.New("有設 OMIXOR_LDAP_BIND_DN,要同時給服務帳號密碼(OMIXOR_LDAP_BIND_PASSWORD 或 OMIXOR_LDAP_BIND_PASSWORD_FILE);空密碼在 LDAP 是匿名 bind")
		}
		if c.BaseDN == "" {
			return errors.New("用服務帳號搜尋要設 OMIXOR_LDAP_BASE_DN")
		}
		if c.UserFilter == "" {
			c.UserFilter = "(" + c.IDAttr + "=%s)"
		}
		if strings.Count(c.UserFilter, "%s") != 1 {
			return fmt.Errorf("OMIXOR_LDAP_USER_FILTER 要剛好有一個 %%s(換成帳號)(收到 %q)", c.UserFilter)
		}
	case c.UserDNTemplate != "":
		// 方式一:直接用使用者身分 bind
		if strings.Count(c.UserDNTemplate, "%s") != 1 {
			return fmt.Errorf("OMIXOR_LDAP_USER_DN_TEMPLATE 要剛好有一個 %%s(換成帳號)(收到 %q)", c.UserDNTemplate)
		}
	default:
		return errors.New("要設 OMIXOR_LDAP_BIND_DN(服務帳號搜尋)或 OMIXOR_LDAP_USER_DN_TEMPLATE(直接用使用者身分 bind)其中一個")
	}
	return nil
}

// conn 用到的 LDAP 連線功能(測試時換成假的)
type conn interface {
	Bind(username, password string) error
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	StartTLS(*tls.Config) error
	Close() error
}

// Authenticator 驗證帳號密碼
type Authenticator struct {
	cfg  Config
	dial func(Config) (conn, error)
}

// New 用設定建立。設定要先過 FromEnv(或自己呼叫 fill 過的)
func New(c Config) (*Authenticator, error) {
	if err := c.fill(); err != nil {
		return nil, err
	}
	return &Authenticator{cfg: c, dial: dialLDAP}, nil
}

func dialLDAP(c Config) (conn, error) {
	tc := &tls.Config{InsecureSkipVerify: c.InsecureSkipVerify} //nolint:gosec // 只給測試環境,由管理員明確開
	if u, _ := url.Parse(c.URL); u != nil {
		tc.ServerName = u.Hostname()
	}
	if c.CACert != "" {
		pem, err := os.ReadFile(c.CACert)
		if err != nil {
			return nil, fmt.Errorf("讀不到 LDAP 的 CA 憑證:%w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("LDAP 的 CA 憑證檔不是有效的 PEM")
		}
		tc.RootCAs = pool
	}
	l, err := ldap.DialURL(c.URL, ldap.DialWithTLSConfig(tc), ldap.DialWithDialer(&net.Dialer{Timeout: c.Timeout}))
	if err != nil {
		return nil, fmt.Errorf("連不到 LDAP 伺服器:%w", err)
	}
	l.SetTimeout(c.Timeout)
	if c.StartTLS && strings.HasPrefix(strings.ToLower(c.URL), "ldap://") {
		if err := l.StartTLS(tc); err != nil {
			l.Close()
			return nil, fmt.Errorf("LDAP StartTLS 失敗:%w", err)
		}
	}
	return l, nil
}

// isBadCredentials bind 的結果是「這組帳號密碼不行」:49 密碼錯(AD 的停用、鎖定、過期也是 49)、
// 50 沒有權限、32 找不到 DN、34 DN 格式不對(直接用 DN 樣板時,不存在的帳號會是這兩種)
func isBadCredentials(err error) bool {
	var le *ldap.Error
	if !errors.As(err, &le) {
		return false
	}
	switch le.ResultCode {
	case ldap.LDAPResultInvalidCredentials, ldap.LDAPResultInsufficientAccessRights, ldap.LDAPResultNoSuchObject, ldap.LDAPResultInvalidDNSyntax:
		return true
	}
	return false
}

// Authenticate 驗證帳號密碼。帳號不存在、被停用、密碼錯都回 ErrInvalidCredentials;
// 連線、服務帳號等設定問題回其他錯誤(給管理員看紀錄,不要原樣給登入的人)
func (a *Authenticator) Authenticate(username, password string) (*Identity, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	// 空密碼在 LDAP 是「匿名 bind」,會被當成成功:一律擋掉
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	c, err := a.dial(a.cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	var dn, name, mail string
	id := username
	if a.cfg.BindDN != "" {
		if err := c.Bind(a.cfg.BindDN, a.cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("LDAP 服務帳號 bind 失敗(檢查 OMIXOR_LDAP_BIND_DN 與密碼):%w", err)
		}
		res, err := c.Search(ldap.NewSearchRequest(a.cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, int(a.cfg.Timeout.Seconds()), false,
			strings.Replace(a.cfg.UserFilter, "%s", ldap.EscapeFilter(username), 1),
			[]string{"dn", a.cfg.IDAttr, a.cfg.NameAttr, "cn", a.cfg.MailAttr}, nil))
		if err != nil {
			return nil, fmt.Errorf("LDAP 搜尋失敗:%w", err)
		}
		if len(res.Entries) != 1 { // 0 = 找不到或被篩選掉(例如停用);2 以上 = 不明確,不能猜
			return nil, ErrInvalidCredentials
		}
		e := res.Entries[0]
		dn, name, mail = e.DN, e.GetAttributeValue(a.cfg.NameAttr), e.GetAttributeValue(a.cfg.MailAttr)
		if name == "" {
			name = e.GetAttributeValue("cn")
		}
		if v := strings.ToLower(strings.TrimSpace(e.GetAttributeValue(a.cfg.IDAttr))); v != "" {
			id = v
		}
	} else {
		dn = strings.Replace(a.cfg.UserDNTemplate, "%s", ldap.EscapeDN(username), 1)
	}

	// 用使用者自己的身分 bind。只有目錄明確說「帳號或密碼不對」才算認證失敗;
	// 連線中斷、逾時、TLS 失敗這類不是 *ldap.Error 的錯誤,以及其他結果碼,都是目錄那邊的問題,要讓管理員看得到
	if err := c.Bind(dn, password); err != nil {
		if isBadCredentials(err) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("LDAP 驗證密碼時出錯:%w", err)
	}
	return &Identity{ID: id, Name: name, Email: strings.ToLower(strings.TrimSpace(mail)), DN: dn}, nil
}
