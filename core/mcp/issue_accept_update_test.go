package mcp_test

import (
	"net/http"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// 人類接受者:issue_update 的 assignee="" 走放回的權限
func TestIssueUpdateClearHumanAssignee(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	for _, u := range []struct{ id, email, role string }{{"u1", "boss@x", "admin"}, {"u2", "amy@x", "member"}, {"u3", "bob@x", "member"}} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		if u.id == "u1" {
			_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
		}
		_ = st.SaveMember(model.Member{Org: "lab", UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
	}
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	amy := &model.Principal{User: model.User{ID: "u2", Email: "amy@x", Role: "member"}}
	bob := &model.Principal{User: model.User{ID: "u3", Email: "bob@x", Role: "member"}}
	if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: "cto", Name: "研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	endpoint := func(p *model.Principal) http.Handler {
		return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: p}, "lab", nil
		}})
	}
	amyEp, bobEp, bossEp := endpoint(amy), endpoint(bob), endpoint(boss)
	human, err := svc.CreateIssue(boss, "lab", service.IssueInput{Title: "給人"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateIssue(boss, "lab", service.IssueInput{Title: "給角色", Assignee: "cto"}); err != nil {
		t.Fatal(err)
	}
	ok(t, amyEp, "", "issue_accept", map[string]any{"id": human.Key})

	assigned := func() bool { // issues 工具沒有 assignee 參數(只有 REST 與 console 的篩選有);用單筆回傳看負責人
		g := ok(t, bossEp, "", "issues", map[string]any{"id": human.Key})
		return g["assigneeActor"] != nil
	}
	if !assigned() {
		t.Fatal("接受後負責人在 assigneeActor")
	}

	// issue_update assignee="":別人 403(指向 issue_accept release=true)、接受者本人放回、沒人接受冪等
	failsWith(t, bobEp, "", "issue_update", map[string]any{"id": human.Key, "assignee": ""}, "release=true")
	if !assigned() {
		t.Fatal("被擋之後負責人還在")
	}
	r := ok(t, amyEp, "", "issue_update", map[string]any{"id": human.Key, "assignee": "", "return": "full"})
	if r["assigneeActor"] != nil {
		t.Fatal("本人用 assignee=\"\" 取消後沒有負責人", r)
	}
	ok(t, bobEp, "", "issue_update", map[string]any{"id": human.Key, "assignee": ""})
	if assigned() {
		t.Fatal("取消後回到沒指派")
	}
}
