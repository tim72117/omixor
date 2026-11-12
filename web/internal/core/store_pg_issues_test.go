package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
)

// 需要 PostgreSQL(OPS_TEST_DATABASE_URL,見 store_pg_test.go 的 withDB);沒設就略過。

func pgCount(t *testing.T, url, q string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var n int
	if err := c.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPGIssuesTable(t *testing.T) {
	url := withDB(t)
	a := New()
	a.Start()
	a.Mu.Lock()
	org := a.CurrentOrgID()
	i1 := a.CreateIssue("第一筆", "內文", "high", []string{"a", "b"}, "src", "https://x", "mcp")
	i2 := a.CreateIssue("第二筆", "", "", nil, "", "", "tim")
	a.CommentIssue(i1.ID, "留言一", "agent:pm")
	a.CommentIssue(i1.ID, "留言二", "tim")
	a.UpdateIssue(i2.ID, "tim", func(i *OpsIssue) { i.Status = "closed" })
	i3 := a.CreateIssue("要刪的", "", "low", nil, "", "", "mcp")
	a.DeleteIssue(i3.ID)
	want := string(Marshal(a.Issues()))
	a.Mu.Unlock()

	if n := pgCount(t, url, `select count(*) from ops_issues where org = $1`, org); n != 2 {
		t.Fatalf("ops_issues 應該 2 列:%d", n)
	}
	if n := pgCount(t, url, `select count(*) from ops_issue_comments where org = $1 and issue_id = $2`, org, i1.ID); n != 2 {
		t.Fatalf("留言應該 2 列:%d", n)
	}
	if n := pgCount(t, url, `select count(*) from ops_issues where org = $1 and id = $2 and status = 'closed' and closed_at is not null`, org, i2.ID); n != 1 {
		t.Fatal("關閉狀態沒存")
	}
	if n := pgCount(t, url, `select count(*) from ops_documents where path like '%issues.json'`); n != 0 {
		t.Fatal("Issue 不該存成文件")
	}

	// 重開:內容與順序一樣
	b := New()
	b.Start()
	b.Mu.Lock()
	got := string(Marshal(b.Issues()))
	b.Mu.Unlock()
	if got != want {
		t.Fatalf("重開後不一樣:\n%s\n%s", got, want)
	}

	// 別的組織:WithOrgIssues 讀寫那個組織,目前組織不變
	b.Mu.Lock()
	b.WithOrgIssues("lab", func() { b.CreateIssue("lab 的", "", "", nil, "", "", "mcp") })
	n := len(b.Issues())
	b.Mu.Unlock()
	if n != 2 || pgCount(t, url, `select count(*) from ops_issues where org = 'lab'`) != 1 {
		t.Fatal("別的組織的 Issue 存錯地方")
	}
}

func TestPGIssueImportRerunAndMigration(t *testing.T) {
	url := withDB(t)
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "orgs", "default"), 0o755)
	_ = os.MkdirAll(filepath.Join(src, "orgs", "lab"), 0o755)
	file := `[
	 {"id":"ISS-3","title":"三","status":"closed","priority":"urgent","labels":["x"],"createdBy":"mcp","createdAt":"2026-11-10T00:00:03Z","updatedAt":"2026-11-10T00:00:04Z","closedAt":"2026-11-10T00:00:04Z",
	  "comments":[{"id":"c1","author":"tim","text":"嗨","at":"2026-11-10T00:00:05Z"}]},
	 {"id":"ISS-1","title":"一","createdBy":"tim","createdAt":"2026-11-10T00:00:01Z","updatedAt":"2026-11-10T00:00:01Z"}
	]`
	_ = os.WriteFile(filepath.Join(src, "orgs", "default", "issues.json"), []byte(file), 0o644)
	_ = os.WriteFile(filepath.Join(src, "orgs", "lab", "issues.json"), []byte(`[{"id":"ISS-1","title":"lab","createdBy":"mcp"}]`), 0o644)

	res, err := ImportIssuesPath(src, "", false)
	if err != nil || len(res) != 2 {
		t.Fatalf("%v %v", res, err)
	}
	// 重跑不重複
	res, err = ImportIssuesPath(src, "", false)
	if err != nil || res[0].Inserted != 0 || res[0].Skipped != 2 {
		t.Fatalf("重跑:%v %v", res, err)
	}
	if n := pgCount(t, url, `select count(*) from ops_issues`); n != 3 {
		t.Fatalf("應該 3 列:%d", n)
	}
	// -overwrite 用檔案覆蓋
	res, _ = ImportIssuesPath(filepath.Join(src, "orgs", "lab", "issues.json"), "lab", true)
	if res[0].Updated != 1 {
		t.Fatalf("%v", res)
	}

	a := New()
	a.Start()
	a.Mu.Lock()
	list := a.Issues()
	a.Mu.Unlock()
	if len(list) != 2 || list[0].ID != "ISS-3" || list[1].ID != "ISS-1" {
		t.Fatalf("順序照檔案:%v", list)
	}
	if c := list[0].Comments; len(c) != 1 || c[0].ID != "c1" || c[0].Author != "tim" || ISO(c[0].At.Time) != "2026-11-10T00:00:05Z" {
		t.Fatalf("留言:%v", c)
	}
	if list[0].ClosedAt == nil || list[0].Labels[0] != "x" || list[0].Priority != "urgent" {
		t.Fatalf("欄位:%+v", list[0])
	}
	// 新建立的排最前面,編號接著
	a.Mu.Lock()
	n := a.CreateIssue("新的", "", "", nil, "", "", "mcp")
	first := a.Issues()[0].ID
	a.Mu.Unlock()
	if n.ID != "ISS-4" || first != "ISS-4" {
		t.Fatal(n.ID, first)
	}

	// ops_documents 裡舊的 issues.json:表裡沒有這個組織時搬進來,舊文件改名保留
	_ = (pgBackend{}).Write("orgs/old/issues.json", []byte(`[{"id":"ISS-7","title":"舊的","createdBy":"mcp"}]`))
	a.Mu.Lock()
	var got []OpsIssue
	a.WithOrgIssues("old", func() { got = a.Issues() })
	a.Mu.Unlock()
	if len(got) != 1 || got[0].ID != "ISS-7" {
		t.Fatalf("沒搬:%v", got)
	}
	if (pgBackend{}).Exists("orgs/old/issues.json") || !(pgBackend{}).Exists("orgs/old/issues.json.migrated") {
		t.Fatal("舊文件要改名保留")
	}
}
