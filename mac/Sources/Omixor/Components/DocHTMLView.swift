import SwiftUI
import WebKit

/// 顯示文件櫃依範本產生的 HTML(DocTemplates.page)。只載入這一頁:不連外、點連結不跳頁
struct DocHTMLView: NSViewRepresentable {
    let html: String

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        let web = WKWebView(frame: .zero, configuration: config)
        web.navigationDelegate = context.coordinator
        web.setValue(false, forKey: "drawsBackground")
        web.loadHTMLString(html, baseURL: nil)
        context.coordinator.loaded = html
        return web
    }

    func updateNSView(_ web: WKWebView, context: Context) {
        guard context.coordinator.loaded != html else { return }
        context.coordinator.loaded = html
        web.loadHTMLString(html, baseURL: nil)
    }

    final class Coordinator: NSObject, WKNavigationDelegate {
        var loaded = ""
        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                     decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
            // 只允許載入自己這一頁(loadHTMLString 是 about:blank)
            decisionHandler(action.request.url?.scheme == "about" ? .allow : .cancel)
        }
    }
}
