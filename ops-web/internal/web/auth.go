package web

import (
	"context"
	"net"
	"net/http"
	"time"

	"opsweb/internal/auth"
	"opsweb/internal/core"
)

// 登入相關的 API:
//
//	GET  /api/auth/status   {mode, user, loggedIn}(不用登入)
//	POST /api/auth/login    {username, password} → 設 session cookie(不用登入)
//	POST /api/auth/logout
//	GET  /api/auth/config   認證設定(管理員;不含密碼)
//	POST /api/auth/config   {config, bindPassword?, testUsername, testPassword} 存設定(管理員)。
//	                        啟用 LDAP 時一定要附一組能登入、而且是管理員的帳密,證明設定可用,避免把自己鎖在外面
//	POST /api/auth/test     {config, bindPassword?, username, password} 只測試,不存(管理員)

const cookieName = "ops_session"

type ctxKey struct{}

func withUser(ctx context.Context, u auth.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func ctxUser(r *http.Request) (auth.User, bool) {
	u, ok := r.Context().Value(ctxKey{}).(auth.User)
	return u, ok
}

func sessionUser(r *http.Request, au *auth.Service) (auth.User, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return auth.User{}, false
	}
	return au.User(c.Value)
}

// userID 這個請求是誰:有登入用登入者,否則是 app.Me(本機模式)
func userID(r *http.Request, app *core.App) string {
	if u, ok := ctxUser(r); ok {
		return u.ID
	}
	return app.Me
}

// currentUser:登入者;沒啟用登入時是本機使用者,視為管理員
func currentUser(r *http.Request, app *core.App, au *auth.Service) (auth.User, bool) {
	if au == nil || !au.Enabled() {
		app.Mu.Lock()
		defer app.Mu.Unlock()
		return auth.User{ID: app.Me, Name: app.Name(core.Person(app.Me)), Admin: true}, true
	}
	if u, ok := ctxUser(r); ok {
		return u, true
	}
	return sessionUser(r, au)
}

func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func registerAuth(mux *http.ServeMux, app *core.App, au *auth.Service) {
	if au == nil {
		au = auth.New(nil)
	}
	mux.HandleFunc("GET /api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		cfg := au.Config()
		u, ok := currentUser(r, app, au)
		out := map[string]any{"mode": cfg.Mode, "loggedIn": ok}
		if ok {
			out["user"] = u
		}
		writeJSON(w, 200, out)
	})

	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password string }
		if err := readInto(r, &in); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		tok, u, err := au.Login(in.Username, in.Password, remoteIP(r))
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": err.Error()})
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: cookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
			Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			MaxAge: au.Config().SessionHours * 3600,
		})
		app.Mu.Lock()
		app.Log(core.Person(u.ID), "person.badge.key", "登入 omixor 網頁版", "settings")
		app.Mu.Unlock()
		writeJSON(w, 200, map[string]any{"result": u})
	})

	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(cookieName); err == nil {
			au.Logout(c.Value)
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0), HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writeJSON(w, 200, map[string]any{"result": true})
	})

	admin := func(w http.ResponseWriter, r *http.Request) bool {
		u, ok := currentUser(r, app, au)
		if !ok || !u.Admin {
			writeJSON(w, 403, map[string]any{"error": "只有管理員可以改認證設定"})
			return false
		}
		return true
	}

	mux.HandleFunc("GET /api/auth/config", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		writeJSON(w, 200, au.Config())
	})

	type configIn struct {
		Config       auth.Config `json:"config"`
		BindPassword *string     `json:"bindPassword"`
		TestUsername string      `json:"testUsername"`
		TestPassword string      `json:"testPassword"`
		Username     string      `json:"username"`
		Password     string      `json:"password"`
	}

	// 測試時用表單上的服務帳號密碼(還沒存);沒給就用已存的
	test := func(in configIn, user, pw string) (auth.User, error) {
		return au.AuthenticateWith(in.Config, in.BindPassword, user, pw)
	}

	mux.HandleFunc("POST /api/auth/test", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		var in configIn
		if err := readInto(r, &in); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		u, err := test(in, in.Username, in.Password)
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"result": u})
	})

	mux.HandleFunc("POST /api/auth/config", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		var in configIn
		if err := readInto(r, &in); err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		if in.Config.Mode == "ldap" {
			u, err := test(in, in.TestUsername, in.TestPassword)
			if err != nil {
				writeJSON(w, 200, map[string]any{"error": "用測試帳號登入失敗,設定沒有存:" + err.Error()})
				return
			}
			if !u.Admin {
				writeJSON(w, 200, map[string]any{"error": "測試帳號不在管理員群組,存了之後就沒有人能改設定,所以沒有存"})
				return
			}
		}
		if err := au.Save(in.Config, in.BindPassword); err != nil {
			writeJSON(w, 200, map[string]any{"error": err.Error()})
			return
		}
		u, _ := currentUser(r, app, au)
		app.Mu.Lock()
		text := "關閉網頁版登入"
		if in.Config.Mode == "ldap" {
			text = "啟用網頁版 LDAP 登入"
		}
		app.Log(core.Person(u.ID), "lock", text, "settings")
		app.Mu.Unlock()
		writeJSON(w, 200, map[string]any{"result": au.Config()})
	})
}

func readInto(r *http.Request, v any) error {
	args, err := readArgs(r)
	if err != nil {
		return err
	}
	return core.ReadInto(args, v)
}
