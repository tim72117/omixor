// Package service 是核心的商業邏輯與權限:授權、組織與成員、Issue。HTTP(api)與 omixor-web 內建模式都呼叫這一層。
//
// 權限:
//   - 伺服器管理員(users.role = admin):所有組織的所有操作,管使用者、token、操作紀錄,建立組織
//   - 組織管理員(org_members.role = admin):自己組織的 Issue 與成員、匯出匯入
//   - 組織成員:自己組織的 Issue;作者一律是自己(不能冒名)
//   - 不是成員:403
package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

const TokenPrefix = "omx_"

// Error 帶 HTTP 狀態碼的錯誤(api 照它回應;omixor-web 內建模式照訊息顯示)
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func fail(code int, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Code 錯誤的 HTTP 狀態碼(不是 *Error 時是 500)
func Code(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 500
}

type Service struct {
	Store     store.Store
	Evoleap   string        // evoleap 網址(證明身分用)
	Bootstrap string        // 第一位伺服器管理員的 email(使用者表是空的時才用)
	TTL       time.Duration // token 有效期(0 是不到期)
	Client    *http.Client
	Now       func() time.Time
	// AgentCredTTL 角色憑證有效時間(0 = 2 小時)
	AgentCredTTL time.Duration
	agents       agentState

	// 登入端點的頻率限制(每個來源每分鐘、整個服務每分鐘)
	LoginPerIP, LoginTotal int
	limMu                  sync.Mutex
	limWindow              time.Time
	limCount               map[string]int
	limAll                 int
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) mt() model.Time { return model.Time{Time: s.now().Truncate(time.Second)} }

func RandID(prefix string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func HashToken(tok string) []byte { h := sha256.Sum256([]byte(tok)); return h[:] }

func (s *Service) Audit(p *model.Principal, action, org string, detail map[string]any) {
	e := model.AuditEntry{At: s.mt(), Action: action, Org: org, Detail: detail}
	if p != nil {
		e.UserID, e.Email = p.User.ID, p.User.Email
	}
	_ = s.Store.AddAudit(e)
}

// ================= 授權 =================

func (s *Service) allowLogin(ip string) bool {
	s.limMu.Lock()
	defer s.limMu.Unlock()
	per, total := s.LoginPerIP, s.LoginTotal
	if per == 0 {
		per = 10
	}
	if total == 0 {
		total = 60
	}
	now := s.now()
	if now.Sub(s.limWindow) >= time.Minute || s.limCount == nil {
		s.limWindow, s.limCount, s.limAll = now, map[string]int{}, 0
	}
	if s.limCount[ip] >= per || s.limAll >= total {
		return false
	}
	s.limCount[ip]++
	s.limAll++
	return true
}

// ClientIP Cloud Run 前面有 Google 的前端:真正的來源是 X-Forwarded-For 的第一個
func ClientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

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
	if !s.allowLogin(ip) {
		return nil, fail(429, "登入嘗試太頻繁,請一分鐘後再試")
	}
	if strings.TrimSpace(evoToken) == "" {
		return nil, fail(400, "要給 evoleap 的 token")
	}
	device = strings.TrimSpace(device)
	if device == "" {
		device = "未命名裝置"
	}
	if r := []rune(device); len(r) > 80 {
		device = string(r[:80])
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
		return nil, fail(502, "連不到 evoleap 驗證身分:%v", err)
	}
	var me evoleapMe
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me)
	resp.Body.Close()
	if resp.StatusCode != 200 || err != nil {
		return nil, fail(502, "evoleap 驗證身分失敗(HTTP %d)", resp.StatusCode)
	}
	if me.User == nil || me.User.ID == "" {
		return nil, fail(401, "evoleap 的登入無效或已過期")
	}
	if !me.User.Google || me.User.Email == "" {
		return nil, fail(403, "evoleap 帳號要用 Google 登入(email 經過驗證)才能用 omixor")
	}
	email := strings.ToLower(strings.TrimSpace(me.User.Email))
	now := s.mt()

	u, err := s.Store.UserByEvoleapID(me.User.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
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
			return nil, err
		}
		if boot := strings.ToLower(strings.TrimSpace(s.Bootstrap)); n == 0 && boot != "" && email == boot {
			u = &model.User{ID: RandID("u_", 9), EvoleapID: me.User.ID, Email: email, Role: "admin", CreatedAt: now}
			s.Audit(&model.Principal{User: *u}, "bootstrap.admin", "", map[string]any{"evoleapId": me.User.ID})
		} else {
			s.Audit(nil, "login.denied", "", map[string]any{"email": email, "evoleapId": me.User.ID})
			return nil, fail(403, "這個帳號(%s)沒有 omixor 的權限,請 omixor 管理員加入", email)
		}
	}
	if u.Disabled {
		s.Audit(&model.Principal{User: *u}, "login.denied", "", map[string]any{"reason": "disabled"})
		return nil, fail(403, "這個帳號已停用")
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
	if err := s.Store.SaveUser(*u); err != nil {
		return nil, err
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
	s.Audit(&model.Principal{User: *u, Token: t}, "login", "", map[string]any{"token": t.ID, "device": device})
	return &LoginResult{Token: raw, TokenID: t.ID, ExpiresAt: t.ExpiresAt, User: *u}, nil
}

// Authenticate 驗證 omixor token
func (s *Service) Authenticate(raw string) (*model.Principal, error) {
	raw = strings.TrimSpace(raw)
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
	return &model.Principal{User: *u, Token: *t}, nil
}

func (s *Service) Logout(p *model.Principal) error {
	_ = s.Store.RevokeToken(p.Token.ID, s.now())
	s.Audit(p, "logout", "", map[string]any{"token": p.Token.ID})
	return nil
}

func needServerAdmin(p *model.Principal) error {
	if !p.ServerAdmin() {
		return fail(403, "要 omixor 伺服器管理員才能做")
	}
	return nil
}

func (s *Service) ListUsers(p *model.Principal) ([]model.User, error) {
	if err := needServerAdmin(p); err != nil {
		return nil, err
	}
	return s.Store.ListUsers()
}

// SaveUser 以 email 新增或修改使用者(伺服器管理員)
func (s *Service) SaveUser(p *model.Principal, email string, role *string, disabled *bool) (*model.User, error) {
	if err := needServerAdmin(p); err != nil {
		return nil, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return nil, fail(400, "要給 email")
	}
	if role != nil && !model.OneOf(*role, model.Roles) {
		return nil, fail(400, "role 必須是 admin 或 member")
	}
	u, err := s.Store.UserByEmail(email)
	created := false
	if errors.Is(err, store.ErrNotFound) {
		u, created = &model.User{ID: RandID("u_", 9), Email: email, Role: "member", CreatedAt: s.mt()}, true
	} else if err != nil {
		return nil, err
	}
	if role != nil {
		u.Role = *role
	}
	if disabled != nil {
		u.Disabled = *disabled
	}
	if u.ID == p.User.ID && (u.Role != "admin" || u.Disabled) {
		return nil, fail(400, "不能把自己降級或停用")
	}
	if err := s.Store.SaveUser(*u); err != nil {
		return nil, err
	}
	action := "user.update"
	if created {
		action = "user.add"
	}
	s.Audit(p, action, "", map[string]any{"email": email, "role": u.Role, "disabled": u.Disabled})
	return u, nil
}

func (s *Service) ListTokens(p *model.Principal) ([]model.Token, error) {
	if err := needServerAdmin(p); err != nil {
		return nil, err
	}
	return s.Store.ListTokens(s.now())
}

func (s *Service) RevokeToken(p *model.Principal, id string) error {
	if err := needServerAdmin(p); err != nil {
		return err
	}
	if err := s.Store.RevokeToken(id, s.now()); err != nil {
		return fail(404, "找不到 token %s", id)
	}
	s.Audit(p, "token.revoke", "", map[string]any{"token": id})
	return nil
}

func (s *Service) ListAudit(p *model.Principal, limit int) ([]model.AuditEntry, error) {
	if err := needServerAdmin(p); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	return s.Store.ListAudit(limit)
}

// ================= 組織與成員 =================

// orgRole 呼叫的人在這個組織的角色(伺服器管理員是 admin);組織不存在 404、不是成員 403
func (s *Service) orgRole(p *model.Principal, org string) (string, error) {
	if p.Agent != nil { // 角色身分預設拒絕;開放的操作用 viewer(見 agents.go)
		return "", fail(403, "角色身分不能做這件事")
	}
	if !model.ValidOrgID(org) {
		return "", fail(400, "組織 id 不對:%s", org)
	}
	if _, err := s.Store.GetOrg(org); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", fail(404, "找不到組織 %s", org)
		}
		return "", err
	}
	if p.ServerAdmin() {
		return "admin", nil
	}
	r, err := s.Store.MemberRole(org, p.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		return "", fail(403, "你不是組織 %s 的成員", org)
	}
	return r, err
}

func (s *Service) needOrgAdmin(p *model.Principal, org string) error {
	r, err := s.orgRole(p, org)
	if err != nil {
		return err
	}
	if r != "admin" {
		return fail(403, "要組織 %s 的管理員才能做", org)
	}
	return nil
}

// ListOrgs 我能用的組織(伺服器管理員是全部)
func (s *Service) ListOrgs(p *model.Principal) ([]model.Org, error) {
	if p.ServerAdmin() {
		list, err := s.Store.ListOrgs()
		for k := range list {
			list[k].MyRole = "admin"
		}
		return list, err
	}
	return s.Store.OrgsOfUser(p.User.ID)
}

// CreateOrg 建立組織(伺服器管理員)
func (s *Service) CreateOrg(p *model.Principal, id, name string) (*model.Org, error) {
	return s.CreateOrgWith(p, model.Org{ID: id, Name: name})
}

// CreateOrgWith 建立組織,含圖示、顏色、備註(伺服器管理員)
func (s *Service) CreateOrgWith(p *model.Principal, in model.Org) (*model.Org, error) {
	if err := needServerAdmin(p); err != nil {
		return nil, err
	}
	id, name := strings.TrimSpace(in.ID), in.Name
	if !model.ValidOrgID(id) || strings.HasPrefix(id, "deleted-orgs") {
		return nil, fail(400, "組織 id 只能用英數字、- 與 _")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = id
	}
	o := model.Org{ID: id, Name: name, Symbol: strings.TrimSpace(in.Symbol), Color: strings.TrimSpace(in.Color),
		Note: strings.TrimSpace(in.Note), CreatedAt: s.mt()}
	if err := s.Store.CreateOrg(o); err != nil {
		return nil, fail(409, "%v", err)
	}
	s.Audit(p, "org.create", id, map[string]any{"name": name})
	o.MyRole = "admin"
	return &o, nil
}

// UpdateOrg 更新組織的名稱、圖示、顏色、備註(伺服器管理員;id 不能改)
func (s *Service) UpdateOrg(p *model.Principal, o model.Org) error {
	if err := needServerAdmin(p); err != nil {
		return err
	}
	o.Name = strings.TrimSpace(o.Name)
	if o.Name == "" {
		return fail(400, "要給組織名稱")
	}
	o.Symbol, o.Color, o.Note = strings.TrimSpace(o.Symbol), strings.TrimSpace(o.Color), strings.TrimSpace(o.Note)
	if err := s.Store.UpdateOrg(o); err != nil {
		return fail(404, "找不到組織 %s", o.ID)
	}
	s.Audit(p, "org.update", o.ID, map[string]any{"name": o.Name})
	return nil
}

// DeleteOrg 刪除組織,連成員與 Issue 一起刪掉(伺服器管理員;不能復原)
func (s *Service) DeleteOrg(p *model.Principal, id string) error {
	if err := needServerAdmin(p); err != nil {
		return err
	}
	if err := s.Store.DeleteOrg(id); err != nil {
		return fail(404, "找不到組織 %s", id)
	}
	s.Audit(p, "org.delete", id, nil)
	return nil
}

func (s *Service) ListMembers(p *model.Principal, org string) ([]model.Member, error) {
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	return s.Store.ListMembers(org)
}

// SaveMember 把人加進組織或改角色(組織管理員);名單上還沒有這個 email 就先建一個待連結的使用者
func (s *Service) SaveMember(p *model.Principal, org, email, role string) (*model.Member, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if role == "" {
		role = "member"
	}
	if !model.OneOf(role, model.Roles) {
		return nil, fail(400, "role 必須是 admin 或 member")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return nil, fail(400, "要給 email")
	}
	u, err := s.Store.UserByEmail(email)
	if errors.Is(err, store.ErrNotFound) {
		u = &model.User{ID: RandID("u_", 9), Email: email, Role: "member", CreatedAt: s.mt()}
		if err := s.Store.SaveUser(*u); err != nil {
			return nil, err
		}
		s.Audit(p, "user.add", org, map[string]any{"email": email})
	} else if err != nil {
		return nil, err
	}
	m := model.Member{Org: org, UserID: u.ID, Email: u.Email, Name: u.Name, Role: role, AddedAt: s.mt()}
	if err := s.Store.SaveMember(m); err != nil {
		return nil, err
	}
	s.Audit(p, "member.save", org, map[string]any{"email": email, "role": role})
	return &m, nil
}

func (s *Service) RemoveMember(p *model.Principal, org, email string) error {
	if err := s.needOrgAdmin(p, org); err != nil {
		return err
	}
	u, err := s.Store.UserByEmail(strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return fail(404, "找不到 %s", email)
	}
	if err := s.Store.RemoveMember(org, u.ID); err != nil {
		return fail(404, "%s 不是組織 %s 的成員", email, org)
	}
	s.Audit(p, "member.remove", org, map[string]any{"email": u.Email})
	return nil
}

// EnsureOrgsForIssues 啟動時:已經有 Issue 但還沒有組織紀錄的(從前沒有組織表時存的),補建組織
func (s *Service) EnsureOrgsForIssues() error {
	orgs, err := s.Store.IssueOrgs()
	if err != nil {
		return err
	}
	for _, id := range orgs {
		if !model.ValidOrgID(id) {
			continue
		}
		if _, err := s.Store.GetOrg(id); errors.Is(err, store.ErrNotFound) {
			name := id
			if id == "default" {
				name = "evoleap"
			}
			if err := s.Store.CreateOrg(model.Org{ID: id, Name: name, CreatedAt: s.mt()}); err != nil {
				return err
			}
		}
	}
	return nil
}
