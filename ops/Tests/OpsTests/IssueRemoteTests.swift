import XCTest
@testable import Ops

/// 假的 ops-web:在記憶體裡實作 /api/issues/<組織>(行為照 Go 版 internal/web/issues_api.go)
final class FakeIssueServer: URLProtocol {
    nonisolated(unsafe) static var orgs: [String: [[String: Any]]] = [:]
    nonisolated(unsafe) static var requests: [(method: String, path: String, body: [String: Any], auth: String?)] = []
    nonisolated(unsafe) static var down = false
    /// 非 nil 時模擬 omixor-issues 的授權:/api/issues 要帶這裡面的 omixor token;/api/auth/evoleap 用 evo-tok 換
    nonisolated(unsafe) static var validTokens: Set<String>?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    static func iso(_ d: Date = .now) -> String { ISO8601DateFormatter().string(from: d) }

    override func startLoading() {
        var data = request.httpBody ?? Data()
        if data.isEmpty, let s = request.httpBodyStream {
            s.open()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable { let n = s.read(&buf, maxLength: buf.count); if n <= 0 { break }; data.append(buf, count: n) }
            s.close()
        }
        if Self.down {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        let path = request.url!.path
        Self.requests.append((request.httpMethod ?? "GET", path, body, request.value(forHTTPHeaderField: "Authorization")))
        // 核心的新路徑 api/orgs/<org>/issues[/id[/delete]] 換成舊的 api/issues/<org>[/id[/delete]] 再處理
        var parts = path.split(separator: "/").map(String.init)
        if parts.count >= 4, parts[1] == "orgs", parts[3] == "issues" {
            parts = ["api", "issues", parts[2]] + parts.dropFirst(4)
        }
        var status = 200
        var out: [String: Any]
        if Self.validTokens != nil {
            func reply(_ code: Int, _ obj: [String: Any]) {
                let resp = HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
                client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: try! JSONSerialization.data(withJSONObject: obj))
                client?.urlProtocolDidFinishLoading(self)
            }
            if path == "/api/auth/evoleap" {
                guard body["token"] as? String == "evo-tok" else { return reply(401, ["error": "evoleap 的登入無效或已過期"]) }
                let t = "omx_\(Self.validTokens!.count + 1)"
                Self.validTokens!.insert(t)
                return reply(200, ["token": t, "expiresAt": Self.iso(.now.addingTimeInterval(86400))])
            }
            let auth = request.value(forHTTPHeaderField: "Authorization") ?? ""
            guard auth.hasPrefix("Bearer "), Self.validTokens!.contains(String(auth.dropFirst(7))) else {
                return reply(401, ["error": "omixor 的登入無效、已過期或已撤銷,請重新登入"])
            }
        }
        let org = parts.count > 2 ? parts[2] : ""
        var list = Self.orgs[org] ?? []
        switch (request.httpMethod ?? "GET", parts.count) {
        case ("GET", 3):
            out = ["result": list]
        case ("POST", 3):
            let num = (list.compactMap { Int(($0["id"] as? String ?? "").filter(\.isNumber)) }.max() ?? 0) + 1
            let now = Self.iso()
            let issue: [String: Any] = [
                "id": "ISS-\(num)", "title": body["title"] ?? "", "body": body["body"] ?? "", "status": "open",
                "priority": body["priority"] ?? "normal", "labels": body["labels"] ?? [], "source": body["source"] ?? "",
                "link": body["link"] ?? "", "createdBy": body["author"] ?? "tim", "createdAt": now, "updatedAt": now, "comments": [],
            ]
            list.insert(issue, at: 0)
            out = ["result": issue]
        case ("POST", 4):
            guard let k = list.firstIndex(where: { ($0["id"] as? String)?.caseInsensitiveCompare(parts[3]) == .orderedSame }) else {
                status = 404
                out = ["error": "找不到 Issue \(parts[3])"]
                break
            }
            var i = list[k]
            for f in ["title", "body", "status", "priority", "labels", "source", "link", "comments"] where body[f] != nil { i[f] = body[f] }
            if i["status"] as? String != list[k]["status"] as? String {
                if i["status"] as? String == "closed" { i["closedAt"] = Self.iso() } else { i.removeValue(forKey: "closedAt") }
            }
            i["updatedAt"] = Self.iso()
            list[k] = i
            out = ["result": i]
        case ("POST", 5):
            list.removeAll { $0["id"] as? String == parts[3] }
            out = ["result": true]
        default:
            status = 404
            out = ["error": "?"]
        }
        Self.orgs[org] = list
        let resp = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: try! JSONSerialization.data(withJSONObject: out))
        client?.urlProtocolDidFinishLoading(self)
    }
}

@MainActor
final class IssueRemoteTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-issues-remote-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        IssueRemote.protocolClasses = [FakeIssueServer.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5999")!, token: "tok-123")
        FakeIssueServer.orgs = [:]
        FakeIssueServer.requests = []
        FakeIssueServer.down = false
        FakeIssueServer.validTokens = nil
        IssueRemote.testVault = nil
    }

    override func tearDown() async throws {
        FakeIssueServer.validTokens = nil
        IssueRemote.testVault = nil
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func call(_ mcp: MCPHandler, _ name: String, _ args: [String: Any]) async throws -> (Any?, String, Bool) {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        return (try? JSONSerialization.jsonObject(with: Data(text.utf8)), text, r["isError"] as? Bool ?? false)
    }

    private var issuesFile: URL { OrgFiles.dir(OrgFiles.defaultId).appendingPathComponent("issues.json") }

    func testConfigDefaultsOff() {
        IssueServerConfig.testOverride = nil
        unsetenv("OPS_ISSUE_SERVER")
        XCTAssertNil(IssueServerConfig.current, "預設關閉")
        setenv("OPS_ISSUE_SERVER", "http://127.0.0.1:5191", 1)
        setenv("OPS_ISSUE_SERVER_TOKEN", "abc", 1)
        XCTAssertEqual(IssueServerConfig.current, IssueServerConfig(url: URL(string: "http://127.0.0.1:5191")!, token: "abc"))
        setenv("OPS_ISSUE_SERVER", "ftp://x", 1)
        XCTAssertNil(IssueServerConfig.current, "只接受 http/https")
        unsetenv("OPS_ISSUE_SERVER")
        unsetenv("OPS_ISSUE_SERVER_TOKEN")
    }

    /// MCP 工具與本機模式的行為一樣,只是資料在伺服器
    func testMCPToolsGoThroughServer() async throws {
        FakeIssueServer.orgs["default"] = [[
            "id": "ISS-4", "title": "伺服器上的", "body": "", "status": "open", "priority": "low", "labels": ["x"],
            "source": "", "link": "", "createdBy": "tim", "createdAt": "2026-11-10T00:00:00Z", "updatedAt": "2026-11-10T00:00:00Z",
            "comments": [["id": "jira-77", "author": "tim", "text": "從 Jira 來的", "at": "2026-11-10T00:00:01Z"]],
        ]]
        let app = AppState()
        let mcp = MCPHandler(app: app)

        var (obj, text, isError) = try await call(mcp, "ops_issues", ["status": "all"])
        XCTAssertFalse(isError, text)
        XCTAssertEqual((obj as? [String: Any])?["total"] as? Int, 1)

        (obj, text, isError) = try await call(mcp, "ops_issue_create", [
            "title": "首頁播放率低", "priority": "high", "labels": ["evoleap", "訪客", "evoleap"], "source": "網站訪客 7d",
        ])
        XCTAssertFalse(isError, text)
        var d = try XCTUnwrap(obj as? [String: Any])
        XCTAssertEqual(d["id"] as? String, "ISS-5", "編號由伺服器決定")
        XCTAssertEqual(d["labels"] as? [String], ["evoleap", "訪客"])
        XCTAssertEqual(d["createdBy"] as? String, "MCP 用戶端")
        let create = try XCTUnwrap(FakeIssueServer.requests.last)
        XCTAssertEqual(create.method, "POST")
        XCTAssertEqual(create.path, "/api/orgs/default/issues", "走核心的組織路徑")
        XCTAssertEqual(create.body["author"] as? String, "mcp")
        XCTAssertEqual(create.auth, "Bearer tok-123")
        XCTAssertEqual(app.issues.first?.id, "ISS-5")
        XCTAssertEqual(app.activity.first?.text, "建立 ISS-5「首頁播放率低」")

        (obj, text, isError) = try await call(mcp, "ops_issue_update", ["id": "iss-5", "comment": "再看一次", "status": "closed", "add_labels": ["播放率"]])
        XCTAssertFalse(isError, text)
        d = try XCTUnwrap(obj as? [String: Any])
        XCTAssertEqual(d["status"] as? String, "closed")
        XCTAssertNotNil(d["closedAt"])
        XCTAssertEqual(d["labels"] as? [String], ["evoleap", "訪客", "播放率"])
        XCTAssertEqual((d["comments"] as? [[String: Any]])?.first?["text"] as? String, "再看一次")
        let srv = try XCTUnwrap(FakeIssueServer.orgs["default"]?.first { $0["id"] as? String == "ISS-5" })
        XCTAssertEqual(srv["status"] as? String, "closed")
        XCTAssertEqual((srv["comments"] as? [[String: Any]])?.first?["author"] as? String, "mcp")
        XCTAssertTrue(app.activity.contains { $0.text == "關閉 ISS-5「首頁播放率低」" })

        // 伺服器上別人(網頁)改的,下次讀就看得到
        FakeIssueServer.orgs["default"]![0]["title"] = "網頁上改的標題"
        (obj, _, _) = try await call(mcp, "ops_issues", ["id": "ISS-5"])
        XCTAssertEqual((obj as? [String: Any])?["title"] as? String, "網頁上改的標題")

        // 不是 UUID 的留言 id:送回去時保持原本的 id
        (_, text, isError) = try await call(mcp, "ops_issue_update", ["id": "ISS-4", "comment": "第二則"])
        XCTAssertFalse(isError, text)
        let ids = (FakeIssueServer.orgs["default"]?.first { $0["id"] as? String == "ISS-4" }?["comments"] as? [[String: Any]])?.compactMap { $0["id"] as? String }
        XCTAssertEqual(ids?.first, "jira-77")
        XCTAssertEqual(ids?.count, 2)

        // 刪除
        app.startIssues().delete("ISS-4")
        XCTAssertEqual(FakeIssueServer.orgs["default"]?.count, 1)
        XCTAssertFalse(app.issues.contains { $0.id == "ISS-4" })

        XCTAssertFalse(FileManager.default.fileExists(atPath: issuesFile.path), "伺服器模式不寫本機 issues.json")
    }

    /// 切換組織:讀那個組織的 Issue
    func testOrgsAreSeparate() throws {
        FakeIssueServer.orgs["default"] = []
        let app = AppState()
        let store = app.startIssues()
        store.create(title: "預設組織的", by: "tim")
        let org = app.createOrg(name: "Lab", symbol: "flask", color: "blue", template: .blank)
        app.switchOrg(org.id)
        XCTAssertTrue(app.issues.isEmpty)
        store.create(title: "Lab 的", by: "tim")
        XCTAssertEqual(FakeIssueServer.orgs[org.id]?.count, 1)
        XCTAssertEqual(FakeIssueServer.orgs["default"]?.count, 1)
        app.switchOrg(OrgFiles.defaultId)
        XCTAssertEqual(app.issues.map(\.title), ["預設組織的"])
    }

    /// 伺服器連不上:不寫本機檔案、不改畫面上的清單、記下錯誤
    func testServerDown() throws {
        FakeIssueServer.orgs["default"] = []
        let app = AppState()
        let store = app.startIssues()
        store.create(title: "a", by: "tim")
        FakeIssueServer.down = true
        let draft = store.create(title: "b", by: "tim")
        XCTAssertEqual(draft.id, "")
        XCTAssertNil(store.update("ISS-1", by: "tim") { $0.title = "改" })
        XCTAssertNotNil(store.remoteError)
        XCTAssertEqual(app.issues.map(\.title), ["a"])
        FakeIssueServer.down = false
        store.refresh()
        XCTAssertNil(store.remoteError)
        XCTAssertFalse(FileManager.default.fileExists(atPath: issuesFile.path))
    }

    /// 伺服器連不上時,MCP 的建立與修改要回錯誤,不能假裝成功(同 Go 版)
    func testMCPReportsServerDown() async throws {
        FakeIssueServer.orgs["default"] = []
        let app = AppState()
        let mcp = MCPHandler(app: app)
        var (_, text, err) = try await call(mcp, "ops_issue_create", ["title": "a"])
        XCTAssertFalse(err, text)
        FakeIssueServer.down = true
        (_, text, err) = try await call(mcp, "ops_issue_create", ["title": "b"])
        XCTAssertTrue(err); XCTAssertTrue(text.hasPrefix("Issue 沒有存起來:"), text)
        (_, text, err) = try await call(mcp, "ops_issue_update", ["id": "ISS-1", "title": "改"])
        XCTAssertTrue(err); XCTAssertTrue(text.hasPrefix("Issue 沒有改成:"), text)
    }

    /// omixor 自己的授權:沒有 omixor token 時用 evoleap 連線換一個;被撤銷(401)時自動再換;沒登入 evoleap 時講清楚
    func testOmixorTokenExchange() throws {
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5999")!, token: nil)
        FakeIssueServer.validTokens = []
        FakeIssueServer.orgs["default"] = []
        IssueRemote.testVault = ["evoleap": "evo-tok"]
        let app = AppState()
        let store = app.startIssues()
        XCTAssertEqual(store.create(title: "a", by: "tim").id, "ISS-1")
        XCTAssertEqual(IssueRemote.testVault?["omixor-issues"], "omx_1", "換到的 omixor token 要存起來")
        XCTAssertTrue(FakeIssueServer.requests.filter { $0.path.hasPrefix("/api/issues") }.allSatisfy { $0.auth == "Bearer omx_1" },
                      "Issue API 只帶 omixor 的 token,不帶 evoleap 的")
        // 伺服器撤銷 omx_1:下一次請求拿到 401,自動換 omx_2 再送
        FakeIssueServer.validTokens = []
        XCTAssertEqual(store.create(title: "b", by: "tim").id, "ISS-2")
        XCTAssertEqual(FakeIssueServer.requests.filter { $0.path == "/api/auth/evoleap" }.count, 2, "被撤銷後要重新換一次")
        XCTAssertTrue(FakeIssueServer.validTokens?.contains(IssueRemote.testVault?["omixor-issues"] ?? "") == true)
        // 沒有 evoleap 登入:錯誤訊息要說清楚
        IssueRemote.testVault = [:]
        let app2 = AppState()
        let store2 = app2.startIssues()
        XCTAssertEqual(store2.create(title: "c", by: "tim").id, "")
        XCTAssertTrue(store2.remoteError?.contains("evoleap 連線") == true, store2.remoteError ?? "")
    }

    /// 關掉伺服器設定:回到本機 issues.json
    func testTurningOffUsesLocalFile() throws {
        FakeIssueServer.orgs["default"] = []
        let app = AppState()
        let store = app.startIssues()
        store.create(title: "伺服器的", by: "tim")
        IssueServerConfig.testOverride = nil
        store.load()
        XCTAssertTrue(app.issues.isEmpty)
        store.create(title: "本機的", by: "tim")
        XCTAssertTrue(FileManager.default.fileExists(atPath: issuesFile.path))
        XCTAssertEqual(FakeIssueServer.orgs["default"]?.count, 1)
    }
}
