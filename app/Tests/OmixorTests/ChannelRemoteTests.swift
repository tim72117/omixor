import XCTest
@testable import Ops

/// 假的 omixor server(core)的 /api/orgs/<組織>/channels:只回固定的資料、記下請求,用來檢查 app 送出的路徑、參數與解碼。
/// 規則本身由伺服器強制,這裡不重做。
final class FakeChannelServer: URLProtocol {
    nonisolated(unsafe) static var requests: [(method: String, path: String, query: [String: String], body: [String: Any])] = []
    nonisolated(unsafe) static var channels: [[String: Any]] = []
    /// 下一次建立頻道要回的錯誤(例如 409)
    nonisolated(unsafe) static var createError: (Int, String)?

    static let now = "2026-11-12T09:50:51Z"

    static func reset() {
        requests = []
        createError = nil
        channels = [["id": "CH-1", "name": "上線", "topic": "", "issueId": "", "members": ["user:tim@example.com", "agent:marketing"],
                     "createdBy": "user:tim@example.com", "createdAt": now, "updatedAt": now, "unread": 2, "lastMessageAt": now],
                    ["id": "CH-2", "name": "私人", "topic": "", "issueId": "ISS-3", "members": NSNull(),
                     "createdBy": "user:tim@example.com", "createdAt": now, "updatedAt": now, "unread": 0, "lastMessageAt": NSNull()]]
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

    private func ok(_ v: Any) { reply(200, ["result": v]) }

    override func startLoading() {
        var data = request.httpBody ?? Data()
        if data.isEmpty, let s = request.httpBodyStream {
            s.open()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable { let n = s.read(&buf, maxLength: buf.count); if n <= 0 { break }; data.append(buf, count: n) }
            s.close()
        }
        let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        let url = request.url!
        var query: [String: String] = [:]
        for q in URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? [] { query[q.name] = q.value ?? "" }
        let method = request.httpMethod ?? "GET"
        Self.requests.append((method, url.path, query, body))
        let p = url.path.split(separator: "/").map(String.init)   // api orgs <org> <kind> …
        guard p.count >= 4, p[0] == "api", p[1] == "orgs" else { return reply(404, ["error": "?"]) }
        let rest = Array(p.dropFirst(4))
        switch (p[3], method, rest.count) {
        case ("agents", "GET", 0):
            return ok([["id": "marketing", "name": "行銷", "role": "", "active": true, "createdAt": Self.now]])
        case ("agent-tasks", "GET", 0): return ok([Any]())
        case ("chain", "GET", 0): return ok(["edges": [Any]()])
        case ("cabinet", "GET", 0): return ok(["layout": [Any](), "docs": [Any](), "layoutSaved": true])
        case ("channels", "GET", 0):
            let issue = query["issue"]
            return ok(Self.channels.filter { issue == nil || $0["issueId"] as? String == issue })
        case ("channels", "POST", 0):
            if let (code, msg) = Self.createError { return reply(code, ["error": msg]) }
            var c: [String: Any] = ["id": "CH-3", "name": body["name"] ?? "", "topic": body["topic"] ?? "", "issueId": body["issueId"] ?? "",
                                    "members": ["user:tim@example.com"] + (body["members"] as? [String] ?? []),
                                    "createdBy": "user:tim@example.com", "createdAt": Self.now, "updatedAt": Self.now,
                                    "unread": 0, "lastMessageAt": NSNull()]
            Self.channels.append(c)
            c["unread"] = 0
            return ok(c)
        case ("channels", "GET", 1) where rest[0] == "inbox":
            return ok(["mentions": [["channelId": "CH-1", "channelName": "上線", "id": "M-4", "author": "user:tim@example.com",
                                     "text": "@行銷 請看", "at": Self.now]],
                       "channels": [["id": "CH-1", "name": "上線", "unread": 3]]])
        case ("channels", "GET", 1):
            guard let c = Self.channels.first(where: { $0["id"] as? String == rest[0] }) else { return reply(404, ["error": "找不到頻道 \(rest[0])"]) }
            return ok(c)
        case ("channels", "POST", 1):
            guard var c = Self.channels.first(where: { $0["id"] as? String == rest[0] }) else { return reply(404, ["error": "找不到頻道"]) }
            if let n = body["name"] { c["name"] = n }
            if let t = body["topic"] { c["topic"] = t }
            return ok(c)
        case ("channels", "POST", 2) where rest[1] == "members":
            guard var c = Self.channels.first(where: { $0["id"] as? String == rest[0] }) else { return reply(404, ["error": "找不到頻道"]) }
            var m = c["members"] as? [String] ?? []
            m.removeAll { (body["remove"] as? [String] ?? []).contains($0) }
            m += body["add"] as? [String] ?? []
            c["members"] = m
            return ok(c)
        case ("channels", "GET", 2) where rest[1] == "messages":
            return ok(["messages": [["id": "M-1", "channelId": rest[0], "author": "agent:marketing", "text": "**好**", "mentions": NSNull(), "at": Self.now],
                                    ["id": "M-2", "channelId": rest[0], "author": "user:tim@example.com", "text": "@行銷 謝謝",
                                     "mentions": ["agent:marketing"], "at": Self.now]],
                       "hasMore": true])
        case ("channels", "POST", 2) where rest[1] == "messages":
            if (body["text"] as? String ?? "").isEmpty { return reply(400, ["error": "訊息不能是空的"]) }
            return ok(["id": "M-9", "channelId": rest[0], "author": body["author"] ?? "user:tim@example.com", "text": body["text"] ?? "",
                       "mentions": ["agent:marketing"], "at": Self.now])
        case ("channels", "POST", 2) where rest[1] == "read":
            return ok(["channelId": rest[0], "member": body["member"] ?? "user:tim@example.com",
                       "lastRead": Int((body["upTo"] as? String ?? "M-9").dropFirst(2)) ?? 9, "at": Self.now])
        default:
            return reply(404, ["error": "沒有這個路由 \(method) \(url.path)"])
        }
    }
}

@MainActor
final class ChannelRemoteTests: XCTestCase {
    var dataDir: URL!
    let org = "~omixor"

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-channels-remote-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        IssueRemote.protocolClasses = [FakeChannelServer.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5997")!, token: "tok-1")
        FakeChannelServer.reset()
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func remoteApp() -> AppState {
        let app = AppState()
        app.currentOrgId = org
        app.startAgents(timer: false)
        return app
    }

    private func last(_ method: String, _ suffix: String) -> (method: String, path: String, query: [String: String], body: [String: Any])? {
        FakeChannelServer.requests.last { $0.method == method && $0.path.hasSuffix(suffix) }
    }

    /// 讀寫都走伺服器(不寫 channels.json);路徑、參數與解碼
    func testStoreUsesServer() throws {
        let app = remoteApp()
        let store = app.startChannels()
        XCTAssertTrue(store.isRemote)
        XCTAssertEqual(app.channels.map(\.id), ["CH-1", "CH-2"])
        XCTAssertEqual(app.channels.first?.unread, 2)
        XCTAssertEqual(app.channels.last?.channel.members, [], "members 是 null 也讀得出來")
        XCTAssertEqual(app.badge(for: .channels), 2)
        XCTAssertNotNil(last("GET", "/api/orgs/omixor/channels"))

        let r = try store.create(name: "新頻道", topic: "主題", issueId: "ISS-5", members: ["marketing"])
        XCTAssertTrue(r.created)
        XCTAssertEqual(r.channel.id, "CH-3")
        let post = try XCTUnwrap(last("POST", "/channels"))
        XCTAssertEqual(post.body["name"] as? String, "新頻道")
        XCTAssertEqual(post.body["topic"] as? String, "主題")
        XCTAssertEqual(post.body["issueId"] as? String, "ISS-5")
        XCTAssertEqual(post.body["members"] as? [String], ["marketing"])
        XCTAssertTrue(app.activity.contains { $0.text.contains("建立頻道「新頻道」") })
        XCTAssertTrue(app.channels.contains { $0.id == "CH-3" }, "建立後重讀清單")

        XCTAssertEqual(try store.list(issue: "ISS-3").map(\.id), ["CH-2"])
        XCTAssertEqual(last("GET", "/channels")?.query["issue"], "ISS-3")

        let page = try store.messages("CH-1", since: "M-0", limit: 500)
        XCTAssertEqual(page.messages.map(\.id), ["M-1", "M-2"])
        XCTAssertEqual(page.messages.first?.mentions, [])
        XCTAssertTrue(page.hasMore)
        let q = try XCTUnwrap(last("GET", "/channels/CH-1/messages")).query
        XCTAssertEqual(q["since"], "M-0")
        XCTAssertEqual(q["limit"], "200", "limit 最多 200")

        let m = try store.post("CH-1", text: "  @行銷 好  ")
        XCTAssertEqual(m.id, "M-9")
        XCTAssertEqual(last("POST", "/channels/CH-1/messages")?.body["text"] as? String, "@行銷 好")
        XCTAssertNil(last("POST", "/channels/CH-1/messages")?.body["author"], "我自己發的不帶 author")
        XCTAssertTrue(app.activity.contains { $0.text.contains("提到 行銷") })

        XCTAssertEqual(try store.markRead("CH-1", upTo: "M-2").lastRead, 2)
        XCTAssertEqual(last("POST", "/channels/CH-1/read")?.body["upTo"] as? String, "M-2")

        XCTAssertEqual(try store.update("CH-1", name: "上線討論").name, "上線討論")
        XCTAssertEqual(last("POST", "/channels/CH-1")?.body["name"] as? String, "上線討論")
        XCTAssertNil(last("POST", "/channels/CH-1")?.body["topic"])
        let changed = try store.changeMembers("CH-1", add: ["agent:narrator"], remove: ["agent:marketing"])
        XCTAssertEqual(changed.channel.members, ["user:tim@example.com", "agent:narrator"])
        XCTAssertEqual(last("POST", "/channels/CH-1/members")?.body["add"] as? [String], ["agent:narrator"])

        let inbox = try store.inbox(for: "agent:marketing")
        XCTAssertEqual(inbox.mentions.first?.id, "M-4")
        XCTAssertEqual(inbox.channels.first?.unread, 3)
        XCTAssertEqual(last("GET", "/channels/inbox")?.query["for"], "agent:marketing")

        // 伺服器的錯誤原文丟出
        FakeChannelServer.createError = (409, "ISS-3 已經有對話頻道 CH-2")
        XCTAssertThrowsError(try store.create(name: "x", issueId: "ISS-3")) { XCTAssertTrue($0.localizedDescription.contains("CH-2")) }
        XCTAssertFalse(FileManager.default.fileExists(atPath: app.dataDir.appendingPathComponent("channels.json").path), "遠端不寫本機檔案")
    }

    /// MCP 在遠端模式:agent 參數送 author / member、列表只留角色是成員的、未讀用 inbox;ops_agent_context 的 mentions 來自 inbox
    func testMCPWithAgent() async throws {
        let app = remoteApp()
        let mcp = MCPHandler(app: app)
        func call(_ name: String, _ args: [String: Any]) async throws -> ([String: Any], String, Bool) {
            let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
            let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
            let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
            let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
            return ((try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] ?? [:], text, r["isError"] as? Bool ?? false)
        }

        var (o, text, err) = try await call("ops_channels", ["agent": "行銷"])
        XCTAssertFalse(err, text)
        let list = try XCTUnwrap(o["channels"] as? [[String: Any]])
        XCTAssertEqual(list.map { $0["id"] as? String ?? "" }, ["CH-1"], "只留角色是成員的")
        XCTAssertEqual(list.first?["unread"] as? Int, 3, "未讀數用角色的")

        (o, text, err) = try await call("ops_channel_messages", ["channel": "CH-1", "agent": "marketing"])
        XCTAssertFalse(err, text)
        XCTAssertEqual((o["messages"] as? [[String: Any]])?.first?["authorName"] as? String, "行銷")
        let read = try XCTUnwrap(last("POST", "/channels/CH-1/read"))
        XCTAssertEqual(read.body["upTo"] as? String, "M-2")
        XCTAssertEqual(read.body["member"] as? String, "agent:marketing")

        (o, text, err) = try await call("ops_channel_post", ["channel": "上線", "text": "收到", "agent": "marketing"])
        XCTAssertFalse(err, text)
        XCTAssertEqual(last("POST", "/channels/CH-1/messages")?.body["author"] as? String, "agent:marketing")
        XCTAssertEqual(o["author"] as? String, "agent:marketing")

        (_, text, err) = try await call("ops_channel_post", ["channel": "CH-2", "text": "x", "agent": "marketing"])
        XCTAssertTrue(err, "角色不是成員")

        (o, text, err) = try await call("ops_agent_context", ["agent": "marketing"])
        XCTAssertFalse(err, text)
        let mentions = try XCTUnwrap(o["mentions"] as? [String: Any])
        XCTAssertEqual((mentions["messages"] as? [[String: Any]])?.first?["id"] as? String, "M-4")
        XCTAssertEqual((mentions["channels"] as? [[String: Any]])?.first?["unread"] as? Int, 3)
    }
}
