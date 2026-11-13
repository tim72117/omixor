package api_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 事件的 API:成員宣告、更新、讀;不是成員 403;匯入要組織管理員、只能一次
func TestIncidentAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveUser("alice@example.com", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("o1", "alice@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.CreateOrg("o2", "組織二"); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	inc, err := alice.DeclareIncident("o1", service.IncidentInput{Title: "付款延遲", Sev: 2, Impact: "延遲", Author: "冒名"})
	if err != nil || inc.ID != "INC-2001" || inc.Timeline[0].Actor != model.PersonActor("alice@example.com") {
		t.Fatalf("%+v %v", inc, err)
	}
	if _, err := alice.DeclareIncident("o2", service.IncidentInput{Title: "x", Sev: 1}); status(err) != 403 {
		t.Fatal("不是成員 403", err)
	}
	if _, err := alice.DeclareIncident("o1", service.IncidentInput{Title: "x", Sev: 0}); status(err) != 400 {
		t.Fatal(err)
	}
	st := "resolved"
	u, err := alice.UpdateIncident("o1", "INC-2001", service.IncidentPatch{Status: &st, AddFollowUps: []string{"ISS-1"}})
	if err != nil || u.Status != "resolved" || u.ResolvedAt == nil || len(u.FollowUps) != 1 {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := alice.GetIncident("o1", "INC-404"); status(err) != 404 {
		t.Fatal(err)
	}
	g, err := alice.GetIncident("o1", "inc-2001")
	if err != nil || g.Status != "resolved" {
		t.Fatal(g, err)
	}
	if l, err := alice.ListIncidents("o1"); err != nil || len(l) != 1 {
		t.Fatal(l, err)
	}
	list := []model.Incident{{ID: "INC-2041", Title: "舊的", Sev: 2, Status: "identified"}}
	if _, err := alice.ImportIncidents("o2", list); status(err) != 403 {
		t.Fatal(err)
	}
	if _, err := boss.ImportIncidents("o1", list); status(err) != 409 {
		t.Fatal("已經有事件 409", err)
	}
	if r, err := boss.ImportIncidents("o2", list); err != nil || r.Inserted != 1 {
		t.Fatal(r, err)
	}
}
