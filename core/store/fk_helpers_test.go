package store

import (
	"fmt"

	"testing"

	"omixor-core/model"
)

// makeAgent 建一個角色(已經有就整筆取代)
func makeAgent(t *testing.T, s Store, org, id string) {
	t.Helper()
	chk(t, s.SaveAgent(org, model.Agent{ID: id, Name: id, Active: true, CreatedAt: mt("2026-01-01T00:00:00Z")}))
}

// makeTasks 在組織建 n 筆排隊中的任務(T-1 起),屬於角色 dev(角色 dev、ops 沒有就先建)
func makeTasks(t *testing.T, s Store, org string, n int) {
	t.Helper()
	makeAgent(t, s, org, "dev")
	makeAgent(t, s, org, "ops")
	at := mt("2026-01-02T03:04:05Z")
	for k := range n {
		_, err := s.CreateAgentTask(org, model.AgentTask{AgentID: "dev", Title: fmt.Sprintf("t%d", k+1), Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at})
		chk(t, err)
	}
}

// makeProject 建一個專案,回傳它的 id
func makeProject(t *testing.T, s Store, org string) string {
	t.Helper()
	at := mt("2026-01-01T00:00:00Z")
	x, err := s.CreateProject(org, model.Project{Name: "p", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	return x.ID
}
