import XCTest
@testable import Ops

@MainActor
final class MCPTests: XCTestCase {
    private var app: AppState!
    private var mcp: MCPHandler!
    private var nextId = 0

    private var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-mcp-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        app = AppState()
        mcp = MCPHandler(app: app)
    }

    override func tearDown() async throws {
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    // MARK: - 小工具

    private func rpc(_ method: String, _ params: [String: Any] = [:]) async throws -> [String: Any] {
        nextId += 1
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": nextId, "method": method, "params": params]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let out = try XCTUnwrap(raw)
        let obj = try XCTUnwrap(try JSONSerialization.jsonObject(with: out) as? [String: Any])
        XCTAssertEqual(obj["id"] as? Int, nextId)
        return obj
    }

    /// 呼叫工具,回傳解析後的結果與 isError
    private func tool(_ name: String, _ args: [String: Any] = [:]) async throws -> (Any, Bool) {
        let r = try await rpc("tools/call", ["name": name, "arguments": args])
        let result = try XCTUnwrap(r["result"] as? [String: Any], "\(r)")
        let text = try XCTUnwrap((result["content"] as? [[String: Any]])?.first?["text"] as? String)
        let isError = result["isError"] as? Bool ?? false
        if isError { return (text, true) }
        return (try JSONSerialization.jsonObject(with: Data(text.utf8), options: .fragmentsAllowed), false)
    }

    private func ok(_ name: String, _ args: [String: Any] = [:]) async throws -> [String: Any] {
        let (out, isError) = try await tool(name, args)
        XCTAssertFalse(isError, "\(name) 失敗:\(out)")
        return try XCTUnwrap(out as? [String: Any])
    }

    // MARK: - 協定

    func testInitializeNegotiatesVersion() async throws {
        let r = try await rpc("initialize", ["protocolVersion": "2025-03-26", "capabilities": [:],
                                       "clientInfo": ["name": "test-client", "version": "1"]])
        let result = try XCTUnwrap(r["result"] as? [String: Any])
        XCTAssertEqual(result["protocolVersion"] as? String, "2025-03-26")
        XCTAssertNotNil((result["capabilities"] as? [String: Any])?["tools"])
        XCTAssertEqual(app.mcp.lastClient, "test-client")

        let r2 = try await rpc("initialize", ["protocolVersion": "1999-01-01"])
        XCTAssertEqual((r2["result"] as? [String: Any])?["protocolVersion"] as? String, MCPHandler.supportedVersions[0])
    }

    func testNotificationHasNoResponse() async throws {
        let msg = #"{"jsonrpc":"2.0","method":"notifications/initialized"}"#
        let none = await mcp.handle(Data(msg.utf8))
        XCTAssertNil(none)
    }

    func testUnknownMethodAndBadJSON() async throws {
        let r = try await rpc("does/not/exist")
        XCTAssertEqual((r["error"] as? [String: Any])?["code"] as? Int, -32601)
        let badRaw = await mcp.handle(Data("{nope".utf8))
        let bad = try XCTUnwrap(badRaw)
        let obj = try JSONSerialization.jsonObject(with: bad) as? [String: Any]
        XCTAssertEqual((obj?["error"] as? [String: Any])?["code"] as? Int, -32700)
    }

    func testToolsListHasSchemas() async throws {
        let r = try await rpc("tools/list")
        let tools = try XCTUnwrap((r["result"] as? [String: Any])?["tools"] as? [[String: Any]])
        XCTAssertEqual(tools.count, MCPHandler.tools.count)
        for t in tools {
            let schema = try XCTUnwrap(t["inputSchema"] as? [String: Any], "\(t["name"] ?? "")")
            XCTAssertEqual(schema["type"] as? String, "object")
        }
        // 每個工具都要有實作(呼叫不會落到「沒有這個工具」)
        for t in MCPHandler.tools {
            let (out, _) = try await tool(t.name, [:])
            XCTAssertFalse("\(out)".contains("沒有這個工具"), t.name)
        }
    }

    // MARK: - 讀取

    func testOverview() async throws {
        let o = try await ok("ops_overview")
        let counts = try XCTUnwrap(o["counts"] as? [String: Any])
        XCTAssertEqual(counts["firingAlerts"] as? Int, app.firingAlerts.count)
        XCTAssertEqual((o["services"] as? [Any])?.count, app.services.count)
    }

    func testEveryCollectionEncodes() async throws {
        for c in MCPHandler.collections {
            let out = try await ok("ops_list", ["collection": c, "limit": 200])
            XCTAssertGreaterThan(out["total"] as? Int ?? 0, 0, c)
        }
    }

    func testListWhereAndSearch() async throws {
        let firing = try await ok("ops_list", ["collection": "alerts", "where": ["state": "firing"]])
        XCTAssertEqual(firing["total"] as? Int, app.alerts.filter { $0.state == .firing }.count)

        let svc = app.services[0]
        let byService = try await ok("ops_list", ["collection": "services", "search": svc.name.uppercased()])
        XCTAssertGreaterThanOrEqual(byService["total"] as? Int ?? 0, 1)

        // 執行者欄位比對 id
        let author = try XCTUnwrap(app.pullRequests.first { if case .ai = $0.author { return true }; return false })
        guard case .ai(let aiId) = author.author else { return XCTFail() }
        let prs = try await ok("ops_list", ["collection": "pull_requests", "where": ["author": aiId]])
        XCTAssertGreaterThanOrEqual(prs["total"] as? Int ?? 0, 1)
    }

    func testListCompactsSeriesButGetIsFull() async throws {
        let list = try await ok("ops_list", ["collection": "servers", "limit": 1])
        let first = try XCTUnwrap((list["items"] as? [[String: Any]])?.first)
        XCTAssertLessThanOrEqual((first["cpuSeries"] as? [Any])?.count ?? 0, 3)

        let id = app.servers[0].id
        let full = try await ok("ops_get", ["collection": "servers", "id": id])
        XCTAssertEqual((full["cpuSeries"] as? [Any])?.count, app.servers[0].cpuSeries.count)
        // 以列舉為 key 的字典要編成物件
        let svc = try await ok("ops_get", ["collection": "services", "id": app.services[0].id])
        XCTAssertNotNil(svc["versions"] as? [String: Any])
    }

    func testGetPipelineRunByNumber() async throws {
        let run = app.pipelineRuns[0]
        let out = try await ok("ops_get", ["collection": "pipeline_runs", "id": "\(run.id)"])
        XCTAssertEqual(out["id"] as? Int, run.id)
        let (msg, isError) = try await tool("ops_get", ["collection": "alerts", "id": "NOPE-1"])
        XCTAssertTrue(isError)
        XCTAssertTrue("\(msg)".contains("找不到"))
    }

    // MARK: - 寫入

    func testAcknowledgeAlertUpdatesAppAndActivity() async throws {
        let alert = try XCTUnwrap(app.alerts.first { $0.state == .firing })
        let out = try await ok("ops_alert_update", ["alert_id": alert.id, "action": "acknowledge", "ai_note": "看起來是 DB 鎖"])
        XCTAssertEqual(out["state"] as? String, "acknowledged")
        let updated = try XCTUnwrap(app.alerts.first { $0.id == alert.id })
        XCTAssertEqual(updated.state, .acknowledged)
        XCTAssertEqual(updated.acknowledgedBy, .mcp)
        XCTAssertEqual(updated.aiNote, "看起來是 DB 鎖")
        XCTAssertEqual(app.activity.first?.actor, .mcp)
        XCTAssertEqual(app.name(of: .mcp), "MCP 用戶端")
    }

    func testCreateAlertAndDeclareIncident() async throws {
        let svc = app.services[0].id
        let alert = try await ok("ops_alert_create", ["title": "測試告警", "severity": "critical", "service": svc,
                                                "rule": "x > 1", "value": "2"])
        let alertId = try XCTUnwrap(alert["id"] as? String)
        XCTAssertEqual(app.alerts.first?.id, alertId)

        let before = app.incidents.count
        let inc = try await ok("ops_incident_declare", ["title": "測試事件", "sev": 2, "services": [svc],
                                                  "impact": "部分使用者無法登入", "alert_ids": [alertId]])
        let incId = try XCTUnwrap(inc["id"] as? String)
        XCTAssertEqual(app.incidents.count, before + 1)
        XCTAssertEqual(app.alerts.first { $0.id == alertId }?.incidentId, incId)

        _ = try await ok("ops_incident_update", ["incident_id": incId, "status": "identified", "note": "找到原因",
                                           "finding": "連線池耗盡", "ai_summary": "摘要"])
        let i = try XCTUnwrap(app.incident(incId))
        XCTAssertEqual(i.status, .identified)
        XCTAssertEqual(i.aiSummary, "摘要")
        XCTAssertTrue(i.timeline.contains { $0.kind == .aiFinding && $0.text == "連線池耗盡" })

        let wi = try await ok("ops_work_item_create", ["title": "加大連線池", "type": "incident", "incident_id": incId])
        let wid = try XCTUnwrap(wi["id"] as? String)
        XCTAssertTrue(app.incident(incId)!.followUps.contains(wid))
        _ = try await ok("ops_work_item_move", ["id": wid, "status": "inProgress"])
        XCTAssertEqual(app.workItems.first { $0.id == wid }?.status, .inProgress)
    }

    func testValidationErrors() async throws {
        var (msg, isError) = try await tool("ops_alert_create", ["title": "x", "severity": "huge", "service": app.services[0].id,
                                                          "rule": "r", "value": "v"])
        XCTAssertTrue(isError); XCTAssertTrue("\(msg)".contains("severity"))
        (msg, isError) = try await tool("ops_incident_declare", ["title": "x", "sev": 9, "impact": "y"])
        XCTAssertTrue(isError)
        (msg, isError) = try await tool("ops_alert_create", ["title": "x", "severity": "info", "service": "nope", "rule": "r", "value": "v"])
        XCTAssertTrue(isError); XCTAssertTrue("\(msg)".contains("找不到服務"))
    }

    func testRetryPipelineAndRequestApproval() async throws {
        let run = app.pipelineRuns[0]
        let out = try await ok("ops_pipeline_update", ["run_id": run.id, "action": "retry"])
        let newRun = try XCTUnwrap(out["newRun"] as? [String: Any])
        XCTAssertEqual(newRun["status"] as? String, "running")
        XCTAssertEqual(app.pipelineRuns[0].triggeredBy, .mcp)

        let pending = app.pendingApprovals.count
        let req = try await ok("ops_request_approval", ["title": "擴容", "action": "kubectl scale ...", "rationale": "佇列積壓",
                                                  "rollback_plan": "縮回 4", "risk": "medium", "evidence": ["queue=1200"]])
        XCTAssertEqual(req["state"] as? String, "pending")
        XCTAssertEqual(app.pendingApprovals.count, pending + 1)
    }

    func testNavigate() async throws {
        let inc = app.incidents[0].id
        _ = try await ok("ops_navigate", ["section": "incidents", "focus_id": inc, "env": "staging"])
        XCTAssertEqual(app.section, .incidents)
        XCTAssertEqual(app.focusId, inc)
        XCTAssertEqual(app.env, .staging)
    }

    // MARK: - HTTP

    func testHTTPParse() async throws {
        let raw = "POST /mcp?x=1 HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 2\r\nOrigin: http://localhost:3000\r\n\r\n{}"
        guard case .complete(let req) = HTTPRequest.parse(Data(raw.utf8), maxBody: 1000) else { return XCTFail() }
        XCTAssertEqual(req.method, "POST")
        XCTAssertEqual(req.path, "/mcp")
        XCTAssertEqual(req.headers["origin"], "http://localhost:3000")
        XCTAssertEqual(req.body, Data("{}".utf8))

        let partial = "POST /mcp HTTP/1.1\r\nContent-Length: 10\r\n\r\n{}"
        guard case .incomplete = HTTPRequest.parse(Data(partial.utf8), maxBody: 1000) else { return XCTFail() }
    }

    func testHTTPRouteRejectsForeignOrigin() async throws {
        let echo: @Sendable (Data) async -> Data? = { $0 }
        let evil = HTTPRequest(method: "POST", path: "/mcp", headers: ["origin": "https://evil.example"], body: Data("{}".utf8))
        let r1 = await MCPServer.route(evil, handle: echo)
        XCTAssertEqual(r1.status, 403)
        let get = HTTPRequest(method: "GET", path: "/mcp", headers: [:], body: Data())
        let r2 = await MCPServer.route(get, handle: echo)
        XCTAssertEqual(r2.status, 405)
        let note = HTTPRequest(method: "POST", path: "/mcp", headers: [:], body: Data())
        let r3 = await MCPServer.route(note, handle: { _ in nil })
        XCTAssertEqual(r3.status, 202)
    }
}
