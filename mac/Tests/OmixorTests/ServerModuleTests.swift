import XCTest
@testable import Omixor

/// 選用模組(core/module):App 讀 GET /api/modules,依模組清單與功能清單決定顯示什麼。
/// 預設的測試守護服務編進全部模組(行為照舊);最小版(-tags omixor_minimal)沒有程式碼倉庫模組:
/// 程式碼倉庫設定頁、開發進度 › Repositories 隱藏,Issue 的倉庫選單沒有候選,也不顯示錯誤
@MainActor
final class ServerModuleTests: XCTestCase {
    private var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-modules-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        ModuleStubServer.modules = nil
        unsetenv("OMIXOR_APP_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    /// 預設(含模組):模組清單有 integrations,Repositories 分頁、程式碼倉庫設定照舊,讀設定沒有錯誤
    func testDefaultCoreHasIntegrations() async throws {
        try TestCore.start(self)
        let app = AppState()
        app.startSystem()
        XCTAssertEqual(app.features.modules, [ServerModule.integrations])
        XCTAssertTrue(app.hasModule(ServerModule.integrations))
        XCTAssertTrue(app.isFeatureEnabled("development.repos"))
        XCTAssertTrue(app.visibleTabs(DevTab.self).contains(.repos))

        let gh = GitHubStore(app: app)
        _ = try gh.remote.saveGitHubSettings(org: app.currentOrgId, add: ["tim72117/evoleap"])
        await gh.syncSettingsAsync()
        XCTAssertEqual(app.githubSettings.repos, ["tim72117/evoleap"])
        XCTAssertNil(app.github.settingsError)
        let gl = GitLabStore(app: app)
        await gl.reload()
        XCTAssertNil(app.gitlabSettingsError)
        XCTAssertTrue(app.hasModule(ServerModule.integrations), "讀得到設定不會被當成沒有模組")
        let groups = IssueRepoOptions.groups(github: app.githubSettings.repos, issues: [], hasModule: app.hasModule(ServerModule.integrations))
        XCTAssertEqual(groups.map(\.label), ["GitHub"])
    }

    /// 最小版(沒有模組):備援清單依模組清單過濾後和 core 一致
    func testFallbackMatchesMinimalCore() throws {
        let cfg = try TestCore.start(self, minimal: true)
        let r = IssueRemote(config: cfg)
        XCTAssertEqual(try r.modules()?.map(\.id), [])
        let srv = try r.serverFeatures()
        XCTAssertFalse(srv.features.contains { $0.key == "development.repos" }, "沒編進模組,功能不存在")
        XCTAssertEqual(srv.features.map(\.key), FeatureFallback.entries(modules: []).map(\.key))
        let org = try r.systemSettings(org: OrgFiles.defaultId)
        let fb = FeatureFallback.settings(modules: [])
        XCTAssertEqual(org.features.map(\.key), fb.features.map(\.key))
        for (c, f) in zip(org.features, fb.features) { XCTAssertEqual(c.enabled, f.enabled, c.key) }
        XCTAssertEqual(org.serverFeatures, fb.serverFeatures)
    }

    /// 最小版(沒有模組):Repositories 分頁隱藏、程式碼倉庫設定頁不列、同步不顯示錯誤、倉庫選單只剩手動輸入與不標記
    func testMinimalCoreHidesIntegrations() async throws {
        let cfg = try TestCore.start(self, minimal: true)
        let app = AppState()
        app.startSystem()
        XCTAssertEqual(app.features.source, .core)
        XCTAssertEqual(app.features.modules, [])
        XCTAssertFalse(app.hasModule(ServerModule.integrations))
        XCTAssertFalse(app.isFeatureEnabled("development.repos"))
        XCTAssertFalse(app.visibleTabs(DevTab.self).contains(.repos))
        XCTAssertTrue(app.isFeatureEnabled("development.issues"), "核心功能照舊")

        // 直接打 integrations:路由不存在 → IntegrationModuleMissing(不是一般錯誤)
        let r = IssueRemote(config: cfg)
        XCTAssertThrowsError(try r.githubSettings(org: OrgFiles.defaultId)) { XCTAssertTrue($0 is IntegrationModuleMissing, "\($0)") }
        XCTAssertThrowsError(try r.gitlabSettings(org: OrgFiles.defaultId)) { XCTAssertTrue($0 is IntegrationModuleMissing, "\($0)") }

        let gh = GitHubStore(app: app)
        app.githubSettings.repos = ["old/local"]   // 這台 github.json 的舊設定也不顯示
        await gh.syncSettingsAsync()
        XCTAssertEqual(app.githubSettings.repos, [])
        XCTAssertNil(app.github.settingsError)
        await gh.refresh()
        XCTAssertNil(app.github.error, "沒有模組不顯示「還沒登入 GitHub」")
        let gl = GitLabStore(app: app)
        await gl.reload()
        XCTAssertNil(app.gitlabSettingsError)
        XCTAssertTrue(IssueRepoOptions.groups(github: ["a/b"], gitlab: ["g/p"], issues: [], hasModule: false).isEmpty)
    }

    /// 模組清單還沒說沒有(例如還沒讀到),但打 integrations 收到 404:當作沒有模組,隱藏、不顯示錯誤
    func testIntegrations404MarksModuleMissing() async throws {
        IssueRemote.protocolClasses = [ModuleStubServer.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5997")!, token: "tok-1")
        let app = AppState()
        XCTAssertNil(app.features.modules)
        XCTAssertTrue(app.isFeatureEnabled("development.repos"), "還沒讀到:備援照舊")
        let gh = GitHubStore(app: app)
        await gh.syncSettingsAsync()
        XCTAssertNil(app.github.settingsError)
        XCTAssertFalse(app.hasModule(ServerModule.integrations))
        XCTAssertFalse(app.isFeatureEnabled("development.repos"))
        let gl = GitLabStore(app: app)
        await gl.reload()
        XCTAssertNil(app.gitlabSettingsError)
    }

    /// GET /api/modules:舊版 server(404)→ nil(全部模組都在);[] → 沒有模組;core 的 JSON 404 不算「沒有模組」
    func testModulesEndpointAndOldServer() async throws {
        IssueRemote.protocolClasses = [ModuleStubServer.self]
        let cfg = IssueServerConfig(url: URL(string: "http://127.0.0.1:5997")!, token: "tok-1")
        IssueServerConfig.testOverride = cfg
        let r = IssueRemote(config: cfg)
        ModuleStubServer.modules = nil
        XCTAssertNil(try r.modules(), "舊版 server 沒有 /api/modules")
        XCTAssertTrue(FeatureFlags(modules: nil).hasModule(ServerModule.integrations))
        ModuleStubServer.modules = []
        XCTAssertEqual(try r.modules()?.map(\.id), [])
        XCTAssertFalse(FeatureFlags(modules: []).hasModule(ServerModule.integrations))
        XCTAssertThrowsError(try r.send("GET", cfg.url.appendingPathComponent("api/json404"), nil)) { e in
            XCTAssertEqual((e as? IssueRemoteError)?.status, 404)
            XCTAssertEqual((e as? IssueRemoteError)?.noRoute, false)
        }
        // 備援(server 讀不到)依模組清單過濾
        XCTAssertFalse(FeatureFallback.settings(modules: []).features.contains { $0.key == "development.repos" })
        XCTAssertTrue(FeatureFallback.settings(modules: nil).features.contains { $0.key == "development.repos" })
    }
}

/// 假的 omixor server:/api/modules 依 modules(nil = 舊版沒有這個端點,回 Go 的純文字 404);
/// /api/json404 回 core 的 JSON 404;其他路由一律純文字 404(例如沒編進模組的 /integrations/…)
final class ModuleStubServer: URLProtocol {
    nonisolated(unsafe) static var modules: [[String: Any]]?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let path = request.url!.path
        var status = 404
        var body = Data("404 page not found\n".utf8)
        if path == "/api/modules", let m = Self.modules {
            status = 200
            body = try! JSONSerialization.data(withJSONObject: ["result": m])
        } else if path == "/api/json404" {
            body = try! JSONSerialization.data(withJSONObject: ["error": "找不到"])
        }
        let resp = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }
}
