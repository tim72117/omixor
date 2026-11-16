import XCTest
@testable import Ops

// 跨語言契約:core/model/testdata/contract/*.json 是 core API 實際回傳的形狀(由 core/model/contract_test.go 產生)。
// 假伺服器把範例檔包成 {"result": …} 回給 App,走 App 從 server 讀資源實際用的方法(IssueRemote 的 list、getChannel、
// listIncidents、claimTask…),斷言關鍵欄位都讀到、值正確。Go 的 model 改了欄位、範例檔重生後,這裡會失敗。

/// 契約範例檔所在的資料夾(由這個檔案的位置推出 repo 根目錄)
enum ContractFixtures {
    static let dir: URL = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()   // OmixorTests
        .deletingLastPathComponent()   // Tests
        .deletingLastPathComponent()   // app
        .deletingLastPathComponent()   // repo
        .appendingPathComponent("core/model/testdata/contract")

    static func data(_ name: String) throws -> Data {
        let u = dir.appendingPathComponent(name)
        guard let d = FileManager.default.contents(atPath: u.path) else {
            throw NSError(domain: "contract", code: 1, userInfo: [NSLocalizedDescriptionKey: "讀不到契約範例檔 \(u.path)"])
        }
        return d
    }
}

/// 假的 core server:依「方法 路徑」回對應的範例檔(包成 {"result": …},同 core/api 的 reply)
final class FakeContractServer: URLProtocol {
    nonisolated(unsafe) static var routes: [String: String] = [:]
    nonisolated(unsafe) static var requests: [String] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let key = "\(request.httpMethod ?? "GET") \(request.url!.path)"
        Self.requests.append(key)
        var status = 200
        var body: Data
        if let name = Self.routes[key], let d = try? ContractFixtures.data(name) {
            body = Data(#"{"result":"#.utf8) + d + Data("}".utf8)
        } else {
            status = 404
            body = Data(#"{"error":"契約測試沒有登記這個路由:\#(key)"}"#.utf8)
        }
        let resp = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }
}

final class ContractTests: XCTestCase {
    let org = "acme"
    var remote: IssueRemote!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        FakeContractServer.routes = [
            "GET /api/orgs": "org_list.json",
            "GET /api/orgs/acme/issues": "issue_list.json",
            "GET /api/orgs/acme/issues/ISS-12": "issue.json",
            "POST /api/orgs/acme/issues": "issue.json",
            "POST /api/orgs/acme/issues/ISS-12/comments": "issue.json",
            "GET /api/orgs/acme/agents": "agent_list.json",
            "POST /api/orgs/acme/agents": "agent.json",
            "GET /api/orgs/acme/agent-tasks": "agent_task_list.json",
            "POST /api/orgs/acme/agent-tasks/T-7/report": "agent_task.json",
            "POST /api/orgs/acme/agent-tasks/T-7/claim": "agent_task_claim.json",
            "POST /api/orgs/acme/agent-tasks/claim": "agent_task_claim_empty.json",
            "GET /api/orgs/acme/channels": "channel_list.json",
            "GET /api/orgs/acme/channels/CH-2": "channel.json",
            "GET /api/orgs/acme/channels/CH-2/messages": "channel_messages.json",
            "POST /api/orgs/acme/channels/CH-2/messages": "channel_message.json",
            "POST /api/orgs/acme/channels/CH-2/read": "channel_read.json",
            "GET /api/orgs/acme/channels/inbox": "channel_inbox.json",
            "GET /api/orgs/acme/incidents": "incident_list.json",
            "GET /api/orgs/acme/incidents/INC-2041": "incident.json",
            "POST /api/orgs/acme/incidents": "incident.json",
            "GET /api/orgs/acme/cabinet": "cabinet.json",
            "POST /api/orgs/acme/docs": "cabinet_doc.json",
            "GET /api/orgs/acme/chain": "org_chain.json",
        ]
        FakeContractServer.requests = []
        IssueRemote.protocolClasses = [FakeContractServer.self]
        remote = IssueRemote(config: IssueServerConfig(url: URL(string: "http://127.0.0.1:5996")!, token: "tok-contract"), scope: "contract")
    }

    override func tearDown() async throws {
        IssueRemote.protocolClasses = []
        remote = nil
    }

    private func date(_ s: String) -> Date { ISO8601DateFormatter().date(from: s)! }

    func testFixturesExist() throws {
        for name in Set(FakeContractServer.routes.values) {
            XCTAssertNoThrow(try ContractFixtures.data(name), name)
        }
    }

    // MARK: Issue(IssueRemote.list / create / addComment → OpsIssue)

    func testIssues() throws {
        let list = try remote.list(org: org)
        XCTAssertEqual(list.map(\.id), ["ISS-12", "ISS-3"])
        let i = list[0]
        XCTAssertEqual(i.title, "登入頁在 Safari 白畫面")
        XCTAssertTrue(i.body.hasPrefix("## 重現步驟"))
        XCTAssertEqual(i.status, .closed)
        XCTAssertEqual(i.priority, .high)
        XCTAssertEqual(i.labels, ["bug", "前端"])
        XCTAssertEqual(i.source, "網站訪客 7d")
        XCTAssertEqual(i.link, "https://example.com/login")
        XCTAssertEqual(i.repo, "github:acme/web")
        XCTAssertEqual(i.assignedAgent, "eng")
        XCTAssertEqual(i.createdBy, "tim@example.com")
        XCTAssertEqual(i.createdAt, date("2026-11-01T08:30:00Z"))
        XCTAssertEqual(i.updatedAt, date("2026-11-03T10:00:05Z"))
        XCTAssertEqual(i.closedAt, date("2026-11-03T10:00:05Z"))
        XCTAssertEqual(i.number, 12)
        XCTAssertEqual(i.comments.map(\.author), ["tim@example.com", "agent:eng"])
        XCTAssertEqual(i.comments.map(\.text), ["我這邊也遇到", "已修正,見 PR #42"])
        XCTAssertEqual(i.comments[0].id, UUID(uuidString: "5f0c6c1e-7a52-4d0b-9a3e-2a1c2b3d4e5f"))
        XCTAssertEqual(i.comments[1].at, date("2026-11-03T09:59:00Z"))

        // 只有必填欄位的:repo、assignee、closedAt 省略
        let m = list[1]
        XCTAssertEqual(m.status, .open)
        XCTAssertNil(m.repoTag)
        XCTAssertNil(m.assignedAgent)
        XCTAssertNil(m.closedAt)
        XCTAssertEqual(m.comments, [])

        let created = try remote.create(org: org, title: "x", body: "", priority: .high, labels: [], source: "", link: "", author: "tim")
        XCTAssertEqual(created.id, "ISS-12")
        XCTAssertEqual(created.comments.count, 2)
        let commented = try remote.addComment(org: org, id: "ISS-12", text: "hi", author: "tim")
        XCTAssertEqual(commented.comments.last?.author, "agent:eng")
    }

    // MARK: 虛擬角色與任務(listAgents / saveAgent / listTasks / reportTask / claimTask)

    func testAgents() throws {
        let agents = try remote.listAgents(org: org)
        XCTAssertEqual(agents.map(\.id), ["eng", "pm"])
        let a = agents[0]
        XCTAssertEqual(a.name, "工程師")
        XCTAssertEqual(a.role, "寫程式、修 bug")
        XCTAssertEqual(a.symbol, "hammer")
        XCTAssertEqual(a.color, "teal")
        XCTAssertTrue(a.active)
        XCTAssertEqual(a.instructions, "你是工程師。**先寫測試**再改程式。")
        XCTAssertEqual(a.createdAt, date("2026-10-01T00:00:00Z"))
        XCTAssertEqual(a.docAccess, ["docs": .write, "/studio/tools": .read, "overview": DocAccess.none])
        XCTAssertEqual(a.routines.count, 2)
        let r = a.routines[0]
        XCTAssertEqual(r.id, "r-daily")
        XCTAssertEqual(r.title, "每日站會摘要")
        XCTAssertEqual(r.instructions, "整理昨天的進度")
        XCTAssertEqual(r.kind, .daily)
        XCTAssertEqual(r.hour, 9)
        XCTAssertEqual(r.minute, 30)
        XCTAssertEqual(r.weekday, 2)
        XCTAssertEqual(r.everyHours, 6)
        XCTAssertEqual(r.priority, .normal)
        XCTAssertTrue(r.enabled)
        XCTAssertEqual(r.lastRunAt, date("2026-11-14T01:30:00Z"))
        XCTAssertEqual(a.routines[1].kind, .hourly)
        XCTAssertFalse(a.routines[1].enabled)
        XCTAssertNil(a.routines[1].lastRunAt)
        XCTAssertFalse(agents[1].active)
        XCTAssertEqual(agents[1].routines, [])

        let saved = try remote.saveAgent(org: org, a)
        XCTAssertEqual(saved, a)
    }

    func testTasks() throws {
        let tasks = try remote.listTasks(org: org)
        XCTAssertEqual(tasks.map(\.id), ["T-7", "T-8"])
        let t = tasks[0]
        XCTAssertEqual(t.agentId, "eng")
        XCTAssertEqual(t.title, "修 ISS-12")
        XCTAssertEqual(t.instructions, "看 **ISS-12**,修好後回報")
        XCTAssertEqual(t.status, .inProgress)
        XCTAssertEqual(t.priority, .high)
        XCTAssertEqual(t.createdBy, "tim@example.com")
        XCTAssertEqual(t.routineId, "r-daily")
        XCTAssertEqual(t.createdAt, date("2026-11-02T00:00:00Z"))
        XCTAssertEqual(t.updatedAt, date("2026-11-02T01:00:00Z"))
        XCTAssertEqual(t.claimedAt, date("2026-11-02T00:50:00Z"))
        XCTAssertEqual(t.claimedBy, "tim@example.com")
        XCTAssertEqual(t.lockedUntil, date("2026-11-02T01:50:00Z"))
        XCTAssertEqual(t.completedAt, date("2026-11-02T02:00:00Z"))
        XCTAssertEqual(t.result, "已修正")
        XCTAssertEqual(t.links, ["ISS-12", "https://github.com/acme/web/pull/42"])
        XCTAssertEqual(t.issueId, "ISS-12")
        XCTAssertEqual(t.notes.map(\.author), ["agent:eng", "omixor"])
        XCTAssertEqual(t.notes[0].text, "開始處理")
        XCTAssertEqual(t.notes[0].at, date("2026-11-02T00:51:00Z"))
        XCTAssertEqual(t.notes[1].id, UUID(uuidString: "8d6a4e0b-1c2d-4e5f-8a9b-0c1d2e3f4a5b"))
        XCTAssertNotNil(t.leaseLabel)

        let q = tasks[1]
        XCTAssertEqual(q.status, .queued)
        XCTAssertNil(q.routineId)
        XCTAssertNil(q.lockedUntil)
        XCTAssertNil(q.claimedBy)
        XCTAssertEqual(q.links, [])

        let reported = try remote.reportTask(org: org, id: "T-7", status: .done, result: "ok", note: nil, links: nil)
        XCTAssertEqual(reported.id, "T-7")
    }

    func testClaim() throws {
        let c = try remote.claimTask(org: org, agent: "eng", task: "T-7")
        XCTAssertEqual(c.agent?.id, "eng")
        XCTAssertEqual(c.task?.id, "T-7")
        XCTAssertEqual(c.task?.status, .inProgress)
        let cred = try XCTUnwrap(c.credential)
        XCTAssertEqual(cred.agentId, "eng")
        XCTAssertEqual(cred.taskId, "T-7")
        XCTAssertEqual(cred.secret, "s3cr3t-example")
        XCTAssertEqual(cred.expiresAt, "2026-11-02T01:50:00Z")
        XCTAssertEqual(cred.endpoint, "http://127.0.0.1:5996/mcp/agent", "core 沒給 endpoint 時用 server 的角色端點")

        // 沒有可領的任務:task 是 null、沒有 credential
        let empty = try remote.claimTask(org: org, agent: "eng", task: nil)
        XCTAssertEqual(empty.agent?.id, "eng")
        XCTAssertNil(empty.task)
        XCTAssertNil(empty.credential)
    }

    // MARK: 頻道

    func testChannels() throws {
        let list = try remote.listChannels(org: org, issue: nil)
        XCTAssertEqual(list.map(\.id), ["CH-2", "CH-1"])
        let s = list[0]
        XCTAssertEqual(s.channel.name, "登入頁改版")
        XCTAssertEqual(s.channel.topic, "討論 Safari 白畫面")
        XCTAssertEqual(s.channel.issueId, "ISS-12")
        XCTAssertTrue(s.channel.hasIssue)
        XCTAssertEqual(s.channel.members, ["user:tim@example.com", "agent:eng"])
        XCTAssertEqual(s.channel.createdBy, "user:tim@example.com")
        XCTAssertEqual(s.channel.createdAt, date("2026-11-01T10:00:00Z"))
        XCTAssertEqual(s.channel.updatedAt, date("2026-11-02T10:00:00Z"))
        XCTAssertEqual(s.unread, 3)
        XCTAssertEqual(s.lastMessageAt, date("2026-11-03T08:00:00Z"))
        XCTAssertNil(list[1].lastMessageAt, "沒訊息時 lastMessageAt 是 null")
        XCTAssertEqual(list[1].unread, 0)
        XCTAssertFalse(list[1].channel.hasIssue)

        XCTAssertEqual(try remote.getChannel(org: org, id: "CH-2"), s)

        let page = try remote.channelMessages(org: org, id: "CH-2", since: nil, before: nil, limit: nil)
        XCTAssertTrue(page.hasMore)
        XCTAssertEqual(page.messages.map(\.id), ["M-4", "M-5"])
        let m = page.messages[1]
        XCTAssertEqual(m.channelId, "CH-2")
        XCTAssertEqual(m.author, "user:tim@example.com")
        XCTAssertEqual(m.text, "@工程師 可以看一下 **ISS-12** 嗎?")
        XCTAssertEqual(m.mentions, ["agent:eng"])
        XCTAssertEqual(m.at, date("2026-11-03T08:00:00Z"))
        XCTAssertEqual(m.number, 5)

        XCTAssertEqual(try remote.postChannelMessage(org: org, id: "CH-2", text: "x", author: nil), m)

        let read = try remote.markChannelRead(org: org, id: "CH-2", upTo: nil, member: nil)
        XCTAssertEqual(read.channelId, "CH-2")
        XCTAssertEqual(read.member, "user:tim@example.com")
        XCTAssertEqual(read.lastRead, 5)
        XCTAssertEqual(read.at, date("2026-11-03T08:01:00Z"))

        let inbox = try remote.channelInbox(org: org, for: "agent:eng")
        XCTAssertEqual(inbox.mentions.count, 1)
        XCTAssertEqual(inbox.mentions[0].channelId, "CH-2")
        XCTAssertEqual(inbox.mentions[0].channelName, "登入頁改版")
        XCTAssertEqual(inbox.mentions[0].id, "M-5")
        XCTAssertEqual(inbox.mentions[0].author, "user:tim@example.com")
        XCTAssertEqual(inbox.mentions[0].at, date("2026-11-03T08:00:00Z"))
        XCTAssertEqual(inbox.channels.map(\.unread), [3])
        XCTAssertEqual(inbox.channels.map(\.name), ["登入頁改版"])
    }

    // MARK: 事件(listIncidents / declareIncident → ServerIncident)

    func testIncidents() throws {
        let list = try remote.listIncidents(org: org)
        XCTAssertEqual(list.map(\.incident.id), ["INC-2041", "INC-2042"])
        let s = list[0]
        let i = s.incident
        XCTAssertEqual(i.title, "API 延遲飆高")
        XCTAssertEqual(i.sev, 2)
        XCTAssertEqual(i.status, .resolved)
        XCTAssertEqual(i.services, ["api", "db"])
        XCTAssertEqual(i.commander, "tim@example.com")
        XCTAssertEqual(i.startedAt, date("2026-11-05T03:00:00Z"))
        XCTAssertEqual(i.resolvedAt, date("2026-11-05T04:30:00Z"))
        XCTAssertEqual(i.impact, "約 20% 請求逾時")
        XCTAssertEqual(i.aiSummary, "資料庫連線池耗盡,調大後恢復")
        XCTAssertEqual(i.followUps, ["ISS-13", "WI-7"])
        XCTAssertEqual(s.alertIds, ["alert-cpu-1"])
        XCTAssertEqual(i.timeline.count, 3)
        XCTAssertEqual(i.timeline.map(\.kind), [.alert, .status, .aiFinding])
        XCTAssertEqual(i.timeline.map(\.actor), [.system, .person("tim@example.com"), .ai("mcp")])
        XCTAssertEqual(i.timeline[0].id, UUID(uuidString: "0b8f2a8e-3c4d-4e5f-9a0b-1c2d3e4f5a6b"))
        XCTAssertEqual(i.timeline[0].date, date("2026-11-05T03:00:00Z"))
        XCTAssertEqual(i.timeline[0].text, "p95 延遲 > 2s")
        XCTAssertEqual(i.timeline[2].id, IncidentJSON.uuid("evt-legacy-1"), "不是 UUID 的 id 換成固定的 UUID")

        let m = list[1].incident
        XCTAssertEqual(m.status, .investigating)
        XCTAssertNil(m.resolvedAt)
        XCTAssertNil(m.aiSummary)
        XCTAssertEqual(list[1].alertIds, [])

        XCTAssertEqual(try remote.declareIncident(org: org, ["title": "x", "sev": 2]).incident, i)
    }

    // MARK: 文件櫃(getCabinet → RemoteCabinet、createDoc → CabinetDoc)

    func testCabinet() throws {
        let c = try remote.getCabinet(org: org)
        XCTAssertEqual(c.layoutSaved, true)
        XCTAssertEqual(c.layout.map(\.id), ["overview", "levels"])
        XCTAssertEqual(c.layout[0].path, "/studio")
        XCTAssertEqual(c.layout[0].tabs, [])
        let tabs = c.layout[1].tabs
        XCTAssertEqual(tabs.map(\.path), ["/studio/tools", "/studio/ai-tips"])
        XCTAssertEqual(tabs[0].code, "L0")
        XCTAssertEqual(tabs[0].group, "做出自己的軟體")
        XCTAssertEqual(tabs[0].title, "L0 AI 工具介紹")
        XCTAssertNil(tabs[1].code)
        XCTAssertNil(tabs[1].group)
        XCTAssertEqual(c.docs.map(\.id), ["DOC-4", "DOC-1"])
        let d = c.docs[0]
        XCTAssertEqual(d.title, "AI 工具介紹")
        XCTAssertEqual(d.body, "# AI 工具\n\n- Claude\n- Codex")
        XCTAssertEqual(d.section, "levels")
        XCTAssertEqual(d.tab, "/studio/tools")
        XCTAssertEqual(d.tags, ["課程", "L0"])
        XCTAssertEqual(d.author, "agent:pm")
        XCTAssertEqual(d.createdAt, date("2026-10-10T00:00:00Z"))
        XCTAssertEqual(d.updatedAt, date("2026-10-11T00:00:00Z"))
        XCTAssertNil(c.docs[1].tab, "放在區塊本身的文件沒有 tab")
        XCTAssertEqual(c.docs[1].body, "")

        let created = try remote.createDoc(org: org, title: "x", body: "", section: "levels", tab: nil, tags: [], author: "tim")
        XCTAssertEqual(created, d)
    }

    // MARK: 組織鏈與組織

    func testChainAndOrgs() throws {
        let chain = try remote.getChain(org: org)
        XCTAssertEqual(chain.edges.map(\.from), ["user:tim@example.com", "agent:pm"])
        XCTAssertEqual(chain.edges.map(\.to), ["agent:pm", "agent:eng"])
        XCTAssertEqual(chain.edges.map(\.labelText), ["目標", "任務"])
        XCTAssertEqual(chain.updatedAt, date("2026-11-01T00:00:00Z"))

        // GET /api/orgs:組織鏈頁用 IssueRemote 讀 myRole
        XCTAssertEqual(try remote.myRole(org: org), "admin")
        XCTAssertEqual(try remote.myRole(org: "side_2"), "member")

        // 登入後的組織清單(OmixorAuth.listOrgs)用預設的 JSONDecoder 解 ServerOrg,再轉成 App 的組織
        let server = URL(string: "https://omixor.example.com")!
        let list = try JSONDecoder().decode([ServerOrg].self, from: try ContractFixtures.data("org_list.json"))
        XCTAssertEqual(list.map(\.id), ["acme", "side_2"])
        XCTAssertEqual(list[0].myRole, "admin")
        let o = list[0].organization(server: server)
        XCTAssertEqual(o.name, "Acme 研發")
        XCTAssertEqual(o.symbol, "building.2")
        XCTAssertEqual(o.color, "teal")
        XCTAssertEqual(o.note, "主要組織")
        XCTAssertEqual(o.createdAt, date("2026-09-01T00:00:00Z"))
        let side = list[1].organization(server: server)
        XCTAssertEqual(side.symbol, "building.2", "沒設圖示用預設")
        XCTAssertEqual(side.note, "")

        let one = try JSONDecoder().decode(ServerOrg.self, from: try ContractFixtures.data("org.json"))
        XCTAssertEqual(one.id, "acme")
        XCTAssertEqual(one.createdAt, "2026-09-01T00:00:00Z")
    }
}
