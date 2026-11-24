// Package oauth 是 omixor 的 OAuth 2.1 授權伺服器 HTTP 層:讓 Claude 自訂 connector 登入 MCP 端點,授權永遠是唯讀。
// resource 有兩種:組織 MCP 端點 <public>/mcp/org/{org}(綁單一組織),和統一入口 <public>/mcp(帳號層級:不綁組織,
// 每次工具呼叫由統一入口用即時成員資格決定能用哪些組織;同意畫面不選組織)。
// 同意畫面證明身分有兩種:主要是「用 evoleap 登入並允許」(瀏覽器登入 evoleap,見下面 /oauth/evoleap/callback;伺服器沒有 evoleap 或被關掉就不顯示),
// 備援是一次性的配對碼(本機伺服器常沒有 evoleap)。
// 規則(證明身分、PKCE、refresh 輪替、唯讀)在 service/oauth.go,存放在 omixor_oauth_*(core/store/pg_oauth.go);這裡只做 HTTP:
//
//	GET  /.well-known/oauth-protected-resource/mcp/org/{org}   RFC 9728:這個組織 MCP 端點的 resource 與授權伺服器(/.well-known/oauth-protected-resource 是沒有路徑的備援)
//	GET  /.well-known/oauth-protected-resource/mcp             RFC 9728:統一入口 /mcp(帳號層級唯讀授權)
//	GET  /.well-known/oauth-authorization-server/oauth         RFC 8414:授權伺服器 metadata(/oauth/.well-known/openid-configuration 同內容)
//	POST /oauth/register                                       RFC 7591 動態註冊(JSON ≤ 16 KB;redirect_uri 只收 Claude 的 callback(含桌面版 claude://claude.ai/mcp-auth-callback/sdk)與本機 /callback)
//	GET  /oauth/authorize                                      同意畫面:主要按鈕「用 evoleap 登入並允許」(有設 evoleap 才有);備援是輸入「配對碼」(在已登入的 omixor 的 手機配對 產生)
//	POST /oauth/authorize                                      送出同意:驗證 CSRF;配對碼成功 303 導回 redirect_uri?code=…&state=…;via=evoleap 則重驗授權請求、
//	                                                           登記一筆登入交易、設綁定瀏覽器的 cookie(omixor_oauth_evo,HttpOnly、SameSite=Lax、Path=/oauth)並 303 導去 evoleap 核准頁
//	GET  /oauth/evoleap/callback                               evoleap 核准後導回(?id=&code=):核對 cookie、交易單一用途(10 分鐘)、向 evoleap 換 token 對上 omixor 使用者後
//	                                                           立刻撤銷 evoleap token,簽發授權碼並 303 導回客戶端的 redirect_uri;任何不符一律同一個通用錯誤頁
//	POST /oauth/token                                          authorization_code(PKCE S256)、refresh_token(輪替);只收 application/x-www-form-urlencoded
//	GET  /oauth/consent.css                                    同意畫面的樣式(CSP 不允許行內樣式,所以獨立一個檔)
//
// 對外網址(OMIXOR_PUBLIC_URL)沒設就不掛這些路由(issuer、resource 都需要它)。
package oauth

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"log"

	"omixor-core/model"
	"omixor-core/service"
)

// Handler OAuth 端點
type Handler struct {
	Svc    *service.Service
	Public string // 對外網址(不含結尾斜線),例如 https://omixor.example.com
	// Log 觀測 log 的輸出(見 log.go);nil = 伺服器既有的標準 logger(log.Writer(),不加時間前綴,一行一個 JSON)
	Log *log.Logger
}

// New 建立;public 是 OMIXOR_PUBLIC_URL(結尾斜線會去掉)
func New(svc *service.Service, public string) *Handler {
	return &Handler{Svc: svc, Public: strings.TrimRight(public, "/")}
}

// Issuer 授權伺服器識別
func (h *Handler) Issuer() string { return h.Public + "/oauth" }

// MCPURL 組織 MCP 端點的網址(也是 OAuth 的 resource)
func (h *Handler) MCPURL(org string) string { return h.Public + "/mcp/org/" + org }

// UnifiedURL 統一入口的網址(也是帳號層級授權的 OAuth resource)
func (h *Handler) UnifiedURL() string { return h.Public + "/mcp" }

// UnifiedResourceMetadataURL 統一入口的 Protected Resource Metadata 網址
func (h *Handler) UnifiedResourceMetadataURL() string {
	return h.Public + "/.well-known/oauth-protected-resource/mcp"
}

// ResourceMetadataURL 組織 MCP 端點的 Protected Resource Metadata 網址
func (h *Handler) ResourceMetadataURL(org string) string {
	return h.Public + "/.well-known/oauth-protected-resource/mcp/org/" + org
}

// orgOfResource resource(RFC 8707)→ 組織 id;不是這個伺服器的組織 MCP 網址回 ""
func (h *Handler) orgOfResource(resource string) string {
	org, ok := strings.CutPrefix(resource, h.Public+"/mcp/org/")
	if !ok || !model.ValidOrgID(org) {
		return ""
	}
	return org
}

// resourceOf resource(RFC 8707)→ 授權範圍:組織 MCP 網址回組織 id;統一入口網址回 account=true;其他都不認得(org 空、account false)
func (h *Handler) resourceOf(resource string) (org string, account bool) {
	if resource == h.UnifiedURL() {
		return "", true
	}
	return h.orgOfResource(resource), false
}

// Mount 把路由掛到 mux
func (h *Handler) Mount(mux *http.ServeMux) {
	obs := func(f http.HandlerFunc) http.Handler { return h.observe(f, nil, nil) }
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp/org/{org}", obs(h.protectedResource))
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", obs(h.protectedResourceUnified))
	mux.Handle("GET /.well-known/oauth-protected-resource", obs(h.protectedResource))
	mux.Handle("GET /.well-known/oauth-authorization-server/oauth", obs(h.serverMetadata))
	mux.Handle("GET /oauth/.well-known/openid-configuration", obs(h.serverMetadata))
	mux.Handle("POST /oauth/register", obs(h.register))
	mux.Handle("GET /oauth/authorize", obs(h.authorizeGet))
	mux.Handle("POST /oauth/authorize", obs(h.authorizePost))
	mux.Handle("GET /oauth/evoleap/callback", obs(h.evoleapCallback))
	mux.Handle("POST /oauth/token", obs(h.token))
	mux.HandleFunc("GET /oauth/consent.css", h.css) // 靜態樣式,不記 log
}

func writeJSON(w http.ResponseWriter, status int, v any, extra ...string) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	for i := 0; i+1 < len(extra); i += 2 {
		w.Header().Set(extra[i], extra[i+1])
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// ---------------- metadata ----------------

func (h *Handler) protectedResourceUnified(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"resource":                 h.UnifiedURL(),
		"authorization_servers":    []string{h.Issuer()},
		"scopes_supported":         []string{model.OAuthScopeRead, model.OAuthScopeWrite, model.OAuthScopeAdmin},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "omixor",
	})
}

func (h *Handler) protectedResource(w http.ResponseWriter, r *http.Request) {
	resource := h.Public // 沒有路徑的備援:沒有組織可以指,只給伺服器本身
	if org := r.PathValue("org"); org != "" {
		if !model.ValidOrgID(org) {
			http.NotFound(w, r)
			return
		}
		resource = h.MCPURL(org)
	}
	writeJSON(w, 200, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{h.Issuer()},
		"scopes_supported":         []string{model.OAuthScopeRead, model.OAuthScopeWrite, model.OAuthScopeAdmin},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "omixor",
	})
}

func (h *Handler) serverMetadata(w http.ResponseWriter, r *http.Request) {
	iss := h.Issuer()
	writeJSON(w, 200, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/authorize",
		"token_endpoint":                        iss + "/token",
		"registration_endpoint":                 iss + "/register",
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{model.OAuthScopeRead, model.OAuthScopeWrite, model.OAuthScopeAdmin, model.OAuthScopeOffline},
	})
}

// ---------------- 錯誤 ----------------

func jsonError(w http.ResponseWriter, err error) {
	var oe *service.OAuthError
	if !errors.As(err, &oe) {
		oe = &service.OAuthError{Status: 500, Code: "server_error", Desc: "伺服器錯誤"}
		_ = service.Code(err) // 內部錯誤不外露細節
	}
	extra := []string{}
	if oe.Status == 429 {
		extra = append(extra, "Retry-After", "60")
	}
	writeJSON(w, oe.Status, map[string]any{"error": oe.Code, "error_description": oe.Desc}, extra...)
}

// ---------------- 動態註冊 ----------------

func mediaType(r *http.Request) string {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt
}

// anyStrings JSON 任意值 → 字串清單(觀測用;非字串清單回 nil)
func anyStrings(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if mediaType(r) != "application/json" {
		writeJSON(w, 415, map[string]any{"error": "invalid_client_metadata", "error_description": "要用 application/json"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeJSON(w, 413, map[string]any{"error": "invalid_client_metadata", "error_description": "內容太大(上限 16 KB)"})
		return
	}
	var in service.OAuthClientInput
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid_client_metadata", "error_description": "內容必須是 JSON 物件"})
		return
	}
	d := detail(r)
	d["client_name"] = service.CleanOAuthClientName(in.ClientName)
	d["redirect_uris"] = anyStrings(in.RedirectURIs)
	d["grant_types"] = anyStrings(in.GrantTypes)
	d["response_types"] = anyStrings(in.ResponseTypes)
	d["token_endpoint_auth_method"], _ = in.TokenEndpointAuthMethod.(string)
	d["scope"], _ = in.Scope.(string)
	cl, err := h.Svc.RegisterOAuthClient(in, service.ClientIP(r))
	if err != nil {
		jsonError(w, err)
		return
	}
	d["client_id"] = cl.ID
	writeJSON(w, 201, map[string]any{
		"client_id": cl.ID, "client_id_issued_at": cl.CreatedAt.Unix(), "client_name": cl.Name,
		"redirect_uris": cl.RedirectURIs, "grant_types": cl.GrantTypes, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// ---------------- token ----------------

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	d := detail(r)
	d["result"] = "error"
	d["body_format"] = "other"
	if mediaType(r) != "application/x-www-form-urlencoded" {
		writeJSON(w, 415, map[string]any{"error": "invalid_request", "error_description": "要用 application/x-www-form-urlencoded"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeJSON(w, 413, map[string]any{"error": "invalid_request", "error_description": "內容太大"})
		return
	}
	f, err := url.ParseQuery(string(body))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid_request", "error_description": "內容格式不對"})
		return
	}
	d["body_format"] = "form"
	d["grant_type"], d["client_id"], d["redirect_uri"] = f.Get("grant_type"), f.Get("client_id"), f.Get("redirect_uri")
	d["resource_present"], d["resource"] = f.Has("resource"), strings.Join(f["resource"], ",")
	d["code_verifier_present"], d["code_present"], d["refresh_token_present"] = f.Get("code_verifier") != "", f.Get("code") != "", f.Get("refresh_token") != ""
	basic := strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "basic ")
	switch {
	case basic:
		d["client_auth_method"] = "basic"
	case f.Has("client_secret"):
		d["client_auth_method"] = "post"
	default:
		d["client_auth_method"] = "none"
	}
	// 公開用戶端(token_endpoint_auth_method=none):帶了密鑰或 Basic 認證都不收
	if basic || f.Has("client_secret") {
		writeJSON(w, 401, map[string]any{"error": "invalid_client", "error_description": "這是公開用戶端(token_endpoint_auth_method=none)"})
		return
	}
	resp, obs, err := h.Svc.OAuthTokenObserved(service.OAuthTokenRequest{
		GrantType: f.Get("grant_type"), ClientID: f.Get("client_id"), Code: f.Get("code"), RedirectURI: f.Get("redirect_uri"),
		CodeVerifier: f.Get("code_verifier"), RefreshToken: f.Get("refresh_token"), Scope: f.Get("scope"), Resources: f["resource"],
	}, service.ClientIP(r))
	d["result"] = obs.Result
	if f.Get("grant_type") == "refresh_token" {
		d["access_remaining_s_at_refresh"] = obs.AccessRemainingS
	}
	if err != nil {
		jsonError(w, err)
		return
	}
	d["expires_in"] = resp.ExpiresIn
	writeJSON(w, 200, resp, "Pragma", "no-cache")
}

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
.opt ul{margin:6px 0 0 18px;padding:0;font-size:14px;color:var(--fg2)}`

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
{{if .Evoleap}}<button class="p wide" type="submit" name="via" value="evoleap">用 evoleap 登入並允許</button>
<p class="note">會前往 evoleap 登入(Google),完成後自動回到這裡並授權;不需要配對碼。</p>
<details{{if .PairOpen}} open{{end}}><summary>改用配對碼</summary>
<label for="pc">配對碼(在已登入的 omixor 的「手機配對」產生,5 分鐘內有效、只能用一次)</label>
<input id="pc" type="text" name="pairing_code" inputmode="text" autocapitalize="characters" autocomplete="off" autocorrect="off" spellcheck="false" maxlength="16" placeholder="XXXX-XXXX">
<div class="row"><button class="p" type="submit" name="action" value="allow">用配對碼允許</button></div></details>
<div class="row"><button type="submit" name="action" value="deny">拒絕</button></div>
{{else}}<label for="pc">配對碼(在已登入的 omixor 的「手機配對」產生,5 分鐘內有效、只能用一次)</label>
<input id="pc" type="text" name="pairing_code" inputmode="text" autocapitalize="characters" autocomplete="off" autocorrect="off" spellcheck="false" maxlength="16" placeholder="XXXX-XXXX" autofocus>
<div class="row"><button type="submit" name="action" value="deny">拒絕</button><button class="p" type="submit" name="action" value="allow">允許</button></div>
{{end}}</form>{{end}}
</main></body></html>
`))

type consentView struct {
	Title, Fatal, Error                                     string
	ClientName, RedirectHost, Org, Scope                    string
	CSRF, ClientID, RedirectURI, Challenge, State, Resource string
	RequestedScope                                          string
	Account, WriteRequested                                 bool
	AdminRequested, AdminDenied                             bool // AdminRequested:客戶端要求 mcp:admin(提供「組織管理員」選項);AdminDenied:剛剛證明身分的使用者不是任何組織的管理員(選項停用)
	Evoleap, PairOpen                                       bool // Evoleap:提供 evoleap 登入按鈕;PairOpen:配對碼區預設展開(剛輸錯)
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

func (h *Handler) renderFatal(w http.ResponseWriter, status int, msg string) {
	pageHeaders(w, "")
	w.WriteHeader(status)
	_ = consentTmpl.Execute(w, consentView{Title: "授權錯誤", Fatal: msg})
}

func (h *Handler) renderConsent(w http.ResponseWriter, status int, c *service.OAuthAuthChecked, req service.OAuthAuthRequest, csrf, errMsg string, adminDenied ...bool) {
	host, origin := "?", ""
	if u, err := url.Parse(c.RedirectURI); err == nil {
		host, origin = u.Host, u.Scheme+"://"+u.Host
	}
	evo := h.Svc.OAuthEvoleapEnabled()
	fa := origin
	if co := h.Svc.OAuthEvoleapConsoleOrigin(); co != "" && co != origin { // 按 evoleap 按鈕後表單會 303 導去 evoleap 核准頁,form-action 也要允許那個來源
		fa += " " + co
	}
	pageHeaders(w, fa)
	w.WriteHeader(status)
	_ = consentTmpl.Execute(w, consentView{Title: "授權確認", Error: errMsg, ClientName: c.Client.Name, RedirectHost: host, Org: c.Org, Scope: c.Scope, Account: c.Account, WriteRequested: c.WriteRequested, AdminRequested: c.AdminRequested, AdminDenied: len(adminDenied) > 0 && adminDenied[0],
		CSRF: csrf, ClientID: c.Client.ID, RedirectURI: c.RedirectURI, Challenge: req.CodeChallenge, State: req.State, Resource: c.Resource,
		RequestedScope: req.Scope, Evoleap: evo, PairOpen: errMsg != ""})
}

func (h *Handler) authRequest(get func(string) string, all func(string) []string) service.OAuthAuthRequest {
	req := service.OAuthAuthRequest{
		ClientID: get("client_id"), RedirectURI: get("redirect_uri"), ResponseType: get("response_type"),
		CodeChallenge: get("code_challenge"), CodeChallengeMethod: get("code_challenge_method"),
		State: get("state"), Scope: get("scope"), Resources: all("resource"),
	}
	if len(req.Resources) == 1 {
		req.Org, req.Account = h.resourceOf(req.Resources[0])
	}
	return req
}

// redirectTo 303 導回 redirect_uri(已經驗證過是註冊的);params 的空值不帶
func redirectTo(w http.ResponseWriter, redirectURI string, kv ...string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect", 500)
		return
	}
	q := u.Query()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	u.RawQuery = q.Encode()
	w.Header().Set("Location", u.String())
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(303)
}

// authError 授權請求的錯誤:client_id 或 redirect_uri 不可信 → 直接顯示;其他導回 redirect_uri?error=…
func (h *Handler) authError(w http.ResponseWriter, r *http.Request, req service.OAuthAuthRequest, err error) {
	var oe *service.OAuthError
	if !errors.As(err, &oe) {
		h.renderFatal(w, 500, "伺服器錯誤,請稍後再試")
		return
	}
	detail(r)["error"] = oe.Code
	if oe.NoRedirect {
		h.renderFatal(w, oe.Status, oe.Desc)
		return
	}
	redirectTo(w, req.RedirectURI, "error", oe.Code, "error_description", oe.Desc, "state", req.State)
}

func (h *Handler) authorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := h.authRequest(q.Get, func(k string) []string { return q[k] })
	d := detail(r)
	d["client_id"], d["redirect_uri"], d["response_type"], d["scope"] = req.ClientID, req.RedirectURI, req.ResponseType, req.Scope
	d["resource_present"], d["resource"] = q.Has("resource"), strings.Join(req.Resources, ",")
	d["code_challenge_method"], d["code_challenge_present"], d["state_present"] = req.CodeChallengeMethod, req.CodeChallenge != "", req.State != ""
	chk, err := h.Svc.CheckOAuthAuthorize(req)
	if err == nil && len(req.State) > 512 {
		err = &service.OAuthError{Status: 400, Code: "invalid_request", Desc: "state 太長"}
	}
	if err != nil {
		h.authError(w, r, req, err)
		return
	}
	csrf, err := h.Svc.NewOAuthCSRF(chk, req.CodeChallenge, service.ClientIP(r))
	if err != nil {
		var oe *service.OAuthError
		if errors.As(err, &oe) {
			h.renderFatal(w, oe.Status, oe.Desc)
		} else {
			h.renderFatal(w, 500, "伺服器錯誤,請稍後再試")
		}
		return
	}
	h.renderConsent(w, 200, chk, req, csrf, "")
}

func (h *Handler) authorizePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	if err != nil {
		h.renderFatal(w, 413, "請求太大")
		return
	}
	f, err := url.ParseQuery(string(body))
	if err != nil {
		h.renderFatal(w, 400, "請求格式不對")
		return
	}
	req := h.authRequest(f.Get, func(k string) []string { return f[k] })
	d := detail(r)
	d["txn_valid"] = false
	d["client_id"], d["redirect_uri"] = req.ClientID, req.RedirectURI
	chk, err := h.Svc.CheckOAuthAuthorize(req)
	if err == nil && len(req.State) > 512 {
		err = &service.OAuthError{Status: 400, Code: "invalid_request", Desc: "state 太長"}
	}
	if err != nil {
		h.authError(w, r, req, err)
		return
	}
	// CSRF:這一次載入的 token,綁定這一次授權請求(client、resource、redirect_uri、code_challenge、要求的 scope 範圍),只能用一次(驗證失敗不導回,請使用者重新開始)
	if !h.Svc.VerifyOAuthCSRF(f.Get("csrf"), chk, req.CodeChallenge) {
		d["pairing_result"] = "bad_csrf"
		h.renderFatal(w, 403, "這個頁面已失效(逾時或重複送出),請回到 Claude 重新開始連線")
		return
	}
	d["txn_valid"] = true
	if f.Get("action") == "deny" {
		d["pairing_result"] = "denied"
		redirectTo(w, chk.RedirectURI, "error", "access_denied", "error_description", "使用者拒絕授權", "state", req.State)
		return
	}
	// 只有表單明確送 grant=write 才是讀寫(其他值、沒送都是唯讀);客戶端沒有要求 mcp:write 卻送 grant=write 由 service 拒絕(invalid_scope)
	grant := service.OAuthGrantRead
	switch f.Get("grant") {
	case "write":
		grant = service.OAuthGrantWrite
	case "admin":
		grant = service.OAuthGrantAdmin
	}
	d["grant_write"] = grant != service.OAuthGrantRead
	d["grant"] = grant
	// 「用 evoleap 登入並允許」:沒填配對碼才走這條(在配對碼欄按 Enter 時瀏覽器會用第一個送出鈕,也就是 evoleap 鈕;有填配對碼就當配對碼)
	if f.Get("via") == "evoleap" && strings.TrimSpace(f.Get("pairing_code")) == "" {
		h.startEvoleap(w, r, f, chk, req, grant, d)
		return
	}
	code, err := h.Svc.ApproveOAuthGrant(chk, req.CodeChallenge, f.Get("pairing_code"), service.ClientIP(r), grant)
	if err != nil {
		var oe *service.OAuthError
		if !errors.As(err, &oe) {
			h.renderFatal(w, 500, "伺服器錯誤,請稍後再試")
			return
		}
		// 配對碼錯:再發一個新的 CSRF token,重新顯示同意畫面(一律同一句話,不說是不存在、過期或用過)
		d["error"] = oe.Code
		switch {
		case oe == service.ErrOAuthPairInvalid:
			d["pairing_result"] = "wrong"
		case oe.Reason != "":
			d["pairing_result"] = oe.Reason
		case oe.Code == "invalid_scope":
			d["pairing_result"] = "bad_scope"
		}
		if oe == service.ErrOAuthNotOrgAdmin { // 身分已證明、但不是任何組織的管理員:重新顯示同意畫面,管理員選項停用(配對碼已消耗,要重新產生)
			csrf, cerr := h.Svc.NewOAuthCSRF(chk, req.CodeChallenge, service.ClientIP(r))
			if cerr != nil {
				h.renderFatal(w, 429, "太頻繁,請稍後再試")
				return
			}
			h.renderConsent(w, 403, chk, req, csrf, oe.Desc+"。若用配對碼,請重新產生一個。", true)
			return
		}
		if oe == service.ErrOAuthPairInvalid {
			csrf, cerr := h.Svc.NewOAuthCSRF(chk, req.CodeChallenge, service.ClientIP(r))
			if cerr != nil {
				h.renderFatal(w, 429, "太頻繁,請稍後再試")
				return
			}
			h.renderConsent(w, 403, chk, req, csrf, oe.Desc)
			return
		}
		if oe.Status == 429 {
			w.Header().Set("Retry-After", "60")
		}
		h.renderFatal(w, oe.Status, oe.Desc)
		return
	}
	d["pairing_result"] = "ok"
	redirectTo(w, chk.RedirectURI, "code", code, "state", req.State)
}

// ---------------- evoleap 登入 ----------------

const evoCookie = "omixor_oauth_evo"

func (h *Handler) evoCallbackURL() string { return h.Public + "/oauth/evoleap/callback" }

func (h *Handler) evoCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: evoCookie, Value: value, Path: "/oauth", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(h.Public, "https://")}
}

// startEvoleap 授權請求與 CSRF 已經驗證過:開始 evoleap 登入,設綁定瀏覽器的 cookie,303 導去 evoleap 核准頁
func (h *Handler) startEvoleap(w http.ResponseWriter, r *http.Request, f url.Values, chk *service.OAuthAuthChecked, req service.OAuthAuthRequest, grant string, d map[string]any) {
	d["via"] = "evoleap"
	loginURL, binding, err := h.Svc.StartOAuthEvoleapGrant(chk, req.CodeChallenge, req.State, h.evoCallbackURL(), grant)
	if err != nil {
		var oe *service.OAuthError
		if !errors.As(err, &oe) {
			h.renderFatal(w, 500, "伺服器錯誤,請稍後再試")
			return
		}
		d["error"], d["pairing_result"] = oe.Code, oe.Reason
		if oe.Reason == "evoleap_unavailable" { // 連不到 evoleap:CSRF 已用掉,發新的重新顯示同意畫面(可改用配對碼)
			csrf, cerr := h.Svc.NewOAuthCSRF(chk, req.CodeChallenge, service.ClientIP(r))
			if cerr != nil {
				h.renderFatal(w, 429, "太頻繁,請稍後再試")
				return
			}
			h.renderConsent(w, oe.Status, chk, req, csrf, oe.Desc)
			return
		}
		h.renderFatal(w, oe.Status, oe.Desc)
		return
	}
	d["pairing_result"] = "evoleap_started"
	http.SetCookie(w, h.evoCookie(binding, int(service.OAuthEvoleapTTL.Seconds())))
	w.Header().Set("Location", loginURL)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(303)
}

// evoleapCallback evoleap 核准後導回:成功 303 導回客戶端 redirect_uri?code=…&state=…;
// 轉址還不可信時(id 不認得、cookie 不對、逾時、重放…)一律同一個通用錯誤頁;之後的失敗導回 redirect_uri?error=…
func (h *Handler) evoleapCallback(w http.ResponseWriter, r *http.Request) {
	d := detail(r)
	d["via"] = "evoleap"
	q := r.URL.Query()
	d["id_present"], d["code_present"] = q.Get("id") != "", q.Get("code") != ""
	binding := ""
	if c, err := r.Cookie(evoCookie); err == nil {
		binding = c.Value
	}
	d["cookie_present"] = binding != ""
	http.SetCookie(w, h.evoCookie("", -1)) // 綁定密鑰只用一次
	res, err := h.Svc.CompleteOAuthEvoleap(q.Get("id"), q.Get("code"), binding)
	if err != nil {
		var oe *service.OAuthError
		if !errors.As(err, &oe) {
			h.renderFatal(w, 500, "伺服器錯誤,請稍後再試")
			return
		}
		d["error"], d["pairing_result"] = oe.Code, oe.Reason
		if res != nil && res.RedirectURI != "" && !oe.NoRedirect {
			redirectTo(w, res.RedirectURI, "error", oe.Code, "error_description", oe.Desc, "state", res.State)
			return
		}
		h.renderFatal(w, oe.Status, oe.Desc)
		return
	}
	d["pairing_result"] = "ok"
	redirectTo(w, res.RedirectURI, "code", res.Code, "state", res.State)
}
