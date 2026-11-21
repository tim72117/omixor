package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// ISS-34 第 3 階段:待決定 / 需要協助的受限可見性。有來源 Issue 的決定繼承該 Issue 的可見性(受限專案只有成員、組織管理員看得到),
// 沒有來源視為組織層。看不到的決定:單筆回和不存在一樣的 404、列表與摘要沒有、不能處理 / 撤回 / 重開 / 開 Issue。

type dEnv struct {
	*rEnv
	secOpen    *model.Decision // 來源 SEC-1(受限),開放中
	secDecided *model.Decision // 來源 SEC-1(受限),boss 已處理
	appDec     *model.Decision // 來源 APP-1(不受限)
	orgDec     *model.Decision // 沒有來源
}

func newDEnv(t *testing.T) *dEnv {
	t.Helper()
	e := &dEnv{rEnv: newREnv(t)}
	add := func(title, src string) *model.Decision {
		d, err := e.svc.AddDecision(e.boss, "o1", service.DecisionInput{Title: title, Options: twoOpts(), SourceIssue: src})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	e.secOpen = add("機密待決定", e.secIssue.ID)
	e.secDecided = add("機密已處理", "SEC-1")
	if _, err := e.svc.AnswerDecision(e.boss, "o1", e.secDecided.ID, service.DecisionAnswer{Answer: "o1"}); err != nil {
		t.Fatal(err)
	}
	e.appDec = add("公開待決定", e.appIssue.ID)
	e.orgDec = add("組織層待決定", "")
	return e
}

func decIDs(l []model.Decision) string {
	out := []string{}
	for _, d := range l {
		out = append(out, d.ID)
	}
	return strings.Join(out, ",")
}

// 非成員(一般成員 alice、沒加進專案的角色 pm)看不到 SEC 來源的決定;專案成員、管理員、專案管理員、專案成員角色看得到
func TestDecisionRestrictedVisibility(t *testing.T) {
	e := newDEnv(t)
	for name, p := range map[string]*model.Principal{"alice": e.alice, "pm": e.pm} {
		l, err := e.svc.ListDecisions(p, "o1", service.DecisionListQuery{})
		if err != nil || decIDs(l) != "DEC-3,DEC-4" {
			t.Fatalf("%s 列表不能有受限專案的決定:%s %v", name, decIDs(l), err)
		}
		if l, _ := e.svc.ListDecisions(p, "o1", service.DecisionListQuery{Status: "decided"}); len(l) != 0 {
			t.Fatalf("%s 看不到已處理的受限決定:%s", name, decIDs(l))
		}
		if l, _ := e.svc.ListDecisions(p, "o1", service.DecisionListQuery{Issue: "SEC-1"}); len(l) != 0 {
			t.Fatalf("%s 依來源 Issue 篩選也不能帶出:%s", name, decIDs(l))
		}
		if l, _ := e.svc.ListDecisions(p, "o1", service.DecisionListQuery{Issue: e.secIssue.ID}); len(l) != 0 {
			t.Fatalf("%s 依內部 id 篩選也不能帶出:%s", name, decIDs(l))
		}
		sum, err := e.svc.DecisionSummaries(p, "o1")
		if err != nil || len(sum) != 1 || sum["APP-1"] != (model.DecisionSummary{Open: 1}) {
			t.Fatalf("%s 摘要不能有 SEC-1:%v %v", name, sum, err)
		}
		_, miss := e.svc.GetDecision(p, "o1", "DEC-99")
		for _, id := range []string{e.secOpen.ID, e.secDecided.ID, "dec-1"} {
			d, err := e.svc.GetDecision(p, "o1", id)
			if d != nil || service.Code(err) != 404 || stripRef(err, id, strings.ToUpper(id)) != stripRef(miss, "DEC-99") {
				t.Fatalf("%s 取 %s 要和不存在一樣的 404:%v / %v", name, id, err, miss)
			}
		}
		if _, err := e.svc.GetDecision(p, "o1", e.appDec.ID); err != nil {
			t.Fatalf("%s 看得到公開的:%v", name, err)
		}
		if _, err := e.svc.GetDecision(p, "o1", e.orgDec.ID); err != nil {
			t.Fatalf("%s 看得到組織層的:%v", name, err)
		}
	}
	// 待我處理(mine)也不帶出
	if l, _ := e.svc.ListDecisions(e.alice, "o1", service.DecisionListQuery{Mine: true}); decIDs(l) != "DEC-3,DEC-4" {
		t.Fatal("mine 不能帶出受限的:", decIDs(l))
	}
	// 看得到的人:專案成員、專案管理員、組織管理員、專案成員的角色
	for name, p := range map[string]*model.Principal{"bob": e.bob, "carol": e.carol, "boss": e.boss, "cto": e.cto} {
		l, err := e.svc.ListDecisions(p, "o1", service.DecisionListQuery{})
		if err != nil || decIDs(l) != "DEC-1,DEC-2,DEC-3,DEC-4" {
			t.Fatalf("%s 看得到全部:%s %v", name, decIDs(l), err)
		}
		d, err := e.svc.GetDecision(p, "o1", e.secOpen.ID)
		if err != nil || d.SourceIssue != "SEC-1" {
			t.Fatalf("%s 看得到受限的單筆:%v %v", name, d, err)
		}
		sum, _ := e.svc.DecisionSummaries(p, "o1")
		if sum["SEC-1"] != (model.DecisionSummary{Open: 1, Decided: 1}) {
			t.Fatalf("%s 摘要有 SEC-1:%v", name, sum)
		}
	}
	// 外部組織:403(組織都進不去)
	if _, err := e.svc.GetDecision(e.eve, "o1", e.secOpen.ID); service.Code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := e.svc.GetDecision(e.xa, "o1", e.secOpen.ID); service.Code(err) != 403 {
		t.Fatal(err)
	}
}

// 看不到的決定不能被處理、撤回、重開、開 Issue(和不存在一樣 404),也不會留下任何副作用
func TestDecisionRestrictedWrites(t *testing.T) {
	e := newDEnv(t)
	_, miss := e.svc.GetDecision(e.alice, "o1", "DEC-99")
	same := func(what string, err error, id string) {
		t.Helper()
		if service.Code(err) != 404 || stripRef(err, id) != stripRef(miss, "DEC-99") {
			t.Fatalf("%s 要和不存在一樣的 404:%v / %v", what, err, miss)
		}
	}
	_, err := e.svc.AnswerDecision(e.alice, "o1", e.secOpen.ID, service.DecisionAnswer{Answer: "o1"})
	same("回答", err, e.secOpen.ID)
	_, err = e.svc.WithdrawDecision(e.alice, "o1", e.secOpen.ID)
	same("撤回", err, e.secOpen.ID)
	_, err = e.svc.ReopenDecision(e.alice, "o1", e.secDecided.ID)
	same("重開", err, e.secDecided.ID)
	_, _, err = e.svc.DecisionToIssue(e.alice, "o1", e.secOpen.ID, service.DecisionToIssueInput{})
	same("開 Issue", err, e.secOpen.ID)
	// 角色憑證 pm(非專案成員)撤回:先過視為存在的檢查,看不到就是 404
	_, err = e.svc.WithdrawDecision(e.pm, "o1", e.secOpen.ID)
	same("pm 撤回", err, e.secOpen.ID)
	if d, _ := e.svc.GetDecision(e.boss, "o1", e.secOpen.ID); d.Status != "open" {
		t.Fatal("看不到的人不能改狀態", d)
	}
	if g, _ := e.svc.GetDecision(e.boss, "o1", e.secDecided.ID); g.Status != "decided" {
		t.Fatal(g)
	}
	// 專案成員 bob 可以處理(來源 Issue 留言在受限專案裡,不外洩)
	if _, err := e.svc.AnswerDecision(e.bob, "o1", e.secOpen.ID, service.DecisionAnswer{Answer: "b"}); err != nil {
		t.Fatal("專案成員可以處理", err)
	}
	// 專案成員的角色(cto)提出受限來源的決定可以;pm(非成員)和來源不存在一樣 400
	if _, err := e.svc.AddDecision(e.cto, "o1", service.DecisionInput{Kind: "help", Title: "機密協助", SourceIssue: "SEC-1"}); err != nil {
		t.Fatal("專案成員的角色可以提出", err)
	}
	_, missSrc := e.svc.AddDecision(e.alice, "o1", service.DecisionInput{Title: "x", Options: twoOpts(), SourceIssue: "ISS-99"})
	for name, p := range map[string]*model.Principal{"alice": e.alice, "pm": e.pm} {
		_, err := e.svc.AddDecision(p, "o1", service.DecisionInput{Title: "x", Options: twoOpts(), SourceIssue: "SEC-1"})
		if service.Code(err) != 400 || stripRef(err, "SEC-1") != stripRef(missSrc, "ISS-99") {
			t.Fatalf("%s 以看不到的 Issue 當來源要和不存在一樣的 400:%v / %v", name, err, missSrc)
		}
		if l, _ := e.svc.ListDecisions(e.boss, "o1", service.DecisionListQuery{}); strings.Contains(decIDs(l), "DEC-6") && name == "alice" {
			t.Fatal("被拒的不能留下決定")
		}
	}
	// 由決定開 Issue 指定受限專案:非成員和不存在的專案一樣 404;成員可以,之後決定繼承新 Issue 的可見性
	_, _, err = e.svc.DecisionToIssue(e.alice, "o1", e.orgDec.ID, service.DecisionToIssueInput{Project: sp(e.sec.ID)})
	_, _, missPrj := e.svc.DecisionToIssue(e.alice, "o1", e.orgDec.ID, service.DecisionToIssueInput{Project: sp("PRJ-99")})
	if service.Code(err) != 404 || stripRef(err, e.sec.ID) != stripRef(missPrj, "PRJ-99") {
		t.Fatalf("指定看不到的專案要和不存在的專案一樣 404:%v / %v", err, missPrj)
	}
	if g, _ := e.svc.GetDecision(e.alice, "o1", e.orgDec.ID); g.SourceIssue != "" {
		t.Fatal("失敗不能改指向", g)
	}
	i, d, err := e.svc.DecisionToIssue(e.bob, "o1", e.orgDec.ID, service.DecisionToIssueInput{Project: sp(e.sec.ID)})
	if err != nil || !strings.HasPrefix(i.Key, "SEC-") || d.SourceIssue != i.Key {
		t.Fatalf("成員可以在受限專案開 Issue:%+v %+v %v", i, d, err)
	}
	if _, err := e.svc.GetDecision(e.alice, "o1", e.orgDec.ID); service.Code(err) != 404 {
		t.Fatal("決定繼承新 Issue 的可見性,非成員看不到:", err)
	}
	if _, err := e.svc.GetDecision(e.bob, "o1", e.orgDec.ID); err != nil {
		t.Fatal("成員看得到", err)
	}
}

// 來源 Issue 搬進受限專案後,決定跟著變成看不到(繼承;不是建立當時的快照)
func TestDecisionVisibilityFollowsIssueMove(t *testing.T) {
	e := newDEnv(t)
	if _, err := e.svc.GetDecision(e.alice, "o1", e.appDec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateIssue(e.boss, "o1", e.appIssue.ID, service.IssuePatch{Project: sp(e.sec.ID)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetDecision(e.alice, "o1", e.appDec.ID); service.Code(err) != 404 {
		t.Fatal("來源 Issue 搬進受限專案,決定要看不到:", err)
	}
	if l, _ := e.svc.ListDecisions(e.alice, "o1", service.DecisionListQuery{}); decIDs(l) != "DEC-4" {
		t.Fatal(decIDs(l))
	}
	if sum, _ := e.svc.DecisionSummaries(e.alice, "o1"); len(sum) != 0 {
		t.Fatal(sum)
	}
	// 用舊 key 當篩選條件也看不到
	if l, _ := e.svc.ListDecisions(e.alice, "o1", service.DecisionListQuery{Issue: "APP-1"}); len(l) != 0 {
		t.Fatal("舊 key 篩選不能帶出", decIDs(l))
	}
	if d, err := e.svc.GetDecision(e.bob, "o1", e.appDec.ID); err != nil || d.SourceIssue == "" || strings.HasPrefix(d.SourceIssue, "APP-") {
		t.Fatalf("成員看得到,來源是新 key:%+v %v", d, err)
	}
}
