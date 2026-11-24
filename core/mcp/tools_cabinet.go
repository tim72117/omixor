package mcp

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"omixor-core/model"
	"omixor-core/service"
)

// 文件櫃工具:cabinet、docs、doc_save、cabinet_layout_save(名稱、參數、輸出與錯誤訊息同 Swift 版 MCPHandler 與 omixor-web 原本的實作),
// 與專案工具 projects、project_save。
// 專案:文件櫃工具都有選填的 project(專案 id);省略 = 未分專案(原本的組織文件櫃,行為和以前一樣)。
// 未分專案一律走 Backend 原本的 GetCabinet / SaveCabinetLayout(只實作舊介面的 Backend 不受影響)。
// 雲端(server)與 omixor-web 的管理端點、角色端點共用這一份實作;資料操作都透過 Backend(Local / Remote)。
//
// 權限:
//   - 角色端點:Backend 是角色身分,service 依憑證角色的 docAccess 強制(看不到、寫不了範圍外的文件);
//     呼叫端帶的 agent 會被端點強制成憑證的角色,這裡的檢查只是給出看得懂的錯誤訊息
//   - 管理端點:呼叫者本人的權限;有給 agent 時依該角色的 docAccess 過濾與檢查,署名是該角色

// cabProject 工具的 project 參數(專案 id;空 = 未分專案)
func cabProject(c *Call) string { return model.NormalizeProjectID(c.Args.Str("project")) }

// getCabinet 專案(空 = 未分專案)的文件櫃;未分專案走 Backend 的 GetCabinet
func getCabinet(e Env, project string) (*model.Cabinet, error) {
	if project == "" {
		return e.Backend().GetCabinet(e.Org())
	}
	return e.Backend().GetProjectCabinet(e.Org(), project)
}

// cabinetOf 這個連線某個專案(空 = 未分專案)的文件櫃(結構與呼叫者看得到的文件);Backend 不支援或專案不存在時回錯誤
func cabinetOf(e Env, project string) (*model.Cabinet, error) {
	c, err := getCabinet(e, project)
	if err != nil {
		return nil, terr(err)
	}
	return c, nil
}

// cabLayout 未分專案的結構;取不到(例如 Backend 不支援文件櫃)回 nil
func cabLayout(e Env) model.Layout { return cabLayoutOf(e, "") }

// cabLayoutOf 專案(空 = 未分專案)的結構;取不到回 nil
func cabLayoutOf(e Env, project string) model.Layout {
	c, err := getCabinet(e, project)
	if err != nil {
		return nil
	}
	return c.Layout
}

// projectName 專案的顯示名稱(取不到回 id)
func projectName(e Env, id string) string {
	if id == "" {
		return ""
	}
	if l, err := e.Backend().ListProjects(e.Org()); err == nil {
		for _, x := range l {
			if x.ID == id {
				return x.Name
			}
		}
	}
	return id
}

// placeOf 錯誤訊息用的位置:專案的前面加「專案名稱 › 」
func placeOf(e Env, project string, l model.Layout, section, tab string) string {
	if project == "" {
		return l.Location(section, tab)
	}
	return projectName(e, project) + " › " + l.Location(section, tab)
}

// cabAgent 有給 agent 參數時找角色(找不到回錯誤);沒給回 nil
func cabAgent(e Env, c *Call) (*model.Agent, error) {
	name, err := identityAgent(c) // 人類端點帶 agent:400;角色端點是憑證強制的角色
	if err != nil || name == "" {
		return nil, err
	}
	g, err := e.Backend().GetAgent(e.Org(), name)
	if err != nil {
		return nil, Errorf("Agent %s not found", name)
	}
	return g, nil
}

// DocScope 角色在「未分專案」的存取範圍,給人看得懂的樣子(依 key 排序;對不到區塊或頁籤的略過)
func DocScope(l model.Layout, g model.Agent) []any { return docScopeIn(l, g, "") }

// docScopeIn 角色在某個專案(空 = 未分專案)的存取範圍;l 是那個專案的結構。專案的項目多帶 project
func docScopeIn(l model.Layout, g model.Agent, project string) []any {
	keys := make([]string, 0, len(g.DocAccess))
	for k := range g.DocAccess {
		if p, _ := model.SplitDocAccessKey(k); p == project {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := []any{}
	for _, k := range keys {
		v := g.DocAccess[k]
		_, key := model.SplitDocAccessKey(k)
		var o map[string]any
		if sec := l.Section(key); sec != nil {
			o = map[string]any{"section": sec.ID, "label": sec.Label, "access": v}
		} else if t := l.TabByPath(key); t != nil {
			o = map[string]any{"tab": t.Path, "label": t.Title(), "access": v}
		} else {
			continue
		}
		if project != "" {
			o["project"] = project
		}
		out = append(out, o)
	}
	return out
}

// docScopeAll 角色在未分專案(l 是它的結構)與 docAccess 提到的各專案的存取範圍
func docScopeAll(e Env, l model.Layout, g model.Agent) []any {
	out := DocScope(l, g)
	for _, pid := range g.DocProjects() {
		if pl := cabLayoutOf(e, pid); pl != nil {
			out = append(out, docScopeIn(pl, g, pid)...)
		}
	}
	return out
}

func docJSON(e Env, l model.Layout, d model.CabinetDoc, full bool) map[string]any {
	o := map[string]any{"id": d.ID, "title": d.Title, "section": d.Section, "location": l.Location(d.Section, d.TabPath()),
		"tags": d.Tags, "author": e.AuthorName(d.Author), "updatedAt": iso(d.UpdatedAt.Time)}
	putActor(o, "authorActor", d.AuthorActor)
	if d.Project != "" {
		o["project"] = d.Project
	}
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

// docVersionJSON 在文件的回傳物件加上 version(內文的版本代號);docs 與 doc_save 共用,
// 讓呼叫端存檔後不必重讀就能拿它當下一次局部修改的 base_version
func docVersionJSON(o map[string]any, d model.CabinetDoc) map[string]any {
	o["version"] = model.DocVersion(d.Body)
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
				acc = g.AccessIn(cab.Project, s.ID, t.Path)
				if acc == "" {
					continue
				}
			}
			p := t.Path
			tabs = append(tabs, map[string]any{"tab": t.Path, "label": t.Title(), "docs": count(s.ID, &p), "access": acc})
		}
		secAcc := "write"
		if g != nil {
			secAcc = g.AccessIn(cab.Project, s.ID, "")
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
// 未分專案之外,docAccess 提到的專案也列(cabinet 多一個 projects:各專案的 id、名稱與區塊)。
// 取不到文件櫃(Backend 不支援)回 nil
func cabinetContext(e Env, g model.Agent, includeBodies bool) map[string]any {
	cab, err := getCabinet(e, "")
	if err != nil {
		return nil
	}
	cabs := []*model.Cabinet{cab}
	projects := []any{}
	for _, pid := range g.DocProjects() {
		pc, err := getCabinet(e, pid)
		if err != nil {
			continue // 專案不在了或 Backend 不支援專案
		}
		cabs = append(cabs, pc)
		projects = append(projects, map[string]any{"project": pid, "name": projectName(e, pid), "sections": cabinetSections(pc, &g)})
	}
	const bodyLimit = 20
	docs := []any{}
	docN := 0
	for _, c := range cabs {
		for _, d := range c.Docs {
			if !g.CanReadDoc(d) {
				continue
			}
			docN++
			if len(docs) >= 50 {
				continue
			}
			o := docJSON(e, c.Layout, d, includeBodies && len(docs) < bodyLimit)
			o["access"] = "read"
			if g.CanWriteDoc(d) {
				o["access"] = "write"
			}
			docs = append(docs, o)
		}
	}
	out := map[string]any{
		"cabinet": cabinetSections(cab, &g),
		"docs":    map[string]any{"total": docN, "docs": docs},
	}
	if len(projects) > 0 {
		out["projects"] = projects
	}
	return out
}

// projectJSON 專案的輸出
func projectJSON(x model.Project) map[string]any {
	return map[string]any{"id": x.ID, "name": x.Name, "key": x.Key, "description": x.Description,
		"repos": nonNilStrings(x.Repos), "restricted": x.Restricted, "myRole": x.MyRole, "createdAt": iso(x.CreatedAt.Time), "updatedAt": iso(x.UpdatedAt.Time)}
}

// projectMembersJSON 專案成員的輸出:node(user:<email>、agent:<角色 id>)、角色、actor 物件
func projectMembersJSON(l []model.ProjectMember) []any {
	out := []any{}
	for _, m := range l {
		o := map[string]any{"node": m.Node, "role": m.Role, "since": iso(m.CreatedAt.Time)}
		if m.Actor != nil {
			o["actor"] = m.Actor
		}
		out = append(out, o)
	}
	return out
}

func nonNilStrings(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// withProject 文件櫃工具輸出加上專案(未分專案不加)
func withProject(e Env, out map[string]any, project string) map[string]any {
	if project != "" {
		out["project"] = map[string]any{"id": project, "name": projectName(e, project)}
	}
	return out
}

// RegisterCabinetTools 登記文件櫃工具:cabinet、docs、doc_save、cabinet_layout_save,與專案工具 projects、project_save。
// 執行環境(Call.Ctx)要實作 AgentEnv。
func RegisterCabinetTools(s *Server) {
	s.Register("projects", func(c *Call) (any, error) {
		e := agentEnv(c)
		list, err := e.Backend().ListProjects(e.Org())
		if err != nil {
			return nil, terr(err)
		}
		out := []any{}
		only := ""
		if id := c.Args.Opt("id"); id != nil {
			only = strings.TrimSpace(*id)
		}
		for _, x := range list {
			if only != "" && !strings.EqualFold(only, x.ID) { // 給 id 只看那個專案(看不到的受限專案不在 list 裡:回找不到,和不存在一樣)
				continue
			}
			o := projectJSON(x)
			if cab, err := getCabinet(e, x.ID); err == nil {
				o["docs"] = len(cab.Docs)
			}
			if only != "" { // 單一專案附成員名單(角色端點不能列成員:略過)
				if ms, err := e.Backend().ListProjectMembers(e.Org(), x.ID); err == nil {
					o["members"] = projectMembersJSON(ms)
				}
			}
			out = append(out, o)
		}
		if only != "" && len(out) == 0 {
			return nil, Errorf("Project %s not found", only)
		}
		return map[string]any{"organization": e.OrgName(), "projects": out,
			"note": "Cabinet tools (cabinet, docs, doc_save, cabinet_layout_save) take project (project id) to operate on that project; omitted means no project"}, nil
	})

	s.Register("project_save", func(c *Call) (any, error) {
		e := agentEnv(c)
		bk, org := e.Backend(), e.Org()
		link, _ := c.Args.Strings("link_repos")
		unlink, _ := c.Args.Strings("unlink_repos")
		addMembers, _ := c.Args.Strings("add_members")
		removeMembers, _ := c.Args.Strings("remove_members")
		role := c.Args.Str("member_role")
		var restricted *bool
		if v, ok := c.Args.Bool("restricted"); ok {
			restricted = &v
		}
		// 專案成員:專案存好之後依序套用(加入、改角色、移除);回傳最後的專案與成員名單
		remember := func(x *model.Project) (any, error) {
			for _, m := range addMembers {
				if _, err := bk.SetProjectMember(org, x.ID, service.ProjectMemberInput{Actor: m, Role: role}); err != nil {
					return nil, terr(err)
				}
			}
			for _, m := range removeMembers {
				if err := bk.RemoveProjectMember(org, x.ID, m); err != nil {
					return nil, terr(err)
				}
			}
			out := projectJSON(*x)
			if len(addMembers)+len(removeMembers) > 0 {
				if ms, err := bk.ListProjectMembers(org, x.ID); err == nil {
					out["members"] = projectMembersJSON(ms)
				}
			}
			return out, nil
		}
		// 連結 repo、取消連結:專案存好之後依序套用(只用於預設值與篩選,見 model.ProjectRepo);回傳最後的專案
		relink := func(x *model.Project) (any, error) {
			for _, r := range link {
				y, err := bk.LinkProjectRepo(org, x.ID, r)
				if err != nil {
					return nil, terr(err)
				}
				x = y
			}
			for _, r := range unlink {
				y, err := bk.UnlinkProjectRepo(org, x.ID, r)
				if err != nil {
					return nil, terr(err)
				}
				x = y
			}
			return remember(x)
		}
		if id := c.Args.Opt("id"); id != nil && *id != "" {
			p := service.ProjectPatch{Name: c.Args.Opt("name"), Key: c.Args.Opt("key"), Description: c.Args.Opt("description"), Restricted: restricted}
			only := p.Name == nil && p.Description == nil && p.Key == nil && p.Restricted == nil // 只改連結、成員:不更新專案本身
			if only && len(link)+len(unlink)+len(addMembers)+len(removeMembers) == 0 {
				return nil, ToolError{Message: "Give at least one of name, key, description, restricted, link_repos, unlink_repos, add_members or remove_members"}
			}
			var x *model.Project
			var err error
			if only { // 成員管理(專案管理員也可以):不經 UpdateProject(它要組織管理員)
				var list []model.Project
				if list, err = bk.ListProjects(org); err == nil {
					err = Errorf("Project %s not found", *id)
					for i := range list {
						if strings.EqualFold(list[i].ID, strings.TrimSpace(*id)) {
							x, err = &list[i], nil
							break
						}
					}
				}
			} else {
				x, err = bk.UpdateProject(org, *id, p)
			}
			if err != nil {
				return nil, terr(err)
			}
			return relink(x)
		}
		name, err := c.Args.Require("name")
		if err != nil {
			return nil, err
		}
		in := service.ProjectInput{Name: name, Key: c.Args.Str("key"), Description: c.Args.Str("description")}
		if restricted != nil {
			in.Restricted = *restricted
		}
		x, err := bk.CreateProject(org, in)
		if err != nil {
			return nil, terr(err)
		}
		return relink(x)
	})

	s.Register("cabinet", func(c *Call) (any, error) {
		e := agentEnv(c)
		g, err := cabAgent(e, c)
		if err != nil {
			return nil, err
		}
		project := cabProject(c)
		cab, err := cabinetOf(e, project)
		if err != nil {
			return nil, err
		}
		return withProject(e, map[string]any{"organization": e.OrgName(), "sections": cabinetSections(cab, g), "layoutVersion": cab.LayoutVersion}, cab.Project), nil
	})

	s.Register("docs", func(c *Call) (any, error) {
		e := agentEnv(c)
		g, err := cabAgent(e, c)
		if err != nil {
			return nil, err
		}
		visible := func(d model.CabinetDoc) bool { return g == nil || g.CanReadDoc(d) }
		if id := c.Args.Opt("id"); id != nil {
			d, err := e.Backend().GetDoc(e.Org(), *id)
			if err != nil {
				return nil, terr(err)
			}
			l := cabLayoutOf(e, d.Project)
			if !visible(*d) {
				return nil, Errorf("%s has no permission to read documents in \"%s\"", g.Name, placeOf(e, d.Project, l, d.Section, d.TabPath()))
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
				return docVersionJSON(docJSON(e, l, *d, true), *d), nil
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
				o["note"] = fmt.Sprintf("The document is long (%d characters), so only the outline and the beginning are returned; use part to read one part, from/to to read some lines, or full to read it all", o["chars"])
			}
			return o, nil
		}
		cab, err := cabinetOf(e, cabProject(c))
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
		return withProject(e, map[string]any{"total": len(list), "docs": out}, cab.Project), nil
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
		// layouts 各專案的結構(空 = 未分專案);取不到(專案不存在、Backend 不支援)回錯誤
		layouts := map[string]model.Layout{}
		layoutOf := func(project string) (model.Layout, error) {
			if l, ok := layouts[project]; ok {
				return l, nil
			}
			cab, err := cabinetOf(e, project)
			if err != nil {
				return nil, err
			}
			layouts[project] = cab.Layout
			return cab.Layout, nil
		}
		checkWrite := func(project string, l model.Layout, s string, t *string) error {
			if g == nil || g.CanWriteIn(project, s, deref(t)) {
				return nil
			}
			labels := []string{}
			for _, x := range docScopeAll(e, cabLayout(e), *g) {
				m := x.(map[string]any)
				if m["access"] == "write" {
					label := m["label"].(string)
					if p, ok := m["project"].(string); ok {
						label = projectName(e, p) + " › " + label
					}
					labels = append(labels, label)
				}
			}
			return Errorf("%s has no permission to write to \"%s\"; places it can write: %s", g.Name, placeOf(e, project, l, s, deref(t)), strings.Join(labels, ", "))
		}
		if id := c.Args.Opt("id"); id != nil {
			d, err := bk.GetDoc(org, *id)
			if err != nil {
				return nil, terr(err)
			}
			l, err := layoutOf(d.Project)
			if err != nil {
				return nil, err
			}
			if err := checkWrite(d.Project, l, d.Section, d.Tab); err != nil {
				return nil, err
			}
			// 局部修改(edits、replace_part、append_to_part):只回改到的那幾行與新版本
			if ed, ok, err := docEditArgs(c); err != nil {
				return nil, err
			} else if ok {
				if c.Args.Opt("project") != nil && cabProject(c) != d.Project {
					return nil, ToolError{Message: "Partial edits (edits, replace_part, append_to_part) cannot move the document to another project at the same time; save in two steps"}
				}
				r, err := bk.EditDoc(org, d.ID, ed)
				if err != nil {
					return nil, terr(err)
				}
				return map[string]any{"id": r.ID, "version": r.Version, "lines": r.Lines, "from": r.From, "to": r.To,
					"text": r.Text, "rebased": r.Rebased, "unchanged": r.Unchanged}, nil
			}
			p := service.DocPatch{}
			// 給 project 就是搬到那個專案(空字串 = 未分專案):區塊、頁籤依目標專案的結構解析;沒給時沿用原本的位置
			target := d.Project
			if c.Args.Opt("project") != nil {
				target = cabProject(c)
			}
			tl := l
			if target != d.Project {
				if tl, err = layoutOf(target); err != nil {
					return nil, err
				}
				p.Project = &target
			}
			if c.Args.Opt("section") != nil || c.Args.Opt("tab") != nil {
				sec, t, msg := tl.Resolve(c.Args.Opt("section"), c.Args.Opt("tab"))
				if msg != "" {
					return nil, ToolError{Message: msg}
				}
				if err := checkWrite(target, tl, sec.ID, tabPath(t)); err != nil {
					return nil, err
				}
				p.Section, p.Tab = &sec.ID, tabPath(t)
			} else if p.Project != nil {
				if !tl.Has(d.Section, d.TabPath()) {
					return nil, Errorf("The target project has no place \"%s\"; also give section (and tab) to name a place in the target project", l.Location(d.Section, d.TabPath()))
				}
				if err := checkWrite(target, tl, d.Section, d.Tab); err != nil {
					return nil, err
				}
			}
			title := c.Args.Opt("title")
			if title != nil && *title == "" {
				return nil, ToolError{Message: "The title cannot be empty"}
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
			return docVersionJSON(docJSON(e, tl, *updated, false), *updated), nil
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		project := cabProject(c)
		l, err := layoutOf(project)
		if err != nil {
			return nil, err
		}
		sec, t, msg := l.Resolve(c.Args.Opt("section"), c.Args.Opt("tab"))
		if msg != "" {
			return nil, ToolError{Message: msg}
		}
		if err := checkWrite(project, l, sec.ID, tabPath(t)); err != nil {
			return nil, err
		}
		body, _ := c.Args.RawString("body")
		tags, _ := c.Args.Strings("tags")
		d, err := bk.CreateDoc(org, service.DocInput{Project: project, Title: title, Body: body, Section: sec.ID, Tab: tabPath(t), Tags: tags, Author: author})
		if err != nil {
			return nil, terr(err)
		}
		return docVersionJSON(docJSON(e, l, *d, false), *d), nil
	})

	s.Register("cabinet_layout_save", func(c *Call) (any, error) {
		e := agentEnv(c)
		project := cabProject(c)
		cab, err := cabinetOf(e, project)
		if err != nil {
			return nil, err
		}
		layout, added, err := applyLayoutOps(model.CloneLayout(cab.Layout), c.Args.Raw)
		if err != nil {
			return nil, err
		}
		// 樂觀鎖:有給 expectedVersion 就用它(之前用 cabinet 讀到的 layoutVersion);沒給就用這次呼叫開頭讀到的版本,
		// 至少擋住「讀了之後、存之前」別人的修改(向下相容:舊的呼叫不用改)
		expected := cab.LayoutVersion
		if n, ok := c.Args.Int("expectedVersion"); ok {
			expected = int64(n)
		}
		if project == "" {
			_, err = e.Backend().SaveCabinetLayout(e.Org(), layout, &expected)
		} else {
			_, err = e.Backend().SaveProjectCabinetLayout(e.Org(), project, layout, &expected)
		}
		if err != nil {
			return nil, terr(err)
		}
		cab, err = cabinetOf(e, project)
		if err != nil {
			return nil, err
		}
		return withProject(e, map[string]any{"organization": e.OrgName(), "sections": cabinetSections(cab, nil), "added": added, "layoutVersion": cab.LayoutVersion}, cab.Project), nil
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
			return nil, false, Errorf("Section \"%s\" not found", section)
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
			return nil, true, Errorf("Section \"%s\" has no tab \"%s\"", section, key)
		}
		return nil, true, Errorf("Tab \"%s\" not found", key)
	case 1:
		return hits[0], false, nil
	}
	paths := make([]string, len(hits))
	for i, t := range hits {
		paths[i] = t.Path
	}
	return nil, false, Errorf("\"%s\" matches several tabs (%s); specify the tab by its path", key, strings.Join(paths, ", "))
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
				return Errorf("Tab code \"%s\" is already used by \"%s › %s\"", *code, s.Label, t.Title())
			}
			if s.ID == sec.ID && t.Label == label {
				return Errorf("\"%s\" already has a tab \"%s\"", sec.Label, label)
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
			return nil, Errorf("%s must be an array of objects", key)
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
		return nil, nil, Errorf("remove must be an array of strings")
	}
	if len(addSecs) == 0 && len(addTabs) == 0 && len(updates) == 0 && len(removes) == 0 {
		return nil, nil, ToolError{Message: "Give at least one of add_sections, add_tabs, update or remove"}
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
			return nil, nil, ToolError{Message: "Every add_sections entry needs a label"}
		}
		if model.Layout(l).Section(label) != nil {
			return nil, nil, Errorf("Section \"%s\" already exists", label)
		}
		id := strings.TrimSpace(a.Str("id"))
		if id == "" {
			n := len(l) + 1
			for model.Layout(l).Section(fmt.Sprintf("s%d", n)) != nil {
				n++
			}
			id = fmt.Sprintf("s%d", n)
		} else if model.Layout(l).Section(id) != nil {
			return nil, nil, Errorf("Duplicate section id: %s", id)
		}
		l = append(l, model.CabinetSection{ID: id, Label: label, Tabs: []model.CabinetTab{}})
		added = append(added, map[string]any{"section": id, "label": label})
	}

	for _, x := range addTabs {
		a := Args{Raw: x}
		secKey, label := strings.TrimSpace(a.Str("section")), strings.TrimSpace(a.Str("label"))
		if secKey == "" || label == "" {
			return nil, nil, ToolError{Message: "Every add_tabs entry needs section and label"}
		}
		sec := model.Layout(l).Section(secKey)
		if sec == nil {
			return nil, nil, Errorf("Section \"%s\" not found", secKey)
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
				return nil, nil, Errorf("Duplicate tab id: %s", path)
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
			return nil, nil, ToolError{Message: "The label in update cannot be empty"}
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
			return nil, nil, ToolError{Message: "Every update entry needs section or tab"}
		}
		sec := model.Layout(l).Section(sk)
		if sec == nil {
			return nil, nil, Errorf("Section \"%s\" not found", sk)
		}
		if label != nil {
			if o := model.Layout(l).Section(*label); o != nil && o.ID != sec.ID {
				return nil, nil, Errorf("Section \"%s\" already exists", *label)
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
					return nil, nil, Errorf("\"%s\" is both a section name and a tab name (%s): refer to the section by id \"%s\" and to the tab by its path", k, where, sec.ID)
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
				return nil, nil, Errorf("Section or tab to remove \"%s\" not found", k)
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
				return ed, false, Errorf("edits entry %d must be {old, new}", i+1)
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
			return ed, false, Errorf("Partial edits (edits, replace_part, append_to_part) cannot be combined with %s; save in two steps", k)
		}
	}
	return ed, true, nil
}
