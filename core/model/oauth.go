package model

import "time"

// OAuth(讓 Claude 自訂 connector 用 OAuth 2.1 連組織的 MCP 端點;唯讀授權)用到的資料。
// 存放在獨立的資料表(omixor_oauth_clients、omixor_oauth_tokens),和使用者 token(omx_)完全分開。

// OAuth 一些固定值
const (
	// OAuthTokenPrefix OAuth access / refresh token 的前綴(使用者 token 是 omx_)
	OAuthTokenPrefix = "omo_"
	// OAuthScopeRead 唯讀 MCP 的 scope(目前唯一會授予的 scope)
	OAuthScopeRead = "mcp:read"
	// OAuthScopeOffline 只用來宣告「可以拿 refresh token」;不會出現在授予的 scope 裡
	OAuthScopeOffline = "offline_access"
)

// OAuthToken 的 Kind
const (
	OAuthKindCSRF    = "csrf"    // 同意畫面的 CSRF token(只存雜湊、短效、用一次)
	OAuthKindCode    = "code"    // 授權碼(60 秒、用一次)
	OAuthKindAccess  = "access"  // access token(10 分鐘)
	OAuthKindRefresh = "refresh" // refresh token(30 天、每次換新、用過的不能再用)
)

// OAuthClient 動態註冊(RFC 7591)的用戶端
type OAuthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	GrantTypes   []string
	CreatedAt    time.Time
}

// OAuthToken 一列 OAuth 授權碼、token 或 CSRF token(原文不存,只存雜湊)
type OAuthToken struct {
	ID            string
	Kind          string
	Hash          []byte
	ClientID      string
	UserID        string // csrf 沒有
	Org           string
	Scope         string
	Resource      string
	RedirectURI   string // code 用
	CodeChallenge string // code 用(PKCE S256)
	FamilyID      string // 同一次授權換出來的 code、access、refresh 共用;refresh 重用時整個家族撤銷
	CreatedAt     time.Time
	ExpiresAt     time.Time
	UsedAt        *time.Time // code、refresh、csrf:已經用過
	RevokedAt     *time.Time
}
