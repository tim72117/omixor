package mcp

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 虛擬角色工具:名稱、參數與輸出格式同 Swift 版 MCPHandler。雲端(server)與 omixor-web 的管理端點、角色端點共用這一份實作。
// 資料操作都透過 Backend(Local / Remote);角色端點的限制由 service 依憑證身分強制,這裡不重複判斷權限。

func toMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var d map[string]any
	_ = json.Unmarshal(b, &d)
	return d
}

// agentJSON 角色的輸出:設定、各狀態未結束任務數、例行任務摘要;l 不是 nil 時 docAccess 換成看得懂的文件櫃範圍(DocScope)
func agentJSON(e AgentEnv, l model.Layout, g model.Agent, open []model.AgentTask) map[string]any {
	d := toMap(g)
	counts := map[string]any{}
	for _, t := range open {
		if t.AgentID == g.ID && model.TaskIsOpen(t.Status) {
			n, _ := counts[t.Status].(int)
			counts[t.Status] = n + 1
		}
	}
	d["openTasks"] = counts
	routines := []any{}
	for _, r := range g.Routines {
		routines = append(routines, map[string]any{"title": r.Title, "schedule": r.ScheduleLabel(), "enabled": r.Enabled, "instructions": r.Instructions})
	}
	d["routines"] = routines
	if l != nil {
		d["docAccess"] = DocScope(l, g)
	}
	if dec, ok := e.(AgentDecorator); ok {
		dec.DecorateAgent(g, d)
	}
	return d
}

// openTasksOf 角色未結束的任務(取不到就是空的;只用來算各狀態的任務數)
func openTasksOf(e AgentEnv, agentID string) []model.AgentTask {
	l, err := e.Backend().ListAgentTasks(e.Org(), service.TaskFilter{AgentID: agentID, Status: "open"})
	if err != nil {
		return nil
	}
	return l
}

// taskJSON 任務的輸出:agentName、createdBy 與備註作者換成顯示名稱
func taskJSON(e AgentEnv, t model.AgentTask) map[string]any {
	d := toMap(t)
	d["agentName"] = e.AgentName(t.AgentID)
	d["createdBy"] = e.AuthorName(t.CreatedBy)
	notes := []any{}
	for _, n := range t.Notes {
		notes = append(notes, map[string]any{"author": e.AuthorName(n.Author), "text": n.Text, "at": iso(n.At.Time)})
	}
	d["notes"] = notes
	return d
}

func terr(err error) error { return ToolError{Message: err.Error()} }

func parseRoutine(d map[string]any) (model.AgentRoutine, error) {
	a := Args{Raw: d}
	title, err := a.Require("title")
	if err != nil {
		return model.AgentRoutine{}, err
	}
	r := model.AgentRoutine{Title: title, Kind: "daily", Hour: 9, Weekday: 2, EveryHours: 6, Priority: "normal", Enabled: true,
		Instructions: func() string { s, _ := a.RawString("instructions"); return s }()}
	if k, err := a.OptionalEnum("kind", model.RoutineKinds); err != nil {
		return r, err
	} else if k != "" {
		r.Kind = k
	}
	clamp := func(k string, def, lo, hi int) int {
		n, ok := a.Int(k)
		if !ok {
			n = def
		}
		return min(hi, max(lo, n))
	}
	r.Hour, r.Minute = clamp("hour", 9, 0, 23), clamp("minute", 0, 0, 59)
	r.Weekday, r.EveryHours = clamp("weekday", 2, 1, 7), max(1, clamp("every_hours", 6, 1, 1<<20))
	if p, err := a.OptionalEnum("priority", model.IssuePriorities); err != nil {
		return r, err
	} else if p != "" {
		r.Priority = p
	}
	if e, ok := d["enabled"].(bool); ok {
		r.Enabled = e
	}
	return r, nil
}

func objects(v any) ([]map[string]any, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := []map[string]any{}
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, m)
	}
	return out, true
}

// RegisterAgentTools 登記虛擬角色工具:agents、agent_context、agent_tasks、agent_task_claim、
// agent_task_report、agent_upsert、agent_task_create。
// 執行環境(Call.Ctx)要實作 AgentEnv;可選實作 AgentDecorator、DocAccessResolver(要自己改寫輸出或 docAccess 的 key 時用;
// 沒實作時依核心的文件櫃結構處理)。
func RegisterAgentTools(s *Server) {
	s.Register("agents", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		tasks, err := bk.ListAgentTasks(org, service.TaskFilter{Status: "open"})
		if err != nil {
			return nil, terr(err)
		}
		if id := c.Args.Opt("id"); id != nil {
			g, err := bk.GetAgent(org, *id)
			if err != nil {
				return nil, terr(err)
			}
			return agentJSON(e, cabLayout(e), *g, tasks), nil
		}
		list, err := bk.ListAgents(org)
		if err != nil {
			return nil, terr(err)
		}
		out := []any{}
		l := cabLayout(e)
		for _, g := range list {
			out = append(out, agentJSON(e, l, g, tasks))
		}
		return map[string]any{"agents": out}, nil
	})

	// 角色能讀取的資訊:角色設定、未完成的任務、文件櫃中能存取的區塊與能讀的文件(依 docAccess);其他由使用的程式透過 ContextExtra 附加
	s.Register("agent_context", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		name, err := c.Args.Require("agent")
		if err != nil {
			return nil, err
		}
		g, err := bk.GetAgent(org, name)
		if err != nil {
			return nil, terr(err)
		}
		open, err := bk.ListAgentTasks(org, service.TaskFilter{AgentID: g.ID, Status: "open"})
		if err != nil {
			return nil, terr(err)
		}
		tasks := []any{}
		for _, t := range open {
			if len(tasks) >= 50 {
				break
			}
			tasks = append(tasks, map[string]any{"id": t.ID, "title": t.Title, "status": t.Status,
				"priority": t.Priority, "updatedAt": iso(t.UpdatedAt.Time)})
		}
		bodies, _ := c.Args.Raw["include_bodies"].(bool)
		out := map[string]any{
			"organization": e.OrgName(),
			"agent":        agentJSON(e, cabLayout(e), *g, open),
			"tasks":        map[string]any{"total": len(open), "tasks": tasks},
		}
		if ch := chainLinks(e, g.ID); ch != nil { // 組織鏈:上游(回報給誰)與下游(可以派給誰,含 label)
			out["chain"] = ch
		}
		if m := channelMentions(e, g.ID); m != nil { // 訊息頻道:提到這個角色的未讀訊息與所在頻道的未讀數
			out["mentions"] = m
		}
		for k, v := range cabinetContext(e, *g, bodies) {
			out[k] = v
		}
		for k, v := range e.ContextExtra(*g, bodies) {
			out[k] = v
		}
		return out, nil
	})

	s.Register("agent_tasks", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		if id := c.Args.Opt("id"); id != nil {
			t, err := bk.GetAgentTask(org, *id)
			if err != nil {
				return nil, terr(err)
			}
			return taskJSON(e, *t), nil
		}
		st := "open"
		if s := c.Args.Opt("status"); s != nil {
			st = *s
		}
		f := service.TaskFilter{Status: st}
		if ag := c.Args.Opt("agent"); ag != nil {
			f.AgentID = *ag
		}
		list, err := bk.ListAgentTasks(org, f)
		if err != nil {
			return nil, terr(err)
		}
		out := []any{}
		for k, t := range list {
			if k >= 50 {
				break
			}
			row := map[string]any{"id": t.ID, "title": t.Title, "agent": t.AgentID, "status": t.Status,
				"priority": t.Priority, "updatedAt": iso(t.UpdatedAt.Time)}
			if t.LockedUntil != nil {
				row["lockedUntil"] = iso(t.LockedUntil.Time)
			}
			if t.ClaimedBy != "" {
				row["claimedBy"] = t.ClaimedBy
			}
			out = append(out, row)
		}
		return map[string]any{"total": len(list), "tasks": out}, nil
	})

	s.Register("agent_task_claim", func(c *Call) (any, error) {
		e := agentEnv(c)
		ag, err := c.Args.Require("agent")
		if err != nil {
			return nil, err
		}
		in := service.ClaimInput{AgentID: ag}
		if id := c.Args.Opt("id"); id != nil {
			in.TaskID = *id
		}
		res, err := e.Backend().ClaimTask(e.Org(), in)
		if err != nil {
			if service.Code(err) == 409 { // 含「已被領取、處理中,鎖定到 hh:mm」:原文轉給呼叫端
				return nil, ToolError{Message: "領取失敗:" + err.Error()}
			}
			return nil, terr(err)
		}
		out := map[string]any{"agent": map[string]any{}, "task": nil}
		name, gid := ag, ag
		if res.Agent != nil {
			out["agent"] = agentJSON(e, cabLayout(e), *res.Agent, openTasksOf(e, res.Agent.ID))
			name, gid = res.Agent.Name, res.Agent.ID
		}
		if res.Task == nil {
			out["message"] = name + " 目前沒有待領取的任務"
			return out, nil
		}
		out["task"] = taskJSON(e, *res.Task)
		if cr := res.Credential; cr != nil {
			// 角色憑證:綁這個任務,只在這次回傳;連角色端點用 Authorization: Bearer <agentId>:<secret>
			m := map[string]any{"agentId": cr.AgentID, "secret": cr.Secret, "expiresAt": cr.ExpiresAt}
			if u := e.EndpointURL(); u != "" {
				m["endpoint"] = u
			}
			out["credential"] = m
		}
		lockNote := ""
		if res.Task.LockedUntil != nil {
			lockNote = "已鎖定到 " + res.Task.LockedUntil.Time.In(time.Local).Format("15:04") + "(其他人不能再領);長任務請定期用 agent_task_report(status: inProgress)回報進度以續約,逾時未回報會被自動退回待領取。"
		}
		out["message"] = "已領取。" + lockNote + "做完用 agent_task_report 回報。開 Issue 或留言時帶 agent: \"" + gid + "\",才會署名這個角色;或改用 credential 連角色端點,署名由憑證決定"
		return out, nil
	})

	s.Register("agent_task_report", func(c *Call) (any, error) {
		e := agentEnv(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		in := service.TaskReport{Status: c.Args.Opt("status"), Note: c.Args.Opt("note")}
		if in.Status != nil && !slices.Contains([]string{"inProgress", "blocked", "done"}, *in.Status) {
			return nil, ToolError{Message: "status 必須是 inProgress、blocked、done 之一"}
		}
		if r, ok := c.Args.RawString("result"); ok {
			in.Result = &r
		}
		if l, ok := c.Args.Strings("links"); ok {
			in.Links = l
		}
		t, err := e.Backend().ReportTask(e.Org(), id, in)
		if err != nil {
			return nil, terr(err)
		}
		return taskJSON(e, *t), nil
	})

	s.Register("agent_upsert", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		var g model.Agent
		isNew := false
		id := c.Args.Opt("id")
		var found *model.Agent
		if id != nil {
			found, _ = bk.GetAgent(org, *id)
		}
		switch {
		case found != nil:
			g = found.Clone()
		case id != nil:
			if c.Args.Str("name") == "" {
				return nil, Errorf("找不到角色 %s;要新增就一起給 name", *id)
			}
			if !model.ValidAgentID(*id) {
				return nil, ToolError{Message: "新角色的 id 只能用小寫英文、數字與 -"}
			}
			g = model.Agent{ID: *id, Active: true, Symbol: "person.crop.circle", Color: "blue", DocAccess: map[string]string{}}
			isNew = true
		default:
			if _, err := c.Args.Require("name"); err != nil {
				return nil, err
			}
			g = model.Agent{Active: true, Symbol: "person.crop.circle", Color: "blue", DocAccess: map[string]string{}}
			isNew = true
		}
		_ = isNew
		if v := c.Args.Str("name"); v != "" {
			g.Name = v
		}
		if v := c.Args.Opt("role"); v != nil {
			g.Role = *v
		}
		if v, ok := c.Args.RawString("instructions"); ok {
			g.Instructions = v
		}
		if v := c.Args.Str("symbol"); v != "" {
			g.Symbol = v
		}
		if v := c.Args.Opt("color"); v != nil {
			if !model.OneOf(*v, model.AgentColors) {
				return nil, ToolError{Message: "color 必須是 " + strings.Join(model.AgentColors, "、") + " 之一"}
			}
			g.Color = *v
		}
		if v, ok := c.Args.Raw["active"].(bool); ok {
			g.Active = v
		}
		if list, ok := objects(c.Args.Raw["routines"]); ok {
			rs := []model.AgentRoutine{}
			for _, d := range list {
				r, err := parseRoutine(d)
				if err != nil {
					return nil, err
				}
				rs = append(rs, r)
			}
			g.Routines = rs
		}
		if list, ok := objects(c.Args.Raw["add_routines"]); ok {
			rs := slices.Clone(g.Routines)
			for _, d := range list {
				r, err := parseRoutine(d)
				if err != nil {
					return nil, err
				}
				rs = append(rs, r)
			}
			g.Routines = rs
		}
		if m, ok := c.Args.Raw["doc_access"].(map[string]any); ok {
			// key 可以是區塊 id 或名稱、頁籤網址、名稱或代號:依文件櫃結構換成存起來的 key(區塊 id 或頁籤網址)
			layout := cabLayout(e)
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				level, _ := m[k].(string)
				if !model.OneOf(level, model.DocAccessLevels) {
					return nil, ToolError{Message: "doc_access 的值必須是 read、write 或 none"}
				}
				key := k
				if rs, ok := e.(DocAccessResolver); ok {
					rk, err := rs.ResolveDocAccessKey(k)
					if err != nil {
						return nil, err
					}
					key = rk
				} else if layout != nil {
					rk, msg := layout.ResolveAccessKey(k)
					if msg != "" {
						return nil, ToolError{Message: msg}
					}
					key = rk
				}
				if level == "none" {
					delete(g.DocAccess, key)
				} else {
					g.DocAccess[key] = level
				}
			}
		}
		saved, err := bk.SaveAgent(org, g)
		if err != nil {
			return nil, terr(err)
		}
		return agentJSON(e, cabLayout(e), *saved, openTasksOf(e, saved.ID)), nil
	})

	s.Register("agent_task_create", func(c *Call) (any, error) {
		e := agentEnv(c)
		ag := c.Args.Str("assign_to")
		if ag == "" {
			return nil, ToolError{Message: "要用 assign_to 指定接任務的角色(agent 是你自己扮演的角色,用來署名)"}
		}
		bk, org := e.Backend(), e.Org()
		target, err := bk.GetAgent(org, ag)
		if err != nil {
			if backend.NotFound(err) {
				return nil, Errorf("找不到角色 %s", ag)
			}
			return nil, terr(err)
		}
		by, err := e.Author(c.Args.Str("agent"))
		if err != nil {
			return nil, err
		}
		if by == model.AgentPrefix+target.ID {
			return nil, Errorf("不能把任務派給自己(%s);要派給別的角色請改 assign_to", target.Name)
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
		t, err := bk.AssignTask(org, service.TaskInput{AgentID: target.ID, Title: title, Instructions: instructions, Priority: priority, Author: by})
		if err != nil {
			return nil, terr(err)
		}
		return taskJSON(e, *t), nil
	})
}
