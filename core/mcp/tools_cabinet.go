package mcp

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"omixor-core/model"
	"omixor-core/service"
)

// 文件櫃工具:cabinet、docs、doc_save(名稱、參數、輸出與錯誤訊息同 Swift 版 MCPHandler 與 omixor-web 原本的實作)。
// 雲端(server)與 omixor-web 的管理端點、角色端點共用這一份實作;資料操作都透過 Backend(Local / Remote)。
//
// 權限:
//   - 角色端點:Backend 是角色身分,service 依憑證角色的 docAccess 強制(看不到、寫不了範圍外的文件);
//     呼叫端帶的 agent 會被端點強制成憑證的角色,這裡的檢查只是給出看得懂的錯誤訊息
//   - 管理端點:呼叫者本人的權限;有給 agent 時依該角色的 docAccess 過濾與檢查,署名是該角色

// cabinetOf 這個連線的文件櫃(結構與呼叫者看得到的文件);Backend 不支援時回錯誤
func cabinetOf(e Env) (*model.Cabinet, error) {
	c, err := e.Backend().GetCabinet(e.Org())
	if err != nil {
		return nil, terr(err)
	}
	return c, nil
}

// cabLayout 結構;取不到(例如 Backend 不支援文件櫃)回 nil
func cabLayout(e Env) model.Layout {
	c, err := e.Backend().GetCabinet(e.Org())
	if err != nil {
		return nil
	}
	return c.Layout
}

// cabAgent 有給 agent 參數時找角色(找不到回錯誤);沒給回 nil
func cabAgent(e Env, c *Call) (*model.Agent, error) {
	name := c.Args.Str("agent")
	if name == "" {
		return nil, nil
	}
	g, err := e.Backend().GetAgent(e.Org(), name)
	if err != nil {
		return nil, Errorf("找不到角色 %s", name)
	}
	return g, nil
}

// DocScope 角色的存取範圍,給人看得懂的樣子(依 key 排序;對不到區塊或頁籤的略過)
func DocScope(l model.Layout, g model.Agent) []any {
	keys := make([]string, 0, len(g.DocAccess))
	for k := range g.DocAccess {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []any{}
	for _, k := range keys {
		v := g.DocAccess[k]
		if sec := l.Section(k); sec != nil {
			out = append(out, map[string]any{"section": sec.ID, "label": sec.Label, "access": v})
		} else if t := l.TabByPath(k); t != nil {
			out = append(out, map[string]any{"tab": t.Path, "label": t.Title(), "access": v})
		}
	}
	return out
}

func docJSON(e Env, l model.Layout, d model.CabinetDoc, full bool) map[string]any {
	o := map[string]any{"id": d.ID, "title": d.Title, "section": d.Section, "location": l.Location(d.Section, d.TabPath()),
		"tags": d.Tags, "author": e.AuthorName(d.Author), "updatedAt": iso(d.UpdatedAt.Time)}
	if d.Tab != nil {
		o["tab"] = *d.Tab
	}
	if full {
		o["body"] = d.Body
	} else {
		o["chars"] = utf8.RuneCountInString(d.Body)
	}
	return o
}

func sameTab(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func tabPath(t *model.CabinetTab) *string {
	if t == nil {
		return nil
	}
	p := t.Path
	return &p
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// cabinetSections 文件櫃的區塊與頁籤;給角色時只列它能存取的位置並標出權限
func cabinetSections(cab *model.Cabinet, g *model.Agent) []any {
	count := func(s string, t *string) int {
		n := 0
		for _, d := range cab.Docs {
			if d.Section == s && sameTab(d.Tab, t) {
				n++
			}
		}
		return n
	}
	out := []any{}
	for _, s := range cab.Layout {
		tabs := []any{}
		for _, t := range s.Tabs {
			acc := "write"
			if g != nil {
				acc = g.Access(s.ID, t.Path)
				if acc == "" {
					continue
				}
			}
			p := t.Path
			tabs = append(tabs, map[string]any{"tab": t.Path, "label": t.Title(), "docs": count(s.ID, &p), "access": acc})
		}
		secAcc := "write"
		if g != nil {
			secAcc = g.Access(s.ID, "")
			if secAcc == "" && len(tabs) == 0 {
				continue
			}
			if secAcc == "" {
				secAcc = "none"
			}
		}
		out = append(out, map[string]any{"section": s.ID, "label": s.Label, "studioPath": s.Path,
			"docs": count(s.ID, nil), "tabs": tabs, "access": secAcc})
	}
	return out
}

// cabinetContext 附加到 agent_context 的欄位:角色能存取的文件櫃區塊,與能讀的文件(依角色的 docAccess)。
// 取不到文件櫃(Backend 不支援)回 nil
func cabinetContext(e Env, g model.Agent, includeBodies bool) map[string]any {
	cab, err := e.Backend().GetCabinet(e.Org())
	if err != nil {
		return nil
	}
	const bodyLimit = 20
	docs := []any{}
	docN := 0
	for _, d := range cab.Docs {
		if !g.CanRead(d.Section, d.TabPath()) {
			continue
		}
		docN++
		if len(docs) >= 50 {
			continue
		}
		o := docJSON(e, cab.Layout, d, includeBodies && len(docs) < bodyLimit)
		o["access"] = "read"
		if g.CanWrite(d.Section, d.TabPath()) {
			o["access"] = "write"
		}
		docs = append(docs, o)
	}
	return map[string]any{
		"cabinet": cabinetSections(cab, &g),
		"docs":    map[string]any{"total": docN, "docs": docs},
	}
}

// RegisterCabinetTools 登記文件櫃工具:cabinet、docs、doc_save。執行環境(Call.Ctx)要實作 AgentEnv。
func RegisterCabinetTools(s *Server) {
	s.Register("cabinet", func(c *Call) (any, error) {
		e := agentEnv(c)
		g, err := cabAgent(e, c)
		if err != nil {
			return nil, err
		}
		cab, err := cabinetOf(e)
		if err != nil {
			return nil, err
		}
		return map[string]any{"organization": e.OrgName(), "sections": cabinetSections(cab, g)}, nil
	})

	s.Register("docs", func(c *Call) (any, error) {
		e := agentEnv(c)
		g, err := cabAgent(e, c)
		if err != nil {
			return nil, err
		}
		visible := func(d model.CabinetDoc) bool { return g == nil || g.CanRead(d.Section, d.TabPath()) }
		if id := c.Args.Opt("id"); id != nil {
			d, err := e.Backend().GetDoc(e.Org(), *id)
			if err != nil {
				return nil, terr(err)
			}
			l := cabLayout(e)
			if !visible(*d) {
				return nil, Errorf("%s沒有權限讀「%s」的文件", g.Name, l.Location(d.Section, d.TabPath()))
			}
			// 分段讀:大綱、一段、幾行、搜尋;只給 id 時回全文,太長的只回大綱與開頭
			in := service.DocRead{Section: c.Args.Str("part"), Grep: c.Args.Str("grep")}
			in.Outline, _ = c.Args.Raw["outline"].(bool)
			in.From, _ = c.Args.Int("from")
			in.To, _ = c.Args.Int("to")
			in.Context, _ = c.Args.Int("context")
			full, _ := c.Args.Raw["full"].(bool)
			partial := in.Outline || in.Section != "" || in.From > 0 || in.To > 0 || in.Grep != ""
			if !partial && (full || utf8.RuneCountInString(d.Body) <= docFullLimit) {
				o := docJSON(e, l, *d, true)
				o["version"] = model.DocVersion(d.Body)
				return o, nil
			}
			v, err := e.Backend().ReadDoc(e.Org(), d.ID, in)
			if err != nil {
				return nil, terr(err)
			}
			o := docJSON(e, l, *d, false)
			for k, x := range map[string]any{"version": v.Version, "lines": v.Lines, "partial": v.Partial} {
				o[k] = x
			}
			if v.Outline != nil {
				o["outline"] = v.Outline
			}
			if v.Text != nil {
				o["text"], o["from"], o["to"] = *v.Text, v.From, v.To
			}
			if in.Grep != "" {
				o["matches"] = v.Matches
			}
			if !partial {
				o["note"] = fmt.Sprintf("文件很長(%d 字),只回大綱與開頭;用 part 讀一段、from/to 讀幾行,或帶 full 讀全文", o["chars"])
			}
			return o, nil
		}
		cab, err := cabinetOf(e)
		if err != nil {
			return nil, err
		}
		l := model.Layout(cab.Layout)
		list := []model.CabinetDoc{}
		for _, d := range cab.Docs {
			if visible(d) {
				list = append(list, d)
			}
		}
		if c.Args.Opt("section") != nil || c.Args.Opt("tab") != nil {
			sec, t, msg := l.Resolve(c.Args.Opt("section"), c.Args.Opt("tab"))
			if msg != "" {
				return nil, ToolError{Message: msg}
			}
			kept := []model.CabinetDoc{}
			for _, d := range list {
				if d.Section == sec.ID && (t == nil || sameTab(d.Tab, tabPath(t))) {
					kept = append(kept, d)
				}
			}
			list = kept
		}
		if q := strings.ToLower(c.Args.Str("query")); q != "" {
			kept := []model.CabinetDoc{}
			for _, d := range list {
				hit := strings.Contains(strings.ToLower(d.Title), q) || strings.Contains(strings.ToLower(d.Body), q)
				for _, tg := range d.Tags {
					hit = hit || strings.Contains(strings.ToLower(tg), q)
				}
				if hit {
					kept = append(kept, d)
				}
			}
			list = kept
		}
		out := []any{}
		for k, d := range list {
			if k >= 50 {
				break
			}
			out = append(out, docJSON(e, l, d, false))
		}
		return map[string]any{"total": len(list), "docs": out}, nil
	})

	s.Register("doc_save", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		g, err := cabAgent(e, c)
		if err != nil {
			return nil, err
		}
		author, err := e.Author(c.Args.Str("agent"))
		if err != nil {
			return nil, err
		}
		cab, err := cabinetOf(e)
		if err != nil {
			return nil, err
		}
		l := model.Layout(cab.Layout)
		checkWrite := func(s string, t *string) error {
			if g == nil || g.CanWrite(s, deref(t)) {
				return nil
			}
			labels := []string{}
			for _, x := range DocScope(l, *g) {
				m := x.(map[string]any)
				if m["access"] == "write" {
					labels = append(labels, m["label"].(string))
				}
			}
			return Errorf("%s沒有權限寫入「%s」;它可以寫的範圍:%s", g.Name, l.Location(s, deref(t)), strings.Join(labels, "、"))
		}
		if id := c.Args.Opt("id"); id != nil {
			d, err := bk.GetDoc(org, *id)
			if err != nil {
				return nil, terr(err)
			}
			if err := checkWrite(d.Section, d.Tab); err != nil {
				return nil, err
			}
			// 局部修改(edits、replace_part、append_to_part):只回改到的那幾行與新版本
			if ed, ok, err := docEditArgs(c); err != nil {
				return nil, err
			} else if ok {
				r, err := bk.EditDoc(org, d.ID, ed)
				if err != nil {
					return nil, terr(err)
				}
				return map[string]any{"id": r.ID, "version": r.Version, "lines": r.Lines, "from": r.From, "to": r.To,
					"text": r.Text, "rebased": r.Rebased, "unchanged": r.Unchanged}, nil
			}
			p := service.DocPatch{}
			if c.Args.Opt("section") != nil || c.Args.Opt("tab") != nil {
				sec, t, msg := l.Resolve(c.Args.Opt("section"), c.Args.Opt("tab"))
				if msg != "" {
					return nil, ToolError{Message: msg}
				}
				if err := checkWrite(sec.ID, tabPath(t)); err != nil {
					return nil, err
				}
				p.Section, p.Tab = &sec.ID, tabPath(t)
			}
			title := c.Args.Opt("title")
			if title != nil && *title == "" {
				return nil, ToolError{Message: "標題不能是空的"}
			}
			p.Title = title
			if b, ok := c.Args.RawString("body"); ok {
				p.Body = &b
			}
			if b, ok := c.Args.RawString("append_body"); ok && b != "" {
				p.AppendBody = &b
			}
			if tags, ok := c.Args.Strings("tags"); ok {
				p.Tags = &tags
			}
			updated, err := bk.UpdateDoc(org, d.ID, p)
			if err != nil {
				return nil, terr(err)
			}
			return docJSON(e, l, *updated, false), nil
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		sec, t, msg := l.Resolve(c.Args.Opt("section"), c.Args.Opt("tab"))
		if msg != "" {
			return nil, ToolError{Message: msg}
		}
		if err := checkWrite(sec.ID, tabPath(t)); err != nil {
			return nil, err
		}
		body, _ := c.Args.RawString("body")
		tags, _ := c.Args.Strings("tags")
		d, err := bk.CreateDoc(org, service.DocInput{Title: title, Body: body, Section: sec.ID, Tab: tabPath(t), Tags: tags, Author: author})
		if err != nil {
			return nil, terr(err)
		}
		return docJSON(e, l, *d, false), nil
	})

	s.Register("cabinet_layout_save", func(c *Call) (any, error) {
		e := agentEnv(c)
		cab, err := cabinetOf(e)
		if err != nil {
			return nil, err
		}
		layout, added, err := applyLayoutOps(model.CloneLayout(cab.Layout), c.Args.Raw)
		if err != nil {
			return nil, err
		}
		if _, err := e.Backend().SaveCabinetLayout(e.Org(), layout); err != nil {
			return nil, terr(err)
		}
		cab, err = cabinetOf(e)
		if err != nil {
			return nil, err
		}
		return map[string]any{"organization": e.OrgName(), "sections": cabinetSections(cab, nil), "added": added}, nil
	})
}

// findTab 依網址、名稱、代號或「代號 名稱」找頁籤,回傳指回 l 裡的那一個。
// section 有給就只找那個區塊(區塊找不到是錯誤,不退回全部找);對到多個頁籤時要求用網址指定,不猜
// 第二個回傳值是「完全對不到」(不是區塊找不到或對到多個)
func findTab(l []model.CabinetSection, section, key string) (*model.CabinetTab, bool, error) {
	secID := ""
	if section != "" {
		s := model.Layout(l).Section(section)
		if s == nil {
			return nil, false, Errorf("找不到區塊「%s」", section)
		}
		secID = s.ID
	}
	var hits []*model.CabinetTab
	for i := range l {
		if secID != "" && l[i].ID != secID {
			continue
		}
		for j := range l[i].Tabs {
			if t := &l[i].Tabs[j]; t.Path == key {
				return t, false, nil
			}
		}
	}
	for i := range l {
		if secID != "" && l[i].ID != secID {
			continue
		}
		for j := range l[i].Tabs {
			t := &l[i].Tabs[j]
			if t.Label == key || (t.Code != nil && *t.Code == key) || t.Title() == key {
				hits = append(hits, t)
			}
		}
	}
	switch len(hits) {
	case 0:
		if section != "" {
			return nil, true, Errorf("區塊「%s」沒有頁籤「%s」", section, key)
		}
		return nil, true, Errorf("找不到頁籤「%s」", key)
	case 1:
		return hits[0], false, nil
	}
	paths := make([]string, len(hits))
	for i, t := range hits {
		paths[i] = t.Path
	}
	return nil, false, Errorf("「%s」對到多個頁籤(%s),用頁籤網址指定", key, strings.Join(paths, "、"))
}

// sectionOfTab 頁籤所在的區塊
func sectionOfTab(l []model.CabinetSection, path string) *model.CabinetSection {
	for i := range l {
		for _, t := range l[i].Tabs {
			if t.Path == path {
				return &l[i]
			}
		}
	}
	return nil
}

// checkTabNames 頁籤代號在整個文件櫃不能重複(文件與角色權限都用代號找頁籤),名稱在同一個區塊裡不能重複;
// self 是正在改的頁籤網址(新增時是 "")
func checkTabNames(l []model.CabinetSection, sec *model.CabinetSection, self, label string, code *string) error {
	for _, s := range l {
		for _, t := range s.Tabs {
			if t.Path == self {
				continue
			}
			if code != nil && t.Code != nil && *t.Code == *code {
				return Errorf("頁籤代號「%s」已經用在「%s › %s」", *code, s.Label, t.Title())
			}
			if s.ID == sec.ID && t.Label == label {
				return Errorf("「%s」已經有頁籤「%s」", sec.Label, label)
			}
		}
	}
	return nil
}

// applyLayoutOps cabinet_layout_save 的參數套用到結構上(依序:add_sections、add_tabs、update、remove)。
// 只改記憶體裡的副本;檢查(名稱不空、id 不重複、移除的位置沒有文件)最後由 service 的 SaveCabinetLayout 做。
// added 是這次新增的區塊與頁籤(含自動編的 id),讓呼叫端接著用
func applyLayoutOps(l []model.CabinetSection, raw map[string]any) ([]model.CabinetSection, []any, error) {
	list := func(key string) ([]map[string]any, error) {
		v, ok := raw[key]
		if !ok || v == nil {
			return nil, nil
		}
		o, ok := objects(v)
		if !ok {
			return nil, Errorf("%s 要是物件陣列", key)
		}
		return o, nil
	}
	addSecs, err := list("add_sections")
	if err != nil {
		return nil, nil, err
	}
	addTabs, err := list("add_tabs")
	if err != nil {
		return nil, nil, err
	}
	updates, err := list("update")
	if err != nil {
		return nil, nil, err
	}
	removes, hasRemove := Args{Raw: raw}.Strings("remove")
	if _, ok := raw["remove"]; ok && !hasRemove {
		return nil, nil, Errorf("remove 要是字串陣列")
	}
	if len(addSecs) == 0 && len(addTabs) == 0 && len(updates) == 0 && len(removes) == 0 {
		return nil, nil, ToolError{Message: "要給 add_sections、add_tabs、update 或 remove"}
	}
	added := []any{}
	optStr := func(a Args, k string) *string {
		if v, ok := a.Raw[k].(string); ok {
			v = strings.TrimSpace(v)
			return &v
		}
		return nil
	}
	nilIfEmpty := func(p *string) *string {
		if p == nil || *p == "" {
			return nil
		}
		return p
	}
	usedTab := func(path string) bool { return model.Layout(l).TabByPath(path) != nil }

	for _, x := range addSecs {
		a := Args{Raw: x}
		label := strings.TrimSpace(a.Str("label"))
		if label == "" {
			return nil, nil, ToolError{Message: "add_sections 每個都要有 label"}
		}
		if model.Layout(l).Section(label) != nil {
			return nil, nil, Errorf("已經有區塊「%s」", label)
		}
		id := strings.TrimSpace(a.Str("id"))
		if id == "" {
			n := len(l) + 1
			for model.Layout(l).Section(fmt.Sprintf("s%d", n)) != nil {
				n++
			}
			id = fmt.Sprintf("s%d", n)
		} else if model.Layout(l).Section(id) != nil {
			return nil, nil, Errorf("區塊 id 重複:%s", id)
		}
		l = append(l, model.CabinetSection{ID: id, Label: label, Tabs: []model.CabinetTab{}})
		added = append(added, map[string]any{"section": id, "label": label})
	}

	for _, x := range addTabs {
		a := Args{Raw: x}
		secKey, label := strings.TrimSpace(a.Str("section")), strings.TrimSpace(a.Str("label"))
		if secKey == "" || label == "" {
			return nil, nil, ToolError{Message: "add_tabs 每個都要有 section 與 label"}
		}
		sec := model.Layout(l).Section(secKey)
		if sec == nil {
			return nil, nil, Errorf("找不到區塊「%s」", secKey)
		}
		code := nilIfEmpty(optStr(a, "code"))
		if err := checkTabNames(l, sec, "", label, code); err != nil {
			return nil, nil, err
		}
		path := strings.TrimSpace(a.Str("path"))
		if path == "" {
			n := len(sec.Tabs) + 1
			for usedTab(fmt.Sprintf("/%s/%d", sec.ID, n)) {
				n++
			}
			path = fmt.Sprintf("/%s/%d", sec.ID, n)
		} else {
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			if usedTab(path) {
				return nil, nil, Errorf("頁籤 id 重複:%s", path)
			}
		}
		t := model.CabinetTab{Path: path, Label: label, Code: code, Group: nilIfEmpty(optStr(a, "group"))}
		sec.Tabs = append(sec.Tabs, t)
		added = append(added, map[string]any{"section": sec.ID, "tab": path, "label": t.Title()})
	}

	for _, x := range updates {
		a := Args{Raw: x}
		label := optStr(a, "label")
		if label != nil && *label == "" {
			return nil, nil, ToolError{Message: "update 的 label 不能是空的"}
		}
		if tk := strings.TrimSpace(a.Str("tab")); tk != "" {
			t, _, err := findTab(l, strings.TrimSpace(a.Str("section")), tk)
			if err != nil {
				return nil, nil, err
			}
			newLabel, newCode := t.Label, t.Code
			if label != nil {
				newLabel = *label
			}
			if v := optStr(a, "code"); v != nil {
				newCode = nilIfEmpty(v)
			}
			if err := checkTabNames(l, sectionOfTab(l, t.Path), t.Path, newLabel, newCode); err != nil {
				return nil, nil, err
			}
			t.Label, t.Code = newLabel, newCode
			if v := optStr(a, "group"); v != nil {
				t.Group = nilIfEmpty(v)
			}
			continue
		}
		sk := strings.TrimSpace(a.Str("section"))
		if sk == "" {
			return nil, nil, ToolError{Message: "update 每個都要有 section 或 tab"}
		}
		sec := model.Layout(l).Section(sk)
		if sec == nil {
			return nil, nil, Errorf("找不到區塊「%s」", sk)
		}
		if label != nil {
			if o := model.Layout(l).Section(*label); o != nil && o.ID != sec.ID {
				return nil, nil, Errorf("已經有區塊「%s」", *label)
			}
			sec.Label = *label
		}
	}

	for _, k := range removes {
		k = strings.TrimSpace(k)
		if sec := model.Layout(l).Section(k); sec != nil {
			// 給的是區塊名稱、又剛好是某個頁籤的名稱或代號:不猜,拿錯會連同頁籤整個區塊不見
			if k != sec.ID {
				if t, none, _ := findTab(l, "", k); !none {
					where := k
					if t != nil {
						where = t.Path
					}
					return nil, nil, Errorf("「%s」同時是區塊和頁籤(%s)的名稱:區塊用 id「%s」,頁籤用網址指定", k, where, sec.ID)
				}
			}
			id := sec.ID
			kept := l[:0:0]
			for _, s := range l {
				if s.ID != id {
					kept = append(kept, s)
				}
			}
			l = kept
			continue
		}
		t, none, err := findTab(l, "", k)
		if err != nil {
			if none {
				return nil, nil, Errorf("找不到要移除的區塊或頁籤「%s」", k)
			}
			return nil, nil, err
		}
		path := t.Path
		for i := range l {
			tabs := []model.CabinetTab{}
			for _, x := range l[i].Tabs {
				if x.Path != path {
					tabs = append(tabs, x)
				}
			}
			l[i].Tabs = tabs
		}
	}
	return l, added, nil
}

// docFullLimit 只給 id 時回全文的上限(字);超過只回大綱與開頭
const docFullLimit = 6000

// docEditArgs doc_save 的局部修改參數(edits、replace_part、append_to_part 三種擇一,不能和 body 等整份修改混用)
func docEditArgs(c *Call) (service.DocEdit, bool, error) {
	ed := service.DocEdit{BaseVersion: c.Args.Str("base_version"), ReplaceSection: c.Args.Str("replace_part"),
		AppendToSection: c.Args.Str("append_to_part"), Content: c.Args.Str("content")}
	if raw, ok := c.Args.Raw["edits"].([]any); ok {
		for i, x := range raw {
			m, ok := x.(map[string]any)
			if !ok {
				return ed, false, Errorf("edits 第 %d 個要是 {old, new}", i+1)
			}
			o, _ := m["old"].(string)
			n, _ := m["new"].(string)
			all, _ := m["all"].(bool)
			ed.Edits = append(ed.Edits, model.TextEdit{Old: o, New: n, All: all})
		}
	}
	if len(ed.Edits) == 0 && ed.ReplaceSection == "" && ed.AppendToSection == "" {
		return ed, false, nil
	}
	for _, k := range []string{"body", "append_body", "title", "section", "tab", "tags"} {
		if _, has := c.Args.Raw[k]; has {
			return ed, false, Errorf("局部修改(edits、replace_part、append_to_part)不能和 %s 一起用;分兩次存", k)
		}
	}
	return ed, true, nil
}
