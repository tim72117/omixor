// agents_claim.go 領取與回報:領取者署名與鎖定訊息、領取驗證碼與密鑰、領取、回報(續約)、放棄領取、逾時租約回收

package service

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// MaxLeaseTotal 一次領取(租約)從領取時間起算的鎖定總長上限;持有者回報進度可以續約,但不會超過這個,避免永遠鎖住
const MaxLeaseTotal = 8 * time.Hour

// claimedByLabel 給錯誤訊息看的領取者(相容字串;退役的角色或查不到寫 actor id 以外的空白時當沒有人):沒有領取者寫「沒有人」
func (s *Service) claimedByLabel(t model.AgentTask) string {
	if t.ClaimedBy == "" {
		return "沒有人"
	}
	if n, err := s.LoadActors(t.ClaimedBy); err == nil {
		if l := n.Legacy(t.ClaimedBy, model.LegacyFree); l != "" {
			return l
		}
	}
	return "別人"
}

// lockLabel 鎖定到期時間的顯示(本地時區 HH:MM;不是今天就加月-日)
func lockLabel(until, now time.Time) string {
	u, n := until.In(time.Local), now.In(time.Local)
	if u.YearDay() != n.YearDay() || u.Year() != n.Year() {
		return u.Format("01-02 15:04")
	}
	return u.Format("15:04")
}

// lockedErr 任務被鎖住時的 409(不含領取者的憑證,只有名稱)
func (s *Service) lockedErr(t model.AgentTask, now time.Time) error {
	who := ""
	if t.ClaimedBy != "" {
		who = "(" + s.claimedByLabel(t) + ")"
	}
	return fail(409, "%s 已被領取%s、處理中,鎖定到 %s;要換人請先由領取者回報,或請組織管理員退回待領取", t.ID, who, lockLabel(t.LockedUntil.Time, now))
}

// ClaimCode 由憑證雜湊算出 6 位數的領取驗證碼(同一次領取不變;只用來確認執行者對的是哪一次領取,不是密碼)
func ClaimCode(hash []byte) string {
	if len(hash) < 4 {
		return ""
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(hash[:4])%1000000)
}

func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ClaimTask 領取任務(組織成員;角色身分不能領):任務改成進行中,回傳任務與一次性憑證。
// 領取即鎖定(租約):任務進行中、lockedUntil = 憑證到期時間;鎖定期間任何人再領都回 409(連原領取者也不行),
// 要換人得等回報離開進行中、管理員退回/取消,或鎖逾時(逾時後可重領,舊憑證作廢)。需要協助(blocked)的不鎖。
// 給 TaskID 就領那筆(待領取、需要協助、鎖已逾時的進行中都可以領);否則領該角色最優先、最早的待領取任務。沒有任務時 Task 是 nil。
func (s *Service) ClaimTask(p *model.Principal, org string, in ClaimInput) (*ClaimResult, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	g, err := s.mustAgent(org, in.AgentID)
	if err != nil {
		return nil, err
	}
	if !g.Active { // 停用的角色不能領任務(指定任務 id 或不指定都一樣;ClaimNext 本來就只挑啟用中的角色)
		return nil, fail(409, "角色 %s 已停用,不能領取任務", g.Name)
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	var pick *model.AgentTask
	if strings.TrimSpace(in.TaskID) != "" {
		t, err := s.findTask(org, in.TaskID)
		if err != nil {
			return nil, err
		}
		if t.AgentID != g.ID {
			owner := t.AgentID
			if o, err := s.Store.GetAgent(org, t.AgentID); err == nil {
				owner = o.Name
			}
			return nil, fail(409, "%s 是指派給 %s 的,不是 %s", t.ID, owner, g.Name)
		}
		if !model.TaskIsOpen(t.Status) {
			return nil, fail(409, "%s 已經%s,不能再領", t.ID, model.TaskStatusLabel(t.Status))
		}
		if t.Locked(s.now()) { // 領取租約還沒到期:任何人(含原領取者)都不能再領
			return nil, s.lockedErr(*t, s.now())
		}
		pick = t
	} else {
		all, err := s.Store.ListAgentTasks(org)
		if err != nil {
			return nil, err
		}
		queued := []model.AgentTask{}
		for _, t := range all {
			if t.AgentID == g.ID && t.Status == "queued" {
				queued = append(queued, t)
			}
		}
		sortQueued(queued)
		if len(queued) > 0 {
			c := queued[0]
			pick = &c
		}
	}
	if pick == nil {
		return &ClaimResult{Agent: g}, nil
	}
	return s.claimPicked(p, org, g, pick)
}

// sortQueued 待領取任務的順序:優先度高的在前,同優先度先建立的在前
func sortQueued(queued []model.AgentTask) {
	sort.SliceStable(queued, func(i, j int) bool {
		pi, pj := priorityRank(queued[i].Priority), priorityRank(queued[j].Priority)
		if pi != pj {
			return pi < pj
		}
		if !queued[i].CreatedAt.Equal(queued[j].CreatedAt.Time) {
			return queued[i].CreatedAt.Before(queued[j].CreatedAt.Time)
		}
		return queued[i].Number() < queued[j].Number()
	})
}

// claimPicked 領取挑好的任務:條件式鎖定、發憑證(呼叫端要拿著 agents.mu)
func (s *Service) claimPicked(p *model.Principal, org string, g *model.Agent, pick *model.AgentTask) (*ClaimResult, error) {
	me, err := s.callerActorID(p, org)
	if err != nil {
		return nil, err
	}
	secret, err := newSecret()
	if err != nil {
		return nil, err
	}
	now := s.now()
	exp := now.Add(s.credTTL()).Truncate(time.Second)
	// 領取:任務改進行中、記領取時間與領取者、鎖到憑證到期。條件式寫入(被別人搶先鎖住回 ErrLocked)
	nt := pick.Clone()
	nowT := model.Time{Time: now.Truncate(time.Second)}
	lock := model.Time{Time: exp}
	nt.Status, nt.ClaimedAt, nt.ClaimedBy, nt.LockedUntil, nt.CompletedAt, nt.UpdatedAt = "inProgress", &nowT, me, &lock, nil, nowT
	if err := s.Store.ClaimAgentTask(org, nt, now); err != nil {
		if errors.Is(err, store.ErrLocked) {
			if cur, e2 := s.Store.GetAgentTask(org, nt.ID); e2 == nil && cur.LockedUntil != nil {
				return nil, s.lockedErr(*cur, now)
			}
			return nil, fail(409, "%s 已被別人領取、處理中", nt.ID)
		}
		return nil, err
	}
	claimed, err := s.presentTask(&nt)
	if err != nil {
		return nil, err
	}
	cred := model.AgentCredential{Org: org, AgentID: g.ID, TaskID: claimed.ID, Hash: HashToken(secret),
		IssuedAt: nowT, ExpiresAt: lock}
	if err := s.Store.SaveAgentCredential(cred); err != nil { // 同任務舊的憑證一併作廢
		_ = s.Store.UpdateAgentTask(org, *pick) // 憑證沒存成:還原任務,免得鎖住卻沒人拿得到憑證
		return nil, err
	}
	s.Audit(p, "agent.task.claim", org, map[string]any{"task": claimed.ID, "agent": g.ID, "expiresAt": cred.ExpiresAt.Time.Format(time.RFC3339)})
	return &ClaimResult{Agent: g, Task: claimed, Credential: &model.AgentGrant{AgentID: g.ID, TaskID: claimed.ID, Secret: secret, Code: ClaimCode(cred.Hash),
		ExpiresAt: cred.ExpiresAt.Time.UTC().Format(time.RFC3339)}}, nil
}

// ReportTask 回報任務(進度、結果、狀態)。組織成員可回報任何任務;角色身分只能回報憑證綁定的那一筆(id 空白就是那一筆),
// 而且狀態只能是 inProgress、blocked、done。離開 inProgress(done、blocked、canceled)憑證立刻作廢。
func (s *Service) ReportTask(p *model.Principal, org, id string, in TaskReport) (*model.AgentTask, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	if p.Agent != nil {
		if strings.TrimSpace(id) == "" {
			id = p.Agent.TaskID
		} else if !strings.EqualFold(strings.TrimSpace(id), p.Agent.TaskID) {
			return nil, fail(403, "這組憑證只能回報任務 %s", p.Agent.TaskID)
		}
	}
	if in.Status != nil {
		ok := []string{"inProgress", "blocked", "done"}
		if p.Agent == nil {
			ok = append(ok, "canceled")
		}
		if !model.OneOf(*in.Status, ok) {
			return nil, fail(400, "status 必須是 %s 之一", strings.Join(ok, "、"))
		}
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if p.Agent != nil && t.AgentID != p.Agent.AgentID {
		return nil, fail(404, "找不到任務 %s", id)
	}
	// 人回報:只有這筆任務的領取者本人或組織管理員可以(完成、取消、改狀態、留言都算)。
	// 否則任何組織成員都能把別人領走的任務標成完成或取消,繞過「取消要組織管理員」與領取鎖,備註還會署名成那個角色。
	// 還沒領取的任務(沒有領取者)也只有組織管理員能直接回報;一般成員要先領取
	var me string
	if p.Agent == nil {
		if me, err = s.callerActorID(p, org); err != nil {
			return nil, err
		}
	}
	if p.Agent == nil && (t.ClaimedBy == "" || t.ClaimedBy != me) {
		if err := s.needOrgAdmin(p, org); err != nil {
			if Code(err) == 403 {
				return nil, fail(403, "%s 的領取者是 %s,只有領取者本人或組織管理員可以回報(還沒領取的任務請先領取)", t.ID, s.claimedByLabel(*t))
			}
			return nil, err
		}
	}
	if t.Status == "canceled" {
		return nil, fail(409, "%s 已取消", t.ID)
	}
	if p.Agent != nil && t.Status != "inProgress" {
		return nil, fail(409, "%s 目前不是進行中,不能回報", t.ID)
	}
	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
	}
	noteBy := ""
	if note != "" {
		if noteBy, err = s.agentActorID(org, t.AgentID); err != nil {
			return nil, err
		}
	}
	renewed := false
	updated, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) {
		defer func() {
			// 回報後仍是進行中(含只留言的進度回報):續約,鎖與憑證從現在起再延一個 credTTL,但總長不超過 MaxLeaseTotal
			if x.Status != "inProgress" {
				return
			}
			now := s.now()
			if t.Status != "inProgress" || x.ClaimedAt == nil || x.LockedUntil == nil { // 從別的狀態改回進行中、或舊資料沒有租約:當作新的一段租約
				nowT := model.Time{Time: now.Truncate(time.Second)}
				x.ClaimedAt, x.ClaimedBy = &nowT, t.ClaimedBy
				if p.Agent == nil {
					x.ClaimedBy = me
				}
			}
			until := now.Add(s.credTTL()).Truncate(time.Second)
			if end := x.ClaimedAt.Add(MaxLeaseTotal); until.After(end) {
				until = end
			}
			if x.LockedUntil == nil || until.After(x.LockedUntil.Time) {
				x.LockedUntil = &model.Time{Time: until}
			}
			renewed = true
		}()
		if note != "" {
			x.Notes = append(x.Notes, model.AgentTaskNote{ID: uuid(), Author: noteBy, Text: note, At: s.mt()})
		}
		if in.Status != nil {
			x.Status = *in.Status
		}
		if in.Result != nil {
			x.Result = *in.Result
		}
		if in.Links != nil {
			x.Links = model.CleanLabels(append(append([]string{}, x.Links...), in.Links...))
		}
	})
	if err != nil {
		return nil, err
	}
	if in.Status != nil && *in.Status != t.Status { // 變成 done、blocked:依組織鏈回報給建立者
		s.chainReport(p, org, updated, note)
	}
	if updated, err = s.presentTask(updated); err != nil {
		return nil, err
	}
	if renewed && updated.LockedUntil != nil {
		if err := s.Store.ExtendAgentCredential(org, updated.ID, updated.LockedUntil.Time); err != nil {
			return nil, err
		}
	}
	if in.Status != nil && *in.Status != t.Status {
		s.Audit(p, "agent.task."+*in.Status, org, map[string]any{"task": t.ID, "agent": t.AgentID})
	}
	return updated, nil
}

// ClaimNext 從全部啟用中角色的待領取任務挑優先度最高(同優先度最舊)的一筆領走(組織成員;角色身分不能領)。
// agentID 不空就限定該角色。挑選與領取都在 agents.mu 內,兩個執行者不會同時挑到同一筆;
// 若條件式寫入仍被別人搶先鎖住(多實例),換下一筆。沒有可領的任務時 Task 是 nil
// (Agent 只有在限定角色時才有)。
func (s *Service) ClaimNext(p *model.Principal, org, agentID string) (*ClaimResult, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	var only *model.Agent
	if strings.TrimSpace(agentID) != "" {
		g, err := s.mustAgent(org, agentID)
		if err != nil {
			return nil, err
		}
		only = g
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	agents, err := s.Store.ListAgents(org)
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.Agent{}
	for i := range agents {
		if agents[i].Active {
			byID[agents[i].ID] = &agents[i]
		}
	}
	all, err := s.Store.ListAgentTasks(org)
	if err != nil {
		return nil, err
	}
	queued := []model.AgentTask{}
	for _, t := range all {
		if t.Status != "queued" || byID[t.AgentID] == nil || (only != nil && t.AgentID != only.ID) {
			continue
		}
		queued = append(queued, t)
	}
	sortQueued(queued)
	for _, t := range queued {
		pick := t
		res, err := s.claimPicked(p, org, byID[t.AgentID], &pick)
		if err != nil {
			if Code(err) == 409 { // 被別人搶先:換下一筆
				continue
			}
			return nil, err
		}
		return res, nil
	}
	return &ClaimResult{Agent: only}, nil
}

// ReleaseTask 放棄領取:進行中的任務退回待領取、鎖解開、憑證作廢,並留一則備註(署名放棄者,含原因)。
// 只有領取者本人或組織管理員可以(否則 403);任務不是進行中回 409。角色身分不能做。
func (s *Service) ReleaseTask(p *model.Principal, org, id, reason string) (*model.AgentTask, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	role, err := s.orgRole(p, org)
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	t, err := s.findTask(org, id)
	if err != nil {
		return nil, err
	}
	if t.Status != "inProgress" {
		return nil, fail(409, "%s 目前是%s,不是進行中,不能放棄領取", t.ID, model.TaskStatusLabel(t.Status))
	}
	who, err := s.callerActorID(p, org)
	if err != nil {
		return nil, err
	}
	if role != "admin" && (t.ClaimedBy == "" || t.ClaimedBy != who) {
		return nil, fail(403, "%s 是 %s 領取的,只有領取者本人或組織管理員可以放棄", t.ID, s.claimedByLabel(*t))
	}
	text := "放棄領取,退回待領取"
	if r := strings.TrimSpace(reason); r != "" {
		text += ":" + r
	}
	u, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) {
		x.Status = "queued"
		x.Notes = append(x.Notes, model.AgentTaskNote{ID: uuid(), Author: who, Text: text, At: s.mt()})
	})
	if err == nil {
		s.Audit(p, "agent.task.release", org, map[string]any{"task": t.ID, "agent": t.AgentID})
	}
	return s.presentTaskErr(u, err)
}

// expireLeases 把「進行中但領取租約已逾時」的任務退回待領取:留一則系統署名的備註、作廢憑證(saveTaskChange)、寫稽核。
// 呼叫端要拿著 agents.mu。
func (s *Service) expireLeases(org string, now time.Time) error {
	tasks, err := s.Store.ListAgentTasks(org)
	if err != nil {
		return err
	}
	var firstErr error
	for _, t := range tasks {
		if !t.LeaseExpired(now) {
			continue
		}
		u, err := s.saveTaskChange(org, t, func(x *model.AgentTask) {
			x.Status = "queued"
			x.Notes = append(x.Notes, model.AgentTaskNote{ID: uuid(), Author: model.SystemActorOmixor,
				Text: "領取逾時(原領取者未回報),已自動退回待領取", At: s.mt()})
		})
		if err != nil {
			firstErr = err
			continue
		}
		s.Audit(nil, "agent.task.lease_expired", org, map[string]any{"task": u.ID, "agent": u.AgentID, "claimedBy": s.claimedByLabel(t)})
	}
	return firstErr
}
