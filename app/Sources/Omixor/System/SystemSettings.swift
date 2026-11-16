import Foundation
import SwiftUI

// 功能開關分兩層,都存在 core 的資料庫(core/model/features.go 是唯一的清單;規則見 core/service/system.go),不再靠編譯決定:
//   - 系統層(整台伺服器一份):GET/POST /api/system/features。任何登入者讀、伺服器管理員改(本機模式的本機使用者就是)。
//     App 的「系統」頁管這一層。沒設定過用 systemDefault(原本正式版的開放、原本只有開發版的不開放);必要功能永遠開放。
//   - 組織層(每個組織一份):GET/POST /api/orgs/<組織>/system。成員讀、組織管理員改。App 的「設定 › 功能」管這一層。
//     回應只列系統層開放的功能(每項 system = true),serverFeatures 附上每個登記的 key 在系統層開放不開放。
// 實際可用 = 系統層開放 && 組織層開 && 頁面規則(頁面關了分頁不顯示;系統層開放的分頁全關時頁面不顯示;必要的永遠開)。
// core 已經把兩層與頁面規則算進每項的 enabled,App 再看一次 serverFeatures(FeatureFlags.enabled)。
//
// 讀取一律透過目前連線的 core server(IssueServerConfig.current,本機模式是本機守護服務):
//   - 讀得到:照 core 的
//   - 讀不到:用這次執行裡 core 回傳過的最後一份(只在記憶體,兩層各自一份)
//   - 都沒有:用保守預設(FeatureFallback:系統層照 systemDefault、組織層照預設值),畫面顯示讀不到的原因
//   - 舊版 server(組織層的回應沒有 serverFeatures、也沒有 /api/system/features):系統層用保守預設,組織層照 server 的
//
// 判斷某個功能開不開:app.isFeatureEnabled("key")(頁面用 app.isVisible(section)、分頁用 app.visibleTabs)。
// 所有功能都編進 App(不再有 OMIXOR_DEV / 正式版、開發版之分)。
//
// 選用模組(core/module;例如 integrations = 程式碼倉庫 GitHub、GitLab):server 編譯時決定要不要編進去,
// 沒編進去的模組,它的功能不在功能清單裡(畫面自然不顯示)、API 路由 404。App 和功能開關一起讀 GET /api/modules(SystemStore),
// 只在記憶體(FeatureFlags.modules);舊版 server 沒有這個端點(404)時當作全部模組都在(維持舊行為)。
// 設定頁這類不靠功能 key 的畫面用 app.features.hasModule("…") 決定顯示不顯示(同 web 的 features.ts 的 hasModule)。

/// core 組織層回傳的一個功能
struct CoreFeature: Codable, Equatable, Identifiable {
    var key: String
    var name: String
    var description: String
    /// 所屬頁面(nil = 頁面本身)
    var parent: String?
    /// 原本的版本分類(stable / dev;只是資料,決定系統層的預設值;nil = 比較舊的 core 沒給)
    var edition: String?
    var experimental: Bool
    var experimentalNote: String?
    var defaultOn: Bool
    var required: Bool
    /// 實際開不開(畫面照這個顯示;core 已算進系統層與頁面規則)
    var enabled: Bool
    /// 這個功能自己在組織層的開關(設定 › 功能的開關照這個)
    var on: Bool
    /// 組織改過(false = 用預設)
    var changed: Bool
    /// 系統層有開放(新的 core 一律 true,因為清單只列有開放的;nil = 舊版 server 沒給)
    var system: Bool?
    var id: String { key }

    enum CodingKeys: String, CodingKey {
        case key, name, description, parent, edition, experimental, experimentalNote, required, enabled, on, changed, system
        case defaultOn = "default"
    }

    init(key: String, name: String, description: String = "", parent: String? = nil, edition: String? = nil, experimental: Bool, experimentalNote: String? = nil,
         defaultOn: Bool, required: Bool, enabled: Bool, on: Bool, changed: Bool = false, system: Bool? = nil) {
        self.key = key; self.name = name; self.description = description; self.parent = parent; self.edition = edition
        self.experimental = experimental; self.experimentalNote = experimentalNote; self.defaultOn = defaultOn
        self.required = required; self.enabled = enabled; self.on = on; self.changed = changed; self.system = system
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decode(String.self, forKey: .key)
        name = (try? c.decode(String.self, forKey: .name)) ?? key
        description = (try? c.decode(String.self, forKey: .description)) ?? ""
        parent = (try? c.decode(String.self, forKey: .parent)).flatMap { $0.isEmpty ? nil : $0 }
        edition = (try? c.decode(String.self, forKey: .edition)).flatMap { $0.isEmpty ? nil : $0 }
        experimental = (try? c.decode(Bool.self, forKey: .experimental)) ?? false
        experimentalNote = try? c.decode(String.self, forKey: .experimentalNote)
        defaultOn = (try? c.decode(Bool.self, forKey: .defaultOn)) ?? true
        required = (try? c.decode(Bool.self, forKey: .required)) ?? false
        enabled = (try? c.decode(Bool.self, forKey: .enabled)) ?? true
        on = (try? c.decode(Bool.self, forKey: .on)) ?? enabled
        changed = (try? c.decode(Bool.self, forKey: .changed)) ?? false
        system = try? c.decode(Bool.self, forKey: .system)
    }
}

/// GET /api/orgs/<組織>/system 的回應(組織層)
struct CoreSystemSettings: Codable, Equatable {
    var features: [CoreFeature]
    var saved: Bool
    var updatedAt: Date?
    var updatedBy: String?
    /// 過時(core 保留給舊用戶端;不再有編譯版本)。只用來認出很舊的 core(沒有 serverFeatures 也沒有 edition)
    var edition: String?
    /// 每個登記的功能在系統層開放不開放(key → 開放;nil = 舊版 server 沒給)
    var serverFeatures: [String: Bool]?
    var canEdit: Bool

    init(features: [CoreFeature], saved: Bool = false, updatedAt: Date? = nil, updatedBy: String? = nil, edition: String? = nil,
         serverFeatures: [String: Bool]? = nil, canEdit: Bool = false) {
        self.features = features; self.saved = saved; self.updatedAt = updatedAt; self.updatedBy = updatedBy
        self.edition = edition; self.serverFeatures = serverFeatures; self.canEdit = canEdit
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        features = (try? c.decode([CoreFeature].self, forKey: .features)) ?? []
        saved = (try? c.decode(Bool.self, forKey: .saved)) ?? false
        updatedAt = try? c.decode(Date.self, forKey: .updatedAt)
        updatedBy = try? c.decode(String.self, forKey: .updatedBy)
        edition = (try? c.decode(String.self, forKey: .edition)).flatMap { $0.isEmpty ? nil : $0 }
        serverFeatures = try? c.decode([String: Bool].self, forKey: .serverFeatures)
        canEdit = (try? c.decode(Bool.self, forKey: .canEdit)) ?? false
    }
}

/// core 系統層回傳的一個功能(GET /api/system/features)
struct CoreServerFeature: Codable, Equatable, Identifiable {
    var key: String
    var name: String
    var description: String
    var parent: String?
    var edition: String?
    var experimental: Bool
    var experimentalNote: String?
    /// 組織層沒設定過時開不開
    var defaultOn: Bool
    var required: Bool
    /// 系統層沒設定過時開放不開放(原本正式版的開放、原本只有開發版的不開放)
    var systemDefault: Bool
    /// 系統層這個功能自己的開關(系統頁的開關照這個;不管所屬頁面)
    var on: Bool
    /// 這台伺服器實際有開放(分頁還要所屬頁面開放)
    var available: Bool
    /// 伺服器管理員改過(false = 用預設)
    var changed: Bool
    var id: String { key }

    enum CodingKeys: String, CodingKey {
        case key, name, description, parent, edition, experimental, experimentalNote, required, systemDefault, on, available, changed
        case defaultOn = "default"
    }

    init(key: String, name: String, description: String = "", parent: String? = nil, edition: String? = nil, experimental: Bool,
         experimentalNote: String? = nil, defaultOn: Bool, required: Bool, systemDefault: Bool, on: Bool, available: Bool, changed: Bool = false) {
        self.key = key; self.name = name; self.description = description; self.parent = parent; self.edition = edition
        self.experimental = experimental; self.experimentalNote = experimentalNote; self.defaultOn = defaultOn; self.required = required
        self.systemDefault = systemDefault; self.on = on; self.available = available; self.changed = changed
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decode(String.self, forKey: .key)
        name = (try? c.decode(String.self, forKey: .name)) ?? key
        description = (try? c.decode(String.self, forKey: .description)) ?? ""
        parent = (try? c.decode(String.self, forKey: .parent)).flatMap { $0.isEmpty ? nil : $0 }
        edition = (try? c.decode(String.self, forKey: .edition)).flatMap { $0.isEmpty ? nil : $0 }
        experimental = (try? c.decode(Bool.self, forKey: .experimental)) ?? false
        experimentalNote = try? c.decode(String.self, forKey: .experimentalNote)
        defaultOn = (try? c.decode(Bool.self, forKey: .defaultOn)) ?? true
        required = (try? c.decode(Bool.self, forKey: .required)) ?? false
        systemDefault = (try? c.decode(Bool.self, forKey: .systemDefault)) ?? true
        on = (try? c.decode(Bool.self, forKey: .on)) ?? systemDefault
        available = (try? c.decode(Bool.self, forKey: .available)) ?? on
        changed = (try? c.decode(Bool.self, forKey: .changed)) ?? false
    }
}

/// GET /api/system/features 的回應(系統層)
struct CoreServerFeatures: Codable, Equatable {
    var features: [CoreServerFeature]
    var saved: Bool
    var updatedAt: Date?
    var updatedBy: String?
    /// 目前的使用者可以改(伺服器管理員)
    var canEdit: Bool

    init(features: [CoreServerFeature], saved: Bool = false, updatedAt: Date? = nil, updatedBy: String? = nil, canEdit: Bool = false) {
        self.features = features; self.saved = saved; self.updatedAt = updatedAt; self.updatedBy = updatedBy; self.canEdit = canEdit
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        features = (try? c.decode([CoreServerFeature].self, forKey: .features)) ?? []
        saved = (try? c.decode(Bool.self, forKey: .saved)) ?? false
        updatedAt = try? c.decode(Date.self, forKey: .updatedAt)
        updatedBy = try? c.decode(String.self, forKey: .updatedBy)
        canEdit = (try? c.decode(Bool.self, forKey: .canEdit)) ?? false
    }
}

/// server 編進來的一個選用模組(GET /api/modules 的一項,見 core/module 的 Info)
struct CoreModule: Codable, Equatable, Identifiable {
    var id: String
    var name: String
    var description: String?
    /// 模組登記的功能 key
    var features: [String]

    init(id: String, name: String = "", description: String? = nil, features: [String] = []) {
        self.id = id; self.name = name; self.description = description; self.features = features
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = (try? c.decode(String.self, forKey: .name)) ?? id
        description = try? c.decode(String.self, forKey: .description)
        features = (try? c.decode([String].self, forKey: .features)) ?? []
    }
}

/// App 認得的選用模組
enum ServerModule {
    /// 程式碼倉庫(GitHub、GitLab 設定;功能 development.repos;core/modules/integrations)
    static let integrations = "integrations"
    /// App 認得的全部模組(某個模組確定沒有、但還沒讀到模組清單時,其他的當作有)
    static let known = [integrations]
}

/// 畫面用的功能開關(AppState.features):組織層加上系統層
struct FeatureFlags: Equatable {
    enum Source: Equatable {
        /// 剛從 core 讀到
        case core
        /// core 讀不到,用這次執行裡最後一份
        case cached
        /// 都沒有,用保守預設(舊版 server 的系統層也是)
        case fallback
    }
    /// 組織層(已整理過:只列系統層開放的,serverFeatures 一定有;見 normalized)
    var settings: CoreSystemSettings
    var source: Source
    /// 讀不到組織層的原因(nil = 正常)
    var error: String?
    /// 系統層(系統頁用)
    var server: CoreServerFeatures
    var serverSource: Source
    /// 讀不到系統層的原因(nil = 正常)
    var serverError: String?
    /// 清單沒列的 key 當作開(很舊的 core:沒有 serverFeatures 也沒有 edition)
    private(set) var unlistedOn: Bool
    /// server 編進來的選用模組 id(GET /api/modules);nil = 不知道(還沒讀到、舊版 server 沒有這個端點),當作全部都在
    var modules: [String]?

    init(settings: CoreSystemSettings = FeatureFallback.settings, source: Source = .fallback, error: String? = nil,
         server: CoreServerFeatures = FeatureFallback.server, serverSource: Source = .fallback, serverError: String? = nil,
         modules: [String]? = nil) {
        let n = Self.normalized(settings)
        self.settings = n.settings
        self.unlistedOn = n.unlistedOn
        self.source = source
        self.error = error
        self.server = server
        self.serverSource = serverSource
        self.serverError = serverError
        self.modules = modules
    }

    /// 換掉組織層、系統層與模組清單照舊
    func with(settings: CoreSystemSettings, source: Source, error: String?) -> FeatureFlags {
        FeatureFlags(settings: settings, source: source, error: error, server: server, serverSource: serverSource, serverError: serverError,
                     modules: modules)
    }

    /// server 有沒有編進這個選用模組(模組清單還沒讀到或舊版 server 沒有模組清單時當作有)
    func hasModule(_ id: String) -> Bool { modules?.contains(id) ?? true }

    var features: [CoreFeature] { settings.features }
    /// 組織層能不能改(組織管理員、剛從 core 讀到)
    var canEdit: Bool { source == .core && settings.canEdit }
    /// 系統層能不能改(伺服器管理員、剛從 core 讀到)
    var serverCanEdit: Bool { serverSource == .core && server.canEdit }

    /// 實際開不開:系統層沒開放的一律關;清單裡有的照 core 的 enabled(已含組織層與頁面規則);
    /// 清單裡沒有的當作沒有(關),很舊的 core 例外(當作開)
    func enabled(_ key: String) -> Bool {
        if let m = FeatureFallback.module(of: FeatureFallback.legacyKeys[key] ?? key), !hasModule(m) { return false }   // 模組沒編進來:功能不存在
        let key = currentKey(key)
        if !serverHas(key) { return false }
        if let f = settings.features.first(where: { $0.key == key }) { return f.enabled }
        return unlistedOn
    }

    /// 系統層有沒有開放(沒登記的 key 不擋)
    func serverHas(_ key: String) -> Bool { settings.serverFeatures?[currentKey(key)] ?? true }

    /// 實際要查的 key:退場的舊 key 當作新的(FeatureFallback.legacyKeys);
    /// 還沒改的舊版 core 清單裡還有舊 key 時,新 key 用舊 key 的值(例如舊版的 development.repos 是範例資料、預設關,GitHub 分頁才是真的)
    private func currentKey(_ key: String) -> String {
        let now = FeatureFallback.legacyKeys[key] ?? key
        if let old = FeatureFallback.legacyKey(of: now), settings.features.contains(where: { $0.key == old }) { return old }
        return now
    }

    /// 系統層還沒開放的功能數(設定 › 功能上方的提示)
    var serverOffCount: Int { settings.serverFeatures?.values.filter { !$0 }.count ?? 0 }

    /// 設定 › 功能:頁面自己的開關關了(必要的除外)時分頁收合不顯示(分頁的值保留,重新打開照原本的)
    func collapsed(_ page: CoreFeature) -> Bool { FeatureItems.collapsed(FeatureItem(org: page)) }

    /// 設定 › 功能要列出的分頁:收合時是空的
    func shownTabs(of page: CoreFeature) -> [CoreFeature] {
        collapsed(page) ? [] : settings.features.filter { $0.parent == page.key }
    }

    /// 頁面底下的分頁數(收合提示用)
    func tabCount(of page: CoreFeature) -> Int { settings.features.filter { $0.parent == page.key }.count }

    /// 整理組織層的回應:
    ///   - 新的 core(有 serverFeatures):core 已經只列系統層開放的;保險起見再拿掉系統層沒開放的
    ///   - 舊版 server(沒有 serverFeatures):系統層用保守預設(FeatureFallback 的 systemDefault;App 不認得的 key 當作開放),
    ///     拿掉沒開放的功能(連分頁),頁面依剩下的分頁重算(同 core 的頁面規則)
    static func normalized(_ s: CoreSystemSettings) -> (settings: CoreSystemSettings, unlistedOn: Bool) {
        if let sf = s.serverFeatures {
            var out = s
            out.features = s.features.filter { $0.system != false && sf[$0.key] != false }
            return (out, false)
        }
        var sf: [String: Bool] = [:]
        for e in FeatureFallback.entries { sf[e.key] = FeatureFallback.serverHas(e.key) }
        for f in s.features where sf[f.key] == nil { sf[f.key] = true }
        let kept = s.features.filter { sf[$0.key] != false }
        let keptKeys = Set(kept.map(\.key))
        var out = s
        out.serverFeatures = sf
        out.features = kept.compactMap { f -> CoreFeature? in
            var g = f
            g.system = true
            if let p = f.parent { return keptKeys.contains(p) ? g : nil }
            guard s.features.contains(where: { $0.parent == f.key }) else { return g }
            let kids = kept.filter { $0.parent == f.key }
            g.enabled = (f.on || f.required) && (kids.isEmpty || kids.contains(where: \.enabled))
            return g
        }
        return (out, s.edition == nil)
    }
}

// MARK: - 卡片清單的一項(系統頁與 設定 › 功能 共用)

/// 卡片上的標籤
struct FeatureTag: Equatable {
    var text: String
    var color: Color
    var icon: String?
}

/// 功能卡片清單的一項(系統層、組織層各自把自己的狀態換成這個)
struct FeatureItem: Identifiable, Equatable {
    var key: String
    var name: String
    var description: String
    var parent: String?
    var experimental: Bool
    var experimentalNote: String?
    var required: Bool
    /// 這一層自己的開關
    var on: Bool
    /// 這一層改過(false = 用預設)
    var changed: Bool
    /// 開著但實際不顯示/不開放的原因(橘色標籤)
    var warn: String?
    /// 其他標籤
    var tags: [FeatureTag] = []
    var id: String { key }

    /// 組織層(設定 › 功能)
    init(org f: CoreFeature) {
        key = f.key; name = f.name; description = f.description; parent = f.parent
        experimental = f.experimental; experimentalNote = f.experimentalNote; required = f.required
        on = f.on; changed = f.changed
        warn = f.on && !f.enabled && !f.required ? (f.parent == nil ? "分頁都關了,不顯示" : "所屬頁面關了") : nil
    }

    /// 系統層(系統頁)
    init(server f: CoreServerFeature) {
        key = f.key; name = f.name; description = f.description; parent = f.parent
        experimental = f.experimental; experimentalNote = f.experimentalNote; required = f.required
        on = f.on; changed = f.changed
        warn = f.on && !f.available && !f.required ? "所屬頁面沒開放,這台伺服器沒有開放" : nil
        if !f.on && !f.required { tags.append(FeatureTag(text: "這台伺服器沒有開放", color: .secondary, icon: "nosign")) }
        if !f.systemDefault && !f.required { tags.append(FeatureTag(text: "預設不開放", color: .indigo, icon: "hammer")) }
    }
}

/// 卡片清單的收合規則(兩層共用):頁面自己的開關關了(必要的除外)時,底下的分頁收合不顯示(值保留)
enum FeatureItems {
    static func collapsed(_ page: FeatureItem) -> Bool { !page.required && !page.on }
    static func pages(_ items: [FeatureItem]) -> [FeatureItem] { items.filter { $0.parent == nil } }
    static func tabs(_ items: [FeatureItem], of page: FeatureItem) -> [FeatureItem] { items.filter { $0.parent == page.key } }
    /// 要列出的分頁:收合時是空的
    static func shownTabs(_ items: [FeatureItem], of page: FeatureItem) -> [FeatureItem] {
        collapsed(page) ? [] : tabs(items, of: page)
    }
}

/// 保守預設(core 讀不到、也沒有讀過時;舊版 server 的系統層也用這個):
/// 系統層照 systemDefault(必要的、原本正式版的開放;實驗、家庭助理、外部 MCP 閘道等原本只有開發版的不開放),
/// 組織層照預設值(已實作的開、實驗的關)。
/// 只是備援;清單以 core 為準(core/model/features.go),測試(SystemSettingsTests)會比對兩邊一致。
enum FeatureFallback {
    struct Entry {
        let key: String
        let name: String
        let parent: String?
        let experimental: Bool
        let defaultOn: Bool
        let required: Bool
        /// 系統層沒設定過時開放不開放(同 core 的 Feature.SystemDefault);沒給時:必要的或不是實驗的開放
        var systemDefaultOverride: Bool? = nil
        /// 屬於哪個選用模組(nil = 核心功能);server 沒編進這個模組時這個功能不存在
        var module: String? = nil
        var systemDefault: Bool { systemDefaultOverride ?? (required || !experimental) }
    }

    static let entries: [Entry] = [
        .init(key: "overview", name: "總覽", parent: nil, experimental: false, defaultOn: true, required: true),
        .init(key: "development", name: "開發進度", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "development.overview", name: "概覽", parent: "development", experimental: true, defaultOn: false, required: false),
        .init(key: "development.board", name: "看板", parent: "development", experimental: false, defaultOn: true, required: false),
        .init(key: "development.issues", name: "Issue", parent: "development", experimental: false, defaultOn: true, required: false),
        .init(key: "development.pulls", name: "Pull Requests", parent: "development", experimental: true, defaultOn: false, required: false),
        // 選用模組 integrations 登記的(core/modules/integrations;核心的功能清單沒有它,模組編進來時才有):
        // 組織追蹤的 repo 與它們的 PR、Actions(真實資料)。原本的 development.github(GitHub 分頁)併進來了,見 legacyKeys
        .init(key: "development.repos", name: "Repositories", parent: "development", experimental: false, defaultOn: true, required: false,
              module: ServerModule.integrations),
        .init(key: "pipelines", name: "CI/CD", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "pipelines.runs", name: "執行紀錄", parent: "pipelines", experimental: true, defaultOn: false, required: false),
        .init(key: "pipelines.deployments", name: "部署", parent: "pipelines", experimental: true, defaultOn: false, required: false),
        .init(key: "pipelines.environments", name: "環境", parent: "pipelines", experimental: true, defaultOn: false, required: false),
        .init(key: "pipelines.voice", name: "旁白發佈", parent: "pipelines", experimental: false, defaultOn: true, required: false),
        .init(key: "infrastructure", name: "伺服器與服務", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "infrastructure.services", name: "服務", parent: "infrastructure", experimental: true, defaultOn: false, required: false),
        .init(key: "infrastructure.servers", name: "伺服器", parent: "infrastructure", experimental: true, defaultOn: false, required: false),
        .init(key: "infrastructure.map", name: "服務地圖", parent: "infrastructure", experimental: true, defaultOn: false, required: false),
        .init(key: "infrastructure.gcp", name: "GCP", parent: "infrastructure", experimental: false, defaultOn: true, required: false),
        .init(key: "monitoring", name: "監控與告警", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "monitoring.alerts", name: "告警", parent: "monitoring", experimental: true, defaultOn: false, required: false),
        .init(key: "monitoring.dashboards", name: "儀表板", parent: "monitoring", experimental: true, defaultOn: false, required: false),
        .init(key: "monitoring.logs", name: "日誌", parent: "monitoring", experimental: true, defaultOn: false, required: false),
        .init(key: "monitoring.rules", name: "規則", parent: "monitoring", experimental: true, defaultOn: false, required: false),
        .init(key: "monitoring.visitors", name: "網站訪客", parent: "monitoring", experimental: false, defaultOn: true, required: false),
        .init(key: "incidents", name: "事件", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "incidents.list", name: "事件", parent: "incidents", experimental: false, defaultOn: true, required: false),
        .init(key: "incidents.oncall", name: "值班", parent: "incidents", experimental: true, defaultOn: false, required: false),
        .init(key: "incidents.stats", name: "統計", parent: "incidents", experimental: false, defaultOn: true, required: false),
        .init(key: "automation", name: "AI 自動化", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "automation.inbox", name: "待核准", parent: "automation", experimental: true, defaultOn: false, required: false),
        .init(key: "automation.agents", name: "虛擬角色", parent: "automation", experimental: false, defaultOn: true, required: false),
        .init(key: "automation.dispatch", name: "派發", parent: "automation", experimental: false, defaultOn: true, required: false),
        .init(key: "automation.chain", name: "組織鏈", parent: "automation", experimental: false, defaultOn: true, required: false),
        .init(key: "automation.runner", name: "自動執行", parent: "automation", experimental: false, defaultOn: true, required: false),
        .init(key: "automation.facebook", name: "粉絲專頁", parent: "automation", experimental: false, defaultOn: true, required: false),
        .init(key: "automation.flows", name: "自動化流程", parent: "automation", experimental: true, defaultOn: false, required: false),
        .init(key: "automation.runs", name: "執行紀錄", parent: "automation", experimental: true, defaultOn: false, required: false),
        .init(key: "automation.insights", name: "成效", parent: "automation", experimental: true, defaultOn: false, required: false),
        .init(key: "help", name: "需要協助", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "channels", name: "對話", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "cabinet", name: "文件櫃", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "familyAssistant", name: "家庭助理", parent: nil, experimental: true, defaultOn: false, required: false),
        .init(key: "mcpGateway", name: "外部 MCP 閘道", parent: nil, experimental: true, defaultOn: false, required: false),
        .init(key: "settings", name: "設定", parent: nil, experimental: false, defaultOn: true, required: true),
        .init(key: "system", name: "系統", parent: nil, experimental: false, defaultOn: true, required: true),
    ]

    static func entry(_ key: String) -> Entry? { entries.first { $0.key == key } }

    /// 退場的功能 key → 現在的 key(同 core:讀到舊值當作新的)。
    /// development.github(獨立的 GitHub 分頁)併進 development.repos(Repositories 分頁)
    static let legacyKeys = ["development.github": "development.repos"]

    /// 現在的 key 對應的舊 key(還沒改的舊版 core 只列舊 key 時用它的值)
    static func legacyKey(of key: String) -> String? { legacyKeys.first { $0.value == key }?.key }

    /// 這個功能屬於哪個選用模組(nil = 核心功能或 App 不認得)
    static func module(of key: String) -> String? { entry(key)?.module }

    /// 依模組清單過濾(modules = nil 時全部;模組沒編進來的功能拿掉)
    static func entries(modules: [String]?) -> [Entry] {
        entries.filter { e in e.module.map { m in modules?.contains(m) ?? true } ?? true }
    }

    /// 系統層這個功能自己開放不開放(保守預設)
    static func serverOwn(_ key: String) -> Bool { entry(key)?.systemDefault ?? true }

    /// 系統層有沒有開放(保守預設;分頁還要所屬頁面開放;App 不認得的 key 當作開放)
    static func serverHas(_ key: String) -> Bool {
        guard let e = entry(key) else { return true }
        return serverOwn(key) && (e.parent.map(serverOwn) ?? true)
    }

    /// 組織層(只列系統層開放的;全部模組都在)
    static var settings: CoreSystemSettings { settings(modules: nil) }

    /// 組織層(只列系統層開放的;modules 是 server 編進來的模組,nil = 全部都在)
    static func settings(modules: [String]?) -> CoreSystemSettings {
        func own(_ e: Entry) -> Bool { e.required || e.defaultOn }
        let all = entries(modules: modules)
        let avail = all.filter { serverHas($0.key) }
        let list = avail.map { e -> CoreFeature in
            var enabled = own(e)
            if let p = e.parent, let pe = avail.first(where: { $0.key == p }) {
                enabled = enabled && own(pe)
            } else if enabled {
                let kids = avail.filter { $0.parent == e.key }
                if !kids.isEmpty { enabled = kids.contains(where: own) }
            }
            return CoreFeature(key: e.key, name: e.name, parent: e.parent, edition: e.systemDefault ? "stable" : "dev", experimental: e.experimental,
                               experimentalNote: e.experimental ? "目前是範例資料" : nil,
                               defaultOn: e.defaultOn, required: e.required, enabled: enabled, on: own(e), system: true)
        }
        var sf: [String: Bool] = [:]
        for e in all { sf[e.key] = serverHas(e.key) }
        return CoreSystemSettings(features: list, serverFeatures: sf)
    }

    /// 系統層(全部登記的功能;不能改;全部模組都在)
    static var server: CoreServerFeatures { server(modules: nil) }

    /// 系統層(登記的功能;modules 是 server 編進來的模組,nil = 全部都在)
    static func server(modules: [String]?) -> CoreServerFeatures {
        CoreServerFeatures(features: entries(modules: modules).map { e in
            CoreServerFeature(key: e.key, name: e.name, parent: e.parent, edition: e.systemDefault ? "stable" : "dev", experimental: e.experimental,
                              experimentalNote: e.experimental ? "目前是範例資料" : nil, defaultOn: e.defaultOn, required: e.required,
                              systemDefault: e.systemDefault, on: e.systemDefault, available: serverHas(e.key))
        })
    }
}

// MARK: - core 用戶端

extension IssueRemote {
    private func decodeSystem(_ data: Data) throws -> CoreSystemSettings {
        do { return try Self.decoder().decode(CoreSystemSettings.self, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的功能設定格式看不懂:\(error.localizedDescription)")
        }
    }

    private func decodeServerFeatures(_ data: Data) throws -> CoreServerFeatures {
        do { return try Self.decoder().decode(CoreServerFeatures.self, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的系統層功能設定格式看不懂:\(error.localizedDescription)")
        }
    }

    private func featuresBody(_ features: [String: Bool], _ reset: [String]) throws -> Data {
        var b: [String: Any] = [:]
        if !features.isEmpty { b["features"] = features }
        if !reset.isEmpty { b["reset"] = reset }
        return try json(b)
    }

    /// GET /api/orgs/<組織>/system:組織層(只列系統層開放的功能)與這個組織目前的開關
    func systemSettings(org: String) throws -> CoreSystemSettings {
        try decodeSystem(try send("GET", orgURL(org, ["system"]), nil))
    }

    /// POST /api/orgs/<組織>/system:改組織層的開關(要組織管理員);reset 是要回到預設的 key
    func saveSystemSettings(org: String, features: [String: Bool], reset: [String] = []) throws -> CoreSystemSettings {
        try decodeSystem(try send("POST", orgURL(org, ["system"]), try featuresBody(features, reset)))
    }

    private var serverFeaturesURL: URL { config.url.appendingPathComponent("api/system/features") }

    /// GET /api/system/features:系統層(這台伺服器開放哪些功能;任何登入者)
    func serverFeatures() throws -> CoreServerFeatures {
        try decodeServerFeatures(try send("GET", serverFeaturesURL, nil))
    }

    /// GET /api/modules:這台 server 編進來的選用模組。舊版 server 沒有這個端點(404)時回 nil(當作全部模組都在)
    func modules() throws -> [CoreModule]? {
        do {
            let data = try send("GET", config.url.appendingPathComponent("api/modules"), nil)
            do { return try Self.decoder().decode([CoreModule].self, from: data) } catch {
                throw IssueRemoteError(message: "omixor 回應的模組清單格式看不懂:\(error.localizedDescription)")
            }
        } catch let e as IssueRemoteError where e.status == 404 {
            return nil
        }
    }

    /// POST /api/system/features:改系統層(要伺服器管理員)
    func saveServerFeatures(features: [String: Bool], reset: [String] = []) throws -> CoreServerFeatures {
        try decodeServerFeatures(try send("POST", serverFeaturesURL, try featuresBody(features, reset)))
    }
}

// MARK: - 讀寫

@MainActor
final class SystemStore {
    unowned let app: AppState
    private var remoteClient: IssueRemote?
    /// 這次執行裡 core 回傳過的最後一份組織層(server 網址 + 組織 → 設定;只在記憶體)
    private var lastGood: [String: CoreSystemSettings] = [:]
    /// 這次執行裡 core 回傳過的最後一份系統層(server 網址 → 設定;只在記憶體)
    private var lastGoodServer: [String: CoreServerFeatures] = [:]
    /// 這次執行裡讀到的模組清單(server 網址 → 模組 id;nil = 舊版 server 沒有模組清單;只在記憶體)
    private var lastModules: [String: [String]?] = [:]

    init(app: AppState) { self.app = app }

    var remote: IssueRemote {
        let cfg = IssueServerConfig.current
        if let c = remoteClient, c.matches(cfg) { return c }   // 連線設定與組織的 token 範圍都相同才沿用
        let c = IssueRemote(config: cfg)
        remoteClient = c
        return c
    }

    private var serverKey: String { IssueServerConfig.current.url.absoluteString }
    private var cacheKey: String { "\(serverKey)|\(app.currentOrgId)" }

    /// 從 core 讀兩層與模組清單;讀不到時用最後一份或保守預設
    func load() {
        let org = app.currentOrgId, r = remote
        apply(org, cacheKey, serverKey, Result { try r.systemSettings(org: org) }, Result { try r.serverFeatures() }, Result { try r.modules() })
    }

    /// 同 load,但請求在背景跑,回主執行緒才更新畫面(切換組織、按「重新讀取」用;不擋主執行緒)
    func reload() async {
        let org = app.currentOrgId, key = cacheKey, sk = serverKey, r = remote
        async let a: Result<CoreSystemSettings, Error> = { do { return .success(try await r.run { try $0.systemSettings(org: org) }) } catch { return .failure(error) } }()
        async let b: Result<CoreServerFeatures, Error> = { do { return .success(try await r.run { try $0.serverFeatures() }) } catch { return .failure(error) } }()
        async let c: Result<[CoreModule]?, Error> = { do { return .success(try await r.run { try $0.modules() }) } catch { return .failure(error) } }()
        let (x, y, z) = await (a, b, c)
        apply(org, key, sk, x, y, z)
    }

    /// 這台 server 確定沒有某個選用模組(例如打 /integrations/github 收到 404):之後當作沒有,直到重新讀到模組清單
    func moduleMissing(_ id: String) {
        let sk = serverKey
        let now = (lastModules[sk] ?? nil) ?? app.features.modules ?? ServerModule.known
        let next = now.filter { $0 != id }
        lastModules[sk] = .some(next)
        if app.features.modules != next { app.features.modules = next }
    }

    private func apply(_ org: String, _ key: String, _ sk: String, _ res: Result<CoreSystemSettings, Error>, _ srv: Result<CoreServerFeatures, Error>,
                       _ mods: Result<[CoreModule]?, Error>) {
        guard app.currentOrgId == org else { return }
        // 模組清單:讀到就照讀到的;舊版 server(404)當作全部都在,除非這次執行已經確定少了哪些;讀不到用最後一份
        let modules: [String]?
        switch mods {
        case .success(.some(let list)):
            modules = list.map(\.id)
            lastModules[sk] = .some(modules)
        case .success(.none):
            modules = lastModules[sk] ?? nil
        case .failure(let e):
            NSLog("模組清單(omixor server):%@", e.localizedDescription)
            modules = lastModules[sk] ?? nil
        }
        let settings: CoreSystemSettings, source: FeatureFlags.Source, error: String?
        switch res {
        case .success(let x):
            lastGood[key] = x
            (settings, source, error) = (x, .core, nil)
        case .failure(let e):
            let msg = e.localizedDescription
            NSLog("功能設定(組織層,omixor server):%@", msg)
            if let x = lastGood[key] { (settings, source, error) = (x, .cached, msg) } else { (settings, source, error) = (FeatureFallback.settings(modules: modules), .fallback, msg) }
        }
        let server: CoreServerFeatures, serverSource: FeatureFlags.Source, serverError: String?
        switch srv {
        case .success(let x):
            lastGoodServer[sk] = x
            (server, serverSource, serverError) = (x, .core, nil)
        case .failure(let e):
            var msg = e.localizedDescription
            if (e as? IssueRemoteError)?.status == 404 { msg = "這台 omixor server 還沒有系統層的功能設定(可能是舊版,請更新 server):\(msg)" }
            NSLog("功能設定(系統層,omixor server):%@", msg)
            if let x = lastGoodServer[sk] { (server, serverSource, serverError) = (x, .cached, msg) } else { (server, serverSource, serverError) = (FeatureFallback.server(modules: modules), .fallback, msg) }
        }
        app.features = FeatureFlags(settings: settings, source: source, error: error, server: server, serverSource: serverSource, serverError: serverError,
                                    modules: modules)
    }

    /// 改組織層的開關(組織管理員);成功後套用 core 回傳的(系統層照舊)
    func set(_ changes: [String: Bool], reset: [String] = []) throws {
        let org = app.currentOrgId
        let x = try remote.saveSystemSettings(org: org, features: changes, reset: reset)
        lastGood[cacheKey] = x
        app.features = app.features.with(settings: x, source: .core, error: nil)
    }

    /// 改系統層的開關(伺服器管理員);成功後重讀兩層(組織層的清單跟著系統層變)
    func setServer(_ changes: [String: Bool], reset: [String] = []) throws {
        let x = try remote.saveServerFeatures(features: changes, reset: reset)
        lastGoodServer[serverKey] = x
        load()
    }
}

// MARK: - 查詢(側邊欄、快速鍵、命令選單、AI 助理、跳轉都用這些)

/// 頁面裡的分頁:對應功能清單的「頁面.分頁」(featureKey 可以覆寫)
protocol FeatureTab: RawRepresentable, CaseIterable, Hashable where RawValue == String, AllCases: RandomAccessCollection {
    static var section: AppSection { get }
    var featureKey: String { get }
    var featureTitle: String { get }
}

extension FeatureTab {
    var featureKey: String { "\(Self.section.rawValue).\(rawValue)" }
}

extension AppState {
    /// 功能開不開(系統層 && 組織層;key 見 core/model/features.go)
    func isFeatureEnabled(_ key: String) -> Bool { features.enabled(key) }

    /// 頁面顯示不顯示(總覽、設定、系統永遠顯示)
    func isVisible(_ section: AppSection) -> Bool {
        if section.alwaysOn { return true }
        return isFeatureEnabled(section.rawValue)
    }

    /// 側邊欄、快速鍵、命令選單用:開著的頁面
    var visibleSections: [AppSection] { AppSection.allCases.filter(isVisible) }

    /// 頁面裡開著的分頁(依原本順序)
    func visibleTabs<T: FeatureTab>(_ type: T.Type) -> [T] {
        T.allCases.filter { isFeatureEnabled($0.featureKey) }
    }

    /// 實際顯示的分頁:選的分頁關了就用第一個開著的
    func shownTab<T: FeatureTab>(_ tab: T) -> T {
        if isFeatureEnabled(tab.featureKey) { return tab }
        return visibleTabs(T.self).first ?? tab
    }

    /// 分頁被切到關掉的(例如從別頁跳過來):提示並回傳要改顯示的分頁
    func redirectTab<T: FeatureTab>(_ tab: T) -> T? {
        guard !isFeatureEnabled(tab.featureKey) else { return nil }
        noteFeatureOff(tab.featureKey, name: tab.featureTitle)
        return visibleTabs(T.self).first
    }

    /// 功能關著的原因(提示文案):系統層沒開放 → 指向「系統」;組織層關了 → 指向「設定 › 功能」
    func featureOffText(_ key: String, name: String) -> String {
        features.serverHas(key) ? "「\(name)」在 設定 › 功能 關閉了" : "這台伺服器沒有開放「\(name)」(伺服器管理員可以到「系統」開放)"
    }

    /// 顯示功能關著的提示(畫面上方;FeatureNoticeBanner 依 featureNoticeKey 決定「前往」哪一頁)
    func noteFeatureOff(_ key: String, name: String, suffix: String = "") {
        featureNoticeKey = key
        featureNotice = featureOffText(key, name: name) + suffix
    }

    /// server 有沒有編進這個選用模組(見 FeatureFlags.hasModule)
    func hasModule(_ id: String) -> Bool { features.hasModule(id) }

    /// 這台 server 確定沒有某個選用模組(例如打 /integrations/… 收到 404):之後當作沒有(畫面隱藏,不顯示錯誤)
    func noteModuleMissing(_ id: String) {
        if let s = systemStore { s.moduleMissing(id); return }
        let next = (features.modules ?? ServerModule.known).filter { $0 != id }
        if features.modules != next { features.modules = next }
    }

    @discardableResult
    func startSystem() -> SystemStore {
        if let s = systemStore { return s }
        let s = SystemStore(app: self)
        systemStore = s
        s.load()
        return s
    }

    /// 功能開關換了:目前所在頁被關掉就切回總覽
    func featuresChanged() {
        syncLiveUpdates()
        if !isVisible(section) {
            noteFeatureOff(section.rawValue, name: section.title, suffix: ",已切回總覽")
            section = .overview
            focusId = nil
        }
    }
}
