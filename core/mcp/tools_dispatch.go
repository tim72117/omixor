package mcp

import (
	"sort"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// 派發工具(執行者端點 /mcp/worker 專用):dispatch_queue、dispatch_claim、dispatch_status、dispatch_report、dispatch_comment、dispatch_release。
// 給「領取任務的 LLM」用,只做找任務、領取、看自己領的、放棄;角色與任務的建立修改、Issue、文件、成員都不在這裡。
// 資料操作都透過 Backend(Local / Remote);「我」的比對用 Backend.Whoami(同 ClaimTask 寫進 ClaimedBy 的值)。

const (
	dispatchDefaultLimit = 30
	dispatchMaxLimit     = 100
)

func prioRank(p string) int {
	for i, v := range model.IssuePriorities {
		if v == p {
			return i
		}
	}
	return len(model.IssuePriorities)
}

// sortTasks 優先度高的在前,同優先度先建立的在前
func sortTasks(list []model.AgentTask) {
	sort.SliceStable(list, func(i, j int) bool {
		pi, pj := prioRank(list[i].Priority), prioRank(list[j].Priority)
		if pi != pj {
			return pi < pj
		}
		if !list[i].CreatedAt.Equal(list[j].CreatedAt.Time) {
			return list[i].CreatedAt.Before(list[j].CreatedAt.Time)
		}
		return list[i].Number() < list[j].Number()
	})
}

func agentRef(e AgentEnv, id string) map[string]any {
	return map[string]any{"id": id, "name": e.AgentName(id)}
}

// RegisterDispatchTools 登記派發工具。執行環境(Call.Ctx)要實作 AgentEnv(Session)。
func RegisterDispatchTools(s *Server) {
	s.Register("dispatch_queue", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		me := bk.Whoami()
		f := service.TaskFilter{Status: "open"}
		if ag := c.Args.Opt("agent"); ag != nil {
			f.AgentID = *ag
		}
		limit := dispatchDefaultLimit
		if n, ok := c.Args.Int("limit"); ok && n > 0 {
			limit = min(n, dispatchMaxLimit)
		}
		agents, err := bk.ListAgents(org)
		if err != nil {
			return nil, terr(err)
		}
		active := map[string]bool{}
		for _, g := range agents {
			active[g.ID] = g.Active
		}
		list, err := bk.ListAgentTasks(org, f)
		if err != nil {
			return nil, terr(err)
		}
		now := time.Now()
		pick := []model.AgentTask{}
		for _, t := range list {
			switch t.Status {
			case "queued", "blocked":
				if active[t.AgentID] {
					pick = append(pick, t)
				}
			case "inProgress":
				if me != "" && t.ClaimedBy == me {
					pick = append(pick, t)
				}
			}
		}
		sortTasks(pick)
		out := []any{}
		for k, t := range pick {
			if k >= limit {
				break
			}
			row := map[string]any{"id": t.ID, "title": t.Title, "agent": agentRef(e, t.AgentID), "priority": t.Priority,
				"status": t.Status, "links": t.Links, "claimable": !t.Locked(now)}
			if t.ClaimedBy != "" {
				row["claimedBy"] = t.ClaimedBy
				putActor(row, "claimedByActor", t.ClaimedByActor)
				row["mine"] = me != "" && t.ClaimedBy == me
			}
			if t.LockedUntil != nil {
				row["lockedUntil"] = iso(t.LockedUntil.Time)
			}
			out = append(out, row)
		}
		return map[string]any{"total": len(pick), "tasks": out}, nil
	})

	s.Register("dispatch_claim", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		agent := c.Args.Str("agent")
		var res *service.ClaimResult
		var err error
		if id := c.Args.Str("id"); id != "" {
			if agent == "" { // 只給任務 id:角色從任務取
				t, gerr := bk.GetAgentTask(org, id)
				if gerr != nil {
					return nil, terr(gerr)
				}
				agent = t.AgentID
			}
			res, err = bk.ClaimTask(org, service.ClaimInput{AgentID: agent, TaskID: id})
		} else {
			res, err = bk.ClaimNext(org, agent)
		}
		if err != nil {
			if service.Code(err) == 409 { // 含「已被領取、處理中,鎖定到 hh:mm」:原文轉給呼叫端
				return nil, prefixErr("Claim failed: ", err)
			}
			return nil, terr(err)
		}
		if res.Task == nil {
			msg := "There are no tasks currently available to claim"
			if res.Agent != nil {
				msg = res.Agent.Name + " has no tasks waiting to be claimed"
			}
			return map[string]any{"task": nil, "message": msg}, nil
		}
		out := map[string]any{"task": taskJSON(e, *res.Task)}
		name := e.AgentName(res.Task.AgentID)
		if g := res.Agent; g != nil {
			out["agent"] = map[string]any{"id": g.ID, "name": g.Name, "role": g.Role, "instructions": g.Instructions}
			name = g.Name
		}
		endpoint := e.EndpointURL()
		if cr := res.Credential; cr != nil {
			m := map[string]any{"agentId": cr.AgentID, "secret": cr.Secret, "code": cr.Code, "expiresAt": cr.ExpiresAt}
			if endpoint != "" {
				m["endpoint"] = endpoint
			}
			out["credential"] = m
		}
		lock := ""
		if t := res.Task; t.LockedUntil != nil {
			out["lockedUntil"] = iso(t.LockedUntil.Time)
			lock = t.LockedUntil.Time.In(time.Local).Format("15:04")
		}
		out["nextSteps"] = nextSteps(res, name, endpoint, lock) + delegateStep(e, res)
		return out, nil
	})

	s.Register("dispatch_status", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		me := bk.Whoami()
		if me == "" {
			return nil, Errorf("Cannot determine your identity, so your claimed tasks cannot be looked up")
		}
		list, err := bk.ListAgentTasks(org, service.TaskFilter{Status: "inProgress"})
		if err != nil {
			return nil, terr(err)
		}
		mine := []model.AgentTask{}
		for _, t := range list {
			if t.ClaimedBy == me {
				mine = append(mine, t)
			}
		}
		sortTasks(mine)
		now := time.Now()
		out := []any{}
		for _, t := range mine {
			row := map[string]any{"id": t.ID, "title": t.Title, "agent": agentRef(e, t.AgentID), "priority": t.Priority, "links": t.Links}
			if t.ClaimedAt != nil {
				row["claimedAt"] = iso(t.ClaimedAt.Time)
			}
			if t.LockedUntil != nil {
				left := t.LockedUntil.Time.Sub(now)
				row["lockedUntil"] = iso(t.LockedUntil.Time)
				row["remainingMinutes"] = max(0, int(left/time.Minute))
				row["expired"] = left <= 0
			} else {
				row["expired"] = true
			}
			out = append(out, row)
		}
		return map[string]any{"me": me, "total": len(out), "tasks": out}, nil
	})

	s.Register("dispatch_report", func(c *Call) (any, error) {
		e := agentEnv(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		in := service.TaskReport{Status: c.Args.Opt("status"), Result: c.Args.Opt("result"), Note: c.Args.Opt("note")}
		if l, ok := c.Args.Strings("links"); ok {
			in.Links = l
		}
		code, err := c.Args.Require("code")
		if err != nil {
			return nil, err
		}
		t, err := e.Backend().WorkerReport(e.Org(), id, code, in)
		if err != nil {
			return nil, terr(err)
		}
		out := map[string]any{"task": taskJSON(e, *t)}
		if t.LockedUntil != nil && t.Status == "inProgress" {
			out["lockedUntil"] = iso(t.LockedUntil.Time)
		}
		return out, nil
	})

	s.Register("dispatch_comment", func(c *Call) (any, error) {
		e := agentEnv(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		issue, err := c.Args.Require("issue")
		if err != nil {
			return nil, err
		}
		cm, err := c.Args.Require("comment")
		if err != nil {
			return nil, err
		}
		code, err := c.Args.Require("code")
		if err != nil {
			return nil, err
		}
		i, err := e.Backend().WorkerComment(e.Org(), id, code, issue, cm)
		if err != nil {
			return nil, terr(err)
		}
		last := map[string]any{}
		if n := len(i.Comments); n > 0 {
			lc := i.Comments[n-1]
			last = map[string]any{"id": lc.ID, "author": lc.Author, "at": iso(lc.At.Time)}
			putActor(last, "authorActor", lc.AuthorActor)
		}
		return map[string]any{"issue": i.ID, "comments": len(i.Comments), "comment": last}, nil
	})

	s.Register("dispatch_delegate", func(c *Call) (any, error) {
		e := agentEnv(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		code, err := c.Args.Require("code")
		if err != nil {
			return nil, err
		}
		to, err := c.Args.Require("to")
		if err != nil {
			return nil, err
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		instructions, _ := c.Args.RawString("instructions")
		priority, err := c.Args.OptionalEnum("priority", IssuePriorities)
		if err != nil {
			return nil, err
		}
		t, err := e.Backend().WorkerDelegate(e.Org(), id, code, service.DelegateInput{To: to, Title: title, Instructions: instructions, Priority: priority})
		if err != nil {
			return nil, terr(err)
		}
		return map[string]any{"task": taskJSON(e, *t),
			"message": "Delegated " + t.ID + " \"" + t.Title + "\" to " + e.AgentName(t.AgentID) + "; when they finish, a \"review report\" task is queued for your agent automatically"}, nil
	})

	s.Register("dispatch_release", func(c *Call) (any, error) {
		e := agentEnv(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		t, err := e.Backend().ReleaseTask(e.Org(), id, c.Args.Str("reason"))
		if err != nil {
			return nil, terr(err)
		}
		return map[string]any{"released": true, "task": taskJSON(e, *t),
			"message": t.ID + " was returned to the queue: the lock is released, the credential is revoked, and others can claim it"}, nil
	})
}

func nextSteps(res *service.ClaimResult, agentName, endpoint, lockHHMM string) string {
	t := res.Task
	code := ""
	if res.Credential != nil {
		code = res.Credential.Code
	}
	var b strings.Builder
	b.WriteString("Claimed " + t.ID + " \"" + t.Title + "\"; you are now the agent \"" + agentName + "\".")
	b.WriteString("\n1. Complete the work following the agent instructions above and the task description; related issues are in task.links.")
	b.WriteString("\n2. The task is locked")
	if lockHHMM != "" {
		b.WriteString(" until " + lockHHMM)
	}
	b.WriteString("; nobody else can claim it while it is locked. While working, report periodically (at least hourly) with dispatch_report(id: " + t.ID + ", code: " + code + ", status: inProgress, optionally with a note) to renew the lease; if you do not report in time, the task is automatically returned to the queue.")
	b.WriteString("\n3. Use dispatch_comment(id: " + t.ID + ", code: " + code + ", issue, comment) to comment on the related issue as the agent and explain the result; when finished, use dispatch_report status=done and write the result in result; if a human must decide, use status=blocked and ask in note.")
	b.WriteString("\n4. If you do not want to do it or cannot continue (and no human decision is needed): abandon it with dispatch_release and the task returns to the queue.")
	b.WriteString("\nClaim verification code " + code + ": always include it with dispatch_report and dispatch_comment; the server uses it to confirm you are acting on the task and agent from this claim (so the wrong one is never picked when you have claimed several).")
	b.WriteString("\nThe tools above are called as your signed-in omixor identity; after the server confirms you are the claimer it signs as the agent for you, so you do not need the credential secret (the secret is for programs that connect directly to the agent endpoint /mcp/agent; never write it into issues, comments, task results or files).")
	return b.String()
}

// delegateStep nextSteps 的委派說明:這個角色在組織鏈上可以委派給誰(沒有下游就不提)
func delegateStep(e AgentEnv, res *service.ClaimResult) string {
	t := res.Task
	can := downstreamAgents(e, t.AgentID)
	if can == "" {
		return ""
	}
	code := ""
	if res.Credential != nil {
		code = res.Credential.Code
	}
	return "\n5. According to the organization chain, this agent can delegate work to: " + can + ". Use dispatch_delegate(id: " + t.ID + ", code: " + code + ", to, title, instructions) to delegate; when they finish, a \"review report\" task is queued for you automatically."
}
