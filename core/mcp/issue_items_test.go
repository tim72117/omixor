package mcp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// Issue 子項工具:管理端點新增、查(預設只回未完成)、改、領取(負責人是角色時回憑證)、回報、卡住、提升;
// 角色端點只有 add、items、report(只能回報自己綁定任務的子項);受限專案非成員看不到
func TestIssueItemTools(t *testing.T) {
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
	if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: "cto", Name: "研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	endpoint := func(p *model.Principal) http.Handler {
		return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: p}, "lab", nil
		}})
	}
	admin, bossEp := endpoint(amy), endpoint(boss)
	iss, err := svc.CreateIssue(amy, "lab", service.IssueInput{Title: "大構想"})
	if err != nil {
		t.Fatal(err)
	}
	k := iss.Key

	fails(t, admin, "", "issue_item_add", map[string]any{"issue": k, "title": "x", "depends_on": "9"}, "找不到同一張 Issue 的子項 9")
	a := ok(t, admin, "", "issue_item_add", map[string]any{"issue": k, "title": "第一階段", "files": []any{"a.go", "b.go"}, "acceptance": "測試過"})
	if a["key"] != k+".1" || a["status"] != "todo" || a["createdBy"] != "amy@x" || len(a["files"].([]any)) != 2 {
		t.Fatal(a)
	}
	if ac, _ := a["createdByActor"].(map[string]any); ac == nil || ac["email"] != "amy@x" {
		t.Fatal("要有 createdByActor 物件", a)
	}
	b := ok(t, admin, "", "issue_item_add", map[string]any{"issue": k, "title": "第二階段", "depends_on": "1", "assignee": "agent:cto"})
	if b["key"] != k+".2" || b["dependsOn"] != k+".1" || b["assignee"] != "agent:cto" {
		t.Fatal(b)
	}
	fails(t, admin, "", "issue_item_claim", map[string]any{"id": k + ".2"}, "Claim failed: "+k+".2 的前置 "+k+".1 還沒完成(待領取)")
	fails(t, admin, "", "issue_item_claim", map[string]any{"id": "ISS-1"}, "id must be an item number such as ISS-37.2 (issue key plus sequence number)")

	// 領取與回報
	c := ok(t, admin, "", "issue_item_claim", map[string]any{"id": k + ".1", "note": "session A"})
	if it := c["item"].(map[string]any); it["status"] != "doing" || it["claimedBy"] != "amy@x" || c["credential"] != nil {
		t.Fatal("輕量領取不發憑證", c)
	}
	d := ok(t, admin, "", "issue_item_report", map[string]any{"id": k + ".1", "status": "done", "evidence": map[string]any{"commit": "abc"}, "note": "好了"})
	if d["status"] != "done" || !strings.Contains(string(mustJSON(d["evidence"])), `"commit":"abc"`) || !strings.Contains(string(mustJSON(d["evidence"])), "好了") {
		t.Fatal(d)
	}
	// 完成證據查核:這裡沒有任何回報過的 commit,回應要帶查核結果與英文提醒;讀取只帶查核結果
	if c, _ := d["evidenceCheck"].(map[string]any); c["commit"] != "invalid_sha" || c["needsVerification"] != true {
		t.Fatal("查核結果", d)
	}
	if m, _ := d["message"].(string); !strings.Contains(m, "flagged needsVerification") {
		t.Fatal("完成回報要提醒", d)
	}
	if g := ok(t, admin, "", "issue_items", map[string]any{"id": k + ".1"}); g["evidenceCheck"] == nil || g["message"] != nil {
		t.Fatal(g)
	}
	fails(t, admin, "", "issue_item_report", map[string]any{"id": k + ".1", "status": "blocked"}, k+".1 已經完成")
	if l := ok(t, admin, "", "issue_items", map[string]any{"issue": k}); l["total"] != 1.0 || l["open"] != 1.0 {
		t.Fatal("預設只回未完成", l)
	}
	if l := ok(t, admin, "", "issue_items", map[string]any{"issue": k, "include_done": true}); l["total"] != 2.0 || l["open"] != 1.0 {
		t.Fatal(l)
	}
	if g := ok(t, admin, "", "issue_items", map[string]any{"id": k + ".1"}); g["key"] != k+".1" {
		t.Fatal(g)
	}
	fails(t, admin, "", "issue_items", map[string]any{"id": k + ".9"}, k+".9 not found")
	fails(t, admin, "", "issue_items", map[string]any{}, "Missing parameter issue")

	// 負責人是角色:領取回傳憑證,角色端點用它回報
	cl := ok(t, admin, "", "issue_item_claim", map[string]any{"id": k + ".2"})
	cred, _ := cl["credential"].(map[string]any)
	if cred == nil || cl["task"] == nil || cred["agentId"] != "cto" || cred["secret"] == "" {
		t.Fatal("角色領取要回憑證與任務", cl)
	}
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	auth := "Bearer cto:" + cred["secret"].(string)
	e := ok(t, role, auth, "issue_item_add", map[string]any{"issue": k, "title": "下一步", "assignee": "cto"})
	if e["key"] != k+".3" || e["createdBy"] != "agent:cto" {
		t.Fatal("角色端點署名是憑證的角色", e)
	}
	fails(t, role, auth, "issue_item_report", map[string]any{"id": k + ".3", "status": "done"}, "這組角色憑證只能回報自己綁定任務的子項")
	if l := ok(t, role, auth, "issue_items", map[string]any{"issue": k}); l["total"] != 2.0 {
		t.Fatal("角色端點可以讀", l)
	}
	for _, n := range listTools(t, role, auth) {
		if n == "issue_item_update" || n == "issue_item_claim" || n == "issue_item_promote" {
			t.Fatal("角色端點的 tools/list 不能有", n)
		}
	}
	bl := ok(t, role, auth, "issue_item_report", map[string]any{"id": k + ".2", "status": "blocked", "block_reason": "要人選", "wait_for_kind": "user_decision", "wait_for": "A 或 B"})
	if bl["status"] != "blocked" || bl["waitForKind"] != "user_decision" || bl["waitFor"] != "A 或 B" {
		t.Fatal(bl)
	}
	if !mcp.AgentToolAllow["issue_item_add"] || !mcp.AgentToolAllow["issue_items"] || !mcp.AgentToolAllow["issue_item_report"] ||
		mcp.AgentToolAllow["issue_item_update"] || mcp.AgentToolAllow["issue_item_claim"] || mcp.AgentToolAllow["issue_item_promote"] ||
		!mcp.AdminToolAllow["issue_item_update"] || !mcp.AdminToolAllow["issue_item_claim"] || !mcp.AdminToolAllow["issue_item_promote"] {
		t.Fatal("登記表的 Admin / Agent 旗標不對")
	}

	// 改、取消、提升(只有管理端點)
	u := ok(t, bossEp, "", "issue_item_update", map[string]any{"id": k + ".3", "title": "改名", "files": []any{"x.go"}, "acceptance": "新"})
	if u["title"] != "改名" || len(u["files"].([]any)) != 1 {
		t.Fatal(u)
	}
	if cnc := ok(t, bossEp, "", "issue_item_update", map[string]any{"id": k + ".3", "cancel": true}); cnc["status"] != "canceled" {
		t.Fatal(cnc)
	}
	f := ok(t, bossEp, "", "issue_item_add", map[string]any{"issue": k, "title": "太大"})
	pr := ok(t, bossEp, "", "issue_item_promote", map[string]any{"id": f["key"], "priority": "high"})
	if pi, _ := pr["issue"].(map[string]any); pi == nil || pi["title"] != "太大" || pi["priority"] != "high" || pi["source"] != f["key"] {
		t.Fatal(pr)
	}
	if it := pr["item"].(map[string]any); it["status"] != "canceled" || it["promotedIssueId"] == "" {
		t.Fatal(pr)
	}

	// 受限專案:非成員(amy 不是 boss 專案的成員)看不到
	prj, err := svc.CreateProject(boss, "lab", service.ProjectInput{Name: "機密", Key: "SEC", Restricted: true})
	if err != nil {
		t.Fatal(err)
	}
	sec, err := svc.CreateIssue(boss, "lab", service.IssueInput{Title: "機密 Issue", Project: &prj.ID})
	if err != nil {
		t.Fatal(err)
	}
	ok(t, bossEp, "", "issue_item_add", map[string]any{"issue": sec.Key, "title": "機密子項"})
	fails(t, admin, "", "issue_items", map[string]any{"issue": sec.Key}, sec.Key+" not found")
	fails(t, admin, "", "issue_items", map[string]any{"id": sec.Key + ".1"}, sec.Key+".1 not found")
	fails(t, admin, "", "issue_item_add", map[string]any{"issue": sec.Key, "title": "偷加"}, sec.Key+" not found")
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
