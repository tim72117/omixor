package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// Issue 子項(ISS-37)的 service 測試:新增、依賴與環、領取競爭、輕量領取與角色領取綁任務並回寫、blocked 與 unblock、
// promote、取消與刪除、關閉 Issue 時的計數、受限專案可見性、角色憑證權限。

func addItem(t *testing.T, e *rEnv, p *model.Principal, issue string, in service.ItemInput) *model.IssueItem {
	t.Helper()
	it, err := e.svc.AddItem(p, "o1", issue, in)
	if err != nil {
		t.Fatalf("AddItem %q:%v", in.Title, err)
	}
	return it
}

func wantCode(t *testing.T, what string, err error, code int) {
	t.Helper()
	if err == nil || service.Code(err) != code {
		t.Fatalf("%s:要 %d,實際 %v", what, code, err)
	}
}

func TestIssueItemLifecycleAndDependencies(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	a := addItem(t, e, e.boss, is, service.ItemInput{Title: "第一階段", Files: []string{"core/model/x.go", "core/model/x.go", " "}, Acceptance: "測試通過"})
	if a.Key != e.appIssue.Key+".1" || a.Seq != 1 || a.Status != model.ItemTodo || len(a.Files) != 1 || a.CreatedBy != "boss@x" || a.CreatedByActor == nil {
		t.Fatalf("新增:%+v", a)
	}
	b := addItem(t, e, e.boss, e.appIssue.Key, service.ItemInput{Title: "第二階段", DependsOn: "1"})
	if b.Key != e.appIssue.Key+".2" || b.DependsOn != a.Key {
		t.Fatalf("依賴要輸出成前置的 key:%+v", b)
	}
	// 依賴:不存在、自己、環
	_, err := e.svc.AddItem(e.boss, "o1", is, service.ItemInput{Title: "壞依賴", DependsOn: "ISS-999.9"})
	wantCode(t, "不存在的前置", err, 400)
	_, err = e.svc.UpdateItem(e.boss, "o1", is, a.Key, service.ItemPatch{DependsOn: &b.Key})
	wantCode(t, "成環", err, 400)
	_, err = e.svc.UpdateItem(e.boss, "o1", is, b.Key, service.ItemPatch{DependsOn: &b.Key})
	wantCode(t, "依賴自己", err, 400)
	// 前置未完成:領取與完成都是 409
	_, err = e.svc.ClaimItem(e.boss, "o1", is, b.Key, service.ItemClaimInput{})
	wantCode(t, "前置未完成不能領", err, 409)
	_, err = e.svc.ReportItem(e.boss, "o1", is, b.Key, service.ItemReport{Status: "done"})
	wantCode(t, "前置未完成不能完成", err, 409)
	// 領取競爭:boss 領走,alice 與再領都 409(連自己再領是續租,可以)
	r, err := e.svc.ClaimItem(e.boss, "o1", is, a.Key, service.ItemClaimInput{Note: "session A"})
	if err != nil || r.Item.Status != model.ItemDoing || r.Item.ClaimedBy != "boss@x" || r.Item.LockedUntil == nil || r.Task != nil || r.Credential != nil {
		t.Fatalf("輕量領取:%+v %v", r, err)
	}
	_, err = e.svc.ClaimItem(e.alice, "o1", is, a.Key, service.ItemClaimInput{})
	wantCode(t, "別人持有租約", err, 409)
	if _, err := e.svc.ReportItem(e.alice, "o1", is, a.Key, service.ItemReport{Status: "done"}); service.Code(err) != 403 {
		t.Fatalf("別人領取中,一般成員不能回報:%v", err)
	}
	// 領取者回報 done:帶證據字串與備註
	done, err := e.svc.ReportItem(e.boss, "o1", is, a.Key, service.ItemReport{Status: "done", Evidence: json.RawMessage(`"go test ./... 通過"`), Note: "沒驗證 Swift"})
	if err != nil || done.Status != model.ItemDone || done.CompletedAt == nil || done.ClaimedBy != "" {
		t.Fatalf("完成:%+v %v", done, err)
	}
	var ev map[string]string
	if err := json.Unmarshal(done.Evidence, &ev); err != nil || ev["summary"] != "go test ./... 通過" || ev["note"] != "沒驗證 Swift" {
		t.Fatalf("證據:%s", done.Evidence)
	}
	iss, _ := e.svc.GetIssue(e.boss, "o1", is)
	found := false
	for _, c := range iss.Comments {
		found = found || strings.Contains(c.Text, a.Key+"「第一階段」完成")
	}
	if !found {
		t.Fatalf("完成要在 Issue 留系統留言:%+v", iss.Comments)
	}
	// 完成後 b 可以領;已完成的不能再改、再領
	if _, err := e.svc.ClaimItem(e.alice, "o1", is, b.Key, service.ItemClaimInput{}); err != nil {
		t.Fatalf("前置完成後可領:%v", err)
	}
	_, err = e.svc.ClaimItem(e.boss, "o1", is, a.Key, service.ItemClaimInput{})
	wantCode(t, "已完成不能再領", err, 409)
	_, err = e.svc.UpdateItem(e.boss, "o1", is, a.Key, service.ItemPatch{Title: str("改")})
	wantCode(t, "已完成不能改", err, 409)
	// 列表:預設只回未完成
	l, err := e.svc.ListItems(e.boss, "o1", is, service.ItemListQuery{})
	if err != nil || len(l) != 1 || l[0].Key != b.Key {
		t.Fatalf("預設只回未完成:%+v %v", l, err)
	}
	l, _ = e.svc.ListItems(e.boss, "o1", is, service.ItemListQuery{IncludeDone: true})
	if len(l) != 2 || l[0].Seq != 1 {
		t.Fatalf("include done:%+v", l)
	}
	if l, _ = e.svc.ListItems(e.boss, "o1", is, service.ItemListQuery{Status: "done"}); len(l) != 1 {
		t.Fatalf("status 篩選:%+v", l)
	}
	if _, err := e.svc.ListItems(e.boss, "o1", is, service.ItemListQuery{Status: "x"}); service.Code(err) != 400 {
		t.Fatalf("status 不認得要 400:%v", err)
	}
	// 放棄領取:領取者本人可以,別人 403
	if _, err := e.svc.ReleaseItem(e.boss, "o1", is, b.Key); err != nil { // boss 是管理員
		t.Fatalf("管理員可放棄:%v", err)
	}
	if _, err := e.svc.ClaimItem(e.alice, "o1", is, b.Key, service.ItemClaimInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReleaseItem(e.bob, "o1", is, b.Key); service.Code(err) != 403 {
		t.Fatalf("非領取者不能放棄:%v", err)
	}
	if got, err := e.svc.ReleaseItem(e.alice, "o1", is, b.Key); err != nil || got.Status != model.ItemTodo || got.ClaimedBy != "" {
		t.Fatalf("放棄:%+v %v", got, err)
	}
}

func TestIssueItemClaimRaceOnlyOneWins(t *testing.T) {
	e := newREnv(t)
	it := addItem(t, e, e.boss, e.appIssue.ID, service.ItemInput{Title: "搶"})
	type res struct{ err error }
	ch := make(chan res, 3)
	for _, p := range []*model.Principal{e.boss, e.alice, e.bob} {
		go func(p *model.Principal) {
			_, err := e.svc.ClaimItem(p, "o1", e.appIssue.ID, it.Key, service.ItemClaimInput{})
			ch <- res{err}
		}(p)
	}
	ok := 0
	for range 3 {
		if r := <-ch; r.err == nil {
			ok++
		} else if service.Code(r.err) != 409 {
			t.Fatalf("輸的人要 409:%v", r.err)
		}
	}
	if ok != 1 {
		t.Fatalf("同時領取只能一個成功:%d", ok)
	}
}

func TestIssueItemBlockedAndUnblock(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	a := addItem(t, e, e.boss, is, service.ItemInput{Title: "做事"})
	other := addItem(t, e, e.boss, is, service.ItemInput{Title: "另一項"})
	if _, err := e.svc.ClaimItem(e.boss, "o1", is, a.Key, service.ItemClaimInput{}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.ReportItem(e.boss, "o1", is, a.Key, service.ItemReport{Status: "blocked"})
	wantCode(t, "blocked 要附原因", err, 400)
	_, err = e.svc.ReportItem(e.boss, "o1", is, a.Key, service.ItemReport{Status: "blocked", BlockReason: "x", WaitForKind: "user_decision"})
	wantCode(t, "等待種類與對象要成對", err, 400)
	_, err = e.svc.ReportItem(e.boss, "o1", is, a.Key, service.ItemReport{Status: "blocked", BlockReason: "x", WaitForKind: "xx", WaitFor: "y"})
	wantCode(t, "等待種類不合法", err, 400)
	b, err := e.svc.BlockItem(e.boss, "o1", is, a.Key, service.ItemReport{BlockReason: "要選 A 或 B", WaitForKind: "user_decision", WaitFor: "用 A 還是 B?"})
	if err != nil || b.Status != model.ItemBlocked || b.BlockReason != "要選 A 或 B" || b.WaitForKind != "user_decision" || b.ClaimedBy != "" || b.LockedUntil != nil {
		t.Fatalf("卡住:%+v %v", b, err)
	}
	_, err = e.svc.ClaimItem(e.alice, "o1", is, a.Key, service.ItemClaimInput{})
	wantCode(t, "卡住的不能領", err, 409)
	// 等待另一個子項:輸出成 key
	w, err := e.svc.ReportItem(e.boss, "o1", is, other.Key, service.ItemReport{Status: "blocked", BlockReason: "等第一項", WaitForKind: "item", WaitFor: "1"})
	if err != nil || w.WaitFor != a.Key {
		t.Fatalf("等待子項:%+v %v", w, err)
	}
	// unblock:任何成員,回 todo 並清欄位;不是卡住 409
	u, err := e.svc.UnblockItem(e.alice, "o1", is, a.Key)
	if err != nil || u.Status != model.ItemTodo || u.BlockReason != "" || u.WaitForKind != "" || u.WaitFor != "" {
		t.Fatalf("unblock:%+v %v", u, err)
	}
	_, err = e.svc.UnblockItem(e.alice, "o1", is, a.Key)
	wantCode(t, "不是卡住", err, 409)
	// report status=todo 也能解除卡住
	if u, err = e.svc.ReportItem(e.alice, "o1", is, other.Key, service.ItemReport{Status: "todo"}); err != nil || u.Status != model.ItemTodo {
		t.Fatalf("report todo 解除卡住:%+v %v", u, err)
	}
	if _, err := e.svc.ClaimItem(e.alice, "o1", is, a.Key, service.ItemClaimInput{}); err != nil {
		t.Fatalf("解除後可領:%v", err)
	}
}

func TestIssueItemRoleClaimBindsTaskAndWritesBack(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	a := addItem(t, e, e.boss, is, service.ItemInput{Title: "交給研發長", Assignee: "agent:cto", Acceptance: "跑測試", Files: []string{"a.go"}})
	if a.Assignee != "agent:cto" || a.AssigneeActor == nil || a.AssigneeActor.Kind != "agent" {
		t.Fatalf("負責人是角色:%+v", a)
	}
	r, err := e.svc.ClaimItem(e.boss, "o1", is, a.Key, service.ItemClaimInput{})
	if err != nil || r.Task == nil || r.Credential == nil || r.Item.TaskID != r.Task.ID || r.Item.Status != model.ItemDoing {
		t.Fatalf("角色領取要綁任務並發憑證:%+v %v", r, err)
	}
	if r.Task.AgentID != "cto" || r.Task.Status != "inProgress" || len(r.Task.Links) != 2 || r.Task.Links[0] != e.appIssue.ID || r.Task.Links[1] != a.ID ||
		!strings.Contains(r.Task.Instructions, a.Key) {
		t.Fatalf("綁定的任務:%+v", r.Task)
	}
	// 由子項產生的任務不會被 ClaimNext 撿走(領取後不是 queued,但釋放回 queued 後也不會)
	cto, err := e.svc.AuthenticateAgent("cto", r.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	// 另一個子項:角色憑證不能回報不是自己綁定任務的子項
	b := addItem(t, e, e.boss, is, service.ItemInput{Title: "不相干"})
	if _, err := e.svc.ReportItem(cto, "o1", is, b.Key, service.ItemReport{Status: "done"}); service.Code(err) != 403 {
		t.Fatalf("角色憑證只能回報自己綁定的:%v", err)
	}
	// 角色憑證 blocked:子項與任務一起 blocked
	bl, err := e.svc.ReportItem(cto, "o1", is, a.Key, service.ItemReport{Status: "blocked", BlockReason: "要人選", WaitForKind: "user_decision", WaitFor: "A 或 B"})
	if err != nil || bl.Status != model.ItemBlocked {
		t.Fatalf("角色回報 blocked:%+v %v", bl, err)
	}
	tk, _ := e.svc.GetAgentTask(e.boss, "o1", r.Task.ID)
	if tk.Status != "blocked" {
		t.Fatalf("任務也要 blocked:%+v", tk)
	}
	if _, err := e.svc.ReportItem(cto, "o1", is, a.Key, service.ItemReport{Status: "done"}); err == nil {
		t.Fatal("憑證在離開進行中後已作廢")
	}
	// 人解除卡住後再領:重用同一筆任務(blocked 的任務可領),新的憑證
	if _, err := e.svc.UnblockItem(e.boss, "o1", is, a.Key); err != nil {
		t.Fatal(err)
	}
	r2, err := e.svc.ClaimItem(e.boss, "o1", is, a.Key, service.ItemClaimInput{})
	if err != nil || r2.Task == nil || r2.Task.ID != r.Task.ID || r2.Credential == nil {
		t.Fatalf("再領重用任務:%+v %v", r2, err)
	}
	cto2, err := e.svc.AuthenticateAgent("cto", r2.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	done, err := e.svc.ReportItem(cto2, "o1", is, a.Key, service.ItemReport{Status: "done", Evidence: json.RawMessage(`{"commit":"abc123","tests":"ok"}`), Note: "做完了"})
	if err != nil || done.Status != model.ItemDone {
		t.Fatalf("角色回報 done:%+v %v", done, err)
	}
	tk, _ = e.svc.GetAgentTask(e.boss, "o1", r.Task.ID)
	if tk.Status != "done" || tk.Result != "做完了" {
		t.Fatalf("任務跟著 done:%+v", tk)
	}

	// 回寫另一個方向:任務用 agent_task_report(ReportTask)完成,子項跟著完成
	c := addItem(t, e, e.boss, is, service.ItemInput{Title: "經任務回報", Assignee: "agent:cto"})
	rc, err := e.svc.ClaimItem(e.boss, "o1", is, c.Key, service.ItemClaimInput{})
	if err != nil {
		t.Fatal(err)
	}
	ctoC, _ := e.svc.AuthenticateAgent("cto", rc.Credential.Secret, "ip")
	st, res := "done", "任務結果"
	if _, err := e.svc.ReportTask(ctoC, "o1", rc.Task.ID, service.TaskReport{Status: &st, Result: &res}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.GetItem(e.boss, "o1", is, c.Key)
	if got.Status != model.ItemDone || !strings.Contains(string(got.Evidence), rc.Task.ID) || !strings.Contains(string(got.Evidence), "任務結果") {
		t.Fatalf("任務完成要回寫子項:%+v %s", got, got.Evidence)
	}
	// 任務 blocked → 子項 blocked
	d := addItem(t, e, e.boss, is, service.ItemInput{Title: "任務卡住", Assignee: "agent:cto"})
	rd, _ := e.svc.ClaimItem(e.boss, "o1", is, d.Key, service.ItemClaimInput{})
	ctoD, _ := e.svc.AuthenticateAgent("cto", rd.Credential.Secret, "ip")
	bst, bres := "blocked", "缺憑證"
	if _, err := e.svc.ReportTask(ctoD, "o1", rd.Task.ID, service.TaskReport{Status: &bst, Result: &bres}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.GetItem(e.boss, "o1", is, d.Key)
	if got.Status != model.ItemBlocked || got.BlockReason != "缺憑證" || got.ClaimedBy != "" {
		t.Fatalf("任務 blocked 要回寫:%+v", got)
	}
	// 放棄領取:任務取消、子項回 todo;ClaimNext 不會撿走由子項產生的任務
	f := addItem(t, e, e.boss, is, service.ItemInput{Title: "放棄", Assignee: "agent:cto"})
	rf, _ := e.svc.ClaimItem(e.boss, "o1", is, f.Key, service.ItemClaimInput{})
	if _, err := e.svc.ReleaseItem(e.boss, "o1", is, f.Key); err != nil {
		t.Fatal(err)
	}
	if tk, _ := e.svc.GetAgentTask(e.boss, "o1", rf.Task.ID); tk.Status != "canceled" {
		t.Fatalf("放棄領取要取消任務:%+v", tk)
	}
	next, err := e.svc.ClaimNext(e.boss, "o1", "cto")
	if err != nil || next.Task != nil {
		t.Fatalf("ClaimNext 不能撿由子項產生的任務:%+v %v", next, err)
	}
	// Issue 指派給角色排的任務與子項任務互不影響
	if _, err := e.svc.UpdateIssue(e.boss, "o1", is, service.IssuePatch{Assignee: str("cto")}); err != nil {
		t.Fatal(err)
	}
	tasks, _ := e.svc.ListAgentTasks(e.boss, "o1", service.TaskFilter{AgentID: "cto"})
	queued := 0
	for _, x := range tasks {
		if x.Status == "queued" {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("Issue 指派要排一筆自己的任務(不被子項任務擋住):%d", queued)
	}
}

func TestIssueItemPromote(t *testing.T) {
	e := newREnv(t)
	a := addItem(t, e, e.boss, e.appIssue.ID, service.ItemInput{Title: "太大了", Acceptance: "拆乾淨", Files: []string{"x.go"}})
	if _, err := e.svc.PromoteItem(e.cto, "o1", e.appIssue.ID, a.Key, service.ItemPromoteInput{}); service.Code(err) != 403 {
		t.Fatalf("角色憑證不能提升:%v", err)
	}
	r, err := e.svc.PromoteItem(e.alice, "o1", e.appIssue.ID, a.Key, service.ItemPromoteInput{Labels: []string{"拆出"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Issue.Title != "太大了" || r.Issue.Source != a.Key || r.Issue.Project != e.app.ID || !strings.Contains(r.Issue.Body, "拆乾淨") || !strings.Contains(r.Issue.Body, "x.go") {
		t.Fatalf("新 Issue:%+v", r.Issue)
	}
	if r.Item.Status != model.ItemCanceled || r.Item.PromotedIssueID != r.Issue.Key {
		t.Fatalf("原子項要取消並記關聯:%+v", r.Item)
	}
	for _, id := range []string{r.Issue.ID, e.appIssue.ID} {
		iss, _ := e.svc.GetIssue(e.boss, "o1", id)
		if len(iss.Comments) == 0 || !strings.Contains(iss.Comments[len(iss.Comments)-1].Text, "提升") {
			t.Fatalf("兩張 Issue 都要留關聯留言:%+v", iss.Comments)
		}
	}
	_, err = e.svc.PromoteItem(e.alice, "o1", e.appIssue.ID, a.Key, service.ItemPromoteInput{})
	wantCode(t, "不能重複提升", err, 409)
	// 指定組織層級
	b := addItem(t, e, e.boss, e.appIssue.ID, service.ItemInput{Title: "放到組織層"})
	empty := ""
	r2, err := e.svc.PromoteItem(e.boss, "o1", e.appIssue.ID, b.Key, service.ItemPromoteInput{Project: &empty, Priority: "high"})
	if err != nil || r2.Issue.Project != "" || r2.Issue.Priority != "high" {
		t.Fatalf("指定組織層級:%+v %v", r2, err)
	}
}

func TestIssueItemCancelDeleteAndSeqNotReused(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	a := addItem(t, e, e.alice, is, service.ItemInput{Title: "alice 的"})
	b := addItem(t, e, e.boss, is, service.ItemInput{Title: "依賴 a", DependsOn: "1"})
	if err := e.svc.DeleteItem(e.bob, "o1", is, a.Key); service.Code(err) != 403 {
		t.Fatalf("非建立者非管理員不能刪:%v", err)
	}
	if err := e.svc.DeleteItem(e.cto, "o1", is, a.Key); service.Code(err) != 403 {
		t.Fatalf("角色憑證不能刪:%v", err)
	}
	c, err := e.svc.CancelItem(e.bob, "o1", is, a.Key)
	if err != nil || c.Status != model.ItemCanceled {
		t.Fatalf("取消:%+v %v", c, err)
	}
	if c, err = e.svc.CancelItem(e.bob, "o1", is, a.Key); err != nil || c.Status != model.ItemCanceled {
		t.Fatalf("重複取消回目前的樣子:%+v %v", c, err)
	}
	_, err = e.svc.ClaimItem(e.bob, "o1", is, b.Key, service.ItemClaimInput{})
	wantCode(t, "取消的前置不算完成", err, 409)
	if err := e.svc.DeleteItem(e.alice, "o1", is, a.Key); err != nil { // 建立者
		t.Fatal(err)
	}
	got, _ := e.svc.GetItem(e.boss, "o1", is, b.Key)
	if got.DependsOn != "" {
		t.Fatalf("前置被刪,依賴清空:%+v", got)
	}
	n := addItem(t, e, e.boss, is, service.ItemInput{Title: "新的"})
	if n.Seq != 3 {
		t.Fatalf("序號不重用:%d", n.Seq)
	}
	if _, err := e.svc.GetItem(e.boss, "o1", is, a.Key); service.Code(err) != 404 {
		t.Fatalf("刪掉的 404:%v", err)
	}
	done := addItem(t, e, e.boss, is, service.ItemInput{Title: "完成的"})
	if _, err := e.svc.ReportItem(e.boss, "o1", is, done.Key, service.ItemReport{Status: "done"}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CancelItem(e.boss, "o1", is, done.Key)
	wantCode(t, "完成的不能取消", err, 409)
}

func TestIssueItemCountsOnIssue(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	a := addItem(t, e, e.boss, is, service.ItemInput{Title: "a"})
	addItem(t, e, e.boss, is, service.ItemInput{Title: "b"})
	c := addItem(t, e, e.boss, is, service.ItemInput{Title: "c"})
	if _, err := e.svc.ReportItem(e.boss, "o1", is, a.Key, service.ItemReport{Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CancelItem(e.boss, "o1", is, c.Key); err != nil {
		t.Fatal(err)
	}
	// 關閉 Issue 不被未完成子項阻擋;計數:不含取消的 2 筆、未完成 1 筆
	if _, err := e.svc.UpdateIssue(e.boss, "o1", is, service.IssuePatch{Status: str("closed")}); err != nil {
		t.Fatalf("關閉不能被子項擋住:%v", err)
	}
	g, err := e.svc.GetIssue(e.boss, "o1", is)
	if err != nil || g.ItemsTotal != 2 || g.ItemsOpen != 1 || g.Status != "closed" {
		t.Fatalf("GetIssue 計數:%+v %v", g, err)
	}
	l, _ := e.svc.ListIssues(e.boss, "o1")
	for _, i := range l {
		if i.ID == is && (i.ItemsTotal != 2 || i.ItemsOpen != 1) {
			t.Fatalf("列表計數:%+v", i)
		}
		if i.ID != is && (i.ItemsTotal != 0 || i.ItemsOpen != 0) {
			t.Fatalf("沒有子項的 Issue 不帶計數:%+v", i)
		}
	}
	b, _ := json.Marshal(l[0])
	if l[0].ID != is && strings.Contains(string(b), "itemsOpen") {
		t.Fatalf("沒有子項不輸出計數欄位:%s", b)
	}
}

func TestIssueItemRestrictedProjectVisibility(t *testing.T) {
	e := newREnv(t)
	sec := e.secIssue.ID
	it := addItem(t, e, e.boss, sec, service.ItemInput{Title: "機密子項"})
	for name, p := range map[string]*model.Principal{"bob(成員)": e.bob, "carol(專案管理員)": e.carol, "boss": e.boss, "cto 角色(成員)": e.cto} {
		if _, err := e.svc.GetItem(p, "o1", sec, it.Key); err != nil {
			t.Fatalf("%s 看得到:%v", name, err)
		}
		if l, err := e.svc.ListItems(p, "o1", e.secIssue.Key, service.ItemListQuery{}); err != nil || len(l) != 1 {
			t.Fatalf("%s 列表:%v %v", name, l, err)
		}
	}
	// 非成員:所有入口都和 Issue 不存在一樣的 404(含舊 key、內部 id)
	for name, p := range map[string]*model.Principal{"alice": e.alice, "pm 角色(非成員)": e.pm} {
		_, err := e.svc.GetItem(p, "o1", sec, it.Key)
		wantCode(t, name+" 單筆", err, 404)
		_, err = e.svc.GetItem(p, "o1", "ISS-9999", "1")
		want := err.Error()
		_, err = e.svc.GetItem(p, "o1", e.secIssue.Key, it.Key)
		if err == nil || service.Code(err) != 404 || !strings.Contains(err.Error(), "找不到 Issue") || want == "" {
			t.Fatalf("%s 要和不存在的 Issue 一樣 404:%v", name, err)
		}
		_, err = e.svc.ListItems(p, "o1", sec, service.ItemListQuery{})
		wantCode(t, name+" 列表", err, 404)
		_, err = e.svc.AddItem(p, "o1", sec, service.ItemInput{Title: "偷加"})
		wantCode(t, name+" 新增", err, 404)
	}
	for name, f := range map[string]func() error{
		"領取": func() error { return ign(e.svc.ClaimItem(e.alice, "o1", sec, it.Key, service.ItemClaimInput{})) },
		"回報": func() error {
			return ign(e.svc.ReportItem(e.alice, "o1", sec, it.Key, service.ItemReport{Status: "done"}))
		},
		"改": func() error {
			return ign(e.svc.UpdateItem(e.alice, "o1", sec, it.Key, service.ItemPatch{Title: str("x")}))
		},
		"取消": func() error { return ign(e.svc.CancelItem(e.alice, "o1", sec, it.Key)) },
		"提升": func() error { return ign(e.svc.PromoteItem(e.alice, "o1", sec, it.Key, service.ItemPromoteInput{})) },
		"刪除": func() error { return e.svc.DeleteItem(e.alice, "o1", sec, it.Key) },
	} {
		wantCode(t, "非成員 "+name, f(), 404)
	}
	// 計數也不能洩漏:非成員的 Issue 列表看不到 SEC-1
	for _, i := range func() []model.Issue { l, _ := e.svc.ListIssues(e.alice, "o1"); return l }() {
		if i.ID == sec {
			t.Fatal("非成員列表不能有受限 Issue")
		}
	}
	// 受限專案的角色負責人要是專案成員
	_, err := e.svc.AddItem(e.boss, "o1", sec, service.ItemInput{Title: "給 pm", Assignee: "agent:pm"})
	wantCode(t, "非成員角色不能當負責人", err, 400)
	if _, err := e.svc.AddItem(e.boss, "o1", sec, service.ItemInput{Title: "給 cto", Assignee: "agent:cto"}); err != nil {
		t.Fatalf("成員角色可以:%v", err)
	}
	// 角色被移出專案:負責人清空
	if err := e.svc.RemoveProjectMember(e.boss, "o1", e.sec.ID, "agent:cto"); err != nil {
		t.Fatal(err)
	}
	l, _ := e.svc.ListItems(e.boss, "o1", sec, service.ItemListQuery{})
	for _, x := range l {
		if x.Assignee != "" {
			t.Fatalf("角色移出受限專案後子項負責人要清掉:%+v", x)
		}
	}
	// Issue 搬進受限專案:非成員看不到它的子項
	moved := addItem(t, e, e.boss, e.appIssue.ID, service.ItemInput{Title: "搬家前"})
	if _, err := e.svc.UpdateIssue(e.boss, "o1", e.appIssue.ID, service.IssuePatch{Project: str(e.sec.ID)}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.GetItem(e.alice, "o1", e.appIssue.ID, moved.ID)
	wantCode(t, "Issue 搬進受限專案後非成員看不到子項", err, 404)
}

func TestIssueItemAgentCredentialRules(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	// 角色憑證可以新增(提出下一步)、列、看;負責人只能留空或自己
	a, err := e.svc.AddItem(e.cto, "o1", is, service.ItemInput{Title: "下一步", Assignee: "agent:cto"})
	if err != nil || a.CreatedBy != "agent:cto" || a.CreatedByActor == nil || a.CreatedByActor.Kind != "agent" {
		t.Fatalf("角色新增:%+v %v", a, err)
	}
	if _, err := e.svc.AddItem(e.cto, "o1", is, service.ItemInput{Title: "指派給人", Assignee: "alice@x"}); service.Code(err) != 403 {
		t.Fatalf("角色不能指派給人:%v", err)
	}
	if _, err := e.svc.AddItem(e.cto, "o1", is, service.ItemInput{Title: "指派給別的角色", Assignee: "agent:pm"}); service.Code(err) != 403 {
		t.Fatalf("角色不能指派給別的角色:%v", err)
	}
	if l, err := e.svc.ListItems(e.cto, "o1", is, service.ItemListQuery{}); err != nil || len(l) != 1 {
		t.Fatalf("角色看清單:%+v %v", l, err)
	}
	// 其餘操作 403
	for name, f := range map[string]func() error{
		"update":  func() error { return ign(e.svc.UpdateItem(e.cto, "o1", is, a.Key, service.ItemPatch{Title: str("x")})) },
		"claim":   func() error { return ign(e.svc.ClaimItem(e.cto, "o1", is, a.Key, service.ItemClaimInput{})) },
		"release": func() error { return ign(e.svc.ReleaseItem(e.cto, "o1", is, a.Key)) },
		"unblock": func() error { return ign(e.svc.UnblockItem(e.cto, "o1", is, a.Key)) },
		"cancel":  func() error { return ign(e.svc.CancelItem(e.cto, "o1", is, a.Key)) },
		"promote": func() error { return ign(e.svc.PromoteItem(e.cto, "o1", is, a.Key, service.ItemPromoteInput{})) },
		"delete":  func() error { return e.svc.DeleteItem(e.cto, "o1", is, a.Key) },
		"report 沒綁定任務": func() error {
			return ign(e.svc.ReportItem(e.cto, "o1", is, a.Key, service.ItemReport{Status: "done"}))
		},
	} {
		wantCode(t, "角色憑證 "+name, f(), 403)
	}
	// 外部組織
	if _, err := e.svc.AddItem(e.xa, "o1", is, service.ItemInput{Title: "外部"}); service.Code(err) != 403 {
		t.Fatalf("外部組織的角色憑證:%v", err)
	}
	if _, err := e.svc.GetItem(e.eve, "o1", is, a.Key); service.Code(err) != 403 {
		t.Fatalf("外部組織的成員:%v", err)
	}
}

func TestIssueItemInputValidation(t *testing.T) {
	e := newREnv(t)
	is := e.appIssue.ID
	for name, in := range map[string]service.ItemInput{
		"空標題":     {Title: "  "},
		"標題太長":    {Title: strings.Repeat("字", model.MaxItemTitle+1)},
		"驗收太長":    {Title: "x", Acceptance: strings.Repeat("a", model.MaxItemAcceptance+1)},
		"檔案太多":    {Title: "x", Files: manyFiles(model.MaxItemFiles + 1)},
		"負責人不存在":  {Title: "x", Assignee: "nobody@x"},
		"依賴不在同一張": {Title: "x", DependsOn: "ISS-1.99"},
	} {
		if _, err := e.svc.AddItem(e.boss, "o1", is, in); service.Code(err) != 400 {
			t.Fatalf("%s 要 400:%v", name, err)
		}
	}
	if _, err := e.svc.AddItem(e.boss, "o1", "ISS-9999", service.ItemInput{Title: "x"}); service.Code(err) != 404 {
		t.Fatalf("Issue 不存在 404:%v", err)
	}
	a := addItem(t, e, e.boss, is, service.ItemInput{Title: "x"})
	if _, err := e.svc.ReportItem(e.boss, "o1", is, a.Key, service.ItemReport{Status: "weird"}); service.Code(err) != 400 {
		t.Fatalf("status 不合法 400:%v", err)
	}
	if _, err := e.svc.ReportItem(e.boss, "o1", is, a.Key, service.ItemReport{Status: "done", Evidence: json.RawMessage(`[1]`)}); service.Code(err) != 400 {
		t.Fatalf("evidence 要物件 400:%v", err)
	}
	if _, err := e.svc.GetItem(e.boss, "o1", is, "99"); service.Code(err) != 404 {
		t.Fatalf("子項不存在 404:%v", err)
	}
	// 子項可用 key、序號、# 序號、id 找
	for _, ref := range []string{a.Key, "1", "#1", a.ID, strings.ToUpper(a.ID)} {
		if got, err := e.svc.GetItem(e.boss, "o1", is, ref); err != nil || got.ID != a.ID {
			t.Fatalf("用 %q 找:%v", ref, err)
		}
	}
}

func manyFiles(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strings.Repeat("f", 3) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	return out
}
