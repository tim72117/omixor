import Foundation
import SwiftUI

// 系統設定(功能開關):功能清單與每個組織的開關存在 core(core/model/features.go 是唯一的清單,
// GET /api/orgs/<組織>/system 回傳清單與目前值;成員讀、組織管理員改,見 core/service/system.go)。
// 讀取一律透過目前連線的 core server(IssueServerConfig.current,本機模式是本機守護服務)。
//   - 讀得到:照 core 的
//   - 讀不到:用這次執行裡 core 回傳過的最後一份(只在記憶體)
//   - 都沒有:用保守預設(已實作的開、實驗的關;FeatureFallback),系統頁顯示讀不到的原因
//
// 判斷某個功能開不開:app.isFeatureEnabled("key")(頁面用 app.isVisible(section)、分頁用 app.visibleTabs)。
//
// 版本(編譯時決定有哪些功能,見 core/model/edition.go):
//   - server 端:core 依自己的編譯版本(go build -tags dev 才是開發版)過濾,/system 只列它有的功能,回應的 edition 是它的版本
//   - App 端:AppEdition(編譯條件 OMIXOR_DEV;scripts/build-app.sh 依 OMIXOR_EDITION 決定)決定「App 這邊有沒有這功能的畫面」
//   兩邊都有、而且組織開著才顯示(FeatureFlags 建立時先用 App 的版本過濾清單)。

/// 這個 App 編譯的版本:開發版用 -Xswiftc -DOMIXOR_DEV 編譯(OMIXOR_EDITION=dev bash scripts/build-app.sh),沒給是正式版
enum AppEdition {
    static let stable = "stable"
    static let dev = "dev"
    #if OMIXOR_DEV
    static let current = dev
    #else
    static let current = stable
    #endif
    private static let rank = [stable: 0, dev: 1]

    /// 版本 build 有沒有包含屬於 e 的功能(大的版本包含小的;不認得的一律不包含;同 core 的 EditionIncludes)
    static func includes(_ e: String, in build: String = current) -> Bool {
        guard let b = rank[build], let x = rank[e] else { return false }
        return x <= b
    }

    /// App 這邊有沒有這個功能的畫面:備援清單登記過的照它的版本;沒登記的(App 不認得)不擋,交給 server 的清單決定
    static func hasFeature(_ key: String) -> Bool {
        guard let e = FeatureFallback.entries.first(where: { $0.key == key }) else { return true }
        return includes(e.edition)
    }

    static func label(_ e: String) -> String {
        switch e {
        case stable: return "正式版"
        case dev: return "開發版"
        default: return e
        }
    }
}

/// core 回傳的一個功能
struct CoreFeature: Codable, Equatable, Identifiable {
    var key: String
    var name: String
    var description: String
    /// 所屬頁面(nil = 頁面本身)
    var parent: String?
    /// 屬於哪個版本(stable / dev;nil = 比較舊的 core 沒給)
    var edition: String?
    var experimental: Bool
    var experimentalNote: String?
    var defaultOn: Bool
    var required: Bool
    /// 實際開不開(畫面照這個顯示)
    var enabled: Bool
    /// 這個功能自己的開關(系統頁的開關照這個)
    var on: Bool
    /// 組織改過(false = 用預設)
    var changed: Bool
    var id: String { key }

    enum CodingKeys: String, CodingKey {
        case key, name, description, parent, edition, experimental, experimentalNote, required, enabled, on, changed
        case defaultOn = "default"
    }

    init(key: String, name: String, description: String = "", parent: String? = nil, edition: String? = nil, experimental: Bool, experimentalNote: String? = nil,
         defaultOn: Bool, required: Bool, enabled: Bool, on: Bool, changed: Bool = false) {
        self.key = key; self.name = name; self.description = description; self.parent = parent; self.edition = edition
        self.experimental = experimental; self.experimentalNote = experimentalNote; self.defaultOn = defaultOn
        self.required = required; self.enabled = enabled; self.on = on; self.changed = changed
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
    }
}

/// GET /api/orgs/<組織>/system 的回應
struct CoreSystemSettings: Codable, Equatable {
    var features: [CoreFeature]
    var saved: Bool
    var updatedAt: Date?
    var updatedBy: String?
    /// server 編譯的版本(stable / dev;nil = 比較舊的 core 沒給,或是 App 的備援清單)
    var edition: String?
    var canEdit: Bool

    init(features: [CoreFeature], saved: Bool = false, updatedAt: Date? = nil, updatedBy: String? = nil, edition: String? = nil, canEdit: Bool = false) {
        self.features = features; self.saved = saved; self.updatedAt = updatedAt; self.updatedBy = updatedBy
        self.edition = edition; self.canEdit = canEdit
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        features = (try? c.decode([CoreFeature].self, forKey: .features)) ?? []
        saved = (try? c.decode(Bool.self, forKey: .saved)) ?? false
        updatedAt = try? c.decode(Date.self, forKey: .updatedAt)
        updatedBy = try? c.decode(String.self, forKey: .updatedBy)
        edition = (try? c.decode(String.self, forKey: .edition)).flatMap { $0.isEmpty ? nil : $0 }
        canEdit = (try? c.decode(Bool.self, forKey: .canEdit)) ?? false
    }
}

/// 畫面用的功能開關(AppState.features)
struct FeatureFlags: Equatable {
    enum Source: Equatable {
        /// 剛從 core 讀到
        case core
        /// core 讀不到,用這次執行裡最後一份
        case cached
        /// 都沒有,用保守預設
        case fallback
    }
    /// 已經用 App 的版本過濾過的設定(見 forApp)
    var settings: CoreSystemSettings
    var source: Source
    /// 讀不到 core 的原因(nil = 正常)
    var error: String?

    init(settings: CoreSystemSettings = FeatureFallback.settings, source: Source = .fallback, error: String? = nil) {
        self.settings = Self.forApp(settings)
        self.source = source
        self.error = error
    }

    var features: [CoreFeature] { settings.features }
    var canEdit: Bool { source == .core && settings.canEdit }

    /// 實際開不開:App 這個版本沒有的一律關;清單裡有的照 core;清單裡沒有的,
    /// core 有回版本(新的 core:沒列 = 那台 server 沒編進來)當作關,沒回版本(比較舊的 core)當作開
    func enabled(_ key: String) -> Bool {
        guard AppEdition.hasFeature(key) else { return false }
        if let f = settings.features.first(where: { $0.key == key }) { return f.enabled }
        return settings.edition == nil
    }

    /// 系統頁:頁面自己的開關關了(必要的除外)時分頁收合不顯示(分頁的值保留,重新打開照原本的)
    func collapsed(_ page: CoreFeature) -> Bool { !page.required && !page.on }

    /// 系統頁要列出的分頁:收合時是空的
    func shownTabs(of page: CoreFeature) -> [CoreFeature] {
        collapsed(page) ? [] : settings.features.filter { $0.parent == page.key }
    }

    /// 頁面底下的分頁數(收合提示用)
    func tabCount(of page: CoreFeature) -> Int { settings.features.filter { $0.parent == page.key }.count }

    /// 用 App 的版本過濾 core 的清單:App 沒有畫面的功能拿掉(連分頁);頁面的分頁被拿掉後重算頁面開不開
    static func forApp(_ s: CoreSystemSettings) -> CoreSystemSettings {
        func keep(_ f: CoreFeature) -> Bool { AppEdition.hasFeature(f.key) && (f.edition.map { AppEdition.includes($0) } ?? true) }
        let kept = s.features.filter(keep)
        let keptKeys = Set(kept.map(\.key))
        var out = s
        out.features = kept.compactMap { f -> CoreFeature? in
            if let p = f.parent { return keptKeys.contains(p) ? f : nil }
            let allKids = s.features.filter { $0.parent == f.key }
            guard !allKids.isEmpty else { return f }
            var g = f
            let kids = kept.filter { $0.parent == f.key }
            g.enabled = f.enabled && kids.contains(where: \.enabled)
            return g
        }
        return out
    }
}

/// 保守預設(core 讀不到、也沒有讀過時):已實作的開、實驗的關。
/// 只是備援;清單以 core 為準(core/model/features.go),測試(SystemSettingsTests)會比對兩邊一致。
/// entries 是 App 認得的全部功能(含版本);settings 只列 App 這個版本有的(AppEdition)。
enum FeatureFallback {
    struct Entry {
        let key: String
        let name: String
        let parent: String?
        let experimental: Bool
        let defaultOn: Bool
        let required: Bool
        /// 屬於哪個版本(同 core 的 Feature.Edition);沒給時實驗功能是開發版、其他是正式版
        var editionOverride: String? = nil
        var edition: String { editionOverride ?? (experimental ? AppEdition.dev : AppEdition.stable) }
    }

    static let entries: [Entry] = [
        .init(key: "overview", name: "總覽", parent: nil, experimental: false, defaultOn: true, required: true),
        .init(key: "development", name: "開發進度", parent: nil, experimental: false, defaultOn: true, required: false),
        .init(key: "development.overview", name: "概覽", parent: "development", experimental: true, defaultOn: false, required: false),
        .init(key: "development.board", name: "Sprint 看板", parent: "development", experimental: true, defaultOn: false, required: false),
        .init(key: "development.issues", name: "Issue", parent: "development", experimental: false, defaultOn: true, required: false),
        .init(key: "development.pulls", name: "Pull Requests", parent: "development", experimental: true, defaultOn: false, required: false),
        .init(key: "development.repos", name: "Repositories", parent: "development", experimental: true, defaultOn: false, required: false),
        .init(key: "development.github", name: "GitHub", parent: "development", experimental: false, defaultOn: true, required: false),
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

    static var settings: CoreSystemSettings {
        func own(_ e: Entry) -> Bool { e.required || e.defaultOn }
        let entries = entries.filter { AppEdition.includes($0.edition) }
        let list = entries.map { e -> CoreFeature in
            var enabled = own(e)
            if let p = e.parent, let pe = entries.first(where: { $0.key == p }) {
                enabled = enabled && own(pe)
            } else if enabled {
                let kids = entries.filter { $0.parent == e.key }
                if !kids.isEmpty { enabled = kids.contains(where: own) }
            }
            return CoreFeature(key: e.key, name: e.name, parent: e.parent, edition: e.edition, experimental: e.experimental,
                               experimentalNote: e.experimental ? "目前是範例資料" : nil,
                               defaultOn: e.defaultOn, required: e.required, enabled: enabled, on: own(e))
        }
        return CoreSystemSettings(features: list)
    }
}

// MARK: - core 用戶端

extension IssueRemote {
    private func decodeSystem(_ data: Data) throws -> CoreSystemSettings {
        do { return try Self.decoder().decode(CoreSystemSettings.self, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的系統設定格式看不懂:\(error.localizedDescription)")
        }
    }

    /// GET /system:功能清單與這個組織目前的開關
    func systemSettings(org: String) throws -> CoreSystemSettings {
        try decodeSystem(try send("GET", orgURL(org, ["system"]), nil))
    }

    /// POST /system:改功能開關(要組織管理員);reset 是要回到預設的 key
    func saveSystemSettings(org: String, features: [String: Bool], reset: [String] = []) throws -> CoreSystemSettings {
        var b: [String: Any] = [:]
        if !features.isEmpty { b["features"] = features }
        if !reset.isEmpty { b["reset"] = reset }
        return try decodeSystem(try send("POST", orgURL(org, ["system"]), try json(b)))
    }
}

// MARK: - 讀寫

@MainActor
final class SystemStore {
    unowned let app: AppState
    private var remoteClient: IssueRemote?
    /// 這次執行裡 core 回傳過的最後一份(server 網址 + 組織 → 設定;只在記憶體)
    private var lastGood: [String: CoreSystemSettings] = [:]

    init(app: AppState) { self.app = app }

    var remote: IssueRemote {
        let cfg = IssueServerConfig.current
        if let c = remoteClient, c.config == cfg { return c }
        let c = IssueRemote(config: cfg)
        remoteClient = c
        return c
    }

    private var cacheKey: String { "\(IssueServerConfig.current.url.absoluteString)|\(app.currentOrgId)" }

    /// 從 core 讀目前組織的設定;讀不到時用最後一份或保守預設
    func load() {
        let org = app.currentOrgId
        apply(org, cacheKey, Result { try remote.systemSettings(org: org) })
    }

    /// 同 load,但請求在背景跑,回主執行緒才更新畫面(切換組織、按「重新讀取」用;不擋主執行緒)
    func reload() async {
        let org = app.currentOrgId, key = cacheKey, r = remote
        let res: Result<CoreSystemSettings, Error>
        do { res = .success(try await r.run { try $0.systemSettings(org: org) }) } catch { res = .failure(error) }
        apply(org, key, res)
    }

    private func apply(_ org: String, _ key: String, _ res: Result<CoreSystemSettings, Error>) {
        switch res {
        case .success(let x):
            guard app.currentOrgId == org else { return }
            lastGood[key] = x
            app.features = FeatureFlags(settings: x, source: .core, error: nil)
        case .failure(let error):
            guard app.currentOrgId == org else { return }
            let msg = error.localizedDescription
            NSLog("系統設定(omixor server):%@", msg)
            if let x = lastGood[key] {
                app.features = FeatureFlags(settings: x, source: .cached, error: msg)
            } else {
                app.features = FeatureFlags(settings: FeatureFallback.settings, source: .fallback, error: msg)
            }
        }
    }

    /// 改功能開關(組織管理員);成功後套用 core 回傳的
    func set(_ changes: [String: Bool], reset: [String] = []) throws {
        let org = app.currentOrgId
        let x = try remote.saveSystemSettings(org: org, features: changes, reset: reset)
        lastGood[cacheKey] = x
        app.features = FeatureFlags(settings: x, source: .core, error: nil)
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
    /// 功能開不開(key 見 core/model/features.go;沒登記的 key 當作開)
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
        featureNotice = "「\(tab.featureTitle)」在系統設定裡關閉了"
        return visibleTabs(T.self).first
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
        if !isVisible(section) {
            featureNotice = "「\(section.title)」在系統設定裡關閉了,已切回總覽"
            section = .overview
            focusId = nil
        }
    }
}
