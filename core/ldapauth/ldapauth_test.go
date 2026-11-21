package ldapauth

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestFromEnv(t *testing.T) {
	// 沒設 URL = 不啟用
	if c, err := FromEnv(env(nil)); c != nil || err != nil {
		t.Fatalf("沒設 URL:%v %v", c, err)
	}

	pw := filepath.Join(t.TempDir(), "ldap_password")
	_ = os.WriteFile(pw, []byte("s3cret\n"), 0o600)
	c, err := FromEnv(env(map[string]string{
		"OMIXOR_LDAP_URL":                  "ldap://ad.example.com:389",
		"OMIXOR_LDAP_BIND_DN":              "cn=svc-omixor,cn=Users,dc=example,dc=com",
		"OMIXOR_LDAP_BIND_PASSWORD_FILE":   pw,
		"OMIXOR_LDAP_BASE_DN":              "ou=AP,ou=Staff,dc=example,dc=com",
		"OMIXOR_LDAP_USER_FILTER":          "(&(sAMAccountName=%s)(userAccountControl=512))",
		"OMIXOR_LDAP_INSECURE_SKIP_VERIFY": "true",
		"OMIXOR_LDAP_TIMEOUT_SECONDS":      "5",
	}))
	if err != nil || c == nil {
		t.Fatal(err)
	}
	if c.BindPassword != "s3cret" || c.IDAttr != "sAMAccountName" || c.NameAttr != "displayName" || c.MailAttr != "mail" ||
		!c.InsecureSkipVerify || c.StartTLS || c.Timeout != 5*time.Second {
		t.Fatalf("設定:%+v", c)
	}

	bad := []struct {
		name string
		m    map[string]string
		want string
	}{
		{"網址格式", map[string]string{"OMIXOR_LDAP_URL": "ad.example.com"}, "ldap://主機"},
		{"沒有驗證方式", map[string]string{"OMIXOR_LDAP_URL": "ldap://h"}, "OMIXOR_LDAP_BIND_DN"},
		{"服務帳號沒密碼", map[string]string{"OMIXOR_LDAP_URL": "ldap://h", "OMIXOR_LDAP_BIND_DN": "cn=a", "OMIXOR_LDAP_BASE_DN": "dc=x"}, "匿名 bind"},
		{"沒有 BaseDN", map[string]string{"OMIXOR_LDAP_URL": "ldap://h", "OMIXOR_LDAP_BIND_DN": "cn=a", "OMIXOR_LDAP_BIND_PASSWORD": "p"}, "BASE_DN"},
		{"篩選條件沒有 %s", map[string]string{"OMIXOR_LDAP_URL": "ldap://h", "OMIXOR_LDAP_BIND_DN": "cn=a", "OMIXOR_LDAP_BIND_PASSWORD": "p", "OMIXOR_LDAP_BASE_DN": "dc=x", "OMIXOR_LDAP_USER_FILTER": "(a=b)"}, "%s"},
		{"ldaps 又 StartTLS", map[string]string{"OMIXOR_LDAP_URL": "ldaps://h", "OMIXOR_LDAP_START_TLS": "true", "OMIXOR_LDAP_USER_DN_TEMPLATE": "%s@x"}, "已經加密"},
		{"密碼檔讀不到", map[string]string{"OMIXOR_LDAP_URL": "ldap://h", "OMIXOR_LDAP_BIND_DN": "cn=a", "OMIXOR_LDAP_BIND_PASSWORD_FILE": filepath.Join(t.TempDir(), "nope")}, "BIND_PASSWORD_FILE"},
		{"逾時不是數字", map[string]string{"OMIXOR_LDAP_URL": "ldap://h", "OMIXOR_LDAP_USER_DN_TEMPLATE": "%s@x", "OMIXOR_LDAP_TIMEOUT_SECONDS": "abc"}, "正整數"},
	}
	for _, b := range bad {
		if _, err := FromEnv(env(b.m)); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s:要錯誤含 %q,得到 %v", b.name, b.want, err)
		}
	}

	// 直接用使用者身分 bind(不用服務帳號)
	if c, err := FromEnv(env(map[string]string{"OMIXOR_LDAP_URL": "ldaps://h", "OMIXOR_LDAP_USER_DN_TEMPLATE": "%s@corp.test"})); err != nil || c.UserDNTemplate == "" {
		t.Fatalf("DN 樣板:%v %v", c, err)
	}
}

// fakeDir 假的 LDAP 目錄:記下 bind 與搜尋,照設定回答
type fakeDir struct {
	service, servicePW string
	users              map[string]fakeUser // dn → 使用者
	binds              []string
	filters            []string
	startTLS           bool
	searchErr          error
	userBindErr        error // 非 nil:使用者自己的 bind 回這個錯誤(模擬連線中斷等)
}
type fakeUser struct {
	pw, id, name, mail string
	disabled           bool
}

func (f *fakeDir) Bind(dn, pw string) error {
	f.binds = append(f.binds, dn)
	if dn == f.service && pw == f.servicePW {
		return nil
	}
	if f.userBindErr != nil {
		return f.userBindErr
	}
	if u, ok := f.users[dn]; ok && u.pw == pw && pw != "" {
		return nil
	}
	return &ldap.Error{ResultCode: ldap.LDAPResultInvalidCredentials, Err: errors.New("invalid")}
}
func (f *fakeDir) Search(r *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.filters = append(f.filters, r.Filter)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	res := &ldap.SearchResult{}
	for dn, u := range f.users {
		if u.disabled || !strings.Contains(r.Filter, "="+u.id+")") {
			continue
		}
		res.Entries = append(res.Entries, ldap.NewEntry(dn, map[string][]string{"sAMAccountName": {u.id}, "displayName": {u.name}, "mail": {u.mail}}))
	}
	return res, nil
}
func (f *fakeDir) StartTLS(*tls.Config) error { f.startTLS = true; return nil }
func (f *fakeDir) Close() error               { return nil }

func newAuth(t *testing.T, f *fakeDir, m map[string]string) *Authenticator {
	t.Helper()
	base := map[string]string{"OMIXOR_LDAP_URL": "ldap://h", "OMIXOR_LDAP_BIND_DN": "cn=svc", "OMIXOR_LDAP_BIND_PASSWORD": "svcpw", "OMIXOR_LDAP_BASE_DN": "ou=AP,dc=x",
		"OMIXOR_LDAP_USER_FILTER": "(&(sAMAccountName=%s)(userAccountControl=512))"}
	for k, v := range m {
		base[k] = v
	}
	c, err := FromEnv(env(base))
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(*c)
	if err != nil {
		t.Fatal(err)
	}
	a.dial = func(Config) (conn, error) { return f, nil }
	return a
}

func TestAuthenticateServiceAccount(t *testing.T) {
	f := &fakeDir{service: "cn=svc", servicePW: "svcpw", users: map[string]fakeUser{
		"cn=Amy Lin,ou=AP,dc=x": {pw: "pw-amy", id: "amy", name: "Amy Lin", mail: "Amy@Corp.Test"},
		"cn=Old Bob,ou=AP,dc=x": {pw: "pw-bob", id: "bob", name: "Old Bob", mail: "bob@corp.test", disabled: true},
		"cn=Cathy,ou=AP,dc=x":   {pw: "pw-c", id: "cathy"},
	}}
	a := newAuth(t, f, nil)

	// 帳號不分大小寫;email 轉小寫;名稱取 displayName
	id, err := a.Authenticate("  AMY ", "pw-amy")
	if err != nil || id.ID != "amy" || id.Name != "Amy Lin" || id.Email != "amy@corp.test" || id.DN != "cn=Amy Lin,ou=AP,dc=x" {
		t.Fatalf("amy:%+v %v", id, err)
	}
	// 先用服務帳號 bind,再用使用者 DN bind;篩選條件裡的帳號有帶進去
	if len(f.binds) != 2 || f.binds[0] != "cn=svc" || f.binds[1] != "cn=Amy Lin,ou=AP,dc=x" || !strings.Contains(f.filters[0], "(sAMAccountName=amy)") {
		t.Fatalf("流程:%v %v", f.binds, f.filters)
	}

	// 沒有 mail、displayName 的人:欄位空白(由 service 決定怎麼辦)
	if id, err := a.Authenticate("cathy", "pw-c"); err != nil || id.Email != "" || id.ID != "cathy" {
		t.Fatalf("cathy:%+v %v", id, err)
	}

	// 錯誤一律同一個:密碼錯、帳號不存在、被篩選掉(停用)、空密碼、空帳號
	for _, c := range []struct{ u, p string }{{"amy", "wrong"}, {"nobody", "x"}, {"bob", "pw-bob"}, {"amy", ""}, {"", "pw-amy"}} {
		if _, err := a.Authenticate(c.u, c.p); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%+v 要 ErrInvalidCredentials,得到 %v", c, err)
		}
	}
	// 空密碼不能走到 LDAP(匿名 bind 會成功)
	before := len(f.binds)
	_, _ = a.Authenticate("amy", "")
	if len(f.binds) != before {
		t.Fatal("空密碼不該送到 LDAP")
	}

	// 帳號裡的篩選字元會被跳脫,不能改變查詢
	f.filters = nil
	_, _ = a.Authenticate("*)(uid=*", "x")
	if len(f.filters) != 1 || strings.Contains(f.filters[0], "(uid=*") || !strings.Contains(f.filters[0], `\2a\29\28uid=\2a`) {
		t.Fatalf("沒跳脫:%v", f.filters)
	}

	// 同一個帳號對到兩筆:不明確,不能猜
	f.users["cn=Amy 2,ou=AP,dc=x"] = fakeUser{pw: "pw-amy", id: "amy"}
	if _, err := a.Authenticate("amy", "pw-amy"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("兩筆:%v", err)
	}
}

func TestAuthenticateErrors(t *testing.T) {
	// 服務帳號密碼錯:是設定問題,不是使用者密碼錯
	f := &fakeDir{service: "cn=svc", servicePW: "other"}
	_, err := newAuth(t, f, nil).Authenticate("amy", "x")
	if err == nil || errors.Is(err, ErrInvalidCredentials) || !strings.Contains(err.Error(), "服務帳號") {
		t.Fatalf("服務帳號錯:%v", err)
	}
	// 搜尋出錯(例如 BaseDN 不存在)
	f = &fakeDir{service: "cn=svc", servicePW: "svcpw", searchErr: errors.New("no such object")}
	if _, err := newAuth(t, f, nil).Authenticate("amy", "x"); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("搜尋錯:%v", err)
	}
	// 連不上
	a := newAuth(t, &fakeDir{}, nil)
	a.dial = func(Config) (conn, error) { return nil, errors.New("連不到 LDAP 伺服器") }
	if _, err := a.Authenticate("amy", "x"); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("連不上:%v", err)
	}
}

// 使用者 bind 的錯誤分類:只有目錄明確說帳號或密碼不對才是認證失敗;連線出問題要讓管理員看得到
func TestAuthenticateBindErrors(t *testing.T) {
	users := map[string]fakeUser{"cn=Amy,ou=AP,dc=x": {pw: "pw", id: "amy"}}
	for _, c := range []struct {
		name string
		err  error
		bad  bool // true = 該回 ErrInvalidCredentials
	}{
		{"連線被切斷", &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}, false},
		{"逾時", os.ErrDeadlineExceeded, false},
		{"連線提早結束", io.EOF, false},
		{"伺服器忙碌(51)", &ldap.Error{ResultCode: ldap.LDAPResultBusy, Err: errors.New("busy")}, false},
		{"伺服器不願意處理(53)", &ldap.Error{ResultCode: ldap.LDAPResultUnwillingToPerform, Err: errors.New("unwilling")}, false},
		{"密碼錯(49)", &ldap.Error{ResultCode: ldap.LDAPResultInvalidCredentials, Err: errors.New("bad")}, true},
		{"沒有權限(50)", &ldap.Error{ResultCode: ldap.LDAPResultInsufficientAccessRights, Err: errors.New("denied")}, true},
		{"找不到 DN(32)", &ldap.Error{ResultCode: ldap.LDAPResultNoSuchObject, Err: errors.New("no object")}, true},
		{"DN 格式不對(34)", &ldap.Error{ResultCode: ldap.LDAPResultInvalidDNSyntax, Err: errors.New("syntax")}, true},
	} {
		f := &fakeDir{service: "cn=svc", servicePW: "svcpw", users: users, userBindErr: c.err}
		_, err := newAuth(t, f, nil).Authenticate("amy", "pw")
		if err == nil || errors.Is(err, ErrInvalidCredentials) != c.bad {
			t.Errorf("%s:ErrInvalidCredentials 應該是 %v,得到 %v", c.name, c.bad, err)
		}
	}
}

func TestAuthenticateDNTemplate(t *testing.T) {
	f := &fakeDir{users: map[string]fakeUser{"amy@corp.test": {pw: "pw-amy"}}}
	a := newAuth(t, f, map[string]string{"OMIXOR_LDAP_BIND_DN": "", "OMIXOR_LDAP_BIND_PASSWORD": "", "OMIXOR_LDAP_USER_DN_TEMPLATE": "%s@corp.test"})
	id, err := a.Authenticate("Amy", "pw-amy")
	if err != nil || id.ID != "amy" || id.DN != "amy@corp.test" || len(f.filters) != 0 {
		t.Fatalf("DN 樣板:%+v %v %v", id, err, f.filters)
	}
	if _, err := a.Authenticate("amy", "bad"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("密碼錯:%v", err)
	}
}

// mail 屬性沒有 @(例如被填成保留的本機身分 omixor-console)視為沒有 mail
func TestCleanMail(t *testing.T) {
	for in, want := range map[string]string{" Amy@Corp.Test ": "amy@corp.test", "omixor-console": "", "tim": "", "": "", "a@": "", "@b": "", "a@b@c": ""} {
		if got := cleanMail(in); got != want {
			t.Errorf("cleanMail(%q) = %q,要 %q", in, got, want)
		}
	}
}
