import Foundation
import SwiftUI

// Issue:要追蹤的問題或待辦,存在 core(目前連線的 core server:遠端 omixor 或本機模式的本機守護服務;IssueRemote.swift)。
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
    /// 所屬的程式碼倉庫(nil 或空 = 未標記):owner/name(GitHub)或 group/sub/project(GitLab),可加 github:、gitlab: 前綴;
    /// 格式見 IssueRepo.clean(同 core 的 model.CleanRepo)
    var repo: String?
    var createdBy: String          // person id、"mcp" 或 "agent:<角色 id>"
    var createdAt: Date = .now
    var updatedAt: Date = .now
    var closedAt: Date?
    var comments: [IssueComment] = []
    /// 指派給的虛擬角色 id(nil 或空 = 未指派)。遠端模式時伺服器會自動替該角色排任務(core/service/agents.go 的 syncAssignee)
    var assignee: String?

    var number: Int { Int(id.filter(\.isNumber)) ?? 0 }
    /// 指派的角色 id(空字串換成 nil)
    var assignedAgent: String? { assignee.flatMap { $0.isEmpty ? nil : $0 } }
    /// 所屬倉庫(空字串換成 nil)
    var repoTag: String? { repo.flatMap { $0.isEmpty ? nil : $0 } }
}

extension IssueComment {
    // 寬鬆解碼(LossyDecoding.swift):id 缺或不是 UUID 時換成由內容算出的固定 UUID(重讀時不會每次都變)
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        author = c.lossy(.author, default: "")
        text = c.lossy(.text, default: "")
        at = c.lossy(.at, default: .distantPast)
        let raw = c.lossyOptional(String.self, .id)
        id = raw.flatMap(UUID.init(uuidString:)) ?? AgentTaskNote.stableId(raw ?? "\(author)|\(at.timeIntervalSince1970)|\(text)")
    }
}

extension OpsIssue {
    // 寬鬆解碼(LossyDecoding.swift):labels、comments 是 null(Go 的 nil slice)或缺 → [];
    // 不認得的 status → open、priority → normal(同 web 的 normIssue);只有 id 是必填(缺了丟錯,列表會略過這一筆)
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = c.lossy(.title, default: "")
        body = c.lossy(.body, default: "")
        status = c.lossyEnum(.status, default: .open)
        priority = c.lossyEnum(.priority, default: .normal)
        labels = c.lossyArray(String.self, .labels)
        source = c.lossy(.source, default: "")
        link = c.lossy(.link, default: "")
        repo = c.lossyOptional(String.self, .repo)
        createdBy = c.lossy(.createdBy, default: "")
        createdAt = c.lossy(.createdAt, default: .distantPast)
        updatedAt = c.lossy(.updatedAt, default: createdAt)
        closedAt = c.lossyOptional(Date.self, .closedAt)
        comments = c.lossyArray(IssueComment.self, .comments)
        assignee = c.lossyOptional(String.self, .assignee)
    }
}

/// Issue 的倉庫標記規則,和 core 的 model.CleanRepo、model.RepoMatch 一致(存的時候 core 會再檢查一次)
enum IssueRepo {
    static let maxLength = 200
    static let prefixes = ["github:", "gitlab:"]

    /// 整理 repo 標記:去頭尾空白;只允許英數與 - _ . / :,最長 200;"" = 未標記。不合格回傳 nil
    static func clean(_ s: String) -> String? {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        guard t.utf8.count <= maxLength else { return nil }
        let ok = t.unicodeScalars.allSatisfy { c in
            ("a"..."z").contains(c) || ("A"..."Z").contains(c) || ("0"..."9").contains(c) || "-_./:".unicodeScalars.contains(c)
        }
        return ok ? t : nil
    }

    static func isValid(_ s: String) -> Bool { clean(s) != nil }

    /// 篩選比對:不分大小寫;平台前綴(github:、gitlab:)有一邊沒寫就忽略前綴比較,兩邊都寫就要相同
    static func matches(_ repo: String, _ filter: String) -> Bool {
        let a = repo.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let b = filter.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if a == b { return true }
        func strip(_ x: String) -> (String, Bool) {
            for p in prefixes where x.hasPrefix(p) { return (String(x.dropFirst(p.count)), true) }
            return (x, false)
        }
        let (ra, pa) = strip(a), (rb, pb) = strip(b)
        return ra == rb && !ra.isEmpty && (!pa || !pb)
    }

    /// 依 repo 篩選(MCP 的 issues repo 參數;空字串 = 不篩),同 core 的 service.FilterIssuesByRepo
    static func filter(_ list: [OpsIssue], _ repo: String?) -> [OpsIssue] {
        guard let r = repo, !r.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return list }
        return list.filter { matches($0.repo ?? "", r) }
    }

    /// 錯誤訊息(同 core)
    static let invalidMessage = "repo 只能用英數與 - _ . / :,最長 \(maxLength) 字(例如 owner/name、gitlab:group/sub/project)"
}

/// Issue 的讀寫;畫面與 MCP 共用。一律透過 core server(IssueServerConfig.current:遠端 omixor 或本機守護服務)的 API,
/// app.issues 是伺服器資料的副本(寫入後、切換組織、每隔幾秒重新讀)。
@MainActor
final class IssueStore {
    let app: AppState
    init(app: AppState) { self.app = app }

    static let mcpAuthor = "mcp"

    // MARK: core server

    private var remoteClient: IssueRemote?
    /// 目前連的 core server 的用戶端(連線換了會換新的)
    var remote: IssueRemote {
        let cfg = IssueServerConfig.current
        if let c = remoteClient, c.matches(cfg) { return c }   // 連線設定與組織的 token 範圍都相同才沿用
        let c = IssueRemote(config: cfg)
        remoteClient = c
        return c
    }
    /// 最近一次和伺服器溝通的錯誤(nil = 正常)
    private(set) var remoteError: String?
    private var lastFetch = Date.distantPast
    private var poller: Timer?
    /// 多久重新讀一次(秒);0 = 不定時讀
    static var pollInterval: TimeInterval = NSClassFromString("XCTestCase") != nil ? 0 : 5

    private func remoteFailed(_ e: Error) {
        let msg = e.localizedDescription
        if remoteError != msg { NSLog("Issue 伺服器:%@", msg) }
        remoteError = msg
    }

    private func startPolling() {
        guard poller == nil, Self.pollInterval > 0 else { return }
        poller = Timer.scheduledTimer(withTimeInterval: Self.pollInterval, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.poll() }
        }
    }

    private var polling = false
    /// 定時重讀:請求在背景跑,上一輪還沒回來就跳過這一輪
    private func poll() async {
        guard !polling else { return }
        polling = true
        defer { polling = false }
        await reload()
    }

    /// 在背景讀目前組織的 Issue,回主執行緒才更新畫面(定時重讀、切換組織用;不擋主執行緒)
    func reload() async {
        let org = app.currentOrgId, r = remote, before = app.issues
        do {
            let list = try await r.run { try $0.list(org: org) }
            guard app.currentOrgId == org else { return }
            lastFetch = .now
            remoteError = nil
            // 讀的期間畫面上的資料被寫入改過:這次讀到的可能比較舊,等下一輪
            guard app.issues == before else { return }
            if list != app.issues { app.issues = list }
        } catch {
            if app.currentOrgId == org { remoteFailed(error) }
        }
    }

    /// 換組織:先清掉上一個組織的 Issue(還沒讀到前不會顯示別的組織的),之後由 reload() 在背景讀
    func clearForOrgChange() {
        if !app.issues.isEmpty { app.issues = [] }
        lastFetch = .distantPast
        startPolling()
    }

    /// 重新讀目前組織的 Issue(內容有變才更新畫面)
    func refresh() {
        do {
            let list = try remote.list(org: app.currentOrgId)
            lastFetch = .now
            remoteError = nil
            if list != app.issues { app.issues = list }
        } catch { remoteFailed(error) }
    }

    /// 超過 maxAge 秒沒讀就重新讀(0 = 一定重新讀)
    func refreshIfStale(_ maxAge: TimeInterval) {
        guard maxAge <= 0 || Date.now.timeIntervalSince(lastFetch) > maxAge else { return }
        refresh()
    }

    // MARK: 讀寫

    func load() {
        refresh()
        startPolling()
    }

    /// 找一筆(先重新讀,MCP 拿到的是最新的)
    func issue(_ id: String) -> OpsIssue? {
        refreshIfStale(0)
        return app.issues.first { $0.id.caseInsensitiveCompare(id) == .orderedSame }
    }

    @discardableResult
    func create(title: String, body: String = "", priority: IssuePriority = .normal, labels: [String] = [],
                source: String = "", link: String = "", repo: String? = nil, assignee: String? = nil, by author: String) -> OpsIssue {
        let assignee = assignee.flatMap { $0.isEmpty ? nil : $0 }
        let repo = repo.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.flatMap { $0.isEmpty ? nil : $0 }
        do {
            let issue = try remote.create(org: app.currentOrgId, title: title, body: body, priority: priority,
                                          labels: Self.clean(labels), source: source, link: link, repo: repo, author: author, assignee: assignee)
            remoteError = nil
            app.issues.removeAll { $0.id == issue.id }
            app.issues.insert(issue, at: 0)
            app.log(actor(author), "exclamationmark.bubble", "建立 \(issue.id)「\(title)」", .development)
            // 有指派:伺服器替角色排了任務,重新讀任務清單
            if assignee != nil { app.agentStore?.refresh() }
            return issue
        } catch {
            // 伺服器連不上:回傳沒有存起來的草稿(id 是空的)
            remoteFailed(error)
            return OpsIssue(id: "", title: title, body: body, priority: priority, labels: Self.clean(labels),
                            source: source, link: link, repo: repo, createdBy: author, assignee: assignee)
        }
    }

    /// 修改一筆;回傳修改後的內容。狀態從 open 變 closed(或反過來)時記到動態牆。
    /// 留言不會隨修改送出(在 change 裡改 comments 沒有作用),新增留言用 comment(_:_:by:)
    @discardableResult
    func update(_ id: String, by author: String, _ change: (inout OpsIssue) -> Void) -> OpsIssue? {
        refreshIfStale(0)   // 用伺服器上最新的內容來改
        guard let i = app.issues.firstIndex(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) else { return nil }
        let before = app.issues[i]
        var n = before
        change(&n)
        n.labels = Self.clean(n.labels)
        if n.assignee?.isEmpty == true { n.assignee = nil }
        n.repo = n.repo.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.flatMap { $0.isEmpty ? nil : $0 }
        let assigneeChanged = n.assignedAgent != before.assignedAgent
        let statusChanged = n.status != before.status
        if statusChanged { n.closedAt = n.status == .closed ? .now : nil }
        guard n != before else { return n }
        do {
            let saved = try remote.update(org: app.currentOrgId, n, author: author, sendAssignee: assigneeChanged)
            remoteError = nil
            if let k = app.issues.firstIndex(where: { $0.id == saved.id }) { app.issues[k] = saved }
            if statusChanged {
                app.log(actor(author), n.status == .closed ? "checkmark.circle" : "arrow.uturn.backward.circle",
                        "\(n.status == .closed ? "關閉" : "重新打開") \(n.id)「\(n.title)」", .development)
            }
            // 指派變了:伺服器取消舊角色未領取的任務、替新角色排任務,重新讀任務清單
            if assigneeChanged { app.agentStore?.refresh() }
            return saved
        } catch {
            remoteFailed(error)
            return nil
        }
    }

    /// 留言:只送這一則給伺服器附加(不整份送回,不會蓋掉別人同時留的言);回傳新增後的內容,失敗回 nil
    func comment(_ id: String, _ text: String, by author: String) -> OpsIssue? {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return issue(id) }
        do {
            let saved = try remote.addComment(org: app.currentOrgId, id: id, text: t, author: author)
            remoteError = nil
            if let k = app.issues.firstIndex(where: { $0.id == saved.id }) { app.issues[k] = saved } else { app.issues.insert(saved, at: 0) }
            return saved
        } catch {
            remoteFailed(error)
            return nil
        }
    }

    func delete(_ id: String) {
        do {
            try remote.delete(org: app.currentOrgId, id: id)
            remoteError = nil
        } catch {
            remoteFailed(error)
            return
        }
        app.issues.removeAll { $0.id == id }
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
