package mcp

import (
	"sort"
	"strings"
	"unicode/utf8"

	"opsweb/internal/core"
)

// 文件櫃工具:同 Swift 版 MCPHandler 的 cabinet / docs / docSave

// docScope 角色的存取範圍,給人看得懂的樣子(依 key 排序;對不到區塊或頁籤的略過)
func docScope(a *core.App, g core.Agent) []any {
	keys := make([]string, 0, len(g.DocAccess))
	for k := range g.DocAccess {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []any{}
	for _, k := range keys {
		v := g.DocAccess[k]
		if sec := a.CabinetSectionByID(k); sec != nil {
			out = append(out, map[string]any{"section": sec.ID, "label": sec.Label, "access": v})
		} else if t := a.CabinetTabByPath(&k); t != nil {
			out = append(out, map[string]any{"tab": t.Path, "label": t.Title(), "access": v})
		}
	}
	return out
}

func docAgent(c *Call) (*core.Agent, error) {
	name := c.Args.Str("agent")
	if name == "" {
		return nil, nil
	}
	g := c.App.FindAgent(name)
	if g == nil {
		return nil, Errorf("找不到角色 %s", name)
	}
	return g, nil
}

func docJSON(a *core.App, d core.CabinetDoc, full bool) map[string]any {
	o := map[string]any{"id": d.ID, "title": d.Title, "section": d.Section, "location": a.DocLocation(d.Section, d.Tab),
		"tags": d.Tags, "author": a.IssueAuthorName(d.Author), "updatedAt": core.ISO(d.UpdatedAt.Time)}
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

func docLoc(c *Call) (*core.CabinetSection, *core.CabinetTab, error) {
	s, t, err := c.App.ResolveCabinet(c.Args.iaOpt("section"), c.Args.iaOpt("tab"))
	return s, t, userToolErr(err)
}

func cabTabPath(t *core.CabinetTab) *string {
	if t == nil {
		return nil
	}
	p := t.Path
	return &p
}

func cabSameTab(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func cabOrgName(a *core.App) string {
	idx := a.Orgs()
	for _, o := range idx.Orgs {
		if o.ID == idx.Current {
			return o.Name
		}
	}
	return idx.Current
}

// cabinetSections 文件櫃的區塊與頁籤;給角色時只列它能存取的位置並標出權限(呼叫時要拿著 Mu)
func cabinetSections(a *core.App, g *core.Agent) []any {
	count := func(s string, t *string) int {
		n := 0
		for _, d := range a.CabinetDocs() {
			if d.Section == s && cabSameTab(d.Tab, t) {
				n++
			}
		}
		return n
	}
	out := []any{}
	for _, s := range a.CabinetLayout() {
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

func init() {
	Register("ops_cabinet", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		g, err := docAgent(c)
		if err != nil {
			return nil, err
		}
		return map[string]any{"organization": cabOrgName(a), "sections": cabinetSections(a, g)}, nil
	})

	Register("ops_docs", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		g, err := docAgent(c)
		if err != nil {
			return nil, err
		}
		visible := func(d core.CabinetDoc) bool {
			return g == nil || g.CanRead(d.Section, cabDeref(d.Tab))
		}
		if id := c.Args.iaOpt("id"); id != nil {
			d := a.FindDoc(*id)
			if d == nil {
				return nil, Errorf("找不到文件 %s", *id)
			}
			if !visible(*d) {
				return nil, Errorf("%s沒有權限讀「%s」的文件", g.Name, a.DocLocation(d.Section, d.Tab))
			}
			return docJSON(a, *d, true), nil
		}
		list := []core.CabinetDoc{}
		for _, d := range a.CabinetDocs() {
			if visible(d) {
				list = append(list, d)
			}
		}
		if c.Args.iaOpt("section") != nil || c.Args.iaOpt("tab") != nil {
			s, t, err := docLoc(c)
			if err != nil {
				return nil, err
			}
			kept := []core.CabinetDoc{}
			for _, d := range list {
				if d.Section == s.ID && (t == nil || cabSameTab(d.Tab, cabTabPath(t))) {
					kept = append(kept, d)
				}
			}
			list = kept
		}
		if q := strings.ToLower(c.Args.Str("query")); q != "" {
			kept := []core.CabinetDoc{}
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
			out = append(out, docJSON(a, d, false))
		}
		return map[string]any{"total": len(list), "docs": out}, nil
	})

	Register("ops_doc_save", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		g, err := docAgent(c)
		if err != nil {
			return nil, err
		}
		author := core.ActorAuthor(c.Actor)
		if g != nil {
			author = core.AgentPrefix + g.ID
		}
		checkWrite := func(s string, t *string) error {
			if g == nil || g.CanWrite(s, cabDeref(t)) {
				return nil
			}
			labels := []string{}
			for _, e := range docScope(a, *g) {
				m := e.(map[string]any)
				if m["access"] == "write" {
					labels = append(labels, m["label"].(string))
				}
			}
			return Errorf("%s沒有權限寫入「%s」;它可以寫的範圍:%s", g.Name, a.DocLocation(s, t), strings.Join(labels, "、"))
		}
		if id := c.Args.iaOpt("id"); id != nil {
			d := a.FindDoc(*id)
			if d == nil {
				return nil, Errorf("找不到文件 %s", *id)
			}
			if err := checkWrite(d.Section, d.Tab); err != nil {
				return nil, err
			}
			moved := false
			var newSec *core.CabinetSection
			var newTab *core.CabinetTab
			if c.Args.iaOpt("section") != nil || c.Args.iaOpt("tab") != nil {
				newSec, newTab, err = docLoc(c)
				if err != nil {
					return nil, err
				}
				if err := checkWrite(newSec.ID, cabTabPath(newTab)); err != nil {
					return nil, err
				}
				moved = true
			}
			title := c.Args.iaOpt("title")
			if title != nil && *title == "" {
				return nil, ToolError{"標題不能是空的"}
			}
			newBody := d.Body
			if b, ok := c.Args.iaRaw("body"); ok {
				newBody = b
			}
			if b, ok := c.Args.iaRaw("append_body"); ok && b != "" {
				if newBody != "" {
					newBody += "\n\n"
				}
				newBody += b
			}
			if e := core.ValidateDocBody(newBody, a.CabinetDocs()); e != "" {
				return nil, ToolError{e}
			}
			updated := a.UpdateDoc(d.ID, func(x *core.CabinetDoc) {
				if title != nil {
					x.Title = *title
				}
				if b, ok := c.Args.iaRaw("body"); ok {
					x.Body = b
				}
				if b, ok := c.Args.iaRaw("append_body"); ok && b != "" {
					if x.Body != "" {
						x.Body += "\n\n"
					}
					x.Body += b
				}
				if moved {
					x.Section = newSec.ID
					x.Tab = cabTabPath(newTab)
				}
				if tags, ok := c.Args.Strings("tags"); ok {
					x.Tags = tags
				}
			})
			if updated == nil {
				return map[string]any{}, nil
			}
			return docJSON(a, *updated, false), nil
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		s, t, err := docLoc(c)
		if err != nil {
			return nil, err
		}
		if err := checkWrite(s.ID, cabTabPath(t)); err != nil {
			return nil, err
		}
		body, _ := c.Args.iaRaw("body")
		if e := core.ValidateDocBody(body, a.CabinetDocs()); e != "" {
			return nil, ToolError{e}
		}
		tags, _ := c.Args.Strings("tags")
		d := a.CreateDoc(title, body, s.ID, cabTabPath(t), tags, author)
		return docJSON(a, d, false), nil
	})
}

func cabDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
