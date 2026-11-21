package api_test

import (
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// Issue 子項的 API:新增、列(預設未完成)、改、領取、回報、卡住與解除、提升、取消、刪除;錯誤碼(依賴 409、環 400、非建立者 403);
// 角色憑證可以新增與讀、不能改與領取;受限專案非成員每個路由都 404 且訊息與不存在的 Issue 相同
func TestIssueItemAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	for _, o := range []string{"lab", "o2"} {
		if _, err := boss.CreateOrg(o, o); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []string{"alice@example.com", "bob@example.com"} {
		if _, err := boss.SaveUser(m, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := boss.SaveMember("lab", m, "member"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := boss.SaveAgent("lab", model.Agent{ID: "cto", Name: "研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	alice, bob := e.login("evo-alice"), e.login("evo-bob")
	iss, err := alice.CreateIssue("lab", service.IssueInput{Title: "大構想"})
	if err != nil {
		t.Fatal(err)
	}
	k := iss.Key
	a, err := alice.AddIssueItem("lab", k, service.ItemInput{Title: "第一階段", Files: []string{"a.go"}, Acceptance: "測試過"})
	if err != nil || a.Key != k+".1" || a.Status != "todo" || a.CreatedBy != "alice@example.com" || a.CreatedByActor == nil || a.CreatedByActor.Email != "alice@example.com" {
		t.Fatalf("%+v %v", a, err)
	}
	b, err := alice.AddIssueItem("lab", k, service.ItemInput{Title: "第二階段", DependsOn: k + ".1"})
	if err != nil || b.DependsOn != k+".1" {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := alice.AddIssueItem("lab", k, service.ItemInput{Title: "x", DependsOn: "9"}); status(err) != 400 {
		t.Fatal("前置不存在 400", err)
	}
	if _, err := alice.AddIssueItem("lab", k, service.ItemInput{Title: " "}); status(err) != 400 {
		t.Fatal("空標題 400", err)
	}
	if _, err := alice.AddIssueItem("o2", k, service.ItemInput{Title: "x"}); status(err) != 403 {
		t.Fatal("不是成員 403", err)
	}
	dep := k + ".2"
	if _, err := alice.UpdateIssueItem("lab", k, "1", service.ItemPatch{DependsOn: &dep}); status(err) != 400 {
		t.Fatal("成環 400", err)
	}
	if _, err := alice.ClaimIssueItem("lab", k, "2", service.ItemClaimInput{}); status(err) != 409 {
		t.Fatal("前置未完成 409", err)
	}
	c, err := alice.ClaimIssueItem("lab", k, "1", service.ItemClaimInput{Note: "session"})
	if err != nil || c.Item.Status != "doing" || c.Item.ClaimedBy != "alice@example.com" || c.Task != nil || c.Credential != nil {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := bob.ClaimIssueItem("lab", k, k+".1", service.ItemClaimInput{}); status(err) != 409 {
		t.Fatal("別人領取中 409", err)
	}
	if _, err := bob.ReportIssueItem("lab", k, "1", service.ItemReport{Status: "done"}); status(err) != 403 {
		t.Fatal("非領取者不能回報 403", err)
	}
	d, err := alice.ReportIssueItem("lab", k, "1", service.ItemReport{Status: "done", Evidence: []byte(`{"commit":"abc"}`)})
	if err != nil || d.Status != "done" || !strings.Contains(string(d.Evidence), "abc") {
		t.Fatalf("%+v %v", d, err)
	}
	if l, err := alice.ListIssueItems("lab", k, service.ItemListQuery{}); err != nil || len(l) != 1 || l[0].Key != k+".2" {
		t.Fatal("預設只回未完成", l, err)
	}
	if l, _ := alice.ListIssueItems("lab", k, service.ItemListQuery{IncludeDone: true}); len(l) != 2 {
		t.Fatal(l)
	}
	if l, _ := alice.ListIssueItems("lab", k, service.ItemListQuery{Status: "done"}); len(l) != 1 {
		t.Fatal(l)
	}
	if _, err := alice.ListIssueItems("lab", k, service.ItemListQuery{Status: "zz"}); status(err) != 400 {
		t.Fatal(err)
	}
	if g, err := bob.GetIssueItem("lab", k, "2"); err != nil || g.Key != k+".2" {
		t.Fatal(g, err)
	}
	if _, err := bob.GetIssueItem("lab", k, "99"); status(err) != 404 {
		t.Fatal(err)
	}
	title := "改名"
	if u, err := bob.UpdateIssueItem("lab", k, "2", service.ItemPatch{Title: &title}); err != nil || u.Title != "改名" {
		t.Fatal(u, err)
	}
	// 卡住與解除、取消、提升、刪除
	if _, err := bob.ClaimIssueItem("lab", k, "2", service.ItemClaimInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.BlockIssueItem("lab", k, "2", service.ItemReport{}); status(err) != 400 {
		t.Fatal("卡住要附原因", err)
	}
	bl, err := bob.BlockIssueItem("lab", k, "2", service.ItemReport{BlockReason: "要選", WaitForKind: "user_decision", WaitFor: "A 或 B"})
	if err != nil || bl.Status != "blocked" || bl.WaitFor != "A 或 B" {
		t.Fatal(bl, err)
	}
	if u, err := alice.UnblockIssueItem("lab", k, "2"); err != nil || u.Status != "todo" || u.BlockReason != "" {
		t.Fatal(u, err)
	}
	if r, err := bob.ReleaseIssueItem("lab", k, "2"); status(err) != 409 {
		t.Fatal("todo 不能放棄領取 409", r, err)
	}
	pr, err := alice.PromoteIssueItem("lab", k, "2", service.ItemPromoteInput{Priority: "high"})
	if err != nil || pr.Issue.Source != k+".2" || pr.Issue.Priority != "high" || pr.Item.Status != "canceled" {
		t.Fatalf("%+v %v", pr, err)
	}
	if _, err := alice.PromoteIssueItem("lab", k, "2", service.ItemPromoteInput{}); status(err) != 409 {
		t.Fatal("重複提升 409", err)
	}
	cn, err := alice.AddIssueItem("lab", k, service.ItemInput{Title: "要取消"})
	if err != nil {
		t.Fatal(err)
	}
	if x, err := bob.CancelIssueItem("lab", k, cn.Key); err != nil || x.Status != "canceled" {
		t.Fatal(x, err)
	}
	if err := bob.DeleteIssueItem("lab", k, cn.Key); status(err) != 403 {
		t.Fatal("非建立者 403", err)
	}
	if err := alice.DeleteIssueItem("lab", k, cn.Key); err != nil {
		t.Fatal(err)
	}
	if err := boss.DeleteIssueItem("lab", k, "1"); err != nil { // 組織管理員
		t.Fatal(err)
	}

	// 角色憑證:可以新增與讀、不能改與領取
	tk, err := boss.AssignTask("lab", service.TaskInput{AgentID: "cto", Title: "做事"})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := alice.ClaimTask("lab", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := client.New(e.srv.URL)
	cred := "cto:" + cl.Credential.Secret
	role.Token = func() string { return cred }
	r1, err := role.AddIssueItem("lab", k, service.ItemInput{Title: "下一步"})
	if err != nil || r1.CreatedBy != "agent:cto" || r1.CreatedByActor == nil || r1.CreatedByActor.Kind != model.ActorKindAgent {
		t.Fatalf("%+v %v", r1, err)
	}
	if _, err := role.AddIssueItem("lab", k, service.ItemInput{Title: "指派給人", Assignee: "alice@example.com"}); status(err) != 403 {
		t.Fatal("角色不能指派給人", err)
	}
	if l, err := role.ListIssueItems("lab", k, service.ItemListQuery{}); err != nil || len(l) != 1 {
		t.Fatal(l, err)
	}
	for name, f := range map[string]func() error{
		"update": func() error {
			_, err := role.UpdateIssueItem("lab", k, r1.Key, service.ItemPatch{Title: &title})
			return err
		},
		"claim":  func() error { _, err := role.ClaimIssueItem("lab", k, r1.Key, service.ItemClaimInput{}); return err },
		"cancel": func() error { _, err := role.CancelIssueItem("lab", k, r1.Key); return err },
		"promote": func() error {
			_, err := role.PromoteIssueItem("lab", k, r1.Key, service.ItemPromoteInput{})
			return err
		},
		"delete": func() error { return role.DeleteIssueItem("lab", k, r1.Key) },
		"report": func() error {
			_, err := role.ReportIssueItem("lab", k, r1.Key, service.ItemReport{Status: "done"})
			return err
		},
	} {
		if status(f()) != 403 {
			t.Fatalf("角色憑證不能 %s", name)
		}
	}

	// 受限專案:非成員每個路由都和不存在的 Issue 一樣 404
	p, err := backend.NewRemote(boss).CreateProject("lab", service.ProjectInput{Name: "機密", Key: "SEC", Restricted: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SetProjectMember("lab", p.ID, service.ProjectMemberInput{Actor: "bob@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	sec, err := boss.CreateIssue("lab", service.IssueInput{Title: "機密標題", Project: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	si, err := boss.AddIssueItem("lab", sec.Key, service.ItemInput{Title: "機密子項"})
	if err != nil {
		t.Fatal(err)
	}
	_, miss := alice.ListIssueItems("lab", "SEC-99", service.ItemListQuery{})
	same := func(what string, err error) {
		t.Helper()
		if status(err) != 404 || strings.ReplaceAll(err.Error(), "SEC-1", "SEC-99") != miss.Error() {
			t.Fatalf("%s:要和不存在的 Issue 一樣 404:%v / %v", what, err, miss)
		}
	}
	_, err = alice.ListIssueItems("lab", sec.Key, service.ItemListQuery{})
	same("GET items", err)
	_, err = alice.GetIssueItem("lab", sec.Key, "1")
	same("GET item", err)
	_, err = alice.AddIssueItem("lab", sec.Key, service.ItemInput{Title: "偷加"})
	same("POST items", err)
	_, err = alice.ClaimIssueItem("lab", sec.Key, "1", service.ItemClaimInput{})
	same("claim", err)
	_, err = alice.ReportIssueItem("lab", sec.Key, "1", service.ItemReport{Status: "done"})
	same("report", err)
	_, err = alice.PromoteIssueItem("lab", sec.Key, "1", service.ItemPromoteInput{})
	same("promote", err)
	same("DELETE", alice.DeleteIssueItem("lab", sec.Key, "1"))
	if g, err := bob.GetIssueItem("lab", sec.Key, si.Key); err != nil || g.Key != si.Key {
		t.Fatal("成員看得到", g, err)
	}
}
