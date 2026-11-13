import Foundation

/// 頻道清單(我看得到的頻道,附未讀數)
@MainActor
final class ChannelsStore: ObservableObject {
    @Published private(set) var channels: [ChannelSummary] = []
    @Published var error: String?
    @Published private(set) var loaded = false
    let session: Session
    let org: String

    init(session: Session, org: String) { self.session = session; self.org = org }

    var totalUnread: Int { channels.reduce(0) { $0 + $1.unread } }

    /// 有新訊息的在前
    nonisolated static func sorted(_ l: [ChannelSummary]) -> [ChannelSummary] {
        l.sorted { ($0.lastMessageAt ?? $0.updatedAt ?? .distantPast) > ($1.lastMessageAt ?? $1.updatedAt ?? .distantPast) }
    }

    func refresh() async {
        guard let api = session.api else { return }
        do {
            channels = Self.sorted(try await api.channels(org: org))
            error = nil
        } catch is CancellationError {
        } catch {
            self.error = session.handle(error)
        }
        loaded = true
    }

    /// 標已讀後先在本機把未讀歸零(下次更新會再對齊伺服器)
    func clearUnread(_ id: String) {
        if let i = channels.firstIndex(where: { $0.id == id }) { channels[i].unread = 0 }
    }
}

/// 一個頻道的訊息串:先載最新一頁,之後只拿新的;往上捲可以載更早的
@MainActor
final class ThreadStore: ObservableObject {
    @Published private(set) var messages: [ChannelMessage] = []
    @Published private(set) var hasMore = false
    @Published var error: String?
    @Published private(set) var loaded = false
    @Published var sending = false
    let session: Session
    let org: String
    let channel: ChannelSummary

    init(session: Session, org: String, channel: ChannelSummary) {
        self.session = session; self.org = org; self.channel = channel
    }

    /// 合併(依 id 去重、照號碼排序)
    nonisolated static func merge(_ a: [ChannelMessage], _ b: [ChannelMessage]) -> [ChannelMessage] {
        var seen = Set<String>()
        return (a + b).filter { seen.insert($0.id).inserted }.sorted { $0.number < $1.number }
    }

    func refresh() async {
        guard let api = session.api else { return }
        do {
            if let last = messages.last {
                var since = last.id
                // 一次最多 200 則;多的話繼續拿
                for _ in 0..<5 {
                    let page = try await api.messages(org: org, channel: channel.id, since: since, limit: 200)
                    messages = Self.merge(messages, page.messages)
                    guard page.hasMore, let l = page.messages.last else { break }
                    since = l.id
                }
            } else {
                let page = try await api.messages(org: org, channel: channel.id)
                messages = page.messages
                hasMore = page.hasMore
            }
            error = nil
        } catch is CancellationError {
        } catch {
            self.error = session.handle(error)
        }
        loaded = true
    }

    func loadOlder() async {
        guard let api = session.api, let first = messages.first, hasMore else { return }
        do {
            let page = try await api.messages(org: org, channel: channel.id, before: first.id)
            messages = Self.merge(page.messages, messages)
            hasMore = page.hasMore
        } catch is CancellationError {
        } catch {
            self.error = session.handle(error)
        }
    }

    /// 標已讀到最新一則
    func markRead() async {
        guard let api = session.api, let last = messages.last else { return }
        try? await api.markRead(org: org, channel: channel.id, upTo: last.id)
    }

    /// 發訊息;成功回 true
    func send(_ text: String) async -> Bool {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty, let api = session.api else { return false }
        sending = true
        defer { sending = false }
        do {
            let m = try await api.postMessage(org: org, channel: channel.id, text: t)
            messages = Self.merge(messages, [m])
            error = nil
            await markRead()
            return true
        } catch {
            self.error = session.handle(error)
            return false
        }
    }
}
