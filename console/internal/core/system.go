package core

import (
	"errors"
	"log"
	"sort"
	"sync"
	"testing"
	"time"

	"omixor-core/backend"
	"omixor-core/config"
	"omixor-core/model"
	"omixor-core/service"
)

// 功能開關:清單與兩層開關都在核心(omixor-core 的 model/features.go、service/system.go):
//
//	系統層(整台伺服器一份;伺服器管理員改):/api/system/features —— 系統頁
//	組織層(每個組織一份;組織管理員改):/api/orgs/{org}/system —— 設定 › 功能
// 實際可用 = 系統層開放 && 組織層開 && 頁面規則(核心算好放在組織層清單的 enabled)。
// 組織層的清單只列系統層有開放的功能:沒列出的登記功能(系統層沒開放)就當作沒有,不顯示。
// 這裡是轉接層(同 macOS App 的 System/SystemSettings.swift):
//   - 讀:組織載入時(背景)、每 15 秒、按「重新讀取」時從核心讀
//   - 讀不到:用這次執行裡核心回傳過的最後一份(只在記憶體);都沒有時用保守預設(已實作的開、實驗的關),
//     畫面(系統頁)顯示讀不到的原因,這時不能修改
//   - 寫:system_save(組織層,要組織管理員)、server_features_save(系統層,要伺服器管理員)交給核心,成功後用回傳的內容;
//     改了系統層會再重讀組織層(組織層的清單跟著變)
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

// systemBackend 測試可以換掉(預設是系統身分的核心)
var systemBackend = agentBackend

// systemWriteBackend 改功能開關用的核心:有呼叫者用他在 org 的身分
func systemWriteBackend(who *Caller, org string) (backend.Backend, error) {
	if who == nil {
		return systemBackend()
	}
	return coreBackendFor(who, org)
}

type sysState struct {
	mu sync.Mutex
	// key 目前這份屬於哪個組織
	key    string
	view   *service.SystemSettingsView
	source string
	err    string
	// lastGood 這次執行裡核心回傳過的最後一份(key → 設定)
	lastGood map[string]*service.SystemSettingsView
	// 系統層(整台伺服器一份;和組織層一起讀)。serverLast 這次執行裡核心回傳過的最後一份
	server       *service.ServerFeaturesView
	serverSource string
	serverErr    string
	serverLast   *service.ServerFeaturesView
	// 提示(目前所在頁被關掉、跳到關掉的頁面):每次加一,畫面看到新的才顯示
	noticeSeq  int
	noticeText string
	// writes 寫入次數(背景讀到舊資料時不要蓋掉剛寫的)
	writes int
	// reqSeq 每次開始重讀加一;applied 已套用的最大序號。
	// 同時有兩次重讀時(例如定時重讀與換組織的重讀),比較晚開始的先回來,較早那次的結果就是過期的,不能蓋掉
	reqSeq  int
	applied int
}

var sysStates sync.Map // *App → *sysState

func sysOf(a *App) *sysState {
	v, _ := sysStates.LoadOrStore(a, newSysState())
	return v.(*sysState)
}

func newSysState() *sysState {
	return &sysState{lastGood: map[string]*service.SystemSettingsView{}}
}

// SystemFallback 保守預設:兩層都用預設值(系統層只開放原本正式版的功能;組織層已實作的開、實驗的關)
func SystemFallback() *service.SystemSettingsView { return service.DefaultSystemView() }

// ServerFallback 系統層的保守預設(原本正式版的功能開放,其他不開放)
func ServerFallback() *service.ServerFeaturesView { return service.DefaultServerFeaturesView() }

// ensureKey 換了組織:先用最後一份(沒有就保守預設),標成讀取中(拿著 st.mu 呼叫)
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
	// 系統層整台一份,換組織時不變
	if st.server == nil {
		st.serverErr = ""
		if x := st.serverLast; x != nil {
			st.server, st.serverSource = x, SysSourceLoading
		} else {
			st.server, st.serverSource = ServerFallback(), SysSourceLoading
		}
	}
}

// current 目前組織的功能開關(拿著 st.mu 呼叫)
func (st *sysState) current(org string) *service.SystemSettingsView {
	st.ensureKey(org)
	return st.view
}

// enabledIn 清單裡的功能開不開(系統層 && 組織層):系統層標了沒開放的關;清單有的照核心的 enabled;
// 登記過但清單沒列(系統層沒開放)的當作關;沒登記的 key(例如 web 獨有分頁)當作開
func enabledIn(v *service.SystemSettingsView, key string) bool {
	if on, ok := v.ServerFeatures[key]; ok && !on {
		return false
	}
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

// webOnlyTabs web 才有、核心沒登記的分頁(永遠顯示)。頁面自己的開關開著時,即使核心登記的分頁全關,
// 頁面也要顯示。由選用模組登記(RegisterWebOnlyTab;integrations 原本的開發進度 › GitLab 已併進 Repositories,目前沒有模組登記);
// view「system」的 webOnlyTabs 帶給畫面(features.ts)
var webOnlyTabs = map[string][]string{}

// sectionOwnOn 頁面自己的開關開不開(只看清單裡這個 key 的 on)
func sectionOwnOn(v *service.SystemSettingsView, key string) bool {
	for _, f := range v.Features {
		if f.Key == key {
			return f.On
		}
	}
	return false
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
	v := st.current(org)
	if enabledIn(v, key) {
		return true
	}
	// 有 web 獨有分頁的頁面:頁面自己開著就顯示(那些分頁永遠顯示)
	return len(webOnlyTabs[key]) > 0 && sectionOwnOn(v, key)
}

// SectionVisible 頁面顯示不顯示(總覽、設定、系統永遠顯示)。要拿著 Mu 呼叫
func (a *App) SectionVisible(section string) bool {
	return a.featureEnabled(a.CurrentOrgID(), section)
}

// FeatureOn 目前組織的某個功能(頁面或「頁面.分頁」)開不開。要拿著 Mu 呼叫
func (a *App) FeatureOn(key string) bool { return a.featureEnabled(a.CurrentOrgID(), key) }

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
	notice := closedSectionNotice(section)
	a.featureNotice(notice)
	return "overview", notice
}

func closedSectionNotice(section string) string {
	return "「" + sectionTitle(section) + "」在系統設定裡關閉了,已改去總覽"
}

// enforceNav 功能開關換了:目前所在頁被關掉就切回總覽並提示(不拿 Mu 呼叫)
func (a *App) enforceNav() {
	a.Mu.Lock()
	cur := a.Nav.Section
	moved := false
	if cur != "" && !a.SectionVisible(cur) {
		if !a.PerUserUI() { // 分人時沒有人看 App.Nav(各畫面自己換掉關閉的頁面),不要在大家的畫面放提示
			a.featureNotice("「" + sectionTitle(cur) + "」在系統設定裡關閉了,已切回總覽")
		}
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

// sysBegin 開始一次讀或寫:同時拿 Mu 與 st.mu(順序:先 Mu),取得目前組織、把 st.key 換成它,回傳序號與寫入次數。
// 之後在 st.mu 裡比對 st.key 就知道期間有沒有換組織(換組織的 OnOrgLoad 會在同一把 Mu 裡改掉 st.key)
func (a *App) sysBegin() (org string, seq, writes int) {
	st := sysOf(a)
	a.Mu.Lock()
	org = a.CurrentOrgID()
	st.mu.Lock()
	st.ensureKey(org)
	st.reqSeq++
	seq, writes = st.reqSeq, st.writes
	st.mu.Unlock()
	a.Mu.Unlock()
	return
}

// SystemRefresh 從核心重讀目前組織的功能開關,回傳畫面要不要更新(不拿 Mu 呼叫)
func (a *App) SystemRefresh() bool {
	org, seq, writes := a.sysBegin()
	st := sysOf(a)

	var x *service.SystemSettingsView
	var srv *service.ServerFeaturesView
	be, err := systemBackend()
	srvErr := err
	if err == nil {
		srv, srvErr = be.GetServerFeatures()
		if org == "" {
			// 還沒有組織(例如沒有資料庫時組織清單是空的):用保守預設並說明
			err = errors.New("還沒有組織,先在 設定 › 組織 建立或選一個")
		} else {
			x, err = be.GetSystemSettings(org)
		}
	}

	st.mu.Lock()
	// 都在 st.mu 裡判斷(不另外拿 Mu):讀的期間有人寫過、換了組織(st.key 被 OnOrgLoad 換掉),
	// 或比這次晚開始的重讀已經套用過 → 這次的結果過期,不套用
	if st.writes != writes || st.key != org || seq <= st.applied {
		st.mu.Unlock()
		return false
	}
	st.applied = seq
	before := string(Marshal(map[string]any{"v": st.view, "s": st.source, "e": st.err, "sv": st.server, "ss": st.serverSource, "se": st.serverErr}))
	st.applyServer(srv, srvErr)
	if err != nil {
		msg := sysErrText(err)
		if msg != st.err {
			log.Printf("系統設定(核心):%s", msg)
		}
		st.err = msg
		if g := st.lastGood[org]; g != nil {
			st.view, st.source = g, SysSourceCached
		} else {
			st.view, st.source = SystemFallback(), SysSourceFallback
		}
	} else {
		st.lastGood[org] = x
		st.view, st.source, st.err = x, SysSourceCore, ""
	}
	after := string(Marshal(map[string]any{"v": st.view, "s": st.source, "e": st.err, "sv": st.server, "ss": st.serverSource, "se": st.serverErr}))
	st.mu.Unlock()
	if before == after {
		return false
	}
	a.Changed("system")
	a.enforceNav()
	return true
}

// applyServer 套用讀到的系統層(拿著 st.mu 呼叫);讀不到時用最後一份或保守預設
func (st *sysState) applyServer(x *service.ServerFeaturesView, err error) {
	if err != nil {
		msg := sysErrText(err)
		if msg != st.serverErr {
			log.Printf("系統層的功能設定(核心):%s", msg)
		}
		st.serverErr = msg
		if g := st.serverLast; g != nil {
			st.server, st.serverSource = g, SysSourceCached
		} else {
			st.server, st.serverSource = ServerFallback(), SysSourceFallback
		}
		return
	}
	st.serverLast = x
	st.server, st.serverSource, st.serverErr = x, SysSourceCore, ""
}

// ServerFeaturesSave 改這台伺服器開放的功能(系統層;核心要伺服器管理員,web 的登入者權限由 internal/web 先擋)。
// 成功後套用核心回傳的,並重讀組織層(組織層的清單跟著系統層變)。不拿 Mu 呼叫
func (a *App) ServerFeaturesSave(features map[string]bool, reset []string, who ...*Caller) (*service.ServerFeaturesView, error) {
	if len(features) == 0 && len(reset) == 0 {
		return nil, UserError{"沒有要改的功能"}
	}
	a.sysBegin()
	be, err := systemWriteBackend(firstCaller(who), "")
	if err != nil {
		return nil, UserError{sysErrText(err)}
	}
	x, err := be.SaveServerFeatures(service.SystemSettingsInput{Features: features, Reset: reset})
	if err != nil {
		return nil, UserError{"存不了:" + sysErrText(err)}
	}
	st := sysOf(a)
	st.mu.Lock()
	st.serverLast = x
	st.server, st.serverSource, st.serverErr = x, SysSourceCore, ""
	st.mu.Unlock()
	a.Changed("system")
	a.InvalidateScoped() // 系統層換了:各組織快取的組織層清單也跟著變
	a.SystemRefresh()
	return x, nil
}

// SystemSave 改目前組織的功能開關(見 SystemSaveFor)
func (a *App) SystemSave(features map[string]bool, reset []string) (*service.SystemSettingsView, error) {
	return a.SystemSaveFor("", features, reset)
}

// errOrgChanged 畫面送出時顯示的組織和目前的組織不同(別的分頁或別人剛切換了組織)
func errOrgChanged(shown, now string) error {
	return StatusError{Status: 409, Message: "目前的組織已經換成「" + now + "」(畫面上是「" + shown + "」),沒有存;請重新整理頁面後再改"}
}

// SystemSaveFor 改功能開關(組織管理員;核心判斷權限,web 的登入者權限由 internal/web 先擋);
// expectOrg 是畫面顯示的組織("" = 不檢查),和目前的組織不同時不存、回 409。成功後套用核心回傳的(不拿 Mu 呼叫)
func (a *App) SystemSaveFor(expectOrg string, features map[string]bool, reset []string, who ...*Caller) (*service.SystemSettingsView, error) {
	if len(features) == 0 && len(reset) == 0 {
		return nil, UserError{"沒有要改的功能"}
	}
	org, _, _ := a.sysBegin()
	if expectOrg != "" && expectOrg != org {
		return nil, errOrgChanged(expectOrg, org)
	}
	be, err := systemWriteBackend(firstCaller(who), org)
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
	st.writes++
	st.lastGood[org] = x
	// 存的期間換了組織:只記在那個組織的最後一份,不蓋掉現在組織的畫面
	if st.key == org {
		st.view, st.source, st.err = x, SysSourceCore, ""
	}
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
	// Server 系統層(這台伺服器開放哪些功能;系統頁用)
	Server ServerViewData `json:"server"`
	// Org 這份設定屬於哪個組織(畫面存的時候帶回來,和目前的組織不同就拒絕)
	Org string `json:"org"`
	// ReadOnlyReason 不能改的原因(web 的登入者不是管理員時由 internal/web 填;"" = 沒有額外原因)
	ReadOnlyReason string `json:"readOnlyReason,omitempty"`
	// AlwaysOn 永遠顯示的頁面
	AlwaysOn []string `json:"alwaysOn"`
	// Modules 這次編譯進來的 web 選用模組(例如 integrations;畫面依此顯示設定頁與分頁)
	Modules []string `json:"modules"`
	// WebOnlyTabs web 才有、核心沒登記的分頁(section → 分頁;由選用模組登記)
	WebOnlyTabs map[string][]string `json:"webOnlyTabs"`
	// Notice 最近一則提示(seq 每次加一)
	Notice struct {
		Seq  int    `json:"seq"`
		Text string `json:"text"`
	} `json:"notice"`
}

// ServerViewData 畫面用的系統層
type ServerViewData struct {
	*service.ServerFeaturesView
	// Source core / cached / fallback / loading(同組織層)
	Source string `json:"source"`
	// Error 讀不到核心的原因("" = 正常)
	Error string `json:"error"`
	// ReadOnlyReason 不能改的原因(web 的登入者不是管理員時由 internal/web 填)
	ReadOnlyReason string `json:"readOnlyReason,omitempty"`
}

func (a *App) systemViewData() SystemViewData {
	st := sysOf(a)
	st.mu.Lock()
	defer st.mu.Unlock()
	org := a.CurrentOrgID()
	v := *st.current(org)
	// 只有剛從核心讀到的才能改(讀不到、用舊的或預設時不能改)
	v.CanEdit = v.CanEdit && st.source == SysSourceCore
	if v.Features == nil {
		v.Features = []service.FeatureState{}
	}
	sv := *st.server
	sv.CanEdit = sv.CanEdit && st.serverSource == SysSourceCore
	if sv.Features == nil {
		sv.Features = []service.ServerFeatureState{}
	}
	out := SystemViewData{SystemSettingsView: &v, Source: st.source, Error: st.err, Org: org,
		Server: ServerViewData{ServerFeaturesView: &sv, Source: st.serverSource, Error: st.serverErr}}
	for k := range alwaysOnSections {
		out.AlwaysOn = append(out.AlwaysOn, k)
	}
	sort.Strings(out.AlwaysOn)
	out.Notice.Seq, out.Notice.Text = st.noticeSeq, st.noticeText
	out.Modules, out.WebOnlyTabs = WebModules(), WebOnlyTabs()
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
	// system_save {org?, features?: {key: bool}, reset?: [key]}:改功能開關(組織管理員;web 的登入者權限由 internal/web 先擋)。
	// org 是畫面顯示的組織,和目前的組織不同時回 409
	RegisterAction("system_save", func(a *App, args map[string]any) (any, error) {
		features, reset, err := systemArgs(args)
		if err != nil {
			return nil, err
		}
		org, _ := args["org"].(string)
		if _, err := a.SystemSaveFor(org, features, reset, CallerOf(args)); err != nil {
			return nil, err
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return a.systemViewData(), nil
	})
	// server_features_save {features?: {key: bool}, reset?: [key]}:改這台伺服器開放的功能(系統層;伺服器管理員;
	// web 的登入者權限由 internal/web 先擋)
	RegisterAction("server_features_save", func(a *App, args map[string]any) (any, error) {
		features, reset, err := systemArgs(args)
		if err != nil {
			return nil, err
		}
		if _, err := a.ServerFeaturesSave(features, reset, CallerOf(args)); err != nil {
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
		st.ensureKey(a.CurrentOrgID())
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
		if config.Current().Bool(config.NoTimers) {
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
