import SwiftUI
import WebKit

// 文件裡的 HTML / JS 展示(```html preview、```js preview)的沙盒。文件可能是角色或 MCP 寫的,內容不可信,所以:
//   - 獨立的 WKWebView,資料不保留(.nonPersistent),和 App 的登入、鑰匙圈、其他網頁都不共用
//   - 不掛任何 script message handler(網頁沒有任何管道呼叫 App);高度由 App 在隔離的 content world 讀
//   - 不能連網:CSP 只允許頁面內嵌的 script/style 與 data: 圖片(connect-src、form-action 都是 none),
//     再加內容規則把 http(s)/ws(s)/file 請求全部擋掉;只允許載入這一頁(不能跳頁、不能開新視窗)
//   - 要人按「執行預覽」才跑;可以隨時停止(拿掉這個 WKWebView,網頁的 process 跟著結束);超過大小上限不執行
// 範本(DocHTMLView)維持只有版面,不放 JS。

enum SandboxPreview {
    /// 原始碼上限(字元)
    static let maxSource = 200_000

    static let csp = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; " +
        "font-src data:; media-src data: blob:; connect-src 'none'; form-action 'none'; frame-src 'none'; base-uri 'none'"

    /// 展示的整頁:CSP 放在最前面(之後的內容只能更嚴、不能放寬)
    static func page(_ source: String, lang: String) -> String {
        let bodyContent = lang == "js" ? "<pre id=\"out\"></pre>\n<script>\n\(source)\n</script>" : source
        return """
        <!doctype html><html><head>
        <meta http-equiv="Content-Security-Policy" content="\(csp)">
        <meta charset="utf-8"><meta name="viewport" content="width=device-width">
        <style>:root{color-scheme:light dark}body{margin:12px;font:14px -apple-system,sans-serif}</style>
        </head><body>
        \(bodyContent)
        </body></html>
        """
    }

    /// 擋掉所有對外請求的內容規則(編譯一次)
    @MainActor static func blockRules() async -> WKContentRuleList? {
        if let r = cachedRules { return r }
        let json = """
        [{"trigger":{"url-filter":"^(https?|wss?|file|ftp)://"},"action":{"type":"block"}}]
        """
        let r = try? await WKContentRuleListStore.default().compileContentRuleList(forIdentifier: "omixor-sandbox-block-network",
                                                                                     encodedContentRuleList: json)
        cachedRules = r
        return r
    }
    @MainActor private static var cachedRules: WKContentRuleList?
}

/// 執行一段展示(已經是使用者按過「執行預覽」才建立)
struct SandboxPreviewView: NSViewRepresentable {
    let source: String
    let lang: String
    @Binding var height: CGFloat

    func makeCoordinator() -> Coordinator { Coordinator(height: $height) }

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        config.defaultWebpagePreferences.allowsContentJavaScript = true
        config.preferences.javaScriptCanOpenWindowsAutomatically = false
        config.mediaTypesRequiringUserActionForPlayback = .all
        let web = WKWebView(frame: .zero, configuration: config)
        web.navigationDelegate = context.coordinator
        web.uiDelegate = context.coordinator
        web.setValue(false, forKey: "drawsBackground")
        let html = SandboxPreview.page(source, lang: lang)
        Task { @MainActor in
            if let rules = await SandboxPreview.blockRules() { config.userContentController.add(rules) }
            web.loadHTMLString(html, baseURL: nil)
        }
        return web
    }

    func updateNSView(_ web: WKWebView, context: Context) {}

    static func dismantleNSView(_ web: WKWebView, coordinator: Coordinator) {
        coordinator.timer?.invalidate()
        web.stopLoading()
        web.loadHTMLString("", baseURL: nil)
    }

    final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate {
        var height: Binding<CGFloat>
        var timer: Timer?
        init(height: Binding<CGFloat>) { self.height = height }

        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                     decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
            // 只允許載入自己這一頁(loadHTMLString 是 about:blank);iframe 也不行
            decisionHandler(action.request.url?.scheme == "about" && action.targetFrame?.isMainFrame != false ? .allow : .cancel)
        }

        /// 不開新視窗(window.open、target=_blank)
        func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                     for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? { nil }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            measure(webView)
            // 展示可能會動態改變高度:之後每秒量一次(在隔離的 content world 讀,網頁拿不到這段程式)
            timer?.invalidate()
            timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self, weak webView] _ in
                guard let self, let webView else { return }
                Task { @MainActor in self.measure(webView) }
            }
        }

        @MainActor private func measure(_ web: WKWebView) {
            web.callAsyncJavaScript("return document.documentElement.scrollHeight", arguments: [:], in: nil, in: .defaultClient) { [weak self] r in
                guard let self, case .success(let v) = r, let h = (v as? NSNumber)?.doubleValue else { return }
                let clamped = min(max(CGFloat(h), 60), 900)
                if abs(clamped - self.height.wrappedValue) > 1 { self.height.wrappedValue = clamped }
            }
        }
    }
}

/// 展示區塊:平常只顯示原始碼;按「執行預覽」才在沙盒裡跑,可以停止
struct SandboxBlock: View {
    let source: String
    let lang: String     // html 或 js
    @State private var running = false
    @State private var runId = UUID()
    @State private var height: CGFloat = 160
    @State private var showSource = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Label("\(lang.uppercased()) 展示", systemImage: "play.rectangle").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                Spacer()
                if source.count > SandboxPreview.maxSource {
                    Text("太大了,不執行").font(.caption).foregroundStyle(.orange)
                } else if running {
                    Button { runId = UUID() } label: { Label("重新執行", systemImage: "arrow.clockwise") }.controlSize(.small)
                    Button { running = false } label: { Label("停止", systemImage: "stop.fill") }.controlSize(.small)
                } else {
                    Button { height = 160; running = true } label: { Label("執行預覽", systemImage: "play.fill") }
                        .controlSize(.small).buttonStyle(.borderedProminent)
                        .help("在隔離的沙盒裡執行:不能連網、拿不到 App 的資料與登入")
                }
            }
            if running {
                // 執行時原始碼收起來(可以展開);展開時限高並裁切,長的原始碼不會溢出蓋到文件其他內容
                DisclosureGroup(isExpanded: $showSource) {
                    ScrollView([.horizontal, .vertical]) {
                        Text(source).font(.system(size: 12.5, design: .monospaced)).lineSpacing(3).fixedSize()
                            .textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(height: 180)
                    .clipped()
                } label: {
                    Text("原始碼").font(.caption).foregroundStyle(.secondary)
                }
            } else {
                ScrollView(.horizontal, showsIndicators: false) {
                    Text(source).font(.system(size: 12.5, design: .monospaced)).lineSpacing(3).fixedSize().textSelection(.enabled)
                }
            }
            if running {
                SandboxPreviewView(source: source, lang: lang, height: $height)
                    .id(runId)
                    .frame(height: height)
                    .background(.background, in: RoundedRectangle(cornerRadius: 6))
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.accentColor.opacity(0.4)))
                Text("沙盒:不能連網、不能開新視窗、拿不到 App 的資料與登入").font(.caption2).foregroundStyle(.tertiary)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 8))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }
}
