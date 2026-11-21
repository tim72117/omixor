package service_test

import (
	"errors"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// ISS-36 審查 #1:匯入的署名。組織管理員(非伺服器管理員)只能署名本組織成員、本組織角色、系統與自己,其餘落到 sys_legacy_*;
// 伺服器管理員搬遷歷史資料維持寬鬆

func importEnv(t *testing.T) (*service.Service, store.Store, *model.Principal, *model.Principal, map[string]string) {
	t.Helper()
	st := storetest.New(t)
	svc, root, boss, _, _ := issueEnv(t, st)
	now := model.Now()
	_ = st.CreateOrg(model.Org{ID: "o2", Name: "組織二", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "o3", Name: "組織三", CreatedAt: now})
	eve := storetest.User(t, st, "eve@y") // 別組織的使用者(不是 o1 的成員)
	_ = st.SaveMember(model.Member{Org: "o2", UserID: "u_eve_y", Email: "eve@y", Role: "member", AddedAt: now})
	spy := storetest.Agent(t, st, "o2", "spy") // 別組織的角色
	return svc, st, root, boss, map[string]string{"eve": eve.ID, "spy": spy.ID, "dev": storetest.Agent(t, st, "o1", "dev").ID,
		"alice": storetest.User(t, st, "alice@x").ID, "boss": storetest.User(t, st, "boss@x").ID, "root": storetest.User(t, st, "root@x").ID}
}

func actorKind(t *testing.T, st store.Store, id string) model.ActorKind {
	t.Helper()
	a, err := st.ActorByID(id)
	if err != nil {
		t.Fatalf("找不到 actor %q:%v", id, err)
	}
	return a.Kind
}

// 要的是「系統 ghost」:不是真人,id 是 sys_legacy_*
func wantGhost(t *testing.T, st store.Store, what, id string) {
	t.Helper()
	if !strings.HasPrefix(id, "sys_legacy_") || actorKind(t, st, id) != model.ActorKindSystem {
		t.Fatalf("%s 要落到 sys_legacy_*,實際 %s", what, id)
	}
}

func noUnregistered(t *testing.T, st store.Store, handles ...string) {
	t.Helper()
	for _, h := range handles {
		if _, err := st.ActorByHandle(model.ActorKindUser, "", h); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("不能建立未註冊使用者 %s:%v", h, err)
		}
	}
}

func TestImportIssuesOrgAdminCannotForgeAuthors(t *testing.T) {
	svc, st, _, boss, id := importEnv(t)
	list := []model.Issue{{
		ID: "ISS-1", Title: "一",
		CreatedBy: "eve@y", // 別組織的使用者 email
		Comments: []model.Comment{
			{Author: "alice@x", Text: "本組織成員"},
			{Author: id["eve"], Text: "別組織使用者的 actor id"},
			{Author: id["spy"], Text: "別組織角色的 actor id"},
			{Author: "agent:spy", Text: "別組織角色"},
			{Author: "zed@x", Text: "沒有這個人"},
			{Author: "任意字串", Text: "未知"},
			{Author: id["root"], Text: "伺服器管理員(不是 o1 成員)"},
			{Author: "agent:dev", Text: "本組織角色"},
			{Author: "routine", Text: "系統"},
			{Author: "boss@x", Text: "呼叫者本人"},
		},
	}}
	if _, err := svc.ImportIssues(boss, "o1", list); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.ListIssues("o1")
	if len(raw) != 1 {
		t.Fatalf("%d", len(raw))
	}
	i := raw[0]
	wantGhost(t, st, "別組織使用者 email 的建立者", i.CreatedBy)
	cs := i.Comments
	if cs[0].Author != id["alice"] {
		t.Fatalf("本組織成員要保留:%s", cs[0].Author)
	}
	for k, what := range map[int]string{1: "別組織使用者 actor id", 2: "別組織角色 actor id", 3: "別組織角色", 4: "不存在的使用者", 5: "未知字串", 6: "非成員的伺服器管理員"} {
		wantGhost(t, st, what, cs[k].Author)
	}
	if cs[7].Author != id["dev"] || cs[8].Author != model.SystemActorRoutine || cs[9].Author != id["boss"] {
		t.Fatalf("本組織角色、系統、呼叫者本人要保留:%+v", cs[7:])
	}
	noUnregistered(t, st, "zed@x")
	// 顯示:ghost 讀回原值(不是真人)
	got, err := svc.ExportIssues(boss, "o1")
	if err != nil || got[0].CreatedBy != "eve@y" || got[0].Comments[4].Author != "zed@x" || got[0].Comments[5].Author != "任意字串" {
		t.Fatalf("ghost 要顯示原值:%+v %v", got, err)
	}
}

// merge 模式(ImportIssuesWith)同一套規則
func TestImportIssuesWithMergeOrgAdminRestricted(t *testing.T) {
	svc, st, _, boss, id := importEnv(t)
	list := []model.Issue{{ID: "ISS-1", Title: "一", CreatedBy: id["eve"], Assignee: "spy", Comments: []model.Comment{{Author: "eve@y", Text: "x"}}}}
	if _, err := svc.ImportIssuesWith(boss, "o1", list, service.IssueImportOptions{Merge: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.ListIssues("o1")
	wantGhost(t, st, "建立者", raw[0].CreatedBy)
	wantGhost(t, st, "指派(別組織的角色)", raw[0].Assignee)
	wantGhost(t, st, "留言", raw[0].Comments[0].Author)
}

// 伺服器管理員搬遷歷史資料:寬鬆(離職的人、不在名單上的署名照舊保留成 actor);別組織的角色 actor id 仍不能署名
func TestImportIssuesServerAdminKeepsHistoricalAuthors(t *testing.T) {
	svc, st, root, _, id := importEnv(t)
	list := []model.Issue{{
		ID: "ISS-1", Title: "一", CreatedBy: "eve@y",
		Comments: []model.Comment{{Author: "zed@x", Text: "離職的人"}, {Author: "agent:dev", Text: "角色"}, {Author: id["spy"], Text: "別組織角色的 actor id"}},
	}}
	if _, err := svc.ImportIssues(root, "o3", list); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.ListIssues("o3")
	if raw[0].CreatedBy != id["eve"] {
		t.Fatalf("管理員匯入歷史署名維持寬鬆(對到現有使用者):%s", raw[0].CreatedBy)
	}
	if actorKind(t, st, raw[0].Comments[0].Author) != model.ActorKindUser {
		t.Fatal("歷史署名 zed@x 對到(未註冊)使用者 actor")
	}
	wantGhost(t, st, "別組織角色的 actor id(寬鬆也不行)", raw[0].Comments[2].Author)
	got, _ := svc.ExportIssues(root, "o3")
	if got[0].Comments[0].Author != "zed@x" {
		t.Fatalf("%+v", got[0].Comments)
	}
}

func TestImportAgentsOrgAdminRestricted(t *testing.T) {
	svc, st, _, boss, id := importEnv(t)
	_ = st.SaveMember(model.Member{Org: "o3", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: model.Now()})
	in := service.AgentImport{
		Agents: []model.Agent{{ID: "pm", Name: "PM"}},
		Tasks: []model.AgentTask{{ID: "T-1", AgentID: "pm", Title: "t", CreatedBy: "eve@y",
			Notes: []model.AgentTaskNote{{Author: "alice@x", Text: "a"}, {Author: id["spy"], Text: "b"}, {Author: "zed@x", Text: "c"}, {Author: "boss@x", Text: "d"}}}},
	}
	if _, err := svc.ImportAgents(boss, "o3", in); err != nil {
		t.Fatal(err)
	}
	ts, _ := st.ListAgentTasks("o3")
	if len(ts) != 1 {
		t.Fatalf("%d", len(ts))
	}
	wantGhost(t, st, "別組織使用者", ts[0].CreatedBy)
	n := ts[0].Notes
	if len(n) != 4 {
		t.Fatalf("%d", len(n))
	}
	// alice 不是 o3 成員、別組織角色、不存在 → ghost;呼叫者本人保留
	for _, x := range n {
		if x.Text == "d" {
			if x.Author != id["boss"] {
				t.Fatalf("呼叫者本人要保留:%s", x.Author)
			}
			continue
		}
		wantGhost(t, st, "備註作者 "+x.Text, x.Author)
	}
	noUnregistered(t, st, "zed@x")
}

func TestImportCabinetOrgAdminRestricted(t *testing.T) {
	svc, st, _, boss, id := importEnv(t)
	layout := []model.CabinetSection{{ID: "docs", Label: "文件"}}
	docs := []model.CabinetDoc{
		{ID: "DOC-1", Title: "a", Section: "docs", Author: "eve@y"},
		{ID: "DOC-2", Title: "b", Section: "docs", Author: id["spy"]},
		{ID: "DOC-3", Title: "c", Section: "docs", Author: "alice@x"},
		{ID: "DOC-4", Title: "d", Section: "docs", Author: "zed@x"},
	}
	if _, err := svc.ImportCabinet(boss, "o1", service.CabinetImport{Layout: layout, Docs: docs}); err != nil {
		t.Fatal(err)
	}
	by := map[string]string{}
	raw, _ := st.ListDocs("o1")
	for _, d := range raw {
		by[d.ID] = d.Author
	}
	wantGhost(t, st, "別組織使用者", by["DOC-1"])
	wantGhost(t, st, "別組織角色", by["DOC-2"])
	if by["DOC-3"] != id["alice"] {
		t.Fatalf("成員保留:%s", by["DOC-3"])
	}
	wantGhost(t, st, "不存在", by["DOC-4"])
	noUnregistered(t, st, "zed@x")
}

func TestImportIncidentsOrgAdminRestricted(t *testing.T) {
	svc, st, _, boss, id := importEnv(t)
	list := []model.Incident{{ID: "INC-1", Title: "t", Commander: "eve@y", CreatedBy: "zed@x",
		Timeline: []model.IncidentEvent{
			{Kind: "note", Text: "x", Actor: model.PersonActor("eve@y")},
			{Kind: "note", Text: "y", Actor: model.PersonActor("alice@x")},
			{Kind: "note", Text: "z", Actor: model.AIActor("spy")},
			{Kind: "note", Text: "w", Actor: model.AIActor("dev")},
			{Kind: "note", Text: "v", Actor: model.PersonActor(id["eve"])},
		}}}
	if _, err := svc.ImportIncidents(boss, "o1", list); err != nil {
		t.Fatal(err)
	}
	raw, err := st.ListIncidents("o1")
	if err != nil || len(raw) != 1 {
		t.Fatalf("%v %d", err, len(raw))
	}
	i := raw[0]
	wantGhost(t, st, "指揮官", i.Commander)
	wantGhost(t, st, "建立者", i.CreatedBy)
	ev := i.Timeline
	wantGhost(t, st, "事件 eve", ev[0].ActorID)
	wantGhost(t, st, "事件 spy", ev[2].ActorID)
	wantGhost(t, st, "事件 eve actor id", ev[4].ActorID)
	if ev[1].ActorID != id["alice"] || ev[3].ActorID != id["dev"] {
		t.Fatalf("成員與本組織角色保留:%+v", ev)
	}
	noUnregistered(t, st, "zed@x")
}
