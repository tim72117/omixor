import Foundation

// core 的資料(core/model)在 iOS 這邊需要的欄位。沒列的欄位解碼時略過;選填的都給預設值,伺服器少給也不會壞。

/// 「誰」的 actor 物件(core model.ActorView):旁邊並存相容字串(createdBy、author…);查不到的舊資料整個欄位省略。
/// kind、status 保持字串:伺服器新增值時不會解碼失敗。
struct ActorView: Codable, Hashable, Sendable {
    var id: String
    var kind: String = ""
    var handle: String = ""
    var name: String = ""
    var email: String = ""
    var status: String = ""

    var displayName: String { name.isEmpty ? handle : name }
    var isAgent: Bool { kind == "agent" }

    enum CodingKeys: String, CodingKey { case id, kind, handle, name, email, status }
    init(id: String, kind: String = "", handle: String = "", name: String = "") {
        self.id = id; self.kind = kind; self.handle = handle; self.name = name
    }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        kind = c.string(.kind); handle = c.string(.handle); name = c.string(.name)
        email = c.string(.email); status = c.string(.status)
    }
}

extension KeyedDecodingContainer {
    /// 選填字串:沒給、null 或型別不對都是空字串(新模型用;不必每個欄位寫 decodeIfPresent ?? "")
    func string(_ key: Key) -> String { (try? decodeIfPresent(String.self, forKey: key)) ?? "" }
    func list<T: Decodable>(_ key: Key) -> [T] { (try? decodeIfPresent([T].self, forKey: key)) ?? [] }
}

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
    var authorActor: ActorView?

    enum CodingKeys: String, CodingKey { case id, author, text, at, authorActor }
    init(id: String, author: String, text: String, at: Date? = nil) { self.id = id; self.author = author; self.text = text; self.at = at }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? UUID().uuidString
        author = try c.decodeIfPresent(String.self, forKey: .author) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        at = try c.decodeIfPresent(Date.self, forKey: .at)
        authorActor = try? c.decodeIfPresent(ActorView.self, forKey: .authorActor)
    }
}

/// id 是內部 id(UUIDv7,永遠不變、不顯示);給人看的編號是 key(ISS-<n> 或 <專案代號>-<n>)。
/// 打 API 時 id 或目前、舊的 key 都收;version 是樂觀鎖的版本(修改時帶回去,不符 409)。
struct Issue: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var key: String = ""
    var project: String = ""
    var serverNumber: Int = 0
    var version: Int = 0
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
    var repo: String = ""
    var link: String = ""
    var source: String = ""
    var closedAt: Date?
    var assigneeActor: ActorView?
    var createdByActor: ActorView?
    /// 工作項目數(列表上只有有項目的 Issue 才有)
    var itemsOpen: Int = 0
    var itemsTotal: Int = 0

    var isOpen: Bool { status != "closed" }
    /// 顯示用編號(沒有 key 的舊回應退回 id)
    var displayKey: String { key.isEmpty ? id : key }
    /// 流水號:伺服器給的 number,沒有時從 key / id 的尾端數字推
    var number: Int {
        if serverNumber > 0 { return serverNumber }
        return Int(displayKey.split(separator: "-").last ?? "") ?? 0
    }

    var priorityLabel: String {
        switch priority {
        case "urgent": return "緊急"
        case "high": return "高"
        case "low": return "低"
        default: return "一般"
        }
    }

    enum CodingKeys: String, CodingKey {
        case id, key, project, version, title, body, status, priority, labels, assignee, createdBy, createdAt, updatedAt, comments
        case repo, link, source, closedAt, assigneeActor, createdByActor, itemsOpen, itemsTotal
        case serverNumber = "number"
    }
    init(id: String, title: String, body: String = "", status: String = "open", labels: [String] = [], key: String = "", version: Int = 0) {
        self.id = id; self.title = title; self.body = body; self.status = status; self.labels = labels; self.key = key; self.version = version
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
        key = c.string(.key); project = c.string(.project); repo = c.string(.repo)
        link = c.string(.link); source = c.string(.source)
        serverNumber = (try? c.decodeIfPresent(Int.self, forKey: .serverNumber)) ?? 0
        version = (try? c.decodeIfPresent(Int.self, forKey: .version)) ?? 0
        itemsOpen = (try? c.decodeIfPresent(Int.self, forKey: .itemsOpen)) ?? 0
        itemsTotal = (try? c.decodeIfPresent(Int.self, forKey: .itemsTotal)) ?? 0
        closedAt = try? c.decodeIfPresent(Date.self, forKey: .closedAt)
        assigneeActor = try? c.decodeIfPresent(ActorView.self, forKey: .assigneeActor)
        createdByActor = try? c.decodeIfPresent(ActorView.self, forKey: .createdByActor)
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
