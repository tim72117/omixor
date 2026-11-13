import XCTest
@testable import Omixor

final class PairLinkTests: XCTestCase {
    func testParseLink() throws {
        let l = try XCTUnwrap(PairLink(url: URL(string: "omixor://pair?server=https://omixor.example.com/&code=abcd-2345")!))
        XCTAssertEqual(l.server.absoluteString, "https://omixor.example.com")
        XCTAssertEqual(l.code, "ABCD2345")
        XCTAssertNotNil(PairLink(scanned: "  omixor://pair?server=http%3A%2F%2F127.0.0.1%3A8080&code=ABCD2345\n"))
        XCTAssertNil(PairLink(url: URL(string: "omixor://other?server=https://x&code=ABCD2345")!))
        XCTAssertNil(PairLink(url: URL(string: "https://pair?server=https://x&code=ABCD2345")!))
        XCTAssertNil(PairLink(url: URL(string: "omixor://pair?server=https://x&code=ABCD234O")!), "O 不在字母表")
        XCTAssertNil(PairLink(url: URL(string: "omixor://pair?server=ftp://x&code=ABCD2345")!))
    }

    func testServerDisplayShowsSchemeHostPort() {
        let https = PairLink(server: URL(string: "https://omixor.example.com")!, code: "ABCD2345")
        XCTAssertEqual(https.serverDisplay, "https://omixor.example.com:443")
        XCTAssertFalse(https.isInsecure)
        let http = PairLink(server: URL(string: "http://evil.example:8080/base")!, code: "ABCD2345")
        XCTAssertEqual(http.serverDisplay, "http://evil.example:8080/base")
        XCTAssertTrue(http.isInsecure)
        XCTAssertEqual(PairLink.display(URL(string: "http://127.0.0.1")!), "http://127.0.0.1:80")
    }

    func testNormalize() {
        XCTAssertEqual(PairLink.normalizeCode(" abcd 2345 "), "ABCD2345")
        XCTAssertNil(PairLink.normalizeCode("ABCD234"))
        XCTAssertNil(PairLink.normalizeCode("ABCD2341"), "1 不在字母表")
        XCTAssertEqual(PairLink.normalizeServer("omixor.example.com")?.absoluteString, "https://omixor.example.com")
        XCTAssertEqual(PairLink.normalizeServer("127.0.0.1:8080/")?.absoluteString, "http://127.0.0.1:8080")
        XCTAssertEqual(PairLink.normalizeServer("192.168.1.5:8080")?.absoluteString, "http://192.168.1.5:8080")
        XCTAssertEqual(PairLink.normalizeServer("http://localhost:9000")?.absoluteString, "http://localhost:9000")
        XCTAssertNil(PairLink.normalizeServer(""))
        XCTAssertNil(PairLink.normalizeServer("ftp://x"))
    }
}

final class KeychainTests: XCTestCase {
    func testSaveLoadClear() {
        // 測試專用的 service 名稱,不碰 App 真的項目
        let k = Keychain(service: "com.tim72117.omixor.ios.tests.\(UUID().uuidString)")
        defer { k.clear() }
        XCTAssertNil(k.load())
        let s = SavedSession(server: URL(string: "https://x.example")!, token: "omx_test", tokenId: "t_1")
        XCTAssertTrue(k.save(s))
        XCTAssertEqual(k.load(), s)
        var s2 = s
        s2.token = "omx_test2"
        XCTAssertTrue(k.save(s2), "已存在時更新")
        XCTAssertEqual(k.load()?.token, "omx_test2")
        k.clear()
        XCTAssertNil(k.load())
    }
}

@MainActor
final class SessionTests: XCTestCase {
    var keychain: Keychain!
    var defaults: UserDefaults!

    override func setUp() async throws {
        keychain = Keychain(service: "com.tim72117.omixor.ios.tests.\(UUID().uuidString)")
        defaults = UserDefaults(suiteName: "omixor.tests.\(UUID().uuidString)")
    }

    override func tearDown() async throws { keychain.clear() }

    private let user = #"{"id":"u1","email":"tim@example.com","name":"Tim","role":"member"}"#

    func testPairStoresTokenAndLoadsOrgs() async throws {
        StubProtocol.reset { req, body in
            switch req.url!.path {
            case "/api/auth/pair/claim":
                XCTAssertNil(req.value(forHTTPHeaderField: "Authorization"), "配對不用登入")
                XCTAssertEqual(json(body)["code"] as? String, "ABCD2345")
                return .init(status: 200, body: #"{"token":"omx_new","tokenId":"t_9","expiresAt":"2027-02-01T00:00:00Z","user":\#(self.user)}"#)
            case "/api/orgs":
                XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer omx_new")
                return .init(status: 200, body: #"{"result":[{"id":"o1","name":"組織一","myRole":"admin","createdAt":"2026-01-01T00:00:00Z"}]}"#)
            default:
                return .init(status: 404, body: #"{"error":"x"}"#)
            }
        }
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        XCTAssertEqual(s.phase, .signedOut)
        try await s.pair(PairLink(server: URL(string: "https://omixor.example.com")!, code: "ABCD2345"))
        XCTAssertEqual(s.phase, .signedIn)
        XCTAssertEqual(keychain.load()?.token, "omx_new")
        XCTAssertEqual(s.user?.email, "tim@example.com")
        XCTAssertEqual(s.org?.id, "o1", "只有一個組織時自動選")
        XCTAssertTrue(s.org?.isAdmin == true)

        // 重新打開 App:從鑰匙圈還原
        let again = Session(keychain: keychain, defaults: defaults)
        XCTAssertEqual(again.phase, .signedIn)
        XCTAssertEqual(again.saved?.tokenId, "t_9")
    }

    func testPairFailureShowsServerMessage() async {
        StubProtocol.reset { _, _ in .init(status: 401, body: #"{"error":"配對碼無效、已過期或已用過,請在 Mac App 重新產生"}"#) }
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        do {
            try await s.pair(PairLink(server: URL(string: "https://x.example")!, code: "ABCD2345"))
            XCTFail("要失敗")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("配對碼無效"))
        }
        XCTAssertEqual(s.phase, .signedOut)
        XCTAssertNil(keychain.load())
    }

    func testLogoutRevokesAndClears() async throws {
        keychain.save(SavedSession(server: URL(string: "https://x.example")!, token: "omx_old", tokenId: "t_1"))
        StubProtocol.reset { _, _ in .init(status: 200, body: #"{"result":true}"#) }
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        await s.logout()
        XCTAssertEqual(StubProtocol.requests.map { $0.0.url!.path }, ["/api/auth/logout"])
        XCTAssertEqual(StubProtocol.requests.first?.0.value(forHTTPHeaderField: "Authorization"), "Bearer omx_old")
        XCTAssertEqual(s.phase, .signedOut)
        XCTAssertNil(keychain.load())
    }

    func testUnauthorizedSignsOut() async {
        keychain.save(SavedSession(server: URL(string: "https://x.example")!, token: "omx_old", tokenId: "t_1"))
        StubProtocol.reset { _, _ in .init(status: 401, body: #"{"error":"omixor 的登入無效、已過期或已撤銷,請重新登入"}"#) }
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        await s.loadOrgs()
        XCTAssertEqual(s.phase, .signedOut)
        XCTAssertNotNil(s.notice)
        XCTAssertNil(keychain.load())
    }

    func testOpenURLSetsPendingLink() {
        let s = Session(keychain: keychain, defaults: defaults)
        s.open(URL(string: "omixor://pair?server=https://x.example&code=ABCD2345")!)
        XCTAssertEqual(s.pendingLink?.code, "ABCD2345")
        s.pendingLink = nil
        s.open(URL(string: "https://example.com")!)
        XCTAssertNil(s.pendingLink)
    }

    private func stubClaim() {
        StubProtocol.reset { req, _ in
            switch req.url!.path {
            case "/api/auth/pair/claim":
                return .init(status: 200, body: #"{"token":"omx_new","tokenId":"t_9","expiresAt":"2027-02-01T00:00:00Z","user":\#(self.user)}"#)
            case "/api/orgs":
                return .init(status: 200, body: #"{"result":[]}"#)
            default:
                return .init(status: 200, body: #"{"result":true}"#)
            }
        }
    }

    func testOpenURLSignedOutDoesNotClaim() async {
        stubClaim()
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        s.open(URL(string: "omixor://pair?server=https://evil.example&code=ABCD2345")!)
        // 讓可能的背景工作有機會跑
        await Task.yield()
        try? await Task.sleep(for: .milliseconds(50))
        XCTAssertTrue(StubProtocol.requests.isEmpty, "連結進來不能自動 claim")
        XCTAssertEqual(s.phase, .signedOut)
        XCTAssertNil(keychain.load())
        XCTAssertEqual(s.pendingLink?.server.host, "evil.example", "等使用者確認")
    }

    func testOpenURLSignedInDoesNotClaim() async {
        keychain.save(SavedSession(server: URL(string: "https://x.example")!, token: "omx_old", tokenId: "t_1"))
        stubClaim()
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        s.open(URL(string: "omixor://pair?server=https://evil.example&code=ABCD2345")!)
        try? await Task.sleep(for: .milliseconds(50))
        XCTAssertTrue(StubProtocol.requests.isEmpty)
        XCTAssertEqual(keychain.load()?.token, "omx_old")
        XCTAssertNotNil(s.pendingLink)
    }

    func testConfirmPendingClaims() async {
        stubClaim()
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        await s.confirmPending()
        XCTAssertTrue(StubProtocol.requests.isEmpty, "沒有待確認的連結時什麼都不做")
        s.open(URL(string: "omixor://pair?server=https://omixor.example.com&code=ABCD2345")!)
        await s.confirmPending()
        XCTAssertEqual(StubProtocol.requests.first?.0.url?.host, "omixor.example.com")
        XCTAssertEqual(StubProtocol.requests.first?.0.url?.path, "/api/auth/pair/claim")
        XCTAssertEqual(s.phase, .signedIn)
        XCTAssertEqual(keychain.load()?.token, "omx_new")
        XCTAssertNil(s.pendingLink)
        XCTAssertNil(s.pendingError)
    }

    func testConfirmPendingFailureKeepsLinkAndShowsError() async {
        StubProtocol.reset { _, _ in .init(status: 401, body: #"{"error":"配對碼無效"}"#) }
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        s.open(URL(string: "omixor://pair?server=https://x.example&code=ABCD2345")!)
        await s.confirmPending()
        XCTAssertEqual(s.phase, .signedOut)
        XCTAssertNotNil(s.pendingLink, "失敗時留在確認畫面")
        XCTAssertTrue(s.pendingError?.contains("配對碼無效") == true)
    }

    func testCancelPending() async {
        stubClaim()
        let s = Session(keychain: keychain, defaults: defaults)
        s.session = StubProtocol.session()
        s.open(URL(string: "omixor://pair?server=https://evil.example&code=ABCD2345")!)
        s.cancelPending()
        XCTAssertNil(s.pendingLink)
        await s.confirmPending()
        XCTAssertTrue(StubProtocol.requests.isEmpty, "取消後不會 claim")
        XCTAssertEqual(s.phase, .signedOut)
    }
}

final class SafeLinkTests: XCTestCase {
    private func links(_ md: String) -> [URL] {
        MarkdownText.inline(md).runs.compactMap(\.link)
    }

    func testMarkdownDropsNonWebLinks() {
        XCTAssertTrue(links("[點我](omixor://pair?server=https://evil.example&code=ABCD2345)").isEmpty, "omixor:// 不能變成連結")
        XCTAssertTrue(links("<omixor://pair?server=https://evil.example&code=ABCD2345>").isEmpty)
        XCTAssertTrue(links("[x](javascript:alert(1))").isEmpty)
        XCTAssertTrue(links("[x](tel:123)").isEmpty)
        XCTAssertEqual(String(MarkdownText.inline("[點我](omixor://pair?code=ABCD2345)").characters), "點我", "文字留著")
        XCTAssertEqual(links("[網站](https://omixor.example.com)").map(\.absoluteString), ["https://omixor.example.com"])
        XCTAssertEqual(links("[信](mailto:a@example.com)").count, 1)
        XCTAssertEqual(links("[本機](http://127.0.0.1:8080)").count, 1)
    }

    func testSafeLinkHelpers() {
        XCTAssertTrue(SafeLink.isAllowed(URL(string: "HTTPS://x.example")!))
        XCTAssertFalse(SafeLink.isAllowed(URL(string: "omixor://pair")!))
        XCTAssertNil(SafeLink.url("omixor://pair?server=https://x&code=ABCD2345"))
        XCTAssertNil(SafeLink.url("httpx://x.example"))
        XCTAssertNotNil(SafeLink.url(" https://x.example "))
    }
}
