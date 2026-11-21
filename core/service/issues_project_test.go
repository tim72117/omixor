package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

func mkProject(t *testing.T, svc *service.Service, p *model.Principal, name, key string) *model.Project {
	t.Helper()
	x, err := svc.CreateProject(p, "o1", service.ProjectInput{Name: name, Key: key})
	if err != nil {
		t.Fatal(name, key, err)
	}
	return x
}

// 建立時帶專案:專案不存在 404、沒有代號 400;專案內與組織層級各自連號
func TestIssueCreateInProject(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	app := mkProject(t, svc, boss, "App", "APP")
	web := mkProject(t, svc, boss, "Web", "web")
	bare := mkProject(t, svc, boss, "無代號", "")
	if web.Key != "WEB" {
		t.Fatal("代號轉大寫", web.Key)
	}
	if _, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "x", Project: sp("PRJ-99")}); code(err) != 404 {
		t.Fatal("專案不存在", err)
	}
	_, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "x", Project: sp(bare.ID)})
	if code(err) != 400 || !strings.Contains(err.Error(), "這個專案還沒設定代號") {
		t.Fatal("沒有代號 400", err)
	}
	if l, _ := svc.ListIssues(alice, "o1"); len(l) != 0 {
		t.Fatal("失敗不能留下 Issue", l)
	}
	mk := func(project string) *model.Issue {
		i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Project: sp(project)})
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	a1, a2, o1, w1, a3, o2 := mk(app.ID), mk(strings.ToLower(app.ID)), mk(""), mk(web.ID), mk(app.ID), mk("")
	got := []string{a1.Key, a2.Key, o1.Key, w1.Key, a3.Key, o2.Key}
	if strings.Join(got, ",") != "APP-1,APP-2,ISS-1,WEB-1,APP-3,ISS-2" {
		t.Fatal("各範圍各自連號:", got)
	}
	if a1.Project != app.ID || o1.Project != "" || a1.Num != 1 || a3.Num != 3 || !model.IsUUID(a1.ID) || a1.ID == a1.Key {
		t.Fatalf("%+v", a1)
	}
	// 專案篩選
	q := func(project string) string {
		l, err := svc.QueryIssues(alice, "o1", model.IssueQuery{Project: project})
		if err != nil {
			t.Fatal(err)
		}
		return keysOf(l)
	}
	if q("") != "ISS-2,APP-3,WEB-1,ISS-1,APP-2,APP-1" || q("-") != "ISS-2,ISS-1" || q(app.ID) != "APP-3,APP-2,APP-1" || q(web.ID) != "WEB-1" {
		t.Fatal("專案篩選:", q(""), "|", q("-"), "|", q(app.ID), "|", q(web.ID))
	}
}

// 搬移專案:發新 key,舊 key 仍可解析(取得、修改、留言、頻道綁 Issue、任務連結);搬回組織層級發新的 ISS-n
func TestIssueMoveKeepsOldKeys(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	app := mkProject(t, svc, boss, "App", "APP")
	web := mkProject(t, svc, boss, "Web", "WEB")
	i, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Assignee: "dev"}) // ISS-1,排了一筆任務
	_, _ = svc.CreateIssue(alice, "o1", service.IssueInput{Title: "占號"})                  // ISS-2

	m, err := svc.UpdateIssue(alice, "o1", i.Key, service.IssuePatch{Project: sp(app.ID)})
	if err != nil || m.Key != "APP-1" || m.Project != app.ID || m.ID != i.ID || m.Version != 2 {
		t.Fatalf("搬進專案:%+v %v", m, err)
	}
	// 沒變的專案不發新 key
	if m2, err := svc.UpdateIssue(alice, "o1", "APP-1", service.IssuePatch{Project: sp(app.ID)}); err != nil || m2.Key != "APP-1" || m2.Version != 2 {
		t.Fatal("同一個專案不發新 key", m2, err)
	}
	for _, ref := range []string{"ISS-1", "iss-1", "APP-1", "app-1", i.ID, strings.ToUpper(i.ID)} {
		g, err := svc.GetIssue(alice, "o1", ref)
		if err != nil || g.ID != i.ID {
			t.Fatal("要解析到同一張:", ref, err)
		}
	}
	if _, err := svc.UpdateIssue(alice, "o1", "ISS-1", service.IssuePatch{Title: sp("改")}); err != nil {
		t.Fatal("舊 key 可以修改", err)
	}
	if g, err := svc.AddIssueComment(alice, "o1", "iss-1", service.CommentInput{Text: "舊 key 留言"}); err != nil || len(g.Comments) != 1 || g.ID != i.ID {
		t.Fatal("舊 key 可以留言", g, err)
	}
	ch, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "討論", IssueID: "ISS-1"})
	if err != nil || ch.IssueID != i.ID {
		t.Fatalf("頻道可以用舊 key 綁 Issue(存內部 id):%+v %v", ch, err)
	}
	if again, err := svc.CreateChannel(alice, "o1", service.ChannelInput{Name: "又一個", IssueID: "APP-1"}); err != nil || again.ID != ch.ID {
		t.Fatal("用新 key 綁同一張 Issue 回傳同一個頻道", again, err)
	}
	// 任務連結:手動派的任務連結舊 key,取消指派時要一起取消(排隊中的)
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "dev", Title: "手動", Links: []string{"ISS-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIssue(alice, "o1", "APP-1", service.IssuePatch{Assignee: sp("")}); err != nil {
		t.Fatal(err)
	}
	if g, err := svc.GetAgentTask(boss, "o1", tk.ID); err != nil || g.Status != "canceled" {
		t.Fatal("連結舊 key 的任務要被取消:", g, err)
	}

	// 專案之間搬:APP-1 → WEB-1;舊的兩個 key 都解析得到
	m, err = svc.UpdateIssue(alice, "o1", "ISS-1", service.IssuePatch{Project: sp(web.ID)})
	if err != nil || m.Key != "WEB-1" {
		t.Fatal(m, err)
	}
	for _, ref := range []string{"ISS-1", "APP-1", "WEB-1"} {
		if g, err := svc.GetIssue(alice, "o1", ref); err != nil || g.ID != i.ID {
			t.Fatal(ref, err)
		}
	}
	// 搬回組織層級:發新的 ISS-n(不沿用 ISS-1;ISS-2 被占了所以是 ISS-3)
	m, err = svc.UpdateIssue(alice, "o1", i.ID, service.IssuePatch{Project: sp("")})
	if err != nil || m.Key != "ISS-3" || m.Project != "" || m.Num != 3 {
		t.Fatalf("搬回組織層級:%+v %v", m, err)
	}
	for _, ref := range []string{"ISS-1", "APP-1", "WEB-1", "ISS-3"} {
		if g, err := svc.GetIssue(alice, "o1", ref); err != nil || g.ID != i.ID || g.Key != "ISS-3" {
			t.Fatal(ref, err)
		}
	}
	// 搬到不存在的專案 404、沒有代號 400;Issue 不動
	bare := mkProject(t, svc, boss, "無代號", "")
	if _, err := svc.UpdateIssue(alice, "o1", i.ID, service.IssuePatch{Project: sp("PRJ-99")}); code(err) != 404 {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIssue(alice, "o1", i.ID, service.IssuePatch{Project: sp(bare.ID)}); code(err) != 400 {
		t.Fatal(err)
	}
	if g, _ := svc.GetIssue(alice, "o1", i.ID); g.Key != "ISS-3" {
		t.Fatal(g.Key)
	}
	// 版本不符時不能搬
	if _, err := svc.UpdateIssue(alice, "o1", i.ID, service.IssuePatch{Project: sp(app.ID), Version: iptr(1)}); code(err) != 409 {
		t.Fatal("舊版本不能搬", err)
	}
	if g, _ := svc.GetIssue(alice, "o1", i.ID); g.Key != "ISS-3" || g.Project != "" {
		t.Fatal("409 不能搬", g)
	}
}

// 專案的限制:有 Issue(含垃圾桶)不能刪;代號在有 Issue 時不能改;代號重複、保留字、格式
func TestProjectKeyRules(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	app := mkProject(t, svc, boss, "App", "APP")
	for _, bad := range []string{"A", "ABCDEFGHIJK", "1AB", "A-B", "A B", "中文", "A_B"} {
		if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "p" + bad, Key: bad}); code(err) != 400 {
			t.Fatal("格式不合:", bad, err)
		}
	}
	for _, r := range []string{"ISS", "PRJ", "T", "DOC", "CH", "INC", "iss"} {
		if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "r" + r, Key: r}); code(err) < 400 || code(err) >= 500 {
			t.Fatal("保留字要擋:", r, err)
		}
	}
	for _, ok := range []string{"AB", "A1", "ABCDEFGHI1"} {
		if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "ok" + ok, Key: ok}); err != nil {
			t.Fatal("合法的代號:", ok, err)
		}
	}
	if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "重複", Key: "app"}); code(err) != 409 {
		t.Fatal("代號重複(不分大小寫)", err)
	}
	other := mkProject(t, svc, boss, "Other", "")
	if _, err := svc.UpdateProject(boss, "o1", other.ID, service.ProjectPatch{Key: sp("APP")}); code(err) != 409 {
		t.Fatal("改成重複的代號", err)
	}
	if _, err := svc.UpdateProject(boss, "o1", other.ID, service.ProjectPatch{Key: sp("ISS")}); code(err) < 400 || code(err) >= 500 {
		t.Fatal("改成保留字", err)
	}
	if u, err := svc.UpdateProject(boss, "o1", other.ID, service.ProjectPatch{Key: sp("oth")}); err != nil || u.Key != "OTH" {
		t.Fatal("沒有 Issue 時可以設定、改代號", u, err)
	}
	if u, err := svc.UpdateProject(boss, "o1", app.ID, service.ProjectPatch{Name: sp("App2")}); err != nil || u.Key != "APP" {
		t.Fatal("只改名稱不動代號", u, err)
	}

	// 專案有 Issue 之後
	i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Project: sp(app.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateProject(boss, "o1", app.ID, service.ProjectPatch{Key: sp("MOB")}); code(err) != 409 {
		t.Fatal("有 Issue 不能改代號", err)
	}
	if u, err := svc.UpdateProject(boss, "o1", app.ID, service.ProjectPatch{Key: sp("app")}); err != nil || u.Key != "APP" {
		t.Fatal("代號沒有變(只差大小寫)不算改", u, err)
	}
	if err := svc.DeleteProject(boss, "o1", app.ID); code(err) != 409 {
		t.Fatal("有 Issue 不能刪專案", err)
	}
	// 丟進垃圾桶也算
	if err := svc.DeleteIssue(alice, "o1", i.Key); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProject(boss, "o1", app.ID); code(err) != 409 {
		t.Fatal("垃圾桶裡的也算", err)
	}
	if _, err := svc.UpdateProject(boss, "o1", app.ID, service.ProjectPatch{Key: sp("MOB")}); code(err) != 409 {
		t.Fatal("垃圾桶裡有 Issue 也不能改代號", err)
	}
	// 永久刪除後才能刪
	if err := svc.PurgeIssue(boss, "o1", i.Key); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProject(boss, "o1", app.ID); err != nil {
		t.Fatal("清空後可以刪專案", err)
	}
	// 搬走也可以(不是永久刪除)
	p2 := mkProject(t, svc, boss, "P2", "PTWO")
	j, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Project: sp(p2.ID)})
	if _, err := svc.UpdateIssue(alice, "o1", j.Key, service.IssuePatch{Project: sp("")}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProject(boss, "o1", p2.ID); err != nil {
		t.Fatal("Issue 搬走後可以刪專案", err)
	}
	if g, err := svc.GetIssue(alice, "o1", j.Key); err != nil || g.Project != "" {
		t.Fatal(g, err)
	}
}

// 解析:UUID、key、舊 key、不分大小寫的 key 解析到同一張;不存在 404;別的組織查不到
func TestResolveIssueRef(t *testing.T) {
	st := storetest.New(t)
	svc, root, boss, alice, _ := issueEnv(t, st)
	app := mkProject(t, svc, boss, "App", "APP")
	i, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	other, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "另一張"})
	_, _ = svc.UpdateIssue(alice, "o1", i.Key, service.IssuePatch{Project: sp(app.ID)})
	for _, ref := range []string{i.ID, strings.ToUpper(i.ID), " " + i.ID + " ", "APP-1", "app-1", "ISS-1", "iss-1", " Iss-1 "} {
		got, err := svc.ResolveIssueID(alice, "o1", ref)
		if err != nil || got != i.ID {
			t.Fatalf("%q 要解析到 %s:%s %v", ref, i.ID, got, err)
		}
	}
	if got, _ := svc.ResolveIssueID(alice, "o1", "ISS-2"); got != other.ID {
		t.Fatal("ISS-2 是另一張", got)
	}
	for _, ref := range []string{"ISS-99", "APP-2", "nope", "", "01a1f739-4500-73fa-80b6-b5c78ad0d654"} {
		if _, err := svc.ResolveIssueID(alice, "o1", ref); code(err) != 404 {
			t.Fatalf("%q 要 404:%v", ref, err)
		}
		if ref == "" {
			continue // GetIssue("") 見 TestGetIssueEmptyRef
		}
		if _, err := svc.GetIssue(alice, "o1", ref); code(err) != 404 {
			t.Fatalf("GetIssue %q 要 404:%v", ref, err)
		}
	}
	// 非成員不能解析
	if _, err := svc.ResolveIssueID(&model.Principal{User: model.User{ID: "u9", Email: "x@x"}}, "o1", i.Key); code(err) != 403 {
		t.Fatal(err)
	}
	// 別的組織查不到(以組織為界)
	if err := st.CreateOrg(model.Org{ID: "o2", Name: "二", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{i.ID, "ISS-1", "APP-1"} {
		if _, err := svc.ResolveIssueID(root, "o2", ref); code(err) != 404 {
			t.Fatalf("o2 查不到 o1 的 %q:%v", ref, err)
		}
	}
	// o2 自己的 ISS-1 是另一張
	j, err := svc.CreateIssue(root, "o2", service.IssueInput{Title: "o2 的"})
	if err != nil || j.Key != "ISS-1" || j.ID == i.ID {
		t.Fatal(j, err)
	}
	if got, err := svc.ResolveIssueID(root, "o2", "ISS-1"); err != nil || got != j.ID {
		t.Fatal(got, err)
	}
	if got, _ := svc.ResolveIssueID(alice, "o1", "ISS-1"); got != i.ID {
		t.Fatal("o1 的 ISS-1 不受影響", got)
	}
}

// 空的編號不能解析成任何 Issue(目前 store.GetIssue 的 ref 為空時會列出全部、回最新的一張:疑似缺陷)
func TestGetIssueEmptyRef(t *testing.T) {
	svc, _, _, alice, _ := issueEnv(t, storetest.New(t))
	_, _ = svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	for _, ref := range []string{"", "  "} {
		if g, err := svc.GetIssue(alice, "o1", ref); code(err) != 404 {
			t.Fatalf("GetIssue(%q) 要 404:%+v %v", ref, g, err)
		}
	}
}

// 建立專案、改代號時:別名表裡有舊編號(匯入的舊格式 id、搬走的 Issue 的舊 key)的代號不能用(400);
// 代號沒變就不查(改名不被擋);專案刪除用計數(含垃圾桶)
func TestProjectKeyAliasGuard(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	// 舊格式 id gh-7 匯入後,別名 GH-7 指向它
	if _, err := svc.ImportIssues(boss, "o1", []model.Issue{{ID: "gh-7", Title: "舊的"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "gh", Key: "GH"}); code(err) != 400 || !strings.Contains(err.Error(), "GH-7") {
		t.Fatal("代號 GH 有舊編號別名 GH-7:", err)
	}
	other := mkProject(t, svc, boss, "Other", "")
	if _, err := svc.UpdateProject(boss, "o1", other.ID, service.ProjectPatch{Key: sp("gh")}); code(err) != 400 {
		t.Fatal("改成有舊編號別名的代號", err)
	}
	// 專案 A 的 Issue 搬走後,別名 APP-1 還在:專案自己改名不被擋,代號換掉後別的專案不能沿用 APP
	a := mkProject(t, svc, boss, "A", "APP")
	i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Project: sp(a.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIssue(alice, "o1", i.Key, service.IssuePatch{Project: sp("")}); err != nil {
		t.Fatal(err)
	}
	if u, err := svc.UpdateProject(boss, "o1", a.ID, service.ProjectPatch{Name: sp("A2"), Key: sp("APP")}); err != nil || u.Key != "APP" {
		t.Fatal("代號沒變不查別名", u, err)
	}
	if _, err := svc.UpdateProject(boss, "o1", a.ID, service.ProjectPatch{Key: sp("APX")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "B", Key: "APP"}); code(err) != 400 {
		t.Fatal("沿用 APP 會撞搬走 Issue 的別名 APP-1:", err)
	}
	// 沒有舊編號的代號照常
	if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "C", Key: "NEW"}); err != nil {
		t.Fatal(err)
	}
}
