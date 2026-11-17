import XCTest
@testable import Ops

// 寬鬆解碼(Core/LossyDecoding.swift):core(Go)送來 null 陣列、不認得的列舉、列表裡有一筆壞掉時,App 照樣讀得出其他資料。

/// 假的 core server:依「方法 路徑」回登記的原始 JSON(包成 {"result": …})
final class LossyFakeServer: URLProtocol {
    nonisolated(unsafe) static var routes: [String: String] = [:]

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let key = "\(request.httpMethod ?? "GET") \(request.url!.path)"
        var status = 200
        var body: Data
        if let r = Self.routes[key] {
            body = Data(#"{"result":\#(r)}"#.utf8)
        } else {
            status = 404
            body = Data(#"{"error":"沒有登記:\#(key)"}"#.utf8)
        }
        let resp = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }
}

final class LossyDecodingTests: XCTestCase {
    let org = "acme"
    var remote: IssueRemote!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        LossyFakeServer.routes = [:]
        IssueRemote.protocolClasses = [LossyFakeServer.self]
        remote = IssueRemote(config: IssueServerConfig(url: URL(string: "http://127.0.0.1:5997")!, token: "tok-lossy"), scope: "lossy")
    }

    override func tearDown() async throws {
        IssueRemote.protocolClasses = []
        LossyFakeServer.routes = [:]
        remote = nil
    }

    private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
        try IssueRemote.decoder().decode(type, from: Data(json.utf8))
    }

    // MARK: LossyList 本身

    func testLossyListSkipsBadElementsAndNull() throws {
        struct Item: Decodable, Equatable { var id: String }
        let l = try decode(LossyList<Item>.self, #"[{"id":"a"},{"x":1},null,"oops",{"id":"b"}]"#)
        XCTAssertEqual(l.elements.map(\.id), ["a", "b"])
        XCTAssertEqual(try decode(LossyList<Item>.self, "null").elements, [])
        XCTAssertEqual(try decode(LossyList<Item>.self, "[]").elements, [])
    }

    // MARK: Issue

    func testIssueNullArraysAndUnknownEnums() throws {
        let i = try decode(OpsIssue.self, #"""
        {"id":"ISS-5","title":"t","labels":null,"comments":null,"status":"wontfix","priority":"p0",
         "createdAt":"2026-11-01T08:30:00Z"}
        """#)
        XCTAssertEqual(i.id, "ISS-5")
        XCTAssertEqual(i.labels, [])
        XCTAssertEqual(i.comments, [])
        XCTAssertEqual(i.status, .open)        // 同 web 的 normIssue
        XCTAssertEqual(i.priority, .normal)
        XCTAssertEqual(i.body, "")
        XCTAssertEqual(i.createdBy, "")
        XCTAssertNil(i.repo)
        XCTAssertEqual(i.updatedAt, i.createdAt)

        // 認得的值照讀;留言 id 缺時用固定的 UUID(重讀一樣)
        let j = #"{"id":"ISS-6","status":"closed","priority":"urgent","comments":[{"author":"a","text":"hi","at":"2026-11-01T08:30:00Z"},{"bad":true}]}"#
        let a = try decode(OpsIssue.self, j), b = try decode(OpsIssue.self, j)
        XCTAssertEqual(a.status, .closed)
        XCTAssertEqual(a.priority, .urgent)
        XCTAssertEqual(a.comments.map(\.text), ["hi", ""])
        XCTAssertEqual(a.comments.map(\.id), b.comments.map(\.id))
    }

    func testIssueMissingIdThrows() {
        XCTAssertThrowsError(try decode(OpsIssue.self, #"{"title":"沒有 id"}"#))
    }

    func testIssueListSkipsBrokenOne() throws {
        LossyFakeServer.routes["GET /api/orgs/acme/issues"] = #"""
        [{"id":"ISS-1","title":"好的","labels":null,"comments":null,"status":"open","priority":"high"},
         {"title":"沒有 id 的壞資料"},
         {"id":"ISS-3","title":"未知狀態","status":"triage","priority":"???","labels":["x"]}]
        """#
        let list = try remote.list(org: org)
        XCTAssertEqual(list.map(\.id), ["ISS-1", "ISS-3"])
        XCTAssertEqual(list[0].priority, .high)
        XCTAssertEqual(list[1].status, .open)
        XCTAssertEqual(list[1].priority, .normal)
        XCTAssertEqual(list[1].labels, ["x"])

        // 整份是 null(Go 的 nil slice):空列表
        LossyFakeServer.routes["GET /api/orgs/acme/issues"] = "null"
        XCTAssertEqual(try remote.list(org: org), [])
    }

    func testBoardSkipsBrokenIssueAndColumn() throws {
        LossyFakeServer.routes["GET /api/orgs/acme/issues/board"] = #"""
        {"columns":[
           {"key":"todo","title":"待處理","dropStatus":"open","issues":[
              {"id":"ISS-1","title":"好的","labels":null,"comments":null,"status":"open"},
              {"title":"沒有 id 的壞資料"},
              {"id":"ISS-2","status":"blocked","priority":"p0"}]},
           {"key":"doing","title":"進行中","dropStatus":"reopened","issues":null},
           {"title":"沒有 key 的欄"},
           {"key":"done","dropStatus":"closed","issues":[{"id":"ISS-9","status":"closed"}]}],
         "labels":null,"total":5}
        """#
        let b = try remote.board(org: org, IssueQuery())
        XCTAssertEqual(b.columns.map(\.key), ["todo", "doing", "done"])
        XCTAssertEqual(b.columns[0].issues.map(\.id), ["ISS-1", "ISS-2"])
        XCTAssertEqual(b.columns[0].issues[1].status, .open)
        XCTAssertEqual(b.columns[0].issues[1].priority, .normal)
        XCTAssertEqual(b.columns[0].issues[0].labels, [])
        XCTAssertEqual(b.columns[1].issues, [])
        XCTAssertNil(b.columns[1].dropStatus)          // 不認得的狀態 → 不改
        XCTAssertEqual(b.columns[2].title, "done")
        XCTAssertEqual(b.columns[2].dropStatus, .closed)
        XCTAssertEqual(b.labels, [])
        XCTAssertEqual(b.total, 5)
        XCTAssertEqual(b.matched, 3)                    // 缺時用各欄的 Issue 數
        XCTAssertEqual(b.column(of: "ISS-9")?.key, "done")

        LossyFakeServer.routes["GET /api/orgs/acme/issues/board"] = #"{"columns":null,"labels":null}"#
        let e = try remote.board(org: org, IssueQuery())
        XCTAssertEqual(e.columns.count, 0)
        XCTAssertEqual(e.total, 0)
    }

    // MARK: 虛擬角色與例行任務

    func testRoutineUnknownKindAndPriority() throws {
        let r = try decode(AgentRoutine.self, #"{"id":"r1","title":"週報","kind":"monthly","priority":"critical","hour":7}"#)
        XCTAssertEqual(r.kind, .daily)
        XCTAssertEqual(r.priority, .normal)
        XCTAssertEqual(r.hour, 7)
        XCTAssertEqual(r.minute, 0)
        XCTAssertTrue(r.enabled)
        XCTAssertNil(r.lastRunAt)
    }

    func testAgentNullRoutinesAndBadEntries() throws {
        let a = try decode(Agent.self, #"{"id":"eng","name":"工程師","routines":null,"docAccess":null}"#)
        XCTAssertEqual(a.routines, [])
        XCTAssertEqual(a.docAccess, [:])

        let b = try decode(Agent.self, #"""
        {"id":"pm","name":"PM","routines":[{"id":"r1","title":"ok","kind":"weekly"},{"title":"沒有 id"}],
         "docAccess":{"docs":"write","levels":"admin"}}
        """#)
        XCTAssertEqual(b.routines.map(\.id), ["r1"])
        XCTAssertEqual(b.routines[0].kind, .weekly)
        XCTAssertEqual(b.docAccess, ["docs": .write])   // 不認得的權限略過
    }

    func testAgentAndTaskListsSkipBrokenOne() throws {
        LossyFakeServer.routes["GET /api/orgs/acme/agents"] = #"""
        [{"id":"eng","name":"工程師","routines":[{"id":"r1","kind":"yearly","priority":"zzz"}]},{"name":"沒有 id"}]
        """#
        let agents = try remote.listAgents(org: org)
        XCTAssertEqual(agents.map(\.id), ["eng"])
        XCTAssertEqual(agents[0].routines.first?.kind, .daily)

        LossyFakeServer.routes["GET /api/orgs/acme/agent-tasks"] = #"""
        [{"id":"T-1","agentId":"eng","status":"paused","priority":"zzz","links":null,"notes":null},{"agentId":"沒有 id"}]
        """#
        let tasks = try remote.listTasks(org: org)
        XCTAssertEqual(tasks.map(\.id), ["T-1"])
        XCTAssertEqual(tasks[0].status, .queued)
        XCTAssertEqual(tasks[0].priority, .normal)
        XCTAssertEqual(tasks[0].links, [])
        XCTAssertEqual(tasks[0].notes, [])
    }

    // MARK: 文件櫃

    func testCabinetSectionNullTabsAndBadTab() throws {
        let s = try decode(CabinetSection.self, #"{"id":"docs","label":"文件","tabs":null}"#)
        XCTAssertEqual(s.tabs, [])
        XCTAssertEqual(s.path, "")

        let t = try decode(CabinetSection.self, #"{"id":"levels","tabs":[{"path":"/a","code":"L0"},{"label":"沒有 path"}]}"#)
        XCTAssertEqual(t.label, "levels")
        XCTAssertEqual(t.tabs.map(\.path), ["/a"])
        XCTAssertEqual(t.tabs[0].label, "/a")
        XCTAssertEqual(t.tabs[0].code, "L0")
    }

    func testRemoteCabinetSkipsBrokenDoc() throws {
        LossyFakeServer.routes["GET /api/orgs/acme/cabinet"] = #"""
        {"layout":[{"id":"docs","label":"文件","tabs":null},{"label":"沒有 id"}],
         "docs":[{"id":"DOC-1","title":"a","section":"docs","tags":null},{"id":"DOC-2","title":"沒有 section"}]}
        """#
        let c = try remote.getCabinet(org: org)
        XCTAssertEqual(c.layout.map(\.id), ["docs"])
        XCTAssertEqual(c.docs.map(\.id), ["DOC-1"])
        XCTAssertEqual(c.docs[0].tags, [])
        XCTAssertNil(c.layoutSaved)

        LossyFakeServer.routes["GET /api/orgs/acme/cabinet"] = #"{"layout":null,"docs":null}"#
        let e = try remote.getCabinet(org: org)
        XCTAssertEqual(e.layout, [])
        XCTAssertEqual(e.docs, [])
    }

    // MARK: 頻道

    func testChannelReadMissingFields() throws {
        let r = try decode(ChannelRead.self, #"{"channelId":"CH-1"}"#)
        XCTAssertEqual(r.channelId, "CH-1")
        XCTAssertEqual(r.member, "")
        XCTAssertEqual(r.lastRead, 0)
        XCTAssertEqual(r.at, .distantPast)
    }

    func testChannelListAndPageAreLossy() throws {
        LossyFakeServer.routes["GET /api/orgs/acme/channels"] = #"""
        [{"id":"CH-1","name":"一般","members":null,"unread":2},{"name":"沒有 id"}]
        """#
        let list = try remote.listChannels(org: org, issue: nil)
        XCTAssertEqual(list.map(\.id), ["CH-1"])
        XCTAssertEqual(list[0].channel.members, [])
        XCTAssertEqual(list[0].unread, 2)

        LossyFakeServer.routes["GET /api/orgs/acme/channels/CH-1/messages"] = #"""
        {"messages":[{"id":"M-1","text":"hi","mentions":null},{"text":"沒有 id"}],"hasMore":true}
        """#
        let page = try remote.channelMessages(org: org, id: "CH-1", since: nil, before: nil, limit: nil)
        XCTAssertEqual(page.messages.map(\.id), ["M-1"])
        XCTAssertEqual(page.messages[0].mentions, [])
        XCTAssertTrue(page.hasMore)

        LossyFakeServer.routes["GET /api/orgs/acme/channels/inbox"] = #"{"mentions":[{"id":"M-1","text":"@eng"},{"text":"沒有 id"}],"channels":null}"#
        let inbox = try remote.channelInbox(org: org, for: "agent:eng")
        XCTAssertEqual(inbox.mentions.map(\.id), ["M-1"])
        XCTAssertEqual(inbox.channels.count, 0)
    }

    // MARK: 事件

    func testIncidentListIsLossy() throws {
        LossyFakeServer.routes["GET /api/orgs/acme/incidents"] = #"""
        [{"id":"INC-1","title":"掛了","status":"exploded","services":null,"timeline":null,"followUps":null,"alertIds":null},
         {"title":"沒有 id"},
         {"id":"INC-2","status":"resolved","timeline":[{"kind":"weird","text":"x"},{"kind":"note","text":"y"}]}]
        """#
        let list = try remote.listIncidents(org: org)
        XCTAssertEqual(list.map(\.incident.id), ["INC-1", "INC-2"])
        XCTAssertEqual(list[0].incident.status, .investigating)
        XCTAssertEqual(list[0].incident.services, [])
        XCTAssertEqual(list[0].incident.timeline.count, 0)
        XCTAssertEqual(list[0].alertIds, [])
        XCTAssertEqual(list[1].incident.status, .resolved)
        XCTAssertEqual(list[1].incident.timeline.map(\.kind), [.note, .note])
    }

    // MARK: 組織與組織鏈

    func testServerOrgOmittedFieldsAndBrokenOne() throws {
        // core 的 Org 沒值時省略 symbol、color、note
        let o = try JSONDecoder().decode(ServerOrg.self, from: Data(#"{"id":"acme","name":"Acme","createdAt":"2026-11-01T08:30:00Z"}"#.utf8))
        XCTAssertNil(o.symbol)
        XCTAssertNil(o.color)
        XCTAssertNil(o.note)
        let org = o.organization(server: URL(string: "http://127.0.0.1:5997")!)
        XCTAssertEqual(org.symbol, "building.2")
        XCTAssertEqual(org.color, "blue")
        XCTAssertEqual(org.note, "")

        LossyFakeServer.routes["GET /api/orgs"] = #"[{"id":"acme","myRole":"admin"},{"name":"沒有 id"}]"#
        XCTAssertEqual(try remote.myRole(org: "acme"), "admin")
    }

    func testOrgChainNullEdges() throws {
        XCTAssertEqual(try decode(OrgChain.self, #"{"edges":null}"#).edges, [])
        let c = try decode(OrgChain.self, #"{"edges":[{"from":"agent:a","to":"agent:b"},"bad"]}"#)
        XCTAssertEqual(c.edges.map(\.id), ["agent:a→agent:b"])
    }
}
