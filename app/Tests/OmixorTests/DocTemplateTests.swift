import XCTest
import WebKit
@testable import Ops

/// 文件櫃的範本:欄位解析、存檔檢查、產生網頁;轉換器與網頁要和 Go 版一模一樣(templates/doc)
@MainActor
final class DocTemplateTests: XCTestCase {
    var dataDir: URL!
    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-doctpl-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }
    override func tearDown() async throws {
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    static let template = """
    ---
    kind: template
    for: lesson
    required: course, lesson
    ---
    # 課程範本

    ```html
    <h1>{{title}}</h1><p class="goal">{{s.目標.text}}</p>
    ```

    ```css
    .goal { color: #b5742a }
    ```
    """
    static let lesson = """
    ---
    kind: lesson
    course: L0
    lesson: 0.intro
    ---
    # 為什麼 <不用> 學寫程式 & "也能"

    ## 目標

    看懂差別
    """

    private func call(_ mcp: MCPHandler, _ name: String, _ args: [String: Any]) async throws -> ([String: Any], String, Bool) {
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: try XCTUnwrap(raw)) as? [String: Any])?["result"] as? [String: Any])
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        return ((try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] ?? [:], text, r["isError"] as? Bool ?? false)
    }

    func testFrontMatterAndFence() {
        let fm = DocTemplates.frontMatter(Self.lesson)
        XCTAssertEqual(fm.meta, ["kind": "lesson", "course": "L0", "lesson": "0.intro"])
        XCTAssertTrue(fm.rest.hasPrefix("# 為什麼"))
        XCTAssertEqual(DocTemplates.frontMatter("# 沒有欄位").meta, [:])
        XCTAssertEqual(DocTemplates.fence(Self.template, "css"), ".goal { color: #b5742a }")
        let doc = CabinetDoc(id: "DOC-1", title: "範本", body: Self.template, section: "tools", tab: nil, author: "mcp")
        XCTAssertEqual(DocTemplates.parse(doc)?.required, ["course", "lesson"])
        XCTAssertEqual(DocTemplates.parse(doc)?.kind, "lesson")
    }

    func testSaveChecksTemplateAndRequiredFields() async throws {
        let app = AppState()
        app.startAgents(timer: false)
        let mcp = MCPHandler(app: app)
        // 範本缺 for、缺 html
        var (_, text, err) = try await call(mcp, "ops_doc_save", ["section": "tools", "title": "壞範本", "body": "---\nkind: template\n---\n"])
        XCTAssertTrue(err); XCTAssertTrue(text.contains("for 欄位"), text)
        (_, text, err) = try await call(mcp, "ops_doc_save", ["section": "tools", "title": "壞範本", "body": "---\nkind: template\nfor: lesson\n---\n"])
        XCTAssertTrue(err); XCTAssertTrue(text.contains("```html"), text)
        // 還沒有範本時,lesson 文件不檢查
        (_, text, err) = try await call(mcp, "ops_doc_save", ["section": "levels", "title": "早到的", "body": "---\nkind: lesson\n---\n# x"])
        XCTAssertFalse(err, text)
        var (d, _, _) = try await call(mcp, "ops_doc_save", ["section": "tools", "title": "課程範本", "body": Self.template])
        let tplId = try XCTUnwrap(d["id"] as? String)
        // 有範本後檢查必填欄位
        (_, text, err) = try await call(mcp, "ops_doc_save", ["section": "levels", "title": "缺欄位", "body": "---\nkind: lesson\ncourse: L0\n---\n# x"])
        XCTAssertTrue(err)
        XCTAssertEqual(text, "kind: lesson 的文件缺少欄位:lesson(見範本 \(tplId))")
        (d, text, err) = try await call(mcp, "ops_doc_save", ["section": "levels", "title": "序章", "body": Self.lesson])
        XCTAssertFalse(err, text)
        let id = try XCTUnwrap(d["id"] as? String)
        // 修改時也檢查(append_body 不會動到欄位)
        (_, text, err) = try await call(mcp, "ops_doc_save", ["id": id, "body": "---\nkind: lesson\n---\n"])
        XCTAssertTrue(err, text)
        (_, text, err) = try await call(mcp, "ops_doc_save", ["id": id, "append_body": "## 備註\n\n補充"])
        XCTAssertFalse(err, text)
        // 有範本的文件產生網頁;範本本身、一般文件不產生
        let doc = try XCTUnwrap(Cabinet.doc(app, id))
        let html = try XCTUnwrap(DocTemplates.html(for: doc, in: app.cabinetDocs))
        XCTAssertTrue(html.contains(".goal { color: #b5742a }"))
        XCTAssertTrue(html.contains("MdRender.mount("))
        XCTAssertFalse(html.contains("<不用>"), "內文要跳脫,不能直接出現在 HTML")
        XCTAssertNil(DocTemplates.html(for: try XCTUnwrap(Cabinet.doc(app, tplId)), in: app.cabinetDocs))
    }

    /// 轉換器和網頁要和 Go 版一樣:templates/doc/docrender.js 與 docpage.html(OPS_EXPORT_DOCPAGE=1 時重新匯出網頁)
    func testSharedWithGo() throws {
        let spec = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("../../../templates/doc").standardized
        guard let js = try? String(contentsOf: spec.appendingPathComponent("docrender.js"), encoding: .utf8) else {
            throw XCTSkip("沒有 templates/doc(單獨拿到 ops 時略過)")
        }
        XCTAssertEqual(DocRenderJS.source, js.hasSuffix("\n") ? String(js.dropLast()) : js, "改了 docrender.js 要跑 app/scripts/gen-docrender.py")
        let tpl = try XCTUnwrap(DocTemplates.parse(CabinetDoc(id: "DOC-1", title: "範本", body: Self.template, section: "tools", tab: nil, author: "mcp")))
        let page = DocTemplates.page(md: Self.lesson, template: tpl)
        let file = spec.appendingPathComponent("docpage.html")
        if ProcessInfo.processInfo.environment["OPS_EXPORT_DOCPAGE"] == "1" {
            try Data(page.utf8).write(to: file)
        } else {
            XCTAssertEqual(page, try String(contentsOf: file, encoding: .utf8), "跑 OPS_EXPORT_DOCPAGE=1 swift test --filter DocTemplateTests 重新匯出")
        }
    }

    /// 網頁元件裡真的跑一次轉換器:範本的欄位都填上、內文跳脫、沒有殘留的 {{…}}
    func testRendersInWebView() async throws {
        let tpl = try XCTUnwrap(DocTemplates.parse(CabinetDoc(id: "DOC-1", title: "範本", body: Self.template, section: "tools", tab: nil, author: "mcp")))
        let web = WKWebView(frame: NSRect(x: 0, y: 0, width: 800, height: 600))
        final class Wait: NSObject, WKNavigationDelegate {
            var done: CheckedContinuation<Void, Never>?
            func webView(_ w: WKWebView, didFinish n: WKNavigation!) { done?.resume(); done = nil }
        }
        let wait = Wait()
        web.navigationDelegate = wait
        await withCheckedContinuation { c in
            wait.done = c
            web.loadHTMLString(DocTemplates.page(md: Self.lesson, template: tpl), baseURL: nil)
        }
        let h1 = try await web.evaluateJavaScript("document.querySelector('#root h1').textContent") as? String
        let goal = try await web.evaluateJavaScript("document.querySelector('.goal').textContent") as? String
        let color = try await web.evaluateJavaScript("getComputedStyle(document.querySelector('.goal')).color") as? String
        let left = try await web.evaluateJavaScript("document.getElementById('root').innerHTML.includes('{{')") as? Bool
        XCTAssertEqual(h1, "為什麼 <不用> 學寫程式 & \"也能\"")
        XCTAssertEqual(goal, "看懂差別")
        XCTAssertEqual(color, "rgb(181, 116, 42)")
        XCTAssertEqual(left, false)
    }
}
