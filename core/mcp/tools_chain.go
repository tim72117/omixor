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
			"Lists the organization chain (direction of information flow): each edge from → to means from can assign work to to (label e.g. goal, task; empty = task), and to reports back to from when done. Nodes are agent:<agent id> or user:<email>; fromName and toName are display names.", true,
			obj(map[string]any{})),
		coreDef("chain_save",
			"Modifies the organization chain (organization admin). edges replaces all edges; or use add to add (an edge with the same from and to is replaced) and remove to delete (matched by from and to). Each edge is {from, to, label}: write nodes as agent:<agent id> or user:<email>, label e.g. goal, task (empty = task). An edge cannot point to itself, be duplicated or form a cycle, and its nodes must exist (the agent exists, the person is an organization member).", false,
			obj(map[string]any{
				"edges":  pObjs("Replaces all edges: [{from, to, label}]"),
				"add":    pObjs("Edges to add: [{from, to, label}]"),
				"remove": pObjs("Edges to remove: [{from, to}]"),
			})),
		coreDef("agent_task_delegate",
			"(Agent endpoint only: the delegator is determined by the credential) Delegates work, as an agent, to a downstream agent on the organization chain (the chain must contain agent:<yourself> → agent:<to>). The task is signed by your agent, links include the task being delegated from and its links, and a label (goal, task...) is written at the start of the task description; when the other side finishes, it reports back to you automatically (a \"review report\" task is queued for you). See chain.downstream of agent_context for who you can delegate to.", false,
			obj(map[string]any{
				"to":           pStr("Id or name of the agent that takes the task (must be downstream on the chain)"),
				"title":        pStr("Title"),
				"instructions": pStr("Task description (Markdown)"),
				"priority":     pEnum("Priority, default normal", prio...),
				"task":         pStr("Id of the source task being delegated from (optional; omitted = your agent's in-progress task)"),
			}, "to", "title")),
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
		edge := map[string]any{"from": x.From, "to": x.To, "label": x.Label,
			"fromName": nodeName(e, x.From), "toName": nodeName(e, x.To)}
		putActor(edge, "fromActor", x.FromActor)
		putActor(edge, "toActor", x.ToActor)
		edges = append(edges, edge)
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
		r := map[string]any{"node": n, "name": nodeName(e, n), "label": x.LabelOrDefault()}
		if n == x.From { // 上游端點是 from、下游是 to
			putActor(r, "actor", x.FromActor)
		} else {
			putActor(r, "actor", x.ToActor)
		}
		return r
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
	return strings.Join(parts, ", ")
}

func parseEdges(v any, key string) ([]model.ChainEdge, bool, error) {
	if v == nil {
		return nil, false, nil
	}
	list, ok := objects(v)
	if !ok {
		return nil, true, Errorf("%s must be an array of [{from, to, label}]", key)
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
			return nil, ToolError{Message: "Give edges (replace all) or add / remove"}
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
		if e.Role() == nil { // 委派者只能由角色憑證決定
			return nil, ToolError{Message: IdentityAgentError, Status: 400}
		}
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
		t, err := bk.DelegateTask(org, in)
		if err != nil {
			return nil, terr(err)
		}
		return taskJSON(e, *t), nil
	})
}
