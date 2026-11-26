package service

import (
	"errors"
	"log"
	"strconv"
	"strings"

	"omixor-core/gitlabauth"
	"omixor-core/ldapauth"
	"omixor-core/model"
	"omixor-core/store"
)

// GitLab 登入:帳號對應規則(HTTP 流程在 gitlab_login.go;GitLab 的 HTTP 在 core/gitlabauth)。
//
//   - GitLab 使用者要是 state=active、不是 bot;有設 EmailDomains 時,GitLab 確認過的 email 網域要在清單裡(否則 403)。
//     「確認過」只看 confirmed_at:自建 GitLab 要開啟 email 確認,這一項才有意義
//   - 穩定鍵是 gitlab:<GitLab 的數字 id>(同 ldap: 前綴,存在 EvoleapID 欄位);這個帳號已經存在就一直用它,不改併
//   - 開了 LinkLDAP 而且 GitLab 帳號有 provider 以 ldap 開頭的身分時,每次登入都用 omixor 自己的 LDAP 服務帳號查那個身分的 DN
//     (帳號名稱取自目錄,不用 GitLab 的 username:那是使用者自己改得動的)。下面任一項不成立就 403、不建任何帳號(請使用者找管理員):
//       1. 查得到、在 BaseDN 底下、符合 UserFilter(AD 停用、被篩掉、不在這個目錄都算查不到)
//       2. 對應的 ldap:<帳號> 沒有停用(已經有 gitlab:<id> 帳號的人也檢查,不能繞過停用)
//       3. 還沒有 gitlab:<id> 帳號時:GitLab 的 email 要確認過,而且等於既有 ldap: 帳號的 email(還沒有那個帳號時,
//          等於目錄算出來的 email,建立後 LDAP 登入是同一帳號)
//     查目錄出錯(連不上等):502
//   - 其他情況(沒開 LinkLDAP、GitLab 帳號沒有 ldap 身分)用 gitlab:<id>;email 要 GitLab 確認過;被別的身分佔用 403;
//     管理員預先加入的名單要 TrustMail 才接手
//   - Admins(GitLab 的數字 id,不是 username:username 改掉後別人可以拿去用)上的人是伺服器管理員,只升不降

// GitLabIDPrefix GitLab 身分的鍵:gitlab:<數字 id>
const GitLabIDPrefix = "gitlab:"

// GitLabLogin GitLab 登入的設定(Service.GitLab;nil = 沒有啟用)
type GitLabLogin struct {
	Client *gitlabauth.Client
	// Public omixor 的對外網址(不含結尾斜線);GitLab 的兩個回呼網址由它推導
	Public string
	// Admins 伺服器管理員的 GitLab 使用者 id
	Admins []int64
	// LinkLDAP 同一個人的 GitLab 與 LDAP 併到同一帳號(要有能用服務帳號查目錄的 LDAP,見 DirectoryLookup)
	LinkLDAP bool
	// TrustMail 管理員預先用 email 加入、還沒對上身分的人,可以由 GitLab 確認過的相同 email 接手
	TrustMail bool
	// EmailDomains 允許登入的 email 網域(小寫、不含 @;完全相同才算,不含子網域);空 = 不限
	EmailDomains []string
}

// DirectoryLookup 能用 DN 查帳號的目錄(ldapauth.Authenticator;併帳號用)
type DirectoryLookup interface {
	LookupDN(dn string) (*ldapauth.Identity, error)
}

// GitLabEnabled 這台伺服器有沒有啟用 GitLab 登入
func (s *Service) GitLabEnabled() bool { return s.GitLab != nil && s.GitLab.Client != nil }

// GitLabOrigin GitLab 的來源(同意畫面 CSP 用);沒啟用回 ""
func (s *Service) GitLabOrigin() string {
	if !s.GitLabEnabled() {
		return ""
	}
	return s.GitLab.Client.Origin()
}

// authenticateGitLab 用授權碼向 GitLab 問出「你是誰」並對上 omixor 使用者;GitLab 的 token 用完立刻撤銷、不保存。
// 錯誤是 *Error:403 這個人不能登入,502 GitLab 或目錄出問題
func (s *Service) authenticateGitLab(code, redirectURI, verifier string) (*model.User, error) {
	cl := s.GitLab.Client
	token, err := cl.Exchange(code, redirectURI, verifier)
	if err != nil {
		log.Printf("GitLab 換 token 失敗:%v", err)
		return nil, fail(502, "GitLab 登入沒有完成,請重新開始")
	}
	defer func() {
		if err := cl.Revoke(token); err != nil {
			log.Printf("撤銷 GitLab token 失敗:%v", err)
		}
	}()
	g, err := cl.CurrentUser(token)
	if err != nil {
		log.Printf("GitLab 查使用者失敗:%v", err)
		return nil, fail(502, "GitLab 驗證身分失敗,請稍後再試")
	}
	return s.resolveGitLabUser(g)
}

func (s *Service) gitlabDomainAllowed(email string) bool {
	if len(s.GitLab.EmailDomains) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	for _, d := range s.GitLab.EmailDomains {
		if at > 0 && strings.EqualFold(email[at+1:], d) {
			return true
		}
	}
	return false
}

func (s *Service) isGitLabAdmin(id int64) bool {
	for _, a := range s.GitLab.Admins {
		if a == id {
			return true
		}
	}
	return false
}

// resolveGitLabUser 規則見檔頭
func (s *Service) resolveGitLabUser(g *gitlabauth.User) (*model.User, error) {
	gid := strconv.FormatInt(g.ID, 10)
	detail := map[string]any{"gitlabId": g.ID, "gitlabUsername": g.Username}
	denied := func(reason, msg string) error {
		s.Audit(nil, "login.denied", "", map[string]any{"via": "gitlab", "gitlabId": g.ID, "gitlabUsername": g.Username, "reason": reason})
		return fail(403, "%s", msg)
	}
	if g.State != "active" || g.Bot {
		return nil, denied("gitlab-inactive", "這個 GitLab 帳號不是啟用中的使用者,不能登入")
	}
	email := ""
	if g.Confirmed() {
		if e := strings.ToLower(strings.TrimSpace(g.Email)); validUserEmail(e) == nil {
			email = e
		}
	}
	if !s.gitlabDomainAllowed(email) {
		return nil, denied("email-domain", "這個 GitLab 帳號的 email 網域不允許登入(要是確認過的公司 email),請聯絡管理員")
	}
	x := externalIdentity{
		key: GitLabIDPrefix + gid, email: email, name: g.Name,
		admin: s.isGitLabAdmin(g.ID), trustMail: s.GitLab.TrustMail,
		via: "gitlab", detail: detail,
		noEmail: fail(403, "這個 GitLab 帳號沒有確認過的 email,請先在 GitLab 完成 email 確認"),
		pending: "管理員已經用這個 email(%s)預先加入你,但這台伺服器不會自動把 GitLab 帳號對上預先加入的名單。請先讓管理員移除這筆,或請伺服器管理員開 OMIXOR_GITLAB_TRUST_MAIL",
	}
	existing := false // 已經有 gitlab:<id> 帳號
	if _, err := s.Store.UserByEvoleapID(x.key); err == nil {
		existing = true
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if !s.GitLab.LinkLDAP || len(g.LDAPDNs()) == 0 {
		return s.resolveExternalUser(x)
	}

	// 有 LDAP 身分的人:目錄說了算,任何不符都拒絕,不能改建另一個帳號
	entry, err := s.gitlabDirectoryEntry(g)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, denied("ldap-not-found", "在 LDAP 目錄找不到這個 GitLab 帳號對應的使用者(可能已停用),請聯絡管理員")
	}
	linked := x
	linked.key, linked.name = LDAPIDPrefix+entry.ID, entry.Name
	linked.detail = map[string]any{"gitlabId": g.ID, "gitlabUsername": g.Username, "username": entry.ID}
	old, err := s.Store.UserByEvoleapID(linked.key)
	switch {
	case errors.Is(err, store.ErrNotFound):
		old = nil
	case err != nil:
		return nil, err
	case old.Disabled:
		s.Audit(&model.Principal{User: *old}, "login.denied", "", map[string]any{"via": "gitlab", "reason": "disabled", "gitlabId": g.ID})
		return nil, fail(403, "這個帳號已停用,請聯絡管理員")
	}
	if existing {
		return s.resolveExternalUser(x)
	}
	if email == "" {
		s.Audit(nil, "login.denied", "", x.auditDetail("reason", "no-email"))
		return nil, x.noEmail
	}
	want := ""
	if old != nil {
		want = old.Email
	} else {
		want = directoryEmail(s.LDAPEmailDomain, entry.ID, entry.Email)
	}
	if !strings.EqualFold(want, email) {
		return nil, denied("ldap-email-differs", "這個 GitLab 帳號的 email 和對應的 LDAP 帳號不同,不能登入。請把 GitLab 的 email 改成公司目錄裡的,或聯絡管理員")
	}
	return s.resolveExternalUser(linked)
}

// gitlabDirectoryEntry GitLab 帳號掛的 LDAP 身分在 omixor 的目錄裡是哪個帳號;查不到或指到不只一個帳號回 nil。
// 目錄本身出問題(或沒有能查的目錄)回 502
func (s *Service) gitlabDirectoryEntry(g *gitlabauth.User) (*ldapauth.Identity, error) {
	dir, ok := s.LDAP.(DirectoryLookup)
	if !ok {
		log.Print("GitLab 併帳號:沒有能用 DN 查詢的 LDAP 目錄")
		return nil, fail(502, "目前查不到 LDAP 目錄,請稍後再試")
	}
	var found *ldapauth.Identity
	for _, dn := range g.LDAPDNs() {
		e, err := dir.LookupDN(dn)
		if errors.Is(err, ldapauth.ErrNoEntry) {
			continue
		}
		if err != nil {
			log.Printf("GitLab 併帳號時查目錄失敗(GitLab id %d):%v", g.ID, err)
			return nil, fail(502, "目前查不到 LDAP 目錄,請稍後再試")
		}
		if e == nil || e.ID == "" {
			continue
		}
		if found != nil && found.ID != e.ID {
			return nil, nil
		}
		found = e
	}
	return found, nil
}
