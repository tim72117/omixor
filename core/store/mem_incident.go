package store

import (
	"fmt"

	"omixor-core/model"
)

// ---- 事件(Mem;欄位宣告在 mem.go) ----

func (m *Mem) ListIncidents(org string) ([]model.Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Incident{}
	for _, i := range m.incidents[org] {
		out = append(out, i.Clone())
	}
	return out, nil
}

func (m *Mem) GetIncident(org, id string) (*model.Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.incidents[org] {
		if i.ID == id {
			c := i.Clone()
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Mem) CreateIncident(org string, i model.Incident) (model.Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.incidents == nil {
		m.incidents = map[string][]model.Incident{}
	}
	n := model.IncidentNumberBase
	for _, x := range m.incidents[org] {
		n = max(n, x.Number())
	}
	i.ID = fmt.Sprintf("%s%d", model.IncidentPrefix, n+1)
	m.incidents[org] = append([]model.Incident{i.Clone()}, m.incidents[org]...)
	return i.Clone(), nil
}

func (m *Mem) UpdateIncident(org string, i model.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, x := range m.incidents[org] {
		if x.ID == i.ID {
			m.incidents[org][k] = i.Clone()
			return nil
		}
	}
	return ErrNotFound
}

func (m *Mem) ImportIncidents(org string, list []model.Incident) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.incidents[org]); n > 0 {
		return 0, fmt.Errorf("組織 %s 已經有 %d 筆事件", org, n)
	}
	if m.incidents == nil {
		m.incidents = map[string][]model.Incident{}
	}
	out := []model.Incident{}
	seen := map[string]bool{}
	for _, i := range list {
		if seen[i.ID] {
			return 0, fmt.Errorf("事件 id 重複:%s", i.ID)
		}
		seen[i.ID] = true
		out = append(out, i.Clone())
	}
	m.incidents[org] = out
	return len(out), nil
}
