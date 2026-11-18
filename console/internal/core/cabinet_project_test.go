package core

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 專案:畫面的動作(cabinet_query、project_create/update/delete、cabinet_layout_save 帶 project、cabinet_doc_get)
func TestCabinetProjectActions(t *testing.T) {
	a := newTestApp(t)
	out, err := Action(a, "project_create", map[string]any{"name": " 官網改版 ", "description": "說明"})
	if err != nil {
		t.Fatal(err)
	}
	p := out.(*model.Project)
	if p.ID != "PRJ-1" || p.Name != "官網改版" {
		t.Fatal(p)
	}
	if _, err := Action(a, "project_create", map[string]any{"name": "官網改版"}); err == nil {
		t.Fatal("名稱重複應該失敗")
	}
	layout := []any{map[string]any{"id": "spec", "label": "規格", "path": "", "tabs": []any{map[string]any{"path": "/spec/home", "label": "首頁"}}}}
	if _, err := Action(a, "cabinet_layout_save", map[string]any{"project": "prj-1", "layout": layout}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CabinetOps().CreateDoc(a.currentOrgNow(), service.DocInput{Title: "首頁規格", Project: "PRJ-1", Section: "spec", Author: "tim"}); err != nil {
		t.Fatal(err)
	}
	q, err := Action(a, "cabinet_query", map[string]any{"project": "PRJ-1"})
	if err != nil {
		t.Fatal(err)
	}
	m := q.(map[string]any)
	if m["project"] != "PRJ-1" || len(m["layout"].([]CabinetSection)) != 1 || len(m["docs"].([]CabinetDoc)) != 1 || len(m["projects"].([]model.Project)) != 1 {
		t.Fatal(m)
	}
	// 未分專案看不到專案的文件
	q, _ = Action(a, "cabinet_query", map[string]any{})
	for _, d := range q.(map[string]any)["docs"].([]CabinetDoc) {
		if d.Project != "" {
			t.Fatal(d)
		}
	}
	if _, err := Action(a, "cabinet_query", map[string]any{"project": "PRJ-9"}); err == nil {
		t.Fatal("不存在的專案應該失敗")
	}
	d, _ := Action(a, "cabinet_doc_get", map[string]any{"id": "doc-1"})
	if d.(*CabinetDoc).Project != "PRJ-1" {
		t.Fatal(d)
	}
	if d, _ := Action(a, "cabinet_doc_get", map[string]any{"id": "DOC-99"}); d != nil {
		t.Fatal(d)
	}
	if h, _ := Action(a, "cabinet_doc_html", map[string]any{"id": "DOC-1"}); h.(map[string]any)["html"] != nil {
		t.Fatal(h)
	}
	out, err = Action(a, "project_update", map[string]any{"id": "prj-1", "name": "官網"})
	if err != nil || out.(*model.Project).Name != "官網" {
		t.Fatal(out, err)
	}
	// 還有文件:刪不掉,錯誤訊息照核心的
	if _, err := Action(a, "project_delete", map[string]any{"id": "PRJ-1"}); err == nil || !strings.Contains(err.Error(), "文件") {
		t.Fatal(err)
	}
	if err := a.CabinetOps().DeleteDoc(a.currentOrgNow(), "DOC-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "project_delete", map[string]any{"id": "PRJ-1"}); err != nil {
		t.Fatal(err)
	}
	q, _ = Action(a, "cabinet_query", map[string]any{})
	if len(q.(map[string]any)["projects"].([]model.Project)) != 0 {
		t.Fatal(q)
	}
}
