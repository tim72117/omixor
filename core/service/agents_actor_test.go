package service_test

import (
	"sync/atomic"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// 任務的「誰」欄位(ISS-36 C-2b):store 存 actor id,服務層出口是相容字串

// claimed_by 用 actor id 比對:email 大小寫不同仍是同一個人;store 裡是合法的 actor id
func TestTaskClaimedByActorID(t *testing.T) {
	svc, st, boss, alice, bob := chainEnv(t) // alice 的 Principal email 是 Alice@X
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "cto", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.CreatedBy != "boss@x" {
		t.Fatalf("派工建立者輸出:%q", tk.CreatedBy)
	}
	res, err := svc.ClaimTask(alice, "o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.ClaimedBy != "alice@x" {
		t.Fatalf("領取者輸出要是 email:%q", res.Task.ClaimedBy)
	}
	raw, err := st.GetAgentTask("o1", tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	au, bu := storetest.User(t, st, "alice@x").ID, storetest.User(t, st, "boss@x").ID
	if raw.ClaimedBy != au || raw.CreatedBy != bu || !model.ValidActorID(raw.ClaimedBy) {
		t.Fatalf("store 要存 actor id:%+v(alice %s boss %s)", raw, au, bu)
	}
	// 同一個人,email 寫成不同大小寫的另一個 Principal:可以回報、可以放棄領取;別人不行
	alice2 := &model.Principal{User: model.User{ID: "u2", Email: "ALICE@X", Role: "member"}}
	if _, err := svc.ReportTask(alice2, "o1", tk.ID, service.TaskReport{Note: sp("進度")}); err != nil {
		t.Fatal("同一個人(大小寫不同)要能回報:", err)
	}
	if _, err := svc.ReportTask(bob, "o1", tk.ID, service.TaskReport{Note: sp("偷")}); code(err) != 403 {
		t.Fatal("別人要 403:", err)
	}
	got, err := svc.ReleaseTask(alice2, "o1", tk.ID, "不做了")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "queued" || got.ClaimedBy != "" {
		t.Fatalf("%+v", got)
	}
	// 備註署名:角色回報的備註是角色(agent:cto),放棄領取的備註是放棄的人
	var authors []string
	for _, n := range got.Notes {
		authors = append(authors, n.Author)
	}
	if len(authors) != 2 || authors[0] != "agent:cto" || authors[1] != "alice@x" {
		t.Fatalf("備註署名:%v", authors)
	}
	rawNotes, _ := st.GetAgentTask("o1", tk.ID)
	if rawNotes.Notes[0].Author != storetest.Agent(t, st, "o1", "cto").ID || rawNotes.Notes[1].Author != au {
		t.Fatalf("store 的備註署名要是 actor id:%+v", rawNotes.Notes)
	}
}

// reportTo 篩選:輸入用嚴格解析器(找不到 400),比對的是 actor id
func TestTaskReportToFilterStrict(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	if _, err := svc.SaveChain(boss, "o1", edges("user:alice@x>agent:cto>目標")); err != nil {
		t.Fatal(err)
	}
	ta, err := svc.AssignTask(alice, "o1", service.TaskInput{AgentID: "cto", Title: "定目標"})
	if err != nil {
		t.Fatal(err)
	}
	_, cto := claimAs(t, svc, boss, "cto", "別的") // 同一個角色再領一筆,不影響
	_ = cto
	r, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: ta.ID})
	if err != nil {
		t.Fatal(err)
	}
	cp, _ := svc.AuthenticateAgent("cto", r.Credential.Secret, "ip")
	blocked := "blocked"
	if _, err := svc.ReportTask(cp, "o1", "", service.TaskReport{Status: &blocked, Note: sp("預算?")}); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"me", "user:ALICE@x", "alice@x", "@alice@x"} {
		p := boss
		if who == "me" {
			p = alice
		}
		l, err := svc.ListAgentTasks(p, "o1", service.TaskFilter{ReportTo: who})
		if err != nil || len(l) != 1 || l[0].ID != ta.ID {
			t.Fatalf("reportTo=%s:%+v %v", who, l, err)
		}
		if l[0].CreatedBy != "alice@x" || l[0].Notes[len(l[0].Notes)-1].Author != "chain" {
			t.Fatalf("輸出署名:%+v", l[0])
		}
	}
	if _, err := svc.ListAgentTasks(boss, "o1", service.TaskFilter{ReportTo: "user:nobody@x"}); code(err) != 400 {
		t.Fatal("找不到的人要 400:", err)
	}
	if _, err := svc.ListAgentTasks(boss, "o1", service.TaskFilter{ReportTo: "agent:ghost"}); code(err) != 400 {
		t.Fatal("找不到的角色要 400:", err)
	}
}

// 例行任務署名 sys_routine;立刻執行的署名是呼叫者
func TestRoutineTaskAuthor(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	past := model.Time{Time: time.Now().Add(-48 * time.Hour).UTC()}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "研發長", Active: true,
		Routines: []model.AgentRoutine{{ID: "r1", Title: "日報", Kind: "daily", Enabled: true, Priority: "normal", CreatedAt: past}}}); err != nil {
		t.Fatal(err)
	}
	made, err := svc.RunDueRoutines(time.Now().Add(48 * time.Hour))
	if err != nil || len(made) == 0 {
		t.Fatalf("要排任務:%v %v", made, err)
	}
	if made[0].CreatedBy != "routine" {
		t.Fatalf("輸出署名:%q", made[0].CreatedBy)
	}
	raw, _ := st.GetAgentTask("o1", made[0].ID)
	if raw.CreatedBy != storetest.System(t, st, model.SystemActorRoutine).ID || raw.CreatedBy != "sys_routine" {
		t.Fatalf("store 要存 sys_routine:%q", raw.CreatedBy)
	}
	now, err := svc.RunRoutine(boss, "o1", "cto", "r1", "")
	if err != nil || now.CreatedBy != "boss@x" || now.RoutineID == nil {
		t.Fatalf("立刻執行署名呼叫者:%+v %v", now, err)
	}
}

// 委派的 created_by 是委派者角色的 actor;轉移備註署名 sys_omixor;角色刪除(退役)後建立者仍讀得回來
func TestTaskDelegateTransferAndRetiredCreator(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	if _, err := svc.SaveChain(boss, "o1", edges("agent:cto>agent:designer>設計")); err != nil {
		t.Fatal(err)
	}
	_, cto := claimAs(t, svc, boss, "cto", "規劃")
	d, err := svc.DelegateTask(cto, "o1", service.DelegateInput{To: "designer", Title: "畫圖"})
	if err != nil {
		t.Fatal(err)
	}
	ctoActor := storetest.Agent(t, st, "o1", "cto").ID
	if raw, _ := st.GetAgentTask("o1", d.ID); raw.CreatedBy != ctoActor || d.CreatedBy != "agent:cto" {
		t.Fatalf("委派 created_by:store %q 輸出 %q", raw.CreatedBy, d.CreatedBy)
	}
	// 轉移:designer 的任務轉給 pm,備註署名 sys_omixor
	if _, err := svc.TransferAgentTasks(boss, "o1", service.AgentTransferInput{From: "designer", To: "pm", Scope: "all"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.GetAgentTask("o1", d.ID)
	if raw.AgentID != "pm" || len(raw.Notes) != 1 || raw.Notes[0].Author != "sys_omixor" {
		t.Fatalf("轉移後:%+v", raw)
	}
	got, err := svc.GetAgentTask(boss, "o1", d.ID)
	if err != nil || got.Notes[0].Author != "omixor" || got.CreatedBy != "agent:cto" {
		t.Fatalf("轉移後輸出:%+v %v", got, err)
	}
	// 刪掉 cto(名下的任務先轉給 pm):它的 actor 退役,d 的 created_by 仍讀回 agent:cto
	if err := svc.DeleteAgentTo(boss, "o1", "cto", "pm"); err != nil {
		t.Fatal(err)
	}
	got, err = svc.GetAgentTask(boss, "o1", d.ID)
	if err != nil || got.CreatedBy != "agent:cto" {
		t.Fatalf("已刪角色的 created_by:%+v %v", got, err)
	}
}

// 檢視回報任務與「已回報給」備註署名 sys_chain,而且能被 reportedTo 找到
func TestChainReviewAuthorIsSysChain(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	if _, err := svc.SaveChain(boss, "o1", edges("agent:cto>agent:designer>設計")); err != nil {
		t.Fatal(err)
	}
	_, cto := claimAs(t, svc, boss, "cto", "規劃")
	d, err := svc.DelegateTask(cto, "o1", service.DelegateInput{To: "designer", Title: "畫圖"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "designer", TaskID: d.ID})
	if err != nil {
		t.Fatal(err)
	}
	dp, _ := svc.AuthenticateAgent("designer", r.Credential.Secret, "ip")
	done := "done"
	if _, err := svc.ReportTask(dp, "o1", "", service.TaskReport{Status: &done, Result: sp("好了")}); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ListAgentTasks("o1")
	review, noted := 0, 0
	for _, x := range all {
		if x.CreatedBy == "sys_chain" {
			review++
		}
		if x.ID == d.ID {
			for _, n := range x.Notes {
				if n.Author == "sys_chain" {
					noted++
				}
			}
		}
	}
	if review != 1 || noted != 1 {
		t.Fatalf("檢視任務 %d、備註 %d", review, noted)
	}
	// 重複回報不會再排第二筆(靠 sys_chain 比對找到未結束的檢視任務)
	if _, err := svc.ReportTask(dp, "o1", "", service.TaskReport{Status: &done}); err == nil {
		// 任務已離開進行中,憑證作廢:這裡只確認沒有多出檢視任務
		t.Log("第二次回報成功")
	}
	after, _ := st.ListAgentTasks("o1")
	n := 0
	for _, x := range after {
		if x.CreatedBy == "sys_chain" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("不能重複排檢視任務:%d", n)
	}
	l, _ := svc.ListAgentTasks(boss, "o1", service.TaskFilter{AgentID: "cto", Status: "open"})
	for _, x := range l {
		if x.CreatedBy == "chain" && x.Title == "檢視回報:畫圖" {
			return
		}
	}
	t.Fatalf("輸出要是 chain:%+v", l)
}

// countingStore 數 ActorsByIDs 的呼叫次數
type countingStore struct {
	store.Store
	n *atomic.Int32
}

func (c countingStore) ActorsByIDs(ids []string) (map[string]model.ActorRecord, error) {
	c.n.Add(1)
	return c.Store.ActorsByIDs(ids)
}

// 列表出口一次批次查詢,不是每筆任務各查一次
func TestListAgentTasksNoNPlusOne(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	for i := 0; i < 12; i++ {
		tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "pm", Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ReportTask(boss, "o1", tk.ID, service.TaskReport{Note: sp("n")}); err != nil {
			t.Fatal(err)
		}
	}
	var n atomic.Int32
	c := &service.Service{Store: countingStore{Store: st, n: &n}}
	l, err := c.ListAgentTasks(boss, "o1", service.TaskFilter{})
	if err != nil || len(l) != 12 {
		t.Fatalf("%d %v", len(l), err)
	}
	if n.Load() != 1 {
		t.Fatalf("ActorsByIDs 呼叫 %d 次,要 1 次", n.Load())
	}
}
