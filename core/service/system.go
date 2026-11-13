package service

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"omixor-core/model"
	"omixor-core/store"
)

// 組織的系統設定:功能開關(清單見 model/features.go)。
//
//	讀:組織成員(角色憑證也可以,只能讀自己組織的)
//	改:組織管理員(伺服器管理員視同管理員)
//
// 回傳附上這次編譯可用的功能清單(model.AvailableFeatures,依 build tag 的版本過濾)與每個功能目前開不開,
// App 與 web 不用自己另外寫清單。
// 改的輸入只要給有變的(key → 開不開),沒給的不動;沒登記的 key、這次編譯沒有的功能、或把必要功能關掉,回 400。
// 這次編譯沒有的功能:組織設定裡存著的舊值保留(換成有這功能的版本時照舊生效),但現在一律當作關。
// Reset 列出的 key 回到預設(拿掉改過的值)。

// systemMu 「讀了再改」在這把鎖裡(單一程式內)
var systemMu sync.Mutex

// FeatureState 清單裡的一個功能加上這個組織目前的狀態
type FeatureState struct {
	model.Feature
	// Enabled 實際開不開(畫面照這個顯示;頁面關了分頁也不顯示,頁面的分頁全關時頁面也不顯示)
	Enabled bool `json:"enabled"`
	// On 這個功能自己的開關(系統設定畫面的開關照這個;不管所屬頁面)
	On bool `json:"on"`
	// Changed 組織改過(false = 用預設)
	Changed bool `json:"changed"`
}

// SystemSettingsView GET /api/orgs/{org}/system 的回應
type SystemSettingsView struct {
	Features  []FeatureState `json:"features"`
	Saved     bool           `json:"saved"`
	UpdatedAt *model.Time    `json:"updatedAt,omitempty"`
	UpdatedBy string         `json:"updatedBy,omitempty"`
	// Edition 這次編譯的版本(stable 正式版 / dev 開發版;見 model/edition.go)
	Edition string `json:"edition"`
	// CanEdit 目前的使用者可以改(組織管理員)
	CanEdit bool `json:"canEdit"`
}

// SystemSettingsInput 修改功能開關:Features 是有變的(key → 開不開);Reset 是要回到預設的 key
type SystemSettingsInput struct {
	Features map[string]bool `json:"features,omitempty"`
	Reset    []string        `json:"reset,omitempty"`
}

func (s *Service) loadSystem(org string) (model.OrgSystemSettings, error) {
	x, err := s.Store.GetSystemSettings(org)
	if errors.Is(err, store.ErrNotFound) {
		return model.OrgSystemSettings{Features: map[string]bool{}}, nil
	}
	if err != nil {
		return model.OrgSystemSettings{}, err
	}
	if x.Features == nil {
		x.Features = map[string]bool{}
	}
	x.Saved = true
	return *x, nil
}

// systemView 合併清單與組織的設定
func systemView(x model.OrgSystemSettings, canEdit bool) *SystemSettingsView {
	v := &SystemSettingsView{Features: []FeatureState{}, Saved: x.Saved, UpdatedAt: x.UpdatedAt, UpdatedBy: x.UpdatedBy,
		Edition: model.BuildEdition, CanEdit: canEdit}
	for _, f := range model.AvailableFeatures() {
		_, changed := x.Features[f.Key]
		v.Features = append(v.Features, FeatureState{Feature: f, Enabled: x.Enabled(f.Key), On: x.Own(f.Key), Changed: changed && !f.Required})
	}
	return v
}

// canEditOrg 是不是組織管理員(角色身分一律不是)
// availableFeature 找這次編譯可用的功能;沒登記或這次編譯沒有回 400
func availableFeature(key string) (model.Feature, error) {
	f, ok := model.FeatureByKey(key)
	if !ok {
		return f, fail(400, "沒有這個功能:%s", strings.TrimSpace(key))
	}
	if !f.Available() {
		return f, fail(400, "這個版本(%s)沒有「%s」功能", model.EditionLabel(model.BuildEdition), f.Name)
	}
	return f, nil
}

func (s *Service) canEditOrg(p *model.Principal, org string) bool {
	if p == nil || p.Agent != nil {
		return false
	}
	r, err := s.orgRole(p, org)
	return err == nil && r == "admin"
}

// GetSystemSettings 組織的系統設定(成員,含角色身分);沒存過全部用預設(saved = false)
func (s *Service) GetSystemSettings(p *model.Principal, org string) (*SystemSettingsView, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	x, err := s.loadSystem(org)
	if err != nil {
		return nil, err
	}
	return systemView(x, s.canEditOrg(p, org)), nil
}

// SaveSystemSettings 修改功能開關(組織管理員)
func (s *Service) SaveSystemSettings(p *model.Principal, org string, in SystemSettingsInput) (*SystemSettingsView, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if len(in.Features) == 0 && len(in.Reset) == 0 {
		return nil, fail(400, "沒有要改的功能")
	}
	keys := make([]string, 0, len(in.Features))
	for k := range in.Features {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f, err := availableFeature(k)
		if err != nil {
			return nil, err
		}
		if f.Required && !in.Features[k] {
			return nil, fail(400, "「%s」不能關", f.Name)
		}
	}
	for _, k := range in.Reset {
		if _, err := availableFeature(k); err != nil {
			return nil, err
		}
	}
	systemMu.Lock()
	defer systemMu.Unlock()
	cur, err := s.loadSystem(org)
	if err != nil {
		return nil, err
	}
	x := cur.Clone()
	for _, k := range in.Reset {
		delete(x.Features, k)
	}
	changed := map[string]bool{}
	for _, k := range keys {
		if f, _ := model.FeatureByKey(k); f.Required {
			delete(x.Features, k) // 必要的一律開,不用存
			continue
		}
		x.Features[k] = in.Features[k]
		changed[k] = in.Features[k]
	}
	now := s.mt()
	x.Saved, x.UpdatedAt, x.UpdatedBy = true, &now, whoName(p)
	if err := s.Store.SaveSystemSettings(org, x); err != nil {
		return nil, err
	}
	s.Audit(p, "system.features.save", org, map[string]any{"features": changed, "reset": in.Reset})
	return systemView(x, true), nil
}
