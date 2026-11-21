import XCTest
@testable import Omixor

/// Issue 的識別:key(顯示、會隨換專案改變)與內部 id(uuid,穩定)
@MainActor
final class IssueIdentityTests: XCTestCase {
    private let uuid = "019a3f2c-8e40-7b1a-9c2d-3e4f5a6b7c8d"

    func testStableIdAndSameness() {
        var a = Issue(id: "ISS-3", uuid: uuid, title: "x", createdBy: "tim")
        XCTAssertEqual(a.stableId, uuid)
        var moved = a
        moved.id = "APP-1"   // 換了專案,key 改了
        XCTAssertTrue(a.isSame(as: moved), "內部 id 相同就是同一筆")
        XCTAssertTrue(moved.matches(uuid.uppercased()))
        XCTAssertFalse(moved.matches("ISS-3"), "App 端只認目前的 key(舊 key 由 server 解析)")
        a.uuid = ""
        XCTAssertEqual(a.stableId, "ISS-3", "舊版 server 沒有內部 id 時用 key")
        XCTAssertTrue(a.isSame(as: Issue(id: "iss-3", title: "y", createdBy: "tim")))
        XCTAssertFalse(a.isSame(as: moved))
    }

    func testLooksLikeKeyAndTaskIssueId() {
        for k in ["ISS-1", "iss-12", "APP-12", "WEB2-3", "AB-1"] { XCTAssertTrue(Issue.looksLikeKey(k), k) }
        for k in ["T-3", "CH-2", "M-5", "DOC-1", "INC-4", "PRJ-2", "A-1", "APP-0", "APP-", "APP-x", "1AB-2",
                  "https://x.com/APP-12", "ABCDEFGHIJK-1", uuid, ""] {
            XCTAssertFalse(Issue.looksLikeKey(k), k)
        }
        let t = AgentTask(id: "T-9", title: "x", agentId: "marketing", createdBy: "tim",
                          links: ["CH-1", "M-4", "T-2", "https://example.com/a-1", "APP-12", "ISS-3"])
        XCTAssertEqual(t.issueId, "APP-12")

        // 有 Issue 清單:內部 id 也認得,回傳目前的 key
        let issues = [Issue(id: "APP-7", uuid: uuid, title: "x", createdBy: "tim")]
        let byUUID = AgentTask(id: "T-1", title: "x", agentId: "a", createdBy: "tim", links: ["CH-1", uuid])
        XCTAssertNil(byUUID.issueId)
        XCTAssertEqual(byUUID.issueKey(in: issues), "APP-7")
        XCTAssertEqual(t.issueKey(in: issues), "APP-12", "對不到清單退回格式判斷")
    }

    func testFocusByUUIDSelectsIssue() {
        let app = AppState()
        app.issues = [Issue(id: "APP-7", uuid: uuid, title: "x", createdBy: "tim")]
        let m = DevModel()
        XCTAssertTrue(m.apply(focus: uuid, app: app), "頻道的 issueId 是內部 id")
        XCTAssertEqual(m.tab, .issues)
        XCTAssertEqual(m.selectedIssue, uuid)
        m.tab = .overview
        XCTAssertTrue(m.apply(focus: "app-7", app: app))
        XCTAssertEqual(m.selectedIssue, uuid, "選取一律存內部 id")
    }

    func testUpdatePayloadOmitsServerFields() throws {
        let i = Issue(id: "APP-7", uuid: uuid, serial: 7, title: "x", createdBy: "tim", assignee: "marketing")
        let d = try IssueRemote.updatePayload(i, author: "tim", sendAssignee: false)
        for k in ["id", "uuid", "serial", "createdAt", "createdBy", "updatedAt", "comments", "assignee"] {
            XCTAssertNil(d[k], k)
        }
        XCTAssertEqual(d["title"] as? String, "x")
        XCTAssertEqual(d["author"] as? String, "tim")
        XCTAssertEqual(try IssueRemote.updatePayload(i, author: "tim", sendAssignee: true)["assignee"] as? String, "marketing")
    }
}
