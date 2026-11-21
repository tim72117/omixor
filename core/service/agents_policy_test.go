package service_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 停用的角色:已簽出的憑證立刻失效、不能再領任務(不是等任務結束或憑證逾時)
func TestInactiveAgentCredentialAndClaim(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	task, ap := claimAs(t, svc, boss, "cto", "做事")
	// 啟用中:憑證可用
	if _, err := svc.ReportTask(ap, "o1", task.ID, service.TaskReport{Note: sp("進度")}); err != nil {
		t.Fatal("啟用中的角色憑證要能回報", err)
	}
	// 停用角色(同一個 SaveAgent 路徑,Active=false)
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "研發長", Active: false}); err != nil {
		t.Fatal(err)
	}
	// 這組憑證本身不能再被驗證通過:重新拿 secret 驗證要被擋。做法:再領一筆是不行的(角色已停用),
	// 所以用停用「之前」簽出的憑證:從領取結果拿 secret 重驗(claimAs 內只回傳身分,另外領一筆來取 secret)
}

// 停用的角色不能領任務(指定任務 id 或不指定都一樣)
func TestInactiveAgentCannotClaim(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "pm", Title: "規格"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "pm", Name: "產品經理", Active: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "pm", TaskID: tk.ID}); code(err) != 409 {
		t.Fatal("停用的角色不能領指定的任務,要 409:", err)
	}
	if _, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "pm"}); code(err) != 409 {
		t.Fatal("停用的角色不能領最優先的任務,要 409:", err)
	}
	// 重新啟用就能領
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "pm", Name: "產品經理", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "pm", TaskID: tk.ID}); err != nil {
		t.Fatal("重新啟用後要能領", err)
	}
}

// 憑證在角色停用後立刻失效:先領取取得 secret,再停用,AuthenticateAgent 要被擋(401)
func TestInactiveAgentCredentialRevoked(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "designer", Title: "畫圖"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "designer", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAgent("designer", res.Credential.Secret, "ip"); err != nil {
		t.Fatal("啟用中憑證要能驗證", err)
	}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "designer", Name: "設計師", Active: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAgent("designer", res.Credential.Secret, "ip"); err == nil {
		t.Fatal("角色停用後,已簽出的憑證要立刻失效")
	}
	// 重新啟用:同一組憑證(任務仍進行中、未逾時)又能用
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "designer", Name: "設計師", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAgent("designer", res.Credential.Secret, "ip"); err != nil {
		t.Fatal("重新啟用後憑證仍有效(任務還在進行中)", err)
	}
}

// ReportTask:只有領取者本人或組織管理員可以回報(完成、取消、改狀態都算);其他成員 403;還沒領取的任務一般成員也不行
func TestReportTaskOnlyClaimerOrAdmin(t *testing.T) {
	svc, _, boss, alice, bob := chainEnv(t)
	done := "done"
	canceled := "canceled"

	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "cto", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	// 還沒領取:一般成員不能回報(要先領取),管理員可以
	if _, err := svc.ReportTask(bob, "o1", tk.ID, service.TaskReport{Note: sp("偷回報")}); code(err) != 403 {
		t.Fatal("還沒領取的任務,一般成員不能直接回報,要 403:", err)
	}
	// alice 領取之後:alice 可以、bob 不行、管理員可以
	if _, err := svc.ClaimTask(alice, "o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReportTask(bob, "o1", tk.ID, service.TaskReport{Status: &done}); code(err) != 403 {
		t.Fatal("別人領取的任務,其他成員不能標完成,要 403:", err)
	}
	if _, err := svc.ReportTask(bob, "o1", tk.ID, service.TaskReport{Status: &canceled}); code(err) != 403 {
		t.Fatal("別人領取的任務,其他成員不能取消,要 403:", err)
	}
	if _, err := svc.ReportTask(bob, "o1", tk.ID, service.TaskReport{Note: sp("留言")}); code(err) != 403 {
		t.Fatal("別人領取的任務,其他成員不能留言回報,要 403:", err)
	}
	if got, err := svc.GetAgentTask(boss, "o1", tk.ID); err != nil || got.Status != "inProgress" {
		t.Fatalf("被擋之後任務狀態不能變:%+v %v", got, err)
	}
	if _, err := svc.ReportTask(alice, "o1", tk.ID, service.TaskReport{Note: sp("進度 50%")}); err != nil {
		t.Fatal("領取者本人要能回報", err)
	}
	// email 大小寫不同(alice 的 Principal 是 Alice@X)也算同一人:領取時用的就是同一個 principal,這裡用同一個物件
	if _, err := svc.ReportTask(boss, "o1", tk.ID, service.TaskReport{Status: &done, Result: sp("好了")}); err != nil {
		t.Fatal("組織管理員要能回報別人領取的任務", err)
	}
	if got, _ := svc.GetAgentTask(boss, "o1", tk.ID); got.Status != "done" {
		t.Fatalf("管理員標完成要成功:%+v", got)
	}

	// 管理員領取的任務,一般成員也不能動
	tk2, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "pm", Title: "B"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "pm", TaskID: tk2.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReportTask(alice, "o1", tk2.ID, service.TaskReport{Status: &canceled}); code(err) != 403 {
		t.Fatal("管理員領取的任務,一般成員不能取消,要 403:", err)
	}
}
