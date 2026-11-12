package api_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

func TestImportAgents(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveUser("alice@example.com", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("o1", "alice@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	rid := "R1"
	in := service.AgentImport{
		Agents: []model.Agent{
			{ID: "analyst", Name: "分析師", Active: true, DocAccess: map[string]string{"overview": "read"},
				Routines: []model.AgentRoutine{{ID: rid, Kind: "daily", Hour: 9, Enabled: true, Title: "每日檢查", Priority: "normal"}}},
			{ID: "dev", Name: "工程師", Active: false},
		},
		Tasks: []model.AgentTask{
			{ID: "T-3", AgentID: "analyst", Title: "進行中的", Status: "inProgress", Priority: "high", ClaimedBy: "x", RoutineID: &rid,
				Notes: []model.AgentTaskNote{{ID: "n1", Author: "agent:analyst", Text: "做到一半"}}},
			{ID: "T-1", AgentID: "dev", Title: "做完的", Status: "done", Result: "好了"},
			{ID: "T-2", AgentID: "analyst", Title: "取消的", Status: "canceled"},
		},
	}
	// 一般成員不行
	if _, err := alice.ImportAgents("o1", in); status(err) != 403 {
		t.Fatal("成員不能匯入", err)
	}
	// 任務的角色不在匯入的角色裡
	bad := service.AgentImport{Agents: in.Agents, Tasks: []model.AgentTask{{ID: "T-9", AgentID: "nobody", Title: "x"}}}
	if _, err := boss.ImportAgents("o1", bad); status(err) != 400 {
		t.Fatal(err)
	}
	r, err := boss.ImportAgents("o1", in)
	if err != nil || r.Agents != 2 || r.Tasks != 3 || len(r.Requeue) != 1 || r.Requeue[0] != "T-3" {
		t.Fatal(r, err)
	}
	ags, _ := boss.ListAgents("o1")
	if len(ags) != 2 || ags[0].DocAccess["overview"] != "read" || len(ags[0].Routines) != 1 {
		t.Fatalf("%+v", ags)
	}
	ts, _ := boss.ListAgentTasks("o1", service.TaskFilter{Status: "all"})
	if len(ts) != 3 || ts[0].ID != "T-3" || ts[0].Status != "queued" || ts[0].ClaimedBy != "" || len(ts[0].Notes) != 1 {
		t.Fatalf("%+v", ts)
	}
	// 之後新建的任務接著編號
	nt, err := boss.AssignTask("o1", service.TaskInput{AgentID: "analyst", Title: "新的"})
	if err != nil || nt.ID != "T-4" {
		t.Fatal(nt, err)
	}
	// 第二次匯入拒絕
	if _, err := boss.ImportAgents("o1", in); status(err) != 409 {
		t.Fatal("已有資料要拒絕", err)
	}
}
