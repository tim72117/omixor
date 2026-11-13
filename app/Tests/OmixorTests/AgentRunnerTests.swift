import XCTest
@testable import Ops

/// 角色自動執行:假伺服器(FakeOmixorServer)+ 假的 Claude(不會真的呼叫 claude)
@MainActor
final class AgentRunnerTests: XCTestCase {
    var dataDir: URL!
    let org = "~omixor"

    override func setUp() async throws {
        try TestCore.start(self)   // 資料一律透過 core server:每個測試一個新的本機守護服務
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-runner-\(UUID().uuidString)")
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
    }

    private func app() -> AppState {
        let a = AppState()
        a.currentOrgId = org
        a.startAgents(timer: false)
        return a
    }

    private func waitIdle(_ r: AgentRunner) async throws {
        for _ in 0..<100 {
            try await Task.sleep(for: .milliseconds(20))
            if r.running == nil, !r.log.isEmpty { return }
        }
    }

    func testConfigValidation() throws {
        let a = app()
        let r = a.agentRunner
        var c = RunnerConfig()
        c.autoRun = true
        c.workdir = "/no/such/dir"
        XCTAssertThrowsError(try r.save("programmer", c), "工作資料夾要存在")
        c.workdir = FileManager.default.homeDirectoryForCurrentUser.path
        XCTAssertThrowsError(try r.save("programmer", c), "不能包住 omixor 的憑證與資料")
        c.workdir = ""
        c.allowBash = true
        c.effort = "huge"
        XCTAssertThrowsError(try r.save("programmer", c))
        c.effort = "low"
        c.timeoutMinutes = 9999
        try r.save("programmer", c)
        XCTAssertEqual(r.config("programmer").timeoutMinutes, 480)
        XCTAssertFalse(r.config("programmer").allowBash, "沒有工作資料夾就不給 Bash")
        XCTAssertTrue(FileManager.default.fileExists(atPath: a.dataDir.appendingPathComponent("agent-runner.json").path))
    }

    /// 開了自動執行:領取 → 帶角色憑證的 MCP 設定檔(600、不在命令列)→ Claude 成功 → 回報 done
    func testRunsAutoTaskAndReportsDone() async throws {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "寫測試", links: ["ISS-2"], by: "tim")
        _ = FakeOmixorServer.newTask(agent: "tester", title: "沒開自動執行的角色", links: [], by: "tim")
        let a = app()
        let r = a.agentRunner
        let work = dataDir.appendingPathComponent("repo")
        try FileManager.default.createDirectory(at: work, withIntermediateDirectories: true)
        var c = RunnerConfig()
        c.autoRun = true
        c.model = "haiku"
        c.workdir = work.path
        try r.save("programmer", c)

        var seen: (String, ClaudeRunOptions, String, Int)?
        r.runClaude = { prompt, o in
            let file = try XCTUnwrap(o.mcpConfigFile)
            let perm = (try FileManager.default.attributesOfItem(atPath: file.path)[.posixPermissions] as? NSNumber)?.intValue ?? 0
            seen = (prompt, o, try String(contentsOf: file, encoding: .utf8), perm)
            return ClaudeRunResult(text: "做完了", isError: false, sessionId: nil, turns: 3, costUSD: 0.01, stderr: "")
        }
        r.tick()
        try await waitIdle(r)

        let (prompt, o, mcp, perm) = try XCTUnwrap(seen)
        XCTAssertTrue(prompt.contains("寫測試") && prompt.contains("ISS-2"))
        XCTAssertEqual(o.model, "haiku")
        XCTAssertEqual(o.workdir?.path, work.path)
        XCTAssertEqual(o.builtinTools, ["Read", "Glob", "Grep", "Edit", "Write"], "沒開 Bash")
        XCTAssertTrue(o.allowedTools.contains("mcp__omixor"))
        XCTAssertEqual(perm, 0o600)
        let srv = (((try JSONSerialization.jsonObject(with: Data(mcp.utf8)) as? [String: Any])?["mcpServers"] as? [String: Any])?["omixor"] as? [String: Any]) ?? [:]
        XCTAssertEqual(srv["url"] as? String, "http://127.0.0.1:5998/mcp/agent")
        XCTAssertEqual((srv["headers"] as? [String: String])?["Authorization"], "Bearer programmer:s3cret")
        XCTAssertFalse(ClaudeCLI.arguments(prompt: prompt, o).joined(separator: " ").contains("s3cret"), "密鑰不在命令列")
        XCTAssertFalse(o.appendSystemPrompt.contains("s3cret") || prompt.contains("s3cret"))
        XCTAssertFalse(FileManager.default.fileExists(atPath: o.mcpConfigFile!.path), "用完刪掉")

        let t = FakeOmixorServer.tasks.first { $0["title"] as? String == "寫測試" }
        XCTAssertEqual(t?["status"] as? String, "done")
        XCTAssertEqual(t?["result"] as? String, "做完了")
        XCTAssertEqual(FakeOmixorServer.tasks.first { $0["agentId"] as? String == "tester" }?["status"] as? String, "queued", "沒開自動執行的不動")
        XCTAssertEqual(r.log.first?.outcome, "done")
    }

    /// Claude 失敗:回報 blocked,原因裡的密鑰換成 ***
    func testFailureReportsBlockedWithoutSecret() async throws {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "會失敗", links: [], by: "tim")
        let a = app()
        let r = a.agentRunner
        var c = RunnerConfig()
        c.autoRun = true
        try r.save("programmer", c)
        r.runClaude = { _, o in
            XCTAssertTrue(o.builtinTools.isEmpty, "沒有工作資料夾就沒有檔案工具")
            throw CabinetError("連不上,token=s3cret")
        }
        r.tick()
        try await waitIdle(r)
        let t = try XCTUnwrap(FakeOmixorServer.tasks.first)
        XCTAssertEqual(t["status"] as? String, "blocked")
        let sent = FakeOmixorServer.requests.map { "\($0.body)" }.joined()
        XCTAssertFalse(sent.contains("s3cret"), "回報裡不能有密鑰")
        XCTAssertFalse(r.log.first?.detail.contains("s3cret") ?? true)
    }

    /// 開了 @ 自動回覆:被 @ 就排一筆「回覆頻道」任務;同一則不重複;沒開的角色不排
    func testMentionCreatesReplyTask() async throws {
        FakeOmixorServer.inbox = [["channelId": "CH-1", "channelName": "動態牆討論", "id": "M-1", "author": "user:tim@example.com",
                                   "text": "@程式設計師 可以看一下嗎?", "at": "2026-11-12T14:33:38Z"]]
        let a = app()
        let r = a.agentRunner
        var c = RunnerConfig()
        c.autoRun = true
        c.autoReply = true
        try r.save("programmer", c)   // save 會 tick 一次(會檢查 @)
        await r.checkMentions()
        let replies = FakeOmixorServer.tasks.filter { ($0["title"] as? String ?? "").hasPrefix("回覆「動態牆討論」") }
        XCTAssertEqual(replies.count, 1, "同一則 @ 只排一次")
        XCTAssertEqual(replies.first?["agentId"] as? String, "programmer")
        XCTAssertEqual(replies.first?["links"] as? [String], ["CH-1", "M-1"])
        let body = FakeOmixorServer.requests.last { $0.path.hasSuffix("/agent-tasks") && $0.method == "POST" }?.body
        XCTAssertTrue((body?["instructions"] as? String ?? "").contains("channel_post"))

        // 沒開自動執行就不能開自動回覆
        var off = RunnerConfig()
        off.autoReply = true
        try r.save("tester", off)
        XCTAssertFalse(r.config("tester").autoReply)
    }

    /// 防止角色互相 @ 的迴圈
    func testReplyLoopGuard() {
        func m(_ author: String) -> ChannelInbox.Mention { .init(channelId: "CH-1", channelName: "x", id: "M-9", author: author, text: "@x", at: .now) }
        XCTAssertNil(AgentRunner.replyBlocked(mentions: [m("user:tim@x")], recent: ["agent:a", "agent:b", "agent:a"], repliesLastHour: 0),
                     "人的 @ 一律可以回")
        XCTAssertNil(AgentRunner.replyBlocked(mentions: [m("agent:a")], recent: ["user:tim@x", "agent:a"], repliesLastHour: 0),
                     "角色接著人的話 @ 一次可以")
        XCTAssertNotNil(AgentRunner.replyBlocked(mentions: [m("agent:a")], recent: ["user:tim@x", "agent:b", "agent:a"], repliesLastHour: 0),
                        "角色之間連續兩則以上就不再觸發")
        XCTAssertNotNil(AgentRunner.replyBlocked(mentions: [m("user:tim@x")], recent: [], repliesLastHour: 6), "一小時上限")
    }
}
