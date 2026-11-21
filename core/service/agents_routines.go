// agents_routines.go 例行任務:到時間排任務、立刻執行、背景排程

package service

import (
	"log"
	"time"

	"omixor-core/model"
)

// ================= 例行任務 =================

// RunDueRoutines 到時間的例行任務各排一筆任務(全部組織;停用的角色、停用的例行任務不排)。
// 一個例行任務上一筆還沒做完(待領取、進行中、需要協助)就不重複排,但仍然記下這次的時間(lastRunAt),
// 下次排程從這個時間往後算。回傳這次新排的任務。
// 由使用核心的程式定時呼叫(server 與 omixor-web 本機核心各自每 60 秒一次);同一個資料庫有多個程式同時排程時可能重複排。
func (s *Service) RunDueRoutines(now time.Time) ([]model.AgentTask, error) {
	_ = s.Store.PruneAgentCredentials(now)
	orgs, err := s.Store.ListOrgs()
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	created := []model.AgentTask{}
	var firstErr error
	for _, o := range orgs {
		if err := s.expireLeases(o.ID, now); err != nil {
			firstErr = err
		}
		agents, err := s.Store.ListAgents(o.ID)
		if err != nil {
			firstErr = err
			continue
		}
		var tasks []model.AgentTask
		loaded := false
		for _, g := range agents {
			if !g.Active {
				continue
			}
			changed := false
			for ri := range g.Routines {
				r := g.Routines[ri]
				if !r.Enabled || r.NextRun().After(now) {
					continue
				}
				if !loaded {
					if tasks, err = s.Store.ListAgentTasks(o.ID); err != nil {
						firstErr = err
						break
					}
					loaded = true
				}
				t := model.Time{Time: now.UTC().Truncate(time.Second)}
				g.Routines[ri].LastRunAt = &t
				changed = true
				pending := false
				for _, x := range tasks {
					if x.RoutineID != nil && *x.RoutineID == r.ID && model.TaskIsOpen(x.Status) {
						pending = true
					}
				}
				if pending {
					continue
				}
				rid := r.ID
				gg := g
				nt, err := s.insertTaskFor(o.ID, &gg, r.Title, r.Instructions, r.Priority, model.SystemActorRoutine, nil, &rid)
				if err != nil {
					firstErr = err
					continue
				}
				tasks = append([]model.AgentTask{*nt}, tasks...)
				created = append(created, *nt)
			}
			if changed {
				if err := s.Store.SaveAgent(o.ID, g); err != nil {
					firstErr = err
				}
			}
		}
	}
	out, err := s.presentTasks(created)
	if err != nil && firstErr == nil {
		firstErr = err
	}
	return out, firstErr
}

// RunRoutine 立刻替角色排一筆例行任務(組織管理員;不管上一筆做完沒有),並把這次當作上次排程的時間。
// by 是署名(同 TaskInput.Author:只有伺服器管理員給的才算數)。
func (s *Service) RunRoutine(p *model.Principal, org, agentID, routineID, by string) (*model.AgentTask, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	g, err := s.mustAgent(org, agentID)
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	for ri := range g.Routines {
		r := g.Routines[ri]
		if r.ID != routineID {
			continue
		}
		// 先解析署名再寫 lastRunAt:署名錯誤(400)時例行任務不能被標成已執行
		rid := r.ID
		who, err := s.ResolveAuthor(p, org, by, AuthorIgnore)
		if err != nil {
			return nil, err
		}
		t := s.mt()
		g.Routines[ri].LastRunAt = &t
		if err := s.Store.SaveAgent(org, *g); err != nil {
			return nil, err
		}
		nt, err := s.insertTaskFor(org, g, r.Title, r.Instructions, r.Priority, who.ID, nil, &rid)
		if err != nil {
			return nil, err
		}
		s.Audit(p, "agent.routine.run", org, map[string]any{"task": nt.ID, "agent": g.ID, "routine": r.ID})
		return s.presentTask(nt)
	}
	return nil, fail(404, "找不到例行任務 %s", routineID)
}

// StartRoutines 啟動背景排程:立刻檢查一次,之後每 interval(0 = 60 秒)檢查一次。
// 新排了任務時呼叫 onCreated(可以是 nil)。回傳停止的函式。
func (s *Service) StartRoutines(interval time.Duration, onCreated func([]model.AgentTask)) (stop func()) {
	if interval <= 0 {
		interval = time.Minute
	}
	done := make(chan struct{})
	run := func() {
		list, err := s.RunDueRoutines(s.now())
		if err != nil {
			log.Printf("例行任務排程:%v", err)
		}
		if len(list) > 0 && onCreated != nil {
			onCreated(list)
		}
	}
	go func() {
		run()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				run()
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
