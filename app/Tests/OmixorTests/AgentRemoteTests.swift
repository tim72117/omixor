import XCTest
@testable import Ops

/// 假的 omixor server(core):在記憶體裡實作 /api/orgs/<組織>/agents、/agent-tasks、/issues(含 assignee)與 /api/version。
/// 行為照 core/service/agents.go:assignee 有給才同步任務;回應包在 {"result": …};任務沒有 routineId、留言 id 不是 UUID。
final class FakeOmixorServer: URLProtocol {
    nonisolated(unsafe) static var agents: [[String: Any]] = []
    nonisolated(unsafe) static var tasks: [[String: Any]] = []
    nonisolated(unsafe) static var issues: [[String: Any]] = []
    nonisolated(unsafe) static var requests: [(method: String, path: String, body: [String: Any])] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    static let now = "2026-11-12T09:50:51Z"

    static func reset() {
        agents = [["id": "programmer", "name": "程式設計師", "role": "寫程式", "active": true, "color": "indigo", "symbol": "hammer",
                   "instructions": "照說明實作", "docAccess": ["docs": "write"], "routines": NSNull(), "createdAt": "2026-11-12T07:53:32Z"],
                  ["id": "tester", "name": "測試員", "role": "測試", "active": false, "color": "teal", "symbol": "checkmark",
                   "instructions": "", "docAccess": [:], "routines": [], "createdAt": "2026-11-12T07:53:32Z"]]
        tasks = []
        issues = []
        requests = []
    }

    private func reply(_ code: Int, _ obj: Any) {
        let resp = HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: try! JSONSerialization.data(withJSONObject: obj))
        client?.urlProtocolDidFinishLoading(self)
    }

    private func ok(_ v: Any) { reply(200, ["result": v]) }
    private func fail(_ code: Int, _ msg: String) { reply(code, ["error": msg]) }

    static func newTask(agent: String, title: String, links: [String], by: String) -> [String: Any] {
        let n = (tasks.compactMap { Int(($0["id"] as? String ?? "").dropFirst(2)) }.max() ?? 0) + 1
        let t: [String: Any] = ["id": "T-\(n)", "agentId": agent, "title": title, "instructions": "", "status": "queued",
                                "priority": "normal", "createdBy": by, "createdAt": now, "updatedAt": now,
                                "links": links, "notes": NSNull(), "result": ""]
        tasks.append(t)
        return t
    }

    /// core 的 syncAssignee:舊角色未領取的取消;新角色(啟用中)沒有未結束任務就排一筆
    static func syncAssignee(issue: [String: Any], old: String?) {
        let id = issue["id"] as! String
        let new = issue["assignee"] as? String ?? ""
        if let old, !old.isEmpty, old != new {
            for i in tasks.indices where tasks[i]["agentId"] as? String == old && tasks[i]["status"] as? String == "queued"
                && (tasks[i]["links"] as? [String] ?? []).contains(id) { tasks[i]["status"] = "canceled" }
        }
        guard !new.isEmpty, agents.contains(where: { $0["id"] as? String == new && $0["active"] as? Bool == true }) else { return }
        let open = tasks.contains { $0["agentId"] as? String == new && (($0["links"] as? [String]) ?? []).contains(id)
            && ["queued", "inProgress", "blocked"].contains($0["status"] as? String ?? "") }
        if !open { _ = newTask(agent: new, title: "處理 \(id):\(issue["title"] as? String ?? "")", links: [id], by: "tim@example.com") }
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
        let path = request.url!.path
        let method = request.httpMethod ?? "GET"
        Self.requests.append((method, path, body))
        if path == "/api/version" {
            return reply(200, ["version": "849e1b2aa55426a942433830e144c2205ab3082c", "commit": "849e1b2aa55426a942433830e144c2205ab3082c",
                               "buildTime": "2026-11-12T09:08:25Z"])
        }
        let p = path.split(separator: "/").map(String.init)   // api orgs <org> <kind> …
        guard p.count >= 4, p[0] == "api", p[1] == "orgs" else { return fail(404, "?") }
        let rest = Array(p.dropFirst(4))
        switch (p[3], method, rest.count) {
        case ("agents", "GET", 0):
            ok(Self.agents)
        case ("agents", "POST", 0):
            var a = body
            if a["id"] == nil || (a["id"] as? String ?? "").isEmpty { a["id"] = "new-agent" }
            if let i = Self.agents.firstIndex(where: { $0["id"] as? String == a["id"] as? String }) { Self.agents[i] = a } else { Self.agents.append(a) }
            ok(a)
        case ("agents", "POST", 2) where rest[1] == "delete":
            Self.agents.removeAll { $0["id"] as? String == rest[0] }
            ok(true)
        case ("agent-tasks", "GET", 0):
            ok(Self.tasks)
        case ("agent-tasks", "POST", 0):
            guard Self.agents.contains(where: { $0["id"] as? String == body["agentId"] as? String }) else { return fail(400, "找不到角色") }
            ok(Self.newTask(agent: body["agentId"] as! String, title: body["title"] as? String ?? "", links: body["links"] as? [String] ?? [],
                            by: "tim@example.com"))
        case ("agent-tasks", "POST", 1) where rest[0] == "claim",
             ("agent-tasks", "POST", 2) where rest[1] == "claim":
            let want = rest.count == 2 ? rest[0] : nil
            guard let i = Self.tasks.firstIndex(where: { t in
                t["agentId"] as? String == body["agentId"] as? String && (want == nil ? t["status"] as? String == "queued" : t["id"] as? String == want)
            }) else { return ok(["task": NSNull()]) }
            if Self.tasks[i]["status"] as? String == "inProgress" { return fail(409, "\(want ?? "") 已被領取,鎖定到 11:50") }
            Self.tasks[i]["status"] = "inProgress"
            Self.tasks[i]["claimedBy"] = "tim@example.com"
            Self.tasks[i]["lockedUntil"] = "2026-11-12T11:50:51Z"
            let agent = Self.agents.first { $0["id"] as? String == body["agentId"] as? String } ?? [:]
            ok(["agent": agent, "task": Self.tasks[i],
                "credential": ["agentId": body["agentId"]!, "taskId": Self.tasks[i]["id"]!, "secret": "s3cret", "expiresAt": "2026-11-12T11:50:51Z"]])
        case ("agent-tasks", "POST", 2):
            guard let i = Self.tasks.firstIndex(where: { $0["id"] as? String == rest[0] }) else { return fail(404, "找不到任務 \(rest[0])") }
            switch rest[1] {
            case "report":
                if let s = body["status"] { Self.tasks[i]["status"] = s }
                if let r = body["result"] { Self.tasks[i]["result"] = r }
                if let n = body["note"] as? String {
                    var notes = Self.tasks[i]["notes"] as? [[String: Any]] ?? []
                    notes.append(["id": "n\(notes.count + 1)", "author": "程式設計師", "text": n, "at": Self.now])
                    Self.tasks[i]["notes"] = notes
                }
            case "cancel": Self.tasks[i]["status"] = "canceled"
            case "requeue": Self.tasks[i]["status"] = "queued"; Self.tasks[i].removeValue(forKey: "lockedUntil")
            default: return fail(404, "?")
            }
            ok(Self.tasks[i])
        case ("issues", "GET", 0):
            ok(Self.issues)
        case ("issues", "POST", 0):
            if let a = body["assignee"] as? String, !a.isEmpty, !Self.agents.contains(where: { $0["id"] as? String == a }) {
                return fail(400, "assignee 指定的角色不存在:\(a)")
            }
            var i: [String: Any] = ["id": "ISS-\(Self.issues.count + 1)", "title": body["title"] ?? "", "body": body["body"] ?? "", "status": "open",
                                    "priority": body["priority"] ?? "normal", "labels": body["labels"] ?? [], "source": "", "link": "",
                                    "createdBy": "tim@example.com", "createdAt": Self.now, "updatedAt": Self.now, "comments": []]
            if let a = body["assignee"] as? String, !a.isEmpty { i["assignee"] = a }
            Self.issues.insert(i, at: 0)
            if i["assignee"] != nil { Self.syncAssignee(issue: i, old: nil) }
            ok(i)
        case ("issues", "POST", 1):
            guard let k = Self.issues.firstIndex(where: { $0["id"] as? String == rest[0] }) else { return fail(404, "找不到") }
            var i = Self.issues[k]
            let old = i["assignee"] as? String
            for f in ["title", "body", "status", "priority", "labels", "comments"] where body[f] != nil { i[f] = body[f] }
            if let a = body["assignee"] as? String {
                if a.isEmpty { i.removeValue(forKey: "assignee") } else { i["assignee"] = a }
            }
            Self.issues[k] = i
            if body["assignee"] != nil { Self.syncAssignee(issue: i, old: old) }
            ok(i)
        default:
            fail(404, "?")
        }
    }
}

@MainActor
final class AgentRemoteTests: XCTestCase {
    var dataDir: URL!
    let org = "~omixor"

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-agents-remote-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        IssueRemote.protocolClasses = [FakeOmixorServer.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: "tok-1")
        FakeOmixorServer.reset()
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
        app.startIssues()
        return app
    }

    private func call(_ mcp: MCPHandler, _ name: String, _ args: [String: Any]) async throws -> (Any?, String, Bool) {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        return (try? JSONSerialization.jsonObject(with: Data(text.utf8)), text, r["isError"] as? Bool ?? false)
    }

    /// 伺服器的任務:沒有 routineId、links/notes 是 null、留言 id 不是 UUID,都要讀得出來
    func testLenientTaskDecoding() throws {
        let json = """
        [{"id":"T-1","agentId":"programmer","title":"x","instructions":"","status":"inProgress","priority":"high","createdBy":"mcp",
          "createdAt":"2026-11-12T07:55:04Z","updatedAt":"2026-11-12T09:59:33Z","claimedAt":"2026-11-12T09:50:51Z",
          "claimedBy":"tim@example.com","lockedUntil":"2026-11-12T11:59:33Z","links":null,"result":"",
          "notes":[{"at":"2026-11-12T09:59:33Z","author":"程式設計師","id":"n-1","text":"開始實作"},{"author":"a","text":"沒有 id"}]},
         {"id":"T-2","agentId":"programmer","title":"y","status":"weird","priority":"?","createdBy":"mcp",
          "createdAt":"2026-11-12T07:55:04Z","updatedAt":"2026-11-12T07:55:04Z","notes":null}]
        """
        let list = try IssueRemote.decoder().decode([AgentTask].self, from: Data(json.utf8))
        XCTAssertEqual(list.count, 2)
        XCTAssertEqual(list[0].status, .inProgress)
        XCTAssertNil(list[0].routineId)
        XCTAssertEqual(list[0].links, [])
        XCTAssertEqual(list[0].notes.map(\.text), ["開始實作", "沒有 id"])
        XCTAssertEqual(list[0].notes[0].id, AgentTaskNote.stableId("n-1"), "同一則留言每次讀都是同一個 id")
        XCTAssertEqual(list[0].claimedBy, "tim@example.com")
        XCTAssertNotNil(list[0].lockedUntil)
        XCTAssertNotNil(list[0].leaseLabel)
        XCTAssertEqual(list[1].status, .queued, "不認得的狀態不讓整份讀不出來")
        XCTAssertEqual(list[1].priority, .normal)
        XCTAssertNil(list[1].leaseLabel)
    }

    /// 遠端模式:角色與任務來自伺服器,組織 id 去掉 "~",不寫本機 agents.json
    func testLoadFromServer() throws {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "處理 ISS-13", links: ["ISS-13"], by: "mcp")
        let app = remoteApp()
        let store = try XCTUnwrap(app.agentStore)
        XCTAssertTrue(store.isRemote)
        XCTAssertNil(store.remoteError)
        XCTAssertEqual(app.agents.map(\.id), ["programmer", "tester"])
        XCTAssertEqual(app.agents.first?.docAccess["docs"], .write)
        XCTAssertEqual(app.agentTasks.map(\.id), ["T-1"])
        XCTAssertEqual(app.agentTasks.first?.issueId, "ISS-13")
        XCTAssertTrue(FakeOmixorServer.requests.contains { $0.path == "/api/orgs/omixor/agents" })
        XCTAssertTrue(FakeOmixorServer.requests.contains { $0.path == "/api/orgs/omixor/agent-tasks" })
        store.runDueRoutines(now: .distantFuture)
        XCTAssertEqual(app.agentTasks.count, 1, "遠端模式的例行任務由伺服器排")
        XCTAssertFalse(FileManager.default.fileExists(atPath: app.dataDir.appendingPathComponent("agents.json").path))
    }

    /// 派任務、領取(發憑證)、回報、取消、重新排入都送到伺服器
    func testTaskLifecycle() throws {
        let app = remoteApp()
        let store = try XCTUnwrap(app.agentStore)
        let t = try XCTUnwrap(store.assign(to: "programmer", title: "寫測試", links: ["ISS-2"], by: "tim"))
        XCTAssertEqual(t.id, "T-1")
        XCTAssertEqual(FakeOmixorServer.requests.last?.path, "/api/orgs/omixor/agent-tasks")
        XCTAssertEqual(FakeOmixorServer.requests.last?.body["links"] as? [String], ["ISS-2"])

        let c = try store.claimWithCredential(agent: "程式設計師", task: "T-1")
        XCTAssertEqual(FakeOmixorServer.requests.last?.path, "/api/orgs/omixor/agent-tasks/T-1/claim")
        XCTAssertEqual(FakeOmixorServer.requests.last?.body["agentId"] as? String, "programmer", "名稱換成角色 id 再送")
        XCTAssertEqual(c.task?.status, .inProgress)
        XCTAssertEqual(c.credential?.secret, "s3cret")
        XCTAssertEqual(c.credential?.taskId, "T-1")
        XCTAssertEqual(c.credential?.endpoint, "http://127.0.0.1:5998/mcp/agent", "伺服器沒給 endpoint 時用連線網址組出來")
        XCTAssertEqual(app.agentTasks.first?.leaseLabel?.contains("鎖定到"), true)

        // 已被鎖住:錯誤原文要傳出來
        XCTAssertThrowsError(try store.claim(agent: "programmer", task: "T-1")) { e in
            XCTAssertTrue(e.localizedDescription.contains("已被領取") || "\(e)".contains("已被領取"), "\(e)")
        }

        let r = try store.report("T-1", status: .blocked, result: nil, note: "要人決定", links: nil)
        XCTAssertEqual(r?.status, .blocked)
        XCTAssertEqual(r?.notes.first?.text, "要人決定")
        XCTAssertEqual(app.agentTasks.first { $0.id == "T-1" }?.status, .blocked)

        XCTAssertEqual(store.requeue("T-1")?.status, .queued)
        XCTAssertEqual(FakeOmixorServer.requests.last?.path, "/api/orgs/omixor/agent-tasks/T-1/requeue")
        XCTAssertEqual(store.cancel("T-1")?.status, .canceled)
        XCTAssertEqual(FakeOmixorServer.requests.last?.path, "/api/orgs/omixor/agent-tasks/T-1/cancel")
        XCTAssertNil(store.update("T-1") { $0.status = .done }, "遠端模式不能直接改狀態")
    }

    /// 角色的新增、修改、刪除送到伺服器
    func testAgentUpsertAndDelete() throws {
        let app = remoteApp()
        let store = try XCTUnwrap(app.agentStore)
        var a = try XCTUnwrap(store.agent("tester"))
        a.active = true
        a.docAccess["levels"] = .read
        let saved = try store.save(agent: a)
        XCTAssertTrue(saved.active)
        let body = try XCTUnwrap(FakeOmixorServer.requests.last?.body)
        XCTAssertEqual(body["id"] as? String, "tester")
        XCTAssertEqual((body["docAccess"] as? [String: String])?["levels"], "read")
        XCTAssertEqual(app.agents.first { $0.id == "tester" }?.active, true)
        store.delete("tester")
        XCTAssertEqual(FakeOmixorServer.requests.map(\.path).last { $0.hasSuffix("/delete") }, "/api/orgs/omixor/agents/tester/delete")
        XCTAssertFalse(app.agents.contains { $0.id == "tester" })
    }

    /// Issue 指派:建立時帶 assignee 會排任務;只有指派真的改變時才送 assignee(避免已完成的任務被重新排)
    func testIssueAssignee() throws {
        let app = remoteApp()
        let issues = try XCTUnwrap(app.issueStore)
        let i = issues.create(title: "做 app", assignee: "programmer", by: "tim")
        XCTAssertEqual(i.assignee, "programmer")
        XCTAssertEqual(FakeOmixorServer.requests.first { $0.path == "/api/orgs/omixor/issues" && $0.method == "POST" }?.body["assignee"] as? String,
                       "programmer")
        XCTAssertEqual(app.agentTasks.filter { $0.links.contains(i.id) }.count, 1, "伺服器排的任務要讀回來")

        // 任務做完後改別的欄位:不能送 assignee,否則伺服器會重新排一筆
        FakeOmixorServer.tasks[0]["status"] = "done"
        _ = issues.update(i.id, by: "tim") { $0.title = "做 app(改)" }
        let upd = try XCTUnwrap(FakeOmixorServer.requests.last { $0.path == "/api/orgs/omixor/issues/\(i.id)" })
        XCTAssertNil(upd.body["assignee"], "指派沒變就不送 assignee")
        XCTAssertEqual(FakeOmixorServer.tasks.count, 1)

        // 改指派給停用角色:送出,舊角色沒有未領取任務要取消;停用角色不排任務
        _ = issues.update(i.id, by: "tim") { $0.assignee = "tester" }
        XCTAssertEqual(FakeOmixorServer.requests.last { $0.path == "/api/orgs/omixor/issues/\(i.id)" }?.body["assignee"] as? String, "tester")
        XCTAssertEqual(app.issues.first { $0.id == i.id }?.assignee, "tester")
        XCTAssertEqual(FakeOmixorServer.tasks.count, 1)

        // 取消指派:送空字串
        _ = issues.update(i.id, by: "tim") { $0.assignee = nil }
        XCTAssertEqual(FakeOmixorServer.requests.last { $0.path == "/api/orgs/omixor/issues/\(i.id)" }?.body["assignee"] as? String, "")
        XCTAssertNil(app.issues.first { $0.id == i.id }?.assignedAgent)

        // 角色不存在:伺服器回 400,錯誤原文留著
        let bad = issues.create(title: "x", assignee: "nobody", by: "tim")
        XCTAssertEqual(bad.id, "")
        XCTAssertEqual(issues.remoteError, "assignee 指定的角色不存在:nobody")
    }

    /// MCP:ops_agent_task_claim 回傳憑證;ops_issue_create 可以帶 assignee;ops_server 回版本
    func testMCPClaimAndServerInfo() async throws {
        let app = remoteApp()
        let mcp = MCPHandler(app: app)
        var (obj, text, err) = try await call(mcp, "ops_issue_create", ["title": "要處理的", "assignee": "programmer"])
        XCTAssertFalse(err, text)
        XCTAssertEqual((obj as? [String: Any])?["assignee"] as? String, "programmer")

        (obj, text, err) = try await call(mcp, "ops_agent_task_claim", ["agent": "programmer"])
        XCTAssertFalse(err, text)
        let d = try XCTUnwrap(obj as? [String: Any])
        XCTAssertEqual((d["task"] as? [String: Any])?["id"] as? String, "T-1")
        let cred = try XCTUnwrap(d["credential"] as? [String: Any])
        XCTAssertEqual(cred["agentId"] as? String, "programmer")
        XCTAssertEqual(cred["endpoint"] as? String, "http://127.0.0.1:5998/mcp/agent")
        XCTAssertNotNil(cred["secret"])

        (obj, text, err) = try await call(mcp, "ops_issue_create", ["title": "x", "assignee": "nobody"])
        XCTAssertTrue(err, text)

        (obj, text, err) = try await call(mcp, "ops_server", [:])
        XCTAssertFalse(err, text)
        // testOverride 不改 OmixorMode,所以這裡是本機模式的輸出:只有 app 版本
        XCTAssertNotNil(((obj as? [String: Any])?["version"] as? [String: Any])?["app"])
    }

    /// GET /api/version:不包 result;version 等於 commit 時只顯示短 commit
    func testServerVersion() async throws {
        let v = try await IssueRemote.fetchVersion(URL(string: "http://127.0.0.1:5998")!)
        XCTAssertEqual(v.commit, "849e1b2aa55426a942433830e144c2205ab3082c")
        XCTAssertTrue(v.label.hasPrefix("849e1b2"), v.label)
        XCTAssertEqual(try IssueRemote(config: IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: nil)).version(), v)
        XCTAssertEqual(ServerVersion(version: "1.2.0", commit: "abcdef123456", buildTime: "dev").label, "1.2.0(abcdef1)")
    }
}
