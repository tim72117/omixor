package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 文件的分段讀寫(像處理真實檔案一樣,不必整份讀、整份寫,省 agent 的上下文):
//   - 大綱:Markdown 標題(# 到 ######;``` 程式碼區塊裡的不算)切成段落,每段有 id(s1、s2…;第一個標題前的內容是 s0)、
//     標題、層級、行號範圍(1 起算,含首尾)與字數。一段 = 標題那一行到下一個同級或更高級標題之前(含子標題)
//   - 讀:大綱、指定段落(id 或標題)、行號範圍、搜尋(命中的行與前後幾行)
//   - 改:字串取代(old 在全文必須唯一,同 Claude Code 的 Edit)、整段替換(保留標題那一行)、段尾附加(內容自帶標題就是插入新段落)
//   - 版本:內文的雜湊(DocVersion);局部修改可以帶讀到的版本,不符時伺服器決定能不能照樣套用
// App(Swift)有同一套規則,改這裡要一起改。

// DocSection 一個段落
type DocSection struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Level int    `json:"level"` // 0 = 第一個標題前的內容
	Start int    `json:"start"` // 第幾行開始(1 起算,含)
	End   int    `json:"end"`   // 第幾行結束(含)
	Chars int    `json:"chars"` // 字數(含子標題)
}

// DocVersion 內文的版本(SHA-256 前 12 碼):內文一樣版本就一樣
func DocVersion(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:6])
}

func docLines(body string) []string {
	return strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
}

func headingOf(line string) (int, string) {
	t := strings.TrimRight(line, " \t")
	n := 0
	for n < len(t) && n < 7 && t[n] == '#' {
		n++
	}
	if n < 1 || n > 6 || len(t) <= n || t[n] != ' ' {
		return 0, ""
	}
	return n, strings.TrimSpace(t[n+1:])
}

// DocOutline 文件的大綱(依出現順序)
func DocOutline(body string) []DocSection {
	lines := docLines(body)
	type h struct {
		level, line int
		title       string
	}
	var hs []h
	inCode := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if lv, t := headingOf(l); lv > 0 {
			hs = append(hs, h{lv, i + 1, t})
		}
	}
	chars := func(a, b int) int {
		n := 0
		for i := a; i <= b && i <= len(lines); i++ {
			n += utf8.RuneCountInString(lines[i-1])
		}
		return n
	}
	out := []DocSection{}
	first := len(lines) + 1
	if len(hs) > 0 {
		first = hs[0].line
	}
	if pre := strings.TrimSpace(strings.Join(lines[:first-1], "\n")); pre != "" {
		out = append(out, DocSection{ID: "s0", Title: "(開頭)", Level: 0, Start: 1, End: first - 1, Chars: chars(1, first-1)})
	}
	for k, x := range hs {
		end := len(lines)
		for _, y := range hs[k+1:] {
			if y.level <= x.level {
				end = y.line - 1
				break
			}
		}
		out = append(out, DocSection{ID: fmt.Sprintf("s%d", k+1), Title: x.title, Level: x.level, Start: x.line, End: end, Chars: chars(x.line, end)})
	}
	return out
}

// FindDocSection 依 id(s3)、標題(不分大小寫,可以帶 # 前綴)找段落;同名標題有好幾個時要用 id
func FindDocSection(body, ref string) (*DocSection, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("要指定段落(大綱裡的 id 或標題)")
	}
	outline := DocOutline(body)
	for i := range outline {
		if outline[i].ID == ref {
			return &outline[i], nil
		}
	}
	title := strings.TrimSpace(strings.TrimLeft(ref, "#"))
	var hit []DocSection
	for _, s := range outline {
		if strings.EqualFold(s.Title, title) {
			hit = append(hit, s)
		}
	}
	switch len(hit) {
	case 1:
		return &hit[0], nil
	case 0:
		return nil, fmt.Errorf("找不到段落「%s」;先讀大綱(outline)", ref)
	default:
		ids := []string{}
		for _, s := range hit {
			ids = append(ids, s.ID)
		}
		return nil, fmt.Errorf("有 %d 個段落叫「%s」(%s),請用 id 指定", len(hit), title, strings.Join(ids, "、"))
	}
}

// DocLines 第 from 到 to 行(1 起算,含首尾;超出範圍自動收斂),回傳文字與實際範圍
func DocLines(body string, from, to int) (string, int, int) {
	lines := docLines(body)
	if from < 1 {
		from = 1
	}
	if to < from || to > len(lines) {
		to = len(lines)
	}
	if from > len(lines) {
		return "", from, from - 1
	}
	return strings.Join(lines[from-1:to], "\n"), from, to
}

// DocLineCount 行數
func DocLineCount(body string) int { return len(docLines(body)) }

// DocMatch 搜尋命中:行號與前後幾行(含行號,方便接著用 lines 讀或改)
type DocMatch struct {
	Line    int    `json:"line"`
	Text    string `json:"text"`
	Section string `json:"section,omitempty"` // 所在段落的 id
}

// DocGrep 搜尋(不分大小寫);context 是前後各幾行;最多回傳 limit 筆
func DocGrep(body, q string, context, limit int) []DocMatch {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	lines := docLines(body)
	outline := DocOutline(body)
	sectionOf := func(n int) string {
		id := ""
		for _, s := range outline {
			if s.Start <= n && n <= s.End {
				id = s.ID // 越後面的越內層
			}
		}
		return id
	}
	out := []DocMatch{}
	for i, l := range lines {
		if !strings.Contains(strings.ToLower(l), q) {
			continue
		}
		a, b := max(0, i-context), min(len(lines)-1, i+context)
		parts := []string{}
		for j := a; j <= b; j++ {
			parts = append(parts, fmt.Sprintf("%d: %s", j+1, lines[j]))
		}
		out = append(out, DocMatch{Line: i + 1, Text: strings.Join(parts, "\n"), Section: sectionOf(i + 1)})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// TextEdit 字串取代:Old 在全文必須剛好出現一次(All = true 時取代全部)
type TextEdit struct {
	Old string `json:"old"`
	New string `json:"new"`
	All bool   `json:"all,omitempty"`
}

// ApplyTextEdits 依序套用字串取代;任何一個找不到或不唯一就整個失敗(不會只改一半)
func ApplyTextEdits(body string, edits []TextEdit) (string, error) {
	for i, e := range edits {
		if e.Old == "" {
			return "", fmt.Errorf("第 %d 個修改沒有給 old", i+1)
		}
		n := strings.Count(body, e.Old)
		switch {
		case n == 0:
			return "", fmt.Errorf("第 %d 個修改:找不到要取代的文字(old);先讀那一段確認原文", i+1)
		case n > 1 && !e.All:
			return "", fmt.Errorf("第 %d 個修改:要取代的文字出現 %d 次,請多帶前後文讓它唯一,或設 all", i+1, n)
		}
		if e.All {
			body = strings.ReplaceAll(body, e.Old, e.New)
		} else {
			body = strings.Replace(body, e.Old, e.New, 1)
		}
	}
	return body, nil
}

func splice(lines []string, from, to int, repl []string) []string {
	out := append([]string{}, lines[:from-1]...)
	out = append(out, repl...)
	return append(out, lines[to:]...)
}

func textLines(s string) []string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// ReplaceDocSection 整段替換:保留標題那一行(s0 沒有標題),換掉其餘內容(含子標題);內容前後各留一行空白
func ReplaceDocSection(body, ref, content string) (string, error) {
	s, err := FindDocSection(body, ref)
	if err != nil {
		return "", err
	}
	lines := docLines(body)
	repl := []string{}
	from := s.Start
	if s.Level > 0 {
		repl = append(repl, lines[s.Start-1], "")
	}
	repl = append(repl, textLines(content)...)
	if s.End < len(lines) {
		repl = append(repl, "")
	}
	return strings.Join(splice(lines, from, s.End, repl), "\n"), nil
}

// AppendToDocSection 接在段落最後(含子標題之後、下一個同級標題之前);content 自己帶標題時,就是在這段後面插入新段落
func AppendToDocSection(body, ref, content string) (string, error) {
	s, err := FindDocSection(body, ref)
	if err != nil {
		return "", err
	}
	lines := docLines(body)
	end := s.End
	for end > s.Start && strings.TrimSpace(lines[end-1]) == "" { // 段落尾端的空行留在新內容後面
		end--
	}
	repl := append([]string{}, lines[s.Start-1:end]...)
	repl = append(repl, "")
	repl = append(repl, textLines(content)...)
	if s.End < len(lines) {
		repl = append(repl, "")
	}
	return strings.Join(splice(lines, s.Start, s.End, repl), "\n"), nil
}

// ChangedLines 新舊內文差在哪幾行(新內文的行號範圍;沒差回傳 0, 0)
func ChangedLines(before, after string) (int, int) {
	a, b := docLines(before), docLines(after)
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	if i == len(a) && i == len(b) {
		return 0, 0
	}
	ja, jb := len(a)-1, len(b)-1
	for ja >= i && jb >= i && a[ja] == b[jb] {
		ja--
		jb--
	}
	if jb < i { // 只刪掉內容:回傳刪除處
		return i + 1, i
	}
	return i + 1, jb + 1
}
