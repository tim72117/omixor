import XCTest
@testable import Ops

/// 系統設定(功能開關):清單與開關在 core(暫存的本機守護服務);App 照 core 的顯示、讀不到時用最後一份或保守預設
@MainActor
final class SystemSettingsTests: XCTestCase {
    private var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-system-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func feature(_ app: AppState, _ key: String) -> CoreFeature? { app.features.features.first { $0.key == key } }

    /// 備援清單要和 core 的一致(清單以 core/model/features.go 為準)
    func testFallbackMatchesCore() throws {
        let cfg = try TestCore.start(self)
        let core = try IssueRemote(config: cfg).systemSettings(org: OrgFiles.defaultId)
        let fb = FeatureFallback.settings
        XCTAssertEqual(core.edition, AppEdition.current, "測試的守護服務和 App 用相同的版本編譯")
        XCTAssertEqual(core.features.map(\.key), fb.features.map(\.key), "FeatureFallback.entries 要照 core/model/features.go 更新")
        for (c, f) in zip(core.features, fb.features) {
            XCTAssertEqual(c.edition, f.edition, c.key)
            XCTAssertEqual(c.parent, f.parent, c.key)
            XCTAssertEqual(c.experimental, f.experimental, c.key)
            XCTAssertEqual(c.defaultOn, f.defaultOn, c.key)
            XCTAssertEqual(c.required, f.required, c.key)
            XCTAssertEqual(c.name, f.name, c.key)
            XCTAssertEqual(c.enabled, f.enabled, "沒設定過時 core 的 enabled 和備援算的一樣:\(c.key)")
        }
        // 每個 App 的頁面與分頁都在清單裡
        let keys = Set(core.features.map(\.key))
        let all = Set(FeatureFallback.entries.map(\.key))
        for s in AppSection.allCases {
            XCTAssertTrue(all.contains(s.rawValue), s.rawValue)
            if AppEdition.hasFeature(s.rawValue) { XCTAssertTrue(keys.contains(s.rawValue), s.rawValue) }
        }
        for k in DevTab.allCases.map(\.featureKey) + CICDTab.allCases.map(\.featureKey) + InfraTab.allCases.map(\.featureKey)
            + MonTab.allCases.map(\.featureKey) + IncidentsView.IncTab.allCases.map(\.featureKey) + AutoTab.allCases.map(\.featureKey) {
            XCTAssertTrue(all.contains(k), k)
            if AppEdition.hasFeature(k) { XCTAssertTrue(keys.contains(k), k) }
        }
    }

    /// 從 core 讀、改;關掉的頁面不能跳、所在頁被關掉切回總覽;關掉的分頁不列
    func testLoadAndToggle() throws {
        try TestCore.start(self)
        let app = AppState()
        let store = app.startSystem()
        XCTAssertEqual(app.features.source, .core)
        XCTAssertNil(app.features.error)
        XCTAssertTrue(app.features.canEdit, "本機模式是管理員")
        XCTAssertTrue(app.isVisible(.channels))
        XCTAssertFalse(app.isFeatureEnabled("monitoring.alerts"), "實驗的預設關")
        XCTAssertEqual(app.visibleTabs(MonTab.self), [.visitors])
        XCTAssertEqual(app.shownTab(MonTab.alerts), .visitors)
        XCTAssertFalse(app.isFeatureEnabled("familyAssistant"))
        #if OMIXOR_DEV
        XCTAssertTrue(feature(app, "familyAssistant")?.experimental == true)
        XCTAssertEqual(feature(app, "familyAssistant")?.edition, "dev")
        #else
        XCTAssertNil(feature(app, "familyAssistant"), "正式版不列家庭助理")
        #endif

        XCTAssertTrue(app.go(.channels))
        XCTAssertEqual(app.section, .channels)
        try store.set(["channels": false])
        XCTAssertFalse(app.isVisible(.channels))
        XCTAssertEqual(app.section, .overview, "所在頁被關掉切回總覽")
        XCTAssertNotNil(app.featureNotice)
        XCTAssertFalse(app.go(.channels), "關掉的頁面不能跳")
        XCTAssertEqual(app.section, .overview)
        XCTAssertNil(app.badge(for: .channels))
        XCTAssertFalse(app.visibleSections.contains(.channels))
        XCTAssertTrue(app.visibleSections.contains(.system) && app.visibleSections.contains(.settings))

        #if OMIXOR_DEV
        try store.set(["monitoring.alerts": true])
        XCTAssertEqual(app.visibleTabs(MonTab.self), [.alerts, .visitors])
        try store.set(["monitoring.alerts": false])
        #else
        XCTAssertNil(feature(app, "monitoring.alerts"), "正式版不列開發版的功能")
        XCTAssertThrowsError(try store.set(["monitoring.alerts": true]), "正式版開不了開發版的功能")
        #endif
        // 分頁全關,頁面也不顯示
        try store.set(["monitoring.visitors": false])
        XCTAssertFalse(app.isVisible(.monitoring))
        // 改回預設
        try store.set([:], reset: ["monitoring.visitors", "channels"])
        XCTAssertTrue(app.isVisible(.monitoring) && app.isVisible(.channels))

        // 必要的不能關
        XCTAssertThrowsError(try store.set(["system": false]))
        // 換一個 AppState 讀回 core 存的
        try store.set(["cabinet": false])
        let app2 = AppState()
        app2.startSystem()
        XCTAssertFalse(app2.isVisible(.cabinet))

        // AI 助理:關掉的功能不建議、問到時說明
        app2.section = .overview
        let r = AssistResponder(app: app2)
        XCTAssertFalse(r.suggestions().contains("誰在值班?"))
        XCTAssertTrue(r.reply(to: "誰在值班?").text.contains("關閉"))
    }

    /// 讀不到 core:有讀過用最後一份(記憶體);都沒有用保守預設;都顯示原因
    func testUnreachable() throws {
        try TestCore.start(self)
        let app = AppState()
        let store = app.startSystem()
        try store.set(["channels": false, "pipelines.voice": false])
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:9")!, token: nil, unavailable: "本機服務還沒啟動")
        store.load()
        XCTAssertEqual(app.features.source, .fallback, "換了 server(網址不同)沒有讀過的")
        XCTAssertNotNil(app.features.error)
        XCTAssertFalse(app.features.canEdit)
        XCTAssertTrue(app.isVisible(.channels))
        XCTAssertFalse(app.isFeatureEnabled("pipelines.runs"))
        XCTAssertTrue(app.isFeatureEnabled("pipelines.voice"), "保守預設,不是剛剛改的")
        XCTAssertTrue(app.isVisible(.settings) && app.isVisible(.system) && app.isVisible(.overview))
    }

    /// 同一台 server 暫時讀不到:用這次執行最後一份
    func testCachedWhenServerDown() throws {
        let cfg = try TestCore.start(self)
        let app = AppState()
        let store = app.startSystem()
        try store.set(["channels": false])
        // 同一個網址、但連不上(unavailable)
        IssueServerConfig.testOverride = IssueServerConfig(url: cfg.url, token: cfg.token, unavailable: "連不到")
        store.load()
        XCTAssertEqual(app.features.source, .cached)
        XCTAssertEqual(app.features.error, "連不到")
        XCTAssertFalse(app.isVisible(.channels))
        XCTAssertFalse(app.features.canEdit, "讀不到時不能改")
    }

    /// 兩層合併:App 這個版本沒有的拿掉(連分頁,頁面重算);core 有回版本時,清單沒列的當作沒有;舊的 core 沒回版本時當作開
    func testEditionMerge() {
        func f(_ key: String, _ parent: String? = nil, _ ed: String? = "stable", enabled: Bool = true) -> CoreFeature {
            CoreFeature(key: key, name: key, parent: parent, edition: ed, experimental: ed == "dev", defaultOn: true, required: false, enabled: enabled, on: enabled)
        }
        // 頁面 monitoring 只有開發版的分頁開著:App 正式版時拿掉後頁面不顯示
        let s = CoreSystemSettings(features: [f("monitoring"), f("monitoring.alerts", "monitoring", "dev"),
                                              f("monitoring.visitors", "monitoring", enabled: false), f("newThing", nil, "beta")],
                                   edition: "dev")
        let flags = FeatureFlags(settings: s, source: .core)
        #if OMIXOR_DEV
        XCTAssertTrue(flags.enabled("monitoring") && flags.enabled("monitoring.alerts"))
        #else
        XCTAssertNil(flags.features.first { $0.key == "monitoring.alerts" })
        XCTAssertFalse(flags.enabled("monitoring.alerts"), "App 正式版沒有這個畫面")
        XCTAssertFalse(flags.enabled("monitoring"), "分頁被拿掉後重算:剩下的分頁都關著")
        #endif
        XCTAssertNil(flags.features.first { $0.key == "newThing" }, "不認得的版本不包含")
        XCTAssertFalse(flags.enabled("channels"), "新的 core 沒列 = 那台沒編進來")
        XCTAssertFalse(flags.enabled("unknown.key"))
        var old = s
        old.edition = nil
        XCTAssertTrue(FeatureFlags(settings: old, source: .core).enabled("channels"), "舊的 core 沒回版本:沒列的當作開")
        XCTAssertTrue(FeatureFlags().enabled("unknown.key"), "備援清單:App 不認得的 key 當作開")
        #if !OMIXOR_DEV
        XCTAssertFalse(FeatureFlags().enabled("familyAssistant"))
        XCTAssertFalse(FeatureFallback.settings.features.contains { $0.experimental }, "正式版的備援不含實驗功能")
        #endif
    }

    /// 系統頁:頁面關了分頁收合(值保留);必要的不收合
    func testCollapsedTabs() {
        let page = CoreFeature(key: "monitoring", name: "監控", experimental: false, defaultOn: true, required: false, enabled: false, on: false)
        let tab = CoreFeature(key: "monitoring.visitors", name: "訪客", parent: "monitoring", experimental: false, defaultOn: true, required: false, enabled: false, on: true)
        var flags = FeatureFlags(settings: CoreSystemSettings(features: [page, tab]), source: .core)
        XCTAssertTrue(flags.collapsed(page))
        XCTAssertEqual(flags.shownTabs(of: page).count, 0)
        XCTAssertEqual(flags.tabCount(of: page), 1)
        var on = page
        on.on = true
        flags = FeatureFlags(settings: CoreSystemSettings(features: [on, tab]), source: .core)
        XCTAssertFalse(flags.collapsed(on))
        XCTAssertEqual(flags.shownTabs(of: on).map(\.key), ["monitoring.visitors"])
        XCTAssertTrue(flags.shownTabs(of: on)[0].on, "分頁原本的值保留")
        let req = CoreFeature(key: "system", name: "系統", experimental: false, defaultOn: true, required: true, enabled: true, on: false)
        XCTAssertFalse(flags.collapsed(req), "必要的不收合")
    }
}
