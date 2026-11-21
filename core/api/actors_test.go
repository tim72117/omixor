package api_test

import (
	"testing"

	"omixor-core/client"
	"omixor-core/model"
)

// GET /api/orgs/{org}/actors:範圍、篩選、權限
func TestListActorsHTTP(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.CreateOrg("other", "別組"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("lab", "alice@example.com", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("other", "carl@example.com", "member"); err != nil { // 只在 other;還沒登入過
		t.Fatal(err)
	}
	if _, err := boss.SaveAgent("lab", model.Agent{ID: "ceo", Name: "執行長", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveAgent("lab", model.Agent{ID: "cfo", Name: "財務長", Active: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveAgent("other", model.Agent{ID: "spy", Name: "別組角色", Active: true}); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")

	l, err := alice.ListActors("lab", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range l {
		got[string(it.Kind)+":"+it.Handle] = it.Status
		if it.ID == "" || it.Name == "" && it.Kind != model.ActorKindSystem {
			t.Errorf("缺欄位 %+v", it)
		}
	}
	if got["user:alice@example.com"] != "active" || got["agent:ceo"] != "active" || got["agent:cfo"] != "suspended" || got["system:omixor"] != "active" {
		t.Fatalf("%v", got)
	}
	for _, k := range []string{"agent:spy", "user:carl@example.com"} {
		if _, ok := got[k]; ok {
			t.Errorf("不該看到 %s", k)
		}
	}
	if l, err := alice.ListActors("lab", "agent", "c", "suspended"); err != nil || len(l) != 1 || l[0].Handle != "cfo" {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := alice.ListActors("lab", "bogus", "", ""); status(err) != 400 {
		t.Fatal(err)
	}
	// 非成員 403;伺服器管理員可看任何組織
	if _, err := alice.ListActors("other", "", "", ""); status(err) != 403 {
		t.Fatal("非成員要 403", err)
	}
	if l, err := boss.ListActors("other", "agent", "", ""); err != nil || len(l) != 1 || l[0].Handle != "spy" {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := client.New(e.srv.URL).ListActors("lab", "", "", ""); status(err) != 401 {
		t.Fatal("沒登入要 401", err)
	}
}
