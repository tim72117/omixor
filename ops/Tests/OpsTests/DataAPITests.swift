import XCTest
@testable import Ops

/// 唯讀資料 API:驗證、只允許唯讀工具、參數轉型、便利路徑、Origin、方法、真的 loopback HTTP
@MainActor
final class DataAPITests: XCTestCase {
    private var app: AppState!
    private var store: DataAPIStore!
    private var dataDir: URL!
    private var token = ""

    static let boss = "tim@example.com"

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-dataapi-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        app = AppState()
        app.startAgents(timer: false)
        store = app.startDataAPI(serve: false)
        store.update { $0.allowedLogins = [Self.boss] }
        token = store.regenerateToken()
    }

    override func tearDown() async throws {
        DataAPI.setToken(nil)
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    /// send("/path") 是 GET;send("POST", "/path") 指定方法
    private func send(_ first: String, _ second: String? = nil, login: String? = boss, bearer: String? = nil,
                      origin: String? = nil, body: Any? = nil) async -> (Int, Any?) {
        let method = second == nil ? "GET" : first
        let target = second ?? first
        var headers: [String: String] = [:]
        if let login { headers["tailscale-user-login"] = login }
        if let bearer { headers["authorization"] = "Bearer \(bearer)" }
        if let origin { headers["origin"] = origin }
        let data = body.map { try! JSONSerialization.data(withJSONObject: $0) } ?? Data()
        let path = target.split(separator: "?", maxSplits: 1).first.map(String.init) ?? target
        let r = await store.handle(HTTPRequest(method: method, path: path, headers: headers, body: data, rawTarget: target))
        return (r.status, try? JSONSerialization.jsonObject(with: r.body, options: .fragmentsAllowed))
    }

    private func err(_ v: Any?) -> String { (v as? [String: Any])?["error"] as? String ?? "" }

    // MARK: - 設定

    func testSettingsAreGlobalAndTokenNotInJSON() throws {
        let file = try String(contentsOf: OrgFiles.root.appendingPathComponent("data-api.json"), encoding: .utf8)
        XCTAssertTrue(file.contains(Self.boss))
        XCTAssertFalse(file.contains(token))
        XCTAssertTrue(token.hasPrefix("ops_"))
        XCTAssertEqual(DataAPI.token(), token)
        let s = try JSONDecoder().decode(DataAPISettings.self, from: Data(#"{"port":"x","enabled":1,"allowedLogins":[" a@b ",""]}"#.utf8))
        XCTAssertEqual(s.port, 3768)
        XCTAssertFalse(s.enabled)
        XCTAssertEqual(s.allowedLogins, ["a@b"])
        XCTAssertTrue(s.allows("A@B"))
        XCTAssertFalse(DataAPISettings().enabled, "預設關閉")
    }

    // MARK: - 驗證

    func testAuthentication() async {
        var (status, body) = await send("/api/v1/overview")
        XCTAssertEqual(status, 200, "白名單通過")
        XCTAssertNotNil((body as? [String: Any])?["services"] ?? body)

        (status, body) = await send("/api/v1/overview", login: "eve@example.com")
        XCTAssertEqual(status, 401)
        XCTAssertEqual(err(body), "Unauthorized")
        XCTAssertEqual(app.dataAPI.rejected.first?.login, "eve@example.com")

        (status, body) = await send("/api/v1/overview", login: nil)
        XCTAssertEqual(status, 401, "缺標頭又沒有 token")
        (status, _) = await send("/api/v1/overview", login: nil, bearer: "ops_wrong")
        XCTAssertEqual(status, 401, "錯的 token")
        (status, _) = await send("/api/v1/overview", login: nil, bearer: String(token.dropLast()))
        XCTAssertEqual(status, 401, "少一個字也不行")
        (status, _) = await send("/api/v1/overview", login: nil, bearer: token)
        XCTAssertEqual(status, 200, "對的 token")
        (status, _) = await send("/api/v1/overview", login: "eve@example.com", bearer: token)
        XCTAssertEqual(status, 200, "不在白名單但 token 對")

        // 未通過時,連路由存不存在都不透露
        (status, body) = await send("/api/v1/nothing", login: nil)
        XCTAssertEqual(status, 401)
        (status, body) = await send("/api/v1/tools/ops_issue_create", login: nil)
        XCTAssertEqual(status, 401)

        // 撤銷 token 後不能用
        store.revokeToken()
        (status, _) = await send("/api/v1/overview", login: nil, bearer: token)
        XCTAssertEqual(status, 401)

        // 一鍵加入白名單
        store.allow("eve@example.com")
        (status, _) = await send("/api/v1/overview", login: "eve@example.com")
        XCTAssertEqual(status, 200)
        XCTAssertFalse(app.dataAPI.rejected.contains { $0.login == "eve@example.com" })
        XCTAssertEqual(app.dataAPI.lastCaller, "eve@example.com")
    }

    func testForeignOriginBlocked() async {
        var (status, _) = await send("/api/v1/overview", origin: "https://evil.example")
        XCTAssertEqual(status, 403)
        (status, _) = await send("/api/v1/overview", origin: "https://office.tailnet.ts.net")
        XCTAssertEqual(status, 403, "ts.net 的網頁也不行")
        (status, _) = await send("/api/v1/overview", origin: "http://localhost:5173")
        XCTAssertEqual(status, 200)
    }

    // MARK: - 只讀

    func testOnlyReadOnlyTools() async {
        let before = app.issues.count
        var (status, body) = await send("POST", "/api/v1/tools/ops_issue_create", body: ["title": "不該建立"])
        XCTAssertEqual(status, 403)
        XCTAssertTrue(err(body).contains("不是唯讀工具"))
        (status, _) = await send("/api/v1/tools/ops_navigate?section=incidents")
        XCTAssertEqual(status, 403)
        (status, _) = await send("/api/v1/tools/ops_alert_update?alert_id=x&action=acknowledge")
        XCTAssertEqual(status, 403)
        XCTAssertEqual(app.issues.count, before)
        XCTAssertEqual(app.section, .overview)
        (status, _) = await send("/api/v1/tools/ops_nope")
        XCTAssertEqual(status, 404)

        // 每個非唯讀工具都擋
        for t in MCPHandler.tools where !t.readOnly {
            let (s, _) = await send("POST", "/api/v1/tools/\(t.name)", body: [:])
            XCTAssertEqual(s, 403, t.name)
        }
    }

    func testIndexListsReadOnlyToolsOnly() async throws {
        let (status, body) = await send("/api/v1")
        XCTAssertEqual(status, 200)
        let o = try XCTUnwrap(body as? [String: Any])
        let names = try XCTUnwrap(o["tools"] as? [[String: Any]]).compactMap { $0["name"] as? String }
        XCTAssertEqual(Set(names), Set(MCPHandler.tools.filter(\.readOnly).map(\.name)))
        XCTAssertFalse(names.contains("ops_issue_create"))
        XCTAssertNotNil((o["tools"] as? [[String: Any]])?.first?["inputSchema"])
        XCTAssertTrue((o["endpoints"] as? [[String: Any]])?.contains { $0["path"] as? String == "/api/v1/overview" } == true)
        let (s2, _) = await send("/api/v1/")
        XCTAssertEqual(s2, 200)
    }

    // MARK: - 參數

    func testQueryConversion() throws {
        let schema = MCPHandler.tools.first { $0.name == "ops_list" }!.properties
        let a = try DataAPI.convert(DataAPI.queryItems("/x?collection=alerts&limit=5&where=%7B%22state%22%3A%22firing%22%7D&search=p95+latency"),
                                    schema: schema)
        XCTAssertEqual(a["limit"] as? Int, 5)
        XCTAssertEqual((a["where"] as? [String: Any])?["state"] as? String, "firing")
        XCTAssertEqual(a["search"] as? String, "p95 latency")
        XCTAssertThrowsError(try DataAPI.convert([("limit", "five")], schema: schema))
        XCTAssertThrowsError(try DataAPI.convert([("where", "[1]")], schema: schema))
        XCTAssertThrowsError(try DataAPI.convert([("bogus", "1")], schema: schema))

        let gh = MCPHandler.tools.first { $0.name == "ops_github_run" }!.properties
        let b = try DataAPI.convert([("log", "true"), ("id", "42")], schema: gh)
        XCTAssertEqual(b["log"] as? Bool, true)
        XCTAssertEqual(b["id"] as? Int, 42)
        XCTAssertThrowsError(try DataAPI.convert([("log", "maybe")], schema: gh))

        let arr: [String: Any] = ["labels": ["type": "array"]]
        XCTAssertEqual(try DataAPI.convert([("labels", #"["a","b"]"#)], schema: arr)["labels"] as? [String], ["a", "b"])
        XCTAssertEqual(try DataAPI.convert([("labels", "a"), ("labels", "b")], schema: arr)["labels"] as? [String], ["a", "b"])
    }

    func testQueryParamsReachTool() async throws {
        var (status, body) = await send("/api/v1/list/alerts?limit=2&where=%7B%22state%22%3A%22firing%22%7D")
        XCTAssertEqual(status, 200, "\(String(describing: body))")
        let o = try XCTUnwrap(body as? [String: Any])
        let items = try XCTUnwrap(o["items"] as? [[String: Any]] ?? o["results"] as? [[String: Any]] ?? o["data"] as? [[String: Any]],
                                  "\(o.keys)")
        XCTAssertLessThanOrEqual(items.count, 2)
        XCTAssertTrue(items.allSatisfy { $0["state"] as? String == "firing" })

        (status, body) = await send("/api/v1/list/alerts?limit=abc")
        XCTAssertEqual(status, 400)
        XCTAssertTrue(err(body).contains("整數"))

        // 工具錯誤 → 400
        (status, body) = await send("/api/v1/list/nope")
        XCTAssertEqual(status, 400)
        XCTAssertFalse(err(body).isEmpty)

        // POST JSON 參數
        (status, body) = await send("POST", "/api/v1/tools/ops_list", body: ["collection": "services", "limit": 1])
        XCTAssertEqual(status, 200)
        (status, body) = await send("POST", "/api/v1/tools/ops_list", body: ["collection": "services", "evil": 1])
        XCTAssertEqual(status, 400)
    }

    // MARK: - 便利路徑

    func testShortcuts() async throws {
        XCTAssertEqual(DataAPI.resolve("/api/v1/get/incidents/INC-2041"), .init(tool: "ops_get", args: ["collection": "incidents", "id": "INC-2041"]))
        XCTAssertEqual(DataAPI.resolve("/api/v1/tasks/"), .init(tool: "ops_agent_tasks"))
        XCTAssertNil(DataAPI.resolve("/api/v2/overview"))
        XCTAssertNil(DataAPI.resolve("/api/v1/overview/extra"))

        for path in ["/overview", "/issues", "/agents", "/tasks", "/docs", "/cabinet", "/orgs", "/list/services"] {
            let (status, body) = await send("/api/v1" + path)
            XCTAssertEqual(status, 200, "\(path):\(String(describing: body))")
        }
        let inc = app.incidents[0].id
        let (status, body) = await send("/api/v1/get/incidents/\(inc)")
        XCTAssertEqual(status, 200)
        XCTAssertEqual((body as? [String: Any])?["id"] as? String, inc)
        // GitHub / GCP 沒登入:工具錯誤 → 400,說明怎麼設定
        let (gs, gb) = await send("/api/v1/github")
        XCTAssertEqual(gs, 400)
        XCTAssertTrue(err(gb).contains("GitHub"))
        let (s404, _) = await send("/api/v1/whatever")
        XCTAssertEqual(s404, 404)
    }

    func testMethods() async {
        var (status, _) = await send("POST", "/api/v1/overview")
        XCTAssertEqual(status, 405)
        (status, _) = await send("DELETE", "/api/v1/tools/ops_overview")
        XCTAssertEqual(status, 405)
        (status, _) = await send("PUT", "/api/v1")
        XCTAssertEqual(status, 405)
        (status, _) = await send("POST", "/api/v1/tools/ops_overview")
        XCTAssertEqual(status, 200)
    }

    // MARK: - 真的 HTTP

    func testLoopbackServer() async throws {
        let port = Int.random(in: 48000..<52000)
        store.update { $0.port = port; $0.enabled = true }
        store.apply(serve: true)
        defer { store.update { $0.enabled = false }; store.apply(serve: false) }
        try await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertEqual(app.dataAPI.server, .listening)

        func get(_ path: String, _ headers: [String: String]) async throws -> (Int, Data) {
            var req = URLRequest(url: URL(string: "http://127.0.0.1:\(port)\(path)")!)
            for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
            let (d, r) = try await URLSession(configuration: .ephemeral).data(for: req)
            return ((r as! HTTPURLResponse).statusCode, d)
        }
        let (ok, data) = try await get("/api/v1/overview", ["Tailscale-User-Login": Self.boss])
        XCTAssertEqual(ok, 200)
        XCTAssertNotNil(try JSONSerialization.jsonObject(with: data))
        let (bearer, _) = try await get("/api/v1/orgs", ["Authorization": "Bearer \(token)"])
        XCTAssertEqual(bearer, 200)
        let (no, noBody) = try await get("/api/v1/overview", [:])
        XCTAssertEqual(no, 401)
        XCTAssertFalse(String(decoding: noBody, as: UTF8.self).contains("services"))
        let (forbidden, _) = try await get("/api/v1/tools/ops_issue_create", ["Tailscale-User-Login": Self.boss])
        XCTAssertEqual(forbidden, 403)
    }
}
