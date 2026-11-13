package service

import (
	"errors"
	"fmt"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 組織鏈(資訊流動方向,資料模型見 model/chain.go):
//
//	讀:組織成員(含角色身分);改:組織管理員(整份取代)
//	驗證:節點存在(角色存在、user 是組織成員)、不能指向自己、From+To 不重複、不能有循環(DAG),錯誤 400 並說明
//	派工:組織管理員照舊(AssignTask);一般成員要鏈上有 user:<自己> → agent:<目標>;
//	      角色身分用 DelegateTask,要鏈上有 agent:<自己> → agent:<目標>;執行者用 WorkerDelegate(同角色身分,要領取驗證碼)
//	回報:任務變成 done 或 blocked、建立者是鏈上節點而且有 建立者 → 任務角色 的邊:
//	      建立者是角色 → 替它排一筆「檢視回報」任務(createdBy = chain,不會再觸發回報);
//	      建立者是真人 → 在原任務留「已回報給 <email>」備註,用 ListAgentTasks 的 reportTo 查
//
// 被刪的角色、被移除的成員:刪除/移除時把相關的邊從存檔拿掉(pruneChainNode);讀取時也再過濾一次(liveChain),
// 所以就算有別的路徑讓節點消失(例如直接改資料),規則也只看得到還存在的節點。

// MaxChainEdges 一個組織最多幾條邊
const MaxChainEdges = 500

// maxChainLabel label 最多幾個字
const maxChainLabel = 40

// DelegateInput 依組織鏈委派任務
type DelegateInput struct {
	To           string `json:"to"`
	Title        string `json:"title"`
	Instructions string `json:"instructions"`
	Priority     string `json:"priority"`
	// From、TaskID 只有伺服器管理員(例如 omixor-web 內建核心、本機 MCP 以 agent 參數署名)給的才算數:
	// 委派者角色與來源任務。角色身分一律是憑證的角色與它綁定的任務
	From   string `json:"from,omitempty"`
	TaskID string `json:"taskId,omitempty"`
}

// chainNodes 組織目前存在的節點:角色(id → 角色)與成員(email 小寫 → 成員)
type chainNodes struct {
	agents  map[string]model.Agent
	members map[string]model.Member
}

func (s *Service) loadChainNodes(org string) (*chainNodes, error) {
	agents, err := s.Store.ListAgents(org)
	if err != nil {
		return nil, err
	}
	members, err := s.Store.ListMembers(org)
	if err != nil {
		return nil, err
	}
	n := &chainNodes{agents: map[string]model.Agent{}, members: map[string]model.Member{}}
	for _, g := range agents {
		n.agents[g.ID] = g
	}
	for _, m := range members {
		if e := strings.ToLower(strings.TrimSpace(m.Email)); e != "" {
			n.members[e] = m
		}
	}
	return n, nil
}

func (n *chainNodes) exists(node string) bool {
	if id, ok := strings.CutPrefix(node, model.AgentPrefix); ok {
		_, has := n.agents[id]
		return has
	}
	if e, ok := strings.CutPrefix(node, model.UserPrefix); ok {
		_, has := n.members[e]
		return has
	}
	return false
}

// name 節點的顯示名稱(角色名稱、成員 email)
func (n *chainNodes) name(node string) string {
	if id, ok := strings.CutPrefix(node, model.AgentPrefix); ok {
		if g, has := n.agents[id]; has {
			return g.Name
		}
		return id
	}
	if e, ok := strings.CutPrefix(node, model.UserPrefix); ok {
		return e
	}
	return node
}

// resolve 把輸入的節點換成存起來的寫法:agent:<id 或名稱>(不分大小寫)→ agent:<角色 id>;user:<email> → user:<email 小寫>
func (n *chainNodes) resolve(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if rest, ok := cutPrefixFold(raw, model.AgentPrefix); ok {
		rest = strings.TrimSpace(rest)
		for id := range n.agents {
			if strings.EqualFold(id, rest) {
				return model.AgentNode(id), nil
			}
		}
		for id, g := range n.agents {
			if g.Name == rest {
				return model.AgentNode(id), nil
			}
		}
		return "", fail(400, "組織鏈的節點 %s:找不到角色 %s", raw, rest)
	}
	if rest, ok := cutPrefixFold(raw, model.UserPrefix); ok {
		e := strings.ToLower(strings.TrimSpace(rest))
		if _, has := n.members[e]; !has {
			return "", fail(400, "組織鏈的節點 %s:%s 不是組織成員", raw, e)
		}
		return model.UserNode(e), nil
	}
	return "", fail(400, "組織鏈的節點要寫成 agent:<角色 id> 或 user:<email>,不是 %q", raw)
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// liveChain 存著的組織鏈,拿掉指向已不存在節點的邊;沒存過是空的
func (s *Service) liveChain(org string) (model.OrgChain, *chainNodes, error) {
	n, err := s.loadChainNodes(org)
	if err != nil {
		return model.OrgChain{}, nil, err
	}
	c, err := s.Store.GetOrgChain(org)
	if errors.Is(err, store.ErrNotFound) {
		return model.OrgChain{Edges: []model.ChainEdge{}}, n, nil
	}
	if err != nil {
		return model.OrgChain{}, nil, err
	}
	out := model.OrgChain{Edges: []model.ChainEdge{}, UpdatedAt: c.UpdatedAt}
	for _, e := range c.Edges {
		if n.exists(e.From) && n.exists(e.To) {
			out.Edges = append(out.Edges, e)
		}
	}
	return out, n, nil
}

// GetChain 讀組織鏈(組織成員,含角色身分)
func (s *Service) GetChain(p *model.Principal, org string) (*model.OrgChain, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	c, _, err := s.liveChain(org)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveChain 整份取代組織鏈(組織管理員)。節點會換成存起來的寫法(角色 id、email 小寫),label 去頭尾空白
func (s *Service) SaveChain(p *model.Principal, org string, edges []model.ChainEdge) (*model.OrgChain, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if len(edges) > MaxChainEdges {
		return nil, fail(400, "組織鏈最多 %d 條邊", MaxChainEdges)
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	n, err := s.loadChainNodes(org)
	if err != nil {
		return nil, err
	}
	out := []model.ChainEdge{}
	seen := map[string]bool{}
	for i, e := range edges {
		from, err := n.resolve(e.From)
		if err != nil {
			return nil, err
		}
		to, err := n.resolve(e.To)
		if err != nil {
			return nil, err
		}
		if from == to {
			return nil, fail(400, "第 %d 條邊 %s → %s 指向自己", i+1, n.name(from), n.name(to))
		}
		key := from + "\x00" + to
		if seen[key] {
			return nil, fail(400, "%s → %s 重複了(同一對節點只能有一條邊)", n.name(from), n.name(to))
		}
		seen[key] = true
		label := strings.TrimSpace(e.Label)
		if len([]rune(label)) > maxChainLabel {
			return nil, fail(400, "label 最多 %d 個字:%s", maxChainLabel, label)
		}
		out = append(out, model.ChainEdge{From: from, To: to, Label: label})
	}
	if cyc := model.ChainCycle(out); cyc != nil {
		names := []string{}
		for _, x := range cyc {
			names = append(names, n.name(x))
		}
		return nil, fail(400, "組織鏈不能有循環:%s", strings.Join(names, " → "))
	}
	now := s.mt()
	c := model.OrgChain{Edges: out, UpdatedAt: &now}
	if err := s.Store.SaveOrgChain(org, c); err != nil {
		return nil, err
	}
	s.Audit(p, "chain.save", org, map[string]any{"edges": len(out)})
	return &c, nil
}

// pruneChainNode 把和這個節點有關的邊從存檔拿掉(刪角色、移除成員時;失敗不影響原本的操作,讀取時也會過濾)
func (s *Service) pruneChainNode(org, node string) {
	c, err := s.Store.GetOrgChain(org)
	if err != nil {
		return
	}
	kept := []model.ChainEdge{}
	for _, e := range c.Edges {
		if e.From != node && e.To != node {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(c.Edges) {
		return
	}
	now := s.mt()
	_ = s.Store.SaveOrgChain(org, model.OrgChain{Edges: kept, UpdatedAt: &now})
}

// downstreamText 「可以派給:A(目標)、B(任務)」;沒有回空字串
func downstreamText(c model.OrgChain, n *chainNodes, node string) string {
	parts := []string{}
	for _, e := range c.Downstream(node) {
		if strings.HasPrefix(e.To, model.AgentPrefix) {
			parts = append(parts, fmt.Sprintf("%s(%s)", n.name(e.To), e.LabelOrDefault()))
		}
	}
	return strings.Join(parts, "、")
}

// chainDenied 依鏈不能派工時的 403(說明可以派給誰)
func chainDenied(c model.OrgChain, n *chainNodes, from, target string) error {
	if can := downstreamText(c, n, from); can != "" {
		return fail(403, "組織鏈上沒有 %s → %s 的邊,不能派工給 %s;依組織鏈可以派給:%s", n.name(from), n.name(target), n.name(target), can)
	}
	return fail(403, "組織鏈上沒有 %s 可以派工的角色(要組織管理員在組織鏈加上 %s → agent:<角色 id> 的邊)", n.name(from), from)
}

// memberAssign 一般成員派工:鏈上要有 user:<自己> → agent:<目標>(呼叫端已確認不是組織管理員)。回傳要寫進任務說明的 label
func (s *Service) memberAssign(p *model.Principal, org string, g *model.Agent) (string, error) {
	c, n, err := s.liveChain(org)
	if err != nil {
		return "", err
	}
	me := model.UserNode(p.User.Email)
	e, ok := c.Edge(me, model.AgentNode(g.ID))
	if !ok || strings.TrimSpace(p.User.Email) == "" {
		return "", chainDenied(c, n, me, model.AgentNode(g.ID))
	}
	return e.Label, nil
}

// DelegateTask 依組織鏈委派任務:角色身分(角色憑證、角色端點)把工作派給鏈上的下游角色(要有 agent:<自己> → agent:<目標>)。
// 任務 CreatedBy = agent:<自己>,links = [委派它的那筆任務(憑證綁定的任務)] + 那筆任務的 links,
// 說明開頭寫上邊的 label(「【目標】」;見 model.LabeledInstructions)。對方做完 done/blocked 會自動回報(見 chainReport)。
// 伺服器管理員可以用 From(與選填的 TaskID)指定委派者(同 TaskInput.Author 的規則);其他人 403。
func (s *Service) DelegateTask(p *model.Principal, org string, in DelegateInput) (*model.AgentTask, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	var from, srcID string
	switch {
	case p.Agent != nil:
		from, srcID = p.Agent.AgentID, p.Agent.TaskID
	case p.ServerAdmin() && strings.TrimSpace(in.From) != "":
		g, err := s.mustAgent(org, strings.TrimPrefix(strings.TrimSpace(in.From), model.AgentPrefix))
		if err != nil {
			return nil, err
		}
		from, srcID = g.ID, strings.TrimSpace(in.TaskID)
	default:
		return nil, fail(403, "只有角色身分(角色憑證、角色端點,或執行者的 dispatch_delegate)可以依組織鏈委派;人要派工請用派任務")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, fail(400, "要給任務標題")
	}
	if strings.TrimSpace(in.To) == "" {
		return nil, fail(400, "要用 to 指定接任務的角色")
	}
	target, err := s.mustAgent(org, strings.TrimPrefix(strings.TrimSpace(in.To), model.AgentPrefix))
	if err != nil {
		return nil, err
	}
	if target.ID == from {
		return nil, fail(400, "不能委派給自己(%s)", target.Name)
	}
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	c, n, err := s.liveChain(org)
	if err != nil {
		return nil, err
	}
	e, ok := c.Edge(model.AgentNode(from), model.AgentNode(target.ID))
	if !ok {
		return nil, chainDenied(c, n, model.AgentNode(from), model.AgentNode(target.ID))
	}
	links := []string{}
	if srcID != "" {
		src, err := s.findTask(org, srcID)
		if err != nil {
			return nil, err
		}
		if src.AgentID != from {
			return nil, fail(400, "來源任務 %s 不是 %s 的任務", src.ID, n.name(model.AgentNode(from)))
		}
		links = append(append(links, src.ID), src.Links...)
	}
	t, err := s.insertTask(org, target, title, model.LabeledInstructions(e.Label, in.Instructions), in.Priority, model.AgentNode(from), links)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "agent.task.delegate", org, map[string]any{"task": t.ID, "from": from, "agent": target.ID, "source": srcID})
	return t, nil
}

// WorkerDelegateInput 執行者委派:領取驗證碼 + 委派內容
type WorkerDelegateInput struct {
	Code string `json:"code"`
	DelegateInput
}

// WorkerDelegate 領取者本人以任務角色的身分依組織鏈委派(/mcp/worker 的 dispatch_delegate;規則同角色身分的 DelegateTask,要領取驗證碼)
func (s *Service) WorkerDelegate(p *model.Principal, org, taskID, code string, in DelegateInput) (*model.AgentTask, error) {
	ap, t, err := s.claimedAgent(p, org, taskID, code)
	if err != nil {
		return nil, err
	}
	in.From, in.TaskID = "", ""
	nt, err := s.DelegateTask(ap, org, in)
	if err == nil {
		s.Audit(p, "agent.task.worker.delegate", org, map[string]any{"task": nt.ID, "source": t.ID, "agent": t.AgentID})
	}
	return nt, err
}

// chainReport 任務變成 done 或 blocked 之後:建立者是鏈上節點、而且鏈上有 建立者 → 任務角色 的邊,就回報給建立者
// (呼叫端要拿著 agents.mu)。失敗只記操作紀錄,不讓回報本身失敗。
func (s *Service) chainReport(p *model.Principal, org string, t *model.AgentTask, note string) {
	if t.Status != "done" && t.Status != "blocked" {
		return
	}
	creator := model.NodeOfAuthor(t.CreatedBy)
	if creator == "" {
		return // routine、chain(檢視回報任務)、mcp 等不是鏈上節點:不回報,也就不會連鎖
	}
	c, n, err := s.liveChain(org)
	if err != nil {
		s.Audit(p, "chain.report.error", org, map[string]any{"task": t.ID, "error": err.Error()})
		return
	}
	if _, ok := c.Edge(creator, model.AgentNode(t.AgentID)); !ok {
		return
	}
	who := n.name(model.AgentNode(t.AgentID))
	text := ""
	if id, ok := strings.CutPrefix(creator, model.AgentPrefix); ok {
		g := n.agents[id]
		all, err := s.Store.ListAgentTasks(org)
		if err != nil {
			s.Audit(p, "chain.report.error", org, map[string]any{"task": t.ID, "error": err.Error()})
			return
		}
		for _, x := range all { // 同一筆原任務已有未結束的檢視任務:不重複排
			if x.AgentID == g.ID && x.CreatedBy == model.ChainAuthor && model.TaskIsOpen(x.Status) && len(x.Links) > 0 && strings.EqualFold(x.Links[0], t.ID) {
				text = fmt.Sprintf("%s%s(已有未結束的檢視任務 %s)", model.ChainReportPrefix, g.Name, x.ID)
				break
			}
		}
		if text == "" {
			var b strings.Builder
			fmt.Fprintf(&b, "%s 回報了你派的 %s「%s」:%s。\n\n", who, t.ID, t.Title, model.TaskStatusLabel(t.Status))
			if t.Status == "done" {
				r := strings.TrimSpace(t.Result)
				if r == "" {
					r = "(沒有填結果)"
				}
				b.WriteString("結果:\n\n" + truncRunes(r, 4000))
			} else {
				q := strings.TrimSpace(note)
				if q == "" && len(t.Notes) > 0 {
					q = t.Notes[len(t.Notes)-1].Text
				}
				if q == "" {
					q = "(沒有說明)"
				}
				b.WriteString("需要協助:\n\n" + truncRunes(q, 4000))
			}
			fmt.Fprintf(&b, "\n\n用 agent_tasks(id: %s)看完整內容與備註;檢視後決定是否結案、補充說明或再委派,再回報這筆任務。", t.ID)
			links := append([]string{t.ID}, t.Links...)
			nt, err := s.insertTask(org, &g, "檢視回報:"+t.Title, b.String(), t.Priority, model.ChainAuthor, links)
			if err != nil {
				s.Audit(p, "chain.report.error", org, map[string]any{"task": t.ID, "error": err.Error()})
				return
			}
			text = fmt.Sprintf("%s%s(%s)", model.ChainReportPrefix, g.Name, nt.ID)
			s.Audit(p, "chain.report", org, map[string]any{"task": t.ID, "to": creator, "review": nt.ID})
		}
	} else {
		text = model.ChainReportPrefix + strings.TrimPrefix(creator, model.UserPrefix)
		s.Audit(p, "chain.report", org, map[string]any{"task": t.ID, "to": creator})
	}
	t.Notes = append(t.Notes, model.AgentTaskNote{ID: uuid(), Author: model.ChainAuthor, Text: text, At: s.mt()})
	if err := s.Store.UpdateAgentTask(org, *t); err != nil {
		s.Audit(p, "chain.report.error", org, map[string]any{"task": t.ID, "error": err.Error()})
	}
}

// reportedTo 任務有沒有「已回報給…」的備註(組織鏈回報過)
func reportedTo(t model.AgentTask) bool {
	for _, n := range t.Notes {
		if n.Author == model.ChainAuthor && strings.HasPrefix(n.Text, model.ChainReportPrefix) {
			return true
		}
	}
	return false
}

// reportToNode 「回報給我的任務」的節點:me = 呼叫者;user:<email> 換小寫;agent:<id> 原樣
func reportToNode(p *model.Principal, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "me") {
		if p.Agent != nil {
			return model.AgentNode(p.Agent.AgentID), nil
		}
		if strings.TrimSpace(p.User.Email) == "" {
			return "", fail(400, "無法判斷你的 email,請改用 reportTo=user:<email>")
		}
		return model.UserNode(p.User.Email), nil
	}
	if rest, ok := cutPrefixFold(raw, model.UserPrefix); ok {
		return model.UserNode(rest), nil
	}
	if rest, ok := cutPrefixFold(raw, model.AgentPrefix); ok {
		return model.AgentNode(strings.TrimSpace(rest)), nil
	}
	return "", fail(400, "reportTo 要是 me、user:<email> 或 agent:<角色 id>")
}
