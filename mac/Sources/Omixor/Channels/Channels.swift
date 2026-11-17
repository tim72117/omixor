import Foundation

// 訊息頻道:把組織成員(人)與虛擬角色拉進同一個頻道對話。頻道可以自由建立,也可以綁一個 Issue(一個 Issue 只有一個頻道)。
// @ 一個角色只通知、不排任務;角色下次執行(agent_context)時看得到提到它的未讀訊息。
// 成員節點同組織鏈:"agent:<角色 id>" 或 "user:<email 小寫>"(本機模式的人是 app 的人員 id,例如 user:tim)。
//
// 一律透過 core server 的 /api/orgs/<組織>/channels(ChannelRemote.swift;遠端是 omixor server,本機是 App 啟動的本機守護服務),
// 規則(名稱長度、成員、綁 Issue、權限、未讀、@ 解析)由伺服器強制。
// 組織資料夾的 channels.json 只在第一次啟動本機守護服務時匯入,留著當備份。

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

    // 寬鬆解碼(LossyDecoding.swift):伺服器的 members 可能是 null、少欄位不要整份讀不出來;id 必填
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lossy(.name, default: "")
        topic = c.lossy(.topic, default: "")
        issueId = c.lossy(.issueId, default: "")
        members = c.lossyArray(String.self, .members)
        createdBy = c.lossy(.createdBy, default: "")
        createdAt = c.lossy(.createdAt, default: .distantPast)
        updatedAt = c.lossy(.updatedAt, default: createdAt)
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
        unread = c.lossy(.unread, default: 0)
        lastMessageAt = c.lossyOptional(Date.self, .lastMessageAt)
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
        channelId = c.lossy(.channelId, default: "")
        author = c.lossy(.author, default: "")
        text = c.lossy(.text, default: "")
        mentions = c.lossyArray(String.self, .mentions)
        at = c.lossy(.at, default: .distantPast)
    }
}

/// 已讀:每個成員每個頻道一筆
struct ChannelRead: Codable, Hashable {
    var channelId: String
    var member: String
    var lastRead: Int
    var at: Date
}

extension ChannelRead {
    // 寬鬆解碼(LossyDecoding.swift):缺的欄位用預設值(lastRead 0、時間 distantPast)
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        channelId = c.lossy(.channelId, default: "")
        member = c.lossy(.member, default: "")
        lastRead = c.lossy(.lastRead, default: 0)
        at = c.lossy(.at, default: .distantPast)
    }
}

/// 訊息列表的一頁(依時間由舊到新)
struct ChannelPage: Codable, Hashable {
    var messages: [ChannelMessage]
    var hasMore: Bool

    init(messages: [ChannelMessage], hasMore: Bool) { self.messages = messages; self.hasMore = hasMore }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        messages = c.lossyArray(ChannelMessage.self, .messages)
        hasMore = c.lossy(.hasMore, default: false)
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
        mentions = c.lossyArray(Mention.self, .mentions)
        channels = c.lossyArray(Unread.self, .channels)
    }
}

extension ChannelInbox.Mention {
    // 寬鬆解碼:id 必填(缺了略過這一則),其他用預設值
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        channelId = c.lossy(.channelId, default: "")
        channelName = c.lossy(.channelName, default: "")
        author = c.lossy(.author, default: "")
        text = c.lossy(.text, default: "")
        at = c.lossy(.at, default: .distantPast)
    }
}

extension ChannelInbox.Unread {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lossy(.name, default: id)
        unread = c.lossy(.unread, default: 0)
    }
}

/// 頻道的錯誤:status 同核心的 HTTP 狀態(400、403、404、409)
struct ChannelError: LocalizedError {
    var status: Int
    var message: String
    init(_ status: Int, _ message: String) { self.status = status; self.message = message }
    var errorDescription: String? { message }
}

// MARK: - @ 輸入

/// @ 的解析在 core(model.ParseMentions);App 只剩輸入框判斷 @ 前面是不是英數用的小工具
enum ChannelMention {
    static func isAlnum(_ c: Character) -> Bool { c.isASCII && (c.isLetter || c.isNumber) }
}

// MARK: - 存放

@MainActor
final class ChannelStore {
    let app: AppState
    init(app: AppState) { self.app = app }

    static let maxName = 80
    static let defaultLimit = 50
    static let maxLimit = 200

    // MARK: core server
    // 頻道一律透過 core server(IssueServerConfig.current:遠端 omixor 或本機守護服務),規則由伺服器強制。

    private var remoteClient: IssueRemote?
    /// 目前連的 core server 的用戶端(和 Issue 同一個;連線換了會換新的)
    var remote: IssueRemote {
        let cfg = IssueServerConfig.current
        if let c = remoteClient, c.matches(cfg) { return c }   // 連線設定與組織的 token 範圍都相同才沿用
        let c = IssueRemote(config: cfg)
        remoteClient = c
        return c
    }
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
            Task { @MainActor in await self?.poll() }
        }
    }

    private var polling = false
    /// 定時重讀(頻道清單只由這裡定時讀;畫面不另外輪詢):請求在背景跑,上一輪還沒回來就跳過這一輪
    private func poll() async {
        guard !polling else { return }
        polling = true
        defer { polling = false }
        await reload()
    }

    /// 在背景讀頻道清單與提到我的未讀訊息,回主執行緒才更新畫面(定時重讀、切換組織、打開頁面用;不擋主執行緒)
    func reload() async {
        let org = app.currentOrgId, r = remote, me = meNode, before = app.channels
        do {
            let (list, inbox) = try await r.run { r in
                (try r.listChannels(org: org, issue: nil), try? r.channelInbox(org: org, for: me))
            }
            guard app.currentOrgId == org else { return }
            remoteError = nil
            if app.channels == before, list != app.channels {
                app.channels = list
                app.channelsVersion += 1
            }
            if let b = inbox, b.mentions != app.myMentions { app.myMentions = b.mentions }
        } catch {
            if app.currentOrgId == org { remoteFailed(error) }
        }
    }

    /// 換組織:先清掉上一個組織的頻道與「提到我」,之後由 reload() 在背景讀
    func clearForOrgChange() {
        app.channels = []
        if !app.myMentions.isEmpty { app.myMentions = [] }
    }

    /// 我的節點:登入遠端時是登入的 email;本機守護服務的使用者就是 App 的使用者(user:<app.me>)
    var meNode: String {
        if let e = app.omixorLogin.account?.email, !e.isEmpty { return ChainNode.user(e) }
        return ChainNode.user(app.me)
    }

    // MARK: 讀寫

    func load() {
        app.channels = []
        refresh()
    }

    /// 更新「提到我的未讀訊息」(需要協助區塊用)
    func refreshMentions() {
        guard let b = try? inbox(for: meNode) else { return }
        if b.mentions != app.myMentions { app.myMentions = b.mentions }
    }

    /// 重新讀頻道清單
    func refresh() {
        do {
            let list = try remote.listChannels(org: app.currentOrgId, issue: nil)
            remoteError = nil
            if list != app.channels {
                app.channels = list
                app.channelsVersion += 1
            }
            refreshMentions()
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

    /// 畫面輸入的成員:純 email 換成 user:<email>,其他照原樣交給伺服器解析(agent:<id 或名稱>、角色 id 或名稱)
    static func memberInput(_ s: String) -> String { s.contains("@") && !s.contains(":") ? ChainNode.user(s) : s }

    /// 伺服器的錯誤換成 ChannelError(保留 HTTP 狀態碼:400、403、404、409)
    private func ch<T>(_ f: (IssueRemote) throws -> T) throws -> T {
        do { return try f(remote) } catch let e as IssueRemoteError {
            throw ChannelError(e.status, e.message)
        }
    }

    /// 同 ch,但請求在背景跑(不擋主執行緒)
    private func chAsync<T>(_ r: IssueRemote, _ f: @escaping (IssueRemote) throws -> T) async throws -> T {
        do { return try await r.run(f) } catch let e as IssueRemoteError {
            throw ChannelError(e.status, e.message)
        }
    }

    // MARK: API(actor = 以誰的身分,nil = 我;伺服器管理員可以代角色或別人操作)

    /// 我(或 actor)看得到的頻道;issue 只找綁該 Issue 的
    func list(issue: String? = nil, as actor: String? = nil) throws -> [ChannelSummary] {
        let r = remote
        var list = try ch { try $0.listChannels(org: app.currentOrgId, issue: issue) }
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

    /// 一個頻道(id 或名稱)
    func channel(_ ref: String, as actor: String? = nil) throws -> ChannelSummary {
        let s = ref.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.uppercased().hasPrefix("CH-"), Int(s.dropFirst(3)) != nil {
            let c = try ch { try $0.getChannel(org: app.currentOrgId, id: s.uppercased()) }
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

    /// 建立頻道(角色不能建)。綁 Issue 而且已有看得到的頻道時回傳那個,created = false
    @discardableResult
    func create(name rawName: String, topic rawTopic: String = "", issueId rawIssue: String = "", members rawMembers: [String] = [],
                as actor: String? = nil, via: Actor? = nil) throws -> (channel: ChannelSummary, created: Bool) {
        let who = actor ?? meNode
        let name = rawName.trimmingCharacters(in: .whitespacesAndNewlines)
        let topic = rawTopic.trimmingCharacters(in: .whitespacesAndNewlines)
        let issue = rawIssue.trimmingCharacters(in: .whitespacesAndNewlines)
        let known = Set(app.channels.map(\.id))
        let c = try ch { try $0.createChannel(org: app.currentOrgId, name: name, topic: topic, issueId: issue, members: rawMembers) }
        remoteError = nil
        let created = !known.contains(c.id)
        if created { logCreate(c.channel, via ?? actorOf(who)) }
        refresh()
        return (c, created)
    }

    private func logCreate(_ c: Channel, _ by: Actor) {
        app.log(by, "bubble.left.and.bubble.right", "建立頻道「\(c.name)」" + (c.hasIssue ? "(\(c.issueId))" : ""), .channels)
    }

    /// 改名稱、主題(成員或組織管理員;角色不能改)
    @discardableResult
    func update(_ id: String, name newName: String? = nil, topic newTopic: String? = nil, as actor: String? = nil) throws -> ChannelSummary {
        let n = newName?.trimmingCharacters(in: .whitespacesAndNewlines)
        let t = newTopic?.trimmingCharacters(in: .whitespacesAndNewlines)
        let c = try ch { try $0.updateChannel(org: app.currentOrgId, id: id, name: n, topic: t) }
        remoteError = nil
        refresh()
        return c
    }

    /// 加減成員(成員或組織管理員;角色不能改)
    @discardableResult
    func changeMembers(_ id: String, add: [String] = [], remove: [String] = [], as actor: String? = nil) throws -> ChannelSummary {
        let c = try ch { try $0.changeChannelMembers(org: app.currentOrgId, id: id, add: add, remove: remove) }
        remoteError = nil
        refresh()
        return c
    }

    /// 訊息:給 since(M-n 或時間)→ 之後最早的 limit 筆;沒給 → 最新的 limit 筆;before 只取之前的。都由舊到新
    func messages(_ id: String, since: String? = nil, before: String? = nil, limit: Int? = nil, as actor: String? = nil) throws -> ChannelPage {
        let lim = min(Self.maxLimit, max(1, limit ?? Self.defaultLimit))
        let p = try ch { try $0.channelMessages(org: app.currentOrgId, id: id, since: since, before: before, limit: lim) }
        remoteError = nil
        return p
    }

    /// 同 messages,但在背景讀(訊息串畫面用)。回傳時組織已經換了 → nil
    func messagesInBackground(_ id: String, since: String? = nil, before: String? = nil, limit: Int? = nil) async throws -> ChannelPage? {
        let org = app.currentOrgId, r = remote
        let lim = min(Self.maxLimit, max(1, limit ?? Self.defaultLimit))
        do {
            let p = try await chAsync(r) { try $0.channelMessages(org: org, id: id, since: since, before: before, limit: lim) }
            guard app.currentOrgId == org else { return nil }
            remoteError = nil
            return p
        } catch {
            guard app.currentOrgId == org else { return nil }
            throw error
        }
    }

    /// 同 markRead(我自己),但在背景寫;頻道清單有未讀時再於背景重讀清單
    func markReadInBackground(_ id: String) async {
        let org = app.currentOrgId, r = remote
        guard let read = try? await chAsync(r, { try $0.markChannelRead(org: org, id: id, upTo: nil, member: nil) }),
              app.currentOrgId == org else { return }
        remoteError = nil
        if let i = app.channels.firstIndex(where: { $0.id == read.channelId }), app.channels[i].unread != 0 {
            await reload()
        }
    }

    /// 發訊息(要是頻道成員);@ 到的角色記到動態牆
    @discardableResult
    func post(_ id: String, text raw: String, as actor: String? = nil, via: Actor? = nil) throws -> ChannelMessage {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { throw ChannelError(400, "訊息不能是空的") }
        let m = try ch { try $0.postChannelMessage(org: app.currentOrgId, id: id, text: text, author: actor) }
        remoteError = nil
        let chName = app.channels.first { $0.id == m.channelId }?.name ?? m.channelId
        logMentions(m, channelName: chName, via ?? actorOf(m.author))
        refresh()
        return m
    }

    /// 被 @ 的角色記到動態牆
    private func logMentions(_ m: ChannelMessage, channelName: String, _ by: Actor) {
        let agents = m.mentions.filter(ChainNode.isAgent)
        guard !agents.isEmpty else { return }
        app.log(by, "at", "在頻道「\(channelName)」提到 " + agents.map(name(of:)).joined(separator: "、"), .channels)
    }

    /// 標已讀到 upTo(M-n;沒給 = 最新);已讀只會往前,不會倒退
    @discardableResult
    func markRead(_ id: String, upTo: String? = nil, as actor: String? = nil) throws -> ChannelRead {
        let read = try ch { try $0.markChannelRead(org: app.currentOrgId, id: id, upTo: upTo, member: actor) }
        remoteError = nil
        if actor == nil, let i = app.channels.firstIndex(where: { $0.id == read.channelId }), app.channels[i].unread != 0 {
            refresh()
        }
        return read
    }

    /// 提到 node 的未讀訊息(新的在前,最多 20 則)與它所在頻道的未讀數
    func inbox(for node: String) throws -> ChannelInbox {
        let b = try ch { try $0.channelInbox(org: app.currentOrgId, for: node) }
        remoteError = nil
        return b
    }

    /// Issue 的對話頻道:已有(看得到的)就回傳,沒有就建立(名稱用 Issue 標題,成員含自己與指派的角色)
    func openForIssue(_ issue: Issue) throws -> ChannelSummary {
        if let found = (try? list(issue: issue.id))?.first { return found }
        var members: [String] = []
        if let a = issue.assignedAgent { members.append(ChainNode.agent(a)) }
        let title = issue.title.trimmingCharacters(in: .whitespacesAndNewlines)
        let name = String((title.isEmpty ? issue.id : title).prefix(Self.maxName))
        return try create(name: name, issueId: issue.id, members: members).channel
    }

}
