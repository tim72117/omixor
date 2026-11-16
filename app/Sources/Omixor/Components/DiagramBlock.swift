import AppKit
import SwiftUI

// 文件裡的圖表程式碼區塊:```wireframe(線框圖 JSON)、```flowchart(流程圖 JSON 或 Mermaid),
// 以及第一行有效內容是 flowchart / graph 的 ```mermaid(其他 Mermaid 圖型照原本顯示程式碼)。同 web 的 components/Diagram.tsx。
//
// 渲染:交給 core server 的 MCP 工具 wireframe_render / flowchart_render(core/tools/wireframe、core/tools/flowchart,
// 只有核心一份渲染器)。core 沒有渲染用的 REST 端點(web 的 POST /api/action/diagram_svg 是 omixor-web 自己的),
// 所以走 App 已經在用的 server 管理端點 MCP(IssueRemote.mcp,/mcp/org/<組織>;本機模式是本機守護服務),只傳 spec、不帶 save,不會改到文件。
//
// 顯示:SVG 一律轉成圖片(NSImage(data:),系統的 SVG 解碼器只畫圖形,不執行腳本、不載入外部資源),
// 不放進網頁、也不當 HTML 執行。渲染失敗(描述有錯、server 連不上、SVG 解不開)時顯示錯誤與原始描述。

enum DiagramKind: String {
    case wireframe, flowchart

    var noun: String { self == .wireframe ? "線框圖" : "流程圖" }
    var sourceLabel: String { self == .wireframe ? "線框圖描述(JSON)" : "流程圖描述" }
    /// core 的 MCP 工具名稱
    var tool: String { rawValue + "_render" }

    /// 程式碼區塊的 info string 取第一個字當語言(「```mermaid title="x"」也算 mermaid),轉小寫
    static func lang(ofInfo info: String) -> String {
        info.trimmingCharacters(in: .whitespaces).split(whereSeparator: { $0 == " " || $0 == "\t" }).first.map { $0.lowercased() } ?? ""
    }

    /// 程式碼區塊 → 要畫成哪種圖(不是圖就回 nil)
    static func of(info: String, code: String) -> DiagramKind? {
        switch lang(ofInfo: info) {
        case "wireframe": return .wireframe
        case "flowchart": return .flowchart
        case "mermaid": return isMermaidFlowchart(code) ? .flowchart : nil
        default: return nil
        }
    }

    /// Mermaid 區塊是不是 flowchart / graph 圖:跳過空行、%% 註解與 --- 前置區塊,看第一行有效內容
    /// (和 web 的 isMermaidFlowchart、core 的 flowchart.IsMermaidFlowchart 一致)
    static func isMermaidFlowchart(_ src: String) -> Bool {
        var front = false
        for raw in src.replacingOccurrences(of: "\r", with: "").components(separatedBy: "\n") {
            let t = raw.trimmingCharacters(in: .whitespaces)
            if t.isEmpty || t.hasPrefix("%%") { continue }
            if t == "---" { front.toggle(); continue }
            if front { continue }
            let lower = t.lowercased()
            for kw in ["flowchart-elk", "flowchart", "graph"] where lower.hasPrefix(kw) {
                let rest = lower.dropFirst(kw.count)
                if let c = rest.first { return c == " " || c == "\t" || c == ";" }
                return true
            }
            return false
        }
        return false
    }
}

/// 一張圖的渲染結果
struct DiagramRender: Equatable {
    var svg: String?
    var width = 0
    var height = 0
    var warnings: [String] = []
    var error: String?

    /// 把 core 工具的回應(tools/call 的 result)轉成渲染結果
    static func from(toolResult r: [String: Any]) -> DiagramRender {
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        if r["isError"] as? Bool == true { return DiagramRender(error: text.isEmpty ? "渲染失敗" : text) }
        guard let obj = (try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] else {
            return DiagramRender(error: "server 的回應看不懂")
        }
        guard let svg = obj["svg"] as? String, !svg.isEmpty else { return DiagramRender(error: "server 沒有回傳 SVG") }
        return DiagramRender(svg: svg, width: (obj["width"] as? NSNumber)?.intValue ?? 0,
                             height: (obj["height"] as? NSNumber)?.intValue ?? 0,
                             warnings: obj["warnings"] as? [String] ?? [])
    }

    /// SVG 轉成圖片(只當圖片解碼;解不開回 nil)
    static func image(_ svg: String) -> NSImage? {
        guard let img = NSImage(data: Data(svg.utf8)), img.size.width > 0, img.size.height > 0 else { return nil }
        return img
    }
}

/// 圖表渲染(MainActor;請求在背景跑)。同一段描述不重算,最多留 40 份;同時間同一段只送一次
@MainActor
enum DiagramRenderer {
    private static var cache: [String: DiagramRender] = [:]
    private static var order: [String] = []
    private static var inflight: [String: Task<DiagramRender, Never>] = [:]

    static func key(_ kind: DiagramKind, _ source: String, server: String, org: String) -> String {
        "\(server)|\(org)|\(kind.rawValue)\n\(source)"
    }

    static func cached(_ key: String) -> DiagramRender? { cache[key] }

    /// 渲染一段描述(只傳 spec,不帶 save,不會改到文件);失敗不放進快取,下次再試
    static func render(_ kind: DiagramKind, _ source: String, app: AppState) async -> DiagramRender {
        let remote = app.startIssues().remote
        let org = app.currentOrgId
        let k = key(kind, source, server: remote.config.url.absoluteString, org: org)
        if let hit = cache[k] { return hit }
        if let t = inflight[k] { return await t.value }
        let tool = kind.tool
        let t = Task { @MainActor () -> DiagramRender in
            do {
                let res = try await remote.run {
                    try $0.mcp(org: org, method: "tools/call", params: ["name": tool, "arguments": ["spec": source]])
                }
                return DiagramRender.from(toolResult: res)
            } catch {
                return DiagramRender(error: "連不上 omixor server,畫不出來:\(error.localizedDescription)")
            }
        }
        inflight[k] = t
        let r = await t.value
        inflight[k] = nil
        if r.error == nil {
            if cache[k] == nil { order.append(k) }
            cache[k] = r
            while order.count > 40 { cache[order.removeFirst()] = nil }
        }
        return r
    }
}

/// 文件裡的一張圖:渲染好顯示圖片(點一下切換「版面寬度 / 原始大小」),下方可展開原始描述;
/// 失敗時顯示錯誤與原始描述
struct DiagramBlockView: View {
    @EnvironmentObject var app: AppState
    let kind: DiagramKind
    let source: String

    @State private var result: DiagramRender?
    @State private var image: NSImage?
    @State private var zoom = false
    @State private var showSource = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let r = result, let e = r.error ?? (image == nil ? "SVG 解不開,沒辦法當圖片顯示" : nil) {
                Label("\(kind.noun)畫不出來:\(e)", systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.red).textSelection(.enabled)
                sourceView
            } else if let img = image, let r = result {
                picture(img, r)
                if !r.warnings.isEmpty {
                    Text(r.warnings.joined(separator: ";")).font(.caption).foregroundStyle(.secondary)
                }
                DisclosureGroup(kind.sourceLabel, isExpanded: $showSource) { sourceView.padding(.top, 4) }
                    .font(.caption).foregroundStyle(.secondary)
            } else {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text("畫\(kind.noun)中…").font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .task(id: "\(kind.rawValue)\n\(source)") { await load() }
    }

    private func load() async {
        let r = await DiagramRenderer.render(kind, source, app: app)
        image = r.svg.flatMap(DiagramRender.image)
        result = r
    }

    /// 圖片:預設縮成版面寬度(不放大超過原始大小),點一下看原始大小(可橫向捲動)
    @ViewBuilder private func picture(_ img: NSImage, _ r: DiagramRender) -> some View {
        let w = CGFloat(r.width > 0 ? r.width : Int(img.size.width))
        let h = CGFloat(r.height > 0 ? r.height : Int(img.size.height))
        Group {
            if zoom {
                ScrollView(.horizontal) {
                    Image(nsImage: img).resizable().frame(width: w, height: h)
                }
            } else {
                Image(nsImage: img).resizable().aspectRatio(w / max(h, 1), contentMode: .fit)
                    .frame(maxWidth: w)
            }
        }
        .background(Color.white)
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(.quaternary))
        .contentShape(Rectangle())
        .onTapGesture { zoom.toggle() }
        .help(zoom ? "點一下縮成版面寬度" : "點一下看原始大小")
        .accessibilityLabel(kind.noun)
    }

    private var sourceView: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            Text(source).font(.system(size: 12, design: .monospaced)).fixedSize().textSelection(.enabled)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 8))
    }
}
