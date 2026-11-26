package service

import (
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 子項的新增、列、讀、改、刪(規則見 issue_items.go 檔頭)。
// 寫入一律在 s.agents.mu 內(子項不需要 issueLock:它們是獨立的列,store 的更新是條件式;領取要建任務、完成要回寫任務,
// 需要和任務的操作互斥,所以直接用任務那把鎖;saveTaskChange 的回寫也在這把鎖內)

// itemAssignee 輸入的負責人 → actor id 與種類。空 = 沒指派。
// 角色憑證只能指派自己;受限專案的角色負責人要是專案成員(400,同 checkAssigneeProject)
func (s *Service) itemAssignee(p *model.Principal, org string, issue *model.Issue, raw string) (string, error) {
	if isBlank(raw) {
		return "", nil
	}
	ref, err := s.ResolveWho(p, org, raw, WantPeople)
	if err != nil {
		return "", err
	}
	if p.Agent != nil {
		self, err := s.SelfActor(p)
		if err != nil {
			return "", err
		}
		if ref.ID != self.ID {
			return "", fail(403, "角色身分建立子項時,負責人只能留空或是自己")
		}
	}
	if ref.Kind == model.ActorKindAgent {
		if err := s.checkAssigneeProject(org, issue.Project, ref.ID); err != nil {
			return "", err
		}
	}
	return ref.ID, nil
}

// AddItem 新增子項(組織成員、角色憑證都可以;署名是呼叫者本人)
func (s *Service) AddItem(p *model.Principal, org, issueRef string, in ItemInput) (*model.IssueItem, error) {
	c, err := s.itemIssue(p, org, issueRef)
	if err != nil {
		return nil, err
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return nil, err
	}
	assignee, err := s.itemAssignee(p, org, c.issue, in.Assignee)
	if err != nil {
		return nil, err
	}
	dep := ""
	if !isBlank(in.DependsOn) {
		if dep, err = itemDepID(c.list, in.DependsOn); err != nil {
			return nil, err
		}
	}
	now := s.mt()
	it := &model.IssueItem{IssueID: c.issue.ID, Title: in.Title, Status: model.ItemTodo, Assignee: assignee, DependsOn: dep,
		Files: in.Files, Acceptance: strings.TrimSpace(in.Acceptance), CreatedBy: by.ID, CreatedAt: now, UpdatedAt: now}
	if err := it.Clean(); err != nil {
		return nil, fail(400, "%v", err)
	}
	s.agents.mu.Lock()
	saved, err := s.Store.CreateIssueItem(org, it)
	s.agents.mu.Unlock()
	if err != nil {
		return nil, itemStoreErr(err, in.Title)
	}
	s.Audit(p, "issue_item.add", org, map[string]any{"id": saved.ID, "issue": c.issue.ID, "key": itemKey(c.issue, *saved), "assignee": assignee})
	return s.presentOne(p, org, c.issue, saved.ID)
}

// presentOne 重新讀這張 Issue 的子項(拿到最新的),輸出指定的那筆
func (s *Service) presentOne(p *model.Principal, org string, issue *model.Issue, id string) (*model.IssueItem, error) {
	all, err := s.Store.ListIssueItems(org, issue.ID)
	if err != nil {
		return nil, err
	}
	it := findItemIn(all, id)
	if it == nil {
		return nil, itemNotFound(id)
	}
	out, err := s.presentItems(p, org, issue, all, []model.IssueItem{*it})
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// ListItems 列一張 Issue 的子項(seq 順序;預設只回未完成,見 ItemListQuery)
func (s *Service) ListItems(p *model.Principal, org, issueRef string, q ItemListQuery) ([]model.IssueItem, error) {
	c, err := s.itemIssue(p, org, issueRef)
	if err != nil {
		return nil, err
	}
	if q.Status != "" && !model.ValidItemStatus(q.Status) {
		return nil, fail(400, "status 必須是 %s 之一", strings.Join(model.ItemStatuses, "、"))
	}
	pick := make([]model.IssueItem, 0, len(c.list))
	for _, it := range c.list {
		switch {
		case q.Status != "":
			if it.Status != q.Status {
				continue
			}
		case !q.IncludeDone && model.ItemTerminal(it.Status):
			continue
		}
		pick = append(pick, it)
	}
	return s.presentItems(p, org, c.issue, c.list, pick)
}

// GetItem 看一筆(itemRef:key、序號或 id)
func (s *Service) GetItem(p *model.Principal, org, issueRef, itemRef string) (*model.IssueItem, error) {
	c, err := s.itemOf(p, org, issueRef, itemRef)
	if err != nil {
		return nil, err
	}
	out, err := s.presentItems(p, org, c.issue, c.list, []model.IssueItem{*c.it})
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// itemFresh 拿著 agents.mu 之後重讀子項(別的請求可能剛改過)
func (s *Service) itemFresh(org string, it *model.IssueItem, key string) (*model.IssueItem, error) {
	cur, err := s.Store.GetIssueItem(org, it.ID)
	if err != nil {
		return nil, itemStoreErr(err, key)
	}
	return cur, nil
}

// UpdateItem 改標題、files、驗收條件、負責人、前置(只有人;完成與取消的子項不能改 409;被別人持有有效租約時不能換負責人 409)
func (s *Service) UpdateItem(p *model.Principal, org, issueRef, itemRef string, in ItemPatch) (*model.IssueItem, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	c, err := s.itemOf(p, org, issueRef, itemRef)
	if err != nil {
		return nil, err
	}
	key := itemKey(c.issue, *c.it)
	var assignee string
	if in.Assignee != nil {
		if assignee, err = s.itemAssignee(p, org, c.issue, *in.Assignee); err != nil {
			return nil, err
		}
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	cur, err := s.itemFresh(org, c.it, key)
	if err != nil {
		return nil, err
	}
	if model.ItemTerminal(cur.Status) {
		return nil, fail(409, "%s 已經%s,不能再改", key, itemStatusLabel(cur.Status))
	}
	from := cur.Status
	if in.Title != nil {
		cur.Title = *in.Title
	}
	if in.Files != nil {
		cur.Files = *in.Files
	}
	if in.Acceptance != nil {
		cur.Acceptance = strings.TrimSpace(*in.Acceptance)
	}
	if in.Assignee != nil && assignee != cur.Assignee {
		if cur.Locked(s.mt()) && cur.ClaimedBy != "" {
			return nil, fail(409, "%s 正被領取、處理中,要換負責人請先放棄領取", key)
		}
		cur.Assignee = assignee
	}
	if in.DependsOn != nil {
		cur.DependsOn = ""
		if !isBlank(*in.DependsOn) {
			if cur.DependsOn, err = itemDepID(c.list, *in.DependsOn); err != nil {
				return nil, err
			}
		}
	}
	if err := cur.Clean(); err != nil {
		return nil, fail(400, "%v", err)
	}
	cur.UpdatedAt = s.mt()
	if err := s.Store.UpdateIssueItem(org, cur, from); err != nil {
		return nil, itemStoreErr(err, key)
	}
	s.Audit(p, "issue_item.update", org, map[string]any{"id": cur.ID, "key": key})
	return s.presentOne(p, org, c.issue, cur.ID)
}

// DeleteItem 刪除子項(建立者或組織管理員;只有人)。綁定的任務還沒結束就取消。序號不重用,別的子項對它的依賴會被清掉
func (s *Service) DeleteItem(p *model.Principal, org, issueRef, itemRef string) error {
	if err := notAgent(p); err != nil {
		return err
	}
	role, err := s.orgRole(p, org)
	if err != nil {
		return err
	}
	c, err := s.itemOf(p, org, issueRef, itemRef)
	if err != nil {
		return err
	}
	me, err := s.callerActorID(p, org)
	if err != nil {
		return err
	}
	key := itemKey(c.issue, *c.it)
	if role != "admin" && c.it.CreatedBy != me {
		return fail(403, "只有子項的建立者或組織管理員可以刪除")
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	cur, err := s.itemFresh(org, c.it, key)
	if err != nil {
		return err
	}
	s.cancelItemTask(org, cur)
	if err := s.Store.DeleteIssueItem(org, cur.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil // 剛好被別人刪了:結果相同
		}
		return err
	}
	s.Audit(p, "issue_item.delete", org, map[string]any{"id": cur.ID, "key": key})
	return nil
}

func itemStatusLabel(s string) string {
	switch s {
	case model.ItemTodo:
		return "待領取"
	case model.ItemDoing:
		return "進行中"
	case model.ItemBlocked:
		return "卡住"
	case model.ItemDone:
		return "完成"
	case model.ItemCanceled:
		return "取消"
	}
	return s
}
