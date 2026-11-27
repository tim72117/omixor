package core

import (
	"errors"
	"sync"
	"time"

	"omixor-core/backend"
	"omixor-core/config"
	"omixor-core/model"
)

// view「projects」:目前組織的專案清單(含 key,給畫面的專案篩選、Issue 的專案選單用)。
//
//	GET /api/view/projects → model.Project[]({id, name, key, description…};沒有專案是 [])
//
// 畫面訂閱 "projects"、"cabinet"、"orgs"、"issues" 通知後重讀。清單是快取(view 在 Mu 下執行,不能發網路請求):
// 還沒載入時當場讀核心,讀到有變就通知 "cabinet"。專案的新增、修改、刪除、每 5 秒會重讀。

type projState struct {
	mu     sync.Mutex
	org    string
	list   []model.Project
	loaded bool
	// want 畫面最近一次要的組織(projectsOf 記的);背景讀回來的結果不是這個組織就丟掉,舊組織的結果不能蓋進快取
	want string
	// stop 關掉背景計時器(stopTimer 只關一次)
	stop     chan struct{}
	stopOnce sync.Once
}

var projStates sync.Map // *App → *projState

func (a *App) projSt() *projState {
	if s, ok := projStates.Load(a); ok {
		return s.(*projState)
	}
	s, _ := projStates.LoadOrStore(a, &projState{stop: make(chan struct{})})
	return s.(*projState)
}

// StopProjectsTimer 停掉專案清單的背景重讀(app 關閉、測試結束時用;可以重複呼叫)
func (a *App) StopProjectsTimer() {
	st := a.projSt()
	st.stopOnce.Do(func() { close(st.stop) })
}

// projectsRefresh 從核心重讀 org 的專案換進快取(不拿 Mu),回傳內容有沒有變;讀不到時保留原本的
func (a *App) projectsRefresh(org string) bool {
	var list []model.Project
	var err error
	if org != "" { // 還沒有組織時不問核心
		var be backend.Backend
		if be, err = agentBackend(); err == nil {
			list, err = be.ListProjects(org)
		}
	}
	st := a.projSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.want != "" && st.want != org { // 讀的期間畫面已經換組織:這份結果過時了,不要蓋進快取
		return false
	}
	if err != nil {
		if st.org != org || !st.loaded {
			st.org, st.list, st.loaded = org, []model.Project{}, true
			return true
		}
		return false
	}
	if list == nil {
		list = []model.Project{}
	}
	changed := st.org != org || !st.loaded || string(Marshal(st.list)) != string(Marshal(list))
	if changed && st.loaded && st.org == org { // 別的路徑改了專案:受限專案的快取作廢
		a.InvalidateRestricted()
	}
	st.org, st.list, st.loaded = org, list, true
	return changed
}

func (a *App) projectsOf(org string) []model.Project {
	st := a.projSt()
	st.mu.Lock()
	st.want = org
	cur := st.loaded && st.org == org
	st.mu.Unlock()
	if !cur {
		a.projectsRefresh(org)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]model.Project{}, st.list...)
}

func init() {
	RegisterView("projects", func(a *App) any { return a.projectsOf(a.CurrentOrgID()) })
	OnStart(func(a *App) {
		if config.Current().Bool(config.NoTimers) {
			return
		}
		st := a.projSt()
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-st.stop:
					return
				case <-t.C:
				}
				if org := a.currentOrgNow(); org != "" && a.projectsRefresh(org) {
					a.VisibilityChanged() // 受限旗標可能是別的路徑改的
				}
			}
		}()
	})
}

// ---- 受限專案清單(分不出呼叫者時用)----
//
// 看得到哪些受限專案見 restricted_visibility.go。
// 這裡只負責問核心:目前有哪些受限專案(5 秒快取;問不到回錯誤,呼叫端要當作「全部專案都不能看」)。

type restrictedCache struct {
	mu   sync.Mutex
	set  map[string]bool
	at   time.Time
	list bool
}

// restrictedCaches 每個組織一份,放在原本的 App 上(一次性 App 共用);系統身分讀的清單,和呼叫者無關
var restrictedCaches sync.Map // scopedKey → *restrictedCache

// RestrictedProjectIDsOf 組織的受限專案 id(5 秒快取)。沒有專案功能(核心不支援)回空集合
func (a *App) RestrictedProjectIDsOf(org string) (map[string]bool, error) {
	v, _ := restrictedCaches.LoadOrStore(scopedKey{a.root(), org}, &restrictedCache{})
	c := v.(*restrictedCache)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list && time.Since(c.at) < scopedTTL {
		return c.set, nil
	}
	be, err := agentBackend()
	if err != nil {
		return nil, err
	}
	list, err := be.ListProjects(org)
	if err != nil {
		if errors.Is(err, backend.ErrUnsupported) { // 沒有專案功能
			return map[string]bool{}, nil
		}
		return nil, err
	}
	set := map[string]bool{}
	for _, x := range list {
		if x.Restricted {
			set[x.ID] = true
		}
	}
	c.set, c.at, c.list = set, time.Now(), true
	return set, nil
}

// RestrictedProjectIDs 目前組織的受限專案 id
func (a *App) RestrictedProjectIDs() (map[string]bool, error) {
	return a.RestrictedProjectIDsOf(a.currentOrgNow())
}

// InvalidateRestricted 專案或成員有變時清掉快取(整筆換掉,正在讀的舊結果不會留下來)
func (a *App) InvalidateRestricted() {
	root := a.root()
	restrictedCaches.Range(func(k, _ any) bool {
		if k.(scopedKey).root == root {
			restrictedCaches.Delete(k)
		}
		return true
	})
}
