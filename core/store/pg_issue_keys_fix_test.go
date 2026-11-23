package store

import (
	"errors"
	"strings"
	"testing"

	"omixor-core/model"
)

// ISS-34 審查後的修正:集合式遷移、別名不被搶、索引換新、匯入前導零、UUID 查詢走索引、專案 Issue 計數

func fixOrg(t *testing.T) (*PG, model.Time) {
	t.Helper()
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	return p, at
}

// TrashIssue2 測試用:把 Issue 丟進垃圾桶(簡寫)
func (p *PG) TrashIssue2(id string) error {
	_, err := p.TrashIssue("o", id, "sys_omixor", mt("2026-01-03T00:00:00Z"))
	return err
}

// 匯入的舊格式 id gh-7 在別名表(GH-7);後來建專案代號 GH,裡面建到第 7 張:跳過 GH-7,別名仍指向原本的 Issue
func TestPGProjectKeyDoesNotStealImportedAlias(t *testing.T) {
	p, at := fixOrg(t)
	a := keyIssue(at)
	a.ID = "gh-7"
	_, err := p.ImportIssues("o", []model.Issue{a})
	chk(t, err)
	orig, err := p.ResolveIssueID("o", "GH-7")
	chk(t, err)
	gh, err := p.CreateProject("o", model.Project{Name: "gh", Key: "GH", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	var keys []string
	for range 8 {
		x := keyIssue(at)
		x.Project = gh.ID
		i, err := p.CreateIssue("o", x)
		chk(t, err)
		keys = append(keys, i.Key)
	}
	if got := strings.Join(keys, " "); got != "GH-1 GH-2 GH-3 GH-4 GH-5 GH-6 GH-8 GH-9" {
		t.Fatal("要跳過 GH-7:", got)
	}
	if id, err := p.ResolveIssueID("o", "gh-7"); err != nil || id != orig {
		t.Fatal("別名 GH-7 不能被搶走", id, err)
	}
}

// 專案 A 的 Issue 搬走後別名 APP-1 還在;之後別的專案沿用代號 APP 建 APP-1:跳號
func TestPGProjectKeyReuseSkipsMovedAlias(t *testing.T) {
	p, at := fixOrg(t)
	a, err := p.CreateProject("o", model.Project{Name: "a", Key: "APP", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	x := keyIssue(at)
	x.Project = a.ID
	moved, err := p.CreateIssue("o", x) // APP-1
	chk(t, err)
	if _, _, err := p.MoveIssue("o", moved.ID, "", 0, at); err != nil {
		t.Fatal(err)
	}
	a.Key = ""
	chk(t, p.UpdateProject("o", a))
	b, err := p.CreateProject("o", model.Project{Name: "b", Key: "APP", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	y := keyIssue(at)
	y.Project = b.ID
	i, err := p.CreateIssue("o", y)
	chk(t, err)
	if i.Key != "APP-2" {
		t.Fatal("APP-1 是搬走 Issue 的別名,要跳號:", i.Key)
	}
	if id, err := p.ResolveIssueID("o", "APP-1"); err != nil || id != moved.ID {
		t.Fatal("APP-1 仍指向搬走的 Issue", id, err)
	}
	// 搬 Issue 進 b 也一樣跳過被占用的號碼
	z := keyIssue(at)
	z, err = p.CreateIssue("o", z)
	chk(t, err)
	m, _, err := p.MoveIssue("o", z.ID, b.ID, 0, at)
	chk(t, err)
	if m.Key != "APP-3" {
		t.Fatal("搬進專案的號碼", m.Key)
	}
}

// addIssueKey:目前的 key 不能搶別張 Issue 的別名;指向同一張沒事
func TestPGAddIssueKeyNoSteal(t *testing.T) {
	p, at := fixOrg(t)
	i1, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	i2, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	chk(t, err)
	defer tx.Rollback(c)
	if err := addIssueKey(c, tx, "o", i1.Key, i2.ID, true); !errors.Is(err, ErrIssueKeyTaken) {
		t.Fatal("不能搶", err)
	}
	chk(t, addIssueKey(c, tx, "o", i1.Key, i1.ID, true))
	chk(t, addIssueKey(c, tx, "o", i1.Key, i2.ID, false)) // 舊的:已經有就不動
}

func TestPGProjectKeyAliasTaken(t *testing.T) {
	p, at := fixOrg(t)
	if k, err := p.ProjectKeyAliasTaken("o", "GH"); err != nil || k != "" {
		t.Fatal("沒有別名", k, err)
	}
	a := keyIssue(at)
	a.ID = "gh-7"
	_, err := p.ImportIssues("o", []model.Issue{a})
	chk(t, err)
	if k, err := p.ProjectKeyAliasTaken("o", "gh"); err != nil || k != "GH-7" {
		t.Fatal("舊編號別名要擋", k, err)
	}
	if k, err := p.ProjectKeyAliasTaken("o", "ISS"); err != nil || k != "" {
		t.Fatal("目前的 key 不算(Issue 自己的 key)", k, err)
	}
	if k, err := p.ProjectKeyAliasTaken("o", "G"); err != nil || k != "" {
		t.Fatal("前綴要整段比對", k, err)
	}
}

// 匯入:key 要標準寫法(ISS-05 擋,400);號碼撞到現有 Issue(寫法不同的 key)是 409 ImportError,不是原始 SQL 錯誤
func TestPGImportKeyLeadingZero(t *testing.T) {
	p, at := fixOrg(t)
	mk := func(key string) model.Issue { x := keyIssue(at); x.Key = key; return x }
	_, err := p.ImportIssues("o", []model.Issue{mk("ISS-5"), mk("ISS-05")})
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Code != 400 {
		t.Fatal("前導零要 400", err)
	}
	if n := pgCount(t, p, `select count(*) from ops_issues`); n != 0 {
		t.Fatal("整批不寫入", n)
	}
	// 組織裡已經有寫法不同的 ISS-05(舊資料,number = 5):匯入 ISS-5 撞號碼 → 409
	pgExec(t, p, `insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at, key)
		values ('o', '0190aaaa-0000-7000-8000-000000000001', 5, 1, 't', 'open', 'normal', 'sys_omixor', now(), now(), 'ISS-05')`)
	_, err = p.MergeIssues("o", []model.Issue{mk("ISS-5")}, false)
	if !errors.As(err, &ie) || ie.Code != 409 {
		t.Fatal("撞號碼要 409 ImportError", err)
	}
	if strings.Contains(err.Error(), "duplicate key value") || strings.Contains(err.Error(), "SQLSTATE") {
		t.Fatal("不能外洩原始 SQL 錯誤", err)
	}
}

// issueRef 用 UUID 查詢:不分大小寫、前後空白都找得到,而且走 (org, id) 主鍵索引(不是 lower(id) 全表掃)
func TestPGIssueRefUUIDUsesIndex(t *testing.T) {
	p, at := fixOrg(t)
	i, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	for _, ref := range []string{i.ID, strings.ToUpper(i.ID), "  " + i.ID + " "} {
		g, err := p.GetIssue("o", ref)
		if err != nil || g.ID != i.ID {
			t.Fatalf("以 %q 查:%v", ref, err)
		}
	}
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	chk(t, err)
	defer tx.Rollback(c)
	// 小表上計畫器隨便挑一條索引:把另一條 (org, pos) 在交易裡拿掉(結束時還原),剩下就要走主鍵
	if _, err := tx.Exec(c, `set local enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(c, `drop index ops_issues_org_pos`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(c, `explain select id from ops_issues where org = $1 and id = `+issueRef(2), "o", i.ID)
	chk(t, err)
	var plan []string
	for rows.Next() {
		var l string
		chk(t, rows.Scan(&l))
		plan = append(plan, l)
	}
	rows.Close()
	all := strings.Join(plan, "\n")
	if !strings.Contains(all, "ops_issues_pkey") || !strings.Contains(all, "AND (id = ") || strings.Contains(all, "lower(id)") {
		t.Fatal("要走主鍵索引:\n" + all)
	}
}

// CountProjectIssues 含垃圾桶
func TestPGCountProjectIssues(t *testing.T) {
	p, at := fixOrg(t)
	a, err := p.CreateProject("o", model.Project{Name: "a", Key: "AAA", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if n, err := p.CountProjectIssues("o", a.ID); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	var last model.Issue
	for range 3 {
		x := keyIssue(at)
		x.Project = a.ID
		last, err = p.CreateIssue("o", x)
		chk(t, err)
	}
	_, err = p.CreateIssue("o", keyIssue(at)) // 組織層級的不算
	chk(t, err)
	chk(t, p.TrashIssue2(last.ID))
	if n, err := p.CountProjectIssues("o", a.ID); err != nil || n != 3 {
		t.Fatal("含垃圾桶", n, err)
	}
}
