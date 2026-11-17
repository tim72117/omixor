import XCTest
@testable import Ops

/// 組織鏈:驗證、角色委派、done/blocked 回報、MCP 工具、遠端請求格式
@MainActor
final class OrgChainTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        try TestCore.start(self)   // 資料一律透過 core server:每個測試一個新的本機守護服務
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-chain-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func call(_ mcp: MCPHandler, _ name: String, _ args: [String: Any]) async throws -> ([String: Any], String, Bool) {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        return ((try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] ?? [:], text, r["isError"] as? Bool ?? false)
    }

    private func e(_ f: String, _ t: String, _ l: String = "") -> ChainEdge { ChainEdge(from: f, to: t, label: l) }

    /// 驗證:自環、重複、循環、不存在的節點;正規化角色名稱與 email 大小寫
    func testValidation() throws {
        let app = AppState()
        let store = app.startTestAgents()
        XCTAssertThrowsError(try store.saveChain([e("agent:triager", "agent:triager")])) { XCTAssertTrue("\($0.localizedDescription)".contains("自己")) }
        XCTAssertThrowsError(try store.saveChain([e("agent:triager", "agent:marketing"), e("agent:triager", "agent:marketing", "x")])) {
            XCTAssertTrue($0.localizedDescription.contains("重複"), $0.localizedDescription)
        }
        XCTAssertThrowsError(try store.saveChain([e("agent:triager", "agent:marketing"), e("agent:marketing", "agent:narrator"),
                                                  e("agent:narrator", "agent:triager")])) {
            XCTAssertTrue($0.localizedDescription.contains("循環"), $0.localizedDescription)
        }
        XCTAssertThrowsError(try store.saveChain([e("agent:nobody", "agent:triager")])) { XCTAssertTrue($0.localizedDescription.contains("找不到角色")) }
        XCTAssertThrowsError(try store.saveChain([e("user:stranger", "agent:triager")])) { XCTAssertTrue($0.localizedDescription.contains("不是組織成員"), $0.localizedDescription) }
        XCTAssertTrue(app.orgChain.edges.isEmpty, "驗證失敗不能存")
        // 人要是組織成員(core 的規則)
        _ = try store.remote.sendJSON("POST", store.remote.orgURL(app.currentOrgId, ["members"]), ["email": "ceo@example.com", "role": "member"])

        let c = try store.saveChain([e("user:CEO@Example.com", "agent:課程開發", "目標"), e("agent:course-dev", "agent:marketing")])
        XCTAssertEqual(c.edges.map(\.from), ["user:ceo@example.com", "agent:course-dev"])
        XCTAssertEqual(c.edges.first?.to, "agent:course-dev")
        XCTAssertEqual(c.layers(), [["user:ceo@example.com"], ["agent:course-dev"], ["agent:marketing"]])

        // 存在 core server(不寫 org-chain.json),重開讀得回來;刪角色時相關的邊一併拿掉
        XCTAssertFalse(FileManager.default.fileExists(atPath: app.dataDir.appendingPathComponent("org-chain.json").path))
        let app2 = AppState()
        let store2 = app2.startAgents(timer: false)
        XCTAssertEqual(app2.orgChain.edges, c.edges)
        store2.delete("marketing")
        XCTAssertEqual(app2.orgChain.edges.count, 1)
        XCTAssertEqual(OrgChain.findCycle([e("a", "b"), e("b", "a")]), ["a", "b", "a"])
    }

    /// 角色委派:鏈上有邊才可以;任務署名委派者、links 帶來源任務、label 寫在說明開頭
    func testDelegate() throws {
        let app = AppState()
        let store = app.startTestAgents()
        try store.saveChain([e("agent:course-dev", "agent:marketing", "目標")])
        let src = try XCTUnwrap(store.assign(to: "course-dev", title: "規劃課程", links: ["ISS-3"], by: "tim"))
        _ = try store.claim(agent: "course-dev", task: src.id)

        let t = try store.delegate(from: "course-dev", to: "行銷", title: "寫推廣文", instructions: "寫三篇", sourceTask: src.id)
        XCTAssertEqual(t.agentId, "marketing")
        XCTAssertEqual(t.createdBy, "agent:course-dev")
        XCTAssertEqual(t.links, [src.id, "ISS-3"])
        XCTAssertEqual(t.instructions, "【目標】\n\n寫三篇")

        XCTAssertThrowsError(try store.delegate(from: "marketing", to: "course-dev", title: "x", instructions: "")) {
            XCTAssertTrue($0.localizedDescription.contains("組織鏈上"), $0.localizedDescription)
        }
        XCTAssertThrowsError(try store.delegate(from: "triager", to: "marketing", title: "x", instructions: "")) {
            XCTAssertTrue($0.localizedDescription.contains("組織鏈上"), $0.localizedDescription)
        }
    }

    /// 回報:角色建立者 → 排「檢視回報」任務(不連鎖、不重複);真人建立者 → 備註與「回報給我的任務」
    func testReport() throws {
        let app = AppState()
        let store = app.startTestAgents()
        // core 只把 email(或 user:<…>)署名的任務當成鏈上的人(model.NodeOfAuthor);本機使用者 tim 不是 email,這裡用另一位成員測
        _ = try store.remote.sendJSON("POST", store.remote.orgURL(app.currentOrgId, ["members"]), ["email": "ceo@example.com", "role": "member"])
        try store.saveChain([e("agent:course-dev", "agent:marketing"), e("user:ceo@example.com", "agent:course-dev", "目標")])

        let t = try store.delegate(from: "course-dev", to: "marketing", title: "寫推廣文", instructions: "")
        _ = try store.claim(agent: "marketing", task: t.id)
        let done = try XCTUnwrap(try store.report(t.id, status: .done, result: "寫好了", note: nil, links: nil))
        let review = try XCTUnwrap(app.agentTasks.first { $0.title == "檢視回報:寫推廣文" })
        XCTAssertEqual(review.agentId, "course-dev")
        XCTAssertEqual(review.createdBy, ChainNode.author)
        XCTAssertEqual(review.status, .queued)
        XCTAssertEqual(review.links.first, t.id)
        XCTAssertTrue(review.instructions.contains("寫好了"))
        XCTAssertEqual(store.task(done.id)?.notes.last?.text, "已回報給 課程開發(\(review.id))")
        XCTAssertEqual(store.task(done.id)?.notes.last?.author, ChainNode.author)

        // 檢視回報任務做完不再觸發回報
        let count = app.agentTasks.count
        _ = try store.claim(agent: "course-dev", task: review.id)
        _ = try store.report(review.id, status: .done, result: "ok", note: nil, links: nil)
        XCTAssertEqual(app.agentTasks.count, count)

        // blocked:帶 note;已經有未結束的檢視任務就不重複排
        let t2 = try store.delegate(from: "course-dev", to: "marketing", title: "拍影片", instructions: "")
        _ = try store.claim(agent: "marketing", task: t2.id)
        _ = try store.report(t2.id, status: .blocked, result: nil, note: "預算不夠", links: nil)
        let r2 = try XCTUnwrap(app.agentTasks.first { $0.title == "檢視回報:拍影片" })
        XCTAssertTrue(r2.instructions.contains("預算不夠"), r2.instructions)
        _ = try store.claim(agent: "marketing", task: t2.id)
        _ = try store.report(t2.id, status: .done, result: "拍好了", note: nil, links: nil)
        XCTAssertEqual(app.agentTasks.filter { $0.title == "檢視回報:拍影片" }.count, 1)

        // 真人建立者:不建任務,原任務留備註,出現在「回報給我的任務」
        let h = try XCTUnwrap(store.assign(to: "course-dev", title: "訂季度目標", by: "ceo@example.com"))
        let before = app.agentTasks.count
        _ = try store.claim(agent: "course-dev", task: h.id)
        _ = try store.report(h.id, status: .done, result: "定好了", note: nil, links: nil)
        XCTAssertEqual(app.agentTasks.count, before)
        XCTAssertEqual(store.task(h.id)?.notes.last?.text, "已回報給 ceo@example.com", "\(String(describing: store.task(h.id)))")
        XCTAssertEqual(try store.remote.reportedTasks(org: app.currentOrgId, reportTo: "user:ceo@example.com").map(\.id), [h.id])

        // 沒有邊:不回報
        let n = try XCTUnwrap(store.assign(to: "marketing", title: "沒有鏈", by: "tim"))
        _ = try store.claim(agent: "marketing", task: n.id)
        _ = try store.report(n.id, status: .done, result: "x", note: nil, links: nil)
        XCTAssertFalse(store.task(n.id)?.notes.contains { $0.author == ChainNode.author } ?? true)
    }

    /// MCP:chain_save(add / remove / edges)、chain、agent_context 的上下游、agent_task_delegate
    func testMCPTools() async throws {
        let app = AppState()
        app.startTestAgents()
        let mcp = MCPHandler(app: app)
        var (d, text, err) = try await call(mcp, "chain_save", [:])
        XCTAssertTrue(err, text)
        (d, text, err) = try await call(mcp, "chain_save", ["add": [["from": "user:tim", "to": "agent:course-dev", "label": "目標"],
                                                                      ["from": "agent:course-dev", "to": "agent:marketing"]]])
        XCTAssertFalse(err, text)
        XCTAssertEqual((d["edges"] as? [[String: Any]])?.count, 2)
        (d, text, err) = try await call(mcp, "chain_save", ["add": [["from": "agent:marketing", "to": "agent:course-dev"]]])
        XCTAssertTrue(err, text)
        XCTAssertTrue(text.contains("循環"), text)

        (d, text, err) = try await call(mcp, "chain", [:])
        XCTAssertFalse(err, text)
        let edges = try XCTUnwrap(d["edges"] as? [[String: Any]])
        XCTAssertEqual(edges.first?["toName"] as? String, "課程開發")
        XCTAssertEqual(edges.first?["label"] as? String, "目標")

        (d, text, err) = try await call(mcp, "agent_context", ["agent": "course-dev"])
        XCTAssertFalse(err, text)
        let chain = try XCTUnwrap(d["chain"] as? [String: Any], text)
        XCTAssertEqual((chain["upstream"] as? [[String: Any]])?.first?["node"] as? String, "user:tim")
        XCTAssertEqual((chain["downstream"] as? [[String: Any]])?.first?["name"] as? String, "行銷")
        XCTAssertEqual((chain["downstream"] as? [[String: Any]])?.first?["label"] as? String, "任務")

        (d, text, err) = try await call(mcp, "agent_task_delegate", ["agent": "course-dev", "to": "marketing", "title": "寫文"])
        XCTAssertFalse(err, text)
        XCTAssertEqual(d["agentId"] as? String, "marketing")
        XCTAssertEqual(d["instructions"] as? String, "【任務】", "說明前綴由 core 加")
        (d, text, err) = try await call(mcp, "agent_task_delegate", ["agent": "marketing", "to": "course-dev", "title": "x"])
        XCTAssertTrue(err, text)

        (d, text, err) = try await call(mcp, "chain_save", ["remove": [["from": "agent:course-dev", "to": "agent:marketing"]]])
        XCTAssertFalse(err, text)
        XCTAssertEqual((d["edges"] as? [[String: Any]])?.count, 1)
    }

    /// 遠端:讀寫 /chain、委派送 /agent-tasks/delegate,錯誤原文丟出
    func testRemote() throws {
        IssueRemote.protocolClasses = [FakeOmixorServer.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: "tok-1")
        FakeOmixorServer.reset()
        FakeOmixorServer.chain = [["from": "user:tim@example.com", "to": "agent:programmer", "label": "目標"]]
        let app = AppState()
        app.currentOrgId = "~omixor"
        let store = app.startAgents(timer: false)
        XCTAssertEqual(app.orgChain.edges.first?.label, "目標")
        XCTAssertTrue(FakeOmixorServer.requests.contains { $0.method == "GET" && $0.path == "/api/orgs/omixor/chain" })

        let c = try store.saveChain([e("agent:programmer", "agent:tester", "任務")])
        XCTAssertEqual(c.edges.count, 1)
        let post = try XCTUnwrap(FakeOmixorServer.requests.last { $0.method == "POST" && $0.path == "/api/orgs/omixor/chain" })
        XCTAssertEqual((post.body["edges"] as? [[String: Any]])?.first?["to"] as? String, "agent:tester")
        XCTAssertThrowsError(try store.saveChain([e("agent:tester", "agent:tester")])) {
            XCTAssertEqual($0.localizedDescription, "邊不能指向自己")
        }

        let t = try store.delegate(from: "programmer", to: "tester", title: "測一下", instructions: "跑全部", sourceTask: "T-9")
        XCTAssertEqual(t.createdBy, "agent:programmer")
        let del = try XCTUnwrap(FakeOmixorServer.requests.last)
        XCTAssertEqual(del.path, "/api/orgs/omixor/agent-tasks/delegate")
        XCTAssertEqual(del.body["from"] as? String, "programmer")
        XCTAssertEqual(del.body["to"] as? String, "tester")
        XCTAssertEqual(del.body["taskId"] as? String, "T-9")
        XCTAssertEqual(del.body["instructions"] as? String, "跑全部", "label 前綴由伺服器加")
        XCTAssertThrowsError(try store.delegate(from: "tester", to: "programmer", title: "x", instructions: "")) {
            XCTAssertTrue($0.localizedDescription.contains("組織鏈上不能派給"), $0.localizedDescription)
        }
        let r = try XCTUnwrap(store.remote)
        XCTAssertEqual(try r.listMembers(org: "~omixor").first?.email, "tim@example.com")
        _ = try r.reportedTasks(org: "~omixor", reportTo: "me")
        XCTAssertTrue(FakeOmixorServer.requests.last?.path == "/api/orgs/omixor/agent-tasks")
    }
}
