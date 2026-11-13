package store

import "omixor-core/model"

// ---- 組織的系統設定(功能開關;Mem;欄位宣告在 mem.go) ----

func (m *Mem) GetSystemSettings(org string) (*model.OrgSystemSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.system[org]
	if !ok {
		return nil, ErrNotFound
	}
	c := x.Clone()
	c.Saved = true
	return &c, nil
}

func (m *Mem) SaveSystemSettings(org string, s model.OrgSystemSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.system == nil {
		m.system = map[string]model.OrgSystemSettings{}
	}
	c := s.Clone()
	c.Saved = true
	m.system[org] = c
	return nil
}
