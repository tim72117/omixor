package service_test

import (
	"errors"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// ISS-7:角色憑證對 Issue 的寫入限制
func TestAgentIssueFieldLimits(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	_, cto := claimAs(t, svc, boss, "cto", "做事")
	other, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "別人的", Body: "原文"})
	if err != nil {
		t.Fatal(err)
	}
	denied := map[string]service.IssuePatch{
		"title":     {Title: sp("改標題")},
		"body":      {Body: sp("整個換掉")},
		"priority":  {Priority: sp("urgent")},
		"labels":    {Labels: &[]string{"x"}},
		"repo":      {Repo: sp("a/b")},
		"source":    {Source: sp("x")},
		"link":      {Link: sp("http://x")},
		"關閉別人的":     {Status: sp("closed")},
		"附加別人的內文":   {AppendBody: sp("多一段")},
		"留言夾帶改標題":   {Title: sp("改"), Comments: &[]model.Comment{{Text: "hi"}}},
		"和可以的欄位一起送": {Status: sp("open"), Priority: sp("low")},
	}
	for name, patch := range denied {
		if _, err := svc.UpdateIssue(cto, "o1", other.ID, patch); code(err) != 403 {
			t.Errorf("%s:要 403:%v", name, err)
		}
	}
	// 和目前一樣的值不算修改
	if _, err := svc.UpdateIssue(cto, "o1", other.ID, service.IssuePatch{Title: sp("別人的"), Status: sp("open"), Priority: sp("normal"), Assignee: sp("")}); err != nil {
		t.Fatal("沒有實際修改要放行:", err)
	}
	// 看得到的都可以留言
	if _, err := svc.AddIssueComment(cto, "o1", other.ID, service.CommentInput{Text: "我看到了"}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetIssue(boss, "o1", other.ID)
	if got.Title != "別人的" || got.Body != "原文" || got.Status != "open" || got.Priority != "normal" || len(got.Comments) != 1 {
		t.Fatalf("被擋下的修改不能生效:%+v", got)
	}

	// 自己的:指派給自己、自己建立的、目前任務連到的
	assigned, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "指派給 cto", Assignee: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateIssue(cto, "o1", service.IssueInput{Title: "cto 開的", Priority: "high", Labels: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "任務連到的"})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "pm", Title: "看這張", Issue: linked.Key})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "pm", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	pm, err := svc.AuthenticateAgent("pm", res.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		who *model.Principal
		id  string
	}{{cto, assigned.ID}, {cto, created.ID}, {pm, linked.ID}} {
		u, err := svc.UpdateIssue(c.who, "o1", c.id, service.IssuePatch{AppendBody: sp("結論"), Status: sp("closed")})
		if err != nil || u.Status != "closed" {
			t.Fatalf("自己的 Issue 要能附加與關閉:%+v %v", u, err)
		}
		if _, err := svc.UpdateIssue(c.who, "o1", c.id, service.IssuePatch{Title: sp("還是不行")}); code(err) != 403 {
			t.Fatal("自己的也不能改標題:", err)
		}
	}

	// 回報時自己把別人的 Issue 連上任務,不能藉此變成自己的
	if _, err := svc.ReportTask(cto, "o1", "", service.TaskReport{Links: []string{other.Key}}); code(err) != 403 {
		t.Fatal("不能把別人的 Issue 連上任務:", err)
	}
	if _, err := svc.ReportTask(cto, "o1", "", service.TaskReport{Links: []string{created.Key, "https://example.com"}}); err != nil {
		t.Fatal("自己建立的 Issue 與網址可以連:", err)
	}
	if _, err := svc.UpdateIssue(cto, "o1", other.ID, service.IssuePatch{Status: sp("closed")}); code(err) != 403 {
		t.Fatal("仍然不能關別人的:", err)
	}
}

// ISS-7:角色指派一定要有組織鏈的邊;改既有 Issue 的指派另外要是自己的
func TestAgentIssueAssigneeLimits(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	_, cto := claimAs(t, svc, boss, "cto", "派工")
	own, err := svc.CreateIssue(cto, "o1", service.IssueInput{Title: "cto 開的"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "別人的"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIssue(cto, "o1", own.ID, service.IssuePatch{Assignee: sp("pm")}); code(err) != 403 {
		t.Fatal("沒有組織鏈:不能指派:", err)
	}
	if _, err := svc.SaveChain(boss, "o1", edges("agent:cto>agent:pm")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIssue(cto, "o1", other.ID, service.IssuePatch{Assignee: sp("pm")}); code(err) != 403 {
		t.Fatal("別人的 Issue 有邊也不能改指派:", err)
	}
	if u, err := svc.UpdateIssue(cto, "o1", own.ID, service.IssuePatch{Assignee: sp("pm")}); err != nil || u.Assignee != "pm" {
		t.Fatal("自己的、鏈上有邊:", u, err)
	}
	if _, err := svc.UpdateIssue(cto, "o1", own.ID, service.IssuePatch{Assignee: sp("designer")}); code(err) != 403 {
		t.Fatal("鏈外:", err)
	}
	if _, err := svc.UpdateIssue(cto, "o1", own.ID, service.IssuePatch{Assignee: sp("")}); code(err) != 403 {
		t.Fatal("不能清掉指派:", err)
	}
}

// linkedAgent 派一筆連到 issue 的任務給 agent 並領取,回傳綁定該任務的角色身分
func linkedAgent(t *testing.T, e *rEnv, agent, issue string) *model.Principal {
	t.Helper()
	tk, err := e.svc.AssignTask(e.boss, "o1", service.TaskInput{AgentID: agent, Title: "看這張", Issue: issue})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.ClaimTask(e.boss, "o1", service.ClaimInput{AgentID: agent, TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.AuthenticateAgent(agent, res.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ISS-7:角色不能靠接受沒人認領的 Issue 變成負責人;任務連到的才能接受
func TestAgentAcceptOnlyOwnIssue(t *testing.T) {
	e := newREnv(t)
	other, err := e.svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "不相關", Project: sp(e.app.ID)})
	if err != nil {
		t.Fatal(err)
	}
	cto := linkedAgent(t, e, "cto", e.appIssue.Key)
	_, err = e.svc.AcceptIssue(cto, "o1", other.Key)
	wantCode(t, "不相關的沒人認領 Issue", err, 403)
	var se *service.Error
	if !errors.As(err, &se) || se.Data["code"] != service.AgentAcceptForbiddenCode {
		t.Fatalf("要帶機器碼:%v", err)
	}
	_, err = e.svc.UpdateIssue(cto, "o1", other.Key, service.IssuePatch{Status: sp("closed")})
	wantCode(t, "接受被擋之後也不能關", err, 403)
	if cur, _ := e.svc.GetIssue(e.boss, "o1", other.Key); cur == nil || cur.AssigneeActor != nil || cur.Status != "open" || len(cur.Comments) != 0 {
		t.Fatalf("被擋下不能留下改動:%+v", cur)
	}
	if i, err := e.svc.AcceptIssue(cto, "o1", e.appIssue.Key); err != nil || i.Assignee != "cto" {
		t.Fatalf("任務連到的可以接受:%+v %v", i, err)
	}
	if _, err := e.svc.ReleaseIssue(cto, "o1", e.appIssue.Key); err != nil {
		t.Fatal(err)
	}
}

// 角色回報不能先連上「之後才存在」的 Issue(還沒建立的編號、垃圾桶裡的),否則建立或還原後就變成自己的
func TestAgentTaskLinksRejectDanglingIssueRefs(t *testing.T) {
	e := newREnv(t)
	cto := linkedAgent(t, e, "cto", e.appIssue.Key)
	report := func(link string) error {
		_, err := e.svc.ReportTask(cto, "o1", "", service.TaskReport{Links: []string{link}})
		return err
	}
	trashed, err := e.svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "丟掉的"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteIssue(e.boss, "o1", trashed.ID); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"ISS-999", "iss-999", "APP-999", "NEW-1", "I\u017f\u017f-999", " ISS-999\n", trashed.Key, trashed.ID} {
		wantCode(t, "擋下 "+l, report(l), 403)
	}
	for _, l := range []string{"https://example.com/ISS-999", "T-1", "DOC-3", "CH-1", "INC-2", "外部連結",
		"0190a000-0000-7000-8000-000000000000", e.appIssue.Key, e.appIssue.ID} {
		if err := report(l); err != nil {
			t.Fatalf("%q 要能連:%v", l, err)
		}
	}
	// 派任務的人用 key 連的(即使還沒建立)原樣保留,角色重報同一個也行
	tk, err := e.svc.AssignTask(e.boss, "o1", service.TaskInput{AgentID: "pm", Title: "手動", Links: []string{"ISS-500"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.ClaimTask(e.boss, "o1", service.ClaimInput{AgentID: "pm", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	pm, err := e.svc.AuthenticateAgent("pm", res.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReportTask(pm, "o1", "", service.TaskReport{Links: []string{"ISS-500"}}); err != nil {
		t.Fatal("原本就有的連結:", err)
	}
}
