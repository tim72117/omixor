package service

import (
	"errors"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 專案成員(第 3 階段;模型見 model.ProjectMember,可見性規則見 project_access.go)。
//
// 權限:
//   - 列出成員:看得到這個專案的組織成員(受限專案要是專案成員或組織管理員;看不到的專案 404)。角色憑證不行(403)
//   - 加入、移除、改角色:組織管理員或專案管理員(成員角色是 admin)。看不到的專案 404;看得到但不是管理員 403;角色憑證 403
//   - 成員可以是組織成員(使用者)或組織裡的角色;不是組織成員的使用者不能加(400,和不存在同一句話)
//   - 使用者被移出組織、角色被刪時,專案成員資格一併清掉(RemoveMember、DeleteAgent)
//
// 受限旗標(Restricted)用 UpdateProject 改(只有組織管理員),專案管理員不能自己把受限專案改成公開。

// ProjectMemberInput 加入成員或改角色
type ProjectMemberInput struct {
	// Actor 成員:使用者(email、user:<email>、actor id)或角色(agent:<角色 id>、角色 id);解析規則同 ResolveActor
	Actor string `json:"actor"`
	// Role admin 或 member(空 = member;已經是成員時空 = 角色不變)
	Role string `json:"role,omitempty"`
}

// presentProjectMembers 成員 actor id → actor 物件與相容節點寫法
func (s *Service) presentProjectMembers(list []model.ProjectMember) ([]model.ProjectMember, error) {
	ids := make([]string, 0, len(list))
	for _, m := range list {
		ids = append(ids, m.ActorID)
	}
	n, err := s.LoadActors(ids...)
	if err != nil {
		return nil, err
	}
	out := make([]model.ProjectMember, len(list))
	for i, m := range list {
		m.Actor = n.View(m.ActorID)
		m.Node = n.Legacy(m.ActorID, model.LegacyNode)
		out[i] = m
	}
	return out, nil
}

// ListProjectMembers 專案的成員(依加入順序)。看得到專案的組織成員都可以看(受限專案要是專案成員或組織管理員);角色憑證 403
func (s *Service) ListProjectMembers(p *model.Principal, org, project string) ([]model.ProjectMember, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	x, _, err := s.projectViewer(p, org, project)
	if err != nil {
		return nil, err
	}
	l, err := s.Store.ListProjectMembers(org, x.ID)
	if err != nil {
		return nil, err
	}
	return s.presentProjectMembers(l)
}

// memberActor 輸入的成員 → actor id:使用者要是組織成員、角色要在組織裡。解析不到(含不是組織成員的使用者)400,同一句話
func (s *Service) memberActor(p *model.Principal, org, raw string) (model.ActorRef, error) {
	ref, err := s.ResolveActor(p, org, raw)
	if err != nil {
		return model.ActorRef{}, err
	}
	switch ref.Kind {
	case model.ActorKindAgent:
		return ref, nil
	case model.ActorKindUser:
		n, err := s.LoadActors(ref.ID)
		if err != nil {
			return model.ActorRef{}, err
		}
		uid := n.Legacy(ref.ID, model.LegacyUserID)
		if uid != "" {
			if _, err := s.Store.MemberRole(org, uid); err == nil {
				return ref, nil
			} else if !errors.Is(err, store.ErrNotFound) {
				return model.ActorRef{}, err
			}
		}
	}
	return model.ActorRef{}, ErrActorNotFound
}

// SetProjectMember 加入成員或改角色(組織管理員或專案管理員)。已經是成員時是改角色(Role 空 = 不變);回傳這位成員
func (s *Service) SetProjectMember(p *model.Principal, org, project string, in ProjectMemberInput) (*model.ProjectMember, error) {
	x, err := s.needProjectAdmin(p, org, project)
	if err != nil {
		return nil, err
	}
	role := strings.ToLower(strings.TrimSpace(in.Role))
	if role != "" && !model.ValidProjectRole(role) {
		return nil, fail(400, "role 只能是 admin(專案管理員)或 member(成員)")
	}
	ref, err := s.memberActor(p, org, in.Actor)
	if err != nil {
		return nil, err
	}
	cur, err := s.Store.ProjectMemberships(org, ref.ID)
	if err != nil {
		return nil, err
	}
	if role == "" {
		role = cur[x.ID]
	}
	if role == "" {
		role = model.ProjectRoleMember
	}
	changed, err := s.Store.SetProjectMember(org, x.ID, ref.ID, role, s.mt().Time)
	if errors.Is(err, store.ErrBadRef) {
		return nil, errProjectNotFound(project)
	} else if err != nil {
		return nil, err
	}
	if changed {
		s.Audit(p, "project.member.set", org, map[string]any{"id": x.ID, "actor": ref.ID, "role": role})
	}
	l, err := s.ListProjectMembersFor(org, x.ID)
	if err != nil {
		return nil, err
	}
	for i := range l {
		if l[i].ActorID == ref.ID {
			return &l[i], nil
		}
	}
	return nil, fail(404, "找不到專案成員")
}

// ListProjectMembersFor 專案成員(不檢查呼叫者;內部用,呼叫端已經授權)
func (s *Service) ListProjectMembersFor(org, project string) ([]model.ProjectMember, error) {
	l, err := s.Store.ListProjectMembers(org, project)
	if err != nil {
		return nil, err
	}
	return s.presentProjectMembers(l)
}

// RemoveProjectMember 移除成員(組織管理員或專案管理員);不是成員 404。移除角色成員時,它在這個受限專案還沒領取的任務一併取消
func (s *Service) RemoveProjectMember(p *model.Principal, org, project, actor string) error {
	x, err := s.needProjectAdmin(p, org, project)
	if err != nil {
		return err
	}
	// 先用嚴格解析器;已經離開組織的人、已刪的角色解析不到,改在現有成員裡用 handle、名稱比對
	id := ""
	if ref, err := s.ResolveActor(p, org, actor); err == nil {
		id = ref.ID
	} else {
		cur, lerr := s.Store.ListProjectMembers(org, x.ID)
		if lerr != nil {
			return lerr
		}
		ids := make([]string, 0, len(cur))
		for _, m := range cur {
			ids = append(ids, m.ActorID)
		}
		n, lerr := s.LoadActors(ids...)
		if lerr != nil {
			return lerr
		}
		id = memberMatching(ids, n, actor)
	}
	removed := false
	if id != "" {
		if removed, err = s.Store.RemoveProjectMember(org, x.ID, id); err != nil {
			return err
		}
	}
	if !removed {
		return fail(404, "專案 %s 沒有成員 %s", x.ID, strings.TrimSpace(actor))
	}
	s.Audit(p, "project.member.remove", org, map[string]any{"id": x.ID, "actor": id})
	if x.Restricted {
		s.pruneProjectTasks(p, org, x.ID)
	}
	return nil
}

// dropProjectMemberships 把 actor 從組織所有專案的成員拿掉(使用者被移出組織、角色被刪;失敗只記錄,不影響原本的操作)。
// 拿掉之後受限專案的任務一併清(角色不再是成員)
func (s *Service) dropProjectMemberships(p *model.Principal, org, actorID string) {
	if actorID == "" {
		return
	}
	projects, err := s.Store.RemoveProjectMembershipsOf(org, actorID)
	if err != nil {
		s.Audit(p, "project.member.error", org, map[string]any{"actor": actorID, "error": auditErrText(err)})
		return
	}
	for _, id := range projects {
		if x, err := s.Store.GetProject(org, id); err == nil && x.Restricted {
			s.pruneProjectTasks(p, org, id)
		}
	}
}

// pruneProjectTasks 受限專案:非專案成員的角色手上「還沒領取」(queued)的、連結到這個專案 Issue 的任務取消(任務的標題與說明帶有 Issue 的標題與內文,
// 角色不再是成員就不該拿到)。已經在進行中的任務不動(租約到期、回報後就結束)。失敗只記操作紀錄。
// 要拿 agents.mu(鎖的順序 agents.mu → cabinetMu):呼叫端不能拿著 cabinetMu
func (s *Service) pruneProjectTasks(p *model.Principal, org, project string) {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	s.pruneProjectTasksLocked(p, org, project)
}

// pruneAllRestrictedTasks 所有受限專案都做一次 pruneProjectTasksLocked(角色任務轉移、刪角色之後:任務可能轉給不是成員的角色)。要拿著 agents.mu
func (s *Service) pruneAllRestrictedTasks(p *model.Principal, org string) {
	list, err := s.Store.ListProjects(org)
	if err != nil {
		return
	}
	for _, x := range list {
		if x.Restricted {
			s.pruneProjectTasksLocked(p, org, x.ID)
		}
	}
}

// pruneProjectTasksLocked 同 pruneProjectTasks,呼叫端已經拿著 agents.mu
func (s *Service) pruneProjectTasksLocked(p *model.Principal, org, project string) {
	proj, err := s.Store.GetProject(org, project)
	if err != nil || !proj.Restricted {
		return
	}
	all, err := s.Store.ListIssuesWithDeleted(org)
	if err != nil {
		s.Audit(p, "project.prune.error", org, map[string]any{"id": project, "error": auditErrText(err)})
		return
	}
	inProject := map[string]bool{}
	for _, i := range all {
		if i.Project == project {
			inProject[i.ID] = true
		}
	}
	if len(inProject) == 0 {
		return
	}
	members, err := s.Store.ListProjectMembers(org, project)
	if err != nil {
		s.Audit(p, "project.prune.error", org, map[string]any{"id": project, "error": auditErrText(err)})
		return
	}
	isMember := map[string]bool{}
	for _, m := range members {
		isMember[m.ActorID] = true
	}
	tasks, err := s.Store.ListAgentTasks(org)
	if err != nil {
		s.Audit(p, "project.prune.error", org, map[string]any{"id": project, "error": auditErrText(err)})
		return
	}
	a := &projectAccess{s: s, org: org}
	for _, t := range tasks {
		if t.Status != "queued" || !a.linksHidden(inProject, t.Links) {
			continue
		}
		if g, err := s.Store.GetAgent(org, t.AgentID); err == nil && g.ActorID != "" && isMember[g.ActorID] {
			continue
		}
		if _, err := s.saveTaskChange(org, t, func(x *model.AgentTask) { x.Status = "canceled" }); err != nil {
			s.Audit(p, "project.prune.error", org, map[string]any{"id": project, "task": t.ID, "error": auditErrText(err)})
		}
	}
	s.pruneProjectItemsLocked(p, org, inProject, isMember) // 子項:非成員角色的負責人清掉
}
