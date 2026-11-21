// agents_import.go 角色與任務的第一次搬資料匯入

package service

import (
	"sort"
	"strings"

	"omixor-core/model"
)

// ================= 匯入 =================

// AgentImport 第一次搬資料用:角色(含例行任務、docAccess)與任務(含已完成、已取消的歷史)
type AgentImport struct {
	Agents []model.Agent     `json:"agents"`
	Tasks  []model.AgentTask `json:"tasks"`
}

// AgentImportResult 匯入結果
type AgentImportResult struct {
	Org     string   `json:"org"`
	Agents  int      `json:"agents"`
	Tasks   int      `json:"tasks"`
	Requeue []string `json:"requeued,omitempty"` // 原本進行中、改成待領取的任務(匯入的任務沒有有效的領取與憑證)
}

// ImportAgents 第一次搬資料用(組織管理員):保留角色與任務的 id、時間、作者與備註;這個組織已經有角色或任務就拒絕。
// 進行中的任務沒有有效的領取(憑證不會搬),改成待領取;鎖定與領取資訊一律清掉。之後一律用單筆新增或修改。
func (s *Service) ImportAgents(p *model.Principal, org string, in AgentImport) (*AgentImportResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if len(in.Agents) == 0 {
		return nil, fail(400, "沒有要匯入的角色")
	}
	now := s.mt()
	ids := map[string]string{} // 小寫 → 角色 id(任務的 agentId 換成角色的寫法,store 比對大小寫)
	for k := range in.Agents {
		g := &in.Agents[k]
		g.ID = strings.TrimSpace(g.ID)
		if g.ID == "" || strings.TrimSpace(g.Name) == "" {
			return nil, fail(400, "每個角色都要有 id 與 name")
		}
		if _, dup := ids[strings.ToLower(g.ID)]; dup {
			return nil, fail(400, "角色 id 重複:%s", g.ID)
		}
		ids[strings.ToLower(g.ID)] = g.ID
		for key, v := range g.DocAccess {
			if !model.OneOf(v, model.DocAccessLevels) {
				return nil, fail(400, "角色 %s 的 docAccess %s 必須是 %s 之一", g.ID, key, strings.Join(model.DocAccessLevels, "、"))
			}
		}
		if g.DocAccess == nil {
			g.DocAccess = map[string]string{}
		}
		if g.Routines == nil {
			g.Routines = []model.AgentRoutine{}
		}
		if g.CreatedAt.IsZero() {
			g.CreatedAt = now
		}
	}
	res := &AgentImportResult{Org: org, Agents: len(in.Agents)}
	who := s.newImportWho(p, org)
	tids := map[string]bool{}
	for k := range in.Tasks {
		t := &in.Tasks[k]
		if t.Number() == 0 || strings.TrimSpace(t.Title) == "" {
			return nil, fail(400, "每筆任務都要有 T-<數字> 的 id 與 title(%s)", t.ID)
		}
		t.ID = strings.ToUpper(t.ID)
		if tids[t.ID] {
			return nil, fail(400, "任務 id 重複:%s", t.ID)
		}
		tids[t.ID] = true
		aid, ok := ids[strings.ToLower(strings.TrimSpace(t.AgentID))]
		if !ok {
			return nil, fail(400, "任務 %s 的角色 %s 不在匯入的角色裡", t.ID, t.AgentID)
		}
		t.AgentID = aid
		if !model.OneOf(t.Priority, model.IssuePriorities) {
			t.Priority = "normal"
		}
		if !model.OneOf(t.Status, model.AgentTaskStatuses) {
			t.Status = "queued"
		}
		if t.Status == "inProgress" {
			t.Status = "queued"
			res.Requeue = append(res.Requeue, t.ID)
		}
		t.ClaimedAt, t.LockedUntil, t.ClaimedBy = nil, nil, ""
		// 匯入檔裡的署名是舊字串(email、agent:<id>、routine…):轉成 actor id(伺服器管理員寬鬆;組織管理員只能署名本組織成員、角色、系統與自己,見 actor_import.go)
		var err error
		if t.CreatedBy, err = who.id(t.CreatedBy, ""); err != nil {
			return nil, err
		}
		for n := range t.Notes {
			if t.Notes[n].Author, err = who.id(t.Notes[n].Author, ""); err != nil {
				return nil, err
			}
		}
		if t.Links == nil {
			t.Links = []string{}
		}
		if t.Notes == nil {
			t.Notes = []model.AgentTaskNote{}
		}
		for n := range t.Notes {
			if t.Notes[n].ID == "" {
				t.Notes[n].ID = uuid()
			}
		}
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now
		}
		if t.UpdatedAt.IsZero() {
			t.UpdatedAt = t.CreatedAt
		}
	}
	// 新的在前(同 ListAgentTasks)
	sort.SliceStable(in.Tasks, func(i, j int) bool { return in.Tasks[i].Number() > in.Tasks[j].Number() })
	res.Tasks = len(in.Tasks)
	s.agents.mu.Lock()
	err := s.Store.ImportAgents(org, in.Agents, in.Tasks)
	s.agents.mu.Unlock()
	if err != nil {
		return nil, fail(409, "%v", err)
	}
	s.Audit(p, "agents.import", org, map[string]any{"agents": res.Agents, "tasks": res.Tasks})
	return res, nil
}
