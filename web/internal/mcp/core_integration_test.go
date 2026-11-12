package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"omixor-web/internal/core"

	"omixor-core/api"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// omixor-web 遠端模式接上真的核心(omixor-core 的 API + 記憶體存放):evoleap 換 token、MCP 工具讀寫都進核心
func TestOpsWebWithCore(t *testing.T) {
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer evo-tok" {
			w.Write([]byte(`{"user":{"id":"e1","email":"boss@example.com","name":"老闆","google":true}}`))
			return
		}
		w.Write([]byte(`{"user":null}`))
	}))
	defer evo.Close()
	st := store.NewMem()
	svc := &service.Service{Store: st, Evoleap: evo.URL, Bootstrap: "boss@example.com", TTL: time.Hour}
	srv := httptest.NewServer(api.New(svc).Handler())
	defer srv.Close()
	t.Setenv("OMIXOR_SERVER", srv.URL)
	t.Setenv("OPS_ISSUE_SERVER", "")
	t.Setenv("OPS_ISSUE_SERVER_TOKEN", "")
	core.Vault = core.NewMemoryVault()
	a := iaApp(t)
	core.ConnSetBearer(a, "evoleap", "evo-tok")
	// 組織要先在核心建好(伺服器管理員);第一次登入的就是 bootstrap 管理員
	_ = st.CreateOrg(modelOrg("default"))
	if err := a.SetMode(core.ModeRemote, srv.URL); err != nil {
		t.Fatal(err)
	}

	d := iaRun(t, a, core.MCPActor, "ops_issue_create", map[string]any{"title": "接上核心", "labels": []any{"x"}})
	if d["id"] != "ISS-1" {
		t.Fatal(d)
	}
	d = iaRun(t, a, core.MCPActor, "ops_issue_update", map[string]any{"id": "ISS-1", "status": "closed", "comment": "好了"})
	if d["status"] != "closed" || len(d["comments"].([]any)) != 1 {
		t.Fatal(d)
	}
	got, _ := st.GetIssue("default", "ISS-1")
	if got == nil || got.Status != "closed" || got.ClosedAt == nil || len(got.Comments) != 1 || got.CreatedBy != "mcp" {
		t.Fatalf("要寫進核心:%+v", got)
	}
	if l := iaRun(t, a, core.MCPActor, "ops_issues", map[string]any{"status": "all"}); l["total"].(float64) != 1 {
		t.Fatal(l)
	}
	// 組織不存在:核心回 404,MCP 要回錯誤
	a.Mu.Lock()
	cur := a.CurrentOrgID()
	a.Mu.Unlock()
	if cur != "~default" {
		t.Fatal(cur)
	}
}

func modelOrg(id string) model.Org { return model.Org{ID: id, Name: id, CreatedAt: model.Now()} }
