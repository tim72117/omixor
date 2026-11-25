package api

import (
	"html"
	"io"
	"net/http"
	"strings"

	"omixor-core/httperr"
	"omixor-core/service"
)

// GitLab 的 CLI / 客戶端登入(伺服器有設 OMIXOR_GITLAB_URL 才啟用,沒啟用一律 404;規則在 service/gitlab.go、gitlab_login.go)。
// 能力偵測:GET /api/version 的 features 含 gitlab_login。
//
//	POST /api/auth/gitlab/start    {redirect, device?, codeChallenge, state} → {url}
//	                               redirect 只收 http 的本機位址(http://127.0.0.1|localhost|[::1]:<埠>/…);codeChallenge 是 PKCE S256;url 用瀏覽器開
//	GET  /api/auth/gitlab/login    ?id=(瀏覽器;只能用一次):設綁定 cookie,303 導去 GitLab
//	GET  /api/auth/gitlab/callback GitLab 導回(要登記在 GitLab Application):303 導回 redirect?code=…&state=…,
//	                               不能登入時 redirect?error=…&error_description=…&state=…;交易本身不對顯示 400 錯誤頁
//	POST /api/auth/gitlab          {code, codeVerifier} → 同 /api/auth/ldap {token, tokenId, expiresAt, user};碼 60 秒、用一次,不對 401

const gitlabLoginCookie = "omixor_gitlab_login"

func (a *API) gitlabCookie(value string, maxAge int) *http.Cookie {
	secure := a.Svc.GitLab != nil && strings.HasPrefix(a.Svc.GitLab.Public, "https://")
	return &http.Cookie{Name: gitlabLoginCookie, Value: value, Path: "/api/auth/gitlab", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure}
}

func browserHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func redirectBrowser(w http.ResponseWriter, to string) {
	browserHeaders(w)
	w.Header().Set("Location", to)
	w.WriteHeader(303)
}

// browserError 給瀏覽器看的錯誤頁(狀態碼與訊息走 httperr,內部細節不外露)
func browserError(w http.ResponseWriter, err error) {
	in := httperr.Report(opName(w), err)
	msg, _ := in.Body()["error"].(string)
	browserHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.WriteHeader(in.StatusOr(500))
	_, _ = io.WriteString(w, `<!doctype html><html lang="zh-Hant"><meta charset="utf-8"><title>omixor</title><h1>無法登入</h1><p>`+html.EscapeString(msg)+`</p></html>`)
}

func (a *API) gitlabRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/gitlab/start", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Redirect      string `json:"redirect"`
			Device        string `json:"device"`
			CodeChallenge string `json:"codeChallenge"`
			State         string `json:"state"`
		}
		if err := readSmall(w, r, &in); err != nil {
			bad(w, err)
			return
		}
		u, err := a.Svc.StartGitLabLogin(in.Redirect, in.Device, in.CodeChallenge, in.State, service.ClientIP(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		writeJSON(w, 200, map[string]any{"url": u})
	})
	mux.HandleFunc("GET /api/auth/gitlab/login", func(w http.ResponseWriter, r *http.Request) {
		to, binding, err := a.Svc.GitLabLoginRedirect(r.URL.Query().Get("id"))
		if err != nil {
			browserError(w, err)
			return
		}
		http.SetCookie(w, a.gitlabCookie(binding, int(service.GitLabTxnTTL.Seconds())))
		redirectBrowser(w, to)
	})
	mux.HandleFunc("GET /api/auth/gitlab/callback", func(w http.ResponseWriter, r *http.Request) {
		binding := ""
		if c, err := r.Cookie(gitlabLoginCookie); err == nil {
			binding = c.Value
		}
		q := r.URL.Query()
		to, err := a.Svc.GitLabLoginCallback(q.Get("state"), q.Get("code"), q.Get("error"), binding)
		if err != nil {
			browserError(w, err) // 不清 cookie:假的回呼不能把進行中的登入弄壞
			return
		}
		http.SetCookie(w, a.gitlabCookie("", -1))
		redirectBrowser(w, to)
	})
	mux.HandleFunc("POST /api/auth/gitlab", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code         string `json:"code"`
			CodeVerifier string `json:"codeVerifier"`
		}
		if err := readSmall(w, r, &in); err != nil {
			bad(w, err)
			return
		}
		res, err := a.Svc.LoginWithGitLab(in.Code, in.CodeVerifier, service.ClientIP(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		writeJSON(w, 200, res)
	})
}

// readSmall 同 read,但不用登入的入口限制在 16 KB
func readSmall(w http.ResponseWriter, r *http.Request, v any) error {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	}
	return read(r, v)
}
