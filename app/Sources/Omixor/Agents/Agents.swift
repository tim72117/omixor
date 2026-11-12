import Foundation
import SwiftUI

// 虛擬角色:每個角色有職責說明(給 AI 的角色設定)、例行任務與被指派的單次任務。
// Ops 不自己跑 AI:任務排在這裡,由 Claude Code 透過 MCP 以角色身分領取(ops_agent_task_claim)、做完回報(ops_agent_task_report)。
// 例行任務到時間時,Ops 會替該角色排一筆任務等人叫 AI 來領。
// 存在 ~/Library/Application Support/Ops/agents.json(OPS_DATA_DIR 可改)。

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
    var author: String            // person id、"mcp" 或角色 id(以 agent: 開頭)
    var text: String
    var at = Date.now
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

    var number: Int { Int(id.filter(\.isNumber)) ?? 0 }
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

    func load() {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        if let data = try? Data(contentsOf: fileURL), let v = try? d.decode(AgentData.self, from: data) {
            app.agents = v.agents
            app.agentTasks = v.tasks
        } else {
            app.agents = Self.defaults
            app.agentTasks = []
            save()
        }
    }

    func save() {
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
        if author.hasPrefix("agent:") { return agent(String(author.dropFirst(6)))?.name ?? author }
        return app.name(of: author == IssueStore.mcpAuthor ? .mcp : .person(author))
    }

    // MARK: - 角色

    func upsert(_ a: Agent) {
        if let i = app.agents.firstIndex(where: { $0.id == a.id }) { app.agents[i] = a } else { app.agents.append(a) }
        save()
    }

    /// 刪除角色:未完成的任務一併取消
    func delete(_ id: String) {
        for i in app.agentTasks.indices where app.agentTasks[i].agentId == id && app.agentTasks[i].status.isOpen {
            app.agentTasks[i].status = .canceled
            app.agentTasks[i].updatedAt = .now
        }
        app.agents.removeAll { $0.id == id }
        save()
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
                by author: String, routineId: String? = nil) -> AgentTask? {
        guard let agent = agent(agentId) else { return nil }
        let num = (app.agentTasks.map(\.number).max() ?? 0) + 1
        let t = AgentTask(id: "T-\(num)", title: title, instructions: instructions, agentId: agent.id,
                          priority: priority, createdBy: author, routineId: routineId)
        app.agentTasks.insert(t, at: 0)
        save()
        let who: Actor = author == Self.routineAuthor ? .system : IssueStore(app: app).actor(author)
        app.log(who, "person.badge.clock", "指派 \(t.id)「\(title)」給\(agent.name)", .automation)
        return t
    }

    @discardableResult
    func update(_ id: String, _ change: (inout AgentTask) -> Void) -> AgentTask? {
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

    /// 角色領取任務:給 id 就領那筆,否則領最優先、最早的一筆待領取任務
    func claim(agent agentId: String, task id: String? = nil) throws -> AgentTask? {
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
        guard let t = task(id) else { throw MCPToolError("找不到任務 \(id)") }
        guard t.status != .canceled else { throw MCPToolError("\(t.id) 已取消") }
        let author = "agent:\(t.agentId)"
        if let note { _ = self.note(t.id, note, by: author) }
        let updated = update(t.id) {
            if let status { $0.status = status }
            if let result { $0.result = result }
            if let links { $0.links = IssueStore.clean($0.links + links) }
        }
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

    // MARK: - 預設角色

    /// 預設角色的文件櫃存取範圍
    static let defaultDocAccess: [String: [String: DocAccess]] = [
        "visitor-analyst": ["members": .write, "overview": .read],
        "narrator": ["levels": .read, "/docs/research/voice-cost": .read, "/docs/research/tts-landscape": .read],
        "triager": ["todo": .write],
        "course-dev": ["levels": .write, "dev": .read, "ideas": .read, "docs": .read],
        "marketing": ["overview": .read, "levels": .read, "docs": .read, "/docs/research/marketing": .write],
        "product-manager": ["overview": .write, "todo": .write, "ideas": .write, "levels": .read, "dev": .read,
                            "docs": .read, "members": .read],
    ]

    static var defaults: [Agent] {
        baseDefaults.map { a in
            var a = a
            a.docAccess = defaultDocAccess[a.id] ?? [:]
            return a
        }
    }

    private static var baseDefaults: [Agent] {
        [
            Agent(id: "visitor-analyst", name: "訪客分析師", role: "盯 evoleap 網站的訪客與播放數字",
                  symbol: "chart.line.uptrend.xyaxis", color: "teal",
                  instructions: """
                  你負責 evoleap 網站的訪客分析。
                  - 用 ops_site_visitors 讀數字(1d、7d 都看),和上次比較。
                  - 看漏斗(打開 → 載入完成 → 有播放)、各課概況、載入錯誤、登入門檻。
                  - 發現異常(播放率掉、某課載入失敗、某課打開很多但沒人播)時,先用 ops_issues 搜尋,沒有才用 ops_issue_create 開 Issue,標籤加 evoleap、訪客。
                  - 回報時列出關鍵數字與你的判斷;樣本太小就說明不下結論。
                  """,
                  routines: [AgentRoutine(title: "每日訪客檢查",
                                          instructions: "讀 1d 與 7d 的訪客統計,摘要重點數字,有異常就開 Issue 或在既有 Issue 留言。",
                                          kind: .daily, hour: 9, minute: 0)]),
            Agent(id: "narrator", name: "旁白製作", role: "把課程旁白做成語音",
                  symbol: "waveform", color: "purple",
                  instructions: """
                  你負責 shuttle 課程的旁白語音。
                  - 先用 ops_voice_submit 加 dry_run 試算有哪些段落缺語音,再決定要不要送批次;一次只會有一個批次在跑。
                  - 用 ops_voice_jobs 追蹤進度。
                  - 你不能發佈:批次完成後回報,請人在 omixor 的 CI/CD › 旁白發佈 試聽後按發佈。
                  """,
                  routines: [AgentRoutine(title: "檢查缺少語音的旁白",
                                          instructions: "用 ops_voice_submit 的 dry_run 試算,回報有幾段缺語音、預估費用;不要直接送出。",
                                          kind: .weekly, hour: 10, minute: 0, weekday: 2)]),
            Agent(id: "triager", name: "Issue 整理員", role: "整理未解決的 Issue",
                  symbol: "tray.full", color: "orange",
                  instructions: """
                  你負責整理 omixor 的 Issue。
                  - 用 ops_issues 列出未解決的 Issue。
                  - 重複的在較新的那筆留言指向舊的並關閉;優先度不合理的調整並留言說明理由。
                  - 不要關閉你不確定已解決的 Issue,改成留言詢問。
                  - 回報時列出你改了哪些。
                  """,
                  routines: [AgentRoutine(title: "每週整理 Issue",
                                          instructions: "整理所有未解決的 Issue:合併重複、調整優先度,回報變更清單。",
                                          kind: .weekly, hour: 17, minute: 0, weekday: 6)]),
            Agent(id: "course-dev", name: "課程開發", role: "規劃與撰寫 evoleap 的課程腳本",
                  symbol: "doc.text", color: "indigo",
                  instructions: """
                  你負責 evoleap 的課程開發,課程與腳本在 shuttle 專案(/Users/caitingyu/Documents/shuttle)。
                  - 課程結構:courses_seed.json(server/internal/store/)→ 單元 → 課;每課的腳本在 web/src/content/live/<課程>/*.json,旁白語音是同名的 .voice.json。
                  - 目前的課:AI 工具介紹、AI 工具入門、Vibe Coding 入門、網頁怎麼運作、給孩子的網頁程式、從做好到上線、Claude Code 工作術;AI Agent 兩門還是空的。
                  - 寫新課或改腳本時,先讀同一門課現有的腳本,沿用語氣、長度與格式;改完跑 shuttle 的課程一致性測試(web/src/content/courseMap.test.ts)。
                  - 只在本機改檔案,不要 commit、不要部署、不要用 evoleap script push 或 lesson set 改線上資料,也不要連資料庫;做完在結果列出改了哪些檔,請人審。
                  - 旁白要配語音時,指派給「旁白製作」(ops_agent_task_create),不要自己送批次。
                  - 參考訪客數字(ops_site_visitors)找出沒人播、或中途離開多的課,作為改善的依據。
                  """,
                  routines: [AgentRoutine(title: "課程成效回顧",
                                          instructions: "用 ops_site_visitors 看 7d 各課的打開、播放與載入錯誤,挑出最需要改善的 1~2 課,提出具體的改寫方向(不要直接改檔),有需要就開 Issue。",
                                          kind: .weekly, hour: 10, minute: 0, weekday: 3)]),
            Agent(id: "marketing", name: "行銷", role: "帶更多訪客認識 evoleap 的課程",
                  symbol: "megaphone", color: "pink",
                  instructions: """
                  你負責 evoleap(https://evoleap.io)的行銷。
                  - 用 ops_site_visitors 看訪客從哪裡來(進站來源、裝置)、首頁停留、從首頁進到哪些課、登入門檻擋掉多少人。
                  - 根據數字提出行銷想法:社群貼文、短影片腳本、課程介紹文案、首頁或課程頁的標題與一句話、SEO 關鍵字。宣傳素材腳本在 shuttle 的 web/src/content/live/promo/。
                  - 文案用繁體中文,口吻親切、具體,講學員能做出什麼,不誇大。
                  - 你不能對外發佈任何東西(不發貼文、不寄信、不改網站);草稿寫在任務結果裡,請人決定要不要用。
                  - 有值得追蹤的想法或問題,開 Issue,標籤加 行銷。
                  """,
                  routines: [AgentRoutine(title: "每週行銷週報",
                                          instructions: "整理 7d 的訪客來源、首頁停留與各課熱度,和上週比較;提出 3 則下週可以發的社群貼文草稿。",
                                          kind: .weekly, hour: 9, minute: 30, weekday: 2)]),
        ]
    }
}
