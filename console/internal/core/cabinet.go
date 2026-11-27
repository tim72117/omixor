package core

import (
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omixor-core/backend"
	"omixor-core/config"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/templates"
)

// 文件櫃:資料與功能都在核心(omixor-core;結構、文件、權限、範本檢查),同 Swift 版 Cabinet/Cabinet.swift:
//
//	內建核心(service)+ PostgreSQL(omixor_cabinet_layouts、omixor_docs)
//
// 這個檔案只是轉接層(同 agents.go 的做法):
//   - 讀:畫面讀 App 的快取(cabState,自己有鎖,不依賴 Mu;載入時讀、每 5 秒背景重讀)
//   - 寫:CabinetOps 交給核心,成功後更新快取、寫動態、通知畫面。呼叫時不要拿著 Mu
//   - 舊資料:本機組織資料夾裡舊的 cabinet.json / cabinet-layout.json 在組織載入時匯入核心一次
//     (寫 cabinet-imported.json 當記號;舊檔案不刪)
//   - 範本:沒存過結構的組織,開了範本(OMIXOR_TEMPLATES)時用 templates/cabinet/ 的結構;新組織用空白結構或複製目前的
//
// MCP 工具(cabinet、docs、doc_save)在核心(omixor-core/mcp),透過 backend 操作。

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
	mu     sync.Mutex
	org    string
	loaded bool
	cab    model.Cabinet
	err    string
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
	if org == "" { // 還沒有組織:空的,不問核心
		st := a.cabSt()
		st.mu.Lock()
		defer st.mu.Unlock()
		changed := st.org != "" || !st.loaded
		st.org, st.cab, st.loaded, st.err = "", emptyCabinet(), true, ""
		return changed
	}
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

// cabinetOf org 的文件櫃(快取;還沒載入時當場讀)。不需要拿 Mu
func (a *App) cabinetOf(org string) model.Cabinet {
	st := a.cabSt()
	st.mu.Lock()
	cur := st.loaded && st.org == org
	st.mu.Unlock()
	if !cur {
		a.cabinetRefresh(org)
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
		d, err := model.ParseDocJSON(r, LocalIdentity, now)
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

// migrateCabinet 組織資料夾裡有舊的文件櫃檔案、還沒匯入過,就匯入核心一次;
// 沒有舊檔案、核心也沒存過結構時,開了範本就存範本的結構。呼叫時可以拿著 Mu(只碰同一個程式裡的核心)
func (a *App) migrateCabinet(org string) {
	if org == "" {
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
		// expected=0:只在「從沒存過」時套範本;判斷與存檔之間別人存了就 409,略過、不覆蓋
		zero := int64(0)
		if _, err := ops.SaveLayout(org, TemplateCabinetLayout(), &zero); err != nil && cabCode(err) != 409 {
			log.Printf("套用文件櫃範本(%s):%v", org, err)
		}
	}
}

// cabCode 核心回的錯誤的 HTTP 狀態碼;其他是 0
func cabCode(err error) int { return backend.Code(err) }

func init() {
	OnOrgLoad(func(a *App) {
		org := a.CurrentOrgID()
		a.migrateCabinet(org)
		a.cabinetRefresh(org)
	})
	OnOrgCreate(func(a *App, dir, template string) {
		// 新組織的文件櫃結構:空白,或複製目前組織的
		layout := BlankCabinetLayout()
		if template == "copyCurrent" {
			layout = model.CloneLayout(a.CabinetLayout())
		}
		if be, err := coreBackendFor(a.caller, filepath.Base(dir)); err == nil { // 建立組織的人
			zero := int64(0) // 新組織第一次存;已經有人存了(409)就不覆蓋
			if _, err := be.SaveCabinetLayout(filepath.Base(dir), layout, &zero); err != nil && cabCode(err) != 409 {
				log.Printf("新組織的文件櫃結構:%v", err)
			}
		}
	})
	OnStart(func(a *App) {
		if config.Current().Bool(config.NoTimers) {
			return
		}
		// 每 5 秒重讀目前組織的文件櫃(別的路徑改了資料才看得到;資料庫請求在鎖外)
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
	// cabinet_query 畫面用:某個專案(project 空 = 未分專案)的結構與文件,加上組織的專案清單(給專案切換用)。
	// 未分專案讀快取(同 cabinet view);專案直接問核心。舊的 server 沒有專案功能時 projects 是空的、projectsError 說明原因
	RegisterAction("cabinet_query", func(a *App, args map[string]any) (any, error) {
		org := a.currentOrgNow()
		project := model.NormalizeProjectID(agentArg(args, "project"))
		ops := a.CabinetOpsFor(CallerOf(args)) // 專案的文件櫃、專案清單照呼叫者看得到的
		var cab model.Cabinet
		if project == "" {
			cab = a.cabinetOf(org)
		} else {
			c, err := ops.GetProjectCabinet(org, project)
			if err != nil {
				return nil, agentUserErr(err)
			}
			cab = *c
		}
		if cab.Layout == nil {
			cab.Layout = []CabinetSection{}
		}
		if cab.Docs == nil {
			cab.Docs = []CabinetDoc{}
		}
		out := map[string]any{"project": project, "layout": cab.Layout, "docs": cab.Docs, "layoutSaved": cab.LayoutSaved, "layoutVersion": cab.LayoutVersion}
		projects, err := ops.ListProjects(org)
		if projects == nil {
			projects = []model.Project{}
		}
		out["projects"] = projects
		if err != nil {
			out["projectsError"] = err.Error()
		}
		if e := a.CabinetError(); e != "" && project == "" {
			out["error"] = e
		}
		return out, nil
	})
	RegisterAction("project_create", func(a *App, args map[string]any) (any, error) {
		name := strings.TrimSpace(agentArg(args, "name"))
		restricted, _ := args["restricted"].(bool) // 受限專案:只有專案成員與組織管理員看得到
		x, err := a.CabinetOpsFor(CallerOf(args)).CreateProject(a.currentOrgNow(), service.ProjectInput{Name: name, Key: strings.TrimSpace(agentArg(args, "key")),
			Description: strings.TrimSpace(agentArg(args, "description")), Restricted: restricted})
		if err != nil {
			return nil, agentConflictErr(err) // 代號格式、重複、保留字的訊息原樣帶回
		}
		return x, nil
	})
	RegisterAction("project_update", func(a *App, args map[string]any) (any, error) {
		p := service.ProjectPatch{}
		if v, ok := args["name"].(string); ok {
			v = strings.TrimSpace(v)
			p.Name = &v
		}
		if v, ok := args["description"].(string); ok {
			v = strings.TrimSpace(v)
			p.Description = &v
		}
		if v, ok := args["key"].(string); ok { // "" = 清掉代號;專案已有 Issue(含垃圾桶)時核心回 409
			v = strings.TrimSpace(v)
			p.Key = &v
		}
		if v, ok := args["restricted"].(bool); ok { // 受限專案:只有專案成員與組織管理員看得到(核心:組織管理員)
			p.Restricted = &v
		}
		if p.Name == nil && p.Description == nil && p.Key == nil && p.Restricted == nil {
			return nil, UserError{"要給 name、key、description 或 restricted"}
		}
		x, err := a.CabinetOpsFor(CallerOf(args)).UpdateProject(a.currentOrgNow(), agentArg(args, "id"), p)
		if err != nil {
			return nil, agentConflictErr(err)
		}
		return x, nil
	})
	// 專案與 repo 的連結(參數 id = 專案 id、repo;核心:組織管理員)
	RegisterAction("project_repo_link", func(a *App, args map[string]any) (any, error) {
		x, err := a.CabinetOpsFor(CallerOf(args)).LinkProjectRepo(a.currentOrgNow(), agentArg(args, "id"), agentArg(args, "repo"))
		if err != nil {
			return nil, agentConflictErr(err)
		}
		return x, nil
	})
	RegisterAction("project_repo_unlink", func(a *App, args map[string]any) (any, error) {
		x, err := a.CabinetOpsFor(CallerOf(args)).UnlinkProjectRepo(a.currentOrgNow(), agentArg(args, "id"), agentArg(args, "repo"))
		if err != nil {
			return nil, agentConflictErr(err)
		}
		return x, nil
	})
	// 專案成員(參數 id = 專案 id;set 帶 actor、role;remove 帶 actor;核心:組織管理員或專案管理員)
	RegisterAction("project_members", func(a *App, args map[string]any) (any, error) {
		l, err := a.CabinetOps().ListProjectMembers(a.currentOrgNow(), agentArg(args, "id"))
		if err != nil {
			return nil, agentUserErr(err)
		}
		if l == nil {
			l = []model.ProjectMember{}
		}
		return l, nil
	})
	RegisterAction("project_member_set", func(a *App, args map[string]any) (any, error) {
		m, err := a.CabinetOpsFor(CallerOf(args)).SetProjectMember(a.currentOrgNow(), agentArg(args, "id"),
			service.ProjectMemberInput{Actor: agentArg(args, "actor"), Role: agentArg(args, "role")})
		if err != nil {
			return nil, agentConflictErr(err)
		}
		return m, nil
	})
	RegisterAction("project_member_remove", func(a *App, args map[string]any) (any, error) {
		return nil, agentUserErr(a.CabinetOpsFor(CallerOf(args)).RemoveProjectMember(a.currentOrgNow(), agentArg(args, "id"), agentArg(args, "actor")))
	})
	RegisterAction("project_delete", func(a *App, args map[string]any) (any, error) {
		return nil, agentUserErr(a.CabinetOpsFor(CallerOf(args)).DeleteProject(a.currentOrgNow(), agentArg(args, "id")))
	})
	// cabinet_doc_get 依 id 找文件(任何專案;畫面從別頁跳過來時用來切到文件所在的專案)。找不到回 null
	RegisterAction("cabinet_doc_get", func(a *App, args map[string]any) (any, error) {
		d, err := a.CabinetOpsFor(CallerOf(args)).GetDoc(a.currentOrgNow(), agentArg(args, "id"))
		if cabCode(err) == 404 || (err == nil && d != nil && HiddenOf(args).Project(d.Project)) {
			return nil, nil
		}
		if err != nil {
			return nil, agentUserErr(err)
		}
		return d, nil
	})
	RegisterAction("cabinet_layout_save", func(a *App, args map[string]any) (any, error) {
		var layout []CabinetSection
		if err := ReadInto(args["layout"], &layout); err != nil {
			return nil, err
		}
		if layout == nil {
			return nil, UserError{"要給 layout"}
		}
		project := model.NormalizeProjectID(agentArg(args, "project"))
		var expected *int64 // 畫面載入結構時記下的 layoutVersion;沒帶(舊畫面)= 無鎖覆蓋
		if v, ok := args["expectedVersion"]; ok && v != nil {
			var n int64
			if err := ReadInto(v, &n); err != nil {
				return nil, UserError{"expectedVersion 要是數字"}
			}
			expected = &n
		}
		l, err := a.CabinetOpsFor(CallerOf(args)).SaveProjectLayout(a.currentOrgNow(), project, layout, expected)
		if err != nil {
			return nil, agentConflictErr(err) // 版本衝突 409 帶 currentVersion 給畫面
		}
		return l, nil
	})
	RegisterAction("cabinet_doc_delete", func(a *App, args map[string]any) (any, error) {
		err := a.CabinetOpsFor(CallerOf(args)).DeleteDoc(a.currentOrgNow(), agentArg(args, "id"))
		if cabCode(err) == 404 { // 已經不在了:同原本的行為,不算錯
			return nil, nil
		}
		return nil, agentUserErr(err)
	})
}
