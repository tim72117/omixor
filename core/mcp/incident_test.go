package mcp_test

import (
	"net/http"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 事件工具(管理端點):宣告、更新、查;錯誤訊息同 Swift 版;角色端點沒有事件工具
func TestIncidentTools(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "amy@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "amy@x", Role: "member", AddedAt: now})
	amy := &model.Principal{User: model.User{ID: "u1", Email: "amy@x", Role: "member"}}
	h := mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: svc, P: amy}, "lab", nil
	}})

	fails(t, h, "", "incident_declare", map[string]any{"title": "x", "sev": 9.0, "impact": "i"}, "sev 必須是 1–4")
	fails(t, h, "", "incident_declare", map[string]any{"title": "x"}, "缺少或無效的整數參數 sev")
	inc := ok(t, h, "", "incident_declare", map[string]any{"title": "付款延遲", "sev": 2.0, "impact": "開通延遲",
		"services": []any{"payments"}, "alert_ids": []any{"ALT-1"}})
	if inc["id"] != "INC-2001" || inc["commander"] != "amy@x" || inc["status"] != "investigating" || len(inc["timeline"].([]any)) != 2 {
		t.Fatal(inc)
	}
	fails(t, h, "", "incident_update", map[string]any{"incident_id": "INC-2001"}, "status、note、finding、ai_summary、impact 至少要給一項")
	fails(t, h, "", "incident_update", map[string]any{"incident_id": "INC-9", "note": "x"}, "找不到事件 INC-9")
	u := ok(t, h, "", "incident_update", map[string]any{"incident_id": "inc-2001", "status": "identified", "note": "找到原因", "finding": "鎖競爭", "ai_summary": "摘要"})
	tl := u["timeline"].([]any)
	if u["status"] != "identified" || len(tl) != 4 || tl[2].(map[string]any)["text"] != "找到原因" || u["aiSummary"] != "摘要" {
		t.Fatal(u)
	}
	ok(t, h, "", "incident_declare", map[string]any{"title": "另一起", "sev": 4.0, "impact": "無"})
	ok(t, h, "", "incident_update", map[string]any{"incident_id": "INC-2002", "status": "resolved"})
	l := ok(t, h, "", "incidents", map[string]any{})
	if l["total"] != 1.0 || l["incidents"].([]any)[0].(map[string]any)["id"] != "INC-2001" {
		t.Fatal("預設只列未解決", l)
	}
	if l := ok(t, h, "", "incidents", map[string]any{"status": "all", "search": "另一"}); l["total"] != 1.0 {
		t.Fatal(l)
	}
	if one := ok(t, h, "", "incidents", map[string]any{"id": "INC-2002"}); one["status"] != "resolved" || one["resolvedAt"] == nil {
		t.Fatal(one)
	}
	if _, ok := mcp.AgentToolAllow["incident_declare"]; ok {
		t.Fatal("角色端點不開放事件工具")
	}
}
