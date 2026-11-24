package mcp_test

import (
	"net/http"
	"slices"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// issue_accept(接受、release=true 放回):管理端點與角色端點都有;人類接受者只在 assigneeActor;
// 被別人接受、放回的授權、找不到都回工具錯誤;issues 列表的 assigneeActor 帶出人類接受者
func TestIssueAcceptTool(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	for _, u := range []struct{ id, email, role string }{{"u1", "boss@x", "admin"}, {"u2", "amy@x", "member"}} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		if u.id == "u1" {
			_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
		}
		_ = st.SaveMember(model.Member{Org: "lab", UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
	}
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	amy := &model.Principal{User: model.User{ID: "u2", Email: "amy@x", Role: "member"}}
	if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: "cto", Name: "研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	endpoint := func(p *model.Principal) http.Handler {
		return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: p}, "lab", nil
		}})
	}
	admin, bossEp := endpoint(amy), endpoint(boss)
	iss, err := svc.CreateIssue(boss, "lab", service.IssueInput{Title: "待分析"})
	if err != nil {
		t.Fatal(err)
	}
	k := iss.Key

	fails(t, admin, "", "issue_accept", map[string]any{}, "Missing parameter id")
	fails(t, admin, "", "issue_accept", map[string]any{"id": "ISS-999"}, "Issue ISS-999 not found")
	a := ok(t, admin, "", "issue_accept", map[string]any{"id": k, "return": "full"})
	ac, _ := a["assigneeActor"].(map[string]any)
	if a["status"] != "open" || ac == nil || ac["email"] != "amy@x" || a["assignee"] != nil || a["message"] == nil {
		t.Fatal("接受後負責人是 amy(只在 assigneeActor)", a)
	}
	if again := ok(t, admin, "", "issue_accept", map[string]any{"id": k}); again["version"] != a["version"] {
		t.Fatal("重複接受要冪等", again)
	}
	failsWith(t, bossEp, "", "issue_accept", map[string]any{"id": k}, "Failed: ")
	// issues 列表要帶出人類接受者
	l := ok(t, admin, "", "issues", map[string]any{})
	rows, _ := l["issues"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["assigneeActor"] == nil {
		t.Fatal("列表要帶 assigneeActor", l)
	}
	// 放回:別人(一般成員以外的管理員可以)
	r := ok(t, bossEp, "", "issue_accept", map[string]any{"id": k, "release": true, "return": "full"})
	if r["assigneeActor"] != nil {
		t.Fatal("放回後沒有負責人", r)
	}
	ok(t, admin, "", "issue_accept", map[string]any{"id": k, "release": true}) // 沒人接受:冪等

	// 角色端點:用任務憑證接受,別人接受不了、一般成員放不回
	ta, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "cto", Title: "做事"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "cto", TaskID: ta.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	auth := "Bearer cto:" + res.Credential.Secret
	g := ok(t, role, auth, "issue_accept", map[string]any{"id": k, "return": "full"})
	if g["assignee"] != "cto" {
		t.Fatal("角色接受後 assignee 是 cto", g)
	}
	failsWith(t, admin, "", "issue_accept", map[string]any{"id": k}, "Failed: ")
	failsWith(t, admin, "", "issue_accept", map[string]any{"id": k, "release": true}, "Failed: ")
	ok(t, role, auth, "issue_accept", map[string]any{"id": k, "release": true})
}

// issue_accept 的預設回執:changed 反映真正改到的欄位,assignee / accepted 說明接受狀態;重複接受、重複放回 changed 為空
func TestIssueAcceptAckReceipt(t *testing.T) {
	h, _, _ := ackHandler(t)
	iss := ok(t, h, "", "issue_create", map[string]any{"title": "回執"})
	id := iss["id"]
	changedOf := func(m map[string]any) []string {
		l, _ := m["changed"].([]any)
		out := []string{}
		for _, v := range l {
			out = append(out, v.(string))
		}
		return out
	}
	a1 := ok(t, h, "", "issue_accept", map[string]any{"id": id})
	if !slices.Contains(changedOf(a1), "assignee") || a1["accepted"] != true || a1["assignee"] == "" || a1["assignee"] == nil || a1["status"] != "open" {
		t.Fatalf("first accept %v", a1)
	}
	a2 := ok(t, h, "", "issue_accept", map[string]any{"id": id})
	if len(changedOf(a2)) != 0 || a2["accepted"] != true || a2["assignee"] != a1["assignee"] {
		t.Fatalf("repeat accept %v", a2)
	}
	r1 := ok(t, h, "", "issue_accept", map[string]any{"id": id, "release": true})
	if !slices.Contains(changedOf(r1), "assignee") || r1["accepted"] != false || r1["assignee"] != "" {
		t.Fatalf("release %v", r1)
	}
	r2 := ok(t, h, "", "issue_accept", map[string]any{"id": id, "release": true})
	if len(changedOf(r2)) != 0 || r2["accepted"] != false {
		t.Fatalf("repeat release %v", r2)
	}
	full := ok(t, h, "", "issue_accept", map[string]any{"id": id, "return": "full"})
	if full["accepted"] != nil || full["changed"] != nil || full["comments"] == nil || full["message"] == nil {
		t.Fatalf("full shape %v", full)
	}
}
