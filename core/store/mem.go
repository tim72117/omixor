package store

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"omixor-core/model"
)

// Mem 記憶體裡的存放處(測試、比對用;程式結束就沒了)
type Mem struct {
	mu      sync.Mutex
	users   map[string]model.User
	tokens  map[string]*memToken
	audit   []model.AuditEntry
	orgs    map[string]model.Org
	members map[string]map[string]model.Member // org → userID
	issues  map[string][]model.Issue           // org → 新的在前
}

type memToken struct {
	model.Token
	revoked bool
}

func NewMem() *Mem {
	return &Mem{users: map[string]model.User{}, tokens: map[string]*memToken{}, orgs: map[string]model.Org{},
		members: map[string]map[string]model.Member{}, issues: map[string][]model.Issue{}}
}

func (m *Mem) CountUsers() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *Mem) ListUsers() ([]model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.User{}
	for _, u := range m.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt.Time) })
	return out, nil
}

func (m *Mem) findUser(f func(model.User) bool) (*model.User, error) {
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

func (m *Mem) UserByID(id string) (*model.User, error) {
	return m.findUser(func(u model.User) bool { return u.ID == id })
}
func (m *Mem) UserByEvoleapID(id string) (*model.User, error) {
	return m.findUser(func(u model.User) bool { return u.EvoleapID != "" && u.EvoleapID == id })
}
func (m *Mem) UserByEmail(e string) (*model.User, error) {
	return m.findUser(func(u model.User) bool { return u.Email == e })
}

func (m *Mem) SaveUser(u model.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, o := range m.users {
		if id != u.ID && o.Email == u.Email {
			return fmt.Errorf("email 重複:%s", u.Email)
		}
	}
	m.users[u.ID] = u
	return nil
}

func (m *Mem) CreateToken(t model.Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[t.ID] = &memToken{Token: t}
	return nil
}

func (m *Mem) TokenByHash(h []byte, now time.Time) (*model.Token, *model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if bytes.Equal(t.Hash, h) && !t.revoked && (t.ExpiresAt == nil || t.ExpiresAt.After(now)) {
			u := m.users[t.UserID]
			c := t.Token
			c.Email = u.Email
			return &c, &u, nil
		}
	}
	return nil, nil, ErrNotFound
}

func (m *Mem) TouchToken(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tokens[id]; ok {
		tt := model.Time{Time: at}
		t.LastUsedAt = &tt
	}
	return nil
}

func (m *Mem) RevokeToken(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok || t.revoked {
		return ErrNotFound
	}
	t.revoked = true
	return nil
}

func (m *Mem) ListTokens(now time.Time) ([]model.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Token{}
	for _, t := range m.tokens {
		if !t.revoked && (t.ExpiresAt == nil || t.ExpiresAt.After(now)) {
			c := t.Token
			c.Email = m.users[t.UserID].Email
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt.Time) })
	return out, nil
}

func (m *Mem) AddAudit(e model.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.ID = int64(len(m.audit) + 1)
	m.audit = append(m.audit, e)
	return nil
}

func (m *Mem) ListAudit(limit int) ([]model.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.AuditEntry{}
	for i := len(m.audit) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.audit[i])
	}
	return out, nil
}

func (m *Mem) ListOrgs() ([]model.Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Org{}
	for _, o := range m.orgs {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt.Time) || (out[i].CreatedAt.Equal(out[j].CreatedAt.Time) && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (m *Mem) GetOrg(id string) (*model.Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &o, nil
}

func (m *Mem) CreateOrg(o model.Org) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orgs[o.ID]; ok {
		return fmt.Errorf("組織 %s 已經存在", o.ID)
	}
	m.orgs[o.ID] = o
	return nil
}

func (m *Mem) UpdateOrg(n model.Org) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[n.ID]
	if !ok {
		return ErrNotFound
	}
	o.Name, o.Symbol, o.Color, o.Note = n.Name, n.Symbol, n.Color, n.Note
	m.orgs[n.ID] = o
	return nil
}

func (m *Mem) DeleteOrg(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orgs[id]; !ok {
		return ErrNotFound
	}
	delete(m.orgs, id)
	delete(m.members, id)
	delete(m.issues, id)
	return nil
}

func (m *Mem) MemberRole(org, userID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mm, ok := m.members[org][userID]; ok {
		return mm.Role, nil
	}
	return "", ErrNotFound
}

func (m *Mem) ListMembers(org string) ([]model.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Member{}
	for _, mm := range m.members[org] {
		u := m.users[mm.UserID]
		mm.Email, mm.Name = u.Email, u.Name
		out = append(out, mm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

func (m *Mem) SaveMember(mm model.Member) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.members[mm.Org] == nil {
		m.members[mm.Org] = map[string]model.Member{}
	}
	m.members[mm.Org][mm.UserID] = mm
	return nil
}

func (m *Mem) RemoveMember(org, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.members[org][userID]; !ok {
		return ErrNotFound
	}
	delete(m.members[org], userID)
	return nil
}

func (m *Mem) OrgsOfUser(userID string) ([]model.Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Org{}
	for id, mm := range m.members {
		if x, ok := mm[userID]; ok {
			o := m.orgs[id]
			o.MyRole = x.Role
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func cloneIssue(i model.Issue) model.Issue {
	i.Labels = append([]string{}, i.Labels...)
	i.Comments = append([]model.Comment{}, i.Comments...)
	return i
}

func (m *Mem) ListIssues(org string) ([]model.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Issue{}
	for _, i := range m.issues[org] {
		out = append(out, cloneIssue(i))
	}
	return out, nil
}

func (m *Mem) GetIssue(org, id string) (*model.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.issues[org] {
		if strings.EqualFold(i.ID, id) {
			c := cloneIssue(i)
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Mem) CreateIssue(org string, i model.Issue) (model.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, x := range m.issues[org] {
		n = max(n, x.Number())
	}
	i.ID = fmt.Sprintf("ISS-%d", n+1)
	m.issues[org] = append([]model.Issue{cloneIssue(i)}, m.issues[org]...)
	return i, nil
}

func (m *Mem) UpdateIssue(org string, i model.Issue) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, x := range m.issues[org] {
		if x.ID == i.ID {
			m.issues[org][k] = cloneIssue(i)
			return nil
		}
	}
	return ErrNotFound
}

func (m *Mem) DeleteIssue(org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := []model.Issue{}
	found := false
	for _, x := range m.issues[org] {
		if x.ID == id {
			found = true
			continue
		}
		kept = append(kept, x)
	}
	if !found {
		return ErrNotFound
	}
	m.issues[org] = kept
	return nil
}

func (m *Mem) ImportIssues(org string, list []model.Issue) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.issues[org]) > 0 {
		return 0, fmt.Errorf("組織 %s 已經有 Issue", org)
	}
	cp := []model.Issue{}
	for _, i := range list {
		cp = append(cp, cloneIssue(i))
	}
	m.issues[org] = cp
	return len(cp), nil
}

func (m *Mem) IssueOrgs() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for o, l := range m.issues {
		if len(l) > 0 {
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out, nil
}
