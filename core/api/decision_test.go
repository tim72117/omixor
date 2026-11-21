package api_test

import (
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// 待決定 / 需要協助的 API:成員提出與處理、兩種 kind、篩選、由決定開 Issue、非成員 403、
// 角色憑證可以提出與讀、不能處理與重新開啟與開 Issue;署名是呼叫者本人(輸出相容字串 + actor 物件)
func TestDecisionAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	for _, o := range []string{"o1", "o2"} {
		if _, err := boss.CreateOrg(o, o); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := boss.SaveUser("alice@example.com", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("o1", "alice@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveAgent("o1", model.Agent{ID: "cto", Name: "研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	iss, err := alice.CreateIssue("o1", service.IssueInput{Title: "來源"})
	if err != nil {
		t.Fatal(err)
	}
	opts := []model.DecisionOption{{Label: "A", Recommended: true}, {Label: "B"}}
	d, err := alice.AddDecision("o1", service.DecisionInput{Title: "選哪個", Options: opts, SourceIssue: iss.Key})
	if err != nil || d.ID != "DEC-1" || d.Kind != "decision" || d.Options[0].ID != "o1" || d.SourceIssue != iss.Key ||
		d.CreatedBy != "alice@example.com" || d.CreatedByActor == nil || d.CreatedByActor.Kind != model.ActorKindUser || d.CreatedByActor.Email != "alice@example.com" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := alice.AddDecision("o1", service.DecisionInput{Kind: "help", Title: "x", Options: opts}); status(err) != 400 {
		t.Fatal("help 帶 options 要 400", err)
	}
	if _, err := alice.AddDecision("o2", service.DecisionInput{Title: "x", Options: opts}); status(err) != 403 {
		t.Fatal("不是成員 403", err)
	}
	if _, err := alice.GetDecision("o1", "DEC-404"); status(err) != 404 {
		t.Fatal(err)
	}

	// 角色憑證
	tk, err := boss.AssignTask("o1", service.TaskInput{AgentID: "cto", Title: "做事"})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := alice.ClaimTask("o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := client.New(e.srv.URL)
	cred := "cto:" + cl.Credential.Secret
	role.Token = func() string { return cred }
	h, err := role.AddDecision("o1", service.DecisionInput{Kind: "help", Title: "請備份", SourceIssue: iss.Key})
	if err != nil || h.CreatedBy != "agent:cto" || h.Kind != "help" || h.CreatedByActor == nil || h.CreatedByActor.Kind != model.ActorKindAgent {
		t.Fatalf("角色憑證可以提出 help:%+v %v", h, err)
	}
	if l, err := role.ListDecisions("o1", service.DecisionListQuery{Kind: "help"}); err != nil || len(l) != 1 {
		t.Fatal(l, err)
	}
	if _, err := role.AnswerDecision("o1", h.ID, service.DecisionAnswer{Answer: "done"}); status(err) != 403 {
		t.Fatal("角色憑證不能處理", err)
	}
	if _, err := role.ReopenDecision("o1", h.ID); status(err) != 403 {
		t.Fatal(err)
	}
	if _, err := role.DecisionToIssue("o1", d.ID, service.DecisionToIssueInput{}); status(err) != 403 {
		t.Fatal("角色憑證不能開 Issue", err)
	}
	if _, err := role.WithdrawDecision("o1", d.ID); status(err) != 403 {
		t.Fatal("角色憑證不能撤回別人提的", err)
	}

	// 處理、列表、摘要、重新開啟
	done, err := alice.AnswerDecision("o1", h.ID, service.DecisionAnswer{Answer: "done", Note: "好了", Evidence: "https://x"})
	if err != nil || done.DecidedBy != "alice@example.com" || done.DecidedByActor == nil {
		t.Fatal(done, err)
	}
	if _, err := alice.AnswerDecision("o1", h.ID, service.DecisionAnswer{Answer: "done"}); status(err) != 409 {
		t.Fatal("重複處理 409", err)
	}
	if l, err := alice.ListDecisions("o1", service.DecisionListQuery{Mine: true}); err != nil || len(l) != 1 || l[0].ID != d.ID {
		t.Fatal("待我處理只剩 DEC-1", l, err)
	}
	if l, _ := alice.ListDecisions("o1", service.DecisionListQuery{Issue: iss.Key, Status: "decided"}); len(l) != 1 || l[0].Evidence != "https://x" {
		t.Fatal(l)
	}
	sum, err := alice.DecisionSummaries("o1")
	if err != nil || sum[iss.Key] != (model.DecisionSummary{Open: 1, Decided: 1}) {
		t.Fatal(sum, err)
	}
	if r, err := alice.ReopenDecision("o1", h.ID); err != nil || r.Status != "open" {
		t.Fatal(r, err)
	}
	if r, err := alice.WithdrawDecision("o1", h.ID); status(err) != 403 {
		t.Fatal("非建立者 403", r, err)
	}
	if r, err := boss.WithdrawDecision("o1", h.ID); err != nil || r.Status != "withdrawn" {
		t.Fatal(r, err)
	}
	// 由決定開 Issue
	if _, err := alice.DecisionToIssue("o1", d.ID, service.DecisionToIssueInput{}); status(err) != 409 {
		t.Fatal("已經有來源 Issue 409", err)
	}
	d2, err := alice.AddDecision("o1", service.DecisionInput{Title: "沒來源的", Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	res, err := alice.DecisionToIssue("o1", d2.ID, service.DecisionToIssueInput{Priority: "high"})
	if err != nil || res.Issue.Source != d2.ID || res.Issue.Priority != "high" || res.Decision.SourceIssue != res.Issue.Key {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := alice.ListDecisions("o1", service.DecisionListQuery{Kind: "zz"}); status(err) != 400 {
		t.Fatal(err)
	}
}

// 受限專案:有來源 Issue 的決定繼承可見性。非成員在每個路由都看不到(列表沒有、摘要沒有、單筆 404 且訊息與不存在相同、不能處理 / 撤回 / 開 Issue),
// 成員看得到;沒有來源的決定大家看得到
func TestDecisionRestrictedRoutes(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "lab"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"alice@example.com", "bob@example.com"} {
		if _, err := boss.SaveMember("lab", m, "member"); err != nil {
			t.Fatal(err)
		}
	}
	alice, bob := e.login("evo-alice"), e.login("evo-bob")
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
	opts := []model.DecisionOption{{Label: "A"}, {Label: "B"}}
	hidden, err := boss.AddDecision("lab", service.DecisionInput{Title: "機密待決定", Options: opts, SourceIssue: sec.Key})
	if err != nil {
		t.Fatal(err)
	}
	open, err := boss.AddDecision("lab", service.DecisionInput{Title: "組織層待決定", Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	// 非成員 alice
	if l, err := alice.ListDecisions("lab", service.DecisionListQuery{}); err != nil || len(l) != 1 || l[0].ID != open.ID {
		t.Fatal("GET decisions 不能帶出受限的", l, err)
	}
	if l, err := alice.ListDecisions("lab", service.DecisionListQuery{Issue: sec.Key}); err != nil || len(l) != 0 {
		t.Fatal("GET decisions?issue", l, err)
	}
	if sum, err := alice.DecisionSummaries("lab"); err != nil || len(sum) != 0 {
		t.Fatal("GET decisions/summary", sum, err)
	}
	_, miss := alice.GetDecision("lab", "DEC-99")
	same := func(what string, err error) {
		t.Helper()
		if status(err) != 404 || strings.ReplaceAll(err.Error(), hidden.ID, "DEC-99") != miss.Error() {
			t.Fatalf("%s:要和不存在一樣的 404:%v / %v", what, err, miss)
		}
		if strings.Contains(err.Error(), "SEC-1") {
			t.Fatalf("%s:不能帶出 Issue 編號:%v", what, err)
		}
	}
	_, err = alice.GetDecision("lab", hidden.ID)
	same("GET decisions/{id}", err)
	_, err = alice.AnswerDecision("lab", hidden.ID, service.DecisionAnswer{Answer: "o1"})
	same("POST answer", err)
	_, err = alice.WithdrawDecision("lab", hidden.ID)
	same("POST withdraw", err)
	_, err = alice.ReopenDecision("lab", hidden.ID)
	same("POST reopen", err)
	_, err = alice.DecisionToIssue("lab", hidden.ID, service.DecisionToIssueInput{})
	same("POST to-issue", err)
	if _, err := alice.AddDecision("lab", service.DecisionInput{Title: "x", Options: opts, SourceIssue: sec.Key}); status(err) != 400 {
		t.Fatal("以看不到的 Issue 當來源 400", err)
	}
	// 成員 bob
	if l, err := bob.ListDecisions("lab", service.DecisionListQuery{}); err != nil || len(l) != 2 {
		t.Fatal(l, err)
	}
	if g, err := bob.GetDecision("lab", hidden.ID); err != nil || g.SourceIssue != sec.Key {
		t.Fatal(g, err)
	}
	if sum, _ := bob.DecisionSummaries("lab"); sum[sec.Key].Open != 1 {
		t.Fatal(sum)
	}
	if _, err := bob.AnswerDecision("lab", hidden.ID, service.DecisionAnswer{Answer: "o1"}); err != nil {
		t.Fatal("成員可以處理", err)
	}
}
