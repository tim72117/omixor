package flowchart

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 固定的配色(灰階為主,決策節點與 highlight 用同一種藍色強調)與字型;描述裡的字串一律只進到 <text> 內容,不會進到屬性
const (
	cBg         = "#ffffff"
	cNodeFill   = "#ffffff"
	cNodeLine   = "#6b7280"
	cText       = "#1f2937"
	cTermFill   = "#f3f4f6" // 開始/結束
	cNoteFill   = "#f9fafb"
	cEdge       = "#6b7280"
	cEdgeText   = "#4b5563"
	cGroupLine  = "#9ca3af"
	cGroupFill  = "#f9fafb"
	cGroupText  = "#6b7280"
	cAccent     = "#2563eb" // 強調色(決策、highlight)
	cAccentFill = "#eff6ff"
	cAccentHi   = "#dbeafe"
	cAccentText = "#1e3a8a"
	cMutedLine  = "#d1d5db"
	cMutedFill  = "#fafafa"
	cMutedText  = "#9ca3af"
	fontCSS     = `text{font-family:system-ui,-apple-system,'Segoe UI','Noto Sans TC','PingFang TC','Microsoft JhengHei',sans-serif}`
	lineMult    = 1.4 // 行高 = 字級 × 1.4
)

// ---- 文字寬度(估算:CJK 約 1em、ASCII 約 0.55em)----

func wide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE4F, r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6, r >= 0x1F300 && r <= 0x1FAFF, r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return unicode.Is(unicode.Han, r)
}

func runeW(r rune) float64 {
	switch {
	case r == ' ':
		return 0.3
	case r < 0x80:
		return 0.55
	case wide(r):
		return 1
	}
	return 0.6
}

// textW 單行文字的估計寬度(px)
func textW(s string, size float64) float64 {
	w := 0.0
	for _, r := range s {
		w += runeW(r)
	}
	return w * size
}

// wrap 依寬度斷行:英文盡量在空白處斷,中文逐字斷;最多 maxLines 行(多的截掉加 …)
func wrap(s string, maxW, size float64, maxLines int) []string {
	out := []string{}
	if maxW < size {
		maxW = size
	}
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		line, w := []rune{}, 0.0
		for _, r := range para {
			cw := runeW(r) * size
			if w+cw > maxW+0.01 && len(line) > 0 {
				k := -1
				if r != ' ' && r < 0x80 { // 英文字中間:退回最後一個空白斷行
					for j := len(line) - 1; j > 0; j-- {
						if line[j] == ' ' {
							k = j
							break
						}
					}
				}
				if k > 0 {
					out = append(out, strings.TrimRight(string(line[:k]), " "))
					line = append([]rune{}, line[k+1:]...)
				} else {
					out = append(out, strings.TrimRight(string(line), " "))
					line = line[:0]
				}
				w = 0
				for _, x := range line {
					w += runeW(x) * size
				}
				if r == ' ' && len(line) == 0 {
					continue
				}
			}
			line = append(line, r)
			w += cw
		}
		out = append(out, string(line))
	}
	if maxLines > 0 && len(out) > maxLines {
		out = out[:maxLines]
		out[maxLines-1] = truncate(out[maxLines-1]+"…", maxW, size)
	}
	return out
}

// truncate 超過寬度時截斷並加 …
func truncate(s string, maxW, size float64) string {
	if textW(s, size) <= maxW+0.01 {
		return s
	}
	ell := runeW('…') * size
	w := 0.0
	var b strings.Builder
	for _, r := range s {
		cw := runeW(r) * size
		if w+cw+ell > maxW {
			break
		}
		b.WriteRune(r)
		w += cw
	}
	return strings.TrimRight(b.String(), " ") + "…"
}

// linesW 多行文字中最寬的一行
func linesW(lines []string, size float64) float64 {
	w := 0.0
	for _, l := range lines {
		w = math.Max(w, textW(l, size))
	}
	return w
}

// ---- XML ----

// esc XML escape;拿掉 XML 不允許的控制字元與無效的 UTF-8
func esc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			continue
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&#39;")
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7F || (r >= 0x80 && r < 0xA0) || (r >= 0xFFFE && r <= 0xFFFF) || (r >= 0xD800 && r <= 0xDFFF):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// clean 去掉控制字元(換行保留),供估算寬度與斷行
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7F || (r >= 0x80 && r < 0xA0) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
}

func f(x float64) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "0"
	}
	return strconv.FormatFloat(math.Round(x*10)/10, 'f', -1, 64)
}

// canvas 畫圖的指令(只會輸出固定的幾種元素;顏色都是本套件的常數)
type canvas struct{ b strings.Builder }

func (c *canvas) rect(x, y, w, h, rx float64, fill, stroke string, width float64, dashed bool) {
	if w <= 0 || h <= 0 {
		return
	}
	c.b.WriteString(`<rect x="` + f(x) + `" y="` + f(y) + `" width="` + f(w) + `" height="` + f(h) + `"`)
	if rx > 0 {
		c.b.WriteString(` rx="` + f(rx) + `"`)
	}
	c.b.WriteString(` fill="` + fill + `"`)
	c.stroke(stroke, width, dashed)
	c.b.WriteString("/>")
}

func (c *canvas) stroke(stroke string, width float64, dashed bool) {
	if stroke == "" {
		return
	}
	c.b.WriteString(` stroke="` + stroke + `"`)
	if width != 1 {
		c.b.WriteString(` stroke-width="` + f(width) + `"`)
	}
	if dashed {
		c.b.WriteString(` stroke-dasharray="5 4"`)
	}
}

func (c *canvas) polygon(pts []float64, fill, stroke string, width float64, dashed bool) {
	c.b.WriteString(`<polygon points="` + ptsStr(pts) + `" fill="` + fill + `"`)
	c.stroke(stroke, width, dashed)
	c.b.WriteString(` stroke-linejoin="round"/>`)
}

func (c *canvas) polyline(pts []float64, stroke string, width float64) {
	c.b.WriteString(`<polyline points="` + ptsStr(pts) + `" fill="none"`)
	c.stroke(stroke, width, false)
	c.b.WriteString("/>")
}

func (c *canvas) line(x1, y1, x2, y2 float64, stroke string, width float64) {
	c.b.WriteString(`<line x1="` + f(x1) + `" y1="` + f(y1) + `" x2="` + f(x2) + `" y2="` + f(y2) + `"`)
	c.stroke(stroke, width, false)
	c.b.WriteString("/>")
}

func (c *canvas) ellipse(cx, cy, rx, ry float64, fill, stroke string, width float64, dashed bool) {
	c.b.WriteString(`<ellipse cx="` + f(cx) + `" cy="` + f(cy) + `" rx="` + f(rx) + `" ry="` + f(ry) + `" fill="` + fill + `"`)
	c.stroke(stroke, width, dashed)
	c.b.WriteString("/>")
}

// path d 只由這個套件用數字與固定的指令字母組出來
func (c *canvas) path(d, fill, stroke string, width float64, dashed bool, extra string) {
	c.b.WriteString(`<path d="` + d + `" fill="` + fill + `"`)
	c.stroke(stroke, width, dashed)
	c.b.WriteString(extra + "/>")
}

func ptsStr(pts []float64) string {
	var s strings.Builder
	for i := 0; i+1 < len(pts); i += 2 {
		if i > 0 {
			s.WriteByte(' ')
		}
		s.WriteString(f(pts[i]) + "," + f(pts[i+1]))
	}
	return s.String()
}

// text 一行文字,(x, y) 是這一行的中心
func (c *canvas) text(x, y float64, s string, size float64, fill string, bold bool, anchor string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	c.b.WriteString(`<text x="` + f(x) + `" y="` + f(y+size*0.35) + `" font-size="` + f(size) + `" fill="` + fill + `"`)
	if bold {
		c.b.WriteString(` font-weight="600"`)
	}
	if anchor != "" && anchor != "start" {
		c.b.WriteString(` text-anchor="` + anchor + `"`)
	}
	c.b.WriteString(">" + esc(s) + "</text>")
}

// lines 多行文字,整塊以 (cx, cy) 為中心
func (c *canvas) lines(cx, cy float64, ls []string, size float64, fill string, bold bool) {
	lh := size * lineMult
	top := cy - lh*float64(len(ls))/2
	for i, l := range ls {
		c.text(cx, top+lh*(float64(i)+0.5), l, size, fill, bold, "middle")
	}
}
