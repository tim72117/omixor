package service

import (
	"errors"
	"fmt"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 文件櫃:Markdown 文件,依區塊與頁籤分類(結構與文件都依組織分)。
//
// 專案(見 project.go、model/project.go):結構每個專案一份(project 空字串 = 未分專案,就是原本的組織文件櫃);
// 文件存在組織底下(DOC-<n> 組織內唯一),CabinetDoc.Project 是所屬專案。列文件只列指定專案的;
// 修改文件給 Project 就是搬到別的專案(目標專案的結構要有對應的區塊與頁籤)。專案參數空白 = 未分專案,不存在的專案 404。
//
// 權限:
//   - 組織成員:讀全部文件與結構、新增、修改、搬移、刪除文件;作者一律是自己(伺服器管理員可以指定角色或系統署名,不能代別的使用者)
//   - 組織管理員:另外可以改結構(區塊與頁籤)、第一次搬資料的匯入
//   - 角色身分(角色憑證):只看得到 docAccess 能讀的文件,只能在 docAccess 可寫的位置新增、修改、搬移
//     (沒有前綴的 key 只適用未分專案,專案用 "<專案 id>:<位置>",見 model.Agent.AccessIn);
//     署名一律是角色;不能刪除文件、不能改結構
//
// 存檔前依範本檢查必填欄位(model.ValidateDocBody);改結構前檢查被拿掉的區塊、頁籤裡沒有文件(model.ValidateCabinetLayout)。

// DocInput 新增文件。Section 是區塊 id、Tab 是頁籤網址(空 = 區塊本身);名稱、代號的解析在介面(core/mcp)
type DocInput struct {
	// Project 放進哪個專案(空 = 未分專案;不存在 404)
	Project string   `json:"project,omitempty"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Section string   `json:"section"`
	Tab     *string  `json:"tab,omitempty"`
	Tags    []string `json:"tags"`
	// Author 伺服器管理員(omixor-web 本機模式)可以指定角色或系統署名;其他人一律是自己(角色憑證是角色)
	Author string `json:"author,omitempty"`
}

// DocPatch 修改文件:有給的欄位才改。給 Section 就是搬到 (Section, Tab)(Tab 沒給或空 = 區塊本身)。
// 給 Project 就是搬到那個專案(空字串 = 未分專案;不存在 404):沒給 Section 時沿用原本的區塊與頁籤,目標專案的結構要有
type DocPatch struct {
	Project    *string   `json:"project,omitempty"`
	Title      *string   `json:"title,omitempty"`
	Body       *string   `json:"body,omitempty"`
	AppendBody *string   `json:"appendBody,omitempty"` // 接在內文後面(中間空一行)
	Section    *string   `json:"section,omitempty"`
	Tab        *string   `json:"tab,omitempty"`
	Tags       *[]string `json:"tags,omitempty"`
}

// CabinetImport 第一次搬資料用(例如 omixor-web 本機舊的 cabinet-layout.json、cabinet.json)。
// 結構存成未分專案的;文件的 project 有給時專案要存在
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

// layoutOf 專案(空 = 未分專案)的結構;沒存過是空白結構(saved = false)
func (s *Service) layoutOf(org, project string) (model.Layout, bool, error) {
	l, saved, _, err := s.layoutVersionOf(org, project)
	return l, saved, err
}

// layoutVersionOf 同 layoutOf,多回傳結構的版本號(沒存過是 0)
func (s *Service) layoutVersionOf(org, project string) (model.Layout, bool, int64, error) {
	l, v, err := s.Store.GetCabinetLayout(org, project)
	if errors.Is(err, store.ErrNotFound) {
		return model.BlankCabinetLayout(), false, 0, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	return l, true, v, nil
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

// docsIn 專案(空 = 未分專案)裡的文件(新的在前)
func docsIn(docs []model.CabinetDoc, project string) []model.CabinetDoc {
	out := []model.CabinetDoc{}
	for _, d := range docs {
		if d.Project == project {
			out = append(out, d)
		}
	}
	return out
}

// GetCabinet 專案(空 = 未分專案)的結構與文件(新的在前);角色身分只看得到 docAccess 能讀的文件。專案不存在 404
func (s *Service) GetCabinet(p *model.Principal, org, project string) (*model.Cabinet, error) {
	g, err := s.docViewer(p, org)
	if err != nil {
		return nil, err
	}
	project, err = s.projectID(org, project)
	if err != nil {
		return nil, err
	}
	l, saved, ver, err := s.layoutVersionOf(org, project)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.ListDocs(org)
	if err != nil {
		return nil, err
	}
	docs := docsIn(all, project)
	if g != nil {
		kept := []model.CabinetDoc{}
		for _, d := range docs {
			if g.CanReadDoc(d) {
				kept = append(kept, d)
			}
		}
		docs = kept
	}
	if docs, err = s.presentDocs(docs); err != nil {
		return nil, err
	}
	return &model.Cabinet{Project: project, Layout: l, Docs: docs, LayoutSaved: saved, LayoutVersion: ver}, nil
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
	if g != nil && !g.CanReadDoc(*d) {
		l, _, _ := s.layoutOf(org, d.Project)
		return nil, fail(403, "%s沒有權限讀「%s」的文件", g.Name, s.docPlace(org, d.Project, l, d.Section, d.TabPath()))
	}
	return s.presentDoc(d)
}

// docPlace 錯誤訊息用的位置:未分專案是「區塊 › 頁籤」,專案的前面加「專案名稱 › 」
func (s *Service) docPlace(org, project string, l model.Layout, section, tab string) string {
	loc := l.Location(section, tab)
	if project == "" {
		return loc
	}
	if x, err := s.Store.GetProject(org, project); err == nil {
		return x.Name + " › " + loc
	}
	return project + " › " + loc
}

// checkPlace 位置要在專案(空 = 未分專案)的結構裡;角色身分要能寫
func (s *Service) checkPlace(org, project string, l model.Layout, g *model.Agent, section string, tab *string) error {
	where := "文件櫃"
	if project != "" {
		where = "專案 " + project + " 的文件櫃"
	}
	if !l.Has(section, tabStr(tab)) {
		if l.Section(section) == nil || l.Section(section).ID != section {
			return fail(400, "%s沒有區塊 %s", where, section)
		}
		return fail(400, "%s的區塊 %s 沒有頁籤 %s", where, section, tabStr(tab))
	}
	if g != nil && !g.CanWriteIn(project, section, tabStr(tab)) {
		return fail(403, "%s沒有權限寫入「%s」", g.Name, s.docPlace(org, project, l, section, tabStr(tab)))
	}
	return nil
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
	by, err := s.ResolveAuthor(p, org, in.Author, AuthorIgnore) // 角色憑證是角色、成員是自己、管理員只能代角色與系統
	if err != nil {
		return nil, err
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	project, err := s.projectID(org, in.Project)
	if err != nil {
		return nil, err
	}
	l, _, err := s.layoutOf(org, project)
	if err != nil {
		return nil, err
	}
	if err := s.checkPlace(org, project, l, g, in.Section, in.Tab); err != nil {
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
	d := model.CabinetDoc{Project: project, Title: in.Title, Body: in.Body, Section: in.Section, Tab: in.Tab, Tags: model.CleanLabels(in.Tags),
		Author: by.ID, CreatedAt: now, UpdatedAt: now}
	saved, err := s.Store.CreateDoc(org, d)
	if err != nil {
		return nil, err
	}
	detail := map[string]any{"id": saved.ID}
	if project != "" {
		detail["project"] = project
	}
	s.Audit(p, "doc.create", org, detail)
	return s.presentDoc(&saved)
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
	d, err := s.updateDocLocked(p, g, org, id, cur, in)
	if err != nil {
		return nil, err
	}
	return s.presentDoc(d)
}

// updateDocLocked UpdateDoc 的本體(呼叫時要拿著 cabinetMu;g 是角色身分的角色,人是 nil)
func (s *Service) updateDocLocked(p *model.Principal, g *model.Agent, org, id string, cur *model.CabinetDoc, in DocPatch) (*model.CabinetDoc, error) {
	l, _, err := s.layoutOf(org, cur.Project)
	if err != nil {
		return nil, err
	}
	if g != nil {
		if !g.CanReadDoc(*cur) {
			return nil, fail(404, "找不到文件 %s", id)
		}
		if !g.CanWriteDoc(*cur) {
			return nil, fail(403, "%s沒有權限寫入「%s」", g.Name, s.docPlace(org, cur.Project, l, cur.Section, cur.TabPath()))
		}
	}
	d := cur.Clone()
	// 搬到別的專案:目標專案要存在,結構要有對應的區塊與頁籤(沒給 Section 時沿用原本的位置)
	if in.Project != nil {
		target, err := s.projectID(org, *in.Project)
		if err != nil {
			return nil, err
		}
		if target != cur.Project {
			tl, _, err := s.layoutOf(org, target)
			if err != nil {
				return nil, err
			}
			l, d.Project = tl, target
			if in.Section == nil {
				if err := s.checkPlace(org, target, l, g, d.Section, d.Tab); err != nil {
					return nil, err
				}
			}
		}
	}
	if in.Section != nil {
		sec, tab := strings.TrimSpace(*in.Section), emptyTab(in.Tab)
		if err := s.checkPlace(org, d.Project, l, g, sec, tab); err != nil {
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
	detail := map[string]any{"id": d.ID}
	if d.Project != cur.Project {
		detail["project"], detail["fromProject"] = d.Project, cur.Project
	}
	s.Audit(p, "doc.update", org, detail)
	return &d, nil
}

func sameDoc(a, b model.CabinetDoc) bool {
	if a.Project != b.Project || a.Title != b.Title || a.Body != b.Body || a.Section != b.Section || a.TabPath() != b.TabPath() || len(a.Tags) != len(b.Tags) {
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

// SaveCabinetLayout 改專案(空 = 未分專案)的結構(組織管理員):先檢查(被拿掉的區塊、頁籤裡不能還有這個專案的文件)。專案不存在 404
//
// 這個版本不鎖(無條件覆蓋,版本號照樣 +1),給內部(範本套用等)與沒有版本號的舊呼叫端;要樂觀鎖用 SaveCabinetLayoutIf
func (s *Service) SaveCabinetLayout(p *model.Principal, org, project string, layout []model.CabinetSection) ([]model.CabinetSection, error) {
	l, _, err := s.SaveCabinetLayoutIf(p, org, project, layout, nil)
	return l, err
}

// SaveCabinetLayoutIf 同 SaveCabinetLayout,多一個樂觀鎖:expected 非 nil 時,結構目前的版本(GetCabinet 的 LayoutVersion;沒存過是 0)
// 必須等於它才存,否則 409(訊息「結構已被別人更新,請重新載入」,Data.currentVersion 是目前版本)。
// nil = 不鎖(舊客戶端:允許,但審計紀錄標 unlocked,方便追查覆蓋)。回傳存好的結構與新版本號
func (s *Service) SaveCabinetLayoutIf(p *model.Principal, org, project string, layout []model.CabinetSection, expected *int64) ([]model.CabinetSection, int64, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, 0, err
	}
	if layout == nil {
		return nil, 0, fail(400, "要給 layout")
	}
	for i := range layout {
		if layout[i].Tabs == nil {
			layout[i].Tabs = []model.CabinetTab{}
		}
	}
	s.cabinetMu.Lock()
	defer s.cabinetMu.Unlock()
	// 限制:下面「被移除區塊裡不能有文件」的檢查在版本鎖之外;cabinetMu 只擋同一實例,跨實例時可能與文件新增競態
	project, err := s.projectID(org, project)
	if err != nil {
		return nil, 0, err
	}
	docs, err := s.Store.ListDocs(org)
	if err != nil {
		return nil, 0, err
	}
	if e := model.ValidateCabinetLayout(layout, docsIn(docs, project)); e != "" {
		return nil, 0, fail(400, "%s", e)
	}
	ver, err := s.Store.SaveCabinetLayout(org, project, layout, expected)
	var vc *store.VersionConflictError
	if errors.As(err, &vc) {
		return nil, 0, &Error{Code: 409, Msg: fmt.Sprintf("結構已被別人更新,請重新載入(目前版本 %d,你的是 %d)", vc.Current, *expected),
			Data: map[string]any{"currentVersion": vc.Current}}
	}
	if err != nil {
		return nil, 0, err
	}
	detail := map[string]any{"sections": len(layout), "version": ver}
	if expected == nil {
		detail["unlocked"] = true // 沒帶 expectedVersion(舊客戶端):無鎖覆蓋
	}
	if project != "" {
		detail["project"] = project
	}
	s.Audit(p, "cabinet.layout", org, detail)
	return layout, ver, nil
}
