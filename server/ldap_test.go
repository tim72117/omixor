package server

import (
	"strings"
	"testing"

	"omixor-core/service"
)

func ldapEnvMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestConfigureLDAP(t *testing.T) {
	// 沒設 OMIXOR_LDAP_URL:不啟用,也不報錯
	svc := &service.Service{}
	if err := configureLDAP(svc, ldapEnvMap(nil)); err != nil || svc.LDAP != nil {
		t.Fatalf("沒設:%v %v", err, svc.LDAP)
	}

	// 典型的 AD 設定(服務帳號搜尋,只允許啟用中的帳號)
	svc = &service.Service{}
	err := configureLDAP(svc, ldapEnvMap(map[string]string{
		"OMIXOR_LDAP_URL":                  "ldap://ad.example.com:389",
		"OMIXOR_LDAP_BIND_DN":              "cn=svc-omixor,cn=Users,dc=example,dc=com",
		"OMIXOR_LDAP_BIND_PASSWORD":        "pw",
		"OMIXOR_LDAP_BASE_DN":              "ou=AP,ou=Staff,dc=example,dc=com",
		"OMIXOR_LDAP_USER_FILTER":          "(&(sAMAccountName=%s)(userAccountControl=512))",
		"OMIXOR_LDAP_INSECURE_SKIP_VERIFY": "true",
		"OMIXOR_LDAP_ADMINS":               " Boss, ,Amy ,",
		"OMIXOR_LDAP_EMAIL_DOMAIN":         "@ad.example.com",
		"OMIXOR_LDAP_TRUST_MAIL":           "true",
	}))
	if err != nil || svc.LDAP == nil {
		t.Fatalf("啟用:%v", err)
	}
	if strings.Join(svc.LDAPAdmins, ",") != "boss,amy" || svc.LDAPEmailDomain != "ad.example.com" || !svc.LDAPTrustMail {
		t.Fatalf("管理員/網域:%v %q", svc.LDAPAdmins, svc.LDAPEmailDomain)
	}

	// 設了 URL 但不完整:啟動就要失敗
	bad := []map[string]string{
		{"OMIXOR_LDAP_URL": "ldap://h"},
		{"OMIXOR_LDAP_URL": "ldap://h", "OMIXOR_LDAP_USER_DN_TEMPLATE": "%s@x", "OMIXOR_LDAP_EMAIL_DOMAIN": "a b"},
		{"OMIXOR_LDAP_URL": "ldap://h", "OMIXOR_LDAP_USER_DN_TEMPLATE": "%s@x", "OMIXOR_LDAP_EMAIL_DOMAIN": "x@y.com"},
	}
	for i, m := range bad {
		if err := configureLDAP(&service.Service{}, ldapEnvMap(m)); err == nil {
			t.Errorf("第 %d 組要失敗", i)
		}
	}
}
