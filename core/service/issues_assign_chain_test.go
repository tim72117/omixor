package service_test

import (
	"testing"

	"omixor-core/service"
)

// Issue 指派給角色會自動排任務,等於派工:有設定組織鏈時不能繞過;沒設定時維持誰都能指派
func TestIssueAssigneeFollowsChain(t *testing.T) {
	svc, _, boss, alice, bob := chainEnv(t)

	// 沒設定組織鏈(沒有任何邊):成員可以指派(維持原本行為)
	i1, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "沒設鏈", Assignee: "cto"})
	if err != nil {
		t.Fatal("沒設組織鏈時成員要能指派:", err)
	}
	if _, err := svc.UpdateIssue(bob, "o1", i1.ID, service.IssuePatch{Assignee: sp("pm")}); err != nil {
		t.Fatal("沒設組織鏈時成員要能改指派:", err)
	}

	// 設定組織鏈:alice → cto;cto → pm
	if _, err := svc.SaveChain(boss, "o1", edges("user:alice@x>agent:cto>研發", "agent:cto>agent:pm>產品")); err != nil {
		t.Fatal(err)
	}

	// 建立時指派:alice 只能指派 cto;pm、designer 被擋(403,訊息有可以指派的對象)
	if _, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "A", Assignee: "cto"}); err != nil {
		t.Fatal("鏈上有 alice→cto,要能指派:", err)
	}
	if _, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "B", Assignee: "pm"}); code(err) != 403 {
		t.Fatal("鏈上沒有 alice→pm,建立時指派要 403:", err)
	}
	if _, err := svc.CreateIssue(bob, "o1", service.IssueInput{Title: "C", Assignee: "cto"}); code(err) != 403 {
		t.Fatal("鏈上沒有 bob→cto,要 403:", err)
	}
	// 被擋的建立不能留下 Issue
	list, _ := svc.ListIssues(boss, "o1")
	for _, it := range list {
		if it.Title == "B" || it.Title == "C" {
			t.Fatalf("被擋下的指派不能建立 Issue:%+v", it)
		}
	}

	// 沒有指派的 Issue 不受影響
	if _, err := svc.CreateIssue(bob, "o1", service.IssueInput{Title: "D"}); err != nil {
		t.Fatal("不指派的 Issue 不受組織鏈限制:", err)
	}

	// 修改指派:換成鏈外的角色被擋;取消指派不受限;原本的指派(管理員設的)沒改就不重驗
	byBoss, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "E", Assignee: "designer"})
	if err != nil {
		t.Fatal("組織管理員不受組織鏈限制:", err)
	}
	if _, err := svc.UpdateIssue(alice, "o1", byBoss.ID, service.IssuePatch{Title: sp("E2")}); err != nil {
		t.Fatal("沒換指派的修改不能因為組織鏈被擋:", err)
	}
	if _, err := svc.UpdateIssue(alice, "o1", byBoss.ID, service.IssuePatch{Assignee: sp("designer")}); err != nil {
		t.Fatal("指派沒變(同一個角色)不重新檢查:", err)
	}
	if _, err := svc.UpdateIssue(alice, "o1", byBoss.ID, service.IssuePatch{Assignee: sp("pm")}); code(err) != 403 {
		t.Fatal("換成鏈外的角色要 403:", err)
	}
	if got, _ := svc.GetIssue(boss, "o1", byBoss.ID); got.Assignee != "designer" {
		t.Fatalf("被擋之後指派不能變:%+v", got)
	}
	if _, err := svc.UpdateIssue(alice, "o1", byBoss.ID, service.IssuePatch{Assignee: sp("cto")}); err != nil {
		t.Fatal("換成鏈上的角色要成功:", err)
	}
	if _, err := svc.UpdateIssue(alice, "o1", byBoss.ID, service.IssuePatch{Assignee: sp("")}); err != nil {
		t.Fatal("取消指派不受限:", err)
	}
	if _, err := svc.UpdateIssue(boss, "o1", byBoss.ID, service.IssuePatch{Assignee: sp("designer")}); err != nil {
		t.Fatal("組織管理員改指派不受限:", err)
	}
}

// 角色身分(角色憑證)指派 Issue:有設定組織鏈時要有 agent:<自己> → agent:<目標> 的邊
func TestIssueAssigneeByAgentFollowsChain(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	_, cto := claimAs(t, svc, boss, "cto", "開 Issue")

	// 沒設定組織鏈:角色不能指派(人可以,角色一定要有明確的邊;ISS-7)
	if _, err := svc.CreateIssue(cto, "o1", service.IssueInput{Title: "無鏈", Assignee: "pm"}); code(err) != 403 {
		t.Fatal("沒設組織鏈時角色不能指派,要 403:", err)
	}
	if _, err := svc.SaveChain(boss, "o1", edges("agent:cto>agent:pm>產品")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateIssue(cto, "o1", service.IssueInput{Title: "鏈上", Assignee: "pm"}); err != nil {
		t.Fatal("鏈上有 cto→pm,要能指派:", err)
	}
	if _, err := svc.CreateIssue(cto, "o1", service.IssueInput{Title: "鏈外", Assignee: "designer"}); code(err) != 403 {
		t.Fatal("鏈上沒有 cto→designer,角色不能繞過組織鏈指派,要 403:", err)
	}
}
