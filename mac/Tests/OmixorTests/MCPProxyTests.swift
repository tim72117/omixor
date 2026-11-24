import XCTest
@testable import Omixor

/// 假的 omixor server 管理用 MCP(/mcp/org/<組織>):tools/list 回兩個核心工具,tools/call 回呼叫的名稱與參數
final class FakeServerMCP: URLProtocol {
    nonisolated(unsafe) static var calls: [(path: String, method: String, params: [String: Any], auth: String?)] = []
    nonisolated(unsafe) static var down = false
    /// false = 舊版 server:沒有 MCP Apps(tools/list 沒有 _meta.ui、resources/list 回 -32601)
    nonisolated(unsafe) static var apps = true

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    static func fail(_ p: FakeServerMCP, id: Int, code: Int, _ message: String) {
        let resp = HTTPURLResponse(url: p.request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        p.client?.urlProtocol(p, didReceive: resp, cacheStoragePolicy: .notAllowed)
        p.client?.urlProtocol(p, didLoad: try! JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": id, "error": ["code": code, "message": message]]))
        p.client?.urlProtocolDidFinishLoading(p)
    }

    override func startLoading() {
        if Self.down {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        var data = request.httpBody ?? Data()
        if data.isEmpty, let s = request.httpBodyStream {
            s.open()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable { let n = s.read(&buf, maxLength: buf.count); if n <= 0 { break }; data.append(buf, count: n) }
            s.close()
        }
        let msg = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        let method = msg["method"] as? String ?? ""
        let params = msg["params"] as? [String: Any] ?? [:]
        Self.calls.append((request.url!.path, method, params, request.value(forHTTPHeaderField: "Authorization")))
        var result: [String: Any] = [:]
        switch method {
        case "tools/list":
            result = ["tools": [
                ["name": "issues", "description": "server 的 issues", "inputSchema": ["type": "object"]],
                ["name": "orgs", "description": "server 的 orgs", "inputSchema": ["type": "object"]],
            ] + (Self.apps ? [["name": "flowchart_render", "description": "server 的流程圖", "inputSchema": ["type": "object"],
                               "_meta": ["ui": ["resourceUri": "ui://omixor/flowchart"], "ui/resourceUri": "ui://omixor/flowchart"]]] : [])]
        case "resources/list" where Self.apps:
            result = ["resources": [["uri": "ui://omixor/flowchart", "name": "flowchart", "mimeType": "text/html;profile=mcp-app"]]]
        case "resources/read" where Self.apps && params["uri"] as? String == "ui://omixor/flowchart":
            result = ["contents": [["uri": "ui://omixor/flowchart", "mimeType": "text/html;profile=mcp-app", "text": "<html>flow</html>",
                                    "_meta": ["ui": ["prefersBorder": true]]]]]
        case "resources/read" where Self.apps:
            Self.fail(self, id: 1, code: -32002, "找不到資源")
            return
        case "resources/list", "resources/read":
            Self.fail(self, id: 1, code: -32601, "不支援的方法")
            return
        case "tools/call":
            let text = String(data: try! JSONSerialization.data(withJSONObject: ["from": "server", "name": params["name"] ?? "",
                                                                                 "arguments": params["arguments"] ?? [:]]), encoding: .utf8)!
            result = ["content": [["type": "text", "text": text]], "isError": false,
                      "_meta": ["omixor/org": "default", "omixor/server": ["name": "fake", "origin": "http://127.0.0.1:5998", "version": "t", "env": "local"]]]
        default:
            break
        }
        let resp = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: try! JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": 1, "result": result]))
        client?.urlProtocolDidFinishLoading(self)
    }
}

/// App 內建 MCP 連 server 時:核心工具(server 有的)用 server 的定義、呼叫轉給 server;App 自己的工具與 keepLocal 照舊
@MainActor
final class MCPProxyTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-mcp-proxy-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
        IssueRemote.protocolClasses = [FakeServerMCP.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: "tok-9")
        FakeServerMCP.calls = []
        FakeServerMCP.down = false
        FakeServerMCP.apps = true
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OMIXOR_APP_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func rpc(_ mcp: MCPHandler, _ method: String, _ params: [String: Any] = [:]) async throws -> [String: Any] {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": method, "params": params]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        return try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
    }

    private func text(_ r: [String: Any]) -> [String: Any] {
        let t = (r["content"] as? [[String: Any]])?.first?["text"] as? String ?? ""
        return (try? JSONSerialization.jsonObject(with: Data(t.utf8))) as? [String: Any] ?? [:]
    }

    /// ISS-65/66:App 不自己實作 issues/issue_*,`return`、`comments_limit`、`comments_before` 原樣轉給 core,
    /// core 結果(含 _meta["omixor/server"])原樣回給用戶端,不被 App 改寫
    func testIssueToolArgsAndMetaPassThrough() async throws {
        let mcp = MCPHandler(app: AppState())
        let r = try await rpc(mcp, "tools/call", ["name": "issues", "arguments": ["id": "ISS-1", "return": "full", "comments_limit": 0, "comments_before": "c-9"]])
        let args = try XCTUnwrap(text(r)["arguments"] as? [String: Any])
        XCTAssertEqual(args["return"] as? String, "full")
        XCTAssertEqual(args["comments_limit"] as? Int, 0)
        XCTAssertEqual(args["comments_before"] as? String, "c-9")
        let meta = try XCTUnwrap(r["_meta"] as? [String: Any])
        XCTAssertEqual((meta["omixor/server"] as? [String: Any])?["env"] as? String, "local")
        XCTAssertEqual(meta["omixor/org"] as? String, "default")
    }

    func testCoreToolsGoToServer() async throws {
        let app = AppState()
        app.features = .allOnForTests   // 範例資料工具(overview 等)要功能開著才列
        let mcp = MCPHandler(app: app)

        // 清單:issues 換成 server 的定義;orgs 留在 App(keepLocal);App 自己的工具都還在
        let listed = try await rpc(mcp, "tools/list")
        let tools = try XCTUnwrap(listed["tools"] as? [[String: Any]])
        let names = tools.compactMap { $0["name"] as? String }
        XCTAssertEqual(names.filter { $0 == "issues" }.count, 1)
        XCTAssertEqual(tools.first { $0["name"] as? String == "issues" }?["description"] as? String, "server 的 issues")
        XCTAssertNotEqual(tools.first { $0["name"] as? String == "orgs" }?["description"] as? String, "server 的 orgs")
        XCTAssertTrue(names.contains("overview") && names.contains("navigate"))
        let list = try XCTUnwrap(FakeServerMCP.calls.first { $0.method == "tools/list" })
        XCTAssertEqual(list.path, "/mcp/org/default")
        XCTAssertEqual(list.auth, "Bearer tok-9")

        // 呼叫:issues(含舊名稱 ops_issues)轉給 server,參數原樣帶過去
        for name in ["issues", "ops_issues"] {
            let out = text(try await rpc(mcp, "tools/call", ["name": name, "arguments": ["query": "登入"]]))
            XCTAssertEqual(out["from"] as? String, "server")
            XCTAssertEqual(out["name"] as? String, "issues")
            XCTAssertEqual((out["arguments"] as? [String: Any])?["query"] as? String, "登入")
        }
        // App 自己的工具不轉
        let before = FakeServerMCP.calls.filter { $0.method == "tools/call" }.count
        _ = try await rpc(mcp, "tools/call", ["name": "overview", "arguments": [:]])
        XCTAssertEqual(FakeServerMCP.calls.filter { $0.method == "tools/call" }.count, before)
        // 工具清單有快取:不會每個請求都去讀
        XCTAssertEqual(FakeServerMCP.calls.filter { $0.method == "tools/list" }.count, 1)
    }

    func testServerDownFallsBackToAppTools() async throws {
        FakeServerMCP.down = true
        let app = AppState()
        app.features = .allOnForTests   // 範例資料工具要功能開著才列
        let mcp = MCPHandler(app: app)
        let listed = try await rpc(mcp, "tools/list")
        let tools = try XCTUnwrap(listed["tools"] as? [[String: Any]])
        XCTAssertEqual(tools.count, MCPHandler.tools.count, "讀不到 server 的清單就用 App 內建的")
        // App 沒有核心工具的實作:清單只有 App 自己的工具,呼叫核心工具回清楚的錯誤(不會落到本機)
        XCTAssertFalse(tools.contains { MCPHandler.coreTools.contains($0["name"] as? String ?? "") })
        for name in ["issues", "ops_issues", "agents", "channel_post"] {
            let r = try await rpc(mcp, "tools/call", ["name": name, "arguments": [:]])
            XCTAssertEqual(r["isError"] as? Bool, true, name)
            let msg = (r["content"] as? [[String: Any]])?.first?["text"] as? String ?? ""
            XCTAssertTrue(msg.contains("core server") && msg.contains("連不上"), "\(name):\(msg)")
            XCTAssertTrue(msg.hasPrefix(name.replacingOccurrences(of: "ops_", with: "")), msg)
        }
    }

    /// server 連得上但工具清單沒有某個核心工具(server 比 App 舊):回錯誤,不轉送、也不在 App 裡執行
    func testCoreToolMissingOnServerIsError() async throws {
        let app = AppState()
        app.features = .allOnForTests
        let mcp = MCPHandler(app: app)
        for name in ["agents", "ops_agents", "doc_save", "chain"] {
            let r = try await rpc(mcp, "tools/call", ["name": name, "arguments": [:]])
            XCTAssertEqual(r["isError"] as? Bool, true, name)
            let msg = (r["content"] as? [[String: Any]])?.first?["text"] as? String ?? ""
            XCTAssertTrue(msg.contains("工具清單裡沒有這個工具"), "\(name):\(msg)")
        }
        XCTAssertTrue(FakeServerMCP.calls.filter { $0.method == "tools/call" }.isEmpty, "server 沒有的工具不轉送")
        // App 自己的工具(含 agent_task_delegate)仍在清單裡
        let listed = try await rpc(mcp, "tools/list")
        let names = Set(try XCTUnwrap(listed["tools"] as? [[String: Any]]).compactMap { $0["name"] as? String })
        XCTAssertTrue(names.isSuperset(of: ["orgs", "server", "agent_task_delegate", "overview"]))
        XCTAssertFalse(names.contains("agents"), "server 沒有、App 也沒有實作的核心工具不列")
    }

    // MARK: - MCP Apps(resources 轉送)

    private func rpcRaw(_ mcp: MCPHandler, _ method: String, _ params: [String: Any] = [:]) async throws -> [String: Any] {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": method, "params": params]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        return try XCTUnwrap(try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])
    }

    func testToolsListKeepsUIMeta() async throws {
        let mcp = MCPHandler(app: AppState())
        let tl = try await rpc(mcp, "tools/list")
        let tools = try XCTUnwrap(tl["tools"] as? [[String: Any]])
        let meta = try XCTUnwrap(tools.first { $0["name"] as? String == "flowchart_render" }?["_meta"] as? [String: Any])
        XCTAssertEqual((meta["ui"] as? [String: Any])?["resourceUri"] as? String, "ui://omixor/flowchart")
        XCTAssertEqual(meta["ui/resourceUri"] as? String, "ui://omixor/flowchart")
    }

    func testResourcesForwardedToServer() async throws {
        let mcp = MCPHandler(app: AppState())
        let init1 = try await rpc(mcp, "initialize", ["protocolVersion": "2025-06-18"])
        let caps = try XCTUnwrap(init1["capabilities"] as? [String: Any])
        XCTAssertNotNil(caps["tools"])
        XCTAssertEqual((caps["resources"] as? [String: Any])?["subscribe"] as? Bool, false)

        let list = try await rpc(mcp, "resources/list")
        XCTAssertEqual((list["resources"] as? [[String: Any]])?.first?["uri"] as? String, "ui://omixor/flowchart")

        let read = try await rpc(mcp, "resources/read", ["uri": "ui://omixor/flowchart"])
        let c = try XCTUnwrap((read["contents"] as? [[String: Any]])?.first)
        XCTAssertEqual(c["mimeType"] as? String, "text/html;profile=mcp-app")
        XCTAssertEqual(c["text"] as? String, "<html>flow</html>")
        XCTAssertEqual(((c["_meta"] as? [String: Any])?["ui"] as? [String: Any])?["prefersBorder"] as? Bool, true)
        let sent = try XCTUnwrap(FakeServerMCP.calls.last { $0.method == "resources/read" })
        XCTAssertEqual(sent.params["uri"] as? String, "ui://omixor/flowchart")
        XCTAssertEqual(sent.path, "/mcp/org/default")
        XCTAssertEqual(FakeServerMCP.calls.filter { $0.method == "resources/list" }.count, 1, "resources/list 有快取")

        // server 沒有這個 uri:JSON-RPC 錯誤(不是例外)
        let missing = try await rpcRaw(mcp, "resources/read", ["uri": "ui://omixor/nope"])
        XCTAssertEqual((missing["error"] as? [String: Any])?["code"] as? Int, -32002)
        let noUri = try await rpcRaw(mcp, "resources/read")
        XCTAssertEqual((noUri["error"] as? [String: Any])?["code"] as? Int, -32602)
    }

    func testResourcesWhenServerDown() async throws {
        FakeServerMCP.down = true
        let mcp = MCPHandler(app: AppState())
        let list = try await rpc(mcp, "resources/list")
        XCTAssertEqual((list["resources"] as? [Any])?.count, 0)
        let read = try await rpcRaw(mcp, "resources/read", ["uri": "ui://omixor/flowchart"])
        XCTAssertNil(read["result"])
        let err = try XCTUnwrap(read["error"] as? [String: Any])
        XCTAssertEqual(err["code"] as? Int, -32002)
        XCTAssertTrue((err["message"] as? String ?? "").contains("ui://omixor/flowchart"))
        let ini = try await rpc(mcp, "initialize")
        let caps = try XCTUnwrap(ini["capabilities"] as? [String: Any])
        XCTAssertNil(caps["resources"], "連不上 server 時不宣告 resources")
    }

    /// 舊版 server(沒有 MCP Apps):tools/list 沒有 _meta.ui、resources/list 不支援 → initialize 不宣告 resources、list 回空、read 回錯誤
    func testOldServerWithoutApps() async throws {
        FakeServerMCP.apps = false
        let mcp = MCPHandler(app: AppState())
        let ini = try await rpc(mcp, "initialize")
        let caps = try XCTUnwrap(ini["capabilities"] as? [String: Any])
        XCTAssertNil(caps["resources"])
        XCTAssertNotNil(caps["tools"])
        let tl = try await rpc(mcp, "tools/list")
        let tools = try XCTUnwrap(tl["tools"] as? [[String: Any]])
        XCTAssertFalse(tools.contains { $0["_meta"] != nil }, "server 沒有畫面,tools/list 不該宣稱有")
        let l = try await rpc(mcp, "resources/list")
        XCTAssertEqual((l["resources"] as? [Any])?.count, 0)
        let rd = try await rpcRaw(mcp, "resources/read", ["uri": "ui://omixor/flowchart"])
        XCTAssertNotNil(rd["error"])
    }

    /// core/model/tools.go(登記表 CoreTools,同 repo)中 Admin: true 的工具,扣掉 keepLocal(orgs)= MCPHandler.coreTools。
    /// 預期清單直接從登記表讀(沒有匯出的核心工具規格檔:console/spec/tools.json 刻意濾掉核心工具),不再手寫;
    /// 解析方式同 console/spec/dump_spec.py(逐行抓 Name: "…")
    func testCoreToolsCoverRegistry() throws {
        let file = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("../../../core/model/tools.go").standardized
        guard let src = try? String(contentsOf: file, encoding: .utf8) else { throw XCTSkip("沒有 core/model/tools.go(單獨拿到 app 時略過)") }
        let re = try NSRegularExpression(pattern: #"Name:\s*"(\w+)""#)
        var registry = Set<String>()
        for line in src.split(separator: "\n") where line.contains("Admin: true") {
            let l = String(line)
            if let m = re.firstMatch(in: l, range: NSRange(l.startIndex..., in: l)), let r = Range(m.range(at: 1), in: l) { registry.insert(String(l[r])) }
        }
        XCTAssertGreaterThan(registry.count, 20, "解析不到登記表的 Admin 工具,檔案格式變了?")
        let admin = registry.subtracting(MCPHandler.keepLocal)
        XCTAssertEqual(MCPHandler.coreTools, admin, "差:\(MCPHandler.coreTools.symmetricDifference(admin).sorted())")
    }
}
