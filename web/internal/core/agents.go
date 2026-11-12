package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omixor-core/backend"
	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// 虛擬角色與任務:資料與功能都在核心(omixor-core):
//
//	本機模式:內建核心(service)+ 本機 PostgreSQL(omixor_agents、omixor_agent_tasks、omixor_agent_credentials)
//	遠端模式:用 core/client 連線上的 omixor server(角色與任務存在雲端)
//
// 這個檔案只是轉接層:
//   - 讀:畫面與工具讀 App 的快取(agentState,自己有鎖,不依賴 Mu;本機載入時讀、每 5 秒背景重讀,
//     遠端模式也是背景讀,網路請求不在 Mu 之內)
//   - 寫:AgentOps(實作 backend.Backend 的角色與任務部分)交給核心,成功後更新快取、寫動態、通知畫面。呼叫時不要拿著 Mu
//   - 例行任務排程:本機核心每 60 秒呼叫 service.RunDueRoutines(遠端模式由雲端自己排)
//
// Ops 不自己跑 AI:任務排在核心,由 Claude Code 透過 MCP 以角色身分領取、回報。

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

// agentBackend 目前的核心:遠端模式是 omixor server,本機模式是同一個程式裡的核心;本機沒有資料庫回 errNoDB
func agentBackend() (backend.Backend, error) {
	switch b := currentIssueBackend().(type) {
	case *remoteBackend:
		return &backend.Remote{Client: func() *client.Client { return b.client("") }, OrgID: serverOrgID}, nil
	case *localBackend:
		return &backend.Local{Svc: b.svc, P: localPrincipal, EnsureOrg: true}, nil
	}
	return nil, errNoDB
}

// AgentEndpointURL 領取任務時告訴呼叫端的角色端點:遠端模式是雲端的 /mcp/agent;本機是本機 MCP 的 /mcp/agent(呼叫時要拿著 Mu)
func (a *App) AgentEndpointURL() string {
	if u := IssueRemoteURL(); u != "" {
		return strings.TrimRight(u, "/") + "/mcp/agent"
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp/agent", a.MCP.Port)
}

// AgentService 本機核心的 service(角色端點 /mcp/agent 驗證憑證用);遠端模式或沒有資料庫回 nil
func AgentService() *service.Service {
	if IsRemote() {
		return nil
	}
	if l := localCore(); l != nil {
		return l.svc
	}
	return nil
}

// RoleCallFinished 角色端點(/mcp/agent)處理完一個請求:核心可能多了 Issue、留言、任務狀態、文件,
// 重讀 Issue 清單、角色資料與文件櫃並通知畫面。不要拿著 Mu
func (a *App) RoleCallFinished() {
	a.Mu.Lock()
	a.RefreshIssuesIfStale(0)
	a.changedIssues()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	a.AgentsChanged()
	if org != "" && a.cabinetRefresh(org) {
		a.Changed("cabinet")
	}
	a.Changed("channels", "badges") // 角色可能發了頻道訊息、標了已讀
}

// ---- 快取 ----

type agentState struct {
	mu      sync.Mutex
	org     string
	loaded  bool
	agents  []Agent
	tasks   []AgentTask
	syncing bool
	err     string
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

// agentSync 遠端:在背景重讀 org,有變就通知畫面(同時只跑一個)
func (a *App) agentSync(org string) {
	st := a.agSt()
	st.mu.Lock()
	if st.syncing {
		st.mu.Unlock()
		return
	}
	st.syncing = true
	st.mu.Unlock()
	changed := a.agentRefresh(org)
	st.mu.Lock()
	st.syncing = false
	st.mu.Unlock()
	if changed {
		a.Changed("agents")
	}
}

// agentSnapshot org 的角色與任務(快取;組織還沒載入:本機當場讀,遠端先給空的並在背景讀)。不需要拿 Mu
func (a *App) agentSnapshot(org string) ([]Agent, []AgentTask) {
	st := a.agSt()
	st.mu.Lock()
	cur := st.loaded && st.org == org
	st.mu.Unlock()
	if !cur {
		if IsRemote() {
			st.mu.Lock()
			st.org, st.agents, st.tasks, st.loaded = org, []Agent{}, []AgentTask{}, true
			st.mu.Unlock()
			go a.agentSync(org)
		} else {
			a.agentRefresh(org)
		}
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

// ---- 寫:交給核心,成功後更新快取、寫動態、通知畫面 ----

// AgentOps 角色與任務的寫入(實作 backend.Backend 的角色部分)。呼叫時不要拿著 Mu。
// 讀取(ListAgents 等)直接問核心,不用快取。
type AgentOps struct{ a *App }

func (a *App) AgentOps() AgentOps { return AgentOps{a} }

func (o AgentOps) be() (backend.Backend, error) { return agentBackend() }

// done 寫入成功後:快取換成核心最新的、通知畫面
func (o AgentOps) done(org string) {
	o.a.agentRefresh(org)
	o.a.Changed("agents")
}

func (o AgentOps) logf(actor Actor, icon, text string) {
	o.a.Mu.Lock()
	o.a.Log(actor, icon, text, "automation")
	o.a.Mu.Unlock()
}

func (o AgentOps) agentName(org, id string) string {
	if g := o.a.FindAgentIn(org, id); g != nil {
		return g.Name
	}
	return id
}

func (o AgentOps) ListAgents(org string) ([]model.Agent, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.ListAgents(org)
}

func (o AgentOps) GetAgent(org, id string) (*model.Agent, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.GetAgent(org, id)
}

func (o AgentOps) SaveAgent(org string, in model.Agent) (*model.Agent, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	g, err := b.SaveAgent(org, in)
	if err == nil {
		o.done(org)
	}
	return g, err
}

func (o AgentOps) DeleteAgent(org, id string) error {
	b, err := o.be()
	if err != nil {
		return err
	}
	if err = b.DeleteAgent(org, id); err == nil {
		o.done(org)
	}
	return err
}

func (o AgentOps) ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.ListAgentTasks(org, f)
}

func (o AgentOps) GetAgentTask(org, id string) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.GetAgentTask(org, id)
}

func (o AgentOps) AssignTask(org string, in service.TaskInput) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	t, err := b.AssignTask(org, in)
	if err != nil {
		return nil, err
	}
	o.done(org)
	o.logAssigned(org, *t)
	return t, nil
}

func (o AgentOps) logAssigned(org string, t model.AgentTask) {
	who := SystemActor
	if t.CreatedBy != RoutineAuthor {
		who = AuthorActor(t.CreatedBy)
	}
	o.logf(who, "person.badge.clock", "指派 "+t.ID+"「"+t.Title+"」給"+o.agentName(org, t.AgentID))
}

func (o AgentOps) ClaimTask(org string, in service.ClaimInput) (*service.ClaimResult, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	was := ""
	if in.TaskID != "" {
		if t, err := b.GetAgentTask(org, in.TaskID); err == nil {
			was = t.Status
		}
	}
	res, err := b.ClaimTask(org, in)
	if err != nil {
		return nil, err
	}
	if res.Task != nil {
		o.done(org)
		if was != "inProgress" && res.Agent != nil {
			o.logf(AI(res.Agent.ID), "figure.walk", res.Agent.Name+"開始處理 "+res.Task.ID+"「"+res.Task.Title+"」")
		}
	}
	return res, nil
}

// ClaimNext 從全部啟用中角色的待領取任務挑最優先的一筆領走(agentID 非空限定角色)
func (o AgentOps) ClaimNext(org, agentID string) (*service.ClaimResult, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	res, err := b.ClaimNext(org, agentID)
	if err != nil {
		return nil, err
	}
	if res.Task != nil {
		o.done(org)
		if res.Agent != nil {
			o.logf(AI(res.Agent.ID), "figure.walk", res.Agent.Name+"開始處理 "+res.Task.ID+"「"+res.Task.Title+"」")
		}
	}
	return res, nil
}

// ReleaseTask 放棄領取:退回待領取
func (o AgentOps) ReleaseTask(org, id, reason string) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	t, err := b.ReleaseTask(org, id, reason)
	if err != nil {
		return nil, err
	}
	o.done(org)
	o.logf(AI(t.AgentID), "arrow.uturn.backward", "放棄領取 "+t.ID+"「"+t.Title+"」,退回待領取")
	return t, nil
}

// Whoami 呼叫者的署名(同領取任務寫進 ClaimedBy 的值);本機是 ops-web,遠端是雲端帳號的 email
func (o AgentOps) Whoami() string {
	b, err := o.be()
	if err != nil {
		return ""
	}
	return b.Whoami()
}

func (o AgentOps) ReportTask(org, id string, in service.TaskReport) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	was := ""
	if t, err := b.GetAgentTask(org, id); err == nil {
		was = t.Status
	}
	t, err := b.ReportTask(org, id, in)
	if err != nil {
		return nil, err
	}
	o.done(org)
	if in.Status != nil && *in.Status != was {
		name := o.agentName(org, t.AgentID)
		switch *in.Status {
		case "done":
			o.logf(AI(t.AgentID), "checkmark.circle", name+"完成 "+t.ID+"「"+t.Title+"」")
		case "blocked":
			o.logf(AI(t.AgentID), "hand.raised", name+"在 "+t.ID+"「"+t.Title+"」需要協助")
		}
	}
	return t, nil
}

// WorkerReport 領取者(本機是 ops-web,遠端是雲端帳號)以角色身分回報自己領取的任務(/mcp/worker 的 dispatch_report)
func (o AgentOps) WorkerReport(org, id, code string, in service.TaskReport) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	t, err := b.WorkerReport(org, id, code, in)
	if err != nil {
		return nil, err
	}
	o.done(org)
	if in.Status != nil {
		name := o.agentName(org, t.AgentID)
		switch *in.Status {
		case "done":
			o.logf(AI(t.AgentID), "checkmark.circle", name+"完成 "+t.ID+"「"+t.Title+"」")
		case "blocked":
			o.logf(AI(t.AgentID), "hand.raised", name+"在 "+t.ID+"「"+t.Title+"」需要協助")
		}
	}
	return t, nil
}

// WorkerComment 領取者以角色身分在 Issue 留言(/mcp/worker 的 dispatch_comment);寫完重讀 Issue 給畫面
func (o AgentOps) WorkerComment(org, taskID, code, issueID, comment string) (*model.Issue, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	i, err := b.WorkerComment(org, taskID, code, issueID, comment)
	if err != nil {
		return nil, err
	}
	o.a.Mu.Lock()
	o.a.ReloadIssues()
	o.a.Mu.Unlock()
	go o.a.Changed("issues")
	return i, nil
}

func (o AgentOps) CancelTask(org, id string) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	t, err := b.CancelTask(org, id)
	if err == nil {
		o.done(org)
	}
	return t, err
}

func (o AgentOps) RequeueTask(org, id string) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	t, err := b.RequeueTask(org, id)
	if err == nil {
		o.done(org)
	}
	return t, err
}

func (o AgentOps) RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	t, err := b.RunRoutine(org, agentID, routineID, author)
	if err != nil {
		return nil, err
	}
	o.done(org)
	o.logAssigned(org, *t)
	return t, nil
}

// AgentsChanged 核心的角色資料被別的路徑改過(角色端點、Issue 指派、例行任務排程):重讀目前組織並通知畫面。不要拿著 Mu
func (a *App) AgentsChanged() {
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	if org != "" {
		a.agentRefresh(org)
	}
	a.Changed("agents")
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
		return "請以 omixor 的虛擬角色「" + g.Name + "」(id: " + g.ID + ")處理任務 " + task.ID + ":用 ops_agent_task_claim 領取,照角色設定與任務說明完成,過程用 ops_agent_task_report 回報,做完把結果回報成 done;開 Issue 或留言時帶 agent: " + g.ID + "。"
	}
	return "請以 omixor 的虛擬角色「" + g.Name + "」(id: " + g.ID + ")工作:用 ops_agent_task_claim 依序領取它的待領取任務,照角色設定與任務說明完成並用 ops_agent_task_report 回報,直到沒有任務為止;開 Issue 或留言時帶 agent: " + g.ID + "。"
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
		// 複製目前組織:角色(不含任務)搬到新組織的核心。本機核心才在這裡做(遠端模式的組織在雲端,不在 Mu 之內打網路)
		if template != "copyCurrent" || IsRemote() {
			return
		}
		be, err := agentBackend()
		if err != nil {
			return
		}
		newOrg := filepath.Base(dir)
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
		n := 0
		for _, t := range a.AgentTasks() {
			if t.Status == "blocked" {
				n++
			}
		}
		return map[string]int{"automation": n}
	})
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		// 例行任務排程(本機核心:每 60 秒;遠端模式由雲端自己排)
		go func() {
			for {
				if !IsRemote() {
					if l := localCore(); l != nil {
						if created, err := l.svc.RunDueRoutines(time.Now()); err != nil {
							fmt.Fprintf(os.Stderr, "例行任務排程:%v\n", err)
						} else if len(created) > 0 {
							a.AgentsChanged()
						}
					}
				}
				time.Sleep(60 * time.Second)
			}
		}()
		// 每 5 秒重讀目前組織的角色與任務(別的路徑改了資料、遠端的修改才看得到;網路與資料庫請求都在鎖外)
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
		saved, err := a.AgentOps().SaveAgent(a.currentOrgNow(), g)
		if err != nil {
			return nil, agentUserErr(err)
		}
		return saved, nil
	})
	RegisterAction("agent_delete", func(a *App, args map[string]any) (any, error) {
		return nil, agentUserErr(a.AgentOps().DeleteAgent(a.currentOrgNow(), agentArg(args, "id")))
	})
	RegisterAction("agent_task_cancel", func(a *App, args map[string]any) (any, error) {
		t, err := a.AgentOps().CancelTask(a.currentOrgNow(), agentArg(args, "id"))
		return t, agentUserErr(err)
	})
	RegisterAction("agent_task_requeue", func(a *App, args map[string]any) (any, error) {
		t, err := a.AgentOps().RequeueTask(a.currentOrgNow(), agentArg(args, "id"))
		return t, agentUserErr(err)
	})
	RegisterAction("agent_routine_run", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		org, me := a.CurrentOrgID(), a.Me
		a.Mu.Unlock()
		_, err := a.AgentOps().RunRoutine(org, agentArg(args, "agent"), agentArg(args, "routine"), me)
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
			if t = a.FindTask(id); t == nil {
				return nil, UserError{"找不到任務 " + id}
			}
		}
		return map[string]any{"text": AgentPrompt(*g, t)}, nil
	})
}

// agentUserErr 核心的錯誤轉成畫面看得懂的 UserError(nil 原樣回傳)
func agentUserErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(UserError); ok {
		return err
	}
	return UserError{err.Error()}
}
