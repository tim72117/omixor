package service

import (
	"errors"
	"log"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 專案可見性與專案管理員:第 3 階段(ISS-34、DOC-10)權限的「單一判斷點」。凡是可能帶出專案內容(Issue、文件、專案本身、衍生的任務與頻道綁定)的入口,
// 都要經過這裡的函式,不要各自判斷。
//
// 規則:
//   - 專案沒有受限(Restricted = false,預設):行為和第 2 階段一樣,組織成員(含角色身分)看得到。
//   - 受限專案:只有「專案成員」(omixor_project_members 裡的 actor,使用者或角色)、組織管理員、伺服器管理員看得到。
//     角色憑證預設視為非成員:要被明確加成員才看得到專案與它的 Issue(文件另外看角色的 docAccess,那是管理員明確給的位置權限)。
//   - 看不到的專案,對呼叫者「就像不存在」:單筆回 404(訊息和真的不存在的一樣)、列表裡不出現、依 key 查 Issue 也是 404。
//   - 專案管理員 = 組織管理員,或該專案的成員角色是 admin:可以管理這個專案的成員(加入、移除、改角色)。
//     改受限旗標、專案代號、刪除專案、連結 repo 仍然只有組織管理員(專案管理員不能把受限專案改成公開,也不能改別的專案)。
//   - 把 Issue 從受限專案搬出去(搬到非受限專案或組織層級)要專案管理員(否則會讓一般成員把內容公開出去)。
//   - 受限專案的 Issue 只能指派給「有看到這個專案的角色」(角色是專案成員);否則角色排到的任務會帶出 Issue 的標題與內文。
//
// 這個檔案只有判斷與篩選;存取在 Store(pg_project_members.go),成員管理的操作在 project_members.go。

// projectAccess 呼叫者對一個組織各專案的可見範圍。建立後可以重複問(不再查資料庫,除了第一次問成員資格)
type projectAccess struct {
	s          *Service
	p          *model.Principal
	org        string
	restricted map[string]bool // 組織裡受限專案的 id
	all        bool            // 組織管理員(含伺服器管理員):看得到、管得到全部專案(角色憑證永遠不是)
	loaded     bool
	roles      map[string]string // 呼叫者是成員的專案 → 角色(第一次需要時才查)
}

// newProjectAccess 建立呼叫者的專案可見範圍(呼叫端要先確認呼叫者看得到這個組織:viewer、orgRole、docViewer)
func (s *Service) newProjectAccess(p *model.Principal, org string) (*projectAccess, error) {
	list, err := s.Store.ListProjects(org)
	if err != nil {
		return nil, err
	}
	a := &projectAccess{s: s, p: p, org: org, restricted: map[string]bool{}}
	for _, x := range list {
		if x.Restricted {
			a.restricted[x.ID] = true
		}
	}
	if p != nil && p.Agent == nil {
		if r, err := s.orgRole(p, org); err == nil && r == "admin" {
			a.all = true
		}
	}
	return a, nil
}

// load 第一次需要時查呼叫者的成員資格。查不到 actor 或讀取失敗都當作「不是任何專案的成員」(寧可看不到,不能多看)
func (a *projectAccess) load() {
	if a.loaded {
		return
	}
	a.loaded = true
	a.roles = map[string]string{}
	if a.p == nil {
		return
	}
	ref, err := a.s.SelfActor(a.p)
	if err != nil || ref.ID == "" {
		return
	}
	m, err := a.s.Store.ProjectMemberships(a.org, ref.ID)
	if err != nil {
		log.Printf("讀專案成員資格失敗(視為非成員):%v", err)
		return
	}
	a.roles = m
}

// Restricted 這個專案是不是受限的("" = 未分專案,不受限)
func (a *projectAccess) Restricted(project string) bool {
	return project != "" && a.restricted[project]
}

// AnyRestricted 組織裡有沒有受限專案(沒有就不用篩選)
func (a *projectAccess) AnyRestricted() bool { return len(a.restricted) > 0 }

// Role 呼叫者在專案的角色:admin(組織管理員或專案管理員)、member(專案成員)、空(不是成員)。未分專案、不存在的專案回空
func (a *projectAccess) Role(project string) string {
	if project == "" {
		return ""
	}
	if a.all {
		return model.ProjectRoleAdmin
	}
	a.load()
	return a.roles[project]
}

// Sees 看得到專案(也就是看得到它的 Issue 與內容):未分專案、非受限專案都看得到;受限專案要是成員或管理員
func (a *projectAccess) Sees(project string) bool {
	if !a.Restricted(project) {
		return true
	}
	return a.Role(project) != ""
}

// Admins 專案管理員(組織管理員,或專案成員角色是 admin)
func (a *projectAccess) Admins(project string) bool { return a.Role(project) == model.ProjectRoleAdmin }

// SeesDocs 看得到專案的文件櫃:同 Sees;角色憑證另外可以靠 docAccess 裡明確列出這個專案(管理員給的位置權限,實際能讀哪些文件仍由 docAccess 決定)
func (a *projectAccess) SeesDocs(project string, g *model.Agent) bool {
	if a.Sees(project) {
		return true
	}
	if g != nil {
		for _, id := range g.DocProjects() {
			if id == project {
				return true
			}
		}
	}
	return false
}

// FilterIssues 去掉看不到的專案的 Issue(回傳新的切片;沒有受限專案時原樣回傳)
func (a *projectAccess) FilterIssues(list []model.Issue) []model.Issue {
	if !a.AnyRestricted() {
		return list
	}
	out := make([]model.Issue, 0, len(list))
	for _, i := range list {
		if a.Sees(i.Project) {
			out = append(out, i)
		}
	}
	return out
}

// FilterDocs 去掉看不到的專案的文件(角色憑證看 SeesDocs)
func (a *projectAccess) FilterDocs(list []model.CabinetDoc, g *model.Agent) []model.CabinetDoc {
	if !a.AnyRestricted() {
		return list
	}
	out := make([]model.CabinetDoc, 0, len(list))
	for _, d := range list {
		if a.SeesDocs(d.Project, g) {
			out = append(out, d)
		}
	}
	return out
}

// FilterProjects 去掉看不到的專案;同時填 MyRole。角色憑證另看 SeesDocs(專案清單本來就是文件櫃的一部分)
func (a *projectAccess) FilterProjects(list []model.Project, g *model.Agent) []model.Project {
	out := make([]model.Project, 0, len(list))
	for _, x := range list {
		if !a.SeesDocs(x.ID, g) {
			continue
		}
		x.MyRole = a.Role(x.ID)
		out = append(out, x)
	}
	return out
}

// errProjectNotFound 專案不存在、或存在但呼叫者看不到:同一句話(不洩漏存在)
func errProjectNotFound(id string) error {
	return fail(404, "找不到專案 %s", strings.TrimSpace(id))
}

// hiddenIssueIDs 呼叫者看不到的專案裡的 Issue(含垃圾桶)的內部 id;沒有看不到的專案時是 nil。
// 給任務、頻道這類只用 Issue 編號關聯的衍生資料篩選用
func (a *projectAccess) hiddenIssueIDs() (map[string]bool, error) {
	hidden := false
	for id := range a.restricted {
		if !a.Sees(id) {
			hidden = true
			break
		}
	}
	if !hidden {
		return nil, nil
	}
	all, err := a.s.Store.ListIssuesWithDeleted(a.org)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, i := range all {
		if !a.Sees(i.Project) {
			out[i.ID] = true
		}
	}
	return out, nil
}

// linksHidden 任務的相關連結有沒有指向看不到的 Issue(內部 id、目前或舊的 key)
func (a *projectAccess) linksHidden(hidden map[string]bool, links []string) bool {
	if len(hidden) == 0 {
		return false
	}
	for _, l := range links {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if hidden[l] || hidden[strings.ToUpper(l)] || hidden[strings.ToLower(l)] {
			return true
		}
		if model.IsUUID(l) || model.IssueKeyNumber(l) == 0 {
			continue
		}
		if id, err := a.s.Store.ResolveIssueID(a.org, l); err == nil && hidden[id] {
			return true
		}
	}
	return false
}

// FilterTasks 去掉連結指向看不到的 Issue 的任務(任務的標題與說明帶有 Issue 的標題與內文)
func (a *projectAccess) FilterTasks(list []model.AgentTask) ([]model.AgentTask, error) {
	hidden, err := a.hiddenIssueIDs()
	if err != nil || len(hidden) == 0 {
		return list, err
	}
	out := make([]model.AgentTask, 0, len(list))
	for _, t := range list {
		if !a.linksHidden(hidden, t.Links) {
			out = append(out, t)
		}
	}
	return out, nil
}

// TaskHidden 單一任務是不是看不到
func (a *projectAccess) TaskHidden(t *model.AgentTask) (bool, error) {
	hidden, err := a.hiddenIssueIDs()
	if err != nil || len(hidden) == 0 {
		return false, err
	}
	return a.linksHidden(hidden, t.Links), nil
}

// IssueHidden 內部 id 的 Issue 是不是看不到(查不到 Issue 當作看得到:沒有東西可以洩漏)
func (a *projectAccess) IssueHidden(issueID string) (bool, error) {
	hidden, err := a.hiddenIssueIDs()
	if err != nil || len(hidden) == 0 {
		return false, err
	}
	return hidden[issueID], nil
}

// ---- 單筆檢查(給只問一次的入口)----

// canSeeProject 呼叫者看得到這個專案嗎("" = 未分專案,永遠看得到)。呼叫端要先確認呼叫者看得到這個組織
func (s *Service) canSeeProject(p *model.Principal, org, project string) (bool, error) {
	if project == "" {
		return true, nil
	}
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return false, err
	}
	return a.Sees(project), nil
}

// projectViewer 取得看得到的專案:不存在與看不到都是 404(同一句話)。呼叫端要先確認呼叫者看得到這個組織
func (s *Service) projectViewer(p *model.Principal, org, id string) (*model.Project, *projectAccess, error) {
	x, err := s.findProject(org, id)
	if err != nil {
		return nil, nil, err
	}
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, nil, err
	}
	if !a.Sees(x.ID) {
		return nil, nil, errProjectNotFound(id)
	}
	x.MyRole = a.Role(x.ID)
	return x, a, nil
}

// needProjectAdmin 專案管理員才能做:組織管理員、或該專案的成員角色是 admin。看不到的專案 404;看得到但不是管理員 403。
// 角色憑證不行(403)。回傳專案
func (s *Service) needProjectAdmin(p *model.Principal, org, id string) (*model.Project, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	x, a, err := s.projectViewer(p, org, id)
	if err != nil {
		return nil, err
	}
	if !a.Admins(x.ID) {
		return nil, fail(403, "要組織管理員或專案 %s 的管理員才能做", x.ID)
	}
	return x, nil
}

// issueErr 取得 Issue 之後的可見性檢查:看不到的專案的 Issue 回和「不存在」一樣的 404
func (a *projectAccess) issueErr(i *model.Issue, ref string) error {
	if i != nil && !a.Sees(i.Project) {
		return issueNotFound(ref)
	}
	return nil
}

// agentMayWork 角色(actor id)能不能處理這個專案的 Issue:非受限專案都可以;受限專案要是專案成員
func (s *Service) agentMayWork(org, project, agentActorID string) (bool, error) {
	if project == "" {
		return true, nil
	}
	x, err := s.Store.GetProject(org, project)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !x.Restricted) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if agentActorID == "" {
		return false, nil
	}
	m, err := s.Store.ProjectMemberships(org, agentActorID)
	if err != nil {
		return false, err
	}
	return m[project] != "", nil
}

// needIssueVisible 內部 id 的 Issue 呼叫者看得到嗎(看不到回和不存在一樣的 404,訊息用呼叫者給的編號 ref)
func (s *Service) needIssueVisible(p *model.Principal, org, issueID, ref string) error {
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return err
	}
	hidden, err := a.IssueHidden(issueID)
	if err != nil {
		return err
	}
	if hidden {
		return issueNotFound(ref)
	}
	return nil
}

// checkAssigneeProject 受限專案的 Issue 只能指派給看得到專案的角色(專案成員):否則角色排到的任務會帶出 Issue 的標題與內文。
// assignee 是角色的 actor id(空 = 不指派);專案是空字串或不受限不檢查。400
func (s *Service) checkAssigneeProject(org, project, assignee string) error {
	if project == "" || assignee == "" {
		return nil
	}
	ok, err := s.agentMayWork(org, project, assignee)
	if err != nil {
		return err
	}
	if !ok {
		return fail(400, "專案 %s 是受限專案,指派的角色要先加為專案成員(或取消指派)", project)
	}
	return nil
}
