package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"

	"omixor-core/httperr"
)

// 啟用 LDAP 時:組織成員身分,以及每位登入者各自的目前組織(誰能登入見 core/caller_access.go,寫入另外由 core 的 Caller 擋)。
//
// API 契約:
//   - 畫面把自己選的組織放在每個請求的 X-Omixor-Org 標頭(存在瀏覽器,伺服器不記;每個分頁獨立)
//   - 沒帶:用預設(系統工作組織,不是成員就用清單裡第一個自己的組織)
//   - 帶了但不能用(移出、組織刪了):409 {orgChanged: true, org: <預設>},畫面改用它;view orgs、account 照常回預設的
//   - view orgs 回 {orgs(只有自己的), current(這個請求用的), perUser: true, system}
//   - org_switch 只回確認,不改伺服器狀態;系統工作組織用 system_org_set 換
//   - /api/issues/{org} 的組織在路徑上,要是那個組織的成員
//
// 組織不是系統工作組織時,核心資料用一次性的 App(core.Scoped);其他功能 403 {notSystemOrg: true}(分類見 orgscope.go)。
// 用原本的 App(系統工作組織)處理的請求期間不會換系統工作組織(讀寫的就是檢查過的那個;鎖的規則見 core/system_gate.go)。
//
// 沒啟用登入、API token(沒有登入者):不看 X-Omixor-Org,行為不變

const orgHeader = "X-Omixor-Org"

// loginOnly 不做檢查的 /api/auth 路徑(登入前就能呼叫的與登出)
var loginOnly = map[string]bool{"/api/auth/web/status": true, "/api/auth/web/login": true, "/api/auth/web/logout": true}

// repairPaths 管理員名單上、對不上核心使用者的帳號唯一能用的路徑(登入、登出、status 本來就能用)
var repairPaths = map[string]bool{"GET /api/auth/config": true, "POST /api/auth/config": true, "POST /api/auth/test": true}

type repairKey struct{}

// staleOK 帶來的組織不能用時,照預設組織回應的請求(畫面靠它得知現在是哪個組織)
var staleOK = map[string]bool{"GET /api/view/orgs": true, "GET /api/view/account": true}

type (
	callerKey struct{}
	scopeKey  struct{}
)

// requestScope 這個請求的登入者能讀寫什麼、用哪個 App
type requestScope struct {
	access core.Access
	app    *core.App
}

func scopeOf(r *http.Request) (requestScope, bool) {
	s, ok := r.Context().Value(scopeKey{}).(requestScope)
	return s, ok
}

// appOf 這個請求要用的 App(登入者選的組織;沒有登入者就是原本的)
func appOf(r *http.Request, app *core.App) *core.App {
	if s, ok := scopeOf(r); ok && s.app != nil {
		return s.app
	}
	return app
}

func membershipGate(next http.Handler, app *core.App, au *auth.Service) http.Handler {
	systemLock := app.SystemOrgLock() // 讀鎖:登入者用原本 App 處理的請求(和 MCP 入口共用;寫鎖在 core 換組織時拿)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || loginOnly[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		who := callerOf(r, au)
		if who == nil { // 系統身分:原本的行為
			next.ServeHTTP(w, r)
			return
		}
		access, err := who.Access()
		if err != nil {
			if repairPaths[r.Method+" "+r.URL.Path] && who.CanRepair(err) { // 只有認證設定
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), repairKey{}, true)))
				return
			}
			denyAccess(w, err)
			return
		}
		if u, ok := ctxUser(r); ok && u.Repair { // 修復登入用完了(帳號已經對得上):要重新登入
			writeJSON(w, 401, map[string]any{"error": "請重新登入", "login": true})
			return
		}
		scope := requestScope{access: access}
		serve := func() {
			ctx := context.WithValue(contextWithCaller(r.Context(), who), scopeKey{}, scope)
			next.ServeHTTP(w, r.WithContext(ctx))
		}
		notMember := func() {
			writeJSON(w, 403, map[string]any{"error": "你不是這個組織的成員", "forbidden": true, "notMember": true})
		}
		kind := routeScope(r.Method, r.URL.Path)
		switch kind {
		case scopeHost, scopeAttachment:
			serve()
			return
		case scopePath:
			org, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/issues/"), "/")
			if !access.Member(org) {
				notMember()
				return
			}
			serve()
			return
		case scopeOffice:
			if !access.Member(app.SystemOrgID()) {
				notMember()
				return
			}
			if r.URL.Path == "/api/office/events" { // 長連線:期間定期重查,移出、停用、換系統工作組織後斷線
				ctx, cancel := context.WithCancel(r.Context())
				defer cancel()
				r = r.WithContext(ctx)
				go recheckOffice(ctx, cancel, r, app, au, app.SystemOrgID())
			}
			serve()
			return
		}
		// 虛擬辦公室的工具:整台一份、不讀組織資料,而且 office_move 可能等很久,不拿鎖
		unlocked := strings.HasPrefix(r.URL.Path, "/api/tool/office_")
		// 拿鎖之前先把請求內容讀完:慢慢送內容的請求不能拿著鎖
		if err := bufferBody(r); err != nil {
			badRequest(w, err)
			return
		}
		// scopeOrg、scopeSystem:決定這個請求的組織
		pin := &gatePin{lock: systemLock, app: app}
		pin.acquire()
		defer pin.release()
		system := app.SystemOrgID()
		org, ok := access.PickOrg(r.Header.Get(orgHeader), system, app.OrgList())
		if !ok && !staleOK[r.Method+" "+r.URL.Path] {
			pin.release()
			writeJSON(w, 409, map[string]any{"error": "你選的組織已經不能使用(被移出或刪除),請重新整理", "orgChanged": true, "org": org})
			return
		}
		if org == "" || org == system { // 系統工作組織(或還沒有任何組織):原本的 App,期間不換組織
			if org == "" && !access.Admin {
				pin.release()
				notMember()
				return
			}
			if unlocked {
				pin.release()
				serve()
				return
			}
			// 回應先放在記憶體,放掉鎖再送:收得很慢的用戶端不能拖住鎖
			buf := &bufferedResponse{header: w.Header()}
			ctx := context.WithValue(contextWithCaller(r.Context(), who), scopeKey{}, scope)
			next.ServeHTTP(buf, r.WithContext(context.WithValue(ctx, pinKey{}, pin)))
			pin.release()
			buf.sendTo(w)
			return
		}
		pin.release()
		if kind != scopeOrg {
			writeJSON(w, 403, map[string]any{"error": "這個功能還沒有分組織,只能在系統工作組織使用;要用請切換到那個組織,或請管理員換系統工作組織",
				"forbidden": true, "notSystemOrg": true})
			return
		}
		s, release := app.Scoped(org, r.Method == http.MethodGet)
		defer release()
		scope.app = s
		serve()
	})
}

// officeRecheck 虛擬辦公室的長連線多久重查一次
var officeRecheck = 15 * time.Second

// recheckOffice 登入失效、不能再讀系統工作組織,或系統工作組織換了就取消連線。暫時查不出來的下次再查
func recheckOffice(ctx context.Context, cancel context.CancelFunc, r *http.Request, app *core.App, au *auth.Service, org string) {
	t := time.NewTicker(officeRecheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		u, ok, serr := lookupSession(r, au)
		if serr != nil && app.SystemOrgID() == org { // 暫時查不出來:下次再查
			continue
		}
		if !ok || app.SystemOrgID() != org {
			cancel()
			return
		}
		access, err := newCaller(au, u).Access() // 每次建新的:Caller 會記住讀到的使用者
		if (err == nil && !access.Member(org)) || (err != nil && httperr.Classify(err).Status == http.StatusForbidden) {
			cancel()
			return
		}
	}
}

type pinKey struct{}

// gatePin 一個請求拿著的系統工作組織讀鎖(core.SystemPin)
type gatePin struct {
	lock *core.SystemGate
	app  *core.App
	held bool
}

func (p *gatePin) acquire() {
	if !p.held {
		p.lock.RLock()
		p.held = true
	}
}

func (p *gatePin) release() {
	if p.held {
		p.held = false
		p.lock.RUnlock()
	}
}

// Away 見 core.SystemPin
func (p *gatePin) Away(wait func()) bool {
	before := p.app.SystemOrgID()
	p.release()
	defer p.acquire()
	wait()
	p.acquire()
	return p.app.SystemOrgID() == before
}

// bufferBody 把請求內容讀進記憶體換回 r.Body(上限同各 handler 自己的上限)
func bufferBody(r *http.Request) error {
	if r.Method != http.MethodPost || r.Body == nil {
		return nil
	}
	limit := int64(4 << 20)
	if r.URL.Path == "/api/attachments" {
		limit = core.MaxAttachment*4/3 + 4096
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return nil
}

// bufferedResponse 暫存 handler 的回應(標頭直接寫在原本的 ResponseWriter 上)
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.WriteHeader(http.StatusOK)
	return b.body.Write(p)
}

func (b *bufferedResponse) sendTo(w http.ResponseWriter) {
	if b.status != 0 {
		w.WriteHeader(b.status)
	}
	_, _ = w.Write(b.body.Bytes())
}

// denyAccess 登入者現在不能用網頁:403 的原因回 401 + login(畫面回登入頁);查不出來照原狀態碼
func denyAccess(w http.ResponseWriter, err error) {
	in := httperr.Classify(err)
	if in.Status == 403 {
		writeJSON(w, 401, map[string]any{"error": err.Error(), "login": true})
		return
	}
	writeJSON(w, 503, map[string]any{"error": err.Error()})
}
