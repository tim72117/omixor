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
	"omixor-core/tools/flowchart"
)

// flowchart_render:Mermaid 與 JSON 都能畫;存文件(新增、更新第一個流程圖區塊,Mermaid 原文照存)、從文件重畫,文件權限同 doc_save
func TestFlowchartTool(t *testing.T) {
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
	merm := "flowchart TD\n  A([開始]) --> B{通過?}\n  B -->|是| C([結束])\n  B -->|否| A"

	out := ok(t, admin, "", "flowchart_render", map[string]any{"spec": "```mermaid\n" + merm + "\n```"})
	if !strings.HasPrefix(out["svg"].(string), "<svg") || !strings.Contains(out["svg"].(string), "通過?") || len(out["warnings"].([]any)) != 0 {
		t.Fatal(out)
	}
	jspec := map[string]any{"direction": "LR", "nodes": []any{map[string]any{"id": "a", "label": "收件"}, map[string]any{"id": "b", "label": "歸檔"}},
		"edges": []any{map[string]any{"from": "a", "to": "b"}}}
	out = ok(t, admin, "", "flowchart_render", map[string]any{"spec": jspec})
	if out["width"].(float64) <= out["height"].(float64) {
		t.Fatal(out)
	}
	fails(t, admin, "", "flowchart_render", map[string]any{"spec": "flowchart TD\n  A --> B[沒結束"}, "流程圖描述錯誤:第 2 行:節點「B」的形狀括號沒有結束(缺少「]」)(在「B[沒結束」附近)")
	fails(t, admin, "", "flowchart_render", map[string]any{}, "要給 spec(流程圖描述),或給 doc(文件 id)讀文件裡的 ```flowchart 區塊")

	// 存成新文件:內文是 note + ```flowchart 區塊,Mermaid 原文照存
	out = ok(t, admin, "", "flowchart_render", map[string]any{"spec": merm, "svg": false, "save": map[string]any{"section": "構想", "title": "登入流程", "note": "第一版"}})
	if out["svg"] != nil || out["doc"] == nil {
		t.Fatal(out)
	}
	id := out["doc"].(map[string]any)["id"].(string)
	d, _ := st.GetDoc("lab", id)
	if d.Body != "第一版\n\n```flowchart\n"+merm+"\n```\n" {
		t.Fatalf("%q", d.Body)
	}
	// 更新:只換掉第一個流程圖區塊(JSON 整理成每個節點一行),其他內文不動
	ok(t, admin, "", "flowchart_render", map[string]any{"spec": jspec, "save": map[string]any{"id": id}})
	d, _ = st.GetDoc("lab", id)
	if b := flowchart.Blocks(d.Body); !strings.HasPrefix(d.Body, "第一版") || len(b) != 1 || !strings.Contains(b[0], `{"id": "a", "label": "收件"}`) {
		t.Fatalf("%q", d.Body)
	}
	// 從文件重畫;flowchart 語法的 ```mermaid 區塊也讀得到
	out = ok(t, admin, "", "flowchart_render", map[string]any{"doc": id})
	if !strings.Contains(out["svg"].(string), "歸檔") {
		t.Fatal(out)
	}
	out = ok(t, admin, "", "doc_save", map[string]any{"section": "ideas", "title": "mermaid 文件", "body": "說明\n\n```mermaid\n" + merm + "\n```\n"})
	out = ok(t, admin, "", "flowchart_render", map[string]any{"doc": out["id"].(string)})
	if !strings.Contains(out["svg"].(string), "開始") {
		t.Fatal(out)
	}

	// 角色的文件權限:只能寫構想
	ok(t, admin, "", "agent_upsert", map[string]any{"id": "designer", "name": "設計師", "doc_access": map[string]any{"構想": "write"}})
	fails(t, admin, "", "flowchart_render", map[string]any{"spec": merm, "agent": "designer", "save": map[string]any{"section": "機密", "title": "x"}},
		"流程圖畫好了,但存到文件櫃失敗:設計師沒有權限寫入「機密」;它可以寫的範圍:構想")

	// 角色端點也開放
	if _, err := svc.AssignTask(boss, "lab", service.TaskInput{AgentID: "designer", Title: "畫流程"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "lab", service.ClaimInput{AgentID: "designer"})
	if err != nil || res.Credential == nil {
		t.Fatal(res, err)
	}
	role := mcp.AgentEndpoint(mcp.AgentEndpointConfig{Svc: svc})
	auth := "Bearer designer:" + res.Credential.Secret
	out = ok(t, role, auth, "flowchart_render", map[string]any{"spec": merm, "save": map[string]any{"section": "ideas", "title": "角色端點畫的"}})
	if out["doc"].(map[string]any)["author"] != "設計師" {
		t.Fatal(out["doc"])
	}
}
