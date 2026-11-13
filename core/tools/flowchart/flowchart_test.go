package flowchart

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"
)

// 輸出要是合法的 XML,而且只有允許的元素
func checkSVG(t *testing.T, svg string) {
	t.Helper()
	allowed := map[string]bool{"svg": true, "title": true, "style": true, "defs": true, "marker": true, "g": true,
		"rect": true, "path": true, "polygon": true, "ellipse": true, "line": true, "polyline": true, "text": true}
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
				if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") || strings.Contains(strings.ToLower(a.Name.Local), "href") {
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
	// FLOWCHART_OUT=資料夾 時把圖存下來看
	if dir := os.Getenv("FLOWCHART_OUT"); dir != "" {
		_ = os.WriteFile(dir+"/"+name+".svg", []byte(svg), 0o644)
	}
}

func render(t *testing.T, name string, in any) *Result {
	t.Helper()
	r, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	checkSVG(t, r.SVG)
	save(t, name, r.SVG)
	return r
}

// layoutOf 跑一次排版(抽象座標),檢查節點不重疊、邊不穿過不相干的節點
func layoutOf(t *testing.T, in any) *layout {
	t.Helper()
	g, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	L := newLayout(g)
	L.build()
	L.order()
	L.coords()
	L.route()
	N := len(g.Nodes)
	type bx struct{ u0, v0, u1, v1 float64 }
	boxes := make([]bx, N)
	for i := 0; i < N; i++ {
		n := L.nodes[i]
		boxes[i] = bx{n.u - n.b/2, L.vc[n.rank] - n.d/2, n.u + n.b/2, L.vc[n.rank] + n.d/2}
	}
	for i := 0; i < N; i++ {
		for j := i + 1; j < N; j++ {
			a, b := boxes[i], boxes[j]
			if a.u0 < b.u1 && b.u0 < a.u1 && a.v0 < b.v1 && b.v0 < a.v1 {
				t.Fatalf("節點「%s」與「%s」重疊", g.Nodes[i].ID, g.Nodes[j].ID)
			}
		}
	}
	for _, e := range L.edges {
		for k := 0; k+1 < len(e.pts); k++ {
			p, q := e.pts[k], e.pts[k+1]
			if math.Abs(p.u-q.u) > 0.01 && math.Abs(p.v-q.v) > 0.01 {
				t.Fatalf("邊 %s→%s 不是正交線:%v → %v", e.e.From, e.e.To, p, q)
			}
			for i := 0; i < N; i++ {
				if i == e.from || i == e.to {
					continue
				}
				b := boxes[i]
				u0, u1 := math.Min(p.u, q.u), math.Max(p.u, q.u)
				v0, v1 := math.Min(p.v, q.v), math.Max(p.v, q.v)
				if u0 < b.u1-1 && b.u0+1 < u1+0.01 && v0 < b.v1-1 && b.v0+1 < v1+0.01 {
					t.Fatalf("邊 %s→%s 穿過節點「%s」", e.e.From, e.e.To, g.Nodes[i].ID)
				}
			}
		}
	}
	return L
}

func TestStraightFlow(t *testing.T) {
	spec := map[string]any{"title": "簡單流程", "nodes": []any{
		map[string]any{"id": "a", "label": "開始", "shape": "start"},
		map[string]any{"id": "b", "label": "填寫申請表"},
		map[string]any{"id": "c", "label": "送出", "shape": "io"},
		map[string]any{"id": "d", "label": "結束", "shape": "end"},
	}, "edges": []any{
		map[string]any{"from": "a", "to": "b"}, map[string]any{"from": "b", "to": "c"}, map[string]any{"from": "c", "to": "d"},
	}}
	r := render(t, "straight", spec)
	if len(r.Warnings) != 0 || r.Width <= 0 || r.Height <= r.Width {
		t.Fatal(r.Width, r.Height, r.Warnings)
	}
	for _, s := range []string{"開始", "填寫申請表", "送出", "結束", "簡單流程", `marker-end="url(#fc-arrow)"`} {
		if !strings.Contains(r.SVG, s) {
			t.Fatalf("少了 %q", s)
		}
	}
	L := layoutOf(t, spec)
	// 一直線:四個節點在同一個 u、各自一層,邊都是直的
	for i := 0; i < 4; i++ {
		if L.nodes[i].rank != i || math.Abs(L.nodes[i].u-L.nodes[0].u) > 0.5 {
			t.Fatalf("節點 %d:rank %d u %v", i, L.nodes[i].rank, L.nodes[i].u)
		}
	}
	for _, e := range L.edges {
		if len(e.pts) != 2 {
			t.Fatalf("直線流程的邊應該是直的:%v", e.pts)
		}
	}
}

const loopFlow = `{
  "direction": "TB",
  "nodes": [
    {"id": "start", "label": "開始", "shape": "start"},
    {"id": "input", "label": "輸入帳號密碼", "shape": "io"},
    {"id": "check", "label": "驗證通過?", "shape": "decision"},
    {"id": "count", "label": "失敗次數 +1"},
    {"id": "lock", "label": "超過 5 次?", "shape": "decision"},
    {"id": "locked", "label": "鎖定帳號", "style": "highlight"},
    {"id": "db", "label": "使用者資料庫", "shape": "database"},
    {"id": "ok", "label": "進入首頁", "shape": "end"}
  ],
  "edges": [
    {"from": "start", "to": "input"},
    {"from": "input", "to": "check"},
    {"from": "check", "to": "ok", "label": "是"},
    {"from": "check", "to": "count", "label": "否"},
    {"from": "count", "to": "lock"},
    {"from": "lock", "to": "locked", "label": "是"},
    {"from": "lock", "to": "input", "label": "否,重新輸入"},
    {"from": "check", "to": "db", "style": "dashed", "label": "查詢"}
  ]
}`

func TestDecisionAndLoop(t *testing.T) {
	r := render(t, "loop", loopFlow)
	if len(r.Warnings) != 0 {
		t.Fatal(r.Warnings)
	}
	for _, s := range []string{"<polygon", "否,重新輸入", "stroke-dasharray", "<ellipse", "#2563eb"} {
		if !strings.Contains(r.SVG, s) {
			t.Fatalf("少了 %q", s)
		}
	}
	L := layoutOf(t, loopFlow)
	var back *ledge
	for _, e := range L.edges {
		if e.back {
			if back != nil {
				t.Fatal("只有一條回邊")
			}
			back = e
		}
	}
	if back == nil || back.e.From != "lock" || back.e.To != "input" {
		t.Fatalf("回邊應該是 lock → input:%+v", back)
	}
	// 回邊從側邊繞:起點在 lock 的右側、終點在 input 的右側,中間都在兩個節點右邊
	s, tn := L.nodes[back.from], L.nodes[back.to]
	first, last := back.pts[0], back.pts[len(back.pts)-1]
	if math.Abs(first.v-L.vc[s.rank]) > 0.5 || first.u < s.u || math.Abs(last.v-L.vc[tn.rank]) > 0.5 || last.u < tn.u {
		t.Fatalf("回邊端點:%v %v", first, last)
	}
	for _, p := range back.pts[1 : len(back.pts)-1] {
		if p.u <= s.u+s.b/2 || p.u <= tn.u+tn.b/2 {
			t.Fatalf("回邊應該在節點右側繞:%v", back.pts)
		}
	}
	// 決策節點出去的兩條邊的標籤不重疊
	var labs []vec
	for _, e := range L.edges {
		if e.e.From == "check" && (e.e.Label == "是" || e.e.Label == "否") {
			labs = append(labs, e.lab)
		}
	}
	if len(labs) != 2 || math.Abs(labs[0].u-labs[1].u) < 20 {
		t.Fatalf("是/否標籤:%v", labs)
	}
}

const mermaidFlow = `---
title: 部署流程
---
flowchart TD
    %% 這是註解
    A([開始]) --> B[建置映像檔]
    B --> C{測試通過?}
    C -->|是| D[[部署到 staging]]
    C -- 否 --> E[通知開發者]:::highlight
    E -.-> B
    subgraph prod [正式環境]
        D ==> F[(資料庫遷移)]
        F --> G[/切換流量/] --> H((完成))
    end
    D & F --> L>寫入稽核紀錄]
    classDef red fill:#f00
    style A fill:#f9f
    click A "javascript:alert(1)"
    H --- Z{{清理}}; Z ~~~ A
`

func TestMermaid(t *testing.T) {
	g, err := Parse(mermaidFlow)
	if err != nil {
		t.Fatal(err)
	}
	if g.Title != "部署流程" || g.Direction != "TB" {
		t.Fatal(g.Title, g.Direction)
	}
	shapes := map[string]string{}
	for _, n := range g.Nodes {
		shapes[n.ID] = n.Shape + "|" + n.Label + "|" + n.Group + "|" + n.Style
	}
	want := map[string]string{
		"A": "start|開始||", "B": "process|建置映像檔||", "C": "decision|測試通過?||", "D": "subprocess|部署到 staging|prod|",
		"E": "process|通知開發者||highlight", "F": "database|資料庫遷移|prod|", "G": "io|切換流量|prod|", "H": "circle|完成|prod|",
		"L": "process|寫入稽核紀錄||", "Z": "hexagon|清理||",
	}
	for k, v := range want {
		if shapes[k] != v {
			t.Fatalf("%s:%q,應該是 %q", k, shapes[k], v)
		}
	}
	if len(g.Groups) != 1 || g.Groups[0].ID != "prod" || g.Groups[0].Label != "正式環境" {
		t.Fatalf("%+v", g.Groups)
	}
	type ek struct{ from, to, label, style, arrow string }
	got := []ek{}
	for _, e := range g.Edges {
		got = append(got, ek{e.From, e.To, e.Label, e.Style, e.Arrow})
	}
	wantE := []ek{
		{"A", "B", "", "", ""}, {"B", "C", "", "", ""}, {"C", "D", "是", "", ""}, {"C", "E", "否", "", ""}, {"E", "B", "", "dashed", ""},
		{"D", "F", "", "thick", ""}, {"F", "G", "", "", ""}, {"G", "H", "", "", ""}, {"D", "L", "", "", ""}, {"F", "L", "", "", ""},
		{"H", "Z", "", "", "none"}, {"Z", "A", "", "invisible", "none"},
	}
	if len(got) != len(wantE) {
		t.Fatalf("%d 條邊:%v", len(got), got)
	}
	for i := range got {
		if got[i] != wantE[i] {
			t.Fatalf("第 %d 條邊 %v,應該是 %v", i, got[i], wantE[i])
		}
	}
	// classDef、style、click 忽略並提醒
	if len(g.Warnings) != 3 {
		t.Fatal(g.Warnings)
	}
	r := render(t, "mermaid", mermaidFlow)
	if strings.Contains(r.SVG, "javascript") || !strings.Contains(r.SVG, "正式環境") || !strings.Contains(r.SVG, `stroke-dasharray="5 4"`) {
		t.Fatal("subgraph 框或忽略 click 有問題")
	}
	layoutOf(t, mermaidFlow)

	// 包在 ```mermaid 區塊裡、graph 關鍵字、分號
	g, err = Parse("```mermaid\ngraph LR; a-->b; b-- 標 籤 -->c\n```")
	if err != nil || g.Direction != "LR" || len(g.Edges) != 2 || g.Edges[1].Label != "標 籤" {
		t.Fatal(g, err)
	}
	// 引號文字裡可以有括號、<br> 換行;雙向箭頭
	g, err = Parse(`flowchart TB
  a["說明 (含括號) [x]"] <--> b("第一行<br/>第二行")
  subgraph outer
    subgraph inner [內層]
      c
    end
    d
  end`)
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[0].Label != "說明 (含括號) [x]" || g.Nodes[1].Label != "第一行\n第二行" || g.Edges[0].Arrow != "both" {
		t.Fatalf("%+v %+v %+v", g.Nodes[0], g.Nodes[1], g.Edges[0])
	}
	if g.Nodes[2].Group != "inner" || g.Nodes[3].Group != "outer" || g.Groups[1].Parent != "outer" {
		t.Fatalf("%+v %+v %+v", g.Nodes[2], g.Nodes[3], g.Groups[1])
	}
	layoutOf(t, g)
}

func TestLR(t *testing.T) {
	src := `flowchart LR
    req[收到請求] --> auth{已登入?}
    auth -->|否| login[導向登入頁]
    auth -->|是| cache{快取命中?}
    cache -->|是| resp[回傳結果]
    cache -->|否| db[(查資料庫)] --> resp
    login --> req`
	r := render(t, "lr", src)
	if r.Width <= r.Height {
		t.Fatalf("LR 應該是橫的:%d×%d", r.Width, r.Height)
	}
	L := layoutOf(t, src)
	// LR:層沿著 x 排,所以 req 在最左邊
	req, resp := L.nodes[0], L.nodes[4]
	if req.rank != 0 || resp.rank <= L.nodes[3].rank {
		t.Fatal(req.rank, resp.rank)
	}
	// RL / BT 也畫得出來
	render(t, "rl", strings.Replace(src, "LR", "RL", 1))
	render(t, "bt", strings.Replace(src, "LR", "BT", 1))
}

func TestGroupsJSON(t *testing.T) {
	spec := `{"direction": "TB", "groups": [{"id": "fe", "label": "前端"}, {"id": "be", "label": "後端"}, {"id": "data", "label": "資料層", "parent": "be"}],
	  "nodes": [{"id": "ui", "label": "畫面", "group": "fe"}, {"id": "api", "label": "API", "group": "be"}, {"id": "svc", "label": "服務", "group": "be"},
	    {"id": "db", "label": "DB", "shape": "database", "group": "data"}, {"id": "q", "label": "佇列", "group": "data"}, {"id": "note", "label": "非同步處理", "shape": "note", "style": "muted"},
	    {"id": "loop", "label": "重試", "shape": "round"}],
	  "edges": [{"from": "ui", "to": "api"}, {"from": "api", "to": "svc"}, {"from": "svc", "to": "db"}, {"from": "svc", "to": "q"}, {"from": "q", "to": "note", "style": "dashed"},
	    {"from": "ui", "to": "db", "label": "直接讀"}, {"from": "loop", "to": "loop", "label": "每 5 秒"}, {"from": "q", "to": "loop"}]}`
	r := render(t, "groups", spec)
	for _, s := range []string{"前端", "後端", "資料層", "每 5 秒"} {
		if !strings.Contains(r.SVG, s) {
			t.Fatalf("少了 %q", s)
		}
	}
	L := layoutOf(t, spec)
	// 同一層裡同群組的節點相鄰
	for _, l := range L.layers {
		last := map[int]int{}
		for p, i := range l {
			n := L.nodes[i]
			if n.n == nil || len(n.chain) == 0 {
				continue
			}
			g := n.chain[0]
			if q, ok := last[g]; ok && q != p-1 {
				between := L.nodes[l[p-1]]
				if len(between.chain) == 0 || between.chain[0] != g {
					t.Fatalf("群組 %d 的成員不相鄰", g)
				}
			}
			last[g] = p
		}
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]string{
		"":                                      "描述是空的",
		"{":                                     "不是有效的 JSON",
		`{"nodes": []}`:                         "沒有節點",
		`{"nodes": [{"id": "a"}, {"id": "a"}]}`: "節點 id 重複:「a」",
		`{"nodes": [{"id": "a"}], "edges": [{"from": "a", "to": "x"}]}`: "edges[0] 的 to「x」不是已知的節點 id",
		"sequenceDiagram\n  A->>B: hi":                                  "第 1 行:只支援 Mermaid 的 flowchart / graph 圖(這裡是「sequenceDiagram」)",
		"flowchart TD\n  A --> B\n  B --> C[沒結束":                        "第 3 行:節點「C」的形狀括號沒有結束(缺少「]」)",
		"flowchart TD\n  A -->":                                         "第 2 行:連線後面少了節點",
		"flowchart TD\n  A --> B\n  end":                                "第 3 行:多了一個 end",
		"flowchart TD\n  subgraph s1\n  A --> B":                        "第 2 行:subgraph「s1」沒有對應的 end",
		"flowchart XY\n  A --> B":                                       "第 1 行:不認得的方向「XY」",
		"flowchart TD\n  A -- 沒有結尾 B":                                   "第 2 行:「-- 文字 -->」沒有結尾的 -->",
		"flowchart TD\n  A@{ shape: rect }":                             "不支援 @{ shape: … } 的新語法",
		"flowchart TD\n  subgraph s1\n  A\n  end\n  B --> s1":           "第 5 行:「s1」是 subgraph 的 id",
		"flowchart TD\n  A --> B |x":                                    "預期連線",
	}
	for in, want := range cases {
		_, err := Render(in)
		var fe *Error
		if !errors.As(err, &fe) || !strings.Contains(err.Error(), want) {
			t.Errorf("%q:%v,應該包含 %q", in, err, want)
		}
	}
	// 上限
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for i := 0; i <= MaxNodes; i++ {
		b.WriteString("  n" + itoa(i) + "\n")
	}
	if _, err := Render(b.String()); err == nil || !strings.Contains(err.Error(), "節點太多") {
		t.Fatal(err)
	}
	if _, err := Render(strings.Repeat(" ", MaxBytes+1) + "x"); err == nil || !strings.Contains(err.Error(), "描述太大") {
		t.Fatal(err)
	}
	// 可以容忍的:不認得的形狀與樣式
	r, err := Render(`{"nodes": [{"id": "a", "shape": "star", "style": "glow"}]}`)
	if err != nil || len(r.Warnings) != 2 {
		t.Fatal(r, err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}

func TestEscape(t *testing.T) {
	spec := map[string]any{"title": `</title><script>alert(1)</script>`, "nodes": []any{
		map[string]any{"id": "a", "label": `<script>alert("x")</script> & 'q'` + "\x00\x1b"},
		map[string]any{"id": "b", "label": `"><foreignObject>`},
	}, "edges": []any{map[string]any{"from": "a", "to": "b", "label": `<a href="javascript:x">`}}}
	r := render(t, "escape", spec)
	if strings.Contains(r.SVG, "<script") || strings.Contains(r.SVG, "<foreignObject") || strings.Contains(r.SVG, "\x00") || strings.Contains(r.SVG, "\x1b") {
		t.Fatal(r.SVG)
	}
	if !strings.Contains(r.SVG, "&lt;script&gt;") || !strings.Contains(r.SVG, "&amp;") {
		t.Fatal("沒有 escape")
	}
	// Mermaid 的 click 與 javascript: 不會出現在輸出
	r = render(t, "escape2", "flowchart TD\n  a[\"<img src=x onerror=alert(1)>\"] --> b\n  click a \"javascript:alert(1)\"")
	if strings.Contains(r.SVG, "<img") || strings.Contains(strings.ToLower(r.SVG), "javascript") {
		t.Fatal(r.SVG)
	}
}

func TestMarkdown(t *testing.T) {
	md := "說明\n\n```mermaid\nsequenceDiagram\n  A->>B: x\n```\n\n```mermaid\n%% 註解\ngraph TD\n  a --> b\n```\n\n```flowchart\n{\"nodes\": [\"x\"]}\n```\n尾巴"
	b := Blocks(md)
	if len(b) != 2 || !strings.Contains(b[0], "graph TD") || !strings.Contains(b[1], `"x"`) {
		t.Fatalf("%q", b)
	}
	// 原本是 ```mermaid、新描述也是 Mermaid:維持 ```mermaid(其他 Markdown 檢視器才看得懂)
	up := Upsert(md, "flowchart LR\n  c --> d")
	if !strings.Contains(up, "sequenceDiagram") || !strings.Contains(up, "```mermaid\nflowchart LR\n  c --> d\n```") || strings.Contains(up, "graph TD") || !strings.HasSuffix(up, "尾巴") {
		t.Fatalf("%q", up)
	}
	// 新描述是 JSON:一定用 ```flowchart
	up = Upsert(md, `{"nodes": ["z"]}`)
	if !strings.Contains(up, "```flowchart\n{\"nodes\": [\"z\"]}\n```") || strings.Contains(up, "graph TD") {
		t.Fatalf("%q", up)
	}
	// FirstBlock 回傳完整原文與語言;info string 帶其他字也認得
	blk, lang, ok := FirstBlock("x\n```mermaid title=\"流程\"\nflowchart TD\n  a --> b\n```\ny")
	if !ok || lang != "mermaid" || blk != "```mermaid title=\"流程\"\nflowchart TD\n  a --> b\n```" {
		t.Fatalf("%v %q %q", ok, lang, blk)
	}
	if _, _, ok := FirstBlock("沒有圖"); ok {
		t.Fatal("沒有區塊應該 ok=false")
	}
	if got := Upsert("", "x"); got != "```flowchart\nx\n```\n" {
		t.Fatalf("%q", got)
	}
	// Source:Mermaid 原文照存;JSON 整理成每個節點一行
	src, err := Source("```mermaid\nflowchart TD\n  a-->b\n```")
	if err != nil || src != "flowchart TD\n  a-->b" {
		t.Fatalf("%q %v", src, err)
	}
	src, err = Source(map[string]any{"nodes": []any{map[string]any{"id": "a", "label": "開始", "shape": "start"}, "b"}, "edges": []any{map[string]any{"from": "a", "to": "b", "label": "下一步"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"direction\": \"TB\",\n  \"nodes\": [\n    {\"id\": \"a\", \"label\": \"開始\", \"shape\": \"start\"},\n    {\"id\": \"b\"}\n  ],\n  \"edges\": [\n    {\"from\": \"a\", \"to\": \"b\", \"label\": \"下一步\"}\n  ]\n}"
	if src != want {
		t.Fatalf("%s", src)
	}
	if _, err := Render(src); err != nil {
		t.Fatal(err)
	}
	if !IsMermaidFlowchart("---\ntitle: x\n---\nflowchart LR\n a-->b") || IsMermaidFlowchart("pie\n \"a\": 1") {
		t.Fatal("IsMermaidFlowchart")
	}
}

// 大圖:300 個節點、600 條邊也要能在合理時間內畫完
func TestLarge(t *testing.T) {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for i := 0; i < 300; i++ {
		b.WriteString("  n" + itoa(i) + " --> n" + itoa((i*7+3)%300) + "\n")
		b.WriteString("  n" + itoa(i) + " --> n" + itoa((i+1)%300) + "\n")
	}
	r, err := Render(b.String())
	if err != nil {
		t.Fatal(err)
	}
	checkSVG(t, r.SVG)
}
