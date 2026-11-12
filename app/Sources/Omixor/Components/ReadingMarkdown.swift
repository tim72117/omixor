import SwiftUI

/// 文件櫃用的閱讀版 Markdown:字大一點、行距寬、段落與標題之間有呼吸空間,版面寬度限制在好讀的範圍。
/// 支援:# 到 ####、段落、- / * / 1. 清單(依縮排分層)、- [ ] / - [x] 待辦、「名稱:說明」清單(名稱加粗)、
/// > 引言、``` 程式碼(可橫向捲動)、| 表格 |、--- 分隔線、![圖片](網址);行內的粗體、斜體、連結、`程式碼` 交給 AttributedString。
/// Issue 的留言等精簡顯示仍用 MarkdownText(Features/Development/DevIssuesTab.swift)。
struct ReadingMarkdown: View {
    let source: String
    init(_ source: String) { self.source = source }

    static let bodySize: CGFloat = 15
    static let lineSpacing: CGFloat = 6
    static let maxWidth: CGFloat = 700

    indirect enum Block: Hashable {
        case heading(Int, String)
        case paragraph(String)
        case item(level: Int, marker: Marker, text: String)
        case quote([String])
        case code(lang: String, text: String)
        case table(header: [String], rows: [[String]])
        case rule
        case image(alt: String, src: String)
    }

    enum Marker: Hashable { case bullet, number(String), task(Bool) }

    // MARK: 解析

    private static let imageLine = try! NSRegularExpression(pattern: #"^!\[([^\]]*)\]\(([^)\s]+)\)$"#)

    static func parse(_ source: String) -> [Block] {
        var out: [Block] = []
        var para: [String] = []
        var quote: [String] = []
        var code: (lang: String, lines: [String])?
        var table: [[String]] = []
        func cells(_ l: String) -> [String] {
            var s = l.trimmingCharacters(in: .whitespaces)
            if s.hasPrefix("|") { s.removeFirst() }
            if s.hasSuffix("|") { s.removeLast() }
            return s.components(separatedBy: "|").map { $0.trimmingCharacters(in: .whitespaces) }
        }
        func flushPara() { if !para.isEmpty { out.append(.paragraph(para.joined(separator: " "))); para = [] } }
        func flushQuote() { if !quote.isEmpty { out.append(.quote(quote)); quote = [] } }
        func flushTable() {
            guard !table.isEmpty else { return }
            // 第二列是 |---|---| 就是表格;不是就當段落
            if table.count >= 2, table[1].allSatisfy({ $0.allSatisfy { "-: ".contains($0) } && $0.contains("-") }) {
                out.append(.table(header: table[0], rows: Array(table.dropFirst(2))))
            } else {
                out += table.map { .paragraph($0.joined(separator: " | ")) }
            }
            table = []
        }
        func flushAll() { flushPara(); flushQuote(); flushTable() }

        for raw in source.replacingOccurrences(of: "\r", with: "").components(separatedBy: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                if let c = code { out.append(.code(lang: c.lang, text: c.lines.joined(separator: "\n"))); code = nil }
                else { flushAll(); code = (String(line.dropFirst(3)).trimmingCharacters(in: .whitespaces), []) }
                continue
            }
            if code != nil { code?.lines.append(raw); continue }
            if line.isEmpty { flushAll(); continue }
            if line.hasPrefix("|") { flushPara(); flushQuote(); table.append(cells(line)); continue } else { flushTable() }
            if line.hasPrefix(">") {
                flushPara()
                quote.append(String(line.dropFirst()).trimmingCharacters(in: .whitespaces))
                continue
            } else { flushQuote() }
            if line.count >= 3, Set(line.replacingOccurrences(of: " ", with: "")).count == 1, "-*_".contains(line.first!) {
                flushPara(); out.append(.rule); continue
            }
            let hashes = line.prefix { $0 == "#" }.count
            if (1...4).contains(hashes), line.dropFirst(hashes).first == " " {
                flushPara(); out.append(.heading(hashes, line.dropFirst(hashes + 1).trimmingCharacters(in: .whitespaces))); continue
            }
            let indent = raw.prefix { $0 == " " || $0 == "\t" }.reduce(0) { $0 + ($1 == "\t" ? 4 : 1) }
            let level = min(indent / 2, 4)
            if let first = line.first, "-*+".contains(first), line.dropFirst().first == " " {
                flushPara()
                var text = String(line.dropFirst(2))
                var marker = Marker.bullet
                if text.hasPrefix("[ ] ") { marker = .task(false); text.removeFirst(4) }
                else if text.lowercased().hasPrefix("[x] ") { marker = .task(true); text.removeFirst(4) }
                out.append(.item(level: level, marker: marker, text: text))
                continue
            }
            let digits = line.prefix { $0.isNumber }
            if !digits.isEmpty, line.dropFirst(digits.count).hasPrefix(". ") {
                flushPara(); out.append(.item(level: level, marker: .number(String(digits)), text: String(line.dropFirst(digits.count + 2)))); continue
            }
            let ns = line as NSString
            if let m = imageLine.firstMatch(in: line, range: NSRange(location: 0, length: ns.length)) {
                flushPara(); out.append(.image(alt: ns.substring(with: m.range(at: 1)), src: ns.substring(with: m.range(at: 2)))); continue
            }
            para.append(line)
        }
        if let c = code { out.append(.code(lang: c.lang, text: c.lines.joined(separator: "\n"))) }
        flushAll()
        return out
    }

    // MARK: 顯示

    var body: some View {
        let blocks = Self.parse(source)
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { i, b in
                view(b).padding(.top, i == 0 ? 0 : spacing(before: b, after: blocks[i - 1]))
            }
        }
        .frame(maxWidth: Self.maxWidth, alignment: .leading)
        .textSelection(.enabled)
    }

    /// 區塊之間的距離:標題前面空多一點,清單項目之間緊一點
    private func spacing(before b: Block, after prev: Block) -> CGFloat {
        switch (b, prev) {
        case (.heading(let l, _), _): return l <= 2 ? 26 : 18
        case (.item, .item): return 6
        case (_, .heading): return 10
        default: return 14
        }
    }

    @ViewBuilder private func view(_ b: Block) -> some View {
        switch b {
        case .heading(let level, let t):
            VStack(alignment: .leading, spacing: 6) {
                inline(t).font(.system(size: [26, 20, 17, 15][min(level, 4) - 1], weight: level <= 2 ? .bold : .semibold))
                    .lineSpacing(2)
                if level <= 2 { Divider().opacity(0.6) }
            }
        case .paragraph(let t):
            inline(t).font(.system(size: Self.bodySize)).lineSpacing(Self.lineSpacing).fixedSize(horizontal: false, vertical: true)
        case .item(let level, let marker, let t):
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                markerView(marker, level: level).frame(minWidth: 16, alignment: .trailing)
                itemText(t, done: marker == .task(true))
            }
            .padding(.leading, CGFloat(level) * 20 + 4)
        case .quote(let lines):
            HStack(alignment: .top, spacing: 12) {
                RoundedRectangle(cornerRadius: 1.5).fill(Color.accentColor.opacity(0.55)).frame(width: 3)
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(Array(lines.enumerated()), id: \.offset) { _, l in
                        inline(l).font(.system(size: Self.bodySize - 1)).lineSpacing(Self.lineSpacing - 1).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            .padding(.vertical, 4)
        case .code(let lang, let t):
            VStack(alignment: .leading, spacing: 4) {
                if !lang.isEmpty { Text(lang).font(.caption2.monospaced()).foregroundStyle(.tertiary) }
                ScrollView(.horizontal, showsIndicators: false) {
                    Text(t).font(.system(size: 12.5, design: .monospaced)).lineSpacing(3).fixedSize()
                }
            }
            .padding(12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 8))
        case .table(let header, let rows):
            tableView(header, rows)
        case .rule:
            Divider().padding(.vertical, 4)
        case .image(let alt, let src):
            MarkdownImage(alt: alt, src: src)
        }
    }

    @ViewBuilder private func markerView(_ m: Marker, level: Int) -> some View {
        switch m {
        case .bullet:
            Circle().fill(Color.secondary.opacity(level == 0 ? 0.8 : 0.5)).frame(width: 5, height: 5).offset(y: -3)
        case .number(let n):
            Text("\(n).").font(.system(size: Self.bodySize).monospacedDigit()).foregroundStyle(.secondary)
        case .task(let done):
            Image(systemName: done ? "checkmark.square.fill" : "square").font(.system(size: 14))
                .foregroundStyle(done ? Color.accentColor : .secondary)
        }
    }

    /// 清單項目:「名稱:說明」(全形或半形冒號,名稱不超過 12 字)的名稱加粗
    private func itemText(_ t: String, done: Bool) -> some View {
        var text = inline(t)
        if let r = t.firstIndex(where: { $0 == ":" || $0 == "\u{FF1A}" }) {
            let n = t.distance(from: t.startIndex, to: r)
            let key = String(t[..<r])
            if (1...12).contains(n), !key.contains("`"), !key.contains("http") {
                let rest = String(t[t.index(after: r)...]).trimmingCharacters(in: .whitespaces)
                text = Text(key).fontWeight(.semibold) + Text("\u{FF1A}") + inline(rest)
            }
        }
        return text.font(.system(size: Self.bodySize)).lineSpacing(Self.lineSpacing)
            .strikethrough(done).foregroundStyle(done ? .secondary : .primary)
            .fixedSize(horizontal: false, vertical: true)
    }

    private func tableView(_ header: [String], _ rows: [[String]]) -> some View {
        let cols = max(header.count, rows.map(\.count).max() ?? 0)
        return Grid(alignment: .leading, horizontalSpacing: 0, verticalSpacing: 0) {
            GridRow {
                ForEach(0..<cols, id: \.self) { c in
                    inline(c < header.count ? header[c] : "").font(.system(size: Self.bodySize - 1, weight: .semibold))
                        .padding(.horizontal, 12).padding(.vertical, 8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .background(.quaternary.opacity(0.5))
            ForEach(Array(rows.enumerated()), id: \.offset) { i, r in
                Divider().gridCellUnsizedAxes(.horizontal)
                GridRow {
                    ForEach(0..<cols, id: \.self) { c in
                        inline(c < r.count ? r[c] : "").font(.system(size: Self.bodySize - 1))
                            .padding(.horizontal, 12).padding(.vertical, 7)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .background(i % 2 == 1 ? Color.secondary.opacity(0.05) : .clear)
            }
        }
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(.quaternary))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }

    private func inline(_ s: String) -> Text {
        let a = (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(s)
        return Text(a)
    }
}
