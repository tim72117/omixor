package auth

// session.go 登入之後的 session,兩種:
//   - 核心的 web session(有資料庫):核心的 omx_ token(裝置名稱 web:<帳號>,有效 sessionHours),
//     身分每個請求從核心使用者重讀。畫面的一般登入都是這種,core/api 也認它
//   - 這裡自己的 session(沒有資料庫,以及管理員修復登入):沒有核心使用者可以對,
//     存記憶體;有資料庫時另存 omixor_sessions(重開不用重新登入)

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"omixor-web/internal/core"

	"omixor-core/httperr"
	"omixor-core/service"
)

type session struct {
	user    User
	expires time.Time
}

// storedSession omixor_sessions 裡的內容
type storedSession struct {
	User
	RepairSession bool `json:"repairSession,omitempty"`
}

var errSessionStore = core.StatusError{Status: 500, Message: "登入暫時失敗,請稍後再試;若持續發生請聯絡管理員"}

func (s *Service) ttl() time.Duration { return time.Duration(s.Config().SessionHours) * time.Hour }

// CoreSessions 現在的登入是不是用核心的 web session(核心可用)
func (s *Service) CoreSessions() bool { return core.AgentService() != nil }

// IssueWebSession 替驗證通過、已經對上核心使用者的人發 web session,回傳 token(放 cookie,不給畫面)
func (s *Service) IssueWebSession(coreUserID, account string) (string, error) {
	svc := core.AgentService()
	if svc == nil {
		return "", errSessionStore
	}
	res, err := svc.IssueWebSession(coreUserID, account, s.ttl())
	if err != nil {
		httperr.LogMasked("POST /api/auth/web/login 發 web session", err)
		return "", errSessionStore
	}
	return res.Token, nil
}

// WebUser web session 的登入者。ok = false 而且 err 是 nil = 沒有登入(無效、過期、撤銷);err = 暫時查不出來。
// 停用的使用者照樣回傳(ok = true):之後的 core.Caller 檢查會擋下並說明原因
func (s *Service) WebUser(token string) (User, bool, error) {
	svc := core.AgentService()
	if svc == nil || token == "" {
		return User{}, false, nil
	}
	p, err := svc.WebSession(token)
	if p == nil {
		var se *service.Error
		if errors.As(err, &se) && se.Code == 401 {
			return User{}, false, nil
		}
		httperr.LogMasked("查 web session", err)
		return User{}, false, core.StatusError{Status: 503, Message: "暫時無法確認登入狀態,請稍後再試"}
	}
	key, ldap := strings.CutPrefix(p.User.EvoleapID, service.LDAPIDPrefix)
	if !ldap {
		return User{}, false, nil
	}
	u := User{ID: key, Name: p.User.Name, Email: p.User.Email}
	// 裝置名稱記著目錄給的大小寫(署名與畫面用)
	if label := strings.TrimPrefix(p.Token.Device, service.WebDevicePrefix); strings.EqualFold(label, key) {
		u.ID = label
	}
	if u.Name == "" {
		u.Name = u.ID
	}
	u.Admin = s.Config().IsAdminAccount(u.ID) // 管理員照目前的名單
	return u, true, nil
}

// EndWebSession 登出:撤銷 web session
func (s *Service) EndWebSession(token string) {
	if svc := core.AgentService(); svc != nil && token != "" {
		if err := svc.EndWebSession(token); err != nil {
			httperr.LogMasked("登出:撤銷 web session", err)
		}
	}
}

func revokeWebSessions() error {
	svc := core.AgentService()
	if svc == nil {
		return nil
	}
	_, err := svc.RevokeWebSessions()
	return err
}

// StartSession 建這裡自己的 session(見檔頭),回傳 token。repair = 管理員修復登入
func (s *Service) StartSession(u User, repair bool) (string, error) {
	u.Repair = repair
	tok := randomToken()
	exp := s.now().Add(s.ttl())
	s.mu.Lock()
	s.sessions[tok] = &session{user: u, expires: exp}
	s.mu.Unlock()
	if core.DB() != nil {
		if err := core.SessionPut(tok, storedSession{User: u, RepairSession: repair}, exp); err != nil {
			httperr.LogMasked("POST /api/auth/web/login 存 session", err) // 資料庫錯誤只進 log,不回給(未登入的)呼叫者
			s.mu.Lock()
			delete(s.sessions, tok) // 沒存起來的 session 不留在記憶體(否則這次回 500,token 卻其實可用)
			s.mu.Unlock()
			return "", errSessionStore
		}
	}
	return tok, nil
}

// User 由這裡自己的 session token 找登入的人(過期回 false)
func (s *Service) User(token string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.sessions[token]
	if !ok && core.DB() != nil && token != "" {
		var st storedSession
		if exp, found := core.SessionGet(token, &st); found {
			st.User.Repair = st.RepairSession
			ss = &session{user: st.User, expires: exp}
			s.sessions[token] = ss
			ok = true
		}
	}
	if !ok {
		return User{}, false
	}
	if s.now().After(ss.expires) {
		delete(s.sessions, token)
		if core.DB() != nil {
			go core.SessionDelete(token)
		}
		return User{}, false
	}
	u := ss.user
	u.Admin = s.cfg.IsAdminAccount(u.ID) // 管理員照目前的名單,不用登入當下的快照
	return u, true
}

// Logout 結束這裡自己的 session
func (s *Service) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
	if core.DB() != nil {
		core.SessionDelete(token)
	}
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
