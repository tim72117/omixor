import SwiftUI

/// Issue 詳情:內文與留言(Markdown)、留言
struct IssueDetailView: View {
    @ObservedObject var store: IssuesStore
    @EnvironmentObject var directory: Directory
    let id: String
    @State private var draft = ""
    @State private var sending = false
    @FocusState private var focused: Bool

    var body: some View {
        ScrollView {
            if let i = store.issue(id) {
                VStack(alignment: .leading, spacing: 14) {
                    header(i)
                    if !i.body.isEmpty {
                        MarkdownText(i.body)
                    } else {
                        Text("沒有內容").foregroundStyle(.secondary)
                    }
                    Divider()
                    Text("留言(\(i.comments.count))").font(.headline)
                    if i.comments.isEmpty { Text("還沒有留言").font(.callout).foregroundStyle(.secondary) }
                    ForEach(i.comments) { c in
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                Text(directory.name(c.author)).font(.subheadline.bold())
                                if let at = c.at { Text(at.shortLabel).font(.caption2).foregroundStyle(.secondary) }
                            }
                            MarkdownText(c.text, font: .callout)
                        }
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 10))
                    }
                }
                .padding()
            } else {
                ProgressView().padding(.top, 60)
            }
        }
        .safeAreaInset(edge: .top) { if let e = store.error { ErrorBanner(message: e) } }
        .safeAreaInset(edge: .bottom) { composer }
        .navigationTitle(id)
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await store.reload(id) }
        .task { await store.reload(id) }
        .foregroundPolling(every: 20) { await store.reload(id) }
    }

    private func header(_ i: Issue) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(i.title).font(.title3.bold())
            HStack(spacing: 6) {
                Pill(text: i.isOpen ? "未關閉" : "已關閉", color: i.isOpen ? .green : .purple)
                Pill(text: "優先:\(i.priorityLabel)", color: i.priority == "urgent" ? .red : i.priority == "high" ? .orange : .secondary)
                if !i.assignee.isEmpty { Pill(text: "指派:\(directory.name("agent:" + i.assignee))", color: .indigo, icon: "sparkles") }
            }
            if !i.labels.isEmpty {
                HStack(spacing: 4) { ForEach(i.labels, id: \.self) { Pill(text: $0, color: .blue) } }
            }
            Text([directory.name(i.createdBy).isEmpty ? nil : "\(directory.name(i.createdBy)) 建立",
                  i.createdAt.map { $0.shortLabel }, i.updatedAt.map { "更新 " + $0.shortLabel }]
                .compactMap { $0 }.joined(separator: " · "))
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private var composer: some View {
        HStack(alignment: .bottom, spacing: 8) {
            TextField("留言(支援 Markdown)", text: $draft, axis: .vertical)
                .lineLimit(1...6)
                .focused($focused)
                .padding(.horizontal, 12).padding(.vertical, 8)
                .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 18))
            Button {
                let text = draft
                sending = true
                Task {
                    if await store.comment(id, text: text) { draft = ""; focused = false }
                    sending = false
                }
            } label: {
                if sending { ProgressView().frame(width: 30, height: 30) } else { Image(systemName: "arrow.up.circle.fill").font(.system(size: 30)) }
            }
            .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || sending)
            .accessibilityLabel("送出留言")
        }
        .padding(.horizontal, 10).padding(.vertical, 6)
        .background(.bar)
    }
}
