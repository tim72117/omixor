import SwiftUI

/// 待決定分頁:自己的 NavigationStack,內容是 DecisionListView
struct DecisionsView: View {
    @ObservedObject var store: DecisionsStore

    var body: some View {
        NavigationStack { DecisionListView(store: store) }
    }
}

/// 待決定清單(待處理 / 已處理);issue 非空 = 只看那張來源 Issue 的(從 Issue 詳情進來)。要放在 NavigationStack 裡
struct DecisionListView: View {
    @ObservedObject var store: DecisionsStore
    var issue = ""
    @State private var filter: DecisionsStore.Filter = .open

    var body: some View {
        let rows = DecisionsStore.rows(store.decisions, filter: filter, issue: issue)
        List {
            if let e = store.error { ErrorBanner(message: e).listRowInsets(EdgeInsets()) }
            Picker("狀態", selection: $filter) {
                ForEach(DecisionsStore.Filter.allCases) { Text($0.label).tag($0) }
            }
            .pickerStyle(.segmented)
            .listRowSeparator(.hidden)
            if store.loaded && rows.isEmpty { empty }
            ForEach(rows) { d in
                // 用目的地連結(不註冊 String 的 navigationDestination):從 Issue 詳情進來時外層已有 Issue 的
                NavigationLink { DecisionDetailView(store: store, id: d.id) } label: { DecisionRow(decision: d) }
            }
        }
        .listStyle(.plain)
        .navigationTitle(issue.isEmpty ? "待決定" : "\(issue) 的待決定")
        .navigationBarTitleDisplayMode(issue.isEmpty ? .large : .inline)
        .refreshable { await store.refresh() }
        .task { await store.refresh() }
        .foregroundPolling(every: 20) { await store.refresh() }
        .overlay { if !store.loaded { ProgressView() } }
    }

    @ViewBuilder private var empty: some View {
        if filter == .open {
            ContentUnavailableView("沒有待處理的項目", systemImage: "checkmark.circle",
                                   description: Text("AI 或同事提出需要你選擇、處理的事,會出現在這裡。"))
        } else {
            ContentUnavailableView("還沒有處理過的項目", systemImage: "tray")
        }
    }
}

extension Decision {
    var color: Color { isHelp ? .orange : .indigo }
    var icon: String { isHelp ? "hand.raised.fill" : "questionmark.bubble.fill" }
    var statusColor: Color { status == "open" ? .orange : status == "decided" ? .green : .secondary }
}

/// 一列:需要協助(只有標題與背景)與待決定(多一行選項摘要)的版面不同
struct DecisionRow: View {
    let decision: Decision

    var body: some View {
        let d = decision
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Image(systemName: d.icon).foregroundStyle(d.color).font(.caption)
                Text(d.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                if !d.sourceIssue.isEmpty { Pill(text: d.sourceIssue, color: .blue, icon: "exclamationmark.circle") }
                Spacer()
                if let at = (d.isOpen ? d.createdAt : d.decidedAt) { Text(at.shortLabel).font(.caption2).foregroundStyle(.secondary) }
            }
            Text(d.title).lineLimit(2).foregroundStyle(d.isOpen ? .primary : .secondary)
            if d.isHelp {
                Text(d.isOpen ? "需要你處理" : "\(d.statusLabel):\(d.answerLabel)")
                    .font(.caption).foregroundStyle(d.isOpen ? Color.orange : .secondary)
            } else if d.isOpen, !d.optionSummary.isEmpty {
                Text(d.optionSummary).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            } else if !d.isOpen {
                Text("\(d.statusLabel):\(d.answerLabel)").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }
}
