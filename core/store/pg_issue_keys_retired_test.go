package store

import (
	"testing"

	"omixor-core/model"
)

// 舊版永久刪除 Issue、用 max+1 發號留下的孤兒參照(頻道、任務連結還寫著已刪掉的 ISS-<n>):
// 遷移後新 Issue 不能再拿到這些 key(否則舊參照會看起來指向不相干的新 Issue)

func retiredKeys(t *testing.T, p *PG) map[string]string {
	t.Helper()
	rows, err := p.pool.Query(bg(), `select key, reason from omixor_issue_keys_retired where org = 'o'`)
	chk(t, err)
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, r string
		chk(t, rows.Scan(&k, &r))
		m[k] = r
	}
	chk(t, rows.Err())
	return m
}

func createKeys(t *testing.T, p *PG, at model.Time, project string, n int) []string {
	t.Helper()
	var out []string
	for range n {
		i := keyIssue(at)
		i.Project = project
		x, err := p.CreateIssue("o", i)
		chk(t, err)
		out = append(out, x.Key)
	}
	return out
}

// 舊結構(ISS-1..ISS-10,ISS-11 已被刪掉但頻道、任務連結還指著它,含小寫、前導零與專案代號形狀)→ 重啟跑完整遷移 → 新 Issue 是 ISS-12
func TestPGIssueOrphanRefsSkippedAfterMigration(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	// 舊資料:key 是空字串、id 是 ISS-<n>;舊版沒有序號表,也沒有頻道的外鍵
	pgExec(t, p, `insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at)
		select 'o', 'ISS-' || g, g, g, 't', 'open', 'normal', 'sys_omixor', '2026-01-01T00:00:00Z'::timestamptz + (g || ' seconds')::interval, now()
		from generate_series(1, 10) g`)
	pgExec(t, p, `delete from omixor_seq`)
	pgExec(t, p, `alter table omixor_channels drop constraint fk_channels_issue`)
	ch, err := p.CreateChannel("o", model.Channel{Name: "c", IssueID: "ISS-11", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	ch2, err := p.CreateChannel("o", model.Channel{Name: "c2", IssueID: "ISS-3", CreatedAt: at, UpdatedAt: at}) // 不是孤兒
	chk(t, err)
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "dev", Active: true, CreatedAt: at}))
	links := []string{"ISS-11", "iss-11", "iss-014", "ABC-2", "DOC-99", "T-3", "https://x/ISS-20", "ISS-5"}
	task, err := p.CreateAgentTask("o", model.AgentTask{AgentID: "dev", Title: "x", Status: "queued", Priority: "normal",
		Links: links, CreatedAt: at, UpdatedAt: at})
	chk(t, err)

	p2 := fkPG(t, url) // 重啟:key 遷移、序號、孤兒 key 保留
	if k := pgCount(t, p2, `select count(*) from ops_issues where key = ''`); k != 0 {
		t.Fatal("key 遷移要做完", k)
	}
	got := retiredKeys(t, p2)
	want := map[string]string{"ISS-11": "orphan-ref", "ISS-14": "orphan-ref", "ABC-2": "orphan-ref"}
	if len(got) != len(want) {
		t.Fatalf("退役的 key:%v,要 %v", got, want)
	}
	for k, r := range want {
		if got[k] != r {
			t.Fatalf("退役的 key:%v,要 %v", got, want)
		}
	}
	if keys := createKeys(t, p2, at, "", 4); keys[0] != "ISS-12" || keys[1] != "ISS-13" || keys[2] != "ISS-15" || keys[3] != "ISS-16" {
		t.Fatal("新 Issue 要跳過孤兒參照的號碼", keys)
	}
	// 專案代號形狀的孤兒:之後有專案用這個代號,發號一樣跳過
	abc, err := p2.CreateProject("o", model.Project{Name: "abc", Key: "ABC", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if keys := createKeys(t, p2, at, abc.ID, 2); keys[0] != "ABC-1" || keys[1] != "ABC-3" {
		t.Fatal("專案的孤兒號碼也要跳過", keys)
	}
	// 使用者資料不動:頻道、任務連結原樣
	g, err := p2.GetChannel("o", ch.ID)
	chk(t, err)
	if g.IssueID != "ISS-11" {
		t.Fatal("孤兒頻道不改", g.IssueID)
	}
	g2, err := p2.GetChannel("o", ch2.ID)
	chk(t, err)
	if !model.IsUUID(g2.IssueID) {
		t.Fatal("非孤兒的頻道照樣換成新 id", g2.IssueID)
	}
	if n := pgCount(t, p2, `select count(*) from omixor_agent_task_links where org = 'o' and task_id = $1 and url = any($2)`, task.ID, links[:7]); n != 7 {
		t.Fatal("孤兒連結不改", n)
	}
	// 冪等:再跑一次不多記
	chk(t, p2.reserveOrphanIssueKeys())
	if n := len(retiredKeys(t, p2)); n != 3 {
		t.Fatal("重跑不多記", n)
	}
}

// 已經跑過 key 遷移的資料庫(修正前遷移的):序號表停在 10,孤兒參照指著 ISS-11 → 重啟後新 Issue 是 ISS-12
func TestPGIssueOrphanRefsSkippedOnMigratedDB(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	keys := createKeys(t, p, at, "", 11)
	pgExec(t, p, `alter table omixor_channels drop constraint fk_channels_issue`)
	_, err := p.CreateChannel("o", model.Channel{Name: "c", IssueID: "ISS-11", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "dev", Active: true, CreatedAt: at}))
	_, err = p.CreateAgentTask("o", model.AgentTask{AgentID: "dev", Title: "x", Status: "queued", Priority: "normal",
		Links: []string{"iss-11"}, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	// 等於舊版把 ISS-11 永久刪掉(別名跟著 cascade)、遷移後序號從 max(number) = 10 起算的狀態
	pgExec(t, p, `delete from ops_issues where org = 'o' and key = $1`, keys[10])
	pgExec(t, p, `update omixor_seq set last = 10 where org = 'o' and scope = 'issue'`)
	if n := len(retiredKeys(t, p)); n != 0 {
		t.Fatal("前提:還沒有退役的 key", n)
	}

	p2 := fkPG(t, url) // 重啟(key 遷移沒事可做,孤兒 key 保留照樣跑)
	if got := retiredKeys(t, p2); len(got) != 1 || got["ISS-11"] != "orphan-ref" {
		t.Fatal("退役的 key", got)
	}
	if k := createKeys(t, p2, at, "", 1); k[0] != "ISS-12" {
		t.Fatal("要跳過孤兒參照的 ISS-11", k)
	}
	p3 := fkPG(t, url) // 再重啟:冪等
	if got := retiredKeys(t, p3); len(got) != 1 {
		t.Fatal("重啟不多記", got)
	}
	if k := createKeys(t, p3, at, "", 1); k[0] != "ISS-13" {
		t.Fatal(k)
	}
}

// 新版本身:永久刪除專案裡的 Issue、刪專案,再用同一個代號建新專案(序號是新的一組)→ 不重發刪掉的 key
func TestPGIssueDeletedKeysNotReissuedAcrossProjects(t *testing.T) {
	p, at := fixOrg(t)
	old, err := p.CreateProject("o", model.Project{Name: "old", Key: "ABC", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	keys := createKeys(t, p, at, old.ID, 2)
	if keys[0] != "ABC-1" || keys[1] != "ABC-2" {
		t.Fatal(keys)
	}
	for _, k := range keys {
		chk(t, p.DeleteIssue("o", k))
	}
	chk(t, p.DeleteProject("o", old.ID))
	if got := retiredKeys(t, p); len(got) != 2 || got["ABC-1"] != "deleted" || got["ABC-2"] != "deleted" {
		t.Fatal("永久刪除要把 key 記進退役表", got)
	}
	nw, err := p.CreateProject("o", model.Project{Name: "new", Key: "ABC", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if k := createKeys(t, p, at, nw.ID, 1); k[0] != "ABC-3" {
		t.Fatal("同代號的新專案不能重發刪掉的 key", k)
	}
	// 退役的 key 查不到 Issue(不是別名)
	if _, err := p.GetIssue("o", "ABC-1"); err == nil {
		t.Fatal("退役的 key 不應查得到 Issue")
	}
	chk(t, p.DeleteOrg("o"))
	if n := pgCount(t, p, `select count(*) from omixor_issue_keys_retired where org = 'o'`); n != 0 {
		t.Fatal("刪組織要一併清掉", n)
	}
}
