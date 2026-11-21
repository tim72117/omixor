package store

// 事件的「誰」(ISS-36 C-2d):commander、created_by、時間軸 actor 直接存 actor id(不經過渡橋)

import (
	"errors"
	"testing"

	"omixor-core/model"
)

func TestIncidentActorColumnsRoundTrip(t *testing.T) {
	st := testPG(t)
	newOrg(t, st, "o")
	alice := newUser(t, st, "alice@x")
	bob := newUser(t, st, "bob@x")
	chkAgent := newAgent(t, st, "o", "cto", alice.ID)
	at := mt("2026-01-02T03:04:05Z")
	i, err := st.CreateIncident("o", model.Incident{Title: "a", Sev: 2, Status: "investigating", StartedAt: at, Commander: alice.ID, CreatedBy: chkAgent.ID,
		Timeline: []model.IncidentEvent{
			{ID: "e1", Date: at, ActorID: alice.ID, Kind: "note", Text: "人"},
			{ID: "e2", Date: at, ActorID: chkAgent.ID, Kind: "aiFinding", Text: "角色"},
			{ID: "e3", Date: at, ActorID: model.SystemActorOmixor, Kind: "status", Text: "系統"},
			{ID: "e4", Date: at, Kind: "note", Text: "沒有"},
		}})
	chk(t, err)
	g, err := st.GetIncident("o", i.ID)
	chk(t, err)
	if g.Commander != alice.ID || g.CreatedBy != chkAgent.ID || len(g.Timeline) != 4 {
		t.Fatalf("%+v", g)
	}
	for k, want := range []string{alice.ID, chkAgent.ID, model.SystemActorOmixor, ""} {
		if g.Timeline[k].ActorID != want || g.Timeline[k].Actor != (model.IncidentActor{}) {
			t.Fatalf("時間軸 %d:%+v(store 只回 actor id,Actor 相容輸出由服務層還原)", k, g.Timeline[k])
		}
	}
	// 沒有指揮官與建立者 = NULL,讀回空字串
	b, err := st.CreateIncident("o", model.Incident{Title: "b", Sev: 3, Status: "investigating", StartedAt: at})
	chk(t, err)
	var nulls int
	chk(t, st.(*PG).pool.QueryRow(ctx0(), `select count(*) from omixor_incidents where org = 'o' and id = $1 and commander is null and created_by is null`, b.ID).Scan(&nulls))
	if nulls != 1 {
		t.Fatal("沒有「誰」要存 NULL,不是空字串")
	}
	// 指揮官索引:(org, commander)
	g.Commander = bob.ID
	chk(t, st.UpdateIncident("o", *g))
	var n int
	chk(t, st.(*PG).pool.QueryRow(ctx0(), `select count(*) from omixor_incidents where org = 'o' and commander = $1`, bob.ID).Scan(&n))
	if n != 1 {
		t.Fatal("以 (org, commander) 查指揮官", n)
	}
	var idx int
	chk(t, st.(*PG).pool.QueryRow(ctx0(), `select count(*) from pg_indexes where indexname = 'omixor_incidents_org_commander'`).Scan(&idx))
	if idx != 1 {
		t.Fatal("(org, commander) 索引要在")
	}
	// 追加時間軸(只插新的幾列)
	g.Timeline = append(g.Timeline, model.IncidentEvent{ID: "e5", Date: at, ActorID: bob.ID, Kind: "note", Text: "追加"})
	chk(t, st.UpdateIncident("o", *g))
	g2, _ := st.GetIncident("o", i.ID)
	if len(g2.Timeline) != 5 || g2.Timeline[4].ActorID != bob.ID || g2.Timeline[0].ActorID != alice.ID {
		t.Fatalf("%+v", g2.Timeline)
	}
	// 不存在的 actor:外鍵擋成 ErrBadRef(建立、更新、時間軸都是)
	_, err = st.CreateIncident("o", model.Incident{Title: "x", StartedAt: at, Commander: "usr_nope"})
	if !errors.Is(err, ErrBadRef) {
		t.Fatal("指揮官不存在要 ErrBadRef", err)
	}
	_, err = st.CreateIncident("o", model.Incident{Title: "x", StartedAt: at, Timeline: []model.IncidentEvent{{ID: "z", ActorID: "agt_nope", Kind: "note", Text: "x"}}})
	if !errors.Is(err, ErrBadRef) {
		t.Fatal("時間軸 actor 不存在要 ErrBadRef", err)
	}
	g2.CreatedBy = "usr_nope"
	if err := st.UpdateIncident("o", *g2); !errors.Is(err, ErrBadRef) {
		t.Fatal("更新 created_by 不存在要 ErrBadRef", err)
	}
}

// 匯入舊格式(字串署名、舊的 {kind, id}):經寬鬆解析器轉成 actor id
func TestIncidentImportLegacyWho(t *testing.T) {
	st := testPG(t)
	newOrg(t, st, "o")
	alice := newUser(t, st, "alice@x")
	cto := newAgent(t, st, "o", "cto", alice.ID)
	at := mt("2026-01-02T03:04:05Z")
	_, err := st.ImportIncidents("o", []model.Incident{{ID: "INC-2001", Title: "舊", Sev: 2, Status: "identified", StartedAt: at, Commander: "alice@x", CreatedBy: "agent:cto",
		Timeline: []model.IncidentEvent{
			{ID: "a", Date: at, Actor: model.PersonActor("alice@x"), Kind: "note", Text: "1"},
			{ID: "b", Date: at, Actor: model.AIActor("cto"), Kind: "note", Text: "2"},
			{ID: "c", Date: at, Actor: model.SystemActor(), Kind: "note", Text: "3"},
			{ID: "d", Date: at, Actor: model.AIActor("auto-9"), Kind: "note", Text: "4"},
		}}})
	chk(t, err)
	g, err := st.GetIncident("o", "INC-2001")
	chk(t, err)
	if g.Commander != alice.ID || g.CreatedBy != cto.ID {
		t.Fatalf("%+v", g)
	}
	if g.Timeline[0].ActorID != alice.ID || g.Timeline[1].ActorID != cto.ID || g.Timeline[2].ActorID != model.SystemActorOmixor {
		t.Fatalf("%+v", g.Timeline)
	}
	a, err := st.ActorsByIDs([]string{g.Timeline[3].ActorID})
	chk(t, err)
	if r := a[g.Timeline[3].ActorID]; r.Kind != model.ActorKindSystem || r.Name != "auto-9" {
		t.Fatalf("不是角色的自動化 id 是 sys_legacy(名稱 = 原值):%+v", r)
	}
}
