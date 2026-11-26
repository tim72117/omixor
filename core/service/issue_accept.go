package service

// issue_accept.go Issue 的「接受」與「放回」(Issue 層級的負責人;和子項的領取是兩層、不互斥)。
//
// 流程:Issue 先被接受(接受者成為負責人 = 沿用 Issue.Assignee,Issue 變進行中,通常還沒有子項,代表正在分析),
// 之後才拆子項,子項再各自被領取執行。這一版是最小版:沒有租約與到期、沒有分析中/實作中的細分、子項領取不連動 Issue。
//
// 規則:
//   - 接受:人與角色憑證都可以(權限同讀取、留言 Issue;受限專案看不到的 Issue 404),接受者一律是呼叫者本人。
//     角色憑證另外只能接受本來就是自己的(指派給自己、自己建立、目前任務連到;checkAgentAccept),否則 403。
//     Issue 已關閉(closed)409;已被別人接受(assignee 有值且不是自己)409(要先放回,或用 issue_update 的 assignee 轉交給角色);
//     已是自己 = 冪等(不改、不留言)。成功後 assignee = 自己,並在 Issue 留一則系統留言、寫稽核 issue.accept。
//     角色接受自己時不排任務(它已經在做了);沒有組織鏈的檢查(只能指派給自己)。
//   - 放回:只有目前的接受者本人(人或角色憑證)或組織管理員;別人 403。沒有人接受 = 冪等(不改)。
//     放回後 assignee 清空(退回待處理);原本是角色且還沒領取的任務一併取消(同取消指派)。留系統留言、寫稽核 issue.release。
//
// 「進行中」:看板與篩選以 model.IssueAssigned 判斷(assignee 字串或 assigneeActor 任一有值),人類接受者(assignee 字串輸出是空的,
// 只有 assigneeActor)也算。

import (
	"errors"

	"omixor-core/model"
	"omixor-core/store"
)

// acceptTries 版本衝突(多個實例同時改同一張 Issue)時重讀重試的次數
const acceptTries = 3

// AcceptIssue 接受 Issue:呼叫者成為負責人。回傳接受後的 Issue
func (s *Service) AcceptIssue(p *model.Principal, org, ref string) (*model.Issue, error) {
	return s.acceptOrRelease(p, org, ref, false)
}

// ReleaseIssue 放回 Issue:取消負責人、退回待處理(目前的接受者或組織管理員)。回傳放回後的 Issue
func (s *Service) ReleaseIssue(p *model.Principal, org, ref string) (*model.Issue, error) {
	return s.acceptOrRelease(p, org, ref, true)
}

func (s *Service) acceptOrRelease(p *model.Principal, org, ref string, release bool) (*model.Issue, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	me, admin, err := s.acceptCaller(p, org)
	if err != nil {
		return nil, err
	}
	id, err := s.resolveIssueID(org, ref)
	if err != nil {
		return nil, err
	}
	acc, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	var (
		out     *model.Issue
		changed bool
		old     string
	)
	for try := 0; try < acceptTries; try++ {
		mu := s.issueLock(org, id)
		mu.Lock()
		out, old, changed, err = s.setAcceptor(p, acc, org, id, ref, me, admin, release)
		mu.Unlock()
		var vc *store.VersionConflictError
		if err == nil || !errors.As(err, &vc) {
			break
		}
	}
	if err != nil {
		var vc *store.VersionConflictError
		if errors.As(err, &vc) { // 重試用完仍衝突:和修改一樣回 409 並帶出目前的內容(current、currentVersion)
			return nil, s.issueWriteErr(org, id, err)
		}
		return nil, err
	}
	if changed { // 留言與任務同步在 Issue 的鎖外(留言自己拿鎖、同步會拿 agents.mu)
		if release {
			s.issueNote(org, id, me, "已放回,等待重新接受")
			s.Audit(p, "issue.release", org, map[string]any{"id": id, "key": out.Key})
			s.syncIssueAssignee(p, org, *out, old, me) // out.Assignee 已清空:取消舊角色還沒領取的任務
		} else {
			s.issueNote(org, id, me, "已接受,由我負責分析與拆解")
			s.Audit(p, "issue.accept", org, map[string]any{"id": id, "key": out.Key})
		}
	}
	fresh, err := s.Store.GetIssue(org, id)
	if err != nil {
		return nil, err
	}
	pi, err := s.presentIssue(fresh)
	if err != nil {
		return nil, err
	}
	if l := s.withItemCounts(org, []model.Issue{*pi}); len(l) == 1 {
		*pi = l[0]
	}
	return pi, nil
}

// acceptCaller 呼叫者本人的 actor id,以及是不是組織管理員(角色憑證不是)
func (s *Service) acceptCaller(p *model.Principal, org string) (me string, admin bool, err error) {
	admin, err = s.callerIsOrgAdmin(p, org)
	if err != nil {
		return "", false, err
	}
	ref, err := s.SelfActor(p)
	if err != nil {
		return "", false, err
	}
	return ref.ID, admin, nil
}

// callerIsOrgAdmin 呼叫者是不是組織管理員(角色憑證一律不是,也不查組織)
func (s *Service) callerIsOrgAdmin(p *model.Principal, org string) (bool, error) {
	if p.Agent != nil {
		return false, nil
	}
	role, err := s.orgRole(p, org)
	if err != nil {
		return false, err
	}
	return role == "admin", nil
}

// checkRelease 放回的權限:只有目前的接受者本人或組織管理員(別人 403)。放回與 issue_update 的 assignee="" 共用
func (s *Service) checkRelease(cur *model.Issue, me string, admin bool) error {
	if cur.Assignee != me && !admin {
		return fail(403, "%s 由%s接受,只有接受者本人或組織管理員可以放回(用 issue_accept 的 release=true)", cur.Display(), s.presentWho(cur.Assignee))
	}
	return nil
}

// checkClearAssignee issue_update 的 assignee="" 要取消目前負責人時的權限(和放回同一條規則):負責人本人直接放行,
// 其他人才查是不是組織管理員(角色憑證不是)。只在真的有負責人、而且呼叫者不是負責人時才多查一次
func (s *Service) checkClearAssignee(p *model.Principal, org string, cur *model.Issue, me string) error {
	if cur.Assignee == me {
		return nil
	}
	admin, err := s.callerIsOrgAdmin(p, org)
	if err != nil {
		return err
	}
	return s.checkRelease(cur, me, admin)
}

// humanAcceptor Issue 目前的負責人是使用者(人類接受者)而不是角色
func humanAcceptor(i *model.Issue) bool {
	k, ok := model.ActorKindOfID(i.Assignee)
	return ok && k == model.ActorKindUser
}

// setAcceptor 讀了再改(拿著 issueLock 呼叫)。回傳改好的 Issue、改之前的 assignee(actor id)、有沒有真的改
func (s *Service) setAcceptor(p *model.Principal, acc *projectAccess, org, id, ref, me string, admin, release bool) (*model.Issue, string, bool, error) {
	cur, err := s.Store.GetIssue(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", false, issueNotFound(ref)
	}
	if err != nil {
		return nil, "", false, err
	}
	if err := acc.issueErr(cur, ref); err != nil { // 看不到的專案:和不存在一樣
		return nil, "", false, err
	}
	key := cur.Display()
	if release {
		if cur.Assignee == "" { // 沒有人接受:冪等
			return cur, "", false, nil
		}
		if err := s.checkRelease(cur, me, admin); err != nil {
			return nil, "", false, err
		}
	} else {
		if cur.Status == "closed" {
			return nil, "", false, fail(409, "%s 已經關閉,不能接受", key)
		}
		if cur.Assignee == me { // 已經是自己:冪等
			return cur, "", false, nil
		}
		if cur.Assignee != "" {
			return nil, "", false, fail(409, "%s 已被%s接受;要先請對方放回,或用 issue_update 的 assignee 轉交", key, s.presentWho(cur.Assignee))
		}
		if err := s.checkAgentAccept(p, org, cur); err != nil {
			return nil, "", false, err
		}
		if p.Agent != nil { // 受限專案:角色要是專案成員(通常看得到就是成員,這裡和指派走同一個檢查)
			if err := s.checkAssigneeProject(org, cur.Project, me); err != nil {
				return nil, "", false, err
			}
		}
	}
	n := *cur
	n.Labels = append([]string{}, cur.Labels...)
	n.Comments = nil // 不動留言
	if release {
		n.Assignee = ""
	} else {
		n.Assignee = me
	}
	n.UpdatedAt = s.mt()
	n.Version = cur.Version // 條件式更新:別的實例同時改過回版本衝突,由呼叫端重讀重試
	if err := s.Store.UpdateIssue(org, n); err != nil {
		var vc *store.VersionConflictError
		if errors.As(err, &vc) {
			return nil, "", false, err
		}
		return nil, "", false, s.issueWriteErr(org, id, err)
	}
	n.Version = cur.Version + 1
	return &n, cur.Assignee, true, nil
}
