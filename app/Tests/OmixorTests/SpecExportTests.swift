import XCTest
@testable import Ops

/// 匯出範例資料給 Go 版(omixor-web)當種子:OPS_EXPORT_SEED=<路徑> swift test --filter SpecExportTests
/// 沒設環境變數時直接略過。時間都是相對於 exportedAt,Go 版載入時會平移到現在。
@MainActor
final class SpecExportTests: XCTestCase {
    func testExportSeed() throws {
        guard let path = ProcessInfo.processInfo.environment["OPS_EXPORT_SEED"] else { throw XCTSkip("沒設 OPS_EXPORT_SEED") }
        setenv("OPS_DATA_DIR", FileManager.default.temporaryDirectory.appendingPathComponent("ops-export-\(UUID().uuidString)").path, 1)
        defer { unsetenv("OPS_DATA_DIR") }
        let now = Date()
        let app = AppState()
        let c: [String: Any] = [
            "people": MCPJSON.object(app.people), "services": MCPJSON.object(app.services),
            "servers": MCPJSON.object(app.servers), "repos": MCPJSON.object(app.repos),
            "pull_requests": MCPJSON.object(app.pullRequests), "work_items": MCPJSON.object(app.workItems),
            "sprint": MCPJSON.object(app.sprint), "pipeline_runs": MCPJSON.object(app.pipelineRuns),
            "deployments": MCPJSON.object(app.deployments), "alerts": MCPJSON.object(app.alerts),
            "alert_rules": MCPJSON.object(app.alertRules), "logs": MCPJSON.object(app.logs),
            "dashboards": MCPJSON.object(app.dashboards), "incidents": MCPJSON.object(app.incidents),
            "on_call": MCPJSON.object(app.onCall), "automations": MCPJSON.object(app.automations),
            "automation_runs": MCPJSON.object(app.automationRuns), "approvals": MCPJSON.object(app.approvals),
            "activity": MCPJSON.object(app.activity), "integrations": MCPJSON.object(app.integrations),
        ]
        let out: [String: Any] = ["exportedAt": MCPJSON.iso(now), "collections": c]
        let data = try JSONSerialization.data(withJSONObject: out, options: [.sortedKeys, .prettyPrinted, .withoutEscapingSlashes])
        try data.write(to: URL(fileURLWithPath: path))
    }

    /// App 自己的工具 ↔ web 自己的工具(web/spec/tools.json):名稱要一樣。
    /// 定義(說明文字、enum)不逐字比:兩邊有刻意的平台差異(例如 navigate 的頁面、server 的說明)。
    /// 核心工具(Issue、角色與任務、文件櫃、組織鏈、頻道、事件、繪圖)兩邊都不自己定義:App 轉給 core server、
    /// web 直接用 omixor-core/mcp.CoreToolDefs(一致性在 web/internal/mcp/spec_test.go 測),所以不在這裡比。
    /// App 新增或移除自己的工具時,web/spec/tools.json 要跟著改(App 開著時在 web 跑 `python3 spec/dump_spec.py` 匯出,會濾掉核心工具;
    /// 匯出後保留 web 的平台差異)。
    func testAppToolsMatchWebSpec() throws {
        let file = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("../../../web/spec/tools.json").standardized
        guard let data = try? Data(contentsOf: file) else { throw XCTSkip("沒有 web/spec/tools.json(單獨拿到 app 時略過)") }
        let web = try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [[String: Any]])
        let webNames = web.compactMap { $0["name"] as? String }
        XCTAssertEqual(Set(webNames).count, web.count, "web/spec/tools.json 工具名稱重複")
        XCTAssertTrue(Set(webNames).isDisjoint(with: MCPHandler.coreTools), "web/spec/tools.json 不該有核心工具")
        // App 自己留著實作、但名稱在 core 登記表(core/model/tools.go)裡的工具:web 那邊用核心的定義
        let appCoreNamed: Set<String> = ["orgs", "agent_task_delegate"]
        XCTAssertTrue(Set(webNames).isDisjoint(with: appCoreNamed))
        let appOwn = Set(MCPHandler.tools.map(\.name)).subtracting(appCoreNamed)
        XCTAssertEqual(appOwn.subtracting(webNames).sorted(), [], "App 有、web/spec/tools.json 沒有的工具")
        XCTAssertEqual(Set(webNames).subtracting(appOwn).sorted(), [], "web/spec/tools.json 有、App 沒有的工具")
    }
}
