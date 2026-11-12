package mcp

import (
	"testing"

	"omixor-web/internal/core"
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

// ops_agent_context:只回傳角色能讀的文件、標出讀寫權限,任務只列這個角色未完成的
func TestAgentContext(t *testing.T) {
	a := iaApp(t)
	iaRun(t, a, core.MCPActor, "ops_doc_save", map[string]any{"title": "L0", "tab": "L0", "body": "你好"})
	iaRun(t, a, core.MCPActor, "ops_doc_save", map[string]any{"title": "待辦", "section": "todo", "body": "x"})
	iaRun(t, a, core.MCPActor, "ops_agent_task_create", map[string]any{"assign_to": "narrator", "title": "補語音"})
	iaRun(t, a, core.MCPActor, "ops_agent_task_create", map[string]any{"assign_to": "triager", "title": "整理"})
	iaErr(t, a, "ops_agent_context", map[string]any{"agent": "nobody"}, "找不到角色 nobody")

	c := iaRun(t, a, core.MCPActor, "ops_agent_context", map[string]any{"agent": "旁白製作"})
	if c["agent"].(map[string]any)["id"] != "narrator" {
		t.Fatal(c["agent"])
	}
	docs := c["docs"].(map[string]any)["docs"].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["title"] != "L0" || docs[0].(map[string]any)["access"] != "read" {
		t.Fatalf("narrator 只能讀課程:%v", docs)
	}
	if _, ok := docs[0].(map[string]any)["body"]; ok {
		t.Fatal("預設不附全文")
	}
	tasks := c["tasks"].(map[string]any)["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["title"] != "補語音" {
		t.Fatal(tasks)
	}

	c = iaRun(t, a, core.MCPActor, "ops_agent_context", map[string]any{"agent": "triager", "include_bodies": true})
	docs = c["docs"].(map[string]any)["docs"].([]any)
	d := docs[0].(map[string]any)
	if len(docs) != 1 || d["title"] != "待辦" || d["access"] != "write" || d["body"] != "x" {
		t.Fatalf("triager 可寫待辦:%v", docs)
	}
	if secs := c["cabinet"].([]any); len(secs) != 1 {
		t.Fatal(secs)
	}
}
