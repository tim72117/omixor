import SwiftUI

/// Issue:清單、搜尋、篩選
struct IssueListView: View {
    @ObservedObject var store: IssuesStore
    @EnvironmentObject var session: Session
    @State private var query = ""
    @State private var filter: IssuesStore.Filter = .open
    @State private var path: [String] = []

    var body: some View {
        let rows = IssuesStore.filter(store.issues, query: query, status: filter)
        NavigationStack(path: $path) {
            List {
                if let e = store.error { ErrorBanner(message: e).listRowInsets(EdgeInsets()) }
                Picker("狀態", selection: $filter) {
                    ForEach(IssuesStore.Filter.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                .listRowSeparator(.hidden)
                if store.loaded && rows.isEmpty {
                    ContentUnavailableView(query.isEmpty ? "沒有 Issue" : "找不到符合的 Issue", systemImage: "magnifyingglass")
                }
                ForEach(rows) { i in
                    NavigationLink(value: i.id) { IssueRow(issue: i) }
                }
            }
            .listStyle(.plain)
            .navigationTitle("Issue")
            .searchable(text: $query, prompt: "搜尋編號、標題、內容、標籤")
            .navigationDestination(for: String.self) { id in IssueDetailView(store: store, id: id) }
            .refreshable { await store.refresh() }
            .task {
                await store.refresh()
                if path.isEmpty, let id = DebugLaunch.openId, store.issue(id) != nil { path = [id] }
            }
            .foregroundPolling(every: 30) { await store.refresh() }
            .overlay { if !store.loaded { ProgressView() } }
        }
    }
}

struct IssueRow: View {
    let issue: Issue

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Image(systemName: issue.isOpen ? "circle" : "checkmark.circle.fill")
                    .foregroundStyle(issue.isOpen ? .green : .purple).font(.caption)
                Text(issue.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                if issue.priority == "urgent" || issue.priority == "high" {
                    Pill(text: issue.priorityLabel, color: issue.priority == "urgent" ? .red : .orange)
                }
                Spacer()
                if let u = issue.updatedAt { Text(u.shortLabel).font(.caption2).foregroundStyle(.secondary) }
            }
            Text(issue.title).font(.body).lineLimit(2)
            HStack(spacing: 4) {
                ForEach(issue.labels.prefix(3), id: \.self) { Pill(text: $0, color: .blue) }
                if !issue.comments.isEmpty {
                    Label("\(issue.comments.count)", systemImage: "bubble.left").font(.caption2).foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 2)
    }
}
