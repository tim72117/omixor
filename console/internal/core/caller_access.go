package core

import (
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 啟用 LDAP 時(本機模式)誰能用 console 的網頁:
//   - 帳號要對得上核心使用者(ldap:<帳號>)而且沒有停用;web 管理員也一樣
//   - 而且是 web 管理員、核心的伺服器管理員,或任一組織的成員
//   - 非管理員只能讀寫自己是成員的組織(web/membership.go 擋讀、principal 擋寫)
//   - 例外:管理員名單上、但對不到核心使用者的帳號(不是停用)可以登入,只能改認證設定(CanRepair;不然沒人修得了)
//
// 登入時檢查一次,之後每個請求再查(停用、移出組織立即生效)。第一次登入而被拒的人,核心使用者已經建好,
// 管理員用 email 把他加進組織後就能登入。
//
// 先用 email 預先加進組織、還沒登入過的人:沒開 trustMail 時目錄帳號對不上那一筆(核心的規則,避免用自己改得動的 mail
// 接手別人的身分),而且移除成員不會刪掉那筆使用者;管理員要開 trustMail(設定 › 認證 或 OMIXOR_LDAP_TRUST_MAIL)讓他登入一次。
// email 已經是另一個身分(evoleap、GitLab、別的帳號)在用的,trustMail 也沒用。

var (
	errNotOrgMember  = StatusError{Status: http.StatusForbidden, Message: "你不是這個組織的成員", Data: map[string]any{"forbidden": true, "notMember": true}}
	errAccessUnknown = StatusError{Status: http.StatusServiceUnavailable, Message: "暫時無法確認你的組織成員身分,請稍後再試;若持續發生請聯絡管理員"}
)

func deniedLogin(msg string) error {
	return StatusError{Status: http.StatusForbidden, Message: "不能登入:" + msg}
}

// directoryID 和 service.ProveLDAP 給 resolveDirectory 的值一樣(ID 是小寫帳號)
func (c *Caller) directoryID() service.DirectoryIdentity {
	return service.DirectoryIdentity{ID: strings.ToLower(c.account), Email: c.email, Name: c.name}
}

// directoryMail 這個帳號會用哪個 email 對使用者(同核心:目錄的 mail,沒有就「帳號@網域」);只用在給人看的說明
func (c *Caller) directoryMail() string {
	id := c.directoryID()
	mail := strings.ToLower(strings.TrimSpace(id.Email))
	if !strings.Contains(mail, "@") {
		mail = ""
	}
	if mail == "" && c.set.EmailDomain != "" {
		mail = id.ID + "@" + strings.TrimPrefix(strings.ToLower(c.set.EmailDomain), "@")
	}
	return mail
}

// resolveUser 建立或更新核心使用者(第一次出現的人是一般使用者,不屬於任何組織);回傳的錯誤是給登入者看的
func (c *Caller) resolveUser(svc *service.Service) (*model.User, error) {
	u, err := svc.ResolveDirectoryUserWith(c.set, c.directoryID())
	if err == nil {
		return u, nil
	}
	var se *service.Error
	if !errors.As(err, &se) {
		log.Printf("登入者 %s 對應 omixor 使用者失敗:%v", c.account, err)
		return nil, errAccessUnknown
	}
	if mail := c.directoryMail(); mail != "" && !c.set.TrustMail {
		if other, oerr := svc.Store.UserByEmail(mail); oerr == nil && other.EvoleapID == "" {
			if other.Disabled {
				return nil, deniedLogin("這個帳號已停用,請聯絡管理員")
			}
			return nil, unmappedLogin("管理員已經用 " + mail + " 預先加入你,但目前沒有開「信任目錄的 email」,對不上。請管理員到 設定 › 認證 開啟 trustMail" +
				"(或設 OMIXOR_LDAP_TRUST_MAIL=true),你登入成功一次之後可以再關掉;只移除成員再重加沒有用")
		}
	}
	if mail := c.directoryMail(); mail != "" {
		if other, oerr := svc.Store.UserByEmail(mail); oerr == nil && other.Disabled { // 停用不是設定修得好的
			return nil, deniedLogin(se.Msg)
		}
	}
	return nil, unmappedLogin(se.Msg)
}

// unmappedKey 拒絕原因是「帳號對不到核心使用者」
const unmappedKey = "unmapped"

func unmappedLogin(msg string) error {
	return StatusError{Status: http.StatusForbidden, Message: "不能登入:" + msg, Data: map[string]any{unmappedKey: true}}
}

// CanRepair 被拒的這位是管理員名單上、對不到核心使用者的帳號:只給他認證設定(不給任何組織資料),讓他修得好。
// 停用、沒加入組織、暫時查不出來都不算
func (c *Caller) CanRepair(err error) bool {
	var se StatusError
	return c != nil && c.webAdmin && errors.As(err, &se) && se.Data[unmappedKey] == true
}

// coreUser 登入者的核心使用者(每個請求重讀,停用立即生效;升級前就登入的 session 在這裡補建);停用或對不上回錯誤
func (c *Caller) coreUser(svc *service.Service) (*model.User, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.user != nil {
		return c.user, nil
	}
	id := c.directoryID()
	if id.ID == "" {
		return nil, deniedLogin("沒有帳號")
	}
	u, err := svc.Store.UserByEvoleapID(service.LDAPIDPrefix + id.ID)
	if errors.Is(err, store.ErrNotFound) {
		u, err = c.resolveUser(svc)
	} else if err != nil {
		log.Printf("查登入者 %s:%v", c.account, err)
		err = errAccessUnknown
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		return nil, deniedLogin("這個帳號已停用,請聯絡管理員")
	}
	c.user = u
	return u, nil
}

// Access 呼叫者能讀寫什麼
type Access struct {
	// Admin web 管理員或核心的伺服器管理員:不受組織成員身分限制
	Admin bool
	orgs  map[string]bool
}

// Member 能不能讀寫這個組織
func (x Access) Member(org string) bool { return x.Admin || x.orgs[org] }

// Access 呼叫者現在能不能用網頁(不能時回給他看的 StatusError:403 不能登入、503 查不出來)。
// c 是 nil(系統身分)或遠端模式不限制
func (c *Caller) Access() (Access, error) {
	if c == nil || IsRemote() {
		return Access{Admin: true}, nil
	}
	l := localCore()
	if l == nil { // 沒有資料庫就沒有使用者與組織:只讓 web 管理員進來處理
		if c.webAdmin {
			return Access{Admin: true}, nil
		}
		return Access{}, errAccessUnknown
	}
	u, err := c.coreUser(l.Svc)
	if err != nil {
		return Access{}, err
	}
	if c.webAdmin || u.Role == "admin" {
		return Access{Admin: true}, nil
	}
	list, err := l.Svc.Store.OrgsOfUser(u.ID)
	if err != nil {
		log.Printf("查 %s 的組織:%v", c.account, err)
		return Access{}, errAccessUnknown
	}
	if len(list) == 0 {
		return Access{}, deniedLogin("你還沒有加入任何組織。請管理員到「成員」頁用 " + u.Email + " 把你加進組織,再登入")
	}
	x := Access{orgs: map[string]bool{}}
	for _, o := range list {
		x.orgs[o.ID] = true
	}
	return x, nil
}

// CheckLogin 登入時(與存認證設定時對測試帳號):更新核心使用者(目錄的名稱、最後登入時間),再檢查能不能用
func (c *Caller) CheckLogin() error {
	if c == nil || IsRemote() {
		return nil
	}
	if l := localCore(); l != nil {
		u, err := c.resolveUser(l.Svc)
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.user = nil
		if !u.Disabled {
			c.user = u
		}
		c.mu.Unlock()
	}
	_, err := c.Access()
	return err
}

// visibleOrgs 組織清單只留呼叫者看得到的;目前的組織看不到時 Current 是空的(畫面的組織選單照樣可以切到自己的)
func visibleOrgs(idx OrgIndex, who *Caller) OrgIndex {
	x, err := who.Access()
	if err == nil && x.Admin {
		return idx
	}
	out := OrgIndex{Orgs: []Organization{}}
	for _, o := range idx.Orgs {
		if err == nil && x.Member(o.ID) {
			out.Orgs = append(out.Orgs, o)
			if o.ID == idx.Current {
				out.Current = o.ID
			}
		}
	}
	return out
}

// PickOrg 這個請求用哪個組織:requested(畫面帶的)能用就用它;沒帶用預設;帶了但不能用(移出、刪除)回預設與 false。
// 預設是固定的:系統工作組織(能用的話),否則清單(建立順序)裡第一個能用的;一個都沒有是空字串
func (x Access) PickOrg(requested, system string, orgs []Organization) (string, bool) {
	fallback := ""
	for _, o := range orgs {
		if !x.Member(o.ID) {
			continue
		}
		if o.ID == requested {
			return o.ID, true
		}
		if o.ID == system || fallback == "" {
			fallback = o.ID
		}
	}
	return fallback, requested == ""
}

// ReadAttachmentAmong 依序在這些組織找附件(登入者的圖片請求帶不了組織;id 是內容雜湊,只找呼叫者能讀的組織)
func ReadAttachmentAmong(id string, orgs []string) ([]byte, string, error) {
	if !attachmentIDRe.MatchString(id) {
		return nil, "", fs.ErrNotExist
	}
	for _, org := range orgs {
		if data, t, err := readOrgAttachment(org, OrgDir(org), id); err == nil {
			return data, t, nil
		}
	}
	return nil, "", fs.ErrNotExist
}

// userOrgsView view「orgs」給登入者:只列自己的組織,current 是這個請求用的;perUser 表示各人自己選(畫面帶 X-Omixor-Org),
// system 是系統工作組織(自己看得到才給)
type userOrgsView struct {
	orgsViewData
	PerUser bool   `json:"perUser"`
	System  string `json:"system,omitempty"`
}

// VisibleOrgs 同 visibleOrgs(/api/tool 的 orgs 工具用;拿著 Mu 呼叫)
func (a *App) VisibleOrgs(who *Caller) OrgIndex { return visibleOrgs(a.Orgs(), who) }

func init() {
	RegisterCallerView("orgs", func(a *App, who *Caller) any {
		v := userOrgsView{orgsViewData: a.orgsView(), PerUser: !IsRemote()}
		system := v.Current
		if a.IsScoped() { // 這裡拿著的是一次性 App 的鎖,拿原本的 App 的不會互鎖
			system = a.root().currentOrgNow()
		}
		v.OrgIndex = visibleOrgs(v.OrgIndex, who)
		for _, o := range v.Orgs {
			if o.ID == system {
				v.System = system
			}
		}
		return v
	})
	// 自動執行的紀錄是整台一份:非管理員只看目前組織的
	RegisterCallerView("agent_runner", func(a *App, who *Caller) any {
		v := a.runnerView()
		if x, err := who.Access(); err == nil && x.Admin {
			return v
		}
		if v.Current != nil && v.Current.Org != v.Org {
			v.Current = nil
		}
		kept := []RunnerRecord{}
		for _, r := range v.History {
			if r.Org == v.Org {
				kept = append(kept, r)
			}
		}
		v.History, v.LastError = kept, ""
		return v
	})
}
