package store

import (
	"context"
	"testing"
	"time"

	"omixor-core/model"
)

// 事件與任務備註、連結拆表(PG)的測試:往返、舊欄位不再寫、追加只插入新列、遷移(展開、壞資料略過、冪等)、cascade

func splitOrg(t *testing.T) *PG {
	t.Helper()
	p := testPG(t).(*PG)
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: model.Now()}))
	chk(t, p.CreateOrg(model.Org{ID: "p", Name: "p", CreatedAt: model.Now()}))
	return p
}

func fullIncident() model.Incident {
	at := mt("2026-01-02T03:04:05Z")
	ai := "AI 摘要"
	return model.Incident{
		Title: "資料庫掛了", Sev: 1, Status: "monitoring", Services: []string{"api", "db", "web"}, Commander: "tim",
		StartedAt: at, ResolvedAt: mtp("2026-01-02T08:00:00Z"), Impact: "部分使用者", AISummary: &ai,
		FollowUps: []string{"ISS-1", "T-2"}, AlertIDs: []string{"al-1", "al-2"}, CreatedBy: "mcp",
		Timeline: []model.IncidentEvent{
			{ID: "e1", Date: at, Actor: model.PersonActor("tim"), Kind: "note", Text: "開始"},
			{ID: "e2", Date: mt("2026-01-02T03:10:00Z"), Actor: model.AIActor("mcp"), Kind: "aiFinding", Text: "原因是 X"},
			{ID: "e3", Actor: model.SystemActor(), Kind: "status", Text: "狀態改為「觀察中」"}, // 沒有時間
		},
	}
}

// fullIncidentIDs 同 fullIncident,但「誰」是真的 actor id(CreateIncident、UpdateIncident 直接收 actor id,Actor 相容輸出由服務層還原)
func fullIncidentIDs(t *testing.T, p *PG, org string) model.Incident {
	t.Helper()
	i := fullIncident()
	tim, err := p.ResolveLegacy(org, "tim@x")
	chk(t, err)
	i.Commander, i.CreatedBy = tim, model.SystemActorOmixor
	i.Timeline[0].Actor, i.Timeline[0].ActorID = model.IncidentActor{}, tim
	i.Timeline[1].Actor, i.Timeline[1].ActorID = model.IncidentActor{}, model.SystemActorRoutine
	i.Timeline[2].Actor, i.Timeline[2].ActorID = model.IncidentActor{}, model.SystemActorOmixor
	return i
}

// convertedIncident 舊格式的事件(字串署名、舊 {kind, id})經寬鬆解析器轉成 actor id 之後的樣子(匯入、遷移展開的期望值)
func convertedIncident(t *testing.T, p *PG, org string, i model.Incident) model.Incident {
	t.Helper()
	i = i.Clone()
	var err error
	if i.Commander, err = p.ResolveLegacy(org, i.Commander); err != nil {
		t.Fatal(err)
	}
	if i.CreatedBy, err = p.ResolveLegacy(org, i.CreatedBy); err != nil {
		t.Fatal(err)
	}
	for k, e := range i.Timeline {
		raw := model.SystemActorOmixor
		switch e.Actor.Kind {
		case "person":
			raw = model.UserPrefix + e.Actor.ID
		case "ai":
			raw = e.Actor.ID
		}
		if i.Timeline[k].ActorID, err = p.ResolveLegacy(org, raw); err != nil {
			t.Fatal(err)
		}
		i.Timeline[k].Actor = model.IncidentActor{}
	}
	return i
}

// 建立後讀回一樣(結構、順序、nil 與空陣列都正規化成空陣列),整份取代後也是
func TestPGIncidentRoundTrip(t *testing.T) {
	p := splitOrg(t)
	at := mt("2026-01-02T03:04:05Z")
	var created []model.Incident
	for _, i := range []model.Incident{
		fullIncidentIDs(t, p, "o"),
		{Title: "最小", Status: "investigating", StartedAt: at},
		{Title: "空清單", Sev: 3, Status: "resolved", StartedAt: at, Services: []string{}, AlertIDs: []string{}, Timeline: []model.IncidentEvent{}},
	} {
		a, err := p.CreateIncident("o", i)
		chk(t, err)
		created = append(created, a)
	}
	// 注意:PG 的 CreateIncident 回傳值原樣帶輸入(nil 清單沒正規化);讀回的才是正規化後的空陣列。列表由新到舊
	pl, err := p.ListIncidents("o")
	chk(t, err)
	if len(pl) != 3 {
		t.Fatal(pl)
	}
	for k, i := range created {
		g, err := p.GetIncident("o", i.ID)
		chk(t, err)
		sameJSON(t, "ListIncidents 與 GetIncident "+i.ID, pl[len(pl)-1-k], g)
		if g.Services == nil || g.Timeline == nil || g.FollowUps == nil {
			t.Fatalf("沒給的清單要讀成空陣列(不是 nil):%+v", g)
		}
	}
	sameJSON(t, "完整的那筆讀回與輸入一樣", pl[2], created[0])
	// 整份取代:清單變短、變長、事件中間改一則
	u := pl[2].Clone()
	u.Services = []string{"x"}
	u.Timeline[1].Text = "改過"
	u.Timeline = append(u.Timeline[:2], model.IncidentEvent{ID: "e9", Date: at, ActorID: model.SystemActorOmixor, Kind: "note", Text: "新"})
	u.FollowUps = nil
	u.AlertIDs = []string{"z"}
	chk(t, p.UpdateIncident("o", u))
	g, err := p.GetIncident("o", u.ID)
	chk(t, err)
	if len(g.Services) != 1 || g.Services[0] != "x" || len(g.Timeline) != 3 || g.Timeline[1].Text != "改過" || g.Timeline[2].ID != "e9" ||
		len(g.AlertIDs) != 1 || g.AlertIDs[0] != "z" || g.FollowUps == nil || len(g.FollowUps) != 0 {
		t.Fatalf("UpdateIncident 後 %+v", g)
	}
}

// 舊 data 不再寫;追加時間軸只插入新的一列;子表有資料
func TestPGIncidentNewTablesAndAppend(t *testing.T) {
	p := splitOrg(t)
	inc, err := p.CreateIncident("o", fullIncidentIDs(t, p, "o"))
	chk(t, err)
	if n := count(t, p, `select count(*) from omixor_incidents where org = 'o' and data = '{}'::jsonb and split_at is not null`); n != 1 {
		t.Fatal("data 要維持空物件、split_at 要有值", n)
	}
	if n := count(t, p, `select count(*) from omixor_incident_events where org = 'o' and incident_id = $1`, inc.ID); n != 3 {
		t.Fatal("事件列", n)
	}
	if n := count(t, p, `select count(*) from omixor_incident_services where org = 'o' and incident_id = $1`, inc.ID); n != 3 {
		t.Fatal("服務列", n)
	}
	if n := count(t, p, `select count(*) from omixor_incidents where org = 'o' and sev = 1 and commander in (select id from omixor_actors where handle = 'tim@x') and title <> '' and ai_summary is not null and resolved_at is not null`); n != 1 {
		t.Fatal("純量欄位")
	}
	// 追加一則:前三列的 xmin 不變(沒被重寫),只多一列
	xmin := func() string {
		var s string
		chk(t, p.pool.QueryRow(context.Background(), `select string_agg(xmin::text, ',' order by pos) from omixor_incident_events where org = 'o' and incident_id = $1`, inc.ID).Scan(&s))
		return s
	}
	before := xmin()
	inc.Timeline = append(inc.Timeline, model.IncidentEvent{ID: "e4", Date: mt("2026-01-02T09:00:00Z"), ActorID: inc.Commander, Kind: "note", Text: "追加"})
	chk(t, p.UpdateIncident("o", inc))
	after := xmin()
	if len(after) <= len(before) || after[:len(before)] != before {
		t.Fatalf("追加不該重寫舊列\n前:%s\n後:%s", before, after)
	}
	g, err := p.GetIncident("o", inc.ID)
	chk(t, err)
	sameJSON(t, "追加後", g, inc)
	// 狀態、嚴重度查詢用的索引欄位
	if n := count(t, p, `select count(*) from omixor_incidents where org = 'o' and status = 'monitoring' and sev = 1`); n != 1 {
		t.Fatal(n)
	}
}

func TestPGTaskNotesLinks(t *testing.T) {
	p := splitOrg(t)
	makeAgent(t, p, "o", "dev")
	makeAgent(t, p, "o", "ops")
	at := mt("2026-01-02T03:04:05Z")
	base := model.AgentTask{AgentID: "dev", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at,
		Links: []string{"https://a", "ISS-1", "https://c"},
		Notes: []model.AgentTaskNote{{ID: "n1", Author: whoID(t, p, "o", "agent:dev"), Text: "一", At: at}, {ID: "n2", Author: whoID(t, p, "o", "agent:ops"), Text: "二", At: at}}}
	a, err := p.CreateAgentTask("o", base)
	chk(t, err)
	if n := count(t, p, `select count(*) from omixor_agent_tasks where org='o' and notes = '[]'::jsonb and links = '[]'::jsonb and split_at is not null`); n != 1 {
		t.Fatal("舊 jsonb 不再寫")
	}
	xmin := func() string {
		var s string
		chk(t, p.pool.QueryRow(context.Background(), `select string_agg(xmin::text, ',' order by pos) from omixor_agent_task_notes where org='o' and task_id=$1`, a.ID).Scan(&s))
		return s
	}
	before := xmin()
	a.Notes = append(a.Notes, model.AgentTaskNote{ID: "n3", Author: whoID(t, p, "o", "agent:dev"), Text: "三", At: at})
	a.UpdatedAt = mt("2026-01-02T04:00:00Z")
	chk(t, p.UpdateAgentTask("o", a))
	if after := xmin(); len(after) <= len(before) || after[:len(before)] != before {
		t.Fatalf("追加備註不該重寫舊列 %s → %s", before, after)
	}
	// 連結被改、第一則備註被改
	a.Links = []string{"x"}
	a.Notes[0].Text = "改"
	chk(t, p.UpdateAgentTask("o", a))
	g, err := p.GetAgentTask("o", a.ID)
	chk(t, err)
	sameJSON(t, "改過", g, a)
	l, err := p.ListAgentTasks("o")
	chk(t, err)
	if len(l) != 1 || len(l[0].Notes) != 3 || len(l[0].Links) != 1 {
		t.Fatal(l)
	}
	// 領取也寫子表
	now := mt("2026-01-02T05:00:00Z").Time
	a.Status, a.ClaimedBy = "inProgress", whoID(t, p, "o", "x")
	a.Notes = append(a.Notes, model.AgentTaskNote{ID: "n4", Author: whoID(t, p, "o", "mcp"), Text: "領取", At: model.Time{Time: now}})
	lu := model.Time{Time: now.Add(time.Hour)}
	a.LockedUntil = &lu
	chk(t, p.ClaimAgentTask("o", a, now))
	g, _ = p.GetAgentTask("o", a.ID)
	if len(g.Notes) != 4 || g.Status != "inProgress" {
		t.Fatalf("領取後 %+v", g)
	}
	// 鎖住時第二次領取失敗,而且不能動子表
	a2 := g.Clone()
	a2.Notes = append(a2.Notes, model.AgentTaskNote{ID: "n5", Author: whoID(t, p, "o", "z"), Text: "搶", At: model.Time{Time: now}})
	if err := p.ClaimAgentTask("o", a2, now); err != ErrLocked {
		t.Fatal(err)
	}
	if n := count(t, p, `select count(*) from omixor_agent_task_notes where org='o' and task_id=$1`, a.ID); n != 4 {
		t.Fatal("被鎖住的領取不能寫備註", n)
	}
	// 角色轉移:加備註、改 agent_id,備註保留
	_, err = p.TransferAgentTasks("o", AgentTransfer{From: "dev", To: "ops", At: now, NoteAuthor: whoID(t, p, "o", "tim"), NoteText: "轉移", OpenOnly: false})
	chk(t, err)
	g, _ = p.GetAgentTask("o", a.ID)
	if g.AgentID != "ops" || len(g.Notes) != 5 || g.Notes[4].Text != "轉移" || g.Notes[0].Text != "改" {
		t.Fatalf("轉移後 %+v", g)
	}
}

// cascade:刪組織、改事件 id 時子表跟著;刪角色轉移後任務(與備註)還在
func TestPGSplitCascade(t *testing.T) {
	p := splitOrg(t)
	makeAgent(t, p, "o", "dev")
	makeAgent(t, p, "p", "dev")
	at := mt("2026-01-02T03:04:05Z")
	for _, org := range []string{"o", "p"} {
		_, err := p.CreateIncident(org, fullIncidentIDs(t, p, org))
		chk(t, err)
		_, err = p.CreateAgentTask(org, model.AgentTask{AgentID: "dev", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at,
			Links: []string{"a"}, Notes: []model.AgentTaskNote{{ID: "n", Author: whoID(t, p, org, "a"), Text: "t", At: at}}})
		chk(t, err)
	}
	// 孤兒寫不進去(外鍵)
	if _, err := p.pool.Exec(context.Background(), `insert into omixor_incident_events (org, incident_id, pos) values ('o','INC-999',0)`); err == nil {
		t.Fatal("事件子表要有外鍵")
	}
	if _, err := p.pool.Exec(context.Background(), `insert into omixor_agent_task_notes (org, task_id, pos) values ('o','T-999',0)`); err == nil {
		t.Fatal("備註子表要有外鍵")
	}
	// on update cascade
	if _, err := p.pool.Exec(context.Background(), `update omixor_incidents set id = 'INC-3000' where org = 'o' and id = 'INC-2001'`); err != nil {
		t.Fatal(err)
	}
	if n := count(t, p, `select count(*) from omixor_incident_events where org = 'o' and incident_id = 'INC-3000'`); n != 3 {
		t.Fatal("改 id 子表要跟著", n)
	}
	chk(t, p.DeleteOrg("o"))
	for _, tb := range []string{"omixor_incident_services", "omixor_incident_follow_ups", "omixor_incident_alerts", "omixor_incident_events", "omixor_agent_task_notes", "omixor_agent_task_links"} {
		if n := count(t, p, `select count(*) from `+tb+` where org = 'o'`); n != 0 {
			t.Fatal(tb, "刪組織後要清掉", n)
		}
		if n := count(t, p, `select count(*) from `+tb+` where org = 'p'`); n == 0 {
			t.Fatal(tb, "別的組織不能被刪")
		}
	}
	// 直接刪任務:備註、連結跟著
	if _, err := p.pool.Exec(context.Background(), `delete from omixor_agent_tasks where org = 'p'`); err != nil {
		t.Fatal(err)
	}
	if n := count(t, p, `select count(*) from omixor_agent_task_notes`) + count(t, p, `select count(*) from omixor_agent_task_links`); n != 0 {
		t.Fatal("刪任務後子表要清掉", n)
	}
}

// 事件、任務備註的匯入也寫新結構
func TestPGImportWritesNewTables(t *testing.T) {
	p := splitOrg(t)
	makeAgent(t, p, "o", "dev")
	at := mt("2026-01-02T03:04:05Z")
	i := fullIncident()
	i.ID = "INC-2041"
	n, err := p.ImportIncidents("o", []model.Incident{i})
	chk(t, err)
	if n != 1 {
		t.Fatal(n)
	}
	g, err := p.GetIncident("o", "INC-2041")
	chk(t, err)
	sameJSON(t, "匯入事件(舊字串經寬鬆解析器轉成 actor id)", g, convertedIncident(t, p, "o", i))
	chk(t, p.ImportAgents("p", []model.Agent{{ID: "dev", Name: "dev", Active: true, CreatedAt: at}}, []model.AgentTask{{ID: "T-7", AgentID: "dev", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at,
		Links: []string{"a"}, Notes: []model.AgentTaskNote{{ID: "n", Author: whoID(t, p, "p", "a"), Text: "t", At: at}}}}))
	tk, err := p.GetAgentTask("p", "T-7")
	chk(t, err)
	if len(tk.Notes) != 1 || len(tk.Links) != 1 {
		t.Fatalf("%+v", tk)
	}
}
