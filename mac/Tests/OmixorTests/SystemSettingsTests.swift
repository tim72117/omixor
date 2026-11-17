import XCTest
@testable import Omixor

/// 功能開關的兩層(系統層 && 組織層):清單與開關在 core(暫存的本機守護服務;本機使用者是伺服器管理員);
/// App 照 core 的顯示、讀不到時用最後一份或保守預設
@MainActor
final class SystemSettingsTests: XCTestCase {
    private var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-system-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        unsetenv("OMIXOR_APP_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func feature(_ app: AppState, _ key: String) -> CoreFeature? { app.features.features.first { $0.key == key } }

    /// 備援清單要和 core 的一致(清單以 core/model/features.go 為準):系統層的 systemDefault、組織層的預設值
    func testFallbackMatchesCore() throws {
        let cfg = try TestCore.start(self)
        let r = IssueRemote(config: cfg)
        let srv = try r.serverFeatures()
        // 選用模組登記的功能只在編進該模組時出現:備援清單依這台守護服務的模組清單過濾(預設的測試守護服務編進全部模組)
        let modules = try XCTUnwrap(try r.modules()).map(\.id)
        XCTAssertEqual(modules, [ServerModule.integrations], "預設的測試守護服務編進全部模組")
        let entries = FeatureFallback.entries(modules: modules)
        XCTAssertEqual(srv.features.map(\.key), entries.map(\.key), "FeatureFallback.entries 要照 core/model/features.go 更新")
        for (c, e) in zip(srv.features, entries) {
            XCTAssertEqual(c.parent, e.parent, c.key)
            XCTAssertEqual(c.experimental, e.experimental, c.key)
            XCTAssertEqual(c.defaultOn, e.defaultOn, c.key)
            XCTAssertEqual(c.required, e.required, c.key)
            XCTAssertEqual(c.name, e.name, c.key)
            XCTAssertEqual(c.systemDefault, e.systemDefault, "系統層的預設:\(c.key)")
            XCTAssertEqual(c.available, FeatureFallback.serverHas(c.key), c.key)
        }
        XCTAssertEqual(srv.features.map(\.key), FeatureFallback.server(modules: modules).features.map(\.key))
        // 組織層沒設定過時:core 的清單、enabled 與 serverFeatures 和備援算的一樣
        let org = try r.systemSettings(org: OrgFiles.defaultId)
        let fb = FeatureFallback.settings(modules: modules)
        XCTAssertEqual(org.features.map(\.key), fb.features.map(\.key), "組織層只列系統層開放的")
        for (c, f) in zip(org.features, fb.features) {
            XCTAssertEqual(c.enabled, f.enabled, "沒設定過時 core 的 enabled 和備援算的一樣:\(c.key)")
            XCTAssertEqual(c.system, true, c.key)
        }
        XCTAssertEqual(org.serverFeatures, fb.serverFeatures)
        // 每個 App 的頁面與分頁都在清單裡(含選用模組的)
        let all = Set(FeatureFallback.entries.map(\.key))
        for s in AppSection.allCases { XCTAssertTrue(all.contains(s.rawValue), s.rawValue) }
        for k in DevTab.allCases.map(\.featureKey) + CICDTab.allCases.map(\.featureKey) + InfraTab.allCases.map(\.featureKey)
            + MonTab.allCases.map(\.featureKey) + IncidentsView.IncTab.allCases.map(\.featureKey) + AutoTab.allCases.map(\.featureKey) {
            XCTAssertTrue(all.contains(k), k)
        }
    }

    /// 組織層:從 core 讀、改;關掉的頁面不能跳、所在頁被關掉切回總覽;關掉的分頁不列
    func testLoadAndToggle() throws {
        try TestCore.start(self)
        let app = AppState()
        let store = app.startSystem()
        XCTAssertEqual(app.features.source, .core)
        XCTAssertEqual(app.features.serverSource, .core)
        XCTAssertNil(app.features.error)
        XCTAssertNil(app.features.serverError)
        XCTAssertTrue(app.features.canEdit, "本機模式是組織管理員")
        XCTAssertTrue(app.features.serverCanEdit, "本機模式的本機使用者是伺服器管理員")
        XCTAssertTrue(app.isVisible(.channels))
        XCTAssertFalse(app.isFeatureEnabled("monitoring.alerts"), "系統層預設不開放")
        XCTAssertEqual(app.visibleTabs(MonTab.self), [.visitors])
        XCTAssertEqual(app.shownTab(MonTab.alerts), .visitors)
        XCTAssertFalse(app.isFeatureEnabled("familyAssistant"))
        XCTAssertNil(feature(app, "familyAssistant"), "系統層沒開放的不列在組織層")
        XCTAssertNil(feature(app, "monitoring.alerts"))
        XCTAssertThrowsError(try store.set(["monitoring.alerts": true]), "系統層沒開放,組織開不了") { e in
            XCTAssertEqual((e as? IssueRemoteError)?.status, 400)
        }
        XCTAssertGreaterThan(app.features.serverOffCount, 0)

        XCTAssertTrue(app.go(.channels))
        XCTAssertEqual(app.section, .channels)
        try store.set(["channels": false])
        XCTAssertFalse(app.isVisible(.channels))
        XCTAssertEqual(app.section, .overview, "所在頁被關掉切回總覽")
        XCTAssertNotNil(app.featureNotice)
        XCTAssertFalse(app.go(.channels), "關掉的頁面不能跳")
        XCTAssertEqual(app.featureNoticeKey, "channels")
        XCTAssertTrue(app.featureNotice?.contains("設定 › 功能") == true, "組織層關的指向 設定 › 功能:\(app.featureNotice ?? "")")
        XCTAssertEqual(app.section, .overview)
        XCTAssertNil(app.badge(for: .channels))
        XCTAssertFalse(app.visibleSections.contains(.channels))
        XCTAssertTrue(app.visibleSections.contains(.system) && app.visibleSections.contains(.settings))

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

        // AI 助理:關掉的功能不建議、問到時說明(值班是系統層沒開放的 → 指向「系統」)
        app2.section = .overview
        let r = AssistResponder(app: app2)
        XCTAssertFalse(r.suggestions().contains("誰在值班?"))
        let reply = r.reply(to: "誰在值班?")
        XCTAssertTrue(reply.text.contains("沒有開放"), reply.text)
    }

    /// 系統層:伺服器管理員開放/不開放;組織層只列開放的,實際可用 = 系統層 && 組織層
    func testServerLayer() throws {
        try TestCore.start(self)
        let app = AppState()
        let store = app.startSystem()
        let alerts = try XCTUnwrap(app.features.server.features.first { $0.key == "monitoring.alerts" })
        XCTAssertFalse(alerts.systemDefault)
        XCTAssertFalse(alerts.available)

        // 系統層開放:組織層列出來,但組織層預設關(實驗功能)
        try store.setServer(["monitoring.alerts": true])
        XCTAssertEqual(app.features.server.features.first { $0.key == "monitoring.alerts" }?.available, true)
        XCTAssertNotNil(feature(app, "monitoring.alerts"))
        XCTAssertEqual(app.features.serverHas("monitoring.alerts"), true)
        XCTAssertFalse(app.isFeatureEnabled("monitoring.alerts"), "組織層還是預設關")
        XCTAssertEqual(app.redirectTab(MonTab.alerts), .visitors)
        XCTAssertTrue(app.featureNotice?.contains("設定 › 功能") == true, app.featureNotice ?? "")
        try store.set(["monitoring.alerts": true])
        XCTAssertEqual(app.visibleTabs(MonTab.self), [.alerts, .visitors])

        // 系統層再關掉:組織存著的值保留但不生效,清單不列
        try store.setServer(["monitoring.alerts": false])
        XCTAssertFalse(app.isFeatureEnabled("monitoring.alerts"))
        XCTAssertNil(feature(app, "monitoring.alerts"))
        XCTAssertEqual(app.visibleTabs(MonTab.self), [.visitors])
        XCTAssertEqual(app.featureOffText("monitoring.alerts", name: "告警"), "這台伺服器沒有開放「告警」(伺服器管理員可以到「系統」開放)")
        _ = app.redirectTab(MonTab.alerts)
        XCTAssertEqual(app.featureNoticeKey, "monitoring.alerts")
        XCTAssertTrue(app.featureNotice?.contains("系統") == true)
        try store.setServer([:], reset: ["monitoring.alerts"])
        // 系統層開放後,組織存著的值照舊生效
        try store.setServer(["monitoring.alerts": true])
        XCTAssertTrue(app.isFeatureEnabled("monitoring.alerts"), "組織存著的舊值保留")
        try store.setServer([:], reset: ["monitoring.alerts"])

        // 頁面不開放:頁面與分頁都不顯示(不管組織層)
        try store.setServer(["monitoring": false])
        XCTAssertFalse(app.isVisible(.monitoring))
        XCTAssertEqual(app.visibleTabs(MonTab.self), [])
        XCTAssertFalse(app.features.serverHas("monitoring.visitors"), "分頁要所屬頁面也開放")
        XCTAssertNil(feature(app, "monitoring"))
        XCTAssertFalse(app.go(.monitoring))
        XCTAssertTrue(app.featureNotice?.contains("沒有開放") == true, app.featureNotice ?? "")
        try store.setServer([:], reset: ["monitoring"])
        XCTAssertTrue(app.isVisible(.monitoring))

        // 必要的不能不開放;沒登記的 key 400
        XCTAssertThrowsError(try store.setServer(["system": false]))
        XCTAssertThrowsError(try store.setServer(["no.such.key": true])) { e in
            XCTAssertEqual((e as? IssueRemoteError)?.status, 400)
        }

        // 存過之後:changed、updatedBy
        try store.setServer(["familyAssistant": true])
        let fa = try XCTUnwrap(app.features.server.features.first { $0.key == "familyAssistant" })
        XCTAssertTrue(fa.changed && fa.on && fa.available)
        XCTAssertTrue(app.features.server.saved)
        XCTAssertNotNil(app.features.server.updatedBy)
        XCTAssertNotNil(feature(app, "familyAssistant"))
    }

    /// 系統層的權限:讀不到時、或不是伺服器管理員時唯讀(組織層照自己的)
    func testServerReadOnly() {
        var srv = FeatureFallback.server
        srv.canEdit = false
        var org = FeatureFallback.settings
        org.canEdit = true
        let flags = FeatureFlags(settings: org, source: .core, server: srv, serverSource: .core)
        XCTAssertFalse(flags.serverCanEdit, "不是伺服器管理員")
        XCTAssertTrue(flags.canEdit, "組織管理員還是能改組織層")
        srv.canEdit = true
        XCTAssertTrue(FeatureFlags(settings: org, source: .core, server: srv, serverSource: .core).serverCanEdit)
        XCTAssertFalse(FeatureFlags(settings: org, source: .core, server: srv, serverSource: .cached).serverCanEdit, "讀不到時不能改")
        XCTAssertFalse(FeatureFlags().serverCanEdit && FeatureFlags().canEdit, "保守預設不能改")
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
        XCTAssertEqual(app.features.serverSource, .fallback)
        XCTAssertNotNil(app.features.error)
        XCTAssertNotNil(app.features.serverError)
        XCTAssertFalse(app.features.canEdit)
        XCTAssertFalse(app.features.serverCanEdit)
        XCTAssertTrue(app.isVisible(.channels))
        XCTAssertFalse(app.isFeatureEnabled("pipelines.runs"))
        XCTAssertFalse(app.isFeatureEnabled("familyAssistant"), "保守預設:原本只有開發版的不開放")
        XCTAssertTrue(app.isFeatureEnabled("pipelines.voice"), "保守預設,不是剛剛改的")
        XCTAssertTrue(app.isVisible(.settings) && app.isVisible(.system) && app.isVisible(.overview))
    }

    /// 同一台 server 暫時讀不到:兩層都用這次執行最後一份
    func testCachedWhenServerDown() throws {
        let cfg = try TestCore.start(self)
        let app = AppState()
        let store = app.startSystem()
        try store.setServer(["monitoring.alerts": true])
        try store.set(["channels": false, "monitoring.alerts": true])
        // 同一個網址、但連不上(unavailable)
        IssueServerConfig.testOverride = IssueServerConfig(url: cfg.url, token: cfg.token, unavailable: "連不到")
        store.load()
        XCTAssertEqual(app.features.source, .cached)
        XCTAssertEqual(app.features.serverSource, .cached)
        XCTAssertEqual(app.features.error, "連不到")
        XCTAssertFalse(app.isVisible(.channels))
        XCTAssertTrue(app.isFeatureEnabled("monitoring.alerts"))
        XCTAssertEqual(app.features.server.features.first { $0.key == "monitoring.alerts" }?.on, true)
        XCTAssertFalse(app.features.canEdit, "讀不到時不能改")
        XCTAssertFalse(app.features.serverCanEdit)
    }

    private func f(_ key: String, _ parent: String? = nil, enabled: Bool = true, on: Bool? = nil, system: Bool? = nil) -> CoreFeature {
        CoreFeature(key: key, name: key, parent: parent, experimental: false, defaultOn: true, required: false,
                    enabled: enabled, on: on ?? enabled, system: system)
    }

    /// 兩層合併(新的 core):serverFeatures 是系統層;沒開放的一律關,清單沒列的當作沒有
    func testTwoLayerMerge() {
        let s = CoreSystemSettings(features: [f("monitoring", system: true), f("monitoring.visitors", "monitoring", system: true),
                                              f("cabinet", system: true)],
                                   serverFeatures: ["monitoring": true, "monitoring.visitors": true, "monitoring.alerts": false,
                                                    "cabinet": false, "channels": true])
        let flags = FeatureFlags(settings: s, source: .core)
        XCTAssertTrue(flags.enabled("monitoring") && flags.enabled("monitoring.visitors"))
        XCTAssertFalse(flags.enabled("cabinet"), "系統層沒開放:即使組織層開著也關")
        XCTAssertNil(flags.features.first { $0.key == "cabinet" }, "系統層沒開放的不列")
        XCTAssertFalse(flags.enabled("monitoring.alerts"))
        XCTAssertFalse(flags.enabled("channels"), "清單沒列 = 沒有")
        XCTAssertFalse(flags.enabled("unknown.key"))
        XCTAssertTrue(flags.serverHas("unknown.key"), "系統層沒登記的不擋(組織層清單沒列還是關)")
        XCTAssertEqual(flags.serverOffCount, 2)
    }

    /// 舊版 server(組織層的回應沒有 serverFeatures):系統層用保守預設,組織層照 server 的
    /// 範例資料計時器(伺服器指標、部署、Pipeline):對應的範例功能至少一個開著才跑,全關就停;開關改變時啟動/停止
    func testLiveUpdatesFollowFeatureFlags() {
        let app = AppState()
        app.features = .withFeatures { !AppState.liveUpdateFeatureKeys.contains($0) }
        app.startLiveUpdates()
        XCTAssertFalse(app.isLiveUpdating, "範例功能全關時不跑")
        app.features = .withFeatures { ["pipelines", "pipelines.runs"].contains($0) }
        XCTAssertTrue(app.isLiveUpdating, "打開一個就開始跑")
        app.features = .allOnForTests
        XCTAssertTrue(app.isLiveUpdating)
        app.features = .withFeatures { !AppState.liveUpdateFeatureKeys.contains($0) }
        XCTAssertFalse(app.isLiveUpdating, "全部關掉就停")
        // 保守預設(core 讀不到):範例功能預設關,不跑
        app.features = FeatureFlags()
        XCTAssertFalse(app.isLiveUpdating)
        // 停掉之後開關改變也不會自己啟動
        app.stopLiveUpdates()
        app.features = .allOnForTests
        XCTAssertFalse(app.isLiveUpdating)
        for k in AppState.liveUpdateFeatureKeys { XCTAssertNotNil(FeatureFallback.entry(k), k) }
    }

    func testOldServerDefaults() {
        // 舊的開發版 server:列了實驗分頁而且開著
        let s = CoreSystemSettings(features: [f("monitoring"), f("monitoring.alerts", "monitoring"),
                                              f("monitoring.visitors", "monitoring", enabled: false),
                                              f("familyAssistant"), f("channels", enabled: false), f("newThing")],
                                   edition: "dev")
        let flags = FeatureFlags(settings: s, source: .core)
        XCTAssertFalse(flags.enabled("monitoring.alerts"), "系統層保守預設:實驗的不開放")
        XCTAssertNil(flags.features.first { $0.key == "monitoring.alerts" })
        XCTAssertFalse(flags.enabled("monitoring"), "剩下開放的分頁都關著,頁面重算為不顯示")
        XCTAssertFalse(flags.enabled("familyAssistant"))
        XCTAssertFalse(flags.enabled("channels"), "組織層照 server 的")
        XCTAssertTrue(flags.enabled("newThing"), "App 不認得的 key:系統層不擋,照 server 的")
        XCTAssertFalse(flags.enabled("unlisted"), "有回 edition 的舊 core:清單沒列的當作沒有")
        XCTAssertFalse(flags.serverHas("monitoring.alerts"))
        XCTAssertTrue(flags.serverHas("monitoring.visitors"))
        // 很舊的 core(沒有 edition):清單沒列的當作開
        var old = s
        old.edition = nil
        XCTAssertTrue(FeatureFlags(settings: old, source: .core).enabled("unlisted"))
        // 保守預設本身
        let fb = FeatureFlags()
        XCTAssertFalse(fb.enabled("familyAssistant") || fb.enabled("mcpGateway") || fb.enabled("monitoring.alerts"))
        XCTAssertTrue(fb.enabled("channels") && fb.enabled("monitoring") && fb.enabled("automation.runner"))
        XCTAssertFalse(FeatureFallback.settings.features.contains { $0.experimental }, "系統層預設不開放實驗功能,組織層不列")
        XCTAssertEqual(FeatureFallback.server.features.count, FeatureFallback.entries.count, "系統層列全部登記的功能")
    }

    /// 設定 › 功能(組織層)與系統頁(系統層):頁面關了分頁收合(值保留);必要的不收合
    func testCollapsedTabs() {
        let page = CoreFeature(key: "monitoring", name: "監控", experimental: false, defaultOn: true, required: false, enabled: false, on: false)
        let tab = CoreFeature(key: "monitoring.visitors", name: "訪客", parent: "monitoring", experimental: false, defaultOn: true, required: false, enabled: false, on: true)
        let sf = ["monitoring": true, "monitoring.visitors": true]
        var flags = FeatureFlags(settings: CoreSystemSettings(features: [page, tab], serverFeatures: sf), source: .core)
        XCTAssertTrue(flags.collapsed(page))
        XCTAssertEqual(flags.shownTabs(of: page).count, 0)
        XCTAssertEqual(flags.tabCount(of: page), 1)
        var on = page
        on.on = true
        flags = FeatureFlags(settings: CoreSystemSettings(features: [on, tab], serverFeatures: sf), source: .core)
        XCTAssertFalse(flags.collapsed(on))
        XCTAssertEqual(flags.shownTabs(of: on).map(\.key), ["monitoring.visitors"])
        XCTAssertTrue(flags.shownTabs(of: on)[0].on, "分頁原本的值保留")
        let req = CoreFeature(key: "system", name: "系統", experimental: false, defaultOn: true, required: true, enabled: true, on: false)
        XCTAssertFalse(flags.collapsed(req), "必要的不收合")

        // 卡片清單(兩層共用):組織層的項目
        let items = [page, tab].map(FeatureItem.init(org:))
        XCTAssertTrue(FeatureItems.collapsed(items[0]))
        XCTAssertEqual(FeatureItems.shownTabs(items, of: items[0]), [])
        XCTAssertEqual(FeatureItems.tabs(items, of: items[0]).count, 1)
        XCTAssertEqual(FeatureItem(org: tab).warn, "所屬頁面關了")
        var allOff = on
        allOff.enabled = false
        XCTAssertEqual(FeatureItem(org: allOff).warn, "分頁都關了,不顯示")

        // 系統層的項目:頁面不開放時分頁收合;沒開放的標「這台伺服器沒有開放」、預設不開放的標「預設不開放」
        let sp = CoreServerFeature(key: "monitoring", name: "監控", experimental: false, defaultOn: true, required: false,
                                   systemDefault: true, on: false, available: false, changed: true)
        let st = CoreServerFeature(key: "monitoring.alerts", name: "告警", parent: "monitoring", experimental: true, defaultOn: false,
                                   required: false, systemDefault: false, on: true, available: false, changed: true)
        let sitems = [sp, st].map(FeatureItem.init(server:))
        XCTAssertTrue(FeatureItems.collapsed(sitems[0]))
        XCTAssertEqual(FeatureItems.shownTabs(sitems, of: sitems[0]), [])
        XCTAssertTrue(sitems[0].tags.contains { $0.text == "這台伺服器沒有開放" })
        XCTAssertTrue(sitems[1].tags.contains { $0.text == "預設不開放" })
        XCTAssertNotNil(sitems[1].warn, "自己開放但所屬頁面沒開放")
        var spOn = sp
        spOn.on = true
        spOn.available = true
        let open = [spOn, st].map(FeatureItem.init(server:))
        XCTAssertEqual(FeatureItems.shownTabs(open, of: open[0]).map(\.key), ["monitoring.alerts"])
    }
}
