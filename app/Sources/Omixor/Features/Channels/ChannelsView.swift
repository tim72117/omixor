import SwiftUI

/// 對話:左邊頻道清單(未讀數)、右邊訊息串與輸入框。人與虛擬角色在同一個頻道對話;@ 角色只通知、不排任務。
/// 本機存在 channels.json,遠端在 omixor server(規則見 Channels/Channels.swift)。每 5 秒或有變動時更新。
struct ChannelsView: View {
    @EnvironmentObject var app: AppState
    @State private var selected: String?
    @State private var creating = false
    @State private var query = ""

    private var store: ChannelStore { app.startChannels() }

    private var list: [ChannelSummary] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return app.channels }
        return app.channels.filter {
            $0.name.lowercased().contains(q) || $0.channel.topic.lowercased().contains(q)
                || $0.channel.issueId.lowercased().contains(q) || $0.id.lowercased().contains(q)
        }
    }

    var body: some View {
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                HStack {
                    TextField("搜尋頻道", text: $query).textFieldStyle(.roundedBorder)
                    Button { creating = true } label: { Image(systemName: "plus") }
                        .help("新增頻道")
                }
                .padding(10)
                Divider()
                if let e = store.remoteError { RemoteErrorBar(message: e) }
                if list.isEmpty {
                    EmptyStateView(title: app.channels.isEmpty ? "還沒有頻道" : "沒有符合的頻道", icon: "bubble.left.and.bubble.right",
                                   message: app.channels.isEmpty ? "按 + 建立頻道,把人和角色拉進來一起討論;Issue 的「開對話頻道」也會建立綁該 Issue 的頻道。" : nil)
                } else {
                    List(selection: $selected) {
                        ForEach(list) { ChannelRow(summary: $0).tag($0.id) }
                    }
                    .listStyle(.inset)
                }
            }
            .frame(width: 280)
            Divider()
            Group {
                if let id = selected, let s = app.channels.first(where: { $0.id == id }) {
                    ChannelDetail(summary: s).id(s.id)
                } else {
                    EmptyStateView(title: "選一個頻道", icon: "bubble.left.and.bubble.right",
                                   message: "在頻道裡 @ 角色只會通知它(不排任務),角色下次執行時就看得到。")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear {
            app.startIssues()
            consumeFocus()
            if selected == nil { selected = app.channels.first?.id }
        }
        // 打開頁面時在背景重讀一次;之後的定時重讀由 ChannelStore 自己做(這裡不另外輪詢)
        .task {
            await store.reload()
            if selected == nil { selected = app.channels.first?.id }
        }
        .onChange(of: app.focusId) { consumeFocus() }
        .onChange(of: app.currentOrgId) { selected = app.channels.first?.id }
        .sheet(isPresented: $creating) {
            NewChannelSheet { selected = $0 }
        }
    }

    /// 其他頁跳過來時:focusId 是頻道 id(CH-n)
    private func consumeFocus() {
        guard let f = app.focusId, f.uppercased().hasPrefix("CH-") else { return }
        if !app.channels.contains(where: { $0.id == f }) { Task { await store.reload() } }
        selected = app.channels.first { $0.id.caseInsensitiveCompare(f) == .orderedSame }?.id ?? f
        app.focusId = nil
    }
}

private struct ChannelRow: View {
    @EnvironmentObject var app: AppState
    let summary: ChannelSummary

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: summary.channel.hasIssue ? "exclamationmark.bubble" : "number")
                .foregroundStyle(.secondary).frame(width: 16)
            VStack(alignment: .leading, spacing: 2) {
                Text(summary.name).lineLimit(1).fontWeight(summary.unread > 0 ? .semibold : .regular)
                HStack(spacing: 6) {
                    if summary.channel.hasIssue { Text(summary.channel.issueId).font(.caption.monospaced()) }
                    Text("\(summary.channel.members.count) 位成員")
                    if let t = summary.lastMessageAt { Text(t, format: .relative(presentation: .named)) }
                }
                .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer(minLength: 4)
            if summary.unread > 0 {
                Text("\(summary.unread)").font(.caption.weight(.bold)).foregroundStyle(.white)
                    .padding(.horizontal, 6).padding(.vertical, 1)
                    .background(Color.accentColor, in: Capsule())
                    .help("\(summary.unread) 則未讀")
            }
        }
        .padding(.vertical, 3)
    }
}

// MARK: - 訊息串

private struct ChannelDetail: View {
    @EnvironmentObject var app: AppState
    let summary: ChannelSummary
    @State private var messages: [ChannelMessage] = []
    @State private var hasMore = false
    @State private var draft = ""
    @State private var error: String?
    @State private var showSettings = false
    @State private var loaded = false
    @FocusState private var composerFocused: Bool

    private var store: ChannelStore { app.startChannels() }
    private var channel: Channel { summary.channel }
    private var me: String { store.meNode }
    private var canPost: Bool { channel.members.contains(me) }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        if hasMore {
                            Button("載入更早的訊息") { loadOlder() }
                                .buttonStyle(.link)
                                .frame(maxWidth: .infinity)
                        }
                        if loaded, messages.isEmpty {
                            Text("還沒有訊息。打個招呼,或用 @ 提到角色。").foregroundStyle(.secondary)
                                .frame(maxWidth: .infinity).padding(.top, 40)
                        }
                        ForEach(messages) { MessageRow(message: $0, me: me).id($0.id) }
                    }
                    .padding(16)
                    .frame(maxWidth: 900, alignment: .leading)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .onChange(of: messages.last?.id) {
                    guard let id = messages.last?.id else { return }
                    withAnimation { proxy.scrollTo(id, anchor: .bottom) }
                }
                .onAppear { if let id = messages.last?.id { proxy.scrollTo(id, anchor: .bottom) } }
            }
            Divider()
            composer
        }
        .onAppear { reload(); composerFocused = true }
        .onChange(of: app.channelsVersion) { reload() }
        .task(id: summary.id) {
            // 每 5 秒讀新訊息(core server)
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                reload()
            }
        }
        .sheet(isPresented: $showSettings) { ChannelSettingsSheet(summary: summary) }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(channel.name).font(.title3.weight(.semibold)).lineLimit(1)
                    Text(channel.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                    if channel.hasIssue {
                        Button { app.go(.development, focus: channel.issueId) } label: {
                            Pill(text: channel.issueId, color: .blue, icon: "exclamationmark.bubble")
                        }
                        .buttonStyle(.plain)
                        .help("打開 Issue \(channel.issueId)")
                    }
                }
                if !channel.topic.isEmpty {
                    Text(channel.topic).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                }
            }
            Spacer()
            HStack(spacing: -4) {
                ForEach(channel.members.prefix(8), id: \.self) { m in
                    AuthorIcon(author: ChannelStore.iconAuthor(m), size: 22)
                        .help(store.name(of: m))
                }
                if channel.members.count > 8 {
                    Text("+\(channel.members.count - 8)").font(.caption).foregroundStyle(.secondary).padding(.leading, 8)
                }
            }
            Button { showSettings = true } label: { Image(systemName: "gearshape") }
                .help("頻道設定:名稱、主題、成員")
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
    }

    // MARK: 輸入框

    /// 正在輸入的 @ 後面的文字(游標視為在最後);沒有在輸入 @ 時是 nil
    private var mentionQuery: String? {
        guard let at = draft.lastIndex(of: "@") else { return nil }
        if at != draft.startIndex, ChannelMention.isAlnum(draft[draft.index(before: at)]) { return nil }
        let q = String(draft[draft.index(after: at)...])
        guard !q.contains(where: { $0.isWhitespace }) else { return nil }
        return q
    }

    private var suggestions: [String] {
        guard let q = mentionQuery?.lowercased() else { return [] }
        return channel.members.filter { m in
            m != me && (q.isEmpty || store.name(of: m).lowercased().contains(q) || m.lowercased().contains(q))
        }
        .prefix(8).map { $0 }
    }

    private func insertMention(_ node: String) {
        guard let at = draft.lastIndex(of: "@") else { return }
        let label = ChainNode.isAgent(node) ? store.name(of: node) : (ChainNode.userId(node) ?? node)
        draft = String(draft[..<at]) + "@" + label + " "
        composerFocused = true
    }

    private var composer: some View {
        VStack(alignment: .leading, spacing: 6) {
            if !suggestions.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        ForEach(suggestions, id: \.self) { m in
                            Button { insertMention(m) } label: {
                                HStack(spacing: 4) {
                                    AuthorIcon(author: ChannelStore.iconAuthor(m), size: 16)
                                    Text(store.name(of: m))
                                }
                                .padding(.horizontal, 8).padding(.vertical, 3)
                                .background(.quaternary.opacity(0.6), in: Capsule())
                            }
                            .buttonStyle(.plain)
                        }
                        Text("Tab 選第一個").font(.caption2).foregroundStyle(.secondary)
                    }
                }
            }
            if let e = error { Text(e).font(.caption).foregroundStyle(.red).lineLimit(2) }
            if canPost {
                HStack(alignment: .bottom, spacing: 8) {
                    TextField("在「\(channel.name)」發訊息(Enter 送出、Shift+Enter 換行、@ 提到成員)", text: $draft, axis: .vertical)
                        .textFieldStyle(.plain)
                        .lineLimit(1...8)
                        .focused($composerFocused)
                        .padding(8)
                        .background(RoundedRectangle(cornerRadius: 8).strokeBorder(.quaternary))
                        .onKeyPress(.return, phases: .down) { press in
                            // 注音等輸入法選字中(還有組字中的文字):Enter 是確認選字,交給輸入法,不送出也不換行
                            if Self.isComposing { return .ignored }
                            if press.modifiers.contains(.shift) || press.modifiers.contains(.option) {
                                draft += "\n"
                                return .handled
                            }
                            send()
                            return .handled
                        }
                        .onKeyPress(.tab, phases: .down) { _ in
                            guard let first = suggestions.first else { return .ignored }
                            insertMention(first)
                            return .handled
                        }
                        .onSubmit { if !Self.isComposing { send() } }
                    Button("送出", action: send)
                        .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            } else {
                Label("你不是這個頻道的成員,只能閱讀;要發訊息請在頻道設定把自己加進來。", systemImage: "eye")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(12)
    }

    /// 輸入框正在用輸入法組字(注音、拼音選字中):這時的 Enter 是確認選字
    static var isComposing: Bool {
        (NSApp.keyWindow?.firstResponder as? NSTextView)?.hasMarkedText() ?? false
    }

    // MARK: 讀寫

    private func send() {
        let t = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return }
        do {
            let m = try store.post(channel.id, text: t)
            draft = ""
            error = nil
            if !messages.contains(where: { $0.id == m.id }) { messages.append(m) }
        } catch { self.error = error.localizedDescription }
    }

    /// 第一次讀最新的 50 則;之後只讀最後一則之後的。有新訊息就標已讀
    private func reload() {
        do {
            var fresh = false
            if let last = messages.last {
                let p = try store.messages(channel.id, since: last.id, limit: ChannelStore.maxLimit)
                let known = Set(messages.map(\.id))
                let add = p.messages.filter { !known.contains($0.id) }
                if !add.isEmpty { messages += add; fresh = true }
            } else {
                let p = try store.messages(channel.id)
                messages = p.messages
                hasMore = p.hasMore
                fresh = !p.messages.isEmpty
            }
            loaded = true
            error = nil
            if fresh || summary.unread > 0 { _ = try? store.markRead(channel.id) }
        } catch { self.error = error.localizedDescription }
    }

    private func loadOlder() {
        guard let first = messages.first else { return }
        do {
            let p = try store.messages(channel.id, before: first.id)
            let known = Set(messages.map(\.id))
            messages = p.messages.filter { !known.contains($0.id) } + messages
            hasMore = p.hasMore
        } catch { self.error = error.localizedDescription }
    }
}

private struct MessageRow: View {
    @EnvironmentObject var app: AppState
    let message: ChannelMessage
    let me: String

    private var store: ChannelStore { app.startChannels() }

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            AuthorIcon(author: ChannelStore.iconAuthor(message.author), size: 28)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(store.name(of: message.author)).font(.callout.weight(.semibold))
                    if ChainNode.isAgent(message.author) { Pill(text: "角色", color: .purple) }
                    Text(message.at, format: .dateTime.month().day().hour().minute()).font(.caption).foregroundStyle(.secondary)
                }
                MarkdownText(message.text)
                if !message.mentions.isEmpty {
                    HStack(spacing: 4) {
                        ForEach(message.mentions, id: \.self) { m in
                            Pill(text: "@" + store.name(of: m), color: m == me ? .orange : .purple)
                        }
                    }
                }
            }
            Spacer(minLength: 0)
        }
    }
}

// MARK: - 成員選項

/// 可以加進頻道的節點:角色與組織成員(server 上的)
@MainActor
private func memberOptions(_ app: AppState, remote members: [RemoteMember]) -> [String] {
    var out = app.agents.filter(\.active).map { ChainNode.agent($0.id) }
    out += members.map { ChainNode.user($0.email) }
    var seen = Set<String>()
    return out.filter { seen.insert($0).inserted }
}

@MainActor
private func loadRemoteMembers(_ app: AppState) -> [RemoteMember] {
    guard let r = app.channelStore?.remote else { return [] }
    return (try? r.listMembers(org: app.currentOrgId)) ?? []
}

private struct MemberToggleList: View {
    @EnvironmentObject var app: AppState
    let options: [String]
    @Binding var picked: Set<String>
    var locked: Set<String> = []

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 4) {
                ForEach(options, id: \.self) { n in
                    Toggle(isOn: Binding(get: { picked.contains(n) || locked.contains(n) },
                                         set: { on in if on { picked.insert(n) } else { picked.remove(n) } })) {
                        HStack(spacing: 6) {
                            AuthorIcon(author: ChannelStore.iconAuthor(n), size: 16)
                            Text(app.startChannels().name(of: n))
                            Text(ChainNode.isAgent(n) ? "角色" : (ChainNode.userId(n) ?? "")).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    .disabled(locked.contains(n))
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(height: 180)
    }
}

// MARK: - 新增頻道

private struct NewChannelSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let onCreate: (String) -> Void
    @State private var name = ""
    @State private var topic = ""
    @State private var issueId = ""
    @State private var picked: Set<String> = []
    @State private var members: [RemoteMember] = []
    @State private var error: String?

    private var store: ChannelStore { app.startChannels() }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("新增頻道").font(.title3.weight(.semibold))
            TextField("名稱(最多 80 字)", text: $name).textFieldStyle(.roundedBorder)
            TextField("主題(選填)", text: $topic).textFieldStyle(.roundedBorder)
            Picker("綁 Issue", selection: $issueId) {
                Text("不綁").tag("")
                ForEach(app.issues.filter { $0.status == .open }) { i in Text("\(i.id) \(i.title)").lineLimit(1).tag(i.id) }
            }
            Text("成員(你自己會自動加入)").font(.caption).foregroundStyle(.secondary)
            MemberToggleList(options: memberOptions(app, remote: members), picked: $picked, locked: [store.meNode])
            if let e = error { Text(e).font(.caption).foregroundStyle(.red) }
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("建立") {
                    do {
                        let r = try store.create(name: name, topic: topic, issueId: issueId,
                                                 members: picked.filter { $0 != store.meNode }.sorted())
                        onCreate(r.channel.id)
                        dismiss()
                    } catch { self.error = error.localizedDescription }
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 520)
        .onAppear {
            app.startIssues()
            members = loadRemoteMembers(app)
        }
    }
}

// MARK: - 頻道設定

private struct ChannelSettingsSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let summary: ChannelSummary
    @State private var name = ""
    @State private var topic = ""
    @State private var picked: Set<String> = []
    @State private var extra = ""
    @State private var members: [RemoteMember] = []
    @State private var error: String?

    private var store: ChannelStore { app.startChannels() }

    private var options: [String] {
        var out = summary.channel.members + memberOptions(app, remote: members)
        out += picked
        var seen = Set<String>()
        return out.filter { seen.insert($0).inserted }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("頻道設定").font(.title3.weight(.semibold))
            TextField("名稱", text: $name).textFieldStyle(.roundedBorder)
            TextField("主題", text: $topic).textFieldStyle(.roundedBorder)
            Text("成員(勾選 = 在頻道裡)").font(.caption).foregroundStyle(.secondary)
            MemberToggleList(options: options, picked: $picked)
            HStack {
                TextField("用 email 或角色 id 加人", text: $extra).textFieldStyle(.roundedBorder)
                Button("加入") {
                    let s = extra.trimmingCharacters(in: .whitespaces)
                    guard !s.isEmpty else { return }
                    picked.insert(ChannelStore.memberInput(s))
                    extra = ""
                    error = nil
                }
            }
            if let e = error { Text(e).font(.caption).foregroundStyle(.red) }
            HStack {
                Text("角色身分不能改成員;組織管理員看得到所有頻道,但要是成員才能發訊息。").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("取消") { dismiss() }
                Button("儲存") { save() }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 560)
        .onAppear {
            name = summary.channel.name
            topic = summary.channel.topic
            picked = Set(summary.channel.members)
            members = loadRemoteMembers(app)
        }
    }

    private func save() {
        do {
            let c = summary.channel
            let n = name.trimmingCharacters(in: .whitespaces), t = topic.trimmingCharacters(in: .whitespaces)
            if n != c.name || t != c.topic {
                try store.update(c.id, name: n != c.name ? n : nil, topic: t != c.topic ? t : nil)
            }
            let add = picked.subtracting(c.members).sorted()
            let remove = Set(c.members).subtracting(picked).sorted()
            if !add.isEmpty || !remove.isEmpty { try store.changeMembers(c.id, add: add, remove: remove) }
            dismiss()
        } catch { self.error = error.localizedDescription }
    }
}
