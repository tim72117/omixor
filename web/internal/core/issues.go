package core

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Issue:同 Swift 版 Issues/Issues.swift。真實資料,存在目前組織資料夾的 issues.json;
// 設了 OPS_DATABASE_URL 時改存資料庫的 ops_issues / ops_issue_comments 表(store_pg_issues.go)。
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
	// Assignee 指派給的虛擬角色 id(空 = 未指派);指派時核心會自動替該角色排任務
	Assignee  string         `json:"assignee,omitempty"`
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
	// Org 這份清單屬於哪個組織
	Org string
	// quiet 不是目前組織(透過 /api/issues/<組織> 操作別的組織):不記動態牆、不通知畫面
	quiet bool
	// saved 資料庫存放時,上次存進去的內容(id → JSON|順序),只寫有變的列
	saved map[string]string
	// Issue 伺服器模式(issues_remote.go):最近一次讀取的時間與錯誤
	fetched   time.Time
	remoteErr string
	// gen 清單每變一次加一:背景讀取期間清單變過(新增、修改、同步讀取),讀回來的舊結果就丟掉
	gen int
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

// ReloadIssues 從存放處重讀目前組織的 Issue(呼叫時要拿著 Mu)
func (a *App) ReloadIssues() { a.loadIssues() }

func (a *App) loadIssues() {
	s := a.issueSt()
	s.Org = a.CurrentOrgID()
	if IsRemote() {
		// 遠端模式:不在拿著鎖時連網(伺服器連不上會卡住畫面),先清空,在背景讀回來再通知畫面
		s.Issues, s.fetched = []OpsIssue{}, time.Time{}
		s.gen++
		go a.issueRemoteSync(s.Org, a.conns().Scope, s.gen)
		return
	}
	a.issueRemoteLoad(s) // 本機模式:核心在同一個程式裡(連本機 PostgreSQL),直接讀
}

// changedIssues 通知畫面(別的組織不用)
func (a *App) changedIssues() {
	if !a.issueSt().quiet {
		go a.Changed("issues")
	}
}

// logIssue 記到動態牆(別的組織不記:動態牆屬於目前組織)
func (a *App) logIssue(actor Actor, icon, text string) {
	if !a.issueSt().quiet {
		a.Log(actor, icon, text, "development")
	}
}

// ValidOrgID 組織 id 只能是英數字與 -(同 NewOrgID 產生的格式)
func ValidOrgID(id string) bool {
	if id == "" || len(id) > 100 {
		return false
	}
	for _, r := range id {
		if !(r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_')) {
			return false
		}
	}
	return true
}

// WithOrgIssues 在指定組織的 Issue 清單上執行 f(呼叫時要拿著 Mu)。org 是空的或目前組織時就是平常的清單;
// 其他組織從存放處讀一份來改、改完存回去,不記動態牆也不通知畫面。給 /api/issues/<組織>(Swift 版遠端存放)用。
func (a *App) WithOrgIssues(org string, f func()) {
	if org == "" || org == a.CurrentOrgID() {
		a.issueSt()
		f()
		return
	}
	cur := a.Ext["issues"]
	s := &issueState{Org: org, quiet: true}
	a.Ext["issues"] = s
	a.issueRemoteLoad(s)
	defer func() { a.Ext["issues"] = cur }()
	f()
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
	return a.CreateIssueFor(title, body, priority, labels, source, link, author, "")
}

// CreateIssueFor 同 CreateIssue,多一個指派的角色 id(空 = 不指派;有給核心會自動替該角色排任務)
func (a *App) CreateIssueFor(title, body, priority string, labels []string, source, link, author, assignee string) OpsIssue {
	s := a.issueSt()
	issue, err := a.issueRemoteCreate(title, body, priority, CleanLabels(labels), source, link, author, assignee)
	if err != nil {
		// 核心存不進去(伺服器連不上、沒有資料庫…):回傳沒有存起來的草稿(id 是空的),同 Swift
		a.issueRemoteFailed(err)
		return OpsIssue{Title: title, Body: body, Priority: priority, Labels: CleanLabels(labels), Status: "open", CreatedBy: author, Comments: []IssueComment{}}
	}
	s.remoteErr = ""
	s.gen++
	kept := []OpsIssue{issue}
	for _, i := range s.Issues {
		if i.ID != issue.ID {
			kept = append(kept, i)
		}
	}
	s.Issues = kept
	a.logIssue(AuthorActor(author), "exclamationmark.bubble", "建立 "+issue.ID+"「"+title+"」")
	a.changedIssues()
	if issue.Assignee != "" {
		go a.AgentsChanged() // 核心替角色排了任務
	}
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
	a.issueRemoteLoad(s) // 用核心裡最新的內容來改
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
	statusChanged := n.Status != before.Status
	if statusChanged {
		if n.Status == "closed" {
			t := nowPrecise()
			n.ClosedAt = &t
		} else {
			n.ClosedAt = nil
		}
	}
	// 狀態變了記到動態牆:存成功之後才記(伺服器模式存失敗時不能留下「關閉了」的紀錄)
	logStatus := func(i OpsIssue) {
		if !statusChanged {
			return
		}
		if i.Status == "closed" {
			a.logIssue(AuthorActor(author), "checkmark.circle", "關閉 "+i.ID+"「"+i.Title+"」")
		} else {
			a.logIssue(AuthorActor(author), "arrow.uturn.backward.circle", "重新打開 "+i.ID+"「"+i.Title+"」")
		}
	}
	if string(Marshal(n)) == string(Marshal(before)) {
		return &n
	}
	saved, err := a.issueRemoteUpdate(n, before, author)
	if err != nil {
		a.issueRemoteFailed(err)
		return nil
	}
	s.remoteErr = ""
	s.gen++
	s.Issues[idx] = saved
	logStatus(saved)
	a.changedIssues()
	if saved.Assignee != before.Assignee {
		go a.AgentsChanged() // 核心取消舊角色未領取的任務、替新角色排任務
	}
	return &saved
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
	if err := a.issueRemoteDelete(id); err != nil {
		a.issueRemoteFailed(err)
		return
	}
	s.remoteErr = ""
	s.gen++
	kept := []OpsIssue{}
	for _, i := range s.Issues {
		if i.ID != id {
			kept = append(kept, i)
		}
	}
	s.Issues = kept
	a.changedIssues()
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
