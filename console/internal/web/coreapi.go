package web

// coreapi.go 畫面的連接埠同時提供和 omixor server 同一套的 REST API(core/api)。API 契約:
//
//	掛上的路徑:/api/orgs…、/api/admin/…、/api/system/…、/api/modules(含選用模組的路由)、GET /api/auth/me、POST /api/auth/logout
//	不掛:登入與配對(/api/auth/evoleap|ldap|gitlab|pair)、/api/version、舊路徑 /api/issues/{org}(同路徑仍是 console 自己的,見 issues_api.go)
//	認證:web session(cookie omx_session)或 Authorization: Bearer omx_…;角色憑證不收(角色用 /mcp/agent)
//	      cookie 與免登入的寫入要 X-Omixor-CSRF: 1(見 core/api/websession.go);guard 的 Host / Origin / JSON 規則照樣套用
//	啟用 LDAP 時:只有 LDAP 帳號的使用者(本機使用者 omixor-console 不行),而且和畫面登入同一套限制(沒停用、是某個組織成員或管理員);
//	      能做什麼照核心的權限。管理員帳號名單只在 web session 上升成伺服器管理員(Bearer 的 token 不升)。
//	      不在名單上的人,寫入只開放 coreMemberWrites 列的日常操作(預設拒絕),/api/admin/ 與結構檢核整個不開放
//	沒啟用登入:本機使用者,只接受本機名稱的 Host;不收 Bearer
//	任何人都不能:建立、修改、刪除組織(console 的組織清單要經過畫面)、改使用者角色或停用(POST /api/admin/users;管理員只看帳號名單)
//	沒有資料庫:404

import (
	"net/http"
	"strings"
	"sync"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"

	"omixor-core/api"
	"omixor-core/model"
	"omixor-core/service"
)

// isCorePath 交給 core/api 的路徑(其餘照舊由 console 處理)
func isCorePath(p string) bool {
	switch p {
	case "/api/orgs", "/api/modules", "/api/auth/me", "/api/auth/logout":
		return true
	}
	return strings.HasPrefix(p, "/api/orgs/") || strings.HasPrefix(p, "/api/admin/") || strings.HasPrefix(p, "/api/system/")
}

// coreBlocked console 上誰都不能用的路由
var coreBlocked = nameSet(
	"POST /api/orgs", "PUT /api/orgs/{org}", "POST /api/orgs/{org}/delete",
	"POST /api/admin/users",
)

// coreMemberWrites 不在管理員帳號名單上的人可以用的寫入(對應 access.go 的 memberActions、memberTools)。
// 沒列的寫入只有管理員能用;發角色憑證的(領取任務、子項、委派)也只給管理員
var coreMemberWrites = nameSet(
	"POST /api/auth/logout",
	// Issue、附件、commit
	"POST /api/orgs/{org}/issues", "POST /api/orgs/{org}/issues/{id}", "POST /api/orgs/{org}/issues/{id}/comments",
	"POST /api/orgs/{org}/issues/{id}/accept", "POST /api/orgs/{org}/issues/{id}/release", "POST /api/orgs/{org}/issues/{id}/delete",
	"POST /api/orgs/{org}/attachments",
	"POST /api/orgs/{org}/commits", "POST /api/orgs/{org}/issues/{id}/commits", "DELETE /api/orgs/{org}/issues/{id}/commits",
	// 子項與待決定
	"POST /api/orgs/{org}/issues/{id}/items", "PATCH /api/orgs/{org}/issues/{id}/items/{itemId}",
	"POST /api/orgs/{org}/issues/{id}/items/{itemId}/release", "POST /api/orgs/{org}/issues/{id}/items/{itemId}/report",
	"POST /api/orgs/{org}/issues/{id}/items/{itemId}/block", "POST /api/orgs/{org}/issues/{id}/items/{itemId}/unblock",
	"POST /api/orgs/{org}/issues/{id}/items/{itemId}/cancel", "POST /api/orgs/{org}/issues/{id}/items/{itemId}/promote",
	"POST /api/orgs/{org}/decisions", "POST /api/orgs/{org}/decisions/{id}/answer", "POST /api/orgs/{org}/decisions/{id}/withdraw",
	"POST /api/orgs/{org}/decisions/{id}/reopen", "POST /api/orgs/{org}/decisions/{id}/to-issue",
	// 文件
	"POST /api/orgs/{org}/docs", "POST /api/orgs/{org}/docs/{id}", "POST /api/orgs/{org}/docs/{id}/read",
	"POST /api/orgs/{org}/docs/{id}/edit", "POST /api/orgs/{org}/docs/{id}/delete",
	// 頻道、事件
	"POST /api/orgs/{org}/channels", "POST /api/orgs/{org}/channels/{id}", "POST /api/orgs/{org}/channels/{id}/members",
	"POST /api/orgs/{org}/channels/{id}/messages", "POST /api/orgs/{org}/channels/{id}/read",
	"POST /api/orgs/{org}/incidents", "POST /api/orgs/{org}/incidents/{id}",
	// 派任務與回報、MCP 工具權限查詢
	"POST /api/orgs/{org}/agent-tasks", "POST /api/orgs/{org}/agent-tasks/{id}/report", "POST /api/orgs/{org}/mcp-tools/resolve",
)

var (
	errCoreBlocked = core.StatusError{Status: 403, Message: "console 不開放用 API 做這個操作,請在畫面上操作", Data: map[string]any{"forbidden": true}}
	errCoreAdmin   = core.StatusError{Status: 403, Message: "只有管理員帳號名單上的人可以用這個 API", Data: map[string]any{"forbidden": true}}
	errCoreToken   = core.StatusError{Status: 403, Message: "這個身分不能用這台 console 的 API;請用 LDAP 帳號登入", Data: map[string]any{"forbidden": true}}
	errCoreOff     = core.StatusError{Status: 401, Message: "沒啟用登入時,API 只接受從本機呼叫"}
)

// admitCore 驗證通過後 console 的限制(見檔頭)
func admitCore(au *auth.Service, r *http.Request, p *model.Principal, cookie bool) (*model.Principal, error) {
	if coreBlocked[r.Pattern] {
		return nil, errCoreBlocked
	}
	if au == nil || !au.Enabled() {
		if p.Via != model.ViaLocal { // 只認本機身分:Bearer、cookie 在沒啟用登入時沒有意義
			return nil, errCoreOff
		}
		return p, nil
	}
	account, ldap := strings.CutPrefix(p.User.EvoleapID, service.LDAPIDPrefix)
	if !ldap || core.IsLocalUser(p.User) {
		return nil, errCoreToken
	}
	set, ok := au.LDAPSettings()
	if !ok {
		return nil, errCoreToken
	}
	admin := cookie && au.Config().IsAdminAccount(account)
	who := core.NewCaller(account, p.User.Name, p.User.Email, admin, set)
	if _, err := who.Access(); err != nil {
		return nil, err
	}
	if !admin {
		read := r.Method == http.MethodGet || r.Method == http.MethodHead
		if strings.HasPrefix(r.URL.Path, "/api/admin/") || r.URL.Path == "/api/system/schema-check" || (!read && !coreMemberWrites[r.Pattern]) {
			return nil, errCoreAdmin
		}
	}
	out, err := who.Principal()
	if err != nil {
		return nil, err
	}
	out.Token = p.Token // 登出要撤銷這一個
	return out, nil
}

// newCoreAPI console 用的 core/api handler
func newCoreAPI(au *auth.Service, svc *service.Service) http.Handler {
	a := api.New(svc)
	a.Session = &api.Session{
		Cookie:             webCookie,
		NoLoginRoutes:      true,
		NoAgentCredentials: true,
		Local: func(r *http.Request) *model.Principal {
			if au != nil && au.Enabled() {
				return nil
			}
			switch strings.ToLower(hostOnly(r.Host)) {
			case "127.0.0.1", "localhost", "::1":
				return core.LocalAPIPrincipal()
			}
			return nil
		},
		Admit: func(r *http.Request, p *model.Principal, cookie bool) (*model.Principal, error) {
			return admitCore(au, r, p, cookie)
		},
	}
	return a.Handler()
}

// withCoreAPI core 的路徑交給 core/api,其餘給 next。資料庫連線池換掉時核心會重建,所以照 service 指標快取
func withCoreAPI(app *core.App, au *auth.Service, next http.Handler) http.Handler {
	var mu sync.Mutex
	var cur *service.Service
	var h http.Handler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isCorePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		svc := core.AgentService() // 沒有資料庫是 nil
		if svc == nil {
			writeJSON(w, 404, map[string]any{"error": "這台 console 目前沒有 REST API(要連 PostgreSQL)"})
			return
		}
		mu.Lock()
		if svc != cur {
			cur, h = svc, newCoreAPI(au, svc)
		}
		use := h
		mu.Unlock()
		rec := &statusRecorder{ResponseWriter: w}
		if r.URL.Path == "/api/auth/logout" {
			// 瀏覽器的 cookie 一起清掉,但只在 session 真的撤銷了(或本來就無效)時:被 CSRF 檢查等擋下的請求
			// 不能清 cookie(不然跨站請求能把人登出,而伺服器上的 session 還在)
			rec.onStatus = func(code int) {
				if code < 300 || code == http.StatusUnauthorized {
					expireCookies(w)
				}
			}
		}
		use.ServeHTTP(rec, r)
		// 寫入後畫面的快取要重讀,開著的畫面要收到通知
		if r.Method != http.MethodGet && r.Method != http.MethodHead && rec.status < 300 && r.URL.Path != "/api/auth/logout" {
			app.RoleCallFinished()
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	// onStatus 第一次決定狀態碼、標頭送出前呼叫(可以是 nil)
	onStatus func(code int)
}

func (s *statusRecorder) record(code int) {
	if s.status != 0 {
		return
	}
	s.status = code
	if s.onStatus != nil {
		s.onStatus(code)
	}
}

func (s *statusRecorder) WriteHeader(code int) {
	s.record(code)
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	s.record(200)
	return s.ResponseWriter.Write(p)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
