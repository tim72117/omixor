package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

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
	Author   string   `json:"author"` // 伺服器管理員(app、ops-web)可以指定;其他人一律是自己的 email
}

// IssuePatch 修改 Issue:有給的欄位整個取代(同 app 的 ops_issue_update)
type IssuePatch struct {
	Title    *string          `json:"title,omitempty"`
	Body     *string          `json:"body,omitempty"`
	Status   *string          `json:"status,omitempty"`
	Priority *string          `json:"priority,omitempty"`
	Labels   *[]string        `json:"labels,omitempty"`
	Source   *string          `json:"source,omitempty"`
	Link     *string          `json:"link,omitempty"`
	Comments *[]model.Comment `json:"comments,omitempty"`
	Author   string           `json:"author,omitempty"`
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
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	return s.Store.ListIssues(org)
}

func (s *Service) GetIssue(p *model.Principal, org, id string) (*model.Issue, error) {
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	i, err := s.Store.GetIssue(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "找不到 Issue %s", id)
	}
	return i, err
}

func (s *Service) CreateIssue(p *model.Principal, org string, in IssueInput) (*model.Issue, error) {
	if _, err := s.orgRole(p, org); err != nil {
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
	now := s.mt()
	i := model.Issue{Title: in.Title, Body: in.Body, Status: "open", Priority: in.Priority, Labels: model.CleanLabels(in.Labels),
		Source: in.Source, Link: in.Link, CreatedBy: author(p, in.Author), CreatedAt: now, UpdatedAt: now, Comments: []model.Comment{}}
	saved, err := s.Store.CreateIssue(org, i)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "issue.create", org, map[string]any{"id": saved.ID})
	return &saved, nil
}

func (s *Service) UpdateIssue(p *model.Principal, org, id string, in IssuePatch) (*model.Issue, error) {
	if _, err := s.orgRole(p, org); err != nil {
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
	cur, err := s.Store.GetIssue(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "找不到 Issue %s", id)
	}
	if err != nil {
		return nil, err
	}
	n := *cur
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
	if in.Labels != nil {
		n.Labels = model.CleanLabels(*in.Labels)
	}
	if in.Comments != nil {
		// 既有留言的作者照伺服器上的(不能改別人的留言作者);新留言:不是伺服器管理員就一律是自己
		old := map[string]model.Comment{}
		for _, c := range cur.Comments {
			old[c.ID] = c
		}
		cs := []model.Comment{}
		for _, c := range *in.Comments {
			if c.ID == "" {
				c.ID = uuid()
			}
			if c.At.IsZero() {
				c.At = s.mt()
			}
			if o, ok := old[c.ID]; ok {
				c.Author = o.Author
			} else if !p.ServerAdmin() || strings.TrimSpace(c.Author) == "" {
				c.Author = author(p, c.Author)
			}
			cs = append(cs, c)
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
	if same(n, *cur) {
		return &n, nil
	}
	n.UpdatedAt = s.mt()
	if err := s.Store.UpdateIssue(org, n); err != nil {
		return nil, err
	}
	s.Audit(p, "issue.update", org, map[string]any{"id": n.ID})
	return &n, nil
}

func same(a, b model.Issue) bool {
	if a.Title != b.Title || a.Body != b.Body || a.Status != b.Status || a.Priority != b.Priority || a.Source != b.Source ||
		a.Link != b.Link || strings.Join(a.Labels, "\x00") != strings.Join(b.Labels, "\x00") || len(a.Comments) != len(b.Comments) {
		return false
	}
	for k := range a.Comments {
		x, y := a.Comments[k], b.Comments[k]
		if x.ID != y.ID || x.Author != y.Author || x.Text != y.Text || !x.At.Equal(y.At.Time) {
			return false
		}
	}
	return true
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
