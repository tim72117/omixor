import XCTest
@testable import Omixor

/// GitLab 設定:組織的主機網址與追蹤的專案存在 core(用 TestCore 的暫存守護服務),
/// 這台的登入(個人存取權杖)存在測試用的鑰匙圈;GitLab 主機用 StubProtocol 假裝,不連真的 GitLab
@MainActor
final class GitLabSettingsTests: XCTestCase {
    private var app: AppState!
    private var dataDir: URL!
    static let host = "https://gitlab.example.test"

    override func setUp() async throws {
        try TestCore.start(self)
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-gl-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        ConnHTTP.protocolClasses = [StubProtocol.self]
        StubProtocol.log = []
        StubProtocol.handler = { req, _ in
            guard req.url?.host == "gitlab.example.test" else { return (599, ["message": "不該連到別的主機"]) }
            guard req.value(forHTTPHeaderField: "Authorization") == "Bearer glpat-test" else { return (401, ["message": "401 Unauthorized"]) }
            if req.url?.path == "/api/v4/user" { return (200, ["username": "tim", "name": "蔡庭宇"]) }
            return (404, ["message": "404 Not Found"])
        }
        app = AppState()
        app.startConnections()
    }

    override func tearDown() async throws {
        for c in app.connections { TokenVault.removeAll(c.id) }
        ConnHTTP.protocolClasses = []
        StubProtocol.handler = nil
        unsetenv("OMIXOR_APP_DATA_DIR")
        IssueServerConfig.testOverride = nil
        try? FileManager.default.removeItem(at: dataDir)
    }

    private var remote: IssueRemote { IssueRemote(config: IssueServerConfig.current) }

    /// 讀寫 /integrations/gitlab:沒存過是空白預設;網址與專案由核心正規化;add、remove
    func testRemoteReadWriteAndNormalize() throws {
        let org = app.currentOrgId
        let empty = try remote.gitlabSettings(org: org)
        XCTAssertFalse(empty.saved)
        XCTAssertEqual(empty.baseUrl, "")
        XCTAssertEqual(empty.projects, [])

        var x = try remote.saveGitLabSettings(org: org, baseUrl: Self.host + "/api/v4/")
        XCTAssertTrue(x.saved)
        XCTAssertEqual(x.baseUrl, Self.host, "去掉結尾的 /api/v4/")
        x = try remote.saveGitLabSettings(org: org, add: [Self.host + "/corp/web/app/-/merge_requests", "git@gitlab.example.test:corp/api.git", "42"])
        XCTAssertEqual(x.projects, ["corp/web/app", "corp/api", "42"])
        x = try remote.saveGitLabSettings(org: org, remove: ["CORP/API"])
        XCTAssertEqual(x.projects, ["corp/web/app", "42"], "移除不分大小寫")
        XCTAssertNotNil(x.updatedAt)

        let again = try remote.gitlabSettings(org: org)
        XCTAssertEqual(again.baseUrl, Self.host)
        XCTAssertEqual(again.projects, ["corp/web/app", "42"])
    }

    /// 格式不對時核心回 400 與說明,App 原樣顯示
    func testNormalizeErrorsAreShown() async throws {
        let org = app.currentOrgId
        XCTAssertThrowsError(try remote.saveGitLabSettings(org: org, baseUrl: "gitlab.example.test")) { e in
            XCTAssertTrue(e.localizedDescription.contains("GitLab 網址要寫成"), e.localizedDescription)
        }
        _ = try remote.saveGitLabSettings(org: org, baseUrl: Self.host)
        XCTAssertThrowsError(try remote.saveGitLabSettings(org: org, add: ["just-a-name"])) { e in
            XCTAssertTrue(e.localizedDescription.contains("專案"), e.localizedDescription)
        }

        // 透過 GitLabStore:錯誤丟給畫面,設定不變
        let s = app.startGitLab()
        await s.reload()
        do { try await s.addProject("bad project!"); XCTFail("應該失敗") } catch {
            XCTAssertTrue(error.localizedDescription.contains("專案"), error.localizedDescription)
        }
        do { try await s.setBaseURL("ftp://x"); XCTFail("應該失敗") } catch {
            XCTAssertTrue(error.localizedDescription.contains("GitLab 網址要寫成"), error.localizedDescription)
        }
        XCTAssertEqual(app.gitlabSettings.baseUrl, Self.host)
        XCTAssertEqual(app.gitlabSettings.projects, [])
    }

    /// GitLabStore:讀核心、改網址、加減專案;Issue 的倉庫選單列出追蹤的專案(gitlab: 前綴)
    func testStoreAndRepoPickerOptions() async throws {
        let s = app.startGitLab()
        await s.reload()
        XCTAssertNil(app.gitlabSettingsError)
        do { try await s.addProject("corp/web"); XCTFail("沒有網址時不能加專案") } catch {}

        try await s.setBaseURL(Self.host + "/")
        XCTAssertEqual(app.gitlabSettings.baseUrl, Self.host)
        try await s.addProject(Self.host + "/corp/web.git")
        try await s.addProject("7")
        XCTAssertEqual(app.gitlabSettings.projects, ["corp/web", "7"])
        try await s.removeProject("7")
        XCTAssertEqual(app.gitlabSettings.projects, ["corp/web"])

        // 別的裝置改了核心:重讀就跟著變
        _ = try remote.saveGitLabSettings(org: app.currentOrgId, add: ["corp/api"])
        await s.reload()
        XCTAssertEqual(app.gitlabSettings.projects, ["corp/web", "corp/api"])

        let groups = IssueRepoOptions.groups(github: ["tim72117/evoleap"], gitlab: app.gitlabSettings.projects, issues: [])
        XCTAssertEqual(groups.map(\.label), ["GitHub", "GitLab 專案"])
        XCTAssertEqual(groups[1].options, ["gitlab:corp/web", "gitlab:corp/api"])
        XCTAssertTrue(groups[1].options.allSatisfy(IssueRepo.isValid))
        XCTAssertEqual(GitLab.projectURL(base: Self.host, key: "corp/web")?.absoluteString, Self.host + "/corp/web")
        XCTAssertEqual(GitLab.projectURL(base: Self.host, key: "7")?.absoluteString, Self.host + "/projects/7")
    }

    /// 這台的登入:token 存在鑰匙圈、只送到組織的 GitLab 主機;換網址要重新登入;登出不動組織設定
    func testSignInLogoutAndBaseChange() async throws {
        let s = app.startGitLab()
        _ = try remote.saveGitLabSettings(org: app.currentOrgId, baseUrl: Self.host, add: ["corp/web"])
        await s.reload()
        XCTAssertFalse(s.isLoggedIn)

        s.signIn(token: "  glpat-test\n")
        XCTAssertTrue(s.isLoggedIn)
        let c = try XCTUnwrap(s.connection)
        XCTAssertEqual(c.baseURL, Self.host)
        XCTAssertEqual(c.auth, .bearer)
        XCTAssertEqual(TokenVault.tokens(GitLab.connectionId)?.accessToken, "glpat-test")
        await app.connStore?.refreshAccount(GitLab.connectionId)
        XCTAssertEqual(app.connectionStates[GitLab.connectionId]?.account, "蔡庭宇")
        XCTAssertTrue(StubProtocol.log.allSatisfy { $0.1.hasPrefix(Self.host + "/") }, "token 只送到 GitLab 主機")

        // 組織換了網址:這台的連線跟著換,舊 token 作廢
        _ = try remote.saveGitLabSettings(org: app.currentOrgId, baseUrl: "https://gitlab2.example.test")
        await s.reload()
        XCTAssertEqual(s.connection?.baseURL, "https://gitlab2.example.test")
        XCTAssertFalse(s.isLoggedIn)
        XCTAssertNil(TokenVault.tokens(GitLab.connectionId))

        s.signIn(token: "glpat-test")
        XCTAssertTrue(s.isLoggedIn)
        await s.logout()
        XCTAssertFalse(s.isLoggedIn)
        XCTAssertEqual(app.gitlabSettings.projects, ["corp/web"], "登出不動組織設定")
    }

    /// 連不到 core server:顯示錯誤,寫入失敗
    func testCoreUnavailableShowsError() async throws {
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:9")!, token: nil, unavailable: "本機服務還沒啟動")
        let s = app.startGitLab()
        await s.reload()
        XCTAssertEqual(app.gitlabSettingsError, "本機服務還沒啟動")
        do { try await s.setBaseURL(Self.host); XCTFail("應該失敗") } catch {}
        XCTAssertEqual(app.gitlabSettings.baseUrl, "")
    }
}
