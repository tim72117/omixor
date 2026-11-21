package api_test

import (
	"errors"
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 刪角色:名下有任務 409(回應帶各狀態的任務數,經 client 與 backend.Remote 原樣帶回);帶 transferTo 才轉移再刪;單獨轉移的路由
func TestAgentDeleteTransferRoutes(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("lab", "bob@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	rboss, rbob := backend.NewRemote(boss), backend.NewRemote(e.login("evo-bob"))
	for _, a := range []model.Agent{{ID: "a1", Name: "甲", Active: true}, {ID: "a2", Name: "乙", Active: true}, {ID: "a3", Name: "丙", Active: false}} {
		if _, err := rboss.SaveAgent("lab", a); err != nil {
			t.Fatal(err)
		}
	}
	for _, title := range []string{"一", "二", "三"} {
		if _, err := rboss.AssignTask("lab", service.TaskInput{AgentID: "a1", Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rboss.CancelTask("lab", "T-3"); err != nil {
		t.Fatal(err)
	}

	// 409:帶各狀態的任務數
	err := rboss.DeleteAgent("lab", "a1")
	var se *service.Error
	if backend.Code(err) != 409 || !errors.As(err, &se) {
		t.Fatal("要 409", err)
	}
	if se.Data["code"] != service.AgentHasTasksCode || se.Data["agent"] != "a1" || se.Data["total"] != float64(3) {
		t.Fatalf("Data:%v", se.Data)
	}
	counts, _ := se.Data["taskCounts"].(map[string]any)
	if counts["queued"] != float64(2) || counts["canceled"] != float64(1) {
		t.Fatalf("taskCounts:%v", counts)
	}
	// 權限、目標不合格
	if err := rbob.DeleteAgentTo("lab", "a1", "a2"); backend.Code(err) != 403 {
		t.Fatal("成員不能刪", err)
	}
	for to, code := range map[string]int{"a1": 400, "a3": 400, "zz": 404} {
		if err := rboss.DeleteAgentTo("lab", "a1", to); backend.Code(err) != code {
			t.Fatalf("轉給 %s 要 %d:%v", to, code, err)
		}
	}
	// 單獨轉移(open:只轉待領取與需要協助)
	if _, err := rbob.TransferAgentTasks("lab", service.AgentTransferInput{From: "a1", To: "a2"}); backend.Code(err) != 403 {
		t.Fatal("成員不能轉移", err)
	}
	r, err := rboss.TransferAgentTasks("lab", service.AgentTransferInput{From: "a1", To: "a2"})
	if err != nil || r.Total != 2 || r.Scope != "open" || r.ByStatus["queued"] != 2 {
		t.Fatal(r, err)
	}
	if t3, _ := rboss.GetAgentTask("lab", "T-3"); t3.AgentID != "a1" {
		t.Fatal("已取消的 open 範圍不轉", t3.AgentID)
	}
	// 還有已取消的任務:照樣擋;帶 transferTo 轉移再刪
	if err := rboss.DeleteAgent("lab", "a1"); backend.Code(err) != 409 {
		t.Fatal(err)
	}
	if err := rboss.DeleteAgentTo("lab", "a1", "a2"); err != nil {
		t.Fatal(err)
	}
	if _, err := rboss.GetAgent("lab", "a1"); backend.Code(err) != 404 {
		t.Fatal("角色要刪掉", err)
	}
	l, _ := rboss.ListAgentTasks("lab", service.TaskFilter{AgentID: "a2"})
	if len(l) != 3 {
		t.Fatal("三筆都在 a2", len(l))
	}
	for _, x := range l {
		if x.AgentID != "a2" {
			t.Fatal(x)
		}
	}
}
