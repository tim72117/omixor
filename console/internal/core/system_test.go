package core

import (
	"errors"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// systemUseBackend 測試用:換掉核心(結束時還原)
func systemUseBackend(t *testing.T, f func() (backend.Backend, error)) {
	t.Helper()
	old := systemBackend
	systemBackend = f
	t.Cleanup(func() { systemBackend = old })
}

func sysView(t *testing.T, a *App) SystemViewData {
	t.Helper()
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return a.systemViewData()
}

func sysFeature(v SystemViewData, key string) *service.FeatureState {
	for i := range v.Features {
		if v.Features[i].Key == key {
			return &v.Features[i]
		}
	}
	return nil
}

// 本機核心:讀到清單(和系統層預設開放的一致)、管理員可以改;頁面的分頁全關 → 頁面不顯示,目前在那頁的切回總覽並提示;
// navigate 去關掉的頁面改去總覽;必要功能不能關;reset 回到預設
func TestSystemLocalReadWrite(t *testing.T) {
	a := connTestApp(t)
	if !a.SystemRefresh() {
		t.Fatal("第一次讀應該要更新畫面")
	}
	v := sysView(t, a)
	if v.Source != SysSourceCore || v.Error != "" || !v.CanEdit || v.Edition != model.EditionStable {
		t.Fatalf("讀取狀態:%+v", v)
	}
	def := model.ServerFeatures{}
	if len(v.Features) != len(def.Available()) {
		t.Fatalf("清單 %d 項,系統層預設開放 %d 項", len(v.Features), len(def.Available()))
	}
	if v.Server.Source != SysSourceCore || v.Server.Error != "" || !v.Server.CanEdit || len(v.Server.Features) != len(model.Features) {
		t.Fatalf("系統層:本機使用者是伺服器管理員:%+v", v.Server)
	}

	// 先停在事件,再把它的分頁全關
	if _, err := Action(a, "navigate", map[string]any{"section": "incidents", "focusId": "INC-1"}); err != nil {
		t.Fatal(err)
	}
	off := map[string]any{}
	for _, f := range def.Available() {
		if f.Parent == "incidents" {
			off[f.Key] = false
		}
	}
	if _, err := Action(a, "system_save", map[string]any{"features": off}); err != nil {
		t.Fatal(err)
	}
	v = sysView(t, a)
	if p := sysFeature(v, "incidents"); p == nil || !p.On || p.Enabled {
		t.Fatalf("incidents 自己開著但分頁全關,應該不顯示:%+v", p)
	}
	a.Mu.Lock()
	nav, visible := a.Nav, a.SectionVisible("incidents")
	a.Mu.Unlock()
	if visible || nav.Section != "overview" || nav.FocusID != "" {
		t.Fatalf("所在頁被關掉要切回總覽:%+v visible=%v", nav, visible)
	}
	if v.Notice.Seq == 0 || !strings.Contains(v.Notice.Text, "事件") {
		t.Fatalf("要有提示:%+v", v.Notice)
	}

	// navigate 去關掉的頁面:改去總覽並回傳 notice
	out, err := Action(a, "navigate", map[string]any{"section": "incidents", "focusId": "INC-2"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := out.(map[string]any)
	if m["section"] != "overview" || !strings.Contains(m["notice"].(string), "事件") {
		t.Fatalf("navigate:%+v", out)
	}
	seq := sysView(t, a).Notice.Seq
	if seq <= v.Notice.Seq {
		t.Fatal("navigate 被擋要多一則提示")
	}
	// 永遠開著的頁面不受影響
	if _, err := Action(a, "navigate", map[string]any{"section": "system"}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	sec := a.Nav.Section
	a.Mu.Unlock()
	if sec != "system" {
		t.Fatal(sec)
	}

	// 必要功能不能關、沒登記的 key 不行
	if _, err := Action(a, "system_save", map[string]any{"features": map[string]any{"overview": false}}); err == nil || !strings.Contains(err.Error(), "不能關") {
		t.Fatalf("必要功能:%v", err)
	}
	if _, err := Action(a, "system_save", map[string]any{"features": map[string]any{"nope": true}}); err == nil {
		t.Fatal("沒登記的 key 應該失敗")
	}
	if _, err := Action(a, "system_save", map[string]any{"features": map[string]any{"incidents.list": "x"}}); err == nil {
		t.Fatal("值不是 bool 應該失敗")
	}
	if _, err := Action(a, "system_save", map[string]any{}); err == nil {
		t.Fatal("沒有要改的應該失敗")
	}

	// 回到預設:incidents 又顯示
	reset := []any{}
	for k := range off {
		reset = append(reset, k)
	}
	if _, err := Action(a, "system_save", map[string]any{"reset": reset}); err != nil {
		t.Fatal(err)
	}
	if !a.FeatureEnabledNow("incidents") {
		t.Fatal("reset 後 incidents 應該顯示")
	}
	// 別的地方(例如 App)改了核心:重讀後跟著變
	be, _ := agentBackend()
	if _, err := be.SaveSystemSettings(DefaultOrgID, service.SystemSettingsInput{Features: map[string]bool{"cabinet": false}}); err != nil {
		t.Fatal(err)
	}
	if !a.SystemRefresh() || a.FeatureEnabledNow("cabinet") {
		t.Fatal("重讀後文件櫃應該關掉")
	}
	if a.SystemRefresh() {
		t.Fatal("沒變時不用通知畫面")
	}
}

type sysFakeBackend struct {
	backend.Unsupported
	view      *service.SystemSettingsView
	err       error
	saveErr   error
	serverErr error
}

func (f *sysFakeBackend) GetServerFeatures() (*service.ServerFeaturesView, error) {
	if f.serverErr != nil {
		return nil, f.serverErr
	}
	return ServerFallback(), nil
}

func (f *sysFakeBackend) GetSystemSettings(string) (*service.SystemSettingsView, error) {
	if f.err != nil {
		return nil, f.err
	}
	x := *f.view
	return &x, nil
}

func (f *sysFakeBackend) SaveSystemSettings(string, service.SystemSettingsInput) (*service.SystemSettingsView, error) {
	return nil, f.saveErr
}

// 讀不到:有讀過用最後一份(cached),沒讀過用保守預設(fallback);兩種都不能改,並帶原因
func TestSystemFallbackAndCache(t *testing.T) {
	a := connTestApp(t)
	fake := &sysFakeBackend{err: errors.New("連不上 omixor server")}
	systemUseBackend(t, func() (backend.Backend, error) { return fake, nil })

	a.SystemRefresh()
	v := sysView(t, a)
	if v.Source != SysSourceFallback || !strings.Contains(v.Error, "連不上") || v.CanEdit {
		t.Fatalf("沒讀過:%+v", v)
	}
	// 保守預設:已實作的開、實驗的關
	for _, f := range v.Features {
		if f.Experimental && f.On {
			t.Fatalf("實驗功能預設要關:%s", f.Key)
		}
		// 旁白發佈、網站訪客要自己開
		if !f.Experimental && !f.On && f.Key != "pipelines.voice" && f.Key != "monitoring.visitors" {
			t.Fatalf("已實作的預設要開:%s", f.Key)
		}
	}

	// 讀到一份(管理員關掉了文件櫃)
	good := SystemFallback()
	for i := range good.Features {
		if good.Features[i].Key == "cabinet" {
			good.Features[i].On, good.Features[i].Enabled, good.Features[i].Changed = false, false, true
		}
	}
	good.CanEdit = true
	fake.view, fake.err = good, nil
	a.SystemRefresh()
	if v := sysView(t, a); v.Source != SysSourceCore || !v.CanEdit || a.FeatureEnabledNow("cabinet") {
		t.Fatalf("讀到:%+v", v)
	}
	// 之後又讀不到:照最後一份,不能改
	fake.err = errors.New("逾時")
	a.SystemRefresh()
	v = sysView(t, a)
	if v.Source != SysSourceCached || v.Error != "逾時" || v.CanEdit || a.FeatureEnabledNow("cabinet") {
		t.Fatalf("讀不到時用最後一份:%+v", v)
	}

	// 本機模式沒有資料庫:說明要設資料庫
	systemUseBackend(t, func() (backend.Backend, error) { return nil, errNoDB })
	a.SystemRefresh()
	if v := sysView(t, a); !strings.Contains(v.Error, "OMIXOR_DATABASE_URL") || v.Source != SysSourceCached {
		t.Fatalf("沒有資料庫:%+v", v)
	}
	if _, err := a.SystemSave(map[string]bool{"cabinet": true}, nil); err == nil || !strings.Contains(err.Error(), "OMIXOR_DATABASE_URL") {
		t.Fatalf("沒有資料庫時存:%v", err)
	}
}

// 非管理員:canEdit = false;存的時候核心回權限錯誤,原文帶給畫面。清單沒列的功能(系統層沒開放)當作關,沒登記的 key 當作開
func TestSystemPermissionAndServerOff(t *testing.T) {
	a := connTestApp(t)
	v := SystemFallback()
	v.CanEdit = false
	kept := v.Features[:0]
	for _, f := range v.Features {
		if f.Key != "pipelines.voice" {
			kept = append(kept, f)
		}
	}
	v.Features = kept
	fake := &sysFakeBackend{view: v, saveErr: errors.New("只有組織管理員可以修改")}
	systemUseBackend(t, func() (backend.Backend, error) { return fake, nil })
	a.SystemRefresh()
	if sv := sysView(t, a); sv.CanEdit || sv.Source != SysSourceCore {
		t.Fatalf("非管理員:%+v", sv)
	}
	_, err := Action(a, "system_save", map[string]any{"features": map[string]any{"cabinet": false}})
	if err == nil || !strings.Contains(err.Error(), "只有組織管理員") {
		t.Fatalf("非管理員存:%v", err)
	}
	if a.FeatureEnabledNow("pipelines.voice") {
		t.Fatal("清單沒列的登記功能要當作關")
	}
	if !a.FeatureEnabledNow("development.gitlab") {
		t.Fatal("沒登記的 key(web 才有的分頁)要當作開")
	}
	if !a.FeatureEnabledNow("development.issues") {
		t.Fatal("development.issues 應該開著")
	}
}

// AI 助理的「前往」按鈕不指向關掉的頁面
func TestAssistHidesClosedSections(t *testing.T) {
	a := connTestApp(t)
	if _, err := a.SystemSave(map[string]bool{"automation": false}, nil); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	r := assistVisibleOnly(a, AssistReply{Actions: []AssistAction{assistGo("automation", nil, "x"), assistGo("incidents", nil, "y")}})
	a.Mu.Unlock()
	if len(r.Actions) != 1 || r.Actions[0]["section"] != "incidents" {
		t.Fatalf("%+v", r.Actions)
	}
}

// 系統層:本機使用者(伺服器管理員)改系統層 → 組織層的清單跟著變;系統層關掉的功能,組織層開不了(400 並說明)、畫面當作關
func TestServerFeaturesLocal(t *testing.T) {
	a := connTestApp(t)
	a.SystemRefresh()
	if a.FeatureEnabledNow("familyAssistant") || sysFeature(sysView(t, a), "familyAssistant") != nil {
		t.Fatal("家庭助理預設系統層不開放、組織層不列")
	}
	if _, err := Action(a, "system_save", map[string]any{"features": map[string]any{"familyAssistant": true}}); err == nil || !strings.Contains(err.Error(), "這台伺服器沒有開放") {
		t.Fatalf("系統層沒開放時組織層開不了:%v", err)
	}
	if _, err := Action(a, "server_features_save", map[string]any{"features": map[string]any{"familyAssistant": true, "cabinet": false}}); err != nil {
		t.Fatal(err)
	}
	v := sysView(t, a)
	if f := sysFeature(v, "familyAssistant"); f == nil || f.On || !v.ServerFeatures["familyAssistant"] {
		t.Fatalf("系統層開放後組織層列出(組織層預設關):%+v", f)
	}
	if sysFeature(v, "cabinet") != nil || v.ServerFeatures["cabinet"] || a.FeatureEnabledNow("cabinet") {
		t.Fatal("系統層關掉文件櫃:組織層不列、畫面當作關")
	}
	if _, err := Action(a, "system_save", map[string]any{"features": map[string]any{"familyAssistant": true}}); err != nil || !a.FeatureEnabledNow("familyAssistant") {
		t.Fatalf("兩層都開:%v", err)
	}
	if _, err := Action(a, "server_features_save", map[string]any{"features": map[string]any{"settings": false}}); err == nil || !strings.Contains(err.Error(), "不能關") {
		t.Fatalf("必要功能:%v", err)
	}
	if _, err := Action(a, "server_features_save", map[string]any{}); err == nil {
		t.Fatal("沒有要改的應該失敗")
	}
	if _, err := Action(a, "server_features_save", map[string]any{"reset": []any{"cabinet", "familyAssistant"}}); err != nil {
		t.Fatal(err)
	}
	if !a.FeatureEnabledNow("cabinet") || a.FeatureEnabledNow("familyAssistant") {
		t.Fatal("系統層 reset 回預設")
	}
}

// 系統層讀不到(例如舊版 server 沒有 /api/system/features):用保守預設、不能改;組織層照常
func TestServerFeaturesUnavailable(t *testing.T) {
	a := connTestApp(t)
	fake := &sysFakeBackend{view: SystemFallback(), serverErr: errors.New("404 沒有這個路徑")}
	fake.view.CanEdit = true
	systemUseBackend(t, func() (backend.Backend, error) { return fake, nil })
	a.SystemRefresh()
	v := sysView(t, a)
	if v.Source != SysSourceCore || !v.CanEdit {
		t.Fatalf("組織層照常:%+v", v)
	}
	if v.Server.Source != SysSourceFallback || v.Server.CanEdit || !strings.Contains(v.Server.Error, "404") || len(v.Server.Features) != len(model.Features) {
		t.Fatalf("系統層讀不到:%+v", v.Server)
	}
}
