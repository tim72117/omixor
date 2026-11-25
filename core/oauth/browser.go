package oauth

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"omixor-core/service"
)

// 同意畫面的表單只能由「同一個瀏覽器、從我們自己的頁面」送出:綁定 cookie(service/oauth_csrf.go)加上來源檢查。所有 provider 共用

// consentCookie 同意畫面的瀏覽器綁定 cookie;同一個瀏覽器的多個分頁共用同一個值(有就沿用),不會互相弄壞
const consentCookie = "omixor_oauth_consent"

var browserSecretRE = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)

// browserSecret 請求帶的綁定值;沒有或格式不對回 ""
func browserSecret(r *http.Request) string {
	if c, err := r.Cookie(consentCookie); err == nil && browserSecretRE.MatchString(c.Value) {
		return c.Value
	}
	return ""
}

// ensureBrowserSecret 沿用或產生綁定值,並(重新)設 cookie,時效跟著 CSRF token
func (h *Handler) ensureBrowserSecret(w http.ResponseWriter, r *http.Request) string {
	v := browserSecret(r)
	if v == "" {
		v = service.RandID("", 24)
	}
	http.SetCookie(w, &http.Cookie{Name: consentCookie, Value: v, Path: "/oauth", MaxAge: int(service.OAuthCSRFTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(h.Public, "https://")})
	return v
}

// originKey scheme://host(小寫、去掉預設埠);解析不了回 ""
func originKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	switch scheme {
	case "https":
		host = strings.TrimSuffix(host, ":443")
	case "http":
		host = strings.TrimSuffix(host, ":80")
	}
	return scheme + "://" + host
}

// crossSite 這個表單送出是不是別的網站發起的:
//   - Sec-Fetch-Site(瀏覽器自己填,網頁改不了)有值時只收 same-origin、none
//   - Origin 有值而且不是 "null" 時,要是對外網址或這個請求的 Host。"null" 不能當成跨站:同意畫面是 Referrer-Policy: no-referrer,
//     自己的表單送出時瀏覽器也是帶 "null";這時靠 Sec-Fetch-Site 與 cookie
//
// 兩個標頭都沒有(舊瀏覽器、非瀏覽器)放行,仍要過 cookie(SameSite=Lax,跨站 POST 不會帶)
func crossSite(r *http.Request, public string) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return true
	}
	o := r.Header.Get("Origin")
	if o == "" || o == "null" {
		return false
	}
	k := originKey(o)
	if k == "" {
		return true
	}
	if k == originKey(public) {
		return false
	}
	u, err := url.Parse(o)
	return err != nil || !strings.EqualFold(u.Host, r.Host)
}
