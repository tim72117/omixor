import XCTest
@testable import Ops

/// 匯出範例資料給 Go 版(ops-web)當種子:OPS_EXPORT_SEED=<路徑> swift test --filter SpecExportTests
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
}
