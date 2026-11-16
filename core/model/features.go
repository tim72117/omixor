package model

// 功能清單(系統設定的開關):每個主要功能(側邊欄的頁面)與值得分開的分頁只在這裡登記一次,App 與 web 都從
// GET /api/orgs/{org}/system 拿這份清單,不自己另外寫一份。和 tools.go 的核心工具登記表同一種做法。
//
//	Key:頁面用 App 的 AppSection(web 的 section)名稱,例如 "development";分頁用「頁面.分頁」,例如 "development.board"
//	Parent:分頁所屬的頁面("" = 頁面本身)
//	Edition:原本的版本分類(見 edition.go):stable(已實作的真功能)、dev(只有範例資料的實驗功能、還在框架階段的功能);
//	         沒寫是 stable。現在所有功能都編進去,Edition 只當作「系統層的預設值」:stable 預設系統開、dev 預設系統關
//	Experimental:還沒真正實作、只有模擬(範例資料)的功能;系統設定裡標「實驗」,預設關(一定是開發版)
//	Default:組織沒設定過時開不開(已實作的開、實驗的關)
//	退場的 key:見 LegacyFeatureKeys(讀舊資料時換成新 key)
//	Required:永遠開著、不能關(總覽、設定、系統;關掉的功能跳過去時會改去總覽)
//
// 兩層開關(都存在 core 的資料庫,不再靠編譯決定):
//
//	系統層(ServerFeatures):伺服器管理員決定這台伺服器開放哪些功能(整台共用);沒開放的功能就像以前沒編進去:
//	         組織的清單不列、組織不能開、相關 API 擋掉;組織存著的舊值保留但不生效
//	組織層(OrgSystemSettings):組織管理員在系統層開放的功能裡,決定自己組織要用哪些
//
// 實際可用 = 系統層開放 && 組織層開 && 頁面規則:頁面關掉時它的分頁全部跟著不顯示;頁面的分頁(系統層有開放的)全部關掉時,
// 頁面也不顯示;必要功能永遠開(兩層都不能關)。
// 兩層都只存「有改過的」,讀的時候和清單合併,清單新增的功能自動套用預設。

// Feature 一個可以開關的功能
type Feature struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Parent      string `json:"parent,omitempty"`
	// Edition 原本的版本分類(EditionStable / EditionDev;清單裡沒寫的在 init 補成 stable)。
	// 不再影響編譯,只決定系統層的預設值(見 SystemDefault)
	Edition      string `json:"edition"`
	Experimental bool   `json:"experimental"`
	// ExperimentalNote 實驗功能的說明(例如「目前是範例資料」)
	ExperimentalNote string `json:"experimentalNote,omitempty"`
	// Default 組織層沒設定過時開不開
	Default  bool `json:"default"`
	Required bool `json:"required,omitempty"`
}

const sampleNote = "目前是範例資料,畫面上的數字與動作都不是真的"

// Features 全部功能(順序就是系統設定畫面的順序;分頁緊接在所屬頁面後面)
var Features = []Feature{
	{Key: "overview", Name: "總覽", Description: "各頁重點彙整(服務健康、部署、Sprint 等卡片目前是範例資料)", Default: true, Required: true},

	{Key: "development", Name: "開發進度", Description: "Issue、追蹤中 repo 的 PR/MR 與建置", Default: true},
	{Key: "development.overview", Parent: "development", Name: "概覽", Description: "Sprint 進度、交付指標", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "development.board", Parent: "development", Name: "看板", Description: "依狀態(待處理、進行中、已完成)顯示組織的 Issue", Default: true},
	{Key: "development.issues", Parent: "development", Name: "Issue", Description: "組織的 Issue(存在 core)", Default: true},
	{Key: "development.pulls", Parent: "development", Name: "Pull Requests", Description: "PR 清單與審查(真的 PR 在 Repositories 分頁)", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	// development.repos(Repositories 分頁:GitHub 與 GitLab 追蹤中的 repo)屬於選用模組 integrations
	// (core/modules/integrations),模組編進來時才登記,排在開發進度的分頁最後。原本的 development.github 已退場(見 LegacyFeatureKeys)

	{Key: "pipelines", Name: "CI/CD", Description: "建置、部署與旁白發佈", Default: true},
	{Key: "pipelines.runs", Parent: "pipelines", Name: "執行紀錄", Description: "Pipeline 執行、重試與取消", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "pipelines.deployments", Parent: "pipelines", Name: "部署", Description: "部署進度、核准與回滾", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "pipelines.environments", Parent: "pipelines", Name: "環境", Description: "各環境的版本", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "pipelines.voice", Parent: "pipelines", Name: "旁白發佈", Description: "shuttle 專案的旁白語音批次(試聽後發佈)", Default: true},

	{Key: "infrastructure", Name: "伺服器與服務", Description: "服務、主機與 GCP Cloud Run", Default: true},
	{Key: "infrastructure.services", Parent: "infrastructure", Name: "服務", Description: "服務健康與版本", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "infrastructure.servers", Parent: "infrastructure", Name: "伺服器", Description: "主機指標", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "infrastructure.map", Parent: "infrastructure", Name: "服務地圖", Description: "服務之間的相依關係", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "infrastructure.gcp", Parent: "infrastructure", Name: "GCP", Description: "Cloud Run 狀態、日誌與監控(唯讀)", Default: true},

	{Key: "monitoring", Name: "監控與告警", Description: "告警、儀表板、日誌與網站訪客", Default: true},
	{Key: "monitoring.alerts", Parent: "monitoring", Name: "告警", Description: "告警的確認、靜音與解除", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote + "(MCP 建立的告警也只存在記憶體)"},
	{Key: "monitoring.dashboards", Parent: "monitoring", Name: "儀表板", Description: "指標圖表", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "monitoring.logs", Parent: "monitoring", Name: "日誌", Description: "日誌查詢與即時追蹤", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "monitoring.rules", Parent: "monitoring", Name: "規則", Description: "告警規則", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "monitoring.visitors", Parent: "monitoring", Name: "網站訪客", Description: "evoleap 的會員與訪客統計(透過連線讀取)", Default: true},

	{Key: "incidents", Name: "事件", Description: "宣告與處理事件(存在 core)", Default: true},
	{Key: "incidents.list", Parent: "incidents", Name: "事件", Description: "事件列表、時間軸與檢討", Default: true},
	{Key: "incidents.oncall", Parent: "incidents", Name: "值班", Description: "值班表", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "incidents.stats", Parent: "incidents", Name: "統計", Description: "事件的統計(用真的事件計算)", Default: true},

	{Key: "automation", Name: "AI 自動化", Description: "虛擬角色、派發、組織鏈、自動執行與粉絲專頁", Default: true},
	{Key: "automation.inbox", Parent: "automation", Name: "待核准", Description: "AI 請求的核准收件匣", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote + "(MCP 的核准請求只存在記憶體,核准後不會真的執行)"},
	{Key: "automation.agents", Parent: "automation", Name: "虛擬角色", Description: "角色設定與任務(存在 core)", Default: true},
	{Key: "automation.dispatch", Parent: "automation", Name: "派發", Description: "任務派發與領取(存在 core)", Default: true},
	{Key: "automation.chain", Parent: "automation", Name: "組織鏈", Description: "誰可以派工作給誰(存在 core)", Default: true},
	{Key: "automation.runner", Parent: "automation", Name: "自動執行", Description: "用這台的 Claude Code 處理派給角色的任務", Default: true},
	{Key: "automation.facebook", Parent: "automation", Name: "粉絲專頁", Description: "Facebook 粉絲專頁的草稿、發文與成效", Default: true},
	{Key: "automation.flows", Parent: "automation", Name: "自動化流程", Description: "流程與自主程度", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "automation.runs", Parent: "automation", Name: "執行紀錄", Description: "流程的執行紀錄", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "automation.insights", Parent: "automation", Name: "成效", Description: "自動化省下的時間與成效", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},

	{Key: "help", Name: "需要協助", Description: "卡住的角色任務與 @ 我的訊息", Default: true},
	{Key: "channels", Name: "對話", Description: "成員與虛擬角色在同一個頻道對話(存在 core)", Default: true},
	{Key: "cabinet", Name: "文件櫃", Description: "組織的文件(存在 core)", Default: true},
	{Key: "familyAssistant", Name: "家庭助理", Description: "家庭助理樣板:像管家的角色,在訊息頻道 @ 就會協助家裡的各種事務", Edition: EditionDev, Experimental: true, ExperimentalNote: "樣板還在實作中,功能可能不完整"},
	{Key: "mcpGateway", Name: "外部 MCP 閘道", Description: "登記外部 MCP 伺服器,由 App 連線並把工具依角色權限開放給 Claude Code 與自動執行的角色", Edition: EditionDev, Experimental: true, ExperimentalNote: "還在實作中:只有 macOS App 會連線,web 與 iOS 還沒有畫面"},
	{Key: "settings", Name: "設定", Description: "組織、整合、連線等設定", Default: true, Required: true},
	{Key: "system", Name: "系統", Description: "開關各項功能", Default: true, Required: true},
}

// LegacyFeatureKeys 退場的功能 key → 取代它的 key。
//
//	development.github → development.repos:GitHub、GitLab 分頁併入 Repositories 分頁
//
// 讀:系統層、組織層存著的設定只有舊 key 的值、沒有新 key 的值時,舊值當作新 key 的值(相容舊資料;見 StoredFeature)。
// 寫:輸入的舊 key 轉成新 key(同時給新舊兩個時以新 key 為準);存的時候順便把舊 key 的值搬到新 key 並拿掉舊 key(見 MigrateLegacyFeatures)
var LegacyFeatureKeys = map[string]string{
	"development.github": "development.repos",
}

// CanonicalFeatureKey 退場的 key 換成取代它的 key;其他照舊
func CanonicalFeatureKey(key string) string {
	if k, ok := LegacyFeatureKeys[key]; ok {
		return k
	}
	return key
}

// StoredFeature 存著的設定裡這個 key 的值:有新 key 用新 key;沒有時用退場的舊 key 的值;都沒有 ok = false
func StoredFeature(m map[string]bool, key string) (v, ok bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for old, k := range LegacyFeatureKeys {
		if k == key {
			if v, ok := m[old]; ok {
				return v, true
			}
		}
	}
	return false, false
}

// MigrateLegacyFeatures 把存著的舊 key 的值搬到新 key(新 key 已經有值時以新 key 為準),並拿掉舊 key(就地修改)
func MigrateLegacyFeatures(m map[string]bool) {
	for old, k := range LegacyFeatureKeys {
		v, ok := m[old]
		if !ok {
			continue
		}
		if _, has := m[k]; !has {
			m[k] = v
		}
		delete(m, old)
	}
}

func init() {
	for i := range Features {
		if Features[i].Edition == "" {
			Features[i].Edition = EditionStable
		}
	}
}

// RegisterFeature 選用模組登記自己擁有的功能(見 core/module;只能在 init() 呼叫)。
// 分頁(Parent 不是空的)排在所屬頁面的最後一個分頁後面;頁面排在「設定」之前。key 空白或重複、所屬頁面沒登記會 panic
func RegisterFeature(f Feature) {
	if f.Key == "" {
		panic("功能 key 空白")
	}
	if _, dup := FeatureByKey(f.Key); dup {
		panic("功能重複登記:" + f.Key)
	}
	if f.Edition == "" {
		f.Edition = EditionStable
	}
	at := -1
	if f.Parent != "" {
		for i, x := range Features {
			if x.Key == f.Parent || x.Parent == f.Parent {
				at = i + 1
			}
		}
		if at < 0 {
			panic("功能 " + f.Key + " 所屬的頁面沒登記:" + f.Parent)
		}
	} else {
		at = len(Features)
		for i, x := range Features {
			if x.Key == "settings" {
				at = i
				break
			}
		}
	}
	Features = append(Features, Feature{})
	copy(Features[at+1:], Features[at:])
	Features[at] = f
}

// SystemDefault 系統層沒設定過時開不開:必要的、原本正式版(stable)的開;原本只有開發版的(實驗、範例資料、家庭助理、
// MCP 閘道等)關
func (f Feature) SystemDefault() bool { return f.Required || f.Edition == EditionStable }

// FeatureByKey 找登記的功能(不管系統層有沒有開放)
func FeatureByKey(key string) (Feature, bool) {
	for _, f := range Features {
		if f.Key == key {
			return f, true
		}
	}
	return Feature{}, false
}

// ServerFeatures 系統層的功能設定(整台伺服器一份;任何登入者讀、伺服器管理員改,規則在 service/system.go)
type ServerFeatures struct {
	// Features 有改過的功能開關(key → 開放不開放);沒列的用 Feature.SystemDefault
	Features map[string]bool `json:"features"`
	// Saved 核心存過這份設定(false = 還沒設定過,全部用預設)
	Saved     bool   `json:"saved"`
	UpdatedAt *Time  `json:"updatedAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// Clone 深拷貝
func (s ServerFeatures) Clone() ServerFeatures {
	m := make(map[string]bool, len(s.Features))
	for k, v := range s.Features {
		m[k] = v
	}
	s.Features = m
	if s.UpdatedAt != nil {
		t := *s.UpdatedAt
		s.UpdatedAt = &t
	}
	return s
}

// Own 系統層這個功能自己的開關(不管所屬頁面):必要的一律開;有改過用改過的,沒改過用 SystemDefault;沒登記的 key 當作開
func (s ServerFeatures) Own(key string) bool {
	f, ok := FeatureByKey(key)
	if !ok {
		return true
	}
	if f.Required {
		return true
	}
	if v, ok := StoredFeature(s.Features, key); ok {
		return v
	}
	return f.SystemDefault()
}

// Has 這台伺服器有沒有開放這個功能:自己開著,分頁還要所屬頁面也開著(取代以前的「這次編譯有沒有」)
func (s ServerFeatures) Has(key string) bool {
	if !s.Own(key) {
		return false
	}
	if f, ok := FeatureByKey(key); ok && f.Parent != "" {
		return s.Own(f.Parent)
	}
	return true
}

// Available 系統層有開放的功能(順序同 Features;組織的系統設定只列這些)
func (s ServerFeatures) Available() []Feature {
	out := make([]Feature, 0, len(Features))
	for _, f := range Features {
		if s.Has(f.Key) {
			out = append(out, f)
		}
	}
	return out
}

// LegacyEdition 給舊用戶端的相容值(過時):系統層開放了任何原本只有開發版的功能時是 dev,否則 stable
func (s ServerFeatures) LegacyEdition() string {
	for _, f := range Features {
		if f.Edition != EditionStable && s.Has(f.Key) {
			return EditionDev
		}
	}
	return EditionStable
}

// OrgSystemSettings 組織的系統設定(一個組織一份;成員讀、組織管理員改,規則在 service/system.go)
type OrgSystemSettings struct {
	// Features 有改過的功能開關(key → 開不開);沒列的用 Feature.Default
	Features map[string]bool `json:"features"`
	// Saved 核心存過這份設定(false = 還沒設定過,全部用預設)
	Saved     bool   `json:"saved"`
	UpdatedAt *Time  `json:"updatedAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// Clone 深拷貝
func (s OrgSystemSettings) Clone() OrgSystemSettings {
	m := make(map[string]bool, len(s.Features))
	for k, v := range s.Features {
		m[k] = v
	}
	s.Features = m
	if s.UpdatedAt != nil {
		t := *s.UpdatedAt
		s.UpdatedAt = &t
	}
	return s
}

// Enabled 實際開不開(畫面照這個顯示):系統層有開放、頁面要自己開著,而且有(系統層開放的)分頁時至少一個分頁開著;
// 分頁要所屬頁面開著
func (s OrgSystemSettings) Enabled(srv ServerFeatures, key string) bool {
	f, ok := FeatureByKey(key)
	if !ok {
		return true // 沒登記的 key(例如舊的清單)當作開
	}
	if !srv.Has(key) {
		return false
	}
	if f.Parent != "" {
		return s.Own(srv, f.Parent) && s.Own(srv, key)
	}
	if !s.Own(srv, key) {
		return false
	}
	hasChild := false
	for _, c := range Features {
		if c.Parent == key && srv.Has(c.Key) {
			hasChild = true
			if s.Own(srv, c.Key) {
				return true
			}
		}
	}
	return !hasChild
}

// Own 組織層這個功能自己的開關(不管所屬頁面):系統層沒開放的一律關(存著的值保留但不生效);必要的一律開;
// 有改過用改過的,沒改過用預設;沒登記的 key 當作開
func (s OrgSystemSettings) Own(srv ServerFeatures, key string) bool {
	f, ok := FeatureByKey(key)
	if !ok {
		return true
	}
	if !srv.Has(key) {
		return false
	}
	if f.Required {
		return true
	}
	if v, ok := StoredFeature(s.Features, key); ok {
		return v
	}
	return f.Default
}
