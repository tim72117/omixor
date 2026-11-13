package wireframe

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// 文件櫃的保存格式:文件內文裡的 ```wireframe 程式碼區塊放 JSON 描述(真正的資料來源),
// 畫面顯示時把區塊渲染成圖;編輯時改的是 JSON。

// Lang 程式碼區塊的語言標記
const Lang = "wireframe"

// FenceLang 程式碼區塊開頭行的語言標記(info string 的第一個字,小寫);不是開頭行回 ""
// 例如「```mermaid title="x"」是 mermaid
func FenceLang(line string) string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "```") {
		return ""
	}
	w := strings.Fields(strings.TrimLeft(t, "`"))
	if len(w) == 0 {
		return ""
	}
	return strings.ToLower(w[0])
}

// isOpen 這一行是不是 ```wireframe 區塊的開頭
func isOpen(line string) bool { return FenceLang(line) == Lang }

// FirstBlock 第一個 ```wireframe 區塊的完整原文(含開頭與結尾的 ``` 行);沒有回 ok=false。
// 用來在文件裡「只換掉這一段」(字串取代),不用整份內文覆寫
func FirstBlock(md string) (block string, ok bool) {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	spans := blockSpans(lines)
	if len(spans) == 0 {
		return "", false
	}
	s := spans[0]
	return strings.Join(lines[s[0]:min(s[1]+1, len(lines))], "\n"), true
}

// blockSpans 每個 ```wireframe 區塊:開頭行、結尾行(沒有結尾就是最後一行之後)
func blockSpans(lines []string) [][2]int {
	out := [][2]int{}
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			continue
		}
		open := isOpen(lines[i])
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
			j++
		}
		if open {
			out = append(out, [2]int{i, j})
		}
		i = j
	}
	return out
}

// Blocks Markdown 裡所有 ```wireframe 區塊的內容(依出現順序)
func Blocks(md string) []string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	out := []string{}
	for _, s := range blockSpans(lines) {
		out = append(out, strings.Join(lines[s[0]+1:min(s[1], len(lines))], "\n"))
	}
	return out
}

// Fence 把描述(JSON 文字)包成 ```wireframe 區塊
func Fence(specJSON string) string {
	return "```" + Lang + "\n" + strings.TrimSpace(specJSON) + "\n```"
}

// Upsert 把 Markdown 裡第一個 ```wireframe 區塊換成新的描述;沒有區塊就接在最後
func Upsert(md, specJSON string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	spans := blockSpans(lines)
	if len(spans) == 0 {
		t := strings.TrimRight(md, "\n ")
		if t == "" {
			return Fence(specJSON) + "\n"
		}
		return t + "\n\n" + Fence(specJSON) + "\n"
	}
	s := spans[0]
	end := min(s[1]+1, len(lines))
	out := append([]string{}, lines[:s[0]]...)
	out = append(out, strings.Split(Fence(specJSON), "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}

// Pretty 把描述(物件或 JSON 字串)整理成好讀的縮排 JSON:type 等常用欄位在前、children 在最後,
// 純值的短陣列放在同一行。描述有錯時回 *Error(同 Render)。
func Pretty(in any) (string, error) {
	m, err := decode(in)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	writePretty(&b, m, 0)
	return b.String(), nil
}

// 欄位順序:前面的先寫,其他依字母,children 與 root 最後
var keyOrder = map[string]int{"title": 1, "device": 2, "type": 3, "text": 4, "label": 5, "variant": 6, "level": 7}

func orderedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		if r, ok := keyOrder[k]; ok {
			return r
		}
		if k == "children" || k == "root" {
			return 1000
		}
		return 100
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := rank(keys[i]), rank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func scalarJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimRight(b.String(), "\n")
}

func flat(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return false
	case []any:
		for _, e := range x {
			if !flat(e) {
				return false
			}
		}
	}
	return true
}

func writePretty(b *bytes.Buffer, v any, depth int) {
	ind := strings.Repeat("  ", depth)
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			b.WriteString("{}")
			return
		}
		// 沒有子節點的小物件寫成一行
		if one := scalarJSON(x); !strings.Contains(one, "\"children\"") && len(one) <= 100 && flat(mapValues(x)) {
			b.WriteString("{")
			for i, k := range orderedKeys(x) {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(scalarJSON(k) + ": " + scalarJSON(x[k]))
			}
			b.WriteString("}")
			return
		}
		b.WriteString("{\n")
		for i, k := range orderedKeys(x) {
			b.WriteString(ind + "  " + scalarJSON(k) + ": ")
			writePretty(b, x[k], depth+1)
			if i < len(x)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(ind + "}")
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		if one := scalarJSON(x); flat(x) && len(one) <= 100 {
			b.WriteString(one)
			return
		}
		b.WriteString("[\n")
		for i, e := range x {
			b.WriteString(ind + "  ")
			writePretty(b, e, depth+1)
			if i < len(x)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(ind + "]")
	default:
		b.WriteString(scalarJSON(x))
	}
}

func mapValues(m map[string]any) []any {
	out := make([]any, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
