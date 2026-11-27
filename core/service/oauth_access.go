package service

// oauth_access.go 驗證 access token(一般 MCP 授權與執行者授權)與觀測用的狀態查詢

import (
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// ---------------- 驗證 access token ----------------

// ErrOAuthInvalidToken access token 無效、過期、撤銷、不是這個資源的(MCP 端點回 401 invalid_token)
var ErrOAuthInvalidToken = fail(401, "OAuth 的 access token 無效、已過期或不屬於這個端點")

// AuthenticateOAuth 驗證 access token(omo_…):要綁定 resource(這個端點的網址)、scope 含 mcp:read(或 mcp:write,隱含 read)、沒過期沒撤銷、帳號沒停用。
// resource 必須與簽發時綁定的字串完全相同:組織 token(/mcp/org/x)不能用在統一入口(/mcp),帳號層級 token(/mcp)也不能用在組織端點。
// token 的 Org 欄位不是授權依據(帳號層級的 Org 是空字串,不代表「所有組織」):組織存取一律由 MCP 端點以即時成員資格決定。
// 回傳的身分:scope 只有 mcp:read → ReadOnly(MCP 端點只開放唯讀工具);含 mcp:write → ReadOnly=false 但 Via=ViaOAuth
// (MCP 端點只開放 ReadOnly ∪ OAuthWriteTools,Principal.ServerAdmin 一律 false,service 層照一般成員檢查);
// 含 mcp:admin(要同時含 mcp:write,否則視為不認得的組合,只當 mcp:write 以下)→ OAuthAdmin=true,多開 OAuthAdminTools(每次呼叫即時檢查該組織的管理員身分)
func (s *Service) AuthenticateOAuth(raw, resource string) (*model.Principal, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, oauthPrefixAccess) || resource == "" {
		return nil, ErrOAuthInvalidToken
	}
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindAccess, oauthHash(model.OAuthKindAccess, raw))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrOAuthInvalidToken
		}
		return nil, err
	}
	now := s.now()
	if t.RevokedAt != nil || !now.Before(t.ExpiresAt) || t.Resource != resource || !oauthScopeValid(t.Scope) || hasScope(t.Scope, model.OAuthScopeWorker) {
		return nil, ErrOAuthInvalidToken
	}
	u, err := s.Store.UserByID(t.UserID)
	if err != nil {
		return nil, ErrOAuthInvalidToken
	}
	if u.Disabled {
		return nil, fail(403, "這個帳號已停用")
	}
	actor, err := s.userActorRef(u)
	if err != nil {
		return nil, err
	}
	return &model.Principal{User: *u, Token: model.Token{ID: t.ID, UserID: u.ID, Device: "OAuth", Email: u.Email, CreatedAt: model.Time{Time: t.CreatedAt}},
		Actor: actor, Via: model.ViaOAuth, ReadOnly: !hasScope(t.Scope, model.OAuthScopeWrite), OAuthAdmin: hasScope(t.Scope, model.OAuthScopeAdmin)}, nil
}

// AuthenticateOAuthWorker 驗證執行者端點的 access token:resource 要和簽發時的字串完全相同(/mcp/worker/{org}),scope 要是 mcp:worker。
// 一般 MCP 的授權(mcp:read / write / admin)在這裡無效,反過來也一樣(AuthenticateOAuth 不收 mcp:worker);REST 一律不收 omo_。
// 回傳的身分不是伺服器管理員,權限就是這位使用者在組織裡的成員權限(service 照授權表檢查);OAuthWorker 讓派發工具不回角色憑證密鑰
func (s *Service) AuthenticateOAuthWorker(raw, resource string) (*model.Principal, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, oauthPrefixAccess) || resource == "" {
		return nil, ErrOAuthInvalidToken
	}
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindAccess, oauthHash(model.OAuthKindAccess, raw))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrOAuthInvalidToken
		}
		return nil, err
	}
	if t.RevokedAt != nil || !s.now().Before(t.ExpiresAt) || t.Resource != resource || t.Scope != model.OAuthScopeWorker {
		return nil, ErrOAuthInvalidToken
	}
	u, err := s.Store.UserByID(t.UserID)
	if err != nil {
		return nil, ErrOAuthInvalidToken
	}
	if u.Disabled {
		return nil, fail(403, "這個帳號已停用")
	}
	actor, err := s.userActorRef(u)
	if err != nil {
		return nil, err
	}
	return &model.Principal{User: *u, Token: model.Token{ID: t.ID, UserID: u.ID, Device: "OAuth", Email: u.Email, CreatedAt: model.Time{Time: t.CreatedAt}},
		Actor: actor, Via: model.ViaOAuth, OAuthWorker: true}, nil
}

// OAuthAccessState 觀測用:access token 目前的狀態(valid / missing / invalid / expired / revoked / wrong_resource),不寫入、不回身分
func (s *Service) OAuthAccessState(raw, resource string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "missing"
	}
	if !strings.HasPrefix(raw, oauthPrefixAccess) {
		return "invalid"
	}
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindAccess, oauthHash(model.OAuthKindAccess, raw))
	if err != nil {
		return "invalid"
	}
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case !s.now().Before(t.ExpiresAt):
		return "expired"
	case t.Resource != resource:
		return "wrong_resource"
	case !oauthScopeValid(t.Scope):
		return "invalid"
	}
	return "valid"
}

// CleanOAuthClientName 對外用的 client_name 清理(同註冊時的規則)
func CleanOAuthClientName(v any) string {
	s, _ := v.(string)
	return cleanClientName(s)
}

func hasScope(scope, want string) bool {
	for _, x := range strings.Fields(scope) {
		if x == want {
			return true
		}
	}
	return false
}

// oauthScopeValid token 的 scope 認得:含 mcp:read 或 mcp:write(write 隱含 read)
func oauthScopeValid(scope string) bool {
	return hasScope(scope, model.OAuthScopeRead) || hasScope(scope, model.OAuthScopeWrite)
}

// OAuthAccessWrite 觀測用:access token 是不是有效且帶 mcp:write(不寫入、不回身分)
func (s *Service) OAuthAccessWrite(raw string) bool {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, oauthPrefixAccess) {
		return false
	}
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindAccess, oauthHash(model.OAuthKindAccess, raw))
	return err == nil && hasScope(t.Scope, model.OAuthScopeWrite)
}

// OAuthAccessAdmin 觀測用:access token 是不是有效且帶 mcp:admin(不寫入、不回身分)
func (s *Service) OAuthAccessAdmin(raw string) bool {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, oauthPrefixAccess) {
		return false
	}
	t, err := s.oauthStore().OAuthTokenByHash(model.OAuthKindAccess, oauthHash(model.OAuthKindAccess, raw))
	return err == nil && hasScope(t.Scope, model.OAuthScopeAdmin)
}
