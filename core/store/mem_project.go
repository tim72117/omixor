package store

import (
	"fmt"

	"omixor-core/model"
)

// ---- 專案(Mem;欄位宣告在 mem.go;專案的文件櫃結構在 mem.go 的 GetCabinetLayout / SaveCabinetLayout) ----

func (m *Mem) ListProjects(org string) ([]model.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.Project{}, m.projects[org]...), nil
}

func (m *Mem) GetProject(org, id string) (*model.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.projects[org] {
		if p.ID == id {
			c := p
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Mem) CreateProject(org string, p model.Project) (model.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projects == nil {
		m.projects = map[string][]model.Project{}
	}
	if m.projSeq == nil {
		m.projSeq = map[string]int{}
	}
	// 號碼不重複使用:取「最後發出去的號碼」和現有最大號碼的較大者再加一(刪掉最大號的專案,號碼也不會再發)
	n := m.projSeq[org]
	for _, x := range m.projects[org] {
		n = max(n, x.Number())
	}
	p.ID = fmt.Sprintf("%s%d", model.ProjectPrefix, n+1)
	m.projSeq[org] = n + 1
	m.projects[org] = append(m.projects[org], p)
	return p, nil
}

func (m *Mem) UpdateProject(org string, p model.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, x := range m.projects[org] {
		if x.ID == p.ID {
			x.Name, x.Description, x.UpdatedAt = p.Name, p.Description, p.UpdatedAt
			m.projects[org][k] = x
			return nil
		}
	}
	return ErrNotFound
}

func (m *Mem) DeleteProject(org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, x := range m.projects[org] {
		if x.ID == id {
			m.projects[org] = append(m.projects[org][:k:k], m.projects[org][k+1:]...)
			delete(m.projLayouts[org], id)
			return nil
		}
	}
	return ErrNotFound
}
