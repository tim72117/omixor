package api_test

import (
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 文件櫃的 HTTP 路由:透過 client 與 backend.Remote(和 omixor-web 遠端模式同一條路)
func TestCabinetRoutes(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("lab", "bob@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	bob := e.login("evo-bob")
	rb := backend.NewRemote(bob)

	c, err := rb.GetCabinet("lab")
	if err != nil || c.LayoutSaved || len(c.Layout) != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	d, err := rb.CreateDoc("lab", service.DocInput{Title: "筆記", Section: "docs", Body: "內容", Tags: []string{"x"}})
	if err != nil || d.ID != "DOC-1" || d.Author != "bob@example.com" {
		t.Fatalf("%+v %v", d, err)
	}
	app := "補充"
	d, err = rb.UpdateDoc("lab", "doc-1", service.DocPatch{AppendBody: &app})
	if err != nil || d.Body != "內容\n\n補充" {
		t.Fatalf("%+v %v", d, err)
	}
	if g, err := rb.GetDoc("lab", "DOC-1"); err != nil || g.Title != "筆記" {
		t.Fatal(g, err)
	}
	// 成員不能改結構(403);管理員可以
	layout := []model.CabinetSection{{ID: "docs", Label: "文件", Tabs: []model.CabinetTab{{Path: "/a", Label: "A"}}}}
	if _, err := rb.SaveCabinetLayout("lab", layout); backend.Code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := boss.SaveCabinetLayout("lab", []model.CabinetSection{{ID: "x", Label: "X"}}); status(err) != 400 {
		t.Fatal("拿掉有文件的區塊要 400", err)
	}
	if l, err := boss.SaveCabinetLayout("lab", layout); err != nil || len(l) != 1 || l[0].Tabs[0].Path != "/a" {
		t.Fatal(l, err)
	}
	if _, err := rb.CreateDoc("lab", service.DocInput{Title: "x", Section: "docs", Body: "---\nkind: template\n---\n"}); backend.Code(err) != 400 {
		t.Fatal("範本檢查", err)
	}
	if err := rb.DeleteDoc("lab", "DOC-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := rb.GetDoc("lab", "DOC-1"); !backend.NotFound(err) {
		t.Fatal(err)
	}
	res, err := boss.ImportCabinet("lab", service.CabinetImport{Docs: []model.CabinetDoc{{ID: "DOC-5", Title: "舊", Section: "docs"}}})
	if err != nil || res.Docs != 1 {
		t.Fatal(res, err)
	}
	c, _ = rb.GetCabinet("lab")
	if !c.LayoutSaved || len(c.Docs) != 1 || c.Docs[0].ID != "DOC-5" {
		t.Fatalf("%+v", c)
	}
}
