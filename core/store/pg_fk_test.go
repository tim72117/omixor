package store

// 只有資料庫才有的外鍵測試:刪除規則(cascade、set null、restrict)、既有資料有孤兒時補外鍵只告警不失敗、'' 專案與 '' ↔ NULL

import (
	"context"
	"errors"
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
	err := p.pool.QueryRow(c, `select true, convalidated from pg_constraint where conname = $1 and conrelid = $2::regclass`, name, table).Scan(&exists, &ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return exists, ok
}

// 所有外鍵都在、都已驗證(空資料庫)
func TestPGFKAllInstalled(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	for _, f := range fkSpecs() {
		if ex, ok := fkValidated(t, p, f.table, f.name); !ex || !ok {
			t.Fatalf("外鍵 %s:存在 %v、已驗證 %v", f.name, ex, ok)
		}
	}
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
	pgExec(t, p, `delete from omixor_issues where org = 'o'`)
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
	for _, tb := range []string{"omixor_issues", "omixor_agents", "omixor_agent_tasks", "omixor_agent_credentials", "omixor_docs", "omixor_projects",
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

// 頻道的 issue_id 可為 NULL(沒綁是 NULL,不是空字串);唯一索引:同組織同 Issue 只能一個頻道,沒綁的可以很多個
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
	p2 := p
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
	if g.IssueID != iss.ID {
		t.Fatal(g.IssueID)
	}
	// 沒綁的可以很多個
	for _, name := range []string{"x", "y"} {
		_, err := p2.CreateChannel("o", model.Channel{Name: name, CreatedAt: at, UpdatedAt: at})
		chk(t, err)
	}
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
