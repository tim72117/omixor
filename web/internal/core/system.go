package core

import (
	"errors"
	"log"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// 系統設定(功能開關):清單與每個組織的開關都在核心(omixor-core 的 model/features.go、service/system.go):
//
//	本機模式:內建核心 + 本機 PostgreSQL;遠端模式:omixor server 的 /api/orgs/{org}/system
//
// 清單以核心回傳的為準:沒列出的功能(這個版本沒編進去)就當作沒有,不顯示。
// 這裡是轉接層(同 macOS App 的 System/SystemSettings.swift):
//   - 讀:組織載入時(背景)、每 15 秒、按「重新讀取」時從核心讀
//   - 讀不到:用這次執行裡核心回傳過的最後一份(只在記憶體);都沒有時用保守預設(已實作的開、實驗的關),
//     畫面(系統頁)顯示讀不到的原因,這時不能修改
//   - 寫:system_save 交給核心(要組織管理員),成功後用回傳的內容
//   - 功能開關換了而目前所在頁被關掉 → 切回總覽並提示;navigate(畫面與 MCP)要去關掉的頁面也改去總覽並提示
//
// 鎖的順序:可以拿著 a.Mu 再拿 sysState.mu,反過來不行。呼叫 SystemRefresh / SystemSave 時不要拿著 Mu(會打網路或資料庫)。

// 功能開關的來源
const (
	SysSourceCore     = "core"     // 剛從核心讀到
	SysSourceCached   = "cached"   // 核心讀不到,用這次執行裡最後一份
	SysSourceFallback = "fallback" // 都沒有,用保守預設
	SysSourceLoading  = "loading"  // 還在讀(畫面先照保守預設或最後一份顯示,但不因此把人趕回總覽)
)

// alwaysOnSections 永遠顯示的頁面(核心登記為必要功能;清單讀不到時也一樣)
var alwaysOnSections = map[string]bool{"overview": true, "settings": true, "system": true}

// systemBackend 測試可以換掉(預設是目前模式的核心)
var systemBackend = agentBackend

type sysState struct {
	mu sync.Mutex
	// key 目前這份屬於哪一個(模式 + 組織)
	key    string
	view   *service.SystemSettingsView
	source string
	err    string
	// lastGood 這次執行裡核心回傳過的最後一份(key → 設定)
	lastGood map[string]*service.SystemSettingsView
	// 提示(目前所在頁被關掉、跳到關掉的頁面):每次加一,畫面看到新的才顯示
	noticeSeq  int
	noticeText string
	// writes 寫入次數(背景讀到舊資料時不要蓋掉剛寫的)
	writes int
}

var sysStates sync.Map // *App → *sysState

func sysOf(a *App) *sysState {
	v, _ := sysStates.LoadOrStore(a, &sysState{lastGood: map[string]*service.SystemSettingsView{}})
	return v.(*sysState)
}

// sysKey 模式 + 組織(換了 server 或組織就是另一份)
func sysKey(org string) string { return IssueRemoteURL() + "|" + org }

// SystemFallback 保守預設:這次編譯可用的功能、全部用預設值(已實作的開、實驗的關)
func SystemFallback() *service.SystemSettingsView {
	x := model.OrgSystemSettings{Features: map[string]bool{}}
	v := &service.SystemSettingsView{Features: []service.FeatureState{}, Edition: model.BuildEdition}
	for _, f := range model.AvailableFeatures() {
		v.Features = append(v.Features, service.FeatureState{Feature: f, Enabled: x.Enabled(f.Key), On: x.Own(f.Key)})
	}
	return v
}

// ensureKey 換了組織或模式:先用最後一份(沒有就保守預設),標成讀取中(拿著 st.mu 呼叫)
func (st *sysState) ensureKey(key string) {
	if st.key == key && st.view != nil {
		return
	}
	st.key, st.err = key, ""
	if x := st.lastGood[key]; x != nil {
		st.view, st.source = x, SysSourceLoading
	} else {
		st.view, st.source = SystemFallback(), SysSourceLoading
	}
}

// current 目前組織的功能開關(拿著 st.mu 呼叫)
func (st *sysState) current(org string) *service.SystemSettingsView {
	st.ensureKey(sysKey(org))
	return st.view
}

// enabledIn 清單裡的功能開不開:清單有的照核心的 enabled;登記過但清單沒列(這個版本沒有)的當作關;
// 沒登記的 key(例如 web 才有的 GitLab 分頁)當作開
func enabledIn(v *service.SystemSettingsView, key string) bool {
	for _, f := range v.Features {
		if f.Key == key {
			return f.Enabled
		}
	}
	if _, ok := model.FeatureByKey(key); ok {
		return false
	}
	return true
}

// FeatureEnabled 目前組織的某個功能開不開(key 見 core/model/features.go;頁面用 section 名稱,分頁用「頁面.分頁」)。
// 可以拿著 Mu 呼叫(不打網路);org 是目前的組織
func (a *App) featureEnabled(org, key string) bool {
	if alwaysOnSections[key] {
		return true
	}
	st := sysOf(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	return enabledIn(st.current(org), key)
}

// SectionVisible 頁面顯示不顯示(總覽、設定、系統永遠顯示)。要拿著 Mu 呼叫
func (a *App) SectionVisible(section string) bool {
	return a.featureEnabled(a.CurrentOrgID(), section)
}

// FeatureEnabledNow 同 SectionVisible,但不用拿著 Mu
func (a *App) FeatureEnabledNow(key string) bool {
	return a.featureEnabled(a.currentOrgNow(), key)
}

// sectionTitle 頁面的中文名稱(提示用)
func sectionTitle(section string) string {
	if t, ok := assistSectionTitle[section]; ok {
		return t
	}
	if f, ok := model.FeatureByKey(section); ok {
		return f.Name
	}
	return section
}

// featureNotice 記一則提示(畫面上方顯示幾秒)
func (a *App) featureNotice(text string) {
	st := sysOf(a)
	st.mu.Lock()
	st.noticeSeq++
	st.noticeText = text
	st.mu.Unlock()
}

// GuardSection 要切到 section:關掉的頁面改去總覽並記提示,回傳實際要去的頁面與提示("" = 沒被擋)。要拿著 Mu 呼叫
func (a *App) GuardSection(section string) (string, string) {
	if section == "" || a.SectionVisible(section) {
		return section, ""
	}
	notice := "「" + sectionTitle(section) + "」在系統設定裡關閉了,已改去總覽"
	a.featureNotice(notice)
	return "overview", notice
}

// enforceNav 功能開關換了:目前所在頁被關掉就切回總覽並提示(不拿 Mu 呼叫)
func (a *App) enforceNav() {
	a.Mu.Lock()
	cur := a.Nav.Section
	moved := false
	if cur != "" && !a.SectionVisible(cur) {
		a.featureNotice("「" + sectionTitle(cur) + "」在系統設定裡關閉了,已切回總覽")
		a.Nav.Section, a.Nav.FocusID = "overview", ""
		moved = true
	}
	a.Mu.Unlock()
	if moved {
		a.Changed("nav", "system")
	}
}

// sysErrText 讀寫核心的錯誤 → 畫面看得懂的
func sysErrText(err error) string {
	if errors.Is(err, errNoDB) {
		return "功能開關存在組織(核心):" + err.Error()
	}
	return err.Error()
}

// SystemRefresh 從核心重讀目前組織的功能開關,回傳畫面要不要更新(不拿 Mu 呼叫)
func (a *App) SystemRefresh() bool {
	org := a.currentOrgNow()
	key := sysKey(org)
	st := sysOf(a)
	st.mu.Lock()
	writes := st.writes
	st.mu.Unlock()

	var x *service.SystemSettingsView
	be, err := systemBackend()
	if err == nil {
		if org == "" {
			// 還沒有組織(例如本機模式沒有資料庫時組織清單是空的):用保守預設並說明
			err = errors.New("還沒有組織,先在 設定 › 組織 建立或選一個")
		} else {
			x, err = be.GetSystemSettings(org)
		}
	}

	stillOrg := a.currentOrgNow() == org // 先在 st.mu 外面看(鎖的順序:不能拿著 st.mu 再拿 Mu)
	st.mu.Lock()
	if st.writes != writes || !stillOrg {
		st.mu.Unlock()
		return false // 讀的期間有人寫過或換了組織:不套用
	}
	st.ensureKey(key)
	before := string(Marshal(map[string]any{"v": st.view, "s": st.source, "e": st.err}))
	if err != nil {
		msg := sysErrText(err)
		if msg != st.err {
			log.Printf("系統設定(核心):%s", msg)
		}
		st.err = msg
		if g := st.lastGood[key]; g != nil {
			st.view, st.source = g, SysSourceCached
		} else {
			st.view, st.source = SystemFallback(), SysSourceFallback
		}
	} else {
		st.lastGood[key] = x
		st.view, st.source, st.err = x, SysSourceCore, ""
	}
	after := string(Marshal(map[string]any{"v": st.view, "s": st.source, "e": st.err}))
	st.mu.Unlock()
	if before == after {
		return false
	}
	a.Changed("system")
	a.enforceNav()
	return true
}

// SystemSave 改功能開關(組織管理員;權限由核心判斷);成功後套用核心回傳的(不拿 Mu 呼叫)
func (a *App) SystemSave(features map[string]bool, reset []string) (*service.SystemSettingsView, error) {
	org := a.currentOrgNow()
	if len(features) == 0 && len(reset) == 0 {
		return nil, UserError{"沒有要改的功能"}
	}
	be, err := systemBackend()
	if err != nil {
		return nil, UserError{sysErrText(err)}
	}
	if org == "" {
		return nil, UserError{"還沒有選組織"}
	}
	x, err := be.SaveSystemSettings(org, service.SystemSettingsInput{Features: features, Reset: reset})
	if err != nil {
		return nil, UserError{"存不了:" + sysErrText(err)}
	}
	st := sysOf(a)
	st.mu.Lock()
	key := sysKey(org)
	st.writes++
	st.lastGood[key] = x
	st.key, st.view, st.source, st.err = key, x, SysSourceCore, ""
	st.mu.Unlock()
	a.Changed("system")
	a.enforceNav()
	return x, nil
}

// SystemViewData 畫面(系統頁、側邊欄、各頁分頁)用的功能開關。要拿著 Mu 呼叫(不打網路)
type SystemViewData struct {
	*service.SystemSettingsView
	// Source core / cached / fallback / loading
	Source string `json:"source"`
	// Error 讀不到核心的原因("" = 正常)
	Error string `json:"error"`
	// Mode local(本機核心)/ remote(omixor server)
	Mode string `json:"mode"`
	// EditionLabel 核心(server 或本機核心)版本的中文名稱(正式版 / 開發版)
	EditionLabel string `json:"editionLabel"`
	// WebEdition 這個 web 程式編譯的版本(go build -tags dev 是開發版);和核心不同時系統頁兩個都列
	WebEdition      string `json:"webEdition"`
	WebEditionLabel string `json:"webEditionLabel"`
	// AlwaysOn 永遠顯示的頁面
	AlwaysOn []string `json:"alwaysOn"`
	// Notice 最近一則提示(seq 每次加一)
	Notice struct {
		Seq  int    `json:"seq"`
		Text string `json:"text"`
	} `json:"notice"`
}

func (a *App) systemViewData() SystemViewData {
	st := sysOf(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	v := *st.current(a.CurrentOrgID())
	// 只有剛從核心讀到的才能改(讀不到、用舊的或預設時不能改)
	v.CanEdit = v.CanEdit && st.source == SysSourceCore
	if v.Features == nil {
		v.Features = []service.FeatureState{}
	}
	out := SystemViewData{SystemSettingsView: &v, Source: st.source, Error: st.err, Mode: "local", EditionLabel: model.EditionLabel(v.Edition),
		WebEdition: model.BuildEdition, WebEditionLabel: model.EditionLabel(model.BuildEdition)}
	if IsRemote() {
		out.Mode = "remote"
	}
	for k := range alwaysOnSections {
		out.AlwaysOn = append(out.AlwaysOn, k)
	}
	sort.Strings(out.AlwaysOn)
	out.Notice.Seq, out.Notice.Text = st.noticeSeq, st.noticeText
	return out
}

func systemArgs(args map[string]any) (map[string]bool, []string, error) {
	features := map[string]bool{}
	if m, ok := args["features"].(map[string]any); ok {
		for k, v := range m {
			b, ok := v.(bool)
			if !ok {
				return nil, nil, UserError{"features 的值要是 true 或 false:" + k}
			}
			features[k] = b
		}
	} else if args["features"] != nil {
		return nil, nil, UserError{"features 要是 {功能: true/false}"}
	}
	var reset []string
	if l, ok := args["reset"].([]any); ok {
		for _, v := range l {
			s, ok := v.(string)
			if !ok {
				return nil, nil, UserError{"reset 要是功能 key 的清單"}
			}
			reset = append(reset, s)
		}
	} else if args["reset"] != nil {
		return nil, nil, UserError{"reset 要是功能 key 的清單"}
	}
	return features, reset, nil
}

func init() {
	RegisterView("system", func(a *App) any { return a.systemViewData() })
	// system_reload:重新從核心讀(系統頁的「重新讀取」)
	RegisterAction("system_reload", func(a *App, _ map[string]any) (any, error) {
		a.SystemRefresh()
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return a.systemViewData(), nil
	})
	// system_save {features?: {key: bool}, reset?: [key]}:改功能開關(組織管理員)
	RegisterAction("system_save", func(a *App, args map[string]any) (any, error) {
		features, reset, err := systemArgs(args)
		if err != nil {
			return nil, err
		}
		if _, err := a.SystemSave(features, reset); err != nil {
			return nil, err
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return a.systemViewData(), nil
	})
	OnOrgLoad(func(a *App) {
		// 拿著 Mu 呼叫:先換成這個組織的最後一份(或預設),再到背景讀。測試不在背景讀(要的時候自己呼叫 SystemRefresh)
		st := sysOf(a)
		st.mu.Lock()
		st.ensureKey(sysKey(a.CurrentOrgID()))
		st.mu.Unlock()
		if testing.Testing() {
			return
		}
		go a.SystemRefresh()
	})
	OnStart(func(a *App) {
		if !testing.Testing() {
			go a.SystemRefresh() // 啟動時讀一次(還沒有組織時也要把原因放上畫面)
		}
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		// 每 15 秒重讀(App、別台機器的管理員改了才看得到;請求都在鎖外)
		go func() {
			for range time.Tick(15 * time.Second) {
				a.SystemRefresh()
			}
		}()
	})
}
