package core

import (
	"log"
	"sync"
	"time"
)

// 一次性 App(Scoped)每個請求都是新的,組織層的資料要快取在原本的 App 上,免得每個請求都問核心、同一個錯誤一直進 log。
// 這裡快取的都是用系統身分讀的、和呼叫者無關的資料(功能開關、受限專案清單);依呼叫者篩選是之後在輸出上做的,不快取。

// scopedTTL 快取多久(別的路徑改的資料最慢這麼久看到;經過這裡的寫入當場作廢,見 InvalidateScoped)
const scopedTTL = 5 * time.Second

type scopedKey struct {
	root *App
	org  string
}

type scopedSys struct {
	mu    sync.Mutex
	state *sysState
	at    time.Time
}

var scopedSysCaches sync.Map // scopedKey → *scopedSys

// useScopedSystem 讓一次性 App s 用組織共用的功能開關狀態,過期才向核心重讀
func useScopedSystem(s *App, org string) {
	v, _ := scopedSysCaches.LoadOrStore(scopedKey{s.root(), org}, &scopedSys{state: newSysState()})
	c := v.(*scopedSys)
	c.mu.Lock()
	defer c.mu.Unlock()
	sysStates.Store(s, c.state)
	if time.Since(c.at) >= scopedTTL {
		s.SystemRefresh()
		c.at = time.Now()
	}
}

// InvalidateScoped 功能開關、專案可能被改過:各組織的快取作廢
func (a *App) InvalidateScoped() {
	root := a.root()
	scopedSysCaches.Range(func(k, _ any) bool {
		if k.(scopedKey).root == root {
			scopedSysCaches.Delete(k)
		}
		return true
	})
	a.InvalidateRestricted()
}

// scopedErrs 一次性 App 最近一次記過的錯誤(scopedKey → 訊息):同一個訊息不重複記
var scopedErrs sync.Map

// logCoreErr 記核心錯誤;一次性 App 的同一組織同一訊息只記一次
func (a *App) logCoreErr(org, msg string) {
	if a.parent != nil {
		if prev, ok := scopedErrs.Swap(scopedKey{a.parent, org}, msg); ok && prev == msg {
			return
		}
	}
	log.Printf("Issue 伺服器:%s", msg)
}
