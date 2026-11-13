import XCTest
@testable import Ops

/// 假的 omixor server 管理用 MCP(/mcp/org/<組織>):tools/list 回兩個核心工具,tools/call 回呼叫的名稱與參數
final class FakeServerMCP: URLProtocol {
    nonisolated(unsafe) static var calls: [(path: String, method: String, params: [String: Any], auth: String?)] = []
    nonisolated(unsafe) static var down = false

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

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
            ]]
        case "tools/call":
            let text = String(data: try! JSONSerialization.data(withJSONObject: ["from": "server", "name": params["name"] ?? "",
                                                                                 "arguments": params["arguments"] ?? [:]]), encoding: .utf8)!
            result = ["content": [["type": "text", "text": text]], "isError": false]
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
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-mcp-proxy-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        IssueRemote.protocolClasses = [FakeServerMCP.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: "tok-9")
        FakeServerMCP.calls = []
        FakeServerMCP.down = false
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
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

    func testCoreToolsGoToServer() async throws {
        let app = AppState()
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
        let mcp = MCPHandler(app: AppState())
        let listed = try await rpc(mcp, "tools/list")
        let tools = try XCTUnwrap(listed["tools"] as? [[String: Any]])
        XCTAssertEqual(tools.count, MCPHandler.tools.count, "讀不到 server 的清單就用 App 內建的")
    }
}
