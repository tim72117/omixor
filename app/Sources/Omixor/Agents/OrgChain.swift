import Foundation

// 組織鏈(資訊流動方向):CEO 給研發長「目標」,研發長給產品經理、設計師、工程師「任務」,做完「回報」給研發長。
// 一條邊 = From 可以派工作給 To(label 例如 目標、任務;空白 = 任務);To 做完回報給 From。
// 節點是 "agent:<角色 id>" 或 "user:<email 小寫>"。
//
// 本機模式存在組織資料夾的 org-chain.json({edges, updatedAt},格式同核心);
// 遠端模式改讀寫 omixor server(core)的 /api/orgs/<組織>/chain,規則由伺服器強制(見 core/README.md)。
// 本機模式的規則(最小可用,行為照核心):
//   - 驗證:節點存在(角色存在;user 是 app 的人員 id、目前使用者或 email)、不能指向自己、From+To 不重複、不能有循環
//   - 派工:本機只有一個使用者(擁有者,等同組織管理員),派工照舊不受鏈限制
//   - 角色委派(agent_task_delegate):鏈上要有 agent:<自己> → agent:<目標>
//   - 回報:任務變成 done 或 blocked、建立者 → 任務角色 有邊時,建立者是角色就替它排「檢視回報」任務,是人就在原任務留備註

struct ChainEdge: Codable, Hashable, Identifiable {
    var from: String
    var to: String
    var label = ""

    var id: String { "\(from)→\(to)" }
    /// 顯示用的 label(空白 = 任務)
    var labelText: String { label.isEmpty ? "任務" : label }
}

extension ChainEdge {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        from = try c.decodeIfPresent(String.self, forKey: .from) ?? ""
        to = try c.decodeIfPresent(String.self, forKey: .to) ?? ""
        label = try c.decodeIfPresent(String.self, forKey: .label) ?? ""
    }
}

struct OrgChain: Codable, Equatable {
    var edges: [ChainEdge] = []
    var updatedAt: Date?

    init(edges: [ChainEdge] = [], updatedAt: Date? = nil) {
        self.edges = edges
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        edges = try c.decodeIfPresent([ChainEdge].self, forKey: .edges) ?? []
        updatedAt = try? c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }

    /// From → To 的邊
    func edge(from: String, to: String) -> ChainEdge? { edges.first { $0.from == from && $0.to == to } }
    /// 上游:可以派工作給 node 的(node 做完回報給它們)
    func upstream(of node: String) -> [ChainEdge] { edges.filter { $0.to == node } }
    /// 下游:node 可以派工作給的
    func downstream(of node: String) -> [ChainEdge] { edges.filter { $0.from == node } }

    /// 全部節點(依第一次出現的順序)
    var nodes: [String] {
        var seen = Set<String>(), out: [String] = []
        for e in edges { for n in [e.from, e.to] where seen.insert(n).inserted { out.append(n) } }
        return out
    }

    /// 分層(由上而下):每個節點的層數 = 從根(沒有上游)出發的最長路徑長度
    func layers() -> [[String]] {
        var depth: [String: Int] = [:]
        func d(_ n: String, _ guardSet: Set<String>) -> Int {
            if let v = depth[n] { return v }
            if guardSet.contains(n) { return 0 }   // 有循環(不應該發生):不再往上找
            let ups = upstream(of: n).map(\.from)
            let v = ups.isEmpty ? 0 : (ups.map { d($0, guardSet.union([n])) }.max() ?? 0) + 1
            depth[n] = v
            return v
        }
        let ns = nodes
        for n in ns { _ = d(n, []) }
        let maxD = depth.values.max() ?? -1
        guard maxD >= 0 else { return [] }
        return (0...maxD).map { lv in ns.filter { depth[$0] == lv } }
    }
}

struct ChainError: LocalizedError {
    var message: String
    var errorDescription: String? { message }
}

enum ChainNode {
    static let agentPrefix = "agent:"
    static let userPrefix = "user:"
    /// 組織鏈自動排的「檢視回報」任務的建立者(不會再觸發回報)
    static let author = "chain"

    static func agent(_ id: String) -> String { agentPrefix + id }
    static func user(_ email: String) -> String { userPrefix + email.lowercased() }

    /// 任務建立者(createdBy)對應的節點:agent:<id> 或 user:<email / 人員 id>;例行排程、MCP、組織鏈自己排的不是節點
    static func of(author: String) -> String? {
        let a = author.trimmingCharacters(in: .whitespaces)
        if a.isEmpty || a == "routine" || a == "mcp" || a == Self.author { return nil }
        if a.hasPrefix(agentPrefix) { return a }
        if a.hasPrefix(userPrefix) { return user(String(a.dropFirst(userPrefix.count))) }
        return user(a)
    }

    static func isAgent(_ node: String) -> Bool { node.hasPrefix(agentPrefix) }
    static func agentId(_ node: String) -> String? { isAgent(node) ? String(node.dropFirst(agentPrefix.count)) : nil }
    static func userId(_ node: String) -> String? { node.hasPrefix(userPrefix) ? String(node.dropFirst(userPrefix.count)) : nil }

    /// 任務說明前綴:「【<label 或 任務>】」
    static func instructions(label: String, _ text: String) -> String {
        let l = label.trimmingCharacters(in: .whitespaces)
        return "【\(l.isEmpty ? "任務" : l)】\n\n\(text)"
    }
}

// MARK: - 驗證(本機模式;遠端由伺服器驗證)

extension OrgChain {
    /// 驗證並正規化:節點換成 agent:<角色 id> / user:<小寫>;沒有前綴的有 @ 視為 user,否則視為角色 id 或名稱。
    /// agents 是組織的角色;userOK 判斷 user 節點是不是組織的人
    static func validated(_ input: [ChainEdge], agents: [Agent], userOK: (String) -> Bool) throws -> [ChainEdge] {
        func norm(_ raw: String) throws -> String {
            let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !s.isEmpty else { throw ChainError(message: "邊的 from、to 不能是空的") }
            var kind = "", rest = s
            if s.hasPrefix(ChainNode.agentPrefix) { kind = "agent"; rest = String(s.dropFirst(ChainNode.agentPrefix.count)) }
            else if s.hasPrefix(ChainNode.userPrefix) { kind = "user"; rest = String(s.dropFirst(ChainNode.userPrefix.count)) }
            else { kind = s.contains("@") ? "user" : "agent" }
            rest = rest.trimmingCharacters(in: .whitespaces)
            if kind == "agent" {
                guard let g = agents.first(where: { $0.id.caseInsensitiveCompare(rest) == .orderedSame }) ?? agents.first(where: { $0.name == rest }) else {
                    throw ChainError(message: "找不到角色 \(rest)")
                }
                return ChainNode.agent(g.id)
            }
            let u = rest.lowercased()
            guard !u.isEmpty, userOK(u) else { throw ChainError(message: "\(s) 不是組織的成員") }
            return ChainNode.user(u)
        }
        var out: [ChainEdge] = []
        for e in input {
            let f = try norm(e.from), t = try norm(e.to)
            if f == t { throw ChainError(message: "邊不能指向自己:\(f)") }
            if out.contains(where: { $0.from == f && $0.to == t }) { throw ChainError(message: "重複的邊:\(f) → \(t)") }
            let label = e.label.trimmingCharacters(in: .whitespacesAndNewlines)
            if label.count > 40 { throw ChainError(message: "label 最多 40 個字:\(label)") }
            out.append(ChainEdge(from: f, to: t, label: label))
        }
        if let cycle = Self.findCycle(out) { throw ChainError(message: "組織鏈不能有循環:\(cycle.joined(separator: " → "))") }
        return out
    }

    /// 找一個循環(回傳經過的節點,頭尾相同);沒有回 nil
    static func findCycle(_ edges: [ChainEdge]) -> [String]? {
        var next: [String: [String]] = [:]
        var order: [String] = []
        for e in edges {
            if next[e.from] == nil { order.append(e.from) }
            next[e.from, default: []].append(e.to)
        }
        var state: [String: Int] = [:]   // 1 = 走訪中、2 = 走完
        var stack: [String] = []
        func dfs(_ n: String) -> [String]? {
            state[n] = 1
            stack.append(n)
            for m in next[n] ?? [] {
                if state[m] == 1, let i = stack.firstIndex(of: m) { return Array(stack[i...]) + [m] }
                if state[m] == nil, let c = dfs(m) { return c }
            }
            stack.removeLast()
            state[n] = 2
            return nil
        }
        for n in order where state[n] == nil { if let c = dfs(n) { return c } }
        return nil
    }
}

// MARK: - 遠端(omixor server)

/// GET /api/orgs/<組織>/members 的一筆(只取畫面要用的)
struct RemoteMember: Decodable, Hashable {
    var email: String
    var name: String?
    var role: String?
}

extension IssueRemote {
    private func decodeChain<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的組織鏈格式看不懂:\(error.localizedDescription)")
        }
    }

    /// 組織鏈(成員)
    func getChain(org: String) throws -> OrgChain {
        try decodeChain(OrgChain.self, try send("GET", orgURL(org, ["chain"]), nil))
    }

    /// 整份取代(組織管理員;伺服器驗證節點、自環、重複與循環,錯誤原文丟出)
    func saveChain(org: String, edges: [ChainEdge]) throws -> OrgChain {
        let list = edges.map { ["from": $0.from, "to": $0.to, "label": $0.label] }
        return try decodeChain(OrgChain.self, try send("POST", orgURL(org, ["chain"]), try json(["edges": list])))
    }

    /// 角色委派:POST /agent-tasks/delegate(角色憑證;用 omixor token 時只有伺服器管理員能帶 from 代角色委派)
    func delegateTask(org: String, from: String?, to: String, title: String, instructions: String,
                      priority: IssuePriority, taskId: String?) throws -> AgentTask {
        var b: [String: Any] = ["to": to, "title": title, "instructions": instructions, "priority": priority.rawValue]
        if let from, !from.isEmpty { b["from"] = from }
        if let taskId, !taskId.isEmpty { b["taskId"] = taskId }
        return try decodeChain(AgentTask.self, try send("POST", orgURL(org, ["agent-tasks", "delegate"]), try json(b)))
    }

    /// 回報給某個節點的任務(reportTo = user:<email> 或 me)
    func reportedTasks(org: String, reportTo: String) throws -> [AgentTask] {
        var c = URLComponents(url: orgURL(org, ["agent-tasks"]), resolvingAgainstBaseURL: false)!
        c.queryItems = [URLQueryItem(name: "reportTo", value: reportTo), URLQueryItem(name: "status", value: "all")]
        return try decodeChain([AgentTask].self, try send("GET", c.url!, nil))
    }

    /// 組織成員(組織鏈選人用)
    func listMembers(org: String) throws -> [RemoteMember] {
        try decodeChain([RemoteMember].self, try send("GET", orgURL(org, ["members"]), nil))
    }

    /// 我在這個組織的角色(GET /api/orgs 的 myRole;admin 才能改組織鏈)
    func myRole(org: String) throws -> String? {
        let url = config.url.appendingPathComponent("api").appendingPathComponent("orgs")
        let list = try decodeChain([ServerOrg].self, try send("GET", url, nil))
        return list.first { $0.id == OmixorMode.serverOrgId(org) }?.myRole
    }
}

// MARK: - AgentStore:讀寫、委派、回報

extension AgentStore {
    /// 讀目前組織的組織鏈(core server)。讀不到不影響角色與任務
    func loadChain() {
        do {
            let c = try remote.getChain(org: app.currentOrgId)
            app.orgChainError = nil
            if c != app.orgChain { app.orgChain = c }
        } catch {
            app.orgChainError = error.localizedDescription
        }
    }

    /// 整份取代組織鏈(要組織管理員;伺服器驗證節點、自環、重複與循環,錯誤原文丟出)
    @discardableResult
    func saveChain(_ edges: [ChainEdge]) throws -> OrgChain {
        let c = try remote.saveChain(org: app.currentOrgId, edges: edges)
        app.orgChain = c
        app.orgChainError = nil
        return c
    }

#if LEGACY_LOCAL_STORE
    // ===== 已停用:原本的本機實作(org-chain.json、App 自己驗證與回報);留著備查,不編譯 =====
    private var chainURL: URL { app.dataDir.appendingPathComponent("org-chain.json") }

    /// 讀目前組織的組織鏈(本機:org-chain.json;遠端:伺服器)。讀不到不影響角色與任務
    func loadChain() {
        if let r = remote {
            do {
                let c = try r.getChain(org: app.currentOrgId)
                app.orgChainError = nil
                if c != app.orgChain { app.orgChain = c }
            } catch {
                app.orgChainError = error.localizedDescription
            }
            return
        }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        if let data = try? Data(contentsOf: chainURL), let c = try? d.decode(OrgChain.self, from: data) {
            app.orgChain = c
        } else {
            app.orgChain = OrgChain()
        }
        app.orgChainError = nil
    }

    private func writeChain() {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? e.encode(app.orgChain) { try? data.write(to: chainURL, options: .atomic) }
    }

    /// 本機的 user 節點:app 的人員 id、目前使用者,或看起來像 email 的
    func chainUserOK(_ u: String) -> Bool {
        u.contains("@") || u == app.me.lowercased() || app.people.contains { $0.id.lowercased() == u }
    }

    /// 整份取代組織鏈(遠端要組織管理員);驗證失敗丟 ChainError / 伺服器錯誤原文
    @discardableResult
    func saveChain(_ edges: [ChainEdge]) throws -> OrgChain {
        if let r = remote {
            let c = try r.saveChain(org: app.currentOrgId, edges: edges)
            app.orgChain = c
            app.orgChainError = nil
            return c
        }
        let clean = try OrgChain.validated(edges, agents: app.agents, userOK: chainUserOK)
        app.orgChain = OrgChain(edges: clean, updatedAt: Date(timeIntervalSince1970: Date.now.timeIntervalSince1970.rounded(.down)))
        writeChain()
        app.log(.person(app.me), "arrow.triangle.branch", "更新組織鏈(\(clean.count) 條)", .automation)
        return app.orgChain
    }

    /// 本機:刪角色時一併拿掉相關的邊
    func pruneChain(agent id: String) {
        guard !isRemote else { return }
        let node = ChainNode.agent(id)
        let kept = app.orgChain.edges.filter { $0.from != node && $0.to != node }
        guard kept.count != app.orgChain.edges.count else { return }
        app.orgChain.edges = kept
        writeChain()
    }

#endif

    /// 節點的顯示名稱:角色名稱或人名 / email
    func chainName(_ node: String) -> String {
        if let id = ChainNode.agentId(node) { return agent(id)?.name ?? id }
        if let u = ChainNode.userId(node) { return app.people.first { $0.id.lowercased() == u }?.name ?? u }
        return node
    }

    /// 「X 可以派給:A(目標)、B(任務)」
    func downstreamText(_ node: String) -> String {
        let ds = app.orgChain.downstream(of: node)
        if ds.isEmpty { return "組織鏈上沒有 \(chainName(node)) 可以派工作的對象" }
        return "\(chainName(node)) 可以派給:" + ds.map { "\(chainName($0.to))(\($0.labelText))" }.joined(separator: "、")
    }

    /// 角色委派:鏈上要有 agent:<from> → agent:<to>。任務署名 agent:<from>,links = [來源任務] + 來源任務的 links,
    /// 說明前綴「【label】」。sourceTask 沒給時用 from 角色進行中、最近更新的任務(本機沒有憑證綁定)。
    func delegate(from fromId: String, to toId: String, title: String, instructions: String,
                  priority: IssuePriority = .normal, sourceTask: String? = nil) throws -> AgentTask {
        guard let g = agent(fromId) else { throw ChainError(message: "找不到角色 \(fromId)") }
        guard let target = agent(toId) else { throw ChainError(message: "找不到角色 \(toId)") }
        let t = title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { throw ChainError(message: "要給任務標題") }
        if g.id == target.id { throw ChainError(message: "不能把任務委派給自己(\(g.name))") }
        do {
            let task = try remote.delegateTask(org: app.currentOrgId, from: g.id, to: target.id, title: t, instructions: instructions,
                                               priority: priority, taskId: sourceTask)
            merge(task)
            app.log(.ai(g.id), "arrow.triangle.branch", "\(g.name)委派 \(task.id)「\(task.title)」給\(target.name)", .automation)
            return task
        } catch { throw ChainError(message: error.localizedDescription) }
    }

#if LEGACY_LOCAL_STORE
    // ===== 已停用:本機委派(原本 delegate 的後半段)與回報 =====
    private func legacyDelegateBody() throws -> AgentTask {
        let fromNode = ChainNode.agent(g.id)
        guard let edge = app.orgChain.edge(from: fromNode, to: ChainNode.agent(target.id)) else {
            throw ChainError(message: "組織鏈上 \(g.name) 不能派工作給 \(target.name);\(downstreamText(fromNode))")
        }
        var source: AgentTask?
        if let sid = sourceTask, !sid.isEmpty {
            guard let s = task(sid) else { throw ChainError(message: "找不到任務 \(sid)") }
            guard s.agentId == g.id else { throw ChainError(message: "\(s.id) 不是 \(g.name) 的任務,不能當委派的來源") }
            source = s
        } else {
            source = tasks(for: g.id).filter { $0.status == .inProgress }.max { $0.updatedAt < $1.updatedAt }
        }
        let links = source.map { IssueStore.clean([$0.id] + $0.links) } ?? []
        guard let created = assign(to: target.id, title: t, instructions: ChainNode.instructions(label: edge.label, instructions),
                                   priority: priority, links: links, by: fromNode) else {
            throw ChainError(message: "任務沒有派出去")
        }
        return created
    }

    /// 回報(本機):任務從別的狀態變成 done 或 blocked,建立者是鏈上節點、而且有 建立者 → 任務角色 的邊。
    /// 建立者是角色:替它排「檢視回報:<原標題>」(CreatedBy = chain,不會再觸發回報;已有未結束的就不重複排);
    /// 建立者是人:不建任務。兩種都在原任務留一則 chain 署名的備註「已回報給 …」。
    func chainReport(before: AgentTask, after: AgentTask, note: String?) {
        guard !isRemote, after.status != before.status, after.status == .done || after.status == .blocked,
              let creator = ChainNode.of(author: after.createdBy),
              app.orgChain.edge(from: creator, to: ChainNode.agent(after.agentId)) != nil else { return }
        if let cid = ChainNode.agentId(creator) {
            guard let g = agent(cid) else { return }
            let reviewTitle = "檢視回報:\(after.title)"
            if let open = app.agentTasks.first(where: { $0.agentId == g.id && $0.createdBy == ChainNode.author && $0.status.isOpen
                && $0.links.first?.caseInsensitiveCompare(after.id) == .orderedSame }) {
                _ = self.note(after.id, "已回報給 \(g.name)(\(open.id))", by: ChainNode.author)
                return
            }
            let who = agent(after.agentId)?.name ?? after.agentId
            var body = "\(who) 回報了 \(after.id)「\(after.title)」:\(after.status.label)。\n\n"
            if after.status == .done {
                body += "結果:\n\(after.result.isEmpty ? "(沒有寫結果)" : after.result)"
            } else {
                let n = note?.trimmingCharacters(in: .whitespacesAndNewlines)
                let text = (n?.isEmpty == false ? n : nil) ?? after.notes.last?.text ?? "(沒有說明)"
                body += "需要協助:\n\(text)"
            }
            body += "\n\n原任務:\(after.id)。用 agent_tasks(id: \(after.id))看完整內容與備註;需要追加工作可以再委派。"
            if let t = assign(to: g.id, title: reviewTitle, instructions: body, priority: after.priority,
                              links: IssueStore.clean([after.id] + after.links), by: ChainNode.author) {
                _ = self.note(after.id, "已回報給 \(g.name)(\(t.id))", by: ChainNode.author)
            }
        } else if let u = ChainNode.userId(creator) {
            _ = self.note(after.id, "已回報給 \(u)", by: ChainNode.author)
        }
    }

#endif

    /// 回報給某個人的任務:建立者是他、有 chain 署名的「已回報給」備註(新的在前)
    func tasksReported(to node: String) -> [AgentTask] {
        app.agentTasks.filter { t in
            ChainNode.of(author: t.createdBy) == node
                && t.notes.contains { $0.author == ChainNode.author && $0.text.hasPrefix("已回報給") }
        }
    }
}
