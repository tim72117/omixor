package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
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

func orgIndexPath() string { return filepath.Join(DataRoot(), "orgs.json") }

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
	if err := ReadJSON(orgIndexPath(), &idx); err == nil && len(idx.Orgs) > 0 {
		found := false
		for _, o := range idx.Orgs {
			found = found || o.ID == idx.Current
		}
		if !found {
			idx.Current = idx.Orgs[0].ID
		}
		return idx
	}
	// 搬遷:舊版放在根目錄的檔案複製到預設組織,原檔保留
	target := OrgDir(DefaultOrgID)
	for _, f := range PerOrgFiles {
		src, dst := filepath.Join(DataRoot(), f), filepath.Join(target, f)
		if Exists(src) && !Exists(dst) {
			if b, err := ReadData(src); err == nil {
				_ = WriteData(dst, b)
			}
		}
	}
	idx = OrgIndex{Current: DefaultOrgID, Orgs: []Organization{{
		ID: DefaultOrgID, Name: "evoleap", Symbol: "graduationcap", Color: "teal", CreatedAt: Now(),
	}}}
	_ = WriteJSON(orgIndexPath(), idx)
	return idx
}

func (a *App) saveOrgs() { _ = WriteJSON(orgIndexPath(), a.orgs().Index) }

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
	if symbol == "" {
		symbol = "building.2"
	}
	if color == "" {
		color = "blue"
	}
	if template != "copyCurrent" {
		template = "blank"
	}
	org := Organization{ID: a.NewOrgID(name), Name: name, Symbol: symbol, Color: color, CreatedAt: Now()}
	dir := OrgDir(org.ID)
	for _, f := range orgCreateHooks {
		f(a, dir, template)
	}
	s := a.orgs()
	s.Index.Orgs = append(s.Index.Orgs, org)
	a.saveOrgs()
	a.Log(a.Actor(), "building.2", "建立組織「"+name+"」", "settings")
	go a.Changed("orgs")
	return org
}

func (a *App) UpdateOrg(org Organization) bool {
	s := a.orgs()
	for i := range s.Index.Orgs {
		if s.Index.Orgs[i].ID == org.ID {
			org.CreatedAt = s.Index.Orgs[i].CreatedAt
			s.Index.Orgs[i] = org
			a.saveOrgs()
			go a.Changed("orgs")
			return true
		}
	}
	return false
}

// DeleteOrg 資料夾移到垃圾桶(~/.Trash);不能刪目前的組織或最後一個(呼叫時要拿著 Mu)
func (a *App) DeleteOrg(id string) error {
	s := a.orgs()
	if id == s.Index.Current {
		return UserError{"不能刪除目前使用中的組織,先切換到別的組織"}
	}
	if len(s.Index.Orgs) <= 1 {
		return UserError{"至少要留一個組織"}
	}
	dir := filepath.Join(DataRoot(), "orgs", id)
	for _, f := range orgDeleteHooks {
		f(a, id, dir)
	}
	if DataDirExists(dir) {
		home, _ := os.UserHomeDir()
		trash := filepath.Join(home, ".Trash", fmt.Sprintf("Ops 組織 %s-%d", id, time.Now().Unix()))
		// 檔案存放處搬到垃圾桶;資料庫存放處搬到資料庫裡的 deleted-orgs/(不直接刪掉)
		if err := MoveDataDir(dir, trash); err != nil {
			keep := filepath.Join(DataRoot(), "deleted-orgs", fmt.Sprintf("%s-%d", id, time.Now().Unix()))
			if err := MoveDataDir(dir, keep); err != nil {
				return err
			}
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
	a.Log(a.Actor(), "trash", "刪除組織「"+name+"」(資料夾已移到垃圾桶)", "settings")
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
		a.Mu.Lock()
		defer a.Mu.Unlock()
		var org Organization
		a.As(UserActor(a, args), func() {
			org = a.CreateOrg(strings.TrimSpace(name), sym, col, tpl)
			if sw {
				a.SwitchOrg(org.ID)
			}
		})
		return org, nil
	})
	RegisterAction("org_update", func(a *App, args map[string]any) (any, error) {
		var org Organization
		if err := ReadInto(args, &org); err != nil {
			return nil, err
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
