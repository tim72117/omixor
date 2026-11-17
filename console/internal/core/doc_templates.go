package core

import (
	"encoding/json"
	"strings"

	"omixor-core/model"
	"omixor-web/internal/templates"
)

// 文件櫃的範本(同 Swift 的 Cabinet/DocTemplates.swift)。欄位解析、範本解析、存檔前的必填檢查在核心
// (omixor-core/model/doctemplates.go;service 存檔前檢查,雲端與本機一致),這裡只負責顯示用的整頁 HTML:
//   - 有對應範本的文件,畫面用網頁顯示(sandbox 的 iframe);轉換器是 templates/doc/docrender.js(和 Swift 版共用,
//     由 internal/templates 從 repo 的 templates/ 讀),沒有就照舊顯示 Markdown
//   - 整頁 HTML 由 web 用核心給的文件與範本產生(本機與遠端模式都一樣),核心不產生 HTML:
//     docrender.js 只有一份(templates/doc/,app 也用同一份),核心模組不需要讀 repo 的 templates/

const DocTemplateKind = model.DocTemplateKind

type DocTemplate = model.DocTemplate

// DocFrontMatter 拆開頭的欄位;沒有欄位時 meta 是空的、rest 是原文
func DocFrontMatter(body string) (map[string]string, string) { return model.DocFrontMatter(body) }

// ParseDocTemplate 文件是範本時解析出來
func ParseDocTemplate(d CabinetDoc) (DocTemplate, bool) { return model.ParseDocTemplate(d) }

// FindDocTemplate 某種 kind 的範本:同一種有好幾份時用最近更新的
func FindDocTemplate(kind string, docs []CabinetDoc) (DocTemplate, bool) {
	return model.FindDocTemplate(kind, docs)
}

// ValidateDocBody 存檔前檢查;回傳錯誤訊息,沒問題回傳 ""(核心的 service 存檔時也會檢查)
func ValidateDocBody(body string, docs []CabinetDoc) string { return model.ValidateDocBody(body, docs) }

// DocHTML 有對應範本時,產生顯示用的整頁 HTML(網頁版放進 sandbox 的 iframe;禁止連外)
func DocHTML(d CabinetDoc, docs []CabinetDoc) (string, bool) {
	t, ok := model.DocTemplateFor(d, docs)
	if !ok {
		return "", false
	}
	return DocPage(d.Body, t), true
}

// DocPage 和 Swift 的 DocTemplates.page 一字不差(templates/doc/docpage.html 比對)
func DocPage(md string, t DocTemplate) string {
	data, _ := json.Marshal(map[string]string{"md": md, "template": t.HTML})
	css := strings.ReplaceAll(t.CSS, "</", "<\\/")
	return `<!doctype html><html lang="zh-Hant"><head><meta charset="utf-8">` +
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><style>` + css + `</style></head>` +
		`<body class="studio"><main id="root"></main><script>` + strings.TrimSuffix(templates.DocRenderJS(), "\n") + "\n" +
		`MdRender.mount(` + string(data) + `)</script></body></html>`
}

func init() {
	// 文件與範本取自核心的文件櫃(快取;本機與遠端模式都一樣)
	RegisterAction("cabinet_doc_html", func(a *App, args map[string]any) (any, error) {
		id, _ := args["id"].(string)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		d := a.FindDoc(id)
		if d == nil {
			return map[string]any{"html": nil}, nil
		}
		if h, ok := DocHTML(*d, a.CabinetDocs()); ok {
			return map[string]any{"html": h}, nil
		}
		return map[string]any{"html": nil}, nil
	})
}
