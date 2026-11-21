package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"omixor-core/model"
)

// 子項與角色任務(agent_task)的接點:領取時綁定、任務結束時回寫、排任務時排除、受限專案清理、Issue 的子項計數。
// 所有函式的呼叫端都拿著 agents.mu(除了 withItemCounts,它只讀)。

// itemRoleAgent 負責人(actor id)是角色就回角色;不是角色、沒指派、角色已不存在回 nil
func (s *Service) itemRoleAgent(org, assignee string) (*model.Agent, error) {
	if assignee == "" {
		return nil, nil
	}
	n, err := s.LoadActors(assignee)
	if err != nil {
		return nil, err
	}
	rec, ok := n.Record(assignee)
	if !ok || rec.Kind != model.ActorKindAgent {
		return nil, nil
	}
	list, err := s.Store.ListAgents(org)
	if err != nil {
		return nil, err
	}
	for k := range list {
		if list[k].ActorID == assignee {
			return &list[k], nil
		}
	}
	return nil, nil
}

// bindItemTask 負責人是角色:建立(或重用這個子項上次還沒結束的)agent_task、領取並發憑證,子項記下任務 id 與租約。
// claimed 已經被 ClaimIssueItem 領走(doing)。失敗時呼叫端會放掉子項的領取
func (s *Service) bindItemTask(p *model.Principal, org string, issue *model.Issue, claimed *model.IssueItem, g *model.Agent, me string, res *ItemClaimResult) error {
	key := itemKey(issue, *claimed)
	var task *model.AgentTask
	created := false
	if claimed.TaskID != "" {
		if t, err := s.Store.GetAgentTask(org, claimed.TaskID); err == nil && t.AgentID == g.ID && model.TaskIsOpen(t.Status) && !t.Locked(s.now()) {
			task = t
		}
	}
	if task == nil {
		var b strings.Builder
		fmt.Fprintf(&b, "請處理 Issue %s 的子項 %s「%s」(優先度 %s)。", issue.Display(), key, claimed.Title, issue.Priority)
		if a := strings.TrimSpace(claimed.Acceptance); a != "" {
			fmt.Fprintf(&b, "\n\n驗收條件:\n%s", a)
		}
		if len(claimed.Files) > 0 {
			fmt.Fprintf(&b, "\n\n會動到的檔案:\n- %s", strings.Join(claimed.Files, "\n- "))
		}
		fmt.Fprintf(&b, "\n\n用 issues(id: %s)讀 Issue 內容與留言;做完用 issue_item_report(id: %s, status: done, evidence: …)回報,"+
			"卡住或需要人決定用 status: blocked 並寫 block_reason(要人決定時 wait_for_kind=user_decision),不要猜答案。", issue.Display(), key)
		t, err := s.insertTask(org, g, fmt.Sprintf("處理子項 %s:%s", key, claimed.Title), b.String(), issue.Priority, me, []string{issue.ID, claimed.ID})
		if err != nil {
			return err
		}
		task, created = t, true
	}
	cr, err := s.claimPicked(p, org, g, task)
	if err != nil {
		if created {
			_, _ = s.saveTaskChange(org, *task, func(x *model.AgentTask) { x.Status = "canceled" })
		}
		return err
	}
	claimed.TaskID, claimed.UpdatedAt = cr.Task.ID, s.mt()
	if cr.Task.LockedUntil != nil {
		claimed.LockedUntil = cr.Task.LockedUntil
	}
	if err := s.Store.UpdateIssueItem(org, claimed, model.ItemDoing); err != nil {
		_, _ = s.saveTaskChange(org, *cr.Task, func(x *model.AgentTask) { x.Status = "canceled" })
		return itemStoreErr(err, key)
	}
	res.Item, res.Task, res.Credential = claimed, cr.Task, cr.Credential
	return nil
}

// cancelItemTask 子項綁定的任務還沒結束就取消(憑證作廢)。失敗只記操作紀錄(子項本身已處理)
func (s *Service) cancelItemTask(org string, it *model.IssueItem) {
	if it.TaskID == "" {
		return
	}
	t, err := s.Store.GetAgentTask(org, it.TaskID)
	if err != nil || !model.TaskIsOpen(t.Status) {
		return
	}
	if _, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) { x.Status = "canceled" }); err != nil {
		s.Audit(nil, "issue_item.task.error", org, map[string]any{"item": it.ID, "task": t.ID, "error": auditErrText(err)})
	}
}

// finishBoundTask 子項回報 done / blocked 時,綁定的任務跟著結束(依組織鏈回報給建立者,同 ReportTask)
func (s *Service) finishBoundTask(p *model.Principal, org string, t *model.AgentTask, status string, in ItemReport, it *model.IssueItem) {
	result := strings.TrimSpace(in.Note)
	if result == "" && status == "blocked" {
		result = it.BlockReason
	}
	u, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) {
		x.Status = status
		if result != "" {
			x.Result = result
		}
	})
	if err != nil {
		s.Audit(p, "issue_item.task.error", org, map[string]any{"item": it.ID, "task": t.ID, "error": auditErrText(err)})
		return
	}
	s.chainReport(p, org, u, result)
}

// itemOfTask 任務是由子項產生的就回那個子項(links 裡的子項 id,而且子項記的 task_id 就是它);不是回 nil
func (s *Service) itemOfTask(org string, t model.AgentTask) *model.IssueItem {
	for _, l := range t.Links {
		if !model.IsUUID(l) {
			continue
		}
		if it, err := s.Store.GetIssueItem(org, l); err == nil && it.TaskID == t.ID {
			return it
		}
	}
	return nil
}

// syncItemFromTask 任務離開進行中(saveTaskChange 呼叫,拿著 agents.mu):綁定的子項(還在 doing)回寫——
// done → done(證據記任務 id 與結果)、blocked → blocked(原因取任務結果或最後一則備註)、其餘(退回待領取、取消、逾時)→ todo。
// 子項已經不是 doing(子項那邊先改好了)就不動。失敗只記操作紀錄
func (s *Service) syncItemFromTask(org string, before, after model.AgentTask) {
	if len(after.Links) < 2 {
		return
	}
	it := s.itemOfTask(org, after)
	if it == nil || it.Status != model.ItemDoing {
		return
	}
	now := s.mt()
	switch after.Status {
	case "done":
		ev := map[string]any{"task": after.ID}
		if r := strings.TrimSpace(after.Result); r != "" {
			ev["summary"] = r
		}
		b, _ := json.Marshal(ev)
		it.Status, it.CompletedAt, it.Evidence = model.ItemDone, &now, b
	case "blocked":
		reason := strings.TrimSpace(after.Result)
		if reason == "" && len(after.Notes) > 0 {
			reason = strings.TrimSpace(after.Notes[len(after.Notes)-1].Text)
		}
		if reason == "" {
			reason = "角色任務回報需要協助"
		}
		it.Status, it.BlockReason = model.ItemBlocked, truncRunes(reason, model.MaxItemReason)
	default:
		it.Status = model.ItemTodo
	}
	it.ClaimedBy, it.LockedUntil, it.UpdatedAt = "", nil, now
	if err := s.Store.UpdateIssueItem(org, it, model.ItemDoing); err != nil {
		s.Audit(nil, "issue_item.task.error", org, map[string]any{"item": it.ID, "task": after.ID, "error": auditErrText(err)})
		return
	}
	s.Audit(nil, "issue_item.report", org, map[string]any{"id": it.ID, "status": it.Status, "task": after.ID, "source": "agent_task"})
}

// itemTaskIDs 組織裡「由子項產生」的任務 id(子項記的 task_id);ClaimNext、ClaimTask(不指定任務)、syncAssignee 排除用,免得重複排、被自動撿走
func (s *Service) itemTaskIDs(org string) map[string]bool {
	m, err := s.Store.ListIssueItemsByOrg(org, nil)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, list := range m {
		for _, it := range list {
			if it.TaskID != "" {
				out[it.TaskID] = true
			}
		}
	}
	return out
}

// dropItemTasks 去掉由子項產生的任務
func (s *Service) dropItemTasks(org string, list []model.AgentTask) []model.AgentTask {
	skip := s.itemTaskIDs(org)
	if len(skip) == 0 {
		return list
	}
	out := make([]model.AgentTask, 0, len(list))
	for _, t := range list {
		if !skip[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// pruneProjectItemsLocked 受限專案(呼叫端拿著 agents.mu,已查好專案裡的 Issue 與成員):負責人是非成員角色的子項,
// 負責人清空;還沒結束的綁定任務(只有 queued 的,進行中的等租約到期)已由 pruneProjectTasksLocked 取消
func (s *Service) pruneProjectItemsLocked(p *model.Principal, org string, inProject map[string]bool, isMember map[string]bool) {
	ids := make([]string, 0, len(inProject))
	for id := range inProject {
		ids = append(ids, id)
	}
	byIssue, err := s.Store.ListIssueItemsByOrg(org, ids)
	if err != nil {
		s.Audit(p, "project.prune.error", org, map[string]any{"error": auditErrText(err)})
		return
	}
	for _, list := range byIssue {
		for _, it := range list {
			if it.Assignee == "" || model.ItemTerminal(it.Status) || isMember[it.Assignee] {
				continue
			}
			g, err := s.itemRoleAgent(org, it.Assignee)
			if err != nil || g == nil {
				continue // 人的負責人不在這裡處理
			}
			from := it.Status
			it.Assignee, it.UpdatedAt = "", s.mt()
			if err := s.Store.UpdateIssueItem(org, &it, from); err != nil {
				s.Audit(p, "project.prune.error", org, map[string]any{"item": it.ID, "error": auditErrText(err)})
			}
		}
	}
}

// withItemCounts 每張 Issue 的子項計數(itemsOpen:todo / doing / blocked;itemsTotal:不含 canceled)。一次批次查詢;查詢失敗就不填
func (s *Service) withItemCounts(org string, list []model.Issue) []model.Issue {
	if len(list) == 0 {
		return list
	}
	ids := make([]string, 0, len(list))
	for _, i := range list {
		ids = append(ids, i.ID)
	}
	if len(ids) > 500 {
		ids = nil // 整個組織一次查
	}
	m, err := s.Store.ListIssueItemsByOrg(org, ids)
	if err != nil || len(m) == 0 {
		return list
	}
	out := make([]model.Issue, len(list))
	for k, i := range list {
		for _, it := range m[i.ID] {
			if it.Status == model.ItemCanceled {
				continue
			}
			i.ItemsTotal++
			if !model.ItemTerminal(it.Status) {
				i.ItemsOpen++
			}
		}
		out[k] = i
	}
	return out
}
