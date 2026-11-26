package oauth

import (
	"net/http"
	"strings"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
)

// MCPMount 把四種 MCP 端點掛到 mux:
//
//	/mcp/agent          角色端點:角色憑證(Bearer <agentId>:<secret>)
//	/mcp/org/{org}      組織端點:omx_ 使用者 token 或(有 OAuth 時)綁這個網址的 OAuth access token(omo_)
//	/mcp                統一入口:omx_ 使用者 token 或(有 OAuth 時)綁 <public>/mcp 的 omo_
//	/mcp/worker/{org}   執行者端點:omx_ 使用者 token
//
// 雲端伺服器與別的宿主(本機 console 的對外 MCP 連接埠)共用這一份,兩邊的認證與權限因此一致;
// 宿主只決定:有沒有 OAuth、Backend 怎麼來、每個請求要不要多做事、哪些使用者不准從這裡進來
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
	// Reject 認證成功之後再擋掉的使用者(回 true = 當成 token 無效,401):宿主有「只能在本機使用」的身分時用(console 的本機使用者)。
	// 組織端點、統一入口、執行者端點都套用(角色端點的身分是角色,不是使用者);nil = 不擋
	Reject func(*model.Principal) bool
	// AgentSource 角色端點「驗證失敗次數」的來源鍵;nil = service.ClientIP。宿主在同一個 service 上另有別的角色端點時用它分開計數
	AgentSource func(*http.Request) string
	// AgentDone 角色端點每次工具呼叫處理完後呼叫(見 mcp.AgentEndpointConfig.Done);nil = 不做事
	AgentDone func(role mcp.Role, tool string, err error)
	// NewBackend 這次連線的 Backend;nil = backend.Local{Svc, P}(直接用 service)
	NewBackend func(*model.Principal) backend.Backend
	// HostTools 宿主的專有工具:只掛在統一入口 /mcp、只給 OAuth 連線,依分級開放(見 mcp.HostTool);nil = 沒有
	HostTools []mcp.HostTool
	// Tool 四種端點每次工具執行前呼叫,回傳的函式在該次處理完後呼叫(見 mcp.Hooks.Tool;宿主處理完重新整理畫面用);nil = 不做事
	Tool func(name string) (done func())
	// Begin 每個帶 id 的 MCP 請求開始時呼叫,回傳的函式在處理完後呼叫(見 mcp.Hooks.Begin);nil = 不做事
	Begin func() (end func())
}

// Mount 註冊四種端點;有 OAuth 時組織端點與統一入口再包上觀測 log(只記沒帶 token 或帶 OAuth token 的請求,見 log.go)
func (m *MCPMount) Mount(mux *http.ServeMux) {
	mux.Handle("/mcp/agent", mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: m.Svc, Begin: m.Begin, Tool: m.Tool, Done: m.AgentDone, Source: m.AgentSource}))
	mux.Handle("/mcp/org/{org}", m.observed(m.adminEndpoint()))
	mux.Handle("/mcp", m.observed(m.unifiedEndpoint()))
	mux.Handle("/mcp/worker/{org}", m.workerEndpoint())
}

func (m *MCPMount) observed(h http.Handler) http.Handler {
	if m.OAuth == nil {
		return h
	}
	return m.OAuth.ObserveMCP(h)
}

// principal 認證組織端點與統一入口的 Bearer token:omo_ 且有 OAuth 時交給 OAuth(resource 是這個端點的網址,綁定別的資源的 token 不收,而且身分是唯讀或受限寫入);
// 其他交給 Authenticate(omx_ 使用者 token;沒有 OAuth 的 omo_ 在那裡一律 403)
func (m *MCPMount) principal(tok, resource string) (*model.Principal, error) {
	if m.OAuth != nil && strings.HasPrefix(tok, model.OAuthTokenPrefix) {
		return m.accept(m.Svc.AuthenticateOAuth(tok, resource))
	}
	return m.accept(m.Svc.Authenticate(tok))
}

// accept 套用 Reject:被擋的使用者和無效的 token 回同一個錯誤(不透露差別)
func (m *MCPMount) accept(p *model.Principal, err error) (*model.Principal, error) {
	if err == nil && m.Reject != nil && m.Reject(p) {
		return nil, service.ErrOAuthInvalidToken
	}
	return p, err
}

// backendOf 認證成功之後建這次連線的 Backend
func (m *MCPMount) backendOf(p *model.Principal) backend.Backend {
	if m.NewBackend != nil {
		return m.NewBackend(p)
	}
	return &backend.Local{Svc: m.Svc, P: p}
}

// workerEndpoint 執行者端點 /mcp/worker/{org}:只收 omx_ 使用者 token(omo_ 在 Authenticate 一律 403)
func (m *MCPMount) workerEndpoint() http.Handler {
	return mcp.WorkerEndpoint(mcp.WorkerEndpointConfig{
		AgentEndpoint: m.AgentEndpoint,
		Begin:         m.Begin,
		Tool:          m.Tool,
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			p, err := m.accept(m.Svc.Authenticate(tok))
			if err != nil {
				return nil, "", err
			}
			return m.backendOf(p), r.PathValue("org"), nil
		},
	})
}

// adminEndpoint 組織端點 /mcp/org/{org}:OAuth access token 只收綁定「這個組織的 MCP 網址」的
func (m *MCPMount) adminEndpoint() http.Handler {
	return mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		AgentEndpoint: m.AgentEndpoint,
		Begin:         m.Begin,
		Tool:          m.Tool,
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
		HostTools:        m.HostTools,
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
