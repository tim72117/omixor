package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"omixor-core/model"
)

// Issue 的 UUID 內部 id、key、專案編號、別名、軟刪除與版本(見 pg_issue.go、pg_issue_keys.go)

func keyIssue(at model.Time) model.Issue {
	return model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at}
}

// 遷移:舊的 ISS-<n> 換成 UUID、原編號變成 key 並寫進別名表;留言、頻道、任務連結跟著改;非數字 id 發新 key;可以重跑
func TestPGIssueKeyMigration(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	// 舊格式的資料:直接寫進表(等於升級前的資料庫);key 是空字串
	pgExec(t, p, `drop index if exists ops_issues_org_number`)
	pgExec(t, p, `create unique index ops_issues_org_number on ops_issues (org, number) where number > 0`) // 舊版的兩欄索引
	for _, r := range []struct {
		id  string
		num int
		at  string
	}{{"ISS-1", 1, "2026-01-01T00:00:00Z"}, {"ISS-2", 2, "2026-01-01T00:00:01Z"}, {"gh-7", 0, "2026-01-01T00:00:02Z"}} {
		pgExec(t, p, `insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at)
			values ('o', $1, $2, $2 + 1, 't', 'open', 'normal', 'sys_omixor', $3::timestamptz, $3::timestamptz)`, r.id, r.num, r.at)
	}
	pgExec(t, p, `insert into ops_issue_comments (org, issue_id, pos, id, author, text, at) values ('o', 'ISS-2', 0, 'c1', 'sys_omixor', '看 ISS-1', now())`)
	ch, err := p.CreateChannel("o", model.Channel{Name: "c", IssueID: "ISS-2", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "dev", Active: true, CreatedAt: at}))
	task, err := p.CreateAgentTask("o", model.AgentTask{AgentID: "dev", Title: "x", Status: "queued", Priority: "normal",
		Links: []string{"iss-2", "https://x"}, CreatedAt: at, UpdatedAt: at})
	chk(t, err)

	chk(t, p.migrateIssueKeys())
	l, err := p.ListIssues("o")
	chk(t, err)
	byKey := map[string]model.Issue{}
	for _, i := range l {
		if !model.IsUUID(i.ID) {
			t.Fatal("id 要換成 UUID", i.ID)
		}
		byKey[i.Key] = i
	}
	i1, i2, gh := byKey["ISS-1"], byKey["ISS-2"], byKey["ISS-3"]
	if i1.ID == "" || i2.ID == "" || gh.ID == "" || i1.Num != 1 || i2.Num != 2 || gh.Num != 3 || i1.Project != "" {
		t.Fatalf("key 與號碼:%v", byKey)
	}
	if !(i1.ID < i2.ID && i2.ID < gh.ID) {
		t.Fatal("UUIDv7 要依建立時間排序", i1.ID, i2.ID, gh.ID)
	}
	if len(i2.Comments) != 1 || i2.Comments[0].Text != "看 ISS-1" {
		t.Fatal("留言要跟著新的 id", i2.Comments)
	}
	for ref, want := range map[string]string{"ISS-2": i2.ID, "iss-1": i1.ID, "GH-7": gh.ID, "gh-7": gh.ID, i2.ID: i2.ID} {
		if got, err := p.ResolveIssueID("o", ref); err != nil || got != want {
			t.Fatalf("解析 %s:%s %v", ref, got, err)
		}
	}
	if c, err := p.GetChannel("o", ch.ID); err != nil || c.IssueID != i2.ID {
		t.Fatal("頻道綁的 Issue 要跟著新的 id", c, err)
	}
	if x, err := p.GetAgentTask("o", task.ID); err != nil || x.Links[0] != i2.ID || x.Links[1] != "https://x" {
		t.Fatal("任務的相關連結要換成新的 id", x, err)
	}
	// 舊的兩欄唯一索引不在遷移裡動:migrateSeq 先建好三欄的新索引再換掉舊的
	chk(t, p.migrateSeq())
	if n := pgCount(t, p, `select count(*) from pg_index where indexrelid = to_regclass('ops_issues_org_number') and indnkeyatts = 2`); n != 0 {
		t.Fatal("舊的兩欄唯一索引要換成三欄")
	}
	// 重跑:不再改任何東西
	chk(t, p.migrateIssueKeys())
	l2, _ := p.ListIssues("o")
	if fmt.Sprint(issueIDs(l2)) != fmt.Sprint(issueIDs(l)) || l2[0].ID != l[0].ID {
		t.Fatal("重跑不能再改", issueIDs(l), issueIDs(l2))
	}
	// 之後建的接在後面
	x, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	if x.Key != "ISS-4" {
		t.Fatal("遷移後建號", x.Key)
	}
}

// 組織層級與各專案各自連號;沒有代號的專案不能放 Issue;專案不存在 ErrBadRef
func TestPGIssueProjectNumbering(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	app, err := p.CreateProject("o", model.Project{Name: "app", Key: "APP", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	web, err := p.CreateProject("o", model.Project{Name: "web", Key: "WEB", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	bare, err := p.CreateProject("o", model.Project{Name: "bare", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	mk := func(project string) model.Issue {
		x := keyIssue(at)
		x.Project = project
		i, err := p.CreateIssue("o", x)
		chk(t, err)
		return i
	}
	got := []string{mk("").Key, mk(app.ID).Key, mk(app.ID).Key, mk("").Key, mk(web.ID).Key, mk(app.ID).Key}
	if fmt.Sprint(got) != "[ISS-1 APP-1 APP-2 ISS-2 WEB-1 APP-3]" {
		t.Fatal("各範圍各自連號", got)
	}
	x := keyIssue(at)
	x.Project = bare.ID
	if _, err := p.CreateIssue("o", x); !errors.Is(err, ErrProjectNoKey) {
		t.Fatal("沒有代號的專案", err)
	}
	x.Project = "PRJ-99"
	if _, err := p.CreateIssue("o", x); !errors.Is(err, ErrBadRef) {
		t.Fatal("不存在的專案", err)
	}
	// 代號:重複(不分大小寫)ErrDuplicateKey;已經有 Issue 的專案不能改代號;沒有 Issue 的可以
	if _, err := p.CreateProject("o", model.Project{Name: "dup", Key: "app", CreatedAt: at, UpdatedAt: at}); !errors.Is(err, ErrDuplicateKey) {
		// 存放處不負責轉大寫(service 會),但唯一索引是 upper(key)
		t.Fatal("代號重複", err)
	}
	app.Key = "MOB"
	if err := p.UpdateProject("o", app); !errors.Is(err, ErrInUse) {
		t.Fatal("已有 Issue 不能改代號", err)
	}
	bare.Key = "BARE"
	chk(t, p.UpdateProject("o", bare))
	// 專案還有 Issue 不能刪(外鍵 restrict)
	if err := p.DeleteProject("o", web.ID); !errors.Is(err, ErrInUse) {
		t.Fatal("還有 Issue 的專案不能刪", err)
	}
}

// 搬移:發新的 key,舊的 key 仍然解析得到;搬回組織層級也發新的 ISS-<n>;版本 +1
func TestPGIssueMoveKeepsOldKey(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	app, err := p.CreateProject("o", model.Project{Name: "app", Key: "APP", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	web, err := p.CreateProject("o", model.Project{Name: "web", Key: "WEB", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	x := keyIssue(at)
	x.Project = app.ID
	a, err := p.CreateIssue("o", x)
	chk(t, err)
	m, old, err := p.MoveIssue("o", "app-1", web.ID, a.Version, at)
	chk(t, err)
	if old != "APP-1" || m.Key != "WEB-1" || m.Project != web.ID || m.ID != a.ID || m.Version != 2 {
		t.Fatal("搬移", old, m)
	}
	if _, _, err := p.MoveIssue("o", m.ID, "", 1, at); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("搬移也檢查版本", err)
	}
	back, _, err := p.MoveIssue("o", "APP-1", "", 0, at) // 用最舊的 key 也找得到
	chk(t, err)
	if back.Key != "ISS-1" || back.Project != "" {
		t.Fatal("搬回組織層級發新的 ISS", back.Key)
	}
	for _, ref := range []string{"APP-1", "web-1", "ISS-1", a.ID} {
		if g, err := p.GetIssue("o", ref); err != nil || g.ID != a.ID {
			t.Fatal("舊的與目前的 key 都要找得到", ref, err)
		}
	}
	// APP 的序號不回收:再建一張是 APP-2
	b, err := p.CreateIssue("o", x)
	chk(t, err)
	if b.Key != "APP-2" {
		t.Fatal("序號不回收", b.Key)
	}
}

// 同一個專案同時建 Issue:號碼不重複、不跳號
func TestPGIssueConcurrentProjectNumbers(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	app, err := p.CreateProject("o", model.Project{Name: "app", Key: "APP", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	const n = 20
	keys := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := keyIssue(at)
			if k%2 == 0 {
				x.Project = app.ID
			}
			i, err := p.CreateIssue("o", x)
			keys[k], errs[k] = i.Key, err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		chk(t, err)
	}
	sort.Strings(keys)
	want := []string{}
	for k := 1; k <= n/2; k++ {
		want = append(want, fmt.Sprintf("APP-%d", k), fmt.Sprintf("ISS-%d", k))
	}
	sort.Strings(want)
	if fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Fatal("號碼重複或跳號", keys)
	}
}

// 軟刪除:垃圾桶裡的不在列表、取得與寫入都是 ErrNotFound;還原;永久刪除連別名一起清;key 不回收
func TestPGIssueTrash(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	a, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	_, err = p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	d, err := p.TrashIssue("o", "iss-1", "sys_omixor", at)
	chk(t, err)
	if d.DeletedAt == nil || d.DeletedBy != "sys_omixor" || d.Version != 2 {
		t.Fatal("丟進垃圾桶", d)
	}
	if l, _ := p.ListIssues("o"); len(l) != 1 || l[0].Key != "ISS-2" {
		t.Fatal("列表不含垃圾桶", issueIDs(l))
	}
	if l, _ := p.ListDeletedIssues("o"); len(l) != 1 || l[0].ID != a.ID {
		t.Fatal("垃圾桶列表", issueIDs(l))
	}
	if l, _ := p.ListIssuesWithDeleted("o"); len(l) != 2 {
		t.Fatal("全部", issueIDs(l))
	}
	_, err = p.GetIssue("o", "ISS-1")
	wantNotFound(t, "取得垃圾桶裡的", err)
	u := a
	u.Title = "改"
	u.Version = 0
	wantNotFound(t, "修改垃圾桶裡的", p.UpdateIssue("o", u))
	_, err = p.AddIssueComment("o", "ISS-1", model.Comment{ID: "c", Author: "sys_omixor", Text: "x", At: at})
	wantNotFound(t, "在垃圾桶裡的留言", err)
	_, _, err = p.MoveIssue("o", "ISS-1", "", 0, at)
	wantNotFound(t, "搬垃圾桶裡的", err)
	_, err = p.TrashIssue("o", "ISS-1", "sys_omixor", at)
	wantNotFound(t, "重複刪除", err)
	r, err := p.RestoreIssue("o", "ISS-1", at)
	chk(t, err)
	if r.DeletedAt != nil || r.Version != 3 {
		t.Fatal("還原", r)
	}
	_, err = p.RestoreIssue("o", "ISS-1", at)
	wantNotFound(t, "沒在垃圾桶裡的不能還原", err)
	chk(t, p.DeleteIssue("o", "ISS-1"))
	if n := pgCount(t, p, `select count(*) from omixor_issue_keys where org = 'o' and issue_id = $1`, a.ID); n != 0 {
		t.Fatal("永久刪除要連別名一起刪", n)
	}
	c, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	if c.Key != "ISS-3" {
		t.Fatal("刪掉的號碼不回收", c.Key)
	}
}

// 版本:條件式更新;版本不符回 VersionConflictError(帶目前版本);0 不檢查;留言不動版本
func TestPGIssueVersion(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	a, err := p.CreateIssue("o", keyIssue(at))
	chk(t, err)
	if a.Version != 1 {
		t.Fatal("新建的版本是 1", a.Version)
	}
	u := a
	u.Title = "一"
	chk(t, p.UpdateIssue("o", u)) // version 1 → 2
	u.Title = "二"
	err = p.UpdateIssue("o", u) // 還帶 1
	var vc *VersionConflictError
	if !errors.As(err, &vc) || vc.Current != 2 {
		t.Fatal("版本不符", err)
	}
	if _, err := p.AddIssueComment("o", a.ID, model.Comment{ID: "c", Author: "sys_omixor", Text: "x", At: at}); err != nil {
		t.Fatal(err)
	}
	g, _ := p.GetIssue("o", a.ID)
	if g.Version != 2 || g.Title != "一" {
		t.Fatal("留言不動版本", g.Version, g.Title)
	}
	u.Version = 0
	chk(t, p.UpdateIssue("o", u)) // 不檢查
	g, _ = p.GetIssue("o", a.ID)
	if g.Version != 3 || g.Title != "二" {
		t.Fatal("不檢查版本的修改", g.Version, g.Title)
	}
}
