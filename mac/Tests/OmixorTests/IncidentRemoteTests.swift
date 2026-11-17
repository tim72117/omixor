import XCTest
@testable import Ops

/// 假的 core server 的 /api/orgs/<組織>/incidents:記下請求、照收到的內容回一筆事件,用來檢查 app 送出的欄位與解碼。
/// 規則本身(編號、狀態、權限)由核心強制,這裡不重做。
final class FakeIncidentServer: URLProtocol {
    nonisolated(unsafe) static var requests: [(method: String, path: String, body: [String: Any])] = []
    nonisolated(unsafe) static var incidents: [[String: Any]] = []

    static let now = "2026-11-13T01:02:03Z"

    static func reset() {
        requests = []
        incidents = [["id": "INC-2001", "title": "付款延遲", "sev": 2, "status": "identified", "services": ["payments"],
                      "commander": "tim@example.com", "startedAt": now, "impact": "延遲",
                      "timeline": [["id": "not-a-uuid", "date": now, "actor": ["kind": "person", "id": "tim@example.com"], "kind": "status", "text": "宣告"],
                                   ["id": "0000A11C-0000-4000-8000-000000000001", "date": now, "actor": ["kind": "system"], "kind": "alert", "text": "告警"]],
                      "followUps": [String](), "alertIds": ["ALT-5121"], "createdBy": "tim@example.com"]]
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    private func reply(_ code: Int, _ obj: Any) {
        let resp = HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: try! JSONSerialization.data(withJSONObject: obj))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func startLoading() {
        var data = request.httpBody ?? Data()
        if data.isEmpty, let s = request.httpBodyStream {
            s.open()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable { let n = s.read(&buf, maxLength: buf.count); if n <= 0 { break }; data.append(buf, count: n) }
            s.close()
        }
        let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        let method = request.httpMethod ?? "GET"
        let url = request.url!
        Self.requests.append((method, url.path, body))
        let p = url.path.split(separator: "/").map(String.init)   // api orgs <org> incidents …
        guard p.count >= 4, p[3] == "incidents" else { return reply(200, ["result": [Any]()]) }
        let rest = Array(p.dropFirst(4))
        switch (method, rest.count) {
        case ("GET", 0):
            return reply(200, ["result": Self.incidents])
        case ("POST", 0):
            var inc: [String: Any] = ["id": "INC-2002", "title": body["title"] ?? "", "sev": body["sev"] ?? 3, "status": "investigating",
                                      "services": body["services"] ?? [String](), "commander": body["commander"] ?? "",
                                      "startedAt": body["startedAt"] ?? Self.now, "impact": body["impact"] ?? "",
                                      "timeline": body["timeline"] ?? [["id": UUID().uuidString, "date": Self.now, "actor": body["actor"] ?? ["kind": "system"],
                                                                        "kind": "status", "text": "宣告"]],
                                      "followUps": [String]()]
            if let a = body["alertIds"] { inc["alertIds"] = a }
            Self.incidents.insert(inc, at: 0)
            return reply(200, ["result": inc])
        case ("POST", 1):
            guard let i = Self.incidents.firstIndex(where: { $0["id"] as? String == rest[0] }) else {
                return reply(404, ["error": "找不到事件 \(rest[0])"])
            }
            var inc = Self.incidents[i]
            if let s = body["status"] as? String { inc["status"] = s }
            if let s = body["aiSummary"] as? String { inc["aiSummary"] = s }
            if let f = body["addFollowUps"] as? [String] { inc["followUps"] = (inc["followUps"] as? [String] ?? []) + f }
            Self.incidents[i] = inc
            return reply(200, ["result": inc])
        default:
            return reply(404, ["error": "?"])
        }
    }
}

@MainActor
final class IncidentRemoteTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-incidents-remote-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        IssueRemote.protocolClasses = [FakeIncidentServer.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: "tok-1")
        FakeIncidentServer.reset()
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    /// 讀:伺服器的事件換掉範例資料;不是 UUID 的時間軸 id 換成固定的 UUID;關聯告警補上 incidentId
    func testLoadFromServer() throws {
        let app = AppState()
        app.currentOrgId = "~omixor"
        if let i = app.alerts.firstIndex(where: { $0.id == "ALT-5121" }) { app.alerts[i].incidentId = nil }
        let store = app.startIncidents()
        XCTAssertNil(store.remoteError)
        XCTAssertEqual(app.incidents.map(\.id), ["INC-2001"])
        let inc = try XCTUnwrap(app.incidents.first)
        XCTAssertEqual(inc.status, .identified)
        XCTAssertEqual(inc.timeline.count, 2)
        XCTAssertEqual(inc.timeline[0].id, IncidentJSON.uuid("not-a-uuid"))
        XCTAssertEqual(inc.timeline[1].actor, .system)
        if app.alerts.contains(where: { $0.id == "ALT-5121" }) {
            XCTAssertEqual(app.alerts.first { $0.id == "ALT-5121" }?.incidentId, "INC-2001")
        }
        XCTAssertTrue(FakeIncidentServer.requests.contains { $0.method == "GET" && $0.path == "/api/orgs/omixor/incidents" })
    }

    /// 寫:宣告與更新都送到伺服器(帶 actor、時間軸、告警標題),回來的內容換進畫面
    func testDeclareAndUpdateGoToServer() throws {
        let app = AppState()
        app.currentOrgId = "~omixor"
        let store = app.startIncidents()
        let alert = try XCTUnwrap(app.alerts.first)
        let inc = try store.declare(title: "新事件", sev: 1, services: ["payments"], commander: "tim", impact: "全掛",
                                    alertIds: [alert.id], by: .person("tim"))
        XCTAssertEqual(inc.id, "INC-2002")
        XCTAssertEqual(app.incidents.first?.id, "INC-2002")
        XCTAssertEqual(app.alerts.first { $0.id == alert.id }?.incidentId, "INC-2002")
        let post = try XCTUnwrap(FakeIncidentServer.requests.last { $0.method == "POST" })
        XCTAssertEqual(post.body["sev"] as? Int, 1)
        XCTAssertEqual((post.body["actor"] as? [String: Any])?["id"] as? String, "tim")
        XCTAssertEqual((post.body["alertTitles"] as? [String: String])?[alert.id], alert.title)
        XCTAssertNotNil(post.body["commanderName"])

        app.updateIncident(id: "INC-2002", status: .resolved, note: "好了")
        XCTAssertEqual(app.incident("INC-2002")?.status, .resolved)
        let up = try XCTUnwrap(FakeIncidentServer.requests.last)
        XCTAssertEqual(up.path, "/api/orgs/omixor/incidents/INC-2002")
        XCTAssertEqual(up.body["status"] as? String, "resolved")
        XCTAssertEqual(up.body["note"] as? String, "好了")

        _ = try store.update(id: "INC-2002", addFollowUps: ["OPS-200"], by: .person("tim"))
        XCTAssertEqual(app.incident("INC-2002")?.followUps, ["OPS-200"])
        XCTAssertThrowsError(try store.update(id: "INC-9", note: "x", by: .person("tim")))
        XCTAssertNotNil(store.remoteError)
    }

    /// 連線不能用(本機服務還沒啟動或中途掉了):宣告與更新丟出錯誤,不會只改記憶體還當成功;畫面上的資料不動
    func testUnavailableThrowsInsteadOfMemoryOnly() throws {
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: "t", unavailable: "本機服務沒有啟動:本機服務意外結束(訊號 9)")
        let app = AppState()
        let before = app.incidents
        let store = app.startIncidents()
        XCTAssertEqual(app.incidents, before, "不能用時保留畫面上的資料")
        XCTAssertThrowsError(try store.declare(title: "x", sev: 3, services: [], commander: "tim", impact: "y", by: .person("tim"))) {
            XCTAssertTrue($0.localizedDescription.contains("意外結束"), $0.localizedDescription)
        }
        let id = try XCTUnwrap(before.first?.id)
        XCTAssertThrowsError(try store.update(id: id, status: .resolved, by: .person("tim")))
        XCTAssertEqual(app.incidents, before, "失敗時不改記憶體")
        XCTAssertNotNil(store.remoteError)
        // 畫面的動作:不改狀態,原因顯示在 errorNotice
        app.updateIncident(id: id, status: .resolved)
        XCTAssertEqual(app.incidents, before)
        XCTAssertTrue(app.errorNotice?.contains("意外結束") ?? false, app.errorNotice ?? "")
        XCTAssertTrue(FakeIncidentServer.requests.isEmpty)
    }

    /// 同上,MCP 的 incident_declare / incident_update 回 isError(看得到原因)
    func testUnavailableMCPReportsError() async throws {
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: "t", unavailable: "本機服務沒有啟動:本機服務意外結束(訊號 9)")
        let app = AppState()
        let before = app.incidents
        let mcp = MCPHandler(app: app)
        func call(_ name: String, _ args: [String: Any]) async throws -> (String, Bool) {
            let req = try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": 1, "method": "tools/call",
                                                                  "params": ["name": name, "arguments": args]])
            let raw = await mcp.handle(req)
            let out = try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any]
            let r = try XCTUnwrap(out?["result"] as? [String: Any])
            return (((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? "", r["isError"] as? Bool ?? false)
        }
        var (text, isError) = try await call("incident_declare", ["title": "x", "sev": 2, "impact": "y"])
        XCTAssertTrue(isError); XCTAssertTrue(text.contains("意外結束"), text)
        let id = try XCTUnwrap(before.first?.id)
        (text, isError) = try await call("incident_update", ["incident_id": id, "status": "resolved"])
        XCTAssertTrue(isError); XCTAssertTrue(text.contains("意外結束"), text)
        XCTAssertEqual(app.incidents, before)
    }

    /// 背景重讀(定時與切換組織用):讀到的換進畫面,主執行緒不等網路
    func testReloadInBackground() async throws {
        let app = AppState()
        app.currentOrgId = "~omixor"
        let store = IncidentStore(app: app)
        app.incidents = []
        await store.reload()
        XCTAssertEqual(app.incidents.map(\.id), ["INC-2001"])
    }
}
