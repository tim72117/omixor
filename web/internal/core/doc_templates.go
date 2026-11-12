package core

import (
	"encoding/json"
	"sort"
	"strings"

	"omixor-web/spec"
)

// 文件櫃的範本(同 Swift 的 Cabinet/DocTemplates.swift):
//   - 文件內文開頭可以有欄位(第一行 ---,欄位一行一個「key: value」,再一行 --- 結束),其中 kind 是文件類型
//   - 範本本身也是文件櫃裡的文件:kind: template、for: <套用的 kind>、required: <必填欄位,逗號分隔>,
//     內文放一個 ```html 區塊(版面,{{…}} 填欄位)與一個 ```css 區塊(樣式)
//   - 有對應範本的文件,畫面用網頁顯示(轉換器是 spec/docrender.js,兩版共用);沒有就照舊顯示 Markdown
//   - ops_doc_save 存檔前依範本檢查必填欄位

const DocTemplateKind = "template"

type DocTemplate struct {
	DocID    string
	Kind     string
	HTML     string
	CSS      string
	Required []string
}

// DocFrontMatter 拆開頭的欄位;沒有欄位時 meta 是空的、rest 是原文
func DocFrontMatter(body string) (map[string]string, string) {
	lines := strings.Split(strings.ReplaceAll(body, "\r", ""), "\n")
	meta := map[string]string{}
	if len(lines) == 0 || lines[0] != "---" {
		return meta, body
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return meta, body
	}
	for _, l := range lines[1:end] {
		i := strings.Index(l, ":")
		if i < 0 {
			continue
		}
		if k := strings.TrimSpace(l[:i]); k != "" {
			meta[k] = strings.TrimSpace(l[i+1:])
		}
	}
	return meta, strings.Join(lines[end+1:], "\n")
}

// docFence 取出第一個 ```<lang> 區塊的內容
func docFence(body, lang string) (string, bool) {
	inside, out := false, []string{}
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		if inside {
			if strings.HasPrefix(l, "```") {
				return strings.Join(out, "\n"), true
			}
			out = append(out, l)
		} else if strings.TrimSpace(l) == "```"+lang {
			inside = true
		}
	}
	return "", false
}

// ParseDocTemplate 文件是範本時解析出來
func ParseDocTemplate(d CabinetDoc) (DocTemplate, bool) {
	meta, _ := DocFrontMatter(d.Body)
	html, ok := docFence(d.Body, "html")
	if meta["kind"] != DocTemplateKind || meta["for"] == "" || !ok {
		return DocTemplate{}, false
	}
	req := []string{}
	for _, r := range strings.Split(meta["required"], ",") {
		if r = strings.TrimSpace(r); r != "" {
			req = append(req, r)
		}
	}
	css, _ := docFence(d.Body, "css")
	return DocTemplate{DocID: d.ID, Kind: meta["for"], HTML: html, CSS: css, Required: req}, true
}

// FindDocTemplate 某種 kind 的範本:同一種有好幾份時用最近更新的
func FindDocTemplate(kind string, docs []CabinetDoc) (DocTemplate, bool) {
	c := []CabinetDoc{}
	for _, d := range docs {
		if m, _ := DocFrontMatter(d.Body); m["for"] == kind {
			c = append(c, d)
		}
	}
	sort.SliceStable(c, func(i, j int) bool {
		if !c[i].UpdatedAt.Equal(c[j].UpdatedAt.Time) {
			return c[i].UpdatedAt.After(c[j].UpdatedAt.Time)
		}
		return c[i].ID > c[j].ID
	})
	for _, d := range c {
		if t, ok := ParseDocTemplate(d); ok {
			return t, true
		}
	}
	return DocTemplate{}, false
}

// ValidateDocBody 存檔前檢查;回傳錯誤訊息,沒問題回傳 ""
func ValidateDocBody(body string, docs []CabinetDoc) string {
	meta, _ := DocFrontMatter(body)
	kind := meta["kind"]
	if kind == "" {
		return ""
	}
	if kind == DocTemplateKind {
		if meta["for"] == "" {
			return "範本要有 for 欄位(這份範本套用在哪種 kind)"
		}
		if _, ok := docFence(body, "html"); !ok {
			return "範本要有一個 ```html 區塊(版面)"
		}
		return ""
	}
	t, ok := FindDocTemplate(kind, docs)
	if !ok {
		return ""
	}
	missing := []string{}
	for _, r := range t.Required {
		if meta[r] == "" {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "kind: " + kind + " 的文件缺少欄位:" + strings.Join(missing, "、") + "(見範本 " + t.DocID + ")"
}

// DocHTML 有對應範本時,產生顯示用的整頁 HTML(網頁版放進 sandbox 的 iframe;禁止連外)
func DocHTML(d CabinetDoc, docs []CabinetDoc) (string, bool) {
	meta, _ := DocFrontMatter(d.Body)
	kind := meta["kind"]
	if kind == "" || kind == DocTemplateKind {
		return "", false
	}
	t, ok := FindDocTemplate(kind, docs)
	if !ok {
		return "", false
	}
	return DocPage(d.Body, t), true
}

// DocPage 和 Swift 的 DocTemplates.page 一字不差(spec/docpage.html 比對)
func DocPage(md string, t DocTemplate) string {
	data, _ := json.Marshal(map[string]string{"md": md, "template": t.HTML})
	css := strings.ReplaceAll(t.CSS, "</", "<\\/")
	return `<!doctype html><html lang="zh-Hant"><head><meta charset="utf-8">` +
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><style>` + css + `</style></head>` +
		`<body class="studio"><main id="root"></main><script>` + strings.TrimSuffix(spec.DocRenderJS, "\n") + "\n" +
		`MdRender.mount(` + string(data) + `)</script></body></html>`
}

func init() {
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
