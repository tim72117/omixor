import XCTest
@testable import Ops

/// GitHub:用 StubProtocol 假裝 api.github.com,回應格式取自 tim72117/evoleap 真實 API 的欄位
@MainActor
final class GitHubTests: XCTestCase {
    private var app: AppState!
    private var mcp: MCPHandler!
    private var dataDir: URL!
    private var nextId = 0

    override func setUp() async throws {
        try TestCore.start(self)   // 追蹤的 repo 是組織的設定,存在 core server:每個測試一個新的本機守護服務
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-gh-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        ConnHTTP.protocolClasses = [StubProtocol.self]
        StubProtocol.log = []
        StubProtocol.handler = Self.fakeGitHub
        app = AppState()
        app.startConnections()
        mcp = MCPHandler(app: app)
    }

    override func tearDown() async throws {
        for c in app.connections { TokenVault.removeAll(c.id) }
        ConnHTTP.protocolClasses = []
        StubProtocol.handler = nil
        unsetenv("OPS_DATA_DIR")
        IssueServerConfig.testOverride = nil
        try? FileManager.default.removeItem(at: dataDir)
    }

    // MARK: 假的 GitHub

    static let repo = "tim72117/evoleap"

    nonisolated static func fakeGitHub(_ req: URLRequest, _ body: Data) -> (Int, Any) {
        guard req.value(forHTTPHeaderField: "Authorization") == "Bearer gho_test" else { return (401, ["message": "Bad credentials"]) }
        let u = req.url!
        let path = u.path
        let base = "/repos/\(repo)"
        switch path {
        case "/user": return (200, ["login": "tim72117", "email": NSNull()])
        case base:
            return (200, ["full_name": repo, "description": "evoleap", "default_branch": "main", "private": false,
                          "open_issues_count": 1, "stargazers_count": 3, "pushed_at": "2026-11-10T17:16:49Z",
                          "html_url": "https://github.com/\(repo)"])
        case base + "/pulls":
            return (200, [["number": 12, "title": "兒童模式入口", "draft": false, "user": ["login": "tim72117"],
                           "head": ["ref": "feat/kids", "sha": "abc123"], "base": ["ref": "main"],
                           "labels": [["name": "kids"]], "requested_reviewers": [],
                           "created_at": "2026-11-09T10:00:00Z", "updated_at": "2026-11-10T10:00:00Z",
                           "html_url": "https://github.com/\(repo)/pull/12"]])
        case base + "/pulls/12":
            return (200, ["number": 12, "title": "兒童模式入口", "draft": false, "user": ["login": "tim72117"],
                          "head": ["ref": "feat/kids", "sha": "abc123"], "base": ["ref": "main"], "labels": [],
                          "body": "## 改了什麼\n- 入口", "additions": 40, "deletions": 2, "changed_files": 1,
                          "mergeable_state": "clean", "html_url": "https://github.com/\(repo)/pull/12"])
        case base + "/pulls/12/files":
            return (200, [["filename": "web/src/routes.tsx", "status": "modified", "additions": 40, "deletions": 2]])
        case base + "/commits/abc123/check-runs":
            return (200, ["total_count": 3, "check_runs": [
                ["status": "completed", "conclusion": "success"],
                ["status": "completed", "conclusion": "failure"],
                ["status": "in_progress", "conclusion": NSNull()],
            ]])
        case base + "/actions/runs":
            let failedOnly = u.query?.contains("status=failure") == true
            let runs: [[String: Any]] = [
                ["id": 42863301165, "name": "Deploy to Cloud Run", "display_title": "v0.35.0:兒童模式", "head_branch": "v0.35.0",
                 "event": "push", "status": "completed", "conclusion": "success", "head_sha": "f8d723e", "run_number": 46,
                 "run_attempt": 1, "created_at": "2026-11-10T17:16:49Z", "run_started_at": "2026-11-10T17:16:49Z",
                 "updated_at": "2026-11-10T17:26:40Z", "actor": ["login": "tim72117"], "html_url": "https://github.com/x/1"],
                ["id": 7, "name": "Test", "display_title": "壞掉的測試", "head_branch": "main", "event": "push",
                 "status": "completed", "conclusion": "failure", "head_sha": "deadbee", "run_number": 9, "run_attempt": 2,
                 "created_at": "2026-11-10T16:00:00Z", "updated_at": "2026-11-10T16:03:00Z", "actor": ["login": "tim72117"],
                 "html_url": "https://github.com/x/7"],
            ]
            return (200, ["total_count": 132, "workflow_runs": failedOnly ? runs.filter { $0["conclusion"] as? String == "failure" } : runs])
        case base + "/actions/runs/7":
            return (200, ["id": 7, "name": "Test", "display_title": "壞掉的測試", "head_branch": "main", "event": "push",
                          "status": "completed", "conclusion": "failure", "head_sha": "deadbee", "run_number": 9, "run_attempt": 2,
                          "created_at": "2026-11-10T16:00:00Z", "updated_at": "2026-11-10T16:03:00Z",
                          "actor": ["login": "tim72117"], "html_url": "https://github.com/x/7"])
        case base + "/actions/runs/7/jobs":
            return (200, ["jobs": [["id": 99, "name": "test / test", "status": "completed", "conclusion": "failure",
                                    "started_at": "2026-11-10T16:00:05Z", "completed_at": "2026-11-10T16:02:55Z",
                                    "html_url": "https://github.com/x/7/job/99",
                                    "steps": [["number": 1, "name": "Set up job", "status": "completed", "conclusion": "success"],
                                              ["number": 2, "name": "go test ./...", "status": "completed", "conclusion": "failure"]]]]])
        default:
            return (404, ["message": "Not Found"])
        }
    }

    private func login() async {
        let g = app.startGitHub(timer: false)
        g.ensureConnection()
        app.connStore?.setBearer("gho_test", for: GitHub.connectionId)
        try? g.addRepo("https://github.com/tim72117/evoleap.git")
        await app.connStore?.refreshAccount(GitHub.connectionId)
    }

    private func tool(_ name: String, _ args: [String: Any] = [:]) async throws -> (Any, Bool) {
        nextId += 1
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": nextId, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let handled = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let raw = try XCTUnwrap(handled)
        let obj = try XCTUnwrap(try JSONSerialization.jsonObject(with: raw) as? [String: Any])
        let result = try XCTUnwrap(obj["result"] as? [String: Any], "\(obj)")
        let text = try XCTUnwrap((result["content"] as? [[String: Any]])?.first?["text"] as? String)
        if result["isError"] as? Bool == true { return (text, true) }
        return (try JSONSerialization.jsonObject(with: Data(text.utf8), options: .fragmentsAllowed), false)
    }

    // MARK: 測試

    func testNormalizeRepo() {
        XCTAssertEqual(GitHub.normalizeRepo("tim72117/evoleap"), "tim72117/evoleap")
        XCTAssertEqual(GitHub.normalizeRepo(" https://github.com/tim72117/evoleap.git "), "tim72117/evoleap")
        XCTAssertEqual(GitHub.normalizeRepo("git@github.com:tim72117/evoleap.git"), "tim72117/evoleap")
        XCTAssertNil(GitHub.normalizeRepo("evoleap"))
        XCTAssertNil(GitHub.normalizeRepo("a/b/c"))
        XCTAssertNil(GitHub.normalizeRepo("a/b?x=1"))
    }

    func testPresetConnectionIsWebOAuth() throws {
        let c = GitHub.presetConnection
        XCTAssertEqual(c.auth, .oauth)
        XCTAssertEqual(c.baseURL, "https://api.github.com")
        XCTAssertEqual(c.oauth.authorizationEndpoint, "https://github.com/login/oauth/authorize")
        XCTAssertEqual(c.oauth.tokenEndpoint, "https://github.com/login/oauth/access_token")
        XCTAssertTrue(GitHub.registerURL.absoluteString.contains("callback_url%5D=http://127.0.0.1/callback")
                      || GitHub.registerURL.absoluteString.contains("callback_url]=http://127.0.0.1/callback"),
                      GitHub.registerURL.absoluteString)
        // GitHub 換 token 失敗時回 200 + error,要顯示原因
        XCTAssertThrowsError(try OAuthConnector.parseTokens(["error": "bad_verification_code",
                                                             "error_description": "The code passed is incorrect or expired."])) {
            XCTAssertEqual(($0 as? ConnectionError)?.message, "The code passed is incorrect or expired.")
        }
    }

    func testRefreshReadsPullsChecksAndRuns() async throws {
        await login()
        let g = try XCTUnwrap(app.ghStore)
        XCTAssertTrue(g.isLoggedIn)
        await g.refresh()
        XCTAssertNil(app.github.error)
        XCTAssertEqual(app.github.repos.first?.defaultBranch, "main")
        let pr = try XCTUnwrap(app.github.pulls.first)
        XCTAssertEqual(pr.number, 12)
        XCTAssertEqual(pr.branch, "feat/kids")
        XCTAssertEqual(pr.labels, ["kids"])
        XCTAssertEqual(pr.checks, GHChecks(total: 3, success: 1, failure: 1, pending: 1, skipped: 0))
        XCTAssertEqual(pr.checks?.state, "failure")
        XCTAssertEqual(app.github.runs.map(\.id), [42863301165, 7])
        let ok = app.github.runs[0]
        XCTAssertEqual(ok.stateLabel, "成功")
        XCTAssertEqual(ok.duration ?? 0, 591, accuracy: 1)
        XCTAssertTrue(app.github.runs[1].isFailure)
        XCTAssertFalse(app.github.busy)
        // 追蹤的 repo 是組織的設定,存在 core server;這台的 github.json 只寫用哪一個連線(不寫 repos)
        let core = try IssueRemote(config: IssueServerConfig.current).githubSettings(org: app.currentOrgId)
        XCTAssertTrue(core.saved)
        XCTAssertEqual(core.repos, ["tim72117/evoleap"])
        if let d = try? Data(contentsOf: app.dataDir.appendingPathComponent("github.json")) {
            let saved = try OrgFiles.decoder().decode(GHSettings.self, from: d)
            XCTAssertEqual(saved.repos, [])
            XCTAssertEqual(saved.connection, GitHub.connectionId)
        }
    }

    /// 這台的舊設定(github.json 的 repos):核心還沒存過時第一次連上匯入,原檔不刪不改;之後以核心為準
    func testLegacyReposImportedOnceAndFileKept() throws {
        let legacy = #"{"connection":"github","repos":["tim72117/evoleap","bad repo","a/b"]}"#
        try FileManager.default.createDirectory(at: app.dataDir, withIntermediateDirectories: true)
        let file = app.dataDir.appendingPathComponent("github.json")
        try Data(legacy.utf8).write(to: file)
        let g = app.startGitHub(timer: false)
        XCTAssertEqual(app.githubSettings.repos, ["tim72117/evoleap", "a/b"], "格式不對的略過")
        XCTAssertNil(app.github.settingsError)
        let remote = IssueRemote(config: IssueServerConfig.current)
        XCTAssertEqual(try remote.githubSettings(org: app.currentOrgId).repos, ["tim72117/evoleap", "a/b"])
        XCTAssertEqual(String(decoding: try Data(contentsOf: file), as: UTF8.self), legacy, "原檔不動")

        // 別的裝置改了核心:這台下次讀就跟著變;舊檔不會再匯入
        _ = try remote.saveGitHubSettings(org: app.currentOrgId, remove: ["a/b"])
        g.load()
        XCTAssertEqual(app.githubSettings.repos, ["tim72117/evoleap"])
        try g.removeRepo("tim72117/evoleap")
        XCTAssertEqual(app.githubSettings.repos, [])
        g.load()
        XCTAssertEqual(app.githubSettings.repos, [], "清空也算存過,不會再匯入舊檔")
        XCTAssertThrowsError(try g.addRepo("evoleap"))
    }

    /// 連不到 core server:顯示這台檔案裡的(唯讀),寫入回錯誤
    func testCoreUnavailableShowsLocalAndRejectsWrites() throws {
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:9")!, token: nil, unavailable: "本機服務還沒啟動")
        try FileManager.default.createDirectory(at: app.dataDir, withIntermediateDirectories: true)
        try Data(#"{"connection":"github","repos":["a/b"]}"#.utf8).write(to: app.dataDir.appendingPathComponent("github.json"))
        let g = app.startGitHub(timer: false)
        XCTAssertEqual(app.githubSettings.repos, ["a/b"])
        XCTAssertEqual(app.github.settingsError, "本機服務還沒啟動")
        XCTAssertThrowsError(try g.addRepo("c/d"))
        XCTAssertEqual(app.githubSettings.repos, ["a/b"])
    }

    func testBadTokenShowsError() async throws {
        await login()
        app.connStore?.setBearer("wrong", for: GitHub.connectionId)
        await app.ghStore?.refresh()
        XCTAssertNotNil(app.github.error)
        XCTAssertTrue(app.github.runs.isEmpty)
    }

    func testMissingRepoIsReportedPerRepo() async throws {
        await login()
        try app.ghStore?.addRepo("someone/private-thing")
        await app.ghStore?.refresh()
        XCTAssertNil(app.github.error)
        XCTAssertNotNil(app.github.errors["someone/private-thing"])
        XCTAssertEqual(app.github.repos.count, 1)
    }

    func testMCPToolsAreReadOnlyAndWork() async throws {
        let ro = MCPHandler.tools.filter { $0.name.hasPrefix("github") }
        XCTAssertEqual(ro.count, 4)

        // 還沒登入:告訴角色要請人登入
        let (msg, isErr) = try await tool("github")
        XCTAssertTrue(isErr)
        XCTAssertTrue("\(msg)".contains("還沒登入 GitHub"))

        await login()
        let sumOut = try await tool("github", ["refresh": true]).0
        let sum = try XCTUnwrap(sumOut as? [String: Any])
        let repo = try XCTUnwrap((sum["repos"] as? [[String: Any]])?.first)
        XCTAssertEqual(repo["failedRuns"] as? Int, 1)
        XCTAssertEqual((repo["openPulls"] as? [Any])?.count, 1)
        XCTAssertEqual(sum["account"] as? String, "tim72117")

        let runsOut = try await tool("github_runs", ["status": "failure"]).0
        let runs = try XCTUnwrap(runsOut as? [String: Any])
        XCTAssertEqual((runs["runs"] as? [[String: Any]])?.map { $0["id"] as? Int }, [7])

        let runOut = try await tool("github_run", ["id": 7]).0
        let run = try XCTUnwrap(runOut as? [String: Any])
        XCTAssertEqual(run["failedSteps"] as? [String], ["test / test › go test ./..."])
        XCTAssertEqual(run["state"] as? String, "失敗")

        let pullOut = try await tool("github_pull", ["number": 12]).0
        let pull = try XCTUnwrap(pullOut as? [String: Any])
        XCTAssertEqual(pull["changedFiles"] as? Int, 1)
        XCTAssertEqual((pull["files"] as? [[String: Any]])?.first?["filename"] as? String, "web/src/routes.tsx")

        // 沒追蹤的 repo 也可以直接查,格式錯誤會擋
        let (bad, badErr) = try await tool("github_runs", ["repo": "not a repo"])
        XCTAssertTrue(badErr, "\(bad)")
        // 只送 GET
        XCTAssertTrue(StubProtocol.log.allSatisfy { $0.0 == "GET" }, "\(StubProtocol.log.map { $0.0 + " " + $0.1 })")
    }

    func testRedirectToOtherHostDropsAuthorization() async throws {
        var original = URLRequest(url: URL(string: "https://api.github.com/repos/a/b/actions/jobs/1/logs")!)
        original.setValue("Bearer secret", forHTTPHeaderField: "Authorization")
        let task = URLSession.shared.dataTask(with: original)
        let resp = HTTPURLResponse(url: original.url!, statusCode: 302, httpVersion: nil, headerFields: nil)!
        var next = URLRequest(url: URL(string: "https://pipelines.actions.githubusercontent.com/log?sig=x")!)
        next.setValue("Bearer secret", forHTTPHeaderField: "Authorization")
        let out = await RedirectGuard.shared.urlSession(URLSession.shared, task: task, willPerformHTTPRedirection: resp, newRequest: next)
        XCTAssertNil(out?.value(forHTTPHeaderField: "Authorization"))
        var same = URLRequest(url: URL(string: "https://api.github.com/other")!)
        same.setValue("Bearer secret", forHTTPHeaderField: "Authorization")
        let kept = await RedirectGuard.shared.urlSession(URLSession.shared, task: task, willPerformHTTPRedirection: resp, newRequest: same)
        XCTAssertEqual(kept?.value(forHTTPHeaderField: "Authorization"), "Bearer secret")
        task.cancel()
    }

    func testOrgsKeepSeparateRepos() async throws {
        await login()
        let other = app.createOrg(name: "Side", template: .blank)
        await app.switchOrg(other.id).value
        XCTAssertEqual(app.githubSettings.repos, [])
        await app.switchOrg(OrgFiles.defaultId).value
        XCTAssertEqual(app.githubSettings.repos, ["tim72117/evoleap"])
        let copy = app.createOrg(name: "Copy", template: .copyCurrent)
        await app.switchOrg(copy.id).value
        XCTAssertEqual(app.githubSettings.repos, ["tim72117/evoleap"])
    }

    /// 條件請求:第二輪帶 If-None-Match,GitHub 回 304 就沿用上次的內容(不扣額度),結果和第一輪一樣
    func testRefreshUsesETags() async throws {
        await login()
        nonisolated(unsafe) var conditional = 0, notModified = 0
        StubProtocol.handler = { req, body in
            guard req.url?.host == "api.github.com", req.url?.path != "/user" else { return Self.fakeGitHub(req, body) }
            let etag = "W/\"\(req.url!.absoluteString.hashValue)\""
            if let sent = req.value(forHTTPHeaderField: "If-None-Match") {
                conditional += 1
                if sent == etag { notModified += 1; return (304, StubReply(headers: ["ETag": etag], body: NSNull())) }
            }
            let (status, obj) = Self.fakeGitHub(req, body)
            return (status, StubReply(headers: ["ETag": etag], body: obj))
        }
        let g = try XCTUnwrap(app.ghStore)
        await g.refresh()
        XCTAssertEqual(conditional, 0, "第一輪不該帶 If-None-Match")
        let first = app.github
        let n = StubProtocol.log.count
        await g.refresh()
        XCTAssertGreaterThan(notModified, 0)
        XCTAssertEqual(notModified, conditional)
        XCTAssertEqual(notModified, StubProtocol.log.count - n, "第二輪每個請求都要是 304")
        XCTAssertNil(app.github.error)
        XCTAssertEqual(app.github.errors, [:])
        XCTAssertEqual(app.github.pulls.map(\.number), first.pulls.map(\.number))
        XCTAssertEqual(app.github.pulls.first?.checks, first.pulls.first?.checks)
        XCTAssertEqual(app.github.runs.map(\.id), first.runs.map(\.id))
        XCTAssertEqual(app.github.repos.first?.defaultBranch, "main")
        // 重新讀組織資料後快取清掉
        g.load()
        conditional = 0
        await g.refresh()
        XCTAssertEqual(conditional, 0, "重新讀組織資料後不該沿用快取")
    }
}
