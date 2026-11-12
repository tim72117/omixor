package mcp

import (
	"encoding/json"
	"errors"

	"omixor-core/backend"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 組織、Issue、虛擬角色與任務、文件櫃的工具(ops_orgs、ops_issue_*、ops_agent*、ops_cabinet、ops_docs、ops_doc_save)在 omixor-core/mcp,
// 透過核心的共同介面 backend.Backend 操作。這裡提供 web 的實作(appBackend):
//   - 組織、Issue:包著 web 的 core.App(組織清單、Issue 快取、動態牆、與核心 service/client 的同步),
//     讓 MCP 工具的副作用和畫面操作完全一樣
//   - 角色與任務:交給 App.AgentOps(本機內建核心或遠端 omixor server),寫入後更新快取、寫動態、通知畫面
//   - 文件櫃:交給 App.CabinetOps(同上;見 tools_cabinet.go)

func init() {
	coremcp.RegisterOrgTools(srv)
	coremcp.RegisterIssueTools(srv)
	coremcp.RegisterAgentTools(srv)
	coremcp.RegisterCabinetTools(srv)
}

// toolErr 把 core.UserError 轉成工具錯誤
func userToolErr(err error) error {
	if ue, ok := err.(core.UserError); ok {
		return ToolError{Message: ue.Message}
	}
	return err
}

// issueAuthor Issue 的署名:有給 agent 就是該角色,否則是呼叫者
func issueAuthor(c *Call) (string, error) {
	return issueAuthorOf(c.App, c.Actor, c.Args.Str("agent"))
}

func issueAuthorOf(a *core.App, actor core.Actor, name string) (string, error) {
	if name == "" {
		return core.ActorAuthor(actor), nil
	}
	g := a.FindAgent(name)
	if g == nil {
		return "", Errorf("找不到角色 %s", name)
	}
	return core.AgentPrefix + g.ID, nil
}

// ---- coremcp.Env / coremcp.AgentEnv ----

func (x *callCtx) Lock()   { x.App.Mu.Lock(); x.locked = true }
func (x *callCtx) Unlock() { x.locked = false; x.App.Mu.Unlock() }

// withMu 在拿著 App.Mu 的情況下執行 f:已經拿著(Issue 工具)就直接執行,否則先鎖
func (x *callCtx) withMu(f func()) {
	if x.locked {
		f()
		return
	}
	x.App.Mu.Lock()
	defer x.App.Mu.Unlock()
	f()
}

func (x *callCtx) Backend() backend.Backend { return appBackend{a: x.App} }
func (x *callCtx) Org() (org string) {
	x.withMu(func() { org = x.App.CurrentOrgID() })
	return
}
func (x *callCtx) Author(agent string) (author string, err error) {
	x.withMu(func() { author, err = issueAuthorOf(x.App, x.Actor, agent) })
	return
}
func (x *callCtx) AuthorName(author string) (name string) {
	x.withMu(func() { name = x.App.IssueAuthorName(author) })
	return
}

// 管理用的 /mcp 沒有角色限制
func (x *callCtx) Role() *coremcp.Role { return nil }
func (x *callCtx) OrgName() (name string) {
	x.withMu(func() { name = cabOrgName(x.App) })
	return
}
func (x *callCtx) AgentName(id string) (name string) {
	x.withMu(func() {
		name = id
		if g := x.App.FindAgent(id); g != nil {
			name = g.Name
		}
	})
	return
}

// ContextExtra 文件櫃的欄位(cabinet、docs)核心已經附上,web 沒有別的要加
func (x *callCtx) ContextExtra(model.Agent, bool) map[string]any { return nil }
func (x *callCtx) EndpointURL() (u string) {
	x.withMu(func() { u = x.App.AgentEndpointURL() })
	return
}

var _ coremcp.AgentEnv = (*callCtx)(nil)

// docAccess 的顯示(換成看得懂的文件櫃範圍)與 ops_agent_upsert 的 doc_access key 解析都由核心依文件櫃結構處理

// appBackend 以 core.App 實作 backend.Backend:組織與 Issue 的部分包 web 的狀態(呼叫時要拿著 App.Mu,MCP 工具會先 Lock),
// 角色與任務的部分交給 App.AgentOps(不要拿著 Mu)。組織 id 參數:Issue 忽略(一律是目前的組織),角色與任務照給的用。
type appBackend struct {
	backend.Unsupported
	a *core.App
}

func (b appBackend) ListOrgs() ([]model.Org, error) {
	var out []model.Org
	for _, o := range b.a.Orgs().Orgs {
		out = append(out, model.Org{ID: o.ID, Name: o.Name, Note: o.Note})
	}
	return out, nil
}

func (b appBackend) ListIssues(string) ([]model.Issue, error) {
	b.a.RefreshIssuesIfStale(0) // Issue 伺服器模式:用最新的
	return toModels(b.a.Issues()), nil
}

func (b appBackend) GetIssue(_, id string) (*model.Issue, error) {
	b.a.RefreshIssuesIfStale(0)
	i := b.a.Issue(id)
	if i == nil {
		return nil, &service.Error{Code: 404, Msg: "找不到 Issue " + id}
	}
	m := toModels([]core.OpsIssue{*i})
	return &m[0], nil
}

func (b appBackend) CreateIssue(_ string, in service.IssueInput) (*model.Issue, error) {
	issue := b.a.CreateIssueFor(in.Title, in.Body, in.Priority, in.Labels, in.Source, in.Link, in.Author, in.Assignee)
	if issue.ID == "" { // Issue 伺服器連不上
		return nil, errors.New(b.a.IssueRemoteError())
	}
	m := toModels([]core.OpsIssue{issue})
	return &m[0], nil
}

func (b appBackend) UpdateIssue(_, id string, p service.IssuePatch) (*model.Issue, error) {
	a := b.a
	updated := a.UpdateIssue(id, p.Author, func(i *core.OpsIssue) {
		if p.Title != nil {
			i.Title = *p.Title
		}
		if p.Body != nil {
			i.Body = *p.Body
		}
		if p.Priority != nil {
			i.Priority = *p.Priority
		}
		if p.Labels != nil {
			i.Labels = *p.Labels
		}
		if p.Status != nil {
			i.Status = *p.Status
		}
		if p.Assignee != nil {
			i.Assignee = *p.Assignee
		}
	})
	// 新增的留言走 CommentIssue(和畫面一樣:空白不做事、記到動態牆)
	if updated != nil && p.Comments != nil && len(*p.Comments) > len(updated.Comments) {
		for _, c := range (*p.Comments)[len(updated.Comments):] {
			updated = a.CommentIssue(id, c.Text, c.Author)
			if updated == nil {
				break
			}
		}
	}
	if updated == nil {
		if e := a.IssueRemoteError(); e != "" {
			return nil, errors.New(e)
		}
		return nil, &service.Error{Code: 404, Msg: "找不到 Issue " + id}
	}
	m := toModels([]core.OpsIssue{*updated})
	return &m[0], nil
}

// toModels web 的 Issue 轉成核心的(JSON 格式相同)
func toModels(l []core.OpsIssue) []model.Issue {
	b, _ := json.Marshal(l)
	out := []model.Issue{}
	_ = json.Unmarshal(b, &out)
	return out
}

// ---- 角色與任務:交給 App.AgentOps ----

func (b appBackend) ListAgents(org string) ([]model.Agent, error) {
	return b.a.AgentOps().ListAgents(org)
}
func (b appBackend) GetAgent(org, id string) (*model.Agent, error) {
	return b.a.AgentOps().GetAgent(org, id)
}
func (b appBackend) SaveAgent(org string, in model.Agent) (*model.Agent, error) {
	return b.a.AgentOps().SaveAgent(org, in)
}
func (b appBackend) DeleteAgent(org, id string) error { return b.a.AgentOps().DeleteAgent(org, id) }
func (b appBackend) ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error) {
	return b.a.AgentOps().ListAgentTasks(org, f)
}
func (b appBackend) GetAgentTask(org, id string) (*model.AgentTask, error) {
	return b.a.AgentOps().GetAgentTask(org, id)
}
func (b appBackend) AssignTask(org string, in service.TaskInput) (*model.AgentTask, error) {
	return b.a.AgentOps().AssignTask(org, in)
}
func (b appBackend) ClaimTask(org string, in service.ClaimInput) (*service.ClaimResult, error) {
	return b.a.AgentOps().ClaimTask(org, in)
}
func (b appBackend) ClaimNext(org, agentID string) (*service.ClaimResult, error) {
	return b.a.AgentOps().ClaimNext(org, agentID)
}
func (b appBackend) ReleaseTask(org, id, reason string) (*model.AgentTask, error) {
	return b.a.AgentOps().ReleaseTask(org, id, reason)
}
func (b appBackend) Whoami() string { return b.a.AgentOps().Whoami() }
func (b appBackend) ReportTask(org, id string, in service.TaskReport) (*model.AgentTask, error) {
	return b.a.AgentOps().ReportTask(org, id, in)
}
func (b appBackend) WorkerReport(org, id, code string, in service.TaskReport) (*model.AgentTask, error) {
	return b.a.AgentOps().WorkerReport(org, id, code, in)
}
func (b appBackend) WorkerComment(org, taskID, code, issueID, comment string) (*model.Issue, error) {
	return b.a.AgentOps().WorkerComment(org, taskID, code, issueID, comment)
}
func (b appBackend) CancelTask(org, id string) (*model.AgentTask, error) {
	return b.a.AgentOps().CancelTask(org, id)
}
func (b appBackend) RequeueTask(org, id string) (*model.AgentTask, error) {
	return b.a.AgentOps().RequeueTask(org, id)
}
func (b appBackend) RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error) {
	return b.a.AgentOps().RunRoutine(org, agentID, routineID, author)
}
