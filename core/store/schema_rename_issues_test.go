package store

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"omixor-core/model"
)

// 步驟 3(rename_issues_tables,schema_rename_issues.go)的測試:舊名 → 新名、資料與外鍵保留、冪等、全新安裝沒有 ops_ 物件

// legacyOpsObjects 目前 schema 裡名字以 ops_ 開頭(或約束名含 _ops_)的表、索引、約束(空 = 沒有舊名殘留)
func legacyOpsObjects(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		select 'relation ' || c.relname from pg_class c where c.relnamespace = current_schema()::regnamespace and c.relname like 'ops\_%' or c.relname like 'fk\_ops\_%'
		union all
		select 'constraint ' || conname from pg_constraint where connamespace = current_schema()::regnamespace and (conname like 'ops\_%' or conname like 'fk\_ops\_%')
		order by 1`)
	chk(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		chk(t, rows.Scan(&s))
		out = append(out, s)
	}
	chk(t, rows.Err())
	return out
}

// 全新安裝只有新名字
func TestFreshInstallHasNoLegacyOpsNames(t *testing.T) {
	pool, _ := stepDB(t)
	if got := legacyOpsObjects(t, pool); len(got) != 0 {
		t.Fatalf("全新資料庫不該有 ops_ 開頭的物件:%v", got)
	}
	for _, name := range []string{"omixor_issues", "omixor_issue_comments"} {
		if !tableExists(t, pool, name) {
			t.Errorf("缺 %s", name)
		}
	}
}

// 舊結構(baseline,表叫 ops_issues)+ 步驟 2 + 有資料 → 啟動(步驟 3)→ 新名字、資料與外鍵都在、新增 Issue 與留言、刪除連帶清留言
func TestRenameIssuesTablesUpgradeKeepsData(t *testing.T) {
	url := testPGBlank(t)
	c := context.Background()
	pool, err := pgxpool.New(c, url)
	chk(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(c, baselineSchemaA+baselineSchemaB+baselineFKSQL)
	chk(t, err)
	chk(t, stampBaseline(c, pool))
	chk(t, runSteps(c, pool, schemaSteps[:1])) // 只套用步驟 2(commits),停在改名之前
	if maxVersion(t, pool) != 2 || !tableExists(t, pool, "ops_issues") || tableExists(t, pool, "omixor_issues") {
		t.Fatalf("前置:應是版本 2、表還叫 ops_issues(max=%d)", maxVersion(t, pool))
	}
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, q := range []string{
		`insert into omixor_orgs (id, name) values ('o', 'o')`,
		`insert into omixor_actors (id, kind, name, handle) values ('usr_u', 'user', 'U', 'u')`,
		`insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at, key)
			values ('o', 'i1', 1, 1, 'old issue', 'open', 'normal', 'usr_u', now(), now(), 'ISS-1')`,
		`insert into ops_issue_comments (org, issue_id, pos, id, author, text, at) values ('o', 'i1', 0, 'c1', 'usr_u', 'hello', now())`,
		`insert into omixor_issue_keys (org, key, issue_id) values ('o', 'ISS-1', 'i1')`,
		`insert into omixor_commits (org, repo, sha) values ('o', 'r', '` + sha + `')`,
		`insert into omixor_issue_commits (org, issue_id, repo, sha, kind, origin) values ('o', 'i1', 'r', '` + sha + `', 'fixes', 'manual')`,
	} {
		if _, err := pool.Exec(c, q); err != nil {
			t.Fatalf("%s:%v", q, err)
		}
	}

	p, err := NewPG(url) // 啟動:只剩步驟 3
	chk(t, err)
	t.Cleanup(p.Close)
	if got := legacyOpsObjects(t, p.root); len(got) != 0 {
		t.Fatalf("改名後不該有 ops_ 物件:%v", got)
	}
	if tableExists(t, p.root, "ops_issues") || tableExists(t, p.root, "ops_issue_comments") {
		t.Fatal("舊表名還在")
	}
	if v := maxVersion(t, p.root); v != knownSchemaVersion(schemaSteps) {
		t.Fatalf("版本應到 %d:%d", knownSchemaVersion(schemaSteps), v)
	}
	// 資料還在
	for q, want := range map[string]int{
		`select count(*) from omixor_issues where id = 'i1' and title = 'old issue' and key = 'ISS-1'`: 1,
		`select count(*) from omixor_issue_comments where issue_id = 'i1' and text = 'hello'`:          1,
		`select count(*) from omixor_issue_keys`:                                                       1,
		`select count(*) from omixor_issue_commits where issue_id = 'i1'`:                              1,
	} {
		if n := scanInt(t, p.root, q); n != want {
			t.Errorf("%s = %d,要 %d", q, n, want)
		}
	}
	// 其他表的外鍵跟著指到新表
	for _, fk := range [][2]string{
		{"omixor_issue_commits", "omixor_issue_commits_org_issue_id_fkey"},
		{"omixor_issue_keys", "omixor_issue_keys_org_issue_id_fkey"},
		{"omixor_channels", "fk_channels_issue"},
		{"omixor_issue_comments", "omixor_issue_comments_org_issue_id_fkey"},
	} {
		if n := scanInt(t, p.root, `select count(*) from pg_constraint where conrelid = $1::regclass and conname = $2 and confrelid = 'omixor_issues'::regclass`, fk[0], fk[1]); n != 1 {
			t.Errorf("外鍵 %s.%s 應指向 omixor_issues", fk[0], fk[1])
		}
	}
	// 發號、新增、留言、刪除連帶都正常
	at := mt("2026-01-02T03:04:05Z")
	x := keyIssue(at)
	x.CreatedBy = "usr_u"
	i2, err := p.CreateIssue("o", x)
	chk(t, err)
	if i2.Key != "ISS-2" {
		t.Errorf("接續發號應是 ISS-2:%s", i2.Key)
	}
	_, err = p.AddIssueComment("o", i2.ID, model.Comment{ID: "c2", Author: "usr_u", Text: "new comment", At: at})
	chk(t, err)
	chk(t, p.DeleteIssue("o", "i1"))
	if n := scanInt(t, p.root, `select count(*) from omixor_issue_comments where issue_id = 'i1'`) + scanInt(t, p.root, `select count(*) from omixor_issue_commits where issue_id = 'i1'`); n != 0 {
		t.Errorf("刪除 Issue 要連帶刪留言與 commit 連結:%d", n)
	}
	// 冪等:再跑一次改名 SQL 不報錯、不改變結構
	before := snapshotOf(t, p)
	_, err = p.root.Exec(c, renameIssuesSQL)
	chk(t, err)
	_, err = p.root.Exec(c, renameIssuesSQL)
	chk(t, err)
	if after := snapshotOf(t, p); strings.Join(after.Constraints, "\n") != strings.Join(before.Constraints, "\n") || strings.Join(after.Indexes, "\n") != strings.Join(before.Indexes, "\n") {
		t.Error("重跑改名 SQL 不該改變結構")
	}
}

// 新舊名字同時存在:報錯、不覆蓋(交易回滾,結構不變)
func TestRenameIssuesTablesRefusesWhenBothExist(t *testing.T) {
	pool, _ := stepDB(t)
	c := context.Background()
	_, err := pool.Exec(c, `create table ops_issues (x int)`)
	chk(t, err)
	_, err = pool.Exec(c, renameIssuesSQL)
	if err == nil || !strings.Contains(err.Error(), "both exist") {
		t.Fatalf("新舊同時存在要報錯:%v", err)
	}
	if !tableExists(t, pool, "ops_issues") || !tableExists(t, pool, "omixor_issues") {
		t.Error("報錯時兩張表都要保持原樣")
	}
}
