package mcp

import (
	"testing"

	"omixor-core/model"
	"omixor-web/internal/core"
)

// issue_accept:本機模式經 appBackend 到核心,接受後 App 的 Issue 快取馬上有負責人(看板進行中),放回後退回待處理
func TestIssueAcceptTool(t *testing.T) {
	a := iaApp(t)
	iaRun(t, a, core.Person("tim"), "issue_create", map[string]any{"title": "待分析"})
	column := func() string {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		b, err := a.IssueBoard(model.IssueQuery{})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range b.Columns {
			for _, i := range c.Issues {
				if i.Key == "ISS-1" {
					return c.Key
				}
			}
		}
		return ""
	}
	if got := column(); got != model.BoardTodo {
		t.Fatalf("接受前在 %q", got)
	}
	i := iaRun(t, a, core.Person("tim"), "issue_accept", map[string]any{"id": "ISS-1"})
	if ac, _ := i["assigneeActor"].(map[string]any); ac == nil || i["status"] != "open" || i["message"] == nil {
		t.Fatalf("接受後要有負責人:%v", i)
	}
	if got := column(); got != model.BoardDoing {
		t.Fatalf("接受後看板要在進行中,實際 %q", got)
	}
	iaRun(t, a, core.Person("tim"), "issue_accept", map[string]any{"id": "iss-1"}) // 冪等、編號不分大小寫
	i = iaRun(t, a, core.Person("tim"), "issue_accept", map[string]any{"id": "ISS-1", "release": true})
	if i["assigneeActor"] != nil || column() != model.BoardTodo {
		t.Fatalf("放回後要退回待處理:%v", i)
	}
	iaErr(t, a, "issue_accept", map[string]any{"id": "ISS-9"}, "找不到 Issue ISS-9")
}
