package model

import "time"

// OAuth(讓 Claude 自訂 connector 用 OAuth 2.1 連組織的 MCP 端點;預設唯讀,使用者明確選擇才可寫入受限白名單)用到的資料。
// 存放在獨立的資料表(omixor_oauth_clients、omixor_oauth_tokens),和使用者 token(omx_)完全分開。

// OAuth 一些固定值
const (
	// OAuthTokenPrefix OAuth access / refresh token 的前綴(使用者 token 是 omx_)
	OAuthTokenPrefix = "omo_"
	// OAuthScopeRead 唯讀 MCP 的 scope(預設授予的 scope)
	OAuthScopeRead = "mcp:read"
	// OAuthScopeWrite 受限寫入的 scope(隱含 mcp:read):只開放 OAuthWriteTools 那組協作工具、不是伺服器管理員(Principal.ServerAdmin 對它一律 false);
	// 客戶端要求、使用者在同意畫面明確選「讀寫」才會授予,永遠不會自動升級
	OAuthScopeWrite = "mcp:write"
	// OAuthScopeOffline 只用來宣告「可以拿 refresh token」;不會出現在授予的 scope 裡
	OAuthScopeOffline = "offline_access"
)

// OAuthToken 的 Kind
const (
	OAuthKindCSRF    = "csrf"    // 同意畫面的 CSRF token(只存雜湊、短效、用一次)
	OAuthKindCode    = "code"    // 授權碼(60 秒、用一次)
	OAuthKindAccess  = "access"  // access token(10 分鐘)
	OAuthKindRefresh = "refresh" // refresh token(30 天、每次換新、用過的不能再用)

	// OAuthKindEvoleapLogin 同意畫面「用 evoleap 登入並允許」的登入交易(10 分鐘、用一次)。資料表的 kind 檢查約束只允許上面四種,
	// 為了不動結構,這種列存成 csrf 種類,靠雜湊的命名空間(oauth:evoleap:<evoleap id>)和真正的 CSRF token 分開(欄位用法見 service/oauth_evoleap.go)
	OAuthKindEvoleapLogin = OAuthKindCSRF
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
