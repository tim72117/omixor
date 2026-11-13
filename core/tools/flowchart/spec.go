package flowchart

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 描述的上限
const (
	MaxBytes    = 256 << 10 // 描述最大 256KB
	MaxNodes    = 300       // 節點最多 300 個
	MaxEdges    = 600       // 邊最多 600 條
	MaxGroups   = 100       // 群組(subgraph)最多 100 個
	maxLabel    = 400       // 標籤最多 400 字
	maxWarnings = 50
)

// Error 描述格式錯誤(不是 JSON、Mermaid 語法錯、id 重複、邊指向不存在的節點…)
type Error struct{ Msg string }

func (e *Error) Error() string { return "流程圖描述錯誤:" + e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// Graph 解析後的流程圖(JSON 與 Mermaid 都解析成這個)
type Graph struct {
	Title     string   `json:"title,omitempty"`
	Direction string   `json:"direction"` // TB | LR | BT | RL
	Nodes     []*Node  `json:"nodes"`
	Edges     []*Edge  `json:"edges"`
	Groups    []*Group `json:"groups,omitempty"`
	Warnings  []string `json:"-"`
}

// Node 節點
type Node struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Shape string `json:"shape,omitempty"` // process | decision | start | end | io | subprocess | database | note | round | circle | hexagon
	Group string `json:"group,omitempty"`
	Style string `json:"style,omitempty"` // highlight | muted
}

// Edge 邊
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Style string `json:"style,omitempty"` // solid | dashed | thick | invisible
	Arrow string `json:"arrow,omitempty"` // end(預設)| none | both
}

// Group 群組(Mermaid 的 subgraph),畫成圓角虛線框;parent 可巢狀
type Group struct {
	ID     string `json:"id"`
	Label  string `json:"label,omitempty"`
	Parent string `json:"parent,omitempty"`
}

func (g *Graph) warn(format string, a ...any) {
	if len(g.Warnings) < maxWarnings {
		g.Warnings = append(g.Warnings, fmt.Sprintf(format, a...))
	}
}

// 形狀的別名
var shapeAlias = map[string]string{
	"": "process", "process": "process", "rect": "process", "rectangle": "process", "box": "process", "step": "process", "action": "process",
	"decision": "decision", "diamond": "decision", "rhombus": "decision", "condition": "decision", "if": "decision", "question": "decision",
	"start": "start", "begin": "start", "stadium": "start", "pill": "start", "terminal": "start", "terminator": "start",
	"end": "end", "stop": "end", "finish": "end",
	"io": "io", "input": "io", "output": "io", "data": "io", "parallelogram": "io",
	"subprocess": "subprocess", "subroutine": "subprocess", "predefined": "subprocess", "call": "subprocess",
	"database": "database", "db": "database", "cylinder": "database", "storage": "database",
	"note": "note", "comment": "note", "document": "note",
	"round": "round", "rounded": "round",
	"circle": "circle", "ellipse": "circle", "oval": "circle",
	"hexagon": "hexagon", "preparation": "hexagon",
}

var dirAlias = map[string]string{"": "TB", "TB": "TB", "TD": "TB", "DOWN": "TB", "LR": "LR", "RIGHT": "LR", "BT": "BT", "UP": "BT", "RL": "RL", "LEFT": "RL"}

// Parse 把描述解析成 Graph:物件或 JSON 字串(以 { 開頭)走 JSON 格式,其他字串當成 Mermaid flowchart。
// 字串外面包了 ```flowchart / ```mermaid / ```json 區塊也可以。格式錯誤回 *Error。
func Parse(in any) (*Graph, error) {
	switch v := in.(type) {
	case nil:
		return nil, errf("沒有描述(給 JSON {\"nodes\": […], \"edges\": […]} 或 Mermaid 的 flowchart 文字)")
	case string:
		return parseText(v)
	case []byte:
		return parseText(string(v))
	case json.RawMessage:
		return parseText(string(v))
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, errf("描述無法轉成 JSON:%v", err)
		}
		return parseText(string(b))
	}
}

// IsMermaid 這段文字(去掉 ``` 區塊外框後)是不是 Mermaid 語法(而不是 JSON)
func IsMermaid(s string) bool {
	t := stripFence(s)
	return t != "" && !strings.HasPrefix(t, "{")
}

func parseText(s string) (*Graph, error) {
	if len(s) > MaxBytes {
		return nil, errf("描述太大(%d bytes,最多 %d)", len(s), MaxBytes)
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	t := stripFence(s)
	if t == "" {
		return nil, errf("描述是空的")
	}
	var g *Graph
	var err error
	if strings.HasPrefix(t, "{") {
		g, err = parseJSON(t)
	} else {
		g, err = parseMermaid(t)
	}
	if err != nil {
		return nil, err
	}
	if err := g.validate(); err != nil {
		return nil, err
	}
	return g, nil
}

// stripFence 字串外面包了 ```xxx … ``` 時拿掉外框(只取第一個區塊)
func stripFence(s string) string {
	t := strings.TrimSpace(strings.TrimPrefix(s, string(rune(0xFEFF))))
	if !strings.HasPrefix(t, "```") {
		return t
	}
	lines := strings.Split(strings.ReplaceAll(t, "\r\n", "\n"), "\n")
	end := len(lines)
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[1:end], "\n"))
}

// ---- JSON ----

func parseJSON(t string) (*Graph, error) {
	var raw any
	if err := json.Unmarshal([]byte(t), &raw); err != nil {
		return nil, errf("不是有效的 JSON(%v)", err)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, errf("描述要是 JSON 物件:{\"nodes\": [{\"id\": \"a\", \"label\": \"開始\"}], \"edges\": [{\"from\": \"a\", \"to\": \"b\"}]}")
	}
	g := &Graph{Title: scalar(m["title"]), Direction: scalar(firstOf(m, "direction", "dir", "rankdir"))}
	arr := func(k string) ([]any, error) {
		v, ok := m[k]
		if !ok || v == nil {
			return nil, nil
		}
		a, ok := v.([]any)
		if !ok {
			return nil, errf("%s 要是陣列", k)
		}
		return a, nil
	}
	nodes, err := arr("nodes")
	if err != nil {
		return nil, err
	}
	edges, err := arr("edges")
	if err != nil {
		return nil, err
	}
	groups, err := arr("groups")
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, errf("沒有節點(nodes 是空的)")
	}
	if len(nodes) > MaxNodes {
		return nil, errf("節點太多(%d 個,最多 %d)", len(nodes), MaxNodes)
	}
	if len(edges) > MaxEdges {
		return nil, errf("邊太多(%d 條,最多 %d)", len(edges), MaxEdges)
	}
	if len(groups) > MaxGroups {
		return nil, errf("群組太多(%d 個,最多 %d)", len(groups), MaxGroups)
	}
	seen := map[string]bool{}
	for i, v := range nodes {
		var n Node
		switch x := v.(type) {
		case string:
			n = Node{ID: strings.TrimSpace(x)}
		case map[string]any:
			n = Node{ID: strings.TrimSpace(scalar(x["id"])), Label: scalar(firstOf(x, "label", "text", "title", "name")),
				Shape: scalar(firstOf(x, "shape", "type", "kind")), Group: strings.TrimSpace(scalar(firstOf(x, "group", "parent", "subgraph"))), Style: scalar(x["style"])}
		default:
			return nil, errf("nodes[%d] 要是物件 {\"id\", \"label\", \"shape\"?}", i)
		}
		if n.ID == "" {
			return nil, errf("nodes[%d] 沒有 id", i)
		}
		if seen[n.ID] {
			return nil, errf("節點 id 重複:「%s」(nodes[%d])", n.ID, i)
		}
		seen[n.ID] = true
		g.Nodes = append(g.Nodes, &n)
	}
	for i, v := range edges {
		x, ok := v.(map[string]any)
		if !ok {
			return nil, errf("edges[%d] 要是物件 {\"from\", \"to\", \"label\"?}", i)
		}
		e := Edge{From: strings.TrimSpace(scalar(firstOf(x, "from", "source", "src"))), To: strings.TrimSpace(scalar(firstOf(x, "to", "target", "dst"))),
			Label: scalar(firstOf(x, "label", "text")), Style: scalar(x["style"]), Arrow: scalar(x["arrow"])}
		if e.From == "" || e.To == "" {
			return nil, errf("edges[%d] 要有 from 與 to", i)
		}
		for _, end := range [][2]string{{"from", e.From}, {"to", e.To}} {
			if !seen[end[1]] {
				return nil, errf("edges[%d] 的 %s「%s」不是已知的節點 id", i, end[0], end[1])
			}
		}
		g.Edges = append(g.Edges, &e)
	}
	gseen := map[string]bool{}
	for i, v := range groups {
		var gr Group
		switch x := v.(type) {
		case string:
			gr = Group{ID: strings.TrimSpace(x)}
		case map[string]any:
			gr = Group{ID: strings.TrimSpace(scalar(x["id"])), Label: scalar(firstOf(x, "label", "title", "name")), Parent: strings.TrimSpace(scalar(firstOf(x, "parent", "group")))}
		default:
			return nil, errf("groups[%d] 要是物件 {\"id\", \"label\"}", i)
		}
		if gr.ID == "" {
			return nil, errf("groups[%d] 沒有 id", i)
		}
		if gseen[gr.ID] {
			return nil, errf("群組 id 重複:「%s」(groups[%d])", gr.ID, i)
		}
		gseen[gr.ID] = true
		g.Groups = append(g.Groups, &gr)
	}
	return g, nil
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

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

// ---- 共同檢查與正規化 ----

func (g *Graph) validate() error {
	if len(g.Nodes) == 0 {
		return errf("沒有節點")
	}
	if len(g.Nodes) > MaxNodes {
		return errf("節點太多(%d 個,最多 %d)", len(g.Nodes), MaxNodes)
	}
	if len(g.Edges) > MaxEdges {
		return errf("邊太多(%d 條,最多 %d)", len(g.Edges), MaxEdges)
	}
	g.Title = limit(strings.TrimSpace(g.Title))
	d, ok := dirAlias[strings.ToUpper(strings.TrimSpace(g.Direction))]
	if !ok {
		g.warn("不認得的方向「%s」,改用 TB(由上而下)", g.Direction)
		d = "TB"
	}
	g.Direction = d

	groups := map[string]*Group{}
	for _, gr := range g.Groups {
		groups[gr.ID] = gr
		gr.Label = limit(strings.TrimSpace(gr.Label))
		if gr.Label == "" {
			gr.Label = gr.ID
		}
	}
	ids := map[string]bool{}
	for _, n := range g.Nodes {
		ids[n.ID] = true
		n.Label = limit(strings.TrimSpace(n.Label))
		if n.Label == "" {
			n.Label = n.ID
		}
		s, ok := shapeAlias[strings.ToLower(strings.TrimSpace(n.Shape))]
		if !ok {
			g.warn("節點「%s」的形狀「%s」不認得,畫成矩形", n.ID, n.Shape)
			s = "process"
		}
		n.Shape = s
		switch st := strings.ToLower(strings.TrimSpace(n.Style)); st {
		case "", "normal", "default", "solid":
			n.Style = ""
		case "highlight", "accent", "primary", "emphasis":
			n.Style = "highlight"
		case "muted", "dim", "disabled", "secondary":
			n.Style = "muted"
		default:
			g.warn("節點「%s」的樣式「%s」不認得(只有 highlight、muted)", n.ID, n.Style)
			n.Style = ""
		}
		if n.Group != "" && groups[n.Group] == nil { // 沒宣告的群組自動建立
			if len(g.Groups) >= MaxGroups {
				return errf("群組太多(最多 %d)", MaxGroups)
			}
			gr := &Group{ID: n.Group, Label: n.Group}
			g.Groups = append(g.Groups, gr)
			groups[gr.ID] = gr
		}
	}
	if len(g.Groups) > MaxGroups {
		return errf("群組太多(%d 個,最多 %d)", len(g.Groups), MaxGroups)
	}
	for _, gr := range g.Groups {
		if gr.Parent != "" && groups[gr.Parent] == nil {
			g.warn("群組「%s」的 parent「%s」不存在,當成最外層", gr.ID, gr.Parent)
			gr.Parent = ""
		}
		// 巢狀不能繞圈
		seen := map[string]bool{gr.ID: true}
		for p := gr.Parent; p != ""; p = groups[p].Parent {
			if seen[p] {
				g.warn("群組「%s」的 parent 繞成一圈,當成最外層", gr.ID)
				gr.Parent = ""
				break
			}
			seen[p] = true
		}
	}
	for i, e := range g.Edges {
		if !ids[e.From] {
			return errf("第 %d 條邊的 from「%s」不是已知的節點 id", i+1, e.From)
		}
		if !ids[e.To] {
			return errf("第 %d 條邊的 to「%s」不是已知的節點 id", i+1, e.To)
		}
		e.Label = limit(strings.TrimSpace(e.Label))
		switch st := strings.ToLower(strings.TrimSpace(e.Style)); st {
		case "", "solid", "normal", "line":
			e.Style = ""
		case "dashed", "dotted", "dash", "dot":
			e.Style = "dashed"
		case "thick", "bold", "heavy":
			e.Style = "thick"
		case "invisible", "hidden", "none":
			e.Style = "invisible"
		default:
			g.warn("第 %d 條邊的樣式「%s」不認得(只有 solid、dashed、thick)", i+1, e.Style)
			e.Style = ""
		}
		switch a := strings.ToLower(strings.TrimSpace(e.Arrow)); a {
		case "", "end", "forward", "true", "yes":
			e.Arrow = ""
		case "none", "no", "false", "off":
			e.Arrow = "none"
		case "both", "two-way", "bidirectional":
			e.Arrow = "both"
		default:
			g.warn("第 %d 條邊的 arrow「%s」不認得(只有 end、none、both)", i+1, e.Arrow)
			e.Arrow = ""
		}
	}
	return nil
}

// limit 標籤最多 maxLabel 字
func limit(s string) string {
	if utf8.RuneCountInString(s) <= maxLabel {
		return s
	}
	r := []rune(s)
	return string(r[:maxLabel]) + "…"
}
