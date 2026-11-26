package service

import (
	"errors"
	"log"
	"strings"
	"time"

	"omixor-core/ldapauth"
	"omixor-core/model"
)

// Directory 驗證帳號密碼的目錄服務(LDAP / Active Directory;實作見 ldapauth,伺服器啟動時由環境變數建立)
type Directory interface {
	Authenticate(username, password string) (*ldapauth.Identity, error)
}

// LDAPIDPrefix LDAP 身分存在使用者的 EvoleapID 欄位(原本只放 evoleap 的 id;欄位是唯一的文字,
// 加前綴就不會和 evoleap 的 id 撞,也不用改資料庫):ldap:<小寫帳號>
const LDAPIDPrefix = "ldap:"

// LDAPSettings 一次目錄登入要用的整組設定。伺服器用啟動時的欄位(見 Service.LDAPSettings);
// 宿主(console)的設定可以在執行中改,每次登入現取一份傳進 ProveLDAP,不改 Service 的欄位
type LDAPSettings struct {
	Dir         Directory
	Admins      []string // 伺服器管理員的帳號名單(不分大小寫)
	EmailDomain string
	TrustMail   bool
	// Failures 失敗鎖定;nil = 這個 service 自己的(宿主另有登入途徑時傳同一個進來共用)
	Failures *LoginFailures
	// UncheckedMaxAge 目錄查不了帳號時,一次 OAuth 授權最長可以用多久(0 = DefaultUncheckedLDAPAge),見 oauth_ldap.go
	UncheckedMaxAge time.Duration
}

// LDAPSettings 伺服器啟動時設定的目錄登入(Dir 為 nil = 沒有啟用)
func (s *Service) LDAPSettings() LDAPSettings {
	return LDAPSettings{Dir: s.LDAP, Admins: s.LDAPAdmins, EmailDomain: s.LDAPEmailDomain, TrustMail: s.LDAPTrustMail}
}

// LoginWithLDAP 用 LDAP 帳號密碼證明身分,換 omixor 自己的 token(規則同 LoginWithEvoleap,差在身分來源)。
//   - 第一次通過 LDAP 的人自動建立成一般使用者(不屬於任何組織,要管理員用 email 加進組織)
//   - LDAPAdmins 名單上的帳號是伺服器管理員(每次登入都確認;從名單拿掉不會自動降級,要到使用者管理改)
//   - email 取目錄裡的 mail;沒有就用「帳號@LDAPEmailDomain」;兩個都沒有就不能建立(組織成員是用 email 加的)
func (s *Service) LoginWithLDAP(username, password, device, ip string) (*LoginResult, error) {
	u, err := s.ProveLDAP(s.LDAPSettings(), username, password, ip)
	if err != nil {
		return nil, err
	}
	return s.issueToken(u, device, s.mt(), "ldap")
}

// ProveLDAP 用目錄帳號密碼證明身分、回傳對上的 omixor 使用者(不發 token;REST 登入與 OAuth 同意頁共用,
// 因此兩邊的每來源頻率限制、帳號失敗鎖定與稽核是同一份)。密碼不寫進任何紀錄
func (s *Service) ProveLDAP(set LDAPSettings, username, password, ip string) (*model.User, error) {
	if set.Dir == nil {
		return nil, fail(404, "這台伺服器沒有啟用 LDAP 登入")
	}
	if !s.allowLogin(&s.logins.ldap, ip) {
		return nil, fail(429, "登入嘗試太頻繁,請一分鐘後再試")
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" || password == "" {
		return nil, fail(400, "要給帳號與密碼")
	}
	// 先預扣一次再送目錄(見 LoginFailures)。不管帳號存不存在都一樣計算,所以不會洩漏哪些帳號存在
	fails, max, win := set.Failures, 0, time.Duration(0)
	if fails == nil {
		fails, max, win = &s.ldapFails, s.LDAPMaxFailures, s.LDAPFailWindow
	} else {
		max, win = fails.Max, fails.Window
	}
	if wait, ok := fails.reserve(username, s.now(), max, win); !ok {
		s.Audit(nil, "login.denied", "", map[string]any{"via": "ldap", "username": username, "reason": "too-many-failures"})
		return nil, fail(429, "這個帳號連續輸錯太多次,請 %d 分鐘後再試", int(wait/time.Minute)+1)
	}
	id, err := set.Dir.Authenticate(username, password)
	if err != nil {
		if errors.Is(err, ldapauth.ErrInvalidCredentials) {
			s.Audit(nil, "login.denied", "", map[string]any{"via": "ldap", "username": username, "reason": "credentials"})
			return nil, fail(401, "帳號或密碼錯誤")
		}
		if errors.Is(err, ldapauth.ErrNotInGroup) {
			fails.Succeeded(username)
			s.Audit(nil, "login.denied", "", map[string]any{"via": "ldap", "username": username, "reason": "not-in-group"})
			return nil, fail(403, "%s", ldapauth.ErrNotInGroup.Error())
		}
		// 連線、服務帳號、BaseDN 這類設定問題:細節只留在伺服器紀錄,不給登入的人
		fails.Abort(username)
		log.Printf("LDAP 驗證失敗(帳號 %s):%v", username, err)
		return nil, fail(502, "LDAP 驗證失敗,請聯絡管理員")
	}
	fails.Succeeded(username)
	return s.resolveDirectory(set, DirectoryIdentity{ID: id.ID, Email: id.Email, Name: id.Name}, set.isAdmin(id.ID))
}

// DirectoryIdentity 目錄(LDAP / AD,或宿主自己的帳號目錄)已經驗證過的身分:ID 是穩定的小寫帳號,Email、Name 可以是空的
type DirectoryIdentity struct{ ID, Email, Name string }

// ResolveDirectoryUser 把目錄驗證過的身分對上 omixor 使用者(LoginWithLDAP 與宿主自己的目錄登入共用;密碼驗證、失敗計數、發 token 不在這裡):
//   - 對應鍵是 LDAPIDPrefix+ID(存在 EvoleapID 欄位);第一次出現的人自動建立成一般使用者,isAdmin(來源目錄的管理員名單)為真則是伺服器管理員
//   - email 取目錄的 mail;沒有(或不合法)就用「帳號@LDAPEmailDomain」;兩個都沒有就不能建立
//   - 同 email 已被另一個身分用 → 403;管理員預先用 email 加入但還沒對上身分的人,要 LDAPTrustMail 才會接手
//   - 停用的帳號 403;isAdmin 為真但目前不是管理員 → 升級(從名單拿掉不會自動降級)
//
// 回傳已存好的使用者(LastLoginAt 已更新)
func (s *Service) ResolveDirectoryUser(id DirectoryIdentity, isAdmin bool) (*model.User, error) {
	return s.resolveDirectory(s.LDAPSettings(), id, isAdmin)
}

// resolveDirectory ResolveDirectoryUser 的本體,email 網域與是否信任 mail 照 set
func (s *Service) resolveDirectory(set LDAPSettings, id DirectoryIdentity, isAdmin bool) (*model.User, error) {
	return s.resolveExternalUser(externalIdentity{
		key: LDAPIDPrefix + id.ID, email: directoryEmail(set.EmailDomain, id.ID, id.Email), name: id.Name,
		admin: isAdmin, trustMail: set.TrustMail,
		via: "ldap", detail: map[string]any{"username": id.ID},
		noEmail: fail(403, "這個 LDAP 帳號(%s)沒有 email:目錄裡要有 mail 屬性,或請管理員設定 OMIXOR_LDAP_EMAIL_DOMAIN", id.ID),
		pending: "管理員已經用這個 email(%s)預先加入你,但這台伺服器不會自動把 LDAP 帳號對上預先加入的名單。請先讓管理員移除這筆,或請伺服器管理員開 OMIXOR_LDAP_TRUST_MAIL",
	})
}

// directoryEmail 目錄帳號的 email:目錄的 mail(沒有 @ 或是保留的本機身分當作沒有),沒有就「帳號@domain」,都沒有回空字串
func directoryEmail(domain, id, mail string) string {
	email := strings.ToLower(strings.TrimSpace(mail))
	if validUserEmail(email) != nil {
		email = ""
	}
	if email == "" && domain != "" {
		email = id + "@" + strings.TrimPrefix(strings.ToLower(domain), "@")
	}
	return email
}

func (set LDAPSettings) isAdmin(id string) bool {
	for _, a := range set.Admins {
		if strings.EqualFold(strings.TrimSpace(a), id) {
			return true
		}
	}
	return false
}
