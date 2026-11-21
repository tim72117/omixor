package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"

	"omixor-core/model"
	"omixor-core/store"
)

// IssueInput 建立 Issue
type IssueInput struct {
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Priority string   `json:"priority"`
	Labels   []string `json:"labels"`
	Source   string   `json:"source"`
	Link     string   `json:"link"`
	// Repo 所屬的程式碼倉庫(空 = 不標記);格式見 model.CleanRepo,不合格 400
	Repo   string `json:"repo,omitempty"`
	Author string `json:"author"` // 伺服器管理員(app、omixor-web)可以指定;其他人一律是自己的 email
	// Assignee 指派給的角色 id(空 = 不指派);角色必須存在(否則 400),且 active 時會自動替它排一筆任務
	Assignee string `json:"assignee,omitempty"`
	// Project 放進的專案 id(空字串 = 明確指定組織層級,key 是 ISS-<n>);專案要存在(404)而且設定了代號(400),key 是 <代號>-<n>。
	// nil(沒給)= 預設:Repo 只連到一個專案(model.ProjectRepo;專案有代號)就放進那個專案,其他情況是組織層級;給了就不看 repo 的連結
	Project *string `json:"project,omitempty"`
}

// IssuePatch 修改 Issue:有給的欄位整個取代(同 app 的 issue_update)
type IssuePatch struct {
	Title    *string   `json:"title,omitempty"`
	Body     *string   `json:"body,omitempty"`
	Status   *string   `json:"status,omitempty"`
	Priority *string   `json:"priority,omitempty"`
	Labels   *[]string `json:"labels,omitempty"`
	Source   *string   `json:"source,omitempty"`
	Link     *string   `json:"link,omitempty"`
	// Repo 改所屬倉庫:有給才改("" = 清除標記);格式不合格 400
	Repo *string `json:"repo,omitempty"`
	// Assignee 改指派:有給才處理("" = 取消指派);角色不存在 400。舊角色還沒領取的任務取消、新角色自動排任務
	Assignee *string `json:"assignee,omitempty"`
	// AppendBody 在內文最後加一段(和現有內文用空行隔開;有給 Body 時接在新的 Body 後面)。
	// 在 Issue 的鎖裡以最新內容附加,不會蓋掉同時進來的修改
	AppendBody *string `json:"appendBody,omitempty"`
	// Comments 整份留言。新增留言請改用 AddIssueComment(POST …/issues/{id}/comments)。
	// 伺服器管理員:整份取代(匯入、維運用)。其他人:只接受新增——伺服器上已有的留言一律保留(沒送的不會被刪、
	// 作者照伺服器上的),改別人或自己已有留言的文字回 403;不認得 id 的留言附加在最後,作者一律是自己
	Comments *[]model.Comment `json:"comments,omitempty"`
	Author   string           `json:"author,omitempty"`
	// Restore 還原模式(只限伺服器管理員,否則 403;CLI issues restore 用):Comments 整份取代時,伺服器上沒有的留言(被刪掉、要補回的)
	// 的作者字串當歷史署名,用匯入的寬鬆解析器(actor_import.go)解析,不套 D9 的「不能代別的使用者」檢查;
	// 沒有這個旗標時,整份取代裡的新留言作者只能是呼叫者本人、角色或系統(AuthorReject)。會留稽核(issue.restore)
	Restore bool `json:"restore,omitempty"`
	// Project 搬到這個專案(有給才處理;"" = 搬回組織層級)。在目的範圍發新的 key,舊的 key 留在別名表照樣查得到
	Project *string `json:"project,omitempty"`
	// Version 樂觀鎖:有給而且和目前版本不同回 409(Data.current 是目前的 Issue、currentVersion 是目前版本);沒給 = 不檢查
	Version *int `json:"version,omitempty"`
}

// errIssueConflict 版本不符的 409:帶目前的 Issue(用戶端可以拿來合併)
func errIssueConflict(cur *model.Issue, version int64) error {
	data := map[string]any{"currentVersion": version}
	if cur != nil {
		data["current"] = cur
	}
	return &Error{Code: 409, Msg: "這張 Issue 已經被別人改過,請重新載入", Data: data}
}

// conflictErr errIssueConflict 的服務層版本:cur 是存放處讀到的(actor id),帶進錯誤前先轉成相容字串
func (s *Service) conflictErr(cur *model.Issue, version int64) error {
	if pc, err := s.presentIssue(cur); err == nil {
		cur = pc
	}
	return errIssueConflict(cur, version)
}

// issueNotFound Issue 不存在(含在垃圾桶裡的)
func issueNotFound(id string) error { return fail(404, "找不到 Issue %s", id) }

// resolveIssueID Issue 編號(內部 id、目前或舊的 key,不分大小寫)→ 內部 id;找不到 404。
// 所有收 Issue 編號的入口先呼叫它(鎖、比對都用內部 id)
func (s *Service) resolveIssueID(org, ref string) (string, error) {
	id, err := s.Store.ResolveIssueID(org, strings.TrimSpace(ref))
	if errors.Is(err, store.ErrNotFound) {
		return "", issueNotFound(ref)
	}
	return id, err
}

// ResolveIssueID 同 resolveIssueID(組織成員;給 api、頻道等外部入口用)
func (s *Service) ResolveIssueID(p *model.Principal, org, ref string) (string, error) {
	if err := s.viewer(p, org); err != nil {
		return "", err
	}
	id, err := s.resolveIssueID(org, ref)
	if err != nil {
		return "", err
	}
	// 受限專案(看不到的)的 key、別名、UUID 都當作不存在:404 與真的不存在同一句話
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return "", err
	}
	if hidden, err := a.IssueHidden(id); err != nil {
		return "", err
	} else if hidden {
		return "", issueNotFound(ref)
	}
	return id, nil
}

// issueProject Issue 要放進的專案:空白 = 組織層級;否則要存在(404)而且有代號(400)。回傳存起來的專案 id。
// 呼叫者看不到的專案(受限)和不存在一樣回 404,而且先於「沒有代號」的檢查(不洩漏存在)
func (s *Service) issueProject(a *projectAccess, org, project string) (string, error) {
	pid, err := s.projectID(org, project)
	if err != nil || pid == "" {
		return pid, err
	}
	if !a.Sees(pid) {
		return "", errProjectNotFound(project)
	}
	x, err := s.findProject(org, pid)
	if err != nil {
		return "", err
	}
	if x.Key == "" {
		return "", fail(400, "這個專案還沒設定代號")
	}
	return pid, nil
}

// issueStoreErr 存放處建立、搬移 Issue 的錯誤 → 給使用者看的
func issueStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrProjectNoKey):
		return fail(400, "這個專案還沒設定代號")
	case errors.Is(err, store.ErrBadRef):
		return fail(404, "找不到專案")
	}
	return err
}

// CommentInput 新增一則留言(POST /api/orgs/{org}/issues/{id}/comments)
type CommentInput struct {
	Text   string `json:"text"`
	Author string `json:"author,omitempty"` // 只有伺服器管理員的有效(同 IssueInput.Author)
}

// issueLock 同一個 Issue 的「讀了再改」在這把鎖裡(一個 Service 實例、一個組織 + Issue id 一把,id 不分大小寫)。
// 是最內層的鎖:拿著時只呼叫 Store 與 Audit,不再拿 Service 的其他鎖(見 locks.go)
func (s *Service) issueLock(org, id string) *sync.Mutex {
	s.issueMu.Lock()
	defer s.issueMu.Unlock()
	if s.issueLocks == nil {
		s.issueLocks = map[string]*sync.Mutex{}
	}
	k := org + "\x00" + strings.ToUpper(strings.TrimSpace(id))
	l := s.issueLocks[k]
	if l == nil {
		l = &sync.Mutex{}
		s.issueLocks[k] = l
	}
	return l
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}

// author 舊的署名字串(伺服器管理員照給的、沒給是 mcp;其他人是自己的 email)。
// Issue、留言、文件已改用 ResolveAuthor(actor id);這個只剩 agents_tasks.go、agents_routines.go(C-2b 的檔)在用,C-2b 改完後刪除
func author(p *model.Principal, given string) string {
	if !p.ServerAdmin() {
		return p.User.Email
	}
	if g := strings.TrimSpace(given); g != "" {
		return g
	}
	return "mcp"
}

func (s *Service) ListIssues(p *model.Principal, org string) ([]model.Issue, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	list, err := s.Store.ListIssues(org)
	if err != nil {
		return nil, err
	}
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	out, err := s.presentIssues(a.FilterIssues(list))
	if err != nil {
		return nil, err
	}
	return s.withItemCounts(org, out), nil
}

// ListDeletedIssues 垃圾桶裡的 Issue(組織管理員;新的在前)
func (s *Service) ListDeletedIssues(p *model.Principal, org string) ([]model.Issue, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	list, err := s.Store.ListDeletedIssues(org)
	if err != nil {
		return nil, err
	}
	return s.presentIssues(list)
}

// QueryIssues 依條件篩選、排序的 Issue(GET /api/orgs/{org}/issues 的 query 參數;規則見 model.QueryIssues)。
// 條件是零值時同 ListIssues(全部、存放處的順序,新的在前)。status、priority、sort 不認得回 400。
// q.Deleted = 列垃圾桶(組織管理員),其他條件照樣套用
func (s *Service) QueryIssues(p *model.Principal, org string, q model.IssueQuery) ([]model.Issue, error) {
	if e := q.Validate(); e != "" {
		return nil, fail(400, "%s", e)
	}
	list, err := s.ListIssues(p, org)
	if q.Deleted {
		list, err = s.ListDeletedIssues(p, org)
	}
	if err != nil {
		return nil, err
	}
	if q, err = s.withRepoLinks(org, q); err != nil {
		return nil, err
	}
	return model.QueryIssues(list, q), nil
}

// IssueBoard 看板(GET /api/orgs/{org}/issues/board):依條件篩選後分成待處理、進行中、已完成三欄(規則見 model.BuildIssueBoard;
// 不看 q.Status 與 q.Sort)。priority 不認得回 400
func (s *Service) IssueBoard(p *model.Principal, org string, q model.IssueQuery) (*model.IssueBoard, error) {
	q.Status, q.Sort = "", ""
	if e := q.Validate(); e != "" {
		return nil, fail(400, "%s", e)
	}
	list, err := s.ListIssues(p, org)
	if err != nil {
		return nil, err
	}
	if q, err = s.withRepoLinks(org, q); err != nil {
		return nil, err
	}
	b := model.BuildIssueBoard(list, q)
	return &b, nil
}

// FilterIssuesByRepo 依 repo 篩選;repo 空字串 = 不篩。同 model.FilterIssues 只給 Repo(比對規則見 model.RepoMatch)
func FilterIssuesByRepo(list []model.Issue, repo string) []model.Issue {
	if strings.TrimSpace(repo) == "" {
		return list
	}
	return model.FilterIssues(list, model.IssueQuery{Repo: repo})
}

func repoErr() error {
	return fail(400, "repo 只能用英數與 - _ . / :,最長 %d 字(例如 owner/name、gitlab:group/sub/project)", model.RepoMaxLen)
}

func (s *Service) GetIssue(p *model.Principal, org, id string) (*model.Issue, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	i, err := s.Store.GetIssue(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, issueNotFound(id)
	}
	if err != nil {
		return nil, err
	}
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	if err := a.issueErr(i, id); err != nil {
		return nil, err
	}
	out, err := s.presentIssue(i)
	if err != nil {
		return nil, err
	}
	if l := s.withItemCounts(org, []model.Issue{*out}); len(l) == 1 {
		*out = l[0]
	}
	return out, nil
}

func (s *Service) CreateIssue(p *model.Principal, org string, in IssueInput) (*model.Issue, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, fail(400, "要給 title")
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	if !model.OneOf(in.Priority, model.IssuePriorities) {
		return nil, fail(400, "priority 必須是 %s 之一", strings.Join(model.IssuePriorities, "、"))
	}
	repo, ok := model.CleanRepo(in.Repo)
	if !ok {
		return nil, repoErr()
	}
	by, err := s.ResolveAuthor(p, org, in.Author, AuthorIgnore)
	if err != nil {
		return nil, err
	}
	acc, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	assignee, err := s.resolveIssueAssignee(p, org, in.Assignee)
	if err != nil {
		return nil, err
	}
	if assignee != "" { // 建立時指派:有設定組織鏈就不能繞過(見 checkIssueAssign)
		if err := s.checkIssueAssign(p, org, assignee); err != nil {
			return nil, err
		}
	}
	var project string
	if in.Project != nil { // 明確指定(含空字串 = 不分專案),不看 repo 的連結
		project, err = s.issueProject(acc, org, *in.Project)
	} else { // 沒指定:repo 只連到一個專案就預設放進去(呼叫者看不到的專案不會被預設進去,退回組織層級)
		project, err = s.defaultProjectForRepo(org, repo)
		if err == nil && !acc.Sees(project) {
			project = ""
		}
	}
	if err != nil {
		return nil, err
	}
	if err := s.checkAssigneeProject(org, project, assignee); err != nil {
		return nil, err
	}
	now := s.mt()
	i := model.Issue{Project: project, Assignee: assignee, Title: in.Title, Body: in.Body, Status: "open", Priority: in.Priority, Labels: model.CleanLabels(in.Labels),
		Repo: repo, Source: in.Source, Link: in.Link, CreatedBy: by.ID, CreatedAt: now, UpdatedAt: now, Comments: []model.Comment{}}
	saved, err := s.Store.CreateIssue(org, i)
	if err != nil {
		return nil, issueStoreErr(err)
	}
	s.Audit(p, "issue.create", org, map[string]any{"id": saved.ID, "key": saved.Key, "project": saved.Project})
	if assignee != "" {
		s.syncIssueAssignee(p, org, saved, "", by.ID)
	}
	return s.presentIssue(&saved)
}

func (s *Service) UpdateIssue(p *model.Principal, org, id string, in IssuePatch) (*model.Issue, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	if in.Restore && (p.Agent != nil || !p.ServerAdmin()) {
		return nil, fail(403, "restore 只有伺服器管理員可以用")
	}
	if in.Priority != nil && !model.OneOf(*in.Priority, model.IssuePriorities) {
		return nil, fail(400, "priority 必須是 %s 之一", strings.Join(model.IssuePriorities, "、"))
	}
	if in.Status != nil && !model.OneOf(*in.Status, model.IssueStatuses) {
		return nil, fail(400, "status 必須是 open、closed 之一")
	}
	if in.Title != nil && strings.TrimSpace(*in.Title) == "" {
		return nil, fail(400, "標題不能是空的")
	}
	if in.Repo != nil {
		r, ok := model.CleanRepo(*in.Repo)
		if !ok {
			return nil, repoErr()
		}
		in.Repo = &r
	}
	if in.Assignee != nil { // 換成角色的 actor id(角色不存在 400)
		a, err := s.resolveIssueAssignee(p, org, *in.Assignee)
		if err != nil {
			return nil, err
		}
		in.Assignee = &a
	}
	acc, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	if in.Project != nil { // 換成存起來的專案 id(不存在或看不到 404、沒有代號 400)
		pr, err := s.issueProject(acc, org, *in.Project)
		if err != nil {
			return nil, err
		}
		in.Project = &pr
	}
	by, err := s.ResolveAuthor(p, org, in.Author, AuthorIgnore)
	if err != nil {
		return nil, err
	}
	ref := id
	id, err = s.resolveIssueID(org, id) // 鎖與存放處都用內部 id(同一張 Issue 用不同編號也是同一把鎖)
	if err != nil {
		return nil, err
	}
	mu := s.issueLock(org, id)
	mu.Lock()
	n, cur, err := s.patchIssue(p, acc, org, id, ref, in)
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	if in.Assignee != nil { // 有給 assignee:取消舊角色未領取的任務、替新角色排任務(已有未結束的不重複排);在 Issue 的鎖外(會拿 agents.mu)
		s.syncIssueAssignee(p, org, *n, cur.Assignee, by.ID)
	}
	return s.presentIssue(n)
}

// patchIssue UpdateIssue 的「讀了再改」(拿著 issueLock 呼叫);回傳改好的與改之前的
func (s *Service) patchIssue(p *model.Principal, acc *projectAccess, org, id, ref string, in IssuePatch) (*model.Issue, *model.Issue, error) {
	cur, err := s.Store.GetIssue(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, issueNotFound(id)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := acc.issueErr(cur, ref); err != nil { // 看不到的專案的 Issue:和不存在一樣(訊息用呼叫者給的編號,不帶內部 id;先於版本檢查,409 會帶出內容)
		return nil, nil, err
	}
	if in.Version != nil && *in.Version != cur.Version {
		return nil, nil, s.conflictErr(cur, int64(cur.Version))
	}
	expected := 0 // 存放處的條件式更新用:有給版本才檢查(多個實例時 issueLock 擋不住,要靠資料庫)
	if in.Version != nil {
		expected = *in.Version
	}
	// 換指派給別的角色:有設定組織鏈時,和直接派任務(AssignTask、DelegateTask)走同一套規則,不能繞過(見 checkIssueAssign)
	if in.Assignee != nil && *in.Assignee != "" && *in.Assignee != cur.Assignee {
		if err := s.checkIssueAssign(p, org, *in.Assignee); err != nil {
			return nil, nil, err
		}
	}
	// 受限專案的規則:搬出去要專案管理員(否則一般成員能把內容公開出去);指派的角色要能看到目標專案
	toProject := cur.Project
	if in.Project != nil {
		toProject = *in.Project
	}
	if toProject != cur.Project && acc.Restricted(cur.Project) && !acc.Admins(cur.Project) {
		return nil, nil, fail(403, "要組織管理員或專案 %s 的管理員才能把 Issue 搬出受限專案", cur.Project)
	}
	toAssignee := cur.Assignee
	if in.Assignee != nil {
		toAssignee = *in.Assignee
	}
	if toProject != cur.Project || toAssignee != cur.Assignee {
		if err := s.checkAssigneeProject(org, toProject, toAssignee); err != nil {
			return nil, nil, err
		}
	}
	orig := cur
	if in.Project != nil && *in.Project != cur.Project { // 搬專案:發新的 key,舊的留在別名表
		moved, oldKey, err := s.Store.MoveIssue(org, cur.ID, *in.Project, expected, s.mt())
		if err != nil {
			return nil, nil, s.issueWriteErr(org, id, err)
		}
		s.Audit(p, "issue.move", org, map[string]any{"id": moved.ID, "from": oldKey, "to": moved.Key, "project": moved.Project})
		cur = moved
		if expected > 0 {
			expected = moved.Version
		}
	}
	n := *cur
	if in.Assignee != nil {
		n.Assignee = *in.Assignee
	}
	n.Labels = append([]string{}, cur.Labels...)
	n.Comments = append([]model.Comment{}, cur.Comments...)
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&n.Title, in.Title)
	set(&n.Body, in.Body)
	set(&n.Status, in.Status)
	set(&n.Priority, in.Priority)
	set(&n.Source, in.Source)
	set(&n.Link, in.Link)
	set(&n.Repo, in.Repo)
	if in.AppendBody != nil && *in.AppendBody != "" { // 以鎖裡讀到的最新內文附加
		if n.Body != "" {
			n.Body += "\n\n"
		}
		n.Body += *in.AppendBody
	}
	if in.Labels != nil {
		n.Labels = model.CleanLabels(*in.Labels)
	}
	if in.Comments != nil {
		cs, err := s.mergeComments(p, org, cur.Comments, *in.Comments, in.Restore)
		if err != nil {
			return nil, nil, err
		}
		n.Comments = cs
	}
	if n.Status != cur.Status {
		if n.Status == "closed" {
			t := s.mt()
			n.ClosedAt = &t
		} else {
			n.ClosedAt = nil
		}
	}
	if !same(n, *cur) {
		n.UpdatedAt = s.mt()
		w := n
		w.Version = expected
		if in.Comments == nil || sameComments(n.Comments, cur.Comments) {
			w.Comments = nil // 留言沒變:不動存放處裡的留言(不會蓋掉別的程式同時新增的留言)
		}
		if err := s.Store.UpdateIssue(org, w); err != nil {
			return nil, nil, s.issueWriteErr(org, id, err)
		}
		// 版本號只在本體(標題、內文、狀態、優先度、標籤、來源、連結、指派、repo)有變才 +1,和 store 的條件一致;
		// 只有留言變了(新增留言)不算修改本體,回應的 Version 要等於實際存下的
		body := n
		body.Comments = cur.Comments
		if same(body, *cur) {
			n.Version = cur.Version
		} else {
			n.Version = cur.Version + 1
		}
		act := "issue.update"
		if in.Restore {
			act = "issue.restore"
		}
		s.Audit(p, act, org, map[string]any{"id": n.ID, "key": n.Key})
	}
	return &n, orig, nil
}

// issueWriteErr 存放處寫入 Issue 的錯誤:版本不符 → 409(帶目前的 Issue);不存在 → 404;其他照 issueStoreErr
func (s *Service) issueWriteErr(org, id string, err error) error {
	var vc *store.VersionConflictError
	if errors.As(err, &vc) {
		cur, _ := s.Store.GetIssue(org, id)
		return s.conflictErr(cur, vc.Current)
	}
	if errors.Is(err, store.ErrNotFound) {
		return issueNotFound(id)
	}
	return issueStoreErr(err)
}

// mergeComments IssuePatch.Comments 的規則(見 IssuePatch):伺服器管理員整份取代;其他人只能新增
func (s *Service) mergeComments(p *model.Principal, org string, cur, given []model.Comment, restore bool) ([]model.Comment, error) {
	old := map[string]model.Comment{}
	for _, c := range cur {
		old[c.ID] = c
	}
	if p.ServerAdmin() {
		// 既有留言的作者照伺服器上的(不能改別人的留言作者);新留言的作者字串走署名規則(ResolveAuthor:沒給是自己,
		// 只能代角色與系統);cur 的作者是 actor id,given 的作者可能是相容字串(讀出再寫回)
		cs := []model.Comment{}
		var who *importWho
		if restore {
			who = s.newImportWho(p, org)
		}
		for _, c := range given {
			if c.ID == "" {
				c.ID = uuid()
			}
			if c.At.IsZero() {
				c.At = s.mt()
			}
			if o, ok := old[c.ID]; ok {
				c.Author = o.Author
			} else if restore { // 還原:歷史署名(email、agent:<id>…)用寬鬆解析器;沒署名的是呼叫者本人
				id, err := who.id(c.Author, "")
				if err != nil {
					return nil, err
				}
				if id == "" {
					ref, err := s.SelfActor(p)
					if err != nil {
						return nil, err
					}
					id = ref.ID
				}
				c.Author = id
			} else {
				ref, err := s.ResolveAuthor(p, org, c.Author, AuthorReject)
				if err != nil {
					return nil, err
				}
				c.Author = ref.ID
			}
			cs = append(cs, c)
		}
		return cs, nil
	}
	cs := append([]model.Comment{}, cur...)
	seen := map[string]bool{}
	var self model.ActorRef
	for _, c := range given {
		if o, ok := old[c.ID]; ok && c.ID != "" {
			if c.Text != o.Text {
				return nil, fail(403, "不能修改已有的留言(%s);新增留言請用 POST /api/orgs/{org}/issues/{id}/comments", c.ID)
			}
			continue
		}
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		if c.ID == "" || seen[c.ID] {
			c.ID = uuid()
		}
		seen[c.ID] = true
		if self.ID == "" {
			var err error
			if self, err = s.SelfActor(p); err != nil {
				return nil, err
			}
		}
		c.Author, c.At = self.ID, s.mt()
		cs = append(cs, c)
	}
	return cs, nil
}

// AddIssueComment 在 Issue 留一則言(成員、角色身分都可以;權限同讀取 Issue)。只附加這一則,不會動到別人的留言,
// 同時多則留言也不會互相蓋掉。作者一律是呼叫者(伺服器管理員可以用 in.Author 指定角色或系統署名,不能代別的使用者;沒給是自己)
func (s *Service) AddIssueComment(p *model.Principal, org, id string, in CommentInput) (*model.Issue, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, fail(400, "留言不能是空的")
	}
	who, err := s.ResolveAuthor(p, org, in.Author, AuthorIgnore)
	if err != nil {
		return nil, err
	}
	c := model.Comment{ID: uuid(), Author: who.ID, Text: text, At: s.mt()}
	ref := id
	id, err = s.resolveIssueID(org, id)
	if err != nil {
		return nil, err
	}
	if err := s.needIssueVisible(p, org, id, ref); err != nil { // 看不到的專案的 Issue:和不存在一樣(不能留言)
		return nil, err
	}
	mu := s.issueLock(org, id)
	mu.Lock()
	i, err := s.Store.AddIssueComment(org, id, c)
	mu.Unlock()
	if errors.Is(err, store.ErrNotFound) {
		return nil, issueNotFound(ref)
	}
	if err != nil {
		return nil, err
	}
	s.Audit(p, "issue.update", org, map[string]any{"id": i.ID})
	return s.presentIssue(i)
}

func sameComments(a, b []model.Comment) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		x, y := a[k], b[k]
		if x.ID != y.ID || x.Author != y.Author || x.Text != y.Text || !x.At.Equal(y.At.Time) {
			return false
		}
	}
	return true
}

func same(a, b model.Issue) bool {
	if a.Project != b.Project || a.Title != b.Title || a.Body != b.Body || a.Status != b.Status || a.Priority != b.Priority || a.Source != b.Source ||
		a.Link != b.Link || a.Repo != b.Repo || a.Assignee != b.Assignee || strings.Join(a.Labels, "\x00") != strings.Join(b.Labels, "\x00") {
		return false
	}
	return sameComments(a.Comments, b.Comments)
}

// DeleteIssue 丟進垃圾桶(軟刪除;組織成員都可以)。之後列表、取得、修改都當作不存在;key 與別名保留、序號不回收。
// 指派給角色、還沒領取的任務取消(同取消指派)。永久刪除是 PurgeIssue,拿回來是 RestoreIssue(都是組織管理員)
func (s *Service) DeleteIssue(p *model.Principal, org, id string) error {
	if _, err := s.orgRole(p, org); err != nil {
		return err
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return err
	}
	iid, err := s.resolveIssueID(org, id)
	if err != nil {
		return err
	}
	if err := s.needIssueVisible(p, org, iid, id); err != nil {
		return err
	}
	mu := s.issueLock(org, iid)
	mu.Lock()
	i, err := s.Store.TrashIssue(org, iid, by.ID, s.mt())
	mu.Unlock()
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return issueNotFound(id)
		}
		return err
	}
	s.Audit(p, "issue.delete", org, map[string]any{"id": i.ID, "key": i.Key})
	if i.Assignee != "" { // 指派的角色還沒領取的任務取消(在 Issue 的鎖外,會拿 agents.mu)
		gone := *i
		gone.Assignee = ""
		s.syncIssueAssignee(p, org, gone, i.Assignee, by.ID)
	}
	return nil
}

// RestoreIssue 從垃圾桶拿回來(組織管理員);不在垃圾桶裡 404。指派不變,但被取消的任務不會自動重排
func (s *Service) RestoreIssue(p *model.Principal, org, id string) (*model.Issue, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	iid, err := s.resolveIssueID(org, id)
	if err != nil {
		return nil, err
	}
	mu := s.issueLock(org, iid)
	mu.Lock()
	i, err := s.Store.RestoreIssue(org, iid, s.mt())
	mu.Unlock()
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "垃圾桶裡沒有 Issue %s", id)
	}
	if err != nil {
		return nil, err
	}
	s.Audit(p, "issue.restore", org, map[string]any{"id": i.ID, "key": i.Key})
	return s.presentIssue(i)
}

// PurgeIssue 永久刪除(組織管理員;垃圾桶裡的或還沒刪的都可以;留言、別名一起刪,綁這張 Issue 的頻道解除綁定)。不能復原
func (s *Service) PurgeIssue(p *model.Principal, org, id string) error {
	if err := s.needOrgAdmin(p, org); err != nil {
		return err
	}
	iid, err := s.resolveIssueID(org, id)
	if err != nil {
		return err
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return err
	}
	cur, _ := s.Store.GetIssue(org, iid)
	mu := s.issueLock(org, iid)
	mu.Lock()
	err = s.Store.DeleteIssue(org, iid)
	mu.Unlock()
	if errors.Is(err, store.ErrNotFound) {
		return issueNotFound(id)
	}
	if err != nil {
		return err
	}
	s.Audit(p, "issue.purge", org, map[string]any{"id": iid, "ref": id})
	if cur != nil && cur.Assignee != "" { // 還沒丟進垃圾桶就永久刪除:任務同樣取消
		gone := *cur
		gone.Assignee = ""
		s.syncIssueAssignee(p, org, gone, cur.Assignee, by.ID)
	}
	return nil
}

// ExportIssues 整份 Issue(組織管理員;不含垃圾桶裡的)
func (s *Service) ExportIssues(p *model.Principal, org string) ([]model.Issue, error) {
	return s.ExportIssuesWith(p, org, false)
}

// ExportIssuesWith 同 ExportIssues;withDeleted 時也包含垃圾桶裡的(帶 deletedAt)
func (s *Service) ExportIssuesWith(p *model.Principal, org string, withDeleted bool) ([]model.Issue, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	list, err := s.Store.ListIssues(org)
	if withDeleted {
		list, err = s.Store.ListIssuesWithDeleted(org)
	}
	if err != nil {
		return nil, err
	}
	s.Audit(p, "issues.export", org, map[string]any{"count": len(list), "withDeleted": withDeleted})
	return s.presentIssues(list)
}
