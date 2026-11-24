package service_test

import (
	"errors"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 派任務帶 Issue:key(不分大小寫)或內部 id 都解析成內部 id、放在 links 最前面;沒帶 Issue 時 links 不變;
// 看不到(受限專案)、不存在、在垃圾桶裡的 Issue 一律 404 + issueNotFound 機器碼,而且不建立任務
func TestAssignTaskWithIssue(t *testing.T) {
	e := newREnv(t)
	assign := func(p *model.Principal, in service.TaskInput) (*model.AgentTask, error) {
		in.AgentID, in.Title = "cto", "手動任務"
		return e.svc.AssignTask(p, "o1", in)
	}
	for _, ref := range []string{e.appIssue.Key, strings.ToLower(e.appIssue.Key), e.appIssue.ID, " " + e.appIssue.Key + " "} {
		tk, err := assign(e.boss, service.TaskInput{Issue: ref, Links: []string{"外部連結"}})
		if err != nil {
			t.Fatalf("%q:%v", ref, err)
		}
		if strings.Join(tk.Links, ",") != e.appIssue.ID+",外部連結" || tk.Title != "手動任務" {
			t.Fatalf("%q 的 links:%v", ref, tk.Links)
		}
	}
	// 沒帶 Issue:links 不變
	tk, err := assign(e.boss, service.TaskInput{Links: []string{"ISS-9"}})
	if err != nil || strings.Join(tk.Links, ",") != "ISS-9" {
		t.Fatalf("沒帶 issue:%+v %v", tk, err)
	}

	count := func() int { l, _ := e.svc.ListAgentTasks(e.boss, "o1", service.TaskFilter{}); return len(l) }
	before := count()
	wantNotFound := func(what string, err error) {
		t.Helper()
		wantCode(t, what, err, 404)
		var se *service.Error
		if !errors.As(err, &se) || se.Data["code"] != service.IssueNotFoundCode {
			t.Fatalf("%s:要帶機器碼 %s:%+v", what, service.IssueNotFoundCode, err)
		}
	}
	_, err = assign(e.boss, service.TaskInput{Issue: "ISS-99"})
	wantNotFound("不存在", err)
	trashed, err := e.svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "會被丟掉"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteIssue(e.boss, "o1", trashed.ID); err != nil {
		t.Fatal(err)
	}
	_, err = assign(e.boss, service.TaskInput{Issue: trashed.ID})
	wantNotFound("垃圾桶裡的", err)

	// 一般成員 alice 要組織鏈上有 user→agent 的邊才能派;SEC 是受限專案,她不是成員 → 看不到
	if _, err := e.svc.SaveChain(e.boss, "o1", edges("user:alice@x>agent:cto>目標")); err != nil {
		t.Fatal(err)
	}
	if _, err := assign(e.alice, service.TaskInput{Issue: e.appIssue.Key}); err != nil {
		t.Fatalf("看得到的 Issue 可以連:%v", err)
	}
	before++
	for _, ref := range []string{e.secIssue.Key, e.secIssue.ID, "APP-2"} { // APP-2 是搬進受限專案前的舊 key
		_, err = assign(e.alice, service.TaskInput{Issue: ref})
		wantNotFound("看不到的 "+ref, err)
	}
	if _, err := assign(e.bob, service.TaskInput{Issue: e.secIssue.Key}); code(err) != 403 { // bob 看得到,但沒有組織鏈的邊
		t.Fatalf("bob 沒有派工權限:%v", err)
	}
	if got := count(); got != before {
		t.Fatalf("被拒絕時不能建立任務:%d → %d", before, got)
	}
}

// 委派的任務繼承來源任務的 links,所以用 Issue 連起來的任務再委派,下游任務仍連到同一張 Issue
func TestDelegateInheritsIssueLink(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	iss, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "要做的事"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveChain(boss, "o1", edges("agent:cto>agent:designer>設計")); err != nil {
		t.Fatal(err)
	}
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "cto", Title: "手動", Issue: iss.Key})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	cto, err := svc.AuthenticateAgent("cto", res.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	d, err := svc.DelegateTask(cto, "o1", service.DelegateInput{To: "designer", Title: "畫圖"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.Links, ",") != tk.ID+","+iss.ID {
		t.Fatalf("委派的任務要繼承來源任務的 links:%v", d.Links)
	}
}
