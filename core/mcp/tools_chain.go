package mcp

import (
	"strings"

	"omixor-core/model"
	"omixor-core/service"
)

// 組織鏈工具:chain(讀)、chain_save(組織管理員)、agent_task_delegate(角色依鏈委派)。
// 規則都在 service/chain.go(透過 Backend);名稱、參數同 Swift 版 MCPHandler。

// ChainToolDefs 組織鏈工具的規格(接在 CoreToolDefs 後面)
func ChainToolDefs() []any {
	prio := []string{"urgent", "high", "normal", "low"}
	return []any{
		coreDef("chain",
			"列出組織鏈(資訊流動方向):每條邊 from → to 表示 from 可以派工作給 to(label 例如 目標、任務;空白 = 任務),to 做完回報給 from。節點是 agent:<角色 id> 或 user:<email>;fromName、toName 是顯示名稱。", true,
			obj(map[string]any{})),
		coreDef("chain_save",
			"修改組織鏈(組織管理員)。edges 取代全部邊;或用 add 加上(同 from、to 的會被取代)、remove 拿掉(依 from、to)。每條邊 {from, to, label}:節點寫 agent:<角色 id> 或 user:<email>,label 例如 目標、任務(空白 = 任務)。不能指向自己、不能重複、不能有循環,節點要存在(角色存在、人是組織成員)。", false,
			obj(map[string]any{
				"edges":  pObjs("取代全部邊:[{from, to, label}]"),
				"add":    pObjs("加上的邊:[{from, to, label}]"),
				"remove": pObjs("拿掉的邊:[{from, to}]"),
			})),
		coreDef("agent_task_delegate",
			"以角色身分把工作委派給組織鏈上的下游角色(鏈上要有 agent:<自己> → agent:<to>)。任務署名你的角色,links 帶上委派它的那筆任務與其 links,label(目標、任務…)寫在任務說明開頭;對方做完會自動回報給你(替你排一筆「檢視回報」任務)。可以派給誰見 agent_context 的 chain.downstream。", false,
			obj(map[string]any{
				"agent":        pStr("你自己正在扮演的角色 id 或名稱(委派者)"),
				"to":           pStr("接任務的角色 id 或名稱(要是鏈上的下游)"),
				"title":        pStr("標題"),
				"instructions": pStr("任務說明(Markdown)"),
				"priority":     pEnum("優先度,預設 normal", prio...),
				"task":         pStr("委派它的來源任務 id(選填;不給就用你的角色進行中的任務)"),
			}, "agent", "to", "title")),
	}
}

// nodeName 節點的顯示名稱:角色換成名稱,成員是 email
func nodeName(e AgentEnv, node string) string {
	if id, ok := strings.CutPrefix(node, model.AgentPrefix); ok {
		return e.AgentName(id)
	}
	return strings.TrimPrefix(node, model.UserPrefix)
}

func chainJSON(e AgentEnv, c model.OrgChain) map[string]any {
	edges := []any{}
	for _, x := range c.Edges {
		edges = append(edges, map[string]any{"from": x.From, "to": x.To, "label": x.Label,
			"fromName": nodeName(e, x.From), "toName": nodeName(e, x.To)})
	}
	out := map[string]any{"edges": edges}
	if c.UpdatedAt != nil {
		out["updatedAt"] = iso(c.UpdatedAt.Time)
	}
	return out
}

// chainLinks 這個角色在鏈上的上游(回報給誰)與下游(可以派給誰);取不到組織鏈回 nil
func chainLinks(e AgentEnv, agentID string) map[string]any {
	c, err := e.Backend().GetChain(e.Org())
	if err != nil {
		return nil
	}
	node := model.AgentNode(agentID)
	row := func(n string, x model.ChainEdge) map[string]any {
		return map[string]any{"node": n, "name": nodeName(e, n), "label": x.LabelOrDefault()}
	}
	up, down := []any{}, []any{}
	for _, x := range c.Upstream(node) {
		up = append(up, row(x.From, x))
	}
	for _, x := range c.Downstream(node) {
		down = append(down, row(x.To, x))
	}
	return map[string]any{"upstream": up, "downstream": down}
}

// downstreamAgents 「A(agent:a,目標)、B(agent:b,任務)」:可以委派的下游角色;沒有回空字串
func downstreamAgents(e AgentEnv, agentID string) string {
	c, err := e.Backend().GetChain(e.Org())
	if err != nil {
		return ""
	}
	parts := []string{}
	for _, x := range c.Downstream(model.AgentNode(agentID)) {
		if id, ok := strings.CutPrefix(x.To, model.AgentPrefix); ok {
			parts = append(parts, e.AgentName(id)+"("+id+","+x.LabelOrDefault()+")")
		}
	}
	return strings.Join(parts, "、")
}

func parseEdges(v any, key string) ([]model.ChainEdge, bool, error) {
	if v == nil {
		return nil, false, nil
	}
	list, ok := objects(v)
	if !ok {
		return nil, true, Errorf("%s 要是 [{from, to, label}] 的陣列", key)
	}
	out := []model.ChainEdge{}
	for _, d := range list {
		a := Args{Raw: d}
		out = append(out, model.ChainEdge{From: a.Str("from"), To: a.Str("to"), Label: a.Str("label")})
	}
	return out, true, nil
}

// RegisterChainTools 登記組織鏈工具。執行環境(Call.Ctx)要實作 AgentEnv
func RegisterChainTools(s *Server) {
	s.Register("chain", func(c *Call) (any, error) {
		e := agentEnv(c)
		ch, err := e.Backend().GetChain(e.Org())
		if err != nil {
			return nil, terr(err)
		}
		return chainJSON(e, *ch), nil
	})

	s.Register("chain_save", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		edges, hasEdges, err := parseEdges(c.Args.Raw["edges"], "edges")
		if err != nil {
			return nil, err
		}
		add, hasAdd, err := parseEdges(c.Args.Raw["add"], "add")
		if err != nil {
			return nil, err
		}
		remove, hasRemove, err := parseEdges(c.Args.Raw["remove"], "remove")
		if err != nil {
			return nil, err
		}
		if !hasEdges && !hasAdd && !hasRemove {
			return nil, ToolError{Message: "要給 edges(取代全部)或 add、remove"}
		}
		if !hasEdges {
			cur, err := bk.GetChain(org)
			if err != nil {
				return nil, terr(err)
			}
			edges = cur.Edges
		}
		// add、remove 依 from、to 比對(不分大小寫;角色名稱與 id 的換算由核心做,這裡只比對寫法)
		same := func(a, b model.ChainEdge) bool {
			return strings.EqualFold(strings.TrimSpace(a.From), strings.TrimSpace(b.From)) && strings.EqualFold(strings.TrimSpace(a.To), strings.TrimSpace(b.To))
		}
		drop := append(append([]model.ChainEdge{}, remove...), add...)
		kept := []model.ChainEdge{}
		for _, x := range edges {
			gone := false
			for _, r := range drop {
				if same(x, r) {
					gone = true
				}
			}
			if !gone {
				kept = append(kept, x)
			}
		}
		saved, err := bk.SaveChain(org, append(kept, add...))
		if err != nil {
			return nil, terr(err)
		}
		return chainJSON(e, *saved), nil
	})

	s.Register("agent_task_delegate", func(c *Call) (any, error) {
		e := agentEnv(c)
		from, err := c.Args.Require("agent")
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
		bk, org := e.Backend(), e.Org()
		in := service.DelegateInput{From: from, To: to, Title: title, Instructions: instructions, Priority: priority, TaskID: c.Args.Str("task")}
		if in.TaskID == "" && e.Role() == nil { // 沒有憑證綁定的任務:用委派者進行中、最近更新的那筆當來源
			if g, err := bk.GetAgent(org, from); err == nil {
				if l, err := bk.ListAgentTasks(org, service.TaskFilter{AgentID: g.ID, Status: "inProgress"}); err == nil && len(l) > 0 {
					best := l[0]
					for _, t := range l {
						if t.UpdatedAt.After(best.UpdatedAt.Time) {
							best = t
						}
					}
					in.TaskID = best.ID
				}
			}
		}
		t, err := bk.DelegateTask(org, in)
		if err != nil {
			return nil, terr(err)
		}
		return taskJSON(e, *t), nil
	})
}
