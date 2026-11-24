package mcp

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"omixor-web/internal/core"

	"omixor-core/api"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// 待決定 / 需要協助工具(issue_decision_*)經 appBackend 轉發給核心(遠端模式):提出、查、處理、由它開 Issue 都進核心;
// 署名由登入身分決定(提出的建立者、處理的處理人都是登入使用者的 actor,沒有任何 agent、author 參數)
func TestConsoleDecisionTools(t *testing.T) {
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer evo-tok" {
			w.Write([]byte(`{"user":{"id":"e1","email":"boss@example.com","name":"老闆","google":true}}`))
			return
		}
		w.Write([]byte(`{"user":null}`))
	}))
	defer evo.Close()
	st := storetest.New(t)
	svc := &service.Service{Store: st, Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	srv := httptest.NewServer(api.New(svc).Handler())
	defer srv.Close()
	t.Setenv("OMIXOR_SERVER", srv.URL)
	t.Setenv("OPS_ISSUE_SERVER", "")
	t.Setenv("OMIXOR_SERVER_TOKEN", "")
	t.Setenv("OPS_ISSUE_SERVER_TOKEN", "")
	core.Vault = core.NewMemoryVault()
	a := iaApp(t)
	core.ConnSetBearer(a, "evoleap", "evo-tok")
	_ = st.CreateOrg(modelOrg("default"))
	if err := a.SetMode(core.ModeRemote, srv.URL); err != nil {
		t.Fatal(err)
	}

	d := iaRun(t, a, core.MCPActor, "issue_decision_add", map[string]any{"title": "用哪個?", "options": []any{map[string]any{"label": "A", "recommended": true}, "B"}})
	if d["id"] != "DEC-1" || d["kind"] != "decision" || d["status"] != "open" {
		t.Fatal(d)
	}
	h := iaRun(t, a, core.MCPActor, "issue_decision_add", map[string]any{"title": "請備份", "kind": "help"})
	if h["id"] != "DEC-2" || h["kind"] != "help" {
		t.Fatal(h)
	}
	got, err := st.GetDecision("default", "DEC-1")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := st.UserByActorID(got.CreatedBy); err != nil || u == nil || u.Email != "boss@example.com" {
		t.Fatalf("建立者要是登入使用者的 actor:%+v %v", got, err)
	}
	if l := iaRun(t, a, core.MCPActor, "issue_decisions", map[string]any{}); l["total"].(float64) != 2 {
		t.Fatal(l)
	}
	ans := iaRun(t, a, core.MCPActor, "issue_decision_answer", map[string]any{"id": "DEC-1", "answer": "o2", "note": "穩"})
	if ans["status"] != "decided" || ans["answer"] != "o2" {
		t.Fatal(ans)
	}
	got, _ = st.GetDecision("default", "DEC-1")
	if u, err := st.UserByActorID(got.DecidedBy); err != nil || u == nil || u.Email != "boss@example.com" {
		t.Fatalf("處理人要是登入使用者的 actor:%+v %v", got, err)
	}
	r := iaRun(t, a, core.MCPActor, "issue_decision_to_issue", map[string]any{"id": "DEC-2", "priority": "high"})
	if is, _ := r["issue"].(map[string]any); is == nil || is["source"] != "DEC-2" || is["priority"] != "high" {
		t.Fatal(r)
	}
	if l := iaRun(t, a, core.MCPActor, "issues", map[string]any{"status": "all"}); l["total"].(float64) != 1 {
		t.Fatal("由決定開出的 Issue 要在核心:", l)
	}
}

// 撤回、重新開啟(console 自己的工具,轉發給核心):成功、權限(403)、狀態不對(409)、找不到,而且 account view 的 features 宣告了這兩個名稱
func TestConsoleDecisionWithdrawReopen(t *testing.T) {
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer evo-tok":
			w.Write([]byte(`{"user":{"id":"e1","email":"boss@example.com","name":"老闆","google":true}}`))
		case "Bearer evo-bob":
			w.Write([]byte(`{"user":{"id":"e2","email":"bob@example.com","name":"阿波","google":true}}`))
		default:
			w.Write([]byte(`{"user":null}`))
		}
	}))
	defer evo.Close()
	st := storetest.New(t)
	svc := &service.Service{Store: st, Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	srv := httptest.NewServer(api.New(svc).Handler())
	defer srv.Close()
	t.Setenv("OMIXOR_SERVER", srv.URL)
	t.Setenv("OPS_ISSUE_SERVER", "")
	t.Setenv("OMIXOR_SERVER_TOKEN", "")
	t.Setenv("OPS_ISSUE_SERVER_TOKEN", "")
	core.Vault = core.NewMemoryVault()
	a := iaApp(t)
	core.ConnSetBearer(a, "evoleap", "evo-tok")
	_ = st.CreateOrg(modelOrg("default"))
	if err := a.SetMode(core.ModeRemote, srv.URL); err != nil {
		t.Fatal(err)
	}

	// 換登入身分:換新鑰匙圈(清掉已換到的 omixor token)、設 evoleap bearer、重新連線
	asUser := func(tok string) {
		t.Helper()
		core.Vault = core.NewMemoryVault()
		if err := a.SetMode(core.ModeLocal, ""); err != nil { // evoleap 的 bearer 存在目前組織的範圍:先回本機(預設組織)再設
			t.Fatal(err)
		}
		core.ConnSetBearer(a, "evoleap", tok)
		if err := a.SetMode(core.ModeRemote, srv.URL); err != nil {
			t.Fatal(err)
		}
	}
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

	// 管理員(boss)提出 DEC-1、DEC-2;撤回 DEC-1;已撤回的不能再撤回(409)
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

	// 一般成員(bob)不是建立者也不是管理員:撤回別人的 403;管理員處理過的,bob 重新開啟 403
	bu := model.User{ID: "u-bob", Email: "bob@example.com", EvoleapID: "e2", Name: "阿波", Role: "member", CreatedAt: model.Now()}
	_ = st.SaveUser(bu)
	_ = st.SaveMember(model.Member{Org: "default", UserID: bu.ID, Email: bu.Email, Role: "member", AddedAt: model.Now()})
	asUser("evo-bob")
	if _, err := Run(a, "issue_decision_withdraw", map[string]any{"id": "DEC-2"}, core.MCPActor); err == nil || ToolErr(err).(ToolError).Status != 403 {
		t.Fatalf("非建立者非管理員撤回要 403:%v", err)
	}
	asUser("evo-tok")
	iaRun(t, a, core.MCPActor, "issue_decision_answer", map[string]any{"id": "DEC-2", "answer": "o2"})
	asUser("evo-bob")
	if _, err := Run(a, "issue_decision_reopen", map[string]any{"id": "DEC-2"}, core.MCPActor); err == nil || ToolErr(err).(ToolError).Status != 403 {
		t.Fatalf("非原處理人非管理員重新開啟要 403:%v", err)
	}
}
