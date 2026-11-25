package service

// issues_actor.go Issue、留言、文件的「誰」欄位(ISS-36 C-2a;契約見 omixor 文件櫃 DOC-34《core/store/ACTOR_CONTRACT.md》)
//
// store 與服務層內部:Issue.CreatedBy / Assignee / DeletedBy、Comment.Author、CabinetDoc.Author 都是 actor id。
// 匯出方法的回傳值在出口用 present 轉成相容字串(人 = email、角色 = agent:<handle>、系統 = 舊名稱;assignee 是角色 handle),
// 新的 actor 物件欄位屬 C-3。輸入端:署名走 ResolveAuthor、assignee 走 resolveIssueAssignee(角色 handle → actor id)、
// 匯入走寬鬆解析器 Store.ResolveLegacy。

import (
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// issueActorIDs 一批 Issue 用到的 actor id(建立者、指派、刪除者、留言作者)
func issueActorIDs(list []model.Issue) []string {
	var ids []string
	for _, i := range list {
		ids = append(ids, i.CreatedBy, i.Assignee, i.DeletedBy)
		for _, c := range i.Comments {
			ids = append(ids, c.Author)
		}
	}
	return ids
}

// presentIssues actor id → 相容字串(整批一次查詢);回傳新的切片與新的留言切片,不動傳入的
func (s *Service) presentIssues(list []model.Issue) ([]model.Issue, error) {
	n, err := s.LoadActors(issueActorIDs(list)...)
	if err != nil {
		return nil, err
	}
	out := make([]model.Issue, len(list))
	for k, i := range list {
		out[k] = presentIssueWith(n, i)
	}
	return out, nil
}

func presentIssueWith(n *Names, i model.Issue) model.Issue {
	createdBy, assignee, deletedBy := i.CreatedBy, i.Assignee, i.DeletedBy // actor id(下面先換成相容字串,物件要用原本的 id 查)
	i.CreatedBy = n.Legacy(i.CreatedBy, model.LegacyFree)
	i.Assignee = n.Legacy(i.Assignee, model.LegacyAgentID)
	i.DeletedBy = n.Legacy(i.DeletedBy, model.LegacyFree)
	i.CreatedByActor, i.AssigneeActor, i.DeletedByActor = n.View(createdBy), n.View(assignee), n.View(deletedBy)
	cs := make([]model.Comment, len(i.Comments))
	for k, c := range i.Comments {
		c.AuthorActor = n.View(c.Author)
		c.Author = n.Legacy(c.Author, model.LegacyFree)
		cs[k] = c
	}
	i.Comments = cs
	return i
}

// presentIssue 單筆(一次查詢);nil 回 nil
func (s *Service) presentIssue(i *model.Issue) (*model.Issue, error) {
	if i == nil {
		return nil, nil
	}
	l, err := s.presentIssues([]model.Issue{*i})
	if err != nil {
		return nil, err
	}
	return &l[0], nil
}

// resolveIssueAssignee Issue 的 assignee 輸入(角色 handle、@handle、agent:<handle>、actor id;舊行為也接受角色名稱)→ 角色的 actor id。
// 空白 = 不指派(回 "");角色不存在 400
func (s *Service) resolveIssueAssignee(p *model.Principal, org, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	in := raw
	low := strings.ToLower(raw)
	if !strings.HasPrefix(low, "agent:") && !model.ValidActorID(low) {
		in = "agent:" + strings.TrimSpace(strings.TrimPrefix(raw, "@")) // 只找角色,不和同名的使用者撞
	}
	ref, err := s.ResolveWho(p, org, in, WantAgent)
	if err == nil {
		return ref.ID, nil
	}
	if errors.Is(err, ErrActorNotFound) { // 舊行為:也可以用角色名稱指定
		g, ferr := s.findAgent(org, raw)
		if errors.Is(ferr, store.ErrNotFound) {
			return "", fail(400, "assignee 指定的角色不存在:%s", raw)
		}
		if ferr != nil {
			return "", ferr
		}
		ref, aerr := s.agentActorRef(g)
		if aerr != nil {
			return "", aerr
		}
		if ref.ID == "" {
			return "", ErrActorUnavailable
		}
		return ref.ID, nil
	}
	if errors.Is(err, ErrActorKind) {
		return "", fail(400, "assignee 只能指派給角色:%s", raw)
	}
	return "", err
}

// importIssueActors 匯入的一筆:建立者、指派、刪除者、留言作者的舊字串都轉成 actor id(解析規則見 actor_import.go:
// 伺服器管理員寬鬆、組織管理員只能署名本組織成員、本組織角色、系統與自己,其餘落到 sys_legacy_*)。
// 建立者與留言作者沒給的當舊的 mcp 署名(sys_legacy_mcp);assignee 是純角色 id(對不上的角色退成 sys_legacy_*,讀回仍是原值)
func (s *Service) importIssueActors(w *importWho, i *model.Issue) error {
	var err error
	if i.CreatedBy, err = w.id(i.CreatedBy, model.MCPActorID); err != nil {
		return err
	}
	if i.Assignee, err = w.agentID(i.Assignee); err != nil {
		return err
	}
	if i.DeletedBy, err = w.id(i.DeletedBy, ""); err != nil {
		return err
	}
	cs := make([]model.Comment, len(i.Comments))
	for k, c := range i.Comments {
		if c.Author, err = w.id(c.Author, model.MCPActorID); err != nil {
			return err
		}
		cs[k] = c
	}
	i.Comments = cs
	return nil
}

// syncIssueAssignee 呼叫 syncAssignee(角色任務,C-2b 的檔):它還用角色 id(handle)比對,所以把 actor id 換成角色 handle 再交給它。
// issue 的 Assignee、old 是 actor id;by 是署名的 actor id
func (s *Service) syncIssueAssignee(p *model.Principal, org string, issue model.Issue, old, by string) {
	n, err := s.LoadActors(issue.Assignee, old)
	if err != nil {
		return
	}
	issue.Assignee = n.Legacy(issue.Assignee, model.LegacyAgentID)
	s.syncAssignee(p, org, issue, n.Legacy(old, model.LegacyAgentID), by)
}

// presentDocs 文件作者 actor id → 相容字串(整批一次查詢);回傳新的切片,不動傳入的
func (s *Service) presentDocs(list []model.CabinetDoc) ([]model.CabinetDoc, error) {
	ids := make([]string, 0, len(list))
	for _, d := range list {
		ids = append(ids, d.Author)
	}
	n, err := s.LoadActors(ids...)
	if err != nil {
		return nil, err
	}
	out := make([]model.CabinetDoc, len(list))
	for k, d := range list {
		d.AuthorActor = n.View(d.Author)
		d.Author = n.Legacy(d.Author, model.LegacyFree)
		out[k] = d
	}
	return out, nil
}

// presentDoc 單筆
func (s *Service) presentDoc(d *model.CabinetDoc) (*model.CabinetDoc, error) {
	l, err := s.presentDocs([]model.CabinetDoc{*d})
	if err != nil {
		return nil, err
	}
	return &l[0], nil
}
