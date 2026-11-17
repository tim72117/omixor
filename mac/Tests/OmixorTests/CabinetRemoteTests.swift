import XCTest
@testable import Ops

/// 遠端模式:文件櫃讀寫 omixor server(假伺服器見 AgentRemoteTests.swift 的 FakeOmixorServer),不寫本機 cabinet.json
@MainActor
final class CabinetRemoteTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-cabinet-remote-\(UUID().uuidString)")
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
}
