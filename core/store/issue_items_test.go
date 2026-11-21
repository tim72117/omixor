package store

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
)

// Issue 子項(omixor_issue_items)的 store 層測試:序號、依賴驗證、條件式更新、領取鎖、cascade、批次、遷移結構。

func itemFixture(t *testing.T) (*PG, string, string, string, string) {
	t.Helper()
	st := testPG(t)
	newOrg(t, st, "o", "p")
	at := mt("2026-01-02T03:04:05Z")
	alice := lid(t, st, "o", "user:a")
	i1, err := st.CreateIssue("o", model.Issue{Title: "一", Status: "open", Priority: "normal", CreatedBy: alice, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	i2, err := st.CreateIssue("o", model.Issue{Title: "二", Status: "open", Priority: "normal", CreatedBy: alice, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	return st.(*PG), alice, i1.ID, i2.ID, ""
}

func newItem(issue, by, title string) *model.IssueItem {
	return &model.IssueItem{IssueID: issue, Title: title, Status: model.ItemTodo, CreatedBy: by}
}

func TestIssueItemsCRUD(t *testing.T) {
	p, alice, iss1, iss2, _ := itemFixture(t)
	eng := lid(t, p, "o", "agent:eng")

	a, err := p.CreateIssueItem("o", &model.IssueItem{IssueID: iss1, Title: "做 A", Status: model.ItemTodo, Assignee: eng, Files: []string{"a.go", "b.go"},
		Acceptance: "測試過", Evidence: json.RawMessage(`{"commit":"abc","unverified":["x"]}`), CreatedBy: alice})
	chk(t, err)
	if a.Seq != 1 || a.ID == "" || a.Org != "o" || a.IssueID != iss1 || a.Assignee != eng || !slices.Equal(a.Files, []string{"a.go", "b.go"}) ||
		a.Acceptance != "測試過" || a.CreatedAt.IsZero() || a.LockedUntil != nil || a.CompletedAt != nil {
		t.Fatalf("%+v", a)
	}
	var ev map[string]any
	chk(t, json.Unmarshal(a.Evidence, &ev))
	if ev["commit"] != "abc" {
		t.Fatal(string(a.Evidence))
	}
	b, err := p.CreateIssueItem("o", newItem(iss1, alice, "做 B"))
	chk(t, err)
	c, err := p.CreateIssueItem("o", newItem(iss1, alice, "做 C"))
	chk(t, err)
	other, err := p.CreateIssueItem("o", newItem(iss2, alice, "別張的"))
	chk(t, err)
	if b.Seq != 2 || c.Seq != 3 || other.Seq != 1 {
		t.Fatal("每張 Issue 各自連號", b.Seq, c.Seq, other.Seq)
	}
	if b.Files != nil || b.Evidence != nil || b.Assignee != "" || b.ClaimedBy != "" {
		t.Fatalf("空欄位讀回是零值 %+v", b)
	}
	// 刪掉的序號不重用
	chk(t, p.DeleteIssueItem("o", b.ID))
	if err := p.DeleteIssueItem("o", b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	d, err := p.CreateIssueItem("o", newItem(iss1, alice, "做 D"))
	chk(t, err)
	if d.Seq != 4 {
		t.Fatal("序號不重用", d.Seq)
	}
	// 失敗的建立不佔號
	bad := newItem(iss1, "usr_nobody", "壞")
	if _, err := p.CreateIssueItem("o", bad); !errors.Is(err, ErrBadRef) {
		t.Fatal("建立者不是 actor 要 ErrBadRef", err)
	}
	if _, err := p.CreateIssueItem("o", newItem("no-such", alice, "x")); !errors.Is(err, ErrBadRef) {
		t.Fatal("Issue 不存在要 ErrBadRef", err)
	}
	if _, err := p.CreateIssueItem("p", newItem(iss1, alice, "x")); !errors.Is(err, ErrBadRef) {
		t.Fatal("別的組織的 Issue 要 ErrBadRef", err)
	}
	e, err := p.CreateIssueItem("o", newItem(iss1, alice, "做 E"))
	chk(t, err)
	if e.Seq != 5 {
		t.Fatal("失敗的建立不佔號", e.Seq)
	}

	// 清單與批次
	l, err := p.ListIssueItems("o", iss1)
	chk(t, err)
	if len(l) != 4 || l[0].Seq != 1 || l[1].Seq != 3 || l[3].Seq != 5 {
		t.Fatalf("依 seq:%+v", l)
	}
	if l, _ := p.ListIssueItems("o", "none"); l == nil || len(l) != 0 {
		t.Fatal("沒有回空切片", l)
	}
	if l, _ := p.ListIssueItems("p", iss1); len(l) != 0 {
		t.Fatal("組織隔離")
	}
	m, err := p.ListIssueItemsByOrg("o", []string{iss1, iss2, "none"})
	chk(t, err)
	if len(m) != 2 || len(m[iss1]) != 4 || len(m[iss2]) != 1 {
		t.Fatalf("批次 %v", m)
	}
	m, err = p.ListIssueItemsByOrg("o", []string{iss2})
	chk(t, err)
	if len(m) != 1 || m[iss2][0].ID != other.ID {
		t.Fatal("只查指定的 Issue", m)
	}
	m, err = p.ListIssueItemsByOrg("o", nil)
	chk(t, err)
	if len(m) != 2 {
		t.Fatal("空 = 整個組織", len(m))
	}
	_, err = p.GetIssueItem("o", "nope")
	wantNotFound(t, "沒有的子項", err)
	_, err = p.GetIssueItem("p", a.ID)
	wantNotFound(t, "別的組織", err)

	// 條件式更新
	done := model.Now()
	a.Status, a.CompletedAt, a.UpdatedAt, a.Title = model.ItemDone, &done, done, "做 A(改)"
	chk(t, p.UpdateIssueItem("o", a, model.ItemTodo))
	if err := p.UpdateIssueItem("o", a, model.ItemTodo); !errors.Is(err, ErrStale) {
		t.Fatal("狀態已經不是 todo 要 ErrStale", err)
	}
	if err := p.UpdateIssueItem("o", &model.IssueItem{ID: "nope", Title: "x", Status: "todo"}, model.ItemTodo); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	g, err := p.GetIssueItem("o", a.ID)
	chk(t, err)
	if g.Status != model.ItemDone || g.Title != "做 A(改)" || g.CompletedAt == nil || g.Seq != 1 || g.CreatedBy != alice || g.Assignee != eng {
		t.Fatalf("%+v", g)
	}

	// 依賴:同 Issue、無環
	c.DependsOn = a.ID
	chk(t, p.UpdateIssueItem("o", c, model.ItemTodo))
	d.DependsOn = c.ID
	chk(t, p.UpdateIssueItem("o", d, model.ItemTodo))
	a.Status, a.CompletedAt = model.ItemDone, &done
	a.DependsOn = d.ID // a→d→c→a 成環
	err = p.UpdateIssueItem("o", a, model.ItemDone)
	if !errors.Is(err, ErrBadRef) || !strings.Contains(err.Error(), "成環") {
		t.Fatal("成環要拒絕", err)
	}
	a.DependsOn = a.ID
	if err := p.UpdateIssueItem("o", a, model.ItemDone); !errors.Is(err, ErrBadRef) {
		t.Fatal("依賴自己", err)
	}
	a.DependsOn = other.ID
	if err := p.UpdateIssueItem("o", a, model.ItemDone); !errors.Is(err, ErrBadRef) {
		t.Fatal("跨 Issue 的依賴要拒絕", err)
	}
	a.DependsOn = "no-such"
	if err := p.UpdateIssueItem("o", a, model.ItemDone); !errors.Is(err, ErrBadRef) {
		t.Fatal("依賴不存在", err)
	}
	if _, err := p.CreateIssueItem("o", &model.IssueItem{IssueID: iss2, Title: "跨", Status: "todo", DependsOn: a.ID, CreatedBy: alice}); !errors.Is(err, ErrBadRef) {
		t.Fatal("建立時跨 Issue 的依賴要拒絕", err)
	}
	ok, err := p.CreateIssueItem("o", &model.IssueItem{IssueID: iss1, Title: "依賴 a", Status: "todo", DependsOn: a.ID, CreatedBy: alice})
	chk(t, err)
	if ok.DependsOn != a.ID {
		t.Fatal(ok.DependsOn)
	}
	// 刪前置:依賴清成空
	chk(t, p.DeleteIssueItem("o", a.ID))
	if g, _ := p.GetIssueItem("o", c.ID); g.DependsOn != "" {
		t.Fatal("前置被刪,依賴清成空", g.DependsOn)
	}
	if g, _ := p.GetIssueItem("o", d.ID); g.DependsOn != c.ID {
		t.Fatal("不相干的依賴不受影響")
	}

	// 重開後還在
	st := reopenPG(t, p)
	if g, err := st.GetIssueItem("o", d.ID); err != nil || g.Seq != 4 {
		t.Fatal(g, err)
	}
	p = st.(*PG)

	// Issue 永久刪除:子項與序號列一起消失;垃圾桶不影響
	chk(t, p.DeleteIssue("o", iss2))
	if _, err := p.GetIssueItem("o", other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cascade", err)
	}
	if n := pgCount(t, p, `select count(*) from omixor_seq where scope = $1`, "item:"+iss2); n != 0 {
		t.Fatal("序號列也清", n)
	}
	// DeleteOrg 一併刪除
	chk(t, p.DeleteOrg("o"))
	if n := pgCount(t, p, `select count(*) from omixor_issue_items`); n != 0 {
		t.Fatal("DeleteOrg", n)
	}
}

// 序號自癒:序號列被清掉或落後時,新號碼接在現有最大 seq 之後(不撞唯一約束)
func TestIssueItemsSeqHeal(t *testing.T) {
	p, alice, iss, _, _ := itemFixture(t)
	for range 3 {
		_, err := p.CreateIssueItem("o", newItem(iss, alice, "x"))
		chk(t, err)
	}
	pgExec(t, p, `delete from omixor_seq where scope = $1`, "item:"+iss)
	n, err := p.CreateIssueItem("o", newItem(iss, alice, "y"))
	chk(t, err)
	if n.Seq != 4 {
		t.Fatal(n.Seq)
	}
}

func TestIssueItemsClaim(t *testing.T) {
	p, alice, iss, _, _ := itemFixture(t)
	bob, eng := lid(t, p, "o", "user:b"), lid(t, p, "o", "agent:eng")
	it, err := p.CreateIssueItem("o", newItem(iss, alice, "領取"))
	chk(t, err)
	until := time.Now().Add(10 * time.Minute)
	got, err := p.ClaimIssueItem("o", it.ID, bob, until)
	chk(t, err)
	if got.Status != model.ItemDoing || got.ClaimedBy != bob || got.LockedUntil == nil || got.LockedUntil.Sub(until).Abs() > 2*time.Second {
		t.Fatalf("%+v", got)
	}
	if _, err := p.ClaimIssueItem("o", it.ID, eng, until); !errors.Is(err, ErrLocked) {
		t.Fatal("別人持有有效租約要 ErrLocked", err)
	}
	if g, err := p.ClaimIssueItem("o", it.ID, bob, until.Add(time.Hour)); err != nil || g.LockedUntil.Sub(until.Add(time.Hour)).Abs() > 2*time.Second {
		t.Fatal("自己可以續租", g, err)
	}
	if _, err := p.ClaimIssueItem("o", "nope", bob, until); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := p.ClaimIssueItem("o", it.ID, "", until); !errors.Is(err, ErrBadRef) {
		t.Fatal("空領取者", err)
	}
	if _, err := p.ClaimIssueItem("o", it.ID, "usr_nobody", until); !errors.Is(err, ErrLocked) && !errors.Is(err, ErrBadRef) {
		t.Fatal(err)
	}
	// 放掉:不是持有者 ErrLocked;持有者放掉回 todo
	if err := p.ReleaseIssueItem("o", it.ID, eng); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	chk(t, p.ReleaseIssueItem("o", it.ID, bob))
	if g, _ := p.GetIssueItem("o", it.ID); g.Status != model.ItemTodo || g.ClaimedBy != "" || g.LockedUntil != nil {
		t.Fatalf("%+v", g)
	}
	if err := p.ReleaseIssueItem("o", it.ID, bob); !errors.Is(err, ErrStale) {
		t.Fatal("不是 doing 要 ErrStale", err)
	}
	if err := p.ReleaseIssueItem("o", "nope", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// 租約過期:別人可以領
	_, err = p.ClaimIssueItem("o", it.ID, bob, until)
	chk(t, err)
	g, _ := p.GetIssueItem("o", it.ID)
	past := model.Time{Time: time.Now().Add(-time.Minute).UTC()}
	g.LockedUntil, g.UpdatedAt = &past, model.Now()
	chk(t, p.UpdateIssueItem("o", g, model.ItemDoing))
	if g2, err := p.ClaimIssueItem("o", it.ID, eng, until); err != nil || g2.ClaimedBy != eng {
		t.Fatal("租約過期別人可以領", g2, err)
	}
	// 管理員放掉(actor 空)
	chk(t, p.ReleaseIssueItem("o", it.ID, ""))
	// blocked / done 不能領
	g, _ = p.GetIssueItem("o", it.ID)
	g.Status, g.BlockReason, g.WaitForKind, g.WaitFor = model.ItemBlocked, "等人", model.WaitUserDecision, "DEC-1"
	chk(t, p.UpdateIssueItem("o", g, model.ItemTodo))
	if _, err := p.ClaimIssueItem("o", it.ID, bob, until); !errors.Is(err, ErrLocked) {
		t.Fatal("blocked 不能領", err)
	}
	if g, _ := p.GetIssueItem("o", it.ID); g.WaitForKind != "user_decision" || g.WaitFor != "DEC-1" || g.BlockReason != "等人" {
		t.Fatalf("%+v", g)
	}

	// 競爭:同時領,只有一個成功
	r, err := p.CreateIssueItem("o", newItem(iss, alice, "搶"))
	chk(t, err)
	actors := []string{alice, bob, eng}
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN, lockedN := 0, 0
	for i := range 9 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.ClaimIssueItem("o", r.ID, actors[i%3], until)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				okN++
			case errors.Is(err, ErrLocked):
				lockedN++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	g, _ = p.GetIssueItem("o", r.ID)
	// 同一個 actor 的重複領取算續租,所以成功次數 >= 1;持有者只有一個
	if okN < 1 || okN+lockedN != 9 || g.ClaimedBy == "" {
		t.Fatal(okN, lockedN, g)
	}
}

// 結構:全新建立與舊資料庫(沒有子項表、沒有 omixor_agent_tasks.item_id)補建後一致;資料不受影響;外鍵都在
func TestIssueItemsMigrationStructure(t *testing.T) {
	fresh := fkPG(t, testPGBlank(t))
	want := structureFP(t, fresh)
	for _, k := range []string{"col omixor_issue_items.assignee", "col omixor_agent_tasks.item_id"} {
		if _, ok := want[k]; !ok {
			t.Fatal("指紋少了", k)
		}
	}
	for _, f := range NewTableActorFields {
		if ex, ok := fkValidated(t, fresh, f.Table, f.FK); !ex || !ok {
			t.Errorf("%s.%s 的外鍵 %s:存在 %v、已驗證 %v", f.Table, f.Column, f.FK, ex, ok)
		}
	}

	url := testPGBlank(t)
	p := fkPG(t, url)
	pgExec(t, p, `drop table omixor_issue_items`)
	pgExec(t, p, `alter table omixor_agent_tasks drop column item_id`)
	p.Close()
	for round := 1; round <= 2; round++ { // 第二次:已經補過,再啟動不動
		p = fkPG(t, url)
		if d := diffFP(structureFP(t, p), want); len(d) > 0 {
			t.Fatal("補建後的結構要和全新建立的一致", round, d)
		}
		for _, name := range []string{"fk_issue_items_assignee", "fk_issue_items_claimed_by", "fk_issue_items_created_by"} {
			if ex, ok := fkValidated(t, p, "omixor_issue_items", name); !ex || !ok {
				t.Fatalf("外鍵 %s:存在 %v、已驗證 %v", name, ex, ok)
			}
		}
		if round == 1 {
			mkOrgs(t, p, "o")
			at := model.Now()
			alice := lid(t, p, "o", "user:a")
			iss, err := p.CreateIssue("o", model.Issue{Title: "x", Status: "open", Priority: "normal", CreatedBy: alice, CreatedAt: at, UpdatedAt: at})
			chk(t, err)
			_, err = p.CreateIssueItem("o", newItem(iss.ID, alice, "存活"))
			chk(t, err)
		} else if n := pgCount(t, p, `select count(*) from omixor_issue_items where title = '存活'`); n != 1 {
			t.Fatal("再啟動資料還在", n)
		}
		p.Close()
	}
}
