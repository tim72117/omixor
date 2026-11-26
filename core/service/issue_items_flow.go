package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// 子項的領取、放棄、回報、卡住、取消、提升(規則見 issue_items.go 檔頭)。

// itemPrereq 前置子項還沒 done 回 409(canceled 的前置也算沒完成:要先改掉依賴或重做)
func (s *Service) itemPrereq(c *itemCtx, it *model.IssueItem) error {
	if it.DependsOn == "" {
		return nil
	}
	for _, x := range c.list {
		if x.ID == it.DependsOn {
			if x.Status != model.ItemDone {
				return fail(409, "%s 的前置 %s 還沒完成(%s)", itemKey(c.issue, *it), itemKey(c.issue, x), itemStatusLabel(x.Status))
			}
			return nil
		}
	}
	return nil
}

// itemLease 現在起一段租約的到期時間(同任務憑證的長度)
func (s *Service) itemLease() time.Time { return s.now().Add(s.credTTL()).Truncate(time.Second) }

// StartItem 開始做 = 領取(同 ClaimItem;給「開始」語意的入口)
func (s *Service) StartItem(p *model.Principal, org, issueRef, itemRef string) (*ItemClaimResult, error) {
	return s.ClaimItem(p, org, issueRef, itemRef, ItemClaimInput{})
}

// ClaimItem 領取子項(只有人;見 issue_items.go 檔頭)。成功後狀態是 doing、領取者是呼叫者、租約到期前別人不能再領;
// 負責人是角色時另外建立並領取一筆綁定的 agent_task,結果帶一次性憑證
func (s *Service) ClaimItem(p *model.Principal, org, issueRef, itemRef string, in ItemClaimInput) (*ItemClaimResult, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	role, err := s.orgRole(p, org)
	if err != nil {
		return nil, err
	}
	c, err := s.itemOf(p, org, issueRef, itemRef)
	if err != nil {
		return nil, err
	}
	me, err := s.callerActorID(p, org)
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	res, err := s.claimLocked(p, org, c, role, me, in.Note)
	if err != nil {
		return nil, err
	}
	return s.claimOut(p, org, c.issue, res)
}

func (s *Service) claimOut(p *model.Principal, org string, issue *model.Issue, res *ItemClaimResult) (*ItemClaimResult, error) {
	out, err := s.presentOne(p, org, issue, res.Item.ID)
	if err != nil {
		return nil, err
	}
	res.Item = out
	return res, nil
}

// claimLocked 領取的本體(呼叫端拿著 agents.mu)。Item 回傳的是 store 的原樣
func (s *Service) claimLocked(p *model.Principal, org string, c *itemCtx, role, me, note string) (*ItemClaimResult, error) {
	key := itemKey(c.issue, *c.it)
	cur, err := s.itemFresh(org, c.it, key)
	if err != nil {
		return nil, err
	}
	switch cur.Status {
	case model.ItemDone, model.ItemCanceled:
		return nil, fail(409, "%s 已經%s,不能再領", key, itemStatusLabel(cur.Status))
	case model.ItemBlocked:
		return nil, fail(409, "%s 卡住中(%s);先解除卡住(unblock)才能領", key, cur.BlockReason)
	}
	if err := s.itemPrereq(c, cur); err != nil {
		return nil, err
	}
	g, err := s.itemRoleAgent(org, cur.Assignee)
	if err != nil {
		return nil, err
	}
	if cur.Assignee != "" && cur.Assignee != me && g == nil && role != "admin" {
		return nil, fail(403, "%s 指派給別人,只有負責人或組織管理員可以領取", key)
	}
	if g != nil {
		if !g.Active {
			return nil, fail(409, "角色 %s 已停用,不能領取子項", g.Name)
		}
		ok, err := s.agentMayWork(org, c.issue.Project, g.ActorID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fail(409, "專案 %s 是受限專案,角色 %s 不是專案成員", c.issue.Project, g.Name)
		}
	}
	claimed, err := s.Store.ClaimIssueItem(org, cur.ID, me, s.itemLease())
	if err != nil {
		if cur2, e2 := s.Store.GetIssueItem(org, cur.ID); e2 == nil && cur2.ClaimedBy != "" && cur2.ClaimedBy != me {
			return nil, fail(409, "%s 已被%s領取、處理中,鎖定到 %s", key, s.presentWho(cur2.ClaimedBy), lockLabel(cur2.LockedUntil.Time, s.now()))
		}
		return nil, itemStoreErr(err, key)
	}
	res := &ItemClaimResult{Item: claimed}
	if g != nil {
		if err := s.bindItemTask(p, org, c.issue, claimed, g, me, res); err != nil {
			_ = s.Store.ReleaseIssueItem(org, claimed.ID, me)
			return nil, err
		}
	}
	s.Audit(p, "issue_item.claim", org, map[string]any{"id": claimed.ID, "key": key, "agent": g != nil, "note": strings.TrimSpace(note)})
	return res, nil
}

// ReleaseItem 放棄領取:doing 退回待領取(領取者本人或組織管理員;只有人)。綁定的任務取消、憑證作廢
func (s *Service) ReleaseItem(p *model.Principal, org, issueRef, itemRef string) (*model.IssueItem, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	role, err := s.orgRole(p, org)
	if err != nil {
		return nil, err
	}
	c, err := s.itemOf(p, org, issueRef, itemRef)
	if err != nil {
		return nil, err
	}
	me, err := s.callerActorID(p, org)
	if err != nil {
		return nil, err
	}
	key := itemKey(c.issue, *c.it)
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	cur, err := s.itemFresh(org, c.it, key)
	if err != nil {
		return nil, err
	}
	if cur.Status != model.ItemDoing {
		return nil, fail(409, "%s 目前是%s,不是進行中,不能放棄領取", key, itemStatusLabel(cur.Status))
	}
	if role != "admin" && cur.ClaimedBy != me {
		return nil, fail(403, "%s 是 %s 領取的,只有領取者本人或組織管理員可以放棄", key, s.presentWho(cur.ClaimedBy))
	}
	who := me
	if role == "admin" {
		who = ""
	}
	if err := s.Store.ReleaseIssueItem(org, cur.ID, who); err != nil {
		return nil, itemStoreErr(err, key)
	}
	s.cancelItemTask(org, cur)
	s.Audit(p, "issue_item.release", org, map[string]any{"id": cur.ID, "key": key})
	return s.presentOne(p, org, c.issue, cur.ID)
}

// normalizeEvidence 證據輸入:空 → nil;JSON 字串 → {"summary": …};物件照用。Note 併進 note
func normalizeEvidence(raw json.RawMessage, note string) (json.RawMessage, error) {
	s := strings.TrimSpace(string(raw))
	m := map[string]any{}
	switch {
	case s == "" || s == "null":
	case strings.HasPrefix(s, `"`):
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return nil, fail(400, "evidence 要是 JSON 物件或字串")
		}
		if t := strings.TrimSpace(str); t != "" {
			m["summary"] = t
		}
	default:
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fail(400, "evidence 要是 JSON 物件或字串")
		}
	}
	if n := strings.TrimSpace(note); n != "" {
		m["note"] = n
	}
	if len(m) == 0 {
		return nil, nil
	}
	b, _ := json.Marshal(m)
	e := json.RawMessage(b)
	if err := model.CleanItemEvidence(&e); err != nil {
		return nil, fail(400, "%v", err)
	}
	return e, nil
}

// ReportItem 回報子項(見 issue_items.go 檔頭)。status:doing(續約;沒領取時等於領取)、todo(放回待領取、或解除卡住)、blocked、done
func (s *Service) ReportItem(p *model.Principal, org, issueRef, itemRef string, in ItemReport) (*model.IssueItem, error) {
	ok := []string{model.ItemTodo, model.ItemDoing, model.ItemBlocked, model.ItemDone}
	if p.Agent != nil {
		ok = []string{model.ItemDoing, model.ItemBlocked, model.ItemDone}
	}
	in.Status = strings.TrimSpace(in.Status)
	c, err := s.itemOf(p, org, issueRef, itemRef) // 先授權(viewer、可見性),再看輸入
	if err != nil {
		return nil, err
	}
	if !model.OneOf(in.Status, ok) {
		return nil, fail(400, "status 必須是 %s 之一", strings.Join(ok, "、"))
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return nil, err
	}
	role, me := "", ""
	if p.Agent == nil {
		if role, err = s.orgRole(p, org); err != nil {
			return nil, err
		}
		me = by.ID
	}
	s.agents.mu.Lock()
	out, text, err := s.reportLocked(p, org, c, role, me, in)
	s.agents.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if text != "" {
		s.issueNote(org, c.issue.ID, by.ID, text)
	}
	return out, nil
}

// reportLocked ReportItem 的本體(呼叫端拿著 agents.mu);回傳輸出與要留在 Issue 的系統留言(空 = 不留)
func (s *Service) reportLocked(p *model.Principal, org string, c *itemCtx, role, me string, in ItemReport) (*model.IssueItem, string, error) {
	key := itemKey(c.issue, *c.it)
	cur, err := s.itemFresh(org, c.it, key)
	if err != nil {
		return nil, "", err
	}
	now := s.mt()
	var bound *model.AgentTask
	if cur.TaskID != "" {
		if t, err := s.Store.GetAgentTask(org, cur.TaskID); err == nil && t.Status == "inProgress" {
			bound = t
		}
	}
	if p.Agent != nil { // 角色憑證:只能回報自己綁定任務的那個子項
		if bound == nil || !strings.EqualFold(bound.ID, p.Agent.TaskID) {
			return nil, "", fail(403, "這組角色憑證只能回報自己綁定任務的子項")
		}
	} else if held := cur.ClaimedBy != "" && cur.Locked(now); held && cur.ClaimedBy != me && role != "admin" {
		return nil, "", fail(403, "%s 是 %s 領取的,只有領取者本人或組織管理員可以回報", key, s.presentWho(cur.ClaimedBy))
	}
	if model.ItemTerminal(cur.Status) {
		return nil, "", fail(409, "%s 已經%s", key, itemStatusLabel(cur.Status))
	}
	from := cur.Status
	text := ""
	switch in.Status {
	case model.ItemDoing:
		switch {
		case cur.Status != model.ItemDoing && p.Agent == nil:
			res, err := s.claimLocked(p, org, c, role, me, "")
			if err != nil {
				return nil, "", err
			}
			out, err := s.presentOne(p, org, c.issue, res.Item.ID)
			return out, "", err
		case cur.Status != model.ItemDoing:
			return nil, "", fail(409, "%s 目前是%s,不是進行中", key, itemStatusLabel(cur.Status))
		}
		if p.Agent == nil { // 進度回報:續約(總長不超過 MaxLeaseTotal,從建立租約起算以更新時間近似)
			until := s.itemLease()
			cur.LockedUntil = &model.Time{Time: until}
			if cur.ClaimedBy == "" {
				cur.ClaimedBy = me
			}
		}
	case model.ItemTodo:
		switch cur.Status {
		case model.ItemDoing:
			who := me
			if role == "admin" || cur.ClaimedBy != me {
				who = ""
			}
			if err := s.Store.ReleaseIssueItem(org, cur.ID, who); err != nil {
				return nil, "", itemStoreErr(err, key)
			}
			s.cancelItemTask(org, cur)
			s.Audit(p, "issue_item.release", org, map[string]any{"id": cur.ID, "key": key})
			out, err := s.presentOne(p, org, c.issue, cur.ID)
			return out, "", err
		case model.ItemBlocked:
			clearBlock(cur)
			cur.Status = model.ItemTodo
			s.Audit(p, "issue_item.unblock", org, map[string]any{"id": cur.ID, "key": key})
			text = fmt.Sprintf("子項 %s「%s」解除卡住", key, cur.Title)
		default:
			out, err := s.presentOne(p, org, c.issue, cur.ID)
			return out, "", err
		}
	case model.ItemBlocked:
		reason := strings.TrimSpace(in.BlockReason)
		if reason == "" {
			return nil, "", fail(400, "blocked 要附原因(blockReason);在等人決定請填 waitForKind=user_decision 與 waitFor")
		}
		cur.Status, cur.BlockReason, cur.WaitForKind, cur.WaitFor = model.ItemBlocked, reason, strings.TrimSpace(in.WaitForKind), strings.TrimSpace(in.WaitFor)
		if !model.ValidWaitKind(cur.WaitForKind) {
			return nil, "", fail(400, "waitForKind 必須是 %s 之一", strings.Join(model.ItemWaitKinds, "、"))
		}
		if cur.WaitForKind == model.WaitItem {
			id, err := itemDepID(c.list, cur.WaitFor)
			if err != nil {
				return nil, "", err
			}
			if id == cur.ID {
				return nil, "", fail(400, "不能等待自己")
			}
			cur.WaitFor = id
		}
		cur.ClaimedBy, cur.LockedUntil = "", nil
		wait := ""
		if cur.WaitForKind != "" {
			wait = fmt.Sprintf(",等待%s:%s", waitKindLabel(cur.WaitForKind), strings.TrimSpace(in.WaitFor))
		}
		text = fmt.Sprintf("子項 %s「%s」卡住:%s%s", key, cur.Title, reason, wait)
	case model.ItemDone:
		if err := s.itemPrereq(c, cur); err != nil {
			return nil, "", err
		}
		ev, err := normalizeEvidence(in.Evidence, in.Note)
		if err != nil {
			return nil, "", err
		}
		if ev != nil {
			cur.Evidence = ev
		}
		cur.Status, cur.CompletedAt = model.ItemDone, &now
		cur.ClaimedBy, cur.LockedUntil = "", nil
		clearBlock(cur)
		text = fmt.Sprintf("子項 %s「%s」完成", key, cur.Title)
		if n := strings.TrimSpace(in.Note); n != "" {
			text += "。" + n
		}
	}
	if err := cur.Clean(); err != nil {
		return nil, "", fail(400, "%v", err)
	}
	cur.UpdatedAt = now
	if err := s.Store.UpdateIssueItem(org, cur, from); err != nil {
		return nil, "", itemStoreErr(err, key)
	}
	// 綁定的任務跟著結束(子項先改好,saveTaskChange 的回寫看到子項不是進行中就不再動)
	if bound != nil && (in.Status == model.ItemDone || in.Status == model.ItemBlocked) {
		s.finishBoundTask(p, org, bound, map[string]string{model.ItemDone: "done", model.ItemBlocked: "blocked"}[in.Status], in, cur)
	}
	action := "issue_item.report"
	if in.Status == model.ItemBlocked {
		action = "issue_item.block"
	}
	s.Audit(p, action, org, map[string]any{"id": cur.ID, "key": key, "status": cur.Status})
	out, err := s.presentOne(p, org, c.issue, cur.ID)
	return out, text, err
}

func clearBlock(it *model.IssueItem) { it.BlockReason, it.WaitForKind, it.WaitFor = "", "", "" }

func waitKindLabel(k string) string {
	switch k {
	case model.WaitUserDecision:
		return "使用者決定"
	case model.WaitItem:
		return "子項"
	case model.WaitExternal:
		return "外部條件"
	}
	return k
}

// BlockItem 標記卡住(同 ReportItem status=blocked;要附 BlockReason)
func (s *Service) BlockItem(p *model.Principal, org, issueRef, itemRef string, in ItemReport) (*model.IssueItem, error) {
	in.Status = model.ItemBlocked
	return s.ReportItem(p, org, issueRef, itemRef, in)
}

// UnblockItem 解除卡住:blocked → todo,清原因與等待對象(只有人,組織成員皆可——通常是做決定的人)
func (s *Service) UnblockItem(p *model.Principal, org, issueRef, itemRef string) (*model.IssueItem, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	c, err := s.itemOf(p, org, issueRef, itemRef)
	if err != nil {
		return nil, err
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return nil, err
	}
	key := itemKey(c.issue, *c.it)
	s.agents.mu.Lock()
	cur, err := s.itemFresh(org, c.it, key)
	if err == nil && cur.Status != model.ItemBlocked {
		err = fail(409, "%s 目前是%s,不是卡住", key, itemStatusLabel(cur.Status))
	}
	if err == nil {
		clearBlock(cur)
		cur.Status, cur.UpdatedAt = model.ItemTodo, s.mt()
		if e := s.Store.UpdateIssueItem(org, cur, model.ItemBlocked); e != nil {
			err = itemStoreErr(e, key)
		}
	}
	s.agents.mu.Unlock()
	if err != nil {
		return nil, err
	}
	s.Audit(p, "issue_item.unblock", org, map[string]any{"id": cur.ID, "key": key})
	s.issueNote(org, c.issue.ID, by.ID, fmt.Sprintf("子項 %s「%s」解除卡住", key, cur.Title))
	return s.presentOne(p, org, c.issue, cur.ID)
}

// CancelItem 取消(放棄這項工作,不用刪除表示;只有人)。綁定的任務一併取消。已經取消回目前的樣子,已完成 409
func (s *Service) CancelItem(p *model.Principal, org, issueRef, itemRef string) (*model.IssueItem, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	c, err := s.itemOf(p, org, issueRef, itemRef)
	if err != nil {
		return nil, err
	}
	key := itemKey(c.issue, *c.it)
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	cur, err := s.itemFresh(org, c.it, key)
	if err != nil {
		return nil, err
	}
	switch cur.Status {
	case model.ItemCanceled:
		return s.presentOne(p, org, c.issue, cur.ID)
	case model.ItemDone:
		return nil, fail(409, "%s 已經完成,不能取消", key)
	}
	from := cur.Status
	now := s.mt()
	cur.Status, cur.ClaimedBy, cur.LockedUntil, cur.UpdatedAt, cur.CompletedAt = model.ItemCanceled, "", nil, now, &now
	if err := s.Store.UpdateIssueItem(org, cur, from); err != nil {
		return nil, itemStoreErr(err, key)
	}
	s.cancelItemTask(org, cur)
	s.Audit(p, "issue_item.cancel", org, map[string]any{"id": cur.ID, "key": key})
	return s.presentOne(p, org, c.issue, cur.ID)
}

// PromoteItem 提升為獨立 Issue(只有人):建立新 Issue(標題 = 子項標題、內文帶說明、驗收條件、檔案與來源),
// 原子項標 canceled 並記 promotedIssueId,兩張 Issue 各留一則系統留言記關聯。已經提升過或已完成 409
func (s *Service) PromoteItem(p *model.Principal, org, issueRef, itemRef string, in ItemPromoteInput) (*ItemPromoteResult, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	c, err := s.itemOf(p, org, issueRef, itemRef)
	if err != nil {
		return nil, err
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return nil, err
	}
	key := itemKey(c.issue, *c.it)
	if c.it.PromotedIssueID != "" {
		return nil, fail(409, "%s 已經提升為 Issue", key)
	}
	if c.it.Status == model.ItemDone || c.it.Status == model.ItemCanceled {
		return nil, fail(409, "%s 已經%s,不能提升", key, itemStatusLabel(c.it.Status))
	}
	var body strings.Builder
	fmt.Fprintf(&body, "由 %s 子項「%s」提升而來(原 Issue %s)。", key, c.it.Title, c.issue.Display())
	if a := strings.TrimSpace(c.it.Acceptance); a != "" {
		fmt.Fprintf(&body, "\n\n## 驗收條件\n\n%s", a)
	}
	if len(c.it.Files) > 0 {
		body.WriteString("\n\n## 會動的檔案\n")
		for _, f := range c.it.Files {
			fmt.Fprintf(&body, "\n- %s", f)
		}
	}
	prio := in.Priority
	if prio == "" {
		prio = c.issue.Priority
	}
	project := in.Project
	if project == nil {
		project = &c.issue.Project
	}
	issue, err := s.CreateIssue(p, org, IssueInput{Title: c.it.Title, Body: body.String(), Priority: prio, Labels: in.Labels, Source: key, Project: project})
	if err != nil {
		return nil, err
	}
	s.agents.mu.Lock()
	cur, err := s.itemFresh(org, c.it, key)
	if err == nil && (cur.PromotedIssueID != "" || model.ItemTerminal(cur.Status)) {
		err = fail(409, "%s 剛剛被別人改了;剛建立的 %s 請自行處理", key, issue.Key)
	}
	if err == nil {
		from, now := cur.Status, s.mt()
		cur.Status, cur.PromotedIssueID, cur.ClaimedBy, cur.LockedUntil, cur.UpdatedAt, cur.CompletedAt = model.ItemCanceled, issue.ID, "", nil, now, &now
		if e := s.Store.UpdateIssueItem(org, cur, from); e != nil {
			err = itemStoreErr(e, key)
			if errors.Is(e, store.ErrStale) {
				err = fail(409, "%s 剛剛被別人改了;剛建立的 %s 請自行處理", key, issue.Key)
			}
		} else {
			s.cancelItemTask(org, cur)
		}
	}
	s.agents.mu.Unlock()
	if err != nil {
		return nil, err
	}
	s.Audit(p, "issue_item.promote", org, map[string]any{"id": cur.ID, "key": key, "issue": issue.Key})
	s.issueNote(org, issue.ID, by.ID, fmt.Sprintf("由 %s 子項「%s」提升而來(原 Issue %s)", key, cur.Title, c.issue.Display()))
	s.issueNote(org, c.issue.ID, by.ID, fmt.Sprintf("子項 %s「%s」已提升為 %s", key, cur.Title, issue.Display()))
	out, err := s.presentOne(p, org, c.issue, cur.ID)
	if err != nil {
		return nil, err
	}
	return &ItemPromoteResult{Issue: issue, Item: out}, nil
}
