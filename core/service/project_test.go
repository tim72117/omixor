package service_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 專案 CRUD:組織管理員新增修改刪除、成員與角色可以列;名稱必填、組織內不重複(不分大小寫)
func TestProjectCRUD(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	if l, err := svc.ListProjects(alice, "o1"); err != nil || len(l) != 0 {
		t.Fatal(l, err)
	}
	if _, err := svc.CreateProject(alice, "o1", service.ProjectInput{Name: "官網"}); code(err) != 403 {
		t.Fatal("一般成員不能新增", err)
	}
	if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "  "}); code(err) != 400 {
		t.Fatal("名稱必填", err)
	}
	a, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: " 官網 ", Description: " 改版 "})
	if err != nil || a.ID != "PRJ-1" || a.Name != "官網" || a.Description != "改版" || a.CreatedAt.IsZero() {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "官網"}); code(err) != 409 {
		t.Fatal("名稱重複", err)
	}
	b, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "App"})
	if err != nil || b.ID != "PRJ-2" {
		t.Fatal(b, err)
	}
	if _, err := svc.UpdateProject(boss, "o1", "prj-2", service.ProjectPatch{Name: sp("官網")}); code(err) != 409 {
		t.Fatal("改成重複的名稱", err)
	}
	if _, err := svc.UpdateProject(boss, "o1", "PRJ-2", service.ProjectPatch{Name: sp("")}); code(err) != 400 {
		t.Fatal("名稱不能改成空的", err)
	}
	if _, err := svc.UpdateProject(alice, "o1", "PRJ-2", service.ProjectPatch{Name: sp("x")}); code(err) != 403 {
		t.Fatal("一般成員不能改", err)
	}
	u, err := svc.UpdateProject(boss, "o1", "prj-2", service.ProjectPatch{Name: sp("app"), Description: sp("手機")})
	if err != nil || u.ID != "PRJ-2" || u.Name != "app" || u.Description != "手機" {
		t.Fatal("id 不分大小寫;自己的名稱只改大小寫可以", u, err)
	}
	if _, err := svc.UpdateProject(boss, "o1", "PRJ-9", service.ProjectPatch{Name: sp("x")}); code(err) != 404 {
		t.Fatal(err)
	}
	ag := &model.Principal{User: model.User{ID: "agent:writer", Role: "member"}, Agent: &model.AgentIdentity{Org: "o1", AgentID: "writer", TaskID: "T-1"}}
	if l, err := svc.ListProjects(ag, "o1"); err != nil || len(l) != 2 || l[0].ID != "PRJ-1" || l[1].Name != "app" {
		t.Fatal("角色也看得到所有專案", l, err)
	}
	if err := svc.DeleteProject(alice, "o1", "PRJ-2"); code(err) != 403 {
		t.Fatal(err)
	}
	if err := svc.DeleteProject(boss, "o1", "prj-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProject(boss, "o1", "PRJ-2"); code(err) != 404 {
		t.Fatal(err)
	}
}

// 文件櫃分專案:結構各自一份;列文件只列該專案的;新增放進指定專案;未分專案(不帶 project)和以前一樣
func TestProjectCabinet(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	p, _ := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "官網"})
	plain, _ := svc.CreateDoc(alice, "o1", service.DocInput{Title: "未分", Section: "docs"})
	c, err := svc.GetCabinet(alice, "o1", "prj-1")
	if err != nil || c.Project != "PRJ-1" || c.LayoutSaved || len(c.Layout) != 2 || len(c.Docs) != 0 {
		t.Fatalf("新專案是空白結構、沒有文件:%+v %v", c, err)
	}
	if _, err := svc.GetCabinet(alice, "o1", "PRJ-9"); code(err) != 404 {
		t.Fatal("不存在的專案 404", err)
	}
	if _, err := svc.GetCabinet(alice, "o1", "nope"); code(err) != 404 {
		t.Fatal("不存在的專案 404", err)
	}
	if _, err := svc.SaveCabinetLayout(alice, "o1", p.ID, []model.CabinetSection{{ID: "spec", Label: "規格"}}); code(err) != 403 {
		t.Fatal("改結構要組織管理員", err)
	}
	if _, err := svc.SaveCabinetLayout(boss, "o1", "PRJ-9", []model.CabinetSection{{ID: "spec", Label: "規格"}}); code(err) != 404 {
		t.Fatal(err)
	}
	if _, err := svc.SaveCabinetLayout(boss, "o1", p.ID, []model.CabinetSection{{ID: "spec", Label: "規格", Tabs: []model.CabinetTab{{Label: "API", Path: "/spec/api"}}}}); err != nil {
		t.Fatal(err)
	}
	// 專案的結構拿掉了 docs:未分專案裡 docs 區塊的文件不擋專案的結構
	c, _ = svc.GetCabinet(alice, "o1", p.ID)
	if !c.LayoutSaved || len(c.Layout) != 1 || c.Layout[0].ID != "spec" {
		t.Fatal(c)
	}
	if c, _ := svc.GetCabinet(alice, "o1", ""); c.LayoutSaved || len(c.Layout) != 2 || len(c.Docs) != 1 || c.Docs[0].ID != plain.ID || c.Project != "" {
		t.Fatal("未分專案不受影響", c)
	}
	if _, err := svc.CreateDoc(alice, "o1", service.DocInput{Project: p.ID, Title: "x", Section: "docs"}); code(err) != 400 {
		t.Fatal("專案的結構沒有這個區塊", err)
	}
	if _, err := svc.CreateDoc(alice, "o1", service.DocInput{Project: "PRJ-9", Title: "x", Section: "spec"}); code(err) != 404 {
		t.Fatal(err)
	}
	pd, err := svc.CreateDoc(alice, "o1", service.DocInput{Project: "prj-1", Title: "API", Section: "spec", Tab: sp("/spec/api")})
	if err != nil || pd.ID != "DOC-2" || pd.Project != "PRJ-1" {
		t.Fatalf("文件號碼在組織內唯一:%+v %v", pd, err)
	}
	c, _ = svc.GetCabinet(alice, "o1", p.ID)
	if len(c.Docs) != 1 || c.Docs[0].ID != pd.ID {
		t.Fatal("只列該專案的", c.Docs)
	}
	if c, _ := svc.GetCabinet(alice, "o1", ""); len(c.Docs) != 1 || c.Docs[0].ID != plain.ID {
		t.Fatal("未分專案只列未分專案的", c.Docs)
	}
	if d, err := svc.GetDoc(alice, "o1", "doc-2"); err != nil || d.Project != "PRJ-1" {
		t.Fatal("依 id 讀不用帶專案", d, err)
	}
	// 專案的結構:被拿掉的區塊裡有這個專案的文件 → 拒絕
	if _, err := svc.SaveCabinetLayout(boss, "o1", p.ID, []model.CabinetSection{{ID: "other", Label: "別的"}}); code(err) != 400 {
		t.Fatal(err)
	}
	// 刪除非空專案被拒(409),訊息說明還有幾份文件
	err = svc.DeleteProject(boss, "o1", p.ID)
	if code(err) != 409 || !strings.Contains(err.Error(), "1 份文件") {
		t.Fatal("非空專案不能刪", err)
	}
	if err := svc.DeleteDoc(alice, "o1", pd.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProject(boss, "o1", p.ID); err != nil {
		t.Fatal("空了就能刪", err)
	}
	if _, err := svc.GetCabinet(alice, "o1", p.ID); code(err) != 404 {
		t.Fatal(err)
	}
}

// 文件在專案間移動:目標專案要存在(404)、結構要有對應的區塊與頁籤(400);沒給 section 沿用原本位置
func TestProjectMoveDoc(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	p, _ := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "官網"})
	q, _ := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "App"})
	_, _ = svc.SaveCabinetLayout(boss, "o1", p.ID, []model.CabinetSection{{ID: "docs", Label: "文件"}, {ID: "spec", Label: "規格", Tabs: []model.CabinetTab{{Label: "API", Path: "/spec/api"}}}})
	_, _ = svc.SaveCabinetLayout(boss, "o1", q.ID, []model.CabinetSection{{ID: "spec", Label: "規格"}})
	d, _ := svc.CreateDoc(alice, "o1", service.DocInput{Title: "搬家", Section: "docs"})
	if _, err := svc.UpdateDoc(alice, "o1", d.ID, service.DocPatch{Project: sp("PRJ-9")}); code(err) != 404 {
		t.Fatal("不存在的專案 404", err)
	}
	if _, err := svc.UpdateDoc(alice, "o1", d.ID, service.DocPatch{Project: sp(q.ID)}); code(err) != 400 {
		t.Fatal("目標專案沒有 docs 區塊", err)
	}
	if _, err := svc.UpdateDoc(alice, "o1", d.ID, service.DocPatch{Project: sp(q.ID), Section: sp("spec"), Tab: sp("/spec/api")}); code(err) != 400 {
		t.Fatal("目標專案的 spec 沒有這個頁籤", err)
	}
	m, err := svc.UpdateDoc(alice, "o1", d.ID, service.DocPatch{Project: sp("prj-1")})
	if err != nil || m.Project != p.ID || m.Section != "docs" || !m.UpdatedAt.After(d.UpdatedAt.Time.Add(-1)) {
		t.Fatal("沿用原本位置搬到專案", m, err)
	}
	m, err = svc.UpdateDoc(alice, "o1", d.ID, service.DocPatch{Project: sp(q.ID), Section: sp("spec")})
	if err != nil || m.Project != q.ID || m.Section != "spec" || m.Tab != nil {
		t.Fatal("搬到另一個專案的指定位置", m, err)
	}
	if c, _ := svc.GetCabinet(alice, "o1", q.ID); len(c.Docs) != 1 {
		t.Fatal(c.Docs)
	}
	if c, _ := svc.GetCabinet(alice, "o1", p.ID); len(c.Docs) != 0 {
		t.Fatal(c.Docs)
	}
	if _, err := svc.UpdateDoc(alice, "o1", d.ID, service.DocPatch{Project: sp("")}); code(err) != 400 {
		t.Fatal("未分專案沒有 spec 區塊", err)
	}
	m, err = svc.UpdateDoc(alice, "o1", d.ID, service.DocPatch{Project: sp(""), Section: sp("docs")})
	if err != nil || m.Project != "" || m.Section != "docs" {
		t.Fatal("搬回未分專案", m, err)
	}
	// 專案的結構移掉區塊:只看這個專案的文件(別的專案同 id 的區塊裡有文件不擋)
	if _, err := svc.SaveCabinetLayout(boss, "o1", p.ID, []model.CabinetSection{{ID: "spec", Label: "規格"}}); err != nil {
		t.Fatal(err)
	}
}

// 角色的 docAccess:沒有前綴的 key 只適用未分專案;"<專案 id>:<位置>" 適用該專案
func TestProjectDocAccess(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	p, _ := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "官網"})
	q, _ := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "App"})
	for _, x := range []string{p.ID, q.ID} {
		_, _ = svc.SaveCabinetLayout(boss, "o1", x, []model.CabinetSection{{ID: "docs", Label: "文件"}, {ID: "spec", Label: "規格", Tabs: []model.CabinetTab{{Label: "API", Path: "/spec/api"}}}})
	}
	// writer:未分專案 docs 可寫、overview 可讀;官網 spec 可讀、官網 /spec/api 可寫;App 什麼都沒有
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", Active: true,
		DocAccess: map[string]string{"docs": "write", "overview": "read", "prj-1:spec": "read", "PRJ-1:/spec/api": "write"}}); err != nil {
		t.Fatal(err)
	}
	g, _ := svc.Store.GetAgent("o1", "writer")
	if g.DocAccess["PRJ-1:spec"] != "read" || g.DocAccess["PRJ-1:/spec/api"] != "write" {
		t.Fatal("專案前綴存成大寫", g.DocAccess)
	}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "bad", Name: "壞", DocAccess: map[string]string{"PRJ-9:docs": "read"}}); code(err) != 400 {
		t.Fatal("專案不存在要 400", err)
	}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "bad", Name: "壞", DocAccess: map[string]string{"PRJ-1:": "read"}}); code(err) != 400 {
		t.Fatal("沒有位置要 400", err)
	}
	ag := &model.Principal{User: model.User{ID: "agent:writer", Role: "member"}, Agent: &model.AgentIdentity{Org: "o1", AgentID: "writer", TaskID: "T-1"}}
	pDocs, _ := svc.CreateDoc(alice, "o1", service.DocInput{Project: p.ID, Title: "官網 docs", Section: "docs"})
	pSpec, _ := svc.CreateDoc(alice, "o1", service.DocInput{Project: p.ID, Title: "官網 spec", Section: "spec"})
	pAPI, _ := svc.CreateDoc(alice, "o1", service.DocInput{Project: p.ID, Title: "官網 api", Section: "spec", Tab: sp("/spec/api")})
	qDocs, _ := svc.CreateDoc(alice, "o1", service.DocInput{Project: q.ID, Title: "App docs", Section: "docs"})
	plain, _ := svc.CreateDoc(alice, "o1", service.DocInput{Title: "未分 docs", Section: "docs"})

	// 讀:未分專案的 "docs" 不適用專案裡的 docs 區塊
	c, err := svc.GetCabinet(ag, "o1", p.ID)
	if err != nil || len(c.Docs) != 2 {
		t.Fatalf("官網只看得到 spec 與 api:%+v %v", c.Docs, err)
	}
	for _, d := range c.Docs {
		if d.ID == pDocs.ID {
			t.Fatal("沒有前綴的 docs 不適用專案", d)
		}
	}
	if c, _ := svc.GetCabinet(ag, "o1", q.ID); len(c.Docs) != 0 {
		t.Fatal("App 沒有權限", c.Docs)
	}
	if c, _ := svc.GetCabinet(ag, "o1", ""); len(c.Docs) != 1 || c.Docs[0].ID != plain.ID {
		t.Fatal("未分專案照舊", c.Docs)
	}
	if _, err := svc.GetDoc(ag, "o1", pDocs.ID); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.GetDoc(ag, "o1", qDocs.ID); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.GetDoc(ag, "o1", pSpec.ID); err != nil {
		t.Fatal(err)
	}
	// 寫:官網 /spec/api 可寫(頁籤優先於區塊)、官網 spec 只能讀、App 不行、未分專案 docs 可寫
	if _, err := svc.UpdateDoc(ag, "o1", pAPI.ID, service.DocPatch{Title: sp("改 api")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateDoc(ag, "o1", pSpec.ID, service.DocPatch{Title: sp("改")}); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.CreateDoc(ag, "o1", service.DocInput{Project: p.ID, Title: "x", Section: "docs"}); code(err) != 403 {
		t.Fatal("沒有前綴的 docs 不能寫專案的 docs", err)
	}
	if d, err := svc.CreateDoc(ag, "o1", service.DocInput{Project: p.ID, Title: "新 api", Section: "spec", Tab: sp("/spec/api")}); err != nil || d.Project != p.ID {
		t.Fatal(d, err)
	}
	if _, err := svc.CreateDoc(ag, "o1", service.DocInput{Project: q.ID, Title: "x", Section: "spec", Tab: sp("/spec/api")}); code(err) != 403 {
		t.Fatal(err)
	}
	// 搬移:未分專案 docs → 官網 /spec/api 可以;→ 官網 docs、App 不行(目標也要能寫)
	if _, err := svc.UpdateDoc(ag, "o1", plain.ID, service.DocPatch{Project: sp(q.ID)}); code(err) != 403 {
		t.Fatal("不能搬到沒權限的專案", err)
	}
	if _, err := svc.UpdateDoc(ag, "o1", plain.ID, service.DocPatch{Project: sp(p.ID)}); code(err) != 403 {
		t.Fatal("不能搬到專案裡沒權限的位置", err)
	}
	if m, err := svc.UpdateDoc(ag, "o1", plain.ID, service.DocPatch{Project: sp(p.ID), Section: sp("spec"), Tab: sp("/spec/api")}); err != nil || m.Project != p.ID {
		t.Fatal(m, err)
	}
	// model 的判斷
	if g.CanRead("spec", "") || !g.CanReadIn(p.ID, "spec", "") || g.CanWriteIn(p.ID, "spec", "") || !g.CanWriteIn(p.ID, "spec", "/spec/api") || g.CanReadIn(q.ID, "spec", "/spec/api") {
		t.Fatal("AccessIn", g.DocAccess)
	}
	// 刪專案時拿掉 docAccess 裡指向它的 key
	if err := svc.DeleteProject(boss, "o1", q.ID); err == nil {
		t.Fatal("App 還有文件,要先搬走")
	}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", Active: true,
		DocAccess: map[string]string{"docs": "write", "PRJ-2:docs": "read"}}); err != nil {
		t.Fatal(err)
	}
	_ = svc.DeleteDoc(alice, "o1", qDocs.ID)
	if err := svc.DeleteProject(boss, "o1", q.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = svc.Store.GetAgent("o1", "writer")
	if _, ok := g.DocAccess["PRJ-2:docs"]; ok || g.DocAccess["docs"] != "write" {
		t.Fatal("刪專案要拿掉它的 docAccess", g.DocAccess)
	}
}

// 存角色時的 docAccess:none 直接拿掉、不檢查專案;目前存的角色本來就有、但專案已刪的 key 當殘留拿掉;
// 新加的 key 指向不存在的專案要 400;正規化後重複、值不一致的 key 要 400
func TestSaveAgentStaleProjectKeys(t *testing.T) {
	svc, boss, _ := cabEnv(t)
	p, _ := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "官網"})
	q, _ := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "App"})
	full := map[string]string{"docs": "write", p.ID + ":docs": "read", q.ID + ":docs": "write"}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", Active: true, DocAccess: full}); err != nil {
		t.Fatal(err)
	}
	// 直接從存放處刪專案(不經過 DeleteProject 的清理),角色留著殘留的 key
	if err := svc.Store.DeleteProject("o1", q.ID); err != nil {
		t.Fatal(err)
	}
	// 編輯器把整份 docAccess 送回來:殘留的 key 拿掉,其他照存
	g, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手 2", Active: true, DocAccess: full})
	if err != nil {
		t.Fatal("殘留的 key 不能讓角色存不了", err)
	}
	if _, ok := g.DocAccess[q.ID+":docs"]; ok || g.DocAccess["docs"] != "write" || g.DocAccess[p.ID+":docs"] != "read" {
		t.Fatal("殘留的 key 要拿掉", g.DocAccess)
	}
	if st, _ := svc.Store.GetAgent("o1", "writer"); len(st.DocAccess) != 2 {
		t.Fatal(st.DocAccess)
	}
	// 新加的 key 指向不存在的專案(包括剛刪掉的):400;值是 none 就直接拿掉、不檢查
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", DocAccess: map[string]string{q.ID + ":docs": "read"}}); code(err) != 400 {
		t.Fatal("新加的 key 專案不存在要 400", err)
	}
	if g, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", DocAccess: map[string]string{"docs": "write", "PRJ-9:docs": "none"}}); err != nil || len(g.DocAccess) != 1 {
		t.Fatal("none 不檢查專案", g, err)
	}
	// 正規化後重複:值不一致 400,一致可以
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", DocAccess: map[string]string{"prj-1:/a": "read", "PRJ-1:/a": "write"}}); code(err) != 400 {
		t.Fatal("重複且值不一致要 400", err)
	}
	if g, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", DocAccess: map[string]string{"prj-1:/a": "read", "PRJ-1:/a": "read"}}); err != nil || g.DocAccess["PRJ-1:/a"] != "read" || len(g.DocAccess) != 1 {
		t.Fatal("重複但值一致可以", g, err)
	}
}

// failAgentSave 存角色一律失敗的存放處(測 DeleteProject 清 docAccess 失敗時要回錯誤)
type failAgentSave struct{ store.Store }

func (failAgentSave) SaveAgent(string, model.Agent) error { return errors.New("寫不進去") }

// 刪專案:清角色 docAccess 失敗要回錯誤(專案已經刪了);和存角色同時跑不會留下指向已刪專案的 key
func TestDeleteProjectDocAccessCleanup(t *testing.T) {
	svc, boss, _ := cabEnv(t)
	p, _ := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "官網"})
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", Active: true, DocAccess: map[string]string{p.ID + ":docs": "read"}}); err != nil {
		t.Fatal(err)
	}
	orig := svc.Store
	svc.Store = failAgentSave{orig}
	if err := svc.DeleteProject(boss, "o1", p.ID); err == nil || !strings.Contains(err.Error(), "寫不進去") {
		t.Fatal("清 docAccess 失敗要回錯誤", err)
	}
	svc.Store = orig
	if _, err := orig.GetProject("o1", p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("專案已經刪了", err)
	}

	// 同時刪專案與存角色:不管誰先,最後角色都不會有指向已刪專案的 key
	for i := 0; i < 20; i++ {
		x, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: fmt.Sprintf("p%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		key := x.ID + ":docs"
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := svc.DeleteProject(boss, "o1", x.ID); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "writer", Name: "寫手", Active: true, DocAccess: map[string]string{key: "read"}}); err != nil && code(err) != 400 {
				t.Error(err)
			}
		}()
		wg.Wait()
		if g, _ := svc.Store.GetAgent("o1", "writer"); g.DocAccess[key] != "" {
			t.Fatal("刪掉的專案還留在 docAccess", g.DocAccess)
		}
	}
}
