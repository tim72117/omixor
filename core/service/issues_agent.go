package service

// issues_agent.go 角色憑證(p.Agent != nil,含執行者代角色)對 Issue 的寫入限制(ISS-7):
//
//   - 看得到的 Issue 都可以留言(AddIssueComment;IssuePatch.Comments 對非伺服器管理員本來就只能新增)
//   - 接受(issue_accept):沒人負責的 Issue 也只能接受「自己的」(自己建立、或目前任務連到),見 checkAgentAccept;
//     否則角色可以靠接受任何沒人認領的 Issue 變成負責人,繞過下面的限制
//   - 改狀態、AppendBody:只限「自己的」Issue——指派給自己、憑證綁定的任務連到它、或自己建立的
//   - 指派:只能改「自己的」Issue,而且組織鏈上要有 agent:<自己> → agent:<目標> 的邊(建立時指派同樣要有邊;
//     沒設定組織鏈也一樣擋,見 checkIssueAssign);不能清掉指派(放回自己接受的用 issue_accept 的 release)
//   - 標題、內文、優先度、標籤、專案、倉庫、來源、連結:不能改
//   - 給的值和目前一樣不算修改
//
// 錯誤訊息照服務層慣例是中文,另帶機器碼(Data["code"]),MCP 依機器碼換成英文(mcp/tools_issues.go)。

import (
	"errors"
	"slices"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

const (
	// AgentIssueForbiddenCode 角色改了不能改的 Issue 欄位(Data["fields"] 是被擋的欄位)
	AgentIssueForbiddenCode = "agentIssueForbidden"
	// AgentAssignForbiddenCode 角色指派給組織鏈上沒有邊的對象
	AgentAssignForbiddenCode = "agentAssignForbidden"
	// AgentAcceptForbiddenCode 角色接受不是自己的 Issue
	AgentAcceptForbiddenCode = "agentAcceptForbidden"
)

// agentPatchFields patch 真正會改到的欄位:never = 角色一律不能改;ownOnly = 只有自己的 Issue 可以改。
// in 的 Repo、Assignee、Project 要已經換成存放的寫法(UpdateIssue 開頭做的)
func agentPatchFields(cur *model.Issue, in IssuePatch) (never, ownOnly []string) {
	differs := func(v *string, old string) bool { return v != nil && *v != old }
	add := func(list *[]string, name string, on bool) {
		if on {
			*list = append(*list, name)
		}
	}
	add(&never, "title", differs(in.Title, cur.Title))
	add(&never, "body", differs(in.Body, cur.Body))
	add(&never, "priority", differs(in.Priority, cur.Priority))
	add(&never, "labels", in.Labels != nil && !slices.Equal(model.CleanLabels(*in.Labels), model.CleanLabels(cur.Labels)))
	add(&never, "project", differs(in.Project, cur.Project))
	add(&never, "repo", differs(in.Repo, cur.Repo))
	add(&never, "source", differs(in.Source, cur.Source))
	add(&never, "link", differs(in.Link, cur.Link))
	reassign := differs(in.Assignee, cur.Assignee)
	add(&never, "assignee", reassign && *in.Assignee == "")
	add(&ownOnly, "assignee", reassign && *in.Assignee != "")
	add(&ownOnly, "status", differs(in.Status, cur.Status))
	add(&ownOnly, "append_body", in.AppendBody != nil && *in.AppendBody != "")
	return never, ownOnly
}

// checkAgentPatch 角色憑證修改 Issue 的欄位限制;人不受限。拿著 issueLock 呼叫(只讀 Store)
func (s *Service) checkAgentPatch(p *model.Principal, org string, cur *model.Issue, in IssuePatch) error {
	if p.Agent == nil {
		return nil
	}
	denied, ownOnly := agentPatchFields(cur, in)
	if len(ownOnly) > 0 {
		own, err := s.agentOwnsIssue(p, org, cur)
		if err != nil {
			return err
		}
		if !own {
			denied = append(denied, ownOnly...)
		}
	}
	if len(denied) == 0 {
		return nil
	}
	return &Error{Code: 403, Msg: "角色身分不能修改 " + cur.Display() + " 的 " + strings.Join(denied, "、") +
		"(看得到的 Issue 都可以留言;改狀態、附加內文、指派只限指派給自己、目前任務連到、或自己建立的 Issue)",
		Data: map[string]any{"code": AgentIssueForbiddenCode, "fields": denied}}
}

// checkAgentAccept 角色憑證只能接受本來就是「自己的」Issue(接受會讓它變成負責人);人不受限。拿著 issueLock 呼叫
func (s *Service) checkAgentAccept(p *model.Principal, org string, cur *model.Issue) error {
	if p.Agent == nil {
		return nil
	}
	own, err := s.agentOwnsIssue(p, org, cur)
	if err != nil || own {
		return err
	}
	return &Error{Code: 403, Msg: "角色身分只能接受指派給自己、目前任務連到、或自己建立的 Issue:" + cur.Display(),
		Data: map[string]any{"code": AgentAcceptForbiddenCode}}
}

// agentOwnsIssue 這張 Issue 是不是角色「自己的」:指派給它、它建立的、或憑證綁定的任務連到它
func (s *Service) agentOwnsIssue(p *model.Principal, org string, cur *model.Issue) (bool, error) {
	me, err := s.SelfActor(p)
	if err != nil {
		return false, err
	}
	if me.ID != "" && (cur.Assignee == me.ID || cur.CreatedBy == me.ID) {
		return true, nil
	}
	if p.Agent.TaskID == "" {
		return false, nil
	}
	t, err := s.findTask(org, p.Agent.TaskID)
	if err != nil {
		return false, err
	}
	return s.linksIssue(org, t.Links, *cur), nil
}

// checkAgentTaskLinks 角色回報時新增的任務連結:任務連到的 Issue 算角色「自己的」,所以不能自己把別人的 Issue 連上來
// 擴大權限。任務原本就有的連結(含派任務的人填的)不限;新連結:
//   - 指向還在的 Issue:要原本就是自己的(指派、建立、或任務已經連到)
//   - 長得像 Issue 但現在沒有對到還在的 Issue(還沒建立的編號、垃圾桶裡的):擋,否則之後建立或還原就變成自己的
//   - 其他(網址、任務 id、文件編號等)不限
func (s *Service) checkAgentTaskLinks(p *model.Principal, org string, t *model.AgentTask, links []string) error {
	if p.Agent == nil {
		return nil
	}
	for _, l := range model.CleanLabels(links) {
		if hasLink(t.Links, l) {
			continue
		}
		var allowed bool
		var err error
		if issue := s.linkedIssue(org, l); issue == nil {
			var dangling bool
			dangling, err = s.danglingIssueRef(org, l)
			allowed = !dangling
		} else if allowed = s.linksIssue(org, t.Links, *issue); !allowed {
			allowed, err = s.agentOwnsIssue(p, org, issue)
		}
		if err != nil {
			return err
		}
		if !allowed {
			return &Error{Code: 403, Msg: "角色回報時只能連上指派給自己或自己建立、而且現在存在的 Issue:" + l,
				Data: map[string]any{"code": AgentIssueForbiddenCode, "fields": []string{"links"}}}
		}
	}
	return nil
}

// danglingIssueRef 沒對到還在的 Issue 的連結,之後會不會指到某張 Issue:可能發出的 key(ISS 或合法的專案代號),
// 或垃圾桶裡 Issue 的內部 id。T-、DOC-、CH-、INC-、PRJ- 是保留字,不會是專案代號
func (s *Service) danglingIssueRef(org, link string) (bool, error) {
	ref := strings.TrimSpace(link)
	if model.IsUUID(ref) {
		_, err := s.Store.ResolveIssueID(org, ref) // 含垃圾桶裡的
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	}
	// 先轉小寫再轉大寫:和 hasLink 的 EqualFold 一致(例如 Kelvin 符號等同 K)
	prefix := strings.ToUpper(strings.ToLower(model.IssueKeyPrefixOf(ref)))
	if prefix == model.IssueKeyPrefix {
		return true, nil
	}
	key, err := model.CleanProjectKey(prefix)
	return err == nil && key != "", nil
}

// linkedIssue 任務連結(內部 id、目前或舊的 key)指向的 Issue;不是 Issue、找不到、在垃圾桶裡回 nil
func (s *Service) linkedIssue(org, link string) *model.Issue {
	id, err := s.resolveIssueID(org, link)
	if err != nil {
		return nil
	}
	i, err := s.Store.GetIssue(org, id)
	if err != nil {
		return nil
	}
	return i
}
