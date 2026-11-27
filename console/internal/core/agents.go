package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"omixor-core/backend"
	"omixor-core/config"
	"omixor-core/httperr"
	"omixor-core/model"
	"omixor-core/service"
)

// 虛擬角色與任務:資料與功能都在核心(omixor-core 的 service + PostgreSQL:omixor_agents、omixor_agent_tasks、omixor_agent_credentials)。
// 這個檔案只是轉接層:
//   - 讀:畫面與工具讀 App 的快取(agentState,自己有鎖,不依賴 Mu;載入時讀、每 5 秒背景重讀)
//   - 寫:AgentOps(實作 backend.Backend 的角色與任務部分)交給核心,成功後更新快取、寫動態、通知畫面。呼叫時不要拿著 Mu
//   - 例行任務排程:每 60 秒呼叫 service.RunDueRoutines
//
// omixor 不自己跑 AI:任務排在核心,由 Claude Code 透過 MCP 以角色身分領取、回報。

type (
	Agent         = model.Agent
	AgentRoutine  = model.AgentRoutine
	AgentTask     = model.AgentTask
	AgentTaskNote = model.AgentTaskNote
)

const RoutineAuthor = model.RoutineAuthor

var (
	AgentColors       = model.AgentColors
	RoutineKinds      = model.RoutineKinds
	AgentTaskStatuses = model.AgentTaskStatuses
	DocAccessLevels   = model.DocAccessLevels
)

func TaskIsOpen(status string) bool { return model.TaskIsOpen(status) }

// ---- 核心的連線 ----

// agentBackend 核心(系統身分);沒有資料庫回 errNoDB
func agentBackend() (backend.Backend, error) { return coreBackend() }

// AgentEndpointURL 領取任務時告訴呼叫端、自動執行要連的角色端點:畫面連接埠的 /mcp/agent(呼叫時要拿著 Mu)
func (a *App) AgentEndpointURL() string {
	if a.AgentLocalEndpoint != "" {
		return a.AgentLocalEndpoint
	}
	return "http://127.0.0.1:5191/mcp/agent" // 還沒掛上入口(測試、匯入指令):預設的畫面連接埠
}

// AgentService 核心的 service(角色端點 /mcp/agent 驗證憑證用);沒有資料庫回 nil
func AgentService() *service.Service {
	if l := localCore(); l != nil {
		return l.Svc
	}
	return nil
}

// RoleCallFinished 角色端點(/mcp/agent)或對外 MCP 連接埠處理完一個會寫入的請求:核心可能多了 Issue、留言、任務狀態、文件,
// 重讀「目前組織」(系統工作組織)的快取,並通知全部組織層的主題:寫入可能在別的組織,看著那個組織的畫面
// (啟用 LDAP 時每人各選)要靠通知重讀。不要拿著 Mu
func (a *App) RoleCallFinished() {
	a.InvalidateScoped()
	a.Mu.Lock()
	a.RefreshIssuesIfStale(0)
	a.changedIssues()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	a.AgentsChanged()
	if org != "" {
		a.cabinetRefresh(org)
		if a.IncidentsRefresh(org) {
			a.Changed("alerts")
		}
	}
	a.Changed("cabinet", "incidents", "chain", "members", "channels", "badges") // 角色可能發了頻道訊息、標了已讀
}

// ---- 快取 ----

type agentState struct {
	mu     sync.Mutex
	org    string
	loaded bool
	agents []Agent
	tasks  []AgentTask
	err    string
}

var agentStates sync.Map // *App → *agentState

func (a *App) agSt() *agentState {
	if s, ok := agentStates.Load(a); ok {
		return s.(*agentState)
	}
	s, _ := agentStates.LoadOrStore(a, &agentState{})
	return s.(*agentState)
}

// agentRefresh 從核心重讀 org 的角色與任務並換進快取(不拿 Mu;網路與資料庫請求都在這裡),回傳內容有沒有變
func (a *App) agentRefresh(org string) bool {
	agents, tasks, err := a.agentFetch(org)
	if agents == nil {
		agents = []Agent{}
	}
	if tasks == nil {
		tasks = []AgentTask{}
	}
	st := a.agSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		msg := err.Error()
		if st.err != msg {
			st.err = msg
		}
		if st.org != org || !st.loaded {
			st.org, st.agents, st.tasks, st.loaded = org, []Agent{}, []AgentTask{}, true
			return true
		}
		return false
	}
	changed := st.org != org || !st.loaded || string(Marshal(st.agents)) != string(Marshal(agents)) || string(Marshal(st.tasks)) != string(Marshal(tasks))
	st.org, st.agents, st.tasks, st.loaded, st.err = org, agents, tasks, true, ""
	return changed
}

func (a *App) agentFetch(org string) ([]Agent, []AgentTask, error) {
	if org == "" { // 還沒有組織
		return nil, nil, nil
	}
	be, err := agentBackend()
	if err != nil {
		return nil, nil, err
	}
	agents, err := be.ListAgents(org)
	if err != nil {
		return nil, nil, err
	}
	tasks, err := be.ListAgentTasks(org, service.TaskFilter{})
	if err != nil {
		return nil, nil, err
	}
	return agents, tasks, nil
}

// agentSnapshot org 的角色與任務(快取;組織還沒載入時當場讀)。不需要拿 Mu
func (a *App) agentSnapshot(org string) ([]Agent, []AgentTask) {
	st := a.agSt()
	st.mu.Lock()
	cur := st.loaded && st.org == org
	st.mu.Unlock()
	if !cur {
		a.agentRefresh(org)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.agents, st.tasks
}

// AgentsError 最近一次讀核心的錯誤(成功後清掉)
func (a *App) AgentsError() string {
	st := a.agSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.err
}

// ---- 讀(目前組織;呼叫時要拿著 Mu,因為要知道目前組織) ----

// Agents 目前組織的全部角色
func (a *App) Agents() []Agent { return a.AgentsOf(a.CurrentOrgID()) }

// AgentTasks 目前組織的全部任務(最新的在前)
func (a *App) AgentTasks() []AgentTask { return a.TasksOf(a.CurrentOrgID()) }

// AgentsOf、TasksOf 指定組織(不需要拿 Mu)
func (a *App) AgentsOf(org string) []Agent { g, _ := a.agentSnapshot(org); return g }
func (a *App) TasksOf(org string) []AgentTask {
	_, t := a.agentSnapshot(org)
	return t
}

// FindAgentIn 依 id(不分大小寫)或名稱找角色(不需要拿 Mu)
func (a *App) FindAgentIn(org, id string) *Agent {
	list := a.AgentsOf(org)
	for i := range list {
		if strings.EqualFold(list[i].ID, id) {
			g := list[i].Clone()
			return &g
		}
	}
	for i := range list {
		if list[i].Name == id {
			g := list[i].Clone()
			return &g
		}
	}
	return nil
}

// FindAgent 目前組織依 id(不分大小寫)或名稱找角色(同 Swift 的 AgentStore.agent)
func (a *App) FindAgent(id string) *Agent { return a.FindAgentIn(a.CurrentOrgID(), id) }

// FindTask 依 id 找任務(不分大小寫)
func (a *App) FindTask(id string) *AgentTask {
	for _, t := range a.AgentTasks() {
		if strings.EqualFold(t.ID, id) {
			c := t.Clone()
			return &c
		}
	}
	return nil
}

// TasksFor 某個角色的任務
func (a *App) TasksFor(agentID string) []AgentTask {
	out := []AgentTask{}
	for _, t := range a.AgentTasks() {
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

// ---- 預設值 ----

// 同 Swift 版的新角色預設值
func NewAgent(id, name string) Agent {
	return Agent{ID: id, Name: name, Symbol: "person.crop.circle", Color: "blue", Active: true,
		Routines: []AgentRoutine{}, CreatedAt: model.Now(), DocAccess: map[string]string{}}
}

func NewRoutine(title string) AgentRoutine {
	return AgentRoutine{ID: UUID(), Title: title, Kind: "daily", Hour: 9, Weekday: 2, EveryHours: 6,
		Priority: "normal", Enabled: true, CreatedAt: model.Now()}
}

// AgentPrompt 給 Claude Code 的一段話(同 Swift 的 prompt)
func AgentPrompt(g Agent, task *AgentTask) string {
	if task != nil {
		return "請以 omixor 的虛擬角色「" + g.Name + "」(id: " + g.ID + ")處理任務 " + task.ID + ":用 agent_task_claim 領取,照角色設定與任務說明完成,過程用 agent_task_report 回報,做完把結果回報成 done;開 Issue 或留言時帶 agent: " + g.ID + "。"
	}
	return "請以 omixor 的虛擬角色「" + g.Name + "」(id: " + g.ID + ")工作:用 agent_task_claim 依序領取它的待領取任務,照角色設定與任務說明完成並用 agent_task_report 回報,直到沒有任務為止;開 Issue 或留言時帶 agent: " + g.ID + "。"
}

func agentArg(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
}

func (a *App) currentOrgNow() string {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return a.CurrentOrgID()
}

func init() {
	OnOrgCreate(func(a *App, dir, template string) {
		// 複製目前組織:角色(不含任務)搬到新組織的核心
		if template != "copyCurrent" {
			return
		}
		newOrg := filepath.Base(dir)
		be, err := coreBackendFor(a.caller, newOrg) // 建立組織的人(拿著 Mu,見 AsCaller)
		if err != nil {
			return
		}
		for _, g := range a.Agents() {
			g = g.Clone()
			for i := range g.Routines {
				g.Routines[i].LastRunAt = nil
				g.Routines[i].CreatedAt = model.Now()
			}
			if _, err := be.SaveAgent(newOrg, g); err != nil {
				break
			}
		}
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
		// 需要協助的角色任務:角色或派發分頁開著才算(同 App 的 badge(for:))
		if !a.FeatureOn("automation.agents") && !a.FeatureOn("automation.dispatch") {
			return nil
		}
		return map[string]int{"automation": blockedTasks(a)}
	})
	OnStart(func(a *App) {
		if config.Current().Bool(config.NoTimers) {
			return
		}
		// 例行任務排程(每 60 秒)
		go func() {
			for {
				if l := localCore(); l != nil {
					if created, err := l.Svc.RunDueRoutines(time.Now()); err != nil {
						fmt.Fprintf(os.Stderr, "例行任務排程:%v\n", err)
					} else if len(created) > 0 {
						a.AgentsChanged()
					}
				}
				time.Sleep(60 * time.Second)
			}
		}()
		// 每 5 秒重讀目前組織的角色與任務(別的路徑改了資料才看得到;資料庫請求在鎖外)
		go func() {
			for range time.Tick(5 * time.Second) {
				if org := a.currentOrgNow(); org != "" && a.agentRefresh(org) {
					a.Changed("agents")
				}
			}
		}()
	})

	RegisterView("agents", func(a *App) any {
		return map[string]any{"agents": a.Agents(), "tasks": a.AgentTasks()}
	})
	RegisterAction("agent_upsert", func(a *App, args map[string]any) (any, error) {
		g := NewAgent("", "")
		m := map[string]any{}
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
		saved, err := a.AgentOpsFor(CallerOf(args)).SaveAgent(a.currentOrgNow(), g)
		if err != nil {
			return nil, agentUserErr(err)
		}
		return saved, nil
	})
	// 名下有任務(任何狀態)的角色要先轉移:不帶 transferTo = 409(回應帶 code = agentHasTasks、taskCounts、total,畫面據此開「轉移給…」視窗);
	// 帶 transferTo(目標角色 id)= 同一個交易裡先轉移再刪
	RegisterAction("agent_delete", func(a *App, args map[string]any) (any, error) {
		return nil, agentConflictErr(a.AgentOpsFor(CallerOf(args)).DeleteAgentTo(a.currentOrgNow(), agentArg(args, "id"), agentArg(args, "transferTo")))
	})
	// 不刪角色的單獨轉移:{from, to, scope: all|open}
	RegisterAction("agent_task_transfer", func(a *App, args map[string]any) (any, error) {
		res, err := a.AgentOpsFor(CallerOf(args)).TransferAgentTasks(a.currentOrgNow(), service.AgentTransferInput{
			From: agentArg(args, "from"), To: agentArg(args, "to"), Scope: agentArg(args, "scope")})
		return res, agentUserErr(err)
	})
	RegisterAction("agent_task_cancel", func(a *App, args map[string]any) (any, error) {
		t, err := a.AgentOpsFor(CallerOf(args)).CancelTask(a.currentOrgNow(), agentArg(args, "id"))
		return t, agentUserErr(err)
	})
	RegisterAction("agent_task_requeue", func(a *App, args map[string]any) (any, error) {
		t, err := a.AgentOpsFor(CallerOf(args)).RequeueTask(a.currentOrgNow(), agentArg(args, "id"))
		return t, agentUserErr(err)
	})
	// 人回覆需要協助的任務:{id, text};回覆記成備註(本人署名)、任務退回待領取,角色下次領取就看得到
	RegisterAction("agent_task_reply", func(a *App, args map[string]any) (any, error) {
		t, err := a.AgentOpsFor(CallerOf(args)).ReplyTask(a.currentOrgNow(), agentArg(args, "id"), agentArg(args, "text"))
		return t, agentUserErr(err)
	})
	RegisterAction("agent_routine_run", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		org, me := a.CurrentOrgID(), UserActor(a, args).ID
		a.Mu.Unlock()
		_, err := a.AgentOpsFor(CallerOf(args)).RunRoutine(org, agentArg(args, "agent"), agentArg(args, "routine"), me)
		return nil, agentUserErr(err)
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
			if t = a.FindTask(id); t == nil || HiddenOf(args).Task(*t) {
				return nil, UserError{"找不到任務 " + id}
			}
		}
		return map[string]any{"text": AgentPrompt(*g, t)}, nil
	})
}

// blockedTasks 需要協助的角色任務數(徽章用;拿著 Mu 呼叫)
func blockedTasks(a *App) int {
	n := 0
	for _, t := range a.AgentTasks() {
		if t.Status == "blocked" {
			n++
		}
	}
	return n
}

// agentConflictErr 同 agentUserErr,但 409 帶著核心給的欄位(StatusError.Data;例如刪角色時名下有任務的 code、taskCounts、
// 版本衝突的 currentVersion);狀態碼、訊息、data 取自 httperr,和 API、MCP 同一份規則
func agentConflictErr(err error) error {
	if err == nil {
		return nil
	}
	in := httperr.Report(callerOp(2), err)
	if in.Status == 409 && len(in.Data) > 0 {
		return StatusError{Status: 409, Message: in.Msg, Data: in.Data}
	}
	return UserError{in.Msg}
}

// agentUserErr 核心的錯誤轉成畫面看得懂的 UserError(nil 原樣回傳);訊息取自 httperr:
// 資料庫錯誤等內部細節換成通用訊息,原文連同呼叫的位置寫進 log
func agentUserErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(UserError); ok {
		return err
	}
	return UserError{httperr.Report(callerOp(2), err).Msg}
}

// callerOp 呼叫端的函式與行號(log 的操作名稱);skip 2 = 呼叫 agentUserErr 的那一行
func callerOp(skip int) string {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "console 動作"
	}
	name := "?"
	if f := runtime.FuncForPC(pc); f != nil {
		name = f.Name()
	}
	if i := strings.LastIndexAny(file, `/\`); i >= 0 {
		file = file[i+1:]
	}
	return fmt.Sprintf("console %s(%s:%d)", name, file, line)
}
