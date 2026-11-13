import SwiftUI

/// 對話:頻道清單與未讀
struct ChannelListView: View {
    @ObservedObject var store: ChannelsStore
    @EnvironmentObject var session: Session
    @State private var path: [ChannelSummary] = []

    var body: some View {
        NavigationStack(path: $path) {
            List {
                if let e = store.error { ErrorBanner(message: e).listRowInsets(EdgeInsets()) }
                if store.loaded && store.channels.isEmpty {
                    ContentUnavailableView("還沒有頻道", systemImage: "bubble.left.and.bubble.right",
                                           description: Text("在 Mac App 或網頁版建立頻道並把你加進去,就會出現在這裡。"))
                }
                ForEach(store.channels) { c in
                    NavigationLink(value: c) { ChannelRow(channel: c) }
                }
            }
            .listStyle(.plain)
            .navigationTitle(session.org?.name ?? "對話")
            .navigationDestination(for: ChannelSummary.self) { c in
                ThreadView(store: ThreadStore(session: session, org: store.org, channel: c), onRead: { store.clearUnread(c.id) })
            }
            .refreshable { await store.refresh() }
            .task {
                await store.refresh()
                if path.isEmpty, let id = DebugLaunch.openId, let c = store.channels.first(where: { $0.id == id }) { path = [c] }
            }
            .foregroundPolling(every: 15) { await store.refresh() }
            .overlay { if !store.loaded { ProgressView() } }
        }
    }
}

struct ChannelRow: View {
    let channel: ChannelSummary

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: channel.issueId.isEmpty ? "number" : "exclamationmark.circle")
                .foregroundStyle(.secondary).frame(width: 22)
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(channel.name).font(channel.unread > 0 ? .body.bold() : .body).lineLimit(1)
                    if !channel.issueId.isEmpty { Pill(text: channel.issueId, color: .blue) }
                }
                if !channel.topic.isEmpty {
                    Text(channel.topic).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 4) {
                if let at = channel.lastMessageAt { Text(at.shortLabel).font(.caption2).foregroundStyle(.secondary) }
                if channel.unread > 0 {
                    Text("\(channel.unread)").font(.caption2.bold()).foregroundStyle(.white)
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Color.accentColor, in: Capsule())
                        .accessibilityLabel("\(channel.unread) 則未讀")
                }
            }
        }
        .padding(.vertical, 2)
    }
}
