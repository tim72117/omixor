import XCTest
@testable import Ops

@MainActor
final class AgentTests: XCTestCase {
    var dataDir: URL!
    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-agents-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }
    override func tearDown() async throws {
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func call(_ mcp: MCPHandler, _ name: String, _ args: [String: Any]) async throws -> ([String: Any], Bool) {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        return ((try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] ?? ["text": text], r["isError"] as? Bool ?? false)
    }

    /// 沒有內建預設角色,也不從資料夾根目錄的舊 agents.json 匯入
    func testNoDefaultOrImportedAgents() throws {
        let root = #"{"agents":[{"id":"old","name":"舊版角色","routines":[]}],"tasks":[]}"#
        try Data(root.utf8).write(to: OrgFiles.root.appendingPathComponent("agents.json"))
        let app = AppState()
        app.startAgents(timer: false)
        XCTAssertTrue(app.agents.isEmpty, "\(app.agents.map(\.name))")
        XCTAssertFalse(FileManager.default.fileExists(atPath: app.dataDir.appendingPathComponent("agents.json").path), "沒有角色就不寫檔")
    }

    func testAssignClaimReportFlow() async throws {
        let app = AppState()
        let store = app.startTestAgents()
        XCTAssertEqual(app.agents.map(\.id), ["visitor-analyst", "narrator", "triager", "course-dev", "marketing"])
        let mcp = MCPHandler(app: app)

        var (d, err) = try await call(mcp, "ops_agent_task_create", ["assign_to": "訪客分析師", "title": "看 7d 數字", "priority": "high"])
        XCTAssertFalse(err)
        XCTAssertEqual(d["id"] as? String, "T-1")
        store.assign(to: "visitor-analyst", title: "低優先", priority: .low, by: "tim")
        // 舊的寫法(agent 當成接任務的角色)要被擋下,不能誤派
        (_, err) = try await call(mcp, "ops_agent_task_create", ["agent": "product-manager", "title": "x"])
        XCTAssertTrue(err)
        (_, err) = try await call(mcp, "ops_agent_task_create", ["agent": "course-dev", "assign_to": "course-dev", "title": "x"])
        XCTAssertTrue(err, "不能派給自己")
        (d, err) = try await call(mcp, "ops_agent_task_create", ["agent": "course-dev", "assign_to": "marketing", "title": "寫貼文"])
        XCTAssertFalse(err)
        XCTAssertEqual(d["agentName"] as? String, "行銷")
        XCTAssertEqual(d["createdBy"] as? String, "課程開發")
        store.update(d["id"] as! String) { $0.status = .canceled }

        (d, err) = try await call(mcp, "ops_agent_task_claim", ["agent": "visitor-analyst"])
        XCTAssertFalse(err)
        XCTAssertEqual((d["task"] as? [String: Any])?["id"] as? String, "T-1", "先領優先度高的")
        XCTAssertNotNil((d["agent"] as? [String: Any])?["instructions"])
        XCTAssertEqual(store.task("T-1")?.status, .inProgress)

        (d, err) = try await call(mcp, "ops_agent_task_claim", ["agent": "narrator", "id": "T-2"])
        XCTAssertTrue(err, "不能領別的角色的任務")

        (d, err) = try await call(mcp, "ops_agent_task_report", ["id": "T-1", "note": "讀了 7d", "status": "done", "result": "播放率 55%", "links": ["ISS-1"]])
        XCTAssertFalse(err)
        XCTAssertEqual(d["status"] as? String, "done")
        XCTAssertEqual((d["notes"] as? [[String: Any]])?.first?["author"] as? String, "訪客分析師")
        XCTAssertEqual(app.activity.first?.text, "訪客分析師完成 T-1「看 7d 數字」")

        _ = try await call(mcp, "ops_agent_task_claim", ["agent": "visitor-analyst"])
        (d, _) = try await call(mcp, "ops_agent_task_claim", ["agent": "visitor-analyst"])
        XCTAssertTrue(d["task"] is NSNull)

        // 例行任務:到時間排一筆,沒做完不重複排
        var a = try XCTUnwrap(store.agent("triager"))
        a.routines[0].createdAt = Date().addingTimeInterval(-8 * 86400)
        store.upsert(a)
        store.runDueRoutines()
        store.runDueRoutines(now: Date().addingTimeInterval(8 * 86400))
        XCTAssertEqual(store.tasks(for: "triager").count, 1)

        // 透過 MCP 新增角色、加例行任務
        (d, err) = try await call(mcp, "ops_agent_upsert", ["name": "客服", "role": "回覆學員問題", "color": "green",
            "routines": [["title": "每日信件", "kind": "daily", "hour": 8]]])
        XCTAssertFalse(err, "\(d)")
        XCTAssertEqual(d["name"] as? String, "客服")
        (d, err) = try await call(mcp, "ops_agent_upsert", ["id": "客服", "add_routines": [["title": "週報", "kind": "weekly", "weekday": 6]]])
        XCTAssertFalse(err, "\(d)")
        XCTAssertEqual((d["routines"] as? [[String: Any]])?.map { $0["schedule"] as? String }, ["每天 08:00", "每週五 09:00"])
        (d, err) = try await call(mcp, "ops_agent_upsert", ["id": "product-manager", "name": "產品經理"])
        XCTAssertFalse(err, "\(d)")
        XCTAssertEqual(d["id"] as? String, "product-manager")
        (_, err) = try await call(mcp, "ops_agent_upsert", ["id": "產品 經理", "name": "x"])
        XCTAssertTrue(err)
        (_, err) = try await call(mcp, "ops_agent_upsert", ["role": "沒有名稱"])
        XCTAssertTrue(err)

        // 重開讀回
        let app2 = AppState()
        app2.startAgents(timer: false)
        XCTAssertEqual(app2.agentTasks.count, 8, "快轉 8 天,其他角色的例行任務也到期")
        XCTAssertEqual(app2.agentTasks.first { $0.id == "T-1" }?.result, "播放率 55%")
        withExtendedLifetime(app) {}
    }
}
