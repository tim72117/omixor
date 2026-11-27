package mcp

import (
	"testing"

	"omixor-web/internal/core"
)

// Issue 子項工具(issue_item_*)經 appBackend 轉發給核心:新增、查、領取、回報、提升都進核心;
// 署名由呼叫者身分決定(建立者、領取者都是本機使用者的 actor,沒有任何 agent、author 參數)
func TestConsoleIssueItemTools(t *testing.T) {
	a := iaApp(t)
	st, org := iaStore(t, a)

	is := iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "大構想"})
	k, _ := is["key"].(string)
	if k == "" {
		t.Fatal(is)
	}
	x := iaRun(t, a, core.MCPActor, "issue_item_add", map[string]any{"issue": k, "title": "第一階段", "files": []any{"a.go"}, "acceptance": "測試過"})
	if x["key"] != k+".1" || x["status"] != "todo" {
		t.Fatal(x)
	}
	y := iaRun(t, a, core.MCPActor, "issue_item_add", map[string]any{"issue": k, "title": "第二階段", "depends_on": "1"})
	if y["dependsOn"] != k+".1" {
		t.Fatal(y)
	}
	iaErr(t, a, "issue_item_claim", map[string]any{"id": k + ".2"}, "Claim failed: "+k+".2 的前置 "+k+".1 還沒完成(待領取)")
	c := iaRun(t, a, core.MCPActor, "issue_item_claim", map[string]any{"id": k + ".1"})
	if it, _ := c["item"].(map[string]any); it == nil || it["status"] != "doing" {
		t.Fatal(c)
	}
	got, err := st.ListIssueItems(org, is["id"].(string))
	if err != nil || len(got) != 2 {
		t.Fatalf("子項要在核心:%v %v", got, err)
	}
	if u, err := st.UserByActorID(got[0].ClaimedBy); err != nil || u == nil || u.Email != core.LocalIdentity {
		t.Fatalf("領取者要是本機使用者的 actor:%+v %v", got[0], err)
	}
	d := iaRun(t, a, core.MCPActor, "issue_item_report", map[string]any{"id": k + ".1", "status": "done", "evidence": "測試通過"})
	if d["status"] != "done" {
		t.Fatal(d)
	}
	if l := iaRun(t, a, core.MCPActor, "issue_items", map[string]any{"issue": k}); l["total"].(float64) != 1 {
		t.Fatal("預設只回未完成", l)
	}
	r := iaRun(t, a, core.MCPActor, "issue_item_promote", map[string]any{"id": k + ".2", "priority": "high"})
	if pi, _ := r["issue"].(map[string]any); pi == nil || pi["priority"] != "high" || pi["source"] != k+".2" {
		t.Fatal(r)
	}
}
