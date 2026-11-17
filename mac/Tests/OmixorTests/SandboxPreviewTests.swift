import XCTest
import WebKit
import SwiftUI
@testable import Ops

/// 文件展示的沙盒:JS 會跑,但不能連網、沒有通往 App 的管道
@MainActor
final class SandboxPreviewTests: XCTestCase {
    func testPreviewLang() {
        XCTAssertEqual(ReadingMarkdown.previewLang("html preview"), "html")
        XCTAssertEqual(ReadingMarkdown.previewLang("JS preview"), "js")
        XCTAssertEqual(ReadingMarkdown.previewLang("javascript preview"), "js")
        XCTAssertNil(ReadingMarkdown.previewLang("html"), "沒標 preview 只顯示原始碼")
        XCTAssertNil(ReadingMarkdown.previewLang("python preview"))
    }

    func testPageHasCSPFirst() {
        let p = SandboxPreview.page("<meta http-equiv=\"Content-Security-Policy\" content=\"default-src *\"><b>x</b>", lang: "html")
        let csp = try! XCTUnwrap(p.range(of: "Content-Security-Policy"))
        let user = try! XCTUnwrap(p.range(of: "default-src *"))
        XCTAssertLessThan(csp.lowerBound, user.lowerBound, "我們的 CSP 在最前面(展示自己加的只能更嚴)")
        XCTAssertTrue(p.contains("connect-src 'none'"))
        XCTAssertTrue(SandboxPreview.page("console.log(1)", lang: "js").contains("<script>\nconsole.log(1)\n</script>"))
    }

    /// 真的跑一次:JS 有執行;fetch、外部圖片、XHR 都被擋;沒有 webkit.messageHandlers
    func testScriptRunsButNetworkBlocked() async throws {
        let src = """
        <div id="r">start</div>
        <script>
        const out = [];
        out.push('js:ok');
        out.push('bridge:' + (window.webkit && window.webkit.messageHandlers ? Object.keys(window.webkit.messageHandlers).length : 'none'));
        fetch('https://example.com/').then(() => out.push('fetch:ALLOWED')).catch(() => out.push('fetch:blocked'))
          .finally(() => { document.getElementById('r').textContent = out.join(' '); });
        </script>
        """
        var h: CGFloat = 160
        let view = SandboxPreviewView(source: src, lang: "html", height: Binding(get: { h }, set: { h = $0 }))
        let coord = view.makeCoordinator()
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        if let rules = await SandboxPreview.blockRules() { config.userContentController.add(rules) }
        let web = WKWebView(frame: CGRect(x: 0, y: 0, width: 400, height: 300), configuration: config)
        web.navigationDelegate = coord
        web.loadHTMLString(SandboxPreview.page(src, lang: "html"), baseURL: nil)
        var text = ""
        for _ in 0..<50 {
            try await Task.sleep(for: .milliseconds(100))
            text = (try? await web.evaluateJavaScript("document.getElementById('r') ? document.getElementById('r').textContent : ''") as? String) ?? ""
            if text.contains("fetch:") { break }
        }
        XCTAssertTrue(text.contains("js:ok"), "JS 要能執行:\(text)")
        XCTAssertTrue(text.contains("fetch:blocked"), "不能連網:\(text)")
        XCTAssertTrue(text.contains("bridge:none") || text.contains("bridge:0"), "沒有通往 App 的管道:\(text)")
        coord.timer?.invalidate()
    }
}
