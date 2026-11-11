import SwiftUI

struct ChatView: View {
    @EnvironmentObject var model: AppModel

    var body: some View {
        VStack(spacing: 0) {
            if model.currentFolder == nil {
                ContentUnavailableView {
                    Label("選一個對話,或開新對話", systemImage: "bubble.left.and.text.bubble.right")
                } description: {
                    Text("左側是這台電腦上的 Claude Code 對話,依專案資料夾分組。")
                } actions: {
                    Button("New Session…") { model.newChat() }
                }
            } else {
                transcript
                ComposerView()
                    .padding(.horizontal, 24).padding(.bottom, 14).padding(.top, 6)
            }
        }
        .background(Color(nsColor: .textBackgroundColor))
    }

    private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 14) {
                    ForEach(model.items) { item in
                        ItemView(item: item).id(item.id)
                    }
                    if model.isRunning {
                        HStack(spacing: 8) {
                            ProgressView().controlSize(.small)
                            Text("Working…").foregroundStyle(.secondary)
                        }
                        .id("running")
                    }
                }
                .frame(maxWidth: 760, alignment: .leading)
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 24).padding(.vertical, 20)
            }
            .overlay {
                if model.isLoading { ProgressView() }
                else if model.items.isEmpty && !model.isRunning {
                    Text(model.currentFolder.map { "在 \(($0 as NSString).lastPathComponent) 開始新對話" } ?? "")
                        .foregroundStyle(.secondary)
                }
            }
            .onChange(of: model.items.count) {
                withAnimation(.easeOut(duration: 0.15)) {
                    proxy.scrollTo(model.isRunning ? "running" : model.items.last?.id, anchor: .bottom)
                }
            }
            .onChange(of: model.isLoading) {
                if !model.isLoading { proxy.scrollTo(model.items.last?.id, anchor: .bottom) }
            }
        }
    }
}

private struct ItemView: View {
    let item: TranscriptItem

    var body: some View {
        switch item {
        case .user(_, let text):
            HStack {
                Spacer(minLength: 80)
                Text(text)
                    .textSelection(.enabled)
                    .padding(.horizontal, 14).padding(.vertical, 9)
                    .background(.quaternary.opacity(0.7), in: RoundedRectangle(cornerRadius: 14))
            }
        case .assistant(_, let text):
            MarkdownText(text: text)
        case .tools(_, let calls):
            ToolsRow(calls: calls)
        case .notice(_, let text, let isError):
            Label(text, systemImage: isError ? "exclamationmark.triangle" : "info.circle")
                .foregroundStyle(isError ? .red : .secondary)
                .textSelection(.enabled)
        }
    }
}

/// 「Ran 3 commands ›」,點開看每個工具呼叫。
private struct ToolsRow: View {
    let calls: [ToolCall]
    @State private var open = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button {
                withAnimation(.easeOut(duration: 0.15)) { open.toggle() }
            } label: {
                HStack(spacing: 4) {
                    Text(label)
                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.semibold))
                        .rotationEffect(.degrees(open ? 90 : 0))
                }
                .foregroundStyle(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if open {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(calls) { call in
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Text(call.name).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                            Text(call.summary).font(.caption.monospaced()).lineLimit(2).textSelection(.enabled)
                        }
                    }
                }
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 8))
            }
        }
    }

    private var label: String {
        let commands = calls.filter { $0.name == "Bash" }.count
        if commands == calls.count {
            return commands == 1 ? "Ran 1 command" : "Ran \(commands) commands"
        }
        return calls.count == 1 ? "Used \(calls[0].name)" : "Used \(calls.count) tools"
    }
}

/// 簡易 Markdown:程式碼區塊用等寬底色,其餘段落用系統的行內 Markdown。
struct MarkdownText: View {
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(MarkdownText.blocks(text).enumerated()), id: \.offset) { _, block in
                switch block {
                case .code(let code):
                    Text(code)
                        .font(.system(.callout, design: .monospaced))
                        .textSelection(.enabled)
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 8))
                case .prose(let s):
                    Text(inline(s)).textSelection(.enabled).lineSpacing(3)
                }
            }
        }
    }

    enum Block { case prose(String), code(String) }

    static func blocks(_ text: String) -> [Block] {
        var out: [Block] = []
        var prose: [Substring] = [], code: [Substring] = []
        var inCode = false
        for line in text.split(separator: "\n", omittingEmptySubsequences: false) {
            if line.trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                if inCode {
                    out.append(.code(code.joined(separator: "\n"))); code = []
                } else if !prose.isEmpty {
                    out.append(.prose(prose.joined(separator: "\n"))); prose = []
                }
                inCode.toggle()
            } else if inCode {
                code.append(line)
            } else {
                prose.append(line)
            }
        }
        if !code.isEmpty { out.append(.code(code.joined(separator: "\n"))) }
        let rest = prose.joined(separator: "\n").trimmingCharacters(in: .newlines)
        if !rest.isEmpty { out.append(.prose(rest)) }
        return out
    }

    private func inline(_ s: String) -> AttributedString {
        let opts = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        return (try? AttributedString(markdown: s, options: opts)) ?? AttributedString(s)
    }
}
