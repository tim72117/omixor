package oauth

import (
	"html/template"
	"net/http"
	"net/url"

	"omixor-core/service"
)

// GitLab 登入完成後的確認頁:GitLab 對授權過的應用程式不會再詢問,所以簽發授權碼之前一定要使用者在這裡看清楚再按允許

var gitlabConfirmTmpl = template.Must(template.New("gitlab-confirm").Parse(`<!doctype html>
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
<form method="post" action="/oauth/gitlab/confirm" autocomplete="off">
<input type="hidden" name="txn" value="{{.Token}}">
<div class="row"><button type="submit" name="action" value="deny">拒絕</button><button class="p" type="submit" name="action" value="allow">允許</button></div>
</form>
</main></body></html>
`))

type gitlabConfirmView struct {
	*service.OAuthGitLabConfirm
	RedirectHost, Permission, Warn string
}

func renderGitLabConfirm(w http.ResponseWriter, c *service.OAuthGitLabConfirm) {
	v := gitlabConfirmView{OAuthGitLabConfirm: c, RedirectHost: "?", Permission: c.Scope + ":唯讀(只能讀取,不能修改任何資料)"}
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
	_ = gitlabConfirmTmpl.Execute(w, v)
}
