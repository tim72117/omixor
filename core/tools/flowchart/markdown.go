package flowchart

import (
	"bytes"
	"encoding/json"
	"strings"
)

// 文件櫃的保存格式:文件內文裡的 ```flowchart 程式碼區塊放原始描述(JSON 或 Mermaid 原文,渲染時自動判斷),
// 畫面顯示時把區塊畫成圖。內容是 flowchart / graph 語法的 ```mermaid 區塊也算流程圖區塊。

// Lang 程式碼區塊的語言標記
const Lang = "flowchart"

// IsMermaidFlowchart Mermaid 文字是不是 flowchart / graph 圖(跳過空行、%% 註解與 --- 前置區塊)
func IsMermaidFlowchart(src string) bool {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	inFront := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		if t == "---" {
			inFront = !inFront
			continue
		}
		if inFront {
			continue
		}
		kw, _ := cutWord(t)
		if k := strings.IndexByte(kw, ';'); k >= 0 {
			kw = kw[:k]
		}
		switch strings.ToLower(kw) {
		case "flowchart", "graph", "flowchart-elk":
			return true
		}
		return false
	}
	return false
}

// blockSpans 每個流程圖區塊:開頭行、結尾行(沒有結尾就是最後一行之後)
func blockSpans(lines []string) [][2]int {
	out := [][2]int{}
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "```") {
			continue
		}
		lang := ""
		if w := strings.Fields(strings.TrimLeft(t, "`")); len(w) > 0 { // info string 的第一個字(「```mermaid title=…」也算)
			lang = strings.ToLower(w[0])
		}
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
			j++
		}
		switch lang {
		case Lang:
			out = append(out, [2]int{i, j})
		case "mermaid":
			if IsMermaidFlowchart(strings.Join(lines[i+1:min(j, len(lines))], "\n")) {
				out = append(out, [2]int{i, j})
			}
		}
		i = j
	}
	return out
}

// Blocks Markdown 裡所有流程圖區塊(```flowchart,以及 flowchart 語法的 ```mermaid)的內容,依出現順序
func Blocks(md string) []string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	out := []string{}
	for _, s := range blockSpans(lines) {
		out = append(out, strings.Join(lines[s[0]+1:min(s[1], len(lines))], "\n"))
	}
	return out
}

// Fence 把描述包成 ```flowchart 區塊
func Fence(src string) string {
	return "```" + Lang + "\n" + strings.TrimSpace(src) + "\n```"
}

// FirstBlock 第一個流程圖區塊的完整原文(含開頭與結尾的 ``` 行)與它的語言標記(flowchart 或 mermaid);沒有回 ok=false。
// 用來在文件裡「只換掉這一段」(字串取代),不用整份內文覆寫
func FirstBlock(md string) (block, lang string, ok bool) {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	spans := blockSpans(lines)
	if len(spans) == 0 {
		return "", "", false
	}
	s := spans[0]
	lang = Lang
	if w := strings.Fields(strings.TrimLeft(strings.TrimSpace(lines[s[0]]), "`")); len(w) > 0 {
		lang = strings.ToLower(w[0])
	}
	return strings.Join(lines[s[0]:min(s[1]+1, len(lines))], "\n"), lang, true
}

// FenceLike 包成和原本區塊同樣語言的程式碼區塊:原本是 ```mermaid 而新描述也是 Mermaid 文字時維持 ```mermaid
// (其他 Markdown 檢視器才看得懂);JSON 描述一律用 ```flowchart
func FenceLike(lang, src string) string {
	s := strings.TrimSpace(src)
	if lang == "mermaid" && !strings.HasPrefix(s, "{") {
		return "```mermaid\n" + s + "\n```"
	}
	return Fence(s)
}

// Upsert 把 Markdown 裡第一個流程圖區塊換成新的描述(原本是 ```mermaid 且新描述也是 Mermaid 就維持 ```mermaid,
// 見 FenceLike);沒有區塊就接在最後
func Upsert(md, src string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	spans := blockSpans(lines)
	if len(spans) == 0 {
		t := strings.TrimRight(md, "\n ")
		if t == "" {
			return Fence(src) + "\n"
		}
		return t + "\n\n" + Fence(src) + "\n"
	}
	s := spans[0]
	end := min(s[1]+1, len(lines))
	_, lang, _ := FirstBlock(md)
	out := append([]string{}, lines[:s[0]]...)
	out = append(out, strings.Split(FenceLike(lang, src), "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}

// Source 要存進 ```flowchart 區塊的文字:Mermaid 原文照存(去掉外框);JSON(物件或字串)整理成好讀的格式。
// 描述有錯時回 *Error(同 Render)。
func Source(in any) (string, error) {
	g, err := Parse(in)
	if err != nil {
		return "", err
	}
	if s, ok := in.(string); ok && IsMermaid(s) {
		return stripFence(s), nil
	}
	return Pretty(g), nil
}

// Pretty 把解析後的流程圖寫成 JSON:每個節點、每條邊一行,省略預設值
func Pretty(g *Graph) string {
	var b bytes.Buffer
	js := func(v any) string {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(v)
		return strings.TrimSpace(buf.String())
	}
	type kv struct{ k, v string }
	obj := func(fields ...kv) string {
		parts := []string{}
		for _, f := range fields {
			if f.v != "" {
				parts = append(parts, js(f.k)+": "+js(f.v))
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	b.WriteString("{\n")
	if g.Title != "" {
		b.WriteString(`  "title": ` + js(g.Title) + ",\n")
	}
	b.WriteString(`  "direction": ` + js(g.Direction))
	if len(g.Groups) > 0 {
		b.WriteString(",\n  \"groups\": [\n")
		for i, gr := range g.Groups {
			label := gr.Label
			if label == gr.ID {
				label = ""
			}
			b.WriteString("    " + obj(kv{"id", gr.ID}, kv{"label", label}, kv{"parent", gr.Parent}))
			if i+1 < len(g.Groups) {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString("  ]")
	}
	b.WriteString(",\n  \"nodes\": [\n")
	for i, n := range g.Nodes {
		label, shape := n.Label, n.Shape
		if label == n.ID {
			label = ""
		}
		if shape == "process" {
			shape = ""
		}
		b.WriteString("    " + obj(kv{"id", n.ID}, kv{"label", label}, kv{"shape", shape}, kv{"group", n.Group}, kv{"style", n.Style}))
		if i+1 < len(g.Nodes) {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ],\n  \"edges\": [")
	if len(g.Edges) > 0 {
		b.WriteString("\n")
		for i, e := range g.Edges {
			b.WriteString("    " + obj(kv{"from", e.From}, kv{"to", e.To}, kv{"label", e.Label}, kv{"style", e.Style}, kv{"arrow", e.Arrow}))
			if i+1 < len(g.Edges) {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString("  ")
	}
	b.WriteString("]\n}")
	return b.String()
}
