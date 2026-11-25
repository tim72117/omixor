package ldapauth

import (
	"crypto/tls"
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

type lookupConn struct {
	req     *ldap.SearchRequest
	entries []*ldap.Entry
	err     error
}

func (c *lookupConn) Bind(string, string) error  { return nil }
func (c *lookupConn) StartTLS(*tls.Config) error { return nil }
func (c *lookupConn) Close() error               { return nil }
func (c *lookupConn) Search(r *ldap.SearchRequest) (*ldap.SearchResult, error) {
	c.req = r
	return &ldap.SearchResult{Entries: c.entries}, c.err
}

func TestLookupDN(t *testing.T) {
	a, err := New(Config{URL: "ldaps://h", BindDN: "cn=svc,dc=x", BindPassword: "p", BaseDN: "ou=Staff,dc=x", UserFilter: "(&(sAMAccountName=%s)(userAccountControl=512))"})
	if err != nil {
		t.Fatal(err)
	}
	fc := &lookupConn{entries: []*ldap.Entry{ldap.NewEntry("CN=Amy,OU=Staff,DC=x", map[string][]string{"sAMAccountName": {"Amy"}, "mail": {"Amy@Corp.Test"}, "cn": {"Amy"}})}}
	a.dial = func(Config) (conn, error) { return fc, nil }

	id, err := a.LookupDN("cn=amy,ou=staff,dc=x")
	if err != nil || id.ID != "amy" || id.Email != "amy@corp.test" || id.Name != "Amy" {
		t.Fatal(id, err)
	}
	if fc.req.Scope != ldap.ScopeBaseObject || fc.req.Filter != "(&(sAMAccountName=*)(userAccountControl=512))" || fc.req.BaseDN != "cn=amy,ou=staff,dc=x" {
		t.Fatalf("查詢:%+v", fc.req)
	}
	// 不在 BaseDN 底下、格式不對:不查
	fc.req = nil
	for _, dn := range []string{"cn=amy,ou=other,dc=x", "dc=x", "not a dn", ""} {
		if _, err := a.LookupDN(dn); !errors.Is(err, ErrNoEntry) {
			t.Error(dn, err)
		}
	}
	if fc.req != nil {
		t.Error("不該查目錄")
	}
	// 被篩掉(停用)
	fc.entries = nil
	if _, err := a.LookupDN("cn=amy,ou=staff,dc=x"); !errors.Is(err, ErrNoEntry) {
		t.Error(err)
	}
	// 目錄出錯不能當成查不到
	fc.err = errors.New("斷線")
	if _, err := a.LookupDN("cn=amy,ou=staff,dc=x"); err == nil || errors.Is(err, ErrNoEntry) {
		t.Error(err)
	}
}
