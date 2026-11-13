import Foundation

// core 的資料(core/model)在 iOS 這邊需要的欄位。沒列的欄位解碼時略過;選填的都給預設值,伺服器少給也不會壞。

struct User: Codable, Equatable, Sendable {
    var id: String
    var email: String
    var name: String = ""
    var role: String = "member"

    var displayName: String { name.isEmpty ? email : name }

    enum CodingKeys: String, CodingKey { case id, email, name, role }
    init(id: String, email: String, name: String = "", role: String = "member") {
        self.id = id; self.email = email; self.name = name; self.role = role
    }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        email = try c.decodeIfPresent(String.self, forKey: .email) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        role = try c.decodeIfPresent(String.self, forKey: .role) ?? "member"
    }
}

struct TokenInfo: Codable, Equatable, Sendable {
    var id: String
    var device: String = ""
    var expiresAt: Date?

    enum CodingKeys: String, CodingKey { case id, device, expiresAt }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        device = try c.decodeIfPresent(String.self, forKey: .device) ?? ""
        expiresAt = try c.decodeIfPresent(Date.self, forKey: .expiresAt)
    }
}

/// POST /api/auth/pair/claim(同 /api/auth/evoleap)的回應
struct LoginResult: Decodable, Sendable {
    var token: String
    var tokenId: String
    var expiresAt: Date?
    var user: User
}

struct MeResponse: Decodable, Sendable {
    var user: User
    var token: TokenInfo
}

struct Org: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var name: String
    var symbol: String = ""
    var color: String = ""
    var note: String = ""
    /// 我在這個組織的角色(admin、member)
    var myRole: String = ""

    var isAdmin: Bool { myRole == "admin" }

    enum CodingKeys: String, CodingKey { case id, name, symbol, color, note, myRole }
    init(id: String, name: String, myRole: String = "") { self.id = id; self.name = name; self.myRole = myRole }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        symbol = try c.decodeIfPresent(String.self, forKey: .symbol) ?? ""
        color = try c.decodeIfPresent(String.self, forKey: .color) ?? ""
        note = try c.decodeIfPresent(String.self, forKey: .note) ?? ""
        myRole = try c.decodeIfPresent(String.self, forKey: .myRole) ?? ""
    }
}

struct Member: Codable, Hashable, Sendable {
    var email: String
    var name: String = ""
    var role: String = "member"

    enum CodingKeys: String, CodingKey { case email, name, role }
    init(email: String, name: String = "", role: String = "member") { self.email = email; self.name = name; self.role = role }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        email = try c.decodeIfPresent(String.self, forKey: .email) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        role = try c.decodeIfPresent(String.self, forKey: .role) ?? "member"
    }
}

struct Agent: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var name: String
    var role: String = ""
    var active: Bool = true
    var symbol: String = ""
    var color: String = ""

    enum CodingKeys: String, CodingKey { case id, name, role, active, symbol, color }
    init(id: String, name: String, role: String = "", active: Bool = true) { self.id = id; self.name = name; self.role = role; self.active = active }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        role = try c.decodeIfPresent(String.self, forKey: .role) ?? ""
        active = try c.decodeIfPresent(Bool.self, forKey: .active) ?? true
        symbol = try c.decodeIfPresent(String.self, forKey: .symbol) ?? ""
        color = try c.decodeIfPresent(String.self, forKey: .color) ?? ""
    }
}

// MARK: 頻道

struct ChannelSummary: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var name: String
    var topic: String = ""
    var issueId: String = ""
    var members: [String] = []
    var unread: Int = 0
    var lastMessageAt: Date?
    var updatedAt: Date?

    enum CodingKeys: String, CodingKey { case id, name, topic, issueId, members, unread, lastMessageAt, updatedAt }
    init(id: String, name: String, members: [String] = [], unread: Int = 0, lastMessageAt: Date? = nil) {
        self.id = id; self.name = name; self.members = members; self.unread = unread; self.lastMessageAt = lastMessageAt
    }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        topic = try c.decodeIfPresent(String.self, forKey: .topic) ?? ""
        issueId = try c.decodeIfPresent(String.self, forKey: .issueId) ?? ""
        members = try c.decodeIfPresent([String].self, forKey: .members) ?? []
        unread = try c.decodeIfPresent(Int.self, forKey: .unread) ?? 0
        lastMessageAt = try c.decodeIfPresent(Date.self, forKey: .lastMessageAt)
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }
}

struct ChannelMessage: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var channelId: String = ""
    /// 節點:user:<email> 或 agent:<id>
    var author: String
    var text: String
    var mentions: [String] = []
    var at: Date

    /// M-12 → 12
    var number: Int { Int(id.uppercased().replacingOccurrences(of: "M-", with: "")) ?? 0 }

    enum CodingKeys: String, CodingKey { case id, channelId, author, text, mentions, at }
    init(id: String, author: String, text: String, at: Date, mentions: [String] = []) {
        self.id = id; self.author = author; self.text = text; self.at = at; self.mentions = mentions
    }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        channelId = try c.decodeIfPresent(String.self, forKey: .channelId) ?? ""
        author = try c.decodeIfPresent(String.self, forKey: .author) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        mentions = try c.decodeIfPresent([String].self, forKey: .mentions) ?? []
        at = try c.decodeIfPresent(Date.self, forKey: .at) ?? Date()
    }
}

struct MessagePage: Decodable, Sendable {
    var messages: [ChannelMessage]
    var hasMore: Bool
}

// MARK: Issue

struct IssueComment: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var author: String
    var text: String
    var at: Date?

    enum CodingKeys: String, CodingKey { case id, author, text, at }
    init(id: String, author: String, text: String, at: Date? = nil) { self.id = id; self.author = author; self.text = text; self.at = at }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? UUID().uuidString
        author = try c.decodeIfPresent(String.self, forKey: .author) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        at = try c.decodeIfPresent(Date.self, forKey: .at)
    }
}

struct Issue: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var title: String
    var body: String = ""
    var status: String = "open"
    var priority: String = "normal"
    var labels: [String] = []
    var assignee: String = ""
    var createdBy: String = ""
    var createdAt: Date?
    var updatedAt: Date?
    var comments: [IssueComment] = []

    var isOpen: Bool { status != "closed" }
    var number: Int { Int(id.uppercased().replacingOccurrences(of: "ISS-", with: "")) ?? 0 }

    var priorityLabel: String {
        switch priority {
        case "urgent": return "緊急"
        case "high": return "高"
        case "low": return "低"
        default: return "一般"
        }
    }

    enum CodingKeys: String, CodingKey { case id, title, body, status, priority, labels, assignee, createdBy, createdAt, updatedAt, comments }
    init(id: String, title: String, body: String = "", status: String = "open", labels: [String] = []) {
        self.id = id; self.title = title; self.body = body; self.status = status; self.labels = labels
    }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        body = try c.decodeIfPresent(String.self, forKey: .body) ?? ""
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? "open"
        priority = try c.decodeIfPresent(String.self, forKey: .priority) ?? "normal"
        labels = try c.decodeIfPresent([String].self, forKey: .labels) ?? []
        assignee = try c.decodeIfPresent(String.self, forKey: .assignee) ?? ""
        createdBy = try c.decodeIfPresent(String.self, forKey: .createdBy) ?? ""
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt)
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt)
        comments = try c.decodeIfPresent([IssueComment].self, forKey: .comments) ?? []
    }
}

// MARK: 派發

enum TaskStatus: String, Codable, CaseIterable, Sendable {
    case queued, inProgress, blocked, done, canceled

    var label: String {
        switch self {
        case .queued: return "待領取"
        case .inProgress: return "進行中"
        case .blocked: return "需要協助"
        case .done: return "完成"
        case .canceled: return "已取消"
        }
    }

    var isOpen: Bool { self == .queued || self == .inProgress || self == .blocked }
}

struct TaskNote: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var author: String
    var text: String
    var at: Date?

    enum CodingKeys: String, CodingKey { case id, author, text, at }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? UUID().uuidString
        author = try c.decodeIfPresent(String.self, forKey: .author) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        at = try c.decodeIfPresent(Date.self, forKey: .at)
    }
}

struct AgentTask: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var agentId: String
    var title: String
    var instructions: String = ""
    var priority: String = "normal"
    var status: TaskStatus = .queued
    var result: String = ""
    var notes: [TaskNote] = []
    var links: [String] = []
    var createdBy: String = ""
    var createdAt: Date?
    var updatedAt: Date?
    var claimedAt: Date?
    var claimedBy: String = ""
    var completedAt: Date?
    var lockedUntil: Date?

    enum CodingKeys: String, CodingKey {
        case id, agentId, title, instructions, priority, status, result, notes, links, createdBy, createdAt, updatedAt, claimedAt, claimedBy, completedAt, lockedUntil
    }
    init(id: String, agentId: String, title: String, status: TaskStatus = .queued, createdAt: Date? = nil, updatedAt: Date? = nil, lockedUntil: Date? = nil) {
        self.id = id; self.agentId = agentId; self.title = title; self.status = status
        self.createdAt = createdAt; self.updatedAt = updatedAt; self.lockedUntil = lockedUntil
    }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        agentId = try c.decodeIfPresent(String.self, forKey: .agentId) ?? ""
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        instructions = try c.decodeIfPresent(String.self, forKey: .instructions) ?? ""
        priority = try c.decodeIfPresent(String.self, forKey: .priority) ?? "normal"
        status = TaskStatus(rawValue: try c.decodeIfPresent(String.self, forKey: .status) ?? "") ?? .queued
        result = try c.decodeIfPresent(String.self, forKey: .result) ?? ""
        notes = try c.decodeIfPresent([TaskNote].self, forKey: .notes) ?? []
        links = try c.decodeIfPresent([String].self, forKey: .links) ?? []
        createdBy = try c.decodeIfPresent(String.self, forKey: .createdBy) ?? ""
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt)
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt)
        claimedAt = try c.decodeIfPresent(Date.self, forKey: .claimedAt)
        claimedBy = try c.decodeIfPresent(String.self, forKey: .claimedBy) ?? ""
        completedAt = try c.decodeIfPresent(Date.self, forKey: .completedAt)
        lockedUntil = try c.decodeIfPresent(Date.self, forKey: .lockedUntil)
    }

    var priorityLabel: String {
        switch priority {
        case "urgent": return "緊急"
        case "high": return "高"
        case "low": return "低"
        default: return "一般"
        }
    }

    /// 卡住的原因(沒卡住是 nil):需要協助、領取租約過期還沒回報、待領取超過 queuedTooLong 沒人領
    func stuckReason(now: Date = Date(), queuedTooLong: TimeInterval = 3600) -> String? {
        switch status {
        case .blocked: return "需要協助"
        case .inProgress:
            if let l = lockedUntil, l <= now { return "租約已過期,沒有回報" }
        case .queued:
            // 重新排入會更新 updatedAt,所以從最後一次變動算起
            if let c = updatedAt ?? createdAt, now.timeIntervalSince(c) >= queuedTooLong { return "超過 \(Int(queuedTooLong / 60)) 分鐘沒人領取" }
        default: break
        }
        return nil
    }
}
