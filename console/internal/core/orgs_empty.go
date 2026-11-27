package core

import (
	"net/http"

	"omixor-web/internal/templates"
)

// 還沒有任何組織的狀態。
//
// 這個狀態下目前組織是 "":畫面只能建立第一個組織;其他 action、工具與資料 API 一律回 errNoOrg,
// 背景工作不向核心查組織 "",避免寫到不存在的組織。

const noOrgMessage = "還沒有組織:請管理員先在畫面上建立第一個組織"

// orgFreeActions 不屬於任何組織、沒有組織時也能做的 action
var orgFreeActions = map[string]bool{
	"navigate": true, "system_reload": true, "server_features_save": true,
	"org_create": true, "org_update": true, "org_delete": true, "org_switch": true, "system_org_set": true,
}

// refreshEmptyOrgs 清單是空的時重讀一次:別的程式(匯入、其他入口)建了組織,或原本讀不到資料庫。呼叫時要拿著 Mu
func (a *App) refreshEmptyOrgs() {
	if len(a.orgs().Index.Orgs) > 0 {
		return
	}
	idx := loadLocalOrgIndex()
	if len(idx.Orgs) == 0 {
		return
	}
	a.orgs().Index = idx
	a.LoadOrgData()
	go a.Changed("*")
}

// RequireOrg 沒有任何組織時回給畫面看的錯誤(409);其他情況 nil。不要拿著 Mu
func (a *App) RequireOrg() error {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.CurrentOrgID() != "" {
		return nil
	}
	a.refreshEmptyOrgs()
	if a.CurrentOrgID() != "" {
		return nil
	}
	msg := noOrgMessage
	if localOrgErr != "" {
		msg = "讀不到組織:" + localOrgErr
	}
	return StatusError{Status: http.StatusConflict, Message: msg}
}

// orgSuggestion 建立第一個組織時預填的值(開了範本才有,來自範本的 org.json)
type orgSuggestion struct {
	Name   string `json:"name"`
	Symbol string `json:"symbol"`
	Color  string `json:"color"`
}

// orgsViewData view「orgs」:組織清單;清單是空的時另外帶讀不到的原因與預填值
type orgsViewData struct {
	OrgIndex
	Error     string         `json:"error,omitempty"`
	Suggested *orgSuggestion `json:"suggested,omitempty"`
}

func (a *App) orgsView() orgsViewData {
	a.refreshEmptyOrgs()
	v := orgsViewData{OrgIndex: a.Orgs()}
	if len(v.Orgs) == 0 {
		v.Error = localOrgErr
		if templates.Enabled() {
			name, symbol, color := templates.Org()
			v.Suggested = &orgSuggestion{Name: name, Symbol: symbol, Color: color}
		}
	}
	return v
}
