package web

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"

	"omixor-core/config"
	"omixor-core/httperr"
	"omixor-core/service"
)

// 登入相關的 API(畫面用;session 與 cookie 見 session.go):
//
//	GET  /api/auth/web/status   {mode, user, loggedIn, noAdmins?, denied?, repair?, unavailable?}(不用登入;noAdmins = 啟用 LDAP 但管理員
//	                            帳號名單是空的;denied = 有 session 但不能用的原因;unavailable = 暫時查不出能不能用,仍是登入中)
//	POST /api/auth/web/login    {username, password} → {result: {user}},設 session cookie,不回 token(不用登入)。帳密對但不能用 console
//	                            (不是任何組織的成員、停用、對不上核心使用者)回 403 與原因,不設 cookie(規則見 core/caller_access.go)。
//	                            例外:管理員名單上、對不上核心使用者的帳號回 200 {repair: true, denied},之後只能用認證設定(status 也回 repair)。
//	                            帳密錯 401(同一句);不在 RequiredGroup 401;鎖定或太頻繁 429 帶 Retry-After;目錄或設定問題 503
//	POST /api/auth/web/logout
//	GET  /api/auth/config   認證設定(管理員;不含密碼;env 是被環境變數蓋掉的項目)
//	POST /api/auth/config   {config, bindPassword?, testUsername, testPassword} 存設定(管理員)。
//	                        啟用 LDAP 時管理員帳號名單不能空、要附一組能登入而且在名單裡的帳密(證明設定可用),
//	                        而且登入中的操作者不能把自己移出名單
//	POST /api/auth/test     {config, bindPassword?, username, password} 只測試,不存(管理員)
//
// GET /api/auth/me、POST /api/auth/logout 是核心 REST API 的(見 coreapi.go)

type ctxKey struct{}

func withUser(ctx context.Context, u auth.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func ctxUser(r *http.Request) (auth.User, bool) {
	u, ok := r.Context().Value(ctxKey{}).(auth.User)
	return u, ok
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
		return auth.User{ID: app.Me, Name: app.MeName(), Admin: true}, true
	}
	if u, ok := ctxUser(r); ok {
		return u, true
	}
	return sessionUser(r, au)
}

func registerAuth(mux *http.ServeMux, app *core.App, au *auth.Service) {
	if au == nil {
		au = auth.New(nil)
	}
	mux.HandleFunc("GET /api/auth/web/status", func(w http.ResponseWriter, r *http.Request) {
		cfg := au.Config()
		out := map[string]any{"mode": cfg.Mode}
		if cfg.Mode == "ldap" && len(cfg.AdminList()) == 0 {
			out["noAdmins"] = true
		}
		if au.Enabled() {
			if _, _, err := lookupSession(r, au); err != nil { // 查不出有沒有登入:當作登入中,畫面顯示原因讓他重試
				out["loggedIn"], out["unavailable"] = true, err.Error()
				writeJSON(w, 200, out)
				return
			}
		}
		u, ok := currentUser(r, app, au)
		var denied error
		repair := false
		if ok && au.Enabled() { // 有 session 但現在不能用(停用、移出組織):當作沒登入
			who := newCaller(au, u)
			if _, denied = who.Access(); denied != nil {
				if httperr.Classify(denied).Status == http.StatusForbidden {
					ok, repair = false, who.CanRepair(denied)
					out["denied"] = denied.Error()
				} else { // 暫時查不出來:還是登入著的,畫面顯示原因讓他重試
					out["unavailable"] = denied.Error()
				}
			} else if u.Repair { // 修復登入用完了(帳號已經對得上):要重新登入
				ok = false
			}
		}
		out["loggedIn"] = ok
		if repair {
			out["repair"] = true
		}
		if ok || repair {
			out["user"] = u
		}
		writeJSON(w, 200, out)
	})

	mux.HandleFunc("POST /api/auth/web/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password string }
		if err := readInto(r, &in); err != nil {
			badRequest(w, err)
			return
		}
		u, err := au.Verify(in.Username, in.Password, service.ClientIP(r))
		if err != nil {
			status := authLoginStatus(err)
			if status == 429 { // 被鎖:告訴用戶端多久後再試
				if sec, ok := httperr.Classify(err).Data["retryAfter"].(int); ok && sec > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(sec))
				}
			}
			writeErr(w, r, status, err)
			return
		}
		// 帳密對了還要對得上核心使用者、沒停用、是某個組織的成員(或管理員);不行就不建 session。
		// 第一次登入的人這裡會建好核心使用者(和 OAuth 授權同一位),管理員才能把他加進組織
		var repair error // 管理員名單上、對不上使用者:給修復用的 session,只能改認證設定
		who := newCaller(au, u)
		if err := who.CheckLogin(); err != nil {
			if !who.CanRepair(err) {
				writeFail(w, r, err)
				return
			}
			repair = err
		}
		maxAge := au.Config().SessionHours * 3600
		if repair == nil && au.CoreSessions() {
			cu := who.CoreUser()
			if cu == nil {
				writeFail(w, r, core.StatusError{Status: 500, Message: "登入暫時失敗,請稍後再試;若持續發生請聯絡管理員"})
				return
			}
			tok, err := au.IssueWebSession(cu.ID, u.ID)
			if err != nil {
				writeFail(w, r, err)
				return
			}
			setSessionCookie(w, r, webCookie, tok, maxAge)
		} else {
			tok, err := au.StartSession(u, repair != nil)
			if err != nil {
				writeFail(w, r, err)
				return
			}
			setSessionCookie(w, r, cookieName, tok, maxAge)
		}
		if repair != nil {
			log.Printf("管理員 %s 對不到 omixor 使用者,這次登入只能改認證設定:%v", u.ID, repair)
			writeJSON(w, 200, map[string]any{"result": map[string]any{"user": u}, "repair": true, "denied": repair.Error()})
			return
		}
		app.Mu.Lock()
		app.Log(core.Person(u.ID), "person.badge.key", "登入 omixor 網頁版", "settings")
		app.Mu.Unlock()
		writeJSON(w, 200, map[string]any{"result": map[string]any{"user": u}})
	})

	mux.HandleFunc("POST /api/auth/web/logout", func(w http.ResponseWriter, r *http.Request) {
		endSessions(r, au)
		expireCookies(w)
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
		writeJSON(w, 200, authConfigView(au.Config()))
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
			badRequest(w, err)
			return
		}
		u, err := test(in, in.Username, in.Password)
		if err != nil {
			writeErrDetail(w, r, auth.StatusOf(err), err)
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
			badRequest(w, err)
			return
		}
		if in.Config.Mode != "ldap" && r.Context().Value(repairKey{}) != nil { // 關掉登入會讓他拿到全部資料:修復用的登入不行
			writeJSON(w, 403, map[string]any{"error": "這次登入只能修認證設定,不能關閉登入;要關閉請在主機上用 OMIXOR_AUTH=off", "forbidden": true})
			return
		}
		if in.Config.Mode == "ldap" {
			if len(in.Config.AdminList()) == 0 {
				writeJSON(w, 400, map[string]any{"error": "要填管理員帳號名單(或設 OMIXOR_LDAP_ADMINS);名單空白就沒有人是管理員,所以沒有存"})
				return
			}
			u, err := test(in, in.TestUsername, in.TestPassword)
			if err != nil {
				writeErrPrefix(w, r, auth.StatusOf(err), "用測試帳號登入失敗,設定沒有存:", err, true)
				return
			}
			if !u.Admin {
				writeJSON(w, 400, map[string]any{"error": "測試帳號不在管理員帳號名單裡,無法確認存了之後還有人能改設定,所以沒有存"})
				return
			}
			if op, ok := currentUser(r, app, au); ok && au.Enabled() && !in.Config.IsAdminAccount(op.ID) {
				writeJSON(w, 400, map[string]any{"error": "不能把自己移出管理員帳號名單,所以沒有存"})
				return
			}
			// 測試帳號照存了之後的設定要真的能登入(對得上 omixor 使用者、沒停用),不然存了就沒人進得來
			who := core.NewCaller(u.ID, u.Name, u.Email, u.Admin, au.UserSettings(in.Config))
			if err := who.CheckLogin(); err != nil {
				writeJSON(w, 400, map[string]any{"error": "測試帳號依這份設定不能登入,設定沒有存。" + err.Error()})
				return
			}
		}
		if err := au.Save(in.Config, in.BindPassword); err != nil {
			writeErr(w, r, failStatus(err), err)
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
		writeJSON(w, 200, map[string]any{"result": authConfigView(au.Config())})
	})
}

// authConfigView 認證設定加上被環境變數蓋掉的項目(畫面顯示用)
func authConfigView(c auth.Config) any {
	return struct {
		auth.Config
		Env map[string]any `json:"env"`
	}{c, authEnv()}
}

func authEnv() map[string]any {
	out := map[string]any{}
	if a := auth.EnvAdmins(); a != nil {
		out["admins"] = a
	}
	cur := config.Current()
	if v := cur.Str(config.LDAPEmailDomain); v != "" {
		out["emailDomain"] = v
	}
	if cur.IsSet(config.LDAPTrustMail) {
		out["trustMail"] = cur.Bool(config.LDAPTrustMail)
	}
	return out
}

func readInto(r *http.Request, v any) error {
	args, err := readArgs(r)
	if err != nil {
		return err
	}
	return core.ReadInto(args, v)
}
