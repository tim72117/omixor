package mcp

import (
	"strings"

	"omixor-core/backend"
	"omixor-core/model"
)

// Role 角色端點一次連線的身分:由憑證決定,呼叫端自帶的 agent 參數不算數
type Role struct {
	Org     string
	AgentID string
	TaskID  string // 憑證綁定的任務
}

// AgentEnv 虛擬角色工具的執行環境(在 Env 之外多一些角色端點與管理端點需要的資訊)
type AgentEnv interface {
	Env
	// Role 角色端點的憑證身分;管理端點是 nil
	Role() *Role
	// OrgName 目前組織的名稱
	OrgName() string
	// AgentName 角色 id → 名稱(找不到回 id)
	AgentName(id string) string
	// ContextExtra 附加到 ops_agent_context 輸出的欄位(例如 web 的文件櫃:cabinet、docs);沒有回 nil
	ContextExtra(g model.Agent, includeBodies bool) map[string]any
	// EndpointURL 領取任務時告訴呼叫端的角色端點網址;不知道回空字串
	EndpointURL() string
}

// AgentDecorator 可選:執行環境(Call.Ctx)實作它,就能改寫角色的輸出(例如 web 把 docAccess 換成看得懂的文件櫃範圍)
type AgentDecorator interface {
	DecorateAgent(g model.Agent, out map[string]any)
}

// DocAccessResolver 可選:ops_agent_upsert 的 doc_access 的 key 先經過它換成存起來的 key
// (例如 web 的文件櫃:區塊 id 或頁籤名稱 → 區塊 id 或頁籤網址);不認得回 ToolError
type DocAccessResolver interface {
	ResolveDocAccessKey(k string) (string, error)
}

func agentEnv(c *Call) AgentEnv {
	e, ok := c.Ctx.(AgentEnv)
	if !ok {
		panic("MCP 沒有設定虛擬角色的執行環境(Call.Ctx 沒實作 mcp.AgentEnv)")
	}
	return e
}

// RoleOf 這次工具呼叫是不是角色端點(憑證身分)。額外掛上來的工具(例如文件櫃)用它取得角色
func RoleOf(c *Call) (Role, bool) {
	if e, ok := c.Ctx.(AgentEnv); ok {
		if r := e.Role(); r != nil {
			return *r, true
		}
	}
	return Role{}, false
}

// Session 一次 MCP 連線的環境:Backend(B)與組織(OrgID),加上可選的掛載點。
// 角色端點與管理端點都用它(兩者只差在 Backend 的身分與 Role)。
type Session struct {
	B     backend.Backend
	OrgID string
	// OrgName 組織名稱;nil 時從 Backend.ListOrgs 找
	OrgNameFn func() string
	// AgentNameFn 角色 id → 名稱;nil 時從 Backend.GetAgent 找
	AgentNameFn func(id string) string
	// Extra 附加到 ops_agent_context 的欄位
	Extra func(g model.Agent, includeBodies bool) map[string]any
	// Decorate 改寫角色的輸出(例如 web 把 docAccess 換成看得懂的文件櫃範圍);nil = 不改
	Decorate func(g model.Agent, out map[string]any)
	// Endpoint 角色端點網址(領取任務時告訴呼叫端)
	Endpoint string
	// Identity 角色端點才有
	Identity *Role

	names map[string]string
}

var _ AgentEnv = (*Session)(nil)

func (s *Session) Lock()                    {}
func (s *Session) Unlock()                  {}
func (s *Session) Backend() backend.Backend { return s.B }
func (s *Session) Org() string { return s.OrgID }
func (s *Session) Role() *Role              { return s.Identity }
func (s *Session) EndpointURL() string      { return s.Endpoint }

func (s *Session) DecorateAgent(g model.Agent, out map[string]any) {
	if s.Decorate != nil {
		s.Decorate(g, out)
	}
}

func (s *Session) ContextExtra(g model.Agent, include bool) map[string]any {
	if s.Extra == nil {
		return nil
	}
	return s.Extra(g, include)
}

func (s *Session) OrgName() string {
	if s.OrgNameFn != nil {
		return s.OrgNameFn()
	}
	if l, err := s.B.ListOrgs(); err == nil {
		for _, o := range l {
			if o.ID == s.OrgID {
				return o.Name
			}
		}
	}
	return s.OrgID
}

func (s *Session) AgentName(id string) string {
	if n, ok := s.names[id]; ok {
		return n
	}
	name := id
	if s.AgentNameFn != nil {
		name = s.AgentNameFn(id)
	} else if g, err := s.B.GetAgent(s.OrgID, id); err == nil {
		name = g.Name
	}
	if s.names == nil {
		s.names = map[string]string{}
	}
	s.names[id] = name
	return name
}

// Author 署名:角色端點一律是憑證的角色;管理端點有給 agent 就是該角色(找不到回錯誤),沒給回空字串(由 service 決定)
func (s *Session) Author(agent string) (string, error) {
	if s.Identity != nil {
		return model.AgentPrefix + s.Identity.AgentID, nil
	}
	if strings.TrimSpace(agent) == "" {
		return "", nil
	}
	g, err := s.B.GetAgent(s.OrgID, agent)
	if err != nil {
		return "", Errorf("找不到角色 %s", agent)
	}
	return model.AgentPrefix + g.ID, nil
}

func (s *Session) AuthorName(author string) string {
	if id, ok := strings.CutPrefix(author, model.AgentPrefix); ok {
		return s.AgentName(id)
	}
	if author == model.RoutineAuthor {
		return "例行排程"
	}
	return author
}
