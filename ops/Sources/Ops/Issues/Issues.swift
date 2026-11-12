import Foundation
import SwiftUI

// Issue:要追蹤的問題或待辦,存在 ~/Library/Application Support/Ops/issues.json(OPS_DATA_DIR 可改);
// 設了 Issue 伺服器(IssueRemote.swift)時改存在 Go 版 ops-web。
// 和開發頁的看板範例資料不同,這裡是真實資料,重開 app 也還在。AI 可以透過 MCP 建立與更新。

enum IssueStatus: String, CaseIterable, Identifiable, Codable {
    case open, closed
    var id: String { rawValue }
    var label: String { self == .open ? "未解決" : "已關閉" }
}

enum IssuePriority: String, CaseIterable, Identifiable, Codable, Comparable {
    case urgent, high, normal, low
    var id: String { rawValue }
    var label: String {
        switch self {
        case .urgent: return "緊急"
        case .high: return "高"
        case .normal: return "一般"
        case .low: return "低"
        }
    }
    var color: Color {
        switch self {
        case .urgent: return .red
        case .high: return .orange
        case .normal: return .blue
        case .low: return .gray
        }
    }
    private var rank: Int { Self.allCases.firstIndex(of: self) ?? 0 }
    static func < (a: Self, b: Self) -> Bool { a.rank < b.rank }
}

struct IssueComment: Codable, Hashable, Identifiable {
    var id = UUID()
    var author: String   // person id、"mcp" 或 "agent:<角色 id>"
    var text: String
    var at: Date = .now
}

struct OpsIssue: Codable, Hashable, Identifiable {
    var id: String                 // ISS-1
    var title: String
    var body = ""                  // Markdown
    var status: IssueStatus = .open
    var priority: IssuePriority = .normal
    var labels: [String] = []
    var source = ""                // 從哪裡發現的,例如「網站訪客 7d」
    var link = ""                  // 相關網址
    var createdBy: String          // person id、"mcp" 或 "agent:<角色 id>"
    var createdAt: Date = .now
    var updatedAt: Date = .now
    var closedAt: Date?
    var comments: [IssueComment] = []

    var number: Int { Int(id.filter(\.isNumber)) ?? 0 }
}

/// 讀寫 issues.json;畫面與 MCP 共用。
/// 設了 Issue 伺服器(IssueRemote.swift)時改透過 Go 版 ops-web 的 API 讀寫,不碰本機的 issues.json;
/// app.issues 是伺服器資料的副本(寫入後、切換組織、每隔幾秒重新讀)。
@MainActor
final class IssueStore {
    let app: AppState
    init(app: AppState) { self.app = app }

    static let mcpAuthor = "mcp"

    private var fileURL: URL { app.dataDir.appendingPathComponent("issues.json") }

    // MARK: Issue 伺服器

    private var remoteClient: IssueRemote?
    /// 伺服器模式時的用戶端(設定改了會換新的;nil = 用本機檔案)
    var remote: IssueRemote? {
        guard let cfg = IssueServerConfig.current else { remoteClient = nil; stopPolling(); return nil }
        if remoteClient?.config != cfg { remoteClient = IssueRemote(config: cfg) }
        return remoteClient
    }
    var isRemote: Bool { remote != nil }
    /// 最近一次和伺服器溝通的錯誤(nil = 正常)
    private(set) var remoteError: String?
    private var lastFetch = Date.distantPast
    private var poller: Timer?
    /// 伺服器模式時多久重新讀一次(秒);0 = 不定時讀
    static var pollInterval: TimeInterval = NSClassFromString("XCTestCase") != nil ? 0 : 5

    private func remoteFailed(_ e: Error) {
        let msg = e.localizedDescription
        if remoteError != msg { NSLog("Issue 伺服器:%@", msg) }
        remoteError = msg
    }

    private func startPolling() {
        guard poller == nil, Self.pollInterval > 0 else { return }
        poller = Timer.scheduledTimer(withTimeInterval: Self.pollInterval, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self, self.isRemote else { return }
                self.refresh()
            }
        }
    }

    private func stopPolling() {
        poller?.invalidate()
        poller = nil
    }

    /// 伺服器模式:重新讀目前組織的 Issue(內容有變才更新畫面)
    func refresh() {
        guard let r = remote else { return }
        do {
            let list = try r.list(org: app.currentOrgId)
            lastFetch = .now
            remoteError = nil
            if list != app.issues { app.issues = list }
        } catch { remoteFailed(error) }
    }

    /// 伺服器模式:超過 maxAge 秒沒讀就重新讀(0 = 一定重新讀)
    func refreshIfStale(_ maxAge: TimeInterval) {
        guard isRemote, maxAge <= 0 || Date.now.timeIntervalSince(lastFetch) > maxAge else { return }
        refresh()
    }

    // MARK: 讀寫

    func load() {
        if isRemote {
            refresh()
            startPolling()
            return
        }
        guard let data = try? Data(contentsOf: fileURL) else { app.issues = []; return }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        app.issues = (try? d.decode([OpsIssue].self, from: data)) ?? []
    }

    func save() {
        guard !isRemote else { return }   // 伺服器模式每個動作各自送出
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? e.encode(app.issues) { try? data.write(to: fileURL, options: .atomic) }
    }

    /// 找一筆(伺服器模式先重新讀,MCP 拿到的是最新的)
    func issue(_ id: String) -> OpsIssue? {
        refreshIfStale(0)
        return app.issues.first { $0.id.caseInsensitiveCompare(id) == .orderedSame }
    }

    @discardableResult
    func create(title: String, body: String = "", priority: IssuePriority = .normal, labels: [String] = [],
                source: String = "", link: String = "", by author: String) -> OpsIssue {
        if let r = remote {
            do {
                let issue = try r.create(org: app.currentOrgId, title: title, body: body, priority: priority,
                                         labels: Self.clean(labels), source: source, link: link, author: author)
                remoteError = nil
                app.issues.removeAll { $0.id == issue.id }
                app.issues.insert(issue, at: 0)
                app.log(actor(author), "exclamationmark.bubble", "建立 \(issue.id)「\(title)」", .development)
                return issue
            } catch {
                // 伺服器連不上:回傳沒有存起來的草稿(id 是空的),不寫本機檔案
                remoteFailed(error)
                return OpsIssue(id: "", title: title, body: body, priority: priority, labels: Self.clean(labels),
                                source: source, link: link, createdBy: author)
            }
        }
        let num = (app.issues.map(\.number).max() ?? 0) + 1
        let issue = OpsIssue(id: "ISS-\(num)", title: title, body: body, priority: priority,
                             labels: Self.clean(labels), source: source, link: link, createdBy: author)
        app.issues.insert(issue, at: 0)
        save()
        app.log(actor(author), "exclamationmark.bubble", "建立 \(issue.id)「\(title)」", .development)
        return issue
    }

    /// 修改一筆;回傳修改後的內容。狀態從 open 變 closed(或反過來)時記到動態牆
    @discardableResult
    func update(_ id: String, by author: String, _ change: (inout OpsIssue) -> Void) -> OpsIssue? {
        refreshIfStale(0)   // 伺服器模式:用伺服器上最新的內容來改
        guard let i = app.issues.firstIndex(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) else { return nil }
        let before = app.issues[i]
        var n = before
        change(&n)
        n.labels = Self.clean(n.labels)
        let statusChanged = n.status != before.status
        if statusChanged { n.closedAt = n.status == .closed ? .now : nil }
        func logStatus() {
            guard statusChanged else { return }
            app.log(actor(author), n.status == .closed ? "checkmark.circle" : "arrow.uturn.backward.circle",
                    "\(n.status == .closed ? "關閉" : "重新打開") \(n.id)「\(n.title)」", .development)
        }
        if let r = remote {
            guard n != before else { return n }
            do {
                let saved = try r.update(org: app.currentOrgId, n, author: author)
                remoteError = nil
                if let k = app.issues.firstIndex(where: { $0.id == saved.id }) { app.issues[k] = saved }
                logStatus()
                return saved
            } catch {
                remoteFailed(error)
                return nil
            }
        }
        logStatus()
        guard n != before else { return n }
        n.updatedAt = .now
        app.issues[i] = n
        save()
        return n
    }

    func comment(_ id: String, _ text: String, by author: String) -> OpsIssue? {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return issue(id) }
        return update(id, by: author) { $0.comments.append(IssueComment(author: author, text: t)) }
    }

    func delete(_ id: String) {
        if let r = remote {
            do {
                try r.delete(org: app.currentOrgId, id: id)
                remoteError = nil
            } catch {
                remoteFailed(error)
                return
            }
            app.issues.removeAll { $0.id == id }
            return
        }
        app.issues.removeAll { $0.id == id }
        save()
    }

    func actor(_ author: String) -> Actor {
        if author == Self.mcpAuthor { return .mcp }
        if author.hasPrefix(Self.agentPrefix) { return .ai(String(author.dropFirst(Self.agentPrefix.count))) }
        return .person(author)
    }

    /// 虛擬角色署名:agent:<角色 id>
    static let agentPrefix = "agent:"
    static func isAI(_ author: String) -> Bool { author == mcpAuthor || author.hasPrefix(agentPrefix) }

    /// 角色 id(或 nil = 不是角色)
    func agent(of author: String) -> Agent? {
        guard author.hasPrefix(Self.agentPrefix) else { return nil }
        let id = String(author.dropFirst(Self.agentPrefix.count))
        return app.agents.first { $0.id == id }
    }

    func authorName(_ author: String) -> String { app.name(of: actor(author)) }

    static func clean(_ labels: [String]) -> [String] {
        var seen = Set<String>()
        return labels.map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty && seen.insert($0.lowercased()).inserted }
    }
}
