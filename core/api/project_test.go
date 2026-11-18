package api_test

import (
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 專案與文件櫃的 project 參數:透過 client 與 backend.Remote
func TestProjectRoutes(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("lab", "bob@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	bob := e.login("evo-bob")
	rb, rboss := backend.NewRemote(bob), backend.NewRemote(boss)

	if _, err := rb.CreateProject("lab", service.ProjectInput{Name: "官網"}); backend.Code(err) != 403 {
		t.Fatal("成員不能新增專案", err)
	}
	p, err := rboss.CreateProject("lab", service.ProjectInput{Name: "官網", Description: "改版"})
	if err != nil || p.ID != "PRJ-1" {
		t.Fatal(p, err)
	}
	if _, err := rboss.CreateProject("lab", service.ProjectInput{Name: "官網"}); backend.Code(err) != 409 {
		t.Fatal(err)
	}
	name := "官網 2"
	if u, err := rboss.UpdateProject("lab", "prj-1", service.ProjectPatch{Name: &name}); err != nil || u.Name != name || u.Description != "改版" {
		t.Fatal(u, err)
	}
	if l, err := rb.ListProjects("lab"); err != nil || len(l) != 1 || l[0].Name != name {
		t.Fatal(l, err)
	}

	// 文件櫃:?project= 與 body 的 project
	if _, err := rb.GetProjectCabinet("lab", "PRJ-9"); backend.Code(err) != 404 {
		t.Fatal(err)
	}
	if _, err := rboss.SaveProjectCabinetLayout("lab", p.ID, []model.CabinetSection{{ID: "spec", Label: "規格"}}); err != nil {
		t.Fatal(err)
	}
	plain, err := rb.CreateDoc("lab", service.DocInput{Title: "未分", Section: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := rb.CreateDoc("lab", service.DocInput{Project: p.ID, Title: "規格", Section: "spec"})
	if err != nil || d.Project != p.ID || d.ID != "DOC-2" {
		t.Fatal(d, err)
	}
	c, err := rb.GetProjectCabinet("lab", p.ID)
	if err != nil || c.Project != p.ID || len(c.Layout) != 1 || len(c.Docs) != 1 || c.Docs[0].ID != d.ID {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err := rb.GetCabinet("lab"); err != nil || c.Project != "" || len(c.Docs) != 1 || c.Docs[0].ID != plain.ID {
		t.Fatal("不帶 project 和以前一樣", c, err)
	}
	if l, err := bob.ListDocs("lab", p.ID); err != nil || len(l) != 1 || l[0].ID != d.ID {
		t.Fatal(l, err)
	}
	// 搬移
	to, sec := p.ID, "spec"
	if m, err := rb.UpdateDoc("lab", plain.ID, service.DocPatch{Project: &to, Section: &sec}); err != nil || m.Project != p.ID {
		t.Fatal(m, err)
	}
	if err := rboss.DeleteProject("lab", p.ID); backend.Code(err) != 409 {
		t.Fatal("還有文件不能刪", err)
	}
	for _, id := range []string{plain.ID, d.ID} {
		if err := rb.DeleteDoc("lab", id); err != nil {
			t.Fatal(err)
		}
	}
	if err := rb.DeleteProject("lab", p.ID); backend.Code(err) != 403 {
		t.Fatal(err)
	}
	if err := rboss.DeleteProject("lab", p.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := rb.ListProjects("lab"); len(l) != 0 {
		t.Fatal(l)
	}
}
