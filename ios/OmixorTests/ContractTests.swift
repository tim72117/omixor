import XCTest
@testable import Omixor

// 跨語言契約:core/model/testdata/contract/*.json 是 core API 實際回傳的形狀(由 core/model/contract_test.go 產生)。
// 假伺服器把範例檔包成 {"result": …},走 APIClient 實際的方法(unwrap + APIClient.decoder)解成 Models.swift 的型別,
// 斷言 iOS 用到的欄位都讀到、值正確。Go 的 model 改了欄位、範例檔重生後,這裡會失敗。
// 模擬器的 App 可以讀 Mac 上的檔案,所以直接用 #filePath 推出 repo 裡的範例檔。

final class ContractTests: XCTestCase {
    static let dir: URL = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()   // OmixorTests
        .deletingLastPathComponent()   // ios
        .deletingLastPathComponent()   // repo
        .appendingPathComponent("core/model/testdata/contract")

    static func fixture(_ name: String) throws -> String {
        let u = dir.appendingPathComponent(name)
        guard let d = FileManager.default.contents(atPath: u.path) else {
            throw NSError(domain: "contract", code: 1, userInfo: [NSLocalizedDescriptionKey: "讀不到契約範例檔 \(u.path)"])
        }
        return String(decoding: d, as: UTF8.self)
    }

    /// 「方法 路徑」→ 範例檔
    let routes: [String: String] = [
        "GET /api/orgs": "org_list.json",
        "GET /api/orgs/acme/agents": "agent_list.json",
        "GET /api/orgs/acme/channels": "channel_list.json",
        "GET /api/orgs/acme/channels/CH-2/messages": "channel_messages.json",
        "POST /api/orgs/acme/channels/CH-2/messages": "channel_message.json",
        "POST /api/orgs/acme/channels/CH-2/read": "channel_read.json",
        "GET /api/orgs/acme/issues": "issue_list.json",
        "GET /api/orgs/acme/issues/ISS-12": "issue.json",
        "POST /api/orgs/acme/issues/ISS-12/comments": "issue.json",
        "GET /api/orgs/acme/agent-tasks": "agent_task_list.json",
        "POST /api/orgs/acme/agent-tasks/T-7/requeue": "agent_task.json",
    ]

    override func setUp() {
        let routes = self.routes
        StubProtocol.reset { req, _ in
            let key = "\(req.httpMethod ?? "GET") \(req.url!.path)"
            guard let name = routes[key], let body = try? ContractTests.fixture(name) else {
                return .init(status: 404, body: #"{"error":"契約測試沒有登記這個路由"}"#)
            }
            return .init(status: 200, body: #"{"result":"# + body + "}")
        }
    }

    func api() -> APIClient { APIClient(baseURL: URL(string: "https://omixor.example.com")!, token: "omx_t", session: StubProtocol.session()) }

    private func date(_ s: String) -> Date { ISO8601DateFormatter().date(from: s)! }

    func testFixturesReadable() throws {
        for name in Set(routes.values) { XCTAssertNoThrow(try Self.fixture(name), name) }
    }

    func testOrgs() async throws {
        let list = try await api().orgs()
        XCTAssertEqual(list.map(\.id), ["acme", "side_2"])
        XCTAssertEqual(list[0].name, "Acme 研發")
        XCTAssertEqual(list[0].symbol, "building.2")
        XCTAssertEqual(list[0].color, "teal")
        XCTAssertEqual(list[0].note, "主要組織")
        XCTAssertTrue(list[0].isAdmin)
        XCTAssertEqual(list[1].myRole, "member")
        XCTAssertEqual(list[1].symbol, "", "沒設的選填欄位省略")
        let one = try APIClient.decoder.decode(Org.self, from: Data(try Self.fixture("org.json").utf8))
        XCTAssertEqual(one.id, "acme")
    }

    func testAgents() async throws {
        let list = try await api().agents(org: "acme")
        XCTAssertEqual(list.map(\.id), ["eng", "pm"])
        XCTAssertEqual(list[0].name, "工程師")
        XCTAssertEqual(list[0].role, "寫程式、修 bug")
        XCTAssertEqual(list[0].symbol, "hammer")
        XCTAssertEqual(list[0].color, "teal")
        XCTAssertTrue(list[0].active)
        XCTAssertFalse(list[1].active)
    }

    func testChannels() async throws {
        let list = try await api().channels(org: "acme")
        XCTAssertEqual(list.map(\.id), ["CH-2", "CH-1"])
        let c = list[0]
        XCTAssertEqual(c.name, "登入頁改版")
        XCTAssertEqual(c.topic, "討論 Safari 白畫面")
        XCTAssertEqual(c.issueId, "ISS-12")
        XCTAssertEqual(c.members, ["user:tim@example.com", "agent:eng"])
        XCTAssertEqual(c.unread, 3)
        XCTAssertEqual(c.lastMessageAt, date("2026-11-03T08:00:00Z"))
        XCTAssertEqual(c.updatedAt, date("2026-11-02T10:00:00Z"))
        XCTAssertNil(list[1].lastMessageAt)

        let page = try await api().messages(org: "acme", channel: "CH-2")
        XCTAssertTrue(page.hasMore)
        XCTAssertEqual(page.messages.map(\.id), ["M-4", "M-5"])
        let m = page.messages[1]
        XCTAssertEqual(m.channelId, "CH-2")
        XCTAssertEqual(m.author, "user:tim@example.com")
        XCTAssertEqual(m.text, "@工程師 可以看一下 **ISS-12** 嗎?")
        XCTAssertEqual(m.mentions, ["agent:eng"])
        XCTAssertEqual(m.at, date("2026-11-03T08:00:00Z"))
        XCTAssertEqual(m.number, 5)

        let posted = try await api().postMessage(org: "acme", channel: "CH-2", text: "x")
        XCTAssertEqual(posted, m)
        try await api().markRead(org: "acme", channel: "CH-2")
    }

    func testIssues() async throws {
        let list = try await api().issues(org: "acme")
        XCTAssertEqual(list.map(\.id), ["ISS-12", "ISS-3"])
        let i = list[0]
        XCTAssertEqual(i.title, "登入頁在 Safari 白畫面")
        XCTAssertTrue(i.body.hasPrefix("## 重現步驟"))
        XCTAssertEqual(i.status, "closed")
        XCTAssertFalse(i.isOpen)
        XCTAssertEqual(i.priority, "high")
        XCTAssertEqual(i.priorityLabel, "高")
        XCTAssertEqual(i.labels, ["bug", "前端"])
        XCTAssertEqual(i.assignee, "eng")
        XCTAssertEqual(i.createdBy, "tim@example.com")
        XCTAssertEqual(i.createdAt, date("2026-11-01T08:30:00Z"))
        XCTAssertEqual(i.updatedAt, date("2026-11-03T10:00:05Z"))
        XCTAssertEqual(i.number, 12)
        XCTAssertEqual(i.comments.map(\.id), ["5f0c6c1e-7a52-4d0b-9a3e-2a1c2b3d4e5f", "jira-10023"])
        XCTAssertEqual(i.comments.map(\.author), ["tim@example.com", "agent:eng"])
        XCTAssertEqual(i.comments[1].text, "已修正,見 PR #42")
        XCTAssertEqual(i.comments[1].at, date("2026-11-03T09:59:00Z"))
        XCTAssertEqual(list[1].assignee, "")
        XCTAssertTrue(list[1].isOpen)

        let one = try await api().issue(org: "acme", id: "ISS-12")
        XCTAssertEqual(one, i)
        let commented = try await api().addComment(org: "acme", issue: "ISS-12", text: "x", author: "tim@example.com")
        XCTAssertEqual(commented.comments.count, 2)
    }

    func testTasks() async throws {
        let list = try await api().tasks(org: "acme")
        XCTAssertEqual(list.map(\.id), ["T-7", "T-8"])
        let t = list[0]
        XCTAssertEqual(t.agentId, "eng")
        XCTAssertEqual(t.title, "修 ISS-12")
        XCTAssertEqual(t.instructions, "看 **ISS-12**,修好後回報")
        XCTAssertEqual(t.priority, "high")
        XCTAssertEqual(t.status, .inProgress)
        XCTAssertEqual(t.result, "已修正")
        XCTAssertEqual(t.links, ["ISS-12", "https://github.com/acme/web/pull/42"])
        XCTAssertEqual(t.createdBy, "tim@example.com")
        XCTAssertEqual(t.createdAt, date("2026-11-02T00:00:00Z"))
        XCTAssertEqual(t.updatedAt, date("2026-11-02T01:00:00Z"))
        XCTAssertEqual(t.claimedAt, date("2026-11-02T00:50:00Z"))
        XCTAssertEqual(t.claimedBy, "tim@example.com")
        XCTAssertEqual(t.completedAt, date("2026-11-02T02:00:00Z"))
        XCTAssertEqual(t.lockedUntil, date("2026-11-02T01:50:00Z"))
        XCTAssertEqual(t.notes.map(\.id), ["n-1", "8d6a4e0b-1c2d-4e5f-8a9b-0c1d2e3f4a5b"])
        XCTAssertEqual(t.notes[0].author, "agent:eng")
        XCTAssertEqual(t.notes[0].at, date("2026-11-02T00:51:00Z"))

        let q = list[1]
        XCTAssertEqual(q.status, .queued)
        XCTAssertNil(q.lockedUntil)
        XCTAssertEqual(q.claimedBy, "")

        let requeued = try await api().requeue(org: "acme", task: "T-7")
        XCTAssertEqual(requeued, t)
    }

    /// iOS 目前沒有讀這些資源,但解碼型別共用:確認領取結果裡的 task 也讀得懂
    func testClaimTaskShape() throws {
        struct Claim: Decodable { var agent: Agent?; var task: AgentTask? }
        let c = try APIClient.decoder.decode(Claim.self, from: Data(try Self.fixture("agent_task_claim.json").utf8))
        XCTAssertEqual(c.agent?.id, "eng")
        XCTAssertEqual(c.task?.id, "T-7")
        let empty = try APIClient.decoder.decode(Claim.self, from: Data(try Self.fixture("agent_task_claim_empty.json").utf8))
        XCTAssertNil(empty.task)
    }
}
