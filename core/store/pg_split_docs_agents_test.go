package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"omixor-core/model"
)

func rawDoc(t *testing.T, p *PG, org, id string, n int, tags string) {
	t.Helper()
	if _, err := p.pool.Exec(context.Background(), `insert into omixor_docs (org, id, number, pos, title, section, tags, created_at, updated_at)
		values ($1, $2, $3::int, $3::bigint, 't', 's', $4::jsonb, now(), now())`, org, id, n, tags); err != nil {
		t.Fatal(err)
	}
}

func rawAgent(t *testing.T, p *PG, org, id, acc, rts string) {
	t.Helper()
	if _, err := p.pool.Exec(context.Background(), `insert into omixor_agents (org, id, name, doc_access, routines) values ($1, $2, $2, $3::jsonb, $4::jsonb)`,
		org, id, acc, rts); err != nil {
		t.Fatal(err)
	}
}

func TestPGDocTagsSplit(t *testing.T) {
	p := cabPG(t)
	at := model.Now()
	d, err := p.CreateDoc("o", model.CabinetDoc{Title: "a", Section: "s", Tags: []string{"b", "a", "b", "c"}, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := p.CreateDoc("o", model.CabinetDoc{Title: "b", Section: "s", Tags: []string{"a"}, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.GetDoc("o", d.ID)
	if err != nil || !reflect.DeepEqual(got.Tags, []string{"b", "a", "c"}) {
		t.Fatal("標籤順序(重複的去掉)", got, err)
	}
	if n := count(t, p, `select count(*) from omixor_doc_tags where org = 'o' and doc_id = $1`, d.ID); n != 3 {
		t.Fatal("新表要有 3 列", n)
	}
	// 舊欄位不再寫,split_at 標記
	if n := count(t, p, `select count(*) from omixor_docs where org = 'o' and tags = '[]'::jsonb and split_at is not null`); n != 2 {
		t.Fatal("舊 tags 欄位不該被寫、split_at 要標記", n)
	}
	// 依標籤篩選(索引 (org, tag))
	if n := count(t, p, `select count(*) from omixor_doc_tags where org = 'o' and tag = 'a'`); n != 2 {
		t.Fatal("依標籤篩選", n)
	}
	if n := count(t, p, `select count(*) from pg_indexes where indexname = 'omixor_doc_tags_tag' and schemaname = current_schema()`); n != 1 {
		t.Fatal("缺 (org, tag) 索引", n)
	}
	// ListDocs 批次讀
	l, err := p.ListDocs("o")
	if err != nil || len(l) != 2 || l[0].ID != d2.ID || !reflect.DeepEqual(l[1].Tags, []string{"b", "a", "c"}) || !reflect.DeepEqual(l[0].Tags, []string{"a"}) {
		t.Fatal("ListDocs", l, err)
	}
	// 更新整份取代;清空
	d.Tags = []string{"z"}
	if err := p.UpdateDoc("o", d); err != nil {
		t.Fatal(err)
	}
	if g, _ := p.GetDoc("o", d.ID); !reflect.DeepEqual(g.Tags, []string{"z"}) {
		t.Fatal("更新", g.Tags)
	}
	d.Tags = nil
	if err := p.UpdateDoc("o", d); err != nil {
		t.Fatal(err)
	}
	if g, _ := p.GetDoc("o", d.ID); g.Tags == nil || len(g.Tags) != 0 {
		t.Fatal("清空要是空陣列", g.Tags)
	}
	// 刪文件 cascade
	if err := p.DeleteDoc("o", d2.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, p, `select count(*) from omixor_doc_tags where doc_id = $1`, d2.ID); n != 0 {
		t.Fatal("刪文件後標籤要清掉", n)
	}
}

func TestPGDocTagsMigration(t *testing.T) {
	p := cabPG(t)
	rawDoc(t, p, "o", "DOC-1", 1, `["x","y"]`)
	rawDoc(t, p, "o", "DOC-2", 2, `[]`)
	rawDoc(t, p, "o", "DOC-3", 3, `["d","d"]`) // 重複:去重後展開
	rawDoc(t, p, "o", "DOC-4", 4, `{"a":1}`)   // 結構不對:跳過
	if err := p.migrateSplitDocsAgents(); err != nil {
		t.Fatal(err)
	}
	g, err := p.GetDoc("o", "DOC-1")
	if err != nil || !reflect.DeepEqual(g.Tags, []string{"x", "y"}) {
		t.Fatal(g, err)
	}
	if n := count(t, p, `select count(*) from omixor_docs where org = 'o' and split_at is not null`); n != 3 {
		t.Fatal("DOC-1、DOC-2、DOC-3 要標記", n)
	}
	if g3, _ := p.GetDoc("o", "DOC-3"); !reflect.DeepEqual(g3.Tags, []string{"d"}) {
		t.Fatal("重複的標籤去重", g3.Tags)
	}
	if n := count(t, p, `select count(*) from omixor_doc_tags where doc_id = 'DOC-4'`); n != 0 {
		t.Fatal("壞資料不展開", n)
	}
	// 新版改過後,舊 jsonb 改變、重跑遷移也不覆蓋
	g.Tags = []string{"new"}
	if err := p.UpdateDoc("o", *g); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pool.Exec(context.Background(), `update omixor_docs set tags = '["old"]' where id = 'DOC-1'`); err != nil {
		t.Fatal(err)
	}
	if err := p.migrateSplitDocsAgents(); err != nil {
		t.Fatal(err)
	}
	if g2, _ := p.GetDoc("o", "DOC-1"); !reflect.DeepEqual(g2.Tags, []string{"new"}) {
		t.Fatal("重跑不該覆蓋", g2.Tags)
	}
	// 壞資料修好後會再試
	if _, err := p.pool.Exec(context.Background(), `update omixor_docs set tags = '["d","e"]' where id = 'DOC-4'`); err != nil {
		t.Fatal(err)
	}
	if err := p.migrateSplitDocsAgents(); err != nil {
		t.Fatal(err)
	}
	if g4, _ := p.GetDoc("o", "DOC-4"); !reflect.DeepEqual(g4.Tags, []string{"d", "e"}) {
		t.Fatal(g4.Tags)
	}
}

func sampleRoutines() []model.AgentRoutine {
	at := model.Now()
	last := model.Time{Time: at.Add(-time.Hour)}
	return []model.AgentRoutine{
		{ID: "r2", Kind: "weekly", Weekday: 3, Hour: 9, Minute: 30, Title: "週報", Instructions: "寫", Enabled: true, Priority: "high", CreatedAt: at, LastRunAt: &last},
		{ID: "r1", Kind: "every", EveryHours: 6, Title: "巡檢", Priority: "normal", CreatedAt: at},
	}
}

func TestPGAgentSplit(t *testing.T) {
	p := cabPG(t)
	rs := sampleRoutines()
	a := model.Agent{ID: "ops", Name: "Ops", Active: true, CreatedAt: model.Now(), Routines: rs,
		DocAccess: map[string]string{"course": "read", "PRJ-1:/a/b": "write", "gone": "none"}}
	b := model.Agent{ID: "dev", Name: "Dev", CreatedAt: model.Now()}
	if err := p.SaveAgent("o", a); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveAgent("o", b); err != nil {
		t.Fatal(err)
	}
	g, err := p.GetAgent("o", "ops")
	if err != nil {
		t.Fatal(err)
	}
	wantActorID(t, *g)
	a.ActorID = g.ActorID
	sameJSON(t, "GetAgent", g, a)
	l, err := p.ListAgents("o")
	if err != nil || len(l) != 2 {
		t.Fatal(l, err)
	}
	for _, x := range l {
		if x.ID == "dev" && (x.DocAccess == nil || x.Routines == nil || len(x.DocAccess) != 0 || len(x.Routines) != 0) {
			t.Fatal("空的要是 {}、[]", x)
		}
		if x.ID == "ops" {
			sameJSON(t, "ListAgents", x, a)
		}
	}
	if n := count(t, p, `select count(*) from omixor_agent_doc_access where org = 'o' and agent_id = 'ops'`); n != 3 {
		t.Fatal(n)
	}
	if n := count(t, p, `select count(*) from omixor_agent_routines where org = 'o' and agent_id = 'ops'`); n != 2 {
		t.Fatal(n)
	}
	if n := count(t, p, `select count(*) from omixor_agents where org = 'o' and doc_access = '{}'::jsonb and routines = '[]'::jsonb and split_at is not null`); n != 2 {
		t.Fatal("舊欄位不該被寫、split_at 要標記", n)
	}
	// 整份取代
	a.DocAccess = map[string]string{"course": "write"}
	a.Routines = rs[1:]
	if err := p.SaveAgent("o", a); err != nil {
		t.Fatal(err)
	}
	g, _ = p.GetAgent("o", "ops")
	sameJSON(t, "取代後", g, a)
	// 重複的例行任務 id 被擋,且原資料不變(交易回滾)
	bad := a
	bad.Routines = []model.AgentRoutine{{ID: "x"}, {ID: "x"}}
	if err := p.SaveAgent("o", bad); err == nil {
		t.Fatal("重複 id 要失敗")
	}
	g, _ = p.GetAgent("o", "ops")
	sameJSON(t, "失敗後不變", g, a)
	// 刪角色:子表 cascade
	if err := p.DeleteAgent("o", "ops"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, p, `select count(*) from omixor_agent_doc_access where agent_id = 'ops'`) + count(t, p, `select count(*) from omixor_agent_routines where agent_id = 'ops'`); n != 0 {
		t.Fatal("刪角色後子表要清掉", n)
	}
}

func TestPGAgentMigration(t *testing.T) {
	p := cabPG(t)
	rawAgent(t, p, "o", "good", `{"course":"read","P:x":"write"}`,
		`[{"id":"r1","kind":"daily","hour":9,"title":"早","enabled":true,"priority":"normal","createdAt":"2026-01-01T00:00:00Z","lastRunAt":"2026-01-02T09:00:00Z"},{"id":"r0","kind":"every","everyHours":3}]`)
	rawAgent(t, p, "o", "empty", `{}`, `[]`)
	rawAgent(t, p, "o", "dup", `{}`, `[{"id":"a"},{"id":"a"}]`) // id 重複:補 uuid 後展開
	rawAgent(t, p, "o", "badacc", `["x"]`, `[]`)
	rawAgent(t, p, "o", "badrt", `{}`, `{"id":"a"}`)
	if err := p.migrateSplitDocsAgents(); err != nil {
		t.Fatal(err)
	}
	g, err := p.GetAgent("o", "good")
	if err != nil || len(g.Routines) != 2 || g.Routines[0].ID != "r1" || g.Routines[1].ID != "r0" ||
		g.Routines[0].LastRunAt == nil || g.Routines[0].CreatedAt.IsZero() || !g.Routines[1].CreatedAt.IsZero() ||
		g.DocAccess["P:x"] != "write" || len(g.DocAccess) != 2 {
		t.Fatalf("展開 %+v %v", g, err)
	}
	if n := count(t, p, `select count(*) from omixor_agents where org = 'o' and split_at is not null`); n != 3 {
		t.Fatal("good、empty、dup 標記", n)
	}
	if n := count(t, p, `select count(*) from omixor_agent_routines where agent_id = 'dup'`); n != 2 {
		t.Fatal("重複 id 的例行任務都要保留", n)
	}
	if n := count(t, p, `select count(*) from omixor_agent_routines where agent_id = 'badrt'`); n != 0 {
		t.Fatal("壞資料不展開", n)
	}
	// 冪等、不覆蓋:新版改過後舊 jsonb 變了也不影響
	g.DocAccess = map[string]string{"new": "read"}
	if err := p.SaveAgent("o", *g); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pool.Exec(context.Background(), `update omixor_agents set doc_access = '{"old":"read"}' where id = 'good'`); err != nil {
		t.Fatal(err)
	}
	if err := p.migrateSplitDocsAgents(); err != nil {
		t.Fatal(err)
	}
	g2, _ := p.GetAgent("o", "good")
	if !reflect.DeepEqual(g2.DocAccess, map[string]string{"new": "read"}) || len(g2.Routines) != 2 {
		t.Fatal("重跑不該覆蓋", g2)
	}
}

func TestPGSplitDeleteOrg(t *testing.T) {
	p := cabPG(t)
	at := model.Now()
	for _, org := range []string{"o", "keep"} {
		if _, err := p.CreateDoc(org, model.CabinetDoc{Title: "a", Section: "s", Tags: []string{"t"}, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := p.SaveAgent(org, model.Agent{ID: "ag", Name: "x", CreatedAt: at, DocAccess: map[string]string{"s": "read"}, Routines: sampleRoutines()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	for _, tb := range []string{"omixor_doc_tags", "omixor_agent_doc_access", "omixor_agent_routines"} {
		if n := count(t, p, `select count(*) from `+tb+` where org = 'o'`); n != 0 {
			t.Fatal(tb, "刪組織後要清掉", n)
		}
		if n := count(t, p, `select count(*) from `+tb+` where org = 'keep'`); n == 0 {
			t.Fatal(tb, "別的組織不能被刪")
		}
	}
}
