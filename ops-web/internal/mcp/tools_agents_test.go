package mcp

import (
	"testing"

	"opsweb/internal/core"
)

func TestAgentToolsFlow(t *testing.T) {
	a := iaApp(t)
	iaErr(t, a, "ops_agent_task_create", map[string]any{"assign_to": "narrator", "agent": "narrator", "title": "x"},
		"不能把任務派給自己(旁白製作);要派給別的角色請改 assign_to")
	task := iaRun(t, a, core.MCPActor, "ops_agent_task_create", map[string]any{"assign_to": "narrator", "agent": "course-dev", "title": "x"})
	if task["createdBy"] != "課程開發" || task["status"] != "queued" {
		t.Fatal(task)
	}
	c := iaRun(t, a, core.MCPActor, "ops_agent_task_claim", map[string]any{"agent": "旁白製作"})
	if c["task"].(map[string]any)["status"] != "inProgress" || c["agent"].(map[string]any)["openTasks"].(map[string]any)["inProgress"].(float64) != 1 {
		t.Fatal(c)
	}
	c = iaRun(t, a, core.MCPActor, "ops_agent_task_claim", map[string]any{"agent": "narrator"})
	if c["task"] != nil || c["message"] != "旁白製作 目前沒有待領取的任務" {
		t.Fatal(c)
	}
	r := iaRun(t, a, core.MCPActor, "ops_agent_task_report", map[string]any{"id": "T-1", "status": "done", "note": "n", "result": "ok"})
	if r["status"] != "done" || r["completedAt"] == nil || r["notes"].([]any)[0].(map[string]any)["author"] != "旁白製作" {
		t.Fatal(r)
	}
	iaErr(t, a, "ops_agent_task_claim", map[string]any{"agent": "narrator", "id": "T-1"}, "T-1 已經完成,不能再領")

	g := iaRun(t, a, core.MCPActor, "ops_agent_upsert", map[string]any{"id": "pm", "name": "PM",
		"routines":   []any{map[string]any{"title": "週會", "kind": "weekly", "hour": 99, "weekday": 0}},
		"doc_access": map[string]any{"L0": "write", "docs": "read"}})
	rs := g["routines"].([]any)[0].(map[string]any)
	if rs["schedule"] != "每週日 23:00" || len(g["docAccess"].([]any)) != 2 {
		t.Fatal(g)
	}
	iaErr(t, a, "ops_agent_upsert", map[string]any{"id": "pm", "doc_access": map[string]any{"zz": "read"}}, "文件櫃沒有「zz」這個區塊或頁籤")
	// 錯誤時不會改到角色
	a.Mu.Lock()
	n := len(a.FindAgent("pm").DocAccess)
	a.Mu.Unlock()
	if n != 2 {
		t.Fatal("錯誤時不能改到角色")
	}
}
