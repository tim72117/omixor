import XCTest
@testable import Omixor

final class APIClientTests: XCTestCase {
    func api() -> APIClient { APIClient(baseURL: URL(string: "https://omixor.example.com")!, token: "omx_t", session: StubProtocol.session()) }

    func testUnwrap() throws {
        XCTAssertEqual(String(decoding: try APIClient.unwrap(Data(#"{"result":[1,2]}"#.utf8), status: 200), as: UTF8.self), "[1,2]")
        XCTAssertThrowsError(try APIClient.unwrap(Data(#"{"error":"沒有權限"}"#.utf8), status: 403)) { e in
            XCTAssertEqual(e as? APIError, APIError(message: "沒有權限", status: 403))
        }
        XCTAssertThrowsError(try APIClient.unwrap(Data("<html>".utf8), status: 502))
        // 登入與 /api/auth/me 沒有 result:整份給呼叫的人
        XCTAssertEqual(String(decoding: try APIClient.unwrap(Data(#"{"user":{}}"#.utf8), status: 200), as: UTF8.self), #"{"user":{}}"#)
    }

    func testPathsAndQuery() async throws {
        StubProtocol.reset { req, _ in
            XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer omx_t")
            return .init(status: 200, body: #"{"result":{"messages":[{"id":"M-3","author":"agent:pm","text":"**hi**","mentions":["user:tim@x"],"at":"2026-11-13T01:02:03Z"}],"hasMore":true}}"#)
        }
        let p = try await api().messages(org: "o 1", channel: "CH-1", since: "M-2", limit: 200)
        XCTAssertEqual(p.messages.first?.number, 3)
        XCTAssertTrue(p.hasMore)
        let u = StubProtocol.requests[0].0.url!
        XCTAssertEqual(u.path, "/api/orgs/o 1/channels/CH-1/messages")
        XCTAssertTrue(u.absoluteString.contains("/api/orgs/o%201/"))
        XCTAssertEqual(URLComponents(url: u, resolvingAgainstBaseURL: false)?.queryItems?.sorted { $0.name < $1.name }.map { "\($0.name)=\($0.value ?? "")" },
                       ["limit=200", "since=M-2"], "空的 before 不送")
    }

    /// Issue 的篩選、搜尋、排序在 core:清單只把條件送出去(規則與搜尋語意的測試在 core/model/issue_query_test.go)
    func testIssueQueryParams() async throws {
        StubProtocol.reset { _, _ in .init(status: 200, body: #"{"result":[{"id":"ISS-2","title":"x"}]}"#) }
        let l = try await api().issues(org: "o1", query: "safari  a+b", status: "open", sort: "status")
        XCTAssertEqual(l.map(\.id), ["ISS-2"])
        let u = StubProtocol.requests[0].0.url!
        XCTAssertEqual(u.path, "/api/orgs/o1/issues")
        XCTAssertEqual(URLComponents(url: u, resolvingAgainstBaseURL: false)?.queryItems?.map { "\($0.name)=\($0.value ?? "")" },
                       ["q=safari  a+b", "sort=status", "status=open"])
        XCTAssertTrue(u.absoluteString.contains("a%2Bb"), "+ 要編碼,不然伺服器當成空白:\(u.absoluteString)")
        _ = try await api().issues(org: "o1")
        XCTAssertNil(StubProtocol.requests[1].0.url!.query, "不給條件 = 舊的呼叫(全部)")
    }

    func testAddCommentPostsOnlyNewComment() async throws {
        StubProtocol.reset { req, body in
            let b = json(body)
            XCTAssertEqual(b["text"] as? String, "新的")
            XCTAssertNil(b["comments"], "不整份送回留言")
            return .init(status: 200, body: #"{"result":{"id":"ISS-1","title":"x","comments":[{"id":"c1","author":"a@x","text":"舊的"},{"id":"c2","author":"tim@x","text":"新的"}]}}"#)
        }
        let i = try await api().addComment(org: "o1", issue: "ISS-1", text: "新的", author: "tim@x")
        XCTAssertEqual(i.comments.map(\.text), ["舊的", "新的"])
        XCTAssertEqual(StubProtocol.requests.map { $0.0.httpMethod ?? "" }, ["POST"], "只有一次往返")
        XCTAssertEqual(StubProtocol.requests[0].0.url?.path, "/api/orgs/o1/issues/ISS-1/comments")
    }

    func testDecodeGoJSON() throws {
        // core 的實際格式(model.Time 是 RFC3339、沒值的選填欄位省略)
        let task = #"{"agentId":"pm","createdAt":"2026-11-13T00:00:00Z","createdBy":"tim@x","id":"T-7","instructions":"做 **A**","links":["ISS-1"],"lockedUntil":"2026-11-13T01:00:00Z","notes":[{"at":"2026-11-13T00:10:00Z","author":"agent:pm","id":"n1","text":"開始"}],"priority":"high","result":"","status":"inProgress","title":"任務","updatedAt":"2026-11-13T00:10:00Z"}"#
        let t = try APIClient.decoder.decode(AgentTask.self, from: Data(task.utf8))
        XCTAssertEqual(t.status, .inProgress)
        XCTAssertEqual(t.notes.count, 1)
        XCTAssertNotNil(t.lockedUntil)
        let ch = #"[{"id":"CH-1","name":"改版","topic":"","issueId":"","members":["user:tim@x","agent:pm"],"createdBy":"user:tim@x","createdAt":"2026-11-13T00:00:00Z","updatedAt":"2026-11-13T00:00:00Z","unread":3,"lastMessageAt":null}]"#
        let cs = try APIClient.decoder.decode([ChannelSummary].self, from: Data(ch.utf8))
        XCTAssertEqual(cs.first?.unread, 3)
        XCTAssertNil(cs.first?.lastMessageAt)
    }
}
