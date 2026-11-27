package mcp

import (
	"slices"
	"testing"

	"omixor-web/internal/core"
)

// 待決定 / 需要協助工具(issue_decision_*)經 appBackend 轉發給核心:提出、查、處理、由它開 Issue 都進核心;
// 署名由呼叫者身分決定(提出的建立者、處理的處理人都是本機使用者的 actor,沒有任何 agent、author 參數)
func TestConsoleDecisionTools(t *testing.T) {
	a := iaApp(t)
	st, org := iaStore(t, a)

	d := iaRun(t, a, core.MCPActor, "issue_decision_add", map[string]any{"title": "用哪個?", "options": []any{map[string]any{"label": "A", "recommended": true}, "B"}})
	if d["id"] != "DEC-1" || d["kind"] != "decision" || d["status"] != "open" {
		t.Fatal(d)
	}
	h := iaRun(t, a, core.MCPActor, "issue_decision_add", map[string]any{"title": "請備份", "kind": "help"})
	if h["id"] != "DEC-2" || h["kind"] != "help" {
		t.Fatal(h)
	}
	got, err := st.GetDecision(org, "DEC-1")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := st.UserByActorID(got.CreatedBy); err != nil || u == nil || u.Email != core.LocalIdentity {
		t.Fatalf("建立者要是本機使用者的 actor:%+v %v", got, err)
	}
	if l := iaRun(t, a, core.MCPActor, "issue_decisions", map[string]any{}); l["total"].(float64) != 2 {
		t.Fatal(l)
	}
	ans := iaRun(t, a, core.MCPActor, "issue_decision_answer", map[string]any{"id": "DEC-1", "answer": "o2", "note": "穩"})
	if ans["status"] != "decided" || ans["answer"] != "o2" {
		t.Fatal(ans)
	}
	got, _ = st.GetDecision(org, "DEC-1")
	if u, err := st.UserByActorID(got.DecidedBy); err != nil || u == nil || u.Email != core.LocalIdentity {
		t.Fatalf("處理人要是本機使用者的 actor:%+v %v", got, err)
	}
	r := iaRun(t, a, core.MCPActor, "issue_decision_to_issue", map[string]any{"id": "DEC-2", "priority": "high"})
	if is, _ := r["issue"].(map[string]any); is == nil || is["source"] != "DEC-2" || is["priority"] != "high" {
		t.Fatal(r)
	}
	if l := iaRun(t, a, core.MCPActor, "issues", map[string]any{"status": "all"}); l["total"].(float64) != 1 {
		t.Fatal("由決定開出的 Issue 要在核心:", l)
	}
}

// 撤回、重新開啟(console 自己的工具,轉發給核心):成功、狀態不對(409)、找不到,而且 account view 的 features 宣告了這兩個名稱
func TestConsoleDecisionWithdrawReopen(t *testing.T) {
	a := iaApp(t)
	for _, n := range []string{"issue_decision_withdraw", "issue_decision_reopen"} {
		if !HasTool(n) {
			t.Fatal("要登記:", n)
		}
	}
	if f := DecisionFeatures(); len(f) != 2 || f[0] != "issue_decision_withdraw" || f[1] != "issue_decision_reopen" {
		t.Fatal("features 要宣告這兩個工具:", f)
	}
	acc, _ := core.View(a, "account")
	feats, _ := acc.(map[string]any)["features"].([]any)
	if !slices.Contains(feats, any("issue_decision_withdraw")) || !slices.Contains(feats, any("issue_decision_reopen")) {
		t.Fatalf("account view 的 features 要含兩個名稱:%+v", acc)
	}

	// 提出 DEC-1、DEC-2;撤回 DEC-1;已撤回的不能再撤回(409)
	iaRun(t, a, core.MCPActor, "issue_decision_add", map[string]any{"title": "d1", "options": []any{"A", "B"}})
	iaRun(t, a, core.MCPActor, "issue_decision_add", map[string]any{"title": "d2", "options": []any{"A", "B"}})
	w := iaRun(t, a, core.MCPActor, "issue_decision_withdraw", map[string]any{"id": "DEC-1"})
	if w["status"] != "withdrawn" || w["id"] != "DEC-1" {
		t.Fatal(w)
	}
	if _, err := Run(a, "issue_decision_withdraw", map[string]any{"id": "DEC-1"}, core.MCPActor); err == nil || ToolErr(err).(ToolError).Status != 409 {
		t.Fatalf("已撤回不能再撤回要 409:%v", err)
	}
	// 還沒處理的不能重新開啟(409);處理後可以重新開啟,答案清空
	if _, err := Run(a, "issue_decision_reopen", map[string]any{"id": "DEC-2"}, core.MCPActor); err == nil || ToolErr(err).(ToolError).Status != 409 {
		t.Fatalf("open 的不能重新開啟要 409:%v", err)
	}
	iaRun(t, a, core.MCPActor, "issue_decision_answer", map[string]any{"id": "DEC-2", "answer": "o1"})
	r := iaRun(t, a, core.MCPActor, "issue_decision_reopen", map[string]any{"id": "DEC-2"})
	if r["status"] != "open" || r["answer"] != nil && r["answer"] != "" {
		t.Fatal(r)
	}
	if _, err := Run(a, "issue_decision_reopen", map[string]any{"id": "DEC-99"}, core.MCPActor); err == nil {
		t.Fatal("沒有這筆要失敗")
	}
	if _, err := Run(a, "issue_decision_withdraw", map[string]any{}, core.MCPActor); err == nil {
		t.Fatal("沒給 id 要失敗")
	}
}
