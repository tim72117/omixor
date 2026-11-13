package wireframe

import (
	"math"
	"strings"
)

// 元件:高度(compH)與畫法(comp)。尺寸都是固定的預設值,描述可以用 width / height 改。

func inputLabelH(n *node) float64 {
	if n.text("label") != "" {
		return 13*lineMult + 4
	}
	return 0
}

func listCount(n *node) (items []string, count int) {
	items = n.strs("items")
	if len(items) > 0 {
		return items, len(items)
	}
	return nil, int(clamp(n.numOr("count", 3), 1, 200))
}

func listRowH(n *node) float64 {
	if n.flag("avatar") {
		return 56
	}
	if n.flag("dense") {
		return 36
	}
	return 48
}

func tableShape(n *node) (cols []string, ncol int, data [][]string, nrow int) {
	cols = n.strs("columns")
	ncol = len(cols)
	if ncol == 0 {
		ncol = int(clamp(n.numOr("cols", 4), 1, 20))
	}
	if l, ok := n.p["rows"].([]any); ok {
		for _, row := range l {
			cells := []string{}
			if rl, ok := row.([]any); ok {
				for _, c := range rl {
					cells = append(cells, strings.TrimSpace(scalar(c)))
				}
			} else {
				cells = append(cells, strings.TrimSpace(scalar(row)))
			}
			data = append(data, cells)
			if len(data) >= 200 {
				break
			}
		}
		return cols, ncol, data, len(data)
	}
	return cols, ncol, nil, int(clamp(n.numOr("rows", 5), 0, 200))
}

func sidebarItems(n *node) []string { return n.strs("items") }

// compH 元件依內容的高度
func (r *renderer) compH(n *node, w float64) float64 {
	switch n.typ {
	case "header", "navbar":
		return 56
	case "footer":
		return 48
	case "sidebar":
		top := 16.0
		if n.text("title") != "" {
			top = 52
		}
		k := len(sidebarItems(n))
		if k == 0 {
			k = 5
		}
		return top + float64(k)*40 + 16
	case "text", "heading", "link":
		fs := textSize(n)
		t := n.text("text", "label")
		if t == "" {
			if n.typ == "text" {
				return clamp(n.numOr("lines", 2), 1, 50) * fs * lineMult
			}
			t = "標題"
		}
		return float64(len(wrap(t, w, fs, 200))) * fs * lineMult
	case "button":
		_, h := btnFont(n)
		return h
	case "input", "select":
		return inputLabelH(n) + 40
	case "textarea":
		return inputLabelH(n) + clamp(n.numOr("rows", 4), 1, 40)*20 + 20
	case "checkbox", "radio", "toggle":
		return 24
	case "image":
		if ratio, ok := n.num("ratio"); ok && ratio > 0.05 {
			return w / ratio
		}
		return clamp(w*0.5625, 60, 240)
	case "avatar":
		return clamp(n.numOr("size", 40), 8, 400)
	case "icon":
		return clamp(n.numOr("size", 24), 8, 400)
	case "list":
		_, c := listCount(n)
		return float64(c) * listRowH(n)
	case "table":
		_, _, _, rows := tableShape(n)
		return 40 + float64(rows)*36
	case "tabs":
		return 40
	case "chart":
		if n.text("title") != "" {
			return 232
		}
		return 200
	case "badge":
		return 22
	case "divider":
		return 1
	case "spacer":
		return math.Max(0, n.numOr("size", 0))
	}
	return 48
}

// comp 畫元件
func (r *renderer) comp(n *node, x, y, w, h float64) {
	c := &r.c
	switch n.typ {
	case "header", "navbar":
		r.header(n, x, y, w, h)
	case "footer":
		c.rect(x, y, w, h, 0, cPanel, "", false)
		c.line(x, y, x+w, y, cSoft, 1, false)
		t := n.text("text", "title")
		if t == "" {
			t = "頁尾"
		}
		c.text(x+w/2, y+(h-13*lineMult)/2, truncate(t, w-32, 13), 13, cMuted, false, "middle")
	case "sidebar":
		r.sidebar(n, x, y, w, h)
	case "text", "heading", "link":
		r.textBlock(n, x, y, w, h)
	case "button":
		r.button(n, x, y, w, h)
	case "input", "select", "textarea":
		r.input(n, x, y, w, h)
	case "checkbox", "radio", "toggle":
		r.check(n, x, y, w, h)
	case "image":
		c.rect(x, y, w, h, 4, cFill, cLine, false)
		c.line(x, y, x+w, y+h, cSoft, 1, false)
		c.line(x, y+h, x+w, y, cSoft, 1, false)
		if l := n.text("label", "alt", "text"); l != "" {
			c.text(x+w/2, y+h/2-13*lineMult/2, truncate(l, w-16, 13), 13, cMuted, false, "middle")
		}
	case "avatar":
		s := math.Min(clamp(n.numOr("size", 40), 8, 400), math.Min(w, h))
		r.avatar(x+s/2, y+h/2, s)
		if l := n.text("label", "text", "name"); l != "" {
			c.text(x+s+8, y+h/2-14*lineMult/2, truncate(l, w-s-8, 14), 14, cText, false, "")
		}
	case "icon":
		s := math.Min(clamp(n.numOr("size", 24), 8, 400), math.Min(w, h))
		ix, iy := x+(w-s)/2, y+(h-s)/2
		c.rect(ix, iy, s, s, s*0.25, "none", cLine, false)
		c.circle(ix+s/2, iy+s/2, s*0.18, cLine, "")
	case "list":
		r.list(n, x, y, w, h)
	case "table":
		r.table(n, x, y, w, h)
	case "tabs":
		r.tabs(n, x, y, w, h)
	case "chart":
		r.chart(n, x, y, w, h)
	case "badge":
		t := n.text("text", "label")
		if t == "" {
			t = "標籤"
		}
		c.rect(x, y, w, h, h/2, cFill2, "", false)
		c.text(x+w/2, y+(h-12*lineMult)/2, truncate(t, w-8, 12), 12, cText, false, "middle")
	case "divider":
		if w <= 2 && h > 2 {
			c.line(x+w/2, y, x+w/2, y+h, cSoft, 1, false)
		} else {
			c.line(x, y+h/2, x+w, y+h/2, cSoft, 1, false)
		}
	case "spacer":
	}
}

func (r *renderer) header(n *node, x, y, w, h float64) {
	c := &r.c
	c.rect(x, y, w, h, 0, cPanel, "", false)
	c.line(x, y+h, x+w, y+h, cSoft, 1, false)
	mid := y + h/2
	cx := x + 16
	if n.flag("back") {
		c.path("M"+f(cx+8)+" "+f(mid-7)+"L"+f(cx+1)+" "+f(mid)+"L"+f(cx+8)+" "+f(mid+7), "none", cText, 2)
		cx += 24
	}
	if n.flag("menu") {
		r.hamburger(cx, mid)
		cx += 32
	}
	if n.flag("logo") {
		c.rect(cx, mid-14, 28, 28, 6, cFill2, cLine, false)
		cx += 38
	}
	right := x + w - 16
	if n.flag("avatar") {
		r.avatar(right-16, mid, 32)
		right -= 44
	}
	title := n.text("title", "text")
	tfs := 17.0
	tw := math.Min(textW(title, tfs), math.Max(0, right-cx))
	items := n.strs("items")
	if len(items) > 0 {
		total := 0.0
		for i, it := range items {
			if i > 0 {
				total += 24
			}
			total += textW(it, 14)
		}
		if cx+tw+24+total <= right {
			ix := right - total
			for _, it := range items {
				c.text(ix, mid-14*lineMult/2, it, 14, cText, false, "")
				ix += textW(it, 14) + 24
			}
		} else { // 放不下:收成漢堡選單
			r.hamburger(right-20, mid)
			right -= 32
		}
	}
	if title != "" {
		c.text(cx, mid-tfs*lineMult/2, truncate(title, math.Max(0, right-cx), tfs), tfs, cText, true, "")
	}
}

func (r *renderer) hamburger(x, mid float64) {
	for i := -1; i <= 1; i++ {
		r.c.line(x, mid+float64(i)*6, x+20, mid+float64(i)*6, cText, 2, false)
	}
}

func (r *renderer) avatar(cx, cy, s float64) {
	c := &r.c
	c.circle(cx, cy, s/2, cFill2, cLine)
	c.circle(cx, cy-s*0.1, s*0.17, cLine, "")
	c.path("M"+f(cx-s*0.28)+" "+f(cy+s*0.33)+"Q"+f(cx)+" "+f(cy)+" "+f(cx+s*0.28)+" "+f(cy+s*0.33), "none", cLine, math.Max(1, s*0.08))
}

func (r *renderer) sidebar(n *node, x, y, w, h float64) {
	c := &r.c
	c.rect(x, y, w, h, 0, cPanel, "", false)
	c.line(x+w, y, x+w, y+h, cSoft, 1, false)
	cy := y + 16
	if t := n.text("title"); t != "" {
		c.text(x+16, cy, truncate(t, w-32, 15), 15, cText, true, "")
		cy += 36
	}
	items := sidebarItems(n)
	active := int(n.numOr("active", 0))
	k := len(items)
	if k == 0 {
		k = 5
	}
	for i := 0; i < k; i++ {
		if cy+36 > y+h+0.5 {
			break
		}
		if i == active {
			c.rect(x+8, cy, w-16, 36, 6, cFill2, "", false)
		}
		c.rect(x+20, cy+10, 16, 16, 4, "none", cLine, false)
		if len(items) > 0 {
			c.text(x+44, cy+18-14*lineMult/2, truncate(items[i], w-60, 14), 14, cText, i == active, "")
		} else {
			c.rect(x+44, cy+14, math.Max(0, math.Min(w-60, []float64{90, 70, 110, 60, 80}[i%5])), 8, 3, cFill2, "", false)
		}
		cy += 40
	}
}

func (r *renderer) textBlock(n *node, x, y, w, h float64) {
	c := &r.c
	fs := textSize(n)
	lh := fs * lineMult
	t := n.text("text", "label")
	color := cText
	if n.flag("muted") {
		color = cMuted
	}
	if n.typ == "link" {
		color = cDark
	}
	bold := n.typ == "heading" || n.flag("bold")
	if t == "" && n.typ == "text" { // 佔位線
		lines := int(clamp(n.numOr("lines", 2), 1, 50))
		for i := 0; i < lines; i++ {
			bw := w * []float64{0.95, 0.8, 0.88, 0.7}[i%4]
			if i == lines-1 && lines > 1 {
				bw = w * 0.6
			}
			c.rect(x, y+float64(i)*lh+(lh-fs*0.6)/2, bw, fs*0.6, 3, cFill2, "", false)
		}
		return
	}
	if t == "" {
		t = "標題"
	}
	anchor, ax := "", x
	switch n.str("align") {
	case "center":
		anchor, ax = "middle", x+w/2
	case "right", "end":
		anchor, ax = "end", x+w
	}
	for i, l := range wrap(t, w, fs, 200) {
		ly := y + float64(i)*lh
		c.text(ax, ly, l, fs, color, bold, anchor)
		if n.typ == "link" {
			lw := textW(l, fs)
			lx := ax
			if anchor == "middle" {
				lx -= lw / 2
			} else if anchor == "end" {
				lx -= lw
			}
			c.underline(lx, ly, lw, fs, cDark)
		}
	}
}

func (r *renderer) button(n *node, x, y, w, h float64) {
	c := &r.c
	fs, _ := btnFont(n)
	t := n.text("text", "label")
	if t == "" {
		t = "按鈕"
	}
	tc := cText
	switch n.str("variant") {
	case "", "primary", "danger", "cta":
		c.rect(x, y, w, h, 6, cDark, "", false)
		tc = cOnDark
	case "ghost", "link", "text", "plain":
		tc = cDark
	default:
		c.rect(x, y, w, h, 6, cBg, cLine, false)
	}
	c.text(x+w/2, y+(h-fs*lineMult)/2, truncate(t, w-16, fs), fs, tc, true, "middle")
}

func (r *renderer) input(n *node, x, y, w, h float64) {
	c := &r.c
	lh := inputLabelH(n)
	if l := n.text("label"); l != "" {
		c.text(x, y, truncate(l, w, 13), 13, cText, false, "")
	}
	by, bh := y+lh, h-lh
	c.rect(x, by, w, bh, 6, cBg, cLine, false)
	right := 12.0
	if n.typ == "select" {
		cx, cy := x+w-19, by+math.Min(bh, 40)/2
		c.path("M"+f(cx-5)+" "+f(cy-3)+"L"+f(cx)+" "+f(cy+2)+"L"+f(cx+5)+" "+f(cy-3), "none", cDark, 1.5)
		right = 32
	}
	t, tc := n.text("value"), cText
	if t != "" && (n.str("kind") == "password" || n.str("type") == "password") {
		t = strings.Repeat("•", int(math.Min(12, float64(len([]rune(t))))))
	}
	if t == "" {
		t, tc = n.text("placeholder"), cMuted
	}
	if t == "" {
		return
	}
	if n.typ == "textarea" {
		for i, l := range wrap(t, w-24, 14, int(math.Max(1, (bh-16)/20))) {
			c.text(x+12, by+8+float64(i)*20, l, 14, tc, false, "")
		}
		return
	}
	c.text(x+12, by+(math.Min(bh, 40)-14*lineMult)/2, truncate(t, w-12-right, 14), 14, tc, false, "")
}

func (r *renderer) check(n *node, x, y, w, h float64) {
	c := &r.c
	mid := y + h/2
	lx := x + 24.0
	switch n.typ {
	case "checkbox":
		on := n.flag("checked") || n.flag("on")
		if on {
			c.rect(x, mid-8, 16, 16, 3, cDark, "", false)
			c.path("M"+f(x+4)+" "+f(mid)+"L"+f(x+7)+" "+f(mid+3)+"L"+f(x+12)+" "+f(mid-3), "none", cOnDark, 2)
		} else {
			c.rect(x, mid-8, 16, 16, 3, cBg, cLine, false)
		}
	case "radio":
		c.circle(x+8, mid, 8, cBg, cLine)
		if n.flag("checked") || n.flag("on") {
			c.circle(x+8, mid, 4, cDark, "")
		}
	case "toggle":
		on := n.flag("on") || n.flag("checked")
		if on {
			c.rect(x, mid-10, 36, 20, 10, cDark, "", false)
			c.circle(x+26, mid, 8, cOnDark, "")
		} else {
			c.rect(x, mid-10, 36, 20, 10, cFill2, cLine, false)
			c.circle(x+10, mid, 8, cBg, cLine)
		}
		lx = x + 44
	}
	if l := n.text("label", "text"); l != "" {
		c.text(lx, mid-14*lineMult/2, truncate(l, math.Max(0, x+w-lx), 14), 14, cText, false, "")
	}
}

func (r *renderer) list(n *node, x, y, w, h float64) {
	c := &r.c
	items, count := listCount(n)
	rh := listRowH(n)
	c.rect(x, y, w, h, 6, "none", cSoft, false)
	for i := 0; i < count; i++ {
		ry := y + float64(i)*rh
		if ry+rh > y+h+0.5 {
			break
		}
		if i > 0 {
			c.line(x+12, ry, x+w-12, ry, cSoft, 1, false)
		}
		tx := x + 12
		if n.flag("avatar") {
			r.avatar(tx+16, ry+rh/2, 32)
			tx += 44
		} else if n.flag("icon") {
			c.rect(tx, ry+rh/2-10, 20, 20, 5, "none", cLine, false)
			tx += 32
		}
		right := x + w - 12
		if n.flag("chevron") {
			cx, cy := right-4, ry+rh/2
			c.path("M"+f(cx-4)+" "+f(cy-5)+"L"+f(cx+1)+" "+f(cy)+"L"+f(cx-4)+" "+f(cy+5), "none", cLine, 1.5)
			right -= 20
		}
		if items != nil {
			c.text(tx, ry+(rh-14*lineMult)/2, truncate(items[i], math.Max(0, right-tx), 14), 14, cText, false, "")
		} else {
			bw := math.Max(0, right-tx) * []float64{0.55, 0.4, 0.65, 0.5}[i%4]
			c.rect(tx, ry+rh/2-5, bw, 10, 3, cFill2, "", false)
		}
	}
}

var barPattern = []float64{0.7, 0.45, 0.85, 0.55, 0.35, 0.75, 0.6, 0.5}

func (r *renderer) table(n *node, x, y, w, h float64) {
	c := &r.c
	cols, ncol, data, nrow := tableShape(n)
	c.rect(x, y, w, h, 4, cBg, cLine, false)
	c.rect(x+0.5, y+0.5, w-1, 39.5, 4, cFill, "", false)
	c.line(x, y+40, x+w, y+40, cLine, 1, false)
	cw := w / float64(ncol)
	for j := 0; j < ncol; j++ {
		cx := x + float64(j)*cw + 12
		if j < len(cols) && cols[j] != "" {
			c.text(cx, y+20-13*lineMult/2, truncate(cols[j], cw-20, 13), 13, cText, true, "")
		} else {
			c.rect(cx, y+16, math.Max(0, (cw-24)*0.5), 8, 3, cSoft, "", false)
		}
	}
	for i := 0; i < nrow; i++ {
		ry := y + 40 + float64(i)*36
		if ry+36 > y+h+0.5 {
			break
		}
		if i > 0 {
			c.line(x, ry, x+w, ry, cSoft, 1, false)
		}
		for j := 0; j < ncol; j++ {
			cx := x + float64(j)*cw + 12
			if data != nil {
				if j < len(data[i]) {
					c.text(cx, ry+18-13*lineMult/2, truncate(data[i][j], cw-20, 13), 13, cText, false, "")
				}
				continue
			}
			c.rect(cx, ry+14, math.Max(0, (cw-24)*barPattern[(i*3+j)%len(barPattern)]), 8, 3, cFill2, "", false)
		}
	}
}

func (r *renderer) tabs(n *node, x, y, w, h float64) {
	c := &r.c
	items := n.strs("items")
	if len(items) == 0 {
		items = []string{"分頁 1", "分頁 2", "分頁 3"}
	}
	active := int(n.numOr("active", 0))
	c.line(x, y+h-0.5, x+w, y+h-0.5, cSoft, 1, false)
	tx := x
	for i, it := range items {
		tw := textW(it, 14) + 32
		if tx+tw > x+w+0.5 {
			break
		}
		tc := cMuted
		if i == active {
			tc = cText
			c.rect(tx, y+h-2, tw, 2, 0, cDark, "", false)
		}
		c.text(tx+tw/2, y+(h-14*lineMult)/2, it, 14, tc, i == active, "middle")
		tx += tw
	}
}

func (r *renderer) chart(n *node, x, y, w, h float64) {
	c := &r.c
	c.rect(x, y, w, h, 6, cBg, cSoft, false)
	top := y + 16
	if t := n.text("title"); t != "" {
		c.text(x+16, y+12, truncate(t, w-32, 14), 14, cText, true, "")
		top = y + 44
	}
	px, py, pw, ph := x+40, top, w-56, y+h-28-top
	if pw <= 10 || ph <= 10 {
		return
	}
	kind := n.str("kind")
	if kind == "" {
		kind = n.str("variant")
	}
	if kind == "pie" || kind == "donut" {
		cx, cy := x+w/2, top+(y+h-16-top)/2
		rad := math.Max(4, math.Min(w-32, y+h-16-top)/2)
		c.circle(cx, cy, rad, cFill2, cLine)
		start := -math.Pi / 2
		for i, frac := range []float64{0.42, 0.26} {
			end := start + frac*2*math.Pi
			large := "0"
			if frac > 0.5 {
				large = "1"
			}
			d := "M" + f(cx) + " " + f(cy) + "L" + f(cx+rad*math.Cos(start)) + " " + f(cy+rad*math.Sin(start)) +
				"A" + f(rad) + " " + f(rad) + " 0 " + large + " 1 " + f(cx+rad*math.Cos(end)) + " " + f(cy+rad*math.Sin(end)) + "Z"
			c.path(d, []string{cLine, cSoft}[i], cBg, 1.5)
			start = end
		}
		if kind == "donut" {
			c.circle(cx, cy, rad*0.55, cBg, "")
		}
		return
	}
	c.line(px, py, px, py+ph, cLine, 1, false)
	c.line(px, py+ph, px+pw, py+ph, cLine, 1, false)
	for i := 1; i <= 3; i++ {
		gy := py + ph*float64(i)/4
		c.line(px, gy, px+pw, gy, cFill2, 1, true)
	}
	k := len(barPattern)
	step := pw / float64(k)
	if kind == "line" || kind == "area" {
		pts := []float64{}
		for i, v := range barPattern {
			pts = append(pts, px+step*(float64(i)+0.5), py+ph*(1-v))
		}
		c.polyline(pts, cDark, 2)
		for i := 0; i+1 < len(pts); i += 2 {
			c.circle(pts[i], pts[i+1], 3, cBg, cDark)
		}
		return
	}
	for i, v := range barPattern {
		bw := step * 0.6
		c.rect(px+step*float64(i)+(step-bw)/2, py+ph*(1-v), bw, ph*v, 2, cFill2, cLine, false)
	}
}
