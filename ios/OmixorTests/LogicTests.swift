import XCTest
@testable import Omixor

final class LogicTests: XCTestCase {
    func testStuckReason() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        XCTAssertNotNil(AgentTask(id: "T-1", agentId: "a", title: "x", status: .blocked).stuckReason(now: now))
        XCTAssertNotNil(AgentTask(id: "T-2", agentId: "a", title: "x", status: .inProgress, lockedUntil: now.addingTimeInterval(-1)).stuckReason(now: now), "租約過期")
        XCTAssertNil(AgentTask(id: "T-3", agentId: "a", title: "x", status: .inProgress, lockedUntil: now.addingTimeInterval(60)).stuckReason(now: now))
        XCTAssertNotNil(AgentTask(id: "T-4", agentId: "a", title: "x", status: .queued, createdAt: now.addingTimeInterval(-7200)).stuckReason(now: now), "很久沒人領")
        XCTAssertNil(AgentTask(id: "T-5", agentId: "a", title: "x", status: .queued, createdAt: now.addingTimeInterval(-7200), updatedAt: now.addingTimeInterval(-60)).stuckReason(now: now), "剛重新排入")
        XCTAssertNil(AgentTask(id: "T-6", agentId: "a", title: "x", status: .done).stuckReason(now: now))
    }

    func testDispatchRows() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        let tasks = [
            AgentTask(id: "T-1", agentId: "a", title: "完成", status: .done, updatedAt: now),
            AgentTask(id: "T-2", agentId: "a", title: "進行中", status: .inProgress, updatedAt: now.addingTimeInterval(-10), lockedUntil: now.addingTimeInterval(60)),
            AgentTask(id: "T-3", agentId: "b", title: "卡住", status: .blocked, updatedAt: now.addingTimeInterval(-100)),
        ]
        XCTAssertEqual(DispatchStore.rows(tasks, scope: .open, agent: "", now: now).map(\.id), ["T-3", "T-2"], "卡住的最前面,完成的不顯示")
        XCTAssertEqual(DispatchStore.rows(tasks, scope: .all, agent: "", now: now).map(\.id), ["T-3", "T-2", "T-1"])
        XCTAssertEqual(DispatchStore.rows(tasks, scope: .all, agent: "a", now: now).map(\.id), ["T-2", "T-1"])
    }

    func testIssueFilter() {
        var a = Issue(id: "ISS-1", title: "登入壞了", labels: ["bug"])
        a.comments = [IssueComment(id: "c", author: "x", text: "Safari 才會")]
        let b = Issue(id: "ISS-2", title: "改版", status: "closed")
        XCTAssertEqual(IssuesStore.filter([a, b], query: "", status: .open).map(\.id), ["ISS-1"])
        XCTAssertEqual(IssuesStore.filter([a, b], query: "", status: .closed).map(\.id), ["ISS-2"])
        XCTAssertEqual(IssuesStore.filter([a, b], query: "safari BUG", status: .all).map(\.id), ["ISS-1"])
        XCTAssertEqual(IssuesStore.filter([a, b], query: "iss-2", status: .all).map(\.id), ["ISS-2"])
    }

    func testMergeMessages() {
        let d = Date()
        let m = { (n: Int) in ChannelMessage(id: "M-\(n)", author: "user:a", text: "\(n)", at: d) }
        XCTAssertEqual(ThreadStore.merge([m(2), m(3)], [m(3), m(10), m(1)]).map(\.id), ["M-1", "M-2", "M-3", "M-10"])
    }

    func testComposerEnterAndMention() {
        XCTAssertTrue(ChatComposer.shouldSubmit(replacement: "\n", composing: false))
        XCTAssertFalse(ChatComposer.shouldSubmit(replacement: "\n", composing: true), "注音選字中按 Enter 不送出")
        XCTAssertFalse(ChatComposer.shouldSubmit(replacement: "a", composing: false))
        XCTAssertEqual(Mention.insert("產品經理", into: ""), "@產品經理 ")
        XCTAssertEqual(Mention.insert("產品經理", into: "嗨"), "嗨 @產品經理 ")
        XCTAssertEqual(Mention.insert("pm", into: "嗨 @"), "嗨 @pm ")
    }

    func testMarkdownBlocks() {
        let b = MarkdownText.blocks("# 標題\n段落一\n第二行\n\n- [x] 完成\n1. 第一\n```\ncode\n```\n> 引用")
        XCTAssertEqual(b, [.heading(1, "標題"), .text("段落一\n第二行"), .bullet("☑", "完成"), .bullet("1.", "第一"), .code("code"), .quote("引用")])
    }

    @MainActor
    func testDirectoryNames() async {
        let d = Directory()
        XCTAssertEqual(d.name("agent:pm"), "pm")
        XCTAssertEqual(d.name("user:tim@x"), "tim@x")
        XCTAssertTrue(d.isAgent("agent:pm"))
    }
}
