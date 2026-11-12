import XCTest
@testable import Ops

/// GCP:用 StubProtocol 假裝 run / logging / monitoring.googleapis.com
@MainActor
final class GCPTests: XCTestCase {
    private var app: AppState!
    private var mcp: MCPHandler!
    private var dataDir: URL!
    private var nextId = 0

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-gcp-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        ConnHTTP.protocolClasses = [StubProtocol.self]
        StubProtocol.log = []
        StubProtocol.handler = Self.fakeGoogle
        app = AppState()
        app.startConnections()
        mcp = MCPHandler(app: app)
    }

    override func tearDown() async throws {
        for c in app.connections { TokenVault.removeAll(c.id) }
        ConnHTTP.protocolClasses = []
        StubProtocol.handler = nil
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    static let base = "/v2/projects/shuttle-045094509/locations/asia-east1"

    nonisolated static func fakeGoogle(_ req: URLRequest, _ body: Data) -> (Int, Any) {
        guard req.value(forHTTPHeaderField: "Authorization") == "Bearer ya29.test" else {
            return (401, ["error": ["code": 401, "message": "Request had invalid authentication credentials."]])
        }
        let u = req.url!
        switch (u.host ?? "", u.path) {
        case ("www.googleapis.com", "/oauth2/v3/userinfo"):
            return (200, ["email": "tim@example.com", "sub": "1"])
        case ("run.googleapis.com", base + "/services"):
            func svc(_ n: String, ready: String, latest: String, created: String) -> [String: Any] {
                ["name": "projects/shuttle-045094509/locations/asia-east1/services/\(n)",
                 "uri": "https://\(n)-taqycykl5q-de.a.run.app",
                 "latestReadyRevision": "projects/p/locations/asia-east1/services/\(n)/revisions/\(latest)",
                 "latestCreatedRevision": "projects/p/locations/asia-east1/services/\(n)/revisions/\(created)",
                 "terminalCondition": ["type": "Ready", "state": ready],
                 "updateTime": "2026-11-10T17:26:30.123456789Z", "lastModifier": "deploy@x.iam.gserviceaccount.com",
                 "template": ["containers": [["image": "asia-east1-docker.pkg.dev/x/y/\(n):f8d723e"]], "scaling": ["maxInstanceCount": 2]],
                 "trafficStatuses": [["type": "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST", "percent": 100]]]
            }
            return (200, ["services": [svc("shuttle-server", ready: "CONDITION_SUCCEEDED", latest: "shuttle-server-00043-xnq", created: "shuttle-server-00043-xnq"),
                                       svc("tripace-server", ready: "CONDITION_FAILED", latest: "t-1", created: "t-2")]])
        case ("run.googleapis.com", base + "/services/shuttle-server/revisions"):
            return (200, ["revisions": [
                ["name": "x/revisions/shuttle-server-00042-abc", "createTime": "2026-11-01T00:00:00Z",
                 "containers": [["image": "img:old"]], "conditions": [["type": "Ready", "state": "CONDITION_SUCCEEDED"]]],
                ["name": "x/revisions/shuttle-server-00043-xnq", "createTime": "2026-11-10T17:20:00.5Z",
                 "containers": [["image": "img:f8d723e"]], "conditions": [["type": "Ready", "state": "CONDITION_SUCCEEDED"]]],
            ]])
        case ("run.googleapis.com", base + "/jobs"):
            return (200, ["jobs": [
                ["name": "x/jobs/shuttle-course-sync",
                 "latestCreatedExecution": ["name": "x/executions/shuttle-course-sync-9fm2q", "createTime": "2026-11-10T17:24:00Z",
                                            "completionTime": "2026-11-10T17:26:21.074088Z", "completionStatus": "EXECUTION_FAILED"]],
                ["name": "x/jobs/tripace-migrate"],
            ]])
        case ("run.googleapis.com", base + "/jobs/shuttle-course-sync/executions"):
            return (200, ["executions": [
                ["name": "x/executions/shuttle-course-sync-9fm2q", "createTime": "2026-11-10T17:24:00Z", "startTime": "2026-11-10T17:24:10Z",
                 "completionTime": "2026-11-10T17:26:21.074088Z", "taskCount": 1, "failedCount": 1,
                 "conditions": [["type": "Completed", "state": "CONDITION_FAILED", "message": "Task failed with exit code 1"]]],
            ]])
        case ("logging.googleapis.com", "/v2/entries:list"):
            return (200, ["entries": [
                ["insertId": "a", "timestamp": "2026-11-10T18:00:00.123456Z", "severity": "ERROR", "textPayload": "panic: db timeout",
                 "resource": ["labels": ["service_name": "shuttle-server", "revision_name": "shuttle-server-00043-xnq"]]],
                ["insertId": "b", "timestamp": "2026-11-10T17:59:00Z", "severity": "ERROR", "jsonPayload": ["message": "lesson not found"],
                 "resource": ["labels": ["service_name": "shuttle-server"]]],
                ["insertId": "c", "timestamp": "2026-11-10T17:58:00Z", "severity": "ERROR",
                 "httpRequest": ["requestMethod": "GET", "requestUrl": "https://evoleap.io/api/x", "status": 503, "latency": "1.2s"],
                 "resource": ["labels": ["service_name": "shuttle-server"]]],
            ]])
        case ("monitoring.googleapis.com", "/v3/projects/shuttle-045094509/timeSeries"):
            let q = u.query ?? ""
            func pt(_ end: String, _ v: Any) -> [String: Any] {
                ["interval": ["startTime": end, "endTime": end], "value": v is Double ? ["doubleValue": v] : ["int64Value": v]]
            }
            if q.contains("request_count") {
                return (200, ["timeSeries": [
                    ["metric": ["labels": ["response_code_class": "2xx"]], "points": [pt("2026-11-10T17:00:00Z", "100"), pt("2026-11-10T18:00:00Z", "50")]],
                    ["metric": ["labels": ["response_code_class": "5xx"]], "points": [pt("2026-11-10T18:00:00Z", "3")]],
                ]])
            }
            if q.contains("request_latencies") {
                return (200, ["timeSeries": [["metric": [:], "points": [pt("2026-11-10T18:00:00Z", 812.5)]]]])
            }
            return (200, ["timeSeries": [["metric": [:], "points": [pt("2026-11-10T18:00:00Z", "2")]]]])
        default:
            return (404, ["error": ["code": 404, "message": "not found \(u.absoluteString)"]])
        }
    }

    private func login(services: [String] = ["shuttle-server"]) async {
        let g = app.startGCP(timer: false)
        g.ensureConnection()
        g.update { $0.project = "shuttle-045094509"; $0.region = "asia-east1"; $0.services = services; $0.jobs = ["shuttle-course-sync"] }
        app.connStore?.setBearer("ya29.test", for: GCP.connectionId)
        await app.connStore?.refreshAccount(GCP.connectionId)
    }

    private func tool(_ name: String, _ args: [String: Any] = [:]) async throws -> (Any, Bool) {
        nextId += 1
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": nextId, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let handled = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let raw = try XCTUnwrap(handled)
        let obj = try XCTUnwrap(try JSONSerialization.jsonObject(with: raw) as? [String: Any])
        let result = try XCTUnwrap(obj["result"] as? [String: Any], "\(obj)")
        let text = try XCTUnwrap((result["content"] as? [[String: Any]])?.first?["text"] as? String)
        if result["isError"] as? Bool == true { return (text, true) }
        return (try JSONSerialization.jsonObject(with: Data(text.utf8), options: .fragmentsAllowed), false)
    }

    // MARK: 測試

    func testGoogleTimestampsWithNanoseconds() {
        XCTAssertNotNil(GCP.date("2026-11-10T17:26:21.074088Z"))
        XCTAssertNotNil(GCP.date("2026-11-10T17:26:30.123456789Z"))
        XCTAssertNotNil(GCP.date("2026-11-10T17:20:00.5Z"))
        XCTAssertNotNil(GCP.date("2026-11-10T17:20:00Z"))
        XCTAssertEqual(GCP.date("2026-11-10T17:26:21.074088Z")!.timeIntervalSince1970,
                       GCP.date("2026-11-10T17:26:21Z")!.timeIntervalSince1970 + 0.074, accuracy: 0.001)
    }

    func testTokenOnlyGoesToSameDomain() async throws {
        let c = GCP.presetConnection
        XCTAssertEqual(c.auth, .oauth)
        XCTAssertTrue(c.oauth.scopes.contains("cloud-platform.read-only"))
        XCTAssertFalse(c.oauth.scopes.contains("auth/cloud-platform "))
        XCTAssertTrue(c.allowsHost("run.googleapis.com"))
        XCTAssertTrue(c.allowsHost("www.googleapis.com"))
        XCTAssertFalse(c.allowsHost("evil.com"))
        XCTAssertFalse(c.allowsHost("googleapis.com.evil.com"))
        XCTAssertFalse(c.allowsHost("xgoogleapis.com"))
        await login()
        do {
            _ = try await app.connStore!.request(GCP.connectionId, "GET", "https://evil.com/steal")
            XCTFail("不該送出")
        } catch {
            XCTAssertTrue("\(error.localizedDescription)".contains("不能呼叫"))
        }
        XCTAssertFalse(StubProtocol.log.contains { $0.1.contains("evil.com") })
    }

    func testRefreshFiltersServicesAndJobs() async throws {
        await login()
        await app.gcpStore?.refresh()
        XCTAssertNil(app.gcp.error)
        XCTAssertEqual(app.gcp.services.map(\.id), ["shuttle-server"])
        let s = app.gcp.services[0]
        XCTAssertTrue(s.ready)
        XCTAssertEqual(s.stateLabel, "正常")
        XCTAssertEqual(s.latestReady, "shuttle-server-00043-xnq")
        XCTAssertEqual(s.maxInstances, 2)
        XCTAssertEqual(s.traffic.first?.percent, 100)
        XCTAssertNotNil(s.updatedAt)
        XCTAssertEqual(app.gcp.jobs.map(\.id), ["shuttle-course-sync"])
        XCTAssertEqual(app.gcp.jobs[0].latest?.statusLabel, "失敗")

        // 全部服務:壞掉的那個要標出來
        app.gcpStore?.update { $0.services = [] }
        await app.gcpStore?.refresh()
        XCTAssertEqual(app.gcp.services.map(\.stateLabel), ["正常", "異常"])

        let revs = try await app.gcpStore!.revisions(service: "shuttle-server")
        XCTAssertEqual(revs.first?.id, "shuttle-server-00043-xnq")
        XCTAssertEqual(GCPImage.tag(revs.first!.image), "f8d723e")
        let ex = try await app.gcpStore!.executions(job: "shuttle-course-sync")
        XCTAssertEqual(ex.first?.status, "failed")
        XCTAssertEqual(ex.first?.message, "Task failed with exit code 1")
        XCTAssertEqual(ex.first?.duration ?? 0, 131, accuracy: 1)

        // 設定存在組織的 gcp.json
        let saved = try OrgFiles.decoder().decode(GCPSettings.self, from: Data(contentsOf: app.dataDir.appendingPathComponent("gcp.json")))
        XCTAssertEqual(saved.project, "shuttle-045094509")
        XCTAssertEqual(saved.jobs, ["shuttle-course-sync"])
    }

    func testLogsFilterAndParsing() async throws {
        await login()
        let logs = try await app.gcpStore!.logs(service: "shuttle-server", severity: "ERROR", hours: 24, contains: "db \"x\"")
        XCTAssertEqual(logs.map(\.text), ["panic: db timeout", "lesson not found", "GET https://evoleap.io/api/x → 503"])
        XCTAssertEqual(logs[2].httpStatus, 503)
        XCTAssertEqual(logs[0].revision, "shuttle-server-00043-xnq")
        let sent = try XCTUnwrap(StubProtocol.log.last { $0.1.contains("entries:list") })
        XCTAssertEqual(sent.0, "POST")
        let body = try XCTUnwrap(try JSONSerialization.jsonObject(with: sent.2) as? [String: Any])
        let filter = try XCTUnwrap(body["filter"] as? String)
        XCTAssertTrue(filter.contains("resource.labels.service_name=\"shuttle-server\""), filter)
        XCTAssertTrue(filter.contains("severity>=ERROR"), filter)
        XCTAssertTrue(filter.contains("\"db \\\"x\\\"\""), filter)
        XCTAssertEqual(body["resourceNames"] as? [String], ["projects/shuttle-045094509"])
    }

    func testMetricsQueryAndTotals() async throws {
        await login()
        let m = try await app.gcpStore!.metrics(service: "shuttle-server", hours: 24)
        XCTAssertEqual(m.totalRequests, 153)
        XCTAssertEqual(m.total("5xx"), 3)
        XCTAssertEqual(m.maxP95, 812.5)
        XCTAssertEqual(m.instances.first?.value, 2)
        let q = try XCTUnwrap(StubProtocol.log.first { $0.1.contains("request_count") }?.1)
        let comps = try XCTUnwrap(URLComponents(string: q))
        let filter = comps.queryItems?.first { $0.name == "filter" }?.value
        XCTAssertEqual(filter, "metric.type=\"run.googleapis.com/request_count\" AND resource.labels.service_name=\"shuttle-server\"")
        XCTAssertEqual(comps.queryItems?.first { $0.name == "aggregation.groupByFields" }?.value, "metric.labels.response_code_class")
        XCTAssertEqual(comps.queryItems?.first { $0.name == "aggregation.alignmentPeriod" }?.value, "3600s")
    }

    func testMCPTools() async throws {
        XCTAssertEqual(MCPHandler.tools.filter { $0.name.hasPrefix("ops_gcp") }.count, 3)
        let (msg, isErr) = try await tool("ops_gcp")
        XCTAssertTrue(isErr)
        XCTAssertTrue("\(msg)".contains("還沒登入 Google Cloud"))

        await login()
        let sumOut = try await tool("ops_gcp", ["refresh": true, "service": "shuttle-server", "job": "shuttle-course-sync"]).0
        let sum = try XCTUnwrap(sumOut as? [String: Any], "\(sumOut)")
        XCTAssertEqual(sum["account"] as? String, "tim@example.com")
        XCTAssertEqual((sum["services"] as? [[String: Any]])?.first?["state"] as? String, "正常")
        XCTAssertEqual((sum["revisions"] as? [Any])?.count, 2)
        XCTAssertEqual((sum["executions"] as? [[String: Any]])?.first?["status"] as? String, "失敗")

        let logsOut = try await tool("ops_gcp_logs", ["service": "shuttle-server"]).0
        XCTAssertEqual((logsOut as? [String: Any])?["count"] as? Int, 3)
        let (bad, badErr) = try await tool("ops_gcp_logs", ["severity": "LOUD"])
        XCTAssertTrue(badErr, "\(bad)")

        // 只有一個服務時可以省略 service
        let mOut = try await tool("ops_gcp_metrics").0
        let totals = try XCTUnwrap((mOut as? [String: Any])?["totals"] as? [String: Any], "\(mOut)")
        XCTAssertEqual(totals["5xx"] as? Int, 3)
        XCTAssertEqual(totals["requests"] as? Int, 153)
        // 只讀:除了 entries:list(讀日誌也是 POST)以外都是 GET
        XCTAssertTrue(StubProtocol.log.allSatisfy { $0.0 == "GET" || $0.1.hasSuffix("/v2/entries:list") })
    }

    func testOrgsKeepSeparateProjects() async throws {
        await login()
        let blank = app.createOrg(name: "Side", template: .blank)
        app.switchOrg(blank.id)
        XCTAssertEqual(app.gcpSettings.project, "")
        app.switchOrg(OrgFiles.defaultId)
        XCTAssertEqual(app.gcpSettings.project, "shuttle-045094509")
        let copy = app.createOrg(name: "Copy", template: .copyCurrent)
        app.switchOrg(copy.id)
        XCTAssertEqual(app.gcpSettings.services, ["shuttle-server"])
    }
}
