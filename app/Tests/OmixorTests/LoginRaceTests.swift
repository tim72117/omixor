import XCTest
@testable import Ops

/// 登入取消與競態:確認登入、換 token 各階段按取消都生效且不留下 token;等核准時切換組織 token 存回原組織
@MainActor
final class LoginRaceTests: XCTestCase {
    let server = URL(string: "https://omixor.test")!
    var srv: OmixorMode.Server!
    private var dataDir: URL!

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-loginrace-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"   // 不碰正式鑰匙圈
        TokenVault.scope = ""
        ConnHTTP.protocolClasses = [StubProtocol.self]
        StubProtocol.log = []
        srv = OmixorMode.Server(id: "prod", name: "正式站", url: server, auth: [OmixorMode.AuthOption(id: "evoleap", label: "evoleap")])
        OmixorMode.testServers = [srv]
        Browser.open = { _ in }
    }

    override func tearDown() async throws {
        Browser.open = { _ in }
        ConnHTTP.protocolClasses = []
        StubProtocol.handler = nil
        OmixorMode.testServers = nil
        OmixorAuth.setToken(nil, server: server)
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func requests(_ path: String) -> [(String, String, Data)] { StubProtocol.log.filter { URL(string: $0.1)?.path == path } }

    /// 模擬使用者在瀏覽器按核准:打開核准頁時,照 start 登記的回呼網址送回 code
    private func approveInBrowser() {
        Browser.open = { _ in
            let redirect = StubProtocol.log.compactMap { e -> String? in
                guard e.1.hasSuffix("/api/cli-auth/start"), let o = try? JSONSerialization.jsonObject(with: e.2) as? [String: Any] else { return nil }
                return o["redirect_uri"] as? String
            }.last!
            let u = URL(string: redirect + "?id=req-1&code=c1")!
            Task.detached { _ = try? await URLSession(configuration: .ephemeral).data(from: u) }
        }
    }

    private func waitFor(_ path: String, _ what: String) async throws {
        try await waitUntil(10, what) { !self.requests(path).isEmpty }
    }

    /// 問題 3:確認存著的登入(/api/auth/me 慢)時按取消:立即回到可選擇的畫面,之後也不開瀏覽器
    func testCancelDuringCheckStopsBeforeBrowser() async throws {
        StubProtocol.handler = { req, _ in
            switch req.url!.path {
            case "/api/auth/me": Thread.sleep(forTimeInterval: 0.6); return (401, ["error": "x"])
            case "/api/cli-auth/start": return (200, ["id": "req-1"])
            default: return (404, ["error": "?"])
            }
        }
        OmixorAuth.setToken(TokenSet(accessToken: "omx_old", tokenType: "Bearer"), server: server)
        let app = AppState()
        let modeBefore = app.mode
        var opened = false
        Browser.open = { _ in opened = true }
        let t = Task { await app.connectRemote(srv, auth: "evoleap") }
        try await waitFor("/api/auth/me", "開始確認登入")
        XCTAssertEqual(app.connecting, "正式站")
        app.cancelConnect()
        XCTAssertNil(app.connecting, "取消要立即生效")
        await t.value
        try await Task.sleep(for: .milliseconds(800))   // 慢的 /api/auth/me 回來之後也不能開瀏覽器
        XCTAssertFalse(opened, "取消後不能再開瀏覽器")
        XCTAssertTrue(requests("/api/cli-auth/start").isEmpty)
        XCTAssertNil(app.connectError)
        XCTAssertNil(app.connectLogin)
        XCTAssertEqual(app.mode, modeBefore)
        XCTAssertEqual(OmixorAuth.token(server: server)?.accessToken, "omx_old", "原本存著的登入不動")
    }

    /// 問題 2:換到 omixor token 之後(撤銷 evoleap token 期間)才按取消:撤銷新的 omixor token、不存、不切模式
    func testCancelAfterExchangeRevokesAndStaysPut() async throws {
        StubProtocol.handler = { req, _ in
            switch req.url!.path {
            case "/api/auth/me": return (401, ["error": "x"])
            case "/api/cli-auth/start": return (200, ["id": "req-1"])
            case "/api/cli-auth/exchange": return (200, ["token": "evo_t"])
            case "/api/auth/evoleap": return (200, ["token": "omx_new", "user": ["email": "a@b", "role": "member"]])
            case "/api/cli-auth/logout": Thread.sleep(forTimeInterval: 0.5); return (200, ["ok": true])
            case "/api/auth/logout": return (200, ["result": true])
            case "/api/orgs": return (200, ["result": [["id": "default", "name": "D"]]])
            default: return (404, ["error": "?"])
            }
        }
        let app = AppState()
        let modeBefore = app.mode
        approveInBrowser()
        let t = Task { await app.connectRemote(srv, auth: "evoleap") }
        try await waitFor("/api/cli-auth/logout", "開始撤銷 evoleap token")
        app.cancelConnect()
        await t.value
        try await waitUntil(5, "撤銷新的 omixor token") { !self.requests("/api/auth/logout").isEmpty }
        XCTAssertEqual(app.mode, modeBefore, "取消後不切到遠端模式")
        XCTAssertNil(OmixorAuth.token(server: server), "取消後不存 token")
        XCTAssertNil(app.connectError)
        XCTAssertNil(app.noOrgsServer)
        XCTAssertTrue(requests("/api/orgs").isEmpty, "取消後不再讀組織清單")
        let revoked = StubProtocol.log.first { URL(string: $0.1)?.path == "/api/auth/logout" }
        XCTAssertNotNil(revoked)
    }

    /// 問題 5(omixor):停在 /api/auth/evoleap 換 token 時按取消:請求不中斷,換到的 token 立刻撤銷、不存
    func testCancelDuringOmixorExchangeRevokesIssuedToken() async throws {
        var revokedAuth: String?
        StubProtocol.handler = { req, _ in
            switch req.url!.path {
            case "/api/auth/me": return (401, ["error": "x"])
            case "/api/cli-auth/start": return (200, ["id": "req-1"])
            case "/api/cli-auth/exchange": return (200, ["token": "evo_t"])
            case "/api/auth/evoleap": Thread.sleep(forTimeInterval: 0.5); return (200, ["token": "omx_issued", "user": ["email": "a@b"]])
            case "/api/cli-auth/logout": return (200, ["ok": true])
            case "/api/auth/logout": revokedAuth = req.value(forHTTPHeaderField: "Authorization"); return (200, ["result": true])
            default: return (404, ["error": "?"])
            }
        }
        let app = AppState()
        approveInBrowser()
        let t = Task { await app.connectRemote(srv, auth: "evoleap") }
        try await waitFor("/api/auth/evoleap", "開始換 omixor token")
        app.cancelConnect()
        await t.value
        try await waitUntil(5, "撤銷換到的 omixor token") { revokedAuth != nil }
        XCTAssertEqual(revokedAuth, "Bearer omx_issued", "伺服器發的 token 要撤銷")
        XCTAssertNil(OmixorAuth.token(server: server))
        XCTAssertFalse(requests("/api/cli-auth/logout").isEmpty, "evoleap token 也要撤銷")
        XCTAssertEqual(app.mode.mode, OmixorMode.local)
    }

    /// 問題 5(evoleap):停在 /api/cli-auth/exchange 時按取消:請求不中斷,換到的 evoleap token 立刻撤銷,不再拿去換 omixor token
    func testCancelDuringEvoleapExchangeRevokesEvoleapToken() async throws {
        var revokedAuth: String?
        StubProtocol.handler = { req, _ in
            switch req.url!.path {
            case "/api/auth/me": return (401, ["error": "x"])
            case "/api/cli-auth/start": return (200, ["id": "req-1"])
            case "/api/cli-auth/exchange": Thread.sleep(forTimeInterval: 0.5); return (200, ["token": "evo_t"])
            case "/api/cli-auth/logout": revokedAuth = req.value(forHTTPHeaderField: "Authorization"); return (200, ["ok": true])
            default: return (404, ["error": "?"])
            }
        }
        let app = AppState()
        approveInBrowser()
        let t = Task { await app.connectRemote(srv, auth: "evoleap") }
        try await waitFor("/api/cli-auth/exchange", "開始換 evoleap token")
        app.cancelConnect()
        await t.value
        try await waitUntil(5, "撤銷 evoleap token") { revokedAuth != nil }
        XCTAssertEqual(revokedAuth, "Bearer evo_t")
        XCTAssertTrue(requests("/api/auth/evoleap").isEmpty, "取消後不再換 omixor token")
        XCTAssertNil(OmixorAuth.token(server: server))
        XCTAssertNil(app.connectError)
    }

    /// 問題 5(連線登入):連線的 evoleap 核准停在換 token 時取消:撤銷、不存
    func testConnectionLoginCancelDuringExchange() async throws {
        var revokedAuth: String?
        StubProtocol.handler = { req, _ in
            switch req.url!.path {
            case "/api/cli-auth/start": return (200, ["id": "req-1"])
            case "/api/cli-auth/exchange": Thread.sleep(forTimeInterval: 0.5); return (200, ["token": "evo_c"])
            case "/api/cli-auth/logout": revokedAuth = req.value(forHTTPHeaderField: "Authorization"); return (200, ["ok": true])
            default: return (404, ["error": "?"])
            }
        }
        let app = AppState()
        app.startConnections()
        let store = try XCTUnwrap(app.connStore)
        approveInBrowser()
        let login = Task { await store.login("evoleap-local") }
        try await waitFor("/api/cli-auth/exchange", "開始換 token")
        store.cancelLogin("evoleap-local")
        await login.value
        XCTAssertEqual(revokedAuth, "Bearer evo_c")
        XCTAssertNil(TokenVault.tokens("evoleap-local"))
        XCTAssertNil(app.connectionStates["evoleap-local"]?.error)
    }

    /// 問題 4:等核准時切換組織:token 存回開始登入的組織,不會寫到切換後的組織
    func testOrgSwitchDuringLoginStoresInOriginalOrg() async throws {
        StubProtocol.handler = { req, _ in
            switch req.url!.path {
            case "/api/cli-auth/start": return (200, ["id": "req-1"])
            case "/api/cli-auth/exchange": return (200, ["token": "evo_org_a"])
            default: return (404, ["error": "?"])
            }
        }
        let app = AppState()
        app.startConnections()
        let store = try XCTUnwrap(app.connStore)
        let orgA = app.currentOrgId
        let scopeA = TokenVault.scope
        var approve: URL?
        Browser.open = { _ in
            let redirect = StubProtocol.log.compactMap { e -> String? in
                guard e.1.hasSuffix("/api/cli-auth/start"), let o = try? JSONSerialization.jsonObject(with: e.2) as? [String: Any] else { return nil }
                return o["redirect_uri"] as? String
            }.last!
            approve = URL(string: redirect + "?id=req-1&code=c1")
        }
        let login = Task { await store.login("evoleap-local") }
        try await waitUntil(10, "開瀏覽器") { approve != nil }
        // 等核准時切到別的組織
        let b = app.createOrg(name: "第二組織", template: .blank)
        await app.switchOrg(b.id).value
        XCTAssertNotEqual(TokenVault.scope, scopeA)
        XCTAssertFalse(store.isLoggingIn("evoleap-local"), "登入工作以組織+連線為鍵,換組織後這個組織沒有在登入")
        // 使用者在瀏覽器按了核准
        _ = try? await URLSession(configuration: .ephemeral).data(from: approve!)
        await login.value
        XCTAssertEqual(TokenVault.tokens("evoleap-local", scope: scopeA)?.accessToken, "evo_org_a", "存回開始登入的組織")
        XCTAssertNil(TokenVault.tokens("evoleap-local"), "不能存到切換後的組織")
        XCTAssertNotEqual(app.currentOrgId, orgA)
    }

    /// 問題 7:換組織時清掉上一個組織的「提到我」;IssueRemote 建立時就固定 evoleap 的組織前綴
    func testOrgChangeClearsMentionsAndRemoteCapturesScope() {
        let app = AppState()
        let channels = app.startChannels()
        app.myMentions = [ChannelInbox.Mention(channelId: "c", channelName: "一般", id: "m", author: "a", text: "@tim", at: .now)]
        channels.clearForOrgChange()
        XCTAssertTrue(app.myMentions.isEmpty)

        TokenVault.scope = "org-a/"
        let cfg = IssueServerConfig(url: server, token: nil)
        let r = IssueRemote(config: cfg)
        XCTAssertEqual(r.evoleapScope, "org-a/")
        XCTAssertTrue(r.matches(cfg))
        TokenVault.scope = "org-b/"
        XCTAssertEqual(r.evoleapScope, "org-a/", "建立後不再讀 TokenVault.scope")
        XCTAssertFalse(r.matches(cfg), "組織前綴換了要換新的用戶端")
        XCTAssertEqual(IssueRemote(config: cfg, scope: "x/").evoleapScope, "x/")
        TokenVault.scope = ""
    }
}

/// 問題 6 用的假伺服器:執行緒安全地記錄請求;換 token 慢一點,讓平行請求同時遇到 401
final class TokenRaceServer: URLProtocol {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var _exchanges = 0
    nonisolated(unsafe) private static var _revoked: [String] = []
    nonisolated(unsafe) private static var _valid: Set<String> = []
    nonisolated(unsafe) static var exchangeDelay: TimeInterval = 0.3

    static func reset() { lock.lock(); _exchanges = 0; _revoked = []; _valid = []; lock.unlock() }
    static var exchanges: Int { lock.lock(); defer { lock.unlock() }; return _exchanges }
    static var revoked: [String] { lock.lock(); defer { lock.unlock() }; return _revoked }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let path = request.url!.path
        let auth = String((request.value(forHTTPHeaderField: "Authorization") ?? "").dropFirst("Bearer ".count))
        var status = 200
        var obj: [String: Any] = ["result": []]
        switch path {
        case "/api/auth/evoleap":
            Thread.sleep(forTimeInterval: Self.exchangeDelay)
            Self.lock.lock()
            Self._exchanges += 1
            let t = "omx_\(Self._exchanges)"
            Self._valid.insert(t)
            Self.lock.unlock()
            obj = ["token": t]
        case "/api/auth/logout":
            Self.lock.lock(); Self._revoked.append(auth); Self._valid.remove(auth); Self.lock.unlock()
            obj = ["result": true]
        default:
            Self.lock.lock(); let ok = Self._valid.contains(auth); Self.lock.unlock()
            if !ok { status = 401; obj = ["error": "omixor 的登入無效"] }
        }
        let resp = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: try! JSONSerialization.data(withJSONObject: obj))
        client?.urlProtocolDidFinishLoading(self)
    }
}

/// 問題 6:換 omixor token 單一飛行;登出後背景請求不自動重新登入
@MainActor
final class TokenExchangeGateTests: XCTestCase {
    let server = URL(string: "http://omixor-race.test")!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"   // 用真的(測試用)鑰匙圈:執行緒安全
        TokenVault.scope = ""
        IssueRemote.testVault = nil
        IssueRemote.protocolClasses = [TokenRaceServer.self]
        TokenRaceServer.reset()
        TokenRaceServer.exchangeDelay = 0.3
        OmixorTokenGate.markLoggedIn(server)
        // 有 evoleap 連線的登入(換 token 用);omixor token 是已失效的舊的
        TokenVault.setTokens(TokenSet(accessToken: "evo-tok", tokenType: "Bearer"), IssueServerConfig.evoleapConnection, scope: "")
        TokenVault.setTokens(TokenSet(accessToken: "omx_stale", tokenType: "Bearer"), IssueServerConfig.omixorTokenId, scope: OmixorMode.tokenScope(for: server))
    }

    override func tearDown() async throws {
        IssueRemote.protocolClasses = []
        OmixorTokenGate.markLoggedIn(server)
        TokenVault.setTokens(nil, IssueServerConfig.omixorTokenId, scope: OmixorMode.tokenScope(for: server))
        TokenVault.setTokens(nil, IssueServerConfig.evoleapConnection, scope: "")
    }

    private var omixorToken: String? { TokenVault.tokens(IssueServerConfig.omixorTokenId, scope: OmixorMode.tokenScope(for: server))?.accessToken }

    /// 平行 reload 同時 401:只換一次 token,大家都用換到的那把
    func testParallel401ExchangesOnce() async throws {
        let cfg = IssueServerConfig(url: server, token: nil)
        let results = await withTaskGroup(of: Bool.self) { g -> [Bool] in
            for i in 0..<6 {
                let r = IssueRemote(config: cfg, scope: "")
                g.addTask { await Task.detached { (try? r.list(org: "org-\(i)")) != nil }.value }
            }
            var out: [Bool] = []
            for await ok in g { out.append(ok) }
            return out
        }
        XCTAssertEqual(results, Array(repeating: true, count: 6), "每個請求都要成功")
        XCTAssertEqual(TokenRaceServer.exchanges, 1, "同時遇到 401 只能換一次 token")
        XCTAssertEqual(omixorToken, "omx_1")
    }

    /// 登出之後,背景請求遇到 401 不自動換 token(直接失敗);重新明確登入後恢復
    func testNoSilentReloginAfterLogout() async throws {
        OmixorTokenGate.markLoggedOut(server)
        let r = IssueRemote(config: IssueServerConfig(url: server, token: nil), scope: "")
        do {
            _ = try await Task.detached { try r.list(org: "o") }.value
            XCTFail("登出後要直接失敗")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("已登出"), error.localizedDescription)
        }
        XCTAssertEqual(TokenRaceServer.exchanges, 0, "登出後不能自動換 token")
        // 重新明確登入(存進新 token)
        OmixorAuth.setToken(TokenSet(accessToken: "omx_stale2", tokenType: "Bearer"), server: server)
        XCTAssertFalse(OmixorTokenGate.isLoggedOut(server))
        _ = try await Task.detached { try r.list(org: "o") }.value
        XCTAssertEqual(TokenRaceServer.exchanges, 1)
    }

    /// 換 token 換到一半使用者登出:換到的 token 撤銷、不存,請求失敗
    func testLogoutDuringExchangeRevokesNewToken() async throws {
        TokenRaceServer.exchangeDelay = 0.5
        let r = IssueRemote(config: IssueServerConfig(url: server, token: nil), scope: "")
        let t = Task.detached { () -> Error? in
            do { _ = try r.list(org: "o"); return nil } catch { return error }
        }
        try await Task.sleep(for: .milliseconds(200))   // 已經在換 token
        OmixorTokenGate.markLoggedOut(server)
        TokenVault.setTokens(nil, IssueServerConfig.omixorTokenId, scope: OmixorMode.tokenScope(for: server))
        let err = await t.value
        XCTAssertNotNil(err, "登出後這個請求要失敗")
        XCTAssertEqual(TokenRaceServer.exchanges, 1)
        XCTAssertEqual(TokenRaceServer.revoked, ["omx_1"], "換到的 token 要撤銷")
        XCTAssertNil(omixorToken, "不能偷偷存回新的 token")
    }
}

/// 問題 1:守護服務在第一次同步期間當掉:不能卡在「可用」,要自動重啟到真的可用
@MainActor
final class LocalCoreSyncRaceTests: XCTestCase {
    private var savedLauncher: ((String, String) throws -> IssueServerConfig)!
    private var savedDelays: [TimeInterval] = []
    private var dataDir: URL!
    private var coreDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-syncrace-\(UUID().uuidString)")
        coreDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-syncrace-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        IssueServerConfig.testOverride = nil
        savedLauncher = AppState.localCoreLauncher
        savedDelays = AppState.localCoreRestartDelays
        AppState.localCoreInTests = true
        AppState.localCoreRestartDelays = [0.2, 0.2, 0.2]
    }

    override func tearDown() async throws {
        LocalCore.shared.onUnexpectedExit = nil
        LocalCore.shared.stop()
        AppState.localCoreInTests = false
        AppState.localCoreLauncher = savedLauncher
        AppState.localCoreRestartDelays = savedDelays
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
        try? FileManager.default.removeItem(at: coreDir)
    }

    private func run(killWait: Bool) async throws {
        let bin = try TestCore.binary(), dir = coreDir!
        nonisolated(unsafe) var first = true
        AppState.localCoreLauncher = { user, name in
            let c = try LocalCore.shared.start(binary: bin, dataDir: dir, user: user, name: name, timeout: 20)
            if first, let pid = LocalCore.shared.pid {
                first = false
                if killWait {
                    // 剛就緒就掉(review 的重現情境):等到 LocalCore 發現結束才回傳
                    kill(pid, SIGKILL)
                    let deadline = Date().addingTimeInterval(5)
                    while LocalCore.shared.config != nil, Date() < deadline { usleep(10_000) }
                } else {
                    // App 開始同步、匯入之後才掉
                    DispatchQueue.global().asyncAfter(deadline: .now() + 0.02) { kill(pid, SIGKILL) }
                }
            }
            return c
        }
        let app = AppState()
        app.startLocalCore()
        try await waitUntil(30, "自動重啟後可用") { app.localCore == .ready && LocalCore.shared.config != nil }
        XCTAssertFalse(first)
        // 穩定下來後仍是可用、且真的有程序在跑
        try await Task.sleep(for: .milliseconds(500))
        XCTAssertEqual(app.localCore, .ready)
        XCTAssertNotNil(LocalCore.shared.pid)
        XCTAssertNil(IssueServerConfig.current.unavailable)
    }

    func testDiesRightAfterReady() async throws { try await run(killWait: true) }

    func testDiesDuringInitialSync() async throws { try await run(killWait: false) }
}
