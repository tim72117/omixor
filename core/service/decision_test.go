package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

func twoOpts() []model.DecisionOption {
	return []model.DecisionOption{{Label: "方案 A", Recommended: true}, {ID: "b", Label: "方案 B", Description: "另一條路"}}
}

// decRole 角色憑證換來的身分(Principal.Actor 沒填,由 SelfActor 依角色補)
func decRole(org, id string) *model.Principal {
	return &model.Principal{Agent: &model.AgentIdentity{Org: org, AgentID: id, TaskID: "T-1"}}
}

func decStranger() *model.Principal {
	return &model.Principal{User: model.User{ID: "u9", Email: "x@y", Role: "member"}}
}

// 提出:兩種 kind 共用規則、decision 要 2 個以上選項並自動編 id、help 不能帶選項、來源 Issue 要存在、
// 非成員 / 外部組織 403、角色憑證可以提出;署名是呼叫者的 actor(輸出相容字串 + actor 物件),不接受指定
func TestDecisionAdd(t *testing.T) {
	svc, st, _, alice, _ := chainEnv(t)
	iss, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "來源 Issue"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddDecision(decStranger(), "o1", service.DecisionInput{Title: "x", Options: twoOpts()}); code(err) != 403 {
		t.Fatal("不是成員 403", err)
	}
	if _, err := svc.AddDecision(decRole("o2", "cto"), "o1", service.DecisionInput{Title: "x", Options: twoOpts()}); code(err) != 403 {
		t.Fatal("別的組織的角色憑證 403", err)
	}
	for name, in := range map[string]service.DecisionInput{
		"沒標題":           {Title: " ", Options: twoOpts()},
		"decision 一個選項": {Title: "x", Options: twoOpts()[:1]},
		"選項沒 label":     {Title: "x", Options: []model.DecisionOption{{Label: "a"}, {Label: " "}}},
		"選項 id 重複":      {Title: "x", Options: []model.DecisionOption{{ID: "a", Label: "a"}, {ID: "A", Label: "b"}}},
		"選項 id 是 other": {Title: "x", Options: []model.DecisionOption{{ID: "other", Label: "a"}, {Label: "b"}}},
		"不認得的 kind":     {Kind: "ask", Title: "x", Options: twoOpts()},
		"help 帶選項":      {Kind: "help", Title: "x", Options: twoOpts()},
		"來源 Issue 不存在":  {Title: "x", Options: twoOpts(), SourceIssue: "ISS-99"},
	} {
		if _, err := svc.AddDecision(alice, "o1", in); code(err) != 400 {
			t.Fatalf("%s 要 400:%v", name, err)
		}
	}
	d, err := svc.AddDecision(alice, "o1", service.DecisionInput{Title: " 用哪個? ", Body: "背景", Options: twoOpts(), SourceIssue: "iss-1", Blocks: []string{"ISS-1", " ", "ISS-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "DEC-1" || d.Kind != "decision" || d.Title != "用哪個?" || d.Status != "open" || d.SourceIssue != iss.Key || !strings.EqualFold(d.CreatedBy, "alice@x") ||
		d.Options[0].ID != "o1" || !d.Options[0].Recommended || d.Options[1].ID != "b" || strings.Join(d.Blocks, ",") != "ISS-1" || d.DecidedBy != "" {
		t.Fatalf("%+v", d)
	}
	if d.CreatedByActor == nil || d.CreatedByActor.Kind != model.ActorKindUser || !strings.EqualFold(d.CreatedByActor.Email, "alice@x") || d.DecidedByActor != nil {
		t.Fatalf("要有 createdByActor(沒處理沒有 decidedByActor):%+v", d)
	}
	h, err := svc.AddDecision(decRole("o1", "cto"), "o1", service.DecisionInput{Kind: "help", Title: "請備份 Cloud SQL", Body: "上線前"})
	if err != nil {
		t.Fatal(err)
	}
	if h.ID != "DEC-2" || h.Kind != "help" || len(h.Options) != 0 || h.Options == nil || h.CreatedBy != "agent:cto" ||
		h.CreatedByActor == nil || h.CreatedByActor.Kind != model.ActorKindAgent || h.CreatedByActor.Handle != "cto" {
		t.Fatalf("角色憑證可以提出 help:%+v", h)
	}
	aud, _ := st.ListAudit(10)
	found := false
	for _, e := range aud {
		found = found || e.Action == "decision.add"
	}
	if !found {
		t.Fatal("要有稽核 decision.add", aud)
	}
}

// 回答:成員皆可;選項 id 或 other+note;不認得的 400;已回答不能再改(409);角色憑證 403;
// 結論寫成來源 Issue 的系統留言(署名處理人的 actor);reopen 只有管理員或原處理人,reopen 後可再回答;處理人是 actor
func TestDecisionAnswer(t *testing.T) {
	svc, st, boss, alice, bob := chainEnv(t)
	iss, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "來源"})
	d, _ := svc.AddDecision(alice, "o1", service.DecisionInput{Title: "用哪個?", Options: twoOpts(), SourceIssue: iss.ID})
	role := decRole("o1", "cto")
	if _, err := svc.AnswerDecision(role, "o1", d.ID, service.DecisionAnswer{Answer: "o1"}); code(err) != 403 {
		t.Fatal("角色憑證不能回答", err)
	}
	if _, err := svc.AnswerDecision(decStranger(), "o1", d.ID, service.DecisionAnswer{Answer: "o1"}); code(err) != 403 {
		t.Fatal("非成員 403", err)
	}
	if _, err := svc.AnswerDecision(bob, "o1", "DEC-99", service.DecisionAnswer{Answer: "o1"}); code(err) != 404 {
		t.Fatal(err)
	}
	if _, err := svc.AnswerDecision(bob, "o1", d.ID, service.DecisionAnswer{Answer: "zzz"}); code(err) != 400 {
		t.Fatal("不認得的選項 400", err)
	}
	if _, err := svc.AnswerDecision(bob, "o1", d.ID, service.DecisionAnswer{Answer: "other"}); code(err) != 400 {
		t.Fatal("other 要附說明", err)
	}
	if _, err := svc.AnswerDecision(bob, "o1", d.ID, service.DecisionAnswer{Answer: "o1", Evidence: "x"}); code(err) != 400 {
		t.Fatal("evidence 只有 help 用", err)
	}
	got, err := svc.AnswerDecision(bob, "o1", "dec-1", service.DecisionAnswer{Answer: "B", Note: "比較穩"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "decided" || got.Answer != "b" || got.AnswerNote != "比較穩" || got.DecidedBy != "bob@x" || got.DecidedAt == nil ||
		got.DecidedByActor == nil || got.DecidedByActor.Email != "bob@x" || got.CreatedBy == "" {
		t.Fatalf("%+v", got)
	}
	if _, err := svc.AnswerDecision(boss, "o1", d.ID, service.DecisionAnswer{Answer: "o1"}); code(err) != 409 {
		t.Fatal("已回答的不能再改", err)
	}
	// 來源 Issue 的系統留言(署名處理人的 actor)
	i, _ := svc.GetIssue(alice, "o1", iss.ID)
	if len(i.Comments) != 1 || i.Comments[0].Author != "bob@x" || i.Comments[0].AuthorActor == nil || i.Comments[0].AuthorActor.Email != "bob@x" ||
		!strings.Contains(i.Comments[0].Text, "DEC-1") || !strings.Contains(i.Comments[0].Text, "方案 B") || !strings.Contains(i.Comments[0].Text, "比較穩") {
		t.Fatalf("來源 Issue 要有結論留言:%+v", i.Comments)
	}
	// reopen:角色 403;不是管理員也不是原處理人 403;原處理人與管理員可以
	if _, err := svc.ReopenDecision(role, "o1", d.ID); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.ReopenDecision(alice, "o1", d.ID); code(err) != 403 {
		t.Fatal("非管理員、非原處理人不能 reopen", err)
	}
	if _, err := svc.ReopenDecision(bob, "o1", d.ID); err != nil {
		t.Fatal("原處理人可以 reopen", err)
	}
	if _, err := svc.ReopenDecision(boss, "o1", d.ID); code(err) != 409 {
		t.Fatal("開放中的不用 reopen", err)
	}
	g, _ := svc.GetDecision(alice, "o1", d.ID)
	if g.Status != "open" || g.Answer != "" || g.DecidedBy != "" || g.DecidedAt != nil || g.DecidedByActor != nil {
		t.Fatalf("reopen 要清掉回答:%+v", g)
	}
	if _, err := svc.AnswerDecision(alice, "o1", d.ID, service.DecisionAnswer{Answer: "other", Note: "都不要"}); err != nil {
		t.Fatal("reopen 後可以再回答", err)
	}
	if _, err := svc.ReopenDecision(boss, "o1", d.ID); err != nil {
		t.Fatal("管理員可以 reopen", err)
	}
	i, _ = svc.GetIssue(alice, "o1", iss.ID)
	if len(i.Comments) != 4 || !strings.Contains(i.Comments[1].Text, "重新開啟") || !strings.Contains(i.Comments[2].Text, "其他") || i.Comments[3].Author != "boss@x" {
		t.Fatalf("%+v", i.Comments)
	}
	actions := map[string]int{}
	aud, _ := st.ListAudit(50)
	for _, e := range aud {
		actions[e.Action]++
	}
	if actions["decision.answer"] != 2 || actions["decision.reopen"] != 2 {
		t.Fatalf("稽核:%v", actions)
	}
}

// help 的處理流程:done(附備註與證據)、declined(要原因、不留證據);help 不接受選項 id
func TestHelpFlow(t *testing.T) {
	svc, _, _, alice, bob := chainEnv(t)
	role := decRole("o1", "cto")
	h1, _ := svc.AddDecision(role, "o1", service.DecisionInput{Kind: "help", Title: "Mac 上編譯"})
	h2, _ := svc.AddDecision(role, "o1", service.DecisionInput{Kind: "help", Title: "給憑證"})
	if _, err := svc.AnswerDecision(role, "o1", h1.ID, service.DecisionAnswer{Answer: "done"}); code(err) != 403 {
		t.Fatal("角色憑證不能處理", err)
	}
	if _, err := svc.AnswerDecision(alice, "o1", h1.ID, service.DecisionAnswer{Answer: "o1"}); code(err) != 400 {
		t.Fatal("help 只接受 done / declined", err)
	}
	if _, err := svc.AnswerDecision(alice, "o1", h2.ID, service.DecisionAnswer{Answer: "declined"}); code(err) != 400 {
		t.Fatal("declined 要附原因", err)
	}
	got, err := svc.AnswerDecision(alice, "o1", h1.ID, service.DecisionAnswer{Answer: "done", Note: "編好了", Evidence: " build #42 "})
	if err != nil || got.Status != "decided" || got.Answer != "done" || got.Evidence != "build #42" || got.Kind != "help" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = svc.AnswerDecision(bob, "o1", h2.ID, service.DecisionAnswer{Answer: "declined", Note: "不給", Evidence: "忽略"})
	if err != nil || got.Answer != "declined" || got.AnswerNote != "不給" || got.Evidence != "" {
		t.Fatalf("declined 不留證據:%+v %v", got, err)
	}
}

// 列表篩選:狀態、kind、來源 Issue(任何編號寫法)、待我處理(open、兩種都含;角色憑證沒有「我」);
// withdraw:建立者或管理員,角色憑證只能撤回自己提的,已處理的不能撤回;摘要以 key 為鍵
func TestDecisionListAndWithdraw(t *testing.T) {
	svc, _, boss, alice, bob := chainEnv(t)
	iss, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "來源"})
	role := decRole("o1", "cto")
	d1, _ := svc.AddDecision(alice, "o1", service.DecisionInput{Title: "d1", Options: twoOpts(), SourceIssue: iss.ID})
	h1, _ := svc.AddDecision(role, "o1", service.DecisionInput{Kind: "help", Title: "h1", SourceIssue: iss.Key})
	d2, _ := svc.AddDecision(bob, "o1", service.DecisionInput{Title: "d2", Options: twoOpts()})
	h2, _ := svc.AddDecision(role, "o1", service.DecisionInput{Kind: "help", Title: "h2"})
	ids := func(q service.DecisionListQuery, p *model.Principal) string {
		t.Helper()
		l, err := svc.ListDecisions(p, "o1", q)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, d := range l {
			out = append(out, d.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(service.DecisionListQuery{}, alice); got != "DEC-1,DEC-2,DEC-3,DEC-4" {
		t.Fatal(got)
	}
	if got := ids(service.DecisionListQuery{Kind: "help"}, role); got != "DEC-2,DEC-4" {
		t.Fatal("角色憑證可以讀", got)
	}
	for _, ref := range []string{"iss-1", iss.Key, iss.ID} {
		if got := ids(service.DecisionListQuery{Issue: ref}, alice); got != "DEC-1,DEC-2" {
			t.Fatal("依來源 Issue", ref, got)
		}
	}
	if got := ids(service.DecisionListQuery{Issue: "ISS-99"}, alice); got != "" {
		t.Fatal("沒有這張 Issue 是空", got)
	}
	if _, err := svc.AnswerDecision(boss, "o1", d1.ID, service.DecisionAnswer{Answer: "o1"}); err != nil {
		t.Fatal(err)
	}
	if got := ids(service.DecisionListQuery{Mine: true}, alice); got != "DEC-2,DEC-3,DEC-4" {
		t.Fatal("待我處理 = open、兩種都含", got)
	}
	if got := ids(service.DecisionListQuery{Mine: true, Kind: "decision"}, alice); got != "DEC-3" {
		t.Fatal(got)
	}
	if got := ids(service.DecisionListQuery{Mine: true}, role); got != "" {
		t.Fatal("角色憑證沒有「待我處理」", got)
	}
	if got := ids(service.DecisionListQuery{Status: "decided"}, alice); got != "DEC-1" {
		t.Fatal(got)
	}
	if _, err := svc.ListDecisions(alice, "o1", service.DecisionListQuery{Status: "x"}); code(err) != 400 {
		t.Fatal(err)
	}
	if _, err := svc.ListDecisions(decStranger(), "o1", service.DecisionListQuery{}); code(err) != 403 {
		t.Fatal(err)
	}
	// 摘要
	sum, err := svc.DecisionSummaries(alice, "o1")
	if err != nil || sum[iss.Key] != (model.DecisionSummary{Open: 1, Decided: 1}) || len(sum) != 1 {
		t.Fatal(sum, err)
	}
	if _, err := svc.DecisionSummaries(decStranger(), "o1"); code(err) != 403 {
		t.Fatal(err)
	}
	// 撤回
	if _, err := svc.WithdrawDecision(bob, "o1", d2.ID); err != nil {
		t.Fatal("建立者可以撤回", err)
	}
	if _, err := svc.WithdrawDecision(alice, "o1", h2.ID); code(err) != 403 {
		t.Fatal("非建立者、非管理員不能撤回", err)
	}
	if _, err := svc.WithdrawDecision(decRole("o1", "pm"), "o1", h2.ID); code(err) != 403 {
		t.Fatal("角色憑證只能撤回自己提的", err)
	}
	if _, err := svc.WithdrawDecision(role, "o1", h2.ID); err != nil {
		t.Fatal("角色憑證可以撤回自己提的", err)
	}
	if _, err := svc.WithdrawDecision(boss, "o1", h1.ID); err != nil {
		t.Fatal("管理員可以撤回", err)
	}
	if _, err := svc.WithdrawDecision(boss, "o1", d1.ID); code(err) != 409 {
		t.Fatal("已處理的不能撤回", err)
	}
	if _, err := svc.AnswerDecision(boss, "o1", d2.ID, service.DecisionAnswer{Answer: "o1"}); code(err) != 409 {
		t.Fatal("已撤回的不能回答", err)
	}
	if _, err := svc.WithdrawDecision(decStranger(), "o1", d2.ID); code(err) != 403 {
		t.Fatal(err)
	}
}

// 由決定開出問題 Issue:標題 = 問題、內文含背景與選項、source = DEC-n、決定指向新 Issue(輸出是 key);已有來源 409;兩種 kind 都可以;
// 角色憑證不行;可指定專案
func TestDecisionToIssue(t *testing.T) {
	svc, _, boss, alice, bob := chainEnv(t)
	d, _ := svc.AddDecision(alice, "o1", service.DecisionInput{Title: "要不要拆表?", Body: "背景說明", Options: twoOpts()})
	res := func(p *model.Principal, id string) (*model.Issue, *model.Decision, error) {
		return svc.DecisionToIssue(p, "o1", id, service.DecisionToIssueInput{})
	}
	if _, _, err := res(decStranger(), d.ID); code(err) != 403 {
		t.Fatal(err)
	}
	if _, _, err := res(decRole("o1", "cto"), d.ID); code(err) != 403 {
		t.Fatal("角色憑證不能開", err)
	}
	if _, _, err := res(bob, "DEC-9"); code(err) != 404 {
		t.Fatal(err)
	}
	i, dd, err := res(bob, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if i.Title != "要不要拆表?" || i.Source != "DEC-1" || i.Status != "open" || strings.Join(i.Labels, ",") != "待決定" ||
		!strings.Contains(i.Body, "背景說明") || !strings.Contains(i.Body, "方案 A") || !strings.Contains(i.Body, "(建議)") || !strings.Contains(i.Body, "另一條路") {
		t.Fatalf("%+v", i)
	}
	if dd.SourceIssue != i.Key {
		t.Fatalf("決定要指向新 Issue:%+v", dd)
	}
	if _, _, err := res(bob, d.ID); code(err) != 409 {
		t.Fatal("已經有來源 Issue 不重複開", err)
	}
	// 回答後結論留在新 Issue
	if _, err := svc.AnswerDecision(alice, "o1", d.ID, service.DecisionAnswer{Answer: "b"}); err != nil {
		t.Fatal(err)
	}
	if g, _ := svc.GetIssue(alice, "o1", i.ID); len(g.Comments) != 1 {
		t.Fatalf("%+v", g.Comments)
	}
	// help 也可以(角色憑證提的,人來開)
	h, _ := svc.AddDecision(decRole("o1", "cto"), "o1", service.DecisionInput{Kind: "help", Title: "請備份"})
	hi, hd, err := res(alice, h.ID)
	if err != nil || strings.Join(hi.Labels, ",") != "需要協助" || hd.SourceIssue != hi.Key {
		t.Fatalf("%+v %+v %v", hi, hd, err)
	}
	// 撤回的不開
	w, _ := svc.AddDecision(alice, "o1", service.DecisionInput{Title: "w", Options: twoOpts()})
	_, _ = svc.WithdrawDecision(alice, "o1", w.ID)
	if _, _, err := res(alice, w.ID); code(err) != 409 {
		t.Fatal(err)
	}
	// 指定專案、優先度與標籤
	prj, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "專案", Key: "APP"})
	if err != nil {
		t.Fatal(err)
	}
	d3, _ := svc.AddDecision(alice, "o1", service.DecisionInput{Title: "d3", Options: twoOpts()})
	pi, pd, err := svc.DecisionToIssue(alice, "o1", d3.ID, service.DecisionToIssueInput{Project: sp(prj.ID), Priority: "high", Labels: []string{"x"}})
	if err != nil || pi.Project != prj.ID || !strings.HasPrefix(pi.Key, "APP-") || pi.Priority != "high" || strings.Join(pi.Labels, ",") != "x" || pd.SourceIssue != pi.Key {
		t.Fatalf("%+v %+v %v", pi, pd, err)
	}
	d4, _ := svc.AddDecision(alice, "o1", service.DecisionInput{Title: "d4", Options: twoOpts()})
	if _, _, err := svc.DecisionToIssue(alice, "o1", d4.ID, service.DecisionToIssueInput{Project: sp("PRJ-99")}); code(err) != 404 {
		t.Fatal("專案不存在 404", err)
	}
	if g, _ := svc.GetDecision(alice, "o1", d4.ID); g.SourceIssue != "" {
		t.Fatal("開 Issue 失敗不改指向", g)
	}
}

// 來源 Issue 永久刪除:決定還在,sourceIssue 清空(回答時不會因為留言失敗而出錯);丟進垃圾桶:決定仍指向它
func TestDecisionSourceIssueDeleted(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	iss, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "來源"})
	d, _ := svc.AddDecision(alice, "o1", service.DecisionInput{Title: "x", Options: twoOpts(), SourceIssue: iss.ID})
	if err := svc.DeleteIssue(boss, "o1", iss.ID); err != nil { // 進垃圾桶
		t.Fatal(err)
	}
	g, err := svc.GetDecision(alice, "o1", d.ID)
	if err != nil || g.SourceIssue != iss.Key {
		t.Fatalf("垃圾桶裡的 Issue 決定仍指向它:%+v %v", g, err)
	}
	if err := svc.PurgeIssue(boss, "o1", iss.ID); err != nil {
		t.Fatal(err)
	}
	g, err = svc.GetDecision(alice, "o1", d.ID)
	if err != nil || g.SourceIssue != "" {
		t.Fatalf("%+v %v", g, err)
	}
	if _, err := svc.AnswerDecision(alice, "o1", d.ID, service.DecisionAnswer{Answer: "b"}); err != nil {
		t.Fatal(err)
	}
}

// 同時處理只有一個成功(store 的條件式更新):另一個是 409
func TestDecisionAnswerStale(t *testing.T) {
	svc, st, _, alice, bob := chainEnv(t)
	d, _ := svc.AddDecision(alice, "o1", service.DecisionInput{Title: "x", Options: twoOpts()})
	raw, _ := st.GetDecision("o1", d.ID)
	if _, err := svc.AnswerDecision(bob, "o1", d.ID, service.DecisionAnswer{Answer: "b"}); err != nil {
		t.Fatal(err)
	}
	raw.Status = model.DecisionDecided // 模擬另一個實例拿著舊的 open 資料來寫
	if err := st.UpdateDecision("o1", *raw, model.DecisionOpen); err != store.ErrStale {
		t.Fatal("狀態已經不是 open 要 ErrStale:", err)
	}
}

// 署名不接受指定:輸入結構沒有 author;伺服器管理員提出的也是自己
func TestDecisionAuthorIsSelf(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	d, err := svc.AddDecision(boss, "o1", service.DecisionInput{Title: "x", Options: twoOpts()})
	if err != nil || d.CreatedBy != "boss@x" || d.CreatedByActor == nil || d.CreatedByActor.Email != "boss@x" {
		t.Fatalf("%+v %v", d, err)
	}
}
