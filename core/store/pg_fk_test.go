package store

// 只有資料庫才有的外鍵測試:刪除規則(cascade、set null、restrict)、既有資料有孤兒時補外鍵只告警不失敗、'' 專案與 '' ↔ NULL

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/model"
)

// fkSchema 回傳一個全新、空的 database 的網址(測完刪掉);可以對同一個網址重複 NewPG(模擬重啟)
func fkSchema(t *testing.T) string {
	t.Helper()
	return testPGBlank(t)
}

func fkPG(t *testing.T, url string) *PG {
	t.Helper()
	p, err := NewPG(url)
	if err != nil {
		t.Fatal(err)
	}
	p.WaitFKValidate() // validate 在背景,等它做完才好檢查結果
	t.Cleanup(p.Close)
	return p
}

func pgExec(t *testing.T, p *PG, q string, args ...any) {
	t.Helper()
	if _, err := p.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(q, err)
	}
}

func pgCount(t *testing.T, p *PG, q string, args ...any) int {
	t.Helper()
	return count(t, p, q, args...)
}

// fkValidated 外鍵是否已驗證(不存在回 false, false)
func fkValidated(t *testing.T, p *PG, table, name string) (exists, ok bool) {
	t.Helper()
	c, cancel := ctx()
	defer cancel()
	ex, ok, err := fkExists(c, p.pool, table, name)
	if err != nil {
		t.Fatal(err)
	}
	return ex, ok
}

// 所有外鍵都在、都已驗證(空資料庫)
func TestPGFKAllInstalled(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	for _, f := range fkSpecs() {
		if ex, ok := fkValidated(t, p, f.table, f.name); !ex || !ok {
			t.Fatalf("外鍵 %s:存在 %v、已驗證 %v", f.name, ex, ok)
		}
	}
	rep, err := p.OrphanReport()
	chk(t, err)
	if len(rep) != len(fkSpecs()) {
		t.Fatal("每條外鍵一筆報告", len(rep))
	}
	for _, o := range rep {
		if o.Count != 0 {
			t.Fatal("空資料庫不該有孤兒", o)
		}
	}
	if len(OrphanSQL()) != len(fkSpecs()) {
		t.Fatal("OrphanSQL")
	}
	// 重跑遷移:冪等
	chk(t, p.migrateForeignKeys())
	chk(t, p.migrateForeignKeys())
}

// 刪除規則:cascade、restrict、set null、on update cascade
func TestPGFKDeleteRules(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	chk(t, p.CreateOrg(model.Org{ID: "keep", Name: "keep", CreatedAt: at}))
	for _, org := range []string{"o", "keep"} {
		makeTasks(t, p, org, 2)
		chk(t, p.SaveAgentCredential(model.AgentCredential{Org: org, AgentID: "dev", TaskID: "T-1", Hash: []byte(org + "1"), IssuedAt: at, ExpiresAt: at}))
		prj := makeProject(t, p, org)
		iss, err := p.CreateIssue(org, model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at})
		chk(t, err)
		ch, err := p.CreateChannel(org, model.Channel{Name: "c", IssueID: iss.ID, CreatedAt: at, UpdatedAt: at})
		chk(t, err)
		_, err = p.CreateChannelMessage(org, model.ChannelMessage{ChannelID: ch.ID, Author: lid(t, p, org, "user:a"), Text: "hi", At: at})
		chk(t, err)
		chk(t, p.SaveChannelRead(org, model.ChannelRead{ChannelID: ch.ID, Member: lid(t, p, org, "user:a"), LastRead: 1, At: at}))
		_, err = p.CreateDoc(org, model.CabinetDoc{Project: prj, Title: "d", Section: "s", Tags: []string{}, CreatedAt: at, UpdatedAt: at})
		chk(t, err)
		_, err = p.CreateDoc(org, model.CabinetDoc{Title: "未分", Section: "s", Tags: []string{}, CreatedAt: at, UpdatedAt: at})
		chk(t, err)
		lay := []model.CabinetSection{{ID: "a", Label: "甲", Tabs: []model.CabinetTab{{Label: "頁", Path: "/a/1"}}}}
		_, err = p.SaveCabinetLayout(org, prj, lay, nil)
		chk(t, err)
		_, err = p.SaveCabinetLayout(org, "", lay, nil)
		chk(t, err)
	}

	// 1. 刪任務 → 憑證跟著刪
	if n := pgCount(t, p, `select count(*) from omixor_agent_credentials where org = 'o'`); n != 1 {
		t.Fatal("憑證", n)
	}
	pgExec(t, p, `delete from omixor_agent_tasks where org = 'o' and id = 'T-1'`)
	if n := pgCount(t, p, `select count(*) from omixor_agent_credentials where org = 'o'`); n != 0 {
		t.Fatal("刪任務要連憑證", n)
	}
	if n := pgCount(t, p, `select count(*) from omixor_agent_credentials where org = 'keep'`); n != 1 {
		t.Fatal("別的組織不受影響", n)
	}

	// 2. 刪 Issue → 頻道的 issue_id 變 NULL(org 不能變 NULL:用 set null (issue_id))
	pgExec(t, p, `delete from ops_issues where org = 'o'`)
	if n := pgCount(t, p, `select count(*) from omixor_channels where org = 'o' and issue_id is null`); n != 1 {
		t.Fatal("頻道的 issue_id 要 NULL、頻道還在", n)
	}
	g, err := p.GetChannel("o", "CH-1")
	chk(t, err)
	if g.IssueID != "" {
		t.Fatal("讀回要 ''", g.IssueID)
	}

	// 3. 刪頻道 → 訊息與已讀跟著刪
	pgExec(t, p, `delete from omixor_channels where org = 'o'`)
	if a, b := pgCount(t, p, `select count(*) from omixor_channel_messages where org = 'o'`), pgCount(t, p, `select count(*) from omixor_channel_reads where org = 'o'`); a != 0 || b != 0 {
		t.Fatal("刪頻道要連訊息與已讀", a, b)
	}
	if n := pgCount(t, p, `select count(*) from omixor_channel_messages where org = 'keep'`); n != 1 {
		t.Fatal("別的組織的訊息", n)
	}

	// 4. 專案裡有文件:restrict;文件清掉才能刪,區塊與頁籤跟著刪
	if _, err := p.pool.Exec(context.Background(), `delete from omixor_projects where org = 'o'`); err == nil {
		t.Fatal("專案裡有文件不能刪")
	}
	if err := p.DeleteProject("o", "PRJ-1"); err == nil || !strings.Contains(err.Error(), "still referenced") {
		t.Fatal("DeleteProject 要回 ErrInUse", err)
	}
	if n := pgCount(t, p, `select count(*) from omixor_cabinet_sections where org = 'o' and project = 'PRJ-1'`); n != 1 {
		t.Fatal("被擋下後區塊還在(交易還原)", n)
	}
	pgExec(t, p, `delete from omixor_docs where org = 'o' and project = 'PRJ-1'`)
	chk(t, p.DeleteProject("o", "PRJ-1"))
	if a, b := pgCount(t, p, `select count(*) from omixor_cabinet_sections where org = 'o' and project = 'PRJ-1'`), pgCount(t, p, `select count(*) from omixor_cabinet_tabs where org = 'o' and project = 'PRJ-1'`); a != 0 || b != 0 {
		t.Fatal("刪專案要連區塊與頁籤", a, b)
	}
	// '' 專案(未分專案)的區塊、頁籤、文件不受影響
	if a, b := pgCount(t, p, `select count(*) from omixor_cabinet_sections where org = 'o' and project = ''`), pgCount(t, p, `select count(*) from omixor_cabinet_tabs where org = 'o' and project = ''`); a != 1 || b != 1 {
		t.Fatal("未分專案的結構", a, b)
	}
	if n := pgCount(t, p, `select count(*) from omixor_docs where org = 'o' and project = '' and project_ref is null`); n != 1 {
		t.Fatal("未分專案的文件 project_ref 要是 NULL", n)
	}

	// 5. 區塊 → 頁籤:刪區塊連頁籤;改區塊 id 頁籤跟著改(on update cascade)
	pgExec(t, p, `update omixor_cabinet_sections set id = 'a2' where org = 'keep' and project = '' and id = 'a'`)
	if n := pgCount(t, p, `select count(*) from omixor_cabinet_tabs where org = 'keep' and project = '' and section_id = 'a2'`); n != 1 {
		t.Fatal("改區塊 id 頁籤要跟著", n)
	}
	pgExec(t, p, `delete from omixor_cabinet_sections where org = 'keep' and project = '' and id = 'a2'`)
	if n := pgCount(t, p, `select count(*) from omixor_cabinet_tabs where org = 'keep' and project = ''`); n != 0 {
		t.Fatal("刪區塊要連頁籤", n)
	}
	if _, err := p.pool.Exec(context.Background(), `insert into omixor_cabinet_tabs (org, project, section_id, path, pos, label) values ('keep', '', 'none', '/x', 0, 'x')`); err == nil {
		t.Fatal("頁籤的區塊不存在不能寫")
	}

	// 6. 角色名下有任務:restrict(DeleteAgent 回 ErrInUse,見一致性測試);直接 SQL 也擋
	if _, err := p.pool.Exec(context.Background(), `delete from omixor_agents where org = 'o' and id = 'dev'`); err == nil {
		t.Fatal("名下有任務的角色不能刪")
	}

	// 7. 組織:直接刪組織列,所有資料跟著刪(cascade);別的組織不受影響
	// (omixor_org_members 本來就沒有 cascade,刪組織要先清成員;DeleteOrg 照舊手動清單,見 DeleteOrg)
	// 角色 → 任務是 restrict,和組織 cascade 並存:同一個語句裡一起刪不會被 restrict 擋
	pgExec(t, p, `delete from omixor_orgs where id = 'o'`)
	for _, tb := range []string{"ops_issues", "omixor_agents", "omixor_agent_tasks", "omixor_agent_credentials", "omixor_docs", "omixor_projects",
		"omixor_cabinet_layouts", "omixor_cabinet_sections", "omixor_cabinet_tabs", "omixor_channels", "omixor_channel_messages", "omixor_channel_reads"} {
		if n := pgCount(t, p, `select count(*) from `+tb+` where org = 'o'`); n != 0 {
			t.Fatal("刪組織要連", tb, n)
		}
	}
	for _, tb := range []string{"omixor_agents", "omixor_agent_tasks", "omixor_docs", "omixor_projects", "omixor_cabinet_layouts", "omixor_cabinet_sections", "omixor_cabinet_tabs"} {
		if n := pgCount(t, p, `select count(*) from `+tb+` where org = 'keep'`); n == 0 {
			t.Fatal("別的組織被刪了", tb)
		}
	}
	// 寫入不存在的組織:擋
	if _, err := p.CreateIssue("ghost", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at}); err == nil {
		t.Fatal("組織不存在不能寫 Issue")
	}
}

// 既有資料有孤兒:外鍵 not valid 加得上、validate 失敗只告警(服務照常起來)、新寫入照樣被擋、孤兒報告看得到;
// 孤兒處理掉之後下次啟動就驗證成功
func TestPGFKOrphansWarnOnly(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	makeAgent(t, p, "o", "dev")
	makeTasks(t, p, "o", 1)
	// 模擬舊資料庫:沒有外鍵,有孤兒(任務指向不存在的角色、憑證指向不存在的任務、文件指向不存在的專案、訊息指向不存在的頻道、
	// 組織不存在的 Issue)
	for _, q := range []string{
		`alter table omixor_agent_tasks drop constraint fk_agent_tasks_agent`,
		`alter table omixor_agent_credentials drop constraint fk_agent_credentials_task`,
		`alter table omixor_docs drop constraint fk_docs_project`,
		`alter table omixor_channel_messages drop constraint fk_channel_messages_channel`,
		`alter table ops_issues drop constraint fk_ops_issues_org`,
		`insert into omixor_agent_tasks (org, id, number, agent_id, title, status, created_at, updated_at) values ('o', 'T-90', 90, 'gone', 't', 'queued', now(), now())`,
		`insert into omixor_agent_tasks (org, id, number, agent_id, title, status, created_at, updated_at) values ('o', 'T-91', 91, 'gone2', 't', 'done', now(), now())`,
		`insert into omixor_agent_credentials (token_hash, org, agent_id, task_id, issued_at, expires_at) values ('\x01', 'o', 'dev', 'T-404', now(), now())`,
		`insert into omixor_docs (org, id, number, pos, title, section, created_at, updated_at, project) values ('o', 'DOC-90', 90, 90, 't', 's', now(), now(), 'PRJ-404')`,
		`insert into omixor_channel_messages (org, channel_id, id, number, author, text, at) values ('o', 'CH-404', 'M-1', 1, 'sys_omixor', 't', now())`,
		`insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at) values ('nobody', 'ISS-1', 1, 1, 't', 'open', 'normal', 'sys_omixor', now(), now())`,
	} {
		pgExec(t, p, q)
	}
	rep, err := p.OrphanReport()
	chk(t, err)
	got := map[string]int64{}
	for _, o := range rep {
		got[o.Constraint] = o.Count
	}
	for name, want := range map[string]int64{"fk_agent_tasks_agent": 2, "fk_agent_credentials_task": 1, "fk_docs_project": 1, "fk_channel_messages_channel": 1, "fk_ops_issues_org": 1, "fk_channel_reads_channel": 0} {
		if got[name] != want {
			t.Fatalf("孤兒報告 %s:%d,要 %d(%v)", name, got[name], want, got)
		}
	}

	// 「重啟」:補外鍵。log 要有孤兒筆數與警告;NewPG 不能失敗
	var buf bytes.Buffer
	oldOut := log.Writer()
	log.SetOutput(&buf)
	p2, err := NewPG(url)
	if p2 != nil {
		p2.WaitFKValidate()
	}
	log.SetOutput(oldOut)
	if err != nil {
		t.Fatal("有孤兒不能讓啟動失敗", err)
	}
	defer p2.Close()
	out := buf.String()
	for _, want := range []string{"外鍵孤兒檢查", "fk_agent_tasks_agent=2", "fk_agent_credentials_task=1", "fk_docs_project=1", "fk_channel_messages_channel=1", "fk_ops_issues_org=1",
		"警告:外鍵 fk_agent_tasks_agent 驗證失敗", "警告:外鍵 fk_docs_project 驗證失敗"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log 缺 %q\n%s", want, out)
		}
	}
	// 外鍵加上了,但還是 not valid;沒有孤兒的外鍵正常驗證
	if ex, ok := fkValidated(t, p2, "omixor_agent_tasks", "fk_agent_tasks_agent"); !ex || ok {
		t.Fatalf("有孤兒:要存在但未驗證(%v %v)", ex, ok)
	}
	if ex, ok := fkValidated(t, p2, "omixor_channel_reads", "fk_channel_reads_channel"); !ex || !ok {
		t.Fatalf("沒孤兒的要驗證成功(%v %v)", ex, ok)
	}
	// 不自動刪資料
	if n := pgCount(t, p2, `select count(*) from omixor_agent_tasks where agent_id in ('gone', 'gone2')`); n != 2 {
		t.Fatal("孤兒資料不能被刪", n)
	}
	// 新寫入照樣受約束
	if _, err := p2.CreateAgentTask("o", model.AgentTask{AgentID: "nope", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at}); err == nil {
		t.Fatal("not valid 的外鍵對新寫入也要生效")
	}
	// 孤兒處理掉,下次啟動驗證成功
	pgExec(t, p2, `delete from omixor_agent_tasks where agent_id in ('gone', 'gone2')`)
	pgExec(t, p2, `delete from omixor_agent_credentials where task_id = 'T-404'`)
	pgExec(t, p2, `delete from omixor_docs where project = 'PRJ-404'`)
	pgExec(t, p2, `delete from omixor_channel_messages where channel_id = 'CH-404'`)
	pgExec(t, p2, `delete from ops_issues where org = 'nobody'`)
	p3, err := NewPG(url)
	if err != nil {
		t.Fatal(err)
	}
	p3.WaitFKValidate()
	defer p3.Close()
	for _, f := range fkSpecs() {
		if ex, ok := fkValidated(t, p3, f.table, f.name); !ex || !ok {
			t.Fatalf("孤兒處理後 %s:存在 %v、已驗證 %v", f.name, ex, ok)
		}
	}
}

// 頻道的 issue_id 從舊的 not null default ” 遷移成可為 NULL,” 都變 NULL;唯一索引照舊(同組織同 Issue 只能一個頻道,沒綁的可以很多個)
func TestPGFKChannelIssueNullable(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	iss, err := p.CreateIssue("o", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	// 同一個 Issue 只能一個頻道
	_, err = p.CreateChannel("o", model.Channel{Name: "a", IssueID: iss.ID, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if _, err := p.CreateChannel("o", model.Channel{Name: "b", IssueID: iss.ID, CreatedAt: at, UpdatedAt: at}); err == nil {
		t.Fatal("同一個 Issue 不能有兩個頻道")
	}
	// 退回舊格式
	for _, q := range []string{
		`alter table omixor_channels drop constraint fk_channels_issue`,
		`update omixor_channels set issue_id = ''`,
		`alter table omixor_channels alter column issue_id set default ''`,
		`alter table omixor_channels alter column issue_id set not null`,
	} {
		pgExec(t, p, q)
	}
	p2 := fkPG(t, url) // 遷移
	var nullable string
	if err := p2.pool.QueryRow(context.Background(), `select is_nullable from information_schema.columns where table_schema = current_schema()
		and table_name = 'omixor_channels' and column_name = 'issue_id'`).Scan(&nullable); err != nil || nullable != "YES" {
		t.Fatal("issue_id 要可為 NULL", nullable, err)
	}
	if n := pgCount(t, p2, `select count(*) from omixor_channels where issue_id = ''`); n != 0 {
		t.Fatal("'' 都要變 NULL", n)
	}
	if ex, ok := fkValidated(t, p2, "omixor_channels", "fk_channels_issue"); !ex || !ok {
		t.Fatal("fk_channels_issue", ex, ok)
	}
	g, err := p2.GetChannel("o", "CH-1")
	chk(t, err)
	if g.IssueID != "" {
		t.Fatal(g.IssueID)
	}
	// 沒綁的可以很多個;綁同一個 Issue 照樣只能一個
	for _, name := range []string{"x", "y"} {
		_, err := p2.CreateChannel("o", model.Channel{Name: name, CreatedAt: at, UpdatedAt: at})
		chk(t, err)
	}
	c, err := p2.CreateChannel("o", model.Channel{Name: "z", IssueID: iss.ID, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if _, err := p2.CreateChannel("o", model.Channel{Name: "w", IssueID: iss.ID, CreatedAt: at, UpdatedAt: at}); err == nil {
		t.Fatal("同一個 Issue 不能有兩個頻道")
	}
	if c.IssueID != iss.ID {
		t.Fatal(c)
	}
}

// ---- 外鍵遷移:不被長交易擋、鎖逾時重試、validate 背景化 ----

// holdTx 開一個交易執行 sql(持有它的鎖),回傳釋放用的函式
func holdTx(t *testing.T, url, sql string) (release func()) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	var once bool
	release = func() {
		if once {
			return
		}
		once = true
		_ = tx.Rollback(ctx)
		conn.Close(ctx)
	}
	t.Cleanup(release)
	return release
}

func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s:%v 內沒做完(被鎖擋住)", what, d)
	}
}

// project_ref 已存在時不下 ALTER:有交易持有 omixor_docs 的讀鎖,遷移也不被擋
func TestPGFKMigrateNotBlockedByReadLock(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	release := holdTx(t, url, `select count(*) from omixor_docs`)
	defer release()
	within(t, 5*time.Second, "migrateForeignKeys", func() { chk(t, p.migrateForeignKeys()) })
	within(t, 5*time.Second, "一般讀取", func() { pgCount(t, p, `select count(*) from omixor_docs`) })
}

// 要加的外鍵拿不到鎖:lock_timeout 後重試有限次,只 log、不讓啟動失敗,鎖放掉後下次遷移補上
func TestPGFKMigrateLockTimeoutRetry(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	oldT, oldR, oldW := fkLockTimeout, fkRetries, fkRetryWait
	fkLockTimeout, fkRetries, fkRetryWait = "200ms", 2, 10*time.Millisecond
	defer func() { fkLockTimeout, fkRetries, fkRetryWait = oldT, oldR, oldW }()
	pgExec(t, p, `alter table omixor_docs drop constraint fk_docs_project`)
	release := holdTx(t, url, `lock table omixor_docs in row exclusive mode`)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	within(t, 10*time.Second, "migrateForeignKeys", func() { chk(t, p.migrateForeignKeys()) })
	if ex, _ := fkValidated(t, p, "omixor_docs", "fk_docs_project"); ex {
		t.Fatal("鎖被持有時不該加上")
	}
	if !strings.Contains(buf.String(), "fk_docs_project") {
		t.Fatal("要有告警 log", buf.String())
	}
	release()
	chk(t, p.migrateForeignKeys())
	p.WaitFKValidate()
	if ex, ok := fkValidated(t, p, "omixor_docs", "fk_docs_project"); !ex || !ok {
		t.Fatal("鎖放掉後要補上並驗證", ex, ok)
	}
}

// validate 在背景:migrateForeignKeys 在 validate 被擋住時照樣回傳;WaitFKValidate 等得到;Close 能取消卡住的 validate
func TestPGFKValidateBackground(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	pgExec(t, p, `alter table omixor_docs drop constraint fk_docs_project`)
	pgExec(t, p, `alter table omixor_docs add constraint fk_docs_project foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict not valid`)
	release := holdTx(t, url, `lock table omixor_docs in row exclusive mode`) // 擋住 validate(SHARE UPDATE EXCLUSIVE)
	within(t, 5*time.Second, "migrateForeignKeys 不等 validate", func() { chk(t, p.migrateForeignKeys()) })
	if _, ok := fkValidated(t, p, "omixor_docs", "fk_docs_project"); ok {
		t.Fatal("validate 還被擋著,不該已驗證")
	}
	release()
	within(t, 10*time.Second, "WaitFKValidate", p.WaitFKValidate)
	if ex, ok := fkValidated(t, p, "omixor_docs", "fk_docs_project"); !ex || !ok {
		t.Fatal("背景 validate 做完應已驗證", ex, ok)
	}
	// Close 取消卡住的背景 validate
	pgExec(t, p, `alter table omixor_docs drop constraint fk_docs_project`)
	pgExec(t, p, `alter table omixor_docs add constraint fk_docs_project foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict not valid`)
	holdTx(t, url, `lock table omixor_docs in row exclusive mode`)
	chk(t, p.migrateForeignKeys())
	within(t, 8*time.Second, "Close", p.Close)
}

// 逾時的 validate 不讓服務起不來:每條獨立逾時,只 log
func TestPGFKValidateTimeoutOnlyLogs(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	old := fkValidateTimeout
	fkValidateTimeout = 300 * time.Millisecond
	defer func() { fkValidateTimeout = old }()
	pgExec(t, p, `alter table omixor_docs drop constraint fk_docs_project`)
	pgExec(t, p, `alter table omixor_docs add constraint fk_docs_project foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict not valid`)
	holdTx(t, url, `lock table omixor_docs in row exclusive mode`)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	chk(t, p.migrateForeignKeys())
	within(t, 10*time.Second, "WaitFKValidate", p.WaitFKValidate)
	if !strings.Contains(buf.String(), "fk_docs_project") {
		t.Fatal("逾時要有告警 log", buf.String())
	}
}

// omixor_docs (org, project_ref) 有索引;DeleteIssue 不靠外鍵的 set null 也清頻道的 issue_id(PG 14 以下)
func TestPGDocsProjectRefIndexAndDeleteIssueWithoutFK(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	if n := pgCount(t, p, `select count(*) from pg_indexes where schemaname = current_schema() and indexname = 'omixor_docs_org_project_ref'`); n != 1 {
		t.Fatal("索引沒建", n)
	}
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	iss, err := p.CreateIssue("o", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	ch, err := p.CreateChannel("o", model.Channel{Name: "c", IssueID: iss.ID, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	pgExec(t, p, `alter table omixor_channels drop constraint fk_channels_issue`)
	chk(t, p.DeleteIssue("o", iss.ID))
	g, err := p.GetChannel("o", ch.ID)
	chk(t, err)
	if g.IssueID != "" {
		t.Fatal("沒有外鍵也要清掉頻道的 issue_id", g.IssueID)
	}
}

// 錯誤分類不看英文訊息:寫入 → ErrBadRef,刪除 → ErrInUse(由呼叫點決定)
func TestFKErrLocaleIndependent(t *testing.T) {
	pe := &pgconn.PgError{Code: "23503", ConstraintName: "fk_docs_project", TableName: "omixor_docs", Message: "違反外鍵", Detail: "其他語言的訊息"}
	if err := fkErr(pe); !errors.Is(err, ErrBadRef) || errors.Is(err, ErrInUse) {
		t.Fatal("寫入 → ErrBadRef", err)
	}
	if err := fkErrDelete(pe); !errors.Is(err, ErrInUse) {
		t.Fatal("刪除 → ErrInUse", err)
	}
	if err := fkErr(errors.New("x")); err == nil || errors.Is(err, ErrBadRef) {
		t.Fatal("其他錯誤不動", err)
	}
}
