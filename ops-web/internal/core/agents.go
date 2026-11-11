package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// 虛擬角色與任務:同 Swift 版 Agents/Agents.swift。存在目前組織資料夾的 agents.json。
// Ops 不自己跑 AI:任務排在這裡,由 Claude Code 透過 MCP 以角色身分領取、回報。
// 例行任務到時間時(每 60 秒檢查),替該角色排一筆任務。

const RoutineAuthor = "routine"

var (
	AgentColors       = []string{"blue", "teal", "green", "orange", "pink", "purple", "indigo", "gray"}
	RoutineKinds      = []string{"daily", "weekly", "hourly"}
	AgentTaskStatuses = []string{"queued", "inProgress", "blocked", "done", "canceled"}
	DocAccessLevels   = []string{"none", "read", "write"}
	weekdayNames      = []string{"日", "一", "二", "三", "四", "五", "六"}
)

type AgentRoutine struct {
	CreatedAt    Time   `json:"createdAt"`
	Enabled      bool   `json:"enabled"`
	EveryHours   int    `json:"everyHours"`
	Hour         int    `json:"hour"`
	ID           string `json:"id"`
	Instructions string `json:"instructions"`
	Kind         string `json:"kind"`
	LastRunAt    *Time  `json:"lastRunAt,omitempty"`
	Minute       int    `json:"minute"`
	Priority     string `json:"priority"`
	Title        string `json:"title"`
	Weekday      int    `json:"weekday"`
}

func (r *AgentRoutine) UnmarshalJSON(b []byte) error {
	type raw AgentRoutine
	x := raw(NewRoutine(""))
	x.ID = ""
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	if x.ID == "" {
		x.ID = UUID()
	}
	if !iaOneOf(x.Kind, RoutineKinds) {
		x.Kind = "daily"
	}
	if !iaOneOf(x.Priority, IssuePriorities) {
		x.Priority = "normal"
	}
	*r = AgentRoutine(x)
	return nil
}

// ScheduleLabel 同 Swift 的 scheduleLabel
func (r AgentRoutine) ScheduleLabel() string {
	t := fmt.Sprintf("%02d:%02d", r.Hour, r.Minute)
	switch r.Kind {
	case "weekly":
		return "每週" + weekdayNames[min(7, max(1, r.Weekday))-1] + " " + t
	case "hourly":
		return "每 " + strconv.Itoa(r.EveryHours) + " 小時"
	}
	return "每天 " + t
}

// NextRun 上次排程(沒有就從建立時間)之後的下一個時間點(本地時區,同 Swift 的 Calendar.current)
func (r AgentRoutine) NextRun() time.Time {
	base := r.CreatedAt.Time
	if r.LastRunAt != nil {
		base = r.LastRunAt.Time
	}
	if r.Kind == "hourly" {
		return base.Add(time.Duration(max(1, r.EveryHours)) * time.Hour)
	}
	if r.Hour < 0 || r.Hour > 23 || r.Minute < 0 || r.Minute > 59 || (r.Kind == "weekly" && (r.Weekday < 1 || r.Weekday > 7)) {
		return base // 同 Swift:對不到時間就回傳 base
	}
	lb := base.In(time.Local)
	for d := 0; d <= 8; d++ {
		day := lb.AddDate(0, 0, d)
		c := time.Date(day.Year(), day.Month(), day.Day(), r.Hour, r.Minute, 0, 0, time.Local)
		if !c.After(base) {
			continue
		}
		if r.Kind == "weekly" && int(c.Weekday())+1 != r.Weekday {
			continue
		}
		return c
	}
	return base
}

type Agent struct {
	Active       bool              `json:"active"`
	Color        string            `json:"color"`
	CreatedAt    Time              `json:"createdAt"`
	DocAccess    map[string]string `json:"docAccess"`
	ID           string            `json:"id"`
	Instructions string            `json:"instructions"`
	Name         string            `json:"name"`
	Role         string            `json:"role"`
	Routines     []AgentRoutine    `json:"routines"`
	Symbol       string            `json:"symbol"`
}

// UnmarshalJSON 寬鬆解碼(同 Swift 的手寫解碼):id、name 必須有,其他缺了用預設值
func (g *Agent) UnmarshalJSON(b []byte) error {
	type raw Agent
	x := raw(NewAgent("", ""))
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(b, &keys)
	if _, ok := keys["id"]; !ok {
		return fmt.Errorf("角色缺少 id")
	}
	if _, ok := keys["name"]; !ok {
		return fmt.Errorf("角色缺少 name")
	}
	if x.Routines == nil {
		x.Routines = []AgentRoutine{}
	}
	acc := map[string]string{}
	for k, v := range x.DocAccess {
		if iaOneOf(v, DocAccessLevels) {
			acc[k] = v
		}
	}
	x.DocAccess = acc
	*g = Agent(x)
	return nil
}

// Access 這個角色對某個位置的權限("" = 不能存取);頁籤的設定優先於區塊
func (g Agent) Access(section, tab string) string {
	if tab != "" {
		if a, ok := g.DocAccess[tab]; ok {
			if a == "none" {
				return ""
			}
			return a
		}
	}
	if a, ok := g.DocAccess[section]; ok && a != "none" {
		return a
	}
	return ""
}

func (g Agent) CanRead(section, tab string) bool  { return g.Access(section, tab) != "" }
func (g Agent) CanWrite(section, tab string) bool { return g.Access(section, tab) == "write" }

func cloneAgent(g Agent) Agent {
	g.Routines = append([]AgentRoutine{}, g.Routines...)
	acc := map[string]string{}
	for k, v := range g.DocAccess {
		acc[k] = v
	}
	g.DocAccess = acc
	return g
}

type AgentTaskNote struct {
	At     Time   `json:"at"`
	Author string `json:"author"`
	ID     string `json:"id"`
	Text   string `json:"text"`
}

func (n *AgentTaskNote) UnmarshalJSON(b []byte) error {
	type raw AgentTaskNote
	x := raw{At: nowPrecise()}
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	if x.ID == "" {
		x.ID = UUID()
	}
	*n = AgentTaskNote(x)
	return nil
}

type AgentTask struct {
	AgentID      string          `json:"agentId"`
	ClaimedAt    *Time           `json:"claimedAt,omitempty"`
	CompletedAt  *Time           `json:"completedAt,omitempty"`
	CreatedAt    Time            `json:"createdAt"`
	CreatedBy    string          `json:"createdBy"`
	ID           string          `json:"id"`
	Instructions string          `json:"instructions"`
	Links        []string        `json:"links"`
	Notes        []AgentTaskNote `json:"notes"`
	Priority     string          `json:"priority"`
	Result       string          `json:"result"`
	RoutineID    *string         `json:"routineId,omitempty"`
	Status       string          `json:"status"`
	Title        string          `json:"title"`
	UpdatedAt    Time            `json:"updatedAt"`
}

func (t *AgentTask) UnmarshalJSON(b []byte) error {
	type raw AgentTask
	now := nowPrecise()
	x := raw{Status: "queued", Priority: "normal", CreatedAt: now, UpdatedAt: now, CreatedBy: MCPAuthor}
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	if !iaOneOf(x.Status, AgentTaskStatuses) {
		x.Status = "queued"
	}
	if !iaOneOf(x.Priority, IssuePriorities) {
		x.Priority = "normal"
	}
	if x.Links == nil {
		x.Links = []string{}
	}
	if x.Notes == nil {
		x.Notes = []AgentTaskNote{}
	}
	*t = AgentTask(x)
	return nil
}

func (t AgentTask) Number() int { return iaIDNumber(t.ID) }

func TaskIsOpen(status string) bool {
	return status == "queued" || status == "inProgress" || status == "blocked"
}

// TaskStatusLabel 同 Swift 的 AgentTaskStatus.label
func TaskStatusLabel(s string) string {
	switch s {
	case "queued":
		return "待領取"
	case "inProgress":
		return "進行中"
	case "blocked":
		return "需要協助"
	case "done":
		return "完成"
	case "canceled":
		return "已取消"
	}
	return s
}

func cloneTask(t AgentTask) AgentTask {
	t.Links = append([]string{}, t.Links...)
	t.Notes = append([]AgentTaskNote{}, t.Notes...)
	return t
}

// AgentData agents.json 的內容
type AgentData struct {
	Agents []Agent     `json:"agents"`
	Tasks  []AgentTask `json:"tasks"`
}

type agentState struct {
	Agents []Agent
	Tasks  []AgentTask
}

func (a *App) agentSt() *agentState {
	if s, ok := a.Ext["agents"].(*agentState); ok {
		return s
	}
	s := &agentState{}
	a.Ext["agents"] = s
	a.loadAgents()
	return s
}

// Agents 全部角色(呼叫時要拿著 Mu)
func (a *App) Agents() []Agent { return a.agentSt().Agents }

// AgentTasks 全部任務(最新的在前;呼叫時要拿著 Mu)
func (a *App) AgentTasks() []AgentTask { return a.agentSt().Tasks }

func (a *App) agentsPath() string { return filepath.Join(a.DataDir(), "agents.json") }

// 檔案沒有或讀不出來時用預設角色(同 Swift);讀不出來的檔案先改名保留
func (a *App) loadAgents() {
	s := a.agentSt()
	path := a.agentsPath()
	b, err := ReadData(path)
	if err == nil {
		var d AgentData
		if jerr := json.Unmarshal(b, &d); jerr == nil {
			if d.Agents == nil {
				d.Agents = []Agent{}
			}
			if d.Tasks == nil {
				d.Tasks = []AgentTask{}
			}
			s.Agents, s.Tasks = d.Agents, d.Tasks
			return
		}
		keep := strings.TrimSuffix(path, ".json") + fmt.Sprintf(".unreadable-%d.json", time.Now().Unix())
		_ = RenameData(path, keep)
	}
	s.Agents, s.Tasks = defaultAgents(), []AgentTask{}
	a.saveAgents()
}

func (a *App) saveAgents() {
	s := a.agentSt()
	_ = WriteJSON(a.agentsPath(), AgentData{Agents: s.Agents, Tasks: s.Tasks})
}

// FindAgent 依 id(不分大小寫)或名稱找角色(同 Swift 的 AgentStore.agent)
func (a *App) FindAgent(id string) *Agent {
	s := a.agentSt()
	for i := range s.Agents {
		if strings.EqualFold(s.Agents[i].ID, id) {
			g := cloneAgent(s.Agents[i])
			return &g
		}
	}
	for i := range s.Agents {
		if s.Agents[i].Name == id {
			g := cloneAgent(s.Agents[i])
			return &g
		}
	}
	return nil
}

// FindTask 依 id 找任務(不分大小寫)
func (a *App) FindTask(id string) *AgentTask {
	for _, t := range a.agentSt().Tasks {
		if strings.EqualFold(t.ID, id) {
			c := cloneTask(t)
			return &c
		}
	}
	return nil
}

// TasksFor 某個角色的任務
func (a *App) TasksFor(agentID string) []AgentTask {
	out := []AgentTask{}
	for _, t := range a.agentSt().Tasks {
		if t.AgentID == agentID {
			out = append(out, t)
		}
	}
	return out
}

// TaskAuthorName 同 Swift 的 AgentStore.authorName
func (a *App) TaskAuthorName(author string) string {
	if author == RoutineAuthor {
		return "例行排程"
	}
	if strings.HasPrefix(author, AgentPrefix) {
		if g := a.FindAgent(strings.TrimPrefix(author, AgentPrefix)); g != nil {
			return g.Name
		}
		return author
	}
	if author == MCPAuthor {
		return a.Name(MCPActor)
	}
	return a.Name(Person(author))
}

// UpsertAgent 新增或取代(依 id 完全相同)
func (a *App) UpsertAgent(g Agent) {
	s := a.agentSt()
	g = cloneAgent(g)
	for i := range s.Agents {
		if s.Agents[i].ID == g.ID {
			s.Agents[i] = g
			a.saveAgents()
			go a.Changed("agents")
			return
		}
	}
	s.Agents = append(s.Agents, g)
	a.saveAgents()
	go a.Changed("agents")
}

// DeleteAgent 刪除角色,未完成的任務一併取消
func (a *App) DeleteAgent(id string) {
	s := a.agentSt()
	for i := range s.Tasks {
		if s.Tasks[i].AgentID == id && TaskIsOpen(s.Tasks[i].Status) {
			s.Tasks[i].Status = "canceled"
			s.Tasks[i].UpdatedAt = nowPrecise()
		}
	}
	kept := []Agent{}
	for _, g := range s.Agents {
		if g.ID != id {
			kept = append(kept, g)
		}
	}
	s.Agents = kept
	a.saveAgents()
	go a.Changed("agents")
}

// NewAgentID 由名稱產生 id(同 Swift 的 newId)
func (a *App) NewAgentID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	parts := []string{}
	for _, p := range strings.Split(b.String(), "-") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	root := strings.Join(parts, "-")
	if root == "" {
		root = fmt.Sprintf("agent-%d", len(a.agentSt().Agents)+1)
	}
	id, n := root, 2
	for a.hasAgentID(id) {
		id = fmt.Sprintf("%s-%d", root, n)
		n++
	}
	return id
}

func (a *App) hasAgentID(id string) bool {
	for _, g := range a.agentSt().Agents {
		if g.ID == id {
			return true
		}
	}
	return false
}

// AssignTask 指派任務(同 Swift 的 assign;找不到角色回傳 nil)
func (a *App) AssignTask(agentID, title, instructions, priority, author string, routineID *string) *AgentTask {
	g := a.FindAgent(agentID)
	if g == nil {
		return nil
	}
	s := a.agentSt()
	num := 0
	for _, t := range s.Tasks {
		num = max(num, t.Number())
	}
	if priority == "" {
		priority = "normal"
	}
	now := nowPrecise()
	t := AgentTask{ID: "T-" + strconv.Itoa(num+1), Title: title, Instructions: instructions, AgentID: g.ID,
		Status: "queued", Priority: priority, CreatedBy: author, RoutineID: routineID, CreatedAt: now, UpdatedAt: now,
		Links: []string{}, Notes: []AgentTaskNote{}}
	s.Tasks = append([]AgentTask{t}, s.Tasks...)
	a.saveAgents()
	who := SystemActor
	if author != RoutineAuthor {
		who = AuthorActor(author)
	}
	a.Log(who, "person.badge.clock", "指派 "+t.ID+"「"+title+"」給"+g.Name, "automation")
	go a.Changed("agents")
	return &t
}

// UpdateTask 修改任務;狀態變了會補上領取/完成時間(同 Swift 的 update)
func (a *App) UpdateTask(id string, change func(*AgentTask)) *AgentTask {
	s := a.agentSt()
	idx := -1
	for i := range s.Tasks {
		if strings.EqualFold(s.Tasks[i].ID, id) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	before := s.Tasks[idx]
	t := cloneTask(before)
	change(&t)
	if string(Marshal(t)) == string(Marshal(before)) {
		return &t
	}
	if t.Status != before.Status {
		now := nowPrecise()
		if t.Status == "inProgress" && t.ClaimedAt == nil {
			t.ClaimedAt = &now
		}
		if t.Status == "done" || t.Status == "canceled" {
			t.CompletedAt = &now
		} else {
			t.CompletedAt = nil
		}
	}
	t.UpdatedAt = nowPrecise()
	s.Tasks[idx] = t
	a.saveAgents()
	go a.Changed("agents")
	return &t
}

// NoteTask 加過程紀錄;空白不做事
func (a *App) NoteTask(id, text, author string) *AgentTask {
	t := strings.TrimSpace(text)
	if t == "" {
		return a.FindTask(id)
	}
	return a.UpdateTask(id, func(x *AgentTask) {
		x.Notes = append(x.Notes, AgentTaskNote{ID: UUID(), Author: author, Text: t, At: nowPrecise()})
	})
}

// ClaimTask 角色領取任務:給 id 就領那筆,否則領最優先、最早的待領取任務(錯誤是 UserError,訊息同 Swift)
func (a *App) ClaimTask(agentID string, taskID *string) (*AgentTask, error) {
	g := a.FindAgent(agentID)
	if g == nil {
		return nil, UserError{"找不到角色 " + agentID}
	}
	var pick *AgentTask
	if taskID != nil {
		t := a.FindTask(*taskID)
		if t == nil {
			return nil, UserError{"找不到任務 " + *taskID}
		}
		if t.AgentID != g.ID {
			owner := t.AgentID
			if o := a.FindAgent(t.AgentID); o != nil {
				owner = o.Name
			}
			return nil, UserError{t.ID + " 是指派給 " + owner + " 的,不是 " + g.Name}
		}
		if !TaskIsOpen(t.Status) {
			return nil, UserError{t.ID + " 已經" + TaskStatusLabel(t.Status) + ",不能再領"}
		}
		pick = t
	} else {
		queued := []AgentTask{}
		for _, t := range a.TasksFor(g.ID) {
			if t.Status == "queued" {
				queued = append(queued, t)
			}
		}
		sort.SliceStable(queued, func(i, j int) bool {
			pi, pj := PriorityRank(queued[i].Priority), PriorityRank(queued[j].Priority)
			if pi != pj {
				return pi < pj
			}
			return queued[i].CreatedAt.Before(queued[j].CreatedAt.Time)
		})
		if len(queued) > 0 {
			c := queued[0]
			pick = &c
		}
	}
	if pick == nil {
		return nil, nil
	}
	claimed := a.UpdateTask(pick.ID, func(x *AgentTask) { x.Status = "inProgress" })
	if pick.Status != "inProgress" {
		a.Log(AI(g.ID), "figure.walk", g.Name+"開始處理 "+pick.ID+"「"+pick.Title+"」", "automation")
	}
	return claimed, nil
}

// ReportTask 回報任務(同 Swift 的 report)。status、result、note、links 是 nil 表示沒給
func (a *App) ReportTask(id string, status, result, note *string, links []string) (*AgentTask, error) {
	t := a.FindTask(id)
	if t == nil {
		return nil, UserError{"找不到任務 " + id}
	}
	if t.Status == "canceled" {
		return nil, UserError{t.ID + " 已取消"}
	}
	author := AgentPrefix + t.AgentID
	if note != nil {
		a.NoteTask(t.ID, *note, author)
	}
	updated := a.UpdateTask(t.ID, func(x *AgentTask) {
		if status != nil {
			x.Status = *status
		}
		if result != nil {
			x.Result = *result
		}
		if links != nil {
			x.Links = CleanLabels(append(append([]string{}, x.Links...), links...))
		}
	})
	if status != nil && *status != t.Status {
		if g := a.FindAgent(t.AgentID); g != nil {
			switch *status {
			case "done":
				a.Log(AI(g.ID), "checkmark.circle", g.Name+"完成 "+t.ID+"「"+t.Title+"」", "automation")
			case "blocked":
				a.Log(AI(g.ID), "hand.raised", g.Name+"在 "+t.ID+"「"+t.Title+"」需要協助", "automation")
			}
		}
	}
	return updated, nil
}

// RunDueRoutines 到時間的例行任務各排一筆;上一筆還沒做完就不重複排(呼叫時要拿著 Mu)
func (a *App) RunDueRoutines(now time.Time) {
	s := a.agentSt()
	changed := false
	for ai := range s.Agents {
		if !s.Agents[ai].Active {
			continue
		}
		for ri := range s.Agents[ai].Routines {
			r := s.Agents[ai].Routines[ri]
			if !r.Enabled || r.NextRun().After(now) {
				continue
			}
			t := Time{now.UTC()}
			s.Agents[ai].Routines[ri].LastRunAt = &t
			changed = true
			pending := false
			for _, x := range s.Tasks {
				if x.RoutineID != nil && *x.RoutineID == r.ID && TaskIsOpen(x.Status) {
					pending = true
				}
			}
			if !pending {
				rid := r.ID
				a.AssignTask(s.Agents[ai].ID, r.Title, r.Instructions, r.Priority, RoutineAuthor, &rid)
			}
		}
	}
	if changed {
		a.saveAgents()
		go a.Changed("agents")
	}
}

// RunRoutineNow 手動立刻排一筆例行任務(畫面的「現在排一筆」)
func (a *App) RunRoutineNow(agentID, routineID string) {
	s := a.agentSt()
	for ai := range s.Agents {
		if s.Agents[ai].ID != agentID {
			continue
		}
		for ri := range s.Agents[ai].Routines {
			r := s.Agents[ai].Routines[ri]
			if r.ID != routineID {
				continue
			}
			t := nowPrecise()
			s.Agents[ai].Routines[ri].LastRunAt = &t
			rid := r.ID
			a.AssignTask(agentID, r.Title, r.Instructions, r.Priority, a.Me, &rid)
			return
		}
		return
	}
}

// AgentPrompt 給 Claude Code 的一段話(同 Swift 的 prompt)
func AgentPrompt(g Agent, task *AgentTask) string {
	if task != nil {
		return "請以 Ops 的虛擬角色「" + g.Name + "」(id: " + g.ID + ")處理任務 " + task.ID + ":用 ops_agent_task_claim 領取,照角色設定與任務說明完成,過程用 ops_agent_task_report 回報,做完把結果回報成 done;開 Issue 或留言時帶 agent: " + g.ID + "。"
	}
	return "請以 Ops 的虛擬角色「" + g.Name + "」(id: " + g.ID + ")工作:用 ops_agent_task_claim 依序領取它的待領取任務,照角色設定與任務說明完成並用 ops_agent_task_report 回報,直到沒有任務為止;開 Issue 或留言時帶 agent: " + g.ID + "。"
}

func agentArg(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
}

func init() {
	OnOrgLoad(func(a *App) {
		a.agentSt()
		a.loadAgents()
	})
	OnOrgCreate(func(a *App, dir, template string) {
		data := AgentData{Agents: []Agent{}, Tasks: []AgentTask{}}
		if template == "copyCurrent" {
			for _, g := range a.Agents() {
				g = cloneAgent(g)
				now := nowPrecise()
				for i := range g.Routines {
					g.Routines[i].LastRunAt = nil
					g.Routines[i].CreatedAt = now
				}
				g.CreatedAt = now
				data.Agents = append(data.Agents, g)
			}
		}
		_ = WriteJSON(filepath.Join(dir, "agents.json"), data)
	})
	OnAIName(func(a *App, id string) (string, bool) {
		for _, g := range a.Agents() {
			if g.ID == id {
				return g.Name, true
			}
		}
		return "", false
	})
	OnBadge(func(a *App) map[string]int {
		n := 0
		for _, t := range a.AgentTasks() {
			if t.Status == "blocked" {
				n++
			}
		}
		return map[string]int{"automation": n}
	})
	OnStart(func(a *App) {
		a.Mu.Lock()
		a.RunDueRoutines(time.Now())
		a.Mu.Unlock()
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		go func() {
			for range time.Tick(60 * time.Second) {
				a.Mu.Lock()
				a.RunDueRoutines(time.Now())
				a.Mu.Unlock()
			}
		}()
	})

	RegisterView("agents", func(a *App) any {
		return map[string]any{"agents": a.Agents(), "tasks": a.AgentTasks()}
	})
	RegisterAction("agent_upsert", func(a *App, args map[string]any) (any, error) {
		var g Agent
		m := map[string]any{"id": "", "name": ""}
		for k, v := range args {
			m[k] = v
		}
		if err := ReadInto(m, &g); err != nil {
			return nil, err
		}
		if strings.TrimSpace(g.Name) == "" {
			return nil, UserError{"要給角色名稱"}
		}
		if c := strings.TrimSpace(g.Color); c != "" && !iaOneOf(c, AgentColors) {
			return nil, UserError{"color 必須是 " + strings.Join(AgentColors, "、") + " 之一"}
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if g.ID == "" {
			g.ID = a.NewAgentID(g.Name)
			g.CreatedAt = nowPrecise()
		}
		kept := []AgentRoutine{}
		for _, r := range g.Routines {
			if strings.TrimFunc(r.Title, func(r rune) bool { return r == '\t' || unicode.Is(unicode.Zs, r) }) != "" {
				kept = append(kept, r)
			}
		}
		g.Routines = kept
		a.UpsertAgent(g)
		return g, nil
	})
	RegisterAction("agent_delete", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		a.DeleteAgent(agentArg(args, "id"))
		return nil, nil
	})
	setStatus := func(status string) ActionFunc {
		return func(a *App, args map[string]any) (any, error) {
			a.Mu.Lock()
			defer a.Mu.Unlock()
			t := a.UpdateTask(agentArg(args, "id"), func(x *AgentTask) { x.Status = status })
			if t == nil {
				return nil, UserError{"找不到任務 " + agentArg(args, "id")}
			}
			return t, nil
		}
	}
	RegisterAction("agent_task_cancel", setStatus("canceled"))
	RegisterAction("agent_task_requeue", setStatus("queued"))
	RegisterAction("agent_routine_run", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		a.RunRoutineNow(agentArg(args, "agent"), agentArg(args, "routine"))
		return nil, nil
	})
	RegisterAction("agent_prompt", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		g := a.FindAgent(agentArg(args, "agent"))
		if g == nil {
			return nil, UserError{"找不到角色 " + agentArg(args, "agent")}
		}
		var t *AgentTask
		if id := agentArg(args, "task"); id != "" {
			if t = a.FindTask(id); t == nil {
				return nil, UserError{"找不到任務 " + id}
			}
		}
		return map[string]any{"text": AgentPrompt(*g, t)}, nil
	})
}
