package mcp

import (
	"slices"
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

// IssueAppendBodySupporter 不是核心自己的 Backend 實作,但認得 IssuePatch.AppendBody 時實作這個介面宣告
// (例如 omixor-web 的 appBackend:交給核心在 Issue 的鎖裡附加)
type IssueAppendBodySupporter interface{ SupportsIssueAppendBody() bool }

// atomicIssueBackend 核心自己的實作(Local、Remote)與宣告了 IssueAppendBodySupporter 的實作支援 IssuePatch.AppendBody
// (在 Issue 的鎖裡以最新內文附加);其他實作不一定認得,照舊由這裡算好整段 body 送出
func atomicIssueBackend(bk backend.Backend) bool {
	switch x := bk.(type) {
	case *backend.Local, *backend.Remote:
		return true
	case IssueAppendBodySupporter:
		return x.SupportsIssueAppendBody()
	}
	return false
}

// addIssueComment 新增一則留言:走 Backend.AddIssueComment(只附加這一則);實作還沒有這個操作(backend.Unsupported)時
// 才退回舊的做法(讀整份 → 加一則 → 整份送回)
func addIssueComment(bk backend.Backend, org, id, text, author string) (*model.Issue, error) {
	updated, err := bk.AddIssueComment(org, id, text, author)
	if !backend.IsUnsupported(err) {
		return updated, err
	}
	cur, err := bk.GetIssue(org, id)
	if err != nil {
		return nil, err
	}
	comments := append(slices.Clone(cur.Comments), model.Comment{ID: uuid(), Author: author, Text: text, At: model.Time{Time: time.Now().UTC()}})
	return bk.UpdateIssue(org, id, service.IssuePatch{Comments: &comments, Author: author})
}

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
			Labels: labels, Source: c.Args.Str("source"), Link: c.Args.Str("link"), Repo: c.Args.Str("repo"), Author: author, Assignee: c.Args.Str("assignee"),
			Project: c.Args.Opt("project")}) // 沒給 = 依 repo 的專案連結預設;空字串 = 不分專案
		if err != nil { // Issue 伺服器連不上(同 Swift)
			return nil, prefixErr("Issue 沒有存起來:", err)
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
			return nil, prefixErr("Issue 沒有改成:", err)
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
			return nil, ToolError{Message: "標題不能是空的"}
		}

		// 變更的順序同 Swift 版:body → append_body、labels → add_labels
		patch := service.IssuePatch{Author: author}
		if title != nil && *title != cur.Title {
			patch.Title = title
		}
		body := cur.Body
		newBody, hasBody := c.Args.RawString("body")
		if hasBody {
			body = newBody
		}
		appendBody, _ := c.Args.RawString("append_body")
		if appendBody != "" && !hasBody && atomicIssueBackend(bk) {
			// 只附加:交給核心在 Issue 的鎖裡以最新內文附加(不用這裡先讀到的,才不會蓋掉同時進來的修改)
			patch.AppendBody = &appendBody
		} else {
			if appendBody != "" {
				if body != "" {
					body += "\n\n"
				}
				body += appendBody
			}
			if body != cur.Body {
				patch.Body = &body
			}
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
		if rp := c.Args.Opt("repo"); rp != nil && strings.TrimSpace(*rp) != cur.Repo { // 所屬倉庫(空字串 = 清除)
			patch.Repo = rp
		}
		if as := c.Args.Opt("assignee"); as != nil && *as != cur.Assignee { // 指派給角色(空字串 = 取消指派)
			patch.Assignee = as
		}
		if pr := c.Args.Opt("project"); pr != nil && !strings.EqualFold(strings.TrimSpace(*pr), cur.Project) { // 搬專案(空字串 = 組織層級)
			patch.Project = pr
		}
		if v, ok := c.Args.Int("version"); ok { // 有給就檢查版本,衝突回錯誤
			patch.Version = &v
		}
		updated, err := bk.UpdateIssue(org, id, patch)
		if err != nil {
			return nil, prefixErr("Issue 沒有改成:", err)
		}
		if cm := c.Args.Opt("comment"); cm != nil {
			if t := strings.TrimSpace(*cm); t != "" { // 空白留言不做事
				updated, err = addIssueComment(bk, org, id, t, author)
				if err != nil {
					return nil, prefixErr("Issue 沒有改成:", err)
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
				return nil, terr(err)
			}
			return issueJSON(e, *i), nil
		}
		status := "open"
		if s := c.Args.Opt("status"); s != nil {
			status = *s
		}
		if status != "open" && status != "closed" && status != "all" {
			return nil, ToolError{Message: "status 必須是 open、closed、all 之一"}
		}
		all, err := bk.ListIssues(org)
		if err != nil {
			return nil, terr(err)
		}
		// 篩選、排序規則同 GET …/issues(model.QueryIssues):query 搜尋標題、內文、留言、編號、標籤,空白分隔的每個字都要有
		q := model.IssueQuery{Q: c.Args.Str("query"), Status: status, Repo: c.Args.Str("repo"), Project: c.Args.Str("project"), Sort: model.IssueSortPriority}
		if label := c.Args.Opt("label"); label != nil {
			q.Label = *label
		}
		if q.Repo != "" && q.Repo != model.IssueFilterNone { // 篩 repo 時帶入專案連結:連到該 repo 的專案裡沒標 repo 的 Issue 也算
			if ps, err := bk.ListProjects(org); err == nil {
				q.RepoProjects = model.ProjectsLinkedToRepo(ps, q.Repo)
			}
		}
		list := model.QueryIssues(all, q)
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
				"key": i.Display(), "id": i.ID, "project": i.Project, "title": i.Title, "status": i.Status, "priority": i.Priority,
				"labels": i.Labels, "source": i.Source, "comments": len(i.Comments), "updatedAt": iso(i.UpdatedAt.Time),
			}
			if i.Assignee != "" {
				row["assignee"] = i.Assignee
				putActor(row, "assigneeActor", i.AssigneeActor)
			}
			if i.Repo != "" {
				row["repo"] = i.Repo
			}
			out = append(out, row)
		}
		return map[string]any{"total": len(list), "issues": out}, nil
	})
}
