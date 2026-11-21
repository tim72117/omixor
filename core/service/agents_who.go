// agents_who.go 角色任務的「誰」欄位(created_by、claimed_by、備註作者):store 與服務內部存 actor id,
// 這裡負責呼叫者與角色算出 actor id(寫入端),以及方法出口把 actor id 轉回相容字串(present,批次、不 N+1)

package service

import (
	"strings"

	"omixor-core/model"
)

// ClaimerName 呼叫者的署名字串(給 backend 的 Whoami、「我領取的任務」比對用,對應任務輸出的 ClaimedBy 相容字串):
// 使用者 = 帳號 email(輸出的 claimedBy 也是 omixor_users.email,大小寫一致),沒有 email 退而用 actor handle,再退名稱
func ClaimerName(p *model.Principal) string {
	if e := strings.TrimSpace(p.User.Email); e != "" {
		return e
	}
	if h := strings.TrimSpace(p.Actor.Handle); h != "" {
		return h
	}
	return strings.TrimSpace(p.User.Name)
}

// callerActorID 呼叫者本人的 actor id(寫進 created_by、claimed_by、備註作者)。
// 使用者與角色都保證有 actor(本機使用者也是);SelfActor 失敗是資料錯誤,直接回錯誤,不退回建立 legacy actor。
func (s *Service) callerActorID(p *model.Principal, org string) (string, error) {
	ref, err := s.SelfActor(p)
	if err != nil {
		return "", err
	}
	return ref.ID, nil
}

// agentActorID 角色在任務裡署名用的 actor id(角色的 actor)。角色沒有 actor(id 不符 handle 規則的舊資料、遷移沒做完或資料錯誤)
// 回明確錯誤 ErrAgentNoActor,不建立 sys_legacy 的 ghost(一般寫入路徑不該憑空長出假署名)
func (s *Service) agentActorID(org, agentID string) (string, error) {
	g, err := s.Store.GetAgent(org, agentID)
	if err != nil {
		return "", err
	}
	if g.ActorID == "" {
		return "", ErrAgentNoActor
	}
	return g.ActorID, nil
}

// ErrAgentNoActor 角色沒有 actor_id:遷移未完成或資料錯誤,不能署名
var ErrAgentNoActor = &Error{Code: 500, Msg: "這個角色沒有對應的 actor(遷移未完成或資料錯誤),請聯絡管理員"}

// taskLegacyActorID 舊字串(email、agent:<id>、routine…,或本來就是 actor id)→ actor id(寬鬆解析器,缺的會建;空字串回空)。
// 給內部流程與匯入用(Issue 指派排的任務署名、ImportAgents 的舊資料);使用者輸入請走 ResolveAuthor / ResolveWho
func (s *Service) taskLegacyActorID(org, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return s.Store.ResolveLegacy(org, raw)
}

// taskActorIDs 一批任務裡出現的 actor id(建立者、領取者、備註作者)
func taskActorIDs(ts []model.AgentTask) []string {
	var ids []string
	for _, t := range ts {
		ids = append(ids, t.CreatedBy, t.ClaimedBy)
		for _, n := range t.Notes {
			ids = append(ids, n.Author)
		}
	}
	return ids
}

// presentTasks 任務出口:created_by、claimed_by、備註作者從 actor id 換成相容字串(一次批次查詢)。回傳新的切片與備註(不改 store 回來的原件)
func (s *Service) presentTasks(ts []model.AgentTask) ([]model.AgentTask, error) {
	if len(ts) == 0 {
		return ts, nil
	}
	n, err := s.LoadActors(taskActorIDs(ts)...)
	if err != nil {
		return nil, err
	}
	out := make([]model.AgentTask, len(ts))
	for i, t := range ts {
		out[i] = presentTaskWith(n, t)
	}
	return out, nil
}

func presentTaskWith(n *Names, t model.AgentTask) model.AgentTask {
	t.CreatedByActor, t.ClaimedByActor = n.View(t.CreatedBy), n.View(t.ClaimedBy)
	t.CreatedBy = n.Legacy(t.CreatedBy, model.LegacyFree)
	t.ClaimedBy = n.Legacy(t.ClaimedBy, model.LegacyFree)
	notes := make([]model.AgentTaskNote, len(t.Notes))
	for i, x := range t.Notes {
		x.AuthorActor = n.View(x.Author)
		x.Author = n.Legacy(x.Author, model.LegacyFree)
		notes[i] = x
	}
	t.Notes = notes
	return t
}

// presentTask 單筆(回報、領取、派工等方法的出口)
func (s *Service) presentTask(t *model.AgentTask) (*model.AgentTask, error) {
	if t == nil {
		return nil, nil
	}
	l, err := s.presentTasks([]model.AgentTask{*t})
	if err != nil {
		return nil, err
	}
	return &l[0], nil
}

// presentTaskErr 方法出口:err 不是 nil 原樣回;否則把 t 轉成相容字串
func (s *Service) presentTaskErr(t *model.AgentTask, err error) (*model.AgentTask, error) {
	if err != nil {
		return nil, err
	}
	return s.presentTask(t)
}
