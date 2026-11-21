package server

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"omixor-core/config"
	"omixor-core/service"
)

// LDAP 設定經 config 集中載入:舊名稱、預設值都照舊;ldapauth 要的名稱都已定義(未定義會 panic)
func TestConfigureLDAPViaConfig(t *testing.T) {
	cfg := config.LoadFrom(envMap(map[string]string{
		"OMIXOR_LDAP_URL":          "ldaps://ad.example.com:636",
		"OMIXOR_LDAP_BIND_DN":      "cn=svc,dc=example,dc=com",
		"OPS_LDAP_BIND_PASSWORD":   "pw-secret-123", // 舊名稱
		"OMIXOR_LDAP_BASE_DN":      "dc=example,dc=com",
		"OMIXOR_LDAP_ADMINS":       "Boss",
		"OMIXOR_LDAP_TRUST_MAIL":   "yes",
		"OMIXOR_LDAP_EMAIL_DOMAIN": "@corp.example.com",
	}), config.Server)
	if err := cfg.Validate(config.Server); err == nil || !strings.Contains(err.Error(), "OMIXOR_DATABASE_URL") {
		t.Fatal("server 必填資料庫網址", err)
	}
	svc := &service.Service{}
	if err := configureLDAP(svc, cfg.Getenv); err != nil || svc.LDAP == nil {
		t.Fatal(err, svc.LDAP)
	}
	if !svc.LDAPTrustMail || svc.LDAPEmailDomain != "corp.example.com" || len(svc.LDAPAdmins) != 1 {
		t.Fatalf("%+v", svc)
	}
	// 服務帳號沒給密碼:錯誤訊息不含機密(密碼檔不存在)
	cfg = config.LoadFrom(envMap(map[string]string{
		"OMIXOR_LDAP_URL": "ldaps://ad.example.com:636", "OMIXOR_LDAP_BIND_DN": "cn=svc", "OMIXOR_LDAP_BASE_DN": "dc=x",
		"OMIXOR_LDAP_BIND_PASSWORD_FILE": "/nonexistent/secret-file",
	}), config.Server)
	if err := configureLDAP(&service.Service{}, cfg.Getenv); err == nil {
		t.Fatal("密碼檔讀不到要失敗")
	}
}

// omixor-local 的 -h 用設定定義產生環境變數說明,機密只標示、不印值
func TestLocalUsageFromConfig(t *testing.T) {
	var buf bytes.Buffer
	_, err := parseLocalArgs([]string{"-h"}, envMap(map[string]string{"OMIXOR_LOCAL_TOKEN": "omx_secret-token-value"}), &buf)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"OMIXOR_DATA_DIR", "OMIXOR_LOCAL_TOKEN", "機密", "OMIXOR_DATABASE_URL", "OPS_DATABASE_URL"} {
		if !strings.Contains(out, want) {
			t.Errorf("-h 缺 %q", want)
		}
	}
	if strings.Contains(out, "secret-token-value") {
		t.Fatal("-h 洩漏機密")
	}
}

// 型別錯誤的環境變數在 omixor-local 啟動時就報中文錯誤,且不含機密
func TestLocalArgsValidatesEnv(t *testing.T) {
	_, err := parseLocalArgs(nil, envMap(map[string]string{
		"OMIXOR_DATABASE_URL": "mysql://omixor:pw-secret-123@h/db", "OMIXOR_EXIT_ON_STDIN_EOF": "maybe",
	}), &bytes.Buffer{})
	if err == nil {
		t.Fatal("應該報錯")
	}
	if !strings.Contains(err.Error(), "OMIXOR_DATABASE_URL") || !strings.Contains(err.Error(), "OMIXOR_EXIT_ON_STDIN_EOF") || strings.Contains(err.Error(), "pw-secret-123") {
		t.Fatal(err)
	}
}
