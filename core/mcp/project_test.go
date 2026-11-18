package mcp_test

import (
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 專案工具(projects、project_save)與文件櫃工具的 project 參數;不帶 project 和以前一樣(未分專案)
func TestProjectTools(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	if _, err := svc.SaveCabinetLayout(boss, "lab", "", []model.CabinetSection{{ID: "docs", Label: "文件"}}); err != nil {
		t.Fatal(err)
	}
	admin := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: boss}, "lab", nil
		},
	})
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})

	// 專案:新增、修改、列出
	if l := ok(t, admin, "", "projects", map[string]any{}); len(l["projects"].([]any)) != 0 {
		t.Fatal(l)
	}
	fails(t, admin, "", "project_save", map[string]any{"description": "x"}, "缺少參數 name")
	p := ok(t, admin, "", "project_save", map[string]any{"name": "官網", "description": "改版"})
	if p["id"] != "PRJ-1" || p["name"] != "官網" || p["description"] != "改版" {
		t.Fatal(p)
	}
	fails(t, admin, "", "project_save", map[string]any{"name": "官網"}, "已經有專案叫「官網」(PRJ-1)")
	if u := ok(t, admin, "", "project_save", map[string]any{"id": "prj-1", "description": "官網改版"}); u["name"] != "官網" || u["description"] != "官網改版" {
		t.Fatal(u)
	}
	fails(t, admin, "", "project_save", map[string]any{"id": "PRJ-1"}, "要給 name 或 description")

	// 專案的結構:cabinet_layout_save 帶 project,不影響未分專案
	lay := ok(t, admin, "", "cabinet_layout_save", map[string]any{"project": "PRJ-1",
		"add_sections": []any{map[string]any{"id": "spec", "label": "規格"}},
		"add_tabs":     []any{map[string]any{"section": "spec", "label": "API", "path": "/spec/api"}}})
	if pj := lay["project"].(map[string]any); pj["id"] != "PRJ-1" || pj["name"] != "官網" || len(lay["sections"].([]any)) != 3 {
		t.Fatal(lay)
	}
	if c := ok(t, admin, "", "cabinet", map[string]any{}); len(c["sections"].([]any)) != 1 || c["project"] != nil {
		t.Fatal("未分專案的結構不變", c)
	}
	if c := ok(t, admin, "", "cabinet", map[string]any{"project": "PRJ-1"}); len(c["sections"].([]any)) != 3 {
		t.Fatal(c)
	}
	if _, e := call(t, admin, "", "cabinet", map[string]any{"project": "PRJ-9"}); !strings.Contains(e, "找不到專案") {
		t.Fatal("不存在的專案", e)
	}

	// doc_save:新增放進專案;section、tab 依專案的結構解析
	plain := ok(t, admin, "", "doc_save", map[string]any{"title": "未分", "section": "文件"})
	if plain["id"] != "DOC-1" || plain["project"] != nil {
		t.Fatal(plain)
	}
	fails(t, admin, "", "doc_save", map[string]any{"title": "x", "section": "規格"}, "要指定區塊(docs)")
	d := ok(t, admin, "", "doc_save", map[string]any{"project": "PRJ-1", "title": "API 規格", "tab": "API", "body": "GET /x"})
	if d["id"] != "DOC-2" || d["project"] != "PRJ-1" || d["location"] != "規格 › API" {
		t.Fatal(d)
	}
	// docs:列文件只列該專案的;給 id 不用帶 project
	if l := ok(t, admin, "", "docs", map[string]any{}); l["total"].(float64) != 1 || l["docs"].([]any)[0].(map[string]any)["id"] != "DOC-1" {
		t.Fatal("不帶 project 只列未分專案", l)
	}
	if l := ok(t, admin, "", "docs", map[string]any{"project": "PRJ-1", "section": "規格"}); l["total"].(float64) != 1 || l["project"].(map[string]any)["id"] != "PRJ-1" {
		t.Fatal(l)
	}
	if r := ok(t, admin, "", "docs", map[string]any{"id": "DOC-2"}); r["body"] != "GET /x" || r["project"] != "PRJ-1" || r["location"] != "規格 › API" {
		t.Fatal(r)
	}
	// 搬移:給 project 就搬;沒給 section 時目標專案要有原本的位置
	ok(t, admin, "", "project_save", map[string]any{"name": "App"})
	ok(t, admin, "", "cabinet_layout_save", map[string]any{"project": "PRJ-2", "remove": []any{"overview", "docs"},
		"add_sections": []any{map[string]any{"id": "spec", "label": "規格"}}})
	fails(t, admin, "", "doc_save", map[string]any{"id": "DOC-1", "project": "prj-2"},
		"要搬到的專案沒有「文件」這個位置;請一起給 section(和 tab)指定目標專案裡的位置")
	if m := ok(t, admin, "", "doc_save", map[string]any{"id": "DOC-1", "project": "PRJ-1"}); m["project"] != "PRJ-1" || m["section"] != "docs" {
		t.Fatal("目標專案有原本的位置:沿用", m)
	}
	m := ok(t, admin, "", "doc_save", map[string]any{"id": "DOC-1", "project": "PRJ-2", "section": "規格"})
	if m["project"] != "PRJ-2" || m["section"] != "spec" {
		t.Fatal(m)
	}
	fails(t, admin, "", "doc_save", map[string]any{"id": "DOC-2", "project": "", "edits": []any{map[string]any{"old": "GET", "new": "POST"}}},
		"局部修改(edits、replace_part、append_to_part)不能同時搬到別的專案;分兩次存")
	if r := ok(t, admin, "", "doc_save", map[string]any{"id": "DOC-2", "project": "PRJ-1", "edits": []any{map[string]any{"old": "GET", "new": "POST"}}}); r["unchanged"] == true {
		t.Fatal(r)
	}
	back := ok(t, admin, "", "doc_save", map[string]any{"id": "DOC-1", "project": "", "section": "docs"})
	if back["project"] != nil || back["section"] != "docs" {
		t.Fatal("搬回未分專案", back)
	}
	if p := ok(t, admin, "", "projects", map[string]any{}); len(p["projects"].([]any)) != 2 || p["projects"].([]any)[0].(map[string]any)["docs"].(float64) != 1 {
		t.Fatal("projects 帶文件數", p)
	}

	// 角色:doc_access 用「專案 id:區塊或頁籤名稱」;沒有前綴的只適用未分專案
	g := ok(t, admin, "", "agent_upsert", map[string]any{"id": "writer", "name": "寫手", "doc_access": map[string]any{"文件": "write", "PRJ-1:API": "write"}})
	scope := g["docAccess"].([]any)
	if len(scope) != 2 || scope[1].(map[string]any)["project"] != "PRJ-1" || scope[1].(map[string]any)["tab"] != "/spec/api" {
		t.Fatal(g)
	}
	if a, _ := st.GetAgent("lab", "writer"); a.DocAccess["PRJ-1:/spec/api"] != "write" || a.DocAccess["docs"] != "write" {
		t.Fatal(a.DocAccess)
	}
	fails(t, admin, "", "agent_upsert", map[string]any{"id": "writer", "doc_access": map[string]any{"PRJ-1:沒有": "read"}}, "專案 PRJ-1:文件櫃沒有「沒有」這個區塊或頁籤")
	fails(t, admin, "", "agent_upsert", map[string]any{"id": "writer", "doc_access": map[string]any{"PRJ-9:docs": "read"}}, "doc_access 的 PRJ-9:docs:找不到專案 PRJ-9")
	fails(t, admin, "", "doc_save", map[string]any{"project": "PRJ-1", "title": "x", "section": "規格", "agent": "writer"},
		"寫手沒有權限寫入「官網 › 規格」;它可以寫的範圍:文件、官網 › API")
	if c := ok(t, admin, "", "cabinet", map[string]any{"project": "PRJ-1", "agent": "writer"}); len(c["sections"].([]any)) != 1 {
		t.Fatal("角色只看到能存取的位置", c)
	}
	ctx := ok(t, admin, "", "agent_context", map[string]any{"agent": "writer"})
	if docs := ctx["docs"].(map[string]any); docs["total"].(float64) != 2 {
		t.Fatalf("未分專案的 docs 與官網的 API:%v", ctx["docs"])
	}
	if pj := ctx["projects"].([]any); len(pj) != 1 || pj[0].(map[string]any)["project"] != "PRJ-1" {
		t.Fatal(ctx["projects"])
	}

	// 角色端點:projects 列得到;project 參數一樣有效、權限由 service 強制
	if _, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "writer", Title: "寫文件"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "writer"})
	if err != nil || res.Credential == nil {
		t.Fatal(res, err)
	}
	auth := "Bearer writer:" + res.Credential.Secret
	if l := listTools(t, role, auth); !contains(l, "projects") || contains(l, "project_save") {
		t.Fatal("角色端點有 projects、沒有 project_save", l)
	}
	if l := ok(t, role, auth, "projects", map[string]any{}); len(l["projects"].([]any)) != 2 {
		t.Fatal(l)
	}
	if l := ok(t, role, auth, "docs", map[string]any{"project": "PRJ-1"}); l["total"].(float64) != 1 {
		t.Fatal(l)
	}
	w := ok(t, role, auth, "doc_save", map[string]any{"project": "PRJ-1", "title": "角色寫的", "tab": "/spec/api"})
	if w["project"] != "PRJ-1" || w["author"] != "寫手" {
		t.Fatal(w)
	}
	if _, e := call(t, role, auth, "doc_save", map[string]any{"project": "PRJ-1", "title": "x", "section": "spec"}); e == "" {
		t.Fatal("角色端點也不能寫範圍外")
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// agent_upsert 的 doc_access 給 none:專案已刪、或專案裡找不到那個位置時,照正規化後的 key 拿掉(殘留的設定移得掉);
// 不是 none 的照舊回錯誤
func TestAgentUpsertRemovesStaleProjectKey(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	if _, err := svc.SaveCabinetLayout(boss, "lab", "", []model.CabinetSection{{ID: "docs", Label: "文件"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(boss, "lab", service.ProjectInput{Name: "官網"}); err != nil {
		t.Fatal(err)
	}
	admin := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: boss}, "lab", nil
		},
	})
	// 直接寫進存放處:PRJ-5 不存在(專案已刪)、PRJ-1 的 /gone 位置已經不在結構裡
	_ = st.SaveAgent("lab", model.Agent{ID: "g", Name: "角色", Active: true, CreatedAt: now,
		DocAccess: map[string]string{"docs": "write", "PRJ-5:/x": "read", "PRJ-1:/gone": "read"}})
	fails(t, admin, "", "agent_upsert", map[string]any{"id": "g", "doc_access": map[string]any{"PRJ-5:/x": "write"}}, "doc_access 的 PRJ-5:/x:找不到專案 PRJ-5")
	fails(t, admin, "", "agent_upsert", map[string]any{"id": "g", "doc_access": map[string]any{"PRJ-1:/gone": "write"}}, "專案 PRJ-1:文件櫃沒有「/gone」這個區塊或頁籤")
	ok(t, admin, "", "agent_upsert", map[string]any{"id": "g", "doc_access": map[string]any{"prj-5:/x": "none", "PRJ-1:/gone": "none"}})
	a, err := st.GetAgent("lab", "g")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.DocAccess) != 1 || a.DocAccess["docs"] != "write" {
		t.Fatal("殘留的專案 key 要拿掉、其他的不動", a.DocAccess)
	}
}
