package model

import (
	"net/url"
	"slices"
	"sort"
	"strings"
)

// Issue 的篩選、排序、搜尋與看板分欄規則。只在這裡寫一份:core API(GET …/issues 的 query 參數、GET …/issues/board)、
// MCP 的 issues 工具、omixor-web 的畫面都用這些函式;macOS App、iOS、web 前端只顯示 API 回傳的結果。

// IssueFilterNone 篩選 Repo 或 Assignee 時代表「沒有標記/沒有指派」的值。
// 含括號,model.CleanRepo 不接受、角色 id 也不會是這個,所以不會和真的值撞到
const IssueFilterNone = "(none)"

// Issue 列表的排序方式(IssueQuery.Sort)
const (
	// IssueSortNone 不排序:保留存放處的順序(新的在前),同舊的 GET …/issues
	IssueSortNone = ""
	// IssueSortPriority 優先度(urgent → low)→ 最近更新 → 編號(看板每欄的排序)
	IssueSortPriority = "priority"
	// IssueSortStatus 未解決的在前,再依 IssueSortPriority(Issue 分頁的列表)
	IssueSortStatus = "status"
	// IssueSortUpdated 最近更新的在前 → 編號
	IssueSortUpdated = "updated"
)

// IssueSorts 認得的排序方式(不含空字串)
var IssueSorts = []string{IssueSortPriority, IssueSortStatus, IssueSortUpdated}

// IssueQuery Issue 的篩選與排序條件;零值 = 全部、不排序(同舊的 GET …/issues)
type IssueQuery struct {
	// Q 搜尋:標題、內文、留言、編號、標籤;以空白分隔的每個字都要出現(AND),不分大小寫
	Q string `json:"q,omitempty"`
	// Status open、closed;"" 或 all = 全部
	Status string `json:"status,omitempty"`
	// Priority urgent、high、normal、low;"" = 全部
	Priority string `json:"priority,omitempty"`
	// Label 有這個標籤的(不分大小寫、完全相同);"" = 全部
	Label string `json:"label,omitempty"`
	// Repo 標記這個倉庫的(規則見 RepoMatch);IssueFilterNone = 沒標記倉庫的;"" = 全部
	Repo string `json:"repo,omitempty"`
	// RepoProjects 連到 Repo 的專案 id(由 service 依專案與 repo 的連結填,不是 URL 參數):篩 Repo 時,屬於這些專案、
	// 而且沒標記倉庫的 Issue 也算符合(專案已經連到這個 repo,沒標 repo 的 Issue 視為同一個倉庫)。空 = 只看 Issue 自己的 Repo
	RepoProjects []string `json:"-"`
	// Assignee 指派給這個角色 id 的(不分大小寫);IssueFilterNone = 沒指派的;"" = 全部
	Assignee string `json:"assignee,omitempty"`
	// Project 所屬專案:"" = 全部;IssueProjectNone("-",也接受 IssueFilterNone)= 只看組織層級的;PRJ-<n> = 只看那個專案(不分大小寫)
	Project string `json:"project,omitempty"`
	// Sort 排序方式,見 IssueSorts;"" = 不排序
	Sort string `json:"sort,omitempty"`
	// Deleted true = 列垃圾桶(已刪除的 Issue,組織管理員);false = 一般列表(不含已刪除的)。URL 參數 deleted=1
	Deleted bool `json:"deleted,omitempty"`
}

// IssueProjectNone 篩選 Project 時代表「組織層級(不屬於任何專案)」的值
const IssueProjectNone = "-"

// Validate 檢查 Status、Priority、Sort 是不是認得的值;不對回傳錯誤訊息(空字串 = 沒問題)
func (q IssueQuery) Validate() string {
	if s := strings.TrimSpace(q.Status); s != "" && s != "all" && !OneOf(s, IssueStatuses) {
		return "status 必須是 open、closed、all 之一"
	}
	if p := strings.TrimSpace(q.Priority); p != "" && !OneOf(p, IssuePriorities) {
		return "priority 必須是 " + strings.Join(IssuePriorities, "、") + " 之一"
	}
	if s := strings.TrimSpace(q.Sort); s != "" && !OneOf(s, IssueSorts) {
		return "sort 必須是 " + strings.Join(IssueSorts, "、") + " 之一"
	}
	return ""
}

// issueQueryKeys URL query 參數名稱(同 JSON 欄位名稱)
var issueQueryKeys = []string{"q", "status", "priority", "label", "repo", "assignee", "project", "sort"}

func (q *IssueQuery) field(k string) *string {
	switch k {
	case "q":
		return &q.Q
	case "status":
		return &q.Status
	case "priority":
		return &q.Priority
	case "label":
		return &q.Label
	case "repo":
		return &q.Repo
	case "assignee":
		return &q.Assignee
	case "project":
		return &q.Project
	case "sort":
		return &q.Sort
	}
	return nil
}

// ParseIssueQuery 從 URL query 讀(GET …/issues?q=&status=&priority=&label=&repo=&assignee=&project=&sort=&deleted=1)
func ParseIssueQuery(v url.Values) IssueQuery {
	var q IssueQuery
	for _, k := range issueQueryKeys {
		*q.field(k) = v.Get(k)
	}
	switch strings.ToLower(strings.TrimSpace(v.Get("deleted"))) {
	case "1", "true", "yes":
		q.Deleted = true
	}
	return q
}

// Values 轉成 URL query(空的欄位不放);和 ParseIssueQuery 互為反向
func (q IssueQuery) Values() url.Values {
	v := url.Values{}
	for _, k := range issueQueryKeys {
		if s := *q.field(k); s != "" {
			v.Set(k, s)
		}
	}
	if q.Deleted {
		v.Set("deleted", "1")
	}
	return v
}

// IssueSearchWords 搜尋字串切成要比對的字:以空白分隔、轉小寫,去掉空的
func IssueSearchWords(q string) []string { return strings.Fields(strings.ToLower(q)) }

// IssueMatchesSearch 每個字都出現在標題、內文、留言、編號或某個標籤裡(各欄位分開比對,不分大小寫);沒有字 = 符合
func IssueMatchesSearch(i Issue, words []string) bool {
	if len(words) == 0 {
		return true
	}
	fields := make([]string, 0, 3+len(i.Labels)+len(i.Comments))
	fields = append(fields, strings.ToLower(i.Key), strings.ToLower(i.ID), strings.ToLower(i.Title), strings.ToLower(i.Body))
	for _, l := range i.Labels {
		fields = append(fields, strings.ToLower(l))
	}
	for _, c := range i.Comments {
		fields = append(fields, strings.ToLower(c.Text))
	}
	for _, w := range words {
		if !slices.ContainsFunc(fields, func(f string) bool { return strings.Contains(f, w) }) {
			return false
		}
	}
	return true
}

// IssueMatches 這張 Issue 符不符合 q 的篩選條件(不看 Sort)
func IssueMatches(i Issue, q IssueQuery) bool { return issueMatches(i, q, IssueSearchWords(q.Q)) }

func issueMatches(i Issue, q IssueQuery, words []string) bool {
	if s := strings.TrimSpace(q.Status); s != "" && s != "all" && i.Status != s {
		return false
	}
	if p := strings.TrimSpace(q.Priority); p != "" && i.Priority != p {
		return false
	}
	if l := strings.TrimSpace(q.Label); l != "" && !slices.ContainsFunc(i.Labels, func(x string) bool { return strings.EqualFold(strings.TrimSpace(x), l) }) {
		return false
	}
	if r := strings.TrimSpace(q.Repo); r == IssueFilterNone {
		if strings.TrimSpace(i.Repo) != "" {
			return false
		}
	} else if r != "" && !RepoMatch(i.Repo, r) && !(strings.TrimSpace(i.Repo) == "" && i.Project != "" && slices.ContainsFunc(q.RepoProjects, func(x string) bool { return strings.EqualFold(x, i.Project) })) {
		return false
	}
	if a := strings.TrimSpace(q.Assignee); a == IssueFilterNone {
		if IssueAssigned(i) {
			return false
		}
	} else if a != "" && !assigneeMatches(i, a) {
		return false
	}
	if pr := strings.TrimSpace(q.Project); pr == IssueProjectNone || pr == IssueFilterNone {
		if i.Project != "" {
			return false
		}
	} else if pr != "" && !strings.EqualFold(i.Project, pr) {
		return false
	}
	return IssueMatchesSearch(i, words)
}

// assigneeMatches assignee 篩選值 a(已 trim、非空)符合負責人:assignee 字串(角色 handle),
// 或 assigneeActor 的 id、handle、email、name 任一(人類接受者只有 assigneeActor);不分大小寫
func assigneeMatches(i Issue, a string) bool {
	if strings.EqualFold(strings.TrimSpace(i.Assignee), a) {
		return true
	}
	if x := i.AssigneeActor; x != nil {
		for _, v := range []string{x.ID, x.Handle, x.Email, x.Name} {
			if strings.EqualFold(strings.TrimSpace(v), a) {
				return true
			}
		}
	}
	return false
}

// FilterIssues 符合 q 篩選條件的 Issue(保留原本順序;不看 Sort)。回傳新的切片,不會是 nil
func FilterIssues(list []Issue, q IssueQuery) []Issue {
	words := IssueSearchWords(q.Q)
	out := []Issue{}
	for _, i := range list {
		if issueMatches(i, q, words) {
			out = append(out, i)
		}
	}
	return out
}

// QueryIssues 篩選後依 q.Sort 排序(Sort 空字串保留原本順序)
func QueryIssues(list []Issue, q IssueQuery) []Issue {
	return SortIssues(FilterIssues(list, q), q.Sort)
}

// IssuePriorityRank 優先度的名次(urgent 0 → low 3;不認得的同 normal)
func IssuePriorityRank(p string) int {
	if k := slices.Index(IssuePriorities, p); k >= 0 {
		return k
	}
	return slices.Index(IssuePriorities, "normal")
}

// issueIDLess 先依 key 的前綴(ISS、APP…),同前綴編號小的在前(ISS-2 < ISS-10);沒有編號的排在有編號的後面,彼此依字串
func issueIDLess(a, b Issue) bool {
	if pa, pb := IssueKeyPrefixOf(a.Display()), IssueKeyPrefixOf(b.Display()); pa != pb && pa != "" && pb != "" {
		return pa < pb
	}
	na, nb := a.Number(), b.Number()
	if (na == 0) != (nb == 0) {
		return na != 0
	}
	if na != nb {
		return na < nb
	}
	return strings.ToUpper(a.ID) < strings.ToUpper(b.ID)
}

// issueLess 優先度 → 最近更新 → 編號
func issueLess(a, b Issue) bool {
	if pa, pb := IssuePriorityRank(a.Priority), IssuePriorityRank(b.Priority); pa != pb {
		return pa < pb
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt.Time) {
		return a.UpdatedAt.After(b.UpdatedAt.Time)
	}
	return issueIDLess(a, b)
}

// SortIssues 依排序方式排(見 IssueSorts;空字串或不認得的 = 不排序)。回傳新的切片,不改 list
func SortIssues(list []Issue, by string) []Issue {
	out := append([]Issue{}, list...)
	var less func(a, b Issue) bool
	switch strings.TrimSpace(by) {
	case IssueSortPriority:
		less = issueLess
	case IssueSortStatus:
		less = func(a, b Issue) bool {
			if oa, ob := a.Status != "closed", b.Status != "closed"; oa != ob {
				return oa
			}
			return issueLess(a, b)
		}
	case IssueSortUpdated:
		less = func(a, b Issue) bool {
			if !a.UpdatedAt.Equal(b.UpdatedAt.Time) {
				return a.UpdatedAt.After(b.UpdatedAt.Time)
			}
			return issueIDLess(a, b)
		}
	default:
		return out
	}
	sort.SliceStable(out, func(x, y int) bool { return less(out[x], out[y]) })
	return out
}

// UsedLabels 用到的標籤(去頭尾空白;不分大小寫去重,保留第一次出現的寫法;依字母排序、不分大小寫),篩選選單用
func UsedLabels(list []Issue) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, i := range list {
		for _, l := range i.Labels {
			l = strings.TrimSpace(l)
			k := strings.ToLower(l)
			if l == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(x, y int) bool {
		a, b := strings.ToLower(out[x]), strings.ToLower(out[y])
		if a != b {
			return a < b
		}
		return out[x] < out[y]
	})
	return out
}

// IssueAssigned 這張 Issue 有沒有負責人(被指派給角色,或被人、角色「接受」,見 service/issue_accept.go)。
// 服務層出口的 Assignee 字串只有角色的 handle(人類接受者是空字串),人類接受者看 AssigneeActor,所以兩個都要看
func IssueAssigned(i Issue) bool {
	return strings.TrimSpace(i.Assignee) != "" || i.AssigneeActor != nil
}

// 看板的欄(IssueBoardColumn 回傳、BoardColumn.Key)
const (
	BoardTodo  = "todo"  // 待處理:open 且沒有負責人(沒指派角色、也沒人接受)
	BoardDoing = "doing" // 進行中:open 且有負責人(指派給角色,或被人、角色接受;見 IssueAssigned)
	BoardDone  = "done"  // 已完成:closed
)

// BoardColumnKeys 看板的欄,依顯示順序
var BoardColumnKeys = []string{BoardTodo, BoardDoing, BoardDone}

var boardTitles = map[string]string{BoardTodo: "待處理", BoardDoing: "進行中", BoardDone: "已完成"}

// IssueBoardColumn 這張 Issue 在看板的哪一欄
func IssueBoardColumn(i Issue) string {
	if i.Status == "closed" {
		return BoardDone
	}
	if !IssueAssigned(i) {
		return BoardTodo
	}
	return BoardDoing
}

// boardDropStatus 拖進這一欄時要把 Issue 的 status 改成什麼:已完成 = closed;待處理、進行中 = open
// (從已完成拖出來 = 重新打開,落在哪一欄由有沒有指派決定;待處理 ↔ 進行中 狀態都是 open,不動作)
func boardDropStatus(col string) *string {
	s := "open"
	if col == BoardDone {
		s = "closed"
	}
	return &s
}

// BoardDrop 把 i 拖到 col 欄要改成的 status;nil = 不用改或不允許(同一欄、或 待處理 ↔ 進行中:要改指派,看板不做)。
// 等同「欄的 DropStatus 和 Issue 現在的 status 不同才改」
func BoardDrop(i Issue, col string) *string {
	if !slices.Contains(BoardColumnKeys, col) || IssueBoardColumn(i) == col {
		return nil
	}
	s := boardDropStatus(col)
	if *s == i.Status {
		return nil
	}
	return s
}

// BoardColumn 看板的一欄
type BoardColumn struct {
	Key   string `json:"key"`   // todo、doing、done
	Title string `json:"title"` // 待處理、進行中、已完成
	// DropStatus 拖進這一欄時要套的 status(open / closed);Issue 現在的 status 已經是這個就不用送修改。
	// null = 拖進來不改狀態(目前每一欄都有值)
	DropStatus *string `json:"dropStatus"`
	// Issues 這一欄的 Issue,已依 IssueSortPriority 排好;沒有時是空陣列
	Issues []Issue `json:"issues"`
}

// IssueBoard GET …/issues/board 的回應
type IssueBoard struct {
	Columns []BoardColumn `json:"columns"`
	// Labels 全部 Issue(篩選前)用到的標籤,篩選選單用(見 UsedLabels)
	Labels []string `json:"labels"`
	// Total 篩選前的 Issue 數;Matched 篩選後(三欄加起來)的
	Total   int `json:"total"`
	Matched int `json:"matched"`
}

// BoardColumns 分成三欄(依 BoardColumnKeys 的順序),每欄依 IssueSortPriority 排序。list 是已經篩選好的
func BoardColumns(list []Issue) []BoardColumn {
	by := map[string][]Issue{}
	for _, i := range list {
		c := IssueBoardColumn(i)
		by[c] = append(by[c], i)
	}
	out := make([]BoardColumn, 0, len(BoardColumnKeys))
	for _, k := range BoardColumnKeys {
		out = append(out, BoardColumn{Key: k, Title: boardTitles[k], DropStatus: boardDropStatus(k), Issues: SortIssues(by[k], IssueSortPriority)})
	}
	return out
}

// BuildIssueBoard 看板:all 是全部 Issue,依 q 篩選(不看 q.Status 與 q.Sort:看板三欄本來就涵蓋全部狀態、每欄固定依優先度排)
func BuildIssueBoard(all []Issue, q IssueQuery) IssueBoard {
	q.Status, q.Sort = "", ""
	f := FilterIssues(all, q)
	return IssueBoard{Columns: BoardColumns(f), Labels: UsedLabels(all), Total: len(all), Matched: len(f)}
}
