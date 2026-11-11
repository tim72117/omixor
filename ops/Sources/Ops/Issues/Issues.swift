import Foundation
import SwiftUI

// Issue:要追蹤的問題或待辦,存在 ~/Library/Application Support/Ops/issues.json(OPS_DATA_DIR 可改)。
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

/// 讀寫 issues.json;畫面與 MCP 共用
@MainActor
final class IssueStore {
    let app: AppState
    init(app: AppState) { self.app = app }

    static let mcpAuthor = "mcp"

    private var fileURL: URL { app.dataDir.appendingPathComponent("issues.json") }

    func load() {
        guard let data = try? Data(contentsOf: fileURL) else { app.issues = []; return }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        app.issues = (try? d.decode([OpsIssue].self, from: data)) ?? []
    }

    func save() {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? e.encode(app.issues) { try? data.write(to: fileURL, options: .atomic) }
    }

    func issue(_ id: String) -> OpsIssue? {
        app.issues.first { $0.id.caseInsensitiveCompare(id) == .orderedSame }
    }

    @discardableResult
    func create(title: String, body: String = "", priority: IssuePriority = .normal, labels: [String] = [],
                source: String = "", link: String = "", by author: String) -> OpsIssue {
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
        guard let i = app.issues.firstIndex(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) else { return nil }
        let before = app.issues[i]
        var n = before
        change(&n)
        n.labels = Self.clean(n.labels)
        if n.status != before.status {
            n.closedAt = n.status == .closed ? .now : nil
            app.log(actor(author), n.status == .closed ? "checkmark.circle" : "arrow.uturn.backward.circle",
                    "\(n.status == .closed ? "關閉" : "重新打開") \(n.id)「\(n.title)」", .development)
        }
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
