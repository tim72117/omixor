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

// author 伺服器管理員照給的(沒給是 mcp);其他人一律是自己的 email
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
	return s.Store.ListIssues(org)
}

// QueryIssues 依條件篩選、排序的 Issue(GET /api/orgs/{org}/issues 的 query 參數;規則見 model.QueryIssues)。
// 條件是零值時同 ListIssues(全部、存放處的順序,新的在前)。status、priority、sort 不認得回 400
func (s *Service) QueryIssues(p *model.Principal, org string, q model.IssueQuery) ([]model.Issue, error) {
	if e := q.Validate(); e != "" {
		return nil, fail(400, "%s", e)
	}
	list, err := s.ListIssues(p, org)
	if err != nil {
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
		return nil, fail(404, "找不到 Issue %s", id)
	}
	return i, err
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
	assignee, err := s.resolveAssignee(org, in.Assignee)
	if err != nil {
		return nil, err
	}
	now := s.mt()
	i := model.Issue{Assignee: assignee, Title: in.Title, Body: in.Body, Status: "open", Priority: in.Priority, Labels: model.CleanLabels(in.Labels),
		Repo: repo, Source: in.Source, Link: in.Link, CreatedBy: author(p, in.Author), CreatedAt: now, UpdatedAt: now, Comments: []model.Comment{}}
	saved, err := s.Store.CreateIssue(org, i)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "issue.create", org, map[string]any{"id": saved.ID})
	if assignee != "" {
		s.syncAssignee(p, org, saved, "", author(p, in.Author))
	}
	return &saved, nil
}

func (s *Service) UpdateIssue(p *model.Principal, org, id string, in IssuePatch) (*model.Issue, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
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
	if in.Assignee != nil { // 換成角色 id(角色不存在 400)
		a, err := s.resolveAssignee(org, *in.Assignee)
		if err != nil {
			return nil, err
		}
		in.Assignee = &a
	}
	mu := s.issueLock(org, id)
	mu.Lock()
	n, cur, err := s.patchIssue(p, org, id, in)
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	if in.Assignee != nil { // 有給 assignee:取消舊角色未領取的任務、替新角色排任務(已有未結束的不重複排);在 Issue 的鎖外(會拿 agents.mu)
		s.syncAssignee(p, org, *n, cur.Assignee, author(p, in.Author))
	}
	return n, nil
}

// patchIssue UpdateIssue 的「讀了再改」(拿著 issueLock 呼叫);回傳改好的與改之前的
func (s *Service) patchIssue(p *model.Principal, org, id string, in IssuePatch) (*model.Issue, *model.Issue, error) {
	cur, err := s.Store.GetIssue(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, fail(404, "找不到 Issue %s", id)
	}
	if err != nil {
		return nil, nil, err
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
		cs, err := s.mergeComments(p, cur.Comments, *in.Comments)
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
		if in.Comments == nil || sameComments(n.Comments, cur.Comments) {
			w.Comments = nil // 留言沒變:不動存放處裡的留言(不會蓋掉別的程式同時新增的留言)
		}
		if err := s.Store.UpdateIssue(org, w); err != nil {
			return nil, nil, err
		}
		s.Audit(p, "issue.update", org, map[string]any{"id": n.ID})
	}
	return &n, cur, nil
}

// mergeComments IssuePatch.Comments 的規則(見 IssuePatch):伺服器管理員整份取代;其他人只能新增
func (s *Service) mergeComments(p *model.Principal, cur, given []model.Comment) ([]model.Comment, error) {
	old := map[string]model.Comment{}
	for _, c := range cur {
		old[c.ID] = c
	}
	if p.ServerAdmin() {
		// 既有留言的作者照伺服器上的(不能改別人的留言作者);新留言沒給作者時用 author() 的規則
		cs := []model.Comment{}
		for _, c := range given {
			if c.ID == "" {
				c.ID = uuid()
			}
			if c.At.IsZero() {
				c.At = s.mt()
			}
			if o, ok := old[c.ID]; ok {
				c.Author = o.Author
			} else if strings.TrimSpace(c.Author) == "" {
				c.Author = author(p, c.Author)
			}
			cs = append(cs, c)
		}
		return cs, nil
	}
	cs := append([]model.Comment{}, cur...)
	seen := map[string]bool{}
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
		c.Author, c.At = author(p, ""), s.mt()
		cs = append(cs, c)
	}
	return cs, nil
}

// AddIssueComment 在 Issue 留一則言(成員、角色身分都可以;權限同讀取 Issue)。只附加這一則,不會動到別人的留言,
// 同時多則留言也不會互相蓋掉。作者一律是呼叫者(伺服器管理員可以用 in.Author 指定,沒給是 mcp)
func (s *Service) AddIssueComment(p *model.Principal, org, id string, in CommentInput) (*model.Issue, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, fail(400, "留言不能是空的")
	}
	c := model.Comment{ID: uuid(), Author: author(p, in.Author), Text: text, At: s.mt()}
	mu := s.issueLock(org, id)
	mu.Lock()
	i, err := s.Store.AddIssueComment(org, id, c)
	mu.Unlock()
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "找不到 Issue %s", id)
	}
	if err != nil {
		return nil, err
	}
	s.Audit(p, "issue.update", org, map[string]any{"id": i.ID})
	return i, nil
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
	if a.Title != b.Title || a.Body != b.Body || a.Status != b.Status || a.Priority != b.Priority || a.Source != b.Source ||
		a.Link != b.Link || a.Repo != b.Repo || a.Assignee != b.Assignee || strings.Join(a.Labels, "\x00") != strings.Join(b.Labels, "\x00") {
		return false
	}
	return sameComments(a.Comments, b.Comments)
}

func (s *Service) DeleteIssue(p *model.Principal, org, id string) error {
	if _, err := s.orgRole(p, org); err != nil {
		return err
	}
	if err := s.Store.DeleteIssue(org, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fail(404, "找不到 Issue %s", id)
		}
		return err
	}
	s.Audit(p, "issue.delete", org, map[string]any{"id": id})
	return nil
}

// ExportIssues 整份 Issue(組織管理員)
func (s *Service) ExportIssues(p *model.Principal, org string) ([]model.Issue, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	list, err := s.Store.ListIssues(org)
	if err == nil {
		s.Audit(p, "issues.export", org, map[string]any{"count": len(list)})
	}
	return list, err
}

// ImportIssues 第一次搬資料用(組織管理員):保留 id、作者與時間;這個組織已經有 Issue 就拒絕。
// 之後一律單筆新增或修改,不做差異比對與同步
func (s *Service) ImportIssues(p *model.Principal, org string, list []model.Issue) (*model.ImportResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fail(400, "沒有要匯入的 Issue")
	}
	seen := map[string]bool{}
	for k, i := range list {
		if i.ID == "" || strings.TrimSpace(i.Title) == "" {
			return nil, fail(400, "每一筆都要有 id 與 title")
		}
		if seen[strings.ToUpper(i.ID)] {
			return nil, fail(400, "id 重複:%s", i.ID)
		}
		seen[strings.ToUpper(i.ID)] = true
		if !model.OneOf(i.Status, model.IssueStatuses) {
			list[k].Status = "open"
		}
		if !model.OneOf(i.Priority, model.IssuePriorities) {
			list[k].Priority = "normal"
		}
		if r, ok := model.CleanRepo(i.Repo); ok {
			list[k].Repo = r
		} else {
			return nil, fail(400, "%s 的 repo 格式不對:%s", i.ID, i.Repo)
		}
		if i.Labels == nil {
			list[k].Labels = []string{}
		}
		if i.Comments == nil {
			list[k].Comments = []model.Comment{}
		}
		if i.CreatedAt.IsZero() {
			list[k].CreatedAt = s.mt()
		}
		if i.UpdatedAt.IsZero() {
			list[k].UpdatedAt = list[k].CreatedAt
		}
	}
	cur, err := s.Store.ListIssues(org)
	if err != nil {
		return nil, err
	}
	if len(cur) > 0 {
		return nil, fail(409, "伺服器上 %s 已經有 %d 筆 Issue;匯入只在第一次搬資料時用,之後請用單筆新增或修改", org, len(cur))
	}
	n, err := s.Store.ImportIssues(org, list)
	if err != nil {
		return nil, fail(409, "%v", err)
	}
	s.Audit(p, "issues.import", org, map[string]any{"inserted": n})
	return &model.ImportResult{Org: org, Inserted: n}, nil
}

// IssueImportOptions ImportIssuesWith 的選項
type IssueImportOptions struct {
	// Merge 可以重跑的匯入(搬舊資料、omixor-web import 用):組織已經有 Issue 也照樣匯入(不回 409);
	// 沒有 id 或 id 重複(不分大小寫,後面的)的略過、算進 Skipped(不回 400);title 可以是空的(照原資料搬)。
	// id 已經在組織裡的看 Overwrite;沒有的保留 id、作者與時間,依 list 的順序排在既有的後面
	Merge bool
	// Overwrite Merge 時 id 已經有的用匯入的內容整筆取代(位置不變、留言整份取代);false 是略過(保留現有的)
	Overwrite bool
}

// IssueImportResult ImportIssuesWith 的結果
type IssueImportResult struct {
	Org      string `json:"org"`
	Inserted int    `json:"inserted"`
	Updated  int    `json:"updated"`
	Skipped  int    `json:"skipped"`
}

// ImportIssuesWith 匯入 Issue(組織管理員)。opt.Merge 是 false 時同 ImportIssues(第一次搬資料;組織已經有 Issue 回 409)
func (s *Service) ImportIssuesWith(p *model.Principal, org string, list []model.Issue, opt IssueImportOptions) (*IssueImportResult, error) {
	if !opt.Merge {
		r, err := s.ImportIssues(p, org, list)
		if err != nil {
			return nil, err
		}
		return &IssueImportResult{Org: r.Org, Inserted: r.Inserted}, nil
	}
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	res := &IssueImportResult{Org: org}
	seen := map[string]bool{}
	keep := []model.Issue{}
	for _, i := range list {
		i.ID = strings.TrimSpace(i.ID)
		if i.ID == "" || seen[strings.ToUpper(i.ID)] {
			res.Skipped++
			continue
		}
		seen[strings.ToUpper(i.ID)] = true
		if !model.OneOf(i.Status, model.IssueStatuses) {
			i.Status = "open"
		}
		if !model.OneOf(i.Priority, model.IssuePriorities) {
			i.Priority = "normal"
		}
		r, ok := model.CleanRepo(i.Repo)
		if !ok {
			return nil, fail(400, "%s 的 repo 格式不對:%s", i.ID, i.Repo)
		}
		i.Repo = r
		i.Labels = model.CleanLabels(i.Labels)
		if i.CreatedAt.IsZero() {
			i.CreatedAt = s.mt()
		}
		if i.UpdatedAt.IsZero() {
			i.UpdatedAt = i.CreatedAt
		}
		cs := make([]model.Comment, 0, len(i.Comments))
		for _, c := range i.Comments {
			if c.ID == "" {
				c.ID = uuid()
			}
			if c.At.IsZero() {
				c.At = i.UpdatedAt
			}
			cs = append(cs, c)
		}
		i.Comments = cs
		keep = append(keep, i)
	}
	if len(keep) > 0 {
		n, err := s.Store.MergeIssues(org, keep, opt.Overwrite)
		if err != nil {
			return nil, err
		}
		res.Inserted, res.Updated, res.Skipped = n.Inserted, n.Updated, res.Skipped+n.Skipped
	}
	s.Audit(p, "issues.import", org, map[string]any{"inserted": res.Inserted, "updated": res.Updated, "skipped": res.Skipped, "merge": true})
	return res, nil
}
