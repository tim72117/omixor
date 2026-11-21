package service

import (
	"errors"
	"strings"

	"omixor-core/model"
)

// 組織鏈與頻道共用的節點解析(ISS-36 C-2c):
//
//	鏈上的節點、頻道的成員、已讀與提及的對象,內部一律是 actor id(usr_… / agt_…);對外輸出才換成相容的 user:<email> / agent:<handle>(見 chain_present.go)。
//	輸入(組織鏈的邊、頻道成員、@ 之外的 as)走共用解析器 ResolveWho(actor_input.go):接受 actor id、@handle、handle、email、agent:<handle>、user:<handle|email>;
//	另外保留「角色名稱」的比對(舊行為:agent:研發長、研發長)。找不到不建立;人還要是組織成員(頻道的呼叫者自己例外)。

// chainNodes 組織目前存在的節點:角色(handle → 角色、actor id → 角色)與成員(actor id → 成員)
type chainNodes struct {
	s       *Service
	agents  map[string]model.Agent
	byActor map[string]model.Agent
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
	n := &chainNodes{s: s, agents: map[string]model.Agent{}, byActor: map[string]model.Agent{}, members: map[string]model.Member{}}
	for _, g := range agents {
		n.agents[g.ID] = g
		if g.ActorID != "" {
			n.byActor[g.ActorID] = g
		}
	}
	for _, m := range members {
		if m.ActorID != "" {
			n.members[m.ActorID] = m
		}
	}
	return n, nil
}

// exists 這個 actor id 是組織現存的節點(現役角色、或組織成員)
func (n *chainNodes) exists(id string) bool {
	if _, ok := n.byActor[id]; ok {
		return true
	}
	_, ok := n.members[id]
	return ok
}

// isAgent 這個 actor id 是組織的角色
func (n *chainNodes) isAgent(id string) bool {
	_, ok := n.byActor[id]
	return ok
}

// agentActor 角色 id(handle)的 actor id;沒有回空字串
func (n *chainNodes) agentActor(handle string) string {
	return n.agents[handle].ActorID
}

// actorOfAgent 輸入是角色的 actor id 或角色 id(handle):回 actor id;對不上回空字串
func (n *chainNodes) actorOfAgent(idOrHandle string) string {
	if _, ok := n.byActor[idOrHandle]; ok {
		return idOrHandle
	}
	return n.agents[idOrHandle].ActorID
}

// node 相容的節點寫法:agent:<角色 id>、user:<email 小寫>;不在組織裡的查 actor 表,查不到回 id 本身
func (n *chainNodes) node(id string) string {
	if g, ok := n.byActor[id]; ok {
		return model.AgentNode(g.ID)
	}
	if m, ok := n.members[id]; ok {
		return model.UserNode(m.Email)
	}
	if r, err := n.s.Store.ActorByID(id); err == nil && r != nil {
		switch r.Kind {
		case model.ActorKindAgent:
			return model.AgentNode(r.Handle)
		case model.ActorKindUser:
			return model.UserNode(r.Handle)
		}
	}
	return id
}

// name 節點的顯示名稱(角色名稱、成員 email 小寫)
func (n *chainNodes) name(id string) string {
	if g, ok := n.byActor[id]; ok {
		return g.Name
	}
	if m, ok := n.members[id]; ok {
		return strings.ToLower(strings.TrimSpace(m.Email))
	}
	return strings.TrimPrefix(strings.TrimPrefix(n.node(id), model.UserPrefix), model.AgentPrefix)
}

// agentByName 用角色名稱(不分大小寫)找角色的 actor id
func (n *chainNodes) agentByName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	for _, g := range n.agents {
		if g.ActorID != "" && strings.EqualFold(g.Name, name) {
			return g.ActorID, true
		}
	}
	return "", false
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// selfActorID 呼叫者本人的 actor id(SelfActor)。使用者與角色都保證有 actor(本機使用者也是),
// 失敗是資料錯誤,直接回錯誤,不退回建立 legacy actor
func (s *Service) selfActorID(org string, p *model.Principal) (string, error) {
	ref, err := s.SelfActor(p)
	if err != nil {
		return "", err
	}
	return ref.ID, nil
}

type nodeMode int

const (
	modeChain   nodeMode = iota // 組織鏈的邊
	modeChannel                 // 頻道成員
)

// resolveNode 輸入的節點 → actor id。n 是組織現存的節點;self 是呼叫者自己的 actor id(頻道:人不是組織成員也可以加自己,例如伺服器管理員)
func (s *Service) resolveNode(p *model.Principal, org, raw string, n *chainNodes, self string, mode nodeMode) (string, error) {
	raw = strings.TrimSpace(raw)
	if mode == modeChannel {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "@"))
	}
	if raw == "" {
		if mode == modeChannel {
			return "", fail(400, "成員不能空白")
		}
		return "", fail(400, "組織鏈的節點不能空白")
	}
	if low := strings.ToLower(raw); model.ValidActorID(low) && (n.exists(low) || (self != "" && low == self)) {
		return low, nil // 已經是現存的 actor id:不必再查
	}
	notMember := func(who string) error {
		if mode == modeChannel {
			return fail(400, "%s 不是組織成員,不能加進頻道", who)
		}
		return fail(400, "組織鏈的節點 %s:%s 不是組織成員", raw, who)
	}
	ref, err := s.ResolveWho(p, org, raw, WantPeople)
	if err != nil {
		if !errors.Is(err, ErrActorNotFound) {
			return "", err
		}
		rest, hasAgent := cutPrefixFold(raw, model.AgentPrefix)
		_, hasUser := cutPrefixFold(raw, model.UserPrefix)
		if !hasUser && !strings.Contains(raw, "@") || hasAgent { // 角色名稱(舊行為:agent:研發長、研發長)
			if id, ok := n.agentByName(strings.TrimPrefix(strings.TrimSpace(rest), "@")); ok {
				return id, nil
			}
		}
		switch {
		case hasAgent:
			if mode == modeChannel {
				return "", fail(400, "找不到角色 %s", strings.TrimSpace(rest))
			}
			return "", fail(400, "組織鏈的節點 %s:找不到角色 %s", raw, strings.TrimSpace(rest))
		case hasUser || strings.Contains(raw, "@"):
			who, _ := cutPrefixFold(raw, model.UserPrefix)
			return "", notMember(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(who), "@")))
		case mode == modeChannel:
			return "", fail(400, "找不到成員 %s(要寫 agent:<角色 id>、角色名稱、user:<email> 或 email)", raw)
		}
		return "", fail(400, "組織鏈的節點要寫成 agent:<角色 id> 或 user:<email>,不是 %q", raw)
	}
	if ref.Kind == model.ActorKindUser && !n.exists(ref.ID) && ref.ID != self {
		return "", notMember(strings.ToLower(ref.Handle))
	}
	return ref.ID, nil
}
