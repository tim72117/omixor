package auth

import (
	"net/url"
	"strings"

	"omixor-web/internal/core"

	"omixor-core/config"
)

// Config:auth.json
type Config struct {
	// off:不用登入(只能從本機開);ldap:要用 LDAP 帳號登入
	Mode string `json:"mode"`
	LDAP LDAP   `json:"ldap"`
	// 登入有效幾小時,預設 12
	SessionHours int `json:"sessionHours"`
	// Admins 網頁管理員的 LDAP 帳號名單(不分大小寫);空的 = 沒有人是管理員。有設 OMIXOR_LDAP_ADMINS 時以環境變數為準
	Admins []string `json:"admins"`
	// EmailDomain 目錄沒有 mail 時,用「帳號@這個網域」當 email(MCP 授權建立 omixor 使用者時用);OMIXOR_LDAP_EMAIL_DOMAIN 有設時以它為準
	EmailDomain string `json:"emailDomain"`
	// TrustMail 管理員先用 email 加進組織、還沒登入過的人,和目錄 mail 相同的 LDAP 帳號對上;OMIXOR_LDAP_TRUST_MAIL 有設時以它為準
	TrustMail bool `json:"trustMail"`
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

	// 屬性:帳號(omixor 裡的使用者 id)、顯示名稱、email
	IDAttr   string `json:"idAttr"`
	NameAttr string `json:"nameAttr"`
	MailAttr string `json:"mailAttr"`

	// 只允許這個群組的成員登入(群組 DN;空白表示所有人)
	RequiredGroup string `json:"requiredGroup"`
	// AdminGroup 舊版的管理員群組:已經不使用(管理員只看 Config.Admins),只用來在啟動時提醒
	AdminGroup string `json:"adminGroup,omitempty"`
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
	c.Admins = splitAccounts(strings.Join(c.Admins, ","))
	c.EmailDomain = strings.TrimPrefix(strings.TrimSpace(c.EmailDomain), "@")
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
	if l.RequiredGroup != "" && l.UserDNTemplate == "" && l.BaseDN == "" && l.GroupBaseDN == "" {
		return core.UserError{Message: "要檢查群組時,要填群組的搜尋起點"}
	}
	if strings.ContainsAny(c.EmailDomain, "@/ ") {
		return core.UserError{Message: "email 網域只要網域,例如 corp.example.com"}
	}
	return nil
}

func splitAccounts(s string) []string {
	out := []string{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// EnvAdmins OMIXOR_LDAP_ADMINS 的名單;沒設回 nil
func EnvAdmins() []string {
	if v := config.Current().Str(config.LDAPAdmins); strings.TrimSpace(v) != "" {
		return splitAccounts(v)
	}
	return nil
}

// AdminList 實際生效的管理員名單:環境變數有設就以它為準,否則是 auth.json 的
func (c Config) AdminList() []string {
	if env := EnvAdmins(); env != nil {
		return env
	}
	return splitAccounts(strings.Join(c.Admins, ","))
}

// IsAdminAccount 這個帳號在不在生效的名單裡(不分大小寫)
func (c Config) IsAdminAccount(account string) bool {
	for _, a := range c.AdminList() {
		if strings.EqualFold(a, account) {
			return true
		}
	}
	return false
}

func (c Config) effectiveEmailDomain() string {
	if v := strings.TrimSpace(config.Current().Str(config.LDAPEmailDomain)); v != "" {
		return strings.TrimPrefix(v, "@")
	}
	return c.EmailDomain
}

func (c Config) effectiveTrustMail() bool {
	if config.Current().IsSet(config.LDAPTrustMail) {
		return config.Current().Bool(config.LDAPTrustMail)
	}
	return c.TrustMail
}

// NoAdminsHelp 名單空白時怎麼救(啟動紀錄與畫面共用)
const NoAdminsHelp = "在主機上設環境變數 OMIXOR_LDAP_ADMINS=<LDAP 帳號,逗號分隔> 後重新啟動;" +
	"或用 OMIXOR_AUTH=off(畫面只開本機)啟動,到 設定 › 認證 填管理員帳號名單,再拿掉 OMIXOR_AUTH=off"

// Warnings 啟動時要提醒管理者的事(沒啟用 LDAP 時一律沒有)
func (c Config) Warnings() []string {
	if c.Mode != "ldap" {
		return nil
	}
	var out []string
	if len(c.AdminList()) == 0 {
		msg := "LDAP 登入已啟用但管理員帳號名單是空的:沒有任何人是網頁管理員(改認證與連線、憑證、對外發佈等一律 403);不是任何組織成員的人也不能登入,沒有管理員就沒有人能加成員。" + NoAdminsHelp
		if strings.TrimSpace(c.LDAP.AdminGroup) != "" {
			msg += "。原本設定的管理員群組已經不使用"
		}
		out = append(out, msg)
	}
	if strings.HasPrefix(strings.ToLower(c.LDAP.URL), "ldap://") && !c.LDAP.StartTLS {
		out = append(out, "LDAP 用明文連線(ldap:// 且沒開 StartTLS),使用者密碼會在網路上明碼傳送;建議改 ldaps:// 或開 StartTLS")
	}
	if c.LDAP.InsecureSkipVerify {
		out = append(out, "LDAP 設定為不驗證伺服器憑證(只建議測試用)")
	}
	return out
}
