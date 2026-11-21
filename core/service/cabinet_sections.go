package service

import (
	"strings"
	"unicode/utf8"

	"omixor-core/model"
)

// 文件的分段讀寫:從 cabinet.go 拆出,行為不變

// ================= 分段讀寫(見 model/docsections.go) =================

// DocRead 分段讀:Outline 只回大綱;Section 回那一段(id 或標題);From/To 回行號範圍;Grep 回命中的行(前後 Context 行)。
// 都沒給就回大綱加開頭(不回全文;要全文用 GetDoc)
type DocRead struct {
	Outline bool   `json:"outline,omitempty"`
	Section string `json:"section,omitempty"`
	From    int    `json:"from,omitempty"`
	To      int    `json:"to,omitempty"`
	Grep    string `json:"grep,omitempty"`
	Context int    `json:"context,omitempty"`
}

// DocView 分段讀的結果
type DocView struct {
	ID      string             `json:"id"`
	Title   string             `json:"title"`
	Section string             `json:"section"`
	Tab     *string            `json:"tab,omitempty"`
	Version string             `json:"version"` // 內文的版本;局部修改時帶回來(baseVersion)
	Lines   int                `json:"lines"`   // 全文行數
	Chars   int                `json:"chars"`   // 全文字數
	Outline []model.DocSection `json:"outline,omitempty"`
	From    int                `json:"from,omitempty"`
	To      int                `json:"to,omitempty"`
	Text    *string            `json:"text,omitempty"`
	Matches []model.DocMatch   `json:"matches,omitempty"`
	Partial bool               `json:"partial"` // true = 只回了一部分
}

// 沒指定讀哪裡時附上的開頭行數
const docPreviewLines = 20

// ReadDoc 分段讀文件(權限同 GetDoc)
func (s *Service) ReadDoc(p *model.Principal, org, id string, in DocRead) (*DocView, error) {
	d, err := s.GetDoc(p, org, id)
	if err != nil {
		return nil, err
	}
	v := &DocView{ID: d.ID, Title: d.Title, Section: d.Section, Tab: d.Tab, Version: model.DocVersion(d.Body),
		Lines: model.DocLineCount(d.Body), Chars: utf8.RuneCountInString(d.Body), Partial: true}
	text := func(t string, a, b int) { v.Text, v.From, v.To = &t, a, b }
	switch {
	case in.Section != "":
		sec, err := model.FindDocSection(d.Body, in.Section)
		if err != nil {
			return nil, fail(404, "%v", err)
		}
		t, a, b := model.DocLines(d.Body, sec.Start, sec.End)
		text(t, a, b)
	case in.From > 0 || in.To > 0:
		t, a, b := model.DocLines(d.Body, in.From, in.To)
		text(t, a, b)
	case strings.TrimSpace(in.Grep) != "":
		c := in.Context
		if c <= 0 {
			c = 2
		}
		v.Matches = model.DocGrep(d.Body, in.Grep, min(c, 20), 30)
	case in.Outline:
		v.Outline = model.DocOutline(d.Body)
	default:
		v.Outline = model.DocOutline(d.Body)
		t, a, b := model.DocLines(d.Body, 1, docPreviewLines)
		text(t, a, b)
		v.Partial = b < v.Lines
	}
	if v.Text != nil && v.From == 1 && v.To >= v.Lines {
		v.Partial = false
	}
	return v, nil
}

// DocEdit 局部修改(三種擇一):Edits 字串取代;ReplaceSection 整段替換;AppendToSection 接在段尾(Content 自帶標題就是插入新段落)。
// BaseVersion 是讀到的版本(選填):版本不符時,字串取代在最新的內文上再套一次(找得到而且唯一才算),整段替換與段尾附加回 409
type DocEdit struct {
	BaseVersion     string           `json:"baseVersion,omitempty"`
	Edits           []model.TextEdit `json:"edits,omitempty"`
	ReplaceSection  string           `json:"replaceSection,omitempty"`
	AppendToSection string           `json:"appendToSection,omitempty"`
	Content         string           `json:"content,omitempty"`
}

// DocEditResult 局部修改的結果:只回改到的那幾行與新版本(不回全文)
type DocEditResult struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Lines     int    `json:"lines"`
	From      int    `json:"from"` // 改到的範圍(新內文的行號);沒改到是 0
	To        int    `json:"to"`
	Text      string `json:"text"`              // 改到的那幾行(前後各多一行)
	Rebased   bool   `json:"rebased,omitempty"` // 版本不符,在最新內文上套用
	Unchanged bool   `json:"unchanged,omitempty"`
}

// EditDoc 局部修改文件(權限、範本檢查同 UpdateDoc)
func (s *Service) EditDoc(p *model.Principal, org, id string, in DocEdit) (*DocEditResult, error) {
	g, err := s.docViewer(p, org)
	if err != nil {
		return nil, err
	}
	ops := 0
	for _, b := range []bool{len(in.Edits) > 0, in.ReplaceSection != "", in.AppendToSection != ""} {
		if b {
			ops++
		}
	}
	if ops != 1 {
		return nil, fail(400, "要給剛好一種修改:edits、replaceSection 或 appendToSection")
	}
	acc, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	cur, err := s.findDocFor(acc, g, org, id)
	if err != nil {
		return nil, err
	}
	ver := model.DocVersion(cur.Body)
	rebased := false
	if in.BaseVersion != "" && in.BaseVersion != ver {
		if len(in.Edits) == 0 {
			return nil, fail(409, "%s 已經被改過(目前版本 %s,你讀到的是 %s):請重讀大綱與那一段再改", cur.ID, ver, in.BaseVersion)
		}
		rebased = true
	}
	var body string
	switch {
	case len(in.Edits) > 0:
		body, err = model.ApplyTextEdits(cur.Body, in.Edits)
	case in.ReplaceSection != "":
		body, err = model.ReplaceDocSection(cur.Body, in.ReplaceSection, in.Content)
	default:
		body, err = model.AppendToDocSection(cur.Body, in.AppendToSection, in.Content)
	}
	if err != nil {
		code := 400
		if rebased {
			code = 409 // 在最新內文上套不上去:多半是別人改到同一處
		}
		return nil, fail(code, "%v", err)
	}
	res := &DocEditResult{ID: cur.ID, Rebased: rebased}
	if body == cur.Body {
		res.Version, res.Lines, res.Unchanged = ver, model.DocLineCount(body), true
		return res, nil
	}
	if _, err := s.updateDocLocked(p, g, org, cur.ID, cur, DocPatch{Body: &body}); err != nil {
		return nil, err
	}
	res.Version, res.Lines = model.DocVersion(body), model.DocLineCount(body)
	res.From, res.To = model.ChangedLines(cur.Body, body)
	if res.From > 0 {
		res.Text, _, _ = model.DocLines(body, res.From-1, max(res.To, res.From)+1)
	}
	return res, nil
}
