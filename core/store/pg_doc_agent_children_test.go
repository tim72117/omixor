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
