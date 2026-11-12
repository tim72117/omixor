package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"omixor-web/internal/templates"
	"unicode"
)

// 文件櫃:同 Swift 版 Cabinet/Cabinet.swift。純文字(Markdown)文件,依研發基地的區塊與頁籤分類。
// 文件存在 cabinet.json,結構存在 cabinet-layout.json(沒有就用 evoleap 研發基地的結構)。

type CabinetTab struct {
	Code  *string `json:"code,omitempty"`
	Group *string `json:"group,omitempty"`
	Label string  `json:"label"`
	Path  string  `json:"path"`
}

// Title L0 AI 工具介紹;沒有代號就是名稱
func (t CabinetTab) Title() string {
	if t.Code != nil {
		return *t.Code + " " + t.Label
	}
	return t.Label
}

type CabinetSection struct {
	ID    string       `json:"id"`
	Label string       `json:"label"`
	Path  string       `json:"path"`
	Tabs  []CabinetTab `json:"tabs"`
}

func (s *CabinetSection) UnmarshalJSON(b []byte) error {
	type raw CabinetSection
	var x raw
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	if x.Tabs == nil {
		x.Tabs = []CabinetTab{}
	}
	*s = CabinetSection(x)
	return nil
}

func cabSP(s string) *string { return &s }

// BlankCabinetLayout 新的空白組織用的結構
func BlankCabinetLayout() []CabinetSection {
	return []CabinetSection{
		{ID: "overview", Label: "總覽", Tabs: []CabinetTab{}},
		{ID: "docs", Label: "文件", Tabs: []CabinetTab{}},
	}
}

// TemplateCabinetLayout 範本的文件櫃結構(templates/cabinet/,一個區塊一個檔);沒開範本就是空白的結構
func TemplateCabinetLayout() []CabinetSection {
	var layout []CabinetSection
	if err := json.Unmarshal(templates.Array("cabinet"), &layout); err != nil || len(layout) == 0 {
		return BlankCabinetLayout()
	}
	return layout
}

type CabinetDoc struct {
	Author    string   `json:"author"`
	Body      string   `json:"body"`
	CreatedAt Time     `json:"createdAt"`
	ID        string   `json:"id"`
	Section   string   `json:"section"`
	Tab       *string  `json:"tab,omitempty"`
	Tags      []string `json:"tags"`
	Title     string   `json:"title"`
	UpdatedAt Time     `json:"updatedAt"`
}

// UnmarshalJSON 同 Swift 的手寫解碼:id、title、section 必須有,其他缺了用預設值
func (d *CabinetDoc) UnmarshalJSON(b []byte) error {
	type raw CabinetDoc
	x := raw{Author: MCPAuthor}
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(b, &keys)
	for _, k := range []string{"id", "title", "section"} {
		if _, ok := keys[k]; !ok {
			return fmt.Errorf("文件缺少 %s", k)
		}
	}
	if _, ok := keys["createdAt"]; !ok {
		x.CreatedAt = nowPrecise()
	}
	if _, ok := keys["updatedAt"]; !ok {
		x.UpdatedAt = x.CreatedAt
	}
	if x.Tags == nil {
		x.Tags = []string{}
	}
	*d = CabinetDoc(x)
	return nil
}

func (d CabinetDoc) Number() int { return iaIDNumber(d.ID) }

type cabinetState struct {
	Layout []CabinetSection
	Docs   []CabinetDoc
}

func (a *App) cabinetSt() *cabinetState {
	if s, ok := a.Ext["cabinet"].(*cabinetState); ok {
		return s
	}
	s := &cabinetState{}
	a.Ext["cabinet"] = s
	a.loadCabinet()
	return s
}

// CabinetLayout 目前組織的文件櫃結構(呼叫時要拿著 Mu)
func (a *App) CabinetLayout() []CabinetSection { return a.cabinetSt().Layout }

// CabinetDocs 全部文件(最新的在前)
func (a *App) CabinetDocs() []CabinetDoc { return a.cabinetSt().Docs }

func (a *App) cabinetPath() string       { return filepath.Join(a.DataDir(), "cabinet.json") }
func (a *App) cabinetLayoutPath() string { return filepath.Join(a.DataDir(), "cabinet-layout.json") }

// 檔案存在但讀不出來時不覆蓋,先改名保留(同 Swift)
func (a *App) loadCabinet() {
	s := a.cabinetSt()
	var layout []CabinetSection
	if err := ReadJSON(a.cabinetLayoutPath(), &layout); err == nil && layout != nil {
		s.Layout = layout
	} else {
		s.Layout = TemplateCabinetLayout()
	}
	path := a.cabinetPath()
	b, err := ReadData(path)
	if err != nil {
		s.Docs = []CabinetDoc{}
		return
	}
	var docs []CabinetDoc
	if err := json.Unmarshal(b, &docs); err != nil || docs == nil {
		keep := strings.TrimSuffix(path, ".json") + fmt.Sprintf(".unreadable-%d.json", time.Now().Unix())
		_ = RenameData(path, keep)
		docs = []CabinetDoc{}
	}
	s.Docs = docs
}

func (a *App) saveCabinet() { _ = WriteJSON(a.cabinetPath(), a.cabinetSt().Docs) }

// CabinetSectionByID 依 id 或名稱找區塊
func (a *App) CabinetSectionByID(id string) *CabinetSection {
	for i, s := range a.CabinetLayout() {
		if s.ID == id || s.Label == id {
			return &a.CabinetLayout()[i]
		}
	}
	return nil
}

// CabinetTabByPath 依網址找頁籤
func (a *App) CabinetTabByPath(path *string) *CabinetTab {
	if path == nil {
		return nil
	}
	for _, s := range a.CabinetLayout() {
		for i, t := range s.Tabs {
			if t.Path == *path {
				return &s.Tabs[i]
			}
		}
	}
	return nil
}

// ResolveCabinet 依 id、名稱或網址找區塊;頁籤可給網址、名稱或代號(同 Swift 的 CabinetLayout.resolve)
func (a *App) ResolveCabinet(section, tab *string) (*CabinetSection, *CabinetTab, error) {
	if tab != nil && *tab != "" {
		candidates := a.CabinetLayout()
		if section != nil {
			if s := a.CabinetSectionByID(*section); s != nil {
				candidates = []CabinetSection{*s}
			}
		}
		for _, s := range candidates {
			for _, t := range s.Tabs {
				if t.Path == *tab || t.Label == *tab || (t.Code != nil && *t.Code == *tab) || t.Title() == *tab {
					s, t := s, t
					return &s, &t, nil
				}
			}
		}
		msg := "找不到頁籤「" + *tab + "」"
		if section != nil {
			msg += "(區塊 " + *section + ")"
		}
		return nil, nil, UserError{msg}
	}
	if section != nil {
		if s := a.CabinetSectionByID(*section); s != nil {
			c := *s
			return &c, nil, nil
		}
		for _, s := range a.CabinetLayout() {
			if s.Path == *section {
				return &s, nil, nil
			}
		}
	}
	ids := []string{}
	for _, s := range a.CabinetLayout() {
		ids = append(ids, s.ID)
	}
	return nil, nil, UserError{"要指定區塊(" + strings.Join(ids, "、") + ")"}
}

// DocLocation 「課程 › L0 AI 工具介紹」
func (a *App) DocLocation(section string, tab *string) string {
	s := section
	if sec := a.CabinetSectionByID(section); sec != nil {
		s = sec.Label
	}
	if t := a.CabinetTabByPath(tab); t != nil {
		return s + " › " + t.Title()
	}
	return s
}

// FindDoc 依 id 找文件(不分大小寫)
func (a *App) FindDoc(id string) *CabinetDoc {
	for _, d := range a.CabinetDocs() {
		if strings.EqualFold(d.ID, id) {
			c := d
			c.Tags = append([]string{}, d.Tags...)
			return &c
		}
	}
	return nil
}

// CreateDoc 新增文件並記到動態牆(同 Swift 的 Cabinet.create)
func (a *App) CreateDoc(title, body, section string, tab *string, tags []string, author string) CabinetDoc {
	s := a.cabinetSt()
	num := 0
	for _, d := range s.Docs {
		num = max(num, d.Number())
	}
	now := nowPrecise()
	d := CabinetDoc{ID: "DOC-" + strconv.Itoa(num+1), Title: title, Body: body, Section: section, Tab: tab,
		Tags: CleanLabels(tags), Author: author, CreatedAt: now, UpdatedAt: now}
	s.Docs = append([]CabinetDoc{d}, s.Docs...)
	a.saveCabinet()
	a.Log(AuthorActor(author), "archivebox", "文件櫃新增 "+d.ID+"「"+title+"」("+a.DocLocation(d.Section, d.Tab)+")", "cabinet")
	go a.Changed("cabinet")
	return d
}

// UpdateDoc 修改文件(不記動態牆,同 Swift)
func (a *App) UpdateDoc(id string, change func(*CabinetDoc)) *CabinetDoc {
	s := a.cabinetSt()
	for i := range s.Docs {
		if !strings.EqualFold(s.Docs[i].ID, id) {
			continue
		}
		before := s.Docs[i]
		d := before
		d.Tags = append([]string{}, before.Tags...)
		change(&d)
		d.Tags = CleanLabels(d.Tags)
		if string(Marshal(d)) == string(Marshal(before)) {
			return &d
		}
		d.UpdatedAt = nowPrecise()
		s.Docs[i] = d
		a.saveCabinet()
		go a.Changed("cabinet")
		return &d
	}
	return nil
}

// DeleteDoc 刪除文件(id 完全相同才刪)
func (a *App) DeleteDoc(id string) {
	s := a.cabinetSt()
	kept := []CabinetDoc{}
	for _, d := range s.Docs {
		if d.ID != id {
			kept = append(kept, d)
		}
	}
	s.Docs = kept
	a.saveCabinet()
	go a.Changed("cabinet")
}

func cabTrimWS(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return r == '\t' || unicode.Is(unicode.Zs, r) })
}

// ValidateCabinetLayout 檢查新結構:id 不重複,被拿掉的區塊/頁籤裡不能還有文件(同 Swift 的 Cabinet.validate)
func (a *App) ValidateCabinetLayout(sections []CabinetSection) string {
	ids := map[string]bool{}
	for _, s := range sections {
		if cabTrimWS(s.Label) == "" {
			return "區塊名稱不能是空的"
		}
		if ids[s.ID] {
			return "區塊 id 重複:" + s.ID
		}
		ids[s.ID] = true
		for _, t := range s.Tabs {
			if cabTrimWS(t.Label) == "" {
				return "「" + s.Label + "」有頁籤沒有名稱"
			}
			if !strings.HasPrefix(t.Path, "/") {
				return "頁籤 id 要用 / 開頭:" + t.Path
			}
			if ids[t.Path] {
				return "頁籤 id 重複:" + t.Path
			}
			ids[t.Path] = true
		}
	}
	for _, d := range a.CabinetDocs() {
		var sec *CabinetSection
		for i := range sections {
			if sections[i].ID == d.Section {
				sec = &sections[i]
				break
			}
		}
		if sec == nil {
			return "「" + d.Title + "」還在被拿掉的區塊裡,先搬走或刪除"
		}
		if d.Tab != nil {
			found := false
			for _, t := range sec.Tabs {
				found = found || t.Path == *d.Tab
			}
			if !found {
				return "「" + d.Title + "」還在被拿掉的頁籤裡,先搬走或刪除"
			}
		}
	}
	return ""
}

// SaveCabinetLayout 存新結構
func (a *App) SaveCabinetLayout(sections []CabinetSection) {
	a.cabinetSt().Layout = sections
	_ = WriteJSON(a.cabinetLayoutPath(), sections)
	go a.Changed("cabinet")
}

func init() {
	OnOrgLoad(func(a *App) {
		a.cabinetSt()
		a.loadCabinet()
	})
	OnOrgCreate(func(a *App, dir, template string) {
		layout := BlankCabinetLayout()
		if template == "copyCurrent" {
			layout = a.CabinetLayout()
		}
		_ = WriteJSON(filepath.Join(dir, "cabinet-layout.json"), layout)
	})
	RegisterView("cabinet", func(a *App) any {
		return map[string]any{"layout": a.CabinetLayout(), "docs": a.CabinetDocs()}
	})
	RegisterAction("cabinet_layout_save", func(a *App, args map[string]any) (any, error) {
		var layout []CabinetSection
		if err := ReadInto(args["layout"], &layout); err != nil {
			return nil, err
		}
		if layout == nil {
			return nil, UserError{"要給 layout"}
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if e := a.ValidateCabinetLayout(layout); e != "" {
			return nil, UserError{e}
		}
		a.SaveCabinetLayout(layout)
		return layout, nil
	})
	RegisterAction("cabinet_doc_delete", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		a.DeleteDoc(agentArg(args, "id"))
		return nil, nil
	})
}
