import XCTest
@testable import Omixor

/// 開發 › 看板與 Issue 列表:分欄、排序、篩選、搜尋的規則只在 core(core/model/issue_query_test.go 測規則本身)。
/// 這裡測 App 這一側:query 參數怎麼送、看板回應怎麼解碼、欄的 dropStatus 怎麼決定拖曳、欄 key 對應的顯示,
/// 以及連真的 core(TestCore)時拿到的結果
final class DevBoardTests: XCTestCase {
    override func setUp() async throws {
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
    }

    private func issue(_ n: Int, _ status: IssueStatus = .open, assignee: String? = nil) -> Issue {
        Issue(id: "ISS-\(n)", title: "Issue \(n)", status: status, createdBy: "me", assignee: assignee)
    }

    func testQueryItems() {
        XCTAssertEqual(IssueQuery().queryItems, [], "空的條件不帶參數(同舊的 GET …/issues)")
        let q = IssueQuery(q: "登入 a+b", status: "open", priority: "high", label: "前端", repo: IssueQuery.none, assignee: "eng", sort: "status")
        XCTAssertEqual(q.queryItems.map(\.name), ["q", "status", "priority", "label", "repo", "assignee", "sort"])
        let u = IssueRemote.withQuery(URL(string: "http://x/api/orgs/o/issues")!, q.queryItems)
        let s = u.absoluteString
        XCTAssertTrue(s.contains("q=%E7%99%BB%E5%85%A5%20a%2Bb"), "空白與 + 都要編碼(Go 會把 + 當空白):\(s)")
        XCTAssertTrue(s.contains("repo=(none)") || s.contains("repo=%28none%29"), s)
        XCTAssertEqual(IssueRemote.withQuery(URL(string: "http://x/a")!, []).absoluteString, "http://x/a")
        XCTAssertEqual(IssueRepoOptions.none, "(none)", "未標記倉庫的值同 core 的 model.IssueFilterNone")
    }

    /// 看板的倉庫篩選有帶進查詢(repo 參數;未標記 = (none)),status、sort 不送
    func testBoardQueryCarriesRepo() {
        XCTAssertEqual(DevBoardTab.query(q: "", priority: nil, label: nil, repo: "").queryItems, [], "沒篩選不帶參數")
        let q = DevBoardTab.query(q: "登入", priority: .high, label: "bug", repo: "github:acme/web")
        XCTAssertEqual(q.repo, "github:acme/web")
        XCTAssertEqual(q.queryItems, [URLQueryItem(name: "q", value: "登入"), URLQueryItem(name: "priority", value: "high"),
                                      URLQueryItem(name: "label", value: "bug"), URLQueryItem(name: "repo", value: "github:acme/web")])
        let none = DevBoardTab.query(q: "", priority: nil, label: nil, repo: IssueRepoOptions.none)
        XCTAssertEqual(none.queryItems, [URLQueryItem(name: "repo", value: "(none)")])
        let u = IssueRemote.withQuery(URL(string: "http://x/api/orgs/o/issues/board")!, none.queryItems).absoluteString
        XCTAssertTrue(u.contains("repo=(none)") || u.contains("repo=%28none%29"), u)
    }

    func testDecodeBoard() throws {
        let json = """
        {"columns":[
          {"key":"todo","title":"待處理","dropStatus":"open","issues":[{"id":"ISS-2","title":"a","body":"","status":"open","priority":"urgent",
            "labels":["bug"],"source":"","link":"","createdBy":"mcp","createdAt":"2026-11-01T00:00:00Z","updatedAt":"2026-11-02T00:00:00Z",
            "comments":[{"id":"jira-1","author":"x","text":"留言","at":"2026-11-01T00:00:00Z"}]}]},
          {"key":"doing","title":"進行中","dropStatus":"open","issues":[]},
          {"key":"done","title":"已完成","dropStatus":null,"issues":[]}
        ],"labels":["bug"],"total":5,"matched":1}
        """
        let r = IssueRemote(config: IssueServerConfig(url: URL(string: "http://127.0.0.1:1")!, token: "t"), scope: "test")
        let b = try r.decodeBoard(Data(json.utf8))
        XCTAssertEqual(b.columns.map(\.key), ["todo", "doing", "done"])
        XCTAssertEqual(b.columns.map(\.title), ["待處理", "進行中", "已完成"])
        XCTAssertEqual(b.columns[0].dropStatus, .open)
        XCTAssertNil(b.columns[2].dropStatus)
        XCTAssertEqual(b.columns[0].issues.first?.priority, .urgent)
        XCTAssertNotNil(b.columns[0].issues.first?.comments.first, "不是 UUID 的留言 id 也解得出來(換成固定的 UUID)")
        XCTAssertEqual(b.labels, ["bug"])
        XCTAssertEqual(b.total, 5)
        XCTAssertEqual(b.matched, 1)
        XCTAssertEqual(b.column(of: "ISS-2")?.key, "todo")
        XCTAssertNil(b.column(of: "ISS-9"))
    }

    /// 拖曳只看欄的 dropStatus:和 Issue 現在的狀態不同才改
    func testDropStatus() {
        let open = IssueBoardResult.Column(key: "doing", title: "進行中", dropStatus: .open, issues: [])
        let done = IssueBoardResult.Column(key: "done", title: "已完成", dropStatus: .closed, issues: [])
        let none = IssueBoardResult.Column(key: "x", title: "x", dropStatus: nil, issues: [])
        XCTAssertEqual(done.status(forDropping: issue(1)), .closed, "拖到已完成 = 關閉")
        XCTAssertEqual(open.status(forDropping: issue(2, .closed)), .open, "從已完成拖出 = 重新打開")
        XCTAssertNil(open.status(forDropping: issue(3)), "待處理 → 進行中:狀態都是 open,不動作")
        XCTAssertNil(done.status(forDropping: issue(4, .closed)))
        XCTAssertNil(none.status(forDropping: issue(5)))
    }

    /// 拖曳游標:能放(移動)只看欄的 dropStatus 和 Issue 現在的狀態;原欄、待處理 ↔ 進行中 不能放
    func testDropCheck() {
        let b = IssueBoardResult(columns: [
            .init(key: "todo", title: "待處理", dropStatus: .open, issues: [issue(1)]),
            .init(key: "doing", title: "進行中", dropStatus: .open, issues: [issue(2, assignee: "a")]),
            .init(key: "done", title: "已完成", dropStatus: .closed, issues: [issue(3, .closed)]),
        ], labels: [], total: 3, matched: 3)
        let (todo, doing, done) = (b.columns[0], b.columns[1], b.columns[2])
        let id1 = issue(1).id, id2 = issue(2).id, id3 = issue(3).id
        XCTAssertEqual(b.dropCheck(dragging: id1, onto: done), .move(.closed), "拖到已完成 = 關閉")
        XCTAssertEqual(b.dropCheck(dragging: id2, onto: done), .move(.closed))
        XCTAssertEqual(b.dropCheck(dragging: id3, onto: todo), .move(.open), "從已完成拖出 = 重新打開")
        XCTAssertEqual(b.dropCheck(dragging: id3, onto: doing), .move(.open))
        XCTAssertEqual(b.dropCheck(dragging: id1, onto: doing), .blocked, "待處理 → 進行中:不能放、要提示")
        XCTAssertEqual(b.dropCheck(dragging: id2, onto: todo), .blocked)
        XCTAssertEqual(b.dropCheck(dragging: id1, onto: todo), .sameColumn, "拖回原欄:不能放、不提示")
        XCTAssertEqual(b.dropCheck(dragging: id3, onto: done), .sameColumn)
        XCTAssertEqual(b.dropCheck(dragging: "ISS-999", onto: done), .unknown)
        XCTAssertEqual(b.dropCheck(dragging: nil, onto: done), .unknown, "沒在拖曳")
        XCTAssertTrue(b.dropCheck(dragging: id1, onto: done).canDrop)
        XCTAssertFalse(b.dropCheck(dragging: id1, onto: doing).canDrop)
        XCTAssertFalse(b.dropCheck(dragging: id1, onto: todo).canDrop)
        XCTAssertFalse(b.dropCheck(dragging: nil, onto: todo).canDrop)
    }

    /// 欄 key → 圖示與顏色(顯示用;標題照 core 回傳)
    func testColumnLook() {
        XCTAssertEqual(IssueBoardColumn.look("todo"), .todo)
        XCTAssertEqual(IssueBoardColumn.look("doing").icon, "circle.lefthalf.filled")
        XCTAssertEqual(IssueBoardColumn.look("done").icon, "checkmark.circle.fill")
        XCTAssertEqual(IssueBoardColumn.look("新欄"), .todo, "不認得的 key 用預設樣式")
    }

    /// 連真的 core:看板與列表照 core 的規則回來(規則本身在 core 測)
    func testBoardAndQueryFromCore() throws {
        let cfg = try TestCore.start(self)
        let r = IssueRemote(config: cfg, scope: "test")
        let org = "default"
        _ = try r.create(org: org, title: "登入壞了", body: "Safari 白畫面", priority: .low, labels: ["Bug"], source: "", link: "",
                         repo: "github:acme/web", author: "tim")
        let b2 = try r.create(org: org, title: "首頁改版", body: "", priority: .urgent, labels: ["ui"], source: "", link: "", author: "tim")
        var b3 = try r.create(org: org, title: "API 逾時", body: "", priority: .high, labels: [], source: "", link: "", author: "tim")
        b3.status = .closed
        _ = try r.update(org: org, b3, author: "tim")
        _ = try r.addComment(org: org, id: b2.id, text: "手機版也要", author: "tim")

        let board = try r.board(org: org, IssueQuery())
        XCTAssertEqual(board.columns.map(\.key), ["todo", "doing", "done"])
        XCTAssertEqual(board.columns[0].issues.map(\.id), [b2.id, "ISS-1"], "待處理依優先度排")
        XCTAssertEqual(board.columns[2].issues.map(\.id), [b3.id])
        XCTAssertEqual(board.columns[2].dropStatus, .closed)
        XCTAssertEqual(board.labels, ["Bug", "ui"])
        XCTAssertEqual(board.total, 3)

        let filtered = try r.board(org: org, IssueQuery(q: "手機", status: "closed", sort: "updated"))
        XCTAssertEqual(filtered.matched, 1, "搜尋含留言;看板不看 status、sort")
        XCTAssertEqual(filtered.columns[0].issues.map(\.id), [b2.id])

        let byRepo = try r.board(org: org, IssueQuery(repo: "ACME/web"))
        XCTAssertEqual(byRepo.matched, 1, "看板依倉庫篩選(不分大小寫、可省略 github: 前綴)")
        XCTAssertEqual(byRepo.columns[0].issues.map(\.id), ["ISS-1"])
        let noRepo = try r.board(org: org, IssueQuery(repo: IssueQuery.none))
        XCTAssertEqual(noRepo.matched, 2, "未標記倉庫")
        XCTAssertEqual(noRepo.columns.flatMap(\.issues).map(\.id).sorted(), [b2.id, b3.id].sorted())

        XCTAssertEqual(try r.query(org: org, IssueQuery()).map(\.id), ["ISS-3", "ISS-2", "ISS-1"], "不帶參數同舊的列表(新的在前)")
        XCTAssertEqual(try r.query(org: org, IssueQuery(sort: "status")).map(\.id), ["ISS-2", "ISS-1", "ISS-3"], "未解決在前 → 優先度")
        XCTAssertEqual(try r.query(org: org, IssueQuery(q: "safari 登入")).map(\.id), ["ISS-1"], "多字 AND,跨標題與內文")
        XCTAssertEqual(try r.query(org: org, IssueQuery(repo: IssueQuery.none, sort: "status")).map(\.id), ["ISS-2", "ISS-3"])
        XCTAssertEqual(try r.query(org: org, IssueQuery(status: "open", repo: "ACME/web")).map(\.id), ["ISS-1"])
        XCTAssertThrowsError(try r.query(org: org, IssueQuery(sort: "nope"))) { e in
            XCTAssertEqual((e as? IssueRemoteError)?.status, 400)
        }
    }
}
