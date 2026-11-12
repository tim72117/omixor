import CryptoKit
import Foundation
import SwiftUI

// 虛擬角色:每個角色有職責說明(給 AI 的角色設定)、例行任務與被指派的單次任務。
// Ops 不自己跑 AI:任務排在這裡,由 Claude Code 透過 MCP 以角色身分領取(ops_agent_task_claim)、做完回報(ops_agent_task_report)。
// 例行任務到時間時,Ops 會替該角色排一筆任務等人叫 AI 來領。
// 本機模式存在組織資料夾的 agents.json(~/Library/Application Support/Ops/orgs/<id>/,OPS_DATA_DIR 可改);
// 遠端模式改讀寫 omixor server 的 /agents 與 /agent-tasks(AgentRemote.swift),例行任務由伺服器排,領取時伺服器發角色憑證。
// 沒有內建預設角色,也不從其他檔案匯入:新組織一開始沒有任何角色,由使用者自己建立。

struct Agent: Codable, Hashable, Identifiable {
    var id: String
    var name: String
    var role = ""                 // 一句話職稱
    var symbol = "person.crop.circle"
    var color = "blue"            // AgentPalette 的名稱
    var instructions = ""         // 給 AI 的角色設定(Markdown)
    var active = true
    var routines: [AgentRoutine] = []
    var createdAt = Date.now
    /// 文件櫃存取範圍:key 是區塊 id(例如 levels)或頁籤網址(例如 /studio/tools);頁籤的設定優先於區塊
    var docAccess: [String: DocAccess] = [:]

    /// 這個角色對某個位置的權限(nil = 不能存取)
    func access(section: String, tab: String?) -> DocAccess? {
        if let tab, let a = docAccess[tab] { return a == .none ? nil : a }
        guard let a = docAccess[section], a != .none else { return nil }
        return a
    }

    func canRead(section: String, tab: String?) -> Bool { access(section: section, tab: tab) != nil }
    func canWrite(section: String, tab: String?) -> Bool { access(section: section, tab: tab) == .write }
}

enum DocAccess: String, Codable, CaseIterable, Identifiable {
    case none, read, write
    var id: String { rawValue }
    var label: String { self == .none ? "不能存取" : self == .read ? "可讀" : "可讀寫" }
}

extension Agent {
    // 手寫解碼:舊的 agents.json 沒有新欄位時用預設值,不會整份讀不出來
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        role = try c.decodeIfPresent(String.self, forKey: .role) ?? ""
        symbol = try c.decodeIfPresent(String.self, forKey: .symbol) ?? "person.crop.circle"
        color = try c.decodeIfPresent(String.self, forKey: .color) ?? "blue"
        instructions = try c.decodeIfPresent(String.self, forKey: .instructions) ?? ""
        active = try c.decodeIfPresent(Bool.self, forKey: .active) ?? true
        routines = try c.decodeIfPresent([AgentRoutine].self, forKey: .routines) ?? []
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? .now
        docAccess = try c.decodeIfPresent([String: DocAccess].self, forKey: .docAccess) ?? [:]
    }
}

enum AgentPalette {
    static let names = ["blue", "teal", "green", "orange", "pink", "purple", "indigo", "gray"]
    static func color(_ name: String) -> Color {
        switch name {
        case "teal": return .teal
        case "green": return .green
        case "orange": return .orange
        case "pink": return .pink
        case "purple": return .purple
        case "indigo": return .indigo
        case "gray": return .gray
        default: return .blue
        }
    }
}

enum RoutineKind: String, Codable, CaseIterable, Identifiable {
    case daily, weekly, hourly
    var id: String { rawValue }
    var label: String { self == .daily ? "每天" : self == .weekly ? "每週" : "每隔幾小時" }
}

/// 例行任務:到時間就替角色排一筆任務
struct AgentRoutine: Codable, Hashable, Identifiable {
    var id = UUID().uuidString
    var title: String
    var instructions = ""
    var kind: RoutineKind = .daily
    var hour = 9
    var minute = 0
    var weekday = 2               // 1 = 週日 … 7 = 週六(Calendar 的定義)
    var everyHours = 6
    var priority: IssuePriority = .normal
    var enabled = true
    var createdAt = Date.now
    var lastRunAt: Date?

    static let weekdayNames = ["日", "一", "二", "三", "四", "五", "六"]

    var scheduleLabel: String {
        let t = String(format: "%02d:%02d", hour, minute)
        switch kind {
        case .daily: return "每天 \(t)"
        case .weekly: return "每週\(Self.weekdayNames[(max(1, min(7, weekday)) - 1)] ) \(t)"
        case .hourly: return "每 \(everyHours) 小時"
        }
    }

    /// 上次排程(沒有就從建立時間)之後的下一個時間點
    func nextRun(calendar: Calendar = .current) -> Date {
        let base = lastRunAt ?? createdAt
        switch kind {
        case .hourly:
            return base.addingTimeInterval(TimeInterval(max(1, everyHours) * 3600))
        case .daily:
            return calendar.nextDate(after: base, matching: DateComponents(hour: hour, minute: minute), matchingPolicy: .nextTime) ?? base
        case .weekly:
            return calendar.nextDate(after: base, matching: DateComponents(hour: hour, minute: minute, weekday: weekday),
                                     matchingPolicy: .nextTime) ?? base
        }
    }
}

enum AgentTaskStatus: String, Codable, CaseIterable, Identifiable {
    case queued, inProgress, blocked, done, canceled
    var id: String { rawValue }
    var label: String {
        switch self {
        case .queued: return "待領取"
        case .inProgress: return "進行中"
        case .blocked: return "需要協助"
        case .done: return "完成"
        case .canceled: return "已取消"
        }
    }
    var color: Color {
        switch self {
        case .queued: return .secondary
        case .inProgress: return .blue
        case .blocked: return .orange
        case .done: return .green
        case .canceled: return .gray
        }
    }
    var icon: String {
        switch self {
        case .queued: return "tray"
        case .inProgress: return "arrow.triangle.2.circlepath"
        case .blocked: return "hand.raised"
        case .done: return "checkmark.circle"
        case .canceled: return "xmark.circle"
        }
    }
    var isOpen: Bool { self == .queued || self == .inProgress || self == .blocked }
}

struct AgentTaskNote: Codable, Hashable, Identifiable {
    var id = UUID()
    var author: String            // person id、"mcp" 或角色 id(以 agent: 開頭);伺服器的是名稱或 email
    var text: String
    var at = Date.now
}

extension AgentTaskNote {
    init(from decoder: Decoder) throws {
        // 伺服器(core)的留言 id 不一定是 UUID:換成由內容算出的固定 UUID(不會送回伺服器)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        author = try c.decodeIfPresent(String.self, forKey: .author) ?? ""
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        at = try c.decodeIfPresent(Date.self, forKey: .at) ?? .distantPast
        let raw: String? = try? c.decodeIfPresent(String.self, forKey: .id)
        id = raw.flatMap(UUID.init(uuidString:)) ?? AgentTaskNote.stableId(raw ?? "\(author)|\(at.timeIntervalSince1970)|\(text)")
    }

    static func stableId(_ s: String) -> UUID {
        var b = Array(SHA256.hash(data: Data(s.utf8)).prefix(16))
        b[6] = b[6] & 0x0f | 0x50
        b[8] = b[8] & 0x3f | 0x80
        return UUID(uuid: (b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15]))
    }
}

struct AgentTask: Codable, Hashable, Identifiable {
    var id: String                // T-1
    var title: String
    var instructions = ""         // Markdown
    var agentId: String
    var status: AgentTaskStatus = .queued
    var priority: IssuePriority = .normal
    var createdBy: String         // person id、"mcp" 或 "routine"
    var routineId: String?
    var createdAt = Date.now
    var updatedAt = Date.now
    var claimedAt: Date?
    var completedAt: Date?
    var result = ""               // 完成時的回報(Markdown)
    var links: [String] = []      // 相關的 Issue id 或網址
    var notes: [AgentTaskNote] = []
    /// 領取租約的到期時間(遠端模式、進行中才有);鎖定期間別人不能領
    var lockedUntil: Date?
    /// 領取者(email 或名稱,遠端模式才有,只供顯示)
    var claimedBy: String?

    var number: Int { Int(id.filter(\.isNumber)) ?? 0 }
    /// 相關的 Issue id(links 裡第一個 ISS-n)
    var issueId: String? { links.first { $0.uppercased().hasPrefix("ISS-") && Int($0.dropFirst(4)) != nil } }
}

extension AgentTask {
    // 手寫解碼:伺服器(core)沒有 routineId、links/notes 可能是 null、未知的狀態或優先度不要讓整份讀不出來
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        instructions = try c.decodeIfPresent(String.self, forKey: .instructions) ?? ""
        agentId = try c.decodeIfPresent(String.self, forKey: .agentId) ?? ""
        status = (try? c.decodeIfPresent(String.self, forKey: .status)).flatMap(AgentTaskStatus.init) ?? .queued
        priority = (try? c.decodeIfPresent(String.self, forKey: .priority)).flatMap(IssuePriority.init) ?? .normal
        createdBy = try c.decodeIfPresent(String.self, forKey: .createdBy) ?? ""
        routineId = try c.decodeIfPresent(String.self, forKey: .routineId)
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? .distantPast
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt) ?? createdAt
        claimedAt = try c.decodeIfPresent(Date.self, forKey: .claimedAt)
        completedAt = try c.decodeIfPresent(Date.self, forKey: .completedAt)
        result = try c.decodeIfPresent(String.self, forKey: .result) ?? ""
        links = try c.decodeIfPresent([String].self, forKey: .links) ?? []
        notes = try c.decodeIfPresent([AgentTaskNote].self, forKey: .notes) ?? []
        lockedUntil = try c.decodeIfPresent(Date.self, forKey: .lockedUntil)
        claimedBy = try c.decodeIfPresent(String.self, forKey: .claimedBy)
    }

    /// 租約文字(進行中且有鎖定時間才有),例如「鎖定到 11:50」
    var leaseLabel: String? {
        guard status == .inProgress, let u = lockedUntil else { return nil }
        let who = claimedBy.flatMap { $0.isEmpty ? nil : $0 }
        return (who.map { "\($0) · " } ?? "") + "鎖定到 " + u.formatted(date: .omitted, time: .shortened)
    }
}

/// 領取時伺服器發的角色憑證(綁單一任務;secret 只在領取時給一次,不要存檔)
struct AgentCredential: Hashable {
    var agentId: String
    var taskId: String
    var secret: String
    var expiresAt: String
    /// 角色端點(<server>/mcp/agent),用 Authorization: Bearer <agentId>:<secret> 連
    var endpoint: String
}

/// 領取結果:沒有可領的任務時 task 是 nil;本機模式沒有 credential
struct AgentClaim {
    var agent: Agent?
    var task: AgentTask?
    var credential: AgentCredential?
}

struct AgentData: Codable {
    var agents: [Agent] = []
    var tasks: [AgentTask] = []
}

@MainActor
final class AgentStore {
    let app: AppState
    private var timer: Timer?
    init(app: AppState) { self.app = app }

    static let routineAuthor = "routine"

    private var fileURL: URL { app.dataDir.appendingPathComponent("agents.json") }

    // MARK: - 遠端(omixor server)

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
    /// 遠端模式多久重新讀一次(秒);0 = 不定時讀
    static var pollInterval: TimeInterval = NSClassFromString("XCTestCase") != nil ? 0 : 10

    private func remoteFailed(_ e: Error) {
        let msg = e.localizedDescription
        if remoteError != msg { NSLog("虛擬角色(omixor server):%@", msg) }
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

    /// 遠端模式:重新讀目前組織的角色與任務(內容有變才更新畫面)
    @discardableResult
    func refresh() -> Bool {
        guard let r = remote else { return false }
        do {
            let org = app.currentOrgId
            let agents = try r.listAgents(org: org)
            let tasks = try r.listTasks(org: org).sorted { $0.number > $1.number }
            remoteError = nil
            if agents != app.agents { app.agents = agents }
            if tasks != app.agentTasks { app.agentTasks = tasks }
            loadChain()   // 組織鏈另外讀;讀不到(例如伺服器還沒有這個功能)不影響角色與任務
            return true
        } catch {
            remoteFailed(error)
            return false
        }
    }

    /// 把伺服器回傳的任務放進清單(新的放最前面)
    func merge(_ t: AgentTask) {
        if let i = app.agentTasks.firstIndex(where: { $0.id == t.id }) { app.agentTasks[i] = t } else { app.agentTasks.insert(t, at: 0) }
    }

    func load() {
        if isRemote {
            // 換組織或剛連上:先清掉,讀不到也不會顯示別的組織的角色
            app.agents = []
            app.agentTasks = []
            app.orgChain = OrgChain()
            refresh()
            startPolling()
            return
        }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        if let data = try? Data(contentsOf: fileURL), let v = try? d.decode(AgentData.self, from: data) {
            app.agents = v.agents
            app.agentTasks = v.tasks
        } else {
            // 沒有角色資料就是空的:不內建預設角色
            app.agents = []
            app.agentTasks = []
        }
        loadChain()
    }

    func save() {
        guard !isRemote else { return }   // 遠端模式每個動作各自送出
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? e.encode(AgentData(agents: app.agents, tasks: app.agentTasks)) {
            try? data.write(to: fileURL, options: .atomic)
        }
    }

    /// 每分鐘檢查例行任務
    func start(timer enabled: Bool = true) {
        runDueRoutines()
        guard enabled, timer == nil else { return }
        if isRemote { startPolling() }
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.runDueRoutines() }
        }
    }

    // MARK: - 查詢

    func agent(_ id: String) -> Agent? {
        app.agents.first { $0.id.caseInsensitiveCompare(id) == .orderedSame }
            ?? app.agents.first { $0.name == id }
    }

    func task(_ id: String) -> AgentTask? {
        app.agentTasks.first { $0.id.caseInsensitiveCompare(id) == .orderedSame }
    }

    func tasks(for agentId: String) -> [AgentTask] { app.agentTasks.filter { $0.agentId == agentId } }

    func authorName(_ author: String) -> String {
        if author == Self.routineAuthor { return "例行排程" }
        if author == ChainNode.author { return "組織鏈" }
        if author.hasPrefix("agent:") { return agent(String(author.dropFirst(6)))?.name ?? author }
        return app.name(of: author == IssueStore.mcpAuthor ? .mcp : .person(author))
    }

    // MARK: - 角色

    func upsert(_ a: Agent) {
        do { try save(agent: a) } catch { remoteFailed(error) }
    }

    /// 新增或修改角色;遠端模式送到伺服器(要組織管理員),回傳伺服器存下來的內容
    @discardableResult
    func save(agent a: Agent) throws -> Agent {
        if let r = remote {
            let saved = try r.saveAgent(org: app.currentOrgId, a)
            remoteError = nil
            if let i = app.agents.firstIndex(where: { $0.id == saved.id }) { app.agents[i] = saved } else { app.agents.append(saved) }
            return saved
        }
        if let i = app.agents.firstIndex(where: { $0.id == a.id }) { app.agents[i] = a } else { app.agents.append(a) }
        save()
        return a
    }

    /// 刪除角色:未完成的任務一併取消
    func delete(_ id: String) {
        if let r = remote {
            do {
                try r.deleteAgent(org: app.currentOrgId, id: id)
                remoteError = nil
                app.agents.removeAll { $0.id == id }
                refresh()
            } catch { remoteFailed(error) }
            return
        }
        for i in app.agentTasks.indices where app.agentTasks[i].agentId == id && app.agentTasks[i].status.isOpen {
            app.agentTasks[i].status = .canceled
            app.agentTasks[i].updatedAt = .now
        }
        app.agents.removeAll { $0.id == id }
        save()
        pruneChain(agent: id)
    }

    func newId(for name: String) -> String {
        let ascii = name.lowercased().unicodeScalars.map { CharacterSet.alphanumerics.contains($0) && $0.isASCII ? String($0) : "-" }.joined()
        let base = ascii.split(separator: "-").joined(separator: "-")
        let root = base.isEmpty ? "agent-\(app.agents.count + 1)" : base
        var id = root
        var n = 2
        while app.agents.contains(where: { $0.id == id }) { id = "\(root)-\(n)"; n += 1 }
        return id
    }

    // MARK: - 任務

    @discardableResult
    func assign(to agentId: String, title: String, instructions: String = "", priority: IssuePriority = .normal,
                links: [String] = [], by author: String, routineId: String? = nil) -> AgentTask? {
        guard let agent = agent(agentId) else { return nil }
        if let r = remote {
            do {
                let t = try r.assignTask(org: app.currentOrgId, agent: agent.id, title: title, instructions: instructions,
                                         priority: priority, links: links, author: author)
                remoteError = nil
                merge(t)
                app.log(IssueStore(app: app).actor(author), "person.badge.clock", "指派 \(t.id)「\(title)」給\(agent.name)", .automation)
                return t
            } catch {
                remoteFailed(error)
                return nil
            }
        }
        let num = (app.agentTasks.map(\.number).max() ?? 0) + 1
        let t = AgentTask(id: "T-\(num)", title: title, instructions: instructions, agentId: agent.id,
                          priority: priority, createdBy: author, routineId: routineId, links: links)
        app.agentTasks.insert(t, at: 0)
        save()
        let who: Actor = author == Self.routineAuthor ? .system : IssueStore(app: app).actor(author)
        app.log(who, "person.badge.clock", "指派 \(t.id)「\(title)」給\(agent.name)", .automation)
        return t
    }

    /// 只用在本機模式:遠端模式的狀態變更走 cancel、requeue、claim、report(伺服器決定時間與憑證)
    @discardableResult
    func update(_ id: String, _ change: (inout AgentTask) -> Void) -> AgentTask? {
        if isRemote {
            remoteError = "遠端模式不能直接改任務,請用取消、退回待領取、領取或回報"
            return nil
        }
        guard let i = app.agentTasks.firstIndex(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) else { return nil }
        var t = app.agentTasks[i]
        let before = t
        change(&t)
        guard t != before else { return t }
        if t.status != before.status {
            if t.status == .inProgress, t.claimedAt == nil { t.claimedAt = .now }
            t.completedAt = (t.status == .done || t.status == .canceled) ? .now : nil
        }
        t.updatedAt = .now
        app.agentTasks[i] = t
        save()
        return t
    }

    func note(_ id: String, _ text: String, by author: String) -> AgentTask? {
        let s = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { return task(id) }
        return update(id) { $0.notes.append(AgentTaskNote(author: author, text: s)) }
    }

    /// 取消任務(遠端要組織管理員)
    @discardableResult
    func cancel(_ id: String) -> AgentTask? {
        guard let r = remote else { return update(id) { $0.status = .canceled } }
        do {
            let t = try r.cancelTask(org: app.currentOrgId, id: id)
            remoteError = nil
            merge(t)
            return t
        } catch { remoteFailed(error); return nil }
    }

    /// 退回待領取 / 重新排入(遠端要組織管理員;領取的憑證會失效)
    @discardableResult
    func requeue(_ id: String) -> AgentTask? {
        guard let r = remote else { return update(id) { $0.status = .queued } }
        do {
            let t = try r.requeueTask(org: app.currentOrgId, id: id)
            remoteError = nil
            merge(t)
            return t
        } catch { remoteFailed(error); return nil }
    }

    /// 角色領取任務:給 id 就領那筆,否則領最優先、最早的一筆待領取任務
    func claim(agent agentId: String, task id: String? = nil) throws -> AgentTask? {
        try claimWithCredential(agent: agentId, task: id).task
    }

    /// 同 claim;遠端模式時伺服器另外發一組綁這筆任務的角色憑證(secret 只有這一次,不存檔)
    func claimWithCredential(agent agentId: String, task id: String? = nil) throws -> AgentClaim {
        if let r = remote {
            let name = agent(agentId)?.id ?? agentId
            do {
                let c = try r.claimTask(org: app.currentOrgId, agent: name, task: id)
                remoteError = nil
                if let t = c.task {
                    merge(t)
                    let a = c.agent ?? agent(t.agentId)
                    app.log(.ai(t.agentId), "figure.walk", "\(a?.name ?? t.agentId)開始處理 \(t.id)「\(t.title)」", .automation)
                }
                return c
            } catch {
                remoteFailed(error)
                throw MCPToolError(error.localizedDescription)
            }
        }
        let t = try claimLocal(agent: agentId, task: id)
        return AgentClaim(agent: agent(agentId), task: t, credential: nil)
    }

    private func claimLocal(agent agentId: String, task id: String?) throws -> AgentTask? {
        guard let agent = agent(agentId) else { throw MCPToolError("找不到角色 \(agentId)") }
        let pick: AgentTask?
        if let id {
            guard let t = task(id) else { throw MCPToolError("找不到任務 \(id)") }
            guard t.agentId == agent.id else { throw MCPToolError("\(t.id) 是指派給 \(self.agent(t.agentId)?.name ?? t.agentId) 的,不是 \(agent.name)") }
            guard t.status == .queued || t.status == .blocked || t.status == .inProgress else {
                throw MCPToolError("\(t.id) 已經\(t.status.label),不能再領")
            }
            pick = t
        } else {
            pick = tasks(for: agent.id).filter { $0.status == .queued }
                .sorted { ($0.priority, $0.createdAt) < ($1.priority, $1.createdAt) }.first
        }
        guard let t = pick else { return nil }
        let claimed = update(t.id) { $0.status = .inProgress }
        if t.status != .inProgress {
            app.log(.ai(agent.id), "figure.walk", "\(agent.name)開始處理 \(t.id)「\(t.title)」", .automation)
        }
        return claimed
    }

    func report(_ id: String, status: AgentTaskStatus?, result: String?, note: String?, links: [String]?) throws -> AgentTask? {
        if let r = remote {
            do {
                let before = task(id)
                let t = try r.reportTask(org: app.currentOrgId, id: id, status: status, result: result, note: note, links: links)
                remoteError = nil
                merge(t)
                if let status, status != before?.status, let a = agent(t.agentId) {
                    switch status {
                    case .done: app.log(.ai(a.id), "checkmark.circle", "\(a.name)完成 \(t.id)「\(t.title)」", .automation)
                    case .blocked: app.log(.ai(a.id), "hand.raised", "\(a.name)在 \(t.id)「\(t.title)」需要協助", .automation)
                    default: break
                    }
                }
                // 伺服器可能依組織鏈替建立者排了「檢視回報」任務:重讀一次
                if let status, status == .done || status == .blocked, status != before?.status { refresh() }
                return t
            } catch {
                remoteFailed(error)
                throw MCPToolError(error.localizedDescription)
            }
        }
        guard let t = task(id) else { throw MCPToolError("找不到任務 \(id)") }
        guard t.status != .canceled else { throw MCPToolError("\(t.id) 已取消") }
        let author = "agent:\(t.agentId)"
        if let note { _ = self.note(t.id, note, by: author) }
        let updated = update(t.id) {
            if let status { $0.status = status }
            if let result { $0.result = result }
            if let links { $0.links = IssueStore.clean($0.links + links) }
        }
        if let updated { chainReport(before: t, after: updated, note: note) }
        if let status, status != t.status, let a = agent(t.agentId) {
            switch status {
            case .done: app.log(.ai(a.id), "checkmark.circle", "\(a.name)完成 \(t.id)「\(t.title)」", .automation)
            case .blocked: app.log(.ai(a.id), "hand.raised", "\(a.name)在 \(t.id)「\(t.title)」需要協助", .automation)
            default: break
            }
        }
        return updated
    }

    // MARK: - 例行任務

    /// 到時間的例行任務各排一筆;上一筆還沒做完就不重複排
    func runDueRoutines(now: Date = .now) {
        guard !isRemote else { return }   // 遠端模式由伺服器排(core 的 RunDueRoutines)
        var changed = false
        for ai in app.agents.indices where app.agents[ai].active {
            for ri in app.agents[ai].routines.indices {
                let r = app.agents[ai].routines[ri]
                guard r.enabled, r.nextRun() <= now else { continue }
                app.agents[ai].routines[ri].lastRunAt = now
                changed = true
                let pending = app.agentTasks.contains { $0.routineId == r.id && $0.status.isOpen }
                if !pending {
                    assign(to: app.agents[ai].id, title: r.title, instructions: r.instructions, priority: r.priority,
                           by: Self.routineAuthor, routineId: r.id)
                }
            }
        }
        if changed { save() }
    }

    /// 手動立刻排一筆例行任務
    func runNow(agent agentId: String, routine routineId: String) {
        if let r = remote {
            do {
                merge(try r.runRoutine(org: app.currentOrgId, agent: agentId, routine: routineId, author: app.me))
                remoteError = nil
            } catch { remoteFailed(error) }
            return
        }
        guard let ai = app.agents.firstIndex(where: { $0.id == agentId }),
              let ri = app.agents[ai].routines.firstIndex(where: { $0.id == routineId }) else { return }
        let r = app.agents[ai].routines[ri]
        app.agents[ai].routines[ri].lastRunAt = .now
        assign(to: agentId, title: r.title, instructions: r.instructions, priority: r.priority, by: app.me, routineId: r.id)
    }

    /// 給 Claude Code 的一段話:貼到對話裡就會以角色身分領任務
    func prompt(agent: Agent, task: AgentTask? = nil) -> String {
        if let task {
            return "請以 omixor 的虛擬角色「\(agent.name)」(id: \(agent.id))處理任務 \(task.id):用 ops_agent_task_claim 領取,照角色設定與任務說明完成,過程用 ops_agent_task_report 回報,做完把結果回報成 done;開 Issue 或留言時帶 agent: \(agent.id)。"
        }
        return "請以 omixor 的虛擬角色「\(agent.name)」(id: \(agent.id))工作:用 ops_agent_task_claim 依序領取它的待領取任務,照角色設定與任務說明完成並用 ops_agent_task_report 回報,直到沒有任務為止;開 Issue 或留言時帶 agent: \(agent.id)。"
    }
}
