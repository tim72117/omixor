import XCTest
@testable import Ops

@MainActor
final class CabinetTests: XCTestCase {
    var dataDir: URL!
    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-cabinet-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }
    override func tearDown() async throws {
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

    func testRoleScopedDocs() async throws {
        let app = AppState()
        app.startTestAgents()
        let mcp = MCPHandler(app: app)

        // 課程開發:課程區塊可讀寫
        var (d, text, err) = try await call(mcp, "ops_doc_save", ["agent": "course-dev", "tab": "L0", "title": "L0 改版筆記", "body": "# 筆記"])
        XCTAssertFalse(err, text)
        XCTAssertEqual(d["location"] as? String, "課程 › L0 AI 工具介紹")
        XCTAssertEqual(d["author"] as? String, "課程開發")

        // 行銷:課程只能讀,不能寫;行銷研究那一頁可寫
        (_, text, err) = try await call(mcp, "ops_doc_save", ["agent": "marketing", "section": "levels", "title": "x"])
        XCTAssertTrue(err)
        XCTAssertTrue(text.contains("沒有權限寫入"), text)
        (_, text, err) = try await call(mcp, "ops_doc_save", ["agent": "marketing", "tab": "/docs/research/marketing", "title": "社群貼文草稿"])
        XCTAssertFalse(err, text)
        (d, _, _) = try await call(mcp, "ops_docs", ["agent": "marketing"])
        XCTAssertEqual(d["total"] as? Int, 2, "課程可讀、行銷研究可寫")

        // 訪客分析師看不到課程文件
        (d, _, _) = try await call(mcp, "ops_docs", ["agent": "visitor-analyst"])
        XCTAssertEqual(d["total"] as? Int, 0)
        (_, text, err) = try await call(mcp, "ops_docs", ["agent": "visitor-analyst", "id": "DOC-1"])
        XCTAssertTrue(err, text)

        // 結構:只列有權限的位置
        (d, _, _) = try await call(mcp, "ops_cabinet", ["agent": "visitor-analyst"])
        XCTAssertEqual((d["sections"] as? [[String: Any]])?.compactMap { $0["section"] as? String }, ["overview", "members"])

        // 用 MCP 調整範圍:給訪客分析師讀課程
        (_, text, err) = try await call(mcp, "ops_agent_upsert", ["id": "visitor-analyst", "doc_access": ["課程": "read", "/docs/course-goals": "write"]])
        XCTAssertFalse(err, text)
        let va = try XCTUnwrap(app.agents.first { $0.id == "visitor-analyst" })
        XCTAssertTrue(va.canRead(section: "levels", tab: "/studio/tools"))
        XCTAssertTrue(va.canWrite(section: "docs", tab: "/docs/course-goals"))
        XCTAssertFalse(va.canRead(section: "docs", tab: "/docs/live"))
        (_, _, err) = try await call(mcp, "ops_agent_upsert", ["id": "visitor-analyst", "doc_access": ["不存在": "read"]])
        XCTAssertTrue(err)

        // 沒帶 agent:MCP 用戶端,全部可讀寫
        (d, _, _) = try await call(mcp, "ops_docs", ["query": "筆記"])
        XCTAssertEqual(d["total"] as? Int, 1)
        (d, text, err) = try await call(mcp, "ops_doc_save", ["id": "DOC-1", "append_body": "補充"])
        XCTAssertFalse(err, text)
        XCTAssertEqual(Cabinet.doc(app, "DOC-1")?.body, "# 筆記\n\n補充")
    }

    func testDocsPersistAcrossRestart() throws {
        let app = AppState()
        app.startCabinet()
        let d = try! Cabinet.create(app, title: "規劃", body: "內文", section: "overview", tab: nil, tags: ["a"], by: "agent:product-manager")
        try! Cabinet.update(app, d.id) { $0.body += "\n補充" }
        try! Cabinet.create(app, title: "第二份", body: "", section: "levels", tab: "/studio/tools", tags: [], by: "tim")

        let app2 = AppState()
        app2.startCabinet()
        XCTAssertEqual(app2.cabinetDocs.map(\.id), ["DOC-2", "DOC-1"])
        let r = try XCTUnwrap(Cabinet.doc(app2, "DOC-1"))
        XCTAssertEqual(r.body, "內文\n補充")
        XCTAssertEqual(r.author, "agent:product-manager")
        XCTAssertEqual(Cabinet.doc(app2, "DOC-2")?.tab, "/studio/tools")
        try! Cabinet.delete(app2, "DOC-2")
        let app3 = AppState()
        app3.startCabinet()
        XCTAssertEqual(app3.cabinetDocs.map(\.id), ["DOC-1"])
        // 下一份編號接著最大的
        XCTAssertEqual(try! Cabinet.create(app3, title: "x", body: "", section: "todo", tab: nil, tags: [], by: "tim").id, "DOC-2")
    }

    func testUnreadableCabinetIsKeptNotOverwritten() throws {
        try Data("not json".utf8).write(to: dataDir.appendingPathComponent("cabinet.json"))
        let app = AppState()
        app.startCabinet()
        XCTAssertTrue(app.cabinetDocs.isEmpty)
        let files = try FileManager.default.contentsOfDirectory(atPath: dataDir.appendingPathComponent("orgs/default").path)
        XCTAssertTrue(files.contains { $0.hasPrefix("cabinet.unreadable-") }, "\(files)")
    }

    func testOldAgentsFileStillLoads() throws {
        // 沒有 docAccess 欄位的舊檔要讀得出來,不能被預設值蓋掉
        let old = #"{"agents":[{"id":"x","name":"舊角色","routines":[]}],"tasks":[]}"#
        let app = AppState()
        try Data(old.utf8).write(to: app.dataDir.appendingPathComponent("agents.json"))
        app.startAgents(timer: false)
        XCTAssertEqual(app.agents.map(\.name), ["舊角色"])
        XCTAssertEqual(app.agents.first?.docAccess, [:])
    }

    /// ops_agent_context:只回傳角色能讀的文件、標出讀寫權限,任務只列這個角色未完成的
    func testAgentContext() async throws {
        let app = AppState()
        app.startTestAgents()
        let mcp = MCPHandler(app: app)
        _ = try await call(mcp, "ops_doc_save", ["title": "L0", "tab": "L0", "body": "你好"])
        _ = try await call(mcp, "ops_doc_save", ["title": "待辦", "section": "todo", "body": "x"])
        _ = try await call(mcp, "ops_agent_task_create", ["assign_to": "narrator", "title": "補語音"])
        _ = try await call(mcp, "ops_agent_task_create", ["assign_to": "triager", "title": "整理"])
        var (_, text, err) = try await call(mcp, "ops_agent_context", ["agent": "nobody"])
        XCTAssertTrue(err)
        XCTAssertEqual(text, "找不到角色 nobody")

        var c: [String: Any]
        (c, _, err) = try await call(mcp, "ops_agent_context", ["agent": "旁白製作"])
        XCTAssertFalse(err)
        XCTAssertEqual((c["agent"] as? [String: Any])?["id"] as? String, "narrator")
        var docs = try XCTUnwrap((c["docs"] as? [String: Any])?["docs"] as? [[String: Any]])
        XCTAssertEqual(docs.map { $0["title"] as? String }, ["L0"])
        XCTAssertEqual(docs[0]["access"] as? String, "read")
        XCTAssertNil(docs[0]["body"], "預設不附全文")
        let tasks = try XCTUnwrap((c["tasks"] as? [String: Any])?["tasks"] as? [[String: Any]])
        XCTAssertEqual(tasks.map { $0["title"] as? String }, ["補語音"])

        (c, _, err) = try await call(mcp, "ops_agent_context", ["agent": "triager", "include_bodies": true])
        docs = try XCTUnwrap((c["docs"] as? [String: Any])?["docs"] as? [[String: Any]])
        XCTAssertEqual(docs.map { $0["title"] as? String }, ["待辦"])
        XCTAssertEqual(docs[0]["access"] as? String, "write")
        XCTAssertEqual(docs[0]["body"] as? String, "x")
        XCTAssertEqual((c["cabinet"] as? [Any])?.count, 1)
    }
}
