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
