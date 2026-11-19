package server

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"omixor-core/ldapauth"
	"omixor-core/service"
)

// configureLDAP 啟動時從環境變數開 LDAP 登入。OMIXOR_LDAP_URL 沒設 = 不啟用(回傳 nil);
// 設了但不完整就回傳錯誤,讓伺服器啟動失敗,不要等到有人登入才發現
func configureLDAP(svc *service.Service, getenv func(string) string) error {
	cfg, err := ldapauth.FromEnv(getenv)
	if err != nil {
		return err
	}
	if cfg == nil {
		return nil
	}
	a, err := ldapauth.New(*cfg)
	if err != nil {
		return err
	}
	svc.LDAP = a
	for _, v := range strings.Split(getenv("OMIXOR_LDAP_ADMINS"), ",") {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			svc.LDAPAdmins = append(svc.LDAPAdmins, v)
		}
	}
	domain := strings.TrimPrefix(strings.TrimSpace(getenv("OMIXOR_LDAP_EMAIL_DOMAIN")), "@")
	if strings.ContainsAny(domain, " @/") {
		return fmt.Errorf("OMIXOR_LDAP_EMAIL_DOMAIN 只要網域,例如 corp.example.com(收到 %q)", domain)
	}
	svc.LDAPEmailDomain = domain
	switch strings.ToLower(strings.TrimSpace(getenv("OMIXOR_LDAP_TRUST_MAIL"))) {
	case "1", "true", "yes", "on":
		svc.LDAPTrustMail = true
	}
	mode := "服務帳號搜尋"
	if cfg.BindDN == "" {
		mode = "直接用使用者身分 bind"
	}
	log.Printf("LDAP 登入已啟用:%s(%s),管理員 %d 人,預先加入的 email %s", cfg.URL, mode, len(svc.LDAPAdmins),
		map[bool]string{true: "會自動對上(OMIXOR_LDAP_TRUST_MAIL)", false: "不自動對上"}[svc.LDAPTrustMail])
	if u, err := url.Parse(cfg.URL); err == nil && u.Scheme == "ldap" && !cfg.StartTLS {
		log.Printf("警告:LDAP 用明文連線(ldap:// 且沒開 OMIXOR_LDAP_START_TLS),使用者密碼會在網路上明碼傳送;建議改 ldaps:// 或開 StartTLS")
	}
	if cfg.InsecureSkipVerify {
		log.Printf("警告:OMIXOR_LDAP_INSECURE_SKIP_VERIFY 開著,不驗證 LDAP 伺服器的憑證")
	}
	return nil
}
