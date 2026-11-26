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
	return out, n, nil // 邊的端點是 actor id;對外輸出用 presentChain 換成節點寫法
}

// GetChain 讀組織鏈(組織成員,含角色身分)
func (s *Service) GetChain(p *model.Principal, org string) (*model.OrgChain, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	c, n, err := s.liveChain(org)
	if err != nil {
		return nil, err
	}
	return s.presentChain(c, n)
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
		from, err := s.resolveNode(p, org, e.From, n, "", modeChain)
		if err != nil {
			return nil, err
		}
		to, err := s.resolveNode(p, org, e.To, n, "", modeChain)
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
	return s.presentChain(c, n)
}

// pruneChainNode 把和這個節點(actor id)有關的邊從存檔拿掉(刪角色、移除成員時;失敗不影響原本的操作,讀取時也會過濾)
func (s *Service) pruneChainNode(org, node string) {
	_ = s.pruneChainNodeOn(s.Store, org, node)
}

// pruneChainNodeOn 同 pruneChainNode,但在指定的 store(例如 Atomic 的 tx)上做,並回傳錯誤(沒存過組織鏈不是錯誤)
func (s *Service) pruneChainNodeOn(st store.Store, org, node string) error {
	c, err := st.GetOrgChain(org)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	kept := []model.ChainEdge{}
	for _, e := range c.Edges {
		if e.From != node && e.To != node {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(c.Edges) {
		return nil
	}
	now := s.mt()
	return st.SaveOrgChain(org, model.OrgChain{Edges: kept, UpdatedAt: &now})
}

// downstreamText 「可以派給:A(目標)、B(任務)」;沒有回空字串
func downstreamText(c model.OrgChain, n *chainNodes, node string) string {
	parts := []string{}
	for _, e := range c.Downstream(node) {
		if n.isAgent(e.To) {
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
	return fail(403, "組織鏈上沒有 %s 可以派工的角色(要組織管理員在組織鏈加上 %s → agent:<角色 id> 的邊)", n.name(from), n.node(from))
}

// checkIssueAssign 把 Issue 指派給角色時的組織鏈檢查(Issue 的 assignee 會自動替該角色排任務,等於派工,所以不能繞過組織鏈):
//   - 組織還沒設定組織鏈(沒有任何邊):人不檢查,維持原本誰都能指派的行為;角色身分一律要有邊(ISS-7),所以全部擋下。
//   - 有設定組織鏈:組織管理員不受限(和 AssignTask 一樣);一般成員要有 user:<自己> → agent:<目標> 的邊;
//     角色身分(角色憑證)要有 agent:<自己> → agent:<目標> 的邊。沒有邊回 403,訊息說明可以指派給誰。
//
// assignee 是已經解析過的角色 actor id(resolveIssueAssignee 的結果)
func (s *Service) checkIssueAssign(p *model.Principal, org, assignee string) error {
	if strings.TrimSpace(assignee) == "" {
		return nil
	}
	c, n, err := s.liveChain(org)
	if err != nil {
		return err
	}
	if len(c.Edges) == 0 && p.Agent == nil {
		return nil
	}
	if p.Agent == nil {
		if role, err := s.orgRole(p, org); err != nil {
			return err
		} else if role == "admin" {
			return nil
		}
	}
	self, err := s.SelfActor(p) // 組織鏈的端點是 actor id:from = 呼叫者本人、to = 被指派角色的 actor id
	if err != nil {
		return err
	}
	from, to := self.ID, assignee
	if _, ok := c.Edge(from, to); !ok {
		err := chainDenied(c, n, from, to)
		var se *Error
		if p.Agent != nil && errors.As(err, &se) {
			se.Data = map[string]any{"code": AgentAssignForbiddenCode}
		}
		return err
	}
	return nil
}

// memberAssign 一般成員派工:鏈上要有 user:<自己> → agent:<目標>(呼叫端已確認不是組織管理員)。回傳要寫進任務說明的 label
func (s *Service) memberAssign(p *model.Principal, org string, g *model.Agent) (string, error) {
	c, n, err := s.liveChain(org)
	if err != nil {
		return "", err
	}
	me, err := s.selfActorID(org, p)
	if err != nil {
		return "", err
	}
	e, ok := c.Edge(me, n.agentActor(g.ID))
	if !ok {
		return "", chainDenied(c, n, me, n.agentActor(g.ID))
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
	fromActor, toActor := n.agentActor(from), n.agentActor(target.ID)
	e, ok := c.Edge(fromActor, toActor)
	if !ok {
		return nil, chainDenied(c, n, fromActor, toActor)
	}
	links := []string{}
	if srcID != "" {
		src, err := s.findTask(org, srcID)
		if err != nil {
			return nil, err
		}
		if src.AgentID != from {
			return nil, fail(400, "來源任務 %s 不是 %s 的任務", src.ID, n.name(fromActor))
		}
		links = append(append(links, src.ID), src.Links...)
	}
	t, err := s.insertTask(org, target, title, model.LabeledInstructions(e.Label, in.Instructions), in.Priority, fromActor, links)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "agent.task.delegate", org, map[string]any{"task": t.ID, "from": from, "agent": target.ID, "source": srcID})
	return s.presentTask(t)
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
	creator := t.CreatedBy // 建立者的 actor id;人或角色才是鏈上節點(routine、chain、omixor 等系統 actor 不是)
	if !strings.HasPrefix(creator, "usr_") && !strings.HasPrefix(creator, "agt_") {
		return // routine、chain(檢視回報任務)、mcp 等不是鏈上節點:不回報,也就不會連鎖
	}
	c, n, err := s.liveChain(org)
	if err != nil {
		s.Audit(p, "chain.report.error", org, map[string]any{"task": t.ID, "error": auditErrText(err)})
		return
	}
	if _, ok := c.Edge(creator, n.agentActor(t.AgentID)); !ok {
		return
	}
	who := n.name(n.agentActor(t.AgentID))
	text := ""
	if g, ok := n.byActor[creator]; ok { // 建立者是角色
		all, err := s.Store.ListAgentTasks(org)
		if err != nil {
			s.Audit(p, "chain.report.error", org, map[string]any{"task": t.ID, "error": auditErrText(err)})
			return
		}
		for _, x := range all { // 同一筆原任務已有未結束的檢視任務:不重複排
			if x.AgentID == g.ID && x.CreatedBy == model.SystemActorChain && model.TaskIsOpen(x.Status) && len(x.Links) > 0 && strings.EqualFold(x.Links[0], t.ID) {
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
			nt, err := s.insertTask(org, &g, "檢視回報:"+t.Title, b.String(), t.Priority, model.SystemActorChain, links)
			if err != nil {
				s.Audit(p, "chain.report.error", org, map[string]any{"task": t.ID, "error": auditErrText(err)})
				return
			}
			text = fmt.Sprintf("%s%s(%s)", model.ChainReportPrefix, g.Name, nt.ID)
			s.Audit(p, "chain.report", org, map[string]any{"task": t.ID, "to": creator, "review": nt.ID})
		}
	} else {
		text = model.ChainReportPrefix + n.name(creator)
		s.Audit(p, "chain.report", org, map[string]any{"task": t.ID, "to": creator})
	}
	t.Notes = append(t.Notes, model.AgentTaskNote{ID: uuid(), Author: model.SystemActorChain, Text: text, At: s.mt()})
	if err := s.Store.UpdateAgentTask(org, *t); err != nil {
		s.Audit(p, "chain.report.error", org, map[string]any{"task": t.ID, "error": auditErrText(err)})
	}
}

// reportedTo 任務有沒有「已回報給…」的備註(組織鏈回報過)
func reportedTo(t model.AgentTask) bool {
	for _, n := range t.Notes {
		if n.Author == model.SystemActorChain && strings.HasPrefix(n.Text, model.ChainReportPrefix) {
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
