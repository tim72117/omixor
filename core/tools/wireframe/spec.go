package wireframe

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 描述的上限
const (
	MaxBytes     = 512 << 10 // 描述最大 512KB
	MaxDepth     = 30        // 巢狀最多 30 層
	MaxNodes     = 2000      // 節點最多 2000 個
	MaxCanvas    = 20000     // 畫布最高(超過截掉並提醒)
	maxWarnings  = 50
	maxDimension = 10000
)

// Error 描述格式錯誤(不是 JSON、缺 root、太深、太多節點…)
type Error struct{ Msg string }

func (e *Error) Error() string { return "線框圖描述錯誤:" + e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// node 解析後的一個節點
type node struct {
	typ  string
	p    map[string]any
	kids []*node
	path string
	name string // 不認得的 type 原本的名稱(虛線框的標籤)
}

// 別名:常見的其他叫法對到標準 type
var aliases = map[string]string{
	"column": "col", "vstack": "col", "vbox": "col", "box": "col", "container": "col",
	"hstack": "row", "hbox": "row", "zstack": "stack", "overlay": "stack",
	"nav": "navbar", "topbar": "header", "appbar": "header", "toolbar": "header",
	"textfield": "input", "textbox": "input", "field": "input", "password": "input",
	"dropdown": "select", "img": "image", "photo": "image", "picture": "image",
	"switch": "toggle", "label": "text", "paragraph": "text", "p": "text",
	"title": "heading", "h1": "heading", "h2": "heading", "h3": "heading",
	"hr": "divider", "separator": "divider", "dialog": "modal", "popup": "modal",
	"tag": "badge", "chip": "badge", "graph": "chart", "tab": "tabs",
	"menu": "sidebar", "drawer": "sidebar", "a": "link", "btn": "button",
}

// 已知的 type
var known = map[string]bool{
	"col": true, "row": true, "stack": true, "grid": true, "card": true, "section": true,
	"header": true, "navbar": true, "footer": true, "sidebar": true, "text": true, "heading": true, "link": true,
	"button": true, "input": true, "textarea": true, "select": true, "checkbox": true, "radio": true, "toggle": true,
	"image": true, "avatar": true, "icon": true, "list": true, "table": true, "tabs": true, "chart": true,
	"badge": true, "divider": true, "spacer": true, "modal": true,
}

// spec 解析後的整份描述
type spec struct {
	title  string
	w, h   float64
	root   *node
	warns  []string
	modals []*node
}

// decode 描述(物件、JSON 字串或 []byte)→ 物件
func decode(in any) (map[string]any, error) {
	var data []byte
	switch v := in.(type) {
	case nil:
		return nil, errf("沒有描述(要給 {\"device\": …, \"root\": {…}})")
	case string:
		data = []byte(stripFence(v))
	case []byte:
		data = []byte(stripFence(string(v)))
	case json.RawMessage:
		data = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, errf("描述無法轉成 JSON:%v", err)
		}
		data = b
	}
	if len(data) > MaxBytes {
		return nil, errf("描述太大(%d bytes,最多 %d)", len(data), MaxBytes)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, errf("描述是空的")
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, errf("不是有效的 JSON(%v)", err)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, errf("描述要是 JSON 物件:{\"device\": \"mobile\", \"root\": {\"type\": \"col\", \"children\": […]}}")
	}
	return m, nil
}

// stripFence 字串外面包了 ```wireframe … ``` 時拿掉
func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	if b := Blocks(t); len(b) > 0 {
		return b[0]
	}
	return t
}

// parse 檢查並解析整份描述
func parse(in any) (*spec, error) {
	m, err := decode(in)
	if err != nil {
		return nil, err
	}
	s := &spec{w: 1280, h: 800}
	if t, ok := m["title"].(string); ok {
		s.title = t
	}
	switch d := m["device"].(type) {
	case nil:
	case string:
		switch strings.ToLower(strings.TrimSpace(d)) {
		case "", "desktop", "web", "pc":
		case "tablet", "ipad", "pad":
			s.w, s.h = 768, 1024
		case "mobile", "phone", "iphone", "android":
			s.w, s.h = 390, 844
		default:
			return nil, errf("device 只能是 desktop、tablet、mobile 或 {\"w\": 寬, \"h\": 高},不認得 %q", d)
		}
	case map[string]any:
		w, okW := numOf(firstOf(d, "w", "width"))
		h, okH := numOf(firstOf(d, "h", "height"))
		if !okW || !okH {
			return nil, errf("自訂 device 要有數字 w 與 h,例如 {\"w\": 1024, \"h\": 768}")
		}
		if w < 200 || w > 4000 || h < 200 || h > 4000 {
			return nil, errf("自訂 device 的 w、h 要介於 200~4000(收到 %v×%v)", w, h)
		}
		s.w, s.h = math.Round(w), math.Round(h)
	default:
		return nil, errf("device 只能是字串(desktop、tablet、mobile)或 {\"w\": 寬, \"h\": 高}")
	}
	rootRaw, ok := m["root"]
	if !ok || rootRaw == nil {
		return nil, errf("缺少 root(畫面的根節點,例如 {\"type\": \"col\", \"children\": […]})")
	}
	count := 0
	root, err := s.node(rootRaw, "root", 1, &count)
	if err != nil {
		return nil, err
	}
	s.root = root
	return s, nil
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func (s *spec) warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	for _, w := range s.warns {
		if w == msg {
			return
		}
	}
	if len(s.warns) < maxWarnings {
		s.warns = append(s.warns, msg)
	}
}

func (s *spec) node(v any, path string, depth int, count *int) (*node, error) {
	if depth > MaxDepth {
		return nil, errf("%s:巢狀超過 %d 層", path, MaxDepth)
	}
	*count++
	if *count > MaxNodes {
		return nil, errf("節點超過 %d 個", MaxNodes)
	}
	var m map[string]any
	switch x := v.(type) {
	case string:
		m = map[string]any{"type": "text", "text": x}
	case float64, bool:
		m = map[string]any{"type": "text", "text": scalar(x)}
	case map[string]any:
		m = x
	default:
		return nil, errf("%s 要是物件 {\"type\": …}(或直接放文字)", path)
	}
	n := &node{p: m, path: path}
	raw, _ := m["type"].(string)
	t := strings.ToLower(strings.TrimSpace(raw))
	if a, ok := aliases[t]; ok {
		if t == "password" {
			if _, has := m["kind"]; !has {
				m["kind"] = "password"
			}
		}
		if lv := map[string]float64{"h1": 1, "h2": 2, "h3": 3}[t]; lv > 0 {
			if _, has := m["level"]; !has {
				m["level"] = lv
			}
		}
		t = a
	}
	kidsRaw, hasKids := m["children"]
	switch {
	case t == "" && hasKids:
		s.warn("%s 沒有 type,當成 col 畫", path)
		t = "col"
	case t == "":
		s.warn("%s 沒有 type,畫成虛線框", path)
		t = "?"
	case !known[t]:
		s.warn("不認得的元件 %q(%s),畫成虛線框", raw, path)
		n.name = strings.TrimSpace(raw)
	}
	n.typ = t
	if hasKids && kidsRaw != nil {
		l, ok := kidsRaw.([]any)
		if !ok {
			return nil, errf("%s.children 要是陣列", path)
		}
		for i, k := range l {
			c, err := s.node(k, fmt.Sprintf("%s.children[%d]", path, i), depth+1, count)
			if err != nil {
				return nil, err
			}
			n.kids = append(n.kids, c)
		}
	}
	if t == "modal" {
		s.modals = append(s.modals, n)
	}
	return n, nil
}

// ---- 屬性 ----

func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func numOf(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(x), "px"), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

func (n *node) str(k string) string {
	return strings.TrimSpace(scalar(n.p[k]))
}

// text 第一個有值的文字屬性
func (n *node) text(keys ...string) string {
	for _, k := range keys {
		if s := n.str(k); s != "" {
			return s
		}
	}
	return ""
}

func (n *node) num(k string) (float64, bool) {
	f, ok := numOf(n.p[k])
	if !ok {
		return 0, false
	}
	return clamp(f, -maxDimension, maxDimension), true
}

func (n *node) numOr(k string, def float64) float64 {
	if f, ok := n.num(k); ok {
		return f
	}
	return def
}

func (n *node) flag(k string) bool {
	switch x := n.p[k].(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1" || x == "yes"
	case float64:
		return x != 0
	}
	return false
}

// strs 字串陣列(數字轉字串;物件取 text、label、title、name)
func (n *node) strs(k string) []string {
	l, ok := n.p[k].([]any)
	if !ok {
		if s := n.str(k); s != "" {
			return []string{s}
		}
		return nil
	}
	out := []string{}
	for _, x := range l {
		switch v := x.(type) {
		case map[string]any:
			out = append(out, strings.TrimSpace(scalar(firstOf(v, "text", "label", "title", "name"))))
		default:
			out = append(out, strings.TrimSpace(scalar(v)))
		}
	}
	return out
}

// 尺寸
type dimKind int

const (
	dimAuto dimKind = iota
	dimFixed
	dimFill
	dimPct
)

type dim struct {
	kind dimKind
	v    float64
}

func (n *node) dim(k string) dim {
	switch x := n.p[k].(type) {
	case float64:
		return dim{dimFixed, clamp(x, 0, maxDimension)}
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "fill", "full", "flex", "stretch", "100%":
			return dim{kind: dimFill}
		case "", "auto", "hug", "fit":
			return dim{}
		}
		if strings.HasSuffix(s, "%") {
			if f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64); err == nil && f > 0 {
				return dim{dimPct, clamp(f/100, 0, 1)}
			}
		}
		if f, ok := numOf(s); ok {
			return dim{dimFixed, clamp(f, 0, maxDimension)}
		}
	}
	return dim{}
}

type edges struct{ t, r, b, l float64 }

func (n *node) padding(def float64) edges {
	switch x := n.p["padding"].(type) {
	case float64:
		v := clamp(x, 0, 400)
		return edges{v, v, v, v}
	case []any:
		f := []float64{}
		for _, e := range x {
			v, _ := numOf(e)
			f = append(f, clamp(v, 0, 400))
		}
		switch len(f) {
		case 1:
			return edges{f[0], f[0], f[0], f[0]}
		case 2:
			return edges{f[0], f[1], f[0], f[1]}
		case 3:
			return edges{f[0], f[1], f[2], f[1]}
		case 4:
			return edges{f[0], f[1], f[2], f[3]}
		}
	}
	return edges{def, def, def, def}
}

func (n *node) gap(def float64) float64 {
	if v, ok := n.num("gap"); ok {
		return clamp(v, 0, 400)
	}
	return def
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
