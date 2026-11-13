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
	"omixor-core/tools/wireframe"
)

// wireframe_render:畫圖、存文件(新增與更新第一個 ```wireframe 區塊)、從文件重畫,文件權限同 doc_save
func TestWireframeTool(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	if _, err := svc.SaveCabinetLayout(boss, "lab", []model.CabinetSection{{ID: "ideas", Label: "構想"}, {ID: "secret", Label: "機密"}}); err != nil {
		t.Fatal(err)
	}
	admin := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		Resolve: func(r *http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: boss}, "lab", nil
		},
	})
	spec := map[string]any{"device": "mobile", "root": map[string]any{"type": "col", "children": []any{
		map[string]any{"type": "header", "title": "登入"}, map[string]any{"type": "button", "text": "送出"}}}}

	out := ok(t, admin, "", "wireframe_render", map[string]any{"spec": spec})
	if out["width"].(float64) != 390 || !strings.HasPrefix(out["svg"].(string), "<svg") || len(out["warnings"].([]any)) != 0 {
		t.Fatal(out)
	}
	fails(t, admin, "", "wireframe_render", map[string]any{"spec": "{"}, "線框圖描述錯誤:不是有效的 JSON(unexpected end of JSON input)")
	fails(t, admin, "", "wireframe_render", map[string]any{}, "要給 spec(線框圖描述),或給 doc(文件 id)讀文件裡的 ```wireframe 區塊")

	// 存成新文件:內文是 note + ```wireframe 區塊
	out = ok(t, admin, "", "wireframe_render", map[string]any{"spec": spec, "svg": false, "save": map[string]any{"section": "構想", "title": "登入頁草稿", "note": "第一版"}})
	if out["svg"] != nil || out["doc"] == nil {
		t.Fatal(out)
	}
	id := out["doc"].(map[string]any)["id"].(string)
	d, _ := st.GetDoc("lab", id)
	if !strings.HasPrefix(d.Body, "第一版\n\n```wireframe\n{") || len(wireframe.Blocks(d.Body)) != 1 {
		t.Fatalf("%q", d.Body)
	}

	// 更新:只換掉第一個區塊,其他內文不動
	spec2 := map[string]any{"device": "desktop", "root": map[string]any{"type": "text", "text": "第二版"}}
	ok(t, admin, "", "wireframe_render", map[string]any{"spec": spec2, "save": map[string]any{"id": id}})
	d, _ = st.GetDoc("lab", id)
	if b := wireframe.Blocks(d.Body); !strings.HasPrefix(d.Body, "第一版") || len(b) != 1 || !strings.Contains(b[0], "第二版") {
		t.Fatalf("%q", d.Body)
	}
	// 從文件重畫
	out = ok(t, admin, "", "wireframe_render", map[string]any{"doc": id})
	if out["width"].(float64) != 1280 || !strings.Contains(out["svg"].(string), "第二版") {
		t.Fatal(out)
	}

	// 角色的文件權限:只能寫構想
	ok(t, admin, "", "agent_upsert", map[string]any{"id": "designer", "name": "設計師", "doc_access": map[string]any{"構想": "write"}})
	fails(t, admin, "", "wireframe_render", map[string]any{"spec": spec, "agent": "designer", "save": map[string]any{"section": "機密", "title": "x"}},
		"線框圖畫好了,但存到文件櫃失敗:設計師沒有權限寫入「機密」;它可以寫的範圍:構想")
	out = ok(t, admin, "", "wireframe_render", map[string]any{"spec": spec, "agent": "designer", "save": map[string]any{"section": "ideas", "title": "角色畫的"}})
	if out["doc"].(map[string]any)["location"] != "構想" {
		t.Fatal(out["doc"])
	}

	// 角色端點也開放
	if _, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "designer", Title: "畫圖"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "designer"})
	if err != nil || res.Credential == nil {
		t.Fatal(res, err)
	}
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	auth := "Bearer designer:" + res.Credential.Secret
	out = ok(t, role, auth, "wireframe_render", map[string]any{"spec": spec, "save": map[string]any{"section": "ideas", "title": "角色端點畫的"}})
	if out["doc"].(map[string]any)["author"] != "設計師" {
		t.Fatal(out["doc"])
	}
}
