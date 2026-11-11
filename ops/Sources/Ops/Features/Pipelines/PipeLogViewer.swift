import SwiftUI

/// 等寬日誌檢視器:可選取、搜尋、只顯示符合的行
struct PipeLogViewer: View {
    let job: PipelineJob?
    var stageName: String?

    @State private var query = ""
    @State private var onlyMatches = false
    @State private var wrap = false

    private struct Line: Identifiable { let id: Int; let text: String }

    private var allLines: [Line] {
        (job?.log ?? []).enumerated().map { Line(id: $0.offset + 1, text: $0.element) }
    }

    private func matches(_ s: String) -> Bool {
        !query.isEmpty && s.range(of: query, options: .caseInsensitive) != nil
    }

    var body: some View {
        let lines = allLines
        let matchCount = query.isEmpty ? 0 : lines.filter { matches($0.text) }.count
        let shown = (onlyMatches && !query.isEmpty) ? lines.filter { matches($0.text) } : lines

        VStack(alignment: .leading, spacing: 0) {
            toolbar(matchCount: matchCount, lines: lines)
            Divider()
            if job == nil {
                placeholder("選一個工作來看日誌", icon: "doc.text.magnifyingglass")
            } else if lines.isEmpty {
                if job?.status == .running {
                    placeholder("等待輸出…", icon: "ellipsis", spinning: true)
                } else if job?.status == .queued || job?.status == .waitingApproval {
                    placeholder("工作還沒開始", icon: "clock")
                } else {
                    placeholder("這個工作沒有日誌", icon: "doc")
                }
            } else {
                logBody(shown)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.08)))
    }

    private func toolbar(matchCount: Int, lines: [Line]) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "terminal").foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 0) {
                Text(job?.name ?? "日誌").font(.callout.weight(.semibold)).lineLimit(1)
                if let stageName {
                    Text("\(stageName) · \(lines.count) 行").font(.caption2).foregroundStyle(.secondary)
                }
            }
            if let job { RunStatusBadge(status: job.status) }
            Spacer(minLength: 8)
            HStack(spacing: 4) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary).font(.caption)
                TextField("搜尋日誌", text: $query).textFieldStyle(.plain).font(.caption)
                    .frame(minWidth: 70, maxWidth: 150)
                if !query.isEmpty {
                    Text("\(matchCount) 筆").font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
                    Button { query = "" } label: { Image(systemName: "xmark.circle.fill") }
                        .buttonStyle(.plain).foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 6).padding(.vertical, 3)
            .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 5))
            Toggle("只顯示符合", isOn: $onlyMatches).toggleStyle(.checkbox).controlSize(.small)
                .disabled(query.isEmpty)
            Menu {
                Toggle("自動換行", isOn: $wrap)
                Button("複製全部日誌") { PipeClipboard.copy(lines.map(\.text).joined(separator: "\n")) }
                    .disabled(lines.isEmpty)
            } label: { Image(systemName: "ellipsis.circle") }
                .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
        }
        .padding(.horizontal, 12).padding(.vertical, 8)
    }

    private func logBody(_ shown: [Line]) -> some View {
        ScrollViewReader { proxy in
            ScrollView(wrap ? [.vertical] : [.vertical, .horizontal]) {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(shown) { line in
                        HStack(alignment: .firstTextBaseline, spacing: 10) {
                            Text("\(line.id)")
                                .foregroundStyle(.tertiary)
                                .frame(width: 34, alignment: .trailing)
                            Text(highlighted(line.text))
                                .foregroundStyle(color(for: line.text))
                                .fixedSize(horizontal: !wrap, vertical: false)
                        }
                        .padding(.horizontal, 10).padding(.vertical, 1)
                        .frame(maxWidth: wrap ? .infinity : nil, alignment: .leading)
                        .background(matches(line.text) ? Color.yellow.opacity(0.1) : .clear)
                        .id(line.id)
                    }
                    if job?.status == .running {
                        PipeCursorLine().padding(.leading, 54).padding(.vertical, 2).id(-1)
                    }
                }
                .padding(.vertical, 8)
                .font(.system(size: 11.5, design: .monospaced))
                .textSelection(.enabled)
            }
            .frame(minHeight: 220, idealHeight: 340, maxHeight: 420)
            .onAppear { if job?.status == .running { proxy.scrollTo(-1, anchor: .bottom) } }
            .onChange(of: shown.count) {
                if job?.status == .running { proxy.scrollTo(-1, anchor: .bottom) }
            }
        }
    }

    private func placeholder(_ text: String, icon: String, spinning: Bool = false) -> some View {
        VStack(spacing: 8) {
            if spinning { ProgressView().controlSize(.small) } else {
                Image(systemName: icon).font(.title2).foregroundStyle(.tertiary)
            }
            Text(text).font(.callout).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, minHeight: 160)
    }

    private func color(for s: String) -> Color {
        let l = s.lowercased()
        if l.contains("error") || l.contains("fail") || l.contains("✗") || l.contains("錯誤") || l.contains("panic") { return .red }
        if l.contains("warn") || l.contains("警告") { return .orange }
        if l.contains("✓") || l.contains("pass") || l.contains("success") || l.contains("done") || l.contains("完成") { return .green }
        if l.hasPrefix("$") || l.hasPrefix("+") || l.hasPrefix(">") { return .secondary }
        return .primary
    }

    private func highlighted(_ s: String) -> AttributedString {
        var a = AttributedString(s)
        guard !query.isEmpty else { return a }
        var lower = a.startIndex
        while lower < a.endIndex, let r = a[lower..<a.endIndex].range(of: query, options: .caseInsensitive) {
            a[r].backgroundColor = Color.yellow.opacity(0.55)
            a[r].foregroundColor = Color.black
            lower = r.upperBound
        }
        return a
    }
}

private struct PipeCursorLine: View {
    @State private var on = false
    var body: some View {
        Rectangle().fill(Color.secondary).frame(width: 7, height: 13)
            .opacity(on ? 1 : 0.15)
            .onAppear { withAnimation(.easeInOut(duration: 0.6).repeatForever()) { on = true } }
    }
}
