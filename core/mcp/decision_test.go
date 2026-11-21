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

// 待決定 / 需要協助工具:管理端點提出(兩種 kind)、查、處理、由它開 Issue;角色端點只有 add 與讀,沒有 answer / to_issue;
// 署名是呼叫者本人(沒有 agent、author 參數);受限專案來源 Issue 的決定,非成員看不到
func TestDecisionTools(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.SaveUser(model.User{ID: "u2", Email: "amy@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u2", Email: "amy@x", Role: "member", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	amy := &model.Principal{User: model.User{ID: "u2", Email: "amy@x", Role: "member"}}
	g := model.Agent{ID: "cto", Name: "研發長", Active: true}
	if _, err := svc.SaveAgent(boss, "lab", g); err != nil {
		t.Fatal(err)
	}
	endpoint := func(p *model.Principal) http.Handler {
		return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: p}, "lab", nil
		}})
	}
	admin, bossEp := endpoint(amy), endpoint(boss)
	iss, _ := svc.CreateIssue(amy, "lab", service.IssueInput{Title: "來源"})

	fails(t, admin, "", "issue_decision_add", map[string]any{"title": "x", "options": []any{"只有一個"}}, "decision 至少要 2 個選項(只是要人做事請用 kind=help)")
	fails(t, admin, "", "issue_decision_add", map[string]any{"title": "x", "kind": "help", "options": []any{"a", "b"}}, "kind=help 不能有 options(要人選擇請用 kind=decision)")
	d := ok(t, admin, "", "issue_decision_add", map[string]any{"title": "用哪個?", "body": "背景", "source_issue": iss.Key,
		"options": []any{map[string]any{"label": "A", "recommended": true}, "B"}})
	opts := d["options"].([]any)
	if d["id"] != "DEC-1" || d["kind"] != "decision" || d["status"] != "open" || d["sourceIssue"] != iss.Key || len(opts) != 2 ||
		opts[0].(map[string]any)["recommended"] != true || opts[1].(map[string]any)["id"] != "o2" || d["createdBy"] != "amy@x" {
		t.Fatal(d)
	}
	if a, _ := d["createdByActor"].(map[string]any); a == nil || a["email"] != "amy@x" || a["kind"] != "user" {
		t.Fatal("要有 createdByActor 物件", d)
	}
	h := ok(t, admin, "", "issue_decision_add", map[string]any{"title": "請備份", "kind": "help"})
	if h["id"] != "DEC-2" || h["kind"] != "help" || len(h["options"].([]any)) != 0 {
		t.Fatal(h)
	}

	// 角色端點
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "cto", Title: "做事"})
	res, err := svc.ClaimTask(amy, "lab", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	auth := "Bearer cto:" + res.Credential.Secret
	rh := ok(t, role, auth, "issue_decision_add", map[string]any{"title": "請提供憑證", "kind": "help", "blocks": []any{iss.Key}})
	if rh["id"] != "DEC-3" || rh["createdBy"] != "研發長" {
		t.Fatal(rh)
	}
	if a, _ := rh["createdByActor"].(map[string]any); a == nil || a["kind"] != "agent" || a["handle"] != "cto" {
		t.Fatal("角色端點署名是憑證的角色", rh)
	}
	if l := ok(t, role, auth, "issue_decisions", map[string]any{"kind": "help"}); l["total"] != 2.0 {
		t.Fatal("角色端點可以讀(兩筆 help)", l)
	}
	for _, n := range listTools(t, role, auth) {
		if n == "issue_decision_answer" || n == "issue_decision_to_issue" {
			t.Fatal("角色端點的 tools/list 不能有", n)
		}
	}
	if !mcp.AgentToolAllow["issue_decision_add"] || !mcp.AgentToolAllow["issue_decisions"] ||
		mcp.AgentToolAllow["issue_decision_answer"] || mcp.AgentToolAllow["issue_decision_to_issue"] ||
		!mcp.AdminToolAllow["issue_decision_answer"] || !mcp.AdminToolAllow["issue_decision_to_issue"] ||
		!mcp.AdminToolAllow["issue_decision_add"] || !mcp.AdminToolAllow["issue_decisions"] {
		t.Fatal("登記表的 Admin / Agent 旗標不對")
	}
	// 查:預設只列開放中的(兩種都含)、依 kind、依來源 Issue、待我處理;id 不分大小寫
	if l := ok(t, admin, "", "issue_decisions", map[string]any{}); l["total"] != 3.0 {
		t.Fatal(l)
	}
	if l := ok(t, admin, "", "issue_decisions", map[string]any{"kind": "decision"}); l["total"] != 1.0 {
		t.Fatal(l)
	}
	if l := ok(t, admin, "", "issue_decisions", map[string]any{"issue": iss.Key}); l["total"] != 1.0 {
		t.Fatal(l)
	}
	if l := ok(t, admin, "", "issue_decisions", map[string]any{"mine": true}); l["total"] != 3.0 {
		t.Fatal(l)
	}
	fails(t, admin, "", "issue_decisions", map[string]any{"id": "DEC-99"}, "找不到 DEC-99")

	// 處理
	failsWith(t, admin, "", "issue_decision_answer", map[string]any{"id": "DEC-1", "answer": "zzz"}, "answer 必須是選項 id")
	a := ok(t, admin, "", "issue_decision_answer", map[string]any{"id": "dec-1", "answer": "o1", "note": "就它"})
	if a["status"] != "decided" || a["answer"] != "o1" || a["decidedBy"] != "amy@x" {
		t.Fatal(a)
	}
	if da, _ := a["decidedByActor"].(map[string]any); da == nil || da["email"] != "amy@x" {
		t.Fatal("要有 decidedByActor", a)
	}
	failsWith(t, admin, "", "issue_decision_answer", map[string]any{"id": "DEC-1", "answer": "o2"}, "已經處理過了")
	hd := ok(t, admin, "", "issue_decision_answer", map[string]any{"id": "DEC-2", "answer": "done", "evidence": "ok"})
	if hd["status"] != "decided" || hd["evidence"] != "ok" {
		t.Fatal(hd)
	}
	if l := ok(t, admin, "", "issue_decisions", map[string]any{"status": "all"}); l["total"] != 3.0 {
		t.Fatal(l)
	}
	if l := ok(t, admin, "", "issue_decisions", map[string]any{}); l["total"] != 1.0 {
		t.Fatal("處理完剩一筆開放", l)
	}
	// 由決定開 Issue
	ti := ok(t, admin, "", "issue_decision_to_issue", map[string]any{"id": "DEC-3", "priority": "high"})
	newIssue := ti["issue"].(map[string]any)
	if newIssue["source"] != "DEC-3" || newIssue["priority"] != "high" || ti["decision"].(map[string]any)["sourceIssue"] != newIssue["key"] {
		t.Fatal(ti)
	}
	failsWith(t, admin, "", "issue_decision_to_issue", map[string]any{"id": "DEC-3"}, "已經有來源 Issue")

	// 受限專案:來源 Issue 在受限專案的決定,非成員看不到
	prj, err := svc.CreateProject(boss, "lab", service.ProjectInput{Name: "機密", Key: "SEC", Restricted: true})
	if err != nil {
		t.Fatal(err)
	}
	sec, err := svc.CreateIssue(boss, "lab", service.IssueInput{Title: "機密 Issue", Project: &prj.ID})
	if err != nil {
		t.Fatal(err)
	}
	sd := ok(t, bossEp, "", "issue_decision_add", map[string]any{"title": "機密待決定", "source_issue": sec.Key, "options": []any{"a", "b"}})
	if sd["sourceIssue"] != sec.Key {
		t.Fatal(sd)
	}
	fails(t, admin, "", "issue_decisions", map[string]any{"id": sd["id"].(string)}, "找不到 "+sd["id"].(string))
	if l := ok(t, admin, "", "issue_decisions", map[string]any{"status": "all"}); l["total"] != 3.0 {
		t.Fatal("列表不含受限專案的決定", l)
	}
	if l := ok(t, bossEp, "", "issue_decisions", map[string]any{"status": "all"}); l["total"] != 4.0 {
		t.Fatal("管理員看得到", l)
	}
	failsWith(t, admin, "", "issue_decision_answer", map[string]any{"id": sd["id"].(string), "answer": "o1"}, "找不到 "+sd["id"].(string))
	failsWith(t, admin, "", "issue_decision_add", map[string]any{"title": "x", "source_issue": sec.Key, "options": []any{"a", "b"}}, "找不到來源 Issue")
	if l := ok(t, role, auth, "issue_decisions", map[string]any{"status": "all"}); l["total"] != 3.0 {
		t.Fatal("非專案成員的角色端點也看不到", l)
	}
}
