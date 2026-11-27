package core

import (
	"errors"
	"log"
	"strings"
	"sync"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 網頁操作的呼叫者(ISS-76.2 第一步):啟用 LDAP 時,畫面的寫入以登入者自己的核心使用者(ldap:<帳號>,和 MCP 的 OAuth 授權
// 同一位)執行並署名。誰能登入、能讀寫哪個組織見 caller_access.go。
//
// nil = 系統身分(本機使用者 omixor-console):沒啟用登入、API token、背景工作、自動執行。有呼叫者時不會退回系統身分:對不到身分就拒絕(見 principal):
//   - web 管理員(管理員帳號名單):登入者本人,權限是伺服器管理員(只在這次請求的 Principal 上,不寫回使用者資料;
//     名單拿掉就沒有)
//   - 目標組織的成員(或本來就是核心的伺服器管理員):登入者本人,權限照核心
//   - 其他:403
//
// 這決定「寫入」、本來就因人而異的資料(對話頻道、專案的文件櫃…)與看得到哪些受限專案(restricted_visibility.go);
// 其他讀取仍用系統身分的共用快取,由 web 層先依組織成員身分擋(web/membership.go)、再篩選(restricted.go),
// 所以不能用某位使用者的 Principal 去填共用快取。

// CallerKey web 呼叫 Action 時把 *Caller 放在 args[CallerKey](沒有登入者就不放)
const CallerKey = "_caller"

// Caller 一次請求的登入者;每個請求建一個
type Caller struct {
	account, name, email string
	webAdmin             bool
	// set 目前的 LDAP 登入設定(email 網域、是否信任 mail):對核心使用者的規則和 OAuth 授權同一套
	set service.LDAPSettings

	mu   sync.Mutex
	user *model.User // 這個請求解析到的核心使用者(只成功時記)
}

// NewCaller account 是 LDAP 帳號,name、email 是登入時目錄給的值;webAdmin 照目前的管理員名單
func NewCaller(account, name, email string, webAdmin bool, set service.LDAPSettings) *Caller {
	return &Caller{account: strings.TrimSpace(account), name: name, email: email, webAdmin: webAdmin, set: set}
}

// CallerOf Action 參數裡的呼叫者(沒有是 nil = 系統身分)
func CallerOf(args map[string]any) *Caller {
	c, _ := args[CallerKey].(*Caller)
	return c
}

// Account 呼叫者的 LDAP 帳號(動態牆用);系統身分是空字串
func (c *Caller) Account() string {
	if c == nil {
		return ""
	}
	return c.account
}

// Name 登入時目錄給的顯示名稱(沒有就是帳號);系統身分是空字串
func (c *Caller) Name() string {
	if c == nil {
		return ""
	}
	if n := strings.TrimSpace(c.name); n != "" {
		return n
	}
	return c.account
}

// IsSelf id 是呼叫者自己的帳號(人員清單裡沒有也可以指派給自己、當事件指揮官)
func (c *Caller) IsSelf(id string) bool { return c != nil && c.account != "" && id == c.account }

// actorOr 寫動態牆的人:有呼叫者用他,否則 fallback
func (c *Caller) actorOr(fallback string) Actor {
	if c != nil && c.account != "" {
		return Person(c.account)
	}
	return Person(fallback)
}

// firstCaller 選填參數裡的呼叫者(沒給是 nil)
func firstCaller(who []*Caller) *Caller {
	if len(who) == 0 {
		return nil
	}
	return who[0]
}

// OrgAdmin 呼叫者是不是 org 的組織管理員(核心的規則:伺服器管理員,或組織角色 admin);查不出來當作不是。
// 只給畫面決定要不要顯示按鈕,能不能做仍由伺服器檢查
func (c *Caller) OrgAdmin(org string) bool {
	l := localCore()
	if c == nil || l == nil {
		return false
	}
	u, err := c.coreUser(l.Svc)
	if err != nil {
		return false
	}
	if u.Role == "admin" {
		return true
	}
	role, err := l.Svc.Store.MemberRole(org, u.ID)
	return err == nil && role == "admin"
}

// principal 呼叫者在 org 用的核心身分(規則見檔頭);org 空 = 和組織無關的操作(只有管理員)
func (c *Caller) principal(svc *service.Service, org string) (*model.Principal, error) {
	u, err := c.coreUser(svc)
	if err != nil {
		return nil, err
	}
	cp := *u // 複本:提升的權限只在這個 Principal 上
	switch {
	case c.webAdmin:
		cp.Role = "admin"
	case cp.Role == "admin": // 本來就是核心的伺服器管理員
	default:
		if !model.ValidOrgID(org) {
			return nil, errNotOrgMember
		}
		if _, err := svc.Store.MemberRole(org, cp.ID); errors.Is(err, store.ErrNotFound) {
			return nil, errNotOrgMember
		} else if err != nil {
			log.Printf("查 %s 在組織 %s 的成員身分:%v", c.account, org, err)
			return nil, errAccessUnknown
		}
	}
	p := &model.Principal{User: cp, Via: model.ViaLogin}
	ref, err := svc.SelfActor(p)
	if err != nil {
		log.Printf("登入者 %s 的 actor 不可用:%v", c.account, err)
		return nil, errAccessUnknown
	}
	p.Actor = ref
	return p, nil
}

// localCoreFor 核心,以呼叫者在 org 的身分;c 是 nil 就是 localCore()。沒有資料庫回 errNoDB
func localCoreFor(c *Caller, org string) (*backend.Local, error) {
	l := localCore()
	if l == nil {
		return nil, errNoDB
	}
	if c == nil {
		return l, nil
	}
	p, err := c.principal(l.Svc, org)
	if err != nil {
		return nil, err
	}
	return &backend.Local{Svc: l.Svc, P: p, EnsureOrg: l.EnsureOrg}, nil
}

// LocalAPIPrincipal 沒啟用登入時給 core/api(REST)用的本機使用者(omixor-console)身分;沒有核心回 nil
func LocalAPIPrincipal() *model.Principal {
	l := localCore()
	if l == nil {
		return nil
	}
	p := *l.P
	return &p
}

// Principal 呼叫者呼叫核心 API(REST)的身分:登入者本人;webAdmin(管理員帳號名單)是伺服器管理員(只在這個複本上)。
// 呼叫前要先通過 Access
func (c *Caller) Principal() (*model.Principal, error) {
	l := localCore()
	if l == nil {
		return nil, errNoDB
	}
	u, err := c.coreUser(l.Svc)
	if err != nil {
		return nil, err
	}
	cp := *u
	if c.webAdmin {
		cp.Role = "admin"
	}
	p := &model.Principal{User: cp, Via: model.ViaLogin}
	ref, err := l.Svc.SelfActor(p)
	if err != nil {
		log.Printf("登入者 %s 的 actor 不可用:%v", c.account, err)
		return nil, errAccessUnknown
	}
	p.Actor = ref
	return p, nil
}

// coreBackendFor 同 coreBackend,用呼叫者在 org 的身分
func coreBackendFor(c *Caller, org string) (backend.Backend, error) {
	if c == nil {
		return coreBackend()
	}
	l, err := localCoreFor(c, org)
	if err != nil {
		return nil, err
	}
	return authorBackend{l}, nil
}

// BackendFor 給 MCP 工具(/api/tool)與選用模組:呼叫者在 org 的核心,沒有署名轉換(同 AgentBackend)
func BackendFor(c *Caller, org string) (backend.Backend, error) {
	be, err := coreBackendFor(c, org)
	if err != nil {
		return nil, err
	}
	return rawBackend(be), nil
}

// AsCaller f 裡的核心寫入(Issue、建立與刪除組織時的寫入)以 c 的身分執行。
// 呼叫時要拿著 Mu,f 執行期間不能放鎖(同 As;所以不會影響到別的請求或背景工作)
func (a *App) AsCaller(c *Caller, f func()) {
	prev := a.caller
	a.caller = c
	defer func() { a.caller = prev }()
	f()
}

// issueWriteBackend Issue 寫入用的核心(目前清單的組織、AsCaller 指定的身分)。呼叫時要拿著 Mu。
// 讀取一律用 issueBackend(系統身分),共用快取才不會變成某位使用者看到的內容
func (a *App) issueWriteBackend() (backend.Backend, error) {
	return coreBackendFor(a.caller, a.issueSt().Org)
}

// 帳號視圖的 self(畫面用它比對「是不是我接受的、我提出的」):有自己身分的呼叫者是本人的 email,其他照舊
func init() {
	RegisterCallerView("account", func(a *App, who *Caller) any {
		v := a.accountView()
		if l := localCore(); l != nil {
			if who == nil {
				return v
			}
			if p, err := who.principal(l.Svc, a.CurrentOrgID()); err == nil {
				v.Self = p.User.Email
			}
		}
		return v
	})
}
