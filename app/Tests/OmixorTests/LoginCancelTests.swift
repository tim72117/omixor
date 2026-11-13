import XCTest
import Security
@testable import Ops

/// 等瀏覽器核准時可以取消:LoopbackCallback 立刻丟 CancellationError、回呼伺服器關掉;連線登入與連遠端 omixor 取消後不顯示錯誤
@MainActor
final class LoginCancelTests: XCTestCase {
    private var dataDir: URL!

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-cancel-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        ConnHTTP.protocolClasses = [StubProtocol.self]
        StubProtocol.log = []
        StubProtocol.handler = { req, _ in
            switch req.url!.path {
            case "/api/cli-auth/start": return (200, ["id": "req-1"])
            case "/api/auth/me": return (401, ["error": "omixor 的登入無效、已過期或已撤銷,請重新登入"])
            default: return (404, ["error": "?"])
            }
        }
        Browser.open = { _ in }
    }

    override func tearDown() async throws {
        Browser.open = { _ in }
        ConnHTTP.protocolClasses = []
        StubProtocol.handler = nil
        OmixorMode.testServers = nil
        TokenVault.testWriteHook = nil
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    /// 本機的回呼伺服器還在不在聽(連得上就是還在)
    private func listening(_ port: UInt16) async -> Bool {
        var req = URLRequest(url: URL(string: "http://127.0.0.1:\(port)/callback")!)
        req.timeoutInterval = 2
        return (try? await URLSession(configuration: .ephemeral).data(for: req)) != nil
    }

    /// 等回呼時取消:不用等逾時,立刻丟 CancellationError
    func testLoopbackWaitCancelsImmediately() async throws {
        let cb = try await LoopbackCallback.start()
        defer { cb.stop() }
        let t = Task { try await cb.waitForCode(timeout: 600) }
        try await Task.sleep(for: .milliseconds(100))
        let started = Date()
        t.cancel()
        do {
            _ = try await t.value
            XCTFail("取消後不該拿到 code")
        } catch is CancellationError {
        } catch {
            XCTFail("要丟 CancellationError:\(error)")
        }
        XCTAssertLessThan(Date().timeIntervalSince(started), 2)
    }

    /// 還沒開始等就已經取消:也一樣立刻結束
    func testLoopbackWaitAlreadyCancelled() async throws {
        let cb = try await LoopbackCallback.start()
        defer { cb.stop() }
        let t = Task {
            withUnsafeCurrentTask { $0?.cancel() }
            return try await cb.waitForCode(timeout: 600)
        }
        do { _ = try await t.value; XCTFail("要被取消") } catch is CancellationError {} catch { XCTFail("\(error)") }
    }

    /// 連線的瀏覽器登入(evoleap 核准)按取消:不顯示錯誤、不存 token、本機回呼伺服器關掉
    func testConnectionLoginCancel() async throws {
        let app = AppState()
        app.startConnections()
        let store = try XCTUnwrap(app.connStore)
        var redirect: String?
        Browser.open = { _ in
            redirect = StubProtocol.log.compactMap { e -> String? in
                guard e.1.hasSuffix("/api/cli-auth/start"), let o = try? JSONSerialization.jsonObject(with: e.2) as? [String: Any] else { return nil }
                return o["redirect_uri"] as? String
            }.last
        }
        let login = Task { await store.login("evoleap-local") }
        for _ in 0..<100 where redirect == nil { try await Task.sleep(for: .milliseconds(20)) }
        let port = try XCTUnwrap(redirect.flatMap { URLComponents(string: $0)?.port }.map(UInt16.init))
        XCTAssertTrue(store.isLoggingIn("evoleap-local"))
        XCTAssertEqual(app.connectionStates["evoleap-local"]?.busy, true)
        store.cancelLogin("evoleap-local")
        await login.value
        XCTAssertFalse(store.isLoggingIn("evoleap-local"))
        XCTAssertEqual(app.connectionStates["evoleap-local"]?.busy, false)
        XCTAssertNil(app.connectionStates["evoleap-local"]?.error, "使用者取消不算錯誤")
        XCTAssertNil(TokenVault.tokens("evoleap-local"))
        let still = await listening(port)
        XCTAssertFalse(still, "取消後回呼伺服器要關掉")
    }

    /// 連遠端 omixor 等 evoleap 核准時按取消:connecting 清掉、不顯示錯誤、留在原本的模式
    func testConnectRemoteCancel() async throws {
        let server = URL(string: "https://omixor.test")!
        let srv = OmixorMode.Server(id: "prod", name: "正式站", url: server, auth: [OmixorMode.AuthOption(id: "evoleap", label: "evoleap 帳號")])
        OmixorMode.testServers = [srv]
        let app = AppState()
        var opened = false
        Browser.open = { _ in opened = true }
        let modeBefore = app.mode
        let connect = Task { await app.connectRemote(srv, auth: "evoleap") }
        for _ in 0..<100 where !opened { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertTrue(opened)
        XCTAssertEqual(app.connecting, "正式站")
        XCTAssertNotNil(app.connectLogin)
        app.cancelConnect()
        await connect.value
        XCTAssertNil(app.connecting)
        XCTAssertNil(app.connectLogin)
        XCTAssertNil(app.connectError)
        XCTAssertEqual(app.mode, modeBefore)
        XCTAssertNil(app.noOrgsServer)
    }

    /// 鑰匙圈寫不進去(舊項目刪不掉、新增回「重複」)時:這次執行仍讀到新的值,刪除也照樣生效,並留下原因;
    /// 原本會一直讀到鑰匙圈裡已撤銷的舊 token(連遠端時 /api/orgs 回 401 → 「讀不到組織清單」)
    func testTokenVaultFallsBackWhenKeychainWriteFails() {
        let scope = "remote/x/"
        TokenVault.setTokens(TokenSet(accessToken: "omx_old", tokenType: "Bearer"), "omixor-issues", scope: scope)
        XCTAssertEqual(TokenVault.tokens("omixor-issues", scope: scope)?.accessToken, "omx_old")
        TokenVault.testWriteHook = { _, _ in errSecDuplicateItem }
        let ok = TokenVault.setTokens(TokenSet(accessToken: "omx_new", tokenType: "Bearer"), "omixor-issues", scope: scope)
        XCTAssertFalse(ok)
        XCTAssertEqual(TokenVault.tokens("omixor-issues", scope: scope)?.accessToken, "omx_new", "寫不進去也要用新的,不能讀到舊的")
        XCTAssertNotNil(TokenVault.lastWriteError)
        TokenVault.setTokens(nil, "omixor-issues", scope: scope)
        XCTAssertNil(TokenVault.tokens("omixor-issues", scope: scope), "刪不掉也要當作已刪除")
        TokenVault.testWriteHook = nil
        XCTAssertTrue(TokenVault.setTokens(nil, "omixor-issues", scope: scope))
        XCTAssertNil(TokenVault.tokens("omixor-issues", scope: scope))
        XCTAssertNil(TokenVault.lastWriteError, "寫成功後清掉原因")
    }

    /// 真的鑰匙圈:改寫既有的項目(SecItemUpdate),讀回來是新的
    func testTokenVaultOverwrites() {
        XCTAssertTrue(TokenVault.setTokens(TokenSet(accessToken: "a", tokenType: "Bearer"), "t", scope: "s/"))
        XCTAssertTrue(TokenVault.setTokens(TokenSet(accessToken: "b", tokenType: "Bearer"), "t", scope: "s/"))
        XCTAssertEqual(TokenVault.tokens("t", scope: "s/")?.accessToken, "b")
        XCTAssertTrue(TokenVault.setTokens(nil, "t", scope: "s/"))
        XCTAssertNil(TokenVault.tokens("t", scope: "s/"))
    }
}
