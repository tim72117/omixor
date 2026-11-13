package wireframe

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 固定的配色(灰階)與字型;描述裡的字串一律不會進到屬性
const (
	cBg      = "#ffffff"
	cLine    = "#9ca3af" // 一般框線
	cSoft    = "#d1d5db" // 淺框線、分隔線
	cFill    = "#f3f4f6" // 淺灰底
	cFill2   = "#e5e7eb" // 中灰底(佔位、選中)
	cPanel   = "#f9fafb" // 側欄、標頭底色
	cText    = "#374151" // 文字
	cMuted   = "#9ca3af" // 次要文字
	cDark    = "#4b5563" // 主要按鈕、選中
	cOnDark  = "#ffffff"
	fontCSS  = `text{font-family:system-ui,-apple-system,'Segoe UI','Noto Sans TC','PingFang TC','Microsoft JhengHei',sans-serif}`
	lineMult = 1.45 // 行高 = 字級 × 1.45
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
		case r < 0x20 || r == 0x7F || (r >= 0xFFFE && r <= 0xFFFF) || (r >= 0xD800 && r <= 0xDFFF):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func f(x float64) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "0"
	}
	return strconv.FormatFloat(math.Round(x*10)/10, 'f', -1, 64)
}

// canvas 畫圖的指令(只會輸出固定的幾種元素)
type canvas struct{ b strings.Builder }

func (c *canvas) rect(x, y, w, h, rx float64, fill, stroke string, dashed bool) {
	if w <= 0 || h <= 0 {
		return
	}
	c.b.WriteString(`<rect x="` + f(x) + `" y="` + f(y) + `" width="` + f(w) + `" height="` + f(h) + `"`)
	if rx > 0 {
		c.b.WriteString(` rx="` + f(rx) + `"`)
	}
	c.b.WriteString(` fill="` + fill + `"`)
	if stroke != "" {
		c.b.WriteString(` stroke="` + stroke + `"`)
		if dashed {
			c.b.WriteString(` stroke-dasharray="6 4"`)
		}
	}
	c.b.WriteString("/>")
}

func (c *canvas) line(x1, y1, x2, y2 float64, stroke string, width float64, dashed bool) {
	c.b.WriteString(`<line x1="` + f(x1) + `" y1="` + f(y1) + `" x2="` + f(x2) + `" y2="` + f(y2) + `" stroke="` + stroke + `"`)
	if width != 1 {
		c.b.WriteString(` stroke-width="` + f(width) + `"`)
	}
	if dashed {
		c.b.WriteString(` stroke-dasharray="6 4"`)
	}
	c.b.WriteString("/>")
}

func (c *canvas) circle(cx, cy, r float64, fill, stroke string) {
	c.b.WriteString(`<circle cx="` + f(cx) + `" cy="` + f(cy) + `" r="` + f(r) + `" fill="` + fill + `"`)
	if stroke != "" {
		c.b.WriteString(` stroke="` + stroke + `"`)
	}
	c.b.WriteString("/>")
}

// path d 只由這個套件用數字組出來
func (c *canvas) path(d, fill, stroke string, width float64) {
	c.b.WriteString(`<path d="` + d + `" fill="` + fill + `"`)
	if stroke != "" {
		c.b.WriteString(` stroke="` + stroke + `" stroke-width="` + f(width) + `" stroke-linecap="round" stroke-linejoin="round"`)
	}
	c.b.WriteString("/>")
}

func (c *canvas) polyline(pts []float64, stroke string, width float64) {
	var s strings.Builder
	for i := 0; i+1 < len(pts); i += 2 {
		if i > 0 {
			s.WriteByte(' ')
		}
		s.WriteString(f(pts[i]) + "," + f(pts[i+1]))
	}
	c.b.WriteString(`<polyline points="` + s.String() + `" fill="none" stroke="` + stroke + `" stroke-width="` + f(width) + `" stroke-linejoin="round"/>`)
}

// text 一行文字:y 是這一行的頂端,anchor 是 start | middle | end
func (c *canvas) text(x, y float64, s string, size float64, fill string, bold bool, anchor string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	base := y + size*lineMult/2 + size*0.35
	c.b.WriteString(`<text x="` + f(x) + `" y="` + f(base) + `" font-size="` + f(size) + `" fill="` + fill + `"`)
	if bold {
		c.b.WriteString(` font-weight="600"`)
	}
	if anchor != "" && anchor != "start" {
		c.b.WriteString(` text-anchor="` + anchor + `"`)
	}
	c.b.WriteString(">" + esc(s) + "</text>")
}

// underline 文字底線(連結)
func (c *canvas) underline(x, y, w, size float64, stroke string) {
	base := y + size*lineMult/2 + size*0.35
	c.line(x, base+2, x+w, base+2, stroke, 1, false)
}
