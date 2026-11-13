package flowchart

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mermaid flowchart 子集的解析器。支援:
//
//	flowchart TD|TB|LR|RL|BT(或 graph …),--- title: … --- 前置區塊的標題
//	節點:A、A[文字]、A(文字)、A([文字])、A((文字))、A{文字}、A{{文字}}、A[(文字)]、A[[文字]]、A[/文字/]、A[\文字\]、A>文字]、
//	     文字可以用 "…" 包起來,<br> 換行;A:::highlight 設樣式
//	邊:-->、---、-.->、-.-、==>、===、~~~(只影響排版,不畫)、<-->(雙向)、-->|標籤|、-- 標籤 -->、-. 標籤 .->、== 標籤 ==>
//	同一行串接 A --> B --> C,A & B --> C,分號分隔多個敘述
//	subgraph id [標題] … end(可巢狀)、%% 註解、class A,B highlight
//
// 忽略(列在 warnings):classDef、style、linkStyle、click、direction(subgraph 內)、accTitle、accDescr。

type mermaidError struct {
	line int
	msg  string
	near string
}

type mparser struct {
	g        *Graph
	nodes    map[string]*Node
	groups   map[string]*Group
	stack    []*Group
	stackAt  []int
	line     int
	auto     map[string]int // 自動建立(沒給形狀)的節點 → 第一次出現的行
	ignored  map[string]bool
	subCount int
}

var reBr = regexp.MustCompile(`(?i)<br\s*/?>`)

func parseMermaid(src string) (*Graph, error) {
	p := &mparser{g: &Graph{}, nodes: map[string]*Node{}, groups: map[string]*Group{}, auto: map[string]int{}, ignored: map[string]bool{}}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	i := 0
	skipBlank := func() {
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if t != "" && !strings.HasPrefix(t, "%%") {
				return
			}
			i++
		}
	}
	skipBlank()
	// --- title: … --- 前置區塊
	if i < len(lines) && strings.TrimSpace(lines[i]) == "---" {
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != "---" {
			if t := strings.TrimSpace(lines[j]); strings.HasPrefix(strings.ToLower(t), "title:") {
				p.g.Title = strings.Trim(strings.TrimSpace(t[len("title:"):]), `"'`)
			}
			j++
		}
		if j >= len(lines) {
			return nil, errf("第 %d 行:前置區塊(---)沒有結束", i+1)
		}
		i = j + 1
		skipBlank()
	}
	if i >= len(lines) {
		return nil, errf("Mermaid 描述是空的(第一行要是 flowchart TD 或 graph LR)")
	}
	// 標頭:flowchart|graph [方向] [; 敘述…]
	head := strings.TrimSpace(lines[i])
	kw, rest := cutWord(head)
	base := kw
	if k := strings.IndexByte(kw, ';'); k >= 0 {
		base = kw[:k]
	}
	switch strings.ToLower(base) {
	case "flowchart", "graph", "flowchart-elk":
	default:
		return nil, errf("第 %d 行:只支援 Mermaid 的 flowchart / graph 圖(這裡是「%s」);第一行要像 flowchart TD", i+1, short(base))
	}
	if k := strings.IndexByte(kw, ';'); k >= 0 { // graph;A-->B
		rest = kw[k:] + rest
	}
	d, r := cutWord(rest)
	if k := strings.IndexByte(d, ';'); k >= 0 { // graph TD;A-->B
		d, r = d[:k], d[k:]+r
	}
	if dd := strings.ToUpper(d); len(dd) == 2 && dirAlias[dd] != "" {
		p.g.Direction, rest = dd, r
	} else if d != "" && !strings.HasPrefix(d, ";") {
		return nil, errf("第 %d 行:不認得的方向「%s」(只有 TB、TD、LR、RL、BT)", i+1, short(d))
	}
	if p.g.Direction == "" {
		p.g.Direction = "TB"
	}
	p.line = i + 1
	if err := p.statements(rest); err != nil {
		return nil, err
	}
	for i++; i < len(lines); i++ {
		p.line = i + 1
		if err := p.statements(lines[i]); err != nil {
			return nil, err
		}
	}
	if len(p.stack) > 0 {
		return nil, errf("第 %d 行:subgraph「%s」沒有對應的 end", p.stackAt[len(p.stackAt)-1], p.stack[len(p.stack)-1].ID)
	}
	// 邊連到 subgraph 的 id(Mermaid 允許,這裡不支援)
	for id, ln := range p.auto {
		if p.groups[id] != nil {
			return nil, errf("第 %d 行:「%s」是 subgraph 的 id,目前不支援把邊連到 subgraph,請改連到裡面的節點", ln, id)
		}
	}
	if len(p.g.Nodes) == 0 {
		return nil, errf("Mermaid 描述裡沒有節點")
	}
	return p.g, nil
}

// cutWord 第一個字(到空白為止)與其餘
func cutWord(s string) (string, string) {
	s = strings.TrimSpace(s)
	if k := strings.IndexFunc(s, unicode.IsSpace); k >= 0 {
		return s[:k], s[k:]
	}
	return s, ""
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 24 {
		return string([]rune(s)[:24]) + "…"
	}
	return s
}

func (p *mparser) fail(msg string, near string) error {
	if near = short(near); near != "" {
		return errf("第 %d 行:%s(在「%s」附近)", p.line, msg, near)
	}
	return errf("第 %d 行:%s", p.line, msg)
}

func (p *mparser) ignore(kind string) {
	if !p.ignored[kind] {
		p.ignored[kind] = true
		p.g.warn("Mermaid 的 %s 目前不支援,已忽略(第 %d 行)", kind, p.line)
	}
}

// statements 一行可能有多個以 ; 分隔的敘述(括號、引號、|…| 裡的分號不算)
func (p *mparser) statements(line string) error {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "%%") {
		return nil
	}
	depth, quote, pipe, start := 0, false, false, 0
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c == '"':
			quote = !quote
		case quote:
		case c == '|':
			pipe = !pipe
		case pipe:
		case c == '[' || c == '(' || c == '{':
			depth++
		case c == ']' || c == ')' || c == '}':
			if depth > 0 {
				depth--
			}
		case c == ';' && depth == 0:
			if err := p.statement(t[start:i]); err != nil {
				return err
			}
			start = i + 1
		}
	}
	return p.statement(t[start:])
}

func (p *mparser) statement(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "%%") {
		return nil
	}
	kw, rest := cutWord(s)
	lk := strings.ToLower(kw)
	switch {
	case lk == "subgraph":
		return p.subgraph(strings.TrimSpace(rest))
	case lk == "end" && strings.TrimSpace(rest) == "":
		if len(p.stack) == 0 {
			return p.fail("多了一個 end(前面沒有 subgraph)", s)
		}
		p.stack = p.stack[:len(p.stack)-1]
		p.stackAt = p.stackAt[:len(p.stackAt)-1]
		return nil
	case lk == "direction":
		p.ignore("subgraph 內的 direction")
		return nil
	case lk == "classdef" || lk == "style" || lk == "linkstyle" || lk == "click":
		p.ignore(kw)
		return nil
	case strings.HasPrefix(lk, "acctitle") || strings.HasPrefix(lk, "accdescr"):
		p.ignore("accTitle / accDescr")
		return nil
	case lk == "class":
		ids, cls := cutWord(strings.TrimSpace(rest))
		cls = strings.TrimSpace(cls)
		st := classStyle(cls)
		if st == "" {
			p.ignore("class(只認得 highlight、muted)")
			return nil
		}
		for _, id := range strings.Split(ids, ",") {
			if id = strings.TrimSpace(id); id != "" {
				n, err := p.touch(id, "", "", false)
				if err != nil {
					return err
				}
				n.Style = st
			}
		}
		return nil
	}
	return p.chain(s)
}

func classStyle(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "highlight", "accent", "primary":
		return "highlight"
	case "muted", "dim", "disabled":
		return "muted"
	}
	return ""
}

// subgraph id [標題] | subgraph id["標題"] | subgraph "標題" | subgraph 標題文字
func (p *mparser) subgraph(rest string) error {
	if len(p.groups) >= MaxGroups {
		return p.fail(fmt.Sprintf("subgraph 太多(最多 %d)", MaxGroups), rest)
	}
	var id, label string
	switch {
	case rest == "":
		p.subCount++
		id = fmt.Sprintf("subgraph%d", p.subCount)
	case strings.HasPrefix(rest, `"`):
		label = strings.Trim(rest, `"`)
		id = label
	default:
		k := strings.IndexAny(rest, "[ \t")
		if k < 0 {
			id = rest
		} else {
			id = rest[:k]
			r := strings.TrimSpace(rest[k:])
			if strings.HasPrefix(r, "[") {
				if !strings.HasSuffix(r, "]") {
					return p.fail("subgraph 的標題括號沒有結束(缺少「]」)", rest)
				}
				label = strings.Trim(strings.TrimSpace(r[1:len(r)-1]), `"`)
			} else if r != "" { // subgraph 一段標題:整段當 id 與標題
				id, label = rest, rest
			}
		}
	}
	label = reBr.ReplaceAllString(label, "\n")
	if label == "" {
		label = id
	}
	if p.groups[id] != nil {
		return p.fail("subgraph 的 id 重複", id)
	}
	gr := &Group{ID: id, Label: label}
	if len(p.stack) > 0 {
		gr.Parent = p.stack[len(p.stack)-1].ID
	}
	p.groups[id] = gr
	p.g.Groups = append(p.g.Groups, gr)
	p.stack = append(p.stack, gr)
	p.stackAt = append(p.stackAt, p.line)
	return nil
}

// isAncestor a 是不是 b 的祖先(或同一個)
func (p *mparser) isAncestor(a, b string) bool {
	for x := b; x != ""; {
		if x == a {
			return true
		}
		g := p.groups[x]
		if g == nil {
			return false
		}
		x = g.Parent
	}
	return false
}

// touch 提到一個節點:沒有就建立;有形狀就更新標籤與形狀;在 subgraph 裡就歸到最內層的 subgraph
func (p *mparser) touch(id, label, shape string, hasShape bool) (*Node, error) {
	n := p.nodes[id]
	if n == nil {
		if len(p.g.Nodes) >= MaxNodes {
			return nil, p.fail(fmt.Sprintf("節點太多(最多 %d 個)", MaxNodes), id)
		}
		n = &Node{ID: id, Label: id, Shape: "process"}
		p.nodes[id] = n
		p.g.Nodes = append(p.g.Nodes, n)
		if !hasShape {
			p.auto[id] = p.line
		}
	}
	if hasShape {
		n.Label, n.Shape = label, shape
		delete(p.auto, id)
	}
	if len(p.stack) > 0 {
		cur := p.stack[len(p.stack)-1].ID
		if n.Group == "" || p.isAncestor(n.Group, cur) {
			n.Group = cur
		}
	}
	return n, nil
}

// link 一條連線的樣式
type link struct {
	label  string
	style  string
	arrow  string
	length int
}

func (p *mparser) chain(s string) error {
	pos := 0
	left, pos, err := p.nodeGroup(s, pos)
	if err != nil {
		return err
	}
	for {
		pos = skipSpace(s, pos)
		if pos >= len(s) {
			return nil
		}
		lk, np, err := p.link(s, pos)
		if err != nil {
			return err
		}
		right, np2, err := p.nodeGroup(s, np)
		if err != nil {
			return err
		}
		for _, a := range left {
			for _, b := range right {
				if len(p.g.Edges) >= MaxEdges {
					return p.fail(fmt.Sprintf("邊太多(最多 %d 條)", MaxEdges), s[pos:])
				}
				p.g.Edges = append(p.g.Edges, &Edge{From: a, To: b, Label: lk.label, Style: lk.style, Arrow: lk.arrow})
			}
		}
		left, pos = right, np2
	}
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// nodeGroup node (& node)*
func (p *mparser) nodeGroup(s string, pos int) ([]string, int, error) {
	ids := []string{}
	for {
		id, np, err := p.node(s, pos)
		if err != nil {
			return nil, pos, err
		}
		ids = append(ids, id)
		pos = skipSpace(s, np)
		if pos < len(s) && s[pos] == '&' {
			pos++
			continue
		}
		return ids, pos, nil
	}
}

func idRune(r rune) bool {
	return r == '_' || r == '-' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

// 節點形狀的括號(長的先比對)
var shapeOpen = []struct{ open, shape string }{
	{"(((", "circle"}, {"((", "circle"}, {"([", "start"}, {"[[", "subprocess"}, {"[(", "database"},
	{"[/", "io"}, {"[\\", "io"}, {"{{", "hexagon"}, {"[", "process"}, {"(", "round"}, {"{", "decision"}, {">", "process"},
}

var shapeClose = map[string][]string{
	"(((": {")))"}, "((": {"))"}, "([": {"])"}, "[[": {"]]"}, "[(": {")]"},
	"[/": {"/]", "\\]"}, "[\\": {"\\]", "/]"}, "{{": {"}}"}, "[": {"]"}, "(": {")"}, "{": {"}"}, ">": {"]"},
}

func (p *mparser) node(s string, pos int) (string, int, error) {
	pos = skipSpace(s, pos)
	start := pos
	for pos < len(s) {
		r, size := utf8.DecodeRuneInString(s[pos:])
		if !idRune(r) {
			break
		}
		// 連線開頭不算 id:-- -. == ~~
		if (r == '-' || r == '.') && pos+1 < len(s) && (s[pos+1] == '-' || s[pos+1] == '.' || s[pos+1] == '>') {
			break
		}
		pos += size
	}
	id := s[start:pos]
	if id == "" {
		if pos >= len(s) {
			return "", pos, p.fail("連線後面少了節點", s)
		}
		return "", pos, p.fail("預期節點 id(英數字、底線、中文)", s[pos:])
	}
	if l := strings.ToLower(id); l == "end" || l == "subgraph" {
		return "", pos, p.fail(fmt.Sprintf("「%s」是保留字,不能當節點 id", id), s[start:])
	}
	if strings.HasPrefix(s[pos:], "@{") {
		return "", pos, p.fail("不支援 @{ shape: … } 的新語法,請改用 [ ]、( )、{ } 等括號", s[start:])
	}
	label, shape, has := "", "", false
	for _, so := range shapeOpen {
		if !strings.HasPrefix(s[pos:], so.open) {
			continue
		}
		body := pos + len(so.open)
		bp := skipSpace(s, body)
		var text string
		end := -1
		closeLen := 0
		if bp < len(s) && s[bp] == '"' { // "…" 包起來的文字可以有括號
			q := strings.IndexByte(s[bp+1:], '"')
			if q < 0 {
				return "", pos, p.fail(fmt.Sprintf("節點「%s」的文字引號沒有結束", id), s[start:])
			}
			text = s[bp+1 : bp+1+q]
			after := skipSpace(s, bp+2+q)
			for _, c := range shapeClose[so.open] {
				if strings.HasPrefix(s[after:], c) {
					end, closeLen = after, len(c)
					break
				}
			}
			if end < 0 {
				return "", pos, p.fail(fmt.Sprintf("節點「%s」的形狀括號沒有結束(缺少「%s」)", id, shapeClose[so.open][0]), s[start:])
			}
		} else {
			for _, c := range shapeClose[so.open] {
				if k := strings.Index(s[body:], c); k >= 0 && (end < 0 || body+k < end) {
					end, closeLen = body+k, len(c)
				}
			}
			if end < 0 {
				return "", pos, p.fail(fmt.Sprintf("節點「%s」的形狀括號沒有結束(缺少「%s」)", id, shapeClose[so.open][0]), s[start:])
			}
			text = s[body:end]
		}
		label, shape, has = mermaidLabel(text), so.shape, true
		pos = end + closeLen
		break
	}
	n, err := p.touch(id, label, shape, has)
	if err != nil {
		return "", pos, err
	}
	if strings.HasPrefix(s[pos:], ":::") {
		k := pos + 3
		for k < len(s) && (idRune(rune(s[k])) && s[k] < 0x80) {
			k++
		}
		if st := classStyle(s[pos+3 : k]); st != "" {
			n.Style = st
		} else {
			p.ignore("::: class(只認得 highlight、muted)")
		}
		pos = k
	}
	return id, pos, nil
}

// mermaidLabel 節點或連線文字:<br> 換行、#quot; 等常見的實體、`markdown` 外框
func mermaidLabel(t string) string {
	t = strings.TrimSpace(t)
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		t = t[1 : len(t)-1]
	}
	if len(t) >= 2 && t[0] == '`' && t[len(t)-1] == '`' {
		t = t[1 : len(t)-1]
	}
	t = reBr.ReplaceAllString(t, "\n")
	t = strings.NewReplacer("#quot;", `"`, "#amp;", "&", "#lt;", "<", "#gt;", ">", "#35;", "#", "#59;", ";").Replace(t)
	return strings.TrimSpace(t)
}

// link 解析一條連線(與後面的 |標籤|)
func (p *mparser) link(s string, pos int) (link, int, error) {
	start := pos
	lk := link{}
	both := false
	if strings.HasPrefix(s[pos:], "<") {
		both = true
		pos++
	}
	count := func(c byte) int {
		n := 0
		for pos+n < len(s) && s[pos+n] == c {
			n++
		}
		return n
	}
	// 「-- 文字 -->」形式:找到結尾的連線
	labeled := func(ends ...string) (string, string, bool) {
		best, bi := "", -1
		for _, e := range ends {
			if k := strings.Index(s[pos:], e); k >= 0 && (bi < 0 || k < bi) {
				best, bi = e, k
			}
		}
		if bi < 0 {
			return "", "", false
		}
		text := s[pos : pos+bi]
		pos += bi + len(best)
		return text, best, true
	}
	switch {
	case strings.HasPrefix(s[pos:], "~~~"):
		pos += count('~')
		lk.style, lk.arrow = "invisible", "none"
	case strings.HasPrefix(s[pos:], "-."):
		pos++
		dots := count('.')
		pos += dots
		switch {
		case strings.HasPrefix(s[pos:], "->"):
			pos += 2
		case strings.HasPrefix(s[pos:], "-"):
			pos++
			lk.arrow = "none"
		default: // -. 文字 .->
			text, end, ok := labeled(".->", ".-")
			if !ok {
				return lk, pos, p.fail("虛線「-. 文字 .->」沒有結尾", s[start:])
			}
			lk.label = mermaidLabel(text)
			if end == ".-" {
				lk.arrow = "none"
			}
		}
		lk.style = "dashed"
	case strings.HasPrefix(s[pos:], "--"):
		n := count('-')
		pos += n
		switch {
		case strings.HasPrefix(s[pos:], ">"):
			pos++
		case n >= 3:
			lk.arrow = "none"
		default: // -- 文字 --> / -- 文字 ---
			text, end, ok := labeled("-->", "---")
			if !ok {
				return lk, pos, p.fail("「-- 文字 -->」沒有結尾的 -->", s[start:])
			}
			for pos < len(s) && s[pos] == '-' {
				pos++
			}
			if end == "---" {
				if strings.HasPrefix(s[pos:], ">") {
					pos++
				} else {
					lk.arrow = "none"
				}
			}
			lk.label = mermaidLabel(text)
		}
	case strings.HasPrefix(s[pos:], "=="):
		n := count('=')
		pos += n
		switch {
		case strings.HasPrefix(s[pos:], ">"):
			pos++
		case n >= 3:
			lk.arrow = "none"
		default:
			text, end, ok := labeled("==>", "===")
			if !ok {
				return lk, pos, p.fail("「== 文字 ==>」沒有結尾的 ==>", s[start:])
			}
			for pos < len(s) && s[pos] == '=' {
				pos++
			}
			if end == "===" {
				if strings.HasPrefix(s[pos:], ">") {
					pos++
				} else {
					lk.arrow = "none"
				}
			}
			lk.label = mermaidLabel(text)
		}
		lk.style = "thick"
	default:
		return lk, pos, p.fail("預期連線(-->、---、-.->、==> …)", s[start:])
	}
	if both {
		if lk.arrow == "none" {
			return lk, pos, p.fail("「<」開頭的連線要有箭頭,例如 <-->", s[start:])
		}
		lk.arrow = "both"
	}
	// |標籤|
	q := skipSpace(s, pos)
	if q < len(s) && s[q] == '|' {
		k := strings.IndexByte(s[q+1:], '|')
		if k < 0 {
			return lk, pos, p.fail("連線標籤 |…| 沒有結束", s[q:])
		}
		lk.label = mermaidLabel(s[q+1 : q+1+k])
		pos = q + 2 + k
	}
	if lk.style == "invisible" {
		lk.label = ""
	}
	return lk, pos, nil
}
