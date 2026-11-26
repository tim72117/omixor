package ldapauth

import (
	"crypto/tls"
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

type recConn struct {
	binds   []string
	filters []string
	search  func(*ldap.SearchRequest) (*ldap.SearchResult, error)
}

func (c *recConn) Bind(u, p string) error { c.binds = append(c.binds, u); return nil }
func (c *recConn) Search(r *ldap.SearchRequest) (*ldap.SearchResult, error) {
	c.filters = append(c.filters, r.Filter)
	return c.search(r)
}
func (c *recConn) StartTLS(*tls.Config) error { return nil }
func (c *recConn) Close() error               { return nil }

func recAuth(t *testing.T, cfg Config, c *recConn) *Authenticator {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.dial = func(Config) (conn, error) { return c, nil }
	return a
}

// 不含 = 的範本(UPN、%s)在宿主模式不做 DN 跳脫;DN 範本照跳脫;伺服器(沒開 RawNonDNTemplate)行為不變
func TestTemplateEscaping(t *testing.T) {
	for _, x := range []struct {
		tpl, user string
		raw       bool
		want      string
	}{
		{"%s", `CORP\Alice`, true, `CORP\Alice`},
		{"%s@corp.test", "a+b,c", true, "a+b,c@corp.test"},
		{"uid=%s,dc=x", "a,ou=admins", true, `uid=a\,ou=admins,dc=x`},
		{"%s", `CORP\Alice`, false, `corp\\alice`},
		{"uid=%s,dc=x", "Amy", false, "uid=amy,dc=x"},
	} {
		c := &recConn{search: func(*ldap.SearchRequest) (*ldap.SearchResult, error) { return &ldap.SearchResult{}, nil }}
		a := recAuth(t, Config{URL: "ldap://h:389", UserDNTemplate: x.tpl, RawNonDNTemplate: x.raw}, c)
		if _, err := a.Authenticate(x.user, "pw"); err != nil {
			t.Fatal(err)
		}
		if len(c.binds) != 1 || c.binds[0] != x.want {
			t.Errorf("%s %q raw=%v:bind %q,要 %q", x.tpl, x.user, x.raw, c.binds, x.want)
		}
	}
}

func TestRecheck(t *testing.T) {
	const group = "cn=users,dc=x"
	attrs := map[string][]string{"memberOf": {group}}
	var searchErr error
	found := true
	c := &recConn{search: func(r *ldap.SearchRequest) (*ldap.SearchResult, error) {
		if searchErr != nil {
			return nil, searchErr
		}
		if !found || strings.Contains(r.Filter, "member=") {
			return &ldap.SearchResult{}, nil
		}
		return &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry("uid=amy,dc=x", attrs)}}, nil
	}}
	a := recAuth(t, Config{URL: "ldap://h:389", BindDN: "cn=svc", BindPassword: "p", BaseDN: "dc=x", IDAttr: "uid",
		UserFilter: "(&(objectClass=person)(uid=%s))", RequiredGroup: group, GroupFilter: "(member=%s)"}, c)
	if _, err := a.Recheck("amy)(uid=*"); err != nil {
		t.Fatal(err)
	}
	if f := c.filters[0]; f != `(&(&(objectClass=person)(uid=*))(uid=amy\29\28uid=\2a))` {
		t.Fatalf("篩選:%s", f)
	}
	for _, x := range []struct {
		set  func()
		want error
	}{
		{func() { attrs = map[string][]string{"memberOf": {group}, "userAccountControl": {"514"}} }, ErrAccountDisabled},
		{func() { attrs = map[string][]string{"memberOf": {group}, "nsAccountLock": {"TRUE"}} }, ErrAccountDisabled},
		{func() { attrs = map[string][]string{"memberOf": {group}, "accountExpires": {"116444736000000000"}} }, ErrAccountDisabled},
		{func() { attrs = map[string][]string{} }, ErrNotInGroup},
		{func() { found = false }, ErrNoEntry},
	} {
		x.set()
		if _, err := a.Recheck("amy"); !errors.Is(err, x.want) {
			t.Errorf("要 %v,得到 %v", x.want, err)
		}
	}
	// 目錄故障不是「帳號有問題」
	searchErr = errors.New("boom")
	_, err := a.Recheck("amy")
	var se *StageError
	if !errors.As(err, &se) || errors.Is(err, ErrNoEntry) {
		t.Fatalf("目錄錯誤:%v", err)
	}
}
