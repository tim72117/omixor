package core

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 受限專案的「衍生資料」篩選。ScrubRestricted(console_policy.go)只認得帶 project 欄位的物件;動態牆的文字、徽章與清單的
// 計數、角色任務(標題與說明帶有 Issue 的內容)、待決定、用過的 repo 這些沒有 project 欄位,要在「算出數字之前、輸出之前」
// 用這裡的 Hidden 篩掉。擋哪些專案 = 核心依呼叫者算出他看不到的那些(restricted_visibility.go):專案成員、組織管理員看得到。
//
// 問不到、對不出來時一律當作看不到。nil *Hidden = 不必篩(管理員、沒有受限專案),每個方法都可以在 nil 上呼叫。

// HiddenKey web 呼叫 Action 時把 *Hidden 放在 args[HiddenKey](只認伺服器放的)
const HiddenKey = "_hidden"

// HiddenOf Action 參數裡的篩選(沒有是 nil)
func HiddenOf(args map[string]any) *Hidden {
	h, _ := args[HiddenKey].(*Hidden)
	return h
}

// Hidden 一次請求要擋掉的受限專案資料;只給單一請求用(內部的查詢結果在請求內快取,不跨 goroutine)
type Hidden struct {
	app *App
	org string
	// who 對話頻道因人而異,用呼叫者的身分查
	who *Caller
	// viewer 查對話頻道改用這個核心(OAuth 授權者);nil = 依 who
	viewer backend.Backend

	projects map[string]bool
	unknown  bool

	// 以下第一次用到才查
	refs      map[string]string // 大寫的 Issue 內部 id、目前 key → 所屬專案
	docs      map[string]string // 大寫的文件 id → 所屬專案
	tasks     map[string]model.AgentTask
	prefixes  map[string]bool // Issue key 的開頭:ISS 與各專案代號
	channels  map[string]bool // 呼叫者看得到、沒有綁著看不到的 Issue 的頻道名稱
	refResult map[string]bool
}

// NewHidden org 的受限專案 projects(unknown = 問不到,所有專案都當作看不到)
func (a *App) NewHidden(org string, who *Caller, projects map[string]bool, unknown bool) *Hidden {
	return &Hidden{app: a, org: org, who: who, projects: projects, unknown: unknown}
}

// ViewedBy 對話頻道用 b(帶著呼叫者身分的核心)查;回傳自己
func (h *Hidden) ViewedBy(b backend.Backend) *Hidden {
	if h != nil {
		h.viewer = b
	}
	return h
}

// Agents 角色的 docAccess 裡指向看不到的專案的 key 拿掉(同核心 ListAgents;快取是系統身分讀的,要自己拿)。回傳複本
func (h *Hidden) Agents(list []model.Agent) []model.Agent {
	if h == nil {
		return list
	}
	out := make([]model.Agent, 0, len(list))
	for _, g := range list {
		g = g.Clone()
		for k := range g.DocAccess {
			if project, _ := model.SplitDocAccessKey(k); h.Project(project) {
				delete(g.DocAccess, k)
			}
		}
		out = append(out, g)
	}
	return out
}

// Project 這個專案看不到("" = 沒放專案,看得到)
func (h *Hidden) Project(id string) bool {
	return h != nil && id != "" && (h.unknown || h.projects[id])
}

// Issues 看得到的 Issue(新的切片)
func (h *Hidden) Issues(list []model.Issue) []model.Issue {
	if h == nil {
		return list
	}
	out := make([]model.Issue, 0, len(list))
	for _, i := range list {
		if !h.Project(i.Project) {
			out = append(out, i)
		}
	}
	return out
}

// Docs 看得到的文件(新的切片)
func (h *Hidden) Docs(list []model.CabinetDoc) []model.CabinetDoc {
	if h == nil {
		return list
	}
	out := make([]model.CabinetDoc, 0, len(list))
	for _, d := range list {
		if !h.Project(d.Project) {
			out = append(out, d)
		}
	}
	return out
}

// Task 看不到這個角色任務:連結、標題、說明、結果、備註提到看不到的 Issue 或文件
func (h *Hidden) Task(t model.AgentTask) bool {
	if h == nil {
		return false
	}
	for _, l := range t.Links {
		if l = strings.TrimSpace(l); l != "" && (h.issueRef(l) || h.mentions(l)) {
			return true
		}
	}
	text := []string{t.Title, t.Instructions, t.Result}
	for _, n := range t.Notes {
		text = append(text, n.Text)
	}
	return h.mentions(strings.Join(text, "\n"))
}

// Tasks 看得到的角色任務(新的切片)
func (h *Hidden) Tasks(list []model.AgentTask) []model.AgentTask {
	if h == nil {
		return list
	}
	out := make([]model.AgentTask, 0, len(list))
	for _, t := range list {
		if !h.Task(t) {
			out = append(out, t)
		}
	}
	return out
}

// Decisions 看得到的待決定:來源 Issue 看不到的整筆拿掉;blocks 裡看不到的 Issue 拿掉
func (h *Hidden) Decisions(list []model.Decision) []model.Decision {
	if h == nil {
		return list
	}
	out := make([]model.Decision, 0, len(list))
	for _, d := range list {
		if d.SourceIssue != "" && h.issueRef(d.SourceIssue) {
			continue
		}
		blocks := []string{}
		for _, b := range d.Blocks {
			if !h.mentions(b) {
				blocks = append(blocks, b)
			}
		}
		d.Blocks = blocks
		out = append(out, d)
	}
	return out
}

var (
	// 文字裡的編號:ISS-5、APP-12、DOC-3、T-7
	refPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-[0-9]+\b`)
	// 動態牆裡引號括住的頻道名稱
	quotedPattern = regexp.MustCompile(`「([^「」]*)」`)
)

// mentions 文字提到看不到的 Issue、文件
func (h *Hidden) mentions(text string) bool {
	for _, ref := range refPattern.FindAllString(text, -1) {
		switch prefix, _, _ := strings.Cut(ref, "-"); prefix {
		case "DOC":
			if h.docRef(ref) {
				return true
			}
		case "T": // 任務提到任務:不再往下查(避免互相引用)
		default:
			if h.issueRef(ref) {
				return true
			}
		}
	}
	return false
}

// backendOf 系統身分的核心(要看得到全部,才分得出哪些要擋)
func (h *Hidden) backendOf() (backend.Backend, bool) {
	b, err := agentBackend()
	if err != nil {
		log.Printf("受限專案篩選讀不到核心:%v", err)
		return nil, false
	}
	return b, true
}

func (h *Hidden) loadRefs() {
	if h.refs != nil {
		return
	}
	h.refs, h.prefixes, h.refResult = map[string]string{}, map[string]bool{"ISS": true}, map[string]bool{}
	b, ok := h.backendOf()
	if !ok {
		return
	}
	var list []model.Issue
	var err error
	if l := localCore(); l != nil {
		list, err = l.Svc.Store.ListIssuesWithDeleted(h.org) // 垃圾桶裡的標題也在動態牆上
	} else {
		list, err = b.ListIssues(h.org)
	}
	if err != nil {
		log.Printf("受限專案篩選讀不到 Issue:%v", err)
		return
	}
	for _, i := range list {
		h.refs[strings.ToUpper(i.ID)] = i.Project
		if i.Key != "" {
			h.refs[strings.ToUpper(i.Key)] = i.Project
		}
	}
	if ps, err := b.ListProjects(h.org); err == nil {
		for _, p := range ps {
			if p.Key != "" {
				h.prefixes[strings.ToUpper(p.Key)] = true
			}
		}
	}
}

// issueRef 編號(內部 id、目前或舊的 key)指到看不到的 Issue。像 Issue 編號但查不到的(永久刪除、讀不到)當作看不到;
// 其他長得像編號的字串(UTF-8、INC-3)看得到
func (h *Hidden) issueRef(ref string) bool {
	h.loadRefs()
	key := strings.ToUpper(strings.TrimSpace(ref))
	if p, ok := h.refs[key]; ok {
		return h.Project(p)
	}
	if v, ok := h.refResult[key]; ok {
		return v
	}
	prefix, num, _ := strings.Cut(key, "-")
	_, numErr := strconv.Atoi(num)
	like := model.IsUUID(strings.TrimSpace(ref)) || numErr == nil && h.prefixes[prefix]
	hidden := like
	// 舊的 key(搬過專案)由核心解析
	if l := localCore(); l != nil {
		if id, err := l.Svc.Store.ResolveIssueID(h.org, strings.TrimSpace(ref)); err == nil {
			p, ok := h.refs[strings.ToUpper(id)]
			hidden = !ok || h.Project(p)
		}
	}
	h.refResult[key] = hidden
	return hidden
}

// docRef 文件 id 指到看不到的文件;查不到(刪掉了、讀不到)當作看不到
func (h *Hidden) docRef(id string) bool {
	key := strings.ToUpper(id)
	if h.docs == nil {
		h.docs = map[string]string{}
		if l := localCore(); l != nil {
			list, err := l.Svc.Store.ListDocs(h.org)
			if err != nil {
				log.Printf("受限專案篩選讀不到文件:%v", err)
			}
			for _, d := range list {
				h.docs[strings.ToUpper(d.ID)] = d.Project
			}
		}
	}
	if p, ok := h.docs[key]; ok {
		return h.Project(p)
	}
	return true
}

// taskRef 任務編號指到看不到的任務;查不到當作看不到
func (h *Hidden) taskRef(id string) bool {
	if h.tasks == nil {
		h.tasks = map[string]model.AgentTask{}
		if b, ok := h.backendOf(); ok {
			list, err := b.ListAgentTasks(h.org, service.TaskFilter{})
			if err != nil {
				log.Printf("受限專案篩選讀不到任務:%v", err)
			}
			for _, t := range list {
				h.tasks[strings.ToUpper(t.ID)] = t
			}
		}
	}
	t, ok := h.tasks[strings.ToUpper(id)]
	return !ok || h.Task(t)
}

// channelVisible 呼叫者看得到叫這個名稱的頻道,而且它沒有綁著看不到的 Issue
func (h *Hidden) channelVisible(name string) bool {
	if h.channels == nil {
		h.channels = map[string]bool{}
		b, err := h.viewer, error(nil)
		if b == nil {
			b, err = coreBackendFor(h.who, h.org)
		}
		if err == nil {
			var list []model.ChannelSummary
			if list, err = b.ListChannels(h.org, channelFilterAll); err == nil {
				for _, c := range list {
					if c.IssueID == "" || !h.issueRef(c.IssueID) {
						h.channels[c.Name] = true
					}
				}
			}
		}
		if err != nil {
			log.Printf("受限專案篩選讀不到對話頻道:%v", err)
		}
	}
	return h.channels[name]
}

// activityHidden 這筆動態提到看不到的 Issue、文件、角色任務,或呼叫者看不到的對話頻道
func (h *Hidden) activityHidden(item map[string]any) bool {
	text, _ := item["text"].(string)
	if h.mentions(text) {
		return true
	}
	for _, ref := range refPattern.FindAllString(text, -1) {
		if strings.HasPrefix(ref, "T-") && h.taskRef(ref) {
			return true
		}
	}
	// 頻道的動態只有名稱(綁 Issue 的頻道名稱常常就是 Issue 標題)
	if section, _ := item["section"].(string); section == "channels" {
		if m := quotedPattern.FindStringSubmatch(text); m != nil && !h.channelVisible(m[1]) {
			return true
		}
	}
	return false
}

// Activity 看得到的動態(新的切片,不動傳入的)
func (h *Hidden) Activity(items []map[string]any) []map[string]any {
	if h == nil {
		return items
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if !h.activityHidden(it) {
			out = append(out, it)
		}
	}
	return out
}

// ActivityRows 同 Activity,給 core.List 轉出來的 []any(MCP 工具 list、get)
func (h *Hidden) ActivityRows(rows []any) []any {
	if h == nil {
		return rows
	}
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		if m, ok := Object(r).(map[string]any); ok && !h.activityHidden(m) {
			out = append(out, r)
		}
	}
	return out
}

// badges 徽章裡由看不到的 Issue、任務算出來的數字扣掉(拿著 a.Mu 呼叫)
func (h *Hidden) badges(a *App, in map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range in {
		out[k] = v
	}
	if a.FeatureOn("development.issues") {
		all := a.Issues()
		out["development"] -= countUrgentIssues(all) - countUrgentIssues(h.Issues(all))
	}
	if a.FeatureOn("automation.agents") || a.FeatureOn("automation.dispatch") {
		for _, t := range a.AgentTasks() {
			if t.Status == "blocked" && h.Task(t) {
				out["automation"]--
			}
		}
	}
	for k, v := range out {
		if v <= 0 {
			delete(out, k)
		}
	}
	return out
}

// runner 自動執行的紀錄帶任務標題
func (h *Hidden) runner(v AgentRunnerView) AgentRunnerView {
	if v.Current != nil && h.taskRef(v.Current.TaskID) {
		v.Current = nil
	}
	kept := []RunnerRecord{}
	for _, r := range v.History {
		if !h.taskRef(r.TaskID) {
			kept = append(kept, r)
		}
	}
	v.History, v.LastError = kept, ""
	return v
}

// visibleActivityHistory 同 ActivityHistory;篩掉受限專案的之後往更舊的補滿 limit(畫面用最後一筆的時間接著讀)
func visibleActivityHistory(a *App, h *Hidden, before time.Time, limit int) []map[string]any {
	if h == nil {
		return ActivityHistory(a, before, limit)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := []map[string]any{}
	for len(out) < limit {
		page := ActivityHistory(a, before, limit)
		out = append(out, h.Activity(page)...)
		if len(page) < limit {
			break
		}
		last, err := ParseTime(fmt.Sprint(page[len(page)-1]["date"]))
		if err != nil || !last.Before(before) {
			break
		}
		before = last
	}
	return out[:min(len(out), limit)]
}

// ---- 需要篩選的 view ----

// hiddenViews view 算出來之後、轉成 JSON 之前的篩選(拿著 a.Mu 呼叫);不改傳入的資料
var hiddenViews = map[string]func(a *App, h *Hidden, v any) any{}

func init() {
	hiddenViews["activity"] = func(_ *App, h *Hidden, v any) any {
		l, _ := v.([]map[string]any)
		return h.Activity(l)
	}
	hiddenViews["badges"] = func(a *App, h *Hidden, v any) any {
		m, _ := v.(map[string]int)
		return h.badges(a, m)
	}
	hiddenViews["agents"] = func(a *App, h *Hidden, _ any) any {
		return map[string]any{"agents": h.Agents(a.Agents()), "tasks": h.Tasks(a.AgentTasks())}
	}
	hiddenViews["issue_repos"] = func(a *App, h *Hidden, v any) any {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		m["used"] = usedRepos(h.Issues(a.Issues()))
		return m
	}
	hiddenViews["agent_runner"] = func(_ *App, h *Hidden, v any) any {
		if r, ok := v.(AgentRunnerView); ok {
			return h.runner(r)
		}
		return nil // 型別對不上:寧可不給
	}
}
