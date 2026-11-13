import SwiftUI

/// 簡單的 Markdown 顯示(同 macOS App 的 MarkdownText:標題、清單、程式碼區塊、段落;段落內的粗體、斜體、連結、行內程式碼用系統的 AttributedString)
struct MarkdownText: View {
    let source: String
    var font: Font = .body
    init(_ source: String, font: Font = .body) { self.source = source; self.font = font }

    enum Block: Hashable {
        case heading(Int, String), bullet(String, String), code(String), quote(String), text(String)
    }

    static func blocks(_ source: String) -> [Block] {
        var out: [Block] = []
        var code: [String]?
        var para: [String] = []
        func flush() { if !para.isEmpty { out.append(.text(para.joined(separator: "\n"))); para = [] } }
        for raw in source.components(separatedBy: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                if let c = code { out.append(.code(c.joined(separator: "\n"))); code = nil } else { flush(); code = [] }
                continue
            }
            if code != nil { code?.append(raw); continue }
            if line.isEmpty { flush(); continue }
            let hashes = line.prefix { $0 == "#" }.count
            if (1...6).contains(hashes), line.dropFirst(hashes).first == " " {
                flush(); out.append(.heading(hashes, line.dropFirst(hashes + 1).trimmingCharacters(in: .whitespaces))); continue
            }
            if let first = line.first, "-*+".contains(first), line.dropFirst().first == " " {
                flush()
                var item = String(line.dropFirst(2))
                var mark = "•"
                if item.hasPrefix("[ ] ") { mark = "☐"; item.removeFirst(4) } else if item.lowercased().hasPrefix("[x] ") { mark = "☑"; item.removeFirst(4) }
                out.append(.bullet(mark, item)); continue
            }
            let digits = line.prefix { $0.isNumber }
            if !digits.isEmpty, line.dropFirst(digits.count).hasPrefix(". ") {
                flush(); out.append(.bullet(digits + ".", String(line.dropFirst(digits.count + 2)))); continue
            }
            if line.hasPrefix(">") {
                flush(); out.append(.quote(line.dropFirst().trimmingCharacters(in: .whitespaces))); continue
            }
            para.append(raw)
        }
        if let c = code { out.append(.code(c.joined(separator: "\n"))) }
        flush()
        return out
    }

    /// 段落內的 Markdown;連結只保留 http/https/mailto,其他 scheme(例如 omixor://pair)拿掉連結、只留文字,
    /// 避免訊息或 Issue 裡的連結一點就觸發配對或打開別的 App
    static func inline(_ s: String) -> AttributedString {
        var a = (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(s)
        for run in a.runs {
            if let url = run.link, !SafeLink.isAllowed(url) { a[run.range].link = nil }
        }
        return a
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(Array(Self.blocks(source).enumerated()), id: \.offset) { _, b in
                switch b {
                case .heading(let n, let s):
                    Text(Self.inline(s)).font(n <= 1 ? .title3.bold() : n == 2 ? .headline : .subheadline.bold())
                case .bullet(let mark, let s):
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(mark).foregroundStyle(.secondary)
                        Text(Self.inline(s)).font(font)
                    }
                case .code(let s):
                    ScrollView(.horizontal, showsIndicators: false) {
                        Text(s).font(.system(.footnote, design: .monospaced)).textSelection(.enabled).padding(8)
                    }
                    .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 6))
                case .quote(let s):
                    Text(Self.inline(s)).font(font).foregroundStyle(.secondary)
                        .padding(.leading, 8)
                        .overlay(alignment: .leading) { Rectangle().fill(.quaternary).frame(width: 3) }
                case .text(let s):
                    Text(Self.inline(s)).font(font)
                }
            }
        }
        .textSelection(.enabled)
        .frame(maxWidth: .infinity, alignment: .leading)
        // 第二道防線:就算有漏網的連結,也只讓 http/https/mailto 打開
        .environment(\.openURL, SafeLink.action)
    }
}

/// App 裡能開的連結只限 http、https、mailto;omixor:// 與其他 scheme 一律不開
enum SafeLink {
    static let allowedSchemes: Set<String> = ["http", "https", "mailto"]

    static func isAllowed(_ url: URL) -> Bool {
        guard let scheme = url.scheme?.lowercased() else { return false }
        return allowedSchemes.contains(scheme)
    }

    /// 文字裡的網址:允許的 scheme 才回傳 URL
    static func url(_ text: String) -> URL? {
        guard let u = URL(string: text.trimmingCharacters(in: .whitespacesAndNewlines)), isAllowed(u) else { return nil }
        return u
    }

    static let action = OpenURLAction { url in isAllowed(url) ? .systemAction : .discarded }
}
