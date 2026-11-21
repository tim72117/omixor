package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// 刪角色與角色任務轉移。
//
// 資料庫對「任務 → 角色」有外鍵(restrict):名下還有任務(任何狀態)的角色不能刪,要先把任務轉給別的角色。
//   - DeleteAgent(…, transferTo):名下沒有任務就直接刪;有任務而且沒給 transferTo → 409,Data 帶各狀態的任務數
//     (code = agentHasTasks);給了 transferTo → 在同一個交易(store.Atomic)裡轉移任務、處理指派與關聯再刪角色,任何一步失敗全部還原。
//   - TransferAgentTasks:不刪角色的單獨轉移(範圍 all = 全部,open = 只轉待領取與需要協助)。
//
// 轉移規則(store.transferAgentTasks):待領取、需要協助保持狀態改角色;進行中(已被領走)先退回待領取、
// 清掉鎖與領取者、作廢憑證;已完成、已取消也一起轉;每筆加一則備註「由 <舊角色> 轉移,<時間>」。
// 刪角色時另外:Issue 指派給被刪角色的改成目標角色;組織鏈的邊、頻道成員裡指向它的節點移除;外部 MCP 權限清掉;例行任務隨角色刪除;
// docAccess 不轉。

// AgentHasTasksCode 409 的 Data.code:角色名下還有任務,要先轉移
const AgentHasTasksCode = "agentHasTasks"

// TransferScope 單獨轉移的範圍
const (
	TransferAll  = "all"  // 全部(進行中的先退回待領取,已完成與已取消也轉)
	TransferOpen = "open" // 只轉待領取與需要協助
)

// AgentTransferInput 單獨轉移任務的參數
type AgentTransferInput struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Scope string `json:"scope,omitempty"` // all 或 open;空 = open(比較保守)
}

// AgentTransferResult 轉移的結果
type AgentTransferResult struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Scope string `json:"scope"`
	store.AgentTransferResult
}

// taskStatusOrder 回報各狀態任務數時的順序
var taskStatusOrder = []string{"queued", "inProgress", "blocked", "done", "canceled"}

func countsText(by map[string]int) string {
	parts := []string{}
	for _, st := range taskStatusOrder {
		if n := by[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", model.TaskStatusLabel(st), n))
		}
	}
	return strings.Join(parts, "、")
}

// DeleteAgent 刪除角色(組織管理員);名下有任務時回 409,要用 DeleteAgentTo 帶 transferTo
func (s *Service) DeleteAgent(p *model.Principal, org, id string) error {
	return s.DeleteAgentTo(p, org, id, "")
}

// DeleteAgentTo 刪除角色(組織管理員)。名下沒有任務就直接刪;有任務:transferTo 空 → 409(Data:code、agent、taskCounts、total),
// 給了就在同一個交易裡轉給那個角色再刪(不能是自己、不存在或已停用的角色)
func (s *Service) DeleteAgentTo(p *model.Principal, org, id, transferTo string) error {
	if err := s.needOrgAdmin(p, org); err != nil {
		return err
	}
	g, err := s.mustAgent(org, id)
	if err != nil {
		return err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	tasks, err := s.Store.ListAgentTasks(org)
	if err != nil {
		return err
	}
	by, total := store.TaskCounts(tasks, g.ID)
	var to *model.Agent
	if strings.TrimSpace(transferTo) != "" { // 有給目標就一律解析、驗證(名下沒任務時,指派給它的 Issue 也要改給目標)
		if to, err = s.transferTarget(org, g, transferTo); err != nil {
			return err
		}
	}
	if total > 0 && to == nil {
		return &Error{Code: 409,
			Msg:  fmt.Sprintf("角色 %s 名下還有 %d 筆任務(%s),要先轉移給其他角色才能刪除", g.Name, total, countsText(by)),
			Data: map[string]any{"code": AgentHasTasksCode, "agent": g.ID, "taskCounts": by, "total": total}}
	}
	now := s.mt()
	var res store.AgentTransferResult
	issues := 0
	err = s.Store.Atomic(func(tx store.Store) error {
		if to != nil {
			var err error
			if res, err = tx.TransferAgentTasks(org, s.transferSpec(g, to, false, now)); err != nil {
				return err
			}
		}
		if err := tx.DeleteAgentCredentials(org, "", g.ID); err != nil {
			return err
		}
		if err := tx.DeleteAgent(org, g.ID); err != nil {
			return err
		}
		if err := s.pruneChainNodeOn(tx, org, g.ActorID); err != nil {
			return err
		}
		if err := s.pruneChannelNodeOn(tx, org, g.ActorID); err != nil {
			return err
		}
		if g.ActorID != "" { // 專案成員資格一併清掉(同 id 重建的角色是新的 actor,不繼承)
			if _, err := tx.RemoveProjectMembershipsOf(org, g.ActorID); err != nil {
				return err
			}
		}
		// 外部 MCP 權限一併清掉(同 id 重建的角色不會繼承);在 s.agents.mu 內,和 SaveMCPGrants 互斥
		if err := s.pruneAgentMCPGrantsOn(tx, org, g.ID); err != nil {
			return err
		}
		n, err := s.reassignIssues(tx, org, g.ActorID, to, now)
		issues = n
		return err
	})
	if errors.Is(err, store.ErrInUse) { // 交易期間別的程式又派了任務給它
		// 帶最新的各狀態任務數,前端的視窗據此更新(不是舊的數字)
		e := &Error{Code: 409, Msg: fmt.Sprintf("角色 %s 名下又有新的任務,請確認筆數後再轉移一次", g.Name)}
		if latest, lerr := s.Store.ListAgentTasks(org); lerr == nil {
			by, total := store.TaskCounts(latest, g.ID)
			e.Data = map[string]any{"code": AgentHasTasksCode, "agent": g.ID, "taskCounts": by, "total": total}
		}
		return e
	}
	if errors.Is(err, store.ErrBadRef) { // 目標角色剛好被刪
		return fail(409, "目標角色已不存在,請重新整理後再試")
	}
	if err != nil {
		return err
	}
	s.pruneAllRestrictedTasks(p, org) // 任務可能轉給了不是受限專案成員的角色(拿著 agents.mu)
	detail := map[string]any{"agent": g.ID}
	if to != nil {
		detail["transferTo"] = to.ID
		detail["tasks"] = res.Total
		detail["issues"] = issues
		if res.Total > 0 {
			s.Audit(p, "agent.task.transfer", org, map[string]any{"from": g.ID, "to": to.ID, "scope": TransferAll, "deleted": true,
				"total": res.Total, "byStatus": res.ByStatus, "released": res.Released})
		}
	}
	s.Audit(p, "agent.delete", org, detail)
	return nil
}

// TransferAgentTasks 不刪角色,把 from 名下的任務轉給 to(組織管理員)。範圍 all 或 open(空 = open)
func (s *Service) TransferAgentTasks(p *model.Principal, org string, in AgentTransferInput) (*AgentTransferResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	switch scope {
	case "":
		scope = TransferOpen
	case TransferAll, TransferOpen:
	default:
		return nil, fail(400, "scope 要是 all(全部)或 open(只轉待領取與需要協助)")
	}
	if strings.TrimSpace(in.From) == "" || strings.TrimSpace(in.To) == "" {
		return nil, fail(400, "要給 from(來源角色)與 to(目標角色)")
	}
	from, err := s.mustAgent(org, in.From)
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	to, err := s.transferTarget(org, from, in.To)
	if err != nil {
		return nil, err
	}
	res, err := s.Store.TransferAgentTasks(org, s.transferSpec(from, to, scope == TransferOpen, s.mt()))
	if errors.Is(err, store.ErrBadRef) {
		return nil, fail(409, "目標角色已不存在,請重新整理後再試")
	}
	if err != nil {
		return nil, err
	}
	s.pruneAllRestrictedTasks(p, org) // 轉給不是受限專案成員的角色:那個專案 Issue 的待領取任務取消
	s.Audit(p, "agent.task.transfer", org, map[string]any{"from": from.ID, "to": to.ID, "scope": scope,
		"total": res.Total, "byStatus": res.ByStatus, "released": res.Released})
	return &AgentTransferResult{From: from.ID, To: to.ID, Scope: scope, AgentTransferResult: res}, nil
}

// transferTarget 找轉移的目標角色:不存在 404;不能是來源自己;已停用的不行(轉過去沒人領)
func (s *Service) transferTarget(org string, from *model.Agent, idOrName string) (*model.Agent, error) {
	to, err := s.mustAgent(org, idOrName)
	if err != nil {
		return nil, err
	}
	if to.ID == from.ID {
		return nil, fail(400, "不能轉給自己")
	}
	if !to.Active {
		return nil, fail(400, "目標角色 %s 已停用,不能轉給它", to.Name)
	}
	return to, nil
}

// transferSpec 轉移的參數(備註:由 <舊角色> 轉移,<時間>)
func (s *Service) transferSpec(from, to *model.Agent, openOnly bool, now model.Time) store.AgentTransfer {
	return store.AgentTransfer{From: from.ID, To: to.ID, OpenOnly: openOnly, At: now.Time, NoteAuthor: model.SystemActorOmixor,
		NoteText: fmt.Sprintf("由 %s 轉移,%s", from.Name, now.In(time.Local).Format("2006-01-02 15:04"))}
}

// reassignIssues 指派給角色(actorID 是它的 actor id)的 Issue:to 不是 nil 就改指派給 to,否則清掉指派。每一個在 Issue 的鎖裡重讀最新的再改,留言不動;
// 拿著 agents.mu 呼叫(順序 agents.mu → issueLock)。回傳改了幾個
func (s *Service) reassignIssues(st store.Store, org, actorID string, to *model.Agent, now model.Time) (int, error) {
	if actorID == "" { // 沒有 actor 的舊角色不可能被指派(空字串會比到沒指派的 Issue)
		return 0, nil
	}
	issues, err := st.ListIssues(org)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, i := range issues {
		if i.Assignee != actorID {
			continue
		}
		if err := s.reassignIssue(st, org, i.ID, actorID, to, now); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Service) reassignIssue(st store.Store, org, issueID, actorID string, to *model.Agent, now model.Time) error {
	mu := s.issueLock(org, issueID)
	mu.Lock()
	defer mu.Unlock()
	i, err := st.GetIssue(org, issueID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if i.Assignee != actorID {
		return nil
	}
	i.Assignee = ""
	if to != nil {
		if to.ActorID == "" {
			return fail(500, "目標角色 %s 沒有 actor,不能改派 Issue", to.ID)
		}
		i.Assignee = to.ActorID
		if i.Project != "" { // 受限專案:目標角色不是專案成員就只清掉指派(不能讓它排到帶出 Issue 內容的任務)
			if pr, err := st.GetProject(org, i.Project); err == nil && pr.Restricted {
				if m, err := st.ProjectMemberships(org, to.ActorID); err != nil {
					return err
				} else if m[i.Project] == "" {
					i.Assignee = ""
				}
			}
		}
	}
	i.UpdatedAt, i.Comments = now, nil
	return st.UpdateIssue(org, *i)
}
