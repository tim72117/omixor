package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

const longDoc = "# 構想\n\n## 定位\n\n- 一句話:動態牆\n- 對象:學員\n\n## 待決定\n\n- 誰來寫\n- 更新頻率\n"

func TestReadDocParts(t *testing.T) {
	svc, _, alice := cabEnv(t)
	d, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "動態牆", Body: longDoc, Section: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := svc.ReadDoc(alice, "o1", d.ID, service.DocRead{Outline: true})
	if err != nil || len(v.Outline) != 3 || v.Text != nil || v.Version != model.DocVersion(longDoc) {
		t.Fatalf("只讀大綱:%+v %v", v, err)
	}
	v, _ = svc.ReadDoc(alice, "o1", d.ID, service.DocRead{Section: "待決定"})
	if v.Text == nil || !strings.HasPrefix(*v.Text, "## 待決定") || strings.Contains(*v.Text, "定位") || !v.Partial {
		t.Fatalf("讀一段:%+v", v)
	}
	v, _ = svc.ReadDoc(alice, "o1", d.ID, service.DocRead{From: 3, To: 3})
	if v.Text == nil || *v.Text != "## 定位" {
		t.Fatal("讀行號", v)
	}
	v, _ = svc.ReadDoc(alice, "o1", d.ID, service.DocRead{Grep: "頻率"})
	if len(v.Matches) != 1 || v.Matches[0].Line != 11 {
		t.Fatalf("搜尋:%+v", v.Matches)
	}
	v, _ = svc.ReadDoc(alice, "o1", d.ID, service.DocRead{})
	if len(v.Outline) != 3 || v.Text == nil || v.Partial {
		t.Fatal("沒指定:大綱加開頭(短文件就是全文)", v)
	}
	if _, err := svc.ReadDoc(alice, "o1", d.ID, service.DocRead{Section: "沒有"}); code(err) != 404 {
		t.Fatal(err)
	}
}

func TestEditDocParts(t *testing.T) {
	svc, _, alice := cabEnv(t)
	d, _ := svc.CreateDoc(alice, "o1", service.DocInput{Title: "動態牆", Body: longDoc, Section: "docs"})
	v0 := model.DocVersion(longDoc)

	r, err := svc.EditDoc(alice, "o1", d.ID, service.DocEdit{BaseVersion: v0, Edits: []model.TextEdit{{Old: "對象:學員", New: "對象:學員與訪客"}}})
	if err != nil || r.From != 6 || r.To != 6 || !strings.Contains(r.Text, "學員與訪客") || r.Version == v0 {
		t.Fatalf("字串取代只回改到的行:%+v %v", r, err)
	}
	v1 := r.Version
	// 用舊版本改別處:字串取代在最新內文上再套一次
	r, err = svc.EditDoc(alice, "o1", d.ID, service.DocEdit{BaseVersion: v0, Edits: []model.TextEdit{{Old: "誰來寫", New: "誰來寫(研發基地)"}}})
	if err != nil || !r.Rebased {
		t.Fatalf("版本不符的字串取代照樣套用:%+v %v", r, err)
	}
	// 用舊版本整段替換:409
	if _, err := svc.EditDoc(alice, "o1", d.ID, service.DocEdit{BaseVersion: v1, ReplaceSection: "待決定", Content: "- 已決定"}); code(err) != 409 {
		t.Fatal("版本不符的整段替換要拒絕", err)
	}
	r, err = svc.EditDoc(alice, "o1", d.ID, service.DocEdit{ReplaceSection: "待決定", Content: "- 已決定"})
	if err != nil {
		t.Fatal(err)
	}
	r, err = svc.EditDoc(alice, "o1", d.ID, service.DocEdit{AppendToSection: "定位", Content: "- 補充:免費"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetDoc(alice, "o1", d.ID)
	if !strings.Contains(got.Body, "- 對象:學員與訪客\n\n- 補充:免費\n\n## 待決定\n\n- 已決定") || strings.Contains(got.Body, "誰來寫") {
		t.Fatalf("結果:%q", got.Body)
	}
	if _, err := svc.EditDoc(alice, "o1", d.ID, service.DocEdit{Edits: []model.TextEdit{{Old: "沒有這段", New: "x"}}}); code(err) != 400 {
		t.Fatal("找不到原文 400", err)
	}
	if _, err := svc.EditDoc(alice, "o1", d.ID, service.DocEdit{Edits: []model.TextEdit{{Old: "a", New: "b"}}, ReplaceSection: "定位"}); code(err) != 400 {
		t.Fatal("一次只能一種修改", err)
	}
	r, _ = svc.EditDoc(alice, "o1", d.ID, service.DocEdit{Edits: []model.TextEdit{{Old: "免費", New: "免費"}}})
	if !r.Unchanged {
		t.Fatal("沒變就不存", r)
	}
}

// 局部修改也照權限與範本檢查:角色只能改可寫的位置;範本必填欄位
func TestEditDocChecks(t *testing.T) {
	svc, boss, alice := cabEnv(t)
	ov, _ := svc.CreateDoc(alice, "o1", service.DocInput{Title: "總覽", Body: "# 總覽\n\n文字", Section: "overview"})
	ag := &model.Principal{User: model.User{ID: "agent:writer", Role: "member"}, Agent: &model.AgentIdentity{Org: "o1", AgentID: "writer", TaskID: "T-1"}}
	if _, err := svc.ReadDoc(ag, "o1", ov.ID, service.DocRead{Outline: true}); err != nil {
		t.Fatal("可讀 overview", err)
	}
	if _, err := svc.EditDoc(ag, "o1", ov.ID, service.DocEdit{Edits: []model.TextEdit{{Old: "文字", New: "改"}}}); code(err) != 403 {
		t.Fatal("overview 只能讀", err)
	}
	_, _ = svc.CreateDoc(boss, "o1", service.DocInput{Title: "範本", Section: "docs",
		Body: "---\nkind: template\nfor: spec\nrequired: owner\n---\n```html\n<p>{{meta.owner}}</p>\n```"})
	spec, err := svc.CreateDoc(alice, "o1", service.DocInput{Title: "規格", Section: "docs", Body: "---\nkind: spec\nowner: tim\n---\n# 規格"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EditDoc(alice, "o1", spec.ID, service.DocEdit{Edits: []model.TextEdit{{Old: "owner: tim\n", New: ""}}}); code(err) != 400 {
		t.Fatal("拿掉必填欄位要拒絕", err)
	}
}
