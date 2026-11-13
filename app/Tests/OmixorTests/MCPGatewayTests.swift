import XCTest
@testable import Ops

/// 外部 MCP 閘道:假的 MCP 伺服器(Fixtures/fake_mcp.py,只用 Python 標準函式庫;stdio 與 HTTP 各一)+ 暫存的本機守護服務(core)。
/// 不碰正式鑰匙圈(TokenVault 與 MCPSecretVault 都換成暫存的 service)、不啟動 Claude、不連任何外部服務。
@MainActor
final class MCPGatewayTests: XCTestCase {
    private var dataDir: URL!
    private var procs: [Process] = []

    static let fixture = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("Fixtures/fake_mcp.py").path

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        MCPSecretVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-gateway-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        for p in procs where p.isRunning { p.terminate() }
        IssueServerConfig.testOverride = nil
        unsetenv("OPS_DATA_DIR")
        unsetenv("OPS_ISSUE_SERVER_TOKEN")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func text(_ r: [String: Any]) -> String {
        ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
    }

    // MARK: - 用戶端

    /// stdio:握手、分頁列工具、呼叫;密鑰只在子程序的環境變數(不在命令列),App 自己的 token 不帶過去;逾時;關閉會停掉子程序
    func testStdioClient() async throws {
        setenv("OPS_ISSUE_SERVER_TOKEN", "app-secret-token", 1)
        let t = MCPStdioTransport(command: "python3", args: ["-I", Self.fixture, "stdio"], cwd: nil, secretEnv: ["FAKE_TOKEN": "tok-12345"])
        let c = MCPClient(transport: t, secrets: ["tok-12345"])
        try await c.connect()
        XCTAssertEqual(c.serverName, "fake-mcp")
        let tools = try await c.listTools()
        XCTAssertEqual(tools.map(\.name), ["echo", "whoami", "write_note"], "分頁讀完")
        XCTAssertEqual(tools.first?.title, "回聲")
        let e1 = try await c.callTool("echo", ["text": "你好"])
        XCTAssertEqual(text(e1), "echo:你好")
        let w1 = try await c.callTool("whoami", [:])
        let who = try JSONSerialization.jsonObject(with: Data(text(w1).utf8)) as? [String: Any]
        XCTAssertEqual(who?["hasToken"] as? Bool, true)
        XCTAssertEqual(who?["tokenLen"] as? Int, 9)
        XCTAssertEqual(who?["inArgv"] as? Bool, false, "密鑰不在命令列")
        XCTAssertEqual(who?["hasAppToken"] as? Bool, false, "App 自己的 token 不帶給子程序")
        do {
            _ = try await c.callTool("echo", ["text": "slow"], timeout: 0.5)
            XCTFail("要逾時")
        } catch { XCTAssertTrue(error.localizedDescription.contains("秒內"), error.localizedDescription) }
        do { _ = try await c.callTool("nope", [:]); XCTFail() } catch { XCTAssertTrue(error.localizedDescription.contains("沒有這個工具")) }
        let pid = try XCTUnwrap(t.pid)
        c.close()
        for _ in 0..<50 where kill(pid, 0) == 0 { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertNotEqual(kill(pid, 0), 0, "關閉後子程序結束")
        do { _ = try await c.callTool("echo", [:]); XCTFail() } catch {}
        // 指令不存在
        let bad = MCPClient(transport: MCPStdioTransport(command: "/no/such/mcp", args: [], cwd: nil, secretEnv: [:]))
        do { try await bad.connect(); XCTFail() } catch { XCTAssertTrue(error.localizedDescription.contains("啟動不了")) }
    }

    /// 啟動 HTTP 版的假伺服器,回傳網址
    private func startHTTPFake(expectAuth: String) throws -> URL {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        p.arguments = ["python3", "-I", Self.fixture, "http"]
        var env = ProcessInfo.processInfo.environment
        env["EXPECT_AUTH"] = expectAuth
        p.environment = env
        let out = Pipe()
        p.standardOutput = out
        try p.run()
        procs.append(p)
        var buf = Data()
        let deadline = Date().addingTimeInterval(10)
        while Date() < deadline {
            buf.append(out.fileHandleForReading.availableData)
            if let s = String(data: buf, encoding: .utf8), let line = s.split(separator: "\n").first, line.hasPrefix("PORT ") {
                return URL(string: "http://127.0.0.1:\(line.dropFirst(5))/mcp")!
            }
        }
        throw MCPClientError(message: "假 HTTP 伺服器沒啟動")
    }

    /// HTTP:標頭從密鑰帶入;錯的密鑰 401;session id;SSE 回應
    func testHTTPClient() async throws {
        let url = try startHTTPFake(expectAuth: "good-key-999")
        let wrong = MCPClient(transport: MCPHTTPTransport(url: url, secretHeaders: ["Authorization": "Bearer bad-key-000"]), secrets: ["bad-key-000"])
        do { try await wrong.connect(); XCTFail("錯的密鑰要失敗") } catch {
            XCTAssertTrue(error.localizedDescription.contains("401"), error.localizedDescription)
            XCTAssertFalse(error.localizedDescription.contains("bad-key-000"), "錯誤訊息不含密鑰")
        }
        let c = MCPClient(transport: MCPHTTPTransport(url: url, secretHeaders: ["Authorization": "Bearer good-key-999"]))
        try await c.connect()
        let lt = try await c.listTools()
        XCTAssertEqual(lt.count, 3)
        let e2 = try await c.callTool("echo", ["text": "sse"])
        XCTAssertEqual(text(e2), "echo:sse", "SSE 回應取對應 id 的那則")
        c.close()
    }

    /// MCPServer 的路由:/mcp/gateway 交給閘道(帶 Authorization);別的來源(Origin)一律 403;沒開閘道時是 404
    func testGatewayRoute() async {
        let gate: @Sendable (Data, String?) async -> (Int, Data?) = { _, auth in auth == "Bearer omxgw_ok" ? (200, Data("{}".utf8)) : (401, Data("Unauthorized".utf8)) }
        let echo: @Sendable (Data) async -> Data? = { $0 }
        func req(_ path: String, _ headers: [String: String]) -> HTTPRequest { HTTPRequest(method: "POST", path: path, headers: headers, body: Data("{}".utf8)) }
        let r1 = await MCPServer.route(req("/mcp/gateway", [:]), handle: echo, gateway: gate)
        XCTAssertEqual(r1.status, 401)
        XCTAssertNotNil(r1.extra["WWW-Authenticate"])
        let r2 = await MCPServer.route(req("/mcp/gateway", ["authorization": "Bearer omxgw_ok"]), handle: echo, gateway: gate)
        XCTAssertEqual(r2.status, 200)
        let r3 = await MCPServer.route(req("/mcp/gateway", ["authorization": "Bearer omxgw_ok", "origin": "https://evil.example.com"]), handle: echo, gateway: gate)
        XCTAssertEqual(r3.status, 403)
        let r4 = await MCPServer.route(req("/mcp/gateway", ["authorization": "Bearer omxgw_ok"]), handle: echo)
        XCTAssertEqual(r4.status, 404)
    }

    func testGrantToggling() {
        let all = ["a", "b", "c"]
        var g = MCPGateway.updatedGrants([], server: "fs", tool: "a", on: true, enabledTools: all)
        XCTAssertEqual(g, [CoreMCPGrant(server: "fs", tools: ["a"])])
        g = MCPGateway.updatedGrants(g, server: "fs", tool: nil, on: true, enabledTools: all)
        XCTAssertEqual(g, [CoreMCPGrant(server: "fs", tools: [])], "整台")
        g = MCPGateway.updatedGrants(g, server: "fs", tool: "b", on: false, enabledTools: all)
        XCTAssertEqual(g, [CoreMCPGrant(server: "fs", tools: ["a", "c"])], "整台時取消一個 → 其他開著的")
        g = MCPGateway.updatedGrants(g, server: "fs", tool: "a", on: false, enabledTools: all)
        g = MCPGateway.updatedGrants(g, server: "fs", tool: "c", on: false, enabledTools: all)
        XCTAssertEqual(g, [], "都取消就拿掉")
    }

    // MARK: - 整個流程(要開發版:外部 MCP 閘道是開發版的實驗功能)

    /// 登記 → 設密鑰 → 連線 → 列工具 → 一般呼叫 → 需確認的呼叫(自動允許/拒絕)→ 角色權限過濾 → 閘道端點 token →
    /// AgentRunner 暫存設定帶上閘道與 token(不啟動 Claude)
    func testGatewayFlow() async throws {
        #if !OMIXOR_DEV
        let cfg = try TestCore.start(self)
        // 正式版:核心沒有這個功能,列表是空的、寫入 403
        let v = try IssueRemote(config: cfg).mcpView(org: OrgFiles.defaultId)
        XCTAssertFalse(v.enabled)
        XCTAssertThrowsError(try IssueRemote(config: cfg).saveMCPServer(org: OrgFiles.defaultId, id: "fake", fields: ["transport": "stdio", "command": "x"]))
        throw XCTSkip("外部 MCP 閘道的完整流程只在開發版測(swift test -Xswiftc -DOMIXOR_DEV)")
        #else
        let cfg = try TestCore.start(self)
        let org = OrgFiles.defaultId
        let rem = IssueRemote(config: cfg)
        let app = AppState()
        app.currentOrgId = org
        let sys = app.startSystem()
        let gw = app.mcpGateway

        // 功能沒開:列表空、寫入 403
        await gw.reload()
        XCTAssertEqual(gw.view?.enabled, false)
        let d0 = await gw.toolDefinitions(agent: nil)
        XCTAssertTrue(d0.isEmpty)
        do { try await gw.saveServer("fake", fields: ["transport": "stdio", "command": "python3"]); XCTFail("沒開時寫入要失敗") } catch {
            XCTAssertTrue(error.localizedDescription.contains("外部 MCP 閘道"), error.localizedDescription)
        }
        try sys.set(["mcpGateway": true])
        XCTAssertTrue(app.isFeatureEnabled("mcpGateway"))

        // 登記(只有密鑰名稱)→ 這台輸入密鑰 → 連線 → 讀到工具
        try await gw.saveServer("fake", fields: ["name": "假伺服器", "transport": "stdio", "command": "python3",
                                                "args": ["-I", Self.fixture, "stdio"], "envNames": ["FAKE_TOKEN"]])
        let reg = try XCTUnwrap(gw.view?.server("fake"))
        XCTAssertEqual(reg.envNames, ["FAKE_TOKEN"])
        // 這台還沒信任這個指令:不會執行
        await gw.connect("fake")
        if case .failed(let why) = gw.state("fake").phase { XCTAssertTrue(why.contains("信任"), why) } else { XCTFail("沒信任不能連") }
        gw.trust("fake")
        await gw.connect("fake")
        if case .failed(let why) = gw.state("fake").phase { XCTAssertTrue(why.contains("FAKE_TOKEN"), "還沒輸入密鑰:\(why)") } else { XCTFail() }
        gw.setSecret("tok-12345", server: "fake", kind: .env, name: "FAKE_TOKEN")
        XCTAssertNotNil(MCPSecretVault.get(org: org, server: "fake", kind: .env, name: "FAKE_TOKEN"))
        await gw.connect("fake")
        XCTAssertEqual(gw.state("fake").phase, .connected)
        XCTAssertEqual(gw.state("fake").tools.count, 3)
        // core 的登記裡沒有密鑰值
        let raw = String(decoding: try rem.send("GET", rem.orgURL(org, ["mcp-servers"]), nil), as: UTF8.self)
        XCTAssertFalse(raw.contains("tok-12345"), "密鑰不進 core")

        // 還沒同步規則:預設不開 → 一般用戶端看不到
        let d1 = await gw.toolDefinitions(agent: nil)
        XCTAssertTrue(d1.isEmpty, "預設不開")
        try await gw.syncTools("fake")
        let rules = try XCTUnwrap(gw.view?.server("fake")?.tools)
        XCTAssertEqual(rules.map(\.name), ["echo", "whoami", "write_note"])
        XCTAssertEqual(rules.first { $0.name == "write_note" }?.confirm, true, "寫入類預設要確認")
        for n in ["echo", "write_note"] { try await gw.setRule("fake", tool: n, enabled: true) }

        // 一般用戶端(App 的 /mcp):看得到開著的
        let handler = MCPHandler(app: app)
        let listed = try await mcpCall(handler, "tools/list", [:])
        let names = ((listed["tools"] as? [[String: Any]]) ?? []).compactMap { $0["name"] as? String }
        XCTAssertTrue(names.contains("fake__echo") && names.contains("fake__write_note"), "\(names)")
        XCTAssertFalse(names.contains("fake__whoami"), "沒開的工具不列")
        let echoed = try await mcpCall(handler, "tools/call", ["name": "fake__echo", "arguments": ["text": "嗨"]])
        XCTAssertEqual(text(echoed), "echo:嗨")
        XCTAssertTrue(app.activity.first?.text.contains("fake__echo") ?? false, "記在動態牆")
        let off = await gw.call("fake__whoami", [:], caller: .member(nil))
        XCTAssertEqual(off["isError"] as? Bool, true)

        // 需要確認:自動拒絕 → 錯誤;自動允許 → 執行
        var asked: [MCPConfirmRequest] = []
        gw.confirmer = { r in asked.append(r); return false }
        let denied = await gw.call("fake__write_note", ["note": "x"], caller: .member("claude-code"))
        XCTAssertEqual(denied["isError"] as? Bool, true)
        XCTAssertTrue(text(denied).contains("blocked"), text(denied))
        XCTAssertEqual(asked.first?.tool, "fake__write_note")
        XCTAssertTrue(asked.first?.argsSummary.contains("x") ?? false)
        gw.confirmer = { _ in true }
        let allowed = await gw.call("fake__write_note", ["note": "好"], caller: .member(nil))
        XCTAssertEqual(text(allowed), "已寫入:好")
        XCTAssertTrue(gw.calls.contains { !$0.ok && $0.tool == "fake__write_note" } && gw.calls.contains { $0.ok && $0.tool == "fake__write_note" })
        XCTAssertFalse(app.activity.contains { $0.text.contains("tok-12345") }, "動態牆沒有密鑰")

        // 逾時沒人回應(用 UI 的確認流程)= 拒絕
        gw.confirmer = nil
        MCPGateway.confirmTimeout = 0.3
        defer { MCPGateway.confirmTimeout = 120 }
        let timedOut = await gw.call("fake__write_note", ["note": "y"], caller: .member(nil))
        XCTAssertEqual(timedOut["isError"] as? Bool, true)
        XCTAssertTrue(gw.pending.isEmpty)
        gw.confirmer = { _ in true }

        // 角色權限:預設不給 → 勾選 echo 後只有 echo
        _ = try rem.saveAgent(org: org, Agent(id: "writer", name: "寫手"))
        let d2 = await gw.toolDefinitions(agent: "writer")
        XCTAssertTrue(d2.isEmpty, "角色預設不給")
        try await gw.setGrant(agent: "writer", server: "fake", tool: "echo", on: true)
        let roleTools = await gw.toolDefinitions(agent: "writer").compactMap { $0["name"] as? String }
        XCTAssertEqual(roleTools, ["fake__echo"])
        // 角色設定頁的可用工具也列出外部工具與原因
        let card = try rem.agentTools(org: org, id: "writer").filter { $0.source == "mcp" }
        XCTAssertEqual(card.first { $0.name == "fake__echo" }?.available, true)
        XCTAssertEqual(card.first { $0.name == "fake__write_note" }?.available, false)
        XCTAssertNotNil(card.first { $0.name == "fake__write_note" }?.reason)

        // 閘道端點:一次性 token 綁定角色與任務;只列、只放行角色可用的
        let ep = MCPGatewayEndpoint(app: app)
        let tok = gw.issueToken(agentId: "writer", taskId: "T-9", ttl: 60)
        func gwCall(_ method: String, _ params: [String: Any], token: String?) async throws -> (Int, [String: Any]) {
            let body = try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": 7, "method": method, "params": params])
            let (st, d) = await ep.handle(body, authorization: token.map { "Bearer \($0)" })
            let obj = d.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
            return (st, obj?["result"] as? [String: Any] ?? [:])
        }
        let (s1, l1) = try await gwCall("tools/list", [:], token: tok)
        XCTAssertEqual(s1, 200)
        XCTAssertEqual((l1["tools"] as? [[String: Any]])?.compactMap { $0["name"] as? String }, ["fake__echo"], "沒有 App 自己的工具")
        let (_, c1) = try await gwCall("tools/call", ["name": "fake__echo", "arguments": ["text": "角色"]], token: tok)
        XCTAssertEqual(text(c1), "echo:角色")
        XCTAssertTrue(app.activity.first.map { $0.text.contains("寫手") || $0.text.contains("writer") } ?? false, "動態牆記是哪個角色")
        XCTAssertEqual(app.activity.first?.actor, .ai("writer"))
        let (_, c2) = try await gwCall("tools/call", ["name": "fake__write_note", "arguments": ["note": "z"]], token: tok)
        XCTAssertEqual(c2["isError"] as? Bool, true, "角色沒權限的工具不放行")
        let u1 = try await gwCall("tools/list", [:], token: nil).0
        XCTAssertEqual(u1, 401)
        let u2 = try await gwCall("tools/list", [:], token: "writer:some-secret").0
        XCTAssertEqual(u2, 401, "omixor 角色憑證不能用在閘道")
        let u3 = try await gwCall("tools/list", [:], token: "omxgw_forged").0
        XCTAssertEqual(u3, 401)
        gw.revokeToken(tok)
        let u4 = try await gwCall("tools/list", [:], token: tok).0
        XCTAssertEqual(u4, 401, "任務結束 token 失效")
        let expired = gw.issueToken(agentId: "writer", taskId: "T-9", ttl: -1)
        let u5 = try await gwCall("tools/list", [:], token: expired).0
        XCTAssertEqual(u5, 401, "過期")

        // AgentRunner:暫存 MCP 設定帶上閘道端點與 token,allowedTools 同步放行;不啟動 Claude
        _ = try rem.assignTask(org: org, agent: "writer", title: "用外部工具", instructions: "", priority: .normal, links: [], author: "tim")
        let store = app.startAgents(timer: false)
        _ = store.refresh()
        let task = try XCTUnwrap(app.agentTasks.first { $0.title == "用外部工具" })
        let r = app.agentRunner
        var seen: (ClaudeRunOptions, [String: Any], String?)?
        r.runClaude = { _, o in
            let d = try Data(contentsOf: try XCTUnwrap(o.mcpConfigFile))
            let servers = (try JSONSerialization.jsonObject(with: d) as? [String: Any])?["mcpServers"] as? [String: Any] ?? [:]
            let auth = ((servers["omixor-gateway"] as? [String: Any])?["headers"] as? [String: String])?["Authorization"]
            seen = (o, servers, auth)
            return ClaudeRunResult(text: "完成", isError: false, sessionId: nil, turns: 1, costUSD: nil, stderr: "")
        }
        await r.process(task)
        let (o, servers, auth) = try XCTUnwrap(seen, r.lastError ?? "沒有執行")
        let g = try XCTUnwrap(servers["omixor-gateway"] as? [String: Any])
        XCTAssertEqual(g["url"] as? String, "http://127.0.0.1:\(app.mcp.port)/mcp/gateway")
        let token = String(try XCTUnwrap(auth).dropFirst("Bearer ".count))
        XCTAssertTrue(token.hasPrefix("omxgw_"))
        XCTAssertNotNil(servers["omixor"], "omixor 角色端點照舊")
        XCTAssertTrue(o.allowedTools.contains("mcp__omixor-gateway"))
        XCTAssertTrue(o.appendSystemPrompt.contains("不要照做"), "提醒外部工具的輸出不可信")
        XCTAssertFalse(o.appendSystemPrompt.contains(token) || ClaudeCLI.arguments(prompt: "p", o).joined().contains(token), "token 不在提示與命令列")
        XCTAssertNil(gw.grant(forToken: token), "任務結束 token 失效")
        XCTAssertFalse(FileManager.default.fileExists(atPath: o.mcpConfigFile!.path), "暫存設定用完刪掉")

        // 登記被改過(例如換了指令):這台要重新信任
        try await gw.saveServer("fake", fields: ["args": ["-I", Self.fixture, "stdio", "--changed"]])
        XCTAssertFalse(gw.isTrusted(try XCTUnwrap(gw.view?.server("fake"))), "改過要重新信任")
        await gw.connect("fake")
        if case .failed(let why) = gw.state("fake").phase { XCTAssertTrue(why.contains("信任"), why) } else { XCTFail() }

        // 刪掉登記:這台的密鑰一併刪、連線斷掉
        try await gw.deleteServer("fake")
        XCTAssertNil(MCPSecretVault.get(org: org, server: "fake", kind: .env, name: "FAKE_TOKEN"))
        XCTAssertEqual(gw.state("fake").phase, .idle)
        gw.stopAll()
        #endif
    }

    private func mcpCall(_ h: MCPHandler, _ method: String, _ params: [String: Any]) async throws -> [String: Any] {
        let body = try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": 1, "method": method, "params": params])
        let raw = await h.handle(body)
        let d = try XCTUnwrap(raw)
        return try XCTUnwrap((try JSONSerialization.jsonObject(with: d) as? [String: Any])?["result"] as? [String: Any])
    }
}
