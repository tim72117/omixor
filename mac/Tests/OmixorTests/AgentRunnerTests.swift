import XCTest
@testable import Omixor

/// 角色自動執行:假伺服器(FakeOmixorServer)+ 假的 Claude(不會真的呼叫 claude)
@MainActor
final class AgentRunnerTests: XCTestCase {
    var dataDir: URL!
    let org = "~omixor"

    override func setUp() async throws {
        try TestCore.start(self)   // 資料一律透過 core server:每個測試一個新的本機守護服務
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-runner-\(UUID().uuidString)")
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
    }

    private func app() -> AppState {
        let a = AppState()
        a.currentOrgId = org
        a.startAgents(timer: false)
        a.agentRunner.checkLogin = { .loggedIn }   // 不真的呼叫 claude
        a.agentRunner.loginForTest(.loggedIn)
        return a
    }

    private func waitIdle(_ r: AgentRunner) async throws {
        for _ in 0..<100 {
            try await Task.sleep(for: .milliseconds(20))
            if r.running == nil, !r.history.isEmpty { return }
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
            self.omixorRan(o)
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
        XCTAssertEqual(r.history.first?.outcome, "done")
    }

    /// 人工啟動:autoRun 關著的角色也能跑(沒存設定用預設設定);迴圈不會領它;切換自動/手動只改 autoRun
    func testManualStartRunsManualRole() async throws {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "人工啟動", links: [], by: "tim")
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "自動角色立刻跑", links: [], by: "tim")
        let a = app()
        let r = a.agentRunner
        r.findClaude = { true }
        var model = "?"
        r.runClaude = { _, o in model = o.model; self.omixorRan(o); return ClaudeRunResult(text: "好了", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "") }
        r.tick()
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertTrue(FakeOmixorServer.tasks.allSatisfy { $0["status"] as? String == "queued" }, "手動角色迴圈不領")
        let t = try XCTUnwrap(a.agentTasks.first { $0.title == "人工啟動" })
        XCTAssertNil(r.startBlockReason(t))
        try await r.startManually(t).value
        XCTAssertEqual(FakeOmixorServer.tasks.first { $0["title"] as? String == "人工啟動" }?["status"] as? String, "done")
        XCTAssertEqual(model, "", "沒存設定:預設設定")
        XCTAssertEqual(r.history.first?.outcome, "done")

        var c = RunnerConfig()
        c.model = "haiku"
        c.timeoutMinutes = 10
        try r.save("programmer", c)
        try r.setAutoRun("programmer", true)
        XCTAssertEqual(r.config("programmer").model, "haiku")
        XCTAssertEqual(r.config("programmer").timeoutMinutes, 10)
        XCTAssertTrue(r.config("programmer").autoRun)

    }

    /// 人工啟動的檢查:功能關閉、不是待領取、角色停用、找不到 claude、忙碌中都回原因,不領取
    func testManualStartGates() async throws {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "一", links: [], by: "tim")
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "二", links: [], by: "tim")
        _ = FakeOmixorServer.newTask(agent: "tester", title: "停用角色的", links: [], by: "tim")
        let a = app()
        let r = a.agentRunner
        r.findClaude = { true }
        let t1 = try XCTUnwrap(a.agentTasks.first { $0.title == "一" })
        let t2 = try XCTUnwrap(a.agentTasks.first { $0.title == "二" })
        let t3 = try XCTUnwrap(a.agentTasks.first { $0.title == "停用角色的" })
        XCTAssertNil(r.startBlockReason(t1))
        var auto = RunnerConfig()
        auto.autoRun = true
        r.configsForTest("programmer", auto)
        XCTAssertNil(r.startBlockReason(t1), "autoRun 開著的角色也能人工立刻啟動")
        r.configsForTest("programmer", RunnerConfig())
        XCTAssertNotNil(r.startBlockReason(t3), "角色停用")
        XCTAssertThrowsError(try r.startManually(t3))
        r.findClaude = { false }
        XCTAssertNotNil(r.startBlockReason(t1), "找不到 claude")
        r.findClaude = { true }

        var s = a.features.settings
        let i = try XCTUnwrap(s.features.firstIndex { $0.key == AgentRunner.featureKey })
        s.features[i].enabled = false
        s.features[i].on = false
        a.features = FeatureFlags(settings: s, source: .core)
        XCTAssertNotNil(r.startBlockReason(t1), "功能關閉")
        s.features[i].enabled = true
        s.features[i].on = true
        a.features = FeatureFlags(settings: s, source: .core)
        XCTAssertNil(r.startBlockReason(t1))
        XCTAssertEqual(FakeOmixorServer.tasks.filter { $0["status"] as? String != "queued" }.count, 0, "被擋下的不能領取")

        // 忙碌:第一筆在跑時第二筆不能啟動;不是待領取也不行
        var release: CheckedContinuation<Void, Never>?
        r.runClaude = { _, _ in
            await withCheckedContinuation { release = $0 }
            return ClaudeRunResult(text: "ok", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "")
        }
        let job = try r.startManually(t1)
        XCTAssertNotNil(r.startBlockReason(t2), "忙碌中")
        for _ in 0..<100 where release == nil { try await Task.sleep(for: .milliseconds(20)) }
        _ = try XCTUnwrap(release)
        release?.resume()
        await job.value
        XCTAssertNotNil(r.startBlockReason(t1), "已經不是待領取")
    }

    func testParseAuth() {
        XCTAssertEqual(AgentRunner.parseAuth(#"{"loggedIn":true,"authMethod":"x"}"#), .loggedIn)
        XCTAssertEqual(AgentRunner.parseAuth(#"{"loggedIn":false,"authMethod":"none"}"#), .loggedOut)
        XCTAssertEqual(AgentRunner.parseAuth("Not logged in. Run claude auth login"), .loggedOut)
        XCTAssertEqual(AgentRunner.parseAuth(""), .unknown, "逾時沒有輸出")
        XCTAssertEqual(AgentRunner.parseAuth("error: unknown command 'auth'"), .unknown)
        XCTAssertEqual(AgentRunner.parseAuth(#"{"x":1}"#), .unknown)
    }

    /// 沒登入:迴圈不領任務(留在待領取、lastError 記一次)、人工啟動被擋;登入後照常
    func testNotLoggedInBlocksClaiming() async throws {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "未登入", links: [], by: "tim")
        let a = app()
        let r = a.agentRunner
        r.findClaude = { true }
        var ran = false
        r.runClaude = { _, _ in ran = true; return ClaudeRunResult(text: "x", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "") }
        var state = AgentRunner.LoginState.loggedOut
        r.checkLogin = { state }
        r.loginForTest(.loggedOut)
        var c = RunnerConfig()
        c.autoRun = true
        try r.save("programmer", c)
        r.tick()
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertFalse(ran)
        XCTAssertEqual(FakeOmixorServer.tasks.first?["status"] as? String, "queued", "未登入不領取")
        XCTAssertEqual(r.lastError, AgentRunner.loginOutError)
        let t = try XCTUnwrap(a.agentTasks.first)
        XCTAssertNotNil(r.startBlockReason(t), "未登入擋人工啟動")
        r.loginForTest(.unknown)
        XCTAssertNotNil(r.startBlockReason(t), "無法確認也不能啟動")
        state = .loggedIn
        r.loginForTest(.loggedIn)
        XCTAssertNil(r.startBlockReason(t))
        r.tick()
        try await waitIdle(r)
        XCTAssertTrue(ran, "登入後照常")
    }

    /// 功能開關「自動執行」關掉:不領新任務(也不因 @ 排回覆任務);打開後照常
    func testFeatureOffStopsClaiming() async throws {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "關掉時不要領", links: [], by: "tim")
        let a = app()
        var s = a.features.settings
        let i = try XCTUnwrap(s.features.firstIndex { $0.key == AgentRunner.featureKey }, "core 的功能清單要有 automation.runner")
        s.features[i].enabled = false
        s.features[i].on = false
        a.features = FeatureFlags(settings: s, source: .core)
        XCTAssertFalse(a.agentRunner.featureEnabled)
        let r = a.agentRunner
        var ran = false
        r.runClaude = { _, _ in ran = true; return ClaudeRunResult(text: "x", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "") }
        var c = RunnerConfig()
        c.autoRun = true
        try r.save("programmer", c)   // save 會 tick
        r.tick()
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertFalse(ran, "關掉時不執行")
        XCTAssertEqual(FakeOmixorServer.tasks.first?["status"] as? String, "queued", "關掉時不領取")

        s.features[i].enabled = true
        s.features[i].on = true
        a.features = FeatureFlags(settings: s, source: .core)
        r.tick()
        try await waitIdle(r)
        XCTAssertTrue(ran, "打開後照常")
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
        XCTAssertFalse(r.history.first?.error.contains("s3cret") ?? true)
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

    // MARK: 執行日誌

    /// 假的 Claude 事件:init 帶 omixor 的 MCP 狀態;calls 為 true 再加一個 omixor 工具呼叫
    private func mcpEvents(_ o: ClaudeRunOptions, status: String, calls: Bool) {
        o.eventSink?(["type": "system", "subtype": "init", "model": "m", "session_id": "s1", "mcp_servers": [["name": "omixor", "status": status]]])
        if calls {
            o.eventSink?(toolUse("t0", "mcp__omixor__agent_context", [:]))
            o.eventSink?(toolResult("t0", "ok"))
        }
    }
    /// 正常情況:omixor 已連上、角色呼叫了一個 omixor 工具
    private func omixorRan(_ o: ClaudeRunOptions) { mcpEvents(o, status: "connected", calls: true) }

    private func runOnce(title: String, _ body: @escaping (ClaudeRunOptions) async throws -> ClaudeRunResult) async throws -> (AgentRunner, [String: Any]) {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: title, links: [], by: "tim")
        let r = app().agentRunner
        var c = RunnerConfig()
        c.autoRun = true
        try r.save("programmer", c)
        r.runClaude = { _, o in try await body(o) }
        r.tick()
        try await waitIdle(r)
        return (r, try XCTUnwrap(FakeOmixorServer.tasks.first { $0["title"] as? String == title }))
    }

    /// MCP pending 且沒呼叫任何 omixor 工具:不報 done,報 blocked,原因帶 MCP 狀態;執行紀錄是錯誤
    func testMcpPendingWithoutToolCallsBlocksNotDone() async throws {
        let (r, t) = try await runOnce(title: "沒連上") { o in
            self.mcpEvents(o, status: "pending", calls: false)
            return ClaudeRunResult(text: "好了", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "")
        }
        XCTAssertEqual(t["status"] as? String, "blocked")
        XCTAssertEqual(r.history.first?.outcome, RunOutcome.error)
        XCTAssertTrue(r.history.first?.error.contains("MCP 狀態:pending") ?? false)
        XCTAssertTrue("\(t)".contains("MCP 狀態:pending"), "任務備註要帶原因")
    }

    /// init 就是 failed:abortReason 馬上有值(ClaudeCLI 會結束 CLI),任務 blocked
    func testMcpFailedAbortsEarly() async throws {
        var aborted: String?
        let (r, t) = try await runOnce(title: "連線失敗") { o in
            self.mcpEvents(o, status: "failed", calls: false)
            aborted = o.abortReason?()
            throw CabinetError(aborted ?? "沒有中止")
        }
        XCTAssertTrue(aborted?.contains("MCP 狀態:failed") ?? false)
        XCTAssertEqual(t["status"] as? String, "blocked")
        XCTAssertEqual(r.history.first?.outcome, RunOutcome.error)
    }

    /// 連上了、有呼叫工具、但沒回報:維持補回報 done(reportedBy=runner)
    func testMcpConnectedFallbackStillDone() async throws {
        let (r, t) = try await runOnce(title: "有做事") { o in
            self.omixorRan(o)
            return ClaudeRunResult(text: "完成", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "")
        }
        XCTAssertEqual(t["status"] as? String, "done")
        XCTAssertEqual(r.history.first?.reportedBy, RunReportedBy.runner)
    }

    /// 呼叫了 omixor 工具但全部失敗(沒回報):不能補報 done,報 blocked、紀錄是錯誤,說明帶呼叫次數與最後一筆錯誤
    func testMcpAllCallsFailedBlocksNotDone() async throws {
        let (r, t) = try await runOnce(title: "全部失敗") { o in
            self.mcpEvents(o, status: "connected", calls: false)
            o.eventSink?(self.toolUse("a", "mcp__omixor__agent_context", [:])); o.eventSink?(self.toolResult("a", "unavailable", error: true))
            o.eventSink?(self.toolUse("b", "mcp__omixor__issues", [:])); o.eventSink?(self.toolResult("b", "permission denied", error: true))
            return ClaudeRunResult(text: "好了", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "")
        }
        XCTAssertEqual(t["status"] as? String, "blocked")
        XCTAssertEqual(r.history.first?.outcome, RunOutcome.error)
        let d = r.history.first?.error ?? ""
        XCTAssertTrue(d.contains("全部失敗") && d.contains("呼叫 2 次") && d.contains("permission denied"), d)
    }

    /// 有失敗也有成功:維持補回報 done
    func testMcpSomeCallsFailedStillDone() async throws {
        let (_, t) = try await runOnce(title: "部分失敗") { o in
            self.mcpEvents(o, status: "connected", calls: false)
            o.eventSink?(self.toolUse("a", "mcp__omixor__agent_context", [:])); o.eventSink?(self.toolResult("a", "boom", error: true))
            o.eventSink?(self.toolUse("b", "mcp__omixor__issues", [:])); o.eventSink?(self.toolResult("b", "ok"))
            return ClaudeRunResult(text: "完成", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "")
        }
        XCTAssertEqual(t["status"] as? String, "done")
    }

    func testMcpGateAndEnv() {
        XCTAssertEqual(McpGate.of("connected"), .proceed)
        XCTAssertEqual(McpGate.of("pending"), .watch)
        for s in ["failed", "needs-auth", ""] { XCTAssertEqual(McpGate.of(s), .abort) }
        XCTAssertEqual(McpGate.of(nil), .abort)
        XCTAssertEqual(ClaudeCLI.waitForMCPEnv(timeoutMs: 15000), ["MCP_CONNECTION_NONBLOCKING": "false", "MCP_CONNECT_TIMEOUT_MS": "15000"])
    }

    private func toolUse(_ id: String, _ name: String, _ input: [String: Any]) -> [String: Any] {
        ["type": "assistant", "message": ["content": [["type": "tool_use", "id": id, "name": name, "input": input]]]]
    }
    private func toolResult(_ id: String, _ text: String, error: Bool = false) -> [String: Any] {
        ["type": "user", "message": ["content": [["type": "tool_result", "tool_use_id": id, "content": text, "is_error": error]]]]
    }

    /// 角色呼叫工具但沒回報:日誌記下工具與結果、機密被遮、過長截斷、reportedBy=runner;日誌檔不含密鑰
    func testRunLogRecordsToolsMasksSecretsAndRunnerFallback() async throws {
        _ = FakeOmixorServer.newTask(agent: "programmer", title: "寫日誌", links: [], by: "tim")
        let a = app()
        let r = a.agentRunner
        var c = RunnerConfig()
        c.autoRun = true
        try r.save("programmer", c)
        r.runClaude = { _, o in
            let sink = try XCTUnwrap(o.eventSink)
            sink(["type": "system", "subtype": "init", "model": "claude-x", "session_id": "s1", "mcp_servers": [["name": "omixor", "status": "connected"]]])
            sink(self.toolUse("t1", "mcp__omixor__agent_context", ["task": "T-1", "token": "abcdef123456", "note": "s3cret"]))
            sink(self.toolResult("t1", "Authorization: Bearer s3cret"))
            sink(self.toolUse("t2", "mcp__omixor__issue_update", ["id": "ISS-1"]))
            sink(self.toolResult("t2", String(repeating: "長", count: 5000), error: true))
            return ClaudeRunResult(text: "完成", isError: false, sessionId: nil, turns: 1, costUSD: 0.01, stderr: "")
        }
        r.tick()
        try await waitIdle(r)

        let l = try XCTUnwrap(r.history.first)
        XCTAssertEqual(RunLogStore.load(a.dataDir.appendingPathComponent("agent-run-logs.json")).runs.first?.runId, l.runId, "也寫進日誌檔")
        XCTAssertEqual(l.entries.map(\.kind), ["start", "tool_call", "tool_result", "tool_call", "tool_result", "end"])
        XCTAssertEqual(l.entries[2].tool, "mcp__omixor__agent_context")
        XCTAssertEqual(l.entries[4].status, "error")
        XCTAssertLessThanOrEqual(l.entries[4].text?.count ?? 0, RunLogLimits.resultMax + 1)
        XCTAssertLessThanOrEqual(l.entries[1].args?.count ?? 0, RunLogLimits.argsMax + 1)
        XCTAssertEqual(l.reportedBy, RunReportedBy.runner)
        XCTAssertEqual(l.model, "claude-x")
        XCTAssertEqual(l.mcpStatus, "omixor:connected")
        XCTAssertEqual(l.toolCalls, 2)
        XCTAssertEqual(l.entries.count, 6)
        let raw = try String(contentsOf: a.dataDir.appendingPathComponent("agent-run-logs.json"), encoding: .utf8)
        XCTAssertFalse(raw.contains("s3cret") || raw.contains("abcdef123456"), "日誌不能有密鑰與 token")
    }

    /// 項目數與保留筆數有上限;結束項目一定留
    func testRunLogCapsAndRetention() throws {
        let t = AgentTask(id: "T-1", title: "x", agentId: "a", createdBy: "tim")
        let rec = RunLogRecorder(task: t, agentName: "a", started: Date(), secrets: { [] })
        for i in 0..<(RunLogLimits.maxEntries + 100) { rec.consume(toolUse("\(i)", "mcp__omixor__x", [:])) }
        let l = rec.finish(outcome: "done", error: "", seconds: 1, cost: nil, reportedBy: RunReportedBy.runner)
        XCTAssertEqual(l.entries.count, RunLogLimits.maxEntries)
        XCTAssertTrue(l.truncated)
        XCTAssertEqual(l.toolCalls, RunLogLimits.maxEntries + 100)
        XCTAssertEqual(l.entries.last?.kind, "end")

        let url = FileManager.default.temporaryDirectory.appendingPathComponent("runlog-\(UUID().uuidString).json")
        defer { try? FileManager.default.removeItem(at: url) }
        for i in 0..<(RunLogLimits.keep + 5) {
            var x = RunLog()
            x.runId = "r\(i)"
            x.startedAt = Date(timeIntervalSince1970: Double(i))
            try RunLogStore.persist(x, to: url)
        }
        let runs = RunLogStore.load(url).runs
        XCTAssertEqual(runs.count, RunLogLimits.keep)
        XCTAssertEqual(runs.first?.runId, "r\(RunLogLimits.keep + 4)")
        XCTAssertEqual(runs.last?.runId, "r5")
    }

    // MARK: 執行日誌頁籤、需要處理的純邏輯(對應 console 的 runLogModel.ts、needsHelpModel.ts)

    private func record(_ id: String, agent: String, outcome: String) -> RunLog {
        var r = RunLog()
        r.runId = id
        r.agentId = agent
        r.agentName = "名字-\(agent)"
        r.outcome = outcome
        return r
    }

    /// 依角色與結果篩選、順序不變;舊版日誌的 "錯誤" 當作 error;沒列到的結果值照原字串顯示
    func testRunLogFiltersAndOutcomeStyle() {
        let h = [record("1", agent: "a", outcome: "done"), record("2", agent: "b", outcome: "blocked"),
                 record("3", agent: "a", outcome: "error"), record("4", agent: "b", outcome: "錯誤")]
        XCTAssertEqual(h.filtered(agent: "", outcome: .all).map(\.runId), ["1", "2", "3", "4"])
        XCTAssertEqual(h.filtered(agent: "a", outcome: .all).map(\.runId), ["1", "3"])
        XCTAssertEqual(h.filtered(agent: "", outcome: .error).map(\.runId), ["3", "4"])
        XCTAssertEqual(h.filtered(agent: "b", outcome: .blocked).map(\.runId), ["2"])
        XCTAssertTrue(h.filtered(agent: "a", outcome: .blocked).isEmpty)
        XCTAssertEqual(h.roles.map(\.id), ["a", "b"], "角色依第一次出現的順序,只列一次")
        XCTAssertEqual(h.roles.first?.name, "名字-a")
        XCTAssertEqual(RunOutcomeStyle.of("done").label, "完成")
        XCTAssertEqual(RunOutcomeStyle.of("blocked").label, "需要協助")
        XCTAssertEqual(RunOutcomeStyle.of(h[3].outcomeKey).label, "錯誤")
        XCTAssertEqual(RunOutcomeStyle.of("weird").label, "weird")
        XCTAssertEqual(RunOutcomeFilter.allCases.map(\.label), ["全部結果", "完成", "需要協助", "錯誤"])
    }

    /// 需要處理:只有 blocked,等最久的在前;回覆要有內容、不超過上限(以去掉頭尾空白後的字數算)
    func testNeedsHelpModel() {
        func task(_ id: String, _ status: AgentTaskStatus, at: Double) -> AgentTask {
            var t = AgentTask(id: id, title: id, agentId: "a", createdBy: "tim")
            t.status = status
            t.updatedAt = Date(timeIntervalSince1970: at)
            return t
        }
        let all = [task("T-1", .blocked, at: 300), task("T-2", .queued, at: 100), task("T-3", .blocked, at: 200), task("T-4", .done, at: 50)]
        XCTAssertEqual(NeedsHelpModel.tasks(all).map(\.id), ["T-3", "T-1"])
        XCTAssertTrue(all.filter(\.needsHelp).allSatisfy { $0.status == .blocked })
        XCTAssertEqual(NeedsHelpModel.replyProblem("  \n "), "要寫回覆內容")
        XCTAssertEqual(NeedsHelpModel.replyProblem("好"), "")
        XCTAssertEqual(NeedsHelpModel.replyProblem(String(repeating: "字", count: NeedsHelpModel.replyLimit)), "")
        XCTAssertEqual(NeedsHelpModel.replyProblem(String(repeating: "字", count: NeedsHelpModel.replyLimit + 1)), "回覆太長(4001 / 4000 字)")
    }

    /// 歷史從日誌檔補上(重開 App 後還看得到),和執行中剛寫入的合併、同 runId 不重複
    func testLoadHistoryMergesFileAndMemory() async throws {
        let a = app()
        let r = a.agentRunner
        var old = record("old", agent: "a", outcome: "done")
        old.startedAt = Date(timeIntervalSince1970: 100)
        try RunLogStore.persist(old, to: a.dataDir.appendingPathComponent("agent-run-logs.json"))
        XCTAssertTrue(r.history.isEmpty)
        await r.loadHistory()
        XCTAssertEqual(r.history.map(\.runId), ["old"])
        await r.loadHistory()
        XCTAssertEqual(r.history.count, 1, "重讀不重複")
    }
}
