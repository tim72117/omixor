package server

// webauth.go 瀏覽器畫面(webui.go)的登入與 cookie 認證;只在畫面啟用時掛上。
//
//	GET  /api/auth/web/status             {mode: "server", loggedIn, user?, methods, capabilities, denied?, unavailable?}(不用登入)
//	GET  /api/auth/web/evoleap/start      導去 evoleap 核准頁(設一次性綁定 cookie)
//	GET  /api/auth/web/evoleap/callback   核准後回來:發 web session(HttpOnly cookie),導回 /console/;失敗導回 /console/?login_error=<代碼>
//	POST /api/auth/web/logout             撤銷 session、清 cookie(要 X-Omixor-CSRF: 1 與同源)
//	GET  /api/web/orgs/{org}/attachments/{id}   附件原始圖片(<img> 用;只收 cookie,權限同核心的附件)
//
// cookie 認證(core/api 的 Session)的限制,比使用者自己的 omx_ token 窄(畫面一旦有 XSS 時的上限):
//   - 只開放 webRoutes 列的路由(預設拒絕):不能產生配對碼(換長期 token)、不能用 /api/admin/*、建立或改組織與成員、改系統功能開關
//   - 伺服器管理員在瀏覽器裡當一般成員:只看得到自己是成員的組織,沒有跨組織權限
//   - 子項領取的回應拿掉角色憑證
//   - web session 的 token 當 Bearer 用一律拒絕,畫面沒啟用時也一樣(core/api 的 RejectWebBearer;MCP 端點本來就拒收)
//   - https 時兩個 cookie 都加 __Host- 前綴(Path=/、Secure、不帶 Domain,子網域塞不進來);本機 http 開發沒有前綴
// 組織隔離與受限專案由核心 service 層判斷,這裡不另外過濾。

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"omixor-core/api"
	"omixor-core/httperr"
	"omixor-core/model"
	"omixor-core/service"
)

const (
	webSessionCookie = "omx_session"
	webLoginCookie   = "omx_web_login"
	webSessionTTL    = 12 * time.Hour
	webLoginTTL      = 10 * time.Minute
)

// webRoutes 瀏覽器 session 可以用的核心路由(core/api 比對到的樣式)。畫面要用新的路由時才加;加之前確認不會發出憑證或做伺服器管理
var webRoutes = func() map[string]bool {
	const org = "/api/orgs/{org}"
	const issue = org + "/issues/{id}"
	const item = issue + "/items/{itemId}"
	const dec = org + "/decisions"
	m := map[string]bool{}
	for _, p := range []string{
		"GET /api/auth/me", "GET /api/orgs",
		"GET " + org + "/actors", "GET " + org + "/system", "GET " + org + "/projects", "GET " + org + "/agents",
		// Issue 與看板
		"GET " + org + "/issues", "GET " + org + "/issues/board", "POST " + org + "/issues", "GET " + issue, "POST " + issue,
		"POST " + issue + "/comments", "POST " + issue + "/accept", "POST " + issue + "/release",
		"POST " + issue + "/delete", "POST " + issue + "/restore", "POST " + issue + "/purge",
		"POST " + org + "/attachments",
		// 子項
		"GET " + issue + "/items", "POST " + item + "/claim", "POST " + item + "/report", "POST " + item + "/cancel",
		// 待決定
		"GET " + dec, "POST " + dec, "POST " + dec + "/{id}/answer", "POST " + dec + "/{id}/withdraw",
		"POST " + dec + "/{id}/reopen", "POST " + dec + "/{id}/to-issue",
	} {
		m[p] = true
	}
	return m
}()

// webCapabilities 這台伺服器有後端的畫面(畫面照這個隱藏其他頁面與動作;組織的功能開關照舊另外生效)
var webCapabilities = map[string]any{
	"sections": []string{"development"},
	"devTabs":  []string{"board", "issues", "decisions"},
}

var errWebRoute = &service.Error{Code: 403, Msg: "網頁版的登入不能做這個操作"}

// browserPrincipal 瀏覽器裡的身分:伺服器管理員也當一般成員
func browserPrincipal(p *model.Principal) *model.Principal {
	q := *p
	q.User.Role = "member"
	return &q
}

// coreSession core/api 的 cookie 認證設定(規則見檔頭)
func coreSession(cookieName string) *api.Session {
	return &api.Session{
		Cookie: cookieName,
		Admit: func(r *http.Request, p *model.Principal, cookie bool) (*model.Principal, error) {
			if !cookie {
				return p, nil
			}
			if !webRoutes[r.Pattern] {
				return nil, errWebRoute
			}
			return browserPrincipal(p), nil
		},
	}
}

// guardCookieResponses 包住核心 API:cookie 認證的回應不快取;子項領取的回應拿掉角色憑證
func guardCookieResponses(cookieName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(cookieName); err != nil || r.Header.Get("Authorization") != "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/items/") || !strings.HasSuffix(r.URL.Path, "/claim") {
			next.ServeHTTP(w, r)
			return
		}
		buf := &bufferedResponse{header: w.Header(), code: http.StatusOK}
		next.ServeHTTP(buf, r)
		body := buf.body.Bytes()
		if buf.code < 300 {
			var out struct {
				Result map[string]json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(body, &out); err != nil || out.Result == nil { // 讀不懂就整份不給(不能漏出憑證)
				writeWebJSON(w, 500, map[string]any{"error": "伺服器錯誤"})
				return
			}
			delete(out.Result, "credential")
			body, _ = json.Marshal(out)
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(buf.code)
		_, _ = w.Write(body)
	})
}

type bufferedResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) WriteHeader(code int)        { b.code = code }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }

func writeWebJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// cookie https 時用 __Host- 前綴(規定要 Secure、Path=/、不帶 Domain);http(本機開發)瀏覽器不收這種前綴,用原名與窄路徑
func (u *webUI) cookie(name, value, path string, maxAge int, same http.SameSite) *http.Cookie {
	if u.https() {
		path = "/"
	}
	return &http.Cookie{Name: u.cookieName(name), Value: value, Path: path, MaxAge: maxAge, HttpOnly: true, SameSite: same, Secure: u.https()}
}

func (u *webUI) cookieName(name string) string {
	if u.https() {
		return "__Host-" + name
	}
	return name
}

func (u *webUI) sessionName() string { return u.cookieName(webSessionCookie) }

func (u *webUI) sessionCookie(value string, maxAge int) *http.Cookie {
	return u.cookie(webSessionCookie, value, "/api", maxAge, http.SameSiteStrictMode)
}

// loginCookie 要 Lax:evoleap 核准後導回來是跨站的頂層 GET
func (u *webUI) loginCookie(value string, maxAge int) *http.Cookie {
	return u.cookie(webLoginCookie, value, "/api/auth/web", maxAge, http.SameSiteLaxMode)
}

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// sameOriginWrite 同 core/api 的 CSRF 規則:要帶 CSRF 標頭,有 Origin 時主機要和 Host 相同
func sameOriginWrite(r *http.Request) bool {
	if r.Header.Get(api.CSRFHeader) != "1" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	p, err := url.Parse(o)
	return err == nil && strings.EqualFold(p.Host, r.Host)
}

func loginErrorCode(err error) string {
	var se *service.Error
	if errors.As(err, &se) {
		switch se.Code {
		case 403:
			return "denied"
		case 429:
			return "throttled"
		case 400:
			return "expired"
		}
	}
	return "unavailable"
}

func (u *webUI) mountAuth(mux *http.ServeMux, svc *service.Service) {
	callback := u.public + "/api/auth/web/evoleap/callback"
	fail := func(w http.ResponseWriter, r *http.Request, err error) {
		httperr.Report("web evoleap 登入", err) // 內部細節只進 log;畫面只拿到固定代碼
		http.Redirect(w, r, uiPrefix+"?login_error="+loginErrorCode(err), http.StatusSeeOther)
	}
	mux.HandleFunc("GET /api/auth/web/status", func(w http.ResponseWriter, r *http.Request) {
		methods := []string{}
		if svc.OAuthEvoleapEnabled() {
			methods = append(methods, "evoleap")
		}
		out := map[string]any{"mode": "server", "loggedIn": false, "methods": methods, "capabilities": webCapabilities}
		if tok := cookieValue(r, u.sessionName()); tok != "" {
			p, err := svc.WebSession(tok)
			var se *service.Error
			switch {
			case err == nil:
				out["loggedIn"] = true
				out["user"] = map[string]any{"id": p.User.ID, "name": p.User.Name, "email": p.User.Email, "admin": false}
			case errors.As(err, &se): // 無效、過期、撤銷、停用:清掉 cookie
				http.SetCookie(w, u.sessionCookie("", -1))
				if p != nil {
					out["denied"] = se.Msg
				}
			default:
				httperr.LogMasked("GET /api/auth/web/status", err)
				out["unavailable"] = "暫時無法確認登入狀態,請稍後再試"
			}
		}
		writeWebJSON(w, 200, out)
	})
	mux.HandleFunc("GET /api/auth/web/evoleap/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		loginURL, binding, err := svc.StartWebEvoleapLogin(callback, service.ClientIP(r))
		if err != nil {
			fail(w, r, err)
			return
		}
		http.SetCookie(w, u.loginCookie(binding, int(webLoginTTL.Seconds())))
		http.Redirect(w, r, loginURL, http.StatusSeeOther)
	})
	mux.HandleFunc("GET /api/auth/web/evoleap/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		q := r.URL.Query()
		binding := cookieValue(r, u.cookieName(webLoginCookie))
		res, used, err := svc.CompleteWebEvoleapLogin(q.Get("id"), q.Get("code"), binding, service.ClientIP(r), webSessionTTL)
		if used { // 是這次登入的回呼:綁定值只用一次(對不上的請求不能弄壞進行中的登入)
			http.SetCookie(w, u.loginCookie("", -1))
		}
		if err != nil {
			fail(w, r, err)
			return
		}
		http.SetCookie(w, u.sessionCookie(res.Token, int(webSessionTTL.Seconds())))
		http.Redirect(w, r, uiPrefix, http.StatusSeeOther)
	})
	mux.HandleFunc("POST /api/auth/web/logout", func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginWrite(r) {
			writeWebJSON(w, 403, map[string]any{"error": "跨站請求檢查沒過"})
			return
		}
		if tok := cookieValue(r, u.sessionName()); tok != "" {
			if err := svc.EndWebSession(tok); err != nil {
				var se *service.Error
				if !errors.As(err, &se) { // 撤銷沒成功:cookie 留著,讓使用者可以再按一次
					httperr.LogMasked("POST /api/auth/web/logout", err)
					writeWebJSON(w, 503, map[string]any{"error": "暫時無法登出,請稍後再試"})
					return
				}
			}
		}
		http.SetCookie(w, u.sessionCookie("", -1))
		writeWebJSON(w, 200, map[string]any{"result": true})
	})
	mux.HandleFunc("GET /api/web/orgs/{org}/attachments/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, err := svc.WebSession(cookieValue(r, u.sessionName()))
		var data []byte
		var mime string
		if err == nil {
			data, mime, err = svc.GetAttachment(browserPrincipal(p), r.PathValue("org"), r.PathValue("id"))
		}
		if err != nil {
			in := httperr.Report("GET 附件圖片", err)
			writeWebJSON(w, in.StatusOr(500), in.Body())
			return
		}
		h := w.Header()
		h.Set("Content-Type", mime)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Cache-Control", "private, no-store")
		_, _ = w.Write(data)
	})
}
