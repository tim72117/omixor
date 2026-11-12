import XCTest
@testable import Ops

/// 角色自動執行:假伺服器(FakeOmixorServer)+ 假的 Claude(不會真的呼叫 claude)
@MainActor
final class AgentRunnerTests: XCTestCase {
    var dataDir: URL!
    let org = "~omixor"

    override func setUp() async throws {
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
}
