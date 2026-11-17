import XCTest
@testable import Ops

/// 連遠端 omixor 的完整流程(AppState.connectRemote):對一個真的 core server(TestCore 起的暫存守護服務)
/// 確認存著的登入 → 讀組織清單 → 進入遠端模式。不碰正式鑰匙圈,也不開瀏覽器(token 先存好,不走 evoleap 登入)
@MainActor
final class ConnectRemoteTests: XCTestCase {
    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
    }

    override func tearDown() async throws {
        OmixorMode.testServers = nil
        IssueServerConfig.testOverride = nil
    }

    func testConnectRemoteReadsOrgs() async throws {
        let cfg = try TestCore.start(self)
        let srv = OmixorMode.Server(id: "local-cloud", name: "本機雲端版(測試)", url: cfg.url,
                                    auth: [OmixorMode.AuthOption(id: "evoleap", label: "evoleap 帳號")])
        OmixorMode.testServers = [srv]
        OmixorAuth.setToken(TokenSet(accessToken: try XCTUnwrap(cfg.token), tokenType: "Bearer"), server: cfg.url)
        defer { OmixorAuth.setToken(nil, server: cfg.url) }

        let st = await OmixorAuth.check(server: cfg.url)
        XCTAssertNotNil(st.account, "\(st)")
        do {
            let list = try await OmixorAuth.listOrgs(server: cfg.url)
            XCTAssertFalse(list.isEmpty)
        } catch {
            XCTFail("讀組織清單失敗:\(error.localizedDescription)")
        }

        let app = AppState()
        IssueServerConfig.testOverride = nil
        await app.connectRemote(srv, auth: "evoleap")
        XCTAssertNil(app.connectError)
        XCTAssertEqual(app.mode.mode, OmixorMode.remote)
        XCTAssertTrue(app.orgs.contains { $0.id == "~local-cloud~default" }, "\(app.orgs.map(\.id))")
    }

    /// 重現「讀不到組織清單」:鑰匙圈裡是已撤銷的舊 token,登入換到新 token 時鑰匙圈寫不進去(舊項目刪不掉 → 新增回「重複」)。
    /// 以前 setToken 默默失敗,/api/orgs 還是帶舊 token → 401 → 「讀不到 … 的組織清單」;現在這次執行改用新 token
    func testNewTokenUsedEvenIfKeychainWriteFails() async throws {
        let cfg = try TestCore.start(self)
        OmixorMode.testServers = [OmixorMode.Server(id: "local-cloud", name: "本機雲端版(測試)", url: cfg.url, auth: [])]
        defer { TokenVault.testWriteHook = nil; OmixorAuth.setToken(nil, server: cfg.url) }
        OmixorAuth.setToken(TokenSet(accessToken: "omx_revoked", tokenType: "Bearer"), server: cfg.url)
        let st = await OmixorAuth.check(server: cfg.url)
        XCTAssertEqual(st, .needLogin, "已撤銷的 token:要重新登入")
        do {
            _ = try await OmixorAuth.listOrgs(server: cfg.url)
            XCTFail("舊 token 要 401")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("HTTP 401"), "要帶出原因:\(error.localizedDescription)")
        }
        TokenVault.testWriteHook = { _, _ in errSecDuplicateItem }
        XCTAssertFalse(OmixorAuth.setToken(TokenSet(accessToken: try XCTUnwrap(cfg.token), tokenType: "Bearer"), server: cfg.url))
        let list = try await OmixorAuth.listOrgs(server: cfg.url)
        XCTAssertFalse(list.isEmpty)
    }
}
