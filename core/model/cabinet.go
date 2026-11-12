package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// 文件櫃:純文字(Markdown)文件,依研發基地的區塊與頁籤分類(同 Swift 版 Cabinet/Cabinet.swift、omixor-web 原本的 cabinet.go)。
// 一個組織一份結構(區塊與頁籤)與一份文件清單。JSON 欄位同 omixor-web 原本的 cabinet.json / cabinet-layout.json。
//
// 角色的文件權限(Agent.DocAccess)的 key 是區塊 id 或頁籤網址(Path);權限判斷見 Agent.Access。

// DocPrefix 文件 id 的前綴:DOC-<n>(一個組織內流水號)
const DocPrefix = "DOC-"

type CabinetTab struct {
	Code  *string `json:"code,omitempty"`
	Group *string `json:"group,omitempty"`
	Label string  `json:"label"`
	Path  string  `json:"path"` // 頁籤 id:用 / 開頭的網址
}

// Title「L0 AI 工具介紹」;沒有代號就是名稱
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

// UnmarshalJSON 沒有 tabs 時是空陣列
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

// BlankCabinetLayout 新的空白組織(還沒存過結構)用的結構
func BlankCabinetLayout() []CabinetSection {
	return []CabinetSection{
		{ID: "overview", Label: "總覽", Tabs: []CabinetTab{}},
		{ID: "docs", Label: "文件", Tabs: []CabinetTab{}},
	}
}

// CloneLayout 深拷貝結構
func CloneLayout(l []CabinetSection) []CabinetSection {
	out := make([]CabinetSection, 0, len(l))
	for _, s := range l {
		s.Tabs = append([]CabinetTab{}, s.Tabs...)
		out = append(out, s)
	}
	return out
}

// CabinetDoc 一份文件。Tab 是頁籤網址(nil = 放在區塊本身)
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

// Number DOC-12 → 12(格式不對回 0)
func (d CabinetDoc) Number() int {
	rest, ok := strings.CutPrefix(strings.ToUpper(d.ID), DocPrefix)
	if !ok || rest == "" {
		return 0
	}
	n := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// TabPath Tab 的值(nil 是 "")
func (d CabinetDoc) TabPath() string {
	if d.Tab == nil {
		return ""
	}
	return *d.Tab
}

// Clone 深拷貝
func (d CabinetDoc) Clone() CabinetDoc {
	d.Tags = append([]string{}, d.Tags...)
	if d.Tab != nil {
		t := *d.Tab
		d.Tab = &t
	}
	return d
}

// Cabinet 一個組織的文件櫃:結構與文件(新的在前)。LayoutSaved 是這個組織有沒有存過結構(沒有時 Layout 是空白結構)
type Cabinet struct {
	Layout      []CabinetSection `json:"layout"`
	Docs        []CabinetDoc     `json:"docs"`
	LayoutSaved bool             `json:"layoutSaved"`
}

// ---- 結構的查找(依 id、名稱、網址、代號) ----

// Layout 結構的查找方法
type Layout []CabinetSection

// Section 依 id 或名稱找區塊
func (l Layout) Section(idOrLabel string) *CabinetSection {
	for i, s := range l {
		if s.ID == idOrLabel || s.Label == idOrLabel {
			return &l[i]
		}
	}
	return nil
}

// TabByPath 依網址找頁籤
func (l Layout) TabByPath(path string) *CabinetTab {
	if path == "" {
		return nil
	}
	for _, s := range l {
		for i, t := range s.Tabs {
			if t.Path == path {
				return &s.Tabs[i]
			}
		}
	}
	return nil
}

// Has 區塊 id 存在,且頁籤(非空時)是這個區塊的
func (l Layout) Has(section, tab string) bool {
	for _, s := range l {
		if s.ID != section {
			continue
		}
		if tab == "" {
			return true
		}
		for _, t := range s.Tabs {
			if t.Path == tab {
				return true
			}
		}
		return false
	}
	return false
}

// Resolve 依 id、名稱或網址找區塊;頁籤可給網址、名稱、代號或「代號 名稱」(同 Swift 的 CabinetLayout.resolve)。
// 找不到回傳給人看的錯誤訊息
func (l Layout) Resolve(section, tab *string) (*CabinetSection, *CabinetTab, string) {
	if tab != nil && *tab != "" {
		candidates := []CabinetSection(l)
		if section != nil {
			if s := l.Section(*section); s != nil {
				candidates = []CabinetSection{*s}
			}
		}
		for _, s := range candidates {
			for _, t := range s.Tabs {
				if t.Path == *tab || t.Label == *tab || (t.Code != nil && *t.Code == *tab) || t.Title() == *tab {
					s, t := s, t
					return &s, &t, ""
				}
			}
		}
		msg := "找不到頁籤「" + *tab + "」"
		if section != nil {
			msg += "(區塊 " + *section + ")"
		}
		return nil, nil, msg
	}
	if section != nil {
		if s := l.Section(*section); s != nil {
			c := *s
			return &c, nil, ""
		}
		for _, s := range l {
			if s.Path == *section {
				return &s, nil, ""
			}
		}
	}
	ids := []string{}
	for _, s := range l {
		ids = append(ids, s.ID)
	}
	return nil, nil, "要指定區塊(" + strings.Join(ids, "、") + ")"
}

// Location「課程 › L0 AI 工具介紹」
func (l Layout) Location(section, tab string) string {
	s := section
	if sec := l.Section(section); sec != nil {
		s = sec.Label
	}
	if t := l.TabByPath(tab); t != nil {
		return s + " › " + t.Title()
	}
	return s
}

// ResolveAccessKey 角色 docAccess 的 key:區塊 id 或名稱 → 區塊 id;頁籤網址、名稱或代號 → 頁籤網址。對不到回錯誤訊息
func (l Layout) ResolveAccessKey(k string) (string, string) {
	if sec := l.Section(k); sec != nil {
		return sec.ID, ""
	}
	if _, t, e := l.Resolve(nil, &k); e == "" && t != nil {
		return t.Path, ""
	}
	return "", "文件櫃沒有「" + k + "」這個區塊或頁籤"
}

func trimWS(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return r == '\t' || unicode.Is(unicode.Zs, r) })
}

// ValidateCabinetLayout 檢查新結構:名稱不空、id 不重複、頁籤用 / 開頭,被拿掉的區塊/頁籤裡不能還有文件
// (同 Swift 的 Cabinet.validate);沒問題回傳 ""
func ValidateCabinetLayout(sections []CabinetSection, docs []CabinetDoc) string {
	ids := map[string]bool{}
	for _, s := range sections {
		if trimWS(s.Label) == "" {
			return "區塊名稱不能是空的"
		}
		if ids[s.ID] {
			return "區塊 id 重複:" + s.ID
		}
		ids[s.ID] = true
		for _, t := range s.Tabs {
			if trimWS(t.Label) == "" {
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
	for _, d := range docs {
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

// ParseDocJSON 寬鬆讀舊的 cabinet.json 的一份文件(同 Swift 的手寫解碼):id、title、section 必須有,其他缺了用預設值
// (author 預設 defaultAuthor,時間預設 now)
func ParseDocJSON(b []byte, defaultAuthor string, now Time) (CabinetDoc, error) {
	type raw CabinetDoc
	x := raw{Author: defaultAuthor}
	if err := json.Unmarshal(b, &x); err != nil {
		return CabinetDoc{}, err
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(b, &keys)
	for _, k := range []string{"id", "title", "section"} {
		if _, ok := keys[k]; !ok {
			return CabinetDoc{}, fmt.Errorf("文件缺少 %s", k)
		}
	}
	if _, ok := keys["createdAt"]; !ok {
		x.CreatedAt = now
	}
	if _, ok := keys["updatedAt"]; !ok {
		x.UpdatedAt = x.CreatedAt
	}
	if x.Tags == nil {
		x.Tags = []string{}
	}
	return CabinetDoc(x), nil
}
