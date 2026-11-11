import XCTest
@testable import Ops

@MainActor
final class OrgTests: XCTestCase {
    var dataDir: URL!
    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-orgs-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
    }
    override func tearDown() async throws {
        unsetenv("OPS_DATA_DIR")
        TokenVault.scope = ""
        try? FileManager.default.removeItem(at: dataDir)
    }

    func testLegacyFilesMoveIntoDefaultOrg() throws {
        let issue = #"[{"id":"ISS-7","title":"舊的","createdBy":"tim","status":"open","priority":"normal","labels":[],"source":"","link":"","body":"","comments":[],"createdAt":"2026-11-10T00:00:00Z","updatedAt":"2026-11-10T00:00:00Z"}]"#
        try Data(issue.utf8).write(to: dataDir.appendingPathComponent("issues.json"))
        let app = AppState()
        app.startIssues()
        XCTAssertEqual(app.issues.map(\.id), ["ISS-7"])
        XCTAssertEqual(app.orgs.map(\.name), ["evoleap"])
        XCTAssertTrue(FileManager.default.fileExists(atPath: dataDir.appendingPathComponent("orgs/default/issues.json").path))
        XCTAssertTrue(FileManager.default.fileExists(atPath: dataDir.appendingPathComponent("issues.json").path), "原檔保留")
    }

    /// 刪除組織時,該組織在鑰匙圈的連線登入要一起清掉(要在資料夾移走前讀連線清單)
    func testDeleteOrgClearsItsTokens() throws {
        let app = AppState()
        app.startConnections()
        let other = app.createOrg(name: "Side", template: .copyCurrent)
        app.switchOrg(other.id)
        TokenVault.setTokens(TokenSet(accessToken: "side-token", tokenType: "Bearer"), "evoleap")
        app.switchOrg(OrgFiles.defaultId)
        TokenVault.scope = "\(other.id)/"
        XCTAssertEqual(TokenVault.tokens("evoleap")?.accessToken, "side-token")
        TokenVault.scope = ""
        try app.deleteOrg(other.id)
        TokenVault.scope = "\(other.id)/"
        defer { TokenVault.scope = "" }
        XCTAssertNil(TokenVault.tokens("evoleap"), "刪掉的組織的登入還留在鑰匙圈")
    }

    func testCreateSwitchIsolateAndDelete() async throws {
        let app = AppState()
        app.startIssues(); app.startAgents(timer: false); app.startCabinet(); app.startConnections()
        app.startIssues().create(title: "evoleap 的 Issue", by: "tim")
        Cabinet.create(app, title: "evoleap 文件", body: "", section: "levels", tab: "/studio/tools", tags: [], by: "tim")
        TokenVault.setTokens(TokenSet(accessToken: "default-token", tokenType: "Bearer"), "evoleap")
        defer { TokenVault.scope = ""; TokenVault.removeAll("evoleap") }
        XCTAssertEqual(app.agents.count, 5)

        // 空白組織
        let blank = app.createOrg(name: "Lab", template: .blank)
        XCTAssertEqual(blank.id, "lab")
        app.switchOrg(blank.id)
        XCTAssertEqual(app.currentOrg?.name, "Lab")
        XCTAssertTrue(app.issues.isEmpty)
        XCTAssertTrue(app.agents.isEmpty, "空白組織沒有角色")
        XCTAssertTrue(app.connections.isEmpty)
        XCTAssertTrue(app.cabinetDocs.isEmpty)
        XCTAssertEqual(CabinetLayout.sections.map(\.id), ["overview", "docs"])
        XCTAssertNil(TokenVault.tokens("evoleap"), "登入依組織分開")
        app.startIssues().create(title: "Lab 的 Issue", by: "tim")
        XCTAssertEqual(app.issues.first?.id, "ISS-1")

        // 改結構:新增區塊與頁籤
        var layout = CabinetLayout.sections
        layout.append(CabinetSection(id: "s3", label: "研究", tabs: [CabinetTab(path: "/s3/1", label: "競品")]))
        XCTAssertNil(Cabinet.validate(app, layout))
        Cabinet.saveLayout(app, layout)
        Cabinet.create(app, title: "競品比較", body: "", section: "s3", tab: "/s3/1", tags: [], by: "tim")
        XCTAssertNotNil(Cabinet.validate(app, CabinetLayout.blank), "有文件的區塊不能拿掉")

        // 切回 evoleap:資料都還在、各自獨立
        app.switchOrg(OrgFiles.defaultId)
        XCTAssertEqual(app.issues.map(\.title), ["evoleap 的 Issue"])
        XCTAssertEqual(app.cabinetDocs.map(\.title), ["evoleap 文件"])
        XCTAssertEqual(CabinetLayout.sections.count, 8)
        XCTAssertEqual(TokenVault.tokens("evoleap")?.accessToken, "default-token")

        // 複製設定的組織:有角色與結構,沒有資料
        let copy = app.createOrg(name: "evoleap 測試", template: .copyCurrent)
        app.switchOrg(copy.id)
        XCTAssertEqual(app.agents.count, 5)
        XCTAssertTrue(app.agentTasks.isEmpty)
        XCTAssertTrue(app.issues.isEmpty)
        XCTAssertEqual(CabinetLayout.sections.count, 8)
        XCTAssertEqual(app.connections.map(\.id), ["evoleap", "evoleap-local"])
        XCTAssertNil(TokenVault.tokens("evoleap"))

        // MCP 看得到目前組織
        let mcp = MCPHandler(app: app)
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": "ops_orgs", "arguments": [:]]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        XCTAssertTrue(String(decoding: try XCTUnwrap(raw), as: UTF8.self).contains(copy.id))

        // 不能刪目前的組織;刪別的會移走資料夾
        XCTAssertThrowsError(try app.deleteOrg(copy.id))
        try app.deleteOrg(blank.id)
        XCTAssertFalse(app.orgs.contains { $0.id == blank.id })
        XCTAssertFalse(FileManager.default.fileExists(atPath: dataDir.appendingPathComponent("orgs/lab").path))

        // 重開 app:記得目前的組織
        let app2 = AppState()
        app2.startIssues()
        XCTAssertEqual(app2.currentOrgId, copy.id)
        XCTAssertEqual(app2.orgs.count, 2)
    }
}
