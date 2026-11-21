// agents_tasks.go 任務佇列:型別、排任務、列出/取得、狀態變更存檔、取消、重新排隊,以及 Issue 指派自動排任務

package service

import (
	"errors"
	"fmt"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// ================= 任務 =================

// TaskInput 派任務
type TaskInput struct {
	AgentID      string   `json:"agentId"`
	Title        string   `json:"title"`
	Instructions string   `json:"instructions"`
	Priority     string   `json:"priority"`
	Links        []string `json:"links"`
	// Author 任務的建立者署名;只有伺服器管理員(例如 omixor-web 內建核心)給的才算數,其他人一律是自己的 email
	Author string `json:"author,omitempty"`
}

// TaskFilter 列任務的條件:AgentID 空 = 全部角色;Status 空或 all = 全部,open = 未結束,或單一狀態;
// ReportTo 不空 = 只列依組織鏈回報給這個節點的任務(me、user:<email>、agent:<角色 id>;見 chain.go)
type TaskFilter struct {
	AgentID  string `json:"agentId,omitempty"`
	Status   string `json:"status,omitempty"`
	ReportTo string `json:"reportTo,omitempty"`
}

// TaskReport 回報任務;nil 表示沒給
type TaskReport struct {
	Status *string  `json:"status,omitempty"`
	Result *string  `json:"result,omitempty"`
	Note   *string  `json:"note,omitempty"`
	Links  []string `json:"links,omitempty"`
}

// ClaimInput 領取任務:TaskID 空 = 領該角色最優先、最早的待領取任務
type ClaimInput struct {
	AgentID string `json:"agentId"`
	TaskID  string `json:"taskId,omitempty"`
}

// ClaimResult 領取結果;沒有可領的任務時 Task 與 Credential 都是 nil
type ClaimResult struct {
	Agent      *model.Agent      `json:"agent,omitempty"`
	Task       *model.AgentTask  `json:"task"`
	Credential *model.AgentGrant `json:"credential,omitempty"`
}

func priorityRank(p string) int {
	for i, v := range model.IssuePriorities {
		if v == p {
			return i
		}
	}
	return len(model.IssuePriorities)
}

// insertTask 排一筆新任務(呼叫端要拿著 agents.mu);by 是建立者的 actor id(系統署名用 model.SystemActor*)
func (s *Service) insertTask(org string, g *model.Agent, title, instructions, priority, by string, links []string) (*model.AgentTask, error) {
	return s.insertTaskFor(org, g, title, instructions, priority, by, links, nil)
}

// insertTaskFor 同 insertTask,多一個例行任務的 id(例行任務排的任務才有)
func (s *Service) insertTaskFor(org string, g *model.Agent, title, instructions, priority, by string, links []string, routineID *string) (*model.AgentTask, error) {
	if priority == "" {
		priority = "normal"
	}
	if !model.OneOf(priority, model.IssuePriorities) {
		return nil, fail(400, "priority 必須是 %s 之一", strings.Join(model.IssuePriorities, "、"))
	}
	now := s.mt()
	t := model.AgentTask{Title: title, Instructions: instructions, AgentID: g.ID, Status: "queued", Priority: priority,
		CreatedBy: by, RoutineID: routineID, CreatedAt: now, UpdatedAt: now, Links: model.CleanLabels(links), Notes: []model.AgentTaskNote{}}
	saved, err := s.Store.CreateAgentTask(org, t)
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// AssignTask 派任務給角色:組織管理員可以派給任何角色;一般成員要組織鏈上有 user:<自己> → agent:<目標> 的邊
// (否則 403,訊息說明可以派給誰),任務說明開頭寫上那條邊的 label(見 chain.go)
func (s *Service) AssignTask(p *model.Principal, org string, in TaskInput) (*model.AgentTask, error) {
	role, err := s.orgRole(p, org)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, fail(400, "要給任務標題")
	}
	g, err := s.mustAgent(org, in.AgentID)
	if err != nil {
		return nil, err
	}
	instructions := in.Instructions
	if role != "admin" {
		label, err := s.memberAssign(p, org, g)
		if err != nil {
			return nil, err
		}
		instructions = model.LabeledInstructions(label, instructions)
	}
	by, err := s.ResolveAuthor(p, org, in.Author, AuthorIgnore) // 伺服器管理員可代角色與系統署名;其他人一律是自己
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.insertTask(org, g, title, instructions, in.Priority, by.ID, in.Links)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "agent.task.assign", org, map[string]any{"task": t.ID, "agent": g.ID})
	return s.presentTask(t)
}

func (s *Service) findTask(org, id string) (*model.AgentTask, error) {
	t, err := s.Store.GetAgentTask(org, strings.ToUpper(strings.TrimSpace(id)))
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "找不到任務 %s", id)
	}
	return t, err
}

// ListAgentTasks 列任務(新的在前)。角色身分只列自己的
func (s *Service) ListAgentTasks(p *model.Principal, org string, f TaskFilter) ([]model.AgentTask, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(f.AgentID)
	reportTo := "" // 回報對象的 actor id(嚴格解析,查詢前就解析好)
	if strings.TrimSpace(f.ReportTo) != "" {
		n, err := s.reportToActor(p, org, f.ReportTo)
		if err != nil {
			return nil, err
		}
		reportTo = n
	}
	if p.Agent != nil && reportTo != "" { // 角色身分只能查回報給自己的(它委派出去的任務)
		me, err := s.agentActorID(org, p.Agent.AgentID)
		if err != nil {
			return nil, err
		}
		if reportTo != me {
			return nil, fail(403, "角色身分只能查回報給自己的任務")
		}
		agentID = ""
	} else if p.Agent != nil {
		agentID = p.Agent.AgentID
	} else if agentID != "" {
		g, err := s.mustAgent(org, agentID)
		if err != nil {
			return nil, err
		}
		agentID = g.ID
	}
	st := strings.TrimSpace(f.Status)
	if st != "" && st != "all" && st != "open" && !model.OneOf(st, model.AgentTaskStatuses) {
		return nil, fail(400, "status 不正確:%s", st)
	}
	all, err := s.Store.ListAgentTasks(org)
	if err != nil {
		return nil, err
	}
	out := []model.AgentTask{}
	for _, t := range all {
		if agentID != "" && t.AgentID != agentID {
			continue
		}
		if reportTo != "" && (t.CreatedBy != reportTo || !reportedTo(t)) {
			continue
		}
		switch st {
		case "", "all":
		case "open":
			if !model.TaskIsOpen(t.Status) {
				continue
			}
		default:
			if t.Status != st {
				continue
			}
		}
		out = append(out, t)
	}
	return s.presentTasks(out)
}

// reportToActor reportTo 篩選的輸入 → actor id:me = 呼叫者(角色身分 = 它自己的角色);其餘用嚴格解析器(人或角色;找不到 400)
func (s *Service) reportToActor(p *model.Principal, org, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "me") {
		if p.Agent != nil {
			return s.agentActorID(org, p.Agent.AgentID)
		}
		return s.callerActorID(p, org)
	}
	ref, err := s.ResolveWho(p, org, raw, WantPeople)
	if err != nil {
		return "", err
	}
	return ref.ID, nil
}

// GetAgentTask 取一筆任務(角色身分只能取自己的;不是自己的一律 404,不洩漏存在與否)
func (s *Service) GetAgentTask(p *model.Principal, org, id string) (*model.AgentTask, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if p.Agent != nil && t.AgentID != p.Agent.AgentID {
		return nil, fail(404, "找不到任務 %s", id)
	}
	return s.presentTask(t)
}

// saveTaskChange 套用修改並存檔:狀態變了補領取/完成時間;離開 inProgress 就作廢憑證(呼叫端要拿著 agents.mu)
func (s *Service) saveTaskChange(org string, before model.AgentTask, change func(*model.AgentTask)) (*model.AgentTask, error) {
	t := before.Clone()
	change(&t)
	if t.Status != before.Status {
		now := s.mt()
		if t.Status == "inProgress" && t.ClaimedAt == nil {
			t.ClaimedAt = &now
		}
		if t.Status == "done" || t.Status == "canceled" {
			t.CompletedAt = &now
		} else {
			t.CompletedAt = nil
		}
	}
	t.UpdatedAt = s.mt()
	if t.Status != "inProgress" { // 離開進行中:鎖一併解開
		t.LockedUntil, t.ClaimedBy = nil, ""
	}
	if err := s.Store.UpdateAgentTask(org, t); err != nil {
		return nil, err
	}
	if t.Status != "inProgress" {
		if err := s.Store.DeleteAgentCredentials(org, t.ID, ""); err != nil {
			return nil, err
		}
	}
	return &t, nil
}

// CancelTask 取消未結束的任務(組織管理員);憑證作廢
func (s *Service) CancelTask(p *model.Principal, org, id string) (*model.AgentTask, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if !model.TaskIsOpen(t.Status) {
		return nil, fail(409, "%s 已經%s,不能取消", t.ID, model.TaskStatusLabel(t.Status))
	}
	u, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) { x.Status = "canceled" })
	if err == nil {
		s.Audit(p, "agent.task.cancel", org, map[string]any{"task": t.ID})
	}
	return s.presentTaskErr(u, err)
}

// RequeueTask 把任務重新排回待領取(組織管理員;已完成、已取消的也可以);憑證作廢
func (s *Service) RequeueTask(p *model.Principal, org, id string) (*model.AgentTask, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if t.Status == "queued" {
		return s.presentTask(t)
	}
	u, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) { x.Status = "queued" })
	if err == nil {
		s.Audit(p, "agent.task.requeue", org, map[string]any{"task": t.ID})
	}
	return s.presentTaskErr(u, err)
}

// ================= Issue 指派觸發 =================

// resolveAssignee 把 Issue 的 assignee 換成角色 id;空白是不指派;角色不存在回 400
func (s *Service) resolveAssignee(org, assignee string) (string, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return "", nil
	}
	g, err := s.findAgent(org, assignee)
	if errors.Is(err, store.ErrNotFound) {
		return "", fail(400, "assignee 指定的角色不存在:%s", assignee)
	}
	if err != nil {
		return "", err
	}
	return g.ID, nil
}

func hasLink(links []string, id string) bool {
	for _, l := range links {
		if strings.EqualFold(l, id) {
			return true
		}
	}
	return false
}

// linksIssue 任務的相關連結有沒有指向這張 Issue:比對內部 id 與目前的 key;其他像 key 的連結(舊的 key、手動填的 ISS-<n>)
// 透過別名表解析後比對
func (s *Service) linksIssue(org string, links []string, i model.Issue) bool {
	if hasLink(links, i.ID) || (i.Key != "" && hasLink(links, i.Key)) {
		return true
	}
	for _, l := range links {
		if l = strings.TrimSpace(l); model.IsUUID(l) || model.IssueKeyNumber(l) == 0 {
			continue
		}
		if id, err := s.Store.ResolveIssueID(org, l); err == nil && id == i.ID {
			return true
		}
	}
	return false
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// syncAssignee Issue 的指派變了之後:舊角色還沒領取(queued)的這個 Issue 的任務取消(已在進行中的不動);
// 新角色(存在、active、Issue 還開著)沒有「未結束」的這個 Issue 的任務就排一筆。
// 失敗只記操作紀錄,不讓 Issue 的儲存失敗(Issue 已經存好,重試會重複建立)。
func (s *Service) syncAssignee(p *model.Principal, org string, issue model.Issue, old, by string) {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	tasks, err := s.Store.ListAgentTasks(org)
	if err != nil {
		s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": auditErrText(err)})
		return
	}
	if old != "" && old != issue.Assignee {
		for _, t := range tasks {
			if t.AgentID == old && t.Status == "queued" && s.linksIssue(org, t.Links, issue) {
				if _, err := s.saveTaskChange(org, t, func(x *model.AgentTask) { x.Status = "canceled" }); err != nil {
					s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": auditErrText(err)})
				}
			}
		}
	}
	if issue.Assignee == "" || issue.Status != "open" {
		return
	}
	g, err := s.Store.GetAgent(org, issue.Assignee)
	if err != nil || !g.Active {
		return
	}
	for _, t := range tasks {
		if t.AgentID == g.ID && model.TaskIsOpen(t.Status) && s.linksIssue(org, t.Links, issue) {
			return // 這個 Issue 已經有一筆未結束的任務,不重複排
		}
	}
	instr := fmt.Sprintf("請處理 Issue %s「%s」(優先度 %s)。\n\n%s\n\n用 issues(id: %s)讀完整內容與留言;處理完在 Issue 留言說明結果,再用 agent_task_report 回報這筆任務。",
		issue.Display(), issue.Title, issue.Priority, truncRunes(strings.TrimSpace(issue.Body), 1000), issue.Display())
	byID, err := s.taskLegacyActorID(org, by) // 呼叫端給的署名(actor id 或舊字串)→ actor id
	if err != nil {
		s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": auditErrText(err)})
		return
	}
	// 相關連結存內部 id(搬專案換 key 也不會失聯);標題與說明寫 key
	t, err := s.insertTask(org, g, fmt.Sprintf("處理 %s:%s", issue.Display(), issue.Title), instr, issue.Priority, byID, []string{issue.ID})
	if err != nil {
		s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": auditErrText(err)})
		return
	}
	s.Audit(p, "issue.assign", org, map[string]any{"issue": issue.ID, "agent": g.ID, "task": t.ID})
}
