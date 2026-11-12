package mcp

import (
	"sort"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// 派發工具(執行者端點 /mcp/worker 專用):dispatch_queue、dispatch_claim、dispatch_status、dispatch_release。
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
				return nil, ToolError{Message: "領取失敗:" + err.Error()}
			}
			return nil, terr(err)
		}
		if res.Task == nil {
			msg := "目前沒有可領的待領取任務"
			if res.Agent != nil {
				msg = res.Agent.Name + " 目前沒有待領取的任務"
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
			m := map[string]any{"agentId": cr.AgentID, "secret": cr.Secret, "expiresAt": cr.ExpiresAt}
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
		out["nextSteps"] = nextSteps(res, name, endpoint, lock)
		return out, nil
	})

	s.Register("dispatch_status", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		me := bk.Whoami()
		if me == "" {
			return nil, Errorf("無法判斷你的身分,查不到你領取中的任務")
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
			"message": t.ID + " 已退回待領取,鎖定解除、憑證作廢,別人可以領了"}, nil
	})
}

func nextSteps(res *service.ClaimResult, agentName, endpoint, lockHHMM string) string {
	t := res.Task
	var b strings.Builder
	b.WriteString("已領取 " + t.ID + "「" + t.Title + "」,你現在是虛擬角色「" + agentName + "」。")
	if cr := res.Credential; cr != nil {
		b.WriteString("\n1. 用憑證連角色端點,以該角色身分工作:")
		if endpoint != "" {
			b.WriteString("MCP 端點 " + endpoint + ",")
		} else {
			b.WriteString("(沒有提供端點網址,請向管理員取得角色端點 /mcp/agent 的網址)")
		}
		b.WriteString("HTTP 標頭 Authorization: Bearer " + cr.AgentID + ":" + cr.Secret + "(密鑰只這一次看得到,不要寫進 Issue、留言或任務結果)。")
		b.WriteString("先用 ops_agent_context 讀角色設定與任務,照任務說明完成;開 Issue 與留言的署名由憑證決定。")
	}
	b.WriteString("\n2. 任務已鎖定")
	if lockHHMM != "" {
		b.WriteString("到 " + lockHHMM)
	}
	b.WriteString(",鎖定期間別人不能領取。工作期間請定期(至少每小時)用 ops_agent_task_report status=inProgress(可附 note)回報進度以續約;逾時沒回報會被自動退回待領取,憑證也會失效。")
	b.WriteString("\n3. 做完用 ops_agent_task_report status=done 並在 result 寫結果;需要人決定就用 status=blocked 並在 note 留言提問。done、blocked 之後憑證立刻失效。")
	b.WriteString("\n4. 不想做了或做不下去(又不需要人決定):用 dispatch_release 放棄,任務會退回待領取。")
	return b.String()
}
