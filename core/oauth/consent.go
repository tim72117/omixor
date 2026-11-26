package oauth

import (
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	"omixor-core/service"
)

// ---------------- 同意畫面 ----------------

const consentCSS = `:root{color-scheme:light dark;--bg:#fff;--fg:#1f2328;--fg2:#656d76;--bd:#d0d7de;--bg2:#f6f8fa;--ac:#0969da;--er:#cf222e}
@media(prefers-color-scheme:dark){:root{--bg:#161618;--fg:#e8e8ea;--fg2:#a0a0a8;--bd:#3a3a3e;--bg2:#222226;--ac:#4493f8;--er:#ff7b72}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.55 system-ui,-apple-system,"Noto Sans TC",sans-serif}
main{max-width:460px;margin:0 auto;padding:24px 16px}
h1{font-size:20px;margin:0 0 12px}
.box{border:1px solid var(--bd);background:var(--bg2);border-radius:10px;padding:12px 14px;margin:12px 0}
dl{margin:0}dt{color:var(--fg2);font-size:13px;margin-top:8px}dt:first-child{margin-top:0}dd{margin:0;word-break:break-all}
.note{color:var(--fg2);font-size:14px}
.err{color:var(--er);font-weight:600}
label{display:block;margin-top:14px}
input[type=text]{width:100%;padding:12px;font-size:20px;letter-spacing:.12em;text-transform:uppercase;border-radius:8px;border:1px solid var(--bd);background:var(--bg);color:var(--fg);margin:6px 0 14px}
input[type=password]{width:100%;padding:12px;font-size:16px;border-radius:8px;border:1px solid var(--bd);background:var(--bg);color:var(--fg);margin:6px 0 14px}
.row{display:flex;gap:10px}
button{flex:1;font:inherit;padding:12px;border-radius:8px;border:1px solid var(--bd);background:var(--bg2);color:var(--fg);cursor:pointer}
button.p{background:var(--ac);border-color:var(--ac);color:#fff}
fieldset{border:0;margin:12px 0;padding:0}
.opt{display:block;border:1px solid var(--bd);border-radius:10px;padding:10px 12px;margin:8px 0;cursor:pointer}
.opt input{margin-right:8px}
.opt.w{border-color:var(--er)}
.warn{color:var(--er);font-weight:600;margin:6px 0 0}
button.wide{width:100%;flex:none;margin:6px 0}
details{margin:14px 0}summary{cursor:pointer;color:var(--fg2)}
.opt ul{margin:6px 0 0 18px;padding:0;font-size:14px;color:var(--fg2)}
input.plain[type=text]{font-size:16px;letter-spacing:normal;text-transform:none}
.row.rev{flex-direction:row-reverse}`

func (h *Handler) css(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, consentCSS)
}

var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="zh-Hant"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark">
<title>{{.Title}}</title><link rel="stylesheet" href="/oauth/consent.css"></head>
<body><main>
{{if .Fatal}}<h1>無法授權</h1><p>{{.Fatal}}</p>{{else}}
<h1>授權連線確認</h1>
{{if .WriteRequested}}{{if .Account}}<p class="note">有一個應用程式想連到你帳號能存取的所有組織(之後你加入或退出組織,範圍會跟著變)。你可以只授予唯讀,或明確選擇讀寫(見下方,預設唯讀)。若你不是剛剛自己新增這個連線,請按「拒絕」。</p>{{else}}<p class="note">有一個應用程式想連到你的 omixor。你可以只授予唯讀,或明確選擇讀寫(見下方,預設唯讀)。若你不是剛剛自己在 Claude 新增這個 connector,請按「拒絕」。</p>{{end}}
{{else}}{{if .Account}}<p class="note">授權這個應用程式以唯讀方式讀取你帳號能存取的所有組織(之後你加入或退出組織,可讀取的範圍會跟著變)。若你不是剛剛自己新增這個連線,請按「拒絕」。</p>{{else}}<p class="note">有一個應用程式想用唯讀的方式連到你的 omixor。若你不是剛剛自己在 Claude 新增這個 connector,請按「拒絕」。</p>{{end}}{{end}}
<div class="box"><dl>
<dt>應用程式名稱(對方自行申報,未經驗證)</dt><dd>{{.ClientName}}</dd>
<dt>授權完成後會回到</dt><dd>{{.RedirectHost}}</dd>
{{if .Account}}<dt>範圍</dt><dd>你帳號能存取的所有組織(包含之後加入的組織)</dd>{{else}}<dt>組織</dt><dd>{{.Org}}</dd>{{end}}
{{if not .WriteRequested}}<dt>要求的權限</dt><dd>{{.Scope}}(只能讀取,不能修改任何資料)</dd>{{end}}
</dl></div>
{{if .WriteRequested}}<fieldset><legend>這個應用程式要求{{if .AdminRequested}}「組織管理員」{{else}}「讀寫」{{end}}權限,請選擇要授予的權限(預設只授予唯讀)</legend>
<label class="opt"><input type="radio" name="grant" form="consent" value="read" checked><strong>唯讀(建議)</strong><br><span class="note">{{.Scope}}:只能讀取,不能修改任何資料。</span></label>
<label class="opt w"><input type="radio" name="grant" form="consent" value="write"><strong>讀寫(進階)</strong>
<p class="warn">這個應用程式將能以你的身分修改資料。</p>
<span class="note">可寫入的只有協作類工具,權限仍受你在組織裡的角色限制:</span>
<ul><li>建立、更新、接受或放回 Issue</li><li>提出與處理待決定、需要協助,由待決定開出 Issue</li><li>新增、修改、回報 Issue 子項,把子項提升為 Issue</li><li>存文件到文件櫃</li><li>建立對話頻道、在頻道發訊息</li><li>回報任務(不能指派任務給角色)</li></ul>
<span class="note">不能做:管理角色、組織鏈、專案與文件櫃結構、事件、領取角色憑證、伺服器管理員的操作(即使你是伺服器管理員也一樣),也不能管理 token 或使用者。</span><br>
<span class="note">撤銷:目前沒有自助的撤銷畫面。退出該組織,或請伺服器管理員停用你的帳號,會立刻失效;存取權杖 10 分鐘到期,續期權杖最久 30 天。</span></label>
{{if .AdminRequested}}<label class="opt w"><input type="radio" name="grant" form="consent" value="admin"{{if .AdminDenied}} disabled{{end}}><strong>組織管理員(最高權限,進階)</strong>
{{if .AdminDenied}}<p class="warn">你不是任何組織的管理員,不能選這個選項;請改選唯讀或讀寫。</p>{{else}}<p class="warn">只有組織管理員能選。這個應用程式將能以你的身分管理你擔任管理員的組織的結構。</p>{{end}}
<span class="note">包含上面「讀寫」的所有權限,另外可以(每次呼叫都會即時確認你當下仍是該組織的管理員,失去管理員身分就立刻失效):</span>
<ul><li>新增、修改虛擬角色</li><li>修改組織鏈(誰向誰回報、誰可以指派誰)</li><li>新增、修改專案與專案成員、可見性</li><li>修改文件櫃的結構</li><li>宣告、更新事件</li></ul>
<span class="note">仍然不能做:領取任務或簽發角色憑證、替角色排任務或轉移角色的任務、寫入 commit 資料、伺服器管理員的操作(即使你是伺服器管理員也一樣),也不能管理 token 或使用者。</span></label>{{end}}
</fieldset>{{end}}
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form id="consent" method="post" action="/oauth/authorize" autocomplete="off">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<input type="hidden" name="response_type" value="code">
<input type="hidden" name="code_challenge" value="{{.Challenge}}">
<input type="hidden" name="code_challenge_method" value="S256">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="scope" value="{{.RequestedScope}}">
<input type="hidden" name="resource" value="{{.Resource}}">
{{range .Buttons}}<button class="p wide" type="submit" name="{{.ActionName}}" value="{{.ActionValue}}">{{.Button}}</button>
<p class="note">{{.Note}}</p>
{{end}}{{if .Buttons}}{{range .Forms}}<details{{if $.Retry}} open{{end}}><summary>{{.Summary}}</summary>
{{template "fields" .}}<div class="row"><button class="p" type="submit" name="{{.ActionName}}" value="{{.ActionValue}}">{{.Button}}</button></div></details>
{{end}}<div class="row"><button type="submit" name="action" value="deny">拒絕</button></div>
{{else}}{{range .Forms}}{{template "fields" .}}{{if .Secret}}<div class="row rev"><button class="p" type="submit" name="{{.ActionName}}" value="{{.ActionValue}}">{{.SoloButton}}</button><button type="submit" name="action" value="deny">拒絕</button></div>{{else}}<div class="row"><button type="submit" name="action" value="deny">拒絕</button><button class="p" type="submit" name="{{.ActionName}}" value="{{.ActionValue}}">{{.SoloButton}}</button></div>{{end}}{{end}}
{{end}}</form>{{end}}
</main></body></html>
{{define "fields"}}{{range .Fields}}<label for="f-{{.Name}}">{{.Label}}</label>
<input id="f-{{.Name}}" type="{{.Type}}" name="{{.Name}}"{{if and (not .Caps) (eq .Type "text")}} class="plain"{{end}} inputmode="text"{{if .Caps}} autocapitalize="characters"{{end}} autocomplete="off" autocorrect="off" spellcheck="false"{{if .MaxLen}} maxlength="{{.MaxLen}}"{{end}}{{if .Placeholder}} placeholder="{{.Placeholder}}"{{end}}{{if .Focus}} autofocus{{end}}>
{{end}}{{end}}
`))

type consentView struct {
	Title, Fatal, Error                                     string
	ClientName, RedirectHost, Org, Scope                    string
	CSRF, ClientID, RedirectURI, Challenge, State, Resource string
	RequestedScope                                          string
	Account, WriteRequested                                 bool
	AdminRequested, AdminDenied                             bool           // AdminRequested:客戶端要求 mcp:admin(提供「組織管理員」選項);AdminDenied:剛剛證明身分的使用者不是任何組織的管理員(選項停用)
	Buttons, Forms                                          []providerView // 證明身分的方式:Buttons 是純按鈕(導去外部登入),Forms 是要填欄位的
	Retry                                                   bool           // 剛剛沒過:有欄位的方式預設展開(收在 <details> 時)
}

// providerView 一個 provider 在同意畫面上的樣子
type providerView struct {
	Presentation
	ActionName, ActionValue string
	Fields                  []fieldView
	Secret                  bool // 有密碼欄:送出鈕要是這張表單的預設鈕
}

type fieldView struct {
	Field
	Type  string // input 的 type:text 或 password
	Focus bool   // 沒有別的按鈕時,第一個欄位自動取得焦點
}

func newProviderView(p IdentityProvider) providerView {
	v := providerView{Presentation: p.Present()}
	v.ActionName, v.ActionValue, _ = strings.Cut(p.FormActions()[0], "=")
	for _, f := range p.Fields() {
		t := "text"
		if f.Kind == "password" {
			t, v.Secret = "password", true
		}
		v.Fields = append(v.Fields, fieldView{Field: f, Type: t})
	}
	return v
}

// providerViews 同意畫面要顯示的 provider(沒啟用的不顯示),分成純按鈕與有欄位的兩組;沒有按鈕時第一個欄位自動取得焦點
func (h *Handler) providerViews() (buttons, forms []providerView) {
	for _, p := range h.Providers {
		if !providerEnabled(p) {
			continue
		}
		v := newProviderView(p)
		if len(v.Fields) == 0 {
			buttons = append(buttons, v)
		} else {
			forms = append(forms, v)
		}
	}
	if len(buttons) == 0 && len(forms) > 0 {
		forms[0].Fields[0].Focus = true
	}
	return buttons, forms
}

// formAction CSP form-action 的來源:客戶端 redirect_uri 的來源,加上各 provider 送出後會導去的外部來源(例如 evoleap 核准頁)
func formAction(origin string, buttons, forms []providerView) string {
	fa := origin
	for _, v := range append(append([]providerView{}, buttons...), forms...) {
		for _, src := range v.FormSources {
			if src != "" && !strings.Contains(" "+fa+" ", " "+src+" ") {
				fa += " " + src
			}
		}
	}
	return fa
}

func pageHeaders(w http.ResponseWriter, formAction string) {
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	// form-action:除了自己,還要允許送出後導向的 redirect_uri 的來源(Chrome 連「送出表單後的 3xx 導向」都檢查 form-action,只寫 'self' 會把導回 Claude 擋掉)
	fa := "'self'"
	if formAction != "" {
		fa += " " + formAction
	}
	hd.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; form-action "+fa)
}

func renderFatal(w http.ResponseWriter, status int, msg string) {
	pageHeaders(w, "")
	w.WriteHeader(status)
	_ = consentTmpl.Execute(w, consentView{Title: "授權錯誤", Fatal: msg})
}

func (h *Handler) renderConsent(w http.ResponseWriter, status int, c *service.OAuthAuthChecked, req service.OAuthAuthRequest, csrf, errMsg string, adminDenied ...bool) {
	host, origin := "?", ""
	if u, err := url.Parse(c.RedirectURI); err == nil {
		host, origin = u.Host, u.Scheme+"://"+u.Host
	}
	buttons, forms := h.providerViews()
	pageHeaders(w, formAction(origin, buttons, forms))
	w.WriteHeader(status)
	_ = consentTmpl.Execute(w, consentView{Title: "授權確認", Error: errMsg, ClientName: c.Client.Name, RedirectHost: host, Org: c.Org, Scope: c.Scope, Account: c.Account, WriteRequested: c.WriteRequested, AdminRequested: c.AdminRequested, AdminDenied: len(adminDenied) > 0 && adminDenied[0],
		CSRF: csrf, ClientID: c.Client.ID, RedirectURI: c.RedirectURI, Challenge: req.CodeChallenge, State: req.State, Resource: c.Resource,
		RequestedScope: req.Scope, Buttons: buttons, Forms: forms, Retry: errMsg != ""})
}
