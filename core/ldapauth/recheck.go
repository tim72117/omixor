package ldapauth

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// 已經授權過的帳號(OAuth refresh)向目錄重新確認還能不能登入:不用密碼,用服務帳號查。
// 沒有服務帳號(只用 DN 範本 bind)就查不了(CanRecheck = false)

// ErrAccountDisabled 目錄裡的帳號已停用或過期
var ErrAccountDisabled = errors.New("目錄裡的帳號已停用")

// CanRecheck 能不能用服務帳號重新確認帳號
func (a *Authenticator) CanRecheck() bool { return a.cfg.BindDN != "" && a.cfg.BaseDN != "" }

// Recheck 這個帳號(登入時取得的 ID)現在還能不能登入。確定不行回 ErrNoEntry(不存在,或被 UserFilter 篩掉)、
// ErrAccountDisabled 或 ErrNotInGroup;其他錯誤是目錄或設定的問題(不代表帳號有問題)
func (a *Authenticator) Recheck(account string) (*Identity, error) {
	if !a.CanRecheck() {
		return nil, errors.New("LDAP 沒有設定服務帳號,不能重新確認帳號")
	}
	if strings.TrimSpace(account) == "" {
		return nil, ErrNoEntry
	}
	c, err := a.dial(a.cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := c.Bind(a.cfg.BindDN, a.cfg.BindPassword); err != nil {
		return nil, stageErr(StageServiceBind, err, "LDAP 服務帳號 bind 失敗(檢查 OMIXOR_LDAP_BIND_DN 與密碼):%w", err)
	}
	attrs := []string{"dn", a.cfg.IDAttr, a.cfg.NameAttr, "cn", a.cfg.MailAttr, "userAccountControl", "nsAccountLock", "accountExpires"}
	if a.cfg.RequiredGroup != "" {
		attrs = append(attrs, "memberOf")
	}
	search := func(filter string) ([]*ldap.Entry, error) {
		res, err := c.Search(ldap.NewSearchRequest(a.cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, int(a.cfg.Timeout.Seconds()), false, filter, attrs, nil))
		if err != nil {
			return nil, stageErr(StageSearch, err, "LDAP 搜尋失敗:%w", err)
		}
		return res.Entries, nil
	}
	// 先用帳號屬性找(登入時的 ID 就取自它),同時保留 UserFilter 的其他條件;
	// 目錄那筆沒有帳號屬性時 ID 是使用者輸入的字串,改用登入時的篩選找
	esc := ldap.EscapeFilter(account)
	entries, err := search("(&" + strings.ReplaceAll(a.cfg.UserFilter, "%s", "*") + "(" + a.cfg.IDAttr + "=" + esc + "))")
	if err == nil && len(entries) == 0 {
		entries, err = search(strings.ReplaceAll(a.cfg.UserFilter, "%s", esc))
	}
	if err != nil {
		return nil, err
	}
	switch {
	case len(entries) == 0:
		return nil, ErrNoEntry
	case len(entries) > 1:
		err := errors.New("符合的帳號不只一筆")
		return nil, stageErr(StageSearch, err, "LDAP 搜尋失敗:%w", err)
	}
	e := entries[0]
	if accountDisabled(e, time.Now()) {
		return nil, ErrAccountDisabled
	}
	var groups []string
	if a.cfg.RequiredGroup != "" {
		groups = e.GetAttributeValues("memberOf")
		base := a.cfg.GroupBaseDN
		if base == "" {
			base = a.cfg.BaseDN
		}
		if !inGroup(groups, a.cfg.RequiredGroup) && a.cfg.GroupFilter != "" {
			f := strings.ReplaceAll(a.cfg.GroupFilter, "%s", ldap.EscapeFilter(e.DN))
			res, err := c.Search(ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, int(a.cfg.Timeout.Seconds()), false, f, []string{"dn"}, nil))
			if err != nil { // 查不到群組是目錄的問題,不能當成「不在群組」
				return nil, stageErr(StageSearch, err, "LDAP 群組搜尋失敗:%w", err)
			}
			for _, g := range res.Entries {
				groups = append(groups, g.DN)
			}
		}
		if !inGroup(groups, a.cfg.RequiredGroup) {
			return nil, ErrNotInGroup
		}
	}
	name := e.GetAttributeValue(a.cfg.NameAttr)
	if name == "" {
		name = e.GetAttributeValue("cn")
	}
	return &Identity{ID: strings.ToLower(account), Account: account, Name: name, Email: cleanMail(e.GetAttributeValue(a.cfg.MailAttr)), DN: e.DN, Groups: groups}, nil
}

// accountDisabled 常見目錄的停用標記:AD 的 userAccountControl(ACCOUNTDISABLE)與 accountExpires、389ds / FreeIPA 的 nsAccountLock。
// 其他目錄的停用要靠 UserFilter 篩掉
func accountDisabled(e *ldap.Entry, now time.Time) bool {
	if n, err := strconv.ParseInt(e.GetAttributeValue("userAccountControl"), 10, 64); err == nil && n&0x2 != 0 {
		return true
	}
	if strings.EqualFold(e.GetAttributeValue("nsAccountLock"), "true") {
		return true
	}
	// accountExpires:1601 起的 100ns;0 與 int64 最大值 = 不過期
	if n, err := strconv.ParseInt(e.GetAttributeValue("accountExpires"), 10, 64); err == nil && n > 0 && n < math.MaxInt64 {
		const epochDiff = 11644473600 // 1601 → 1970 的秒數
		return time.Unix(n/10_000_000-epochDiff, 0).Before(now)
	}
	return false
}
