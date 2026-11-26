import XCTest
@testable import Omixor

@MainActor
final class CabinetTests: XCTestCase {
    var dataDir: URL!
    override func setUp() async throws {
        try TestCore.start(self)   // 資料一律透過 core server:每個測試一個新的本機守護服務
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-cabinet-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
    }
    override func tearDown() async throws {
        unsetenv("OMIXOR_APP_DATA_DIR")
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
        let role = RoleMCP(app: app)

        // App 的 MCP(人類端點)不能用 agent 參數指定角色身分(core ISS-36):以角色身分要領任務拿憑證、連角色端點
        var (_, rejected, isErr) = try await call(mcp, "doc_save", ["agent": "course-dev", "tab": "L0", "title": "x"])
        XCTAssertTrue(isErr)
        XCTAssertTrue(rejected.contains("An agent identity cannot be specified"), rejected)
        (_, rejected, isErr) = try await call(mcp, "docs", ["agent": "marketing"])
        XCTAssertTrue(isErr, rejected)

        // 課程開發:課程區塊可讀寫
        var (d, text, err) = try await role.call("course-dev", "doc_save", ["tab": "L0", "title": "L0 改版筆記", "body": "# 筆記"])
        XCTAssertFalse(err, text)
        XCTAssertEqual(d["location"] as? String, "課程 › L0 AI 工具介紹")
        XCTAssertEqual(d["author"] as? String, "課程開發")

        // 行銷:課程只能讀,不能寫;行銷研究那一頁可寫
        (_, text, err) = try await role.call("marketing", "doc_save", ["section": "levels", "title": "x"])
        XCTAssertTrue(err)
        XCTAssertTrue(text.contains("has no permission to write to"), text)
        (_, text, err) = try await role.call("marketing", "doc_save", ["tab": "/docs/research/marketing", "title": "社群貼文草稿"])
        XCTAssertFalse(err, text)
        (d, _, _) = try await role.call("marketing", "docs", [:])
        XCTAssertEqual(d["total"] as? Int, 2, "課程可讀、行銷研究可寫")

        // 訪客分析師看不到課程文件
        (d, _, _) = try await role.call("visitor-analyst", "docs", [:])
        XCTAssertEqual(d["total"] as? Int, 0)
        (_, text, err) = try await role.call("visitor-analyst", "docs", ["id": "DOC-1"])
        XCTAssertTrue(err, text)

        // 結構:只列有權限的位置
        (d, _, _) = try await role.call("visitor-analyst", "cabinet", [:])
        XCTAssertEqual((d["sections"] as? [[String: Any]])?.compactMap { $0["section"] as? String }, ["overview", "members"])

        // 用 MCP 調整範圍:給訪客分析師讀課程
        (_, text, err) = try await call(mcp, "agent_upsert", ["id": "visitor-analyst", "doc_access": ["課程": "read", "/docs/course-goals": "write"]])
        XCTAssertFalse(err, text)
        let va = try XCTUnwrap(app.agents.first { $0.id == "visitor-analyst" })
        XCTAssertTrue(va.canRead(section: "levels", tab: "/studio/tools"))
        XCTAssertTrue(va.canWrite(section: "docs", tab: "/docs/course-goals"))
        XCTAssertFalse(va.canRead(section: "docs", tab: "/docs/live"))
        (_, _, err) = try await call(mcp, "agent_upsert", ["id": "visitor-analyst", "doc_access": ["不存在": "read"]])
        XCTAssertTrue(err)

        // 沒帶 agent:MCP 用戶端,全部可讀寫
        (d, _, _) = try await call(mcp, "docs", ["query": "筆記"])
        XCTAssertEqual(d["total"] as? Int, 1)
        (d, text, err) = try await call(mcp, "doc_save", ["id": "DOC-1", "append_body": "補充"])
        XCTAssertFalse(err, text)
        XCTAssertEqual(Cabinet.doc(app, "DOC-1")?.body, "# 筆記\n\n補充")
    }

    func testDocsPersistAcrossRestart() throws {
        let app = AppState()
        app.startTestAgents()   // 伺服器管理員(本機使用者)只能代「存在的」角色署名,不存在的名字會被記成自己(core ISS-36)
        app.startCabinet()
        let d = try! Cabinet.create(app, title: "規劃", body: "內文", section: "overview", tab: nil, tags: ["a"], by: "agent:course-dev")
        try! Cabinet.update(app, d.id) { $0.body += "\n補充" }
        try! Cabinet.create(app, title: "第二份", body: "", section: "levels", tab: "/studio/tools", tags: [], by: "tim")

        let app2 = AppState()
        app2.startCabinet()
        XCTAssertEqual(app2.cabinetDocs.map(\.id), ["DOC-2", "DOC-1"])
        let r = try XCTUnwrap(Cabinet.doc(app2, "DOC-1"))
        XCTAssertEqual(r.body, "內文\n補充")
        XCTAssertEqual(r.author, "agent:course-dev")
        XCTAssertEqual(Cabinet.doc(app2, "DOC-2")?.tab, "/studio/tools")
        try! Cabinet.delete(app2, "DOC-2")
        let app3 = AppState()
        app3.startCabinet()
        XCTAssertEqual(app3.cabinetDocs.map(\.id), ["DOC-1"])
        // 號碼不重用(core):DOC-2 刪掉了,下一份是 DOC-3
        XCTAssertEqual(try! Cabinet.create(app3, title: "x", body: "", section: "todo", tab: nil, tags: [], by: "tim").id, "DOC-3")
    }

    /// 專案(真的 core):新增專案、切過去讀到空白結構、在專案裡新增文件、搬回未分專案;非空的專案不能刪
    func testProjectsAgainstCore() async throws {
        let app = AppState()
        app.startCabinet()
        let baseDocs = app.cabinetDocs.count
        let p = try Cabinet.createProject(app, name: "官網改版", description: "改版")
        XCTAssertTrue(p.id.hasPrefix("PRJ-"), p.id)
        XCTAssertEqual(app.cabinetProjects.map(\.id), [p.id])

        await Cabinet.selectProject(app, p.id)
        XCTAssertNil(app.cabinetRemoteError)
        XCTAssertEqual(CabinetLayout.sections.map(\.id), ["overview", "docs"], "新專案是空白結構")
        XCTAssertEqual(app.cabinetDocs, [])
        let d = try Cabinet.create(app, title: "首頁規格", body: "x", section: "docs", tab: nil, tags: [], by: "tim")
        XCTAssertEqual(d.project, p.id)

        XCTAssertThrowsError(try Cabinet.deleteProject(app, p.id), "專案裡還有文件")
        XCTAssertEqual(app.cabinetProjects.map(\.id), [p.id])

        try Cabinet.move(app, d.id, toProject: "", section: "overview", tab: nil)
        XCTAssertEqual(app.cabinetDocs, [], "搬走了")
        try Cabinet.updateProject(app, p.id, name: "官網")
        XCTAssertEqual(app.cabinetProjects.first?.name, "官網")
        try Cabinet.deleteProject(app, p.id)
        XCTAssertEqual(app.cabinetProject, "")
        XCTAssertEqual(app.cabinetProjects, [])
        XCTAssertEqual(app.cabinetDocs.count, baseDocs + 1)
        XCTAssertNil(Cabinet.doc(app, d.id)?.project, "回到未分專案")
    }

    /// 第一次匯入本機資料:讀不出來的 cabinet.json 不匯入,原檔不動
    func testUnreadableCabinetIsKeptNotOverwritten() throws {
        let f = OrgFiles.dir(OrgFiles.defaultId).appendingPathComponent("cabinet.json")
        try Data("not json".utf8).write(to: f)
        let problems = LocalCoreImport.sync(IssueRemote(config: IssueServerConfig.current), orgs: [Organization(id: OrgFiles.defaultId, name: "evoleap")])
        XCTAssertEqual(problems, [])
        let app = AppState()
        app.startCabinet()
        XCTAssertTrue(app.cabinetDocs.isEmpty)
        XCTAssertEqual(try Data(contentsOf: f), Data("not json".utf8), "原檔不動")
    }

    /// 第一次匯入本機資料:沒有 docAccess 欄位的舊 agents.json 也匯入得了;原檔留著
    func testOldAgentsFileStillLoads() throws {
        let old = #"{"agents":[{"id":"x","name":"舊角色","routines":[]}],"tasks":[]}"#
        let f = OrgFiles.dir(OrgFiles.defaultId).appendingPathComponent("agents.json")
        try Data(old.utf8).write(to: f)
        let problems = LocalCoreImport.sync(IssueRemote(config: IssueServerConfig.current), orgs: [Organization(id: OrgFiles.defaultId, name: "evoleap")])
        XCTAssertEqual(problems, [])
        let app = AppState()
        app.startAgents(timer: false)
        XCTAssertEqual(app.agents.map(\.name), ["舊角色"])
        XCTAssertEqual(app.agents.first?.docAccess, [:])
        XCTAssertTrue(FileManager.default.fileExists(atPath: f.path))
        // 第二次不再匯入(改了檔案也不會覆蓋 server 上的)
        try Data(#"{"agents":[{"id":"y","name":"別的","routines":[]}],"tasks":[]}"#.utf8).write(to: f)
        _ = LocalCoreImport.sync(IssueRemote(config: IssueServerConfig.current), orgs: [Organization(id: OrgFiles.defaultId, name: "evoleap")])
        app.startAgents(timer: false).refresh()
        XCTAssertEqual(app.agents.map(\.name), ["舊角色"])
    }

    /// agent_context:只回傳角色能讀的文件、標出讀寫權限,任務只列這個角色未完成的
    func testAgentContext() async throws {
        let app = AppState()
        app.startTestAgents()
        let mcp = MCPHandler(app: app)
        _ = try await call(mcp, "doc_save", ["title": "L0", "tab": "L0", "body": "你好"])
        _ = try await call(mcp, "doc_save", ["title": "待辦", "section": "todo", "body": "x"])
        _ = try await call(mcp, "agent_task_create", ["assign_to": "narrator", "title": "補語音"])
        _ = try await call(mcp, "agent_task_create", ["assign_to": "triager", "title": "整理"])
        var (_, text, err) = try await call(mcp, "agent_context", ["agent": "nobody"])
        XCTAssertTrue(err)
        XCTAssertEqual(MCPErrorText.message(text), "找不到角色 nobody")

        var c: [String: Any]
        (c, _, err) = try await call(mcp, "agent_context", ["agent": "旁白製作"])
        XCTAssertFalse(err)
        XCTAssertEqual((c["agent"] as? [String: Any])?["id"] as? String, "narrator")
        var docs = try XCTUnwrap((c["docs"] as? [String: Any])?["docs"] as? [[String: Any]])
        XCTAssertEqual(docs.map { $0["title"] as? String }, ["L0"])
        XCTAssertEqual(docs[0]["access"] as? String, "read")
        XCTAssertNil(docs[0]["body"], "預設不附全文")
        let tasks = try XCTUnwrap((c["tasks"] as? [String: Any])?["tasks"] as? [[String: Any]])
        XCTAssertEqual(tasks.map { $0["title"] as? String }, ["補語音"])

        (c, _, err) = try await call(mcp, "agent_context", ["agent": "triager", "include_bodies": true])
        docs = try XCTUnwrap((c["docs"] as? [String: Any])?["docs"] as? [[String: Any]])
        XCTAssertEqual(docs.map { $0["title"] as? String }, ["待辦"])
        XCTAssertEqual(docs[0]["access"] as? String, "write")
        XCTAssertEqual(docs[0]["body"] as? String, "x")
        XCTAssertEqual((c["cabinet"] as? [Any])?.count, 1)
    }
}
