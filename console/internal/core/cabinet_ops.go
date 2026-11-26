package core

import (
	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 文件櫃與專案的寫入(CabinetOps);讀取、快取、舊資料匯入與畫面登記見 cabinet.go

// ---- 寫:交給核心,成功後更新快取、寫動態、通知畫面 ----

// CabinetOps 文件櫃的寫入。呼叫時不要拿著 Mu
type CabinetOps struct {
	a *App
	// who 網頁的呼叫者;nil = 系統身分(舊資料匯入、MCP 3765)
	who *Caller
}

func (a *App) CabinetOps() CabinetOps { return CabinetOps{a: a} }

// CabinetOpsFor 以網頁呼叫者的身分寫入(who 是 nil 同 CabinetOps)
func (a *App) CabinetOpsFor(who *Caller) CabinetOps { return CabinetOps{a: a, who: who} }

// be 寫入用的核心:呼叫者在 org 的身分
func (o CabinetOps) be(org string) (backend.Backend, error) { return coreBackendFor(o.who, "", org) }

// projectsDone 專案或成員寫入成功後:受限專案快取作廢、清單換新、通知畫面
func (o CabinetOps) projectsDone(org string) {
	o.a.InvalidateRestricted()
	o.a.projectsRefresh(org)
	o.a.Changed("cabinet")
}

// read 讀取用的核心:有呼叫者用他的身分(受限專案照核心的規則看不到),否則系統身分
func (o CabinetOps) read(org string) (backend.Backend, error) {
	if o.who != nil {
		return o.be(org)
	}
	return agentBackend()
}

func (o CabinetOps) done(org string) {
	o.a.cabinetRefresh(org)
	o.a.Changed("cabinet")
}

func (o CabinetOps) GetCabinet(org string) (*model.Cabinet, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.GetCabinet(org)
}

func (o CabinetOps) GetDoc(org, id string) (*CabinetDoc, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.GetDoc(org, id)
}

// CreateDoc 新增文件並記到動態牆(同 Swift 的 Cabinet.create)
func (o CabinetOps) CreateDoc(org string, in service.DocInput) (*CabinetDoc, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	d, err := b.CreateDoc(org, in)
	if err != nil {
		return nil, err
	}
	o.done(org)
	loc := o.docPlace(org, d)
	o.a.Mu.Lock()
	o.a.Log(AuthorActor(d.Author), "archivebox", "文件櫃新增 "+d.ID+"「"+d.Title+"」("+loc+")", "cabinet")
	o.a.Mu.Unlock()
	return d, nil
}

// UpdateDoc 修改文件(不記動態牆,同 Swift)
func (o CabinetOps) UpdateDoc(org, id string, p service.DocPatch) (*CabinetDoc, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	d, err := b.UpdateDoc(org, id, p)
	if err == nil {
		o.done(org)
	}
	return d, err
}

// ReadDoc 分段讀(大綱、段落、行號範圍、搜尋;見 omixor-core/model/docsections.go)
func (o CabinetOps) ReadDoc(org, id string, in service.DocRead) (*service.DocView, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.ReadDoc(org, id, in)
}

// EditDoc 局部修改(字串取代、整段替換、段尾附加);改完更新快取、通知畫面
func (o CabinetOps) EditDoc(org, id string, in service.DocEdit) (*service.DocEditResult, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	r, err := b.EditDoc(org, id, in)
	if err == nil && !r.Unchanged {
		o.done(org)
	}
	return r, err
}

func (o CabinetOps) DeleteDoc(org, id string) error {
	b, err := o.be(org)
	if err != nil {
		return err
	}
	if err = b.DeleteDoc(org, id); err == nil {
		o.done(org)
	}
	return err
}

// SaveLayout 存未分專案的新結構(核心檢查:被拿掉的區塊、頁籤裡不能還有文件;遠端模式要組織管理員)
// expected 非 nil 是樂觀鎖(版本不符 409,訊息「結構已被別人更新,請重新載入」);nil 是無鎖覆蓋
func (o CabinetOps) SaveLayout(org string, layout []CabinetSection, expected *int64) ([]CabinetSection, error) {
	return o.SaveProjectLayout(org, "", layout, expected)
}

// SaveProjectLayout 存某個專案的新結構(project 空 = 未分專案;不存在的專案 404)
func (o CabinetOps) SaveProjectLayout(org, project string, layout []CabinetSection, expected *int64) ([]CabinetSection, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	var l []CabinetSection
	if project == "" {
		l, err = b.SaveCabinetLayout(org, layout, expected)
	} else {
		l, err = b.SaveProjectCabinetLayout(org, project, layout, expected)
	}
	if err == nil || cabCode(err) == 409 { // 版本衝突:別人改過了,先把最新的結構換進快取、通知畫面重讀
		o.done(org)
	}
	return l, err
}

// GetProjectCabinet 某個專案的結構與文件(project 空 = 未分專案;不存在的專案 404)。直接問核心,不走快取
func (o CabinetOps) GetProjectCabinet(org, project string) (*model.Cabinet, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	if project == "" {
		return b.GetCabinet(org)
	}
	return b.GetProjectCabinet(org, project)
}

// docPlace 文件在動態牆上的位置:「區塊 › 頁籤」,專案的文件前面加專案名稱
func (o CabinetOps) docPlace(org string, d *CabinetDoc) string {
	if d.Project == "" {
		return model.Layout(o.a.cabinetOf(org).Layout).Location(d.Section, d.TabPath())
	}
	name := d.Project
	if l, err := o.ListProjects(org); err == nil {
		for _, x := range l {
			if x.ID == d.Project {
				name = x.Name
			}
		}
	}
	if c, err := o.GetProjectCabinet(org, d.Project); err == nil {
		return name + " › " + model.Layout(c.Layout).Location(d.Section, d.TabPath())
	}
	return name + " › " + d.Section
}

// ---- 專案(組織底下的一層,目前只有文件櫃分專案;見 omixor-core/model/project.go) ----

func (o CabinetOps) ListProjects(org string) ([]model.Project, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.ListProjects(org)
}

// CreateProject 新增專案(核心:組織管理員;名稱必填、不重複)
func (o CabinetOps) CreateProject(org string, in service.ProjectInput) (*model.Project, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	x, err := b.CreateProject(org, in)
	if err != nil {
		return nil, err
	}
	o.projectsDone(org)
	return x, nil
}

// UpdateProject 改專案名稱或說明
func (o CabinetOps) UpdateProject(org, id string, p service.ProjectPatch) (*model.Project, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	x, err := b.UpdateProject(org, id, p)
	if err == nil {
		o.projectsDone(org)
	}
	return x, err
}

// DeleteProject 刪除專案(裡面還有文件時核心回 409)
func (o CabinetOps) DeleteProject(org, id string) error {
	b, err := o.be(org)
	if err != nil {
		return err
	}
	if err = b.DeleteProject(org, id); err == nil {
		o.projectsDone(org)
	}
	return err
}

// LinkProjectRepo 連結專案與程式碼倉庫(核心:組織管理員;只用於預設值與篩選,不是權限來源)
func (o CabinetOps) LinkProjectRepo(org, project, repo string) (*model.Project, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	x, err := b.LinkProjectRepo(org, project, repo)
	if err == nil {
		o.projectsDone(org)
	}
	return x, err
}

// UnlinkProjectRepo 取消專案與程式碼倉庫的連結(不影響已經建立的 Issue)
func (o CabinetOps) UnlinkProjectRepo(org, project, repo string) (*model.Project, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	x, err := b.UnlinkProjectRepo(org, project, repo)
	if err == nil {
		o.projectsDone(org)
	}
	return x, err
}

// ListProjectMembers 專案成員(核心:看得到專案的組織成員;受限專案要是專案成員或組織管理員)
func (o CabinetOps) ListProjectMembers(org, project string) ([]model.ProjectMember, error) {
	b, err := o.read(org)
	if err != nil {
		return nil, err
	}
	return b.ListProjectMembers(org, project)
}

// SetProjectMember 加入專案成員或改角色(核心:組織管理員或專案管理員)
func (o CabinetOps) SetProjectMember(org, project string, in service.ProjectMemberInput) (*model.ProjectMember, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	m, err := b.SetProjectMember(org, project, in)
	if err == nil {
		o.projectsDone(org)
	}
	return m, err
}

// RemoveProjectMember 移除專案成員(核心:組織管理員或專案管理員)
func (o CabinetOps) RemoveProjectMember(org, project, actor string) error {
	b, err := o.be(org)
	if err != nil {
		return err
	}
	err = b.RemoveProjectMember(org, project, actor)
	if err == nil {
		o.projectsDone(org)
	}
	return err
}

func (o CabinetOps) Import(org string, in service.CabinetImport) (*service.CabinetImportResult, error) {
	b, err := o.be(org)
	if err != nil {
		return nil, err
	}
	res, err := b.ImportCabinet(org, in)
	if err == nil {
		o.done(org)
	}
	return res, err
}
