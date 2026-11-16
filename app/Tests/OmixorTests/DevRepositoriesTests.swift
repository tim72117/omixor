import XCTest
@testable import Ops

/// 開發進度 › Repositories(真實資料,功能 development.repos,屬於選用模組 integrations):
/// 分頁顯示條件、退場的 development.github 相容、其他頁跳過來的目標、清單每列的摘要。
/// 不連 core 也不連 GitHub:server 用 ModuleStubServer(全部 404),功能開關直接給
@MainActor
final class DevRepositoriesTests: XCTestCase {
    private var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-devrepos-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        IssueRemote.protocolClasses = [ModuleStubServer.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5997")!, token: "tok-1")
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func feature(_ key: String, parent: String? = "development", experimental: Bool = false, enabled: Bool) -> CoreFeature {
        CoreFeature(key: key, name: key, parent: parent, experimental: experimental, defaultOn: !experimental, required: false,
                    enabled: enabled, on: enabled, system: true)
    }

    /// 新版 core:development.repos 在清單裡(模組編進來時)
    private func newCore(reposOn: Bool = true) -> CoreSystemSettings {
        CoreSystemSettings(features: [feature("development", parent: nil, enabled: true), feature("development.issues", enabled: true),
                                      feature("development.repos", enabled: reposOn)],
                           serverFeatures: ["development": true, "development.issues": true, "development.repos": true])
    }

    /// 還沒改的舊版 core:development.repos 是範例資料(實驗、關),真的在 development.github
    private func oldCore(githubOn: Bool = true) -> CoreSystemSettings {
        CoreSystemSettings(features: [feature("development", parent: nil, enabled: true), feature("development.issues", enabled: true),
                                      feature("development.repos", experimental: true, enabled: false),
                                      feature("development.github", enabled: githubOn)],
                           serverFeatures: ["development": true, "development.issues": true, "development.repos": true, "development.github": true])
    }

    // MARK: 功能清單與顯示條件

    func testFallbackEntry() throws {
        let e = try XCTUnwrap(FeatureFallback.entry("development.repos"))
        XCTAssertEqual(e.module, ServerModule.integrations, "Repositories 屬於選用模組 integrations")
        XCTAssertFalse(e.experimental, "是真實功能,不再是實驗/範例資料")
        XCTAssertTrue(e.defaultOn)
        XCTAssertTrue(e.systemDefault)
        XCTAssertNil(FeatureFallback.entry("development.github"), "development.github 退場")
        XCTAssertFalse(DevTab.allCases.map(\.rawValue).contains("github"), "沒有獨立的 GitHub 分頁")
        XCTAssertEqual(DevTab.repos.featureKey, "development.repos")
        XCTAssertFalse(FeatureFallback.settings(modules: []).features.contains { $0.key == "development.repos" }, "沒有模組時不列")
        XCTAssertTrue(FeatureFallback.settings(modules: nil).features.contains { $0.key == "development.repos" && $0.enabled })
    }

    func testTabVisibility() {
        let app = AppState()
        app.features = FeatureFlags(settings: newCore(), source: .core, modules: [ServerModule.integrations])
        XCTAssertTrue(app.visibleTabs(DevTab.self).contains(.repos))
        // 組織層關掉
        app.features = FeatureFlags(settings: newCore(reposOn: false), source: .core, modules: [ServerModule.integrations])
        XCTAssertFalse(app.visibleTabs(DevTab.self).contains(.repos))
        // 沒有 integrations 模組:整個分頁不顯示(就算清單裡有也一樣)
        app.features = FeatureFlags(settings: newCore(), source: .core, modules: [])
        XCTAssertFalse(app.visibleTabs(DevTab.self).contains(.repos))
        XCTAssertTrue(app.visibleTabs(DevTab.self).contains(.issues))
        // 還沒讀到模組清單:備援(全部模組都在)
        app.features = FeatureFlags(modules: nil)
        XCTAssertTrue(app.visibleTabs(DevTab.self).contains(.repos))
    }

    /// 舊 key 的相容:舊的 development.github 當作 development.repos;還沒改的舊版 core 照 development.github 的值
    func testLegacyGitHubKey() {
        var f = FeatureFlags(settings: newCore(), source: .core, modules: [ServerModule.integrations])
        XCTAssertTrue(f.enabled("development.github"), "舊 key 當作 development.repos")
        f = FeatureFlags(settings: newCore(reposOn: false), source: .core, modules: [ServerModule.integrations])
        XCTAssertFalse(f.enabled("development.github"))

        f = FeatureFlags(settings: oldCore(), source: .core, modules: [ServerModule.integrations])
        XCTAssertTrue(f.enabled("development.repos"), "舊版 core:Repositories 照 GitHub 分頁的開關(不是範例資料那個)")
        XCTAssertTrue(f.serverHas("development.repos"))
        f = FeatureFlags(settings: oldCore(githubOn: false), source: .core, modules: [ServerModule.integrations])
        XCTAssertFalse(f.enabled("development.repos"))
        f = FeatureFlags(settings: oldCore(), source: .core, modules: [])
        XCTAssertFalse(f.enabled("development.repos"), "沒有模組一律不顯示")
        XCTAssertFalse(f.enabled("development.github"))
        XCTAssertEqual(FeatureFallback.legacyKey(of: "development.repos"), "development.github")
        XCTAssertNil(FeatureFallback.legacyKey(of: "development.issues"))
    }

    // MARK: 跳轉目標

    func testFocusTargets() {
        let app = AppState()
        app.githubSettings.repos = ["tim72117/evoleap"]
        app.gitlabSettings.projects = ["corp/web"]
        let m = DevModel()
        XCTAssertTrue(m.apply(focus: "github", app: app), "舊的 GitHub 分頁跳轉改到 Repositories")
        XCTAssertEqual(m.tab, .repos)
        XCTAssertNil(m.selectedRepo)

        m.tab = .overview
        XCTAssertTrue(m.apply(focus: "repos", app: app))
        XCTAssertEqual(m.tab, .repos)

        m.tab = .overview
        XCTAssertTrue(m.apply(focus: "Tim72117/Evoleap", app: app), "追蹤的 repo(不分大小寫)")
        XCTAssertEqual(m.tab, .repos)
        XCTAssertEqual(m.selectedRepo, "tim72117/evoleap")

        XCTAssertTrue(m.apply(focus: "github:tim72117/evoleap", app: app))
        XCTAssertEqual(m.selectedRepo, "tim72117/evoleap")

        XCTAssertTrue(m.apply(focus: "gitlab:corp/web", app: app))
        XCTAssertEqual(m.selectedRepo, "gitlab:corp/web")

        m.tab = .overview
        XCTAssertTrue(m.apply(focus: "issues", app: app))
        XCTAssertEqual(m.tab, .issues)

        XCTAssertFalse(m.apply(focus: "not/tracked", app: app), "沒追蹤的不認得")
        XCTAssertFalse(m.apply(focus: "gitlab:tim72117/evoleap", app: app), "平台不同不算")
    }

    // MARK: 清單

    func testListingSummary() {
        let entries = RepoListing.entries(github: ["a/b", "c/d"], gitlab: ["g/p"])
        XCTAssertEqual(entries.map(\.id), ["a/b", "c/d", "gitlab:g/p"])
        XCTAssertEqual(entries.map(\.platform), [.github, .github, .gitlab])
        XCTAssertEqual(entries.last?.key, "g/p")

        let info = GHRepo(id: "a/b", description: "", defaultBranch: "main", isPrivate: false, openIssues: 0, stars: 0, url: "https://github.com/a/b")
        func run(_ id: Int, _ repo: String, _ branch: String, _ conclusion: String?, ago: TimeInterval) -> GHRun {
            GHRun(id: id, repo: repo, workflow: "CI", title: "t\(id)", branch: branch, event: "push", status: conclusion == nil ? "in_progress" : "completed",
                  conclusion: conclusion, sha: "abc", actor: "tim", runNumber: id, attempt: 1, createdAt: Date.now.addingTimeInterval(-ago), url: "")
        }
        let runs = [run(1, "a/b", "feature", "failure", ago: 10), run(2, "a/b", "main", "failure", ago: 60),
                    run(3, "a/b", "main", "success", ago: 600), run(4, "c/d", "main", "success", ago: 5)]
        XCTAssertEqual(RepoListing.latestDefaultBranchRun(repo: "a/b", info: info, runs: runs)?.id, 2, "只看預設分支、取最新")
        XCTAssertNil(RepoListing.latestDefaultBranchRun(repo: "a/b", info: nil, runs: runs), "還不知道預設分支")
        XCTAssertNil(RepoListing.latestDefaultBranchRun(repo: "x/y", info: info, runs: runs))

        func pull(_ repo: String, _ n: Int) -> GHPull {
            GHPull(repo: repo, number: n, title: "p", author: "tim", branch: "f", base: "main", draft: false, labels: [], reviewers: [],
                   headSHA: "", url: "")
        }
        let pulls = [pull("a/b", 1), pull("a/b", 2), pull("c/d", 1)]
        XCTAssertEqual(RepoListing.openPullCount(repo: "a/b", pulls: pulls), 2)
        XCTAssertEqual(RepoListing.openPullCount(repo: "g/p", pulls: pulls), 0)
    }
}
