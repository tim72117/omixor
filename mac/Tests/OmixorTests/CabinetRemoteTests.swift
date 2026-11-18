import XCTest
@testable import Omixor

/// 遠端模式:文件櫃讀寫 omixor server(假伺服器見 AgentRemoteTests.swift 的 FakeOmixorServer),不寫本機 cabinet.json
@MainActor
final class CabinetRemoteTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-cabinet-remote-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
        IssueRemote.protocolClasses = [FakeOmixorServer.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5998")!, token: "tok-1")
        FakeOmixorServer.reset()
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OMIXOR_APP_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
        CabinetLayout.sections = CabinetLayout.evoleapStudio
    }

    private func remoteApp() -> AppState {
        let app = AppState()
        app.currentOrgId = "~local~test"
        app.startCabinet()
        return app
    }

    func testReadWriteThroughServer() throws {
        let app = remoteApp()
        XCTAssertNil(app.cabinetRemoteError)
        XCTAssertEqual(CabinetLayout.sections.map(\.id), ["overview", "docs"], "結構來自伺服器")
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-1"])
        XCTAssertTrue(FakeOmixorServer.requests.contains { $0.path == "/api/orgs/test/cabinet" }, "組織 id 去掉 ~<伺服器>~")

        let d = try Cabinet.create(app, title: "新文件", body: "x", section: "docs", tab: "/docs/research", tags: ["b"], by: "tim")
        XCTAssertEqual(d.id, "DOC-2")
        XCTAssertEqual(app.cabinetDocs.first?.id, "DOC-2")
        XCTAssertEqual(FakeOmixorServer.docs.first?["tab"] as? String ?? "", "/docs/research")

        let u = try Cabinet.update(app, "doc-2") { $0.body += "\n補充"; $0.section = "overview"; $0.tab = nil }
        XCTAssertEqual(u?.body, "x\n補充")
        let sent = try XCTUnwrap(FakeOmixorServer.requests.last?.body)
        XCTAssertEqual(Set(sent.keys), ["body", "section", "tab"], "只送改了的欄位")
        XCTAssertEqual(sent["tab"] as? String, "", "搬到區塊本身送空字串")

        XCTAssertThrowsError(try Cabinet.create(app, title: "規格", body: "---\nkind: spec\n---", section: "docs", tab: nil, tags: [], by: "tim")) {
            XCTAssertTrue($0.localizedDescription.contains("owner"), "伺服器的錯誤原文要傳回來:\($0.localizedDescription)")
        }

        try Cabinet.delete(app, "DOC-1")
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-2"])
        XCTAssertEqual(FakeOmixorServer.docs.count, 1)

        try Cabinet.saveLayout(app, [CabinetSection(id: "overview", label: "總覽")])
        XCTAssertEqual(CabinetLayout.sections.map(\.id), ["overview"])
        XCTAssertFalse(FileManager.default.fileExists(atPath: app.dataDir.appendingPathComponent("cabinet.json").path), "遠端不寫本機檔案")
        XCTAssertFalse(FileManager.default.fileExists(atPath: app.dataDir.appendingPathComponent("cabinet-layout.json").path))
    }

    // MARK: 專案

    /// 切換專案:清掉畫面、帶 ?project= 讀那個專案的結構與文件;切回未分專案不帶 project
    func testSwitchProjectLoadsItsCabinet() async throws {
        FakeOmixorServer.projects = [["id": "PRJ-1", "name": "App", "description": "", "createdAt": FakeOmixorServer.now, "updatedAt": FakeOmixorServer.now],
                                     ["id": "PRJ-2", "name": "官網改版", "description": "改版", "createdAt": FakeOmixorServer.now, "updatedAt": FakeOmixorServer.now]]
        FakeOmixorServer.projectLayouts["PRJ-2"] = [["id": "spec", "label": "規格", "path": "", "tabs": [["code": "P1", "label": "首頁", "path": "/spec/home"]]]]
        FakeOmixorServer.docs.append(["id": "DOC-9", "title": "首頁規格", "body": "", "section": "spec", "tab": "/spec/home", "tags": [],
                                      "author": "agent:pm", "project": "PRJ-2", "createdAt": FakeOmixorServer.now, "updatedAt": FakeOmixorServer.now])
        let app = remoteApp()
        XCTAssertEqual(app.cabinetProject, "")
        XCTAssertEqual(app.cabinetProjects.map(\.id), ["PRJ-1", "PRJ-2"], "讀文件櫃時一併讀專案清單")
        XCTAssertEqual(app.cabinetProjects[1].name, "官網改版")
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-1"], "未分專案只看到沒有專案的文件")
        XCTAssertNil(FakeOmixorServer.requests.last { $0.path.hasSuffix("/cabinet") }?.query["project"], "未分專案不帶 project")

        await Cabinet.selectProject(app, "PRJ-2")
        XCTAssertEqual(app.cabinetProject, "PRJ-2")
        XCTAssertEqual(FakeOmixorServer.requests.last { $0.path.hasSuffix("/cabinet") }?.query["project"], "PRJ-2")
        XCTAssertEqual(CabinetLayout.sections.map(\.id), ["spec"], "換成專案的結構")
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-9"])
        XCTAssertEqual(app.cabinetDocs.first?.project, "PRJ-2")
        XCTAssertNil(app.cabinetRemoteError)

        // 在專案裡新增文件、改結構:都帶 project
        let d = try Cabinet.create(app, title: "關於我們", body: "x", section: "spec", tab: nil, tags: [], by: "tim")
        XCTAssertEqual(FakeOmixorServer.requests.last?.body["project"] as? String, "PRJ-2")
        XCTAssertEqual(d.project, "PRJ-2")
        try Cabinet.saveLayout(app, [CabinetSection(id: "spec", label: "規格"), CabinetSection(id: "qa", label: "測試")])
        XCTAssertEqual(FakeOmixorServer.requests.last?.body["project"] as? String, "PRJ-2")
        XCTAssertEqual(FakeOmixorServer.projectLayouts["PRJ-2"]?.count, 2)
        XCTAssertEqual(FakeOmixorServer.layout.count, 2, "未分專案的結構沒被改")

        // 切回未分專案
        await Cabinet.selectProject(app, "")
        XCTAssertEqual(CabinetLayout.sections.map(\.id), ["overview", "docs"])
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-1"])
        XCTAssertNil(FakeOmixorServer.requests.last { $0.path.hasSuffix("/cabinet") }?.query["project"])

        // 看的專案被別人刪掉:重讀時切回未分專案
        await Cabinet.selectProject(app, "PRJ-1")
        XCTAssertEqual(app.cabinetProject, "PRJ-1")
        FakeOmixorServer.projects.removeAll { $0["id"] as? String == "PRJ-1" }
        await Cabinet.reload(app)
        XCTAssertEqual(app.cabinetProject, "")
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-1"])
        XCTAssertNil(app.cabinetRemoteError)
    }

    /// 搬家:POST docs/<id> 帶 project(空字串 = 搬回未分專案)與新位置;搬走的文件不在目前專案的畫面上
    func testMoveDocBetweenProjects() async throws {
        let app = remoteApp()
        let p = try Cabinet.createProject(app, name: "官網改版", description: "改版")
        XCTAssertEqual(p.id, "PRJ-1")
        XCTAssertEqual(FakeOmixorServer.requests.last?.path, "/api/orgs/test/projects")
        XCTAssertEqual(FakeOmixorServer.requests.last?.body["name"] as? String, "官網改版")
        XCTAssertEqual(app.cabinetProjects.map(\.id), ["PRJ-1"])

        try Cabinet.move(app, "DOC-1", toProject: "PRJ-1", section: "overview", tab: nil)
        var req = try XCTUnwrap(FakeOmixorServer.requests.last)
        XCTAssertEqual(req.method, "POST")
        XCTAssertEqual(req.path, "/api/orgs/test/docs/DOC-1")
        XCTAssertEqual(req.body["project"] as? String, "PRJ-1")
        XCTAssertEqual(req.body["section"] as? String, "overview")
        XCTAssertEqual(req.body["tab"] as? String, "")
        XCTAssertEqual(Set(req.body.keys), ["project", "section", "tab"], "只送搬移要的欄位")
        XCTAssertEqual(app.cabinetDocs.map(\.id), [], "搬到別的專案,未分專案的畫面上沒有了")
        XCTAssertEqual(FakeOmixorServer.docs.first?["project"] as? String, "PRJ-1")

        // 專案裡還有文件:刪除失敗,伺服器的錯誤原文要傳回來
        XCTAssertThrowsError(try Cabinet.deleteProject(app, "PRJ-1")) {
            XCTAssertTrue($0.localizedDescription.contains("還有文件"), $0.localizedDescription)
        }
        XCTAssertEqual(app.cabinetProjects.map(\.id), ["PRJ-1"])

        // 到專案裡把它搬回未分專案(同位置:只送 project = "")
        await Cabinet.selectProject(app, "PRJ-1")
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-1"])
        try Cabinet.move(app, "DOC-1", toProject: "", section: "overview", tab: nil)
        req = try XCTUnwrap(FakeOmixorServer.requests.last)
        XCTAssertEqual(req.body.count, 1)
        XCTAssertEqual(req.body["project"] as? String, "", "空字串 = 搬回未分專案")
        XCTAssertEqual(app.cabinetDocs.map(\.id), [])
        XCTAssertNil(FakeOmixorServer.docs.first?["project"])

        // 改名、刪除(空的專案可以刪;刪的是正在看的就切回未分專案)
        let renamed = try Cabinet.updateProject(app, "PRJ-1", name: "官網")
        XCTAssertEqual(renamed.name, "官網")
        XCTAssertEqual(FakeOmixorServer.requests.last?.method, "PUT")
        XCTAssertEqual(FakeOmixorServer.requests.last?.body["name"] as? String, "官網")
        XCTAssertNil(FakeOmixorServer.requests.last?.body["description"], "沒給的欄位不送")
        XCTAssertEqual(app.cabinetProjects.first?.name, "官網")
        try Cabinet.deleteProject(app, "PRJ-1")
        XCTAssertTrue(FakeOmixorServer.requests.contains { $0.path == "/api/orgs/test/projects/PRJ-1/delete" && $0.method == "POST" })
        XCTAssertEqual(app.cabinetProjects, [])
        XCTAssertEqual(app.cabinetProject, "")
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-1"], "切回未分專案並重讀")
    }

    /// 看專案時,範本也取自未分專案(和 console 的 cabinet_doc_html 一樣);換組織清掉
    func testProjectDocsUseUnassignedTemplates() async throws {
        FakeOmixorServer.projects = [["id": "PRJ-1", "name": "App", "description": "", "createdAt": FakeOmixorServer.now, "updatedAt": FakeOmixorServer.now]]
        FakeOmixorServer.docs.append(["id": "DOC-7", "title": "規格範本", "body": "---\nkind: template\nfor: spec\nrequired: owner\n---\n```html\n<div>x</div>\n```",
                                      "section": "docs", "tags": [], "author": "tim", "createdAt": FakeOmixorServer.now, "updatedAt": FakeOmixorServer.now])
        FakeOmixorServer.docs.append(["id": "DOC-8", "title": "首頁規格", "body": "---\nkind: spec\nowner: tim\n---\n內文", "section": "docs", "tags": [],
                                      "author": "tim", "project": "PRJ-1", "createdAt": FakeOmixorServer.now, "updatedAt": FakeOmixorServer.now])
        let app = remoteApp()
        XCTAssertEqual(app.cabinetBaseTemplates, [], "看未分專案時範本就在 cabinetDocs 裡")
        XCTAssertEqual(Cabinet.templateDocs(app).map(\.id), app.cabinetDocs.map(\.id))

        await Cabinet.selectProject(app, "PRJ-1")
        XCTAssertEqual(app.cabinetDocs.map(\.id), ["DOC-8"])
        XCTAssertEqual(app.cabinetBaseTemplates.map(\.id), ["DOC-7"], "只留未分專案的範本")
        let doc = try XCTUnwrap(app.cabinetDocs.first)
        XCTAssertNotNil(DocTemplates.html(for: doc, in: Cabinet.templateDocs(app)), "專案的文件套得到未分專案的範本")
        XCTAssertNotNil(DocTemplates.validate("---\nkind: spec\n---", in: Cabinet.templateDocs(app)), "存檔檢查也用未分專案的範本")

        // 同步重讀(refresh)也一樣
        app.cabinetBaseTemplates = []
        Cabinet.refresh(app)
        XCTAssertEqual(app.cabinetBaseTemplates.map(\.id), ["DOC-7"])

        // 換組織:清掉
        app.cabinetLoaded = false
        Cabinet.begin(app)
        XCTAssertEqual(app.cabinetBaseTemplates, [])
    }

    /// 換組織:回到未分專案、清掉專案清單
    func testOrgChangeResetsProject() async throws {
        FakeOmixorServer.projects = [["id": "PRJ-1", "name": "App"]]
        let app = remoteApp()
        await Cabinet.selectProject(app, "PRJ-1")
        XCTAssertEqual(app.cabinetProject, "PRJ-1")
        app.cabinetLoaded = false
        Cabinet.begin(app)
        XCTAssertEqual(app.cabinetProject, "")
        XCTAssertEqual(app.cabinetProjects, [])
    }

    /// 解碼:Project 與 CabinetDoc.project 用寬鬆解碼
    func testProjectDecoding() throws {
        let list = try IssueRemote.decoder().decode(LossyList<Project>.self, from: Data(#"""
        [{"id":"PRJ-1","name":"App","description":null,"createdAt":"2026-10-01T00:00:00Z"},
         {"name":"沒有 id"},
         {"id":"PRJ-3"}]
        """#.utf8)).elements
        XCTAssertEqual(list.map(\.id), ["PRJ-1", "PRJ-3"], "缺 id 的那筆略過")
        XCTAssertEqual(list[0].description, "", "null → 空字串")
        XCTAssertEqual(list[0].updatedAt, list[0].createdAt, "沒有 updatedAt 用 createdAt")
        XCTAssertEqual(list[1].name, "PRJ-3", "沒有名稱用 id")

        let d = try IssueRemote.decoder().decode(CabinetDoc.self, from: Data(#"{"id":"DOC-1","section":"docs","project":""}"#.utf8))
        XCTAssertNil(d.project, "空字串 = 未分專案")
        let e = try IssueRemote.decoder().decode(CabinetDoc.self, from: Data(#"{"id":"DOC-2","section":"docs","project":7}"#.utf8))
        XCTAssertNil(e.project, "型別不對當作沒有")
        let c = try IssueRemote.decoder().decode(RemoteCabinet.self, from: Data(#"{"layout":null,"docs":null}"#.utf8))
        XCTAssertNil(c.project)
    }
}
