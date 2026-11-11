package mcp

import (
	"testing"

	"opsweb/internal/core"
)

func iaApp(t *testing.T) *core.App {
	t.Helper()
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	a := core.New()
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	return a
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
	i := iaRun(t, a, core.MCPActor, "ops_issue_create", map[string]any{"title": "a", "labels": []any{"x", "X"}})
	if i["createdBy"] != "MCP 用戶端" || len(i["labels"].([]any)) != 1 {
		t.Fatal(i)
	}
	// 畫面呼叫:署名目前使用者
	i = iaRun(t, a, core.Person("tim"), "ops_issue_create", map[string]any{"title": "b"})
	a.Mu.Lock()
	by := a.Issue("ISS-2").CreatedBy
	a.Mu.Unlock()
	if by != "tim" {
		t.Fatalf("畫面建立的 Issue 要署名使用者:%s", by)
	}
	i = iaRun(t, a, core.MCPActor, "ops_issue_update", map[string]any{"id": "ISS-1", "comment": "c", "agent": "triager", "status": "closed"})
	if i["status"] != "closed" || i["comments"].([]any)[0].(map[string]any)["author"] != "Issue 整理員" {
		t.Fatal(i)
	}
	iaErr(t, a, "ops_issue_update", map[string]any{"id": "ISS-9"}, "找不到 Issue ISS-9")
	iaErr(t, a, "ops_issues", map[string]any{"status": "x"}, "status 必須是 open、closed、all 之一")
	l := iaRun(t, a, core.MCPActor, "ops_issues", map[string]any{"status": "all", "limit": 1})
	if l["total"].(float64) != 2 || len(l["issues"].([]any)) != 1 {
		t.Fatal(l)
	}
}
