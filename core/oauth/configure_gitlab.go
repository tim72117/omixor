package oauth

import (
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"

	"omixor-core/config"
	"omixor-core/gitlabauth"
	"omixor-core/service"
)

// ConfigureGitLab 啟動時從環境變數開 GitLab 登入(要在 LDAP 設定之後);omixor-server 與 console 的對外 MCP 連接埠共用。
// OMIXOR_GITLAB_URL 沒設 = 不啟用(回傳 nil);設了但不完整就回傳錯誤,讓程式啟動失敗。public 是 OMIXOR_PUBLIC_URL
//
//	OMIXOR_GITLAB_URL、CLIENT_ID、CLIENT_SECRET(_FILE)、CA_CERT  見 core/gitlabauth
//	OMIXOR_GITLAB_ADMINS      伺服器管理員的 GitLab 使用者 id(數字,逗號分隔)
//	OMIXOR_GITLAB_EMAIL_DOMAINS  允許登入的 email 網域(逗號分隔;沒設不限)
//	OMIXOR_GITLAB_LINK_LDAP   true:同一個人的 GitLab 與 LDAP 併到同一帳號(要啟用 LDAP 且有服務帳號;規則見 service/gitlab.go)
//	OMIXOR_GITLAB_TRUST_MAIL  true:管理員先用 email 加進名單、還沒登入過的人,會和 GitLab 確認過的相同 email 對上
func ConfigureGitLab(svc *service.Service, getenv func(string) string, public string) error {
	cfg, err := gitlabauth.FromEnv(getenv)
	if err != nil {
		return err
	}
	if cfg == nil {
		return nil
	}
	public = strings.TrimRight(strings.TrimSpace(public), "/")
	pu, err := url.Parse(public)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return fmt.Errorf("GitLab 登入需要 OMIXOR_PUBLIC_URL(對外網址,例如 https://omixor.example.com)")
	}
	cl, err := gitlabauth.New(*cfg)
	if err != nil {
		return err
	}
	g := &service.GitLabLogin{Client: cl, Public: public,
		LinkLDAP: isOn(getenv("OMIXOR_GITLAB_LINK_LDAP")), TrustMail: isOn(getenv("OMIXOR_GITLAB_TRUST_MAIL"))}
	for _, v := range strings.Split(getenv("OMIXOR_GITLAB_ADMINS"), ",") {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("OMIXOR_GITLAB_ADMINS 要是 GitLab 使用者 id(數字),不收 username(收到 %q)", v)
		}
		g.Admins = append(g.Admins, n)
	}
	domains := getenv("OMIXOR_GITLAB_EMAIL_DOMAINS")
	for _, v := range strings.Split(domains, ",") {
		if v = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(v), "@")); v == "" {
			continue
		}
		if strings.ContainsAny(v, " @/*:") {
			return fmt.Errorf("OMIXOR_GITLAB_EMAIL_DOMAINS 只要網域,例如 corp.example.com(收到 %q)", v)
		}
		g.EmailDomains = append(g.EmailDomains, v)
	}
	if len(g.EmailDomains) == 0 && strings.TrimSpace(domains) != "" {
		return fmt.Errorf("OMIXOR_GITLAB_EMAIL_DOMAINS 沒有任何網域(收到 %q)", domains)
	}
	if g.LinkLDAP {
		_, lookup := svc.LDAP.(service.DirectoryLookup)
		can, ok := svc.LDAP.(interface{ CanLookup() bool })
		if !lookup || (ok && !can.CanLookup()) {
			return fmt.Errorf("OMIXOR_GITLAB_LINK_LDAP 要同時啟用 LDAP 登入,而且用服務帳號(OMIXOR_LDAP_BIND_DN、BASE_DN)")
		}
	}
	svc.GitLab = g
	log.Printf("GitLab 登入已啟用:%s,管理員 %d 人,允許的 email 網域 %d 個(0 = 不限),併 LDAP 帳號 %s,預先加入的 email %s", cl.URL(), len(g.Admins), len(g.EmailDomains),
		map[bool]string{true: "開", false: "關"}[g.LinkLDAP],
		map[bool]string{true: "會自動對上(OMIXOR_GITLAB_TRUST_MAIL)", false: "不自動對上"}[g.TrustMail])
	if u, err := url.Parse(cl.URL()); err == nil && u.Scheme == "http" && !config.IsLoopbackHost(u.Hostname()) {
		log.Printf("警告:OMIXOR_GITLAB_URL 是 http,client secret 與 token 會在網路上明碼傳送;建議改 https")
	}
	if pu.Scheme == "http" && !config.IsLoopbackHost(pu.Hostname()) {
		log.Printf("警告:OMIXOR_PUBLIC_URL 是 http,GitLab 登入的綁定 cookie 與授權碼沒有加密保護;建議改 https")
	}
	return nil
}

func isOn(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
