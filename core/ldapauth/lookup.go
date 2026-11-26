package ldapauth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// ErrNoEntry 目錄裡沒有這個 DN 的帳號(不在 BaseDN 底下、不存在,或被 UserFilter 篩掉,例如已停用)
var ErrNoEntry = errors.New("目錄裡沒有這個帳號")

// CanLookup 能不能用服務帳號查目錄(直接用使用者身分 bind 的設定不行)
func (a *Authenticator) CanLookup() bool { return a.cfg.BindDN != "" }

// LookupDN 用服務帳號查某個 DN 是不是一個可以登入的帳號(不驗密碼;呼叫者要另外有「這個人就是它」的證明,
// 例如 GitLab 的 LDAP 身分)。條件和帳密登入相同:要在 BaseDN 底下、符合 UserFilter(%s 換成萬用字元),
// 回傳的 ID 取自 IDAttr(和帳密登入認人用的一樣)
func (a *Authenticator) LookupDN(dn string) (*Identity, error) {
	if !a.CanLookup() {
		return nil, errors.New("LDAP 沒有設定服務帳號,不能查目錄")
	}
	want, err := ldap.ParseDN(dn)
	if err != nil || strings.TrimSpace(dn) == "" {
		return nil, ErrNoEntry
	}
	base, err := ldap.ParseDN(a.cfg.BaseDN)
	if err != nil {
		return nil, fmt.Errorf("OMIXOR_LDAP_BASE_DN 格式不對:%w", err)
	}
	if !base.AncestorOfFold(want) {
		return nil, ErrNoEntry
	}
	c, err := a.dial(a.cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := c.Bind(a.cfg.BindDN, a.cfg.BindPassword); err != nil {
		return nil, fmt.Errorf("LDAP 服務帳號 bind 失敗(檢查 OMIXOR_LDAP_BIND_DN 與密碼):%w", err)
	}
	res, err := c.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 2, int(a.cfg.Timeout.Seconds()), false,
		strings.ReplaceAll(a.cfg.UserFilter, "%s", "*"),
		[]string{"dn", a.cfg.IDAttr, a.cfg.NameAttr, "cn", a.cfg.MailAttr}, nil))
	if err != nil {
		var le *ldap.Error
		if errors.As(err, &le) && le.ResultCode == ldap.LDAPResultNoSuchObject {
			return nil, ErrNoEntry
		}
		return nil, fmt.Errorf("LDAP 搜尋失敗:%w", err)
	}
	if len(res.Entries) != 1 {
		return nil, ErrNoEntry
	}
	e := res.Entries[0]
	id := strings.ToLower(strings.TrimSpace(e.GetAttributeValue(a.cfg.IDAttr)))
	if id == "" {
		return nil, ErrNoEntry
	}
	name := e.GetAttributeValue(a.cfg.NameAttr)
	if name == "" {
		name = e.GetAttributeValue("cn")
	}
	return &Identity{ID: id, Name: name, Email: cleanMail(e.GetAttributeValue(a.cfg.MailAttr)), DN: e.DN}, nil
}
