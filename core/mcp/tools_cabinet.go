package mcp

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"omixor-core/model"
	"omixor-core/service"
)

// 文件櫃工具:ops_cabinet、ops_docs、ops_doc_save(名稱、參數、輸出與錯誤訊息同 Swift 版 MCPHandler 與 omixor-web 原本的實作)。
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

// cabinetContext 附加到 ops_agent_context 的欄位:角色能存取的文件櫃區塊,與能讀的文件(依角色的 docAccess)。
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

// RegisterCabinetTools 登記文件櫃工具:ops_cabinet、ops_docs、ops_doc_save。執行環境(Call.Ctx)要實作 AgentEnv。
func RegisterCabinetTools(s *Server) {
	s.Register("ops_cabinet", func(c *Call) (any, error) {
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

	s.Register("ops_docs", func(c *Call) (any, error) {
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

	s.Register("ops_doc_save", func(c *Call) (any, error) {
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
}

// docFullLimit 只給 id 時回全文的上限(字);超過只回大綱與開頭
const docFullLimit = 6000

// docEditArgs ops_doc_save 的局部修改參數(edits、replace_part、append_to_part 三種擇一,不能和 body 等整份修改混用)
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
