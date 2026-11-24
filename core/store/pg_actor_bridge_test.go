package store

// 過渡橋與 NULL 輔助(ISS-36 C-2 共用地基)
//   - NullID、IDOrNil、IDPtr:NULL 安全的讀寫
//   - 寫入:舊字串 → actor id(空字串 NULL / sys_legacy_unknown、同字串同 actor、本來就是 actor id 就是自己);讀取:轉回舊字串
//   - 欄位沒轉換(舊結構)時橋原樣寫字串

import (
	"context"
	"strings"
	"testing"

	"omixor-core/model"
)

func TestNullIDHelpers(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	mkOrgs(t, p, "o")
	c := context.Background()
	// 寫:空字串 → NULL;讀:NULL → ""
	pgExec(t, p, `insert into omixor_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at, assignee, deleted_by)
		values ('o', 'a', 1, 1, 't', 'open', 'normal', 'sys_omixor', now(), now(), $1, $2)`, IDOrNil(""), NullID(""))
	var as, del NullID
	chk(t, p.pool.QueryRow(c, `select assignee, deleted_by from omixor_issues where id = 'a'`).Scan(&as, &del))
	if as != "" || del != "" || as.String() != "" {
		t.Fatal("NULL 讀成空字串", as, del)
	}
	var n int
	chk(t, p.pool.QueryRow(c, `select count(*) from omixor_issues where assignee is null and deleted_by is null`).Scan(&n))
	if n != 1 {
		t.Fatal("寫入的空字串要是 NULL", n)
	}
	pgExec(t, p, `update omixor_issues set assignee = $1, deleted_by = $2 where id = 'a'`, IDOrNil("sys_chain"), NullID("sys_routine"))
	chk(t, p.pool.QueryRow(c, `select assignee, deleted_by from omixor_issues where id = 'a'`).Scan(&as, &del))
	if as != "sys_chain" || del != "sys_routine" {
		t.Fatal("有值原樣", as, del)
	}
	// 外鍵:不存在的 actor id、空字串(非 NULL)都被擋
	for _, v := range []any{"not-an-actor", ""} {
		if _, err := p.pool.Exec(c, `update omixor_issues set assignee = $1 where id = 'a'`, v); err == nil {
			t.Fatalf("外鍵要擋 %q", v)
		}
	}
	// text[] 的 NULL 元素
	ps := []*string{IDPtr(""), IDPtr("x")}
	if ps[0] != nil || ps[1] == nil || *ps[1] != "x" {
		t.Fatal("IDPtr")
	}
	var bad NullID
	if err := bad.Scan(5); err == nil {
		t.Fatal("不支援的型別要錯")
	}
}

// 寫入橋:舊字串 → actor id;讀取橋:actor id → 舊字串
func TestBridgeRoundTripAndResolve(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	mkOrgs(t, p, "o")
	saveU(t, p, "u_a", "a@x.com", "A")
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev", Active: true, CreatedAt: model.Now()}))
	w := p.newWho(context.Background())
	// 空字串:可為 NULL 的欄位 → nil;NOT NULL 的欄位 → sys_legacy_unknown
	if v, err := w.col("omixor_issues.assignee", "o", "  "); err != nil || v != nil {
		t.Fatal("空白 → NULL", v, err)
	}
	if v, err := w.col("omixor_issues.created_by", "o", ""); err != nil || v != "sys_legacy_unknown" {
		t.Fatal("NOT NULL 欄位的空字串 → sys_legacy_unknown", v, err)
	}
	// 同一個字串永遠同一個 actor;已經是 actor id 的就是自己
	a1, _ := w.col("omixor_issues.created_by", "o", "冒名")
	a2, _ := p.newWho(context.Background()).col("omixor_issues.deleted_by", "o", "冒名")
	if a1 != a2 || !strings.HasPrefix(a1.(string), "sys_legacy_") {
		t.Fatal("同字串同 actor", a1, a2)
	}
	if v, _ := w.col("omixor_issues.created_by", "o", a1.(string)); v != a1 {
		t.Fatal("actor id 對到自己", v)
	}
	// 登記過的使用者(email 大小寫不同)對到同一個 actor;角色對到角色 actor
	ua, _ := w.col("omixor_issues.created_by", "o", "A@X.com")
	ub, _ := w.col("omixor_issues.created_by", "o", "user:a@x.com")
	if ua != ub {
		t.Fatal("同一個使用者", ua, ub)
	}
	g, _ := p.GetAgent("o", "dev")
	if v, _ := w.col("omixor_issues.assignee", "o", "dev"); v != g.ActorID {
		t.Fatal("assignee 純角色 id 對到角色 actor", v, g.ActorID)
	}
	// 欄位 key 不認得
	if _, err := w.col("omixor_issues.nope", "o", "x"); err == nil {
		t.Fatal("不認得的欄位要錯")
	}
	// store 方法本身不再展開舊字串(一般路徑已拆掉):寫 actor id、讀回 actor id;空的 assignee 讀回空字串
	at := model.Now()
	i, err := p.CreateIssue("o", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: ua.(string), Assignee: "", CreatedAt: at, UpdatedAt: at,
		Comments: []model.Comment{{ID: "c", Author: g.ActorID, Text: "x", At: at}}})
	chk(t, err)
	got, err := p.GetIssue("o", i.ID)
	chk(t, err)
	if got.CreatedBy != ua.(string) {
		t.Fatal("建立者讀回 actor id", got.CreatedBy)
	}
	if got.Assignee != "" || got.DeletedBy != "" || got.Comments[0].Author != g.ActorID {
		t.Fatalf("assignee 空字串、留言作者角色 actor id:%+v", got)
	}
	i2, err := p.CreateIssue("o", model.Issue{Title: "t2", Status: "open", Priority: "normal", CreatedBy: g.ActorID, Assignee: g.ActorID, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	got2, _ := p.GetIssue("o", i2.ID)
	if got2.CreatedBy != g.ActorID || got2.Assignee != g.ActorID {
		t.Fatalf("傳 actor id 讀回 actor id:%+v", got2)
	}
	// 舊字串直接寫 store 會被外鍵擋(展開只留給遷移與匯入)
	if _, err := p.CreateIssue("o", model.Issue{Title: "t3", Status: "open", Priority: "normal", CreatedBy: "A@X.com", CreatedAt: at, UpdatedAt: at}); err == nil {
		t.Fatal("舊字串不再展開,外鍵要擋")
	}
}
