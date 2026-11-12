// Package authtest 是測試用的假 LDAP 伺服器:dc=example,dc=com 底下有
// alice(管理員、使用者群組)、bob(使用者群組)、carol(不在群組),密碼是 <名字>-pw;服務帳號 cn=ops-svc 密碼 svc-pw。
package authtest

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jimlambrt/gldap"
)

// 假的 LDAP 伺服器:dc=example,dc=com 底下有使用者與群組
type Fake struct {
	Addr    string
	Mu      sync.Mutex
	entries map[string]map[string][]string // DN → 屬性
	pw      map[string]string              // DN(小寫)→ 密碼
	Binds   []string
	Filters []string
}

const (
	Base     = "dc=example,dc=com"
	SvcDN    = "cn=ops-svc,ou=svc," + Base
	AliceDN  = "uid=alice,ou=people," + Base
	BobDN    = "uid=bob,ou=people," + Base
	CarolDN  = "uid=carol,ou=people," + Base
	AdminsDN = "cn=ops-admins,ou=groups," + Base
	UsersDN  = "cn=ops-users,ou=groups," + Base
)

// Start 啟動假伺服器(測試結束自動停)
func Start(t testing.TB) *Fake {
	f, stop, err := StartServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return f
}

// StartServer 在 addr 啟動假伺服器(127.0.0.1:0 表示隨便一個埠);給測試與 tools/fake-ldap 用
func StartServer(addr string) (*Fake, func(), error) {
	f := &Fake{entries: map[string]map[string][]string{
		AliceDN:  {"uid": {"alice"}, "cn": {"Alice Chen"}, "mail": {"alice@example.com"}, "objectClass": {"person"}},
		BobDN:    {"uid": {"bob"}, "cn": {"Bob Lin"}, "objectClass": {"person"}},
		CarolDN:  {"uid": {"carol"}, "cn": {"Carol"}, "objectClass": {"person"}},
		AdminsDN: {"cn": {"ops-admins"}, "member": {AliceDN}, "objectClass": {"groupOfNames"}},
		UsersDN:  {"cn": {"ops-users"}, "member": {AliceDN, BobDN}, "objectClass": {"groupOfNames"}},
	}, pw: map[string]string{strings.ToLower(SvcDN): "svc-pw", AliceDN: "alice-pw", BobDN: "bob-pw", CarolDN: "carol-pw"}}
	s, err := gldap.NewServer()
	if err != nil {
		return nil, nil, err
	}
	mux, _ := gldap.NewMux()
	_ = mux.Bind(f.bind)
	_ = mux.Search(f.search)
	_ = s.Router(mux)
	if strings.HasSuffix(addr, ":0") {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, nil, err
		}
		addr = ln.Addr().String()
		ln.Close()
	}
	f.Addr = addr
	go func() { _ = s.Run(addr) }()
	for i := 0; i < 200 && !s.Ready(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return f, func() { _ = s.Stop() }, nil
}

func (f *Fake) bind(w *gldap.ResponseWriter, r *gldap.Request) {
	resp := r.NewBindResponse(gldap.WithResponseCode(gldap.ResultInvalidCredentials))
	defer func() { _ = w.Write(resp) }()
	m, err := r.GetSimpleBindMessage()
	if err != nil {
		return
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Binds = append(f.Binds, m.UserName)
	if pw, ok := f.pw[strings.ToLower(m.UserName)]; ok && pw == string(m.Password) {
		resp.SetResultCode(gldap.ResultSuccess)
	}
}

func (f *Fake) search(w *gldap.ResponseWriter, r *gldap.Request) {
	m, err := r.GetSearchMessage()
	if err != nil {
		return
	}
	f.Mu.Lock()
	f.Filters = append(f.Filters, m.Filter)
	f.Mu.Unlock()
	for dn, attrs := range f.entries {
		inScope := (m.Scope == gldap.BaseObject && strings.EqualFold(dn, m.BaseDN)) ||
			(m.Scope != gldap.BaseObject && strings.HasSuffix(strings.ToLower(dn), strings.ToLower(m.BaseDN)))
		if !inScope || !evalFilter(m.Filter, attrs) {
			continue
		}
		e := r.NewSearchResponseEntry(dn)
		for k, v := range attrs {
			e.AddAttribute(k, v)
		}
		_ = w.Write(e)
	}
	_ = w.Write(r.NewSearchDoneResponse(gldap.WithResponseCode(gldap.ResultSuccess)))
}

// 簡單的篩選求值:(&…)、(|…)、(a=b)、(a=*),值會先解開 \xx 跳脫
func evalFilter(f string, attrs map[string][]string) bool {
	f = strings.TrimSpace(f)
	if !strings.HasPrefix(f, "(") || !strings.HasSuffix(f, ")") {
		return false
	}
	inner := f[1 : len(f)-1]
	if strings.HasPrefix(inner, "&") || strings.HasPrefix(inner, "|") {
		parts := splitParts(inner[1:])
		for _, p := range parts {
			ok := evalFilter(p, attrs)
			if inner[0] == '&' && !ok {
				return false
			}
			if inner[0] == '|' && ok {
				return true
			}
		}
		return inner[0] == '&'
	}
	k, v, ok := strings.Cut(inner, "=")
	if !ok {
		return false
	}
	for ak, vals := range attrs {
		if !strings.EqualFold(ak, k) {
			continue
		}
		if v == "*" {
			return true
		}
		for _, x := range vals {
			if strings.EqualFold(x, unescape(v)) {
				return true
			}
		}
	}
	return false
}

func splitParts(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			if depth == 0 {
				start = i
			}
			depth++
		case ')':
			depth--
			if depth == 0 {
				out = append(out, s[start:i+1])
			}
		}
	}
	return out
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+2 < len(s) {
			var n byte
			for _, c := range s[i+1 : i+3] {
				n <<= 4
				switch {
				case c >= '0' && c <= '9':
					n |= byte(c - '0')
				case c >= 'a' && c <= 'f':
					n |= byte(c-'a') + 10
				case c >= 'A' && c <= 'F':
					n |= byte(c-'A') + 10
				}
			}
			b.WriteByte(n)
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
