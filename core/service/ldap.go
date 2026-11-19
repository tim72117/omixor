package service

import (
	"errors"
	"log"
	"strings"
	"time"

	"omixor-core/ldapauth"
	"omixor-core/model"
	"omixor-core/store"
)

// Directory 驗證帳號密碼的目錄服務(LDAP / Active Directory;實作見 ldapauth,伺服器啟動時由環境變數建立)
type Directory interface {
	Authenticate(username, password string) (*ldapauth.Identity, error)
}

// LDAPIDPrefix LDAP 身分存在使用者的 EvoleapID 欄位(原本只放 evoleap 的 id;欄位是唯一的文字,
// 加前綴就不會和 evoleap 的 id 撞,也不用改資料庫):ldap:<小寫帳號>
const LDAPIDPrefix = "ldap:"

// LoginWithLDAP 用 LDAP 帳號密碼證明身分,換 omixor 自己的 token(規則同 LoginWithEvoleap,差在身分來源)。
//   - 第一次通過 LDAP 的人自動建立成一般使用者(不屬於任何組織,要管理員用 email 加進組織)
//   - LDAPAdmins 名單上的帳號是伺服器管理員(每次登入都確認;從名單拿掉不會自動降級,要到使用者管理改)
//   - email 取目錄裡的 mail;沒有就用「帳號@LDAPEmailDomain」;兩個都沒有就不能建立(組織成員是用 email 加的)
func (s *Service) LoginWithLDAP(username, password, device, ip string) (*LoginResult, error) {
	if s.LDAP == nil {
		return nil, fail(404, "這台伺服器沒有啟用 LDAP 登入")
	}
	if !s.allowLogin(ip) {
		return nil, fail(429, "登入嘗試太頻繁,請一分鐘後再試")
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" || password == "" {
		return nil, fail(400, "要給帳號與密碼")
	}
	if wait := s.ldapBlockedFor(username); wait > 0 {
		s.Audit(nil, "login.denied", "", map[string]any{"via": "ldap", "username": username, "reason": "too-many-failures"})
		return nil, fail(429, "這個帳號連續輸錯太多次,請 %d 分鐘後再試", int(wait/time.Minute)+1)
	}
	id, err := s.LDAP.Authenticate(username, password)
	if err != nil {
		if errors.Is(err, ldapauth.ErrInvalidCredentials) {
			s.ldapFailed(username)
			s.Audit(nil, "login.denied", "", map[string]any{"via": "ldap", "username": username, "reason": "credentials"})
			return nil, fail(401, "帳號或密碼錯誤")
		}
		// 連線、服務帳號、BaseDN 這類設定問題:細節只留在伺服器紀錄,不給登入的人
		log.Printf("LDAP 驗證失敗(帳號 %s):%v", username, err)
		return nil, fail(502, "LDAP 驗證失敗,請聯絡管理員")
	}
	s.ldapCleared(username)
	key := LDAPIDPrefix + id.ID
	email := strings.ToLower(strings.TrimSpace(id.Email))
	if email == "" && s.LDAPEmailDomain != "" {
		email = id.ID + "@" + strings.TrimPrefix(strings.ToLower(s.LDAPEmailDomain), "@")
	}
	now := s.mt()

	u, err := s.Store.UserByEvoleapID(key)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if u == nil {
		if email == "" {
			s.Audit(nil, "login.denied", "", map[string]any{"via": "ldap", "username": id.ID, "reason": "no-email"})
			return nil, fail(403, "這個 LDAP 帳號(%s)沒有 email:目錄裡要有 mail 屬性,或請管理員設定 OMIXOR_LDAP_EMAIL_DOMAIN", id.ID)
		}
		if other, err := s.Store.UserByEmail(email); err == nil {
			if other.EvoleapID != "" {
				// 這個 email 已經是另一個身分(evoleap 或別的 LDAP 帳號)在用:不能接管
				s.Audit(nil, "login.denied", "", map[string]any{"via": "ldap", "username": id.ID, "reason": "email-in-use", "other": other.ID})
				return nil, fail(403, "這個 email(%s)已經是另一個帳號在用,請聯絡管理員", email)
			}
			// 管理員先用 email 加進名單、還沒對上身分的人。目錄的 mail 使用者自己改得動的話,
			// 這一步等於讓人用自己填的 email 接手別人預先加入的身分(連同組織權限),所以要管理員明確開 LDAPTrustMail
			if !s.LDAPTrustMail {
				s.Audit(nil, "login.denied", "", map[string]any{"via": "ldap", "username": id.ID, "reason": "email-pending", "other": other.ID})
				return nil, fail(403, "管理員已經用這個 email(%s)預先加入你,但這台伺服器不會自動把 LDAP 帳號對上預先加入的名單。請先讓管理員移除這筆,或請伺服器管理員開 OMIXOR_LDAP_TRUST_MAIL", email)
			}
			u = other
			u.EvoleapID = key
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	isAdmin := s.isLDAPAdmin(id.ID)
	if u == nil {
		role := "member"
		if isAdmin {
			role = "admin"
		}
		u = &model.User{ID: RandID("u_", 9), EvoleapID: key, Email: email, Role: role, CreatedAt: now}
		s.Audit(&model.Principal{User: *u}, "user.created", "", map[string]any{"via": "ldap", "username": id.ID, "role": role})
	}
	if u.Disabled {
		s.Audit(&model.Principal{User: *u}, "login.denied", "", map[string]any{"via": "ldap", "reason": "disabled"})
		return nil, fail(403, "這個帳號已停用")
	}
	if isAdmin && u.Role != "admin" {
		u.Role = "admin"
		s.Audit(&model.Principal{User: *u}, "ldap.admin", "", map[string]any{"username": id.ID})
	}
	if id.Name != "" {
		u.Name = id.Name
	}
	u.LastLoginAt = &now
	if err := s.Store.SaveUser(*u); err != nil {
		return nil, err
	}
	return s.issueToken(u, device, now, "ldap")
}

func (s *Service) isLDAPAdmin(id string) bool {
	for _, a := range s.LDAPAdmins {
		if strings.EqualFold(strings.TrimSpace(a), id) {
			return true
		}
	}
	return false
}

// ldapFail 一個帳號目前這一輪的輸錯次數
type ldapFail struct {
	n     int
	since time.Time
}

func (s *Service) ldapLimits() (int, time.Duration) {
	max, win := s.LDAPMaxFailures, s.LDAPFailWindow
	if max <= 0 {
		max = 5
	}
	if win <= 0 {
		win = 10 * time.Minute
	}
	return max, win
}

// ldapBlockedFor 這個帳號被擋的話,還要擋多久(0 = 沒被擋)。
// 擋在 omixor 這一層:AD 的鎖定是整個網域的,輸錯幾次就鎖。不擋的話,任何連得到登入網址的人都能一直送錯的密碼,
// 把別人的帳號鎖住(來源 IP 的頻率限制擋不住:X-Forwarded-For 的值可以自己填)。
// 不管帳號存不存在都一樣計算,所以不會洩漏哪些帳號存在
func (s *Service) ldapBlockedFor(user string) time.Duration {
	max, win := s.ldapLimits()
	s.ldapMu.Lock()
	defer s.ldapMu.Unlock()
	f, ok := s.ldapFails[user]
	if !ok {
		return 0
	}
	if left := win - s.now().Sub(f.since); left > 0 && f.n >= max {
		return left
	}
	return 0
}

// ldapFailed 記一次輸錯(上限內第一次輸錯開始算一輪;過了一輪就重新算)
func (s *Service) ldapFailed(user string) {
	_, win := s.ldapLimits()
	now := s.now()
	s.ldapMu.Lock()
	defer s.ldapMu.Unlock()
	if s.ldapFails == nil {
		s.ldapFails = map[string]ldapFail{}
	}
	if len(s.ldapFails) >= 1000 { // 清掉過期的,不讓亂編帳號把記憶體灌滿
		for k, v := range s.ldapFails {
			if now.Sub(v.since) >= win {
				delete(s.ldapFails, k)
			}
		}
	}
	f := s.ldapFails[user]
	if f.n == 0 || now.Sub(f.since) >= win {
		f = ldapFail{since: now}
	}
	f.n++
	s.ldapFails[user] = f
}

// ldapCleared 通過驗證:清掉這個帳號的輸錯次數
func (s *Service) ldapCleared(user string) {
	s.ldapMu.Lock()
	delete(s.ldapFails, user)
	s.ldapMu.Unlock()
}
