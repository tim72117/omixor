package omixor

import (
	"bytes"
	"sort"
	"sync"
	"time"

	"opsweb/internal/core"
)

// MemStore 記憶體裡的存放處(測試與本機試用)
type MemStore struct {
	mu      sync.Mutex
	users   map[string]User
	tokens  map[string]*memToken
	audit   []AuditEntry
	nowFunc func() time.Time
}

type memToken struct {
	Token
	revoked bool
}

func NewMemStore() *MemStore {
	return &MemStore{users: map[string]User{}, tokens: map[string]*memToken{}}
}

func (m *MemStore) now() time.Time {
	if m.nowFunc != nil {
		return m.nowFunc()
	}
	return time.Now().UTC()
}

func (m *MemStore) CountUsers() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *MemStore) ListUsers() ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []User{}
	for _, u := range m.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) find(f func(User) bool) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if f(u) {
			c := u
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemStore) UserByEvoleapID(id string) (*User, error) {
	return m.find(func(u User) bool { return u.EvoleapID != "" && u.EvoleapID == id })
}
func (m *MemStore) UserByEmail(e string) (*User, error) {
	return m.find(func(u User) bool { return u.Email == e })
}
func (m *MemStore) SaveUser(u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[u.ID] = u
	return nil
}

func (m *MemStore) CreateToken(t Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[t.ID] = &memToken{Token: t}
	return nil
}

func (m *MemStore) TokenByHash(h []byte) (*Token, *User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if bytes.Equal(t.Hash, h) && !t.revoked && (t.ExpiresAt == nil || t.ExpiresAt.After(m.now())) {
			u := m.users[t.UserID]
			c := t.Token
			c.Email = u.Email
			return &c, &u, nil
		}
	}
	return nil, nil, ErrNotFound
}

func (m *MemStore) TouchToken(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tokens[id]; ok {
		t.LastUsedAt = &at
	}
	return nil
}

func (m *MemStore) RevokeToken(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok || t.revoked {
		return ErrNotFound
	}
	t.revoked = true
	return nil
}

func (m *MemStore) ListTokens(now time.Time) ([]Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Token{}
	for _, t := range m.tokens {
		if !t.revoked && (t.ExpiresAt == nil || t.ExpiresAt.After(now)) {
			c := t.Token
			c.Email = m.users[t.UserID].Email
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *MemStore) AddAudit(e AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.ID = int64(len(m.audit) + 1)
	m.audit = append(m.audit, e)
	return nil
}

func (m *MemStore) ListAudit(limit int) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []AuditEntry{}
	for i := len(m.audit) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.audit[i])
	}
	return out, nil
}

// MemIssues 記憶體裡的 Issue(測試用;匯入規則同 core.PGImportIssues)
type MemIssues struct {
	mu   sync.Mutex
	Orgs map[string][]core.OpsIssue
}

func (m *MemIssues) Read(org string) ([]core.OpsIssue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]core.OpsIssue{}, m.Orgs[org]...), nil
}

func (m *MemIssues) Import(org string, list []core.OpsIssue, overwrite bool) (core.IssueImportResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Orgs == nil {
		m.Orgs = map[string][]core.OpsIssue{}
	}
	res := core.IssueImportResult{Org: org}
	cur := m.Orgs[org]
	for _, i := range list {
		idx := -1
		for k := range cur {
			if cur[k].ID == i.ID {
				idx = k
			}
		}
		switch {
		case idx < 0:
			cur = append(cur, i)
			res.Inserted++
		case overwrite:
			cur[idx] = i
			res.Updated++
		default:
			res.Skipped++
		}
	}
	m.Orgs[org] = cur
	return res, nil
}
