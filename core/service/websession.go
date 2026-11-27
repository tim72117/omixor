package service

// websession.go 瀏覽器 session:就是一個 omixor token(omx_,存 omixor_tokens),裝置名稱以 web: 開頭、一定會到期,
// 由宿主放在 HttpOnly cookie 裡(console 的網頁登入;cookie 與 CSRF 檢查見 core/api/websession.go)。
// 身分驗證由宿主先做完(例如 VerifyDirectory + ResolveDirectoryUserWith),這裡只發、驗、撤銷

import (
	"errors"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// WebDevicePrefix web session token 的裝置名稱前綴;後面是宿主要記的帳號字串
const WebDevicePrefix = "web:"

var errWebSession = &Error{Code: 401, Msg: "登入無效、已過期或已撤銷,請重新登入"}

// IssueWebSession 替已經驗證過身分的使用者發 web session。label 記在裝置名稱裡(WebSession 回傳的 Token.Device 拿得到);
// ttl 一定要大於 0(瀏覽器 session 不能永不到期)
func (s *Service) IssueWebSession(userID, label string, ttl time.Duration) (*LoginResult, error) {
	if ttl <= 0 {
		return nil, fail(500, "web session 要有有效時間")
	}
	u, err := s.Store.UserByID(userID)
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		return nil, fail(403, "這個帳號已停用")
	}
	return s.issueTokenTTL(u, WebDevicePrefix+label, s.mt(), "web", ttl)
}

// WebSession 驗證 web session。不是 web session、無效、過期、撤銷回 401;查不出來(資料庫)回原本的錯誤,不當成沒登入。
// 使用者已停用時回 403 的錯誤,同時回傳 principal(讓宿主知道是誰;呼叫端不能拿它做任何操作)
func (s *Service) WebSession(raw string) (*model.Principal, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, TokenPrefix) {
		return nil, errWebSession
	}
	t, u, err := s.Store.TokenByHash(HashToken(raw), s.now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, errWebSession
	}
	if err != nil {
		return nil, err
	}
	if t == nil || u == nil || !strings.HasPrefix(t.Device, WebDevicePrefix) {
		return nil, errWebSession
	}
	p := &model.Principal{User: *u, Token: *t, Via: model.ViaLogin}
	if u.Disabled {
		return p, fail(403, "這個帳號已停用")
	}
	if t.LastUsedAt == nil || s.now().Sub(t.LastUsedAt.Time) > time.Minute {
		_ = s.Store.TouchToken(t.ID, s.now())
	}
	if p.Actor, err = s.userActorRef(u); err != nil {
		return nil, err
	}
	return p, nil
}

// EndWebSession 撤銷一個 web session(登出);無效的 token 不算錯誤
func (s *Service) EndWebSession(raw string) error {
	p, err := s.WebSession(raw)
	if p == nil {
		if errors.Is(err, errWebSession) {
			return nil
		}
		return err
	}
	if err := s.Store.RevokeToken(p.Token.ID, s.now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	s.Audit(p, "logout", "", map[string]any{"token": p.Token.ID, "via": "web"})
	return nil
}

// RevokeWebSessions 撤銷全部 web session(認證設定改了,所有人要重新登入)
func (s *Service) RevokeWebSessions() (int, error) {
	return s.Store.RevokeTokensByDevicePrefix(WebDevicePrefix, s.now())
}
