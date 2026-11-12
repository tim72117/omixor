package mcp

import (
	"sort"
	"strings"

	"omixor-web/internal/core"
)

// 虛擬角色工具:同 Swift 版 MCPHandler 的 agentsList / agentTasks / agentClaim / agentReport / agentUpsert / agentCreate

func agentJSON(a *core.App, g core.Agent) map[string]any {
	d, _ := core.Object(g).(map[string]any)
	counts := map[string]any{}
	for _, t := range a.TasksFor(g.ID) {
		if core.TaskIsOpen(t.Status) {
			n, _ := counts[t.Status].(int)
			counts[t.Status] = n + 1
		}
	}
	d["openTasks"] = counts
	d["docAccess"] = docScope(a, g)
	routines := []any{}
	for _, r := range g.Routines {
		routines = append(routines, map[string]any{"title": r.Title, "schedule": r.ScheduleLabel(), "enabled": r.Enabled, "instructions": r.Instructions})
	}
	d["routines"] = routines
	return d
}

func taskJSON(a *core.App, t core.AgentTask) map[string]any {
	d, _ := core.Object(t).(map[string]any)
	d["agentName"] = t.AgentID
	if g := a.FindAgent(t.AgentID); g != nil {
		d["agentName"] = g.Name
	}
	d["createdBy"] = a.TaskAuthorName(t.CreatedBy)
	notes := []any{}
	for _, n := range t.Notes {
		notes = append(notes, map[string]any{"author": a.TaskAuthorName(n.Author), "text": n.Text, "at": core.ISO(n.At.Time)})
	}
	d["notes"] = notes
	return d
}

// rawBool 同 Swift 的 a.raw[k] as? Bool(JSON 的 0/1 也會轉成布林)
func agentBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case int64:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	case float64:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	}
	return false, false
}

// rawObjects 同 Swift 的 a.raw[k] as? [[String: Any]]:每個元素都要是物件
func agentObjects(v any) ([]map[string]any, bool) {
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

func agentClamp(v, lo, hi int) int { return min(hi, max(lo, v)) }

func parseRoutine(d map[string]any) (core.AgentRoutine, error) {
	r := Args{Raw: d}
	title, err := r.Require("title")
	if err != nil {
		return core.AgentRoutine{}, err
	}
	x := core.NewRoutine(title)
	x.Instructions, _ = r.iaRaw("instructions")
	kind, err := r.OptionalEnum("kind", core.RoutineKinds)
	if err != nil {
		return x, err
	}
	if kind != "" {
		x.Kind = kind
	}
	intOr := func(k string, def int) int {
		if n, ok := r.Int(k); ok {
			return n
		}
		return def
	}
	x.Hour = agentClamp(intOr("hour", 9), 0, 23)
	x.Minute = agentClamp(intOr("minute", 0), 0, 59)
	x.Weekday = agentClamp(intOr("weekday", 2), 1, 7)
	x.EveryHours = max(1, intOr("every_hours", 6))
	p, err := r.OptionalEnum("priority", core.IssuePriorities)
	if err != nil {
		return x, err
	}
	if p != "" {
		x.Priority = p
	}
	if e, ok := agentBool(d["enabled"]); ok {
		x.Enabled = e
	}
	return x, nil
}

func validNewAgentID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

func init() {
	Register("ops_agents", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if id := c.Args.iaOpt("id"); id != nil {
			g := a.FindAgent(*id)
			if g == nil {
				return nil, Errorf("找不到角色 %s", *id)
			}
			return agentJSON(a, *g), nil
		}
		list := []any{}
		for _, g := range a.Agents() {
			list = append(list, agentJSON(a, g))
		}
		return map[string]any{"agents": list}, nil
	})

	// 角色能讀取的資訊:角色設定、未完成的任務、文件櫃權限與能讀的文件(同 Swift 版 agentContext)
	Register("ops_agent_context", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		name, err := c.Args.Require("agent")
		if err != nil {
			return nil, err
		}
		g := a.FindAgent(name)
		if g == nil {
			return nil, Errorf("找不到角色 %s", name)
		}
		tasks := []any{}
		openN := 0
		for _, t := range a.AgentTasks() {
			if t.AgentID != g.ID || !core.TaskIsOpen(t.Status) {
				continue
			}
			openN++
			if len(tasks) < 50 {
				tasks = append(tasks, map[string]any{"id": t.ID, "title": t.Title, "status": t.Status,
					"priority": t.Priority, "updatedAt": core.ISO(t.UpdatedAt.Time)})
			}
		}
		// 只收真正的布林(和 Swift 版一樣,"true" 字串不算)
		bodies, _ := c.Args.Raw["include_bodies"].(bool)
		const bodyLimit = 20
		docs := []any{}
		docN := 0
		for _, d := range a.CabinetDocs() {
			tab := cabDeref(d.Tab)
			if !g.CanRead(d.Section, tab) {
				continue
			}
			docN++
			if len(docs) >= 50 {
				continue
			}
			o := docJSON(a, d, bodies && len(docs) < bodyLimit)
			o["access"] = "read"
			if g.CanWrite(d.Section, tab) {
				o["access"] = "write"
			}
			docs = append(docs, o)
		}
		return map[string]any{
			"organization": cabOrgName(a),
			"agent":        agentJSON(a, *g),
			"tasks":        map[string]any{"total": openN, "tasks": tasks},
			"cabinet":      cabinetSections(a, g),
			"docs":         map[string]any{"total": docN, "docs": docs},
		}, nil
	})

	Register("ops_agent_tasks", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if id := c.Args.iaOpt("id"); id != nil {
			t := a.FindTask(*id)
			if t == nil {
				return nil, Errorf("找不到任務 %s", *id)
			}
			return taskJSON(a, *t), nil
		}
		list := a.AgentTasks()
		if ag := c.Args.iaOpt("agent"); ag != nil {
			g := a.FindAgent(*ag)
			if g == nil {
				return nil, Errorf("找不到角色 %s", *ag)
			}
			list = a.TasksFor(g.ID)
		}
		st := "open"
		if s := c.Args.iaOpt("status"); s != nil {
			st = *s
		}
		filtered := []core.AgentTask{}
		switch {
		case st == "open":
			for _, t := range list {
				if core.TaskIsOpen(t.Status) {
					filtered = append(filtered, t)
				}
			}
		case st == "all":
			filtered = list
		default:
			valid := false
			for _, v := range core.AgentTaskStatuses {
				valid = valid || v == st
			}
			if !valid {
				return nil, Errorf("status 不正確:%s", st)
			}
			for _, t := range list {
				if t.Status == st {
					filtered = append(filtered, t)
				}
			}
		}
		out := []any{}
		for k, t := range filtered {
			if k >= 50 {
				break
			}
			out = append(out, map[string]any{"id": t.ID, "title": t.Title, "agent": t.AgentID, "status": t.Status,
				"priority": t.Priority, "updatedAt": core.ISO(t.UpdatedAt.Time)})
		}
		return map[string]any{"total": len(filtered), "tasks": out}, nil
	})

	Register("ops_agent_task_claim", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		ag, err := c.Args.Require("agent")
		if err != nil {
			return nil, err
		}
		t, err := a.ClaimTask(ag, c.Args.iaOpt("id"))
		if err != nil {
			return nil, userToolErr(err)
		}
		g := a.FindAgent(ag)
		out := map[string]any{"agent": map[string]any{}, "task": nil}
		name, gid := ag, ag
		if g != nil {
			out["agent"] = agentJSON(a, *g)
			name, gid = g.Name, g.ID
		}
		if t == nil {
			out["message"] = name + " 目前沒有待領取的任務"
		} else {
			out["task"] = taskJSON(a, *t)
			out["message"] = "已領取,做完用 ops_agent_task_report 回報。開 Issue 或留言時帶 agent: \"" + gid + "\",才會署名這個角色"
		}
		return out, nil
	})

	Register("ops_agent_task_report", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		status := c.Args.iaOpt("status")
		if status != nil && *status != "inProgress" && *status != "blocked" && *status != "done" {
			return nil, ToolError{"status 必須是 inProgress、blocked、done 之一"}
		}
		var result *string
		if s, ok := c.Args.iaRaw("result"); ok {
			result = &s
		}
		links, ok := c.Args.Strings("links")
		if !ok {
			links = nil
		}
		t, err := a.ReportTask(id, status, result, c.Args.iaOpt("note"), links)
		if err != nil {
			return nil, userToolErr(err)
		}
		if t == nil {
			return map[string]any{}, nil
		}
		return taskJSON(a, *t), nil
	})

	Register("ops_agent_upsert", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		var g core.Agent
		isNew := false
		id := c.Args.iaOpt("id")
		var found *core.Agent
		if id != nil {
			found = a.FindAgent(*id)
		}
		switch {
		case found != nil:
			g = *found
		case id != nil:
			name := c.Args.Str("name")
			if name == "" {
				return nil, Errorf("找不到角色 %s;要新增就一起給 name", *id)
			}
			if !validNewAgentID(*id) {
				return nil, ToolError{"新角色的 id 只能用小寫英文、數字與 -"}
			}
			g = core.NewAgent(*id, name)
			isNew = true
		default:
			name, err := c.Args.Require("name")
			if err != nil {
				return nil, err
			}
			g = core.NewAgent(a.NewAgentID(name), name)
			isNew = true
		}
		if v := c.Args.Str("name"); v != "" {
			g.Name = v
		}
		if v := c.Args.iaOpt("role"); v != nil {
			g.Role = *v
		}
		if v, ok := c.Args.iaRaw("instructions"); ok {
			g.Instructions = v
		}
		if v := c.Args.Str("symbol"); v != "" {
			g.Symbol = v
		}
		if v := c.Args.iaOpt("color"); v != nil {
			valid := false
			for _, n := range core.AgentColors {
				valid = valid || n == *v
			}
			if !valid {
				return nil, ToolError{"color 必須是 " + strings.Join(core.AgentColors, "、") + " 之一"}
			}
			g.Color = *v
		}
		if v, ok := agentBool(c.Args.Raw["active"]); ok {
			g.Active = v
		}
		if list, ok := agentObjects(c.Args.Raw["routines"]); ok {
			rs := []core.AgentRoutine{}
			for _, d := range list {
				r, err := parseRoutine(d)
				if err != nil {
					return nil, err
				}
				rs = append(rs, r)
			}
			g.Routines = rs
		}
		if list, ok := agentObjects(c.Args.Raw["add_routines"]); ok {
			rs := append([]core.AgentRoutine{}, g.Routines...)
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
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				level, _ := m[k].(string)
				if level != "read" && level != "write" && level != "none" {
					return nil, ToolError{"doc_access 的值必須是 read、write 或 none"}
				}
				var key string
				if sec := a.CabinetSectionByID(k); sec != nil {
					key = sec.ID
				} else if _, t, err := a.ResolveCabinet(nil, &k); err == nil && t != nil {
					key = t.Path
				} else {
					return nil, Errorf("文件櫃沒有「%s」這個區塊或頁籤", k)
				}
				if level == "none" {
					delete(g.DocAccess, key)
				} else {
					g.DocAccess[key] = level
				}
			}
		}
		a.UpsertAgent(g)
		verb := "修改"
		if isNew {
			verb = "新增"
		}
		a.Log(c.Actor, "person.badge.plus", verb+"角色「"+g.Name+"」", "automation")
		return agentJSON(a, g), nil
	})

	Register("ops_agent_task_create", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		ag := c.Args.Str("assign_to")
		if ag == "" {
			return nil, ToolError{"要用 assign_to 指定接任務的角色(agent 是你自己扮演的角色,用來署名)"}
		}
		target := a.FindAgent(ag)
		if target == nil {
			return nil, Errorf("找不到角色 %s", ag)
		}
		by, err := issueAuthor(c)
		if err != nil {
			return nil, err
		}
		if by == core.AgentPrefix+target.ID {
			return nil, Errorf("不能把任務派給自己(%s);要派給別的角色請改 assign_to", target.Name)
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		instructions, _ := c.Args.iaRaw("instructions")
		priority, err := c.Args.OptionalEnum("priority", core.IssuePriorities)
		if err != nil {
			return nil, err
		}
		t := a.AssignTask(ag, title, instructions, priority, by, nil)
		if t == nil {
			return map[string]any{}, nil
		}
		return taskJSON(a, *t), nil
	})
}
