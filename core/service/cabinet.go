package service

import (
	"errors"
	"strings"
	"unicode/utf8"

	"omixor-core/model"
	"omixor-core/store"
)

// 文件櫃:Markdown 文件,依區塊與頁籤分類(結構與文件都依組織分)。
//
// 權限:
//   - 組織成員:讀全部文件與結構、新增、修改、搬移、刪除文件;作者一律是自己(伺服器管理員可以指定署名)
//   - 組織管理員:另外可以改結構(區塊與頁籤)、第一次搬資料的匯入
//   - 角色身分(角色憑證):只看得到 docAccess 能讀的文件,只能在 docAccess 可寫的位置新增、修改、搬移;
//     署名一律是角色;不能刪除文件、不能改結構
//
// 存檔前依範本檢查必填欄位(model.ValidateDocBody);改結構前檢查被拿掉的區塊、頁籤裡沒有文件(model.ValidateCabinetLayout)。

// DocInput 新增文件。Section 是區塊 id、Tab 是頁籤網址(空 = 區塊本身);名稱、代號的解析在介面(core/mcp)
type DocInput struct {
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Section string   `json:"section"`
	Tab     *string  `json:"tab,omitempty"`
	Tags    []string `json:"tags"`
	// Author 伺服器管理員(omixor-web 本機模式)可以指定署名;其他人一律是自己(角色憑證是角色)
	Author string `json:"author,omitempty"`
}

// DocPatch 修改文件:有給的欄位才改。給 Section 就是搬到 (Section, Tab)(Tab 沒給或空 = 區塊本身)
type DocPatch struct {
	Title      *string   `json:"title,omitempty"`
	Body       *string   `json:"body,omitempty"`
	AppendBody *string   `json:"appendBody,omitempty"` // 接在內文後面(中間空一行)
	Section    *string   `json:"section,omitempty"`
	Tab        *string   `json:"tab,omitempty"`
	Tags       *[]string `json:"tags,omitempty"`
}

// CabinetImport 第一次搬資料用(例如 omixor-web 本機舊的 cabinet-layout.json、cabinet.json)
type CabinetImport struct {
	Layout []model.CabinetSection `json:"layout,omitempty"` // 沒給不動結構;組織已經存過結構就略過
	Docs   []model.CabinetDoc     `json:"docs,omitempty"`   // 新的在前;保留 id、作者與時間
}

type CabinetImportResult struct {
	Org string `json:"org"`
	// LayoutSaved 這次存了結構;LayoutSkipped 有給結構但組織已經有了,沒動
	LayoutSaved   bool `json:"layoutSaved"`
	LayoutSkipped bool `json:"layoutSkipped"`
	Docs          int  `json:"docs"`
}

func emptyTab(t *string) *string {
	if t == nil || strings.TrimSpace(*t) == "" {
		return nil
	}
	v := strings.TrimSpace(*t)
	return &v
}

func tabStr(t *string) string {
	if t == nil {
		return ""
	}
	return *t
}

// layoutOf 組織的結構;沒存過是空白結構(saved = false)
func (s *Service) layoutOf(org string) (model.Layout, bool, error) {
	l, err := s.Store.GetCabinetLayout(org)
	if errors.Is(err, store.ErrNotFound) {
		return model.BlankCabinetLayout(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return l, true, nil
}

// docViewer 呼叫者可以用這個組織的文件櫃嗎;角色身分回傳它的角色(用來依 docAccess 過濾)
func (s *Service) docViewer(p *model.Principal, org string) (*model.Agent, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	if p.Agent == nil {
		return nil, nil
	}
	g, err := s.Store.GetAgent(org, p.Agent.AgentID)
	if err != nil {
		return nil, fail(403, "找不到角色 %s", p.Agent.AgentID)
	}
	return g, nil
}

func (s *Service) findDoc(org, id string) (*model.CabinetDoc, error) {
	id = strings.TrimSpace(id)
	if d, err := s.Store.GetDoc(org, id); err == nil {
		return d, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	list, err := s.Store.ListDocs(org)
	if err != nil {
		return nil, err
	}
	for _, d := range list {
		if strings.EqualFold(d.ID, id) {
			return &d, nil
		}
	}
	return nil, fail(404, "找不到文件 %s", id)
}

// GetCabinet 結構與文件(新的在前);角色身分只看得到 docAccess 能讀的文件
func (s *Service) GetCabinet(p *model.Principal, org string) (*model.Cabinet, error) {
	g, err := s.docViewer(p, org)
	if err != nil {
		return nil, err
	}
	l, saved, err := s.layoutOf(org)
	if err != nil {
		return nil, err
	}
	docs, err := s.Store.ListDocs(org)
	if err != nil {
		return nil, err
	}
	if g != nil {
		kept := []model.CabinetDoc{}
		for _, d := range docs {
			if g.CanRead(d.Section, d.TabPath()) {
				kept = append(kept, d)
			}
		}
		docs = kept
	}
	return &model.Cabinet{Layout: l, Docs: docs, LayoutSaved: saved}, nil
}

// GetDoc 依 id 取文件(不分大小寫)
func (s *Service) GetDoc(p *model.Principal, org, id string) (*model.CabinetDoc, error) {
	g, err := s.docViewer(p, org)
	if err != nil {
		return nil, err
	}
	d, err := s.findDoc(org, id)
	if err != nil {
		return nil, err
	}
	if g != nil && !g.CanRead(d.Section, d.TabPath()) {
		l, _, _ := s.layoutOf(org)
		return nil, fail(403, "%s沒有權限讀「%s」的文件", g.Name, l.Location(d.Section, d.TabPath()))
	}
	return d, nil
}

// checkPlace 位置要在結構裡;角色身分要能寫
func checkPlace(l model.Layout, g *model.Agent, section string, tab *string) error {
	if !l.Has(section, tabStr(tab)) {
		if l.Section(section) == nil || l.Section(section).ID != section {
			return fail(400, "文件櫃沒有區塊 %s", section)
		}
		return fail(400, "區塊 %s 沒有頁籤 %s", section, tabStr(tab))
	}
	if g != nil && !g.CanWrite(section, tabStr(tab)) {
		return fail(403, "%s沒有權限寫入「%s」", g.Name, l.Location(section, tabStr(tab)))
	}
	return nil
}

func docAuthor(p *model.Principal, given string) string {
	if p.Agent != nil {
		return model.AgentPrefix + p.Agent.AgentID
	}
	return author(p, given)
}

// CreateDoc 新增文件(id 是 DOC-<最大號+1>)
func (s *Service) CreateDoc(p *model.Principal, org string, in DocInput) (*model.CabinetDoc, error) {
	g, err := s.docViewer(p, org)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, fail(400, "要給 title")
	}
	in.Section, in.Tab = strings.TrimSpace(in.Section), emptyTab(in.Tab)
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	l, _, err := s.layoutOf(org)
	if err != nil {
		return nil, err
	}
	if err := checkPlace(l, g, in.Section, in.Tab); err != nil {
		return nil, err
	}
	docs, err := s.Store.ListDocs(org)
	if err != nil {
		return nil, err
	}
	if e := model.ValidateDocBody(in.Body, docs); e != "" {
		return nil, fail(400, "%s", e)
	}
	now := s.mt()
	d := model.CabinetDoc{Title: in.Title, Body: in.Body, Section: in.Section, Tab: in.Tab, Tags: model.CleanLabels(in.Tags),
		Author: docAuthor(p, in.Author), CreatedAt: now, UpdatedAt: now}
	saved, err := s.Store.CreateDoc(org, d)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "doc.create", org, map[string]any{"id": saved.ID})
	return &saved, nil
}

// UpdateDoc 修改或搬移文件;內容沒變不更新時間
func (s *Service) UpdateDoc(p *model.Principal, org, id string, in DocPatch) (*model.CabinetDoc, error) {
	g, err := s.docViewer(p, org)
	if err != nil {
		return nil, err
	}
	if in.Title != nil && strings.TrimSpace(*in.Title) == "" {
		return nil, fail(400, "標題不能是空的")
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	cur, err := s.findDoc(org, id)
	if err != nil {
		return nil, err
	}
	return s.updateDocLocked(p, g, org, id, cur, in)
}

// updateDocLocked UpdateDoc 的本體(呼叫時要拿著 cabinetMu;g 是角色身分的角色,人是 nil)
func (s *Service) updateDocLocked(p *model.Principal, g *model.Agent, org, id string, cur *model.CabinetDoc, in DocPatch) (*model.CabinetDoc, error) {
	l, _, err := s.layoutOf(org)
	if err != nil {
		return nil, err
	}
	if g != nil {
		if !g.CanRead(cur.Section, cur.TabPath()) {
			return nil, fail(404, "找不到文件 %s", id)
		}
		if !g.CanWrite(cur.Section, cur.TabPath()) {
			return nil, fail(403, "%s沒有權限寫入「%s」", g.Name, l.Location(cur.Section, cur.TabPath()))
		}
	}
	d := cur.Clone()
	if in.Section != nil {
		sec, tab := strings.TrimSpace(*in.Section), emptyTab(in.Tab)
		if err := checkPlace(l, g, sec, tab); err != nil {
			return nil, err
		}
		d.Section, d.Tab = sec, tab
	}
	if in.Title != nil {
		d.Title = *in.Title
	}
	if in.Body != nil {
		d.Body = *in.Body
	}
	if in.AppendBody != nil && *in.AppendBody != "" {
		if d.Body != "" {
			d.Body += "\n\n"
		}
		d.Body += *in.AppendBody
	}
	if in.Tags != nil {
		d.Tags = *in.Tags
	}
	d.Tags = model.CleanLabels(d.Tags)
	docs, err := s.Store.ListDocs(org)
	if err != nil {
		return nil, err
	}
	if e := model.ValidateDocBody(d.Body, docs); e != "" {
		return nil, fail(400, "%s", e)
	}
	if sameDoc(d, *cur) {
		return &d, nil
	}
	d.UpdatedAt = s.mt()
	if err := s.Store.UpdateDoc(org, d); err != nil {
		return nil, err
	}
	s.Audit(p, "doc.update", org, map[string]any{"id": d.ID})
	return &d, nil
}

func sameDoc(a, b model.CabinetDoc) bool {
	if a.Title != b.Title || a.Body != b.Body || a.Section != b.Section || a.TabPath() != b.TabPath() || len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	return true
}

// DeleteDoc 刪除文件(組織成員;角色身分不行)
func (s *Service) DeleteDoc(p *model.Principal, org, id string) error {
	if err := notAgent(p); err != nil {
		return err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return err
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	d, err := s.findDoc(org, id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteDoc(org, d.ID); errors.Is(err, store.ErrNotFound) {
		return fail(404, "找不到文件 %s", id)
	} else if err != nil {
		return err
	}
	s.Audit(p, "doc.delete", org, map[string]any{"id": d.ID})
	return nil
}

// SaveCabinetLayout 改結構(組織管理員):先檢查(被拿掉的區塊、頁籤裡不能還有文件)
func (s *Service) SaveCabinetLayout(p *model.Principal, org string, layout []model.CabinetSection) ([]model.CabinetSection, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if layout == nil {
		return nil, fail(400, "要給 layout")
	}
	for i := range layout {
		if layout[i].Tabs == nil {
			layout[i].Tabs = []model.CabinetTab{}
		}
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	docs, err := s.Store.ListDocs(org)
	if err != nil {
		return nil, err
	}
	if e := model.ValidateCabinetLayout(layout, docs); e != "" {
		return nil, fail(400, "%s", e)
	}
	if err := s.Store.SaveCabinetLayout(org, layout); err != nil {
		return nil, err
	}
	s.Audit(p, "cabinet.layout", org, map[string]any{"sections": len(layout)})
	return layout, nil
}

// ImportCabinet 第一次搬資料用(組織管理員):結構只在組織還沒存過時寫入;文件保留 id、作者與時間,
// 組織已經有文件就整個拒絕(409)
func (s *Service) ImportCabinet(p *model.Principal, org string, in CabinetImport) (*CabinetImportResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if in.Layout == nil && len(in.Docs) == 0 {
		return nil, fail(400, "沒有要匯入的結構或文件")
	}
	seen := map[string]bool{}
	docs := []model.CabinetDoc{}
	for _, d := range in.Docs {
		if d.Number() == 0 || strings.TrimSpace(d.Title) == "" || d.Section == "" {
			return nil, fail(400, "每一份文件都要有 id(DOC-<n>)、title 與 section")
		}
		if seen[strings.ToUpper(d.ID)] {
			return nil, fail(400, "id 重複:%s", d.ID)
		}
		seen[strings.ToUpper(d.ID)] = true
		d.Tab = emptyTab(d.Tab)
		if d.Tags == nil {
			d.Tags = []string{}
		}
		if d.CreatedAt.IsZero() {
			d.CreatedAt = s.mt()
		}
		if d.UpdatedAt.IsZero() {
			d.UpdatedAt = d.CreatedAt
		}
		docs = append(docs, d)
	}
	if in.Layout != nil {
		for i := range in.Layout {
			if in.Layout[i].Tabs == nil {
				in.Layout[i].Tabs = []model.CabinetTab{}
			}
		}
		if e := model.ValidateCabinetLayout(in.Layout, nil); e != "" {
			return nil, fail(400, "%s", e)
		}
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	res := &CabinetImportResult{Org: org}
	if len(docs) > 0 {
		cur, err := s.Store.ListDocs(org)
		if err != nil {
			return nil, err
		}
		if len(cur) > 0 {
			return nil, fail(409, "%s 的文件櫃已經有 %d 份文件;匯入只在第一次搬資料時用", org, len(cur))
		}
	}
	// 先匯入文件再存結構:文件被拒(409)時什麼都沒寫,之後可以整個重來
	if len(docs) > 0 {
		n, err := s.Store.ImportDocs(org, docs)
		if err != nil {
			return nil, fail(409, "%v", err)
		}
		res.Docs = n
	}
	if in.Layout != nil {
		if _, saved, err := s.layoutOf(org); err != nil {
			return nil, err
		} else if saved {
			res.LayoutSkipped = true
		} else {
			if err := s.Store.SaveCabinetLayout(org, in.Layout); err != nil {
				return nil, err
			}
			res.LayoutSaved = true
		}
	}
	s.Audit(p, "cabinet.import", org, map[string]any{"docs": res.Docs, "layout": res.LayoutSaved})
	return res, nil
}

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
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	cur, err := s.findDoc(org, id)
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
