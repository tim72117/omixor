package core

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/templates"
)

// 文件櫃:資料與功能都在核心(omixor-core;結構、文件、權限、範本檢查),同 Swift 版 Cabinet/Cabinet.swift:
//
//	本機模式:內建核心(service)+ 本機 PostgreSQL(omixor_cabinet_layouts、omixor_docs)
//	遠端模式:用 core/client 連線上的 omixor server(文件櫃存在雲端)
//
// 這個檔案只是轉接層(同 agents.go 的做法):
//   - 讀:畫面讀 App 的快取(cabState,自己有鎖,不依賴 Mu;本機載入時讀、每 5 秒背景重讀,遠端模式也是背景讀)
//   - 寫:CabinetOps 交給核心,成功後更新快取、寫動態、通知畫面。呼叫時不要拿著 Mu
//   - 舊資料:本機組織資料夾裡舊的 cabinet.json / cabinet-layout.json 在組織載入時匯入核心一次
//     (寫 cabinet-imported.json 當記號;舊檔案不刪)
//   - 範本:沒存過結構的組織,開了範本(OMIXOR_TEMPLATES)時用 templates/cabinet/ 的結構;新組織用空白結構或複製目前的
//
// MCP 工具(ops_cabinet、ops_docs、ops_doc_save)在核心(omixor-core/mcp),透過 backend 操作。

type (
	CabinetTab     = model.CabinetTab
	CabinetSection = model.CabinetSection
	CabinetDoc     = model.CabinetDoc
)

func cabSP(s string) *string { return &s }

// BlankCabinetLayout 新的空白組織用的結構
func BlankCabinetLayout() []CabinetSection { return model.BlankCabinetLayout() }

// TemplateCabinetLayout 範本的文件櫃結構(templates/cabinet/,一個區塊一個檔);沒開範本就是空白的結構
func TemplateCabinetLayout() []CabinetSection {
	var layout []CabinetSection
	if err := json.Unmarshal(templates.Array("cabinet"), &layout); err != nil || len(layout) == 0 {
		return BlankCabinetLayout()
	}
	return layout
}

// ---- 快取 ----

type cabState struct {
	mu      sync.Mutex
	org     string
	loaded  bool
	cab     model.Cabinet
	syncing bool
	err     string
	// migrateErr 本機舊資料(cabinet.json)沒有匯入的原因(核心已經有文件);匯入成功前一直顯示在畫面
	migrateErr string
}

var cabStates sync.Map // *App → *cabState

func (a *App) cabSt() *cabState {
	if s, ok := cabStates.Load(a); ok {
		return s.(*cabState)
	}
	s, _ := cabStates.LoadOrStore(a, &cabState{})
	return s.(*cabState)
}

func emptyCabinet() model.Cabinet {
	return model.Cabinet{Layout: BlankCabinetLayout(), Docs: []CabinetDoc{}}
}

// cabinetRefresh 從核心重讀 org 的文件櫃換進快取(不拿 Mu),回傳內容有沒有變
func (a *App) cabinetRefresh(org string) bool {
	var cab *model.Cabinet
	be, err := agentBackend()
	if err == nil {
		cab, err = be.GetCabinet(org)
	}
	st := a.cabSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.err = err.Error()
		if st.org != org || !st.loaded {
			st.org, st.cab, st.loaded = org, emptyCabinet(), true
			return true
		}
		return false
	}
	if cab.Docs == nil {
		cab.Docs = []CabinetDoc{}
	}
	if cab.Layout == nil {
		cab.Layout = []CabinetSection{}
	}
	changed := st.org != org || !st.loaded || string(Marshal(st.cab)) != string(Marshal(*cab))
	st.org, st.cab, st.loaded, st.err = org, *cab, true, ""
	return changed
}

// cabinetSync 遠端:在背景重讀 org,有變就通知畫面(同時只跑一個)
func (a *App) cabinetSync(org string) {
	st := a.cabSt()
	st.mu.Lock()
	if st.syncing {
		st.mu.Unlock()
		return
	}
	st.syncing = true
	st.mu.Unlock()
	changed := a.cabinetRefresh(org)
	st.mu.Lock()
	st.syncing = false
	st.mu.Unlock()
	if changed {
		a.Changed("cabinet")
	}
}

// cabinetOf org 的文件櫃(快取;還沒載入:本機當場讀,遠端先給空的並在背景讀)。不需要拿 Mu
func (a *App) cabinetOf(org string) model.Cabinet {
	st := a.cabSt()
	st.mu.Lock()
	cur := st.loaded && st.org == org
	st.mu.Unlock()
	if !cur {
		if IsRemote() {
			st.mu.Lock()
			st.org, st.cab, st.loaded = org, emptyCabinet(), true
			st.mu.Unlock()
			go a.cabinetSync(org)
		} else {
			a.cabinetRefresh(org)
		}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.cab
}

// CabinetError 最近一次讀核心的錯誤(成功後清掉)
func (a *App) CabinetError() string {
	st := a.cabSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.migrateErr != "" {
		if st.err != "" {
			return st.migrateErr + ";" + st.err
		}
		return st.migrateErr
	}
	return st.err
}

// ---- 讀(目前組織;呼叫時要拿著 Mu,因為要知道目前組織) ----

// CabinetLayout 目前組織的文件櫃結構
func (a *App) CabinetLayout() []CabinetSection { return a.cabinetOf(a.CurrentOrgID()).Layout }

// CabinetDocs 全部文件(最新的在前)
func (a *App) CabinetDocs() []CabinetDoc { return a.cabinetOf(a.CurrentOrgID()).Docs }

func (a *App) layout() model.Layout { return model.Layout(a.CabinetLayout()) }

// CabinetSectionByID 依 id 或名稱找區塊
func (a *App) CabinetSectionByID(id string) *CabinetSection { return a.layout().Section(id) }

// CabinetTabByPath 依網址找頁籤
func (a *App) CabinetTabByPath(path *string) *CabinetTab {
	if path == nil {
		return nil
	}
	return a.layout().TabByPath(*path)
}

// ResolveCabinet 依 id、名稱或網址找區塊;頁籤可給網址、名稱或代號(同 Swift 的 CabinetLayout.resolve)
func (a *App) ResolveCabinet(section, tab *string) (*CabinetSection, *CabinetTab, error) {
	s, t, msg := a.layout().Resolve(section, tab)
	if msg != "" {
		return nil, nil, UserError{msg}
	}
	return s, t, nil
}

// DocLocation 「課程 › L0 AI 工具介紹」
func (a *App) DocLocation(section string, tab *string) string {
	t := ""
	if tab != nil {
		t = *tab
	}
	return a.layout().Location(section, t)
}

// FindDoc 依 id 找文件(不分大小寫)
func (a *App) FindDoc(id string) *CabinetDoc {
	for _, d := range a.CabinetDocs() {
		if strings.EqualFold(d.ID, id) {
			c := d.Clone()
			return &c
		}
	}
	return nil
}

// ValidateCabinetLayout 檢查新結構(同 Swift 的 Cabinet.validate;核心存檔時也會再檢查一次)
func (a *App) ValidateCabinetLayout(sections []CabinetSection) string {
	return model.ValidateCabinetLayout(sections, a.CabinetDocs())
}

// ---- 寫:交給核心,成功後更新快取、寫動態、通知畫面 ----

// CabinetOps 文件櫃的寫入。呼叫時不要拿著 Mu
type CabinetOps struct{ a *App }

func (a *App) CabinetOps() CabinetOps { return CabinetOps{a} }

func (o CabinetOps) done(org string) {
	o.a.cabinetRefresh(org)
	o.a.Changed("cabinet")
}

func (o CabinetOps) GetCabinet(org string) (*model.Cabinet, error) {
	b, err := agentBackend()
	if err != nil {
		return nil, err
	}
	return b.GetCabinet(org)
}

func (o CabinetOps) GetDoc(org, id string) (*CabinetDoc, error) {
	b, err := agentBackend()
	if err != nil {
		return nil, err
	}
	return b.GetDoc(org, id)
}

// CreateDoc 新增文件並記到動態牆(同 Swift 的 Cabinet.create)
func (o CabinetOps) CreateDoc(org string, in service.DocInput) (*CabinetDoc, error) {
	b, err := agentBackend()
	if err != nil {
		return nil, err
	}
	d, err := b.CreateDoc(org, in)
	if err != nil {
		return nil, err
	}
	o.done(org)
	loc := model.Layout(o.a.cabinetOf(org).Layout).Location(d.Section, d.TabPath())
	o.a.Mu.Lock()
	o.a.Log(AuthorActor(d.Author), "archivebox", "文件櫃新增 "+d.ID+"「"+d.Title+"」("+loc+")", "cabinet")
	o.a.Mu.Unlock()
	return d, nil
}

// UpdateDoc 修改文件(不記動態牆,同 Swift)
func (o CabinetOps) UpdateDoc(org, id string, p service.DocPatch) (*CabinetDoc, error) {
	b, err := agentBackend()
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
	b, err := agentBackend()
	if err != nil {
		return nil, err
	}
	return b.ReadDoc(org, id, in)
}

// EditDoc 局部修改(字串取代、整段替換、段尾附加);改完更新快取、通知畫面
func (o CabinetOps) EditDoc(org, id string, in service.DocEdit) (*service.DocEditResult, error) {
	b, err := agentBackend()
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
	b, err := agentBackend()
	if err != nil {
		return err
	}
	if err = b.DeleteDoc(org, id); err == nil {
		o.done(org)
	}
	return err
}

// SaveLayout 存新結構(核心檢查:被拿掉的區塊、頁籤裡不能還有文件;遠端模式要組織管理員)
func (o CabinetOps) SaveLayout(org string, layout []CabinetSection) ([]CabinetSection, error) {
	b, err := agentBackend()
	if err != nil {
		return nil, err
	}
	l, err := b.SaveCabinetLayout(org, layout)
	if err == nil {
		o.done(org)
	}
	return l, err
}

func (o CabinetOps) Import(org string, in service.CabinetImport) (*service.CabinetImportResult, error) {
	b, err := agentBackend()
	if err != nil {
		return nil, err
	}
	res, err := b.ImportCabinet(org, in)
	if err == nil {
		o.done(org)
	}
	return res, err
}

// ---- 舊資料(本機的 cabinet.json / cabinet-layout.json)一次性匯入 ----

const cabinetImportedFile = "cabinet-imported.json"

// readOldCabinet 讀組織資料夾裡舊的檔案。layout 沒有檔案是 nil;cabinet.json 讀不出來時不覆蓋,先改名保留(同 Swift)
func readOldCabinet(dir string) (layout []CabinetSection, docs []CabinetDoc, found bool) {
	lp, dp := filepath.Join(dir, "cabinet-layout.json"), filepath.Join(dir, "cabinet.json")
	if err := ReadJSON(lp, &layout); err == nil && layout != nil {
		found = true
	} else {
		layout = nil
	}
	b, err := ReadData(dp)
	if err != nil {
		return layout, nil, found
	}
	found = true
	var raws []json.RawMessage
	bad := json.Unmarshal(b, &raws) != nil || raws == nil
	now := model.Time{Time: time.Now().UTC().Truncate(time.Second)}
	for _, r := range raws {
		d, err := model.ParseDocJSON(r, MCPAuthor, now)
		if err != nil {
			bad = true
			break
		}
		docs = append(docs, d)
	}
	if bad {
		keep := strings.TrimSuffix(dp, ".json") + fmt.Sprintf(".unreadable-%d.json", time.Now().Unix())
		_ = RenameData(dp, keep)
		docs = nil
	}
	return layout, docs, found
}

// migrateCabinet 本機模式:組織資料夾裡有舊的文件櫃檔案、還沒匯入過,就匯入核心一次;
// 沒有舊檔案、核心也沒存過結構時,開了範本就存範本的結構。呼叫時可以拿著 Mu(只碰本機核心)
func (a *App) migrateCabinet(org string) {
	if IsRemote() || org == "" {
		return
	}
	if _, err := agentBackend(); err != nil {
		return
	}
	dir := OrgDir(org)
	ops := a.CabinetOps()
	if !Exists(filepath.Join(dir, cabinetImportedFile)) {
		if layout, docs, found := readOldCabinet(dir); found {
			mark := map[string]any{"at": ISO(time.Now()), "docs": 0, "layout": false}
			if layout == nil && len(docs) == 0 {
				_ = WriteJSON(filepath.Join(dir, cabinetImportedFile), mark)
			} else {
				res, err := ops.Import(org, service.CabinetImport{Layout: layout, Docs: docs})
				st := a.cabSt()
				msg := ""
				switch {
				case err == nil:
					mark["docs"], mark["layout"] = res.Docs, res.LayoutSaved
					_ = WriteJSON(filepath.Join(dir, cabinetImportedFile), mark)
				case cabCode(err) == 400: // 舊資料不合格:重試也不會過,記下來不再重試(舊檔案留著)
					mark["error"] = err.Error()
					_ = WriteJSON(filepath.Join(dir, cabinetImportedFile), mark)
					log.Printf("文件櫃舊資料沒有匯入(%s):%v", org, err)
				case cabCode(err) == 409: // 核心已經有文件:不寫記號(舊檔案留著,下次載入再試),並顯示在畫面上讓人處理
					msg = "本機舊的文件櫃資料(" + filepath.Join(dir, "cabinet.json") + ")還沒匯入:" + err.Error()
					log.Printf("文件櫃舊資料沒有匯入(%s):%v", org, err)
				default:
					log.Printf("文件櫃舊資料匯入失敗(%s):%v", org, err)
				}
				st.mu.Lock()
				st.migrateErr = msg
				st.mu.Unlock()
			}
		}
	}
	if !templates.Enabled() {
		return
	}
	if cab, err := ops.GetCabinet(org); err == nil && !cab.LayoutSaved {
		_, _ = ops.SaveLayout(org, TemplateCabinetLayout())
	}
}

// cabCode 核心回的錯誤的 HTTP 狀態碼(本機與遠端一致);其他是 0
func cabCode(err error) int { return backend.Code(err) }

func init() {
	OnOrgLoad(func(a *App) {
		org := a.CurrentOrgID()
		a.migrateCabinet(org)
		if !IsRemote() {
			a.cabinetRefresh(org)
		}
	})
	OnOrgCreate(func(a *App, dir, template string) {
		// 新組織的文件櫃結構:空白,或複製目前組織的(本機核心才在這裡做;遠端模式的組織在雲端)
		if IsRemote() {
			return
		}
		layout := BlankCabinetLayout()
		if template == "copyCurrent" {
			layout = model.CloneLayout(a.CabinetLayout())
		}
		if be, err := agentBackend(); err == nil {
			if _, err := be.SaveCabinetLayout(filepath.Base(dir), layout); err != nil {
				log.Printf("新組織的文件櫃結構:%v", err)
			}
		}
	})
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		// 每 5 秒重讀目前組織的文件櫃(別的路徑改了資料、遠端的修改才看得到;網路與資料庫請求都在鎖外)
		go func() {
			for range time.Tick(5 * time.Second) {
				if org := a.currentOrgNow(); org != "" && a.cabinetRefresh(org) {
					a.Changed("cabinet")
				}
			}
		}()
	})
	RegisterView("cabinet", func(a *App) any {
		cab := a.cabinetOf(a.CurrentOrgID())
		v := map[string]any{"layout": cab.Layout, "docs": cab.Docs}
		if e := a.CabinetError(); e != "" {
			v["error"] = e
		}
		return v
	})
	RegisterAction("cabinet_layout_save", func(a *App, args map[string]any) (any, error) {
		var layout []CabinetSection
		if err := ReadInto(args["layout"], &layout); err != nil {
			return nil, err
		}
		if layout == nil {
			return nil, UserError{"要給 layout"}
		}
		l, err := a.CabinetOps().SaveLayout(a.currentOrgNow(), layout)
		if err != nil {
			return nil, agentUserErr(err)
		}
		return l, nil
	})
	RegisterAction("cabinet_doc_delete", func(a *App, args map[string]any) (any, error) {
		err := a.CabinetOps().DeleteDoc(a.currentOrgNow(), agentArg(args, "id"))
		if cabCode(err) == 404 { // 已經不在了:同原本的行為,不算錯
			return nil, nil
		}
		return nil, agentUserErr(err)
	})
}
