package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"omixor-core/model"
)

func rawChannel(t *testing.T, p *PG, org, id string, n int, members string) {
	t.Helper()
	pgExec(t, p, `insert into omixor_channels (org, id, number, name, members, created_at, updated_at) values ($1,$2,$3::int,'c',$4::jsonb,now(),now())`, org, id, n, members)
}

func unmigrated(t *testing.T, p *PG) int {
	t.Helper()
	n, err := p.UnmigratedRows()
	chk(t, err)
	return n
}

// 重複的標籤、成員、提及:無損,遷移時去重(留第一個)後照常展開,不略過、不丟其他資料
func TestPGMigrateDedupsLosslessly(t *testing.T) {
	p := cabPG(t)
	rawChannel(t, p, "o", "CH-1", 1, `["user:a","user:a","agent:b"]`)
	pgExec(t, p, `insert into omixor_channel_messages (org, channel_id, id, number, author, text, mentions, at)
		values ('o','CH-1','M-1',1,'sys_omixor','t','["agent:b","agent:b","user:a"]',now())`)
	rawDoc(t, p, "o", "DOC-1", 1, `["a","a","b"]`)
	rawAgent(t, p, "o", "dev", `{"course":"read","P:x":"write"}`,
		`[{"id":"r","title":"一","kind":"daily"},{"id":"r","title":"二","kind":"daily"},{"id":"","title":"三","kind":"daily"},{"id":"z","title":"四","kind":"daily"}]`)

	chk(t, p.migrateChannelSplit())
	chk(t, p.migrateSplitDocsAgents())
	if n := unmigrated(t, p); n != 0 {
		t.Fatal("去重後都該展開,未展開:", n)
	}
	ch, err := p.GetChannel("o", "CH-1")
	chk(t, err)
	sameJSON(t, "成員", ch.Members, lids(t, p, "o", "user:a", "agent:b"))
	msgs, _, err := p.ListChannelMessages("o", "CH-1", model.MessageQuery{})
	chk(t, err)
	sameJSON(t, "提及", msgs[0].Mentions, lids(t, p, "o", "agent:b", "user:a"))
	d, err := p.GetDoc("o", "DOC-1")
	chk(t, err)
	sameJSON(t, "標籤", d.Tags, []string{"a", "b"})
	a, err := p.GetAgent("o", "dev")
	chk(t, err)
	if len(a.Routines) != 4 || a.Routines[0].ID != "r" || a.Routines[0].Title != "一" || a.Routines[3].ID != "z" {
		t.Fatalf("例行任務 %+v", a.Routines)
	}
	seen := map[string]bool{}
	for _, r := range a.Routines {
		if r.ID == "" || seen[r.ID] {
			t.Fatalf("id 要補成唯一:%+v", a.Routines)
		}
		seen[r.ID] = true
	}
	sameJSON(t, "doc_access 也要保留", a.DocAccess, map[string]string{"course": "read", "P:x": "write"})
}

// 真的展開不了的列(壞 JSON、型別不符):split_at 保持 NULL、讀取退回舊 jsonb(不是空白)、寫入被拒絕且不蓋掉舊資料、未展開列數可查
func TestPGUnsplitRowsReadFallbackAndWriteGuard(t *testing.T) {
	p := cabPG(t)
	rawDoc(t, p, "o", "DOC-1", 1, `["x",5]`) // 元素型別不符:展開不了,讀取退回(5 轉成文字)
	rawDoc(t, p, "o", "DOC-2", 2, `{"a":1}`) // 不是陣列
	rawAgent(t, p, "o", "bad", `["x"]`, `[{"id":"r","title":"t"}]`)
	rawChannel(t, p, "o", "CH-1", 1, `["user:a",5]`)
	pgExec(t, p, `insert into omixor_channel_messages (org, channel_id, id, number, author, text, mentions, at) values ('o','CH-1','M-1',1,'sys_omixor','t','["x",1]',now())`)
	pgExec(t, p, `insert into omixor_org_chains (org, edges) values ('o','[{"from":1}]')`)
	pgExec(t, p, `insert into omixor_cabinet_layouts (org, project, layout) values ('o','','{"x":1}'::jsonb)`)
	pgExec(t, p, `insert into omixor_cabinet_layouts (org, project, layout) values ('o','PRJ-1','[{"id":"a","label":"a"},{"id":"a","label":"b"}]'::jsonb)`)

	chk(t, p.migrateCabinetLayouts())
	chk(t, p.migrateChannelSplit())
	chk(t, p.migrateSplitDocsAgents())
	by, err := p.UnmigratedRowsByTable()
	chk(t, err)
	want := map[string]int{"omixor_docs": 2, "omixor_agents": 1, "omixor_channels": 1, "omixor_channel_messages": 1, "omixor_org_chains": 1, "omixor_cabinet_layouts": 2,
		"omixor_agent_tasks": 0, "omixor_incidents": 0}
	if !reflect.DeepEqual(by, want) {
		t.Fatalf("未展開列數 %v,想要 %v", by, want)
	}
	if n := unmigrated(t, p); n != 8 {
		t.Fatal("總數", n)
	}

	// 讀取退回舊 jsonb,不是空白
	d, err := p.GetDoc("o", "DOC-1")
	chk(t, err)
	sameJSON(t, "文件標籤退回舊 jsonb", d.Tags, []string{"x", "5"})
	if d2, _ := p.GetDoc("o", "DOC-2"); len(d2.Tags) != 0 {
		t.Fatal("結構不對的回空", d2.Tags)
	}
	a, err := p.GetAgent("o", "bad")
	chk(t, err)
	if len(a.Routines) != 1 || a.Routines[0].ID != "r" || len(a.DocAccess) != 0 {
		t.Fatalf("角色退回舊 jsonb %+v", a)
	}
	if l, err := p.ListAgents("o"); err != nil || len(l) != 1 || len(l[0].Routines) != 1 {
		t.Fatal("ListAgents 也要退回", l, err)
	}
	ch, err := p.GetChannel("o", "CH-1")
	chk(t, err)
	sameJSON(t, "頻道成員退回", ch.Members, lids(t, p, "o", "user:a"))
	if l, err := p.ListChannels("o"); err != nil || len(l) != 1 || len(l[0].Members) != 1 {
		t.Fatal("ListChannels 也要退回", l, err)
	}
	msgs, _, err := p.ListChannelMessages("o", "CH-1", model.MessageQuery{})
	chk(t, err)
	sameJSON(t, "提及退回", msgs[0].Mentions, lids(t, p, "o", "x"))
	if c, err := p.GetOrgChain("o"); err != nil || len(c.Edges) != 0 {
		t.Fatal("壞的組織鏈回空邊", c, err)
	}
	if _, _, err := p.GetCabinetLayout("o", ""); !errors.Is(err, ErrUnmigratedRow) {
		t.Fatal("壞 JSON 的文件櫃要回明確錯誤,不是空結構:", err)
	}
	lay, _, err := p.GetCabinetLayout("o", "PRJ-1")
	chk(t, err)
	if len(lay) != 2 || lay[0].ID != "a" || lay[1].Label != "b" || lay[0].Tabs == nil {
		t.Fatalf("重複 id 的文件櫃退回舊 layout %+v", lay)
	}

	// 寫入被拒絕,舊資料不被蓋掉
	d.Tags = []string{"new"}
	if err := p.UpdateDoc("o", *d); !errors.Is(err, ErrUnmigratedRow) {
		t.Fatal("寫入未展開的壞列要拒絕", err)
	}
	a.Active = true
	if err := p.SaveAgent("o", *a); !errors.Is(err, ErrUnmigratedRow) {
		t.Fatal("SaveAgent", err)
	}
	ch.Members = lids(t, p, "o", "user:new")
	if err := p.UpdateChannel("o", *ch); !errors.Is(err, ErrUnmigratedRow) {
		t.Fatal("UpdateChannel", err)
	}
	if err := p.SaveOrgChain("o", model.OrgChain{Edges: []model.ChainEdge{{From: lid(t, p, "o", "user:a"), To: lid(t, p, "o", "user:b")}}}); !errors.Is(err, ErrUnmigratedRow) {
		t.Fatal("SaveOrgChain", err)
	}
	if _, err := p.SaveCabinetLayout("o", "", []model.CabinetSection{{ID: "s", Label: "s", Tabs: []model.CabinetTab{}}}, nil); !errors.Is(err, ErrUnmigratedRow) {
		t.Fatal("SaveCabinetLayout 壞 JSON", err)
	}
	if n := count(t, p, `select count(*) from omixor_docs where id = 'DOC-1' and tags = '["x",5]'::jsonb and split_at is null`); n != 1 {
		t.Fatal("舊資料被動到了")
	}
	if n := count(t, p, `select count(*) from omixor_channels where id = 'CH-1' and members = '["user:a",5]'::jsonb and split_at is null`); n != 1 {
		t.Fatal("舊成員被動到了")
	}
	if n := unmigrated(t, p); n != 8 {
		t.Fatal("被拒絕的寫入不能改變未展開數", n)
	}

	// 舊 JSON 能解、只是有重複 id 的文件櫃:存檔(整份取代)照常
	if _, err := p.SaveCabinetLayout("o", "PRJ-1", []model.CabinetSection{{ID: "a", Label: "a", Tabs: []model.CabinetTab{}}}, nil); err != nil {
		t.Fatal(err)
	}
	// 修好舊資料:下次寫入先展開,成功
	pgExec(t, p, `update omixor_docs set tags = '["x","y"]' where id = 'DOC-1'`)
	d.Tags = []string{"x", "y", "z"}
	chk(t, p.UpdateDoc("o", *d))
	if g, _ := p.GetDoc("o", "DOC-1"); !reflect.DeepEqual(g.Tags, []string{"x", "y", "z"}) {
		t.Fatal(g.Tags)
	}
	// 重啟遷移:剩下的壞列仍略過,修好的那幾列已展開
	chk(t, p.migrateSplitDocsAgents())
	if n := unmigrated(t, p); n != 6 {
		t.Fatal("剩下", n)
	}
}

// 遷移分批:每批 N 列,批內壞資料只略過那一列;沒子資料的列一個語句標記
func TestPGMigrateBatches(t *testing.T) {
	p := cabPG(t)
	rawChannel(t, p, "o", "CH-1", 1, `[]`)
	// 60 則訊息:5、20、33 是壞資料,3 的倍數沒有提及,其餘有一個提及
	pgExec(t, p, `insert into omixor_channel_messages (org, channel_id, id, number, author, text, mentions, at)
		select 'o','CH-1','M-'||g, g, 'sys_omixor','t',
		  case when g in (5,20,33) then '[1]'::jsonb when g % 3 = 0 then '[]'::jsonb else '["user:a"]'::jsonb end, now() from generate_series(1,60) g`)
	oldSize := migrateBatchSize
	migrateBatchSize = 7
	batches, rows := 0, 0
	onSplitBatch = func(table string, n int) {
		if table == "omixor_channel_messages" {
			batches++
			rows += n
		}
	}
	defer func() { migrateBatchSize, onSplitBatch = oldSize, nil }()
	chk(t, p.migrateChannelSplit())
	if rows != 60 || batches != 9 {
		t.Fatalf("要分成 9 批共 60 列:%d 批 %d 列", batches, rows)
	}
	if n := unmigrated(t, p); n != 3 {
		t.Fatal("只有 3 個壞的略過,其他批照常展開:", n)
	}
	want := 60 - 3 - 19 // 有提及的(33 是壞資料,3 的倍數只有 19 個是空提及)
	if n := count(t, p, `select count(*) from omixor_channel_mentions`); n != want {
		t.Fatal("提及列數", n, want)
	}
	// 冪等:全部標記過的表不再掃(沒有新批)
	batches = 0
	chk(t, p.migrateChannelSplit())
	if batches != 1 { // 只剩 3 列壞的,各批仍是「略過」
		t.Fatal(batches)
	}
}

// 逾時只告警、不讓啟動失敗;下次啟動接著做
func TestPGMigrateTimeoutOnlyWarns(t *testing.T) {
	p := cabPG(t)
	rawChannel(t, p, "o", "CH-1", 1, `["user:a"]`)
	oldT := migrateTimeout
	migrateTimeout = time.Nanosecond
	err := p.migrateChannelSplit()
	migrateTimeout = oldT
	if err != nil {
		t.Fatal("逾時不能讓遷移回錯:", err)
	}
	if n := unmigrated(t, p); n != 1 {
		t.Fatal("逾時沒做完", n)
	}
	// 沒做完時讀取退回舊 jsonb
	if ch, err := p.GetChannel("o", "CH-1"); err != nil || !reflect.DeepEqual(ch.Members, lids(t, p, "o", "user:a")) {
		t.Fatal("退回讀取", ch, err)
	}
	chk(t, p.migrateChannelSplit())
	if n := unmigrated(t, p); n != 0 {
		t.Fatal("下次啟動接著做", n)
	}
}

// 已展開的新表(新版寫的)不被舊 jsonb 覆蓋,且「沒有未展開列」時 split_at 檢查走部分索引
func TestPGMigrateUsesPartialIndexes(t *testing.T) {
	p := cabPG(t)
	chk(t, p.migrateChannelSplit())
	chk(t, p.migrateSplitDocsAgents())
	chk(t, p.migrateCabinetLayouts())
	chk(t, p.migrateSplitIncidentsTasks())
	for _, s := range allSplitSpecs() {
		if n := count(t, p, `select count(*) from pg_indexes where schemaname = current_schema() and indexname = $1`, s.table+"_unsplit"); n != 1 {
			t.Fatal("缺部分索引", s.table)
		}
	}
	// 重跑不報錯(欄位、索引、子表都在,DDL 略過)
	chk(t, p.migrateSplitIncidentsTasks())
	if ok, err := p.incidentsTasksSchemaReady(context.Background()); err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestPGInvalidUTF8Sanitized(t *testing.T) {
	p := cabPG(t)
	at := mt("2026-01-02T03:04:05Z")
	bad := "壞\xff字"
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "dev", Active: true, CreatedAt: at,
		Routines: []model.AgentRoutine{{ID: "r", Title: bad, Instructions: bad, Kind: "daily", Priority: "normal", CreatedAt: at}}}))
	a, err := p.GetAgent("o", "dev")
	chk(t, err)
	if a.Routines[0].Title != "壞�字" {
		t.Fatalf("%q", a.Routines[0].Title)
	}
	task, err := p.CreateAgentTask("o", model.AgentTask{AgentID: "dev", Title: "t", Status: "queued", Priority: "normal", CreatedAt: at, UpdatedAt: at,
		Links: []string{bad}, Notes: []model.AgentTaskNote{{ID: "n", Author: whoID(t, p, "o", "a@x"), Text: bad, At: at}}})
	chk(t, err)
	g, err := p.GetAgentTask("o", task.ID)
	chk(t, err)
	if g.Notes[0].Text != "壞�字" || g.Links[0] != "壞�字" {
		t.Fatalf("%+v", g)
	}
	// 再更新一次(備註原樣):不報錯
	chk(t, p.UpdateAgentTask("o", *g))
}

// 標籤、頻道成員、提及寫入時去重;例行任務 id 重複拒絕
func TestPGDedup(t *testing.T) {
	m := testPG(t)
	at := mt("2026-01-02T03:04:05Z")
	chk(t, m.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	d, err := m.CreateDoc("o", model.CabinetDoc{Title: "t", Section: "s", Tags: []string{"a", "a", "b"}, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	g, _ := m.GetDoc("o", d.ID)
	sameJSON(t, "標籤", g.Tags, []string{"a", "b"})
	g.Tags = []string{"c", "c"}
	chk(t, m.UpdateDoc("o", *g))
	g, _ = m.GetDoc("o", d.ID)
	sameJSON(t, "更新標籤", g.Tags, []string{"c"})
	ua, ab, ux := lid(t, m, "o", "user:a"), lid(t, m, "o", "agent:b"), lid(t, m, "o", "user:x")
	ch, err := m.CreateChannel("o", model.Channel{Name: "c", Members: []string{ua, ua, ab}, CreatedBy: ua, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	c2, _ := m.GetChannel("o", ch.ID)
	sameJSON(t, "成員", c2.Members, []string{ua, ab})
	c2.Members = []string{ux, ux}
	chk(t, m.UpdateChannel("o", *c2))
	c2, _ = m.GetChannel("o", ch.ID)
	sameJSON(t, "更新成員", c2.Members, []string{ux})
	msg, err := m.CreateChannelMessage("o", model.ChannelMessage{ChannelID: ch.ID, Author: ua, Text: "t", Mentions: []string{ab, ab}, At: at})
	chk(t, err)
	// 注意:PG 的 CreateChannelMessage 回傳值原樣帶輸入的 Mentions(沒去重);去重發生在寫入表時,讀回才是去重後的
	sameJSON(t, "建立回傳的提及(未去重)", msg.Mentions, []string{ab, ab})
	ms, _, err := m.ListChannelMessages("o", ch.ID, model.MessageQuery{})
	chk(t, err)
	if len(ms) != 1 {
		t.Fatal(ms)
	}
	sameJSON(t, "讀回的提及", ms[0].Mentions, []string{ab})
	dup := model.Agent{ID: "dev", Name: "dev", Active: true, CreatedAt: at, Routines: []model.AgentRoutine{{ID: "r", Title: "a"}, {ID: "r", Title: "b"}}}
	if err := m.SaveAgent("o", dup); err == nil {
		t.Fatal("例行任務 id 重複要拒絕")
	}
}
