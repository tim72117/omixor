package service

// auth.go 登入(evoleap;各種登入的共同尾段 issueToken)、omixor token 驗證與登出。
// LDAP 在 ldap.go、GitLab 在 gitlab.go / gitlab_login.go、手機配對在 pair.go

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// ================= 授權 =================

// LoginResult 登入結果(Token 是原文,只在這時候給一次)
type LoginResult struct {
	Token     string      `json:"token"`
	TokenID   string      `json:"tokenId"`
	ExpiresAt *model.Time `json:"expiresAt"`
	User      model.User  `json:"user"`
}

type evoleapMe struct {
	User *struct {
		ID     string `json:"id"`
		Email  string `json:"email"`
		Name   string `json:"name"`
		Google bool   `json:"google"`
	} `json:"user"`
}

// LoginWithEvoleap 用 evoleap 的 token 證明身分,換 omixor 自己的 token。能不能用由 omixor 的使用者名單決定
func (s *Service) LoginWithEvoleap(evoToken, device, ip string) (*LoginResult, error) {
	if !s.allowLogin(&s.logins.evoleap, ip) {
		return nil, fail(429, "登入嘗試太頻繁,請一分鐘後再試")
	}
	u, now, err := s.resolveEvoleapUser(evoToken)
	if err != nil {
		if err == errEvoleapDisabled {
			return nil, fail(403, "這個帳號已停用")
		}
		return nil, err
	}
	return s.issueToken(u, device, now, "")
}

// errEvoleapDisabled resolveEvoleapUser:evoleap 身分對上的 omixor 帳號已停用(回傳的 user 仍有值)
var errEvoleapDisabled = &Error{Code: 403, Msg: "這個帳號已停用"}

// resolveEvoleapUser 用 evoleap 的 token 對上 omixor 的使用者(登入與 OAuth 同意畫面共用,規則完全相同):
// 向 evoleap 查 /api/me;要是 Google 登入(email 經過驗證);依 evoleap id 找使用者,找不到就用 email 對上管理員預先加入、還沒對上 evoleap 的人,
// 都沒有時只有「使用者表是空的、email 等於 bootstrap email」才建立第一位伺服器管理員,其餘一律 403(不會悄悄建立新使用者);
// 停用的帳號回 errEvoleapDisabled(user 仍回傳);成功會更新 email、名字、最後登入時間。不發任何 omixor token、不做頻率限制(由呼叫者負責)
func (s *Service) resolveEvoleapUser(evoToken string) (*model.User, model.Time, error) {
	if strings.TrimSpace(evoToken) == "" {
		return nil, model.Time{}, fail(400, "要給 evoleap 的 token")
	}
	req, _ := http.NewRequest("GET", strings.TrimRight(s.Evoleap, "/")+"/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(evoToken))
	req.Header.Set("Accept", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, model.Time{}, fail(502, "連不到 evoleap 驗證身分:%v", err)
	}
	var me evoleapMe
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me)
	resp.Body.Close()
	if resp.StatusCode != 200 || err != nil {
		return nil, model.Time{}, fail(502, "evoleap 驗證身分失敗(HTTP %d)", resp.StatusCode)
	}
	if me.User == nil || me.User.ID == "" {
		return nil, model.Time{}, fail(401, "evoleap 的登入無效或已過期")
	}
	if !me.User.Google || me.User.Email == "" {
		return nil, model.Time{}, fail(403, "evoleap 帳號要用 Google 登入(email 經過驗證)才能用 omixor")
	}
	email := strings.ToLower(strings.TrimSpace(me.User.Email))
	now := s.mt()

	u, err := s.Store.UserByEvoleapID(me.User.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, now, err
	}
	if u == nil {
		// 管理員先用 email 加進名單、還沒對上 evoleap 帳號的人
		if pend, err := s.Store.UserByEmail(email); err == nil && pend.EvoleapID == "" {
			u = pend
			u.EvoleapID = me.User.ID
		}
	}
	if u == nil {
		n, err := s.Store.CountUsers()
		if err != nil {
			return nil, now, err
		}
		if boot := strings.ToLower(strings.TrimSpace(s.Bootstrap)); n == 0 && boot != "" && email == boot {
			u = &model.User{ID: RandID("u_", 9), EvoleapID: me.User.ID, Email: email, Role: "admin", CreatedAt: now}
			s.Audit(&model.Principal{User: *u}, "bootstrap.admin", "", map[string]any{"evoleapId": me.User.ID})
		} else {
			s.Audit(nil, "login.denied", "", map[string]any{"email": email, "evoleapId": me.User.ID})
			return nil, now, fail(403, "這個帳號(%s)沒有 omixor 的權限,請 omixor 管理員加入", email)
		}
	}
	if u.Disabled {
		s.Audit(&model.Principal{User: *u}, "login.denied", "", map[string]any{"reason": "disabled"})
		return u, now, errEvoleapDisabled
	}
	// evoleap 上改了 email,新的已經被名單上另一個人用了:保留原本的(避免 email 重複,登入失敗)
	if email != u.Email && u.Email != "" {
		if other, err := s.Store.UserByEmail(email); err == nil && other.ID != u.ID {
			s.Audit(&model.Principal{User: *u}, "email.conflict", "", map[string]any{"evoleapEmail": email, "other": other.ID})
			email = u.Email
		}
	}
	u.Email, u.LastLoginAt = email, &now
	if me.User.Name != "" {
		u.Name = me.User.Name
	}
	if err := s.saveUser(u); err != nil {
		return nil, now, err
	}
	return u, now, nil
}

// issueToken 替通過驗證的使用者發一個 omixor token(登入的共同尾段;via 是身分來源,只記在操作紀錄,"" 不記)
func (s *Service) issueToken(u *model.User, device string, now model.Time, via string) (*LoginResult, error) {
	device = strings.TrimSpace(device)
	if device == "" {
		device = "未命名裝置"
	}
	if r := []rune(device); len(r) > 80 {
		device = string(r[:80])
	}
	raw := RandID(TokenPrefix, 32)
	t := model.Token{ID: RandID("t_", 9), UserID: u.ID, Device: device, CreatedAt: now, Hash: HashToken(raw)}
	if s.TTL > 0 {
		exp := model.Time{Time: now.Add(s.TTL)}
		t.ExpiresAt = &exp
	}
	if err := s.Store.CreateToken(t); err != nil {
		return nil, err
	}
	detail := map[string]any{"token": t.ID, "device": device}
	if via != "" {
		detail["via"] = via
	}
	s.Audit(&model.Principal{User: *u, Token: t}, "login", "", detail)
	return &LoginResult{Token: raw, TokenID: t.ID, ExpiresAt: t.ExpiresAt, User: *u}, nil
}

// Authenticate 驗證 omixor token
func (s *Service) Authenticate(raw string) (*model.Principal, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, model.OAuthTokenPrefix) { // OAuth 的 access token 是唯讀授權,只能連組織的 MCP 端點(AuthenticateOAuth);REST 一律拒絕
		return nil, fail(403, "OAuth 連線是唯讀授權,只能用在 MCP,不能使用 REST API")
	}
	if !strings.HasPrefix(raw, TokenPrefix) {
		return nil, fail(401, "要帶 omixor 的登入(Authorization: Bearer omx_…);用 omixor login 或在 app 登入")
	}
	t, u, err := s.Store.TokenByHash(HashToken(raw), s.now())
	if err != nil || t == nil || u == nil {
		return nil, fail(401, "omixor 的登入無效、已過期或已撤銷,請重新登入")
	}
	if u.Disabled {
		return nil, fail(403, "這個帳號已停用")
	}
	if t.LastUsedAt == nil || s.now().Sub(t.LastUsedAt.Time) > time.Minute {
		_ = s.Store.TouchToken(t.ID, s.now())
	}
	actor, err := s.userActorRef(u)
	if err != nil {
		return nil, err
	}
	return &model.Principal{User: *u, Token: *t, Actor: actor, Via: model.ViaLogin}, nil
}

func (s *Service) Logout(p *model.Principal) error {
	_ = s.Store.RevokeToken(p.Token.ID, s.now())
	s.Audit(p, "logout", "", map[string]any{"token": p.Token.ID})
	return nil
}
