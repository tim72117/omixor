package mcp

import (
	"encoding/json"
	"os"
	"testing"

	"omixor-web/internal/core"
)

// iaApp 測試用的 App:自己的暫存資料夾;本機模式時核心(記憶體)裡放好測試用的 5 個角色(../core/testdata/agents.json)
func iaApp(t *testing.T) *core.App {
	t.Helper()
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	a := core.New()
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	seedAgents(t, a)
	return a
}

// seedAgents 本機模式時,把測試用的角色存進目前組織的核心(遠端模式的測試:角色在假的雲端,不放)
func seedAgents(t *testing.T, a *core.App) {
	t.Helper()
	if core.IsRemote() {
		return
	}
	a.Mu.Lock()
	org := a.CurrentOrgID()
	a.Mu.Unlock()
	b, err := os.ReadFile("../core/testdata/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	var list []core.Agent
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	for _, g := range list {
		if _, err := a.AgentOps().SaveAgent(org, g); err != nil {
			t.Fatal(err)
		}
	}
}

func iaRun(t *testing.T, a *core.App, actor core.Actor, name string, args map[string]any) map[string]any {
	t.Helper()
	out, err := Run(a, name, args, actor)
	if err != nil {
		t.Fatalf("%s:%v", name, err)
	}
	m, _ := core.Object(out).(map[string]any)
	return m
}

func iaErr(t *testing.T, a *core.App, name string, args map[string]any, want string) {
	t.Helper()
	_, err := Run(a, name, args, core.MCPActor)
	if err == nil || err.Error() != want {
		t.Fatalf("%s:想要錯誤「%s」,得到 %v", name, want, err)
	}
}

func TestIssueToolsAuthors(t *testing.T) {
	a := iaApp(t)
	i := iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "a", "labels": []any{"x", "X"}})
	if i["createdBy"] != "MCP 用戶端" || len(i["labels"].([]any)) != 1 {
		t.Fatal(i)
	}
	// 畫面呼叫:署名目前使用者
	i = iaRun(t, a, core.Person("tim"), "issue_create", map[string]any{"title": "b"})
	a.Mu.Lock()
	by := a.Issue("ISS-2").CreatedBy
	a.Mu.Unlock()
	if by != "tim" {
		t.Fatalf("畫面建立的 Issue 要署名使用者:%s", by)
	}
	i = iaRun(t, a, core.MCPActor, "issue_update", map[string]any{"id": "ISS-1", "comment": "c", "agent": "triager", "status": "closed"})
	if i["status"] != "closed" || i["comments"].([]any)[0].(map[string]any)["author"] != "Issue 整理員" {
		t.Fatal(i)
	}
	iaErr(t, a, "issue_update", map[string]any{"id": "ISS-9"}, "找不到 Issue ISS-9")
	iaErr(t, a, "issues", map[string]any{"status": "x"}, "status 必須是 open、closed、all 之一")
	l := iaRun(t, a, core.MCPActor, "issues", map[string]any{"status": "all", "limit": 1})
	if l["total"].(float64) != 2 || len(l["issues"].([]any)) != 1 {
		t.Fatal(l)
	}
}
