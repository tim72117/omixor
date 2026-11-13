package store

import "omixor-core/model"

// ---- 組織的 GitHub、GitLab 設定(Mem;欄位宣告在 mem.go) ----

func (m *Mem) GetGitHubSettings(org string) (*model.GitHubSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.integrations[org].GitHub
	if x == nil {
		return nil, ErrNotFound
	}
	c := x.Clone()
	c.Saved = true
	return &c, nil
}

func (m *Mem) SaveGitHubSettings(org string, s model.GitHubSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.integrations == nil {
		m.integrations = map[string]model.OrgIntegrations{}
	}
	x := m.integrations[org]
	c := s.Clone()
	c.Saved = true
	x.GitHub = &c
	m.integrations[org] = x
	return nil
}

func (m *Mem) GetGitLabSettings(org string) (*model.GitLabSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.integrations[org].GitLab
	if x == nil {
		return nil, ErrNotFound
	}
	c := x.Clone()
	c.Saved = true
	return &c, nil
}

func (m *Mem) SaveGitLabSettings(org string, s model.GitLabSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.integrations == nil {
		m.integrations = map[string]model.OrgIntegrations{}
	}
	x := m.integrations[org]
	c := s.Clone()
	c.Saved = true
	x.GitLab = &c
	m.integrations[org] = x
	return nil
}
