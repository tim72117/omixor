package service

import (
	"fmt"
	"strings"

	"omixor-core/model"
)

// 組織樣板(定義在 model/orgtemplates.go):
//
//	列表、預覽:組織成員;樣板的功能開關(OrgTemplate.Feature)關閉時列表不列出、預覽與套用回 403;
//	      這次編譯的版本沒有那個功能(例如正式版沒有 familyAssistant)時列表不列出、預覽與套用回 404
//	套用:組織管理員;用現有的 service(SaveAgent、SaveCabinetLayout、CreateDoc、CreateChannel、SaveChain)逐項建立,
//	      已存在的略過並回報(角色:同 id 或同名稱;頻道:同名稱;區塊:同 id;文件:同區塊同標題;組織鏈:同一條邊)
//	      重複套用不會重複建立。
//
// 目前是框架(最小可行):一項失敗會記在結果裡、繼續做下一項,不回滾已建立的。

// TemplateApplyInput 套用的選項
type TemplateApplyInput struct {
	// Scenarios 帶入模擬情境:在樣板的頻道寫入示範對話、建立示範任務(標「模擬」)
	Scenarios bool `json:"scenarios"`
}

// TemplateSummary 列表用
type TemplateSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Feature     string `json:"feature"`
	Agents      int    `json:"agents"`
	Channels    int    `json:"channels"`
	Docs        int    `json:"docs"`
	Edges       int    `json:"edges"`
	Scenarios   int    `json:"scenarios"`
}

// TemplateItem 套用(或預覽)的一項
type TemplateItem struct {
	// Kind agent、chain、channel、section、doc
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Status 預覽:create / skip;套用:created / skipped / failed
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// TemplatePlan 預覽或套用的結果
type TemplatePlan struct {
	Template model.OrgTemplate `json:"template"`
	Items    []TemplateItem    `json:"items"`
	Applied  bool              `json:"applied"`
	Created  int               `json:"created"`
	Skipped  int               `json:"skipped"`
	Failed   int               `json:"failed"`
	// SuggestAutoReply 建議打開自動執行與自動回覆的角色 id(每台裝置的設定,核心不動)
	SuggestAutoReply []string `json:"suggestAutoReply"`
	// CanApply 目前的使用者可以套用(組織管理員)
	CanApply bool `json:"canApply"`
}

func templateSummary(t model.OrgTemplate) TemplateSummary {
	return TemplateSummary{ID: t.ID, Name: t.Name, Description: t.Description, Feature: t.Feature,
		Agents: len(t.Agents), Channels: len(t.Channels), Docs: len(t.Docs), Edges: len(t.Chain), Scenarios: len(t.Scenarios)}
}

// templateEnabled 樣板的功能開關有沒有開
func (s *Service) templateEnabled(org string, t model.OrgTemplate) (bool, error) {
	if t.Feature == "" {
		return true, nil
	}
	x, err := s.loadSystem(org)
	if err != nil {
		return false, err
	}
	return x.Enabled(t.Feature), nil
}

// ListOrgTemplates 可用的樣板(組織成員;功能開關關閉的不列)
func (s *Service) ListOrgTemplates(p *model.Principal, org string) ([]TemplateSummary, error) {
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	out := []TemplateSummary{}
	for _, t := range model.OrgTemplates {
		ok, err := s.templateEnabled(org, t)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, templateSummary(t))
		}
	}
	return out, nil
}

// usableTemplate 找樣板並檢查功能開關
func (s *Service) usableTemplate(org, id string) (model.OrgTemplate, error) {
	t, ok := model.OrgTemplateByID(strings.TrimSpace(id))
	if !ok {
		return t, fail(404, "沒有這個樣板:%s", id)
	}
	if f, ok := model.FeatureByKey(t.Feature); ok && !f.Available() {
		return t, fail(404, "這個版本(%s)沒有「%s」功能,「%s」樣板不能用;要用請改用開發版(-tags dev)編譯",
			model.EditionLabel(model.BuildEdition), f.Name, t.Name)
	}
	on, err := s.templateEnabled(org, t)
	if err != nil {
		return t, err
	}
	if !on {
		name := t.Feature
		if f, ok := model.FeatureByKey(t.Feature); ok {
			name = f.Name
		}
		return t, fail(403, "「%s」功能還沒開啟:請組織管理員先到「系統」頁開啟「%s」", t.Name, name)
	}
	return t, nil
}

// templateNode 樣板的節點換成鏈上的節點:creator → user:<email>;其他是角色 id
func templateNode(raw, creator string) string {
	if raw == model.TemplateCreator {
		return creator
	}
	return model.AgentNode(raw)
}

// PreviewOrgTemplate 預覽套用會建立哪些、略過哪些(組織成員);scenarios = 連模擬情境一起列
func (s *Service) PreviewOrgTemplate(p *model.Principal, org, id string, scenarios bool) (*TemplatePlan, error) {
	role, err := s.orgRole(p, org)
	if err != nil {
		return nil, err
	}
	t, err := s.usableTemplate(org, id)
	if err != nil {
		return nil, err
	}
	return s.runTemplate(p, org, t, role == "admin", false, scenarios)
}

// ApplyOrgTemplate 套用樣板(組織管理員);已存在的略過
func (s *Service) ApplyOrgTemplate(p *model.Principal, org, id string, in TemplateApplyInput) (*TemplatePlan, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	t, err := s.usableTemplate(org, id)
	if err != nil {
		return nil, err
	}
	plan, err := s.runTemplate(p, org, t, true, true, in.Scenarios)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "template.apply", org, map[string]any{"template": t.ID, "scenarios": in.Scenarios, "created": plan.Created, "skipped": plan.Skipped, "failed": plan.Failed})
	return plan, nil
}

// runTemplate 逐項檢查;apply = true 時真的建立
func (s *Service) runTemplate(p *model.Principal, org string, t model.OrgTemplate, canApply, apply, scenarios bool) (*TemplatePlan, error) {
	plan := &TemplatePlan{Template: t, Items: []TemplateItem{}, Applied: apply, CanApply: canApply, SuggestAutoReply: []string{}}
	add := func(kind, name string, exists bool, detail string, do func() error) {
		it := TemplateItem{Kind: kind, Name: name, Detail: detail}
		switch {
		case exists && apply:
			it.Status = "skipped"
		case exists:
			it.Status = "skip"
		case !apply:
			it.Status = "create"
		default:
			if err := do(); err != nil {
				it.Status, it.Detail = "failed", err.Error()
			} else {
				it.Status = "created"
			}
		}
		switch it.Status {
		case "created":
			plan.Created++
		case "skipped", "skip":
			plan.Skipped++
		case "failed":
			plan.Failed++
		}
		plan.Items = append(plan.Items, it)
	}

	// 1. 文件櫃區塊(角色的 docAccess 指向它)
	if t.Section != nil {
		l, _, err := s.layoutOf(org)
		if err != nil {
			return nil, err
		}
		sec := *t.Section
		add("section", sec.Label, l.Section(sec.ID) != nil, "", func() error {
			_, err := s.SaveCabinetLayout(p, org, append(model.CloneLayout(l), sec))
			return err
		})
	}

	// 2. 角色
	for _, g := range t.Agents {
		if g.SuggestAutoReply {
			plan.SuggestAutoReply = append(plan.SuggestAutoReply, g.ID)
		}
		_, err1 := s.findAgent(org, g.ID)
		_, err2 := s.findAgent(org, g.Name)
		exists := err1 == nil || err2 == nil
		detail := ""
		if exists {
			detail = "已經有同 id 或同名稱的角色"
		}
		a := g.Agent.Clone()
		add("agent", g.Name, exists, detail, func() error {
			_, err := s.SaveAgent(p, org, a)
			return err
		})
	}

	// 3. 文件(放在樣板的區塊)
	if t.Section != nil && len(t.Docs) > 0 {
		docs, err := s.Store.ListDocs(org)
		if err != nil {
			return nil, err
		}
		for _, d := range t.Docs {
			exists := false
			for _, x := range docs {
				exists = exists || (x.Section == t.Section.ID && x.Title == d.Title)
			}
			d := d
			add("doc", d.Title, exists, "", func() error {
				_, err := s.CreateDoc(p, org, DocInput{Title: d.Title, Body: d.Body, Section: t.Section.ID, Tags: d.Tags})
				return err
			})
		}
	}

	// 4. 頻道(建立者自動是成員)
	creator := model.UserNode(p.User.Email)
	if len(t.Channels) > 0 {
		chs, err := s.Store.ListChannels(org)
		if err != nil {
			return nil, err
		}
		for _, c := range t.Channels {
			exists := false
			for _, x := range chs {
				exists = exists || x.Name == c.Name
			}
			members := []string{}
			for _, m := range c.Members {
				if m != model.TemplateCreator {
					members = append(members, model.AgentNode(m))
				}
			}
			c := c
			add("channel", c.Name, exists, "", func() error {
				_, err := s.CreateChannel(p, org, ChannelInput{Name: c.Name, Topic: c.Topic, Members: members})
				return err
			})
		}
	}

	// 5. 組織鏈:加上還沒有的邊(建立者不是組織成員時,和他有關的邊略過)
	if len(t.Chain) > 0 {
		cur, n, err := s.liveChain(org)
		if err != nil {
			return nil, err
		}
		edges := append([]model.ChainEdge{}, cur.Edges...)
		type pending struct {
			item TemplateItem
			edge model.ChainEdge
		}
		news := []pending{}
		for _, e := range t.Chain {
			from, to := templateNode(e.From, creator), templateNode(e.To, creator)
			name := e.From + " → " + e.To
			if _, ok := cur.Edge(from, to); ok {
				add("chain", name, true, "", nil)
				continue
			}
			if (from == creator || to == creator) && !n.exists(creator) {
				add("chain", name, true, "你不是組織成員(例如伺服器管理員),這條邊略過;請在組織鏈手動加", nil)
				continue
			}
			news = append(news, pending{TemplateItem{Kind: "chain", Name: name}, model.ChainEdge{From: from, To: to, Label: e.Label}})
		}
		if len(news) > 0 {
			var saveErr error
			if apply {
				for _, x := range news {
					edges = append(edges, x.edge)
				}
				_, saveErr = s.SaveChain(p, org, edges)
			}
			for _, x := range news {
				add(x.item.Kind, x.item.Name, false, "", func() error { return saveErr })
			}
		}
	}
	// 6. 模擬情境(選了才做):同標題的模擬任務已存在就略過
	if scenarios && len(t.Scenarios) > 0 && len(t.Agents) > 0 {
		tasks, err := s.Store.ListAgentTasks(org)
		if err != nil {
			return nil, err
		}
		chID := ""
		if chs, err := s.Store.ListChannels(org); err == nil {
			for _, c := range chs {
				if c.Name == t.ScenarioChannel {
					chID = c.ID
				}
			}
		}
		for _, sc := range t.Scenarios {
			title := scenarioTitle(sc.Title)
			exists := false
			for _, x := range tasks {
				exists = exists || x.Title == title
			}
			sc := sc
			add("scenario", sc.Title, exists, "", func() error {
				if chID == "" {
					return fail(400, "找不到頻道「%s」,不能寫入示範對話", t.ScenarioChannel)
				}
				return s.applyScenario(p, org, t.Agents[0].ID, chID, sc)
			})
		}
	}
	return plan, nil
}

func scenarioTitle(title string) string { return "[" + model.ScenarioTag + "] " + title }

// applyScenario 寫入一個模擬情境:管家的任務(建立者是家人)、委派給專職角色的任務(建立者是管家)與回報,
// 以及頻道裡的示範對話。
// 不觸發真的流程:任務一律直接存成完成或需要協助(不會被自動執行領走)、不排「檢視回報」任務(回報寫成備註)、
// 訊息不記 @(不會觸發自動回覆),寫完把頻道成員的已讀推到最新。
func (s *Service) applyScenario(p *model.Principal, org, butlerID, chID string, sc model.TemplateScenario) error {
	butler, err := s.mustAgent(org, butlerID)
	if err != nil {
		return err
	}
	by := strings.TrimSpace(p.User.Email)
	if by == "" {
		by = model.SystemAuthor
	}
	note := func(author, text string) model.AgentTaskNote {
		return model.AgentTaskNote{ID: uuid(), Author: author, Text: text, At: s.mt()}
	}
	finish := func(t *model.AgentTask, status, result string, notes []model.AgentTaskNote) error {
		_, err := s.saveTaskChange(org, *t, func(x *model.AgentTask) {
			x.Status, x.Result = status, result
			x.Notes = append(x.Notes, notes...)
		})
		return err
	}
	parent, err := s.insertTask(org, butler, scenarioTitle(sc.Title), model.LabeledInstructions("需求", sc.Request), "normal", by, nil)
	if err != nil {
		return err
	}
	pnotes := []model.AgentTaskNote{}
	for _, st := range sc.Steps {
		g, err := s.mustAgent(org, st.Agent)
		if err != nil {
			return err
		}
		child, err := s.insertTask(org, g, scenarioTitle(st.Title),
			model.LabeledInstructions(model.DefaultChainLabel, st.Title+"\n\n(來源:"+parent.ID+")"), "normal", model.AgentNode(butler.ID), []string{parent.ID})
		if err != nil {
			return err
		}
		status := st.Status
		if status != "blocked" {
			status = "done"
		}
		if err := finish(child, status, st.Report, []model.AgentTaskNote{
			note(model.AgentNode(g.ID), st.Report),
			note(model.ChainAuthor, model.ChainReportPrefix+butler.Name+"(模擬,不排檢視任務)"),
		}); err != nil {
			return err
		}
		pnotes = append(pnotes, note(model.AgentNode(butler.ID), fmt.Sprintf("已委派給 %s:%s(%s)", g.Name, child.ID, model.TaskStatusLabel(status))))
	}
	status := "done"
	if len(sc.Decisions) > 0 {
		status = "blocked"
		pnotes = append(pnotes, note(model.AgentNode(butler.ID), "需要家人決定:\n- "+strings.Join(sc.Decisions, "\n- ")))
	}
	if by != model.SystemAuthor {
		pnotes = append(pnotes, note(model.ChainAuthor, model.ChainReportPrefix+strings.ToLower(by)))
	}
	if err := finish(parent, status, sc.FinalReply, pnotes); err != nil {
		return err
	}

	// 頻道的示範對話(不記 @)
	creator := model.UserNode(p.User.Email)
	tag := "【" + model.ScenarioTag + "】"
	msgs := []model.ChannelMessage{
		{Author: creator, Text: tag + sc.Request},
		{Author: model.AgentNode(butler.ID), Text: tag + sc.ButlerReply},
		{Author: model.AgentNode(butler.ID), Text: tag + sc.FinalReply + "\n\n(模擬任務 " + parent.ID + ")"},
	}
	last := 0
	for _, m := range msgs {
		m.ChannelID, m.Mentions, m.At = chID, []string{}, s.mt()
		saved, err := s.Store.CreateChannelMessage(org, m)
		if err != nil {
			return err
		}
		last = saved.Number()
	}
	c, err := s.findChannel(org, chID)
	if err != nil {
		return err
	}
	s.channelMu.Lock()
	for _, m := range c.Members {
		_, _ = s.advanceRead(org, chID, m, last)
	}
	s.channelMu.Unlock()
	return nil
}
