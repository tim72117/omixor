import XCTest
@testable import Ops

@MainActor
final class IssueTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-issues-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func call(_ mcp: MCPHandler, _ name: String, _ args: [String: Any]) async throws -> (Any?, String, Bool) {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        return (try? JSONSerialization.jsonObject(with: Data(text.utf8)), text, r["isError"] as? Bool ?? false)
    }

    func testCreateUpdateQueryAndPersist() async throws {
        let app = AppState()
        let mcp = MCPHandler(app: app)

        let tools = await mcp.handle(try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": 1, "method": "tools/list"]))
        let names = String(decoding: try XCTUnwrap(tools), as: UTF8.self)
        for n in ["ops_issue_create", "ops_issue_update", "ops_issues"] { XCTAssertTrue(names.contains(n), n) }

        var (obj, text, isError) = try await call(mcp, "ops_issue_create", [
            "title": "首頁訪客 7 天播放率只有 55%", "body": "## 現象\n- 打開 29 次,播放 16 次",
            "priority": "high", "labels": ["evoleap", "訪客", "evoleap"], "source": "網站訪客 7d",
        ])
        XCTAssertFalse(isError, text)
        var d = try XCTUnwrap(obj as? [String: Any])
        XCTAssertEqual(d["id"] as? String, "ISS-1")
        XCTAssertEqual(d["labels"] as? [String], ["evoleap", "訪客"], "重複標籤只留一個")
        XCTAssertEqual(d["createdBy"] as? String, "MCP 用戶端")
        XCTAssertEqual(app.activity.first?.text, "建立 ISS-1「首頁訪客 7 天播放率只有 55%」")

        (_, text, isError) = try await call(mcp, "ops_issue_create", ["title": "第二筆", "priority": "urgent"])
        XCTAssertFalse(isError, text)
        (_, _, isError) = try await call(mcp, "ops_issue_create", ["body": "沒有標題"])
        XCTAssertTrue(isError)
        (_, _, isError) = try await call(mcp, "ops_issue_create", ["title": "x", "priority": "p0"])
        XCTAssertTrue(isError)

        (obj, text, isError) = try await call(mcp, "ops_issue_update", [
            "id": "iss-1", "comment": "又看了 1 天的數字,45%", "add_labels": ["播放率"], "append_body": "## 建議\n縮短開場",
        ])
        XCTAssertFalse(isError, text)
        d = try XCTUnwrap(obj as? [String: Any])
        XCTAssertEqual(d["labels"] as? [String], ["evoleap", "訪客", "播放率"])
        XCTAssertEqual((d["comments"] as? [[String: Any]])?.first?["author"] as? String, "MCP 用戶端")
        XCTAssertTrue((d["body"] as? String ?? "").hasSuffix("## 建議\n縮短開場"))

        // 列表:預設只列未解決,緊急的排前面
        (obj, _, _) = try await call(mcp, "ops_issues", [:])
        XCTAssertEqual(((obj as? [String: Any])?["issues"] as? [[String: Any]])?.map { $0["id"] as? String }, ["ISS-2", "ISS-1"])
        (obj, _, _) = try await call(mcp, "ops_issues", ["query": "45%"])
        XCTAssertEqual((obj as? [String: Any])?["total"] as? Int, 1, "搜尋包含留言")

        (_, text, isError) = try await call(mcp, "ops_issue_update", ["id": "ISS-2", "status": "closed"])
        XCTAssertFalse(isError, text)
        (obj, _, _) = try await call(mcp, "ops_issues", ["label": "訪客"])
        XCTAssertEqual((obj as? [String: Any])?["total"] as? Int, 1)
        (obj, _, _) = try await call(mcp, "ops_issues", ["status": "closed"])
        XCTAssertEqual(((obj as? [String: Any])?["issues"] as? [[String: Any]])?.first?["id"] as? String, "ISS-2")
        XCTAssertNotNil(app.issues.first { $0.id == "ISS-2" }?.closedAt)

        (_, _, isError) = try await call(mcp, "ops_issue_update", ["id": "ISS-99", "comment": "x"])
        XCTAssertTrue(isError)

        // 以虛擬角色身分建立與留言:署名角色
        app.startAgents(timer: false)
        (obj, text, isError) = try await call(mcp, "ops_issue_create", ["title": "UTM", "agent": "行銷"])
        XCTAssertFalse(isError, text)
        XCTAssertEqual((obj as? [String: Any])?["createdBy"] as? String, "行銷")
        XCTAssertEqual(app.activity.first?.actor, .ai("marketing"))
        (obj, text, isError) = try await call(mcp, "ops_issue_update", ["id": "ISS-1", "comment": "補數字", "agent": "visitor-analyst"])
        XCTAssertFalse(isError, text)
        XCTAssertEqual(((obj as? [String: Any])?["comments"] as? [[String: Any]])?.last?["author"] as? String, "訪客分析師")
        (_, _, isError) = try await call(mcp, "ops_issue_create", ["title": "x", "agent": "不存在"])
        XCTAssertTrue(isError)

        // 重開 app:從 issues.json 讀回來
        let app2 = AppState()
        app2.startIssues()
        XCTAssertEqual(app2.issues.count, 3)
        let i1 = try XCTUnwrap(app2.issues.first { $0.id == "ISS-1" })
        XCTAssertEqual(i1.priority, .high)
        XCTAssertEqual(i1.comments.map(\.author), ["mcp", "agent:visitor-analyst"])
        XCTAssertEqual(i1.source, "網站訪客 7d")
        withExtendedLifetime(app) {}
    }
}
