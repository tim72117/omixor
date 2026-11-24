import XCTest
@testable import Omixor

@MainActor
final class MCPTests: XCTestCase {
    private var app: AppState!
    private var mcp: MCPHandler!
    private var nextId = 0

    private var dataDir: URL!

    override func setUp() async throws {
        try TestCore.start(self)   // 資料一律透過 core server:每個測試一個新的本機守護服務
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-mcp-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
        app = AppState()
        app.features = .allOnForTests   // 範例資料工具(overview、list、alert_create…)要對應的功能開著才能用
        mcp = MCPHandler(app: app)
    }

    override func tearDown() async throws {
        unsetenv("OMIXOR_APP_DATA_DIR")
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
        // 統一的結果形狀:structuredContent 是物件,content[0].text 是同一份 JSON;唯一例外是失敗結果 text 的 error.data 少了大欄位、多 truncated
        let structured = try XCTUnwrap(result["structuredContent"] as? [String: Any], "\(result)")
        let parsed = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any])
        let expected = isError ? Self.withoutLargeErrData(structured) : structured
        XCTAssertEqual(NSDictionary(dictionary: isError ? Self.withoutTruncated(parsed) : parsed), NSDictionary(dictionary: expected), name)
        if isError { return (try XCTUnwrap((structured["error"] as? [String: Any])?["message"] as? String), true) }
        return (structured, false)
    }

    /// 同 core 的 sameExceptLargeErrData:structuredContent 拿掉 error.data 的 current 與超過 300 位元組的欄位,應等於 text(扣掉 truncated)
    private static func withoutLargeErrData(_ sc: [String: Any]) -> [String: Any] {
        guard var e = sc["error"] as? [String: Any], let d = e["data"] as? [String: Any] else { return sc }
        e["data"] = d.filter { k, v in
            k != "current" && ((try? JSONSerialization.data(withJSONObject: v, options: [.fragmentsAllowed]).count) ?? .max) <= MCPResult.errDataValueMax
        }
        return ["error": e]
    }

    private static func withoutTruncated(_ text: [String: Any]) -> [String: Any] {
        guard var e = text["error"] as? [String: Any], var d = e["data"] as? [String: Any] else { return text }
        d["truncated"] = nil
        e["data"] = d
        return ["error": e]
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

    /// 工具名稱拿掉 ops_ 前綴:tools/list 只列新名稱,舊名稱還能呼叫
    func testLegacyToolNames() async throws {
        XCTAssertFalse(MCPHandler.tools.contains { $0.name.hasPrefix("ops_") })
        XCTAssertEqual(MCPHandler.legacyToolName("ops_overview"), "overview")
        XCTAssertEqual(MCPHandler.legacyToolName("ops_nope"), "ops_nope")
        _ = try await ok("ops_overview")
    }

    func testToolsListHasSchemas() async throws {
        let r = try await rpc("tools/list")
        let tools = try XCTUnwrap((r["result"] as? [String: Any])?["tools"] as? [[String: Any]])
        // App 的工具都在;server 可能多提供核心才有的工具(例如 incidents)
        let names = Set(tools.compactMap { $0["name"] as? String })
        XCTAssertTrue(names.isSuperset(of: MCPHandler.tools.map(\.name)), "\(Set(MCPHandler.tools.map(\.name)).subtracting(names))")
        XCTAssertEqual(names.count, tools.count, "工具名稱不能重複")
        // 核心工具 App 沒有實作,清單裡的定義來自 core server(這裡是 TestCore 的本機守護服務),每個都要有
        XCTAssertTrue(names.isSuperset(of: MCPHandler.coreTools), "\(MCPHandler.coreTools.subtracting(names))")
        XCTAssertTrue(Set(MCPHandler.tools.map(\.name)).isDisjoint(with: MCPHandler.coreTools), "核心工具不能在 App 重複定義")
        for t in tools {
            let schema = try XCTUnwrap(t["inputSchema"] as? [String: Any], "\(t["name"] ?? "")")
            XCTAssertEqual(schema["type"] as? String, "object")
        }
        // 每個工具都要有實作(呼叫不會落到「Unknown tool」)
        for t in MCPHandler.tools {
            let (out, _) = try await tool(t.name, [:])
            XCTAssertFalse("\(out)".contains("Unknown tool"), t.name)
        }
    }

    // MARK: - 讀取

    func testOverview() async throws {
        let o = try await ok("overview")
        let counts = try XCTUnwrap(o["counts"] as? [String: Any])
        XCTAssertEqual(counts["firingAlerts"] as? Int, app.firingAlerts.count)
        XCTAssertEqual((o["services"] as? [Any])?.count, app.services.count)
    }

    func testEveryCollectionEncodes() async throws {
        for c in MCPHandler.collections {
            let out = try await ok("list", ["collection": c, "limit": 200])
            XCTAssertGreaterThan(out["total"] as? Int ?? 0, 0, c)
        }
    }

    func testListWhereAndSearch() async throws {
        let firing = try await ok("list", ["collection": "alerts", "where": ["state": "firing"]])
        XCTAssertEqual(firing["total"] as? Int, app.alerts.filter { $0.state == .firing }.count)

        let svc = app.services[0]
        let byService = try await ok("list", ["collection": "services", "search": svc.name.uppercased()])
        XCTAssertGreaterThanOrEqual(byService["total"] as? Int ?? 0, 1)

        // 執行者欄位比對 id
        let author = try XCTUnwrap(app.pullRequests.first { if case .ai = $0.author { return true }; return false })
        guard case .ai(let aiId) = author.author else { return XCTFail() }
        let prs = try await ok("list", ["collection": "pull_requests", "where": ["author": aiId]])
        XCTAssertGreaterThanOrEqual(prs["total"] as? Int ?? 0, 1)
    }

    func testListCompactsSeriesButGetIsFull() async throws {
        let list = try await ok("list", ["collection": "servers", "limit": 1])
        let first = try XCTUnwrap((list["items"] as? [[String: Any]])?.first)
        XCTAssertLessThanOrEqual((first["cpuSeries"] as? [Any])?.count ?? 0, 3)

        let id = app.servers[0].id
        let full = try await ok("get", ["collection": "servers", "id": id])
        XCTAssertEqual((full["cpuSeries"] as? [Any])?.count, app.servers[0].cpuSeries.count)
        // 以列舉為 key 的字典要編成物件
        let svc = try await ok("get", ["collection": "services", "id": app.services[0].id])
        XCTAssertNotNil(svc["versions"] as? [String: Any])
    }

    func testGetPipelineRunByNumber() async throws {
        let run = app.pipelineRuns[0]
        let out = try await ok("get", ["collection": "pipeline_runs", "id": "\(run.id)"])
        XCTAssertEqual(out["id"] as? Int, run.id)
        let (msg, isError) = try await tool("get", ["collection": "alerts", "id": "NOPE-1"])
        XCTAssertTrue(isError)
        XCTAssertTrue("\(msg)".contains("No record with id"))
    }

    // MARK: - 寫入

    func testAcknowledgeAlertUpdatesAppAndActivity() async throws {
        let alert = try XCTUnwrap(app.alerts.first { $0.state == .firing })
        let out = try await ok("alert_update", ["alert_id": alert.id, "action": "acknowledge", "ai_note": "看起來是 DB 鎖"])
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
        let alert = try await ok("alert_create", ["title": "測試告警", "severity": "critical", "service": svc,
                                                "rule": "x > 1", "value": "2"])
        let alertId = try XCTUnwrap(alert["id"] as? String)
        XCTAssertEqual(app.alerts.first?.id, alertId)

        app.startIncidents()   // 同 App 啟動:事件存在 core server(incident_declare、incident_update 轉給 core 處理)
        let before = app.incidents.count
        let inc = try await ok("incident_declare", ["title": "測試事件", "sev": 2, "services": [svc],
                                                  "impact": "部分使用者無法登入", "alert_ids": [alertId]])
        let incId = try XCTUnwrap(inc["id"] as? String)
        XCTAssertEqual(app.incidents.count, before + 1)
        XCTAssertEqual(app.alerts.first { $0.id == alertId }?.incidentId, incId)

        _ = try await ok("incident_update", ["incident_id": incId, "status": "identified", "note": "找到原因",
                                           "finding": "連線池耗盡", "ai_summary": "摘要"])
        let i = try XCTUnwrap(app.incident(incId))
        XCTAssertEqual(i.status, .identified)
        XCTAssertEqual(i.aiSummary, "摘要")
        XCTAssertTrue(i.timeline.contains { $0.kind == .aiFinding && $0.text == "連線池耗盡" })

        let wi = try await ok("work_item_create", ["title": "加大連線池", "type": "incident", "incident_id": incId])
        let wid = try XCTUnwrap(wi["id"] as? String)
        XCTAssertTrue(app.incident(incId)!.followUps.contains(wid))
        _ = try await ok("work_item_move", ["id": wid, "status": "inProgress"])
        XCTAssertEqual(app.workItems.first { $0.id == wid }?.status, .inProgress)
    }

    func testValidationErrors() async throws {
        var (msg, isError) = try await tool("alert_create", ["title": "x", "severity": "huge", "service": app.services[0].id,
                                                          "rule": "r", "value": "v"])
        XCTAssertTrue(isError); XCTAssertTrue("\(msg)".contains("severity"))
        (msg, isError) = try await tool("incident_declare", ["title": "x", "sev": 9, "impact": "y"])
        XCTAssertTrue(isError)
        (msg, isError) = try await tool("alert_create", ["title": "x", "severity": "info", "service": "nope", "rule": "r", "value": "v"])
        XCTAssertTrue(isError); XCTAssertTrue("\(msg)".contains("Service not found"))
    }

    func testRetryPipelineAndRequestApproval() async throws {
        let run = app.pipelineRuns[0]
        let out = try await ok("pipeline_update", ["run_id": run.id, "action": "retry"])
        let newRun = try XCTUnwrap(out["newRun"] as? [String: Any])
        XCTAssertEqual(newRun["status"] as? String, "running")
        XCTAssertEqual(app.pipelineRuns[0].triggeredBy, .mcp)

        let pending = app.pendingApprovals.count
        let req = try await ok("request_approval", ["title": "擴容", "action": "kubectl scale ...", "rationale": "佇列積壓",
                                                  "rollback_plan": "縮回 4", "risk": "medium", "evidence": ["queue=1200"]])
        XCTAssertEqual(req["state"] as? String, "pending")
        XCTAssertEqual(app.pendingApprovals.count, pending + 1)
    }

    /// 範例資料工具依功能開關:功能關了 tools/list 不列、tools/call 回「this feature is turned off」;核心工具不擋
    func testSampleToolsFollowFeatureFlags() async throws {
        func listed() async throws -> Set<String> {
            let r = try await rpc("tools/list")
            return Set(try XCTUnwrap((r["result"] as? [String: Any])?["tools"] as? [[String: Any]]).compactMap { $0["name"] as? String })
        }
        // 全開:範例工具都在
        var names = try await listed()
        XCTAssertTrue(names.isSuperset(of: MCPHandler.sampleToolFeatures.keys), "\(Set(MCPHandler.sampleToolFeatures.keys).subtracting(names))")

        // 只關告警:alert_create、alert_update 不列,呼叫回錯誤,不改資料;其他範例工具照常
        app.features = .withFeatures { $0 != "monitoring.alerts" }
        names = try await listed()
        XCTAssertFalse(names.contains("alert_create")); XCTAssertFalse(names.contains("alert_update"))
        XCTAssertTrue(names.isSuperset(of: ["overview", "list", "get", "pipeline_update", "request_approval"]))
        let alerts = app.alerts
        var (msg, isError) = try await tool("alert_create", ["title": "x", "severity": "info", "service": app.services[0].id,
                                                          "rule": "r", "value": "v"])
        XCTAssertTrue(isError); XCTAssertTrue("\(msg)".contains("this feature is turned off"), "\(msg)")
        XCTAssertEqual(app.alerts, alerts, "關掉時不改資料")
        // list / get:關掉的集合回錯誤,開著的照常
        (msg, isError) = try await tool("list", ["collection": "alerts"])
        XCTAssertTrue(isError); XCTAssertTrue("\(msg)".contains("this feature is turned off"), "\(msg)")
        (msg, isError) = try await tool("get", ["collection": "alerts", "id": alerts[0].id])
        XCTAssertTrue(isError); XCTAssertTrue("\(msg)".contains("this feature is turned off"), "\(msg)")
        _ = try await ok("list", ["collection": "pipeline_runs"])
        _ = try await ok("list", ["collection": "people"])

        // 範例功能全關(預設):範例工具都不列、呼叫回錯誤;App 的其他工具與核心工具照常列
        app.features = .withFeatures { !MCPHandler.sampleFeatureKeys.contains($0) }
        names = try await listed()
        XCTAssertTrue(names.isDisjoint(with: MCPHandler.sampleToolFeatures.keys), "\(names.intersection(MCPHandler.sampleToolFeatures.keys))")
        XCTAssertTrue(names.isSuperset(of: ["navigate", "activity_post", "orgs", "server"]))
        XCTAssertTrue(names.isSuperset(of: MCPHandler.coreTools), "核心工具不擋:\(MCPHandler.coreTools.subtracting(names))")
        for n in ["overview", "ops_overview", "pipeline_update", "request_approval", "work_item_create", "log_append"] {
            (msg, isError) = try await tool(n, [:])
            XCTAssertTrue(isError, n); XCTAssertTrue("\(msg)".contains("this feature is turned off"), "\(n):\(msg)")
        }
        (msg, isError) = try await tool("issues")   // 核心工具(轉給 core server)不擋
        XCTAssertFalse(isError, "\(msg)")

        // 再打開:回來
        app.features = .allOnForTests
        _ = try await ok("overview")
    }

    /// 功能開關用的 key 都是 core 登記的(FeatureFallback 和 core/model/features.go 一致,見 SystemSettingsTests)
    func testSampleToolFeatureKeysAreRegistered() {
        for keys in MCPHandler.sampleToolFeatures.values {
            for k in keys { XCTAssertNotNil(FeatureFallback.entry(k), k) }
        }
        for c in MCPHandler.collectionFeatures.keys { XCTAssertTrue(MCPHandler.collections.contains(c), c) }
    }

    func testNavigate() async throws {
        let inc = app.incidents[0].id
        _ = try await ok("navigate", ["section": "incidents", "focus_id": inc, "env": "staging"])
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
        let auth: @Sendable (String?) -> Bool = { $0 == "Bearer omxl_ok" }
        let ok = ["authorization": "Bearer omxl_ok"]
        let evil = HTTPRequest(method: "POST", path: "/mcp", headers: ["origin": "https://evil.example", "authorization": "Bearer omxl_ok"], body: Data("{}".utf8))
        let r1 = await MCPServer.route(evil, authorize: auth, handle: echo)
        XCTAssertEqual(r1.status, 403)
        let get = HTTPRequest(method: "GET", path: "/mcp", headers: ok, body: Data())
        let r2 = await MCPServer.route(get, authorize: auth, handle: echo)
        XCTAssertEqual(r2.status, 405)
        let note = HTTPRequest(method: "POST", path: "/mcp", headers: ok, body: Data())
        let r3 = await MCPServer.route(note, authorize: auth, handle: { _ in nil })
        XCTAssertEqual(r3.status, 202)
    }

    // MARK: - 本機 MCP token

    /// /mcp 沒帶、帶錯、帶閘道 token 一律 401(handler 不會被呼叫);帶對才放行
    func testHTTPRouteRequiresToken() async throws {
        MCPLocalToken.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"
        defer { MCPLocalToken.removeForTests() }
        let tok = try XCTUnwrap(MCPLocalToken.current())
        XCTAssertTrue(tok.hasPrefix("omxl_"))
        XCTAssertEqual(MCPLocalToken.current(), tok, "第二次讀到同一把")
        let auth: @Sendable (String?) -> Bool = { MCPLocalToken.authorizes($0) }
        let called = LockedFlag()
        let echo: @Sendable (Data) async -> Data? = { called.set(); return $0 }
        func post(_ headers: [String: String]) -> HTTPRequest { HTTPRequest(method: "POST", path: "/mcp", headers: headers, body: Data("{}".utf8)) }
        for h in [[:], ["authorization": "Bearer nope"], ["authorization": "Bearer omxgw_forged"], ["authorization": "Basic \(tok)"],
                  ["authorization": "Bearer \(tok)x"]] {
            let r = await MCPServer.route(post(h), authorize: auth, handle: echo)
            XCTAssertEqual(r.status, 401, "\(h)")
            XCTAssertNotNil(r.extra["WWW-Authenticate"])
        }
        let get = await MCPServer.route(HTTPRequest(method: "GET", path: "/mcp", headers: [:], body: Data()), authorize: auth, handle: echo)
        XCTAssertEqual(get.status, 401, "沒帶 token 不透露任何東西")
        XCTAssertFalse(called.value, "沒認證不會進 handler")
        let r = await MCPServer.route(post(["authorization": "Bearer \(tok)"]), authorize: auth, handle: echo)
        XCTAssertEqual(r.status, 200)
        XCTAssertTrue(called.value)

        // 重新產生:舊的立刻失效
        let new = try XCTUnwrap(MCPLocalToken.regenerate())
        XCTAssertNotEqual(new, tok)
        XCTAssertFalse(MCPLocalToken.authorizes("Bearer \(tok)"))
        XCTAssertTrue(MCPLocalToken.authorizes("bearer \(new)"))
        // 本機 token 不能用在角色的閘道端點
        let (st, _) = await MCPGatewayEndpoint(app: app).handle(Data("{}".utf8), authorization: "Bearer \(new)")
        XCTAssertEqual(st, 401)

        // 亂數來源失敗:不發 token
        MCPLocalToken.randomHook = { nil }
        defer { MCPLocalToken.randomHook = nil }
        XCTAssertNil(MCPLocalToken.generate())
        XCTAssertNil(MCPLocalToken.regenerate())
        XCTAssertTrue(MCPLocalToken.authorizes("Bearer \(new)"), "重新產生失敗時舊的照舊")

        // 給使用者的指令帶 --header
        let cmd = MCPLocalToken.claudeCommand(url: "http://127.0.0.1:3765/mcp", token: "omxl_abc")
        XCTAssertEqual(cmd, "claude mcp add --transport http omixor http://127.0.0.1:3765/mcp --header \"Authorization: Bearer omxl_abc\"")
    }

    /// 閘道 token:亂數來源失敗時不發
    func testGatewayTokenRandomFailure() {
        MCPGateway.randomHook = { nil }
        defer { MCPGateway.randomHook = nil }
        XCTAssertNil(app.mcpGateway.issueToken(agentId: "a", taskId: "T-1", ttl: 60))
        MCPGateway.randomHook = nil
        let t = app.mcpGateway.issueToken(agentId: "a", taskId: "T-1", ttl: 60)
        XCTAssertTrue(t?.hasPrefix("omxgw_") ?? false)
    }

    /// 確認卡片的參數:完整(不截斷)、高風險欄位在前、密鑰換成 ***
    func testConfirmFields() {
        let long = String(repeating: "很長的內容", count: 200)
        let fields = app.mcpGateway.confirmFields(["note": long, "subject": "嗨", "to": "a@example.com", "file_path": "/etc/hosts",
                                                   "amount": 100, "nested": ["url": "https://x.example"]], server: nil)
        XCTAssertEqual(fields.prefix(3).map(\.key), ["amount", "file_path", "to"], "高風險的在前")
        XCTAssertTrue(fields.prefix(3).allSatisfy(\.risky))
        XCTAssertEqual(fields.first { $0.key == "note" }?.value, long, "長字串完整顯示")
        XCTAssertFalse(fields.first { $0.key == "subject" }?.risky ?? true)
        XCTAssertTrue(fields.first { $0.key == "nested" }?.value.contains("https://x.example") ?? false)
        XCTAssertTrue(MCPGateway.isRisky("recipientEmail") && MCPGateway.isRisky("BCC") && MCPGateway.isRisky("callback_url"))
    }

    /// stdio 參數的 ${名稱}:只代入登記的名稱、找不到值不連、不會二次展開
    func testExpandArgs() throws {
        let vals = ["API_KEY": "k-1", "TOKEN": "t-${API_KEY}"]
        let out = try MCPGateway.expandArgs(["--api-key", "${API_KEY}", "--api-key=${API_KEY}", "-H", "Authorization: Bearer ${TOKEN}", "plain"],
                                            declared: ["API_KEY", "TOKEN"]) { vals[$0] }
        XCTAssertEqual(out, ["--api-key", "k-1", "--api-key=k-1", "-H", "Authorization: Bearer t-${API_KEY}", "plain"], "值裡的 ${…} 不再展開")
        XCTAssertThrowsError(try MCPGateway.expandArgs(["${OTHER}"], declared: ["API_KEY"]) { vals[$0] }) {
            XCTAssertTrue($0.localizedDescription.contains("OTHER"))
        }
        XCTAssertThrowsError(try MCPGateway.expandArgs(["${MISSING}"], declared: ["MISSING"]) { vals[$0] }) {
            XCTAssertTrue($0.localizedDescription.contains("還沒輸入"))
        }
        XCTAssertThrowsError(try MCPGateway.expandArgs(["${API_KEY"], declared: ["API_KEY"]) { vals[$0] })
        XCTAssertThrowsError(try MCPGateway.expandArgs(["${bad name}"], declared: ["API_KEY"]) { vals[$0] })
        XCTAssertEqual(MCPGateway.placeholders(["a${X}b${Y}", "${X}"]), ["X", "Y"])
    }
}

/// 測試用:可以跨執行緒設定的旗標
final class LockedFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var v = false
    func set() { lock.withLock { v = true } }
    var value: Bool { lock.withLock { v } }
}
