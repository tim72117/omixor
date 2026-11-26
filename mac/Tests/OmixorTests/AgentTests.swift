import XCTest
@testable import Omixor

@MainActor
final class AgentTests: XCTestCase {
    // 舊 server 不會把任務結果貼到 Issue:只有領取回應明示會貼,提示詞才叫角色不要自己留言
    func testSystemPromptFollowsServerCapability() {
        let auto = "系統會自動把 result 貼到該 Issue 的留言", manual = "需要在相關 Issue 說明時,用 issue_update 留言;"
        let new = AgentRunner.systemPrompt(name: "工程", agent: nil, workdir: nil, autoPostsResult: true)
        XCTAssertTrue(new.contains(auto))
        XCTAssertFalse(new.contains("\n- " + manual))
        let old = AgentRunner.systemPrompt(name: "工程", agent: nil, workdir: nil)
        XCTAssertTrue(old.contains("\n- " + manual))
        XCTAssertFalse(old.contains(auto))
        for p in [new, old] { XCTAssertTrue(p.contains("\n- 不要把任何憑證"), p) }
    }

    var dataDir: URL!
    override func setUp() async throws {
        try TestCore.start(self)   // 資料一律透過 core server:每個測試一個新的本機守護服務
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-agents-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
    }
    override func tearDown() async throws {
        unsetenv("OMIXOR_APP_DATA_DIR")
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

    /// App 的 agent_task_create 攔截(提示改用 assign_to)只在 agent 是非空白字串時(同 core 的 TrimSpace);
    /// 空白字串放行;null、非字串交給 core 判斷(core 的 RejectAgentArg 會以「不能指定角色身分」拒絕)
    func testAgentTaskCreateRejectsAgentLikeCore() async throws {
        let app = AppState()
        app.startTestAgents()
        let mcp = MCPHandler(app: app)
        var (d, err) = try await call(mcp, "agent_task_create", ["agent": " \n\t", "assign_to": "marketing", "title": "x"])
        XCTAssertFalse(err, "空白的 agent 當沒給:\(d)")
        // 同 core 的 RejectAgentArg:null、數字這類非字串的 agent 也擋
        for who: Any in [NSNull(), 3] {
            (d, err) = try await call(mcp, "agent_task_create", ["agent": who, "assign_to": "marketing", "title": "x"])
            XCTAssertTrue(err, "agent=\(who) 要擋:\(d)")
            XCTAssertTrue(MCPErrorText.message(d).contains("agent_task_create cannot take agent"), "agent=\(who) 由 App 攔截:\(d)")
        }
        (d, err) = try await call(mcp, "agent_task_create", ["agent": " course-dev ", "assign_to": "marketing", "title": "x"])
        XCTAssertTrue(err)
        XCTAssertTrue(MCPErrorText.message(d).contains("agent_task_create cannot take agent"), "\(d)")
    }

    func testAssignClaimReportFlow() async throws {
        let app = AppState()
        let store = app.startTestAgents()
        XCTAssertEqual(app.agents.map(\.id), ["visitor-analyst", "narrator", "triager", "course-dev", "marketing"])
        let mcp = MCPHandler(app: app)

        var (d, err) = try await call(mcp, "agent_task_create", ["assign_to": "訪客分析師", "title": "看 7d 數字", "priority": "high"])
        XCTAssertFalse(err)
        XCTAssertEqual(d["id"] as? String, "T-1")
        store.assign(to: "visitor-analyst", title: "低優先", priority: .low, by: "tim")
        // 舊的寫法(agent 當成接任務的角色)要被擋下,不能誤派
        (_, err) = try await call(mcp, "agent_task_create", ["agent": "product-manager", "title": "x"])
        XCTAssertTrue(err)
        // 以角色身分派任務:agent_task_create 不能帶 agent(core ISS-36 人類端點拒絕 agent;角色端點也沒有 agent_task_create),
        // 改走依組織鏈委派(agent_task_delegate,App 走 REST /agent-tasks/delegate)
        (d, err) = try await call(mcp, "agent_task_create", ["agent": "course-dev", "assign_to": "marketing", "title": "寫貼文"])
        XCTAssertTrue(err)
        XCTAssertTrue(MCPErrorText.message(d).contains("agent_task_create cannot take agent"), "\(d)")
        (d, err) = try await call(mcp, "agent_task_delegate", ["agent": "course-dev", "to": "marketing", "title": "寫貼文"])
        XCTAssertTrue(err, "組織鏈上沒有 course-dev → marketing 不能委派:\(d)")
        try store.saveChain([ChainEdge(from: "agent:course-dev", to: "agent:marketing", label: "")])
        (_, err) = try await call(mcp, "agent_task_delegate", ["agent": "course-dev", "to": "course-dev", "title": "x"])
        XCTAssertTrue(err, "不能派給自己")
        (d, err) = try await call(mcp, "agent_task_delegate", ["agent": "course-dev", "to": "marketing", "title": "寫貼文"])
        XCTAssertFalse(err, "\(d)")
        XCTAssertEqual(d["agentName"] as? String, "行銷")
        XCTAssertEqual(d["createdBy"] as? String, "課程開發")
        store.cancel(try XCTUnwrap(d["id"] as? String))

        (d, err) = try await call(mcp, "agent_task_claim", ["agent": "visitor-analyst"])
        XCTAssertFalse(err)
        XCTAssertEqual((d["task"] as? [String: Any])?["id"] as? String, "T-1", "先領優先度高的")
        XCTAssertNotNil((d["agent"] as? [String: Any])?["instructions"])
        XCTAssertEqual(store.task("T-1")?.status, .inProgress)

        (d, err) = try await call(mcp, "agent_task_claim", ["agent": "narrator", "id": "T-2"])
        XCTAssertTrue(err, "不能領別的角色的任務")

        (d, err) = try await call(mcp, "agent_task_report", ["id": "T-1", "note": "讀了 7d", "status": "done", "result": "播放率 55%", "links": ["ISS-1"]])
        XCTAssertFalse(err)
        XCTAssertEqual(d["status"] as? String, "done")
        XCTAssertEqual((d["notes"] as? [[String: Any]])?.first?["author"] as? String, "訪客分析師")
        XCTAssertEqual(store.task("T-1")?.status, .done, "MCP 改了 server 上的資料,App 馬上重讀")

        _ = try await call(mcp, "agent_task_claim", ["agent": "visitor-analyst"])
        (d, _) = try await call(mcp, "agent_task_claim", ["agent": "visitor-analyst"])
        XCTAssertTrue(d["task"] is NSNull)

        // 例行任務由 server 排(core 的 StartRoutines);手動立刻排一筆
        let a = try XCTUnwrap(store.agent("triager"))
        store.runNow(agent: "triager", routine: a.routines[0].id)
        XCTAssertEqual(store.tasks(for: "triager").count, 1)

        // 透過 MCP 新增角色、加例行任務
        (d, err) = try await call(mcp, "agent_upsert", ["name": "客服", "role": "回覆學員問題", "color": "green",
            "routines": [["title": "每日信件", "kind": "daily", "hour": 8]]])
        XCTAssertFalse(err, "\(d)")
        XCTAssertEqual(d["name"] as? String, "客服")
        (d, err) = try await call(mcp, "agent_upsert", ["id": "客服", "add_routines": [["title": "週報", "kind": "weekly", "weekday": 6]]])
        XCTAssertFalse(err, "\(d)")
        XCTAssertEqual((d["routines"] as? [[String: Any]])?.map { $0["schedule"] as? String }, ["每天 08:00", "每週五 09:00"])
        (d, err) = try await call(mcp, "agent_upsert", ["id": "product-manager", "name": "產品經理"])
        XCTAssertFalse(err, "\(d)")
        XCTAssertEqual(d["id"] as? String, "product-manager")
        (_, err) = try await call(mcp, "agent_upsert", ["id": "產品 經理", "name": "x"])
        XCTAssertTrue(err)
        (_, err) = try await call(mcp, "agent_upsert", ["role": "沒有名稱"])
        XCTAssertTrue(err)

        // 重開讀回
        let app2 = AppState()
        app2.startAgents(timer: false)
        XCTAssertEqual(app2.agentTasks.count, 4, "資料在 server 上,重開讀得回來")
        XCTAssertEqual(app2.agentTasks.first { $0.id == "T-1" }?.result, "播放率 55%")
        withExtendedLifetime(app) {}
    }
}
