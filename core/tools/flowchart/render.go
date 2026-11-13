package flowchart

import (
	"math"
	"sort"
	"strings"
)

// Result 渲染結果
type Result struct {
	SVG      string   `json:"svg"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Warnings []string `json:"warnings"`
}

const margin = 24.0

type box struct{ x, y, w, h float64 }

func (a box) overlaps(b box) bool {
	return a.x < b.x+b.w && b.x < a.x+a.w && a.y < b.y+b.h && b.y < a.y+a.h
}

// Render 把描述(JSON 物件/字串,或 Mermaid flowchart 文字;外面包了 ```flowchart / ```mermaid 區塊也可以)排版並畫成 SVG。
// 描述格式錯誤回 *Error;可以容忍的問題(不認得的形狀、忽略的 Mermaid 語法)放進 Warnings。
func Render(in any) (*Result, error) {
	g, err := Parse(in)
	if err != nil {
		return nil, err
	}
	return RenderGraph(g), nil
}

// RenderGraph 畫一份已解析(Parse)的流程圖
func RenderGraph(g *Graph) *Result {
	L := newLayout(g)
	L.build()
	L.order()
	L.coords()
	L.route()

	// 抽象座標 → 實際座標
	real := func(p vec) (float64, float64) {
		x, y := p.u, p.v
		if L.horiz {
			x, y = p.v, p.u
		}
		if L.flip {
			if L.horiz {
				x = -x
			} else {
				y = -y
			}
		}
		return x, y
	}
	N := len(g.Nodes)
	nb := make([]box, N)
	for i := 0; i < N; i++ {
		n := L.nodes[i]
		cx, cy := real(vec{n.u, L.vc[n.rank]})
		nb[i] = box{cx - n.w/2, cy - n.h/2, n.w, n.h}
	}
	// 群組框:由內而外,包住成員與子群組
	G := len(g.Groups)
	gb := make([]box, G)
	has := make([]bool, G)
	depthOf := func(i int) int {
		d := 0
		for p := L.gpar[i]; p >= 0 && d <= G; p = L.gpar[p] {
			d++
		}
		return d
	}
	gorder := make([]int, G)
	for i := range gorder {
		gorder[i] = i
	}
	sort.SliceStable(gorder, func(a, b int) bool { return depthOf(gorder[a]) > depthOf(gorder[b]) })
	union := func(gi int, b box) {
		if !has[gi] {
			gb[gi], has[gi] = b, true
			return
		}
		a := gb[gi]
		x0, y0 := math.Min(a.x, b.x), math.Min(a.y, b.y)
		x1, y1 := math.Max(a.x+a.w, b.x+b.w), math.Max(a.y+a.h, b.y+b.h)
		gb[gi] = box{x0, y0, x1 - x0, y1 - y0}
	}
	for i := 0; i < N; i++ {
		if ch := L.nodes[i].chain; len(ch) > 0 {
			b := nb[i]
			if L.nodes[i].extra > 0 { // 自環也包進去
				if L.horiz {
					b.h += L.nodes[i].extra
				} else {
					b.w += L.nodes[i].extra
				}
			}
			union(ch[len(ch)-1], b)
		}
	}
	for _, gi := range gorder {
		if !has[gi] {
			continue
		}
		b := gb[gi]
		gb[gi] = box{b.x - 14, b.y - 30, b.w + 28, b.h + 44}
		if p := L.gpar[gi]; p >= 0 {
			union(p, gb[gi])
		}
	}
	for gi, gr := range g.Groups {
		if !has[gi] {
			g.warn("群組「%s」沒有任何節點,不畫", gr.ID)
		}
	}

	// 邊與標籤的實際座標
	type redge struct {
		e    *ledge
		pts  [][2]float64
		lab  box
		labC [2]float64
	}
	res := []*redge{}
	for _, e := range L.edges {
		if len(e.pts) == 0 {
			continue
		}
		r := &redge{e: e}
		for _, p := range e.pts {
			x, y := real(p)
			r.pts = append(r.pts, [2]float64{x, y})
		}
		r.pts = simplify(r.pts)
		if len(e.lines) > 0 {
			x, y := real(e.lab)
			r.labC = [2]float64{x, y}
			r.lab = box{x - e.lw/2, y - e.lh/2, e.lw, e.lh}
		}
		res = append(res, r)
	}
	// 標籤避開節點與其他標籤:沿流向前後挪
	placed := []box{}
	for _, r := range res {
		if len(r.e.lines) == 0 {
			continue
		}
		free := func(b box) bool {
			for _, n := range nb {
				if b.overlaps(box{n.x - 2, n.y - 2, n.w + 4, n.h + 4}) {
					return false
				}
			}
			for _, p := range placed {
				if b.overlaps(p) {
					return false
				}
			}
			return true
		}
		best := r.lab
		if !free(best) {
			for k := 1; k <= 12; k++ {
				found := false
				for _, sgn := range []float64{1, -1} {
					d := sgn * float64(k) * 6
					c := r.lab
					if L.horiz {
						c.x += d
					} else {
						c.y += d
					}
					if free(c) {
						best, found = c, true
						break
					}
				}
				if found {
					break
				}
			}
		}
		r.lab = best
		r.labC = [2]float64{best.x + best.w/2, best.y + best.h/2}
		placed = append(placed, best)
	}

	// 整張圖的範圍
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	add := func(b box) {
		x0, y0 = math.Min(x0, b.x), math.Min(y0, b.y)
		x1, y1 = math.Max(x1, b.x+b.w), math.Max(y1, b.y+b.h)
	}
	for _, b := range nb {
		add(b)
	}
	for gi := range gb {
		if has[gi] {
			add(gb[gi])
		}
	}
	for _, r := range res {
		for _, p := range r.pts {
			add(box{p[0] - 6, p[1] - 6, 12, 12})
		}
		if len(r.e.lines) > 0 {
			add(r.lab)
		}
	}
	titleH := 0.0
	var titleLines []string
	if g.Title != "" {
		titleLines = wrap(g.Title, math.Max(x1-x0, 240), 16, 2)
		titleH = 16*lineMult*float64(len(titleLines)) + 12
		add(box{x0, y0, linesW(titleLines, 16), 0})
	}
	dx, dy := margin-x0, margin+titleH-y0
	W := math.Ceil(x1 - x0 + 2*margin)
	H := math.Ceil(y1 - y0 + 2*margin + titleH)

	c := &canvas{}
	if len(titleLines) > 0 {
		for i, l := range titleLines {
			c.text(margin, margin+16*lineMult*(float64(i)+0.5), l, 16, cText, true, "start")
		}
	}
	// 群組(外層先畫)
	for i := len(gorder) - 1; i >= 0; i-- {
		gi := gorder[i]
		if !has[gi] {
			continue
		}
		b := gb[gi]
		c.rect(b.x+dx, b.y+dy, b.w, b.h, 8, cGroupFill, cGroupLine, 1, true)
		c.text(b.x+dx+10, b.y+dy+15, truncate(strings.ReplaceAll(g.Groups[gi].Label, "\n", " "), b.w-20, groupFS), groupFS, cGroupText, true, "start")
	}
	// 邊
	for _, r := range res {
		e := r.e.e
		pts := make([][2]float64, len(r.pts))
		for i, p := range r.pts {
			pts[i] = [2]float64{p[0] + dx, p[1] + dy}
		}
		width, dashed := 1.5, false
		switch e.Style {
		case "dashed":
			dashed = true
		case "thick":
			width = 2.5
		}
		extra := ""
		if e.Arrow != "none" {
			extra += ` marker-end="url(#fc-arrow)"`
		}
		if e.Arrow == "both" {
			extra += ` marker-start="url(#fc-arrow)"`
		}
		c.path(roundPath(pts, 6), "none", cEdge, width, dashed, ` stroke-linejoin="round"`+extra)
	}
	// 節點
	for i := 0; i < N; i++ {
		drawNode(c, L.nodes[i], nb[i].x+dx, nb[i].y+dy)
	}
	// 邊的標籤(白底)
	for _, r := range res {
		if len(r.e.lines) == 0 {
			continue
		}
		b := r.lab
		c.rect(b.x+dx, b.y+dy, b.w, b.h, 3, cBg, "", 0, false)
		c.lines(r.labC[0]+dx, r.labC[1]+dy, r.e.lines, edgeFS, cEdgeText, false)
	}

	var out strings.Builder
	title := g.Title
	if title == "" {
		title = "流程圖"
	}
	out.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="` + f(W) + `" height="` + f(H) + `" viewBox="0 0 ` + f(W) + ` ` + f(H) +
		`" role="img" aria-label="` + esc(title) + `"><title>` + esc(title) + `</title><style>` + fontCSS + `</style>`)
	out.WriteString(`<defs><marker id="fc-arrow" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="10" markerHeight="10" markerUnits="userSpaceOnUse" orient="auto-start-reverse">` +
		`<path d="M0,0 L10,5 L0,10 z" fill="` + cEdge + `"/></marker></defs>`)
	out.WriteString(`<rect x="0" y="0" width="` + f(W) + `" height="` + f(H) + `" fill="` + cBg + `"/>`)
	out.WriteString(c.b.String())
	out.WriteString("</svg>")
	warns := g.Warnings
	if warns == nil {
		warns = []string{}
	}
	return &Result{SVG: out.String(), Width: int(W), Height: int(H), Warnings: warns}
}

// simplify 去掉重複點與共線的中間點
func simplify(pts [][2]float64) [][2]float64 {
	out := [][2]float64{}
	for _, p := range pts {
		if n := len(out); n > 0 && math.Abs(out[n-1][0]-p[0]) < 0.05 && math.Abs(out[n-1][1]-p[1]) < 0.05 {
			continue
		}
		out = append(out, p)
	}
	for i := 1; i+1 < len(out); {
		a, b, c := out[i-1], out[i], out[i+1]
		cross := (b[0]-a[0])*(c[1]-b[1]) - (b[1]-a[1])*(c[0]-b[0])
		dot := (b[0]-a[0])*(c[0]-b[0]) + (b[1]-a[1])*(c[1]-b[1])
		if math.Abs(cross) < 0.01 && dot >= 0 {
			out = append(out[:i], out[i+1:]...)
			continue
		}
		i++
	}
	return out
}

// roundPath 折線在轉角處用小圓弧(二次曲線)接起來
func roundPath(pts [][2]float64, r float64) string {
	if len(pts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("M" + f(pts[0][0]) + " " + f(pts[0][1]))
	for i := 1; i < len(pts); i++ {
		p := pts[i]
		if i == len(pts)-1 {
			b.WriteString(" L" + f(p[0]) + " " + f(p[1]))
			break
		}
		a, n := pts[i-1], pts[i+1]
		d1 := math.Hypot(p[0]-a[0], p[1]-a[1])
		d2 := math.Hypot(n[0]-p[0], n[1]-p[1])
		rr := math.Min(r, math.Min(d1/2, d2/2))
		if rr < 0.5 {
			b.WriteString(" L" + f(p[0]) + " " + f(p[1]))
			continue
		}
		s := [2]float64{p[0] - (p[0]-a[0])/d1*rr, p[1] - (p[1]-a[1])/d1*rr}
		t := [2]float64{p[0] + (n[0]-p[0])/d2*rr, p[1] + (n[1]-p[1])/d2*rr}
		b.WriteString(" L" + f(s[0]) + " " + f(s[1]) + " Q" + f(p[0]) + " " + f(p[1]) + " " + f(t[0]) + " " + f(t[1]))
	}
	return b.String()
}

// drawNode 畫一個節點((x, y) 是左上角)
func drawNode(c *canvas, n *lnode, x, y float64) {
	w, h := n.w, n.h
	fill, stroke, text, sw, dashed := cNodeFill, cNodeLine, cText, 1.5, false
	switch n.n.Shape {
	case "start", "end":
		fill = cTermFill
	case "note":
		fill = cNoteFill
	case "decision":
		fill, stroke, text = cAccentFill, cAccent, cAccentText
	}
	switch n.n.Style {
	case "highlight":
		fill, stroke, text, sw = cAccentHi, cAccent, cAccentText, 2
	case "muted":
		fill, stroke, text, dashed = cMutedFill, cMutedLine, cMutedText, true
	}
	cx, cy := x+w/2, y+h/2
	switch n.n.Shape {
	case "decision":
		c.polygon([]float64{cx, y, x + w, cy, cx, y + h, x, cy}, fill, stroke, sw, dashed)
	case "start", "end":
		c.rect(x, y, w, h, h/2, fill, stroke, sw, dashed)
	case "round":
		c.rect(x, y, w, h, 12, fill, stroke, sw, dashed)
	case "io":
		c.polygon([]float64{x + skewIO, y, x + w, y, x + w - skewIO, y + h, x, y + h}, fill, stroke, sw, dashed)
	case "subprocess":
		c.rect(x, y, w, h, 2, fill, stroke, sw, dashed)
		c.line(x+8, y, x+8, y+h, stroke, 1)
		c.line(x+w-8, y, x+w-8, y+h, stroke, 1)
	case "database":
		ry := cylRY
		d := "M" + f(x) + " " + f(y+ry) + " L" + f(x) + " " + f(y+h-ry) + " A" + f(w/2) + " " + f(ry) + " 0 0 0 " + f(x+w) + " " + f(y+h-ry) +
			" L" + f(x+w) + " " + f(y+ry) + " Z"
		c.path(d, fill, stroke, sw, dashed, "")
		c.ellipse(cx, y+ry, w/2, ry, fill, stroke, sw, dashed)
		cy += ry
	case "note":
		fold := 12.0
		c.polygon([]float64{x, y, x + w - fold, y, x + w, y + fold, x + w, y + h, x, y + h}, fill, stroke, sw, dashed)
		c.polyline([]float64{x + w - fold, y, x + w - fold, y + fold, x + w, y + fold}, stroke, 1)
	case "circle":
		c.ellipse(cx, cy, w/2, h/2, fill, stroke, sw, dashed)
	case "hexagon":
		c.polygon([]float64{x + hexIn, y, x + w - hexIn, y, x + w, cy, x + w - hexIn, y + h, x + hexIn, y + h, x, cy}, fill, stroke, sw, dashed)
	default:
		c.rect(x, y, w, h, 4, fill, stroke, sw, dashed)
	}
	c.lines(cx, cy, n.lines, nodeFS, text, n.n.Shape == "start" || n.n.Shape == "end")
}
