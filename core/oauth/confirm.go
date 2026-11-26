package oauth

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"

	"omixor-core/service"
)

// 簽發授權碼前的確認頁(GitLab 與 LDAP 共用):身分已經證明,使用者看清楚帳號、應用程式、回到哪裡與權限後才按允許

var confirmTmpl = template.Must(template.New("confirm").Parse(`<!doctype html>
<html lang="zh-Hant"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark">
<title>授權確認</title><link rel="stylesheet" href="/oauth/consent.css"></head>
<body><main>
<h1>確認授權</h1>
<p class="note">請確認以下內容。若你不是剛剛自己在這個瀏覽器開始連線,請按「拒絕」。</p>
<div class="box"><dl>
<dt>你登入的帳號</dt><dd>{{if .UserName}}{{.UserName}}({{.UserEmail}}){{else}}{{.UserEmail}}{{end}}</dd>
<dt>應用程式名稱(對方自行申報,未經驗證)</dt><dd>{{.ClientName}}</dd>
<dt>授權完成後會回到</dt><dd>{{.RedirectHost}}</dd>
{{if .Org}}<dt>組織</dt><dd>{{.Org}}</dd>{{else}}<dt>範圍</dt><dd>你帳號能存取的所有組織(包含之後加入的組織)</dd>{{end}}
<dt>授予的權限</dt><dd>{{.Permission}}</dd>
</dl></div>
{{if .Warn}}<p class="err">{{.Warn}}</p>{{end}}
<form method="post" action="/oauth/{{.Provider}}/confirm" autocomplete="off">
<input type="hidden" name="txn" value="{{.Token}}">
<div class="row"><button type="submit" name="action" value="deny">拒絕</button><button class="p" type="submit" name="action" value="allow">允許</button></div>
</form>
</main></body></html>
`))

type confirmView struct {
	*service.OAuthConfirmPage
	Provider                       string // 送回 /oauth/<Provider>/confirm
	RedirectHost, Permission, Warn string
}

func renderConfirm(w http.ResponseWriter, provider string, c *service.OAuthConfirmPage) {
	v := confirmView{OAuthConfirmPage: c, Provider: provider, RedirectHost: "?", Permission: c.Scope + ":唯讀(只能讀取,不能修改任何資料)"}
	switch c.Grant {
	case service.OAuthGrantWrite:
		v.Permission, v.Warn = c.Scope+":讀寫(協作類工具)", "這個應用程式將能以你的身分修改資料。"
	case service.OAuthGrantAdmin:
		v.Permission, v.Warn = c.Scope+":組織管理員(最高權限)", "這個應用程式將能以你的身分修改資料,並管理你擔任管理員的組織的結構。"
	}
	origin := ""
	if u, err := url.Parse(c.RedirectURI); err == nil {
		v.RedirectHost, origin = u.Host, u.Scheme+"://"+u.Host
	}
	pageHeaders(w, origin)
	w.WriteHeader(200)
	_ = confirmTmpl.Execute(w, v)
}

// confirmForm 讀確認頁的送出:拒絕跨站(在讀內容之前);回傳交易 token 與是否允許(只有 action=allow 是允許)。ok=false 時已經回應
func confirmForm(w http.ResponseWriter, r *http.Request, public string) (token string, allow, ok bool) {
	if crossSite(r, public) {
		detail(r)["pairing_result"] = "cross_site"
		renderFatal(w, 403, "這個請求不是從授權頁送出的,已拒絕")
		return "", false, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		renderFatal(w, 400, "請求格式不對")
		return "", false, false
	}
	return r.PostForm.Get("txn"), r.PostForm.Get("action") == "allow", true
}

// confirmFailed 確認(或外部登入回呼)失敗:轉址還不可信時顯示錯誤頁,可信時導回 redirect_uri?error=…
func confirmFailed(w http.ResponseWriter, r *http.Request, res *service.OAuthGitLabResult, err error) {
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
