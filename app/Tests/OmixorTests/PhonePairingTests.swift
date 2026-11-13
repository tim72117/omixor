import XCTest
@testable import Ops

/// 配對手機:連結格式、QR code,與真的守護服務走一次 pair → claim
@MainActor
final class PhonePairingTests: XCTestCase {
    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
    }

    func testLinkAndDisplay() throws {
        let url = PhonePairing.link(server: URL(string: "https://omixor.example.com")!, code: "ABCD2345")
        let c = try XCTUnwrap(URLComponents(url: url, resolvingAgainstBaseURL: false))
        XCTAssertEqual(c.scheme, "omixor")
        XCTAssertEqual(c.host, "pair")
        XCTAssertEqual(c.queryItems?.first { $0.name == "server" }?.value, "https://omixor.example.com")
        XCTAssertEqual(c.queryItems?.first { $0.name == "code" }?.value, "ABCD2345")
        XCTAssertEqual(PhonePairing.display("ABCD2345"), "ABCD-2345")
        XCTAssertNotNil(PhonePairing.qrImage(url.absoluteString))
    }

    func testPairAndClaimAgainstCore() throws {
        let cfg = try TestCore.start(self)
        let code = try PhonePairing.request(config: cfg)
        XCTAssertEqual(code.code.count, 8)
        XCTAssertGreaterThan(code.expiresAt, Date())

        func claim(_ code: String) throws -> (Int, [String: Any]) {
            var req = URLRequest(url: cfg.url.appendingPathComponent("api/auth/pair/claim"))
            req.httpMethod = "POST"
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONSerialization.data(withJSONObject: ["code": code, "device": "測試 iPhone"])
            let (status, data) = try IssueRemote(config: cfg).perform(req)
            return (status, (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:])
        }
        let (s1, r1) = try claim(code.code)
        XCTAssertEqual(s1, 200)
        let tok = try XCTUnwrap(r1["token"] as? String)
        XCTAssertTrue(tok.hasPrefix("omx_"))
        XCTAssertNotEqual(tok, cfg.token, "要發一把新的 token")
        // 新 token 可以用
        var me = URLRequest(url: cfg.url.appendingPathComponent("api/auth/me"))
        me.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
        XCTAssertEqual(try IssueRemote(config: cfg).perform(me).0, 200)
        // 用過就失效
        XCTAssertEqual(try claim(code.code).0, 401)
    }
}
