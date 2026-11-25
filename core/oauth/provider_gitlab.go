package oauth

import (
	"errors"
	"net/http"
	"strings"

	"omixor-core/service"
)

// GitLabProvider 用自建 GitLab 登入證明身分:Start 設綁定瀏覽器的 cookie 並導去 GitLab 授權頁;
// Callback(/oauth/gitlab/callback)核對 state 與 cookie(10 分鐘、用一次)、換 token 對上使用者後顯示確認頁;
// Confirm(POST /oauth/gitlab/confirm)使用者按下允許才簽發授權碼(service.CompleteOAuthGitLab、ConfirmOAuthGitLab)
type GitLabProvider struct {
	Svc    *service.Service
	Public string // 對外網址(不含結尾斜線);cookie 的 Secure 由它決定
}

const gitlabCookie = "omixor_oauth_gitlab"

func (p *GitLabProvider) ID() string            { return "gitlab" }
func (p *GitLabProvider) Label() string         { return "GitLab" }
func (p *GitLabProvider) Fields() []Field       { return nil }
func (p *GitLabProvider) FormActions() []string { return []string{"via=gitlab"} }

// Enabled 伺服器有設 GitLab 登入才在同意畫面顯示
func (p *GitLabProvider) Enabled() bool { return p.Svc.GitLabEnabled() }

func (p *GitLabProvider) Present() Presentation {
	pr := Presentation{Button: "用 GitLab 登入並允許", Note: "會前往 GitLab 登入,完成後自動回到這裡並授權;不需要配對碼。"}
	if o := p.Svc.GitLabOrigin(); o != "" { // 按鈕送出後 303 導去 GitLab
		pr.FormSources = []string{o}
	}
	return pr
}

func (p *GitLabProvider) Callback() http.Handler { return http.HandlerFunc(p.callback) }

func (p *GitLabProvider) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: gitlabCookie, Value: value, Path: "/oauth", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(p.Public, "https://")}
}

// Start 授權請求與 CSRF 已經驗證過
func (p *GitLabProvider) Start(w http.ResponseWriter, r *http.Request, t Txn) (Outcome, error) {
	d := detail(r)
	d["via"] = "gitlab"
	loginURL, binding, err := p.Svc.StartOAuthGitLab(t.Checked, t.Req.CodeChallenge, t.Req.State, t.Grant, gitlabBinding(r))
	if err != nil {
		return Outcome{}, err
	}
	d["pairing_result"] = "gitlab_started"
	return Outcome{Redirect: loginURL, Cookie: p.cookie(binding, int(service.GitLabTxnTTL.Seconds()))}, nil
}

func gitlabBinding(r *http.Request) string {
	if c, err := r.Cookie(gitlabCookie); err == nil {
		return c.Value
	}
	return ""
}

func (p *GitLabProvider) Confirm() http.Handler { return http.HandlerFunc(p.confirm) }

// callback GitLab 導回:身分證明成功顯示確認頁;轉址還不可信時一律同一個通用錯誤頁,可信時失敗導回 redirect_uri?error=…
func (p *GitLabProvider) callback(w http.ResponseWriter, r *http.Request) {
	d := detail(r)
	d["via"] = "gitlab"
	q := r.URL.Query()
	d["code_present"] = q.Get("code") != ""
	binding := gitlabBinding(r)
	d["cookie_present"] = binding != ""
	res, err := p.Svc.CompleteOAuthGitLab(q.Get("state"), q.Get("code"), q.Get("error"), binding)
	if err != nil {
		p.failed(w, r, res, err)
		return
	}
	d["pairing_result"] = "gitlab_confirm"
	// cookie 不清掉(別的分頁可能也在用),把時效續到確認的時限
	http.SetCookie(w, p.cookie(binding, int(service.GitLabTxnTTL.Seconds())))
	renderGitLabConfirm(w, res.Confirm)
}

// confirm 確認頁送出:只有 action=allow 是允許,其他一律當拒絕
func (p *GitLabProvider) confirm(w http.ResponseWriter, r *http.Request) {
	d := detail(r)
	d["via"] = "gitlab"
	if crossSite(r, p.Public) {
		d["pairing_result"] = "cross_site"
		renderFatal(w, 403, "這個請求不是從授權頁送出的,已拒絕")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		renderFatal(w, 400, "請求格式不對")
		return
	}
	res, err := p.Svc.ConfirmOAuthGitLab(r.PostForm.Get("txn"), gitlabBinding(r), r.PostForm.Get("action") == "allow")
	if err != nil {
		p.failed(w, r, res, err)
		return
	}
	d["pairing_result"] = "ok"
	redirectTo(w, res.RedirectURI, "code", res.Code, "state", res.State)
}

func (p *GitLabProvider) failed(w http.ResponseWriter, r *http.Request, res *service.OAuthGitLabResult, err error) {
	d := detail(r)
	var oe *service.OAuthError
	if !errors.As(err, &oe) {
		renderFatal(w, 500, "伺服器錯誤,請稍後再試") // 原因在 service 已記 log
		return
	}
	d["error"], d["pairing_result"] = oe.Code, oe.Reason
	if res != nil && res.RedirectURI != "" && !oe.NoRedirect {
		redirectTo(w, res.RedirectURI, "error", oe.Code, "error_description", oe.Desc, "state", res.State)
		return
	}
	renderFatal(w, oe.Status, oe.Desc)
}
