package oauth

import (
	"errors"
	"net/http"
	"strings"

	"omixor-core/service"
)

// EvoleapProvider 用 evoleap 瀏覽器登入證明身分:Start 向 evoleap 登記回呼、設綁定瀏覽器的 cookie 並導去 evoleap 核准頁;
// Callback(/oauth/evoleap/callback)核對 cookie 與 10 分鐘單一用途的登入交易、換 token 對上使用者後簽發授權碼(都在 service.CompleteOAuthEvoleap)。
// 因為使用者是在「另一個請求」回來的,簽發授權碼在 service 內完成,不經過 Outcome.Done
type EvoleapProvider struct {
	Svc    *service.Service
	Public string // 對外網址(不含結尾斜線);回呼網址與 cookie 的 Secure 由它決定
}

const evoCookie = "omixor_oauth_evo"

func (p *EvoleapProvider) ID() string            { return "evoleap" }
func (p *EvoleapProvider) Label() string         { return "evoleap" }
func (p *EvoleapProvider) Fields() []Field       { return nil }
func (p *EvoleapProvider) FormActions() []string { return []string{"via=evoleap"} }

// Enabled 伺服器有 evoleap 網址、沒被關掉才在同意畫面顯示(沒啟用時送出 via=evoleap 仍會被 service 以 400 擋下)
func (p *EvoleapProvider) Enabled() bool { return p.Svc.OAuthEvoleapEnabled() }

func (p *EvoleapProvider) Present() Presentation {
	pr := Presentation{Button: "用 evoleap 登入並允許", Note: "會前往 evoleap 登入(Google),完成後自動回到這裡並授權;不需要配對碼。"}
	if co := p.Svc.OAuthEvoleapConsoleOrigin(); co != "" { // 按鈕送出後 303 導去 evoleap 核准頁
		pr.FormSources = []string{co}
	}
	return pr
}

func (p *EvoleapProvider) Callback() http.Handler { return http.HandlerFunc(p.callback) }

func (p *EvoleapProvider) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: evoCookie, Value: value, Path: "/oauth", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(p.Public, "https://")}
}

// Start 授權請求與 CSRF 已經驗證過:開始 evoleap 登入,回傳綁定瀏覽器的 cookie 與 evoleap 核准頁網址。連不到 evoleap 時 CSRF 已用掉,重新顯示同意畫面(可改用配對碼)
func (p *EvoleapProvider) Start(w http.ResponseWriter, r *http.Request, t Txn) (Outcome, error) {
	d := detail(r)
	d["via"] = "evoleap"
	loginURL, binding, err := p.Svc.StartOAuthEvoleapGrant(t.Checked, t.Req.CodeChallenge, t.Req.State, callbackURL(p.Public, p.ID()), t.Grant)
	if err != nil {
		var oe *service.OAuthError
		if errors.As(err, &oe) && oe.Reason == "evoleap_unavailable" {
			return retryOutcome(r, oe, oe.Reason), nil
		}
		return Outcome{}, err
	}
	d["pairing_result"] = "evoleap_started"
	return Outcome{Redirect: loginURL, Cookie: p.cookie(binding, int(service.OAuthEvoleapTTL.Seconds()))}, nil
}

// callback evoleap 核准後導回:成功 303 導回客戶端 redirect_uri?code=…&state=…;
// 轉址還不可信時(id 不認得、cookie 不對、逾時、重放…)一律同一個通用錯誤頁;之後的失敗導回 redirect_uri?error=…
func (p *EvoleapProvider) callback(w http.ResponseWriter, r *http.Request) {
	d := detail(r)
	d["via"] = "evoleap"
	q := r.URL.Query()
	d["id_present"], d["code_present"] = q.Get("id") != "", q.Get("code") != ""
	binding := ""
	if c, err := r.Cookie(evoCookie); err == nil {
		binding = c.Value
	}
	d["cookie_present"] = binding != ""
	http.SetCookie(w, p.cookie("", -1)) // 綁定密鑰只用一次
	res, err := p.Svc.CompleteOAuthEvoleap(q.Get("id"), q.Get("code"), binding)
	if err != nil {
		var oe *service.OAuthError
		if !errors.As(err, &oe) {
			d["error"], d["detail"] = "server_error", err.Error() // 內部錯誤:頁面不外露,log 留原因
			renderFatal(w, 500, "伺服器錯誤,請稍後再試")
			return
		}
		noteError(r, oe)
		if res != nil && res.RedirectURI != "" && !oe.NoRedirect {
			redirectTo(w, res.RedirectURI, "error", oe.Code, "error_description", oe.Desc, "state", res.State)
			return
		}
		renderFatal(w, oe.Status, oe.Desc)
		return
	}
	d["pairing_result"] = "ok"
	redirectTo(w, res.RedirectURI, "code", res.Code, "state", res.State)
}

// Check 啟動自我檢查(見 diagnose.go):要登記在 evoleap 的回呼網址;沒啟用時說明為什麼(沒設 EVOLEAP_URL、或被 OMIXOR_OAUTH_EVOLEAP_LOGIN=false 關掉)。
// 沒啟用本身不是問題(可能只用 GitLab 或 LDAP);剩下哪些登入方式由 loginProblems 判斷
func (p *EvoleapProvider) Check(public string) []Problem {
	if p.Enabled() {
		return []Problem{{Level: Info, Msg: "evoleap 登入已啟用,要在 evoleap 登記回呼網址 " + callbackURL(public, p.ID())}}
	}
	why, fix := "沒有 evoleap 網址", "設 EVOLEAP_URL"
	if p.Svc.OAuthEvoleapOff {
		why, fix = "被 OMIXOR_OAUTH_EVOLEAP_LOGIN=false 關掉", "要用 evoleap 登入就拿掉這個設定"
	}
	return []Problem{{Level: Info, Msg: "evoleap 登入沒有啟用(" + why + ")", Fix: fix}}
}
