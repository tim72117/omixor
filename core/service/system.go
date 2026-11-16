package service

import (
	"errors"
	"sort"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 功能開關分兩層(清單見 model/features.go;都存在 core 的資料庫,不再靠編譯決定):
//
//	系統層(整台伺服器一份,GET/POST /api/system/features):
//	  讀:任何登入的使用者(角色憑證不行);改:伺服器管理員(本機守護服務的本機使用者就是伺服器管理員)
//	  沒設定過用 Feature.SystemDefault(原本正式版的開放、原本只有開發版的不開放);必要功能永遠開放、不能關
//	組織層(GET/POST /api/orgs/{org}/system):
//	  讀:組織成員(角色憑證也可以,只能讀自己組織的);改:組織管理員(伺服器管理員視同管理員)
//	  回傳只列系統層有開放的功能(model.ServerFeatures.Available;就像以前沒編進去的功能不列),
//	  serverFeatures 附上每個登記的 key 在系統層開放不開放,App 與 web 不用自己另外寫清單
//
// 退場的 key(model.LegacyFeatureKeys,例如 development.github → development.repos):讀的時候存著的舊值當作新 key 的值;
// 改的時候輸入的舊 key 轉成新 key,存的時候把舊值搬到新 key 並拿掉舊 key。
// 改的輸入只要給有變的(key → 開不開),沒給的不動;沒登記的 key 或把必要功能關掉回 400;
// 組織層對系統層沒開放的功能(開、關、reset)回 400。系統層沒開放的功能:組織設定裡存著的舊值保留
// (系統層再開放時照舊生效),但現在一律當作關。Reset 列出的 key 回到預設(拿掉改過的值)。
// 實際可用 = 系統層開放 && 組織層開 && 頁面規則(見 OrgSystemSettings.Enabled)。

// FeatureState 清單裡的一個功能加上這個組織目前的狀態
type FeatureState struct {
	model.Feature
	// Enabled 實際開不開(畫面照這個顯示;頁面關了分頁也不顯示,頁面的分頁全關時頁面也不顯示)
	Enabled bool `json:"enabled"`
	// On 這個功能自己的開關(系統設定畫面的開關照這個;不管所屬頁面)
	On bool `json:"on"`
	// Changed 組織改過(false = 用預設)
	Changed bool `json:"changed"`
	// System 系統層有開放(清單只列有開放的,所以一律是 true;給用戶端明確知道)
	System bool `json:"system"`
}

// SystemSettingsView GET /api/orgs/{org}/system 的回應
type SystemSettingsView struct {
	Features  []FeatureState `json:"features"`
	Saved     bool           `json:"saved"`
	UpdatedAt *model.Time    `json:"updatedAt,omitempty"`
	UpdatedBy string         `json:"updatedBy,omitempty"`
	// Edition 過時(保留給舊用戶端,不再有編譯版本):系統層開放了任何原本只有開發版的功能時是 dev,否則 stable
	Edition string `json:"edition"`
	// ServerFeatures 每個登記的功能在系統層開放不開放(key → 開放;分頁還要所屬頁面開放)
	ServerFeatures map[string]bool `json:"serverFeatures"`
	// CanEdit 目前的使用者可以改(組織管理員)
	CanEdit bool `json:"canEdit"`
}

// SystemSettingsInput 修改功能開關:Features 是有變的(key → 開不開);Reset 是要回到預設的 key
type SystemSettingsInput struct {
	Features map[string]bool `json:"features,omitempty"`
	Reset    []string        `json:"reset,omitempty"`
}

// canonical 退場的 key(model.LegacyFeatureKeys,例如 development.github)轉成取代它的 key;
// 同時給新舊兩個 key 時以新 key 為準
func (in SystemSettingsInput) canonical() SystemSettingsInput {
	out := SystemSettingsInput{}
	if in.Features != nil {
		out.Features = map[string]bool{}
		for k, v := range in.Features {
			if c := model.CanonicalFeatureKey(k); c != k {
				if _, direct := in.Features[c]; !direct {
					out.Features[c] = v
				}
				continue
			}
			out.Features[k] = v
		}
	}
	for _, k := range in.Reset {
		out.Reset = append(out.Reset, model.CanonicalFeatureKey(k))
	}
	return out
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

// systemView 合併清單、系統層與組織的設定
func systemView(srv model.ServerFeatures, x model.OrgSystemSettings, canEdit bool) *SystemSettingsView {
	v := &SystemSettingsView{Features: []FeatureState{}, Saved: x.Saved, UpdatedAt: x.UpdatedAt, UpdatedBy: x.UpdatedBy,
		Edition: srv.LegacyEdition(), ServerFeatures: map[string]bool{}, CanEdit: canEdit}
	for _, f := range model.Features {
		v.ServerFeatures[f.Key] = srv.Has(f.Key)
	}
	for _, f := range srv.Available() {
		_, changed := model.StoredFeature(x.Features, f.Key)
		v.Features = append(v.Features, FeatureState{Feature: f, Enabled: x.Enabled(srv, f.Key), On: x.Own(srv, f.Key),
			Changed: changed && !f.Required, System: true})
	}
	return v
}

// registeredFeature 找登記的功能;沒登記回 400
func registeredFeature(key string) (model.Feature, error) {
	f, ok := model.FeatureByKey(key)
	if !ok {
		return f, fail(400, "沒有這個功能:%s", strings.TrimSpace(key))
	}
	return f, nil
}

// serverOffError 系統層沒開放的功能
func serverOffError(f model.Feature) error {
	return fail(400, "這台伺服器沒有開放「%s」功能,請伺服器管理員到 系統 開啟", f.Name)
}

// availableFeature 找系統層有開放的功能;沒登記或系統層沒開放回 400
func availableFeature(srv model.ServerFeatures, key string) (model.Feature, error) {
	f, err := registeredFeature(key)
	if err != nil {
		return f, err
	}
	if !srv.Has(f.Key) {
		return f, serverOffError(f)
	}
	return f, nil
}

// canEditOrg 是不是組織管理員(角色身分一律不是)
func (s *Service) canEditOrg(p *model.Principal, org string) bool {
	if p == nil || p.Agent != nil {
		return false
	}
	r, err := s.orgRole(p, org)
	return err == nil && r == "admin"
}

// loadServerFeatures 系統層的設定;沒存過全部用預設(Saved = false)
func (s *Service) loadServerFeatures() (model.ServerFeatures, error) {
	x, err := s.Store.GetServerFeatures()
	if errors.Is(err, store.ErrNotFound) {
		return model.ServerFeatures{Features: map[string]bool{}}, nil
	}
	if err != nil {
		return model.ServerFeatures{}, err
	}
	if x.Features == nil {
		x.Features = map[string]bool{}
	}
	x.Saved = true
	return *x, nil
}

// loadFeatures 系統層加上組織層(判斷功能實際開不開用:org.Enabled(srv, key))
func (s *Service) loadFeatures(org string) (model.ServerFeatures, model.OrgSystemSettings, error) {
	srv, err := s.loadServerFeatures()
	if err != nil {
		return srv, model.OrgSystemSettings{}, err
	}
	x, err := s.loadSystem(org)
	return srv, x, err
}

// featureEnabled 這個組織的某個功能實際開不開(系統層 && 組織層 && 頁面規則)
func (s *Service) featureEnabled(org, key string) (bool, error) {
	srv, x, err := s.loadFeatures(org)
	if err != nil {
		return false, err
	}
	return x.Enabled(srv, key), nil
}

// GetSystemSettings 組織的系統設定(成員,含角色身分);沒存過全部用預設(saved = false)
func (s *Service) GetSystemSettings(p *model.Principal, org string) (*SystemSettingsView, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	srv, x, err := s.loadFeatures(org)
	if err != nil {
		return nil, err
	}
	return systemView(srv, x, s.canEditOrg(p, org)), nil
}

// SaveSystemSettings 修改功能開關(組織管理員)
func (s *Service) SaveSystemSettings(p *model.Principal, org string, in SystemSettingsInput) (*SystemSettingsView, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	in = in.canonical()
	if len(in.Features) == 0 && len(in.Reset) == 0 {
		return nil, fail(400, "沒有要改的功能")
	}
	s.systemMu.Lock()
	defer s.systemMu.Unlock()
	srv, cur, err := s.loadFeatures(org)
	if err != nil {
		return nil, err
	}
	keys := sortedKeys(in.Features)
	for _, k := range keys {
		f, err := availableFeature(srv, k)
		if err != nil {
			return nil, err
		}
		if f.Required && !in.Features[k] {
			return nil, fail(400, "「%s」不能關", f.Name)
		}
	}
	for _, k := range in.Reset {
		if _, err := availableFeature(srv, k); err != nil {
			return nil, err
		}
	}
	x := cur.Clone()
	model.MigrateLegacyFeatures(x.Features) // 舊 key 的值搬到新 key(之後的 reset / 修改才會蓋到)
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
	return systemView(srv, x, true), nil
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---- 系統層 ----

// ServerFeatureState 清單裡的一個功能加上系統層目前的狀態
type ServerFeatureState struct {
	model.Feature
	// SystemDefault 系統層沒設定過時開放不開放(原本正式版的開放、原本只有開發版的不開放)
	SystemDefault bool `json:"systemDefault"`
	// On 系統層這個功能自己的開關(系統頁的開關照這個;不管所屬頁面)
	On bool `json:"on"`
	// Available 這台伺服器實際有開放(分頁還要所屬頁面開放)
	Available bool `json:"available"`
	// Changed 伺服器管理員改過(false = 用預設)
	Changed bool `json:"changed"`
}

// ServerFeaturesView GET /api/system/features 的回應
type ServerFeaturesView struct {
	Features  []ServerFeatureState `json:"features"`
	Saved     bool                 `json:"saved"`
	UpdatedAt *model.Time          `json:"updatedAt,omitempty"`
	UpdatedBy string               `json:"updatedBy,omitempty"`
	// CanEdit 目前的使用者可以改(伺服器管理員)
	CanEdit bool `json:"canEdit"`
}

func serverFeaturesView(x model.ServerFeatures, canEdit bool) *ServerFeaturesView {
	v := &ServerFeaturesView{Features: []ServerFeatureState{}, Saved: x.Saved, UpdatedAt: x.UpdatedAt, UpdatedBy: x.UpdatedBy, CanEdit: canEdit}
	for _, f := range model.Features {
		_, changed := model.StoredFeature(x.Features, f.Key)
		v.Features = append(v.Features, ServerFeatureState{Feature: f, SystemDefault: f.SystemDefault(), On: x.Own(f.Key),
			Available: x.Has(f.Key), Changed: changed && !f.Required})
	}
	return v
}

func canEditServer(p *model.Principal) bool { return p != nil && p.Agent == nil && p.ServerAdmin() }

// GetServerFeatures 系統層的功能設定(任何登入的使用者;角色憑證不行);沒存過全部用預設(saved = false)
func (s *Service) GetServerFeatures(p *model.Principal) (*ServerFeaturesView, error) {
	if p == nil || p.Agent != nil {
		return nil, fail(403, "角色憑證不能讀系統層的功能設定")
	}
	x, err := s.loadServerFeatures()
	if err != nil {
		return nil, err
	}
	return serverFeaturesView(x, canEditServer(p)), nil
}

// SaveServerFeatures 修改系統層的功能開關(伺服器管理員);輸入同組織層(features 有變的、reset 回到預設)
func (s *Service) SaveServerFeatures(p *model.Principal, in SystemSettingsInput) (*ServerFeaturesView, error) {
	if !canEditServer(p) {
		return nil, fail(403, "要 omixor 伺服器管理員才能改這台伺服器開放的功能")
	}
	in = in.canonical()
	if len(in.Features) == 0 && len(in.Reset) == 0 {
		return nil, fail(400, "沒有要改的功能")
	}
	keys := sortedKeys(in.Features)
	for _, k := range keys {
		f, err := registeredFeature(k)
		if err != nil {
			return nil, err
		}
		if f.Required && !in.Features[k] {
			return nil, fail(400, "「%s」不能關", f.Name)
		}
	}
	for _, k := range in.Reset {
		if _, err := registeredFeature(k); err != nil {
			return nil, err
		}
	}
	s.systemMu.Lock()
	defer s.systemMu.Unlock()
	cur, err := s.loadServerFeatures()
	if err != nil {
		return nil, err
	}
	x := cur.Clone()
	model.MigrateLegacyFeatures(x.Features) // 舊 key 的值搬到新 key(之後的 reset / 修改才會蓋到)
	for _, k := range in.Reset {
		delete(x.Features, k)
	}
	changed := map[string]bool{}
	for _, k := range keys {
		if f, _ := model.FeatureByKey(k); f.Required {
			delete(x.Features, k) // 必要的一律開放,不用存
			continue
		}
		x.Features[k] = in.Features[k]
		changed[k] = in.Features[k]
	}
	now := s.mt()
	x.Saved, x.UpdatedAt, x.UpdatedBy = true, &now, whoName(p)
	if err := s.Store.SaveServerFeatures(x); err != nil {
		return nil, err
	}
	s.Audit(p, "system.server.features.save", "", map[string]any{"features": changed, "reset": in.Reset})
	return serverFeaturesView(x, true), nil
}

// DefaultSystemView 兩層都還沒設定過時的組織層畫面(用戶端讀不到核心時的保守預設;不能改)
func DefaultSystemView() *SystemSettingsView {
	return systemView(model.ServerFeatures{Features: map[string]bool{}}, model.OrgSystemSettings{Features: map[string]bool{}}, false)
}

// DefaultServerFeaturesView 系統層還沒設定過時的畫面(用戶端讀不到核心時的保守預設;不能改)
func DefaultServerFeaturesView() *ServerFeaturesView {
	return serverFeaturesView(model.ServerFeatures{Features: map[string]bool{}}, false)
}
