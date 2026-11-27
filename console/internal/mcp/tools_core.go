package mcp

import (
	"errors"
	"strings"

	"omixor-core/backend"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 組織、Issue、虛擬角色與任務、文件櫃的工具(orgs、issue_*、agent*、cabinet、docs、doc_save)在 omixor-core/mcp,
// 透過核心的共同介面 backend.Backend 操作。這裡提供 web 的實作(appBackend):
//   - 組織、Issue:包著 web 的 core.App(組織清單、Issue 快取、動態牆、與核心 service/client 的同步),
//     讓 MCP 工具的副作用和畫面操作完全一樣
//   - 角色與任務:交給 App.AgentOps(內建核心),寫入後更新快取、寫動態、通知畫面
//   - 文件櫃:交給 App.CabinetOps(同上;見 tools_cabinet.go)

func init() {
	coremcp.RegisterOrgTools(srv)
	coremcp.RegisterSchemaTools(srv) // schema_check(見 tools_schema.go)
	coremcp.RegisterIssueTools(srv)
	coremcp.RegisterAgentTools(srv)
	coremcp.RegisterCabinetTools(srv)
	coremcp.RegisterChainTools(srv)
	coremcp.RegisterChannelTools(srv)
	coremcp.RegisterDecisionTools(srv) // issue_decision_add、issue_decisions、issue_decision_answer、issue_decision_to_issue(見 core/mcp/tools_decision.go)
	coremcp.RegisterItemTools(srv)     // issue_item_add、issue_items、issue_item_update、issue_item_claim、issue_item_report、issue_item_promote(見 core/mcp/tools_issue_items.go、tools_issue_items.go)
	coremcp.RegisterCommitTools(srv)   // commits_report、commits、issue_commit_link、issue_commit_unlink(見 core/mcp/tools_commits.go、tools_commits.go)
	coremcp.RegisterIncidentTools(srv) // incidents、incident_declare、incident_update(見 tools_incident.go)
}

// toolErr 把 core.UserError 轉成工具錯誤
func userToolErr(err error) error {
	if ue, ok := err.(core.UserError); ok {
		return ToolError{Message: ue.Message}
	}
	return err
}

// issueAuthor 署名:一律是呼叫者(人 = 使用者 actor;MCP 用戶端 = MCPActor)。不能指定角色身分(見 identity.go)
func issueAuthor(c *Call) (string, error) {
	if err := rejectIdentity("", c.Args.Raw); err != nil {
		return "", err
	}
	return core.ActorAuthor(c.Actor), nil
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

func (x *callCtx) Backend() backend.Backend {
	return appBackend{a: x.App, who: x.who, hidden: x.hidden}
}
func (x *callCtx) Org() (org string) {
	x.withMu(func() { org = x.App.CurrentOrgID() })
	return
}
func (x *callCtx) Author(agent string) (author string, err error) {
	if strings.TrimSpace(agent) != "" { // 核心工具傳進來的 agent:本機不允許(Run 與 Guard 已先擋,這裡是最後一道)
		return "", ToolError{Message: coremcp.IdentityAgentError, Status: 400}
	}
	x.withMu(func() { author = core.ActorAuthor(x.Actor) })
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

// docAccess 的顯示(換成看得懂的文件櫃範圍)與 agent_upsert 的 doc_access key 解析都由核心依文件櫃結構處理

// appBackend 以 core.App 實作 backend.Backend:組織與 Issue 的部分包 web 的狀態(呼叫時要拿著 App.Mu,MCP 工具會先 Lock),
// 角色與任務的部分交給 App.AgentOps(不要拿著 Mu)。組織 id 參數:Issue 忽略(一律是目前的組織),角色與任務照給的用。
type appBackend struct {
	backend.Unsupported
	a *core.App
	// who 網頁 /api/tool 的呼叫者;nil = 系統身分(角色與執行者端點)
	who *core.Caller
	// hidden 網頁非管理員看不到的受限專案資料(在核心工具算 total 之前拿掉)
	hidden *core.Hidden
}

// agentOps 角色、任務、組織鏈、頻道的操作(寫入與頻道用呼叫者身分)
func (b appBackend) agentOps() core.AgentOps { return b.a.AgentOpsFor(b.who) }

func (b appBackend) ListOrgs() ([]model.Org, error) {
	out := []model.Org{}
	for _, o := range b.a.VisibleOrgs(b.who).Orgs { // 網頁的非管理員只看得到自己的組織
		out = append(out, model.Org{ID: o.ID, Name: o.Name, Note: o.Note})
	}
	return out, nil
}

func (b appBackend) ListIssues(string) ([]model.Issue, error) {
	b.a.RefreshIssuesIfStale(0) // Issue 伺服器模式:用最新的
	return copyIssues(b.hidden.Issues(b.a.Issues())), nil
}

func (b appBackend) GetIssue(_, id string) (*model.Issue, error) {
	b.a.RefreshIssuesIfStale(0)
	i := b.a.Issue(id)
	if i == nil || b.hidden.Project(i.Project) { // 看不到的受限專案:錯誤和不存在一模一樣
		return nil, &service.Error{Code: 404, Msg: "找不到 Issue " + id}
	}
	c := copyIssue(*i)
	return &c, nil
}

func (b appBackend) CreateIssue(_ string, in service.IssueInput) (*model.Issue, error) {
	var issue model.Issue
	// in.Project:放進的專案(空 = 組織層級);專案不存在或沒有代號時核心回錯,原因在 IssueRemoteErr
	b.a.AsCaller(b.who, func() { issue = b.a.CreateIssueIn(in) })
	if issue.ID == "" { // Issue 伺服器連不上,或核心拒絕(專案不存在、沒有代號…)
		if e := b.a.IssueRemoteErr(); e != nil {
			return nil, e // 原型別帶回去(*service.Error 的狀態碼與 Data)
		}
		return nil, errors.New(b.a.IssueRemoteError())
	}
	c := copyIssue(issue)
	return &c, nil
}

func (b appBackend) UpdateIssue(_, id string, p service.IssuePatch) (out *model.Issue, err error) {
	b.a.AsCaller(b.who, func() { out, err = b.updateIssue(id, p) })
	return
}

func (b appBackend) updateIssue(id string, p service.IssuePatch) (*model.Issue, error) {
	a := b.a
	appendBody := ""
	if p.AppendBody != nil {
		appendBody = *p.AppendBody
	}
	// 只改 patch 有給的欄位;UpdateIssueAtVersion 只把有變的欄位送給核心,append_body 交給核心在 Issue 的鎖裡附加;
	// p.Version 有給就用它做樂觀鎖(核心版本不同回 409,不會蓋掉別人先做的修改)
	version := 0
	if p.Version != nil {
		version = *p.Version
	}
	updated := a.UpdateIssueAtVersion(id, p.Author, appendBody, version, func(i *model.Issue) {
		if p.Project != nil {
			i.Project = strings.TrimSpace(*p.Project) // 搬專案(核心發新的 key,舊的留在別名表)
		}
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
			if strings.TrimSpace(i.Assignee) == "" {
				i.AssigneeActor = nil // 明確取消指派:人類接受者只有 assigneeActor,要清掉才看得出有變(issuePatchFor 會送 assignee="")
			}
		}
		if p.Repo != nil {
			i.Repo = strings.TrimSpace(*p.Repo)
		}
	})
	// 給了整份留言(核心的工具已經不這樣做,留著相容):比現有多的那幾則一則一則走只附加的路徑
	if updated != nil && p.Comments != nil && len(*p.Comments) > len(updated.Comments) {
		for _, c := range (*p.Comments)[len(updated.Comments):] {
			updated = a.CommentIssue(id, c.Text, c.Author)
			if updated == nil {
				break
			}
		}
	}
	if updated == nil {
		if e := a.IssueRemoteErr(); e != nil {
			return nil, e // 原型別帶回去:409 版本衝突的 Data(current、currentVersion)才出得去
		}
		return nil, &service.Error{Code: 404, Msg: "找不到 Issue " + id}
	}
	c := copyIssue(*updated)
	return &c, nil
}

// SupportsIssueAppendBody UpdateIssue 認得 IssuePatch.AppendBody(交給核心在鎖裡附加),核心的 issue_update 會改送 AppendBody
func (appBackend) SupportsIssueAppendBody() bool { return true }

// AddIssueComment 新增一則留言:App.AddIssueComment 走核心的只附加路徑
func (b appBackend) AddIssueComment(_, id, text, author string) (*model.Issue, error) {
	var i *model.Issue
	var err error
	b.a.AsCaller(b.who, func() { i, err = b.a.AddIssueComment(id, text, author) })
	if err != nil {
		return nil, err
	}
	c := copyIssue(*i)
	return &c, nil
}

var _ coremcp.IssueAppendBodySupporter = appBackend{}

// copyIssue、copyIssues 給核心工具一份拷貝(App 快取裡的清單不會被改到)
func copyIssue(i model.Issue) model.Issue {
	i.Labels = append([]string{}, i.Labels...)
	i.Comments = append([]model.Comment{}, i.Comments...)
	if i.ClosedAt != nil {
		t := *i.ClosedAt
		i.ClosedAt = &t
	}
	return i
}

func copyIssues(l []model.Issue) []model.Issue {
	out := make([]model.Issue, 0, len(l))
	for _, i := range l {
		out = append(out, copyIssue(i))
	}
	return out
}

// ---- 角色與任務:交給 App.AgentOps ----

func (b appBackend) ListAgents(org string) ([]model.Agent, error) {
	return b.agentOps().ListAgents(org)
}
func (b appBackend) GetAgent(org, id string) (*model.Agent, error) {
	return b.agentOps().GetAgent(org, id)
}
func (b appBackend) SaveAgent(org string, in model.Agent) (*model.Agent, error) {
	return b.agentOps().SaveAgent(org, in)
}
func (b appBackend) DeleteAgent(org, id string) error { return b.agentOps().DeleteAgent(org, id) }
func (b appBackend) DeleteAgentTo(org, id, transferTo string) error {
	return b.agentOps().DeleteAgentTo(org, id, transferTo)
}
func (b appBackend) TransferAgentTasks(org string, in service.AgentTransferInput) (*service.AgentTransferResult, error) {
	return b.agentOps().TransferAgentTasks(org, in)
}
func (b appBackend) ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error) {
	list, err := b.agentOps().ListAgentTasks(org, f)
	if err != nil {
		return nil, err
	}
	return b.hidden.Tasks(list), nil
}
func (b appBackend) GetAgentTask(org, id string) (*model.AgentTask, error) {
	t, err := b.agentOps().GetAgentTask(org, id)
	if err == nil && t != nil && b.hidden.Task(*t) {
		return nil, &service.Error{Code: 404, Msg: "找不到任務 " + id}
	}
	return t, err
}
func (b appBackend) AssignTask(org string, in service.TaskInput) (*model.AgentTask, error) {
	return b.agentOps().AssignTask(org, in)
}
func (b appBackend) ClaimTask(org string, in service.ClaimInput) (*service.ClaimResult, error) {
	return b.agentOps().ClaimTask(org, in)
}
func (b appBackend) ClaimNext(org, agentID string) (*service.ClaimResult, error) {
	return b.agentOps().ClaimNext(org, agentID)
}
func (b appBackend) ReleaseTask(org, id, reason string) (*model.AgentTask, error) {
	return b.agentOps().ReleaseTask(org, id, reason)
}
func (b appBackend) Whoami() string { return b.agentOps().Whoami() }
func (b appBackend) ReportTask(org, id string, in service.TaskReport) (*model.AgentTask, error) {
	return b.agentOps().ReportTask(org, id, in)
}
func (b appBackend) WorkerReport(org, id, code string, in service.TaskReport) (*model.AgentTask, error) {
	return b.agentOps().WorkerReport(org, id, code, in)
}
func (b appBackend) WorkerComment(org, taskID, code, issueID, comment string) (*model.Issue, error) {
	return b.agentOps().WorkerComment(org, taskID, code, issueID, comment)
}
func (b appBackend) CancelTask(org, id string) (*model.AgentTask, error) {
	return b.agentOps().CancelTask(org, id)
}
func (b appBackend) RequeueTask(org, id string) (*model.AgentTask, error) {
	return b.agentOps().RequeueTask(org, id)
}
func (b appBackend) RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error) {
	return b.agentOps().RunRoutine(org, agentID, routineID, author)
}

// ---- 組織鏈:交給 App.AgentOps ----

func (b appBackend) GetChain(org string) (*model.OrgChain, error) {
	return b.agentOps().GetChain(org)
}
func (b appBackend) SaveChain(org string, edges []model.ChainEdge) (*model.OrgChain, error) {
	return b.agentOps().SaveChain(org, edges)
}
func (b appBackend) DelegateTask(org string, in service.DelegateInput) (*model.AgentTask, error) {
	return b.agentOps().DelegateTask(org, in)
}
func (b appBackend) WorkerDelegate(org, taskID, code string, in service.DelegateInput) (*model.AgentTask, error) {
	return b.agentOps().WorkerDelegate(org, taskID, code, in)
}

// ---- 訊息頻道:交給 App.AgentOps ----

func (b appBackend) ListChannels(org string, f service.ChannelFilter) ([]model.ChannelSummary, error) {
	return b.agentOps().ListChannels(org, f)
}
func (b appBackend) GetChannel(org, id, as string) (*model.ChannelSummary, error) {
	return b.agentOps().GetChannel(org, id, as)
}
func (b appBackend) CreateChannel(org string, in service.ChannelInput) (*model.ChannelSummary, error) {
	return b.agentOps().CreateChannel(org, in)
}
func (b appBackend) UpdateChannel(org, id string, in service.ChannelPatch) (*model.ChannelSummary, error) {
	return b.agentOps().UpdateChannel(org, id, in)
}
func (b appBackend) ChangeChannelMembers(org, id string, in service.ChannelMembersInput) (*model.ChannelSummary, error) {
	return b.agentOps().ChangeChannelMembers(org, id, in)
}
func (b appBackend) ListChannelMessages(org, id string, in service.MessageListInput) (*service.MessagePage, error) {
	return b.agentOps().ListChannelMessages(org, id, in)
}
func (b appBackend) PostChannelMessage(org, id string, in service.PostInput) (*model.ChannelMessage, error) {
	return b.agentOps().PostChannelMessage(org, id, in)
}
func (b appBackend) MarkChannelRead(org, id string, in service.ReadInput) (*model.ChannelRead, error) {
	return b.agentOps().MarkChannelRead(org, id, in)
}
func (b appBackend) ChannelInbox(org, who string) (*service.ChannelInbox, error) {
	return b.agentOps().ChannelInbox(org, who)
}
