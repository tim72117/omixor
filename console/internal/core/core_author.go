package core

import (
	"strings"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 署名:console 呼叫核心(本機內建核心或遠端 omixor server)時,「誰」由登入身分決定(ISS-36 C-5):
//   - 本機模式:操作者就是本機使用者 omixor-console(伺服器管理員),Principal.Actor 已填
//   - 遠端模式:server 依 token 解出 Principal
//
// 所以對核心傳的 author、as、commander 只可能是「角色」(agent:<id>)或系統(routine、chain、system);
// 人員 id(範例資料的 tim、mcp、ops-web、omixor-console、email…)一律不傳(空 = 呼叫者本人),
// 不然核心的嚴格解析器會回 400(不存在的使用者)或 403(別的使用者,D9)。

// CoreAuthor 把 console 內部的作者字串換成可以傳給核心的署名:角色(agent:<id>)原樣;系統署名(routine、chain、omixor、system)
// 換成核心嚴格解析器認得的系統 actor id(sys_routine、sys_chain、sys_omixor;系統 actor 只能用 id);其餘(人員 id、mcp、本機身分)是空字串 = 呼叫者本人
func CoreAuthor(author string) string {
	a := strings.TrimSpace(author)
	switch {
	case strings.HasPrefix(strings.ToLower(a), AgentPrefix):
		return a
	case a == RoutineAuthor:
		return model.SystemActorRoutine
	case a == model.ChainAuthor:
		return model.SystemActorChain
	case a == model.SystemAuthor, a == "system":
		return model.SystemActorOmixor
	}
	return ""
}

// corePerson 只收「人或角色」的欄位(頻道的 as、author、member,事件指揮官):角色(agent:<id>)原樣,其餘(含系統署名)是空字串 = 呼叫者本人
// (核心對這些欄位的嚴格解析器不收系統 actor,會回 400)
func corePerson(author string) string {
	a := strings.TrimSpace(author)
	if strings.HasPrefix(strings.ToLower(a), AgentPrefix) {
		return a
	}
	return ""
}

// coreIncidentActor 事件的執行者:角色(ai、id 是角色)原樣,人與 MCP 不傳(核心用呼叫者本人)
func coreIncidentActor(x *model.IncidentActor) *model.IncidentActor {
	if x == nil || x.Kind != "ai" || x.ID == "" || x.ID == MCPActorID {
		return nil
	}
	return x
}

// authorBackend 包著核心的 backend.Backend,只把會帶署名的呼叫換成 CoreAuthor 之後的值;其他方法原樣轉給核心
type authorBackend struct{ backend.Backend }

// rawBackend 去掉 authorBackend 的包裝(要判斷是不是本機核心時用)
func rawBackend(b backend.Backend) backend.Backend {
	if w, ok := b.(authorBackend); ok {
		return w.Backend
	}
	return b
}

func (b authorBackend) CreateIssue(org string, in service.IssueInput) (*model.Issue, error) {
	in.Author = CoreAuthor(in.Author)
	return b.Backend.CreateIssue(org, in)
}

func (b authorBackend) UpdateIssue(org, id string, p service.IssuePatch) (*model.Issue, error) {
	p.Author = CoreAuthor(p.Author)
	if p.Comments != nil { // 整份取代的留言:作者是本機人員別名的換成本機使用者,其餘(核心輸出的 email、agent:<id>)原樣讓核心解析
		cs := make([]model.Comment, len(*p.Comments))
		for k, c := range *p.Comments {
			c.Author = coreNode(c.Author)
			cs[k] = c
		}
		p.Comments = &cs
	}
	return b.Backend.UpdateIssue(org, id, p)
}

func (b authorBackend) AddIssueComment(org, id, text, author string) (*model.Issue, error) {
	return b.Backend.AddIssueComment(org, id, text, CoreAuthor(author))
}

func (b authorBackend) RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error) {
	return b.Backend.RunRoutine(org, agentID, routineID, CoreAuthor(author))
}

func (b authorBackend) AssignTask(org string, in service.TaskInput) (*model.AgentTask, error) {
	in.Author = CoreAuthor(in.Author)
	return b.Backend.AssignTask(org, in)
}

func (b authorBackend) CreateDoc(org string, in service.DocInput) (*model.CabinetDoc, error) {
	in.Author = CoreAuthor(in.Author)
	return b.Backend.CreateDoc(org, in)
}

func (b authorBackend) DeclareIncident(org string, in service.IncidentInput) (*model.Incident, error) {
	in.Author, in.Actor = CoreAuthor(in.Author), coreIncidentActor(in.Actor)
	in.Commander = corePerson(in.Commander)
	return b.Backend.DeclareIncident(org, in)
}

func (b authorBackend) UpdateIncident(org, id string, p service.IncidentPatch) (*model.Incident, error) {
	p.Author, p.Actor = CoreAuthor(p.Author), coreIncidentActor(p.Actor)
	if p.Commander != nil {
		c := corePerson(*p.Commander)
		p.Commander = &c
	}
	return b.Backend.UpdateIncident(org, id, p)
}

func (b authorBackend) PostChannelMessage(org, id string, in service.PostInput) (*model.ChannelMessage, error) {
	in.As, in.Author = corePerson(in.As), corePerson(in.Author)
	return b.Backend.PostChannelMessage(org, id, in)
}

// coreNode 組織鏈端點、頻道成員:本機人員別名(tim、mcp、ops-web、omixor-console)換成本機使用者的 handle,
// 其餘(角色 agent:<id>、其他使用者)原樣,由核心的嚴格解析器判斷
func coreNode(s string) string {
	t := strings.TrimSpace(s)
	x := strings.TrimPrefix(strings.TrimPrefix(t, model.UserPrefix), model.LocalPrefix)
	if strings.HasPrefix(strings.ToLower(t), AgentPrefix) {
		return t
	}
	if x == LocalIdentity {
		return t
	}
	for _, al := range LocalAliases() {
		if strings.EqualFold(x, al) {
			return model.UserPrefix + LocalIdentity
		}
	}
	return t
}

func coreNodes(l []string) []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l))
	for k, s := range l {
		out[k] = coreNode(s)
	}
	return out
}

func (b authorBackend) SaveChain(org string, edges []model.ChainEdge) (*model.OrgChain, error) {
	list := make([]model.ChainEdge, len(edges))
	for k, e := range edges {
		e.From, e.To = coreNode(e.From), coreNode(e.To)
		list[k] = e
	}
	return b.Backend.SaveChain(org, list)
}

func (b authorBackend) ListChannels(org string, f service.ChannelFilter) ([]model.ChannelSummary, error) {
	f.As = corePerson(f.As)
	return b.Backend.ListChannels(org, f)
}

func (b authorBackend) GetChannel(org, id, as string) (*model.ChannelSummary, error) {
	return b.Backend.GetChannel(org, id, corePerson(as))
}

func (b authorBackend) CreateChannel(org string, in service.ChannelInput) (*model.ChannelSummary, error) {
	in.As, in.Members = corePerson(in.As), coreNodes(in.Members)
	return b.Backend.CreateChannel(org, in)
}

func (b authorBackend) UpdateChannel(org, id string, in service.ChannelPatch) (*model.ChannelSummary, error) {
	in.As = corePerson(in.As)
	return b.Backend.UpdateChannel(org, id, in)
}

func (b authorBackend) ChangeChannelMembers(org, id string, in service.ChannelMembersInput) (*model.ChannelSummary, error) {
	in.As, in.Add, in.Remove = corePerson(in.As), coreNodes(in.Add), coreNodes(in.Remove)
	return b.Backend.ChangeChannelMembers(org, id, in)
}

func (b authorBackend) ListChannelMessages(org, id string, in service.MessageListInput) (*service.MessagePage, error) {
	in.As = corePerson(in.As)
	return b.Backend.ListChannelMessages(org, id, in)
}

func (b authorBackend) MarkChannelRead(org, id string, in service.ReadInput) (*model.ChannelRead, error) {
	in.As, in.Member = corePerson(in.As), corePerson(in.Member)
	return b.Backend.MarkChannelRead(org, id, in)
}

func (b authorBackend) ChannelInbox(org, who string) (*service.ChannelInbox, error) {
	return b.Backend.ChannelInbox(org, coreNode(who)) // 本機人員別名 = 本機使用者(呼叫者本人)
}
