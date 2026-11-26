package core

import (
	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 角色與任務的寫入(AgentOps);讀取與快取見 agents.go,頻道見 channel.go,組織鏈見 chain.go

// ---- 寫:交給核心,成功後更新快取、寫動態、通知畫面 ----

// AgentOps 角色與任務的寫入(實作 backend.Backend 的角色部分)。呼叫時不要拿著 Mu。
// 讀取(ListAgents 等)直接問核心,不用快取。
type AgentOps struct {
	a *App
	// who 網頁的呼叫者;nil = 系統身分(背景工作、自動執行、MCP 3765)
	who *Caller
}

func (a *App) AgentOps() AgentOps { return AgentOps{a: a} }

// AgentOpsFor 以網頁呼叫者的身分寫入(who 是 nil 同 AgentOps)
func (a *App) AgentOpsFor(who *Caller) AgentOps { return AgentOps{a: a, who: who} }

// be 寫入(與對話頻道)用的核心:呼叫者在 org 的身分
func (o AgentOps) be(org string) (backend.Backend, error) { return coreBackendFor(o.who, "", org) }

// read 角色、任務、組織鏈的讀取:有呼叫者用他的身分(任務會帶受限專案 Issue 的內容),否則系統身分
func (o AgentOps) read(org string) (backend.Backend, error) {
	if o.who != nil {
		return o.be(org)
	}
	return agentBackend()
}

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
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.ListAgents(org)
}

func (o AgentOps) GetAgent(org, id string) (*model.Agent, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.GetAgent(org, id)
}

func (o AgentOps) SaveAgent(org string, in model.Agent) (*model.Agent, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	g, err := b.SaveAgent(org, in)
	if err == nil {
		o.done(org)
	}
	return g, err
}

func (o AgentOps) DeleteAgent(org, id string) error { return o.DeleteAgentTo(org, id, "") }

// DeleteAgentTo 刪角色;名下有任務時 transferTo 空 = 409(核心的 *service.Error,Data 有各狀態的任務數),不空 = 先轉給它再刪
func (o AgentOps) DeleteAgentTo(org, id, transferTo string) error {
	b, err := o.be(org)
	if err != nil {
		return err
	}
	if err = b.DeleteAgentTo(org, id, transferTo); err == nil {
		o.done(org)
	}
	return err
}

// TransferAgentTasks 不刪角色的單獨轉移(scope:all 全部 | open 只轉待領取與需要協助,空 = open)
func (o AgentOps) TransferAgentTasks(org string, in service.AgentTransferInput) (*service.AgentTransferResult, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	res, err := b.TransferAgentTasks(org, in)
	if err == nil {
		o.done(org)
	}
	return res, err
}

func (o AgentOps) ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.ListAgentTasks(org, f)
}

func (o AgentOps) GetAgentTask(org, id string) (*model.AgentTask, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.GetAgentTask(org, id)
}

func (o AgentOps) AssignTask(org string, in service.TaskInput) (*model.AgentTask, error) {
	b, err := o.be(org)
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
	b, err := o.be(org)
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
	b, err := o.be(org)
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
	b, err := o.be(org)
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

// Whoami 呼叫者的署名(同領取任務寫進 ClaimedBy 的值);本機是 omixor-console,遠端是雲端帳號的 email
func (o AgentOps) Whoami() string {
	b, err := o.be("")
	if err != nil {
		return ""
	}
	return b.Whoami()
}

func (o AgentOps) ReportTask(org, id string, in service.TaskReport) (*model.AgentTask, error) {
	b, err := o.be(org)
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

// WorkerReport 領取者(本機是 omixor-console,遠端是雲端帳號)以角色身分回報自己領取的任務(/mcp/worker 的 dispatch_report)
func (o AgentOps) WorkerReport(org, id, code string, in service.TaskReport) (*model.AgentTask, error) {
	b, err := o.be(org)
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
	b, err := o.be(org)
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
	b, err := o.be(org)
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
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	t, err := b.RequeueTask(org, id)
	if err == nil {
		o.done(org)
	}
	return t, err
}

func (o AgentOps) ReplyTask(org, id, text string) (*model.AgentTask, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	t, err := b.ReplyTask(org, id, text)
	if err == nil {
		o.done(org)
	}
	return t, err
}

func (o AgentOps) RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error) {
	b, err := o.be(org)
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
	if a.parent != nil { // 一次性 App 可能已經丟掉:只通知
		a.Changed("agents")
		return
	}
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	if org != "" {
		a.agentRefresh(org)
	}
	a.Changed("agents")
}
