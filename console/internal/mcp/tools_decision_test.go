package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"omixor-web/internal/core"

	"omixor-core/api"
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
