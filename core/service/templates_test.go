package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

func templateEnv(t *testing.T) (*service.Service, *store.Mem, *model.Principal, *model.Principal) {
	t.Helper()
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.CreateOrg(model.Org{ID: "home", Name: "測試家庭", CreatedAt: now})
	for _, u := range []struct{ id, email, role string }{{"u1", "parent@x", "admin"}, {"u2", "kid@x", "member"}} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		_ = st.SaveMember(model.Member{Org: "home", UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
	}
	return svc, st,
		&model.Principal{User: model.User{ID: "u1", Email: "parent@x", Role: "member"}},
		&model.Principal{User: model.User{ID: "u2", Email: "kid@x", Role: "member"}}
}

func countStatus(p *service.TemplatePlan, kind, status string) int {
	n := 0
	for _, it := range p.Items {
		if (kind == "" || it.Kind == kind) && it.Status == status {
			n++
		}
	}
	return n
}

// 家庭助理樣板:開關關閉時不列、預覽與套用 403;打開後套用建立角色、區塊、文件、頻道、組織鏈;再套用全部略過
func TestFamilyTemplate(t *testing.T) {
	svc, _, parent, kid := templateEnv(t)
	tpl, ok := model.OrgTemplateByID("family")
	if !ok || tpl.Feature != "familyAssistant" {
		t.Fatal("要有 family 樣板,開關是 familyAssistant")
	}
	if _, ok := model.FeatureByKey(tpl.Feature); !ok {
		t.Fatal("familyAssistant 要登記在 features.go")
	}
	l, err := svc.ListOrgTemplates(parent, "home")
	if err != nil || len(l) != 0 {
		t.Fatalf("開關預設關,列表是空的:%+v %v", l, err)
	}
	if model.BuildEdition != model.EditionDev {
		// 正式版沒有家庭助理:開不了、預覽與套用 404 並說明;存著的舊值也不生效
		if _, err := svc.SaveSystemSettings(parent, "home", service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true}}); service.Code(err) != 400 {
			t.Fatal("正式版不能開家庭助理", err)
		}
		_ = svc.Store.SaveSystemSettings("home", model.OrgSystemSettings{Features: map[string]bool{"familyAssistant": true}})
		if l, _ := svc.ListOrgTemplates(kid, "home"); len(l) != 0 {
			t.Fatal("正式版不列", l)
		}
		if _, err := svc.ApplyOrgTemplate(parent, "home", "family", service.TemplateApplyInput{}); service.Code(err) != 404 || !strings.Contains(err.Error(), "開發版") {
			t.Fatal("正式版套用 404 並說明", err)
		}
		if _, err := svc.PreviewOrgTemplate(kid, "home", "family", false); service.Code(err) != 404 {
			t.Fatal(err)
		}
		return
	}
	if _, err := svc.ApplyOrgTemplate(parent, "home", "family", service.TemplateApplyInput{}); service.Code(err) != 403 || !strings.Contains(err.Error(), "系統") {
		t.Fatal("開關關閉套用 403 並說明到系統頁開啟", err)
	}
	if _, err := svc.PreviewOrgTemplate(parent, "home", "family", false); service.Code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.SaveSystemSettings(parent, "home", service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true}}); err != nil {
		t.Fatal(err)
	}
	if l, _ := svc.ListOrgTemplates(kid, "home"); len(l) != 1 || l[0].ID != "family" || l[0].Agents != len(tpl.Agents) {
		t.Fatalf("打開後成員看得到:%+v", l)
	}
	if _, err := svc.ApplyOrgTemplate(kid, "home", "family", service.TemplateApplyInput{}); service.Code(err) != 403 {
		t.Fatal("成員不能套用", err)
	}
	if _, err := svc.ApplyOrgTemplate(parent, "home", "nope", service.TemplateApplyInput{}); service.Code(err) != 404 {
		t.Fatal(err)
	}
	pv, err := svc.PreviewOrgTemplate(kid, "home", "family", true)
	if err != nil || pv.Applied || pv.CanApply || countStatus(pv, "", "create") != len(pv.Items) || countStatus(pv, "scenario", "create") != len(tpl.Scenarios) {
		t.Fatalf("預覽全部是 create:%+v %v", pv, err)
	}

	res, err := svc.ApplyOrgTemplate(parent, "home", "family", service.TemplateApplyInput{})
	if err != nil || res.Failed != 0 || res.Skipped != 0 || res.Created != len(res.Items) || countStatus(res, "scenario", "created") != 0 {
		t.Fatalf("第一次全部建立(沒選模擬情境):%+v %v", res, err)
	}
	if len(res.SuggestAutoReply) != 1 || res.SuggestAutoReply[0] != "butler" {
		t.Fatal(res.SuggestAutoReply)
	}
	agents, _ := svc.ListAgents(parent, "home")
	if len(agents) != len(tpl.Agents) {
		t.Fatal(len(agents))
	}
	c, _ := svc.GetChain(parent, "home")
	if len(c.Edges) != len(tpl.Chain) {
		t.Fatalf("%+v", c)
	}
	if _, ok := c.Edge("user:parent@x", "agent:butler"); !ok {
		t.Fatal("家人 → 管家")
	}
	chs, _ := svc.ListChannels(parent, "home", service.ChannelFilter{})
	if len(chs) != 1 || chs[0].Name != "家庭" || len(chs[0].Members) != len(tpl.Agents)+1 {
		t.Fatalf("%+v", chs)
	}
	cab, _ := svc.GetCabinet(parent, "home")
	if len(cab.Docs) != len(tpl.Docs) || model.Layout(cab.Layout).Section(model.FamilySection) == nil {
		t.Fatalf("%+v", cab)
	}

	// 第二次:全部略過;帶模擬情境時只新增情境
	res, err = svc.ApplyOrgTemplate(parent, "home", "family", service.TemplateApplyInput{Scenarios: true})
	if err != nil || res.Failed != 0 || res.Created != len(tpl.Scenarios) || countStatus(res, "scenario", "created") != len(tpl.Scenarios) {
		t.Fatalf("第二次只建立模擬情境:%+v %v", res, err)
	}
	tasks, _ := svc.ListAgentTasks(parent, "home", service.TaskFilter{})
	blocked, queued := 0, 0
	for _, x := range tasks {
		if !strings.HasPrefix(x.Title, "[模擬]") {
			t.Fatal("模擬任務要標模擬", x.Title)
		}
		switch x.Status {
		case "blocked":
			blocked++
		case "queued", "inProgress":
			queued++
		}
	}
	if len(tasks) == 0 || blocked == 0 || queued != 0 {
		t.Fatalf("模擬任務只有完成與需要協助,不會被領走:%d %d %d", len(tasks), blocked, queued)
	}
	page, _ := svc.ListChannelMessages(parent, "home", chs[0].ID, service.MessageListInput{Limit: 200})
	if len(page.Messages) != 3*len(tpl.Scenarios) {
		t.Fatal(len(page.Messages))
	}
	for _, m := range page.Messages {
		if len(m.Mentions) != 0 || !strings.HasPrefix(m.Text, "【模擬】") {
			t.Fatalf("示範訊息不記 @、標模擬:%+v", m)
		}
	}
	inbox, _ := svc.ChannelInbox(parent, "home", "agent:butler")
	if len(inbox.Mentions) != 0 {
		t.Fatal("模擬對話不會讓管家有未讀的 @(不觸發自動回覆)")
	}
	res, err = svc.ApplyOrgTemplate(parent, "home", "family", service.TemplateApplyInput{Scenarios: true})
	if err != nil || res.Created != 0 || res.Failed != 0 || res.Skipped != len(res.Items) {
		t.Fatalf("第三次全部略過:%+v %v", res, err)
	}
}
