package core

import (
	"sync"
	"time"

	"omixor-core/config"
	"omixor-core/model"
)

// view「projects」:目前組織的專案清單(含 key,給畫面的專案篩選、Issue 的專案選單用)。
//
//	GET /api/view/projects → model.Project[]({id, name, key, description…};沒有專案是 [])
//
// 畫面訂閱 "cabinet"、"orgs"、"issues" 通知後重讀。清單是快取(view 在 Mu 下執行,不能發網路請求):
// 本機當場讀核心,遠端先給空的並在背景讀,讀到有變就通知 "cabinet"。專案的新增、修改、刪除、每 5 秒會重讀。

type projState struct {
	mu      sync.Mutex
	org     string
	list    []model.Project
	loaded  bool
	syncing bool
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
	be, err := agentBackend()
	if err == nil {
		list, err = be.ListProjects(org)
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
		if IsRemote() {
			st.mu.Lock()
			st.org, st.list, st.loaded = org, []model.Project{}, true
			start := !st.syncing
			st.syncing = true
			st.mu.Unlock()
			if start {
				go func() {
					changed := a.projectsRefresh(org)
					st.mu.Lock()
					st.syncing = false
					st.mu.Unlock()
					if changed {
						a.Changed("cabinet")
					}
				}()
			}
		} else {
			a.projectsRefresh(org)
		}
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
					a.Changed("cabinet")
				}
			}
		}()
	})
}
