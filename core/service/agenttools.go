package service

import (
	"strings"

	"omixor-core/model"
)

// 角色在角色端點(/mcp/agent)能用的核心工具:依角色目前的設定算出來。
// 角色端點的 tools/list 只列可用的(角色不用試了被擋才知道),agent_context 的 tools 與角色設定畫面也用同一份。
// 權限仍由各個 service 方法在呼叫時強制;這裡只決定「列不列」。

// AgentTool 一個角色端點工具與目前能不能用
type AgentTool struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Group     string `json:"group"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"` // 不能用的原因(要怎麼開)
	// 外部 MCP 閘道的工具(Source = "mcp"):由 App 的閘道提供,名稱是 <伺服器 id>__<工具名>(見 service/mcp.go)
	Source  string `json:"source,omitempty"`
	Server  string `json:"server,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Confirm bool   `json:"confirm,omitempty"` // 呼叫前要人確認
}

// AgentToolNames 角色端點可能開放的全部核心工具(登記表 model.CoreTools 的 Agent)
func AgentToolNames() []string {
	out := []string{}
	for _, t := range model.CoreTools {
		if t.Agent {
			out = append(out, t.Name)
		}
	}
	return out
}

// AgentTools 角色能用的工具(組織成員;角色身分只能查自己):角色端點的核心工具,
// 加上「外部 MCP 閘道」開著時 App 閘道提供的外部工具(依角色權限,見 service/mcp.go)
func (s *Service) AgentTools(p *model.Principal, org, id string) ([]AgentTool, error) {
	g, err := s.GetAgent(p, org, id)
	if err != nil {
		return nil, err
	}
	return append(s.agentTools(org, *g), s.mcpAgentTools(org, *g)...), nil
}

// AgentToolSet 角色端點用:可用的工具名稱(讀不到角色時只開任務與 Issue 這些不需要額外設定的)
func (s *Service) AgentToolSet(org, agentID string) map[string]bool {
	out := map[string]bool{}
	g, err := s.Store.GetAgent(org, agentID)
	if err != nil {
		g = &model.Agent{ID: agentID}
	}
	for _, t := range s.agentTools(org, *g) {
		if t.Available {
			out[t.Name] = true
		}
	}
	return out
}

// AgentToolsOf 同 AgentTools,但不檢查權限(角色端點內部用)
func (s *Service) AgentToolsOf(org string, g model.Agent) []AgentTool { return s.agentTools(org, g) }

func (s *Service) agentTools(org string, g model.Agent) []AgentTool {
	node := model.AgentPrefix + g.ID
	// 文件櫃:docAccess 有任何位置才能讀;有 write 才能寫
	read, write := len(g.DocAccess) > 0, false
	for _, v := range g.DocAccess {
		if strings.EqualFold(v, "write") {
			write = true
		}
	}
	// 組織鏈:有下游角色才能委派
	delegate := false
	if c, _, err := s.liveChain(org); err == nil {
		for _, e := range c.Downstream(node) {
			if strings.HasPrefix(e.To, model.AgentPrefix) {
				delegate = true
				break
			}
		}
	}
	// 訊息頻道:是任何一個頻道的成員才用得到
	member := false
	if l, err := s.Store.ListChannels(org); err == nil {
	outer:
		for _, c := range l {
			for _, m := range c.Members {
				if strings.EqualFold(m, node) {
					member = true
					break outer
				}
			}
		}
	}
	ok := map[string]bool{model.NeedDocRead: read, model.NeedDocWrite: write, model.NeedDownstream: delegate, model.NeedChannel: member}
	why := map[string]string{
		model.NeedDocRead:    "沒有文件權限(在角色設定的「文件權限」加位置)",
		model.NeedDocWrite:   "沒有可寫的文件位置(文件權限要設成「可寫」)",
		model.NeedDownstream: "組織鏈上沒有下游角色(在「組織鏈」加一條從這個角色出發的邊)",
		model.NeedChannel:    "還不是任何頻道的成員(把角色加進頻道)",
	}
	out := []AgentTool{}
	for _, ct := range model.CoreTools {
		if !ct.Agent {
			continue
		}
		t := AgentTool{Name: ct.Name, Label: ct.Title, Group: ct.Group, Available: true}
		if ct.Needs != "" && !ok[ct.Needs] {
			t.Available, t.Reason = false, why[ct.Needs]
		}
		out = append(out, t)
	}
	return out
}
