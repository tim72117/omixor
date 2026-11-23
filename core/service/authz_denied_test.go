package service_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// (c) 依操作表自動產生「未授權被拒」測試(ISS-33 第 1 階段,不改行為)。
//
// 每個操作用四種呼叫者各試一次(每個操作都從全新的資料開始,免得放行的差異污染後面的案例):
//   member    組織 o1 的一般成員(不是管理員、不是任務領取者、不是頻道成員、組織鏈上沒有邊)
//   ext       只屬於組織 o2 的成員(對 o1 是外部人)
//   agent     組織 o1 角色 cto 的角色憑證(綁定自己領取的那筆任務;探測的資源是別人的)
//   extAgent  組織 o2 角色的角色憑證
// 預期被拒的組合由表推導(deniedFor);預期 403(頻道看不到的資源回 404 不洩漏存在,也接受)。
// 實際行為與表不符時不改行為:登記在 knownDenyGaps(預期被拒卻放行)或 knownDenyCodes(有拒絕但狀態碼不是 403),附原因;
// 白名單的項目已經修好(行為變正確)時,測試會要求把它拿掉。

type who string

const (
	wMember   who = "member"
	wExt      who = "ext"
	wAgent    who = "agent"
	wExtAgent who = "extAgent"
)

var allWho = []who{wMember, wExt, wAgent, wExtAgent}

type authzEnv struct {
	t         *testing.T
	svc       *service.Service
	boss      *model.Principal // o1 組織管理員
	alice     *model.Principal // o1 一般成員
	eve       *model.Principal // 只屬於 o2 的成員
	zed       *model.Principal // o2 管理員
	agent     *model.Principal // o1 cto 的角色憑證(綁 task)
	extAgent  *model.Principal // o2 xa 的角色憑證
	task      string           // agent 綁定的任務(boss 領取)
	other     string           // o1 pm 的任務(未領取)
	issue     string
	doc       string
	project   string
	channel   string // boss 建的頻道(alice 與 agent 都不是成員)
	incident  string
	decision  string // boss 提的待決定(開放中)
	decided   string // boss 提出並處理過的待決定
	item      string // boss 建立並領取中的 Issue 子項(Issue 是 e.issue)
	claimCode string // task 的領取驗證碼
}

func (e *authzEnv) pr(w who) *model.Principal {
	switch w {
	case wMember:
		return e.alice
	case wExt:
		return e.eve
	case wAgent:
		return e.agent
	}
	return e.extAgent
}

func ign[T any](_ T, err error) error { return err }

func newAuthzEnv(t *testing.T) *authzEnv {
	t.Helper()
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.CreateOrg(model.Org{ID: "o1", Name: "組織一", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "o2", Name: "組織二", CreatedAt: now})
	for _, u := range []struct{ id, email, org, role string }{
		{"u1", "boss@x", "o1", "admin"}, {"u2", "alice@x", "o1", "member"}, {"u3", "eve@x", "o2", "member"}, {"u4", "zed@x", "o2", "admin"},
	} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		_ = st.SaveMember(model.Member{Org: u.org, UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
	}
	mk := func(id, email string) *model.Principal {
		return &model.Principal{User: model.User{ID: id, Email: email, Role: "member"}}
	}
	e := &authzEnv{t: t, svc: svc, boss: mk("u1", "boss@x"), alice: mk("u2", "alice@x"), eve: mk("u3", "eve@x"), zed: mk("u4", "zed@x")}
	chk := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長", Active: true}, {ID: "pm", Name: "產品經理", Active: true}} {
		chk(ign(svc.SaveAgent(e.boss, "o1", g)))
	}
	chk(ign(svc.SaveAgent(e.zed, "o2", model.Agent{ID: "xa", Name: "外部角色", Active: true})))
	claim := func(admin *model.Principal, org, agent string) (*model.AgentTask, *model.Principal, string) {
		a, err := svc.AssignTask(admin, org, service.TaskInput{AgentID: agent, Title: "做事"})
		chk(err)
		res, err := svc.ClaimTask(admin, org, service.ClaimInput{AgentID: agent, TaskID: a.ID})
		chk(err)
		p, err := svc.AuthenticateAgent(agent, res.Credential.Secret, "ip")
		chk(err)
		return res.Task, p, res.Credential.Code
	}
	tk, ap, code := claim(e.boss, "o1", "cto")
	e.task, e.agent, e.claimCode = tk.ID, ap, code
	_, e.extAgent, _ = claim(e.zed, "o2", "xa")
	ot, err := svc.AssignTask(e.boss, "o1", service.TaskInput{AgentID: "pm", Title: "別人的任務"})
	chk(err)
	e.other = ot.ID
	iss, err := svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "一個 Issue"})
	chk(err)
	e.issue = iss.ID
	prj, err := svc.CreateProject(e.boss, "o1", service.ProjectInput{Name: "專案", Key: "APP"})
	chk(err)
	e.project = prj.ID
	d, err := svc.CreateDoc(e.boss, "o1", service.DocInput{Title: "文件", Section: "docs", Body: "# a\n"})
	chk(err)
	e.doc = d.ID
	ch, err := svc.CreateChannel(e.boss, "o1", service.ChannelInput{Name: "c1"})
	chk(err)
	e.channel = ch.ID
	inc, err := svc.DeclareIncident(e.boss, "o1", service.IncidentInput{Title: "事件", Sev: 3})
	chk(err)
	e.incident = inc.ID
	opts := []model.DecisionOption{{Label: "A"}, {Label: "B"}}
	dc, err := svc.AddDecision(e.boss, "o1", service.DecisionInput{Title: "待決定", Options: opts})
	chk(err)
	e.decision = dc.ID
	dd, err := svc.AddDecision(e.boss, "o1", service.DecisionInput{Title: "處理過的", Options: opts})
	chk(err)
	chk(ign(svc.AnswerDecision(e.boss, "o1", dd.ID, service.DecisionAnswer{Answer: "o1"})))
	e.decided = dd.ID
	it, err := svc.AddItem(e.boss, "o1", e.issue, service.ItemInput{Title: "子項"})
	chk(err)
	e.item = it.ID
	_, err = svc.ClaimItem(e.boss, "o1", e.issue, e.item, service.ItemClaimInput{})
	chk(err)
	return e
}

func str(s string) *string { return &s }

// authzProbes 每個操作的探測呼叫:用呼叫者 p 對「別人的資源」做這個操作(參數合法,失敗原因只該是授權)
var authzProbes = map[string]func(e *authzEnv, p *model.Principal) error{
	"auth.pair.create": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.CreatePairCode(p)) },
	"org.list":         func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListOrgs(p)) },
	"org.create": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.CreateOrgWith(p, model.Org{ID: "o9", Name: "新組織"}))
	},
	"org.update": func(e *authzEnv, p *model.Principal) error {
		return e.svc.UpdateOrg(p, model.Org{ID: "o1", Name: "改名"})
	},
	"org.delete": func(e *authzEnv, p *model.Principal) error { return e.svc.DeleteOrg(p, "o1") },
	"user.list":  func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListUsers(p)) },
	"user.save": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.SaveUser(p, "new@x.com", nil, nil))
	},
	"token.list":           func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListTokens(p)) },
	"token.revoke":         func(e *authzEnv, p *model.Principal) error { return e.svc.RevokeToken(p, "t_none") },
	"schema.check":         func(e *authzEnv, p *model.Principal) error { return ign(e.svc.SchemaCheck(p)) },
	"audit.list":           func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListAudit(p, 10)) },
	"server.features.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetServerFeatures(p)) },
	"server.features.save": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.SaveServerFeatures(p, service.SystemSettingsInput{Features: map[string]bool{}}))
	},
	"member.list": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListMembers(p, "o1")) },
	"member.save": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.SaveMember(p, "o1", "new@x.com", "member"))
	},
	"member.remove": func(e *authzEnv, p *model.Principal) error { return e.svc.RemoveMember(p, "o1", "alice@x") },
	"actor.list": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ListOrgActors(p, "o1", service.ActorListFilter{}))
	},
	"attachment.save": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.SaveAttachment(p, "o1", []byte("x"))) },
	"attachment.get": func(e *authzEnv, p *model.Principal) error {
		_, _, err := e.svc.GetAttachment(p, "o1", "att_none")
		return err
	},

	"issue.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListIssues(p, "o1")) },
	"issue.create": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.CreateIssue(p, "o1", service.IssueInput{Title: "新的"}))
	},
	"issue.update": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.UpdateIssue(p, "o1", e.issue, service.IssuePatch{Title: str("改")}))
	},
	"issue.comment": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.AddIssueComment(p, "o1", e.issue, service.CommentInput{Text: "留言"}))
	},
	"issue.accept":     func(e *authzEnv, p *model.Principal) error { return ign(e.svc.AcceptIssue(p, "o1", e.issue)) },
	"issue.release":    func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ReleaseIssue(p, "o1", e.issue)) },
	"issue.delete":     func(e *authzEnv, p *model.Principal) error { return e.svc.DeleteIssue(p, "o1", e.issue) },
	"issue.trash.list": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListDeletedIssues(p, "o1")) },
	"issue.restore":    func(e *authzEnv, p *model.Principal) error { return ign(e.svc.RestoreIssue(p, "o1", e.issue)) },
	"issue.purge":      func(e *authzEnv, p *model.Principal) error { return e.svc.PurgeIssue(p, "o1", e.issue) },
	"issue.export":     func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ExportIssues(p, "o1")) },
	"issue.import":     func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ImportIssues(p, "o1", nil)) },

	"agent.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetAgent(p, "o1", "pm")) },
	"agent.save": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.SaveAgent(p, "o1", model.Agent{ID: "zz", Name: "新角色"}))
	},
	"agent.delete": func(e *authzEnv, p *model.Principal) error { return e.svc.DeleteAgent(p, "o1", "pm") },
	"agent.import": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ImportAgents(p, "o1", service.AgentImport{}))
	},
	"agent.routine.run": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.RunRoutine(p, "o1", "pm", "r1", ""))
	},
	"agent.task.transfer": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.TransferAgentTasks(p, "o1", service.AgentTransferInput{From: "cto", To: "pm"}))
	},
	"agent.task.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetAgentTask(p, "o1", e.other)) },
	"agent.task.create": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.AssignTask(p, "o1", service.TaskInput{AgentID: "pm", Title: "派工"}))
	},
	"agent.task.claim": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ClaimTask(p, "o1", service.ClaimInput{AgentID: "pm", TaskID: e.other}))
	},
	"agent.task.release": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ReleaseTask(p, "o1", e.task, "x")) },
	"agent.task.report": func(e *authzEnv, p *model.Principal) error {
		id := e.task // 角色憑證探測「不是自己綁定的」那一筆
		if p == e.agent {
			id = e.other
		}
		return ign(e.svc.ReportTask(p, "o1", id, service.TaskReport{Note: str("進度")}))
	},
	"agent.task.cancel":  func(e *authzEnv, p *model.Principal) error { return ign(e.svc.CancelTask(p, "o1", e.other)) },
	"agent.task.requeue": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.RequeueTask(p, "o1", e.other)) },
	"agent.task.delegate": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.DelegateTask(p, "o1", service.DelegateInput{To: "pm", Title: "委派"}))
	},
	"agent.task.worker.report": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.WorkerReport(p, "o1", e.task, e.claimCode, service.TaskReport{Note: str("進度")}))
	},
	"agent.task.worker.comment": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.WorkerComment(p, "o1", e.task, e.claimCode, e.issue, "留言"))
	},
	"agent.task.worker.delegate": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.WorkerDelegate(p, "o1", e.task, e.claimCode, service.DelegateInput{To: "pm", Title: "委派"}))
	},

	"cabinet.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetCabinet(p, "o1", "")) },
	"doc.read":     func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetDoc(p, "o1", e.doc)) },
	"doc.create": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.CreateDoc(p, "o1", service.DocInput{Title: "新文件", Section: "docs"}))
	},
	"doc.update": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.UpdateDoc(p, "o1", e.doc, service.DocPatch{Title: str("改")}))
	},
	"doc.delete": func(e *authzEnv, p *model.Principal) error { return e.svc.DeleteDoc(p, "o1", e.doc) },
	"cabinet.layout.save": func(e *authzEnv, p *model.Principal) error {
		_, _, err := e.svc.SaveCabinetLayoutIf(p, "o1", "", []model.CabinetSection{{ID: "docs", Label: "文件"}}, nil)
		return err
	},
	"cabinet.import": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ImportCabinet(p, "o1", service.CabinetImport{}))
	},
	"project.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListProjects(p, "o1")) },
	"project.create": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.CreateProject(p, "o1", service.ProjectInput{Name: "新專案"}))
	},
	"project.update": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.UpdateProject(p, "o1", e.project, service.ProjectPatch{Name: str("改")}))
	},
	"project.delete": func(e *authzEnv, p *model.Principal) error { return e.svc.DeleteProject(p, "o1", e.project) },
	"project.member.list": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ListProjectMembers(p, "o1", e.project))
	},
	"project.member.set": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.SetProjectMember(p, "o1", e.project, service.ProjectMemberInput{Actor: "boss@x", Role: "member"}))
	},
	"project.member.remove": func(e *authzEnv, p *model.Principal) error {
		return e.svc.RemoveProjectMember(p, "o1", e.project, "boss@x")
	},
	"project.repo.list": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ListProjectRepos(p, "o1", e.project))
	},
	"project.repo.link": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.LinkProjectRepo(p, "o1", e.project, "acme/app"))
	},
	"project.repo.unlink": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.UnlinkProjectRepo(p, "o1", e.project, "acme/app"))
	},

	"chain.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetChain(p, "o1")) },
	"chain.save": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.SaveChain(p, "o1", []model.ChainEdge{})) },

	"channel.list": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ListChannels(p, "o1", service.ChannelFilter{}))
	},
	"channel.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetChannel(p, "o1", e.channel, "")) },
	"channel.create": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.CreateChannel(p, "o1", service.ChannelInput{Name: "新頻道"}))
	},
	"channel.update": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.UpdateChannel(p, "o1", e.channel, service.ChannelPatch{Topic: str("改")}))
	},
	"channel.members": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ChangeChannelMembers(p, "o1", e.channel, service.ChannelMembersInput{Remove: []string{"agent:pm"}}))
	},
	"channel.messages": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ListChannelMessages(p, "o1", e.channel, service.MessageListInput{}))
	},
	"channel.post": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.PostChannelMessage(p, "o1", e.channel, service.PostInput{Text: "嗨"}))
	},
	"channel.markRead": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.MarkChannelRead(p, "o1", e.channel, service.ReadInput{}))
	},
	"channel.inbox": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ChannelInbox(p, "o1", "pm")) },

	"incident.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListIncidents(p, "o1")) },
	"incident.declare": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.DeclareIncident(p, "o1", service.IncidentInput{Title: "新事件", Sev: 3}))
	},
	"incident.update": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.UpdateIncident(p, "o1", e.incident, service.IncidentPatch{Note: str("備註")}))
	},
	"incident.import": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ImportIncidents(p, "o1", nil)) },

	"decision.add": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.AddDecision(p, "o1", service.DecisionInput{Title: "新的", Options: []model.DecisionOption{{Label: "A"}, {Label: "B"}}}))
	},
	"decision.list": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ListDecisions(p, "o1", service.DecisionListQuery{}))
	},
	"decision.get":     func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetDecision(p, "o1", e.decision)) },
	"decision.summary": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.DecisionSummaries(p, "o1")) },
	"decision.answer": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.AnswerDecision(p, "o1", e.decision, service.DecisionAnswer{Answer: "o1"}))
	},
	"decision.reopen": func(e *authzEnv, p *model.Principal) error { // 探測 boss 處理過的:一般成員不是管理員也不是原處理人
		return ign(e.svc.ReopenDecision(p, "o1", e.decided))
	},
	"decision.withdraw": func(e *authzEnv, p *model.Principal) error { // 探測 boss 提的:一般成員、角色憑證都不是建立者
		return ign(e.svc.WithdrawDecision(p, "o1", e.decision))
	},
	"decision.to_issue": func(e *authzEnv, p *model.Principal) error {
		_, _, err := e.svc.DecisionToIssue(p, "o1", e.decision, service.DecisionToIssueInput{})
		return err
	},

	// Issue 子項:探測 boss 建立並領取中的子項 e.item(一般成員不是領取者也不是建立者、角色憑證沒綁定它)
	"issue.item.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetItem(p, "o1", e.issue, e.item)) },
	"issue.item.add": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.AddItem(p, "o1", e.issue, service.ItemInput{Title: "新子項"}))
	},
	"issue.item.update": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.UpdateItem(p, "o1", e.issue, e.item, service.ItemPatch{Title: str("改")}))
	},
	"issue.item.claim": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ClaimItem(p, "o1", e.issue, e.item, service.ItemClaimInput{}))
	},
	"issue.item.release": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ReleaseItem(p, "o1", e.issue, e.item)) },
	"issue.item.report": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ReportItem(p, "o1", e.issue, e.item, service.ItemReport{Status: "doing"}))
	},
	"issue.item.block": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.BlockItem(p, "o1", e.issue, e.item, service.ItemReport{BlockReason: "卡住"}))
	},
	"issue.item.unblock": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.UnblockItem(p, "o1", e.issue, e.item)) },
	"issue.item.cancel":  func(e *authzEnv, p *model.Principal) error { return ign(e.svc.CancelItem(p, "o1", e.issue, e.item)) },
	"issue.item.promote": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.PromoteItem(p, "o1", e.issue, e.item, service.ItemPromoteInput{}))
	},
	"issue.item.delete": func(e *authzEnv, p *model.Principal) error { return e.svc.DeleteItem(p, "o1", e.issue, e.item) },

	"mcp.gateway.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetMCP(p, "o1")) },
	"mcp.gateway.save": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.SaveMCPServer(p, "o1", service.MCPServerInput{ID: "s1", Name: str("伺服器")}))
	},
	"system.settings.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.GetSystemSettings(p, "o1")) },
	"system.settings.save": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.SaveSystemSettings(p, "o1", service.SystemSettingsInput{}))
	},
	"template.read": func(e *authzEnv, p *model.Principal) error { return ign(e.svc.ListOrgTemplates(p, "o1")) },
	"template.apply": func(e *authzEnv, p *model.Principal) error {
		return ign(e.svc.ApplyOrgTemplate(p, "o1", "x", service.TemplateApplyInput{}))
	},
}

// noProbe 沒有自動探測的操作與原因
var noProbe = map[string]string{
	"auth.logout": "只作用於呼叫者自己的 token,沒有可被拒絕的情境(角色憑證到不了這個端點)",
}

// agentSelfOther 角色憑證「只限自己」的操作:探測的是別人的資源(別的角色、別人的任務、自己不在的頻道、別人的收件匣),預期被拒
var agentSelfOther = map[string]bool{
	"agent.read": true, "agent.task.read": true, "agent.task.report": true,
	"channel.read": true, "channel.messages": true, "channel.post": true, "channel.markRead": true, "channel.inbox": true,
	"decision.withdraw": true,                           // 角色憑證只能撤回自己提的;探測的是 boss 提的
	"issue.item.report": true, "issue.item.block": true, // 角色憑證只能回報自己綁定任務的子項;探測的是 boss 領取的
}

// channelNeedsMembership 一般成員不是頻道成員時,看不到或不能操作(回 404 或 403)
var channelNeedsMembership = map[string]bool{
	"channel.read": true, "channel.update": true, "channel.members": true, "channel.messages": true, "channel.post": true, "channel.markRead": true,
}

// deniedFor 由表推導:這個呼叫者對這個操作,預期被拒嗎
func deniedFor(o service.AuthzOp, w who) bool {
	adminLevel := o.Level == service.LvOrgAdmin || o.Level == service.LvServerAdmin
	switch w {
	case wMember:
		return adminLevel || o.Agent == service.AgentOnly || hasCap(o, service.CapClaimer) || hasCap(o, service.CapChainEdge) || hasCap(o, service.CapProjectAdmin) ||
			hasCap(o, service.CapOwnerOrAdmin) || channelNeedsMembership[o.ID]
	case wExt:
		return o.Level != service.LvAuthenticated
	case wAgent:
		return adminLevel || o.Agent == service.AgentNo || hasCap(o, service.CapDownstream) || agentSelfOther[o.ID]
	}
	return true // 外部組織的角色憑證:任何操作都不行
}

// knownDenyGaps 預期被拒卻放行的已知差異(漏檢),key = "<操作>/<呼叫者>"。不改行為,只登記;修好後要拿掉
var knownDenyGaps = map[string]string{
	"org.list/agent":    "低風險:ListOrgs 沒有拒絕分支,角色憑證在 service 層呼叫得到(回「角色 user id 的成員組織」= 空清單,無資料外洩);REST 端點只收 omx_ token,角色憑證到不了",
	"org.list/extAgent": "同 org.list/agent",
}

// knownDenyCodes 有被拒絕,但狀態碼不是 403(或頻道的 404),key 同上,值 = 原因
var knownDenyCodes = map[string]string{
	"agent.read/agent":      "角色憑證讀別的角色回 404「找不到角色」(不洩漏存在),不是 403;表上寫 self",
	"agent.task.read/agent": "角色憑證讀別人的任務回 404「找不到任務」(不洩漏存在),不是 403;表上寫 self",
}

func TestAuthzDeniedMatrix(t *testing.T) {
	usedGap, usedCode := map[string]bool{}, map[string]bool{}
	var shared *authzEnv
	pt := t
	var report []string
	for _, o := range service.AuthzOps {
		o := o
		probe := probes(o.ID)
		if probe == nil {
			if r := noProbe[o.ID]; r == "" {
				t.Errorf("操作 %s 沒有探測(authzProbes)也不在 noProbe(寫明原因)", o.ID)
			}
			continue
		}
		t.Run(o.ID, func(t *testing.T) {
			for _, w := range allWho {
				if !deniedFor(o, w) {
					continue
				}
				// 資料庫與資料共用,直到有案例放行(可能改了資料)才重建;被拒的呼叫不改資料
				if shared == nil {
					shared = newAuthzEnv(pt)
				}
				e := shared
				err := probe(e, e.pr(w))
				if err == nil {
					shared = nil
				}
				key := o.ID + "/" + string(w)
				code := 0
				if err != nil {
					code = service.Code(err)
				}
				okCode := code == 403 || (code == 404 && hasCap(o, service.CapChannel))
				switch {
				case err == nil:
					if _, known := knownDenyGaps[key]; known {
						usedGap[key] = true
						report = append(report, "放行 "+key)
					} else {
						t.Errorf("漏檢:%s 預期被拒(表:level=%s agent=%s caps=%v),實際放行", key, o.Level, o.Agent, o.Caps)
					}
				case okCode:
					if _, known := knownDenyGaps[key]; known {
						t.Errorf("knownDenyGaps[%s] 已修好(實際 %d),請從白名單移除", key, code)
					}
					if _, known := knownDenyCodes[key]; known {
						t.Errorf("knownDenyCodes[%s] 已修好(實際 %d),請從白名單移除", key, code)
					}
				default:
					if _, known := knownDenyCodes[key]; known {
						usedCode[key] = true
						report = append(report, fmt.Sprintf("狀態碼 %s=%d", key, code))
					} else {
						t.Errorf("%s 預期 403,實際 %d:%v", key, code, err)
					}
				}
			}
		})
	}
	for k := range knownDenyGaps {
		if !usedGap[k] && !t.Failed() {
			t.Errorf("knownDenyGaps[%s] 沒有對應到任何案例(操作改名或不再預期被拒?)", k)
		}
	}
	for k := range knownDenyCodes {
		if !usedCode[k] && !t.Failed() {
			t.Errorf("knownDenyCodes[%s] 沒有對應到任何案例", k)
		}
	}
	sort.Strings(report)
	if len(report) > 0 {
		t.Logf("已知差異(%d):\n  %s", len(report), strings.Join(report, "\n  "))
	}
}

func probes(id string) func(e *authzEnv, p *model.Principal) error { return authzProbes[id] }

// 每個操作的探測都登記了(沒登記也沒寫 noProbe 原因就失敗);探測的操作名都在表裡
func TestAuthzProbesMatchTable(t *testing.T) {
	for id := range authzProbes {
		if _, ok := service.AuthzOpByID(id); !ok {
			t.Errorf("探測 %s 不在操作表", id)
		}
	}
	for id := range noProbe {
		if _, ok := service.AuthzOpByID(id); !ok {
			t.Errorf("noProbe %s 不在操作表", id)
		}
	}
}

// 已確認仍存在的漏檢(docs/authorization.md §4.2):這個測試「確認它們現在還在」,不改行為。
// 第 2 階段補上某一項後,對應的斷言會失敗,屆時把這一項改成「被拒」的正向測試並從文件的漏檢清單移除。
func TestAuthzKnownGapsStillPresent(t *testing.T) {
	e := newAuthzEnv(t)
	svc := e.svc

	// R-4:事件對角色憑證開放(MCP 工具表不開,REST 與 service 開)
	if _, err := svc.DeclareIncident(e.agent, "o1", service.IncidentInput{Title: "角色宣告", Sev: 3}); err != nil {
		t.Error("R-4 現況:角色憑證可宣告事件(若失敗代表已收緊,請同步工具表):", err)
	}

	// R-10:一般成員可刪 Issue 與文件
	if err := svc.DeleteIssue(e.alice, "o1", e.issue); err != nil {
		t.Error("R-10 現況:一般成員可刪 Issue:", err)
	}
	if err := svc.DeleteDoc(e.alice, "o1", e.doc); err != nil {
		t.Error("R-10 現況:一般成員可刪文件:", err)
	}
}

// R-3(已修):Issue 的 assignee 與 AssignTask / DelegateTask 一樣受組織鏈約束。
// 沒設定組織鏈(沒有任何邊)維持原行為;設定之後成員要有 user→agent 的邊、角色憑證要有 agent→agent 的邊;組織管理員不受限
func TestAuthzIssueAssigneeFollowsChain(t *testing.T) {
	e := newAuthzEnv(t)
	svc := e.svc
	pmTasks := func(title string) int {
		l, err := svc.ListAgentTasks(e.boss, "o1", service.TaskFilter{AgentID: "pm"})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, x := range l {
			if strings.Contains(x.Title, title) {
				n++
			}
		}
		return n
	}
	// 沒設組織鏈:維持原行為,成員與角色憑證都能指派
	if _, err := svc.CreateIssue(e.alice, "o1", service.IssueInput{Title: "無鏈成員", Assignee: "pm"}); err != nil {
		t.Fatal("沒設組織鏈時成員要能指派:", err)
	}
	// 設定組織鏈:alice → cto(只這一條)
	if _, err := svc.SaveChain(e.boss, "o1", edges("user:alice@x>agent:cto>研發")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateIssue(e.alice, "o1", service.IssueInput{Title: "成員繞過鏈", Assignee: "pm"}); code(err) != 403 {
		t.Fatal("成員建立時指派鏈外的角色要 403:", err)
	}
	if _, err := svc.UpdateIssue(e.alice, "o1", e.issue, service.IssuePatch{Assignee: str("pm")}); code(err) != 403 {
		t.Fatal("成員改指派鏈外的角色要 403:", err)
	}
	if _, err := svc.UpdateIssue(e.agent, "o1", e.issue, service.IssuePatch{Assignee: str("pm")}); code(err) != 403 {
		t.Fatal("角色憑證(cto)沒有 cto→pm 的邊,改 assignee 要 403:", err)
	}
	if _, err := svc.CreateIssue(e.agent, "o1", service.IssueInput{Title: "角色繞過鏈", Assignee: "pm"}); code(err) != 403 {
		t.Fatal("角色憑證建立時指派鏈外的角色要 403:", err)
	}
	if n := pmTasks("成員繞過鏈") + pmTasks("角色繞過鏈") + pmTasks("一個 Issue"); n != 0 {
		t.Fatalf("被擋下的指派不能替 pm 排任務:%d", n)
	}
	if got, _ := svc.GetIssue(e.boss, "o1", e.issue); got.Assignee != "" {
		t.Fatalf("被擋之後指派不能變:%+v", got)
	}
	// 鏈上有的對象可以;組織管理員不受限
	if _, err := svc.CreateIssue(e.alice, "o1", service.IssueInput{Title: "鏈上", Assignee: "cto"}); err != nil {
		t.Fatal("鏈上有 alice→cto 要能指派:", err)
	}
	if _, err := svc.UpdateIssue(e.boss, "o1", e.issue, service.IssuePatch{Assignee: str("pm")}); err != nil {
		t.Fatal("組織管理員不受組織鏈限制:", err)
	}
	if pmTasks("一個 Issue") != 1 {
		t.Error("組織管理員指派應替 pm 排了任務")
	}
}

// R-9(已修):組織管理員只能把「已存在的伺服器使用者」加進組織;要建立新使用者(SaveUser)只有伺服器管理員
func TestAuthzSaveMemberNeedsExistingUser(t *testing.T) {
	e := newAuthzEnv(t)
	svc := e.svc
	if _, err := svc.SaveUser(e.boss, "invited@x.com", nil, nil); code(err) != 403 {
		t.Fatalf("前提:組織管理員不能 SaveUser:%v", err)
	}
	if _, err := svc.SaveMember(e.boss, "o1", "invited@x.com", "member"); code(err) != 403 {
		t.Fatal("組織管理員加不存在的 email 要 403:", err)
	}
	if u, err := svc.Store.UserByEmail("invited@x.com"); err == nil && u != nil {
		t.Fatalf("被擋之後不能留下伺服器使用者:%+v", u)
	}
	// 已存在的使用者(在別的組織)可以加進來
	if m, err := svc.SaveMember(e.boss, "o1", "eve@x", "member"); err != nil || m.Email != "eve@x" {
		t.Fatal("已存在的使用者要能加入:", m, err)
	}
	// 伺服器管理員仍可替新 email 建立使用者並加入
	sa := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "admin"}}
	if _, err := svc.SaveMember(sa, "o1", "invited@x.com", "member"); err != nil {
		t.Fatal("伺服器管理員可以加新 email:", err)
	}
	if u, err := svc.Store.UserByEmail("invited@x.com"); err != nil || u == nil {
		t.Fatal("伺服器管理員加新 email 要建立使用者:", err)
	}
}

// R-8(已修):沒有任何 docAccess 的角色憑證看不到專案(與文件櫃一致:清單是空的,單取 403);有 docAccess 才看得到
func TestAuthzProjectNeedsDocAccess(t *testing.T) {
	e := newAuthzEnv(t)
	svc := e.svc
	if g, err := svc.GetAgent(e.boss, "o1", "cto"); err != nil || len(g.DocAccess) != 0 {
		t.Fatalf("前提:cto 沒有 docAccess:%v %+v", err, g)
	}
	if l, err := svc.ListProjects(e.agent, "o1"); err != nil || len(l) != 0 {
		t.Fatalf("沒有 docAccess 的角色列專案要是空的:%v %+v", err, l)
	}
	if _, err := svc.GetProject(e.agent, "o1", e.project); code(err) != 403 {
		t.Fatal("沒有 docAccess 的角色取專案要 403:", err)
	}
	if l, err := svc.ListProjects(e.alice, "o1"); err != nil || len(l) != 1 {
		t.Fatalf("成員不受影響:%v %+v", err, l)
	}
	g, _ := svc.GetAgent(e.boss, "o1", "cto")
	g.DocAccess = map[string]string{"docs": "read"}
	if _, err := svc.SaveAgent(e.boss, "o1", *g); err != nil {
		t.Fatal(err)
	}
	if l, err := svc.ListProjects(e.agent, "o1"); err != nil || len(l) != 1 {
		t.Fatalf("有 docAccess 的角色看得到專案:%v %+v", err, l)
	}
	if _, err := svc.GetProject(e.agent, "o1", e.project); err != nil {
		t.Fatal("有 docAccess 的角色取專案要成功:", err)
	}
}
