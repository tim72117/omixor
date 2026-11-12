package mcp

import (
	"os"
	"strings"
	"testing"

	"omixor-web/internal/core"
	"omixor-web/internal/templates"
)

// 同 Swift 的 DocTemplateTests
const tplBody = "---\nkind: template\nfor: lesson\nrequired: course, lesson\n---\n# 課程範本\n\n```html\n<h1>{{title}}</h1><p class=\"goal\">{{s.目標.text}}</p>\n```\n\n```css\n.goal { color: #b5742a }\n```"
const lessonBody = "---\nkind: lesson\ncourse: L0\nlesson: 0.intro\n---\n# 為什麼 <不用> 學寫程式 & \"也能\"\n\n## 目標\n\n看懂差別"

func TestDocFrontMatterAndTemplate(t *testing.T) {
	meta, rest := core.DocFrontMatter(lessonBody)
	if len(meta) != 3 || meta["lesson"] != "0.intro" || !strings.HasPrefix(rest, "# 為什麼") {
		t.Fatal(meta, rest)
	}
	if m, _ := core.DocFrontMatter("# 沒有欄位"); len(m) != 0 {
		t.Fatal(m)
	}
	tp, ok := core.ParseDocTemplate(core.CabinetDoc{ID: "DOC-1", Body: tplBody})
	if !ok || tp.Kind != "lesson" || strings.Join(tp.Required, ",") != "course,lesson" || tp.CSS != ".goal { color: #b5742a }" {
		t.Fatalf("%+v", tp)
	}
}

func TestDocSaveChecksTemplate(t *testing.T) {
	a := iaApp(t)
	iaErr(t, a, "ops_doc_save", map[string]any{"section": "tools", "title": "壞範本", "body": "---\nkind: template\n---\n"}, "範本要有 for 欄位(這份範本套用在哪種 kind)")
	iaErr(t, a, "ops_doc_save", map[string]any{"section": "tools", "title": "壞範本", "body": "---\nkind: template\nfor: lesson\n---\n"}, "範本要有一個 ```html 區塊(版面)")
	iaRun(t, a, core.MCPActor, "ops_doc_save", map[string]any{"section": "levels", "title": "早到的", "body": "---\nkind: lesson\n---\n# x"})
	tpl := iaRun(t, a, core.MCPActor, "ops_doc_save", map[string]any{"section": "tools", "title": "課程範本", "body": tplBody})
	iaErr(t, a, "ops_doc_save", map[string]any{"section": "levels", "title": "缺欄位", "body": "---\nkind: lesson\ncourse: L0\n---\n# x"},
		"kind: lesson 的文件缺少欄位:lesson(見範本 "+tpl["id"].(string)+")")
	d := iaRun(t, a, core.MCPActor, "ops_doc_save", map[string]any{"section": "levels", "title": "序章", "body": lessonBody})
	id := d["id"].(string)
	if _, err := Run(a, "ops_doc_save", map[string]any{"id": id, "body": "---\nkind: lesson\n---\n"}, core.MCPActor); err == nil {
		t.Fatal("修改時也要檢查")
	}
	iaRun(t, a, core.MCPActor, "ops_doc_save", map[string]any{"id": id, "append_body": "## 備註\n\n補充"})
	out, err := core.Action(a, "cabinet_doc_html", map[string]any{"id": id})
	h, _ := out.(map[string]any)["html"].(string)
	if err != nil || !strings.Contains(h, ".goal { color: #b5742a }") || !strings.Contains(h, "MdRender.mount(") || strings.Contains(h, "<不用>") {
		t.Fatal(err, h)
	}
	out, _ = core.Action(a, "cabinet_doc_html", map[string]any{"id": tpl["id"]})
	if out.(map[string]any)["html"] != nil {
		t.Fatal("範本本身不產生網頁")
	}
}

// 網頁要和 Swift 版一字不差(templates/doc/docpage.html 由 Swift 測試匯出)
func TestDocPageMatchesSwift(t *testing.T) {
	tp, _ := core.ParseDocTemplate(core.CabinetDoc{ID: "DOC-1", Body: tplBody})
	want, err := os.ReadFile("../../../templates/doc/docpage.html")
	if err != nil {
		t.Fatal(err)
	}
	if got := core.DocPage(lessonBody, tp); got != string(want) {
		i := 0
		for i < len(got) && i < len(want) && got[i] == want[i] {
			i++
		}
		t.Fatalf("第 %d 個字元開始不同:\ngo:    %q\nswift: %q", i, got[i:min(len(got), i+80)], string(want[i:min(len(want), i+80)]))
	}
	if !strings.Contains(templates.DocRenderJS(), "MdRender") {
		t.Fatal("docrender.js")
	}
}
