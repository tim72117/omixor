package model

// 功能清單(系統設定的開關):每個主要功能(側邊欄的頁面)與值得分開的分頁只在這裡登記一次,App 與 web 都從
// GET /api/orgs/{org}/system 拿這份清單,不自己另外寫一份。和 tools.go 的核心工具登記表同一種做法。
//
//	Key:頁面用 App 的 AppSection(web 的 section)名稱,例如 "development";分頁用「頁面.分頁」,例如 "development.board"
//	Parent:分頁所屬的頁面("" = 頁面本身)
//	Edition:屬於哪個版本(見 edition.go):stable 正式版(已實作的真功能)、dev 開發版(只有範例資料的實驗功能、
//	         還在框架階段的功能);沒寫是 stable。這次編譯沒包含的功能(Available() = false)API 不列、不能開、相關 API 擋掉,
//	         組織設定裡存著的舊值保留但不生效
//	Experimental:還沒真正實作、只有模擬(範例資料)的功能;系統設定裡標「實驗」,預設關(一定是開發版)
//	Default:組織沒設定過時開不開(已實作的開、實驗的關)
//	Required:永遠開著、不能關(總覽、設定、系統;關掉的功能跳過去時會改去總覽)
//
// 頁面關掉時它的分頁全部跟著不顯示;頁面的分頁(有登記的)全部關掉時,頁面也不顯示。
// 組織的設定只存「有改過的」(OrgSystemSettings.Features),讀的時候和清單合併,清單新增的功能自動套用預設。

// Feature 一個可以開關的功能
type Feature struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Parent      string `json:"parent,omitempty"`
	// Edition 屬於哪個版本(EditionStable / EditionDev;清單裡沒寫的在 init 補成 stable)
	Edition      string `json:"edition"`
	Experimental bool   `json:"experimental"`
	// ExperimentalNote 實驗功能的說明(例如「目前是範例資料」)
	ExperimentalNote string `json:"experimentalNote,omitempty"`
	Default          bool   `json:"default"`
	Required         bool   `json:"required,omitempty"`
}

const sampleNote = "目前是範例資料,畫面上的數字與動作都不是真的"

// Features 全部功能(順序就是系統設定畫面的順序;分頁緊接在所屬頁面後面)
var Features = []Feature{
	{Key: "overview", Name: "總覽", Description: "各頁重點彙整(服務健康、部署、Sprint 等卡片目前是範例資料)", Default: true, Required: true},

	{Key: "development", Name: "開發進度", Description: "Issue、GitHub 的 PR 與 Actions", Default: true},
	{Key: "development.overview", Parent: "development", Name: "概覽", Description: "Sprint 進度、交付指標", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "development.board", Parent: "development", Name: "Sprint 看板", Description: "工作項目看板", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "development.issues", Parent: "development", Name: "Issue", Description: "組織的 Issue(存在 core)", Default: true},
	{Key: "development.pulls", Parent: "development", Name: "Pull Requests", Description: "PR 清單與審查(真的 PR 在 GitHub 分頁)", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "development.repos", Parent: "development", Name: "Repositories", Description: "Repo 清單、弱點與覆蓋率", Edition: EditionDev, Experimental: true, ExperimentalNote: sampleNote},
	{Key: "development.github", Parent: "development", Name: "GitHub", Description: "追蹤中 repo 的 PR 與 Actions(唯讀,用各裝置的 token)", Default: true},

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

func init() {
	for i := range Features {
		if Features[i].Edition == "" {
			Features[i].Edition = EditionStable
		}
	}
}

// Available 這次編譯的版本有沒有這個功能
func (f Feature) Available() bool { return EditionIncludes(BuildEdition, f.Edition) }

// AvailableFeatures 這次編譯可用的功能(順序同 Features;系統設定只列這些)
func AvailableFeatures() []Feature {
	out := make([]Feature, 0, len(Features))
	for _, f := range Features {
		if f.Available() {
			out = append(out, f)
		}
	}
	return out
}

// FeatureByKey 找登記的功能(不管這次編譯有沒有;要判斷可不可用看 Available())
func FeatureByKey(key string) (Feature, bool) {
	for _, f := range Features {
		if f.Key == key {
			return f, true
		}
	}
	return Feature{}, false
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

// Enabled 實際開不開(畫面照這個顯示):這次編譯有這個功能、頁面要自己開著,而且有(這次編譯可用的)分頁時至少一個分頁開著;
// 分頁要所屬頁面開著
func (s OrgSystemSettings) Enabled(key string) bool {
	f, ok := FeatureByKey(key)
	if !ok {
		return true // 沒登記的 key(例如舊的清單)當作開
	}
	if !f.Available() {
		return false
	}
	if f.Parent != "" {
		return s.Own(f.Parent) && s.Own(key)
	}
	if !s.Own(key) {
		return false
	}
	hasChild := false
	for _, c := range Features {
		if c.Parent == key && c.Available() {
			hasChild = true
			if s.Own(c.Key) {
				return true
			}
		}
	}
	return !hasChild
}

// Own 這個功能自己的開關(不管所屬頁面):這次編譯沒有的一律關(存著的值保留但不生效);必要的一律開;
// 有改過用改過的,沒改過用預設;沒登記的 key 當作開
func (s OrgSystemSettings) Own(key string) bool {
	f, ok := FeatureByKey(key)
	if !ok {
		return true
	}
	if !f.Available() {
		return false
	}
	if f.Required {
		return true
	}
	if v, ok := s.Features[key]; ok {
		return v
	}
	return f.Default
}
