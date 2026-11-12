import XCTest
@testable import Ops

/// 訊息頻道(本機模式):建立與驗證、成員權限、@ 解析、未讀/已讀、訊息分頁、綁 Issue、刪角色時拿掉成員、MCP 工具
@MainActor
final class ChannelTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-channels-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func setup() -> (AppState, ChannelStore) {
        let app = AppState()
        app.startTestAgents()
        return (app, app.startChannels())
    }

    private func status(_ block: () throws -> Void, _ want: Int, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertThrowsError(try block(), file: file, line: line) { e in
            XCTAssertEqual((e as? ChannelError)?.status, want, "\(e.localizedDescription)", file: file, line: line)
        }
    }

    private func call(_ mcp: MCPHandler, _ name: String, _ args: [String: Any]) async throws -> ([String: Any], String, Bool) {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        return ((try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] ?? [:], text, r["isError"] as? Bool ?? false)
    }

    /// 建立:名稱、主題長度、成員正規化、角色不能建;存在 channels.json,重開讀得回來;動態牆記一筆
    func testCreateAndValidation() throws {
        let (app, store) = setup()
        let me = ChainNode.user(app.me)
        status({ try store.create(name: "   ") }, 400)
        status({ try store.create(name: String(repeating: "長", count: 81)) }, 400)
        status({ try store.create(name: "x", topic: String(repeating: "t", count: 501)) }, 400)
        status({ try store.create(name: "x", members: ["agent:nobody"]) }, 400)
        status({ try store.create(name: "x", members: ["沒有這個角色"]) }, 400)
        status({ try store.create(name: "x", as: "agent:marketing") }, 403)
        XCTAssertTrue(app.channels.isEmpty, "驗證失敗不能存")

        let r = try store.create(name: "  課程上線  ", topic: "討論上線", members: ["行銷", "agent:course-dev", "PM@Example.com", "narrator", "marketing"])
        XCTAssertTrue(r.created)
        let c = r.channel.channel
        XCTAssertEqual(c.id, "CH-1")
        XCTAssertEqual(c.name, "課程上線")
        XCTAssertEqual(c.members, [me, "agent:marketing", "agent:course-dev", "user:pm@example.com", "agent:narrator"])
        XCTAssertEqual(c.createdBy, me)
        XCTAssertEqual(c.issueId, "")
        XCTAssertEqual(c.createdAt.timeIntervalSince1970, c.createdAt.timeIntervalSince1970.rounded(.down), "時間到秒")
        XCTAssertEqual(app.channels.map(\.id), ["CH-1"])
        XCTAssertTrue(app.activity.contains { $0.text.contains("建立頻道「課程上線」") && $0.section == .channels })
        XCTAssertEqual(try store.create(name: "第二個").channel.id, "CH-2")

        XCTAssertTrue(FileManager.default.fileExists(atPath: app.dataDir.appendingPathComponent("channels.json").path))
        let app2 = AppState()
        app2.startTestAgents()
        let store2 = app2.startChannels()
        XCTAssertEqual(try store2.channel("課程上線").channel, c)
        XCTAssertEqual(Set(app2.channels.map(\.id)), ["CH-1", "CH-2"])

        // JSON 格式同核心:列表項目是攤平的 Channel + unread + lastMessageAt
        let obj = try XCTUnwrap(MCPJSON.object(r.channel) as? [String: Any])
        XCTAssertEqual(obj["id"] as? String, "CH-1")
        XCTAssertEqual(obj["unread"] as? Int, 0)
        XCTAssertTrue(obj["lastMessageAt"] is NSNull)
        XCTAssertEqual(obj["issueId"] as? String, "")
    }

    /// 權限:角色只能看與發自己是成員的頻道、不能改;管理員(本機使用者)看得到全部,但發訊息要是成員
    func testPermissions() throws {
        let (app, store) = setup()
        let me = ChainNode.user(app.me)
        let c = try store.create(name: "行銷討論", members: ["marketing"]).channel
        let other = try store.create(name: "私人").channel

        // 角色:成員可以讀、發;非成員看不到
        XCTAssertEqual(try store.list(as: "agent:marketing").map(\.id), [c.id])
        XCTAssertEqual(try store.list(as: "agent:narrator").map(\.id), [])
        status({ _ = try store.channel(other.id, as: "agent:marketing") }, 403)
        status({ _ = try store.channel("私人", as: "agent:marketing") }, 404)
        status({ try store.post(other.id, text: "hi", as: "agent:marketing") }, 403)
        XCTAssertEqual(try store.post(c.id, text: "收到", as: "agent:marketing").author, "agent:marketing")
        // 角色不能改成員、名稱
        status({ try store.changeMembers(c.id, add: ["narrator"], as: "agent:marketing") }, 403)
        status({ try store.update(c.id, name: "新名稱", as: "agent:marketing") }, 403)

        // 管理員把自己拿掉:還看得到、可以改,但不能發
        try store.changeMembers(c.id, add: ["narrator"], remove: [me])
        XCTAssertEqual(try store.channel(c.id).channel.members, ["agent:marketing", "agent:narrator"])
        XCTAssertTrue(app.channels.contains { $0.id == c.id })
        status({ try store.post(c.id, text: "我是管理員") }, 403)
        XCTAssertEqual(try store.update(c.id, name: "行銷", topic: "主題").channel.name, "行銷")
        status({ try store.update(c.id, name: " ") }, 400)
        XCTAssertNoThrow(try store.messages(c.id))
        // 非成員的一般使用者看不到
        status({ _ = try store.messages(c.id, as: "user:alice@example.com") }, 403)
        // 訊息驗證
        status({ try store.post(other.id, text: "   ") }, 400)
        status({ try store.post(other.id, text: String(repeating: "a", count: 8001)) }, 400)
        status({ try store.post("CH-99", text: "x") }, 404)
    }

    /// @ 解析:角色名稱、角色 id、email、完整節點;取最長、邊界、只算成員、作者不算、去重
    func testMentionParsing() throws {
        let (app, store) = setup()
        var boss = Agent(id: "marketing-lead", name: "行銷主管")
        boss.active = true
        store.app.startAgents().upsert(boss)
        let members = ["user:tim", "user:pm@example.com", "agent:marketing", "agent:marketing-lead", "agent:course-dev"]
        func p(_ t: String, author: String = "user:tim") -> [String] {
            ChannelMention.parse(t, members: members, agents: app.agents, author: author)
        }
        XCTAssertEqual(p("@行銷 看一下"), ["agent:marketing"])
        XCTAssertEqual(p("請@行銷主管處理"), ["agent:marketing-lead"], "前面是中文(非英數)也算;取最長的")
        XCTAssertEqual(p("@marketing-lead 與 @marketing,還有 @MARKETING"), ["agent:marketing-lead", "agent:marketing"], "不分大小寫、去重")
        XCTAssertEqual(p("@marketingx @marketing.y @marketing-x"), [], "id 後面接英數或 ._- 不算")
        XCTAssertEqual(p("@marketing。"), ["agent:marketing"])
        XCTAssertEqual(p("cc @PM@example.com 看一下"), ["user:pm@example.com"])
        XCTAssertEqual(p("@pm@example.comx @pm@example.com.tw"), [], "email 後面接英數或 ._- 不算")
        XCTAssertEqual(p("寄到 tim@example.com"), [], "@ 前面是英數不算")
        XCTAssertEqual(p("@agent:course-dev @user:pm@example.com"), ["agent:course-dev", "user:pm@example.com"])
        XCTAssertEqual(p("@narrator 不是成員"), [], "不是成員的忽略")
        XCTAssertEqual(p("@tim 我自己"), [], "作者自己不算")
        XCTAssertEqual(p("@tim", author: "agent:marketing"), ["user:tim"])
        XCTAssertEqual(p("@"), [])

        // 發訊息時記下 mentions,@ 角色寫進動態牆
        let c = try store.create(name: "x", members: ["marketing", "narrator"]).channel
        let m = try store.post(c.id, text: "@行銷 @narrator 請看 @course-dev")
        XCTAssertEqual(m.mentions, ["agent:marketing", "agent:narrator"])
        XCTAssertTrue(app.activity.contains { $0.text.contains("提到 行銷、旁白製作") }, app.activity.prefix(3).map(\.text).joined(separator: "\n"))
    }

    /// 未讀/已讀:自己的訊息不算未讀;標已讀(到最新或 upTo);inbox 只列提到它的未讀訊息
    func testUnreadAndInbox() throws {
        let (app, store) = setup()
        let c = try store.create(name: "x", members: ["marketing", "narrator"]).channel
        try store.post(c.id, text: "大家好")
        XCTAssertEqual(app.channels.first?.unread, 0, "自己的訊息不算未讀")
        XCTAssertNotNil(app.channels.first?.lastMessageAt)
        try store.post(c.id, text: "@tim 我做完了", as: "agent:marketing")
        try store.post(c.id, text: "補充", as: "agent:narrator")
        XCTAssertEqual(app.channels.first?.unread, 2)
        XCTAssertEqual(app.badge(for: .channels), 2)
        XCTAssertEqual(try store.markRead(c.id, upTo: "M-2").lastRead, 2)
        XCTAssertEqual(app.channels.first?.unread, 1)
        XCTAssertEqual(try store.markRead(c.id, upTo: "M-1").lastRead, 2, "已讀不會倒退")
        XCTAssertEqual(try store.markRead(c.id).lastRead, 3)
        XCTAssertEqual(app.channels.first?.unread, 0)
        status({ try store.markRead(c.id, upTo: "abc") }, 400)

        // 角色的 inbox
        try store.post(c.id, text: "@行銷 再看一下", as: "agent:narrator")
        try store.post(c.id, text: "@marketing 還有這個")
        var b = try store.inbox(for: "agent:marketing")
        XCTAssertEqual(b.mentions.map(\.id), ["M-5", "M-4"], "新的在前,自己發的不算")
        XCTAssertEqual(b.mentions.first?.channelName, "x")
        XCTAssertEqual(b.channels.first?.unread, 3, "M-3、M-4、M-5(M-2 是自己發的;發訊息時之前的算讀過)")
        try store.markRead(c.id, as: "agent:marketing")
        b = try store.inbox(for: "agent:marketing")
        XCTAssertEqual(b.mentions.count, 0)
        XCTAssertEqual(b.channels.first?.unread, 0)
        XCTAssertEqual(try store.inbox(for: "agent:course-dev").channels.count, 0, "不是成員的頻道不列")
    }

    /// 訊息分頁:沒給 since → 最新的 limit 筆;since(M-n 或時間)→ 之後最早的;before;limit 預設 50、最多 200
    func testMessagePaging() throws {
        let (_, store) = setup()
        let c = try store.create(name: "x").channel
        for i in 1...60 { try store.post(c.id, text: "第 \(i) 則") }
        var p = try store.messages(c.id)
        XCTAssertEqual(p.messages.count, 50)
        XCTAssertEqual(p.messages.first?.id, "M-11")
        XCTAssertEqual(p.messages.last?.id, "M-60")
        XCTAssertTrue(p.hasMore)
        p = try store.messages(c.id, since: "M-10", limit: 5)
        XCTAssertEqual(p.messages.map(\.id), ["M-11", "M-12", "M-13", "M-14", "M-15"])
        XCTAssertTrue(p.hasMore)
        p = try store.messages(c.id, since: "M-58")
        XCTAssertEqual(p.messages.map(\.id), ["M-59", "M-60"])
        XCTAssertFalse(p.hasMore)
        p = try store.messages(c.id, before: "M-5")
        XCTAssertEqual(p.messages.map(\.id), ["M-1", "M-2", "M-3", "M-4"])
        XCTAssertFalse(p.hasMore)
        p = try store.messages(c.id, limit: 500)
        XCTAssertEqual(p.messages.count, 60)
        p = try store.messages(c.id, since: "2000-01-01T00:00:00Z", limit: 3)
        XCTAssertEqual(p.messages.map(\.id), ["M-1", "M-2", "M-3"])
        status({ _ = try store.messages(c.id, since: "昨天") }, 400)
    }

    /// 綁 Issue:Issue 要存在;同一個 Issue 只能有一個頻道:看得到就回傳既有的,看不到 409;Issue 詳情的「開對話頻道」
    func testIssueBinding() throws {
        let (app, store) = setup()
        let issues = app.startIssues()
        let issue = issues.create(title: "首頁變慢", assignee: "marketing", by: "tim")
        status({ try store.create(name: "x", issueId: "ISS-99") }, 404)

        let opened = try store.openForIssue(issue)
        XCTAssertEqual(opened.channel.issueId, issue.id)
        XCTAssertEqual(opened.name, "首頁變慢")
        XCTAssertEqual(opened.channel.members, ["user:tim", "agent:marketing"], "成員含自己與指派的角色")
        XCTAssertEqual(try store.openForIssue(issue).id, opened.id, "已有就回傳那個")
        let again = try store.create(name: "另一個", issueId: issue.id.lowercased())
        XCTAssertFalse(again.created)
        XCTAssertEqual(again.channel.id, opened.id)
        XCTAssertEqual(try store.list(issue: issue.id).map(\.id), [opened.id])
        XCTAssertEqual(app.channels.count, 1)

        // 看不到的人(不是成員、不是管理員)→ 409,說明頻道 id
        XCTAssertThrowsError(try store.create(name: "y", issueId: issue.id, as: "user:alice@example.com")) { e in
            XCTAssertEqual((e as? ChannelError)?.status, 409)
            XCTAssertTrue(e.localizedDescription.contains(opened.id), e.localizedDescription)
        }
    }

    /// 刪角色時從所有頻道成員拿掉
    func testDeleteAgentPrunesMembers() throws {
        let (app, store) = setup()
        let a = try store.create(name: "a", members: ["marketing", "narrator"]).channel
        let b = try store.create(name: "b", members: ["marketing"]).channel
        app.startAgents().delete("marketing")
        XCTAssertEqual(try store.channel(a.id).channel.members, ["user:tim", "agent:narrator"])
        XCTAssertEqual(try store.channel(b.id).channel.members, ["user:tim"])
        XCTAssertEqual(try store.list(as: "agent:marketing").count, 0)
        // 寫回 channels.json
        let app2 = AppState()
        app2.startTestAgents()
        XCTAssertEqual(try app2.startChannels().channel(a.id).channel.members, ["user:tim", "agent:narrator"])
    }

    /// MCP:ops_channel_create、ops_channel_post(agent 署名、要是成員)、ops_channels、ops_channel_messages(讀了算已讀)、ops_agent_context 的 mentions
    func testMCPTools() async throws {
        let (app, store) = setup()
        let mcp = MCPHandler(app: app)
        for n in ["ops_channels", "ops_channel_create", "ops_channel_messages", "ops_channel_post"] {
            XCTAssertTrue(MCPHandler.tools.contains { $0.name == n }, n)
        }

        var (o, text, err) = try await call(mcp, "ops_channel_create", ["name": "上線", "topic": "週五上線", "members": ["行銷", "narrator"]])
        XCTAssertFalse(err, text)
        XCTAssertEqual(o["id"] as? String, "CH-1")
        XCTAssertEqual(o["created"] as? Bool, true)
        XCTAssertEqual((o["members"] as? [[String: Any]])?.map { $0["name"] as? String ?? "" }, ["tim", "行銷", "旁白製作"].map { app.person($0)?.name ?? $0 })
        (_, text, err) = try await call(mcp, "ops_channel_create", ["name": "x", "members": ["nobody"]])
        XCTAssertTrue(err)
        XCTAssertTrue(text.contains("找不到角色"), text)

        (o, text, err) = try await call(mcp, "ops_channel_post", ["channel": "上線", "text": "@行銷 文案好了嗎?"])
        XCTAssertFalse(err, text)
        XCTAssertEqual(o["author"] as? String, "user:tim")
        XCTAssertEqual(o["mentions"] as? [String], ["agent:marketing"])
        (_, text, err) = try await call(mcp, "ops_channel_post", ["channel": "CH-1", "text": "我不是成員", "agent": "course-dev"])
        XCTAssertTrue(err, "角色要是頻道成員")
        (_, text, err) = try await call(mcp, "ops_channel_post", ["channel": "CH-1", "text": "x", "agent": "nobody"])
        XCTAssertTrue(err)
        XCTAssertTrue(text.contains("找不到角色"), text)

        // 角色執行時:ops_agent_context 看得到提到它的未讀訊息
        (o, text, err) = try await call(mcp, "ops_agent_context", ["agent": "marketing"])
        XCTAssertFalse(err, text)
        let mentions = try XCTUnwrap(o["mentions"] as? [String: Any])
        let msgs = try XCTUnwrap(mentions["messages"] as? [[String: Any]])
        XCTAssertEqual(msgs.count, 1)
        XCTAssertEqual(msgs.first?["channelName"] as? String, "上線")
        XCTAssertEqual(msgs.first?["authorName"] as? String, app.person("tim")?.name ?? "tim")
        XCTAssertEqual((mentions["channels"] as? [[String: Any]])?.first?["unread"] as? Int, 1)

        (o, text, err) = try await call(mcp, "ops_channels", ["agent": "marketing"])
        XCTAssertFalse(err, text)
        XCTAssertEqual((o["channels"] as? [[String: Any]])?.first?["unread"] as? Int, 1)
        (o, _, _) = try await call(mcp, "ops_channels", ["agent": "course-dev"])
        XCTAssertEqual((o["channels"] as? [[String: Any]])?.count, 0)

        // 讀了算已讀;以角色身分回覆
        (o, text, err) = try await call(mcp, "ops_channel_messages", ["channel": "上線", "agent": "marketing"])
        XCTAssertFalse(err, text)
        XCTAssertEqual((o["channel"] as? [String: Any])?["id"] as? String, "CH-1")
        XCTAssertEqual((o["messages"] as? [[String: Any]])?.map { $0["id"] as? String ?? "" }, ["M-1"])
        XCTAssertEqual(o["hasMore"] as? Bool, false)
        XCTAssertEqual(try store.inbox(for: "agent:marketing").mentions.count, 0)
        (o, text, err) = try await call(mcp, "ops_channel_post", ["channel": "CH-1", "text": "好了,@tim 請看", "agent": "行銷"])
        XCTAssertFalse(err, text)
        XCTAssertEqual(o["author"] as? String, "agent:marketing")
        XCTAssertEqual(o["authorName"] as? String, "行銷")
        XCTAssertEqual(app.channels.first?.unread, 1)
        (o, _, _) = try await call(mcp, "ops_channel_messages", ["channel": "CH-1", "since": "M-1"])
        XCTAssertEqual((o["messages"] as? [[String: Any]])?.map { $0["id"] as? String ?? "" }, ["M-2"])
        XCTAssertEqual(app.channels.first?.unread, 0)

        // 綁 Issue:重複建立回傳既有的
        let issue = app.startIssues().create(title: "bug", by: "tim")
        (o, _, _) = try await call(mcp, "ops_channel_create", ["name": "bug", "issue": issue.id])
        XCTAssertEqual(o["created"] as? Bool, true)
        let id = o["id"] as? String
        (o, _, _) = try await call(mcp, "ops_channel_create", ["name": "bug 2", "issue": issue.id])
        XCTAssertEqual(o["created"] as? Bool, false)
        XCTAssertEqual(o["id"] as? String, id)
        (o, _, _) = try await call(mcp, "ops_channels", ["issue": issue.id])
        XCTAssertEqual((o["channels"] as? [[String: Any]])?.map { $0["id"] as? String ?? "" }, [id ?? ""])
    }
}
