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

// 繪圖工具存回既有文件:只換掉圖的區塊(不整份覆寫)、維持 ```mermaid、提醒另一種圖的區塊沒動;
// 在角色端點,內部呼叫的 doc_save 也套用角色的工具開關(沒開放就不能藉繪圖工具寫文件)
func TestDiagramSaveEditsAndNestedAllow(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	if _, err := svc.SaveCabinetLayout(boss, "lab", []model.CabinetSection{{ID: "ideas", Label: "構想"}}); err != nil {
		t.Fatal(err)
	}
	admin := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		Resolve: func(r *http.Request) (backend.Backend, string, error) { return &backend.Local{Svc: svc, P: boss}, "lab", nil },
	})

	body := "前言\n\n```wireframe\n{\"device\": \"mobile\", \"root\": {\"type\": \"col\"}}\n```\n\n```mermaid title=\"舊\"\nflowchart TD\n  a --> b\n```\n\n結尾"
	d := ok(t, admin, "", "doc_save", map[string]any{"section": "ideas", "title": "混合", "body": body})
	id := d["id"].(string)

	out := ok(t, admin, "", "flowchart_render", map[string]any{"spec": "flowchart LR\n  x --> y", "svg": false, "save": map[string]any{"id": id}})
	doc, _ := st.GetDoc("lab", id)
	if !strings.HasPrefix(doc.Body, "前言") || !strings.HasSuffix(doc.Body, "結尾") || !strings.Contains(doc.Body, "```wireframe") {
		t.Fatalf("其他內文要保留:%q", doc.Body)
	}
	if !strings.Contains(doc.Body, "```mermaid\nflowchart LR\n  x --> y\n```") || strings.Contains(doc.Body, "a --> b") {
		t.Fatalf("要換掉第一個流程圖區塊並維持 mermaid:%q", doc.Body)
	}
	if w, _ := out["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(string), "```wireframe") {
		t.Fatalf("要提醒另一種圖的區塊沒動:%v", out["warnings"])
	}

	// 沒有流程圖區塊的文件:接在最後
	d = ok(t, admin, "", "doc_save", map[string]any{"section": "ideas", "title": "空白", "body": "只有文字"})
	ok(t, admin, "", "flowchart_render", map[string]any{"spec": "flowchart TD\n  p --> q", "svg": false, "save": map[string]any{"id": d["id"].(string)}})
	doc, _ = st.GetDoc("lab", d["id"].(string))
	if !strings.HasPrefix(doc.Body, "只有文字") || !strings.Contains(doc.Body, "```flowchart\nflowchart TD\n  p --> q\n```") {
		t.Fatalf("%q", doc.Body)
	}

	// 角色端點:沒有文件權限的角色,doc_save 沒開放;繪圖工具能畫,但不能藉它存文件
	if _, err := svc.SaveAgent(boss, "lab", model.Agent{ID: "solo", Name: "獨行", Active: true}); err != nil {
		t.Fatal(err)
	}
	tk, _ := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "solo", Title: "畫圖"})
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "solo", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	auth := "Bearer solo:" + res.Credential.Secret
	if out := ok(t, role, auth, "flowchart_render", map[string]any{"spec": "flowchart TD\n  a --> b", "svg": false}); out["width"] == nil {
		t.Fatal(out)
	}
	failsWith(t, role, auth, "flowchart_render", map[string]any{"spec": "flowchart TD\n  a --> b", "save": map[string]any{"section": "ideas", "title": "偷存"}}, "沒有開放 doc_save")
	failsWith(t, role, auth, "flowchart_render", map[string]any{"doc": id}, "沒有開放 docs")
}
