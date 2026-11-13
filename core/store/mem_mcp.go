package store

import "omixor-core/model"

// ---- 組織的外部 MCP 登記(Mem;欄位宣告在 mem.go;沒有任何密鑰,見 model/mcp.go) ----

func (m *Mem) GetMCPConfig(org string) (*model.OrgMCPConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.mcp[org]
	if !ok {
		return nil, ErrNotFound
	}
	c := x.Clone()
	return &c, nil
}

func (m *Mem) SaveMCPConfig(org string, c model.OrgMCPConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mcp == nil {
		m.mcp = map[string]model.OrgMCPConfig{}
	}
	m.mcp[org] = c.Clone()
	return nil
}
