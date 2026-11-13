package wireframe

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

const mobileLogin = `{
  "title": "登入",
  "device": "mobile",
  "root": {"type": "col", "children": [
    {"type": "header", "title": "omixor", "back": true},
    {"type": "col", "padding": [32, 24], "gap": 16, "children": [
      {"type": "heading", "text": "歡迎回來", "level": 1},
      {"type": "text", "text": "用公司帳號登入", "muted": true},
      {"type": "input", "label": "Email", "placeholder": "you@example.com"},
      {"type": "input", "label": "密碼", "kind": "password", "value": "secret"},
      {"type": "row", "children": [
        {"type": "checkbox", "label": "記住我", "checked": true},
        {"type": "spacer"},
        {"type": "link", "text": "忘記密碼?"}
      ]},
      {"type": "button", "text": "登入", "variant": "primary", "width": "fill"},
      {"type": "divider"},
      {"type": "button", "text": "用 Google 登入", "variant": "secondary", "width": "fill"}
    ]}
  ]}
}`

const desktopDashboard = `{
  "title": "營運儀表板",
  "device": "desktop",
  "root": {"type": "col", "gap": 0, "children": [
    {"type": "header", "title": "omixor 營運", "logo": true, "items": ["總覽", "事件", "文件櫃", "設定"], "avatar": true},
    {"type": "row", "gap": 0, "children": [
      {"type": "sidebar", "title": "選單", "items": ["總覽", "服務", "告警", "部署", "報表"], "active": 0},
      {"type": "col", "padding": 24, "gap": 20, "children": [
        {"type": "row", "children": [
          {"type": "heading", "text": "今日概況", "level": 2},
          {"type": "spacer"},
          {"type": "select", "placeholder": "最近 7 天", "width": 160},
          {"type": "button", "text": "匯出", "variant": "secondary"}
        ]},
        {"type": "grid", "columns": 4, "children": [
          {"type": "card", "title": "請求數", "children": [{"type": "heading", "text": "1.2M", "level": 1}, {"type": "badge", "text": "+8%"}]},
          {"type": "card", "title": "錯誤率", "children": [{"type": "heading", "text": "0.3%", "level": 1}]},
          {"type": "card", "title": "延遲 p95", "children": [{"type": "heading", "text": "220ms", "level": 1}]},
          {"type": "card", "title": "告警", "children": [{"type": "heading", "text": "2", "level": 1}]}
        ]},
        {"type": "row", "children": [
          {"type": "card", "title": "流量", "children": [{"type": "chart", "kind": "line"}]},
          {"type": "card", "title": "來源", "width": 360, "children": [{"type": "chart", "kind": "pie"}]}
        ]},
        {"type": "tabs", "items": ["事件", "部署", "稽核"], "active": 1},
        {"type": "table", "columns": ["時間", "服務", "狀態", "負責人"], "rows": 8},
        {"type": "fancy-widget"}
      ]}
    ]},
    {"type": "modal", "title": "確認部署", "children": [
      {"type": "text", "text": "要把 v1.4.2 部署到 production 嗎?"},
      {"type": "row", "justify": "end", "children": [
        {"type": "button", "text": "取消", "variant": "secondary"},
        {"type": "button", "text": "部署"}
      ]}
    ]}
  ]}
}`

// 輸出要是合法的 XML,而且只有允許的元素
func checkSVG(t *testing.T, svg string) {
	t.Helper()
	allowed := map[string]bool{"svg": true, "title": true, "style": true, "rect": true, "line": true, "circle": true, "path": true, "polyline": true, "text": true}
	dec := xml.NewDecoder(strings.NewReader(svg))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("SVG 不是合法的 XML:%v", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			if !allowed[se.Name.Local] {
				t.Fatalf("不該出現的元素 <%s>", se.Name.Local)
			}
			for _, a := range se.Attr {
				if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") || a.Name.Local == "href" {
					t.Fatalf("不該出現的屬性 %s", a.Name.Local)
				}
			}
		}
	}
	low := strings.ToLower(svg)
	for _, bad := range []string{"<script", "<foreignobject", "<a ", "<image", "<use", "xlink:", "<iframe"} {
		if strings.Contains(low, bad) {
			t.Fatalf("SVG 含有 %q", bad)
		}
	}
}

func save(t *testing.T, name, svg string) {
	// WIREFRAME_OUT=資料夾 時把圖存下來看
	if dir := os.Getenv("WIREFRAME_OUT"); dir != "" {
		_ = os.WriteFile(dir+"/"+name+".svg", []byte(svg), 0o644)
	}
}

func TestMobileLogin(t *testing.T) {
	r, err := Render(mobileLogin)
	if err != nil {
		t.Fatal(err)
	}
	checkSVG(t, r.SVG)
	save(t, "mobile-login", r.SVG)
	if r.Width != 390 || r.Height != 844 {
		t.Fatalf("尺寸 %d×%d", r.Width, r.Height)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("不該有提醒:%v", r.Warnings)
	}
	for _, s := range []string{"歡迎回來", "you@example.com", "••••••", "忘記密碼?", "用 Google 登入", "<title>登入</title>"} {
		if !strings.Contains(r.SVG, s) {
			t.Fatalf("少了 %q", s)
		}
	}
	if strings.Contains(r.SVG, "secret") {
		t.Fatal("密碼欄位不該顯示原文")
	}
}

func TestDesktopDashboard(t *testing.T) {
	r, err := Render(desktopDashboard)
	if err != nil {
		t.Fatal(err)
	}
	checkSVG(t, r.SVG)
	save(t, "desktop-dashboard", r.SVG)
	if r.Width != 1280 || r.Height <= 800 {
		t.Fatalf("內容比一屏長,畫布要加高:%d×%d", r.Width, r.Height)
	}
	if !strings.Contains(r.SVG, "第一屏") || !strings.Contains(r.SVG, `fill-opacity="0.35"`) || !strings.Contains(r.SVG, "確認部署") {
		t.Fatal("少了第一屏標記或疊層")
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "fancy-widget") || !strings.Contains(r.SVG, "fancy-widget") {
		t.Fatalf("不認得的元件要畫成虛線框並提醒:%v", r.Warnings)
	}
}

func TestEscape(t *testing.T) {
	evil := `<script>alert(1)</script>&"'` + "\x00\x07"
	spec := map[string]any{
		"title":  evil,
		"device": map[string]any{"w": 400, "h": 300},
		"root": map[string]any{"type": "col", "children": []any{
			map[string]any{"type": "heading", "text": evil},
			map[string]any{"type": "button", "text": `"><foreignObject>`},
			map[string]any{"type": `<img src=x onerror=alert(1)>`},
			map[string]any{"type": "table", "columns": []any{"<b>"}, "rows": []any{[]any{"</text><script>"}}},
			"純文字 <a href=\"javascript:alert(1)\">",
		}},
	}
	r, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	checkSVG(t, r.SVG)
	if !strings.Contains(r.SVG, "&lt;script&gt;alert(1)&lt;/script&gt;&amp;&quot;&#39;") {
		t.Fatal("文字沒有 escape")
	}
	if strings.ContainsAny(r.SVG, "\x00\x07") {
		t.Fatal("控制字元要拿掉")
	}
	if r.Width != 400 || r.Height < 300 {
		t.Fatalf("尺寸 %d×%d", r.Width, r.Height)
	}
}

func TestErrors(t *testing.T) {
	deep := map[string]any{"type": "text", "text": "底"}
	for i := 0; i < MaxDepth+1; i++ {
		deep = map[string]any{"type": "col", "children": []any{deep}}
	}
	many := []any{}
	for i := 0; i < MaxNodes+1; i++ {
		many = append(many, map[string]any{"type": "badge"})
	}
	cases := []struct {
		in   any
		want string
	}{
		{"{不是 JSON", "不是有效的 JSON"},
		{`[1, 2]`, "要是 JSON 物件"},
		{`{"device": "mobile"}`, "缺少 root"},
		{`{"device": "watch", "root": {"type": "col"}}`, "device"},
		{`{"device": {"w": 10, "h": 10}, "root": {"type": "col"}}`, "200~4000"},
		{`{"root": {"type": "col", "children": {"type": "text"}}}`, "children 要是陣列"},
		{`{"root": [1]}`, "要是物件"},
		{map[string]any{"root": deep}, "巢狀超過"},
		{map[string]any{"root": map[string]any{"type": "col", "children": many}}, "節點超過"},
		{nil, "沒有描述"},
	}
	for _, c := range cases {
		_, err := Render(c.in)
		var e *Error
		if !errors.As(err, &e) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v:要有錯誤 %q,得到 %v", c.in, c.want, err)
		}
	}
}

func TestLenient(t *testing.T) {
	// 沒有 type 但有 children 當成 col;字串子節點當成 text;別名;外面包了 ```wireframe 也可以
	r, err := Render("```wireframe\n" + `{"root": {"children": ["你好", {"type": "hstack", "children": [{"type": "btn", "text": "OK"}]}]}}` + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	checkSVG(t, r.SVG)
	if !strings.Contains(r.SVG, "你好") || !strings.Contains(r.SVG, ">OK<") || len(r.Warnings) != 1 {
		t.Fatalf("%v", r.Warnings)
	}
}

func TestWrapAndWidth(t *testing.T) {
	if w := textW("中文", 10); w != 20 {
		t.Fatal(w)
	}
	if w := textW("ab", 10); w != 11 {
		t.Fatal(w)
	}
	l := wrap("hello world foo bar", 50, 10, 0)
	if len(l) < 2 || l[0] != "hello" {
		t.Fatalf("%q", l)
	}
	l = wrap("一二三四五六七八九十", 50, 10, 0)
	if len(l) != 2 || l[0] != "一二三四五" {
		t.Fatalf("%q", l)
	}
	if s := truncate("一二三四五六", 40, 10); s != "一二三…" {
		t.Fatalf("%q", s)
	}
}

func TestMarkdownBlocks(t *testing.T) {
	md := "# 登入頁\n\n說明\n\n```js\nx\n```\n\n```wireframe\n{\"root\": {\"type\": \"col\"}}\n```\n\n結尾"
	b := Blocks(md)
	if len(b) != 1 || b[0] != `{"root": {"type": "col"}}` {
		t.Fatalf("%q", b)
	}
	up := Upsert(md, `{"root": {"type": "row"}}`)
	if b := Blocks(up); len(b) != 1 || !strings.Contains(b[0], "row") || !strings.Contains(up, "結尾") || !strings.Contains(up, "```js") {
		t.Fatalf("%s", up)
	}
	if up := Upsert("只有文字", "{}"); !strings.HasPrefix(up, "只有文字\n\n```wireframe\n{}\n```") {
		t.Fatalf("%q", up)
	}
	p, err := Pretty(map[string]any{"root": map[string]any{"children": []any{map[string]any{"text": "嗨", "type": "text"}}, "type": "col"}, "device": "mobile"})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"device\": \"mobile\",\n  \"root\": {\n    \"type\": \"col\",\n    \"children\": [\n      {\"type\": \"text\", \"text\": \"嗨\"}\n    ]\n  }\n}"
	if p != want {
		t.Fatalf("%s", p)
	}
	if _, err := Render(p); err != nil {
		t.Fatal(err)
	}
}
