import XCTest
import SwiftUI
@testable import Ops

/// app 登入 omixor:換 token、撤銷 evoleap token、讀目前帳號、登出(瀏覽器那段在 ApprovalConnector,ConnectionTests 測過)
@MainActor
final class OmixorAuthTests: XCTestCase {
    let server = URL(string: "https://omixor.test")!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        ConnHTTP.protocolClasses = [StubProtocol.self]
        StubProtocol.log = []
        StubProtocol.handler = { req, body in
            let path = req.url?.path ?? ""
            let auth = req.value(forHTTPHeaderField: "Authorization") ?? ""
            switch path {
            case "/api/auth/evoleap":
                let b = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
                if b?["token"] as? String == "evo-ok" {
                    return (200, ["token": "omx_abc", "expiresAt": "2027-02-10T01:02:03.123456Z", "user": ["email": "boss@example.com", "role": "admin"]])
                }
                return (403, ["error": "這個帳號(x@example.com)沒有 omixor 的權限"])
            case "/api/cli-auth/logout":
                return (200, ["ok": true])
            case "/api/auth/me":
                guard auth == "Bearer omx_abc" else { return (401, ["error": "omixor 的登入無效"]) }
                return (200, ["user": ["email": "boss@example.com", "name": "老闆", "role": "admin"], "token": ["device": "omixor app(Mac)", "expiresAt": "2027-02-10T01:02:03Z"]])
            case "/broken/api/auth/me":
                return (500, ["error": "壞掉了"])
            case "/api/orgs":
                guard auth == "Bearer omx_abc" else { return (401, ["error": "omixor 的登入無效"]) }
                return (200, ["result": [["id": "default", "name": "evoleap", "createdAt": "2026-11-12T00:00:00Z", "myRole": "admin"]]])
            case "/api/auth/logout":
                return (200, ["result": true])
            default:
                return (404, ["error": "?"])
            }
        }
    }

    override func tearDown() async throws {
        OmixorAuth.setToken(nil)
        ConnHTTP.protocolClasses = []
        StubProtocol.handler = nil
    }

    private var evo: Connection { Connection.defaults.first { $0.id == "evoleap" }! }
    private var paths: [String] { StubProtocol.log.map { URL(string: $0.1)?.path ?? "" } }

    func testExchangeStoresOmixorTokenAndRevokesEvoleap() async throws {
        let a = try await OmixorAuth.exchange(evo, TokenSet(accessToken: "evo-ok", tokenType: "Bearer"), server: server)
        XCTAssertEqual(a.email, "boss@example.com")
        XCTAssertEqual(a.role, "admin")
        XCTAssertNotNil(a.expiresAt, "有小數秒的時間也要讀得懂")
        XCTAssertEqual(OmixorAuth.token?.accessToken, "omx_abc")
        XCTAssertEqual(paths, ["/api/auth/evoleap", "/api/cli-auth/logout"], "換完要撤銷 evoleap token")
        let me = await OmixorAuth.me(server: server)
        XCTAssertEqual(me?.email, "boss@example.com")
        await OmixorAuth.logout(server: server)
        XCTAssertNil(OmixorAuth.token)
        XCTAssertTrue(paths.contains("/api/auth/logout"))
        let after = await OmixorAuth.me(server: server)
        XCTAssertNil(after)
    }

    func testRejectedStillRevokesEvoleap() async {
        do {
            _ = try await OmixorAuth.exchange(evo, TokenSet(accessToken: "evo-other", tokenType: "Bearer"), server: server)
            XCTFail("要被拒絕")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("沒有 omixor 的權限"), error.localizedDescription)
        }
        XCTAssertEqual(paths, ["/api/auth/evoleap", "/api/cli-auth/logout"], "被拒絕也要撤銷 evoleap token")
        XCTAssertNil(OmixorAuth.token)
    }

    /// 開啟時的檢查:沒存登入 → 登入畫面;401 → 登入畫面;伺服器錯誤 → 連不到;有效 → 進主畫面
    func testCheckStates() async {
        var st = await OmixorAuth.check(server: server)
        XCTAssertEqual(st, .needLogin)
        OmixorAuth.setToken(TokenSet(accessToken: "omx_old", tokenType: "Bearer"))
        st = await OmixorAuth.check(server: server)
        XCTAssertEqual(st, .needLogin, "token 失效要回登入畫面")
        OmixorAuth.setToken(TokenSet(accessToken: "omx_abc", tokenType: "Bearer"))
        st = await OmixorAuth.check(server: server)
        guard case .loggedIn(let a) = st else { return XCTFail("\(st)") }
        XCTAssertEqual(a.email, "boss@example.com")
        XCTAssertEqual(a.name, "老闆", "左下角顯示的名稱來自伺服器")
        XCTAssertEqual(st.account, a)
        st = await OmixorAuth.check(server: URL(string: "https://omixor.test/broken")!)
        if case .unreachable = st {} else { XCTFail("\(st)") }
    }

    /// 選擇連接對象的畫面輸出成圖片(OPS_RENDER_OUT 有設時寫檔,給人看)
    func testRenderLoginView() throws {
        OmixorMode.testServers = [OmixorMode.Server(id: "prod", name: "正式站", url: server, auth: [OmixorMode.AuthOption(id: "evoleap", label: "evoleap 帳號")])]
        defer { OmixorMode.testServers = nil }
        let app = AppState()
        let view = ConnectPicker().environmentObject(app).frame(width: 900, height: 620).background(Color.white)
        let r = ImageRenderer(content: view)
        let img = try XCTUnwrap(r.nsImage)
        XCTAssertGreaterThan(img.size.width, 0)
        if let out = ProcessInfo.processInfo.environment["OPS_RENDER_OUT"],
           let tiff = img.tiffRepresentation, let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) {
            try png.write(to: URL(fileURLWithPath: out).appendingPathComponent("login.png"))
        }
    }

    /// 組織清單:登入後向伺服器取得;沒登入回傳 nil
    func testFetchOrgs() async throws {
        let none = await OmixorAuth.fetchOrgs(server: server)
        XCTAssertNil(none)
        OmixorAuth.setToken(TokenSet(accessToken: "omx_abc", tokenType: "Bearer"))
        let orgs = await OmixorAuth.fetchOrgs(server: server)
        XCTAssertEqual(orgs, [ServerOrg(id: "default", name: "evoleap", createdAt: "2026-11-12T00:00:00Z", myRole: "admin")])
        let o = try XCTUnwrap(orgs?.first?.organization)
        XCTAssertEqual(o.id, "~default", "遠端組織加前綴")
        XCTAssertEqual(o.symbol, "building.2", "沒填圖示用預設")
    }

    /// omixor token 不分組織:換組織(TokenVault.scope)也讀得到
    func testTokenIgnoresOrgScope() {
        OmixorAuth.setToken(TokenSet(accessToken: "omx_abc", tokenType: "Bearer"))
        TokenVault.scope = "other/"
        defer { TokenVault.scope = "" }
        XCTAssertEqual(OmixorAuth.token?.accessToken, "omx_abc")
        XCTAssertNil(TokenVault.tokens(OmixorAuth.tokenId), "不存在組織底下")
    }

    /// 舊版存在預設組織底下的 token 搬到 remote/
    func testMigrateLegacyToken() {
        TokenVault.setTokens(TokenSet(accessToken: "omx_old", tokenType: "Bearer"), OmixorAuth.tokenId, scope: "")
        OmixorAuth.migrateLegacyToken()
        XCTAssertEqual(OmixorAuth.token?.accessToken, "omx_old")
        XCTAssertNil(TokenVault.tokens(OmixorAuth.tokenId, scope: ""))
    }
}
