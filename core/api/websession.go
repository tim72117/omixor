package api

// websession.go 宿主(console)在瀏覽器同一個來源掛核心 API 時用的選項:cookie 裝 web session(service/websession.go)、
// CSRF 檢查,以及宿主自己的限制。API.Session 是 nil(omixor server)時完全不看 cookie,行為不變。
//
// 規則:
//   - 有 Authorization 標頭:只看它(omixor token),不看 cookie,不需要 CSRF 標頭
//   - 否則問 Local(宿主的免登入模式;有身分就用它,殘留的 cookie 不看),沒有才看 cookie
//   - 這兩種(瀏覽器自動帶上或不用憑證)的非 GET / HEAD 要 X-Omixor-CSRF: 1,有 Origin 時主機與埠要和 Host 相同
//   - 驗證通過後一律過 Admit(宿主的登入限制;可以換成調整過的身分)

import (
	"net/http"
	"net/url"
	"strings"

	"omixor-core/model"
	"omixor-core/service"
)

// CSRFHeader 用 cookie 認證的寫入要帶的標頭(值是 1);別的網站帶自訂標頭會先被瀏覽器的 preflight 擋下
const CSRFHeader = "X-Omixor-CSRF"

// Session 見檔頭
type Session struct {
	// Cookie 裝 web session 的 cookie 名稱(空白 = 不收 cookie)
	Cookie string
	// Local 沒帶任何憑證時的身分;nil 或回 nil = 沒有(401)
	Local func(r *http.Request) *model.Principal
	// Admit 驗證通過後宿主的限制:回傳這個請求用的身分,或拒絕。cookie = 身分來自 web session cookie。
	// r.Pattern 是比對到的路由,可以依路由決定。nil = 不限制
	Admit func(r *http.Request, p *model.Principal, cookie bool) (*model.Principal, error)
	// NoLoginRoutes 不掛登入端點(evoleap、LDAP、GitLab、手機配對)
	NoLoginRoutes bool
	// NoAgentCredentials 不收角色憑證(Bearer <agentId>:<secret>)
	NoAgentCredentials bool
}

var (
	errCSRF   = &statusErr{403, "跨站請求檢查沒過(要帶 " + CSRFHeader + ": 1,而且來源要是同一個網站)"}
	errNoAuth = &statusErr{401, "請先登入"}

	errWebBearer = &statusErr{403, "網頁版的登入只能在瀏覽器裡使用"}
)

// statusErr 這個套件自己的固定錯誤(給 httperr 取狀態碼與訊息)
type statusErr struct {
	code int
	msg  string
}

func (e *statusErr) Error() string                    { return e.msg }
func (e *statusErr) HTTPError() (int, map[string]any) { return e.code, nil }

func (a *API) principal(r *http.Request) (*model.Principal, error) {
	hdr := r.Header.Get("Authorization")
	tok, _ := strings.CutPrefix(hdr, "Bearer ")
	s := a.Session
	bearer := func() (*model.Principal, error) {
		p, err := a.Svc.Authenticate(tok)
		if err == nil && a.RejectWebBearer && strings.HasPrefix(p.Token.Device, service.WebDevicePrefix) {
			return nil, errWebBearer
		}
		return p, err
	}
	if s == nil {
		return bearer()
	}
	var (
		p      *model.Principal
		err    error
		cookie bool
	)
	if hdr != "" {
		p, err = bearer()
	} else {
		if s.Local != nil {
			p = s.Local(r)
		}
		c := sessionCookie(r, s.Cookie)
		if p == nil && c == "" {
			return nil, errNoAuth
		}
		if !sameSite(r) {
			return nil, errCSRF
		}
		if p == nil {
			cookie = true
			p, err = a.Svc.WebSession(c)
		}
	}
	if err != nil {
		return nil, err
	}
	if s.Admit != nil {
		return s.Admit(r, p, cookie)
	}
	return p, nil
}

func sessionCookie(r *http.Request, name string) string {
	if name == "" {
		return ""
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// sameSite 瀏覽器自動帶憑證的請求:讀取不限;其他要有 CSRFHeader,有 Origin 時要和 Host 相同
func sameSite(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	if r.Header.Get(CSRFHeader) != "1" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}
