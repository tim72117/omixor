package store

import (
	"errors"
	"fmt"
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

// 集合式遷移:很多筆時結果正確(留言、頻道、任務連結跟著改,連結的大小寫不同也改),重跑冪等,序號接得上
func TestPGIssueKeyMigrationBulk(t *testing.T) {
	p, at := fixOrg(t)
	const n = 600
	pgExec(t, p, `insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at)
		select 'o', case when g % 10 = 0 then 'gh-' || g else 'ISS-' || g end, case when g % 10 = 0 then 0 else g end, g, 't', 'open', 'normal', 'sys_omixor',
			'2026-01-01T00:00:00Z'::timestamptz + (g || ' seconds')::interval, now() from generate_series(1, $1::int) g`, n)
	pgExec(t, p, `insert into ops_issue_comments (org, issue_id, pos, id, author, text, at)
		select 'o', id, 0, 'c-' || id, 'sys_omixor', 'x', now() from ops_issues`)
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "dev", Active: true, CreatedAt: at}))
	pgExec(t, p, `insert into omixor_agent_tasks (org, id, number, agent_id, title, status, priority, created_at, updated_at)
		select 'o', 'T-' || g, g, 'dev', 't', 'queued', 'normal', now(), now() from generate_series(1, $1::int) g`, n)
	// 連結:大小寫與原 id 不同的也要改;指向不存在 Issue 的長得像編號的算孤兒
	pgExec(t, p, `insert into omixor_agent_task_links (org, task_id, pos, url)
		select 'o', 'T-' || g, 0, case when g % 2 = 0 then lower(case when g % 10 = 0 then 'gh-' || g else 'ISS-' || g end) else case when g % 10 = 0 then 'gh-' || g else 'ISS-' || g end end from generate_series(1, $1::int) g`, n)
	pgExec(t, p, `insert into omixor_agent_task_links (org, task_id, pos, url) values ('o', 'T-1', 1, 'ISS-99999'), ('o', 'T-1', 2, 'https://x')`)

	chk(t, p.migrateIssueKeys())
	if k := pgCount(t, p, `select count(*) from ops_issues where key = ''`); k != 0 {
		t.Fatal("都要有 key", k)
	}
	if k := pgCount(t, p, `select count(*) from omixor_agent_task_links l join ops_issues x on x.org = l.org and x.id = l.url`); k != n {
		t.Fatal("任務連結要全部換成新 id(含大小寫不同的)", k)
	}
	if k := pgCount(t, p, `select count(*) from ops_issue_comments c join ops_issues x on x.org = c.org and x.id = c.issue_id`); k != n {
		t.Fatal("留言要跟著", k)
	}
	if k := pgCount(t, p, `select count(*) from omixor_issue_keys`); k != n+n/10 {
		t.Fatal("別名:每張 key 一筆、舊格式 id 再一筆", k)
	}
	c, cancel := ctx()
	defer cancel()
	orphanCh, orphanLinks, err := issueRefOrphans(c, p.pool)
	chk(t, err)
	if orphanCh != 0 || orphanLinks != 1 {
		t.Fatal("孤兒數", orphanCh, orphanLinks)
	}
	x, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	if x.Key != fmt.Sprintf("ISS-%d", n+n/10) {
		t.Fatal("遷移後接著發號", x.Key)
	}
	// 號碼:保留的 ISS-<n> 不變,舊格式發的是新號碼,且沒有重複
	if k := pgCount(t, p, `select count(distinct number) from ops_issues where org = 'o'`); k != n+1 {
		t.Fatal("號碼不能重複", k)
	}
	before := pgCount(t, p, `select count(*) from ops_issues where key <> ''`)
	chk(t, p.migrateIssueKeys())
	if after := pgCount(t, p, `select count(*) from ops_issues where key <> ''`); after != before {
		t.Fatal("重跑不改", before, after)
	}
}

// 遷移:ISS-<n> 的 key 已經被別名占用(指向別的 Issue)時,不搶:這一筆改發新號碼
func TestPGIssueKeyMigrationSkipsTakenAlias(t *testing.T) {
	p, at := fixOrg(t)
	old, err := p.CreateIssue("o", keyIssue(at)) // ISS-1
	chk(t, err)
	chk(t, p.TrashIssue2(old.ID))
	pgExec(t, p, `insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at)
		values ('o', 'ISS-2', 2, 9, 't', 'open', 'normal', 'sys_omixor', now(), now())`)
	pgExec(t, p, `insert into omixor_issue_keys (org, key, issue_id, created_at) values ('o', 'ISS-2', $1, now())`, old.ID) // 別名 ISS-2 指向別張
	chk(t, p.migrateIssueKeys())
	l, err := p.ListIssues("o")
	chk(t, err)
	for _, i := range l {
		if i.Key == "ISS-2" {
			t.Fatal("ISS-2 被別名占用,不能搶", i)
		}
	}
	if id, err := p.ResolveIssueID("o", "ISS-2"); err != nil || id != old.ID {
		t.Fatal("別名 ISS-2 仍指向原本的", id, err)
	}
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

// 舊的 (org, number) 兩欄索引還在(遷移被跳過、逾時):migrateSeq 先建新索引再換掉舊的,專案內 Issue 不撞
func TestPGSeqReplacesTwoColumnIndex(t *testing.T) {
	p, at := fixOrg(t)
	pgExec(t, p, `drop index ops_issues_org_number`)
	pgExec(t, p, `create unique index ops_issues_org_number on ops_issues (org, number) where number > 0`)
	chk(t, p.migrateSeq())
	if n := pgCount(t, p, `select count(*) from pg_index where indexrelid = to_regclass('ops_issues_org_number') and indnkeyatts = 3 and indisvalid and indisunique and indpred is not null`); n != 1 {
		t.Fatal("要換成 (org, project, number) 的有效部分唯一索引", n)
	}
	if n := pgCount(t, p, `select count(*) from pg_class where relname = 'ops_issues_org_number_new'`); n != 0 {
		t.Fatal("暫時的索引名要改回來")
	}
	chk(t, p.migrateSeq()) // 冪等
	a, err := p.CreateProject("o", model.Project{Name: "a", Key: "AAA", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	b, err := p.CreateProject("o", model.Project{Name: "b", Key: "BBB", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	for _, pr := range []string{"", a.ID, b.ID} {
		x := keyIssue(at)
		x.Project = pr
		if _, err := p.CreateIssue("o", x); err != nil {
			t.Fatal("各範圍的 1 號不能撞索引:", err)
		}
	}
	// 同範圍的重複還是擋(索引有效)
	if _, err := p.pool.Exec(t.Context(), `insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at, project)
		values ('o', 'x1', 1, 99, 't', 'open', 'normal', 'a', now(), now(), $1)`, a.ID); !isUniqueViolation(err) {
		t.Fatal("同範圍同號碼要被擋", err)
	}
}

// 兩欄索引 + 啟動時 key 遷移被跳過:NewPG 之後專案內建 Issue 不撞(整合:整個啟動流程)
func TestPGStartupWithOldIndexProjectIssue(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	i, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	if i.Key != "ISS-1" {
		t.Fatal(i.Key)
	}
	pgExec(t, p, `drop index ops_issues_org_number`)
	pgExec(t, p, `create unique index ops_issues_org_number on ops_issues (org, number) where number > 0`)
	p.Close()
	p2 := fkPG(t, url)
	pr, err := p2.CreateProject("o", model.Project{Name: "a", Key: "AAA", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	x := keyIssue(at)
	x.Project = pr.ID
	if y, err := p2.CreateIssue("o", x); err != nil || y.Key != "AAA-1" {
		t.Fatal("專案內建 Issue 不得撞舊索引", y.Key, err)
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
