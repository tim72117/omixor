import XCTest
@testable import Ops

@MainActor
final class SiteVisitorsTests: XCTestCase {
    /// 依 evoleap store.AdminOverview 的格式:Go 的 nil slice 是 null、時間是 RFC 3339(有時帶小數秒)
    static let fixture: [String: Any] = [
        "users": 12, "activeUsers": 5, "guests": 340,
        "lessons": [
            ["lesson": "l0/0.0", "learners": 4, "completed": 2, "avgPercent": 63.5, "guestViews": 120, "guestVisitors": 90,
             "guestWatchedMs": 3_600_000, "guestBots": 7, "guestTracked": 100, "guestReady": 92, "guestFailed": 3,
             "guestPlayed": 40, "guestIdleVisibleMs": 15000, "guestReadyAvgMs": 1800],
            ["lesson": "gate:/courses/tools/0", "guestTracked": 5, "guestBots": 1],
            ["lesson": "page:/", "guestTracked": 30],
            ["lesson": "l0/0.1", "learners": 0, "completed": 0, "avgPercent": 0, "guestViews": 10, "guestVisitors": 8,
             "guestWatchedMs": 0, "guestBots": 0, "guestTracked": 10, "guestReady": 9, "guestFailed": 0,
             "guestPlayed": 2, "guestIdleVisibleMs": 0, "guestReadyAvgMs": 2100],
        ],
        "guestStats": [
            "devices": [["name": "mobile", "count": 70], ["name": "bot", "count": 7]],
            "sources": NSNull(),
            "errors": [["name": "timeout", "count": 3]],
            "idleVisible": [["name": "<10s", "count": 30]],
            "idleVisits": [["lesson": "l0/0.0", "device": "mobile", "source": "google.com", "readyMs": 1500, "loadError": "",
                            "visibleMs": 8000, "events": [["t": 1200, "e": "ready"], ["t": 7000, "e": "leave:/"]],
                            "openedAt": "2026-11-10T08:00:00.123456Z"]],
            "homePaths": [["steps": [["lesson": "page:/", "played": false], ["lesson": "l0/0.0", "played": true]], "count": 9, "played": 6]],
            "homeVisits": 55,
            "homeStays": [["guest": "a1b2c3d4", "device": "desktop", "source": "", "visits": 3, "totalMs": 95000,
                           "longestMs": 60000, "maxScroll": 75, "lastOpened": "2026-11-10T09:30:00Z"]],
            "login": ["visits": 14, "outcomes": [["name": "google", "count": 4]], "next": NSNull(), "fails": NSNull(),
                      "googleReady": 12, "googleReadyAvgMs": 900, "googleFailed": 1, "googleErrors": NSNull()],
        ],
    ]

    func testDecodesGoStyleOverview() throws {
        let d = try SiteOverview.decode(Self.fixture)
        XCTAssertEqual(d.users, 12)
        XCTAssertEqual(d.lessons.count, 4)
        XCTAssertEqual(d.playerLessons.map(\.lesson), ["l0/0.0", "l0/0.1"], "登入門檻與一般頁面不算課")
        XCTAssertEqual(d.gates, [SiteCount(name: "/courses/tools/0", count: 5)])
        XCTAssertEqual(d.guestStats.sources, [], "Go 的 null slice 要變成空陣列")
        XCTAssertEqual(d.guestStats.login.next, [])
        XCTAssertNotNil(d.guestStats.idleVisits.first?.openedAt, "帶小數秒的時間也要解得出來")
        XCTAssertNotNil(d.guestStats.homeStays.first?.lastOpened)
        XCTAssertEqual(d.guestStats.homePaths.first?.steps.last?.played, true)
        let f = d.funnel
        XCTAssertEqual(f.tracked, 110)
        XCTAssertEqual(f.ready, 101)
        XCTAssertEqual(f.played, 42)
        XCTAssertEqual(f.bots, 7)
        XCTAssertEqual(42.percent(of: 110), "38%")
        XCTAssertEqual(0.percent(of: 0), "—")
    }

    func testToleratesMissingFields() throws {
        let d = try SiteOverview.decode(["users": 1, "lessons": [["lesson": "x"]], "extraField": true])
        XCTAssertEqual(d.users, 1)
        XCTAssertEqual(d.lessons.first?.guestTracked, 0)
        XCTAssertEqual(d.guestStats.homeVisits, 0)
    }

    func testRangePathsAndFormat() {
        XCTAssertEqual(SiteRange.all.path, "/api/admin/overview")
        XCTAssertEqual(SiteRange.d7.path, "/api/admin/overview?range=7d")
        XCTAssertEqual(SiteFmt.ms(1500), "1.5 秒")
        XCTAssertEqual(SiteFmt.ms(95000), "1 分 35 秒")
    }

    func testMCPToolReadsThroughConnection() async throws {
        let dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-visitors-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        ConnHTTP.protocolClasses = [StubProtocol.self]
        var seenPaths: [String] = []
        StubProtocol.handler = { req, _ in
            seenPaths.append(req.url!.path + "?" + (req.url!.query ?? ""))
            guard req.value(forHTTPHeaderField: "Authorization") == "Bearer admin-token" else { return (401, ["error": "unauthorized"]) }
            return (200, Self.fixture)
        }
        defer {
            ConnHTTP.protocolClasses = []
            unsetenv("OPS_DATA_DIR")
            try? FileManager.default.removeItem(at: dataDir)
        }

        let app = AppState()
        app.startConnections()
        defer { TokenVault.removeAll("evoleap") }
        let mcp = MCPHandler(app: app)
        func call(_ args: [String: Any]) async throws -> (String, Bool) {
            let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": "ops_site_visitors", "arguments": args]]
            let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
            let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
            return (((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? "", r["isError"] as? Bool ?? false)
        }

        // 還沒登入:回錯誤,不會當機
        var (text, isError) = try await call([:])
        XCTAssertTrue(isError)
        XCTAssertTrue(text.contains("登入"), text)

        TokenVault.setTokens(TokenSet(accessToken: "admin-token", tokenType: "Bearer"), "evoleap")
        (text, isError) = try await call(["range": "7d"])
        XCTAssertFalse(isError, text)
        XCTAssertTrue(seenPaths.contains("/api/admin/overview?range=7d"), "\(seenPaths)")
        let obj = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any])
        XCTAssertEqual((obj["funnel"] as? [String: Any])?["played"] as? Int, 42)
        XCTAssertEqual((obj["lessons"] as? [[String: Any]])?.map { $0["lesson"] as? String }, ["l0/0.0", "l0/0.1"])
        XCTAssertEqual((obj["gates"] as? [[String: Any]])?.first?["views"] as? Int, 5)
        XCTAssertEqual(obj["range"] as? String, "7d")

        (text, isError) = try await call(["range": "2y"])
        XCTAssertTrue(isError)
    }
}
