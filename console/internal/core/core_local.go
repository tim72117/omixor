package core

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 核心(同一個程式裡的 service + PostgreSQL):本機使用者的身分與核心的建立。呼叫核心的介面見 issues_remote.go

var (
	localCoreMu  sync.Mutex
	localCoreVal *backend.Local
	localCoreKey any // 建立核心時的資料庫連線池,換了就重建

	// localServiceInit 建本機核心的 service 之後要套的設定(對外 MCP 連接埠的 evoleap、GitLab 登入等,見 ConfigureLocalService);nil = 不套
	localServiceInit func(*service.Service)

	// newTestCore 沒有資料庫時改用的核心(測試用;UseTestStores 設定)。nil = 沒有資料庫就不能用
	newTestCore func() *backend.Local
)

const (
	// LocalIdentity 本機使用者的 handle(固定,不可設定;ISS-36 D4):console 沒有登入者時的操作者就是這位使用者(伺服器管理員),
	// 寫進任務領取者、頻道成員、建立者、留言與稽核的「誰」都是它的 actor
	LocalIdentity = "omixor-console"
	// localUserKey 本機使用者那一列的固定 evoleap_id(辨識「本機建立的那一列」;一般登入的 evoleap id 沒有這個前綴、LDAP 是 ldap:)
	localUserKey = "local:" + LocalIdentity
	// legacyLocalIdentity 產品早期叫 Ops 時的本機身分(舊資料裡的字串;遷移時對到本機使用者)
	legacyLocalIdentity = "ops-web"
	// legacyLocalPerson 範例資料的人員 id,也是早期寫死的本機使用者(舊資料的建立者、留言作者常是它);只為相容舊資料保留
	legacyLocalPerson = "tim"
)

// IsLocalUser 這位使用者是不是本機使用者 omixor-console(本機建立的那一列,或舊版沒有標記的同名列)。
// 對外 MCP 連接埠用:本機使用者是伺服器管理員,只能在本機使用,不能被任何遠端登入或 token 對上
func IsLocalUser(u model.User) bool {
	return u.EvoleapID == localUserKey || strings.EqualFold(strings.TrimSpace(u.Email), LocalIdentity)
}

// LocalAliases 舊資料裡會對到本機使用者的字串(遷移與寬鬆解析器用;mcp 由遷移規則內建)
func LocalAliases() []string { return []string{legacyLocalIdentity, legacyLocalPerson, MCPAuthor} }

// ConfigureLocalIdentity 在建 store(NewPG、NewPGFromPool)之前告訴遷移本機使用者是誰:
// 舊資料裡的 mcp、ops-web、omixor-console、tim 都對到這位使用者的 actor,不會留下孤兒。冪等;要在遷移開始前呼叫
func ConfigureLocalIdentity() { store.SetMigrationLocalUser(LocalIdentity, LocalAliases()...) }

// ensureLocalPrincipal 確保本機使用者存在、是伺服器管理員、有 actor,回傳呼叫核心時用的 Principal(Actor、Via = ViaLocal)。
// 與 service.saveUser 同樣語意:Store.SaveUser 在同一個交易建立使用者並建立或接回 actor
// (遷移對到本機別名時建的「未註冊使用者」actor 會被接回,舊資料的署名因此全都是這位使用者);之後重讀取回 actor_id。冪等
func ensureLocalPrincipal(st *store.PG) (*model.Principal, error) {
	// 只採用「本機建立的那一列」:以固定的 evoleap_id(localUserKey)辨識。舊版沒有這個標記的列,只有在 email 剛好是本機 handle、
	// 而且沒有綁任何外部身分(evoleap_id 空)時才接手並補上標記;email 是 omixor-console 卻綁了別的身分(例如 LDAP 的 mail 被填成這個字串、
	// 與雲端共用資料庫)的列不採用、也不升級,直接報錯,免得外部登入的人被升成管理員
	u, err := st.UserByEvoleapID(localUserKey)
	if errors.Is(err, store.ErrNotFound) {
		u, err = st.UserByEmail(LocalIdentity)
		switch {
		case errors.Is(err, store.ErrNotFound):
			u, err = &model.User{ID: service.RandID("u_", 9), EvoleapID: localUserKey, Email: LocalIdentity, Name: LocalIdentity, Role: "admin", CreatedAt: model.Now()}, nil
			if err = st.SaveUser(*u); err == nil {
				u, err = st.UserByEvoleapID(localUserKey)
			}
		case err == nil && u.EvoleapID == "":
			u.EvoleapID, u.Role, u.Disabled = localUserKey, "admin", false
			if err = st.SaveUser(*u); err == nil {
				u, err = st.UserByEvoleapID(localUserKey)
			}
		case err == nil:
			return nil, fmt.Errorf("使用者 %s 已經被另一個登入身分佔用(%s),不能當本機使用者", LocalIdentity, u.ID)
		}
	}
	if err != nil {
		return nil, err
	}
	if u.Role != "admin" || u.Disabled || u.ActorID == "" {
		u.Role, u.Disabled = "admin", false
		if err := st.SaveUser(*u); err != nil {
			return nil, err
		}
		if u, err = st.UserByEvoleapID(localUserKey); err != nil {
			return nil, err
		}
	}
	p := &model.Principal{User: *u, Via: model.ViaLocal}
	ref, err := (&service.Service{Store: st}).SelfActor(p)
	if err != nil {
		return nil, fmt.Errorf("本機使用者 %s 的 actor 不可用:%w", LocalIdentity, err)
	}
	p.Actor = ref
	return p, nil
}

// localCore 本機模式的核心:連 PostgreSQL(OMIXOR_DATABASE_URL);沒有資料庫時用 newTestCore(測試)。都沒有回傳 nil
func localCore() *backend.Local {
	localCoreMu.Lock()
	defer localCoreMu.Unlock()
	if pool := pgPool(); pool != nil {
		if localCoreVal == nil || localCoreKey != any(pool) {
			ConfigureLocalIdentity() // 要在 NewPGFromPool(啟動遷移)之前
			st, err := store.NewPGFromPool(pool)
			if err != nil {
				log.Printf("核心建表失敗:%v", err)
				return nil
			}
			p, err := ensureLocalPrincipal(st)
			if err != nil {
				log.Printf("本機使用者建立失敗:%v", err)
				return nil
			}
			svc := &service.Service{Store: st}
			if localServiceInit != nil {
				localServiceInit(svc)
			}
			if err := svc.EnsureOrgsForIssues(); err != nil {
				log.Printf("補建組織失敗:%v", err)
			}
			migrateLegacyAttachments(svc, p) // 舊的 console 附件搬進核心(冪等,見 attachments_legacy.go)
			localCoreVal, localCoreKey = &backend.Local{Svc: svc, P: p, EnsureOrg: true}, any(pool)
		}
		return localCoreVal
	}
	if newTestCore != nil {
		return newTestCore()
	}
	return nil
}

// ConfigureLocalService 登記本機核心的 service 建好之後要套的設定(例如 oauth.ConfigureService)。
// 要在第一次用核心(core.New、App.Start)之前呼叫:service 建立後就不再改它的欄位(請求處理中的 goroutine 會讀)
func ConfigureLocalService(init func(*service.Service)) {
	localCoreMu.Lock()
	defer localCoreMu.Unlock()
	localServiceInit = init
}

// ensureLocalOrg 組織還沒建就補建(omixor-web 的組織是自己管的;同 backend.Local 的 EnsureOrg)
func ensureLocalOrg(l *backend.Local, org string) {
	if !model.ValidOrgID(org) {
		return
	}
	if _, err := l.Svc.Store.GetOrg(org); err != nil {
		_ = l.Svc.Store.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: model.Now()})
	}
}
