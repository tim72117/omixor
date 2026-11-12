package mcp

import (
	"sort"
	"strings"

	"omixor-web/internal/core"
)

// Issue 工具:同 Swift 版 MCPHandler 的 issueCreate / issueUpdate / issuesQuery

// toolErr 把 core.UserError 轉成工具錯誤
func userToolErr(err error) error {
	if ue, ok := err.(core.UserError); ok {
		return ToolError{ue.Message}
	}
	return err
}

// optString 有給(就算是空字串)回傳指標,沒給回傳 nil(同 Swift 的 a.string(k) 是否為 nil)
func (a Args) iaOpt(k string) *string {
	if s, ok := a.String(k); ok {
		return &s
	}
	return nil
}

// rawString 原樣的字串(不去空白;不是字串就當沒給),同 Swift 的 a.raw[k] as? String
func (a Args) iaRaw(k string) (string, bool) {
	s, ok := a.Raw[k].(string)
	return s, ok
}

// issueAuthor Issue 的署名:有給 agent 就是該角色,否則是呼叫者
func issueAuthor(c *Call) (string, error) {
	name := c.Args.Str("agent")
	if name == "" {
		return core.ActorAuthor(c.Actor), nil
	}
	g := c.App.FindAgent(name)
	if g == nil {
		return "", Errorf("找不到角色 %s", name)
	}
	return core.AgentPrefix + g.ID, nil
}

func issueJSON(a *core.App, i core.OpsIssue) any {
	d, _ := core.Object(i).(map[string]any)
	d["createdBy"] = a.IssueAuthorName(i.CreatedBy)
	comments := []any{}
	for _, c := range i.Comments {
		comments = append(comments, map[string]any{"author": a.IssueAuthorName(c.Author), "text": c.Text, "at": core.ISO(c.At.Time)})
	}
	d["comments"] = comments
	return d
}

func init() {
	Register("ops_issue_create", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		author, err := issueAuthor(c)
		if err != nil {
			return nil, err
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		body, _ := c.Args.iaRaw("body")
		priority, err := c.Args.OptionalEnum("priority", core.IssuePriorities)
		if err != nil {
			return nil, err
		}
		labels, _ := c.Args.Strings("labels")
		issue := a.CreateIssue(title, body, priority, labels, c.Args.Str("source"), c.Args.Str("link"), author)
		if issue.ID == "" { // Issue 伺服器連不上(同 Swift)
			return nil, Errorf("Issue 沒有存起來:%s", a.IssueRemoteError())
		}
		return issueJSON(a, issue), nil
	})

	Register("ops_issue_update", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		a.RefreshIssuesIfStale(0)
		if a.Issue(id) == nil {
			return nil, Errorf("找不到 Issue %s", id)
		}
		author, err := issueAuthor(c)
		if err != nil {
			return nil, err
		}
		priority, err := c.Args.OptionalEnum("priority", core.IssuePriorities)
		if err != nil {
			return nil, err
		}
		status, err := c.Args.OptionalEnum("status", core.IssueStatuses)
		if err != nil {
			return nil, err
		}
		title := c.Args.iaOpt("title")
		if title != nil && *title == "" {
			return nil, ToolError{"標題不能是空的"}
		}
		updated := a.UpdateIssue(id, author, func(i *core.OpsIssue) {
			if title != nil {
				i.Title = *title
			}
			if b, ok := c.Args.iaRaw("body"); ok {
				i.Body = b
			}
			if b, ok := c.Args.iaRaw("append_body"); ok && b != "" {
				if i.Body != "" {
					i.Body += "\n\n"
				}
				i.Body += b
			}
			if priority != "" {
				i.Priority = priority
			}
			if l, ok := c.Args.Strings("labels"); ok {
				i.Labels = l
			}
			if l, ok := c.Args.Strings("add_labels"); ok {
				i.Labels = append(i.Labels, l...)
			}
			if status != "" {
				i.Status = status
			}
		})
		if cm := c.Args.iaOpt("comment"); cm != nil {
			updated = a.CommentIssue(id, *cm, author)
		}
		if updated == nil {
			if e := a.IssueRemoteError(); e != "" {
				return nil, Errorf("Issue 沒有改成:%s", e)
			}
			return map[string]any{}, nil
		}
		return issueJSON(a, *updated), nil
	})

	Register("ops_issues", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		a.RefreshIssuesIfStale(0) // Issue 伺服器模式:用最新的
		if id := c.Args.iaOpt("id"); id != nil {
			i := a.Issue(*id)
			if i == nil {
				return nil, Errorf("找不到 Issue %s", *id)
			}
			return issueJSON(a, *i), nil
		}
		status := "open"
		if s := c.Args.iaOpt("status"); s != nil {
			status = *s
		}
		if status != "open" && status != "closed" && status != "all" {
			return nil, ToolError{"status 必須是 open、closed、all 之一"}
		}
		label := c.Args.iaOpt("label")
		q := strings.ToLower(c.Args.Str("query"))
		list := []core.OpsIssue{}
		for _, i := range a.Issues() {
			if status != "all" && i.Status != status {
				continue
			}
			if label != nil {
				found := false
				for _, l := range i.Labels {
					found = found || strings.ToLower(l) == strings.ToLower(*label)
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
			px, py := core.PriorityRank(list[x].Priority), core.PriorityRank(list[y].Priority)
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
			out = append(out, map[string]any{
				"id": i.ID, "title": i.Title, "status": i.Status, "priority": i.Priority,
				"labels": i.Labels, "source": i.Source, "comments": len(i.Comments), "updatedAt": core.ISO(i.UpdatedAt.Time),
			})
		}
		return map[string]any{"total": len(list), "issues": out}, nil
	})
}
