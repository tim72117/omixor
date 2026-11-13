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

// docs 分段讀、doc_save 局部修改
func TestDocPartsTools(t *testing.T) {
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	admin := mcp.AdminEndpoint(mcp.AdminEndpointConfig{
		Resolve: func(r *http.Request) (backend.Backend, string, error) { return &backend.Local{Svc: svc, P: boss}, "lab", nil },
	})
	body := "# 構想\n\n## 定位\n\n- 一句話:動態牆\n\n## 待決定\n\n- 誰來寫\n"
	d := ok(t, admin, "", "doc_save", map[string]any{"title": "動態牆", "section": "docs", "body": body})
	id := d["id"].(string)

	full := ok(t, admin, "", "docs", map[string]any{"id": id})
	if full["body"] != body || full["version"] == "" {
		t.Fatal("短文件只給 id 回全文與版本", full)
	}
	o := ok(t, admin, "", "docs", map[string]any{"id": id, "outline": true})
	if len(o["outline"].([]any)) != 3 || o["body"] != nil || o["text"] != nil {
		t.Fatal("只回大綱", o)
	}
	p := ok(t, admin, "", "docs", map[string]any{"id": id, "part": "待決定"})
	if p["text"] != "## 待決定\n\n- 誰來寫\n" || p["partial"] != true {
		t.Fatalf("讀一段:%q", p["text"])
	}
	gr := ok(t, admin, "", "docs", map[string]any{"id": id, "grep": "動態"})
	if m := gr["matches"].([]any); len(m) != 1 || m[0].(map[string]any)["line"].(float64) != 5 {
		t.Fatal("搜尋", gr)
	}

	ver := o["version"].(string)
	r := ok(t, admin, "", "doc_save", map[string]any{"id": id, "base_version": ver, "edits": []any{map[string]any{"old": "誰來寫", "new": "誰來寫?研發基地"}}})
	if r["from"].(float64) != 9 || !strings.Contains(r["text"].(string), "研發基地") || r["version"] == ver || r["body"] != nil {
		t.Fatal("局部修改只回改到的行", r)
	}
	ok(t, admin, "", "doc_save", map[string]any{"id": id, "append_to_part": "定位", "content": "- 對象:學員"})
	fails(t, admin, "", "doc_save", map[string]any{"id": id, "base_version": ver, "replace_part": "定位", "content": "x"},
		id+" 已經被改過(目前版本 "+mustVersion(t, svc, boss, id)+",你讀到的是 "+ver+"):請重讀大綱與那一段再改")
	fails(t, admin, "", "doc_save", map[string]any{"id": id, "edits": []any{map[string]any{"old": "x", "new": "y"}}, "body": "z"},
		"局部修改(edits、replace_part、append_to_part)不能和 body 一起用;分兩次存")
	got, _ := svc.GetDoc(boss, "lab", id)
	if !strings.Contains(got.Body, "- 一句話:動態牆\n\n- 對象:學員\n\n## 待決定") {
		t.Fatalf("%q", got.Body)
	}

	// 長文件只給 id:只回大綱與開頭
	long := "# 長文件\n\n" + strings.Repeat("一二三四五六七八九十\n", 700)
	ld := ok(t, admin, "", "doc_save", map[string]any{"title": "長", "section": "docs", "body": long})
	lv := ok(t, admin, "", "docs", map[string]any{"id": ld["id"]})
	if lv["body"] != nil || lv["outline"] == nil || lv["note"] == nil {
		t.Fatal("長文件不回全文", lv["note"])
	}
	if lf := ok(t, admin, "", "docs", map[string]any{"id": ld["id"], "full": true}); lf["body"] != long {
		t.Fatal("full 一定回全文")
	}
}

func mustVersion(t *testing.T, svc *service.Service, p *model.Principal, id string) string {
	t.Helper()
	d, err := svc.GetDoc(p, "lab", id)
	if err != nil {
		t.Fatal(err)
	}
	return model.DocVersion(d.Body)
}
