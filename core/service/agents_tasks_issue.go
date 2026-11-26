package service

// agents_tasks_issue.go 任務與來源 Issue 的連動(ISS-43):Issue 關閉時取消還沒領取的任務、任務完成時把結果貼回 Issue。
// 由子項產生的任務不在這裡處理(它的狀態與子項連動,見 issue_items_task.go)。

import (
	"fmt"
	"strings"

	"omixor-core/model"
)

// maxTaskResultNote 貼回 Issue 的任務結果最多幾個字
const maxTaskResultNote = 4000

// cancelQueuedTasks 取消 tasks 裡角色 agentID 還沒領取(queued)、連到這張 Issue 的任務(呼叫端要拿著 agents.mu)。
// 改指派與關閉 Issue 共用;失敗只記操作紀錄
func (s *Service) cancelQueuedTasks(p *model.Principal, org string, issue model.Issue, agentID string, tasks []model.AgentTask) {
	for _, t := range tasks {
		if t.AgentID != agentID || t.Status != "queued" || !s.linksIssue(org, t.Links, issue) {
			continue
		}
		if _, err := s.saveTaskChange(org, t, func(x *model.AgentTask) { x.Status = "canceled" }); err != nil {
			s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": auditErrText(err)})
		}
	}
}

// cancelClosedIssueTasks Issue 關閉了:指派角色還沒領取的任務取消(進行中的不動;重新打開不會自動重排)。
// issue.Assignee 是角色 id(handle);在 Issue 的鎖外呼叫(會拿 agents.mu)
func (s *Service) cancelClosedIssueTasks(p *model.Principal, org string, issue model.Issue) {
	if issue.Assignee == "" {
		return
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	tasks, err := s.Store.ListAgentTasks(org)
	if err != nil {
		s.Audit(p, "issue.assign.error", org, map[string]any{"issue": issue.ID, "error": auditErrText(err)})
		return
	}
	s.cancelQueuedTasks(p, org, issue, issue.Assignee, s.dropItemTasks(org, tasks))
}

// noteTaskResult 任務完成:在它連到的 Issue(links 裡第一張還在的)留一則系統留言。不會關閉 Issue。
// 署名看是誰回報的:角色身分(角色憑證、執行者與 runner 代角色的 WorkerReport)署名任務的角色,沒填結果也留言;
// 人手動完成署名本人,只貼本次填的,本次沒填就不留言(不能替角色發言)。
// t.Links 要是回報前的連結(任務是為哪張 Issue 做的);拿著 agents.mu 呼叫(順序 agents.mu → issueLock)
func (s *Service) noteTaskResult(p *model.Principal, org string, t model.AgentTask, result string) {
	result = strings.TrimSpace(result)
	if s.itemOfTask(org, t) != nil || (p.Agent == nil && result == "") {
		return
	}
	for _, l := range t.Links {
		issue := s.linkedIssue(org, l)
		if issue == nil {
			continue
		}
		who, err := s.taskNoteAuthor(p, org, t)
		if err != nil {
			s.Audit(p, "agent.task.note.error", org, map[string]any{"task": t.ID, "error": auditErrText(err)})
			return
		}
		if result == "" {
			result = "(沒有填結果)"
		}
		s.issueNote(org, issue.ID, who, fmt.Sprintf("完成任務 %s「%s」。\n\n%s", t.ID, t.Title, truncRunes(result, maxTaskResultNote)))
		return
	}
}

// taskNoteAuthor 完成留言的署名 actor:角色身分回報是任務的角色,人回報是本人
func (s *Service) taskNoteAuthor(p *model.Principal, org string, t model.AgentTask) (string, error) {
	if p.Agent != nil {
		return s.agentActorID(org, t.AgentID)
	}
	return s.callerActorID(p, org)
}

// firstNonEmpty 第一個去掉空白後不是空的字串
func firstNonEmpty(list ...string) string {
	for _, v := range list {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
