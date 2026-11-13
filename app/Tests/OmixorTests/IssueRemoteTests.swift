import XCTest
@testable import Ops

/// 假的 omixor-web:在記憶體裡實作 /api/issues/<組織>(行為照 Go 版 internal/web/issues_api.go)
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
        // 這個假伺服器只有 REST;管理用 MCP(/mcp/org/…)回 404,App 內建 MCP 就改用自己的實作(測的是那一份)
        if path.hasPrefix("/mcp/") {
            let resp = HTTPURLResponse(url: request.url!, statusCode: 404, httpVersion: nil, headerFields: nil)!
            client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data())
            client?.urlProtocolDidFinishLoading(self)
            return
        }
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
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
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
        OmixorMode.testServers = []
        defer { OmixorMode.testServers = nil }
        XCTAssertNotNil(IssueServerConfig.current.unavailable, "本機模式:連本機守護服務(測試裡沒有啟動 → 不能用,不會退回本機檔案)")
        let srv = OmixorMode.Server(id: "prod", name: "正式站", url: URL(string: "http://127.0.0.1:5191")!, auth: [])
        OmixorMode.testServers = [srv]
        OmixorMode.save(OmixorMode.File(mode: OmixorMode.remote, server: "prod"))
        XCTAssertEqual(IssueServerConfig.current.url, srv.url, "遠端模式:連設定裡的 server")
        XCTAssertNil(IssueServerConfig.current.unavailable)
        OmixorMode.testServers = []
        XCTAssertTrue(IssueServerConfig.current.unavailable != nil && IssueServerConfig.current.url != srv.url, "上次的遠端不在設定裡就回本機")
        OmixorMode.save(OmixorMode.File(mode: OmixorMode.local))
    }

    /// 遠端組織 id 的 "~" 前綴不送給伺服器
    func testRemoteOrgPrefixStripped() throws {
        let r = IssueRemote(config: IssueServerConfig(url: URL(string: "http://127.0.0.1:5999")!, token: "tok-123"))
        FakeIssueServer.orgs = [:]
        _ = try r.list(org: "~default")
        XCTAssertEqual(FakeIssueServer.requests.last?.path, "/api/orgs/default/issues")
    }

    /// MCP 的 Issue 工具轉給 core server 的管理端點(這裡連真的本機守護服務);改了之後 App 馬上重讀
    func testMCPToolsGoThroughServer() async throws {
        IssueRemote.protocolClasses = []
        try TestCore.start(self)
        let app = AppState()
        let mcp = MCPHandler(app: app)
        var (obj, text, isError) = try await call(mcp, "issue_create", [
            "title": "首頁播放率低", "priority": "high", "labels": ["evoleap", "訪客", "evoleap"], "source": "網站訪客 7d",
        ])
        XCTAssertFalse(isError, text)
        var d = try XCTUnwrap(obj as? [String: Any], text)
        XCTAssertEqual(d["id"] as? String, "ISS-1", "編號由伺服器決定")
        XCTAssertEqual(d["labels"] as? [String], ["evoleap", "訪客"])
        XCTAssertEqual(d["createdBy"] as? String, "mcp")
        XCTAssertEqual(app.issues.first?.id, "ISS-1")

        (obj, text, isError) = try await call(mcp, "issue_update", ["id": "iss-1", "comment": "再看一次", "status": "closed", "add_labels": ["播放率"]])
        XCTAssertFalse(isError, text)
        d = try XCTUnwrap(obj as? [String: Any], text)
        XCTAssertEqual(d["status"] as? String, "closed")
        XCTAssertNotNil(d["closedAt"])
        XCTAssertEqual(d["labels"] as? [String], ["evoleap", "訪客", "播放率"])
        XCTAssertEqual(app.issues.first?.status, .closed)
        (obj, _, _) = try await call(mcp, "issues", ["status": "all"])
        XCTAssertEqual((obj as? [String: Any])?["total"] as? Int, 1)
        XCTAssertFalse(FileManager.default.fileExists(atPath: issuesFile.path), "不寫本機 issues.json")
    }

    /// 伺服器上的留言 id 不是 UUID(例如從 Jira 匯入的):App 改了送回去時保持原本的 id;刪除送到伺服器
    func testForeignCommentIdsKept() throws {
        FakeIssueServer.orgs["default"] = [[
            "id": "ISS-4", "title": "伺服器上的", "body": "", "status": "open", "priority": "low", "labels": ["x"],
            "source": "", "link": "", "createdBy": "tim", "createdAt": "2026-11-10T00:00:00Z", "updatedAt": "2026-11-10T00:00:00Z",
            "comments": [["id": "jira-77", "author": "tim", "text": "從 Jira 來的", "at": "2026-11-10T00:00:01Z"]],
        ]]
        let app = AppState()
        let store = app.startIssues()
        XCTAssertNotNil(store.comment("ISS-4", "第二則", by: "tim"))
        let ids = (FakeIssueServer.orgs["default"]?.first { $0["id"] as? String == "ISS-4" }?["comments"] as? [[String: Any]])?.compactMap { $0["id"] as? String }
        XCTAssertEqual(ids?.first, "jira-77")
        XCTAssertEqual(ids?.count, 2)
        // 伺服器上別人(網頁)改的,下次讀就看得到
        FakeIssueServer.orgs["default"]![0]["title"] = "網頁上改的標題"
        XCTAssertEqual(store.issue("ISS-4")?.title, "網頁上改的標題")
        store.delete("ISS-4")
        XCTAssertEqual(FakeIssueServer.orgs["default"]?.count, 0)
        XCTAssertFalse(app.issues.contains { $0.id == "ISS-4" })
        XCTAssertFalse(FileManager.default.fileExists(atPath: issuesFile.path), "不寫本機 issues.json")
    }

    /// 切換組織:讀那個組織的 Issue
    func testOrgsAreSeparate() async throws {
        FakeIssueServer.orgs["default"] = []
        let app = AppState()
        let store = app.startIssues()
        store.create(title: "預設組織的", by: "tim")
        let org = app.createOrg(name: "Lab", symbol: "flask", color: "blue", template: .blank)
        await app.switchOrg(org.id).value
        XCTAssertTrue(app.issues.isEmpty)
        store.create(title: "Lab 的", by: "tim")
        XCTAssertEqual(FakeIssueServer.orgs[org.id]?.count, 1)
        XCTAssertEqual(FakeIssueServer.orgs["default"]?.count, 1)
        await app.switchOrg(OrgFiles.defaultId).value
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

    /// 伺服器連不上時,MCP 的建立與修改要回錯誤,不能假裝成功,也不改用本機檔案
    func testMCPReportsServerDown() async throws {
        FakeIssueServer.down = true
        let app = AppState()
        let mcp = MCPHandler(app: app)
        var (_, text, err) = try await call(mcp, "issue_create", ["title": "b"])
        XCTAssertTrue(err); XCTAssertTrue(text.contains("core server"), text)
        (_, text, err) = try await call(mcp, "issue_update", ["id": "ISS-1", "title": "改"])
        XCTAssertTrue(err); XCTAssertTrue(text.contains("core server"), text)
        XCTAssertFalse(FileManager.default.fileExists(atPath: issuesFile.path))
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

    /// 關掉伺服器設定(本機模式):連本機守護服務;它還沒啟動時回錯誤,不退回本機 issues.json
    func testTurningOffUsesLocalFile() throws {
        FakeIssueServer.orgs["default"] = []
        let app = AppState()
        let store = app.startIssues()
        store.create(title: "伺服器的", by: "tim")
        IssueServerConfig.testOverride = nil
        store.load()
        XCTAssertEqual(store.create(title: "本機的", by: "tim").id, "", "沒有存起來")
        XCTAssertNotNil(store.remoteError)
        XCTAssertFalse(FileManager.default.fileExists(atPath: issuesFile.path))
        XCTAssertEqual(FakeIssueServer.orgs["default"]?.count, 1)
    }
}
