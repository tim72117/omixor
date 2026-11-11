package core

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Issue:同 Swift 版 Issues/Issues.swift。真實資料,存在目前組織資料夾的 issues.json。
// 作者字串:person id、"mcp" 或 "agent:<角色 id>"。

const (
	MCPAuthor   = "mcp"
	AgentPrefix = "agent:"
)

// IssuePriorities 依嚴重程度排序(同 Swift 的 IssuePriority.allCases)
var IssuePriorities = []string{"urgent", "high", "normal", "low"}

var IssueStatuses = []string{"open", "closed"}

// PriorityRank urgent=0 … low=3;不認得的當 0(同 Swift 的 firstIndex ?? 0)
func PriorityRank(p string) int {
	for i, v := range IssuePriorities {
		if v == p {
			return i
		}
	}
	return 0
}

func iaOneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

type IssueComment struct {
	At     Time   `json:"at"`
	Author string `json:"author"`
	ID     string `json:"id"`
	Text   string `json:"text"`
}

func (c *IssueComment) UnmarshalJSON(b []byte) error {
	type raw IssueComment
	r := raw{At: nowPrecise()}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if r.ID == "" {
		r.ID = UUID()
	}
	*c = IssueComment(r)
	return nil
}

type OpsIssue struct {
	Body      string         `json:"body"`
	ClosedAt  *Time          `json:"closedAt,omitempty"`
	Comments  []IssueComment `json:"comments"`
	CreatedAt Time           `json:"createdAt"`
	CreatedBy string         `json:"createdBy"`
	ID        string         `json:"id"`
	Labels    []string       `json:"labels"`
	Link      string         `json:"link"`
	Priority  string         `json:"priority"`
	Source    string         `json:"source"`
	Status    string         `json:"status"`
	Title     string         `json:"title"`
	UpdatedAt Time           `json:"updatedAt"`
}

// UnmarshalJSON 寬鬆解碼:缺欄位用預設值
func (i *OpsIssue) UnmarshalJSON(b []byte) error {
	type raw OpsIssue
	now := nowPrecise()
	r := raw{Status: "open", Priority: "normal", CreatedAt: now, UpdatedAt: now, CreatedBy: MCPAuthor}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if !iaOneOf(r.Status, IssueStatuses) {
		r.Status = "open"
	}
	if !iaOneOf(r.Priority, IssuePriorities) {
		r.Priority = "normal"
	}
	if r.Labels == nil {
		r.Labels = []string{}
	}
	if r.Comments == nil {
		r.Comments = []IssueComment{}
	}
	*i = OpsIssue(r)
	return nil
}

// Number ISS-12 → 12
func (i OpsIssue) Number() int { return iaIDNumber(i.ID) }

func iaIDNumber(id string) int {
	var b strings.Builder
	for _, r := range id {
		if unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	n, err := strconv.Atoi(b.String())
	if err != nil {
		return 0
	}
	return n
}

// nowPrecise 現在時間(保留小數秒,排序用;存檔與輸出時只到秒,同 Swift)
func nowPrecise() Time { return Time{time.Now().UTC()} }

type issueState struct {
	Issues []OpsIssue
}

func (a *App) issueSt() *issueState {
	if s, ok := a.Ext["issues"].(*issueState); ok {
		return s
	}
	s := &issueState{}
	a.Ext["issues"] = s
	a.loadIssues()
	return s
}

// Issues 全部 Issue(最新的在前;呼叫時要拿著 Mu)
func (a *App) Issues() []OpsIssue { return a.issueSt().Issues }

func (a *App) loadIssues() {
	s := a.issueSt()
	var list []OpsIssue
	if err := ReadJSON(filepath.Join(a.DataDir(), "issues.json"), &list); err != nil || list == nil {
		list = []OpsIssue{}
	}
	s.Issues = list
}

func (a *App) saveIssues() {
	_ = WriteJSON(filepath.Join(a.DataDir(), "issues.json"), a.issueSt().Issues)
}

// Issue 依 id 找(不分大小寫)
func (a *App) Issue(id string) *OpsIssue {
	for i := range a.issueSt().Issues {
		if strings.EqualFold(a.issueSt().Issues[i].ID, id) {
			c := a.issueSt().Issues[i]
			return &c
		}
	}
	return nil
}

// AuthorActor 作者字串 → Actor(同 Swift 的 IssueStore.actor)
func AuthorActor(author string) Actor {
	if author == MCPAuthor {
		return MCPActor
	}
	if strings.HasPrefix(author, AgentPrefix) {
		return AI(strings.TrimPrefix(author, AgentPrefix))
	}
	return Person(author)
}

// ActorAuthor Actor → 作者字串(MCP 呼叫是 "mcp",畫面呼叫是使用者 id)
func ActorAuthor(actor Actor) string {
	if actor.Kind == "person" {
		return actor.ID
	}
	if actor.Kind == "ai" && actor.ID != MCPActorID && actor.ID != "" {
		return AgentPrefix + actor.ID
	}
	return MCPAuthor
}

// IssueAuthorName 同 Swift 的 IssueStore.authorName
func (a *App) IssueAuthorName(author string) string { return a.Name(AuthorActor(author)) }

// CleanLabels 去頭尾空白、去空、不分大小寫去重(同 Swift 的 IssueStore.clean)
func CleanLabels(labels []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range labels {
		t := strings.TrimFunc(l, func(r rune) bool { return r == '\t' || unicode.Is(unicode.Zs, r) })
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// CreateIssue 同 Swift 的 IssueStore.create(呼叫時要拿著 Mu)
func (a *App) CreateIssue(title, body, priority string, labels []string, source, link, author string) OpsIssue {
	s := a.issueSt()
	num := 0
	for _, i := range s.Issues {
		num = max(num, i.Number())
	}
	now := nowPrecise()
	if priority == "" {
		priority = "normal"
	}
	issue := OpsIssue{
		ID: "ISS-" + strconv.Itoa(num+1), Title: title, Body: body, Status: "open", Priority: priority,
		Labels: CleanLabels(labels), Source: source, Link: link, CreatedBy: author, CreatedAt: now, UpdatedAt: now,
		Comments: []IssueComment{},
	}
	s.Issues = append([]OpsIssue{issue}, s.Issues...)
	a.saveIssues()
	a.Log(AuthorActor(author), "exclamationmark.bubble", "建立 "+issue.ID+"「"+title+"」", "development")
	go a.Changed("issues")
	return issue
}

func cloneIssue(i OpsIssue) OpsIssue {
	i.Labels = append([]string{}, i.Labels...)
	i.Comments = append([]IssueComment{}, i.Comments...)
	return i
}

// UpdateIssue 修改一筆,回傳修改後的內容;狀態變了記到動態牆(同 Swift 的 IssueStore.update;呼叫時要拿著 Mu)
func (a *App) UpdateIssue(id, author string, change func(*OpsIssue)) *OpsIssue {
	s := a.issueSt()
	idx := -1
	for i := range s.Issues {
		if strings.EqualFold(s.Issues[i].ID, id) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	before := s.Issues[idx]
	n := cloneIssue(before)
	change(&n)
	n.Labels = CleanLabels(n.Labels)
	if n.Status != before.Status {
		if n.Status == "closed" {
			t := nowPrecise()
			n.ClosedAt = &t
			a.Log(AuthorActor(author), "checkmark.circle", "關閉 "+n.ID+"「"+n.Title+"」", "development")
		} else {
			n.ClosedAt = nil
			a.Log(AuthorActor(author), "arrow.uturn.backward.circle", "重新打開 "+n.ID+"「"+n.Title+"」", "development")
		}
	}
	if string(Marshal(n)) == string(Marshal(before)) {
		return &n
	}
	n.UpdatedAt = nowPrecise()
	s.Issues[idx] = n
	a.saveIssues()
	go a.Changed("issues")
	return &n
}

// CommentIssue 加留言;空白留言不做事(同 Swift 的 IssueStore.comment)
func (a *App) CommentIssue(id, text, author string) *OpsIssue {
	t := strings.TrimSpace(text)
	if t == "" {
		return a.Issue(id)
	}
	return a.UpdateIssue(id, author, func(i *OpsIssue) {
		i.Comments = append(i.Comments, IssueComment{ID: UUID(), Author: author, Text: t, At: nowPrecise()})
	})
}

// DeleteIssue 刪除(id 完全相同才刪,同 Swift)
func (a *App) DeleteIssue(id string) {
	s := a.issueSt()
	kept := []OpsIssue{}
	for _, i := range s.Issues {
		if i.ID != id {
			kept = append(kept, i)
		}
	}
	s.Issues = kept
	a.saveIssues()
	go a.Changed("issues")
}

func init() {
	OnOrgLoad(func(a *App) {
		a.issueSt()
		a.loadIssues()
	})
	RegisterView("issues", func(a *App) any { return a.Issues() })
	RegisterAction("issue_delete", func(a *App, args map[string]any) (any, error) {
		id, _ := args["id"].(string)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		a.DeleteIssue(id)
		return nil, nil
	})
	OnBadge(func(a *App) map[string]int {
		n := 0
		for _, i := range a.Issues() {
			if i.Status == "open" && PriorityRank(i.Priority) <= 1 {
				n++
			}
		}
		return map[string]int{"development": n}
	})
}
