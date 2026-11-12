import Foundation

// 訊息頻道:把組織成員(人)與虛擬角色拉進同一個頻道對話。頻道可以自由建立,也可以綁一個 Issue(一個 Issue 只有一個頻道)。
// @ 一個角色只通知、不排任務;角色下次執行(ops_agent_context)時看得到提到它的未讀訊息。
// 成員節點同組織鏈:"agent:<角色 id>" 或 "user:<email 小寫>"(本機模式的人是 app 的人員 id,例如 user:tim)。
//
// 本機模式存在組織資料夾的 channels.json({channels, messages, reads},格式同核心);
// 遠端模式改讀寫 omixor server(core)的 /api/orgs/<組織>/channels(ChannelRemote.swift),規則由伺服器強制。
// 本機模式的規則(行為照核心):
//   - 名稱必填(去頭尾空白,最多 80 字)、topic 最多 500 字;訊息去頭尾空白後不能空白,最多 8000 字
//   - 建立者自動是成員;成員可以給 agent:<id 或名稱>、user:<email>、純 email、純角色 id 或名稱;角色要存在、人要是組織成員
//   - 綁 Issue:Issue 要存在(404);已有頻道而且看得到 → 回傳那個(不新建);看不到 → 409
//   - 權限:本機只有一個使用者(擁有者,等同組織管理員):看得到全部、可以改名稱主題與成員,發訊息要是成員;
//     角色只能看與發自己是成員的頻道,不能建頻道、不能改成員
//   - 未讀 = 號碼 > lastRead 而且作者不是自己的訊息;刪角色時把它從所有頻道成員拿掉

struct Channel: Codable, Hashable, Identifiable {
    var id: String               // CH-1
    var name: String
    var topic = ""
    var issueId = ""             // 沒綁是 ""
    var members: [String] = []
    var createdBy: String
    var createdAt: Date
    var updatedAt: Date

    var number: Int { Int(id.split(separator: "-").last ?? "") ?? 0 }
    var hasIssue: Bool { !issueId.isEmpty }

    init(id: String, name: String, topic: String = "", issueId: String = "", members: [String] = [],
         createdBy: String, createdAt: Date, updatedAt: Date) {
        self.id = id; self.name = name; self.topic = topic; self.issueId = issueId
        self.members = members; self.createdBy = createdBy; self.createdAt = createdAt; self.updatedAt = updatedAt
    }

    // 手寫解碼:伺服器的 members 可能是 null、少欄位不要整份讀不出來
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        topic = try c.decodeIfPresent(String.self, forKey: .topic) ?? ""
        issueId = try c.decodeIfPresent(String.self, forKey: .issueId) ?? ""
        members = try c.decodeIfPresent([String].self, forKey: .members) ?? []
        createdBy = try c.decodeIfPresent(String.self, forKey: .createdBy) ?? ""
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? .distantPast
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt) ?? createdAt
    }
}

/// 列表項目:Channel 的欄位 + 我的未讀數與最後一則訊息的時間(JSON 是攤平的)
struct ChannelSummary: Codable, Hashable, Identifiable {
    var channel: Channel
    var unread = 0
    var lastMessageAt: Date?

    var id: String { channel.id }
    var name: String { channel.name }

    init(channel: Channel, unread: Int = 0, lastMessageAt: Date? = nil) {
        self.channel = channel; self.unread = unread; self.lastMessageAt = lastMessageAt
    }

    private enum Keys: String, CodingKey { case unread, lastMessageAt }

    init(from decoder: Decoder) throws {
        channel = try Channel(from: decoder)
        let c = try decoder.container(keyedBy: Keys.self)
        unread = try c.decodeIfPresent(Int.self, forKey: .unread) ?? 0
        lastMessageAt = try? c.decodeIfPresent(Date.self, forKey: .lastMessageAt)
    }

    func encode(to encoder: Encoder) throws {
        try channel.encode(to: encoder)
        var c = encoder.container(keyedBy: Keys.self)
        try c.encode(unread, forKey: .unread)
        try c.encode(lastMessageAt, forKey: .lastMessageAt)
    }
}

struct ChannelMessage: Codable, Hashable, Identifiable {
    var id: String               // M-1(每個頻道各自編號)
    var channelId: String
    var author: String           // 節點
    var text: String             // Markdown
    var mentions: [String] = []
    var at: Date

    var number: Int { Int(id.split(separator: "-").last ?? "") ?? 0 }

    init(id: String, channelId: String, author: String, text: String, mentions: [String] = [], at: Date) {
        self.id = id; self.channelId = channelId; self.author = author; self.text = text; self.mentions = mentions; self.at = at
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        channelId = try c.decodeIfPresent(String.self, forKey: .channelId) ?? ""
        author = try c.decodeIfPresent(String.self, forKey: .author) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        mentions = try c.decodeIfPresent([String].self, forKey: .mentions) ?? []
        at = try c.decodeIfPresent(Date.self, forKey: .at) ?? .distantPast
    }
}

/// 已讀:每個成員每個頻道一筆
struct ChannelRead: Codable, Hashable {
    var channelId: String
    var member: String
    var lastRead: Int
    var at: Date
}

/// 訊息列表的一頁(依時間由舊到新)
struct ChannelPage: Codable, Hashable {
    var messages: [ChannelMessage]
    var hasMore: Bool

    init(messages: [ChannelMessage], hasMore: Bool) { self.messages = messages; self.hasMore = hasMore }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        messages = try c.decodeIfPresent([ChannelMessage].self, forKey: .messages) ?? []
        hasMore = try c.decodeIfPresent(Bool.self, forKey: .hasMore) ?? false
    }
}

/// 提到某個節點的未讀訊息(新的在前,最多 20 則)與它所在頻道的未讀數
struct ChannelInbox: Codable, Hashable {
    struct Mention: Codable, Hashable {
        var channelId: String
        var channelName: String
        var id: String
        var author: String
        var text: String
        var at: Date
    }
    struct Unread: Codable, Hashable {
        var id: String
        var name: String
        var unread: Int
    }
    var mentions: [Mention] = []
    var channels: [Unread] = []

    init(mentions: [Mention] = [], channels: [Unread] = []) { self.mentions = mentions; self.channels = channels }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mentions = try c.decodeIfPresent([Mention].self, forKey: .mentions) ?? []
        channels = try c.decodeIfPresent([Unread].self, forKey: .channels) ?? []
    }
}

/// 本機模式的 channels.json
struct ChannelData: Codable {
    var channels: [Channel] = []
    var messages: [ChannelMessage] = []
    var reads: [ChannelRead] = []

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        channels = try c.decodeIfPresent([Channel].self, forKey: .channels) ?? []
        messages = try c.decodeIfPresent([ChannelMessage].self, forKey: .messages) ?? []
        reads = try c.decodeIfPresent([ChannelRead].self, forKey: .reads) ?? []
    }
}

/// 頻道的錯誤:status 同核心的 HTTP 狀態(400、403、404、409)
struct ChannelError: LocalizedError {
    var status: Int
    var message: String
    init(_ status: Int, _ message: String) { self.status = status; self.message = message }
    var errorDescription: String? { message }
}

// MARK: - @ 解析

enum ChannelMention {
    static func isAlnum(_ c: Character) -> Bool { c.isASCII && (c.isLetter || c.isNumber) }

    /// 文字中 @ 前面是開頭或非英數字元時,後面依序比對頻道成員的角色名稱、角色 id、email、完整節點(不分大小寫,取最長的);
    /// 角色 id、email(與完整節點)比對到之後,下一個字元若是英數或 -_(或後面接英數的句點)就不算。只算頻道成員、作者自己不算、去重
    static func parse(_ text: String, members: [String], agents: [Agent], author: String) -> [String] {
        let chars = Array(text.lowercased())
        var cands: [(text: [Character], node: String, boundary: Bool)] = []
        for m in members {
            if let id = ChainNode.agentId(m) {
                if let a = agents.first(where: { $0.id == id }), !a.name.isEmpty { cands.append((Array(a.name.lowercased()), m, false)) }
                cands.append((Array(id.lowercased()), m, true))
            } else if let u = ChainNode.userId(m) {
                cands.append((Array(u.lowercased()), m, true))
            }
            cands.append((Array(m.lowercased()), m, true))
        }
        var out: [String] = []
        var i = 0
        while i < chars.count {
            if chars[i] == "@", i == 0 || !isAlnum(chars[i - 1]) {
                var best: (len: Int, node: String)?
                for c in cands where !c.text.isEmpty && i + 1 + c.text.count <= chars.count {
                    let end = i + 1 + c.text.count
                    guard Array(chars[(i + 1)..<end]) == c.text else { continue }
                    if c.boundary, end < chars.count {
                        let nx = chars[end]
                        if isAlnum(nx) || "-_".contains(nx) { continue }
                        // 句點後面還接英數(email 還沒完)才算同一個字;句尾的句點不算(同核心 model.ParseMentions)
                        if nx == ".", end + 1 < chars.count, isAlnum(chars[end + 1]) { continue }
                    }
                    if best == nil || c.text.count > best!.len { best = (c.text.count, c.node) }
                }
                if let b = best {
                    if b.node != author, !out.contains(b.node) { out.append(b.node) }
                    i += 1 + b.len
                    continue
                }
            }
            i += 1
        }
        return out
    }
}

// MARK: - 存放

@MainActor
final class ChannelStore {
    let app: AppState
    init(app: AppState) { self.app = app }

    static let maxName = 80
    static let maxTopic = 500
    static let maxText = 8000
    static let defaultLimit = 50
    static let maxLimit = 200
    static let inboxLimit = 20

    /// 本機模式的資料(channels.json)
    private(set) var data = ChannelData()
    private var fileURL: URL { app.dataDir.appendingPathComponent("channels.json") }

    // MARK: 遠端(omixor server)

    private var remoteClient: IssueRemote?
    /// 遠端模式時的用戶端(和 Issue 同一個伺服器與登入;nil = 用本機檔案)
    var remote: IssueRemote? {
        guard let cfg = IssueServerConfig.current else { remoteClient = nil; stopPolling(); return nil }
        if remoteClient?.config != cfg { remoteClient = IssueRemote(config: cfg) }
        return remoteClient
    }
    var isRemote: Bool { remote != nil }
    /// 最近一次和伺服器溝通的錯誤(nil = 正常)
    private(set) var remoteError: String?
    private var poller: Timer?
    /// 多久重新讀一次頻道清單(秒);0 = 不定時讀
    static var pollInterval: TimeInterval = NSClassFromString("XCTestCase") != nil ? 0 : 5

    private func remoteFailed(_ e: Error) {
        let msg = e.localizedDescription
        if remoteError != msg { NSLog("訊息頻道(omixor server):%@", msg) }
        remoteError = msg
    }

    func start() {
        guard poller == nil, Self.pollInterval > 0 else { return }
        poller = Timer.scheduledTimer(withTimeInterval: Self.pollInterval, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
    }

    private func stopPolling() {
        poller?.invalidate()
        poller = nil
    }

    /// 我的節點:遠端是登入的 email,本機是 app 的使用者
    var meNode: String {
        if isRemote, let e = app.omixorLogin.account?.email, !e.isEmpty { return ChainNode.user(e) }
        return ChainNode.user(app.me)
    }

    // MARK: 讀寫

    func load() {
        if isRemote {
            data = ChannelData()
            app.channels = []
            refresh()
            return
        }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        if let raw = try? Data(contentsOf: fileURL), let v = try? d.decode(ChannelData.self, from: raw) {
            data = v
        } else {
            data = ChannelData()
        }
        publish()
    }

    private func save() {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let raw = try? e.encode(data) { try? raw.write(to: fileURL, options: .atomic) }
        publish()
    }

    /// 更新畫面用的頻道清單(我看得到的)並通知畫面重新讀訊息
    private func publish() {
        let list = localSummaries(for: meNode, issue: nil)
        if list != app.channels { app.channels = list }
        app.channelsVersion += 1
    }

    /// 重新讀頻道清單(遠端:伺服器;本機:重新計算未讀)
    func refresh() {
        guard let r = remote else { publish(); return }
        do {
            let list = try r.listChannels(org: app.currentOrgId, issue: nil)
            remoteError = nil
            if list != app.channels {
                app.channels = list
                app.channelsVersion += 1
            }
        } catch { remoteFailed(error) }
    }

    /// 到秒的現在時間(格式同核心)
    static func now() -> Date { Date(timeIntervalSince1970: Date.now.timeIntervalSince1970.rounded(.down)) }

    // MARK: 顯示

    /// 節點的顯示名稱:角色名稱、人名或 email
    func name(of node: String) -> String {
        if let id = ChainNode.agentId(node) { return app.agents.first { $0.id == id }?.name ?? id }
        if let u = ChainNode.userId(node) {
            if let p = app.people.first(where: { $0.id.lowercased() == u }) { return p.name }
            if let a = app.omixorLogin.account, a.email.lowercased() == u, !a.name.isEmpty { return a.name }
            return u
        }
        return node
    }

    /// 給 AuthorIcon 的作者字串:角色是 agent:<id>,人是 id / email
    static func iconAuthor(_ node: String) -> String { ChainNode.userId(node) ?? node }

    /// 節點對應的動態牆執行者
    func actorOf(_ node: String) -> Actor {
        if let id = ChainNode.agentId(node) { return .ai(id) }
        return .person(ChainNode.userId(node) ?? node)
    }

    // MARK: 本機:權限

    /// 本機只有一個使用者(擁有者,等同組織管理員)
    private func isAdmin(_ actor: String) -> Bool { actor == ChainNode.user(app.me) }

    private func canView(_ c: Channel, _ actor: String) -> Bool {
        c.members.contains(actor) || (!ChainNode.isAgent(actor) && isAdmin(actor))
    }

    private func canManage(_ c: Channel, _ actor: String) -> Bool {
        !ChainNode.isAgent(actor) && (c.members.contains(actor) || isAdmin(actor))
    }

    private func userOK(_ u: String) -> Bool {
        u.contains("@") || u == app.me.lowercased() || app.people.contains { $0.id.lowercased() == u }
    }

    /// 成員正規化:agent:<id 或名稱>、user:<email>、純 email、純角色 id 或名稱 → 標準節點
    func normalizeMember(_ raw: String, creator: String? = nil) throws -> String {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { throw ChannelError(400, "成員不能是空的") }
        var kind: String, rest = s
        if s.hasPrefix(ChainNode.agentPrefix) { kind = "agent"; rest = String(s.dropFirst(ChainNode.agentPrefix.count)) }
        else if s.hasPrefix(ChainNode.userPrefix) { kind = "user"; rest = String(s.dropFirst(ChainNode.userPrefix.count)) }
        else { kind = s.contains("@") ? "user" : "agent" }
        rest = rest.trimmingCharacters(in: .whitespaces)
        if kind == "agent" {
            guard let g = app.agents.first(where: { $0.id.caseInsensitiveCompare(rest) == .orderedSame }) ?? app.agents.first(where: { $0.name == rest }) else {
                // 純文字找不到角色時,也可能是本機的人員 id(例如 tim)
                if !s.hasPrefix(ChainNode.agentPrefix), userOK(rest.lowercased()) { return ChainNode.user(rest) }
                throw ChannelError(400, "找不到角色 \(rest)")
            }
            return ChainNode.agent(g.id)
        }
        let node = ChainNode.user(rest)
        guard !rest.isEmpty, node == creator || userOK(rest.lowercased()) else { throw ChannelError(400, "\(s) 不是組織的成員") }
        return node
    }

    /// 拿掉成員時的正規化:找不到的角色、人也照樣換成節點(讓已經不存在的成員也拿得掉)
    private func looseNode(_ raw: String) -> String {
        if let n = try? normalizeMember(raw) { return n }
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.hasPrefix(ChainNode.agentPrefix) { return s }
        if s.hasPrefix(ChainNode.userPrefix) { return ChainNode.user(String(s.dropFirst(ChainNode.userPrefix.count))) }
        return s.contains("@") ? ChainNode.user(s) : ChainNode.agent(s)
    }

    // MARK: 本機:查詢

    private func localChannel(_ id: String) -> Channel? {
        data.channels.first { $0.id.caseInsensitiveCompare(id) == .orderedSame }
    }

    private func msgs(_ channelId: String) -> [ChannelMessage] {
        data.messages.filter { $0.channelId == channelId }.sorted { $0.number < $1.number }
    }

    private func lastRead(_ channelId: String, _ member: String) -> Int {
        data.reads.first { $0.channelId == channelId && $0.member == member }?.lastRead ?? 0
    }

    private func unread(_ c: Channel, for member: String) -> Int {
        let lr = lastRead(c.id, member)
        return data.messages.filter { $0.channelId == c.id && $0.number > lr && $0.author != member }.count
    }

    private func summary(_ c: Channel, for actor: String) -> ChannelSummary {
        ChannelSummary(channel: c, unread: unread(c, for: actor), lastMessageAt: msgs(c.id).last?.at)
    }

    private func localSummaries(for actor: String, issue: String?) -> [ChannelSummary] {
        data.channels
            .filter { canView($0, actor) && (issue == nil || $0.issueId.caseInsensitiveCompare(issue!) == .orderedSame) }
            .map { summary($0, for: actor) }
            .sorted { ($0.lastMessageAt ?? $0.channel.createdAt, $0.channel.number) > ($1.lastMessageAt ?? $1.channel.createdAt, $1.channel.number) }
    }

    /// 本機:找看得到的頻道(id 或名稱);不存在 404、看不到 403
    private func viewable(_ ref: String, _ actor: String) throws -> Channel {
        let r = ref.trimmingCharacters(in: .whitespacesAndNewlines)
        if let c = localChannel(r) {
            guard canView(c, actor) else { throw ChannelError(403, "你不是頻道 \(c.id) 的成員") }
            return c
        }
        let visible = data.channels.filter { canView($0, actor) }
        if let c = visible.first(where: { $0.name == r }) ?? visible.first(where: { $0.name.caseInsensitiveCompare(r) == .orderedSame }) {
            return c
        }
        throw ChannelError(404, "找不到頻道 \(r)")
    }

    // MARK: API(本機與遠端同一套介面;actor = 以誰的身分,nil = 我)

    /// 我(或 actor)看得到的頻道;issue 只找綁該 Issue 的
    func list(issue: String? = nil, as actor: String? = nil) throws -> [ChannelSummary] {
        let who = actor ?? meNode
        if let r = remote {
            var list = try r.listChannels(org: app.currentOrgId, issue: issue)
            remoteError = nil
            if actor == nil, issue == nil, list != app.channels { app.channels = list; app.channelsVersion += 1 }
            if let a = actor, ChainNode.isAgent(a) {
                // 角色身分:只留它是成員的頻道,未讀數改用它的(inbox)
                let unread = Dictionary((try? r.channelInbox(org: app.currentOrgId, for: a))?.channels.map { ($0.id, $0.unread) } ?? [],
                                        uniquingKeysWith: { x, _ in x })
                list = list.filter { $0.channel.members.contains(a) }.map { var s = $0; s.unread = unread[s.id] ?? 0; return s }
            }
            return list
        }
        return localSummaries(for: who, issue: issue)
    }

    /// 一個頻道(id 或名稱)
    func channel(_ ref: String, as actor: String? = nil) throws -> ChannelSummary {
        let who = actor ?? meNode
        if let r = remote {
            let s = ref.trimmingCharacters(in: .whitespacesAndNewlines)
            if s.uppercased().hasPrefix("CH-"), Int(s.dropFirst(3)) != nil {
                let c = try r.getChannel(org: app.currentOrgId, id: s.uppercased())
                if let a = actor, ChainNode.isAgent(a), !c.channel.members.contains(a) {
                    throw ChannelError(403, "\(name(of: a)) 不是頻道 \(c.id) 的成員")
                }
                return c
            }
            let all = try list(as: actor)
            guard let c = all.first(where: { $0.name == s }) ?? all.first(where: { $0.name.caseInsensitiveCompare(s) == .orderedSame }) else {
                throw ChannelError(404, "找不到頻道 \(s)")
            }
            return c
        }
        return summary(try viewable(ref, who), for: who)
    }

    /// 建立頻道(角色不能建)。綁 Issue 而且已有看得到的頻道時回傳那個,created = false
    @discardableResult
    func create(name rawName: String, topic rawTopic: String = "", issueId rawIssue: String = "", members rawMembers: [String] = [],
                as actor: String? = nil, via: Actor? = nil) throws -> (channel: ChannelSummary, created: Bool) {
        let who = actor ?? meNode
        let name = rawName.trimmingCharacters(in: .whitespacesAndNewlines)
        let topic = rawTopic.trimmingCharacters(in: .whitespacesAndNewlines)
        let issue = rawIssue.trimmingCharacters(in: .whitespacesAndNewlines)
        if let r = remote {
            let known = Set(app.channels.map(\.id))
            let c = try r.createChannel(org: app.currentOrgId, name: name, topic: topic, issueId: issue, members: rawMembers)
            remoteError = nil
            let created = !known.contains(c.id)
            if created { logCreate(c.channel, via ?? actorOf(who)) }
            refresh()
            return (c, created)
        }
        if ChainNode.isAgent(who) { throw ChannelError(403, "角色不能建立頻道") }
        guard !name.isEmpty else { throw ChannelError(400, "頻道名稱不能是空的") }
        guard name.count <= Self.maxName else { throw ChannelError(400, "頻道名稱最多 \(Self.maxName) 個字") }
        guard topic.count <= Self.maxTopic else { throw ChannelError(400, "主題最多 \(Self.maxTopic) 個字") }
        var issueId = ""
        if !issue.isEmpty {
            guard let i = app.startIssues().issue(issue) else { throw ChannelError(404, "找不到 Issue \(issue)") }
            issueId = i.id
            if let existing = data.channels.first(where: { $0.issueId == issueId }) {
                guard canView(existing, who) else {
                    throw ChannelError(409, "\(issueId) 已經有對話頻道 \(existing.id),你不是它的成員;請頻道成員把你加進去")
                }
                return (summary(existing, for: who), false)
            }
        }
        var members = [who]
        for m in rawMembers {
            let n = try normalizeMember(m, creator: who)
            if !members.contains(n) { members.append(n) }
        }
        let num = (data.channels.map(\.number).max() ?? 0) + 1
        let now = Self.now()
        let c = Channel(id: "CH-\(num)", name: name, topic: topic, issueId: issueId, members: members, createdBy: who, createdAt: now, updatedAt: now)
        data.channels.append(c)
        save()
        logCreate(c, via ?? actorOf(who))
        return (summary(c, for: who), true)
    }

    private func logCreate(_ c: Channel, _ by: Actor) {
        app.log(by, "bubble.left.and.bubble.right", "建立頻道「\(c.name)」" + (c.hasIssue ? "(\(c.issueId))" : ""), .channels)
    }

    /// 改名稱、主題(成員或組織管理員;角色不能改)
    @discardableResult
    func update(_ id: String, name newName: String? = nil, topic newTopic: String? = nil, as actor: String? = nil) throws -> ChannelSummary {
        let who = actor ?? meNode
        let n = newName?.trimmingCharacters(in: .whitespacesAndNewlines)
        let t = newTopic?.trimmingCharacters(in: .whitespacesAndNewlines)
        if let r = remote {
            let c = try r.updateChannel(org: app.currentOrgId, id: id, name: n, topic: t)
            remoteError = nil
            refresh()
            return c
        }
        var c = try viewable(id, who)
        guard canManage(c, who) else { throw ChannelError(403, "只有頻道成員或組織管理員可以修改頻道") }
        if let n {
            guard !n.isEmpty else { throw ChannelError(400, "頻道名稱不能是空的") }
            guard n.count <= Self.maxName else { throw ChannelError(400, "頻道名稱最多 \(Self.maxName) 個字") }
            c.name = n
        }
        if let t {
            guard t.count <= Self.maxTopic else { throw ChannelError(400, "主題最多 \(Self.maxTopic) 個字") }
            c.topic = t
        }
        return try replace(c, who)
    }

    /// 加減成員(成員或組織管理員;角色不能改)
    @discardableResult
    func changeMembers(_ id: String, add: [String] = [], remove: [String] = [], as actor: String? = nil) throws -> ChannelSummary {
        let who = actor ?? meNode
        if let r = remote {
            let c = try r.changeChannelMembers(org: app.currentOrgId, id: id, add: add, remove: remove)
            remoteError = nil
            refresh()
            return c
        }
        var c = try viewable(id, who)
        guard canManage(c, who) else { throw ChannelError(403, "只有頻道成員或組織管理員可以加減成員") }
        let adds = try add.map { try normalizeMember($0, creator: c.createdBy) }
        let removes = Set(remove.map(looseNode))
        c.members.removeAll { removes.contains($0) }
        for n in adds where !c.members.contains(n) { c.members.append(n) }
        return try replace(c, who)
    }

    private func replace(_ c: Channel, _ who: String) throws -> ChannelSummary {
        guard let i = data.channels.firstIndex(where: { $0.id == c.id }) else { throw ChannelError(404, "找不到頻道 \(c.id)") }
        var c = c
        if c != data.channels[i] { c.updatedAt = Self.now() }
        data.channels[i] = c
        save()
        return summary(c, for: who)
    }

    /// 訊息:給 since(M-n 或時間)→ 之後最早的 limit 筆;沒給 → 最新的 limit 筆;before 只取之前的。都由舊到新
    func messages(_ id: String, since: String? = nil, before: String? = nil, limit: Int? = nil, as actor: String? = nil) throws -> ChannelPage {
        let who = actor ?? meNode
        let lim = min(Self.maxLimit, max(1, limit ?? Self.defaultLimit))
        if let r = remote {
            let p = try r.channelMessages(org: app.currentOrgId, id: id, since: since, before: before, limit: lim)
            remoteError = nil
            return p
        }
        let c = try viewable(id, who)
        var list = msgs(c.id)
        if let b = before?.trimmingCharacters(in: .whitespaces), !b.isEmpty {
            let k = try Self.cursor(b, "before")
            list = list.filter { k.isBefore($0) }
        }
        if let s = since?.trimmingCharacters(in: .whitespaces), !s.isEmpty {
            let k = try Self.cursor(s, "since")
            let after = list.filter { !k.isBefore($0) && !k.isSame($0) }
            return ChannelPage(messages: Array(after.prefix(lim)), hasMore: after.count > lim)
        }
        return ChannelPage(messages: Array(list.suffix(lim)), hasMore: list.count > lim)
    }

    /// since / before 的位置:訊息號碼或時間
    private struct Cursor {
        var number: Int?
        var date: Date?
        func isBefore(_ m: ChannelMessage) -> Bool { number.map { m.number < $0 } ?? (m.at < date!) }
        func isSame(_ m: ChannelMessage) -> Bool { number.map { m.number == $0 } ?? (m.at == date!) }
    }

    private static func cursor(_ s: String, _ key: String) throws -> Cursor {
        if let n = messageNumber(s) { return Cursor(number: n) }
        let f = ISO8601DateFormatter()
        if let d = f.date(from: s) { return Cursor(date: d) }
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = f.date(from: s) { return Cursor(date: d) }
        throw ChannelError(400, "\(key) 要是訊息 id(M-n)或時間(ISO 8601)")
    }

    /// "M-12" → 12
    static func messageNumber(_ s: String) -> Int? {
        let t = s.trimmingCharacters(in: .whitespaces)
        guard t.uppercased().hasPrefix("M-") else { return nil }
        return Int(t.dropFirst(2))
    }

    /// 發訊息(要是頻道成員);@ 到的角色記到動態牆
    @discardableResult
    func post(_ id: String, text raw: String, as actor: String? = nil, via: Actor? = nil) throws -> ChannelMessage {
        let who = actor ?? meNode
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if let r = remote {
            guard !text.isEmpty else { throw ChannelError(400, "訊息不能是空的") }
            let m = try r.postChannelMessage(org: app.currentOrgId, id: id, text: text, author: actor)
            remoteError = nil
            let chName = app.channels.first { $0.id == m.channelId }?.name ?? m.channelId
            logMentions(m, channelName: chName, via ?? actorOf(m.author))
            refresh()
            return m
        }
        let c = try viewable(id, who)
        guard c.members.contains(who) else { throw ChannelError(403, "只有頻道成員可以發訊息") }
        guard !text.isEmpty else { throw ChannelError(400, "訊息不能是空的") }
        guard text.count <= Self.maxText else { throw ChannelError(400, "訊息最多 \(Self.maxText) 個字") }
        let num = (msgs(c.id).last?.number ?? 0) + 1
        let m = ChannelMessage(id: "M-\(num)", channelId: c.id, author: who, text: text,
                               mentions: ChannelMention.parse(text, members: c.members, agents: app.agents, author: who), at: Self.now())
        data.messages.append(m)
        setRead(c.id, who, num)   // 自己發的訊息之前的都算看過了
        save()
        logMentions(m, channelName: c.name, via ?? actorOf(who))
        return m
    }

    /// 被 @ 的角色記到動態牆
    private func logMentions(_ m: ChannelMessage, channelName: String, _ by: Actor) {
        let agents = m.mentions.filter(ChainNode.isAgent)
        guard !agents.isEmpty else { return }
        app.log(by, "at", "在頻道「\(channelName)」提到 " + agents.map(name(of:)).joined(separator: "、"), .channels)
    }

    private func setRead(_ channelId: String, _ member: String, _ n: Int) {
        let now = Self.now()
        if let i = data.reads.firstIndex(where: { $0.channelId == channelId && $0.member == member }) {
            data.reads[i].lastRead = max(data.reads[i].lastRead, n)
            data.reads[i].at = now
        } else {
            data.reads.append(ChannelRead(channelId: channelId, member: member, lastRead: n, at: now))
        }
    }

    /// 標已讀到 upTo(M-n;沒給 = 最新);已讀只會往前,不會倒退
    @discardableResult
    func markRead(_ id: String, upTo: String? = nil, as actor: String? = nil) throws -> ChannelRead {
        let who = actor ?? meNode
        if let r = remote {
            let read = try r.markChannelRead(org: app.currentOrgId, id: id, upTo: upTo, member: actor)
            remoteError = nil
            if actor == nil, let i = app.channels.firstIndex(where: { $0.id == read.channelId }), app.channels[i].unread != 0 {
                refresh()
            }
            return read
        }
        let c = try viewable(id, who)
        let latest = msgs(c.id).last?.number ?? 0
        var n = latest
        if let u = upTo?.trimmingCharacters(in: .whitespaces), !u.isEmpty {
            guard let k = Self.messageNumber(u) else { throw ChannelError(400, "upTo 要是訊息 id(M-n)") }
            n = min(k, latest)
        }
        let before = lastRead(c.id, who)
        setRead(c.id, who, n)
        // 已讀沒有前進就不寫檔、不通知畫面(避免打開頻道時重畫迴圈)
        if lastRead(c.id, who) != before { save() }
        return data.reads.first { $0.channelId == c.id && $0.member == who }!
    }

    /// 提到 node 的未讀訊息(新的在前,最多 20 則)與它所在頻道的未讀數
    func inbox(for node: String) throws -> ChannelInbox {
        if let r = remote {
            let b = try r.channelInbox(org: app.currentOrgId, for: node)
            remoteError = nil
            return b
        }
        let mine = data.channels.filter { $0.members.contains(node) }
        var mentions: [ChannelInbox.Mention] = []
        for c in mine {
            let lr = lastRead(c.id, node)
            for m in data.messages where m.channelId == c.id && m.number > lr && m.author != node && m.mentions.contains(node) {
                mentions.append(.init(channelId: c.id, channelName: c.name, id: m.id, author: m.author, text: m.text, at: m.at))
            }
        }
        mentions.sort { ($0.at, $0.channelId, Self.messageNumber($0.id) ?? 0) > ($1.at, $1.channelId, Self.messageNumber($1.id) ?? 0) }
        let channels = mine.sorted { $0.number < $1.number }.map { ChannelInbox.Unread(id: $0.id, name: $0.name, unread: unread($0, for: node)) }
        return ChannelInbox(mentions: Array(mentions.prefix(Self.inboxLimit)), channels: channels)
    }

    /// 本機:刪角色、從組織移除成員時,把該節點從所有頻道成員拿掉
    func pruneMember(_ node: String) {
        guard !isRemote else { return }   // 遠端由伺服器處理
        var changed = false
        for i in data.channels.indices where data.channels[i].members.contains(node) {
            data.channels[i].members.removeAll { $0 == node }
            data.channels[i].updatedAt = Self.now()
            changed = true
        }
        if changed { save() }
    }

    /// Issue 的對話頻道:已有(看得到的)就回傳,沒有就建立(名稱用 Issue 標題,成員含自己與指派的角色)
    func openForIssue(_ issue: OpsIssue) throws -> ChannelSummary {
        if let found = (try? list(issue: issue.id))?.first { return found }
        var members: [String] = []
        if let a = issue.assignedAgent { members.append(ChainNode.agent(a)) }
        let title = issue.title.trimmingCharacters(in: .whitespacesAndNewlines)
        let name = String((title.isEmpty ? issue.id : title).prefix(Self.maxName))
        return try create(name: name, issueId: issue.id, members: members).channel
    }
}
