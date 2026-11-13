package mcp

import (
	"slices"
	"sort"
	"strings"
	"time"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// Issue 工具:同 Swift 版 MCPHandler 的 issueCreate / issueUpdate / issuesQuery

// IssuePriorities 依嚴重程度排序(同 Swift 的 IssuePriority.allCases)
var IssuePriorities = []string{"urgent", "high", "normal", "low"}

var IssueStatuses = []string{"open", "closed"}

// PriorityRank 優先度排序用的名次(不認得的排最後)
func PriorityRank(p string) int {
	for i, v := range IssuePriorities {
		if v == p {
			return i
		}
	}
	return len(IssuePriorities)
}

// iso 同 Swift 的 MCPJSON.iso:UTC、到秒
func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z07:00") }

// RegisterIssueTools 登記 Issue 工具:issue_create、issue_update、issues
func RegisterIssueTools(s *Server) {
	s.Register("issue_create", func(c *Call) (any, error) {
		e := env(c)
		e.Lock()
		defer e.Unlock()
		author, err := e.Author(c.Args.Str("agent"))
		if err != nil {
			return nil, err
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		body, _ := c.Args.RawString("body")
		priority, err := c.Args.OptionalEnum("priority", IssuePriorities)
		if err != nil {
			return nil, err
		}
		labels, _ := c.Args.Strings("labels")
		issue, err := e.Backend().CreateIssue(e.Org(), service.IssueInput{Title: title, Body: body, Priority: priority,
			Labels: labels, Source: c.Args.Str("source"), Link: c.Args.Str("link"), Author: author, Assignee: c.Args.Str("assignee")})
		if err != nil { // Issue 伺服器連不上(同 Swift)
			return nil, Errorf("Issue 沒有存起來:%s", err.Error())
		}
		return issueJSON(e, *issue), nil
	})

	s.Register("issue_update", func(c *Call) (any, error) {
		e := env(c)
		e.Lock()
		defer e.Unlock()
		bk, org := e.Backend(), e.Org()
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		cur, err := bk.GetIssue(org, id)
		if err != nil {
			if backend.NotFound(err) {
				return nil, Errorf("找不到 Issue %s", id)
			}
			return nil, Errorf("Issue 沒有改成:%s", err.Error())
		}
		author, err := e.Author(c.Args.Str("agent"))
		if err != nil {
			return nil, err
		}
		priority, err := c.Args.OptionalEnum("priority", IssuePriorities)
		if err != nil {
			return nil, err
		}
		status, err := c.Args.OptionalEnum("status", IssueStatuses)
		if err != nil {
			return nil, err
		}
		title := c.Args.Opt("title")
		if title != nil && *title == "" {
			return nil, ToolError{"標題不能是空的"}
		}

		// 變更的順序同 Swift 版:body → append_body、labels → add_labels
		patch := service.IssuePatch{Author: author}
		if title != nil && *title != cur.Title {
			patch.Title = title
		}
		body := cur.Body
		if b, ok := c.Args.RawString("body"); ok {
			body = b
		}
		if b, ok := c.Args.RawString("append_body"); ok && b != "" {
			if body != "" {
				body += "\n\n"
			}
			body += b
		}
		if body != cur.Body {
			patch.Body = &body
		}
		if priority != "" && priority != cur.Priority {
			patch.Priority = &priority
		}
		labels := slices.Clone(cur.Labels)
		if l, ok := c.Args.Strings("labels"); ok {
			labels = l
		}
		if l, ok := c.Args.Strings("add_labels"); ok {
			labels = append(labels, l...)
		}
		if !slices.Equal(labels, cur.Labels) {
			patch.Labels = &labels
		}
		if status != "" && status != cur.Status {
			patch.Status = &status
		}
		if as := c.Args.Opt("assignee"); as != nil && *as != cur.Assignee { // 指派給角色(空字串 = 取消指派)
			patch.Assignee = as
		}
		updated, err := bk.UpdateIssue(org, id, patch)
		if err != nil {
			return nil, Errorf("Issue 沒有改成:%s", err.Error())
		}
		if cm := c.Args.Opt("comment"); cm != nil {
			if t := strings.TrimSpace(*cm); t != "" { // 空白留言不做事
				comments := append(slices.Clone(updated.Comments),
					model.Comment{ID: uuid(), Author: author, Text: t, At: model.Time{Time: time.Now().UTC()}})
				updated, err = bk.UpdateIssue(org, id, service.IssuePatch{Comments: &comments, Author: author})
				if err != nil {
					return nil, Errorf("Issue 沒有改成:%s", err.Error())
				}
			}
		}
		return issueJSON(e, *updated), nil
	})

	s.Register("issues", func(c *Call) (any, error) {
		e := env(c)
		e.Lock()
		defer e.Unlock()
		bk, org := e.Backend(), e.Org()
		if id := c.Args.Opt("id"); id != nil {
			i, err := bk.GetIssue(org, *id)
			if err != nil {
				if backend.NotFound(err) {
					return nil, Errorf("找不到 Issue %s", *id)
				}
				return nil, ToolError{err.Error()}
			}
			return issueJSON(e, *i), nil
		}
		status := "open"
		if s := c.Args.Opt("status"); s != nil {
			status = *s
		}
		if status != "open" && status != "closed" && status != "all" {
			return nil, ToolError{"status 必須是 open、closed、all 之一"}
		}
		all, err := bk.ListIssues(org)
		if err != nil {
			return nil, ToolError{err.Error()}
		}
		label := c.Args.Opt("label")
		q := strings.ToLower(c.Args.Str("query"))
		list := []model.Issue{}
		for _, i := range all {
			if status != "all" && i.Status != status {
				continue
			}
			if label != nil {
				found := false
				for _, l := range i.Labels {
					found = found || strings.EqualFold(l, *label)
				}
				if !found {
					continue
				}
			}
			if q != "" {
				hit := strings.Contains(strings.ToLower(i.Title), q) || strings.Contains(strings.ToLower(i.Body), q)
				for _, cm := range i.Comments {
					hit = hit || strings.Contains(strings.ToLower(cm.Text), q)
				}
				if !hit {
					continue
				}
			}
			list = append(list, i)
		}
		sort.SliceStable(list, func(x, y int) bool {
			px, py := PriorityRank(list[x].Priority), PriorityRank(list[y].Priority)
			if px != py {
				return px < py
			}
			return list[y].UpdatedAt.Before(list[x].UpdatedAt.Time)
		})
		limit := 30
		if n, ok := c.Args.Int("limit"); ok {
			limit = n
		}
		limit = max(1, limit)
		out := []any{}
		for k, i := range list {
			if k >= limit {
				break
			}
			row := map[string]any{
				"id": i.ID, "title": i.Title, "status": i.Status, "priority": i.Priority,
				"labels": i.Labels, "source": i.Source, "comments": len(i.Comments), "updatedAt": iso(i.UpdatedAt.Time),
			}
			if i.Assignee != "" {
				row["assignee"] = i.Assignee
			}
			out = append(out, row)
		}
		return map[string]any{"total": len(list), "issues": out}, nil
	})
}
