import SwiftUI

/// 一筆待決定的詳情:背景、選項、處理紀錄;待處理的可以回答(DecisionAnswerSheet)
struct DecisionDetailView: View {
    @ObservedObject var store: DecisionsStore
    let id: String
    @State private var answering = false

    var body: some View {
        ScrollView {
            if let d = store.decision(id) {
                VStack(alignment: .leading, spacing: 14) {
                    header(d)
                    if d.body.isEmpty { Text("沒有內文").foregroundStyle(.secondary) } else { MarkdownText(d.body) }
                    if !d.isHelp && !d.options.isEmpty { optionsSection(d) }
                    Divider()
                    record(d)
                }
                .padding()
            } else {
                ContentUnavailableView("找不到這筆待決定", systemImage: "questionmark.bubble",
                                       description: Text("可能已被移除,或你看不到它。"))
                    .padding(.top, 60)
            }
        }
        .safeAreaInset(edge: .top) { if let e = store.error { ErrorBanner(message: e) } }
        .safeAreaInset(edge: .bottom) {
            if let d = store.decision(id), d.isOpen { answerButton(d) }
        }
        .navigationTitle(id)
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await store.refresh() }
        .foregroundPolling(every: 20) { await store.refresh() }
        .sheet(isPresented: $answering) {
            if let d = store.decision(id) { DecisionAnswerSheet(store: store, decision: d) }
        }
    }

    private func header(_ d: Decision) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(d.title).font(.title3.bold())
            HStack(spacing: 6) {
                Pill(text: d.kindLabel, color: d.color, icon: d.icon)
                Pill(text: d.statusLabel, color: d.statusColor)
                if !d.sourceIssue.isEmpty { Pill(text: d.sourceIssue, color: .blue, icon: "exclamationmark.circle") }
                ForEach(d.blocks, id: \.self) { Pill(text: "擋住:\($0)", icon: "hand.raised") }
            }
            Text("\(d.createdByName) 提出" + (d.createdAt.map { " · " + $0.shortLabel } ?? ""))
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func optionsSection(_ d: Decision) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("選項").font(.headline)
            ForEach(d.options) { o in
                let picked = d.status == "decided" && d.answer == o.id
                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 6) {
                        Text(o.label).font(.subheadline.bold())
                        if o.recommended { Pill(text: "建議", color: .green, icon: "star.fill") }
                        if picked { Pill(text: "已選", color: .blue, icon: "checkmark") }
                    }
                    if !o.description.isEmpty { Text(o.description).font(.caption).foregroundStyle(.secondary) }
                }
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(picked ? Color.blue.opacity(0.1) : Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 10))
            }
        }
    }

    @ViewBuilder private func record(_ d: Decision) -> some View {
        Text("處理紀錄").font(.headline)
        switch d.status {
        case "open": Text("還沒有人處理。").font(.callout).foregroundStyle(.secondary)
        case "withdrawn": Text("已撤回,沒有處理紀錄。").font(.callout).foregroundStyle(.secondary)
        default:
            VStack(alignment: .leading, spacing: 6) {
                Text(d.answerLabel).font(.subheadline.bold())
                Text("\(d.decidedByName)" + (d.decidedAt.map { " · " + $0.shortLabel } ?? "")).font(.caption).foregroundStyle(.secondary)
                if !d.answerNote.isEmpty { labeled("說明", d.answerNote) }
                if !d.evidence.isEmpty { labeled("證據", d.evidence) }
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 10))
            if !d.sourceIssue.isEmpty { Text("結論已寫成來源 Issue 的留言。").font(.caption).foregroundStyle(.secondary) }
        }
    }

    private func labeled(_ title: String, _ text: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(text).font(.callout).textSelection(.enabled)
        }
    }

    private func answerButton(_ d: Decision) -> some View {
        Button { answering = true } label: {
            Label(d.isHelp ? "回報處理結果" : "回答", systemImage: "checkmark.circle").frame(maxWidth: .infinity)
        }
        .buttonStyle(.borderedProminent)
        .controlSize(.large)
        .padding(.horizontal).padding(.vertical, 8)
        .background(.bar)
    }
}
