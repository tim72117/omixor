package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

func ip(n int) *int { return &n }

// 事件:成員宣告與更新、執行者一律是自己、狀態與時間軸規則、組織外的人 403、匯入只給管理員且只在空的時候
func TestIncidentDeclareAndUpdate(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	stranger := &model.Principal{User: model.User{ID: "u9", Email: "x@y", Role: "member"}}
	if _, err := svc.DeclareIncident(stranger, "o1", service.IncidentInput{Title: "x", Sev: 1}); code(err) != 403 {
		t.Fatal("不是成員要 403", err)
	}
	if _, err := svc.DeclareIncident(alice, "o1", service.IncidentInput{Title: " ", Sev: 1}); code(err) != 400 {
		t.Fatal("沒有標題要 400", err)
	}
	if _, err := svc.DeclareIncident(alice, "o1", service.IncidentInput{Title: "x", Sev: 5}); code(err) != 400 {
		t.Fatal("sev 超出範圍要 400", err)
	}
	inc, err := svc.DeclareIncident(alice, "o1", service.IncidentInput{Title: " 付款延遲 ", Sev: 2, Services: []string{"payments", " payments", ""},
		Impact: "開通延遲", AlertIDs: []string{"ALT-1"}, AlertTitles: map[string]string{"ALT-1": "p95 過高"},
		Actor: &model.Actor{Kind: "ai", ID: "冒名"}, Author: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if inc.ID != "INC-2001" || inc.Title != "付款延遲" || inc.Status != "investigating" || strings.Join(inc.Services, ",") != "payments" ||
		inc.Commander != "Alice@X" || inc.CreatedBy != "Alice@X" || len(inc.Timeline) != 2 {
		t.Fatalf("%+v", inc)
	}
	if e := inc.Timeline[0]; e.Kind != "status" || e.Text != "宣告 SEV2 事件,指揮官:Alice@X" || e.Actor != model.PersonActor("Alice@X") {
		t.Fatalf("一般成員的執行者一律是自己:%+v", e)
	}
	if e := inc.Timeline[1]; e.Kind != "alert" || e.Text != "關聯告警「p95 過高」" {
		t.Fatalf("%+v", e)
	}
	// 更新:狀態(沒給 note 是預設文字)、註記、AI 發現、摘要
	u, err := svc.UpdateIncident(alice, "o1", "inc-2001", service.IncidentPatch{Status: sp("resolved"), Finding: sp("鎖競爭"), AISummary: sp("根因")})
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != "resolved" || u.ResolvedAt == nil || len(u.Timeline) != 4 || u.Timeline[2].Text != "狀態改為「已解決」" ||
		u.Timeline[3].Kind != "aiFinding" || u.AISummary == nil || *u.AISummary != "根因" {
		t.Fatalf("%+v", u)
	}
	u, err = svc.UpdateIncident(boss, "o1", "INC-2001", service.IncidentPatch{Status: sp("monitoring"), Note: sp("又出現了"), AISummary: sp(""),
		AddFollowUps: []string{"OPS-1", "OPS-1"}, Events: []service.IncidentEventInput{{Kind: "deploy", Text: "回滾 v3.3.6", Actor: &model.Actor{Kind: "system"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if u.ResolvedAt != nil || u.AISummary != nil || strings.Join(u.FollowUps, ",") != "OPS-1" || u.Timeline[4].Text != "又出現了" ||
		u.Timeline[5].Actor.Kind != "system" || u.Timeline[4].Actor.ID != "boss@x" {
		t.Fatalf("改回其他狀態清掉 resolvedAt、空摘要清掉:%+v", u)
	}
	if _, err := svc.UpdateIncident(alice, "o1", "INC-2001", service.IncidentPatch{Status: sp("done")}); code(err) != 400 {
		t.Fatal("狀態不對要 400", err)
	}
	if _, err := svc.UpdateIncident(alice, "o1", "INC-2001", service.IncidentPatch{Events: []service.IncidentEventInput{{Kind: "x", Text: "y"}}}); code(err) != 400 {
		t.Fatal("時間軸種類不對要 400", err)
	}
	if _, err := svc.UpdateIncident(alice, "o1", "INC-9", service.IncidentPatch{Note: sp("x")}); code(err) != 404 {
		t.Fatal("找不到要 404", err)
	}
	// 只有註記(空白不加)
	u, _ = svc.UpdateIncident(alice, "o1", "INC-2001", service.IncidentPatch{Note: sp("  ")})
	if len(u.Timeline) != 6 {
		t.Fatal("空白註記不加", len(u.Timeline))
	}
	l, _ := svc.ListIncidents(alice, "o1")
	if len(l) != 1 {
		t.Fatal(l)
	}
	// 角色憑證可以看、宣告,執行者是該角色
	g, err := svc.DeclareIncident(agentP("pm"), "o1", service.IncidentInput{Title: "y", Sev: 3})
	if err != nil || g.ID != "INC-2002" || g.Timeline[0].Actor != model.AIActor("pm") || g.CreatedBy != "agent:pm" {
		t.Fatalf("%+v %v", g, err)
	}
}

// 伺服器管理員(app、web 內建核心)可以指定執行者與初始時間軸
func TestIncidentServerAdminActor(t *testing.T) {
	svc, st, _, _, _ := chainEnv(t)
	_ = st.SaveUser(model.User{ID: "root", Email: "ops-web", Role: "admin"})
	root := &model.Principal{User: model.User{ID: "root", Email: "ops-web", Role: "admin"}}
	inc, err := svc.DeclareIncident(root, "o1", service.IncidentInput{Title: "x", Sev: 1, Author: "tim", Commander: "tim", CommanderName: "蔡庭宇"})
	if err != nil || inc.Timeline[0].Actor != model.PersonActor("tim") || inc.Timeline[0].Text != "宣告 SEV1 事件,指揮官:蔡庭宇" {
		t.Fatalf("%+v %v", inc, err)
	}
	inc, _ = svc.DeclareIncident(root, "o1", service.IncidentInput{Title: "y", Sev: 2})
	if inc.Timeline[0].Actor != model.AIActor("mcp") || inc.CreatedBy != "mcp" {
		t.Fatalf("沒指定是 ai/mcp:%+v", inc)
	}
	at := model.Time{Time: inc.StartedAt.Add(-3600e9)}
	inc, _ = svc.DeclareIncident(root, "o1", service.IncidentInput{Title: "z", Sev: 3, StartedAt: &at, Actor: &model.Actor{Kind: "person", ID: "tim"},
		Timeline: []service.IncidentEventInput{{Kind: "alert", Text: "告警觸發", Date: &at, Actor: &model.Actor{Kind: "system"}}, {Kind: "status", Text: "從告警建立"}}})
	if len(inc.Timeline) != 2 || !inc.StartedAt.Equal(at.Time) || inc.Timeline[1].Actor != model.PersonActor("tim") || !inc.Timeline[0].Date.Equal(at.Time) {
		t.Fatalf("%+v", inc)
	}
}

func TestIncidentImport(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	list := []model.Incident{{ID: "inc-2041", Title: "a", Sev: 2, Status: "identified",
		Timeline: []model.IncidentEvent{{Kind: "note", Text: "x", Actor: model.SystemActor()}}}, {ID: "INC-2040", Title: "b", Sev: 9, Status: "?"}}
	if _, err := svc.ImportIncidents(alice, "o1", list); code(err) != 403 {
		t.Fatal("要組織管理員", err)
	}
	res, err := svc.ImportIncidents(boss, "o1", list)
	if err != nil || res.Inserted != 2 {
		t.Fatal(res, err)
	}
	if _, err := svc.ImportIncidents(boss, "o1", list); code(err) != 409 {
		t.Fatal("已經有事件要 409", err)
	}
	l, _ := svc.ListIncidents(alice, "o1")
	if l[0].ID != "INC-2041" || l[0].Timeline[0].ID == "" || l[1].Sev != 3 || l[1].Status != "investigating" {
		t.Fatalf("%+v", l)
	}
	if n, _ := svc.DeclareIncident(alice, "o1", service.IncidentInput{Title: "c", Sev: 4}); n.ID != "INC-2042" {
		t.Fatal("接著最大號碼編", n.ID)
	}
}
