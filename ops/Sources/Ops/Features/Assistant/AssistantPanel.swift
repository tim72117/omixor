import SwiftUI

/// 右側檢閱欄的 AI 維運助理:依目前頁面給出狀況摘要、建議問題、對話與可直接執行的動作。
struct AssistantPanel: View {
    @EnvironmentObject var app: AppState
    @StateObject private var chat = AssistChat()
    @State private var draft = ""
    @FocusState private var composerFocused: Bool

    private var responder: AssistResponder { AssistResponder(app: app) }

    var body: some View {
        VStack(spacing: 0) {
            AssistHeader(focusLabel: responder.focusLabel, canClear: !chat.messages.isEmpty) { chat.clear() }
            Divider()
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        AssistBriefingCard(briefing: responder.briefing()) { ask("總結目前狀況") }
                        AssistDecisionsCard()
                        AssistSuggestions(prompts: responder.suggestions(), disabled: chat.busy) { ask($0) }
                        if !chat.messages.isEmpty {
                            AssistThreadDivider()
                            ForEach(chat.messages) { m in
                                AssistBubble(message: m, chat: chat).id(m.id)
                            }
                        }
                        Color.clear.frame(height: 1).id("bottom")
                    }
                    .padding(12)
                }
                .onChange(of: chat.messages) { _, _ in
                    withAnimation(.easeOut(duration: 0.15)) { proxy.scrollTo("bottom", anchor: .bottom) }
                }
            }
            Divider()
            AssistComposer(text: $draft, busy: chat.busy, focused: $composerFocused) { send() }
        }
        .background(Color(nsColor: .windowBackgroundColor))
    }

    private func ask(_ q: String) {
        chat.send(q, app: app)
    }

    private func send() {
        guard !chat.busy else { return }
        let q = draft
        guard !q.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        draft = ""
        ask(q)
        composerFocused = true
    }
}

// MARK: - 頁首

private struct AssistHeader: View {
    @EnvironmentObject var app: AppState
    let focusLabel: String?
    let canClear: Bool
    let onClear: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Circle().fill(AssistTheme.gradient)
                    .frame(width: 24, height: 24)
                    .overlay(Image(systemName: "sparkles").font(.system(size: 11, weight: .semibold)).foregroundStyle(.white))
                Text("AI 助理").font(.headline)
                Spacer()
                Button(action: onClear) { Image(systemName: "trash") }
                    .buttonStyle(.borderless)
                    .disabled(!canClear)
                    .help("清除對話")
                Button { app.showAssistant = false } label: { Image(systemName: "sidebar.trailing") }
                    .buttonStyle(.borderless)
                    .help("隱藏 AI 助理(⌘J)")
            }
            HStack(spacing: 5) {
                Image(systemName: app.section.icon)
                Text("正在看:\(app.section.title)").fontWeight(.medium)
                if let focusLabel {
                    Text("·").foregroundStyle(.secondary)
                    Text(focusLabel).lineLimit(1).truncationMode(.tail)
                }
            }
            .font(.callout)
            .foregroundStyle(AssistTheme.accent)
            .padding(.horizontal, 8).padding(.vertical, 4)
            .background(AssistTheme.accent.opacity(0.10), in: Capsule())
            .help("助理會依照目前的頁面回答")
        }
        .padding(.horizontal, 12).padding(.vertical, 10)
    }
}

// MARK: - 目前狀況

private struct AssistBriefingCard: View {
    let briefing: AssistBriefing
    let onExplain: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Text("目前狀況").font(.callout.weight(.semibold)).foregroundStyle(.secondary)
                Spacer()
                Button("詳細說明", action: onExplain)
                    .buttonStyle(.borderless)
                    .font(.callout)
                    .foregroundStyle(AssistTheme.accent)
            }
            HStack(spacing: 6) {
                Circle().fill(briefing.tone).frame(width: 8, height: 8)
                Text(briefing.headline).font(.body.weight(.semibold))
            }
            VStack(alignment: .leading, spacing: 5) {
                ForEach(briefing.lines) { line in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Image(systemName: line.icon).foregroundStyle(line.color).frame(width: 14)
                        Text(line.text).fixedSize(horizontal: false, vertical: true)
                    }
                    .font(.callout)
                }
            }
        }
        .padding(11)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(AssistTheme.soft, in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.purple.opacity(0.18)))
    }
}

// MARK: - 待你決定

private struct AssistDecisionsCard: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        let pending = app.pendingApprovals.sorted { $0.expiresAt < $1.expiresAt }
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Label("待你決定", systemImage: "hand.raised.fill")
                    .font(.callout.weight(.semibold))
                    .foregroundStyle(pending.isEmpty ? Color.secondary : Color.purple)
                if !pending.isEmpty {
                    Text("\(pending.count)")
                        .font(.caption.weight(.bold)).monospacedDigit()
                        .padding(.horizontal, 6).padding(.vertical, 1)
                        .foregroundStyle(.white)
                        .background(Color.purple, in: Capsule())
                }
                Spacer()
                Button("全部查看") { app.go(.automation) }
                    .buttonStyle(.borderless)
                    .font(.callout)
                    .foregroundStyle(AssistTheme.accent)
            }
            if pending.isEmpty {
                Text("沒有等待核准的 AI 請求").font(.callout).foregroundStyle(.secondary)
            } else {
                ForEach(pending.prefix(3)) { p in
                    Button { app.go(.automation, focus: p.id) } label: {
                        HStack(spacing: 6) {
                            Circle().fill(p.risk.color).frame(width: 6, height: 6)
                            Text(p.title).lineLimit(1).truncationMode(.tail)
                            Spacer(minLength: 4)
                            Text(p.risk.label).foregroundStyle(p.risk.color)
                            Image(systemName: "chevron.right").foregroundStyle(.tertiary)
                        }
                        .font(.callout)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .help("前往 AI 自動化查看「\(p.title)」")
                }
                if pending.count > 3 {
                    Text("還有 \(pending.count - 3) 個").font(.system(size: 11)).foregroundStyle(.secondary)
                }
            }
        }
        .padding(11)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.primary.opacity(0.07)))
    }
}

// MARK: - 建議問題

private struct AssistSuggestions: View {
    let prompts: [String]
    let disabled: Bool
    let onPick: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("可以問我").font(.callout.weight(.semibold)).foregroundStyle(.secondary)
            AssistFlowLayout(spacing: 6) {
                ForEach(prompts, id: \.self) { p in
                    Button { onPick(p) } label: {
                        Text(p)
                            .font(.callout)
                            .padding(.horizontal, 9).padding(.vertical, 5)
                            .foregroundStyle(AssistTheme.accent)
                            .background(AssistTheme.accent.opacity(0.08), in: Capsule())
                            .overlay(Capsule().strokeBorder(AssistTheme.accent.opacity(0.22)))
                            .contentShape(Capsule())
                    }
                    .buttonStyle(.plain)
                    .disabled(disabled)
                    .opacity(disabled ? 0.5 : 1)
                }
            }
        }
    }
}

private struct AssistThreadDivider: View {
    var body: some View {
        HStack(spacing: 8) {
            Rectangle().fill(Color.primary.opacity(0.08)).frame(height: 1)
            Text("對話").font(.system(size: 11)).foregroundStyle(.tertiary)
            Rectangle().fill(Color.primary.opacity(0.08)).frame(height: 1)
        }
        .padding(.vertical, 2)
    }
}

// MARK: - 對話泡泡

private struct AssistBubble: View {
    @EnvironmentObject var app: AppState
    let message: AssistMessage
    @ObservedObject var chat: AssistChat

    var body: some View {
        switch message.role {
        case .user:
            HStack {
                Spacer(minLength: 36)
                Text(message.text)
                    .font(AssistTheme.messageFont)
                    .textSelection(.enabled)
                    .padding(.horizontal, 10).padding(.vertical, 7)
                    .foregroundStyle(.white)
                    .background(AssistTheme.gradient, in: RoundedRectangle(cornerRadius: 12))
            }
        case .assistant:
            HStack(alignment: .top, spacing: 7) {
                Circle().fill(AssistTheme.gradient)
                    .frame(width: 20, height: 20)
                    .overlay(Image(systemName: "sparkles").font(.system(size: 9, weight: .semibold)).foregroundStyle(.white))
                VStack(alignment: .leading, spacing: 8) {
                    if message.thinking {
                        AssistThinking()
                    } else {
                        Text(AssistMarkdown.render(message.text))
                            .font(AssistTheme.messageFont)
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if !message.actions.isEmpty {
                        AssistFlowLayout(spacing: 6) {
                            ForEach(message.actions) { action in actionButton(action) }
                        }
                    }
                }
                .padding(.horizontal, 10).padding(.vertical, 8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
                .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.07)))
            }
        }
    }

    @ViewBuilder private func actionButton(_ action: AssistAction) -> some View {
        let available = chat.isAvailable(action, in: message, app: app)
        let label = (!available && action.isCopy) ? "已複製" : action.label
        let button = Button { chat.perform(action, from: message.id, app: app) } label: {
            Label(label, systemImage: (!available && action.isCopy) ? "checkmark" : action.icon)
                .lineLimit(1)
        }
        .controlSize(.small)
        .disabled(!available)
        if action.prominent {
            button.buttonStyle(.borderedProminent).tint(.indigo)
        } else {
            button.buttonStyle(.bordered)
        }
    }
}

private extension AssistAction {
    var isCopy: Bool { if case .copy = self { return true } else { return false } }
}

/// 三個跳動的點,表示 AI 正在思考
private struct AssistThinking: View {
    @State private var phase = 0
    private let timer = Timer.publish(every: 0.3, on: .main, in: .common).autoconnect()

    var body: some View {
        HStack(spacing: 6) {
            HStack(spacing: 3) {
                ForEach(0..<3) { i in
                    Circle().fill(AssistTheme.accent)
                        .frame(width: 5, height: 5)
                        .opacity(phase == i ? 1 : 0.3)
                }
            }
            Text("分析目前資料中…").font(.callout).foregroundStyle(.secondary)
        }
        .onReceive(timer) { _ in
            withAnimation(.easeInOut(duration: 0.2)) { phase = (phase + 1) % 3 }
        }
    }
}

// MARK: - 輸入框

private struct AssistComposer: View {
    @Binding var text: String
    let busy: Bool
    var focused: FocusState<Bool>.Binding
    let onSend: () -> Void

    private var empty: Bool { text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .bottom, spacing: 6) {
                TextField("問問目前的狀況…", text: $text, axis: .vertical)
                    .textFieldStyle(.plain)
                    .font(AssistTheme.messageFont)
                    .lineLimit(1...5)
                    .focused(focused)
                    .onSubmit(onSend)
                    .padding(.vertical, 2)
                Button(action: onSend) {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.system(size: 20))
                        .foregroundStyle(empty || busy ? AnyShapeStyle(.tertiary) : AnyShapeStyle(AssistTheme.gradient))
                }
                .buttonStyle(.plain)
                .disabled(empty || busy)
                .keyboardShortcut(.return, modifiers: .command)
                .help("送出(Return)")
            }
            .padding(.horizontal, 10).padding(.vertical, 7)
            .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
            .overlay(RoundedRectangle(cornerRadius: 10)
                .strokeBorder(focused.wrappedValue ? AssistTheme.accent.opacity(0.5) : Color.primary.opacity(0.12)))

            HStack(spacing: 4) {
                Image(systemName: "info.circle")
                Text("目前為模擬回應,尚未連接模型")
            }
            .font(.system(size: 11))
            .foregroundStyle(.tertiary)
        }
        .padding(10)
    }
}

// MARK: - 工具

enum AssistMarkdown {
    /// 支援 **粗體** 之類的行內 Markdown,保留換行;解析失敗就顯示原文
    static func render(_ s: String) -> AttributedString {
        let opts = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        return (try? AttributedString(markdown: s, options: opts)) ?? AttributedString(s)
    }
}

/// 會自動換行的水平排列(標籤、按鈕用)
struct AssistFlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let rows = arrange(width: proposal.width ?? .infinity, subviews: subviews)
        let height = rows.reduce(0) { $0 + $1.height } + CGFloat(max(rows.count - 1, 0)) * spacing
        let width = rows.map(\.width).max() ?? 0
        return CGSize(width: proposal.width ?? width, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        for row in arrange(width: bounds.width, subviews: subviews) {
            var x = bounds.minX
            for i in row.items {
                let size = subviews[i].sizeThatFits(ProposedViewSize(width: bounds.width, height: nil))
                subviews[i].place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(width: min(size.width, bounds.width), height: size.height))
                x += min(size.width, bounds.width) + spacing
            }
            y += row.height + spacing
        }
    }

    private struct Row { var items: [Int] = []; var width: CGFloat = 0; var height: CGFloat = 0 }

    private func arrange(width: CGFloat, subviews: Subviews) -> [Row] {
        var rows: [Row] = [Row()]
        for i in subviews.indices {
            let size = subviews[i].sizeThatFits(ProposedViewSize(width: width, height: nil))
            let w = min(size.width, width)
            if !rows[rows.count - 1].items.isEmpty, rows[rows.count - 1].width + spacing + w > width {
                rows.append(Row())
            }
            var row = rows[rows.count - 1]
            row.width += (row.items.isEmpty ? 0 : spacing) + w
            row.height = max(row.height, size.height)
            row.items.append(i)
            rows[rows.count - 1] = row
        }
        return rows.filter { !$0.items.isEmpty }
    }
}
