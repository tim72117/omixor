package store

import (
	"errors"
	"testing"

	"omixor-core/model"
)

// 待決定 / 需要協助:編號 DEC-n 連號(omixor_seq,失敗的建立不佔號)、兩種 kind 共用、條件式更新(狀態對不上回 ErrStale)、
// 建立者與處理人是 actor id(不存在的 actor 回 ErrBadRef)、來源 Issue 是內部 id(不存在回 ErrBadRef;永久刪除 Issue 清成空)、
// 重開後還在、DeleteOrg 一併刪除(一致性測試的一項,見 conformance_test.go)
func suiteDecisions(t *testing.T, impl storeImpl) {
	st := impl.open(t)
	newOrg(t, st, "o", "p")
	at := mt("2026-01-02T03:04:05Z")
	alice, bob, eng := lid(t, st, "o", "user:a"), lid(t, st, "o", "user:b"), lid(t, st, "o", "agent:eng")
	iss, err := st.CreateIssue("o", model.Issue{Title: "來源", Status: "open", Priority: "normal", CreatedBy: alice, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	opts := []model.DecisionOption{{ID: "o1", Label: "A", Recommended: true}, {ID: "o2", Label: "B", Description: "說明"}}
	a, err := st.CreateDecision("o", model.Decision{Kind: "decision", Title: "選哪個", Body: "背景", Options: opts, Status: "open",
		SourceIssue: iss.ID, Blocks: []string{"ISS-9", "等憑證"}, CreatedBy: alice, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	b, err := st.CreateDecision("o", model.Decision{Kind: "help", Title: "請備份", Options: []model.DecisionOption{}, Status: "open",
		Blocks: []string{}, CreatedBy: eng, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if a.ID != "DEC-1" || b.ID != "DEC-2" {
		t.Fatal(a.ID, b.ID)
	}
	empty := model.Decision{Kind: "help", Title: "別的組織", Status: "open", Options: []model.DecisionOption{}, Blocks: []string{}, CreatedAt: at, UpdatedAt: at}
	empty.CreatedBy = lid(t, st, "p", "user:a")
	if c, _ := st.CreateDecision("p", empty); c.ID != "DEC-1" {
		t.Fatal("每個組織各自編號", c.ID)
	}
	bad := model.Decision{Kind: "help", Title: "x", Status: "open", Options: []model.DecisionOption{}, Blocks: []string{}, CreatedBy: "u_nobody", CreatedAt: at, UpdatedAt: at}
	if _, err := st.CreateDecision("o", bad); !errors.Is(err, ErrBadRef) {
		t.Fatal("建立者不是 actor 要 ErrBadRef", err)
	}
	bad.CreatedBy, bad.SourceIssue = alice, "no-such-issue"
	if _, err := st.CreateDecision("o", bad); !errors.Is(err, ErrBadRef) {
		t.Fatal("來源 Issue 不存在要 ErrBadRef", err)
	}
	// 失敗的建立不佔號:下一筆接著 DEC-3
	bad.SourceIssue = ""
	if c, err := st.CreateDecision("o", bad); err != nil || c.ID != "DEC-3" {
		t.Fatal(c.ID, err)
	}
	// 處理 b(help):done 附證據
	done := mtp("2026-01-02T05:00:00Z")
	b.Status, b.Answer, b.AnswerNote, b.Evidence, b.DecidedBy, b.DecidedAt, b.UpdatedAt = "decided", "done", "已備份", "https://x/1", bob, done, *done
	chk(t, st.UpdateDecision("o", b, "open"))
	if err := st.UpdateDecision("o", b, "open"); !errors.Is(err, ErrStale) {
		t.Fatal("狀態已經不是 open 要回 ErrStale", err)
	}
	if err := st.UpdateDecision("o", model.Decision{ID: "DEC-9"}, "open"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	_, err = st.GetDecision("o", "DEC-9")
	wantNotFound(t, "沒有的決定", err)
	_, err = st.GetDecision("o", "dec-1")
	wantNotFound(t, "id 大小寫要一致", err)
	_, err = st.GetDecision("p", "DEC-2")
	wantNotFound(t, "別的組織", err)

	st = impl.reopen(t, st)
	g, err := st.GetDecision("o", "DEC-2")
	chk(t, err)
	sameJSON(t, "處理後讀回", g, b)
	g, err = st.GetDecision("o", "DEC-1")
	chk(t, err)
	sameJSON(t, "含選項與 blocks 讀回", g, a)
	if g.DecidedBy != "" || g.DecidedAt != nil {
		t.Fatal("沒處理的處理人是空、沒有處理時間", g)
	}
	// reopen:清掉回答,處理人變 NULL
	b.Status, b.Answer, b.AnswerNote, b.Evidence, b.DecidedBy, b.DecidedAt = "open", "", "", "", "", nil
	chk(t, st.UpdateDecision("o", b, "decided"))
	if g, _ := st.GetDecision("o", "DEC-2"); g.DecidedBy != "" || g.DecidedAt != nil || g.Status != "open" {
		t.Fatalf("reopen 讀回:%+v", g)
	}
	l, err := st.ListDecisions("o")
	chk(t, err)
	if len(l) != 3 || l[0].ID != "DEC-1" || l[1].ID != "DEC-2" || l[2].ID != "DEC-3" {
		t.Fatal("舊的在前", l)
	}
	if l, err := st.ListDecisions("zz"); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("沒有決定要是 []:%#v %v", l, err)
	}
	// 來源 Issue 永久刪除:sourceIssue 清成空,決定還在
	chk(t, st.DeleteIssue("o", iss.ID))
	g, err = st.GetDecision("o", "DEC-1")
	chk(t, err)
	if g.SourceIssue != "" || g.Title != "選哪個" {
		t.Fatalf("來源 Issue 刪掉後 sourceIssue 要清空:%+v", g)
	}
	st = impl.reopen(t, st)
	if g, _ := st.GetDecision("o", "DEC-1"); g.SourceIssue != "" {
		t.Fatal("重開後仍是空的", g)
	}
	chk(t, st.DeleteOrg("o"))
	if l, _ := st.ListDecisions("o"); len(l) != 0 {
		t.Fatal("DeleteOrg 要一併刪除決定", l)
	}
	if l, _ := st.ListDecisions("p"); len(l) != 1 {
		t.Fatal("別的組織不受影響", l)
	}
}

// 結構:建立者 not null、處理人可為 NULL、兩條 actor 外鍵都在而且已驗證
func TestDecisionsTableStructure(t *testing.T) {
	fresh := fkPG(t, testPGBlank(t))
	for col, want := range map[string]string{"created_by": "NO", "decided_by": "YES"} {
		var nullable string
		chk(t, fresh.pool.QueryRow(bg(), `select is_nullable from information_schema.columns where table_schema = current_schema()
			and table_name = 'omixor_decisions' and column_name = $1`, col).Scan(&nullable))
		if nullable != want {
			t.Fatalf("%s is_nullable = %s,要 %s", col, nullable, want)
		}
	}
	for _, f := range NewTableActorFields {
		if ex, ok := fkValidated(t, fresh, f.Table, f.FK); !ex || !ok {
			t.Errorf("%s.%s 的外鍵 %s:存在 %v、已驗證 %v", f.Table, f.Column, f.FK, ex, ok)
		}
	}
}

// actor 被決定參照(建立者、處理人)時不能刪(restrict)
func TestDecisionsActorRestrict(t *testing.T) {
	st := testPG(t)
	newOrg(t, st, "o")
	at := mt("2026-01-02T03:04:05Z")
	a := lid(t, st, "o", "user:a")
	_, err := st.CreateDecision("o", model.Decision{Kind: "help", Title: "x", Options: []model.DecisionOption{}, Blocks: []string{}, Status: "open", CreatedBy: a, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	p := st.(*PG)
	if _, err := p.pool.Exec(bg(), `delete from omixor_actors where id = $1`, a); err == nil {
		t.Fatal("被決定參照的 actor 不能刪")
	}
}
