package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 測試用:組織 o1,管理員 boss、成員 alice;角色 writer(可寫 docs、可讀 overview)
func cabEnv(t *testing.T) (*service.Service, *model.Principal, *model.Principal) {
	t.Helper()
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	for _, u := range []model.User{{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now}, {ID: "u2", Email: "alice@x", Role: "member", CreatedAt: now}} {
		if err := st.SaveUser(u); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.CreateOrg(model.Org{ID: "o1", Name: "組織一", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "o1", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	_ = st.SaveMember(model.Member{Org: "o1", UserID: "u2", Email: "alice@x", Role: "member", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	alice := &model.Principal{User: model.User{ID: "u2", Email: "alice@x", Role: "member"}}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", Active: true,
		DocAccess: map[string]string{"docs": "write", "overview": "read"}}); err != nil {
		t.Fatal(err)
	}
	return svc, boss, alice
}

func code(err error) int {
	if err == nil {
		return 0
	}
	return service.Code(err)
}

func sp(s string) *string { return &s }

func TestCabinetDocsAndLayout(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	c, err := svc.GetCabinet(alice, "o1")
	if err != nil || c.LayoutSaved || len(c.Layout) != 2 || c.Layout[0].ID != "overview" || len(c.Docs) != 0 {
		t.Fatalf("沒存過結構是空白結構:%+v %v", c, err)
	}
	// 成員可以新增;作者一律是自己
	d, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "第一份", Section: "docs", Tags: []string{" a ", "A", "b"}, Author: "冒名"})
	if err != nil || d.ID != "DOC-1" || d.Author != "alice@x" || strings.Join(d.Tags, ",") != "a,b" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "x", Section: "nope"}); code(err) != 400 {
		t.Fatal("區塊不存在要 400", err)
	}
	if _, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: " ", Section: "docs"}); code(err) != 400 {
		t.Fatal("沒有標題要 400")
	}
	// 結構:只有組織管理員能改
	layout := []model.CabinetSection{{ID: "overview", Label: "總覽"}, {ID: "docs", Label: "文件", Tabs: []model.CabinetTab{{Path: "/docs/a", Label: "A", Code: sp("A1")}}}}
	if _, err := svc.SaveCabinetLayout(alice, "o1", layout); code(err) != 403 {
		t.Fatal("成員不能改結構", err)
	}
	if _, err := svc.SaveCabinetLayout(boss, "o1", []model.CabinetSection{{ID: "overview", Label: "總覽"}}); err == nil || err.Error() != "「第一份」還在被拿掉的區塊裡,先搬走或刪除" {
		t.Fatal("拿掉有文件的區塊要擋", err)
	}
	if _, err := svc.SaveCabinetLayout(boss, "o1", layout); err != nil {
		t.Fatal(err)
	}
	// 搬到頁籤、接內文;內容沒變不更新
	u, err := svc.UpdateDoc(alice, "o1", "doc-1", service.DocPatch{Section: sp("docs"), Tab: sp("/docs/a"), AppendBody: sp("補充")})
	if err != nil || u.TabPath() != "/docs/a" || u.Body != "補充" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := svc.UpdateDoc(alice, "o1", "DOC-1", service.DocPatch{Section: sp("docs"), Tab: sp("/nope")}); code(err) != 400 {
		t.Fatal("頁籤不存在要 400")
	}
	c, _ = svc.GetCabinet(alice, "o1")
	if !c.LayoutSaved || len(c.Docs) != 1 || c.Docs[0].TabPath() != "/docs/a" {
		t.Fatalf("%+v", c)
	}
	d2, _ := svc.CreateDoc(boss, "o1", service.DocInput{Title: "第二份", Section: "overview"})
	if d2.ID != "DOC-2" {
		t.Fatal(d2.ID)
	}
	if c, _ = svc.GetCabinet(alice, "o1"); c.Docs[0].ID != "DOC-2" {
		t.Fatal("新的在前")
	}
	// 刪除:成員可以;找不到 404
	if err := svc.DeleteDoc(alice, "o1", "doc-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDoc(alice, "o1", "DOC-2"); code(err) != 404 {
		t.Fatal(err)
	}
	// 不是成員 403
	out := &model.Principal{User: model.User{ID: "u9", Email: "x@x", Role: "member"}}
	if _, err := svc.GetCabinet(out, "o1"); code(err) != 403 {
		t.Fatal(err)
	}
	// 刪組織一併刪
	admin := &model.Principal{User: model.User{ID: "root", Role: "admin"}}
	if err := svc.DeleteOrg(admin, "o1"); err != nil {
		t.Fatal(err)
	}
	if l, _ := svc.Store.ListDocs("o1"); len(l) != 0 {
		t.Fatal("刪組織要刪文件")
	}
	if _, err := svc.Store.GetCabinetLayout("o1"); err != store.ErrNotFound {
		t.Fatal("刪組織要刪結構")
	}
}

const tplBody = "---\nkind: template\nfor: lesson\nrequired: course, lesson\n---\n# 範本\n\n```html\n<h1>{{title}}</h1>\n```"

func TestDocTemplatesChecked(t *testing.T) {
	svc, _, alice := cabEnv(t)
	if _, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "壞範本", Section: "docs", Body: "---\nkind: template\n---\n"}); err == nil || err.Error() != "範本要有 for 欄位(這份範本套用在哪種 kind)" {
		t.Fatal(err)
	}
	tpl, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "範本", Section: "docs", Body: tplBody})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "缺", Section: "docs", Body: "---\nkind: lesson\ncourse: L0\n---\n"}); err == nil ||
		err.Error() != "kind: lesson 的文件缺少欄位:lesson(見範本 "+tpl.ID+")" {
		t.Fatal(err)
	}
	d, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "好", Section: "docs", Body: "---\nkind: lesson\ncourse: L0\nlesson: 1\n---\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateDoc(alice, "o1", d.ID, service.DocPatch{Body: sp("---\nkind: lesson\n---\n")}); code(err) != 400 {
		t.Fatal("修改也要檢查", err)
	}
	c, _ := svc.GetCabinet(alice, "o1")
	if _, ok := model.DocTemplateFor(c.Docs[0], c.Docs); !ok {
		t.Fatal("有範本的文件要找得到範本")
	}
}

// 角色身分:依 docAccess 過濾與限制,署名是角色,不能刪、不能改結構
func TestCabinetAgentIdentity(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	if _, err := svc.SaveCabinetLayout(boss, "o1", []model.CabinetSection{{ID: "overview", Label: "總覽"}, {ID: "docs", Label: "文件"}, {ID: "secret", Label: "機密"}}); err != nil {
		t.Fatal(err)
	}
	sec, _ := svc.CreateDoc(alice, "o1", service.DocInput{Title: "機密", Section: "secret"})
	ov, _ := svc.CreateDoc(alice, "o1", service.DocInput{Title: "總覽", Section: "overview"})
	ag := &model.Principal{User: model.User{ID: "agent:writer", Role: "member"}, Agent: &model.AgentIdentity{Org: "o1", AgentID: "writer", TaskID: "T-1"}}
	c, err := svc.GetCabinet(ag, "o1")
	if err != nil || len(c.Docs) != 1 || c.Docs[0].ID != ov.ID {
		t.Fatalf("只看得到能讀的:%+v %v", c, err)
	}
	if _, err := svc.GetDoc(ag, "o1", sec.ID); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.CreateDoc(ag, "o1", service.DocInput{Title: "x", Section: "overview"}); code(err) != 403 {
		t.Fatal("只能讀的位置不能寫", err)
	}
	d, err := svc.CreateDoc(ag, "o1", service.DocInput{Title: "寫", Section: "docs", Author: "冒名"})
	if err != nil || d.Author != "agent:writer" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := svc.UpdateDoc(ag, "o1", ov.ID, service.DocPatch{Title: sp("改")}); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.UpdateDoc(ag, "o1", d.ID, service.DocPatch{Section: sp("secret")}); code(err) != 403 {
		t.Fatal("不能搬到範圍外", err)
	}
	if err := svc.DeleteDoc(ag, "o1", d.ID); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.SaveCabinetLayout(ag, "o1", c.Layout); code(err) != 403 {
		t.Fatal(err)
	}
	other := &model.Principal{User: model.User{ID: "agent:writer"}, Agent: &model.AgentIdentity{Org: "o2", AgentID: "writer"}}
	if _, err := svc.GetCabinet(other, "o1"); code(err) != 403 {
		t.Fatal("別的組織的憑證不能用")
	}
}

func TestImportCabinet(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	layout := []model.CabinetSection{{ID: "docs", Label: "文件"}}
	docs := []model.CabinetDoc{
		{ID: "DOC-7", Title: "新", Section: "docs", Author: "tim", CreatedAt: model.Now()},
		{ID: "DOC-3", Title: "舊", Section: "docs", Author: "mcp"},
	}
	if _, err := svc.ImportCabinet(alice, "o1", service.CabinetImport{Layout: layout, Docs: docs}); code(err) != 403 {
		t.Fatal("要組織管理員")
	}
	res, err := svc.ImportCabinet(boss, "o1", service.CabinetImport{Layout: layout, Docs: docs})
	if err != nil || !res.LayoutSaved || res.Docs != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	c, _ := svc.GetCabinet(alice, "o1")
	if len(c.Layout) != 1 || len(c.Docs) != 2 || c.Docs[0].ID != "DOC-7" || c.Docs[1].Author != "mcp" || c.Docs[1].Tags == nil {
		t.Fatalf("%+v", c)
	}
	if _, err := svc.ImportCabinet(boss, "o1", service.CabinetImport{Docs: docs}); code(err) != 409 {
		t.Fatal("已經有文件要拒絕", err)
	}
	if res, err := svc.ImportCabinet(boss, "o1", service.CabinetImport{Layout: layout}); err != nil || !res.LayoutSkipped {
		t.Fatal("已經有結構就略過", res, err)
	}
	d, _ := svc.CreateDoc(alice, "o1", service.DocInput{Title: "接著", Section: "docs"})
	if d.ID != "DOC-8" {
		t.Fatal("編號接在匯入的後面", d.ID)
	}
}

// 已經有文件時匯入整個被拒,結構也不會先寫進去(之後可以整個重來)
func TestImportCabinetRejectedWritesNothing(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	if _, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "先有的", Section: "docs"}); err != nil {
		t.Fatal(err)
	}
	layout := []model.CabinetSection{{ID: "docs", Label: "文件"}, {ID: "extra", Label: "另外"}}
	docs := []model.CabinetDoc{{ID: "DOC-3", Title: "舊", Section: "docs"}}
	if _, err := svc.ImportCabinet(boss, "o1", service.CabinetImport{Layout: layout, Docs: docs}); code(err) != 409 {
		t.Fatal("已經有文件要拒絕", err)
	}
	c, _ := svc.GetCabinet(alice, "o1")
	if c.LayoutSaved || len(c.Docs) != 1 {
		t.Fatalf("被拒時不能先存結構:%+v", c)
	}
}
