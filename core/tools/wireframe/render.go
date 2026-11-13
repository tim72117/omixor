package wireframe

import (
	"math"
	"strings"
)

// Result 渲染結果
type Result struct {
	SVG      string   `json:"svg"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Warnings []string `json:"warnings"`
}

// Render 把描述(物件、JSON 字串或 []byte;字串外面包了 ```wireframe 區塊也可以)畫成 SVG。
// 描述格式錯誤回 *Error;不認得的元件只會畫成虛線框並放進 Warnings。
func Render(in any) (*Result, error) {
	s, err := parse(in)
	if err != nil {
		return nil, err
	}
	r := &renderer{s: s, W: s.w}
	H := math.Max(s.h, math.Ceil(r.boxH(s.root, s.w)))
	r.place(s.root, 0, 0, s.w, H)

	// 疊層(modal):先量大小,畫布要容得下
	type mbox struct {
		n          *node
		x, y, w, h float64
	}
	boxes := []mbox{}
	for _, m := range s.modals {
		mw := math.Min(480, s.w-32)
		switch d := m.dim("width"); d.kind {
		case dimFixed:
			mw = d.v
		case dimPct:
			mw = s.w * d.v
		case dimFill:
			mw = s.w - 32
		}
		mw = clamp(mw, math.Min(160, s.w), math.Max(160, s.w-32))
		mh := r.col(m, 0, 0, mw, 0, false)
		if d := m.dim("height"); d.kind == dimFixed {
			mh = d.v
		}
		my := math.Max(24, (s.h-mh)/2)
		boxes = append(boxes, mbox{m, (s.w - mw) / 2, my, mw, mh})
		H = math.Max(H, math.Ceil(my+mh+24))
	}
	if H > MaxCanvas {
		s.warn("畫面太長,只畫到 %d px", MaxCanvas)
		H = MaxCanvas
	}
	if H > s.h+1 { // 第一屏的位置
		r.c.line(0, s.h, s.w, s.h, cSoft, 1, true)
		r.c.text(s.w-8, s.h-18, "第一屏", 11, cMuted, false, "end")
	}
	for _, b := range boxes {
		r.c.b.WriteString(`<rect x="0" y="0" width="` + f(s.w) + `" height="` + f(H) + `" fill="#111827" fill-opacity="0.35"/>`)
		r.col(b.n, b.x, b.y, b.w, b.h, true)
	}

	var out strings.Builder
	title := s.title
	if title == "" {
		title = "線框圖"
	}
	out.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="` + f(s.w) + `" height="` + f(H) + `" viewBox="0 0 ` + f(s.w) + ` ` + f(H) +
		`" role="img" aria-label="` + esc(title) + `"><title>` + esc(title) + `</title><style>` + fontCSS + `</style>`)
	out.WriteString(`<rect x="0" y="0" width="` + f(s.w) + `" height="` + f(H) + `" fill="` + cBg + `"/>`)
	out.WriteString(r.c.b.String())
	out.WriteString("</svg>")
	warns := s.warns
	if warns == nil {
		warns = []string{}
	}
	return &Result{SVG: out.String(), Width: int(s.w), Height: int(H), Warnings: warns}, nil
}

type renderer struct {
	s *spec
	c canvas
	W float64
}

// ---- 分類 ----

func isUnknown(n *node) bool { return !known[n.typ] }

// container 會排子節點的類型(不認得的類型也當成直排的框)
func container(n *node) bool {
	switch n.typ {
	case "col", "row", "stack", "grid", "card", "section":
		return true
	}
	return isUnknown(n)
}

// intrinsic 寬度依內容決定的元件
func intrinsic(t string) bool {
	switch t {
	case "button", "badge", "avatar", "icon", "checkbox", "radio", "toggle", "link":
		return true
	}
	return false
}

// stretchy 在 row 裡沒指定 align 時撐滿高度的
func stretchy(n *node) bool {
	return container(n) || n.typ == "sidebar" || n.typ == "divider"
}

// flow 參與版面的子節點(modal 另外畫在最上層)
func flow(n *node) []*node {
	out := make([]*node, 0, len(n.kids))
	for _, k := range n.kids {
		if k.typ != "modal" {
			out = append(out, k)
		}
	}
	return out
}

// ---- 尺寸 ----

// boxH 節點在寬度 w 時的高度:有固定高度用固定的,否則量內容
func (r *renderer) boxH(n *node, w float64) float64 {
	if d := n.dim("height"); d.kind == dimFixed {
		return d.v
	}
	return r.natH(n, w)
}

// widthIn 子節點在父層(dir:'r' 橫排、'c' 直排或疊放)可用寬度 avail 下的寬度;fill = 要分剩餘空間(只在橫排)
func (r *renderer) widthIn(k *node, avail float64, dir byte) (float64, bool) {
	if k.typ == "spacer" {
		if dir != 'r' {
			return 0, false
		}
		if sz, ok := k.num("size"); ok {
			return math.Min(math.Max(sz, 0), avail), false
		}
		return 0, true
	}
	switch d := k.dim("width"); d.kind {
	case dimFixed:
		return math.Min(d.v, avail), false
	case dimPct:
		return avail * d.v, false
	case dimFill:
		if dir == 'r' {
			return 0, true
		}
		return avail, false
	}
	if dir == 'r' {
		switch {
		case intrinsic(k.typ), k.typ == "text", k.typ == "heading", k.typ == "divider":
			return math.Min(r.natW(k, avail), avail), false
		case k.typ == "sidebar":
			return math.Min(240, avail), false
		}
		return 0, true
	}
	if intrinsic(k.typ) {
		return math.Min(r.natW(k, avail), avail), false
	}
	return avail, false
}

func btnFont(n *node) (fs, h float64) {
	switch n.str("size") {
	case "sm", "small":
		return 13, 32
	case "lg", "large":
		return 16, 48
	}
	return 14, 40
}

// natW 依內容的寬度
func (r *renderer) natW(n *node, avail float64) float64 {
	switch n.typ {
	case "button":
		fs, _ := btnFont(n)
		w := textW(n.text("text", "label"), fs) + 32
		if n.text("text", "label") == "" {
			w = textW("按鈕", fs) + 32
		}
		return math.Max(w, 64)
	case "badge":
		t := n.text("text", "label")
		if t == "" {
			t = "標籤"
		}
		return textW(t, 12) + 20
	case "avatar":
		s := clamp(n.numOr("size", 40), 8, 400)
		if l := n.text("label", "text", "name"); l != "" {
			return s + 8 + textW(l, 14)
		}
		return s
	case "icon":
		return clamp(n.numOr("size", 24), 8, 400)
	case "checkbox", "radio":
		return 24 + textW(n.text("label", "text"), 14)
	case "toggle":
		if l := n.text("label", "text"); l != "" {
			return 44 + textW(l, 14)
		}
		return 36
	case "link":
		return textW(n.text("text", "label"), textSize(n)) + 1
	case "text", "heading":
		t := n.text("text", "label")
		if t == "" {
			return math.Min(avail, 160)
		}
		fs := textSize(n)
		w := 0.0
		for _, l := range strings.Split(t, "\n") {
			w = math.Max(w, textW(l, fs))
		}
		return w + 1
	case "divider":
		return 1
	}
	return avail
}

func textSize(n *node) float64 {
	if v, ok := n.num("size"); ok {
		return clamp(v, 8, 96)
	}
	if n.typ == "heading" {
		switch int(n.numOr("level", 2)) {
		case 1:
			return 28
		case 3:
			return 18
		case 4, 5, 6:
			return 16
		}
		return 22
	}
	return 14
}

// natH 依內容的高度
func (r *renderer) natH(n *node, w float64) float64 {
	switch n.typ {
	case "row":
		return r.row(n, 0, 0, w, 0, false)
	case "stack":
		return r.stack(n, 0, 0, w, 0, false)
	case "grid":
		return r.grid(n, 0, 0, w, 0, false)
	case "modal":
		return 0
	}
	if container(n) {
		return r.col(n, 0, 0, w, 0, false)
	}
	return r.compH(n, w)
}

// place 把節點畫在 (x, y, w, h)
func (r *renderer) place(n *node, x, y, w, h float64) {
	switch n.typ {
	case "row":
		r.row(n, x, y, w, h, true)
	case "stack":
		r.stack(n, x, y, w, h, true)
	case "grid":
		r.grid(n, x, y, w, h, true)
	case "modal":
	default:
		if container(n) {
			r.col(n, x, y, w, h, true)
		} else {
			r.comp(n, x, y, w, h)
		}
	}
}

// ---- 容器 ----

// head 卡片、區段、疊層與不認得的框上方的標題區高度與字級
func head(n *node) (string, float64, float64) {
	switch {
	case n.typ == "card" || n.typ == "section" || n.typ == "modal":
		t := n.text("title")
		if t == "" && n.typ == "modal" {
			return "", 0, 20 // 留位置給關閉鈕
		}
		if t == "" {
			return "", 0, 0
		}
		fs := map[string]float64{"card": 16, "section": 18, "modal": 18}[n.typ]
		return t, fs, fs*lineMult + 8
	case isUnknown(n):
		l := n.name
		if l == "" {
			l = "未指定 type"
		}
		return l, 12, 12 * lineMult
	}
	return "", 0, 0
}

func defPad(n *node) float64 {
	switch {
	case n.typ == "card":
		return 16
	case n.typ == "modal":
		return 20
	case isUnknown(n):
		return 10
	}
	return 0
}

// box 容器的底色與外框
func (r *renderer) box(n *node, x, y, w, h float64) {
	fill := "none"
	if n.flag("fill") {
		fill = cPanel
	}
	switch {
	case n.typ == "card":
		if fill == "none" {
			fill = cBg
		}
		r.c.rect(x, y, w, h, 8, fill, cLine, false)
	case n.typ == "modal":
		r.c.rect(x, y, w, h, 10, cBg, cLine, false)
		cx, cy := x+w-24, y+24
		r.c.line(cx-6, cy-6, cx+6, cy+6, cDark, 1.5, false)
		r.c.line(cx-6, cy+6, cx+6, cy-6, cDark, 1.5, false)
	case isUnknown(n):
		r.c.rect(x, y, w, h, 4, fill, cLine, true)
	case n.flag("border"):
		r.c.rect(x, y, w, h, 6, fill, cSoft, false)
	case fill != "none":
		r.c.rect(x, y, w, h, 6, fill, "", false)
	}
}

// col 直排(也用在 card、section、modal 與不認得的類型)。draw=false 時只回傳內容高度
func (r *renderer) col(n *node, x, y, w, h float64, draw bool) float64 {
	pad := n.padding(defPad(n))
	gap := n.gap(12)
	title, tfs, top := head(n)
	iw := math.Max(0, w-pad.l-pad.r)
	kids := flow(n)
	align := n.str("align")
	ws := make([]float64, len(kids))
	hs := make([]float64, len(kids))
	fill := make([]bool, len(kids))
	inner := h - pad.t - pad.b - top
	sum := 0.0
	for i, k := range kids {
		kw, _ := r.widthIn(k, iw, 'c')
		if align == "stretch" && intrinsic(k.typ) && k.dim("width").kind == dimAuto {
			kw = iw
		}
		ws[i] = kw
		d := k.dim("height")
		switch {
		case k.typ == "spacer":
			if sz, ok := k.num("size"); ok {
				hs[i] = math.Max(sz, 0)
			} else {
				fill[i] = true
			}
		case d.kind == dimFixed:
			hs[i] = d.v
		case d.kind == dimFill:
			fill[i], hs[i] = true, r.natH(k, kw)
		case d.kind == dimPct && draw:
			hs[i] = math.Max(0, inner) * d.v
		default:
			hs[i] = r.natH(k, kw)
		}
		sum += hs[i]
	}
	gaps := gap * math.Max(0, float64(len(kids)-1))
	content := pad.t + top + sum + gaps + pad.b
	if isUnknown(n) {
		content = math.Max(content, 48)
	}
	if !draw {
		return content
	}
	r.box(n, x, y, w, h)
	if title != "" {
		tc := cText
		if isUnknown(n) {
			tc = cMuted
		}
		tw := w - pad.l - pad.r
		if n.typ == "modal" {
			tw -= 24
		}
		r.c.text(x+pad.l, y+pad.t, truncate(title, math.Max(0, tw), tfs), tfs, tc, !isUnknown(n), "")
	}
	free := inner - sum - gaps
	nf := 0
	for _, b := range fill {
		if b {
			nf++
		}
	}
	extra := 0.0
	cy := y + pad.t + top
	if nf > 0 && free > 0 {
		share := free / float64(nf)
		for i := range hs {
			if fill[i] {
				hs[i] += share
			}
		}
	} else if nf == 0 && free > 0 {
		switch n.str("justify") {
		case "center":
			cy += free / 2
		case "end":
			cy += free
		case "between":
			if len(kids) > 1 {
				extra = free / float64(len(kids)-1)
			}
		}
	}
	for i, k := range kids {
		kx := x + pad.l
		switch align {
		case "center":
			kx += (iw - ws[i]) / 2
		case "end":
			kx += iw - ws[i]
		}
		r.place(k, kx, cy, ws[i], hs[i])
		cy += hs[i] + gap + extra
	}
	return content
}

// rowWidths 橫排子節點的寬度:固定、依內容、剩下的平分給 fill;放不下時壓縮
func (r *renderer) rowWidths(n *node, kids []*node, iw, gap float64) ([]float64, int) {
	ws := make([]float64, len(kids))
	fill := make([]bool, len(kids))
	gaps := gap * math.Max(0, float64(len(kids)-1))
	used, nf := 0.0, 0
	for i, k := range kids {
		w, fl := r.widthIn(k, iw, 'r')
		if fl {
			fill[i] = true
			nf++
			continue
		}
		ws[i] = w
		used += w
	}
	room := iw - gaps
	// 要分剩餘空間的(spacer 除外)至少留一點寬度
	solid := 0
	for i, k := range kids {
		if fill[i] && k.typ != "spacer" {
			solid++
		}
	}
	minFill := 0.0
	if solid > 0 {
		minFill = math.Min(80, math.Max(0, room)/float64(len(kids)))
	}
	reserve := minFill * float64(solid)
	if used+reserve > room+0.5 && used > 0 {
		r.s.warn("%s 的內容比可用寬度寬,已壓縮", n.path)
		scale := math.Max(0, room-reserve) / used
		used = 0
		for i := range ws {
			if !fill[i] {
				ws[i] *= scale
				used += ws[i]
			}
		}
	}
	if nf > 0 {
		share := math.Max(0, room-used) / float64(nf)
		for i := range ws {
			if fill[i] {
				ws[i] = share
			}
		}
	}
	return ws, nf
}

// row 橫排
func (r *renderer) row(n *node, x, y, w, h float64, draw bool) float64 {
	pad := n.padding(0)
	gap := n.gap(12)
	iw := math.Max(0, w-pad.l-pad.r)
	kids := flow(n)
	ws, nf := r.rowWidths(n, kids, iw, gap)
	hs := make([]float64, len(kids))
	maxH := 0.0
	for i, k := range kids {
		if k.typ == "spacer" || k.typ == "divider" {
			continue
		}
		if d := k.dim("height"); d.kind == dimPct || d.kind == dimFill {
			hs[i] = r.natH(k, ws[i])
		} else {
			hs[i] = r.boxH(k, ws[i])
		}
		maxH = math.Max(maxH, hs[i])
	}
	content := pad.t + maxH + pad.b
	if !draw {
		return content
	}
	r.box(n, x, y, w, h)
	ih := math.Max(0, h-pad.t-pad.b)
	align := n.str("align")
	cx := x + pad.l
	extra := 0.0
	if nf == 0 {
		used := gap * math.Max(0, float64(len(kids)-1))
		for _, v := range ws {
			used += v
		}
		if free := iw - used; free > 0 {
			switch n.str("justify") {
			case "center":
				cx += free / 2
			case "end":
				cx += free
			case "between":
				if len(kids) > 1 {
					extra = free / float64(len(kids)-1)
				}
			}
		}
	}
	for i, k := range kids {
		kh := hs[i]
		d := k.dim("height")
		stretch := align == "stretch" || d.kind == dimFill || (align == "" && d.kind == dimAuto && stretchy(k))
		switch {
		case stretch:
			kh = ih
		case d.kind == dimPct:
			kh = ih * d.v
		}
		ky := y + pad.t
		if !stretch {
			switch align {
			case "start", "top":
			case "end", "bottom":
				ky += ih - kh
			default:
				ky += (ih - kh) / 2
			}
		}
		r.place(k, cx, ky, ws[i], kh)
		cx += ws[i] + gap + extra
	}
	return content
}

// stack 疊放:子節點畫在同一塊,用子節點的 place(top-left、center、bottom-right…)或父層的 align 決定位置
func (r *renderer) stack(n *node, x, y, w, h float64, draw bool) float64 {
	pad := n.padding(0)
	iw := math.Max(0, w-pad.l-pad.r)
	ih := math.Max(0, h-pad.t-pad.b)
	kids := flow(n)
	ws := make([]float64, len(kids))
	hs := make([]float64, len(kids))
	maxH := 0.0
	for i, k := range kids {
		ws[i], _ = r.widthIn(k, iw, 'c')
		if k.dim("height").kind != dimFill {
			hs[i] = r.boxH(k, ws[i])
		}
		maxH = math.Max(maxH, hs[i])
	}
	content := pad.t + maxH + pad.b
	if !draw {
		return content
	}
	r.box(n, x, y, w, h)
	for i, k := range kids {
		kh := hs[i]
		if d := k.dim("height"); d.kind == dimFill {
			kh = ih
		} else if d.kind == dimPct {
			kh = ih * d.v
		}
		pos := k.str("place")
		if pos == "" {
			pos = n.str("align")
		}
		kx, ky := x+pad.l, y+pad.t
		switch {
		case strings.Contains(pos, "right") || pos == "end":
			kx += iw - ws[i]
		case strings.Contains(pos, "left") || pos == "start" || pos == "":
		case strings.Contains(pos, "center") || pos == "top" || pos == "bottom":
			kx += (iw - ws[i]) / 2
		}
		switch {
		case strings.Contains(pos, "bottom") || pos == "end":
			ky += ih - kh
		case strings.Contains(pos, "top") || pos == "start" || pos == "":
		case strings.Contains(pos, "center") || pos == "left" || pos == "right":
			ky += (ih - kh) / 2
		}
		r.place(k, kx, ky, ws[i], kh)
	}
	return content
}

// grid 格狀:columns 欄,子節點可用 span 跨欄;每一列的高度是該列最高的
func (r *renderer) grid(n *node, x, y, w, h float64, draw bool) float64 {
	pad := n.padding(0)
	gap := n.gap(16)
	cols := int(clamp(n.numOr("columns", n.numOr("cols", 2)), 1, 12))
	iw := math.Max(0, w-pad.l-pad.r)
	cw := math.Max(0, (iw-gap*float64(cols-1))/float64(cols))
	type cell struct {
		k     *node
		col   int
		w, h  float64
		rowIx int
	}
	cells := []cell{}
	rowH := []float64{}
	c, ri := 0, 0
	for _, k := range flow(n) {
		span := int(clamp(k.numOr("span", 1), 1, float64(cols)))
		if c+span > cols {
			c, ri = 0, ri+1
		}
		if ri >= len(rowH) {
			rowH = append(rowH, 0)
		}
		kw := cw*float64(span) + gap*float64(span-1)
		ew, _ := r.widthIn(k, kw, 'c')
		kh := r.boxH(k, ew)
		rowH[ri] = math.Max(rowH[ri], kh)
		cells = append(cells, cell{k, c, ew, kh, ri})
		c += span
		if c >= cols {
			c, ri = 0, ri+1
		}
	}
	sum := 0.0
	for _, v := range rowH {
		sum += v
	}
	content := pad.t + sum + gap*math.Max(0, float64(len(rowH)-1)) + pad.b
	if !draw {
		return content
	}
	r.box(n, x, y, w, h)
	rowY := make([]float64, len(rowH))
	cy := y + pad.t
	for i, v := range rowH {
		rowY[i] = cy
		cy += v + gap
	}
	for _, e := range cells {
		kh := e.h
		if stretchy(e.k) && e.k.dim("height").kind == dimAuto {
			kh = rowH[e.rowIx]
		}
		r.place(e.k, x+pad.l+float64(e.col)*(cw+gap), rowY[e.rowIx], e.w, kh)
	}
	return content
}
