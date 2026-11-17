import AppKit
import XCTest
@testable import Omixor

/// Issue 的倉庫標記(repo)與文件櫃的圖表區塊(wireframe / flowchart),對齊 core 的 model.CleanRepo、RepoMatch 與 core/tools 的渲染器
@MainActor
final class IssueRepoDiagramTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.omixor.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-repo-diagram-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OMIXOR_APP_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        unsetenv("OMIXOR_APP_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func call(_ mcp: MCPHandler, _ name: String, _ args: [String: Any]) async throws -> (Any?, String, Bool) {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        return (try? JSONSerialization.jsonObject(with: Data(text.utf8)), text, r["isError"] as? Bool ?? false)
    }

    // MARK: - 規則(同 core)

    func testRepoCleanAndMatch() {
        XCTAssertEqual(IssueRepo.clean("  tim72117/evoleap "), "tim72117/evoleap")
        XCTAssertEqual(IssueRepo.clean(""), "")
        XCTAssertEqual(IssueRepo.clean("gitlab:group/sub/proj-1_2.x"), "gitlab:group/sub/proj-1_2.x")
        XCTAssertNil(IssueRepo.clean("a b/c"), "中間有空白")
        XCTAssertNil(IssueRepo.clean("倉庫/名稱"), "只允許英數")
        XCTAssertNil(IssueRepo.clean("a?b"))
        XCTAssertEqual(IssueRepo.clean(String(repeating: "a", count: 200)), String(repeating: "a", count: 200))
        XCTAssertNil(IssueRepo.clean(String(repeating: "a", count: 201)), "最長 200")

        XCTAssertTrue(IssueRepo.matches("Tim72117/Evoleap", "tim72117/evoleap"), "不分大小寫")
        XCTAssertTrue(IssueRepo.matches("github:tim/x", "tim/x"), "一邊沒寫前綴就忽略")
        XCTAssertTrue(IssueRepo.matches("tim/x", "GitHub:tim/x"))
        XCTAssertFalse(IssueRepo.matches("github:tim/x", "gitlab:tim/x"), "兩邊都寫要相同")
        XCTAssertFalse(IssueRepo.matches("tim/x", "tim/y"))
        XCTAssertFalse(IssueRepo.matches("github:", "gitlab:"), "拿掉前綴是空的不算")
        XCTAssertFalse(IssueRepo.matches("", "tim/x"))

        let a = Issue(id: "ISS-1", title: "a", repo: "tim/x", createdBy: "tim")
        let b = Issue(id: "ISS-2", title: "b", createdBy: "tim")
        XCTAssertEqual(IssueRepo.filter([a, b], nil).count, 2)
        XCTAssertEqual(IssueRepo.filter([a, b], " ").count, 2, "空白 = 不篩")
        XCTAssertEqual(IssueRepo.filter([a, b], "github:TIM/x").map(\.id), ["ISS-1"])
        XCTAssertEqual(IssueRepoOptions.used([a, b, Issue(id: "ISS-3", title: "c", repo: "TIM/X", createdBy: "tim")]), ["tim/x"],
                       "用過的倉庫去重(不分大小寫)")
        XCTAssertNotEqual(IssueRepo.clean(IssueRepoOptions.none), IssueRepoOptions.none, "「未標記」的篩選值不會和存起來的倉庫撞到")
    }

    func testDiagramKinds() {
        XCTAssertEqual(DiagramKind.of(info: "wireframe", code: "{}"), .wireframe)
        XCTAssertEqual(DiagramKind.of(info: " Flowchart extra", code: "x"), .flowchart, "info string 取第一個字,不分大小寫")
        XCTAssertEqual(DiagramKind.of(info: "mermaid title=\"x\"", code: "%% 註解\n\nflowchart TD\n A-->B"), .flowchart)
        XCTAssertEqual(DiagramKind.of(info: "mermaid", code: "---\ntitle: x\n---\ngraph LR;A-->B"), .flowchart, "跳過 --- 前置區塊")
        XCTAssertEqual(DiagramKind.of(info: "mermaid", code: "graph"), .flowchart)
        XCTAssertNil(DiagramKind.of(info: "mermaid", code: "sequenceDiagram\n A->>B: hi"), "其他 Mermaid 圖型照原本顯示程式碼")
        XCTAssertNil(DiagramKind.of(info: "mermaid", code: "graphical"))
        XCTAssertNil(DiagramKind.of(info: "json", code: "{}"))
        XCTAssertNil(DiagramKind.of(info: "wireframes", code: "{}"))

        // 文件櫃的解析:圖表區塊還是 code 區塊,顯示時才換成圖
        let blocks = ReadingMarkdown.parse("說明\n\n```wireframe\n{\"root\":{}}\n```\n")
        XCTAssertTrue(blocks.contains(.code(lang: "wireframe", text: "{\"root\":{}}")))

        // server 的回應 → 渲染結果
        let ok = DiagramRender.from(toolResult: ["content": [["type": "text", "text": #"{"svg":"<svg/>","width":10,"height":20,"warnings":["w"]}"#]], "isError": false])
        XCTAssertEqual(ok, DiagramRender(svg: "<svg/>", width: 10, height: 20, warnings: ["w"]))
        let bad = DiagramRender.from(toolResult: ["content": [["type": "text", "text": "描述有錯"]], "isError": true])
        XCTAssertEqual(bad.error, "描述有錯")
        XCTAssertNil(DiagramRender.image("<html><script>alert(1)</script></html>"), "不是 SVG 就不當圖片")
    }

    func testSettingsReposPane() {
        let all = SettingsPane.allCases
        XCTAssertEqual(all.firstIndex(of: .repos), all.firstIndex(of: .integrations).map { $0 + 1 }, "程式碼倉庫在整合後面")
        XCTAssertEqual(SettingsPane(rawValue: "repos"), .repos, "其他頁用 focusId \"repos\" 跳過來")
        XCTAssertEqual(SettingsPane.repos.title, "程式碼倉庫")
    }

    // MARK: - 透過 core server

    func testIssueRepoThroughCore() async throws {
        let cfg = try TestCore.start(self)
        let app = AppState()
        let mcp = MCPHandler(app: app)

        var (obj, text, isError) = try await call(mcp, "issue_create", ["title": "登入頁跑版", "repo": " Tim72117/Evoleap "])
        XCTAssertFalse(isError, text)
        XCTAssertEqual((obj as? [String: Any])?["repo"] as? String, "Tim72117/Evoleap", "去頭尾空白")
        XCTAssertEqual(app.issues.first?.repo, "Tim72117/Evoleap")

        (_, text, isError) = try await call(mcp, "issue_create", ["title": "格式不對", "repo": "a b"])
        XCTAssertTrue(isError, "repo 不合格要回錯誤:\(text)")

        let store = app.startIssues()
        let second = store.create(title: "GitLab 那邊的", repo: "gitlab:group/sub/proj", by: app.me)
        XCTAssertEqual(second.repo, "gitlab:group/sub/proj")
        let none = store.create(title: "沒標記", by: app.me)
        XCTAssertNil(none.repo, "沒標記時 JSON 不帶 repo")

        (obj, text, isError) = try await call(mcp, "issues", ["repo": "github:tim72117/evoleap", "status": "all"])
        XCTAssertFalse(isError, text)
        let rows = try XCTUnwrap((obj as? [String: Any])?["issues"] as? [[String: Any]])
        XCTAssertEqual(rows.map { $0["repo"] as? String }, ["Tim72117/Evoleap"], "篩選規則同 RepoMatch")

        // REST ?repo=
        let remote = IssueRemote(config: cfg)
        XCTAssertEqual(try remote.list(org: app.currentOrgId, repo: "group/sub/proj").map(\.id), [second.id])
        XCTAssertEqual(try remote.list(org: app.currentOrgId).count, 3)

        // 畫面的修改:換倉庫、清除
        var u = try XCTUnwrap(store.update(none.id, by: app.me) { $0.repo = "tim/other" })
        XCTAssertEqual(u.repo, "tim/other")
        u = try XCTUnwrap(store.update(none.id, by: app.me) { $0.repo = nil })
        XCTAssertNil(u.repo, "清除標記")
        u = try XCTUnwrap(store.update(second.id, by: app.me) { $0.title = "只改標題" })
        XCTAssertEqual(u.repo, "gitlab:group/sub/proj", "沒改倉庫時保留")

        (obj, text, isError) = try await call(mcp, "issue_update", ["id": second.id, "repo": ""])
        XCTAssertFalse(isError, text)
        XCTAssertNil((obj as? [String: Any])?["repo"], "issue_update repo 空字串 = 清除")
    }

    func testDiagramToolsThroughCore() async throws {
        try TestCore.start(self)
        let app = AppState()
        let mcp = MCPHandler(app: app)

        // App 內建 MCP:server 的繪圖工具出現在清單,呼叫轉給 server
        let list = await mcp.handle(try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": 1, "method": "tools/list"]))
        let names = String(decoding: try XCTUnwrap(list), as: UTF8.self)
        for n in ["wireframe_render", "flowchart_render"] { XCTAssertTrue(names.contains("\"\(n)\""), n) }
        let (obj, text, isError0) = try await call(mcp, "flowchart_render", ["spec": "flowchart TD\n  A([開始]) --> B{通過?}\n  B -->|是| C([結束])"])
        XCTAssertFalse(isError0, text)
        XCTAssertTrue(((obj as? [String: Any])?["svg"] as? String)?.contains("<svg") == true)
        let (_, text2, isError) = try await call(mcp, "wireframe_render", ["spec": "{ 不是 JSON"])
        XCTAssertTrue(isError, "描述有錯要回錯誤:\(text2)")

        // 文件櫃的圖:server 畫好 SVG,App 只當圖片解碼
        let wire = #"{"device":"mobile","root":{"type":"col","children":[{"type":"header","title":"登入"},{"type":"input","label":"Email"},{"type":"button","text":"登入"}]}}"#
        let w = await DiagramRenderer.render(.wireframe, wire, app: app)
        XCTAssertNil(w.error, w.error ?? "")
        XCTAssertGreaterThan(w.width, 0)
        let img = try XCTUnwrap(DiagramRender.image(try XCTUnwrap(w.svg)), "SVG 要能轉成圖片")
        XCTAssertGreaterThan(img.size.width, 0)

        let f = await DiagramRenderer.render(.flowchart, "graph LR\n  A[輸入] --> B[(資料庫)]", app: app)
        XCTAssertNil(f.error, f.error ?? "")
        XCTAssertNotNil(f.svg.flatMap(DiagramRender.image))

        let e = await DiagramRenderer.render(.wireframe, "{ 壞掉", app: app)
        XCTAssertNotNil(e.error, "渲染失敗時畫面顯示錯誤與原始碼")
    }
}
