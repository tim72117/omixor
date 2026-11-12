package core

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"omixor-core/model"
	"omixor-web/internal/templates"
)

// 組織:同 Swift 版 Orgs.swift。每個組織的資料放在 <資料夾>/orgs/<組織 id>/,orgs.json 記清單與目前的組織。
// 第一次啟動時,舊版直接放在 <資料夾> 的 issues.json 等檔案會複製到預設組織(原檔保留)。
//
// 各功能用這三個鉤子跟組織連動(在 init() 登記):
//   OnOrgLoad(f)   啟動時與切換組織後,從 a.DataDir() 重新讀自己的檔案(呼叫時拿著 Mu)
//   OnOrgCreate(f) 新增組織時寫初始檔案(template 是 "blank" 或 "copyCurrent";呼叫時拿著 Mu,dir 是新組織的資料夾)
//   OnOrgDelete(f) 刪除組織前清掉該組織的東西(例如鑰匙圈裡的登入;呼叫時拿著 Mu)

const DefaultOrgID = "default"

var PerOrgFiles = []string{"issues.json", "agents.json", "cabinet.json", "cabinet-layout.json", "connections.json", "github.json", "gcp.json"}

type Organization struct {
	Color     string `json:"color"`
	CreatedAt Time   `json:"createdAt"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Note      string `json:"note"`
	Symbol    string `json:"symbol"`
}

type OrgIndex struct {
	Current string         `json:"current"`
	Orgs    []Organization `json:"orgs"`
}

type OrgState struct {
	Index OrgIndex
}

var (
	orgLoadHooks   []func(*App)
	orgCreateHooks []func(a *App, dir, template string)
	orgDeleteHooks []func(a *App, id, dir string)
)

func OnOrgLoad(f func(*App))                           { orgLoadHooks = append(orgLoadHooks, f) }
func OnOrgCreate(f func(a *App, dir, template string)) { orgCreateHooks = append(orgCreateHooks, f) }
func OnOrgDelete(f func(a *App, id, dir string))       { orgDeleteHooks = append(orgDeleteHooks, f) }

// 組織索引:本機模式 orgs.json;遠端模式 orgs-remote.json(伺服器的組織清單,上次讀到的)
func orgIndexPath() string {
	if IsRemote() {
		return remoteOrgsPath()
	}
	return filepath.Join(DataRoot(), "orgs.json")
}

// OrgDir 組織的資料夾(不存在就建立)
func OrgDir(id string) string {
	d := filepath.Join(DataRoot(), "orgs", id)
	_ = os.MkdirAll(d, 0o755)
	return d
}

func (a *App) orgs() *OrgState {
	if s, ok := a.Ext["orgs"].(*OrgState); ok {
		return s
	}
	s := &OrgState{Index: loadOrgIndex()}
	a.Ext["orgs"] = s
	return s
}

// Orgs 組織清單與目前的組織(呼叫時要拿著 Mu)
func (a *App) Orgs() OrgIndex { return a.orgs().Index }

// CurrentOrgID 目前組織(呼叫時要拿著 Mu)
func (a *App) CurrentOrgID() string { return a.orgs().Index.Current }

// DataDir 目前組織的資料夾(呼叫時要拿著 Mu)
func (a *App) DataDir() string { return OrgDir(a.CurrentOrgID()) }

func loadOrgIndex() OrgIndex {
	var idx OrgIndex
	if IsRemote() {
		// 遠端:只有伺服器的組織。沒存過清單(第一次、或環境變數直接指定遠端)就當場讀一次,連不上就先空著
		if err := ReadJSON(orgIndexPath(), &idx); err != nil || len(idx.Orgs) == 0 {
			idx = OrgIndex{Orgs: []Organization{}}
			if list, err := fetchRemoteOrgs(IssueRemoteURL()); err == nil && len(list) > 0 {
				idx = OrgIndex{Current: list[0].ID, Orgs: list}
				_ = WriteJSON(orgIndexPath(), idx)
			}
		}
		if idx.Orgs == nil {
			idx.Orgs = []Organization{}
		}
		return idx
	}
	return loadLocalOrgIndex()
}

// localOrgErr 本機模式讀不到組織的原因(沒有資料庫等);正常時是空的。給畫面顯示
var localOrgErr string

// loadLocalOrgIndex 本機模式:組織以核心(omixor-core,PostgreSQL)的組織表為準,名稱與建立時間照核心;
// orgs.json 只是覆蓋層,存每個組織的圖示、顏色、備註與目前的組織
// fromCoreOrg 核心的組織 → web 的組織(圖示、顏色沒填的用預設;prefix 是遠端組織的 id 前綴)
func fromCoreOrg(o model.Org, prefix string) Organization {
	name := o.Name
	if name == "" {
		name = o.ID
	}
	sym, col := o.Symbol, o.Color
	if sym == "" {
		sym = "building.2"
	}
	if col == "" {
		col = "blue"
	}
	return Organization{ID: prefix + o.ID, Name: name, Symbol: sym, Color: col, Note: o.Note, CreatedAt: Time{o.CreatedAt.Time}}
}

// toCoreOrg web 的組織 → 核心的組織(遠端組織的 id 要去掉前綴)
func toCoreOrg(o Organization) model.Org {
	return model.Org{ID: serverOrgID(o.ID), Name: o.Name, Symbol: o.Symbol, Color: o.Color, Note: o.Note}
}

// loadLocalOrgIndex 本機模式:組織的所有資訊(名稱、圖示、顏色、備註)都在核心(PostgreSQL);
// orgs.json 只記「目前選的組織」
func loadLocalOrgIndex() OrgIndex {
	empty := OrgIndex{Orgs: []Organization{}}
	l := localCore()
	if l == nil {
		localOrgErr = errNoDB.Error()
		return empty
	}
	list, err := l.svc.ListOrgs(localPrincipal)
	if err == nil && len(list) == 0 {
		name, symbol, color := templates.Org() // 沒開範本是 omixor
		if _, err = l.svc.CreateOrgWith(localPrincipal, model.Org{ID: DefaultOrgID, Name: name, Symbol: symbol, Color: color}); err == nil {
			list, err = l.svc.ListOrgs(localPrincipal)
		}
	}
	if err != nil {
		localOrgErr = err.Error()
		return empty
	}
	localOrgErr = ""
	var saved struct {
		Current string `json:"current"`
	}
	_ = ReadJSON(orgIndexPath(), &saved)
	idx := OrgIndex{Current: saved.Current, Orgs: make([]Organization, 0, len(list))}
	found := false
	for _, c := range list {
		idx.Orgs = append(idx.Orgs, fromCoreOrg(c, ""))
		found = found || c.ID == idx.Current
	}
	if !found {
		idx.Current = idx.Orgs[0].ID
	}
	_ = WriteJSON(orgIndexPath(), map[string]string{"current": idx.Current})
	return idx
}

// saveOrgs 存組織索引:本機只存「目前的組織」(其餘都在核心);遠端存伺服器組織清單的快取
func (a *App) saveOrgs() {
	if IsRemote() {
		_ = WriteJSON(orgIndexPath(), a.orgs().Index)
		return
	}
	_ = WriteJSON(orgIndexPath(), map[string]string{"current": a.orgs().Index.Current})
}

// NewOrgID 由名稱產生 id(英數字,其他換成 -;重複時加 -2、-3…)
func (a *App) NewOrgID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	parts := []string{}
	for _, p := range strings.Split(b.String(), "-") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	root := strings.Join(parts, "-")
	if root == "" {
		root = fmt.Sprintf("org-%d", len(a.orgs().Index.Orgs)+1)
	}
	id, n := root, 2
	for {
		taken := DataDirExists(filepath.Join(DataRoot(), "orgs", id))
		for _, o := range a.orgs().Index.Orgs {
			taken = taken || o.ID == id
		}
		if !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", root, n)
		n++
	}
}

// LoadOrgData 讓各功能讀目前組織的資料(啟動時與切換後;呼叫時要拿著 Mu)
func (a *App) LoadOrgData() {
	for _, f := range orgLoadHooks {
		f(a)
	}
}

// CreateOrg 建立組織並寫好初始檔案;不會切換過去(呼叫時要拿著 Mu)
func (a *App) CreateOrg(name, symbol, color, template string) Organization {
	org, err := a.createOrg(name, symbol, color, template)
	if err != nil {
		log.Printf("建立組織失敗:%v", err)
	}
	return org
}

// createOrg 先在核心建立組織(伺服器管理員身分),成功才寫資料夾與覆蓋層;失敗回傳錯誤
func (a *App) createOrg(name, symbol, color, template string) (Organization, error) {
	if IsRemote() {
		return Organization{}, UserError{"遠端的組織要用 omixor server 建立"} // org_create 動作走 remoteCreateOrg
	}
	l := localCore()
	if l == nil {
		return Organization{}, errNoDB
	}
	if symbol == "" {
		symbol = "building.2"
	}
	if color == "" {
		color = "blue"
	}
	if template != "copyCurrent" {
		template = "blank"
	}
	id := a.NewOrgID(name)
	made, err := l.svc.CreateOrgWith(localPrincipal, model.Org{ID: id, Name: name, Symbol: symbol, Color: color})
	if err != nil {
		return Organization{}, err
	}
	org := fromCoreOrg(*made, "")
	dir := OrgDir(org.ID)
	for _, f := range orgCreateHooks {
		f(a, dir, template)
	}
	s := a.orgs()
	s.Index.Orgs = append(s.Index.Orgs, org)
	a.saveOrgs()
	a.Log(a.Actor(), "building.2", "建立組織「"+name+"」", "settings")
	go a.Changed("orgs")
	return org, nil
}

// UpdateOrg 改組織的名稱、圖示、顏色、備註:本機寫進核心(呼叫時要拿著 Mu)
func (a *App) UpdateOrg(org Organization) bool {
	if IsRemote() {
		return false
	}
	s := a.orgs()
	for k := range s.Index.Orgs {
		if s.Index.Orgs[k].ID != org.ID {
			continue
		}
		l := localCore()
		if l == nil {
			return false
		}
		if err := l.svc.UpdateOrg(localPrincipal, toCoreOrg(org)); err != nil {
			log.Printf("改組織失敗:%v", err)
			return false
		}
		org.CreatedAt = s.Index.Orgs[k].CreatedAt
		s.Index.Orgs[k] = org
		a.saveOrgs()
		go a.Changed("orgs")
		return true
	}
	return false
}

// DeleteOrg 刪除組織:核心裡的組織(連 Issue)與組織資料夾都直接刪掉,不備份、不能復原;
// 不能刪目前的組織或最後一個(呼叫時要拿著 Mu)
func (a *App) DeleteOrg(id string) error {
	if IsRemote() {
		return UserError{"遠端的組織要在 omixor server 管理,這裡不能刪"}
	}
	s := a.orgs()
	if id == s.Index.Current {
		return UserError{"不能刪除目前使用中的組織,先切換到別的組織"}
	}
	if len(s.Index.Orgs) <= 1 {
		return UserError{"至少要留一個組織"}
	}
	l := localCore()
	if l == nil {
		return errNoDB
	}
	if err := l.svc.DeleteOrg(localPrincipal, id); err != nil {
		return err
	}
	dir := filepath.Join(DataRoot(), "orgs", id)
	for _, f := range orgDeleteHooks {
		f(a, id, dir)
	}
	if DataDirExists(dir) {
		if err := RemoveDataDir(dir); err != nil {
			return err
		}
	}
	name := id
	kept := s.Index.Orgs[:0]
	for _, o := range s.Index.Orgs {
		if o.ID == id {
			name = o.Name
			continue
		}
		kept = append(kept, o)
	}
	s.Index.Orgs = kept
	a.saveOrgs()
	a.Log(a.Actor(), "trash", "刪除組織「"+name+"」", "settings")
	go a.Changed("orgs")
	return nil
}

// SwitchOrg 切換組織:各功能重新讀這個組織的資料(呼叫時要拿著 Mu)
func (a *App) SwitchOrg(id string) bool {
	s := a.orgs()
	if id == s.Index.Current {
		return false
	}
	var org *Organization
	for i := range s.Index.Orgs {
		if s.Index.Orgs[i].ID == id {
			org = &s.Index.Orgs[i]
		}
	}
	if org == nil {
		return false
	}
	s.Index.Current = id
	a.saveOrgs()
	a.LoadOrgData()
	a.Nav.FocusID = ""
	a.Log(a.Actor(), "arrow.left.arrow.right", "切換到組織「"+org.Name+"」", "settings")
	go a.Changed("*")
	return true
}

func init() {
	// 先於其他功能載入組織,再讓各功能讀目前組織的資料
	OnStart(func(a *App) {
		a.Mu.Lock()
		a.orgs()
		a.LoadOrgData()
		a.Mu.Unlock()
	})
	RegisterView("orgs", func(a *App) any { return a.Orgs() })
	RegisterAction("org_create", func(a *App, args map[string]any) (any, error) {
		name, _ := args["name"].(string)
		if strings.TrimSpace(name) == "" {
			return nil, UserError{"要給組織名稱"}
		}
		sym, _ := args["symbol"].(string)
		col, _ := args["color"].(string)
		tpl, _ := args["template"].(string)
		sw, _ := args["switch"].(bool)
		if IsRemote() {
			return a.remoteCreateOrg(strings.TrimSpace(name), sym, col, sw)
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		var org Organization
		var err error
		a.As(UserActor(a, args), func() {
			if org, err = a.createOrg(strings.TrimSpace(name), sym, col, tpl); err == nil && sw {
				a.SwitchOrg(org.ID)
			}
		})
		return org, err
	})
	RegisterAction("org_update", func(a *App, args map[string]any) (any, error) {
		var org Organization
		if err := ReadInto(args, &org); err != nil {
			return nil, err
		}
		if IsRemote() {
			return a.remoteUpdateOrg(org) // 寫到 omixor server(要伺服器管理員)
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if !a.UpdateOrg(org) {
			return nil, UserError{"找不到組織 " + org.ID}
		}
		return org, nil
	})
	RegisterAction("org_delete", func(a *App, args map[string]any) (any, error) {
		id, _ := args["id"].(string)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		var err error
		a.As(UserActor(a, args), func() { err = a.DeleteOrg(id) })
		return nil, err
	})
	RegisterAction("org_switch", func(a *App, args map[string]any) (any, error) {
		id, _ := args["id"].(string)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		var ok bool
		a.As(UserActor(a, args), func() { ok = a.SwitchOrg(id) })
		if !ok {
			return nil, UserError{"找不到組織 " + id + ",或已經是目前的組織"}
		}
		return a.Orgs(), nil
	})
}

// ReadInto 把 JSON 物件轉成 struct
func ReadInto(v any, out any) error {
	if err := jsonUnmarshal(Marshal(v), out); err != nil {
		return UserError{"參數格式不對:" + err.Error()}
	}
	return nil
}
