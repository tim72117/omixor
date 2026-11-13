import SwiftUI

/// 一個頻道的訊息串、發訊息、@ 成員或角色;看到最新訊息時標已讀
struct ThreadView: View {
    @StateObject private var store: ThreadStore
    @EnvironmentObject var directory: Directory
    @EnvironmentObject var session: Session
    let onRead: () -> Void
    @State private var draft = ""

    init(store: ThreadStore, onRead: @escaping () -> Void) {
        _store = StateObject(wrappedValue: store)
        self.onRead = onRead
    }

    private var me: String { "user:" + (session.user?.email ?? "") }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    if store.hasMore {
                        Button("載入更早的訊息") { Task { await store.loadOlder() } }
                            .font(.footnote).frame(maxWidth: .infinity)
                    }
                    if store.loaded && store.messages.isEmpty {
                        Text("還沒有訊息,說點什麼吧。").foregroundStyle(.secondary).frame(maxWidth: .infinity).padding(.top, 40)
                    }
                    ForEach(store.messages) { m in
                        MessageRow(message: m, mine: m.author.caseInsensitiveCompare(me) == .orderedSame).id(m.id)
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .padding(.horizontal)
                .padding(.vertical, 8)
            }
            .defaultScrollAnchor(.bottom)
            .refreshable { await store.refresh() }
            .onChange(of: store.messages.last?.id) { _, _ in
                withAnimation { proxy.scrollTo("bottom", anchor: .bottom) }
                Task { await store.markRead(); onRead() }
            }
        }
        .safeAreaInset(edge: .top) { if let e = store.error { ErrorBanner(message: e) } }
        .safeAreaInset(edge: .bottom) { composer }
        .navigationTitle(store.channel.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                VStack(spacing: 0) {
                    Text(store.channel.name).font(.headline).lineLimit(1)
                    Text("\(store.channel.members.count) 位成員").font(.caption2).foregroundStyle(.secondary)
                }
            }
        }
        .task {
            await store.refresh()
            await store.markRead()
            onRead()
        }
        .foregroundPolling(every: 5) { await store.refresh() }
    }

    private var composer: some View {
        HStack(alignment: .bottom, spacing: 8) {
            Menu {
                Section("提及成員或角色") {
                    ForEach(store.channel.members.filter { $0.caseInsensitiveCompare(me) != .orderedSame }, id: \.self) { node in
                        Button {
                            draft = Mention.insert(Mention.handle(for: node, directory: directory), into: draft)
                        } label: {
                            Label(directory.name(node), systemImage: directory.isAgent(node) ? "sparkles" : "person")
                        }
                    }
                }
            } label: {
                Image(systemName: "at").font(.title3).frame(width: 36, height: 36)
            }
            .accessibilityLabel("提及")
            ZStack(alignment: .topLeading) {
                if draft.isEmpty {
                    Text("傳訊息到 \(store.channel.name)").foregroundStyle(.tertiary).padding(.horizontal, 9).padding(.vertical, 8)
                        .allowsHitTesting(false)
                }
                ChatComposer(text: $draft, placeholder: "訊息", onSubmit: send)
            }
            .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 18))
            Button(action: send) {
                Image(systemName: store.sending ? "hourglass" : "arrow.up.circle.fill").font(.system(size: 30))
            }
            .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || store.sending)
            .accessibilityLabel("送出")
        }
        .padding(.horizontal, 10).padding(.vertical, 6)
        .background(.bar)
    }

    private func send() {
        let text = draft
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, !store.sending else { return }
        draft = ""
        Task {
            if !(await store.send(text)) { draft = text } // 失敗時把字放回去
        }
    }
}

struct MessageRow: View {
    let message: ChannelMessage
    let mine: Bool
    @EnvironmentObject var directory: Directory

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            avatar
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(directory.name(message.author)).font(.subheadline.bold())
                    if directory.isAgent(message.author) { Pill(text: "角色", color: .purple) }
                    Text(message.at.shortLabel).font(.caption2).foregroundStyle(.secondary)
                }
                MarkdownText(message.text, font: .callout)
            }
        }
        .padding(8)
        .background(mine ? Color.accentColor.opacity(0.08) : .clear, in: RoundedRectangle(cornerRadius: 10))
    }

    private var avatar: some View {
        let name = directory.name(message.author)
        return Text(String(name.first ?? "?").uppercased())
            .font(.caption.bold()).foregroundStyle(.white)
            .frame(width: 30, height: 30)
            .background(directory.isAgent(message.author) ? Color.purple : Color.accentColor, in: Circle())
    }
}
