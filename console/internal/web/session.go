package web

import (
	"net/http"
	"time"

	"omixor-web/internal/auth"
)

// session.go 瀏覽器的登入 cookie(session 本身見 internal/auth/session.go):
//   - omx_session:核心的 web session(一般登入),Path=/api
//   - ops_session:console 自己的 session(沒有資料庫、管理員修復登入),Path=/。
//     有核心時它只認修復登入(升級前的舊登入因此失效,要重新登入)
const (
	webCookie  = "omx_session"
	cookieName = "ops_session"
)

// lookupSession 這個請求的登入者;err = 暫時查不出來(不是沒登入)
func lookupSession(r *http.Request, au *auth.Service) (auth.User, bool, error) {
	own := func() (auth.User, bool) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" {
			return auth.User{}, false
		}
		return au.User(c.Value)
	}
	if !au.CoreSessions() {
		u, ok := own()
		return u, ok, nil
	}
	if c, err := r.Cookie(webCookie); err == nil && c.Value != "" {
		u, ok, err := au.WebUser(c.Value)
		if ok || err != nil {
			return u, ok, err
		}
	}
	if u, ok := own(); ok && u.Repair {
		return u, true, nil
	}
	return auth.User{}, false, nil
}

func sessionUser(r *http.Request, au *auth.Service) (auth.User, bool) {
	u, ok, err := lookupSession(r, au)
	return u, ok && err == nil
}

// setSessionCookie 設其中一種 cookie,另一種清掉
func setSessionCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	path := "/"
	if name == webCookie {
		path = "/api"
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge: maxAge,
	})
	expireCookies(w, name)
}

// expireCookies 清掉登入 cookie(keep 列的除外)
func expireCookies(w http.ResponseWriter, keep ...string) {
	for name, path := range map[string]string{webCookie: "/api", cookieName: "/"} {
		if len(keep) > 0 && keep[0] == name {
			continue
		}
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, Expires: time.Unix(0, 0), HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
}

// endSessions 登出這個瀏覽器帶來的 session
func endSessions(r *http.Request, au *auth.Service) {
	if c, err := r.Cookie(webCookie); err == nil {
		au.EndWebSession(c.Value)
	}
	if c, err := r.Cookie(cookieName); err == nil {
		au.Logout(c.Value)
	}
}
