package service_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"omixor-core/httperr"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// transferStores 用 PG(每個測試一個獨立 database)
func transferStores(t *testing.T) map[string]func(t *testing.T) store.Store {
	return map[string]func(t *testing.T) store.Store{"pg": func(t *testing.T) store.Store { return storetest.New(t) }}
}

func forEachStore(t *testing.T, fn func(t *testing.T, st store.Store)) {
	for name, open := range transferStores(t) {
		t.Run(name, func(t *testing.T) { fn(t, open(t)) })
	}
}

// failChain 讓 SaveOrgChain 失敗的 store(包括 Atomic 給 fn 的 tx):模擬刪角色做到一半出錯
type failChain struct{ store.Store }

func (f failChain) SaveOrgChain(string, model.OrgChain) error { return errors.New("boom") }
func (f failChain) Atomic(fn func(store.Store) error) error {
	return f.Store.Atomic(func(tx store.Store) error { return fn(failChain{tx}) })
}

// 角色 dev 名下:每種狀態各一筆(T-1..T-5)+ 指派 Issue 自動排的一筆(T-6);ops 一筆(T-7);off 是停用的角色
func transferEnv(t *testing.T, st store.Store) (svc *service.Service, boss, alice *model.Principal, issueID string) {
	t.Helper()
	svc, _, boss, alice, _ = issueEnv(t, st)
	for _, a := range []model.Agent{{ID: "ops", Name: "維運", Active: true}, {ID: "off", Name: "停用的", Active: false}} {
		if _, err := svc.SaveAgent(boss, "o1", a); err != nil {
			t.Fatal(err)
		}
	}
	at := model.Now()
	until := model.Time{Time: at.Add(time.Hour)}
	for _, status := range []string{"queued", "inProgress", "blocked", "done", "canceled"} {
		tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "dev", Title: status})
		if err != nil {
			t.Fatal(err)
		}
		if tk, err = st.GetAgentTask("o1", tk.ID); err != nil { // 改 store 裡的原件(輸出是相容字串,不能寫回 store)
			t.Fatal(err)
		}
		tk.Status = status
		switch status {
		case "inProgress":
			tk.ClaimedAt, tk.LockedUntil, tk.ClaimedBy = &at, &until, storetest.User(t, st, "alice@x").ID
		case "done", "canceled":
			tk.CompletedAt = &at
		}
		if err := st.UpdateAgentTask("o1", *tk); err != nil {
			t.Fatal(err)
		}
		if status == "inProgress" {
			if err := st.SaveAgentCredential(model.AgentCredential{Org: "o1", AgentID: "dev", TaskID: tk.ID, Hash: []byte{7}, IssuedAt: at, ExpiresAt: until}); err != nil {
				t.Fatal(err)
			}
		}
	}
	iss, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "指派給 dev 的", Assignee: "dev"}) // T-6
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "ops", Title: "ops 的"}); err != nil { // T-7
		t.Fatal(err)
	}
	bossA, devA, opsA, aliceA := storetest.User(t, st, "boss@x").ID, storetest.Agent(t, st, "o1", "dev").ID, storetest.Agent(t, st, "o1", "ops").ID, storetest.User(t, st, "alice@x").ID
	if err := st.SaveOrgChain("o1", model.OrgChain{Edges: []model.ChainEdge{{From: bossA, To: devA}, {From: bossA, To: opsA}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateChannel("o1", model.Channel{Name: "c", Members: []string{aliceA, devA, opsA}, CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	return svc, boss, alice, iss.ID
}

func taskOf(t *testing.T, st store.Store, id string) model.AgentTask {
	t.Helper()
	x, err := st.GetAgentTask("o1", id)
	if err != nil {
		t.Fatal(err)
	}
	return *x
}

func auditActions(st store.Store) []string {
	l, _ := st.ListAudit(100)
	out := []string{}
	for _, e := range l {
		out = append(out, e.Action)
	}
	return out
}

// 沒有任務:直接刪(不用 transferTo)
func TestDeleteAgentNoTasks(t *testing.T) {
	forEachStore(t, func(t *testing.T, st store.Store) {
		svc, _, boss, _, _ := issueEnv(t, st)
		if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "tmp", Name: "暫時", Active: true}); err != nil {
			t.Fatal(err)
		}
		if err := svc.DeleteAgent(boss, "o1", "tmp"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.GetAgent("o1", "tmp"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("角色要刪掉", err)
		}
		if err := svc.DeleteAgent(boss, "o1", "tmp"); service.Code(err) != 404 {
			t.Fatal("再刪一次 404", err)
		}
	})
}

// 名下有任務:不帶 transferTo → 409,帶各狀態的任務數;角色與任務都不動
func TestDeleteAgentWithTasksConflict(t *testing.T) {
	forEachStore(t, func(t *testing.T, st store.Store) {
		svc, boss, alice, _ := transferEnv(t, st)
		err := svc.DeleteAgent(boss, "o1", "dev")
		var se *service.Error
		if service.Code(err) != 409 || !errors.As(err, &se) {
			t.Fatal("要 409", err)
		}
		if se.Data["code"] != service.AgentHasTasksCode || se.Data["agent"] != "dev" || se.Data["total"] != 6 {
			t.Fatalf("Data:%v", se.Data)
		}
		by, _ := se.Data["taskCounts"].(map[string]int)
		if by["queued"] != 2 || by["inProgress"] != 1 || by["blocked"] != 1 || by["done"] != 1 || by["canceled"] != 1 {
			t.Fatalf("各狀態的任務數:%v", by)
		}
		for _, w := range []string{"6 筆", "待領取 2", "進行中 1", "需要協助 1", "完成 1", "已取消 1"} {
			if !strings.Contains(err.Error(), w) {
				t.Fatalf("訊息缺 %q:%s", w, err.Error())
			}
		}
		if _, err := st.GetAgent("o1", "dev"); err != nil {
			t.Fatal("角色不能被刪", err)
		}
		if g := taskOf(t, st, "T-2"); g.Status != "inProgress" || g.AgentID != "dev" {
			t.Fatal("任務不能動", g)
		}
		// 一般成員不能刪;不能轉給自己、不存在、已停用;失敗都不動
		if err := svc.DeleteAgentTo(alice, "o1", "dev", "ops"); service.Code(err) != 403 {
			t.Fatal("成員不能刪角色", err)
		}
		for target, want := range map[string]int{"dev": 400, "DEV": 400, "ghost": 404, "off": 400} {
			if err := svc.DeleteAgentTo(boss, "o1", "dev", target); service.Code(err) != want {
				t.Fatalf("轉給 %s:要 %d,拿到 %v", target, want, err)
			}
		}
		if _, err := st.GetAgent("o1", "dev"); err != nil {
			t.Fatal("角色不能被刪", err)
		}
		if g := taskOf(t, st, "T-1"); g.AgentID != "dev" || len(g.Notes) != 0 {
			t.Fatal("任務不能動", g)
		}
	})
}

// 轉移再刪:各狀態、備註、憑證、Issue 指派、組織鏈、頻道成員、稽核
func TestDeleteAgentWithTransfer(t *testing.T) {
	forEachStore(t, func(t *testing.T, st store.Store) {
		svc, boss, alice, issueID := transferEnv(t, st)
		if err := svc.DeleteAgentTo(boss, "o1", "dev", "ops"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.GetAgent("o1", "dev"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("角色要刪掉", err)
		}
		want := map[string]string{"T-1": "queued", "T-2": "queued", "T-3": "blocked", "T-4": "done", "T-5": "canceled", "T-6": "queued", "T-7": "queued"}
		for id, status := range want {
			g := taskOf(t, st, id)
			if g.AgentID != "ops" || g.Status != status {
				t.Fatalf("%s:角色 %s 狀態 %s,要 ops/%s", id, g.AgentID, g.Status, status)
			}
			notes := 0
			for _, n := range g.Notes {
				if strings.HasPrefix(n.Text, "由 工程師 轉移,") {
					notes++
					if n.Author != model.SystemActorOmixor || n.ID == "" {
						t.Fatal("備註的署名與 id", n)
					}
				}
			}
			if id == "T-7" && notes != 0 {
				t.Fatal("原本就是 ops 的不加備註")
			}
			if id != "T-7" && notes != 1 {
				t.Fatalf("%s 要有一則轉移備註:%v", id, g.Notes)
			}
		}
		g2 := taskOf(t, st, "T-2")
		if g2.LockedUntil != nil || g2.ClaimedBy != "" {
			t.Fatal("進行中的要解鎖", g2)
		}
		if _, err := st.AgentCredentialForTask("o1", "T-2"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("憑證要作廢", err)
		}
		if g := taskOf(t, st, "T-4"); g.CompletedAt == nil {
			t.Fatal("已完成的完成時間不變", g)
		}
		got, err := svc.GetIssue(alice, "o1", issueID)
		if err != nil || got.Assignee != "ops" {
			t.Fatal("Issue 的指派要改成目標角色", got, err)
		}
		c, _ := st.GetOrgChain("o1")
		if len(c.Edges) != 1 || c.Edges[0].To != storetest.Agent(t, st, "o1", "ops").ID {
			t.Fatal("組織鏈指向被刪角色的邊要移除", c.Edges)
		}
		chs, _ := st.ListChannels("o1")
		if len(chs) != 1 || strings.Join(chs[0].Members, ",") != storetest.User(t, st, "alice@x").ID+","+storetest.Agent(t, st, "o1", "ops").ID {
			t.Fatal("頻道成員要拿掉被刪角色", chs)
		}
		acts := strings.Join(auditActions(st), " ")
		if !strings.Contains(acts, "agent.task.transfer") || !strings.Contains(acts, "agent.delete") {
			t.Fatal("稽核", acts)
		}
		al, _ := st.ListAudit(100)
		for _, e := range al {
			if e.Action == "agent.task.transfer" && (e.Detail["from"] != "dev" || e.Detail["to"] != "ops" || fmt.Sprint(e.Detail["total"]) != "6" || fmt.Sprint(e.Detail["released"]) != "1") {
				t.Fatalf("轉移稽核:%v", e.Detail)
			}
		}
	})
}

// 刪角色做到一半失敗:全部還原(角色、任務、憑證、Issue 指派、頻道成員都不動)
func TestDeleteAgentTransferRollback(t *testing.T) {
	forEachStore(t, func(t *testing.T, st store.Store) {
		svc, boss, alice, issueID := transferEnv(t, st)
		svc.Store = failChain{st} // 組織鏈有指向 dev 的邊,拿掉時失敗
		err := svc.DeleteAgentTo(boss, "o1", "dev", "ops")
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatal("要失敗", err)
		}
		svc.Store = st
		if _, err := st.GetAgent("o1", "dev"); err != nil {
			t.Fatal("角色要還在", err)
		}
		for _, id := range []string{"T-1", "T-2", "T-3", "T-4", "T-5", "T-6"} {
			if g := taskOf(t, st, id); g.AgentID != "dev" || len(g.Notes) != 0 {
				t.Fatalf("%s 要還原:%v", id, g)
			}
		}
		if g := taskOf(t, st, "T-2"); g.Status != "inProgress" || g.LockedUntil == nil || g.ClaimedBy != storetest.User(t, st, "alice@x").ID {
			t.Fatal("進行中的要還原", g)
		}
		if _, err := st.AgentCredentialForTask("o1", "T-2"); err != nil {
			t.Fatal("憑證要還原", err)
		}
		if got, _ := svc.GetIssue(alice, "o1", issueID); got.Assignee != "dev" {
			t.Fatal("Issue 指派要還原", got.Assignee)
		}
		chs, _ := st.ListChannels("o1")
		if len(chs[0].Members) != 3 {
			t.Fatal("頻道成員要還原", chs[0].Members)
		}
		if c, _ := st.GetOrgChain("o1"); len(c.Edges) != 2 {
			t.Fatal("組織鏈要還原", c.Edges)
		}
		for _, a := range auditActions(st) {
			if a == "agent.delete" || a == "agent.task.transfer" {
				t.Fatal("失敗不留稽核", a)
			}
		}
	})
}

// 不刪角色的單獨轉移:範圍 open(預設)只轉待領取與需要協助;all 全部轉(進行中退回待領取)
func TestTransferAgentTasks(t *testing.T) {
	forEachStore(t, func(t *testing.T, st store.Store) {
		svc, boss, alice, _ := transferEnv(t, st)
		if _, err := svc.TransferAgentTasks(alice, "o1", service.AgentTransferInput{From: "dev", To: "ops"}); service.Code(err) != 403 {
			t.Fatal("成員不能轉移", err)
		}
		for _, bad := range []service.AgentTransferInput{
			{From: "dev", To: "dev"}, {From: "dev", To: "off"}, {From: "dev", To: "ghost"}, {From: "ghost", To: "ops"},
			{From: "", To: "ops"}, {From: "dev", To: ""}, {From: "dev", To: "ops", Scope: "weird"},
		} {
			if _, err := svc.TransferAgentTasks(boss, "o1", bad); err == nil || service.Code(err) < 400 || service.Code(err) >= 500 {
				t.Fatalf("%+v 要 4xx:%v", bad, err)
			}
		}
		if g := taskOf(t, st, "T-1"); g.AgentID != "dev" {
			t.Fatal("失敗不能動")
		}
		r, err := svc.TransferAgentTasks(boss, "o1", service.AgentTransferInput{From: "dev", To: "ops"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Scope != service.TransferOpen || r.Total != 3 || r.ByStatus["queued"] != 2 || r.ByStatus["blocked"] != 1 || r.Released != 0 {
			t.Fatalf("open 的結果:%+v", r)
		}
		if g := taskOf(t, st, "T-2"); g.AgentID != "dev" || g.Status != "inProgress" {
			t.Fatal("open 範圍不動進行中的", g)
		}
		if _, err := st.AgentCredentialForTask("o1", "T-2"); err != nil {
			t.Fatal("open 範圍不動憑證", err)
		}
		if g := taskOf(t, st, "T-4"); g.AgentID != "dev" {
			t.Fatal("open 範圍不動已完成的")
		}
		r, err = svc.TransferAgentTasks(boss, "o1", service.AgentTransferInput{From: "dev", To: "ops", Scope: "all"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Total != 3 || r.Released != 1 || r.ByStatus["inProgress"] != 1 || r.ByStatus["done"] != 1 || r.ByStatus["canceled"] != 1 {
			t.Fatalf("all 的結果:%+v", r)
		}
		if g := taskOf(t, st, "T-2"); g.AgentID != "ops" || g.Status != "queued" || g.LockedUntil != nil || g.ClaimedBy != "" {
			t.Fatal("all 範圍進行中的退回待領取", g)
		}
		if _, err := st.AgentCredentialForTask("o1", "T-2"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("憑證作廢", err)
		}
		// 轉完了:dev 沒有任務,可以直接刪;Issue 的指派在「不刪角色」的轉移不動(還是 dev),刪角色時才清
		if err := svc.DeleteAgent(boss, "o1", "dev"); err != nil {
			t.Fatal(err)
		}
		acts := strings.Join(auditActions(st), " ")
		if strings.Count(acts, "agent.task.transfer") != 2 {
			t.Fatal("每次轉移一筆稽核", acts)
		}
	})
}

// 名下沒有任務但帶了 transferTo:一律驗證目標,指派給它的 Issue 改給目標(不是清空);目標不合格照樣擋
func TestDeleteAgentZeroTasksWithTransferTarget(t *testing.T) {
	forEachStore(t, func(t *testing.T, st store.Store) {
		svc, _, boss, alice, _ := issueEnv(t, st)
		for _, a := range []model.Agent{{ID: "tmp", Name: "暫時", Active: true}, {ID: "ops", Name: "維運", Active: true}, {ID: "off", Name: "停用", Active: false}} {
			if _, err := svc.SaveAgent(boss, "o1", a); err != nil {
				t.Fatal(err)
			}
		}
		iss, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "指派給 tmp"})
		if err != nil {
			t.Fatal(err)
		}
		cur, err := st.GetIssue("o1", iss.ID) // 直接寫 store 指派(經 service 指派會自動排任務)
		if err != nil {
			t.Fatal(err)
		}
		cur.Assignee, cur.Comments = storetest.Agent(t, st, "o1", "tmp").ID, nil
		if err := st.UpdateIssue("o1", *cur); err != nil {
			t.Fatal(err)
		}
		// 沒有任務也要驗證目標
		if err := svc.DeleteAgentTo(boss, "o1", "tmp", "off"); service.Code(err) != 400 {
			t.Fatal("停用的目標要 400", err)
		}
		if err := svc.DeleteAgentTo(boss, "o1", "tmp", "zz"); service.Code(err) != 404 {
			t.Fatal("不存在的目標要 404", err)
		}
		// 本來就沒有 queued 的任務被 AssignTask 排進 tmp 的可能已被刪光:確認名下 0 筆
		if l, _ := st.ListAgentTasks("o1"); func() bool {
			_, n := store.TaskCounts(l, "tmp")
			return n != 0
		}() {
			t.Fatal("測試前提:tmp 名下沒有任務")
		}
		if err := svc.DeleteAgentTo(boss, "o1", "tmp", "ops"); err != nil {
			t.Fatal(err)
		}
		got, err := st.GetIssue("o1", iss.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Assignee != storetest.Agent(t, st, "o1", "ops").ID {
			t.Fatalf("Issue 要改指派給目標,不是清空:%q", got.Assignee)
		}
	})
}

// 外鍵錯誤的對外狀態碼與訊息:仍被參照 409、指向不存在 400,訊息是通用中文、不夾帶資料庫原文
func TestFKErrorsPublicMapping(t *testing.T) {
	inUse := fmt.Errorf("%w: update or delete on table \"omixor_projects\" violates foreign key constraint \"fk_docs_project\"", store.ErrInUse)
	bad := fmt.Errorf("%w: insert on table \"omixor_docs\" violates foreign key constraint", store.ErrBadRef)
	if service.Code(inUse) != 409 || service.Code(bad) != 400 {
		t.Fatal("狀態碼", service.Code(inUse), service.Code(bad))
	}
	for _, e := range []error{inUse, bad} {
		m := httperr.Classify(e).Msg
		if strings.Contains(m, "omixor_") || strings.Contains(m, "constraint") || m == "" {
			t.Fatal("訊息不能夾帶資料庫原文:", m)
		}
	}
	if m := httperr.Classify(&service.Error{Code: 404, Msg: "找不到"}).Msg; m != "找不到" {
		t.Fatal(m)
	}
}
