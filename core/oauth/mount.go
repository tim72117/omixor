package oauth

import (
	"net/http"
	"strings"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
)

// MCPMount 把 MCP 的人類端點(組織端點 /mcp/org/{org} 與統一入口 /mcp)掛到 mux,認證收 omx_ 使用者 token 與(有 OAuth 時)OAuth access token(omo_)。
// 雲端伺服器與別的宿主(例如本機 console)共用這一份,宿主只決定:有沒有 OAuth、Backend 怎麼來、每個請求要不要多做事
type MCPMount struct {
	Svc *service.Service
	// Public 對外網址(OMIXOR_PUBLIC_URL,不含結尾斜線;可為空);統一入口的 omixor/server 顯示用
	Public string
	// OAuth 授權伺服器;nil = 沒有 OAuth(沒設對外網址):omo_ token 一律拒絕,metadata 與觀測 log 都不掛
	OAuth *Handler
	// Cloud 這個伺服器是雲端部署(有 K_SERVICE),見 mcp.UnifiedEndpointConfig.Cloud
	Cloud bool
	// AgentEndpoint 角色端點的網址(領取任務時告訴呼叫端);空字串不附
	AgentEndpoint string
	// OAuthOnly 只收 OAuth access token(omo_):其他 token(omx_ 使用者 token…)一律當認證失敗(401,和沒帶 token 一樣,不透露 token 是否有效)。
	// 對外公開的連接埠(例如 console 的 OAuth 連接埠)用:不讓長效的使用者 token 走公開網路;預設 false(雲端伺服器兩種都收)
	OAuthOnly bool
	// NewBackend 這次連線的 Backend;nil = backend.Local{Svc, P}(直接用 service)
	NewBackend func(*model.Principal) backend.Backend
	// Tool 統一入口每次工具執行前呼叫,回傳的函式在該次處理完後呼叫(見 mcp.Hooks.Tool;宿主處理完重新整理畫面用);nil = 不做事
	Tool func(name string) (done func())
	// Begin 每個帶 id 的 MCP 請求開始時呼叫,回傳的函式在處理完後呼叫(見 mcp.Hooks.Begin);nil = 不做事
	Begin func() (end func())
}

// Mount 註冊 /mcp/org/{org} 與 /mcp;有 OAuth 時再包上觀測 log(只記沒帶 token 或帶 OAuth token 的請求,見 log.go)
func (m *MCPMount) Mount(mux *http.ServeMux) {
	mux.Handle("/mcp/org/{org}", m.observed(m.adminEndpoint()))
	m.MountUnified(mux)
}

// MountUnified 只掛統一入口 /mcp(宿主不開放組織端點 /mcp/org/{org} 時用,例如 console 的 OAuth 連接埠)
func (m *MCPMount) MountUnified(mux *http.ServeMux) {
	mux.Handle("/mcp", m.observed(m.unifiedEndpoint()))
}

func (m *MCPMount) observed(h http.Handler) http.Handler {
	if m.OAuth == nil {
		return h
	}
	return m.OAuth.ObserveMCP(h)
}

// principal 認證 Bearer token:omo_ 且有 OAuth 時交給 OAuth(resource 是這個端點的網址,綁定別的資源的 token 不收,而且身分是唯讀或受限寫入);
// 其他交給 Authenticate(omx_ 使用者 token;沒有 OAuth 的 omo_ 在那裡一律 403);OAuthOnly 時其他一律拒絕
func (m *MCPMount) principal(tok, resource string) (*model.Principal, error) {
	if m.OAuth != nil && strings.HasPrefix(tok, model.OAuthTokenPrefix) {
		return m.Svc.AuthenticateOAuth(tok, resource)
	}
	if m.OAuthOnly {
		return nil, service.ErrOAuthInvalidToken
	}
	return m.Svc.Authenticate(tok)
}

// backendOf 認證成功之後建這次連線的 Backend
func (m *MCPMount) backendOf(p *model.Principal) backend.Backend {
	if m.NewBackend != nil {
		return m.NewBackend(p)
	}
	return &backend.Local{Svc: m.Svc, P: p}
}

// adminEndpoint 組織端點 /mcp/org/{org}:OAuth access token 只收綁定「這個組織的 MCP 網址」的
func (m *MCPMount) adminEndpoint() http.Handler {
	return mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		AgentEndpoint: m.AgentEndpoint,
		Begin:         m.Begin,
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			org := r.PathValue("org")
			resource := ""
			if m.OAuth != nil {
				resource = m.OAuth.MCPURL(org)
			}
			p, err := m.principal(tok, resource)
			if err != nil {
				return nil, "", err
			}
			return m.backendOf(p), org, nil
		},
		ResourceMetadata: func(r *http.Request) string {
			if m.OAuth == nil || !model.ValidOrgID(r.PathValue("org")) {
				return ""
			}
			return m.OAuth.ResourceMetadataURL(r.PathValue("org"))
		},
	})
}

// unifiedEndpoint 統一入口 /mcp:單一連線,組織由每次呼叫的 org 參數指定(不做預設組織)。OAuth access token(omo_)是帳號層級,
// 只收綁定「統一入口網址」的;組織 token(/mcp/org/{org})不能用在這裡。沒有 OAuth 時 omo_ 回 403 說明
func (m *MCPMount) unifiedEndpoint() http.Handler {
	rm := ""
	if m.OAuth != nil {
		rm = m.OAuth.UnifiedResourceMetadataURL()
	}
	return mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{
		AgentEndpoint:    m.AgentEndpoint,
		PublicURL:        m.Public,
		Cloud:            m.Cloud,
		ResourceMetadata: rm,
		Begin:            m.Begin,
		Tool:             m.Tool,
		Resolve: func(r *http.Request) (backend.Backend, error) {
			tok, _ := mcp.BearerToken(r)
			resource := ""
			if m.OAuth != nil {
				resource = m.OAuth.UnifiedURL()
			} else if strings.HasPrefix(tok, model.OAuthTokenPrefix) {
				return nil, &service.Error{Code: 403, Msg: "OAuth is not enabled on this server (OMIXOR_PUBLIC_URL is not set); use an omx_ user token."}
			}
			p, err := m.principal(tok, resource)
			if err != nil {
				return nil, err
			}
			return m.backendOf(p), nil
		},
	})
}
