import Foundation

// 遠端模式(OmixorMode.swift)時虛擬角色與任務不讀寫本機的 agents.json,改透過 omixor server(core)的
// /api/orgs/<組織>/agents 與 /api/orgs/<組織>/agent-tasks 讀寫(路由說明見 core/api/api.go 檔頭)。
// 網址、token 與重新登入都和 Issue 共用 IssueRemote(Issues/IssueRemote.swift)。
//
// 回應包在 {"result": …};日期是 RFC 3339(秒),伺服器的任務沒有 routineId、留言 id 不一定是 UUID(AgentTask 的解碼是寬鬆的)。

/// GET /api/version 的回應(不用登入,也不包 result)
struct ServerVersion: Decodable, Equatable {
    var version: String
    var commit: String
    var buildTime: String

    /// 一行文字,例如「1.2.0(abc1234,2026-11-12 09:08)」;version 就是 commit 時只顯示短 commit
    var label: String {
        let short = String(commit.prefix(7))
        var head = version == commit ? short : version
        if head.isEmpty { head = "dev" }
        var extra: [String] = []
        if version != commit, !short.isEmpty, short != "dev" { extra.append(short) }
        if let t = ISO8601DateFormatter().date(from: buildTime) {
            extra.append(t.formatted(date: .numeric, time: .shortened))
        } else if !buildTime.isEmpty, buildTime != "dev" {
            extra.append(buildTime)
        }
        return extra.isEmpty ? head : "\(head)(\(extra.joined(separator: ",")))"
    }
}

extension IssueRemote {
    private func decode<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的格式看不懂:\(error.localizedDescription)")
        }
    }

    // MARK: 角色

    func listAgents(org: String) throws -> [Agent] {
        try decode([Agent].self, try send("GET", orgURL(org, ["agents"]), nil))
    }

    /// 新增或修改(組織管理員);沒給的欄位伺服器用預設值
    func saveAgent(org: String, _ a: Agent) throws -> Agent {
        try decode(Agent.self, try send("POST", orgURL(org, ["agents"]), try Self.encoder().encode(a)))
    }

    /// 刪除(未結束的任務一併取消、憑證作廢)
    func deleteAgent(org: String, id: String) throws {
        _ = try send("POST", orgURL(org, ["agents", id, "delete"]), try json([:]))
    }

    /// 立刻排一筆例行任務
    func runRoutine(org: String, agent: String, routine: String, author: String) throws -> AgentTask {
        try decode(AgentTask.self, try send("POST", orgURL(org, ["agents", agent, "routines", routine, "run"]),
                                            try json(["author": author])))
    }

    // MARK: 任務

    /// 全部任務(含已完成、取消)
    func listTasks(org: String) throws -> [AgentTask] {
        var c = URLComponents(url: orgURL(org, ["agent-tasks"]), resolvingAgainstBaseURL: false)!
        c.queryItems = [URLQueryItem(name: "status", value: "all")]
        return try decode([AgentTask].self, try send("GET", c.url!, nil))
    }

    /// 派任務(組織管理員)
    func assignTask(org: String, agent: String, title: String, instructions: String, priority: IssuePriority,
                    links: [String], author: String) throws -> AgentTask {
        let b = try json(["agentId": agent, "title": title, "instructions": instructions, "priority": priority.rawValue,
                          "links": links, "author": author])
        return try decode(AgentTask.self, try send("POST", orgURL(org, ["agent-tasks"]), b))
    }

    /// 領取:給 task 就領那筆,否則領該角色最優先的待領取任務。回傳角色憑證(secret 只有這一次,不要存檔)
    func claimTask(org: String, agent: String, task: String?) throws -> AgentClaim {
        var body: [String: Any] = ["agentId": agent]
        let path: [String]
        if let task, !task.isEmpty { path = ["agent-tasks", task, "claim"]; body["taskId"] = task } else { path = ["agent-tasks", "claim"] }
        let data = try send("POST", orgURL(org, path), try json(body))
        let obj = (try JSONSerialization.jsonObject(with: data, options: .fragmentsAllowed)) as? [String: Any] ?? [:]
        func part<T: Decodable>(_ key: String, _ type: T.Type) throws -> T? {
            guard let v = obj[key], !(v is NSNull) else { return nil }
            return try decode(type, try JSONSerialization.data(withJSONObject: v))
        }
        var cred: AgentCredential?
        if let c = obj["credential"] as? [String: Any], let id = c["agentId"] as? String, let secret = c["secret"] as? String {
            cred = AgentCredential(agentId: id, taskId: c["taskId"] as? String ?? "", secret: secret,
                                   expiresAt: c["expiresAt"] as? String ?? "",
                                   endpoint: (c["endpoint"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? agentEndpoint)
        }
        return AgentClaim(agent: try part("agent", Agent.self), task: try part("task", AgentTask.self), credential: cred)
    }

    /// 角色端點:領到的憑證用 Bearer <agentId>:<secret> 連這裡
    var agentEndpoint: String { config.url.appendingPathComponent("mcp").appendingPathComponent("agent").absoluteString }

    /// 回報:status 只能是 inProgress、blocked、done
    func reportTask(org: String, id: String, status: AgentTaskStatus?, result: String?, note: String?, links: [String]?) throws -> AgentTask {
        var b: [String: Any] = [:]
        if let status { b["status"] = status.rawValue }
        if let result { b["result"] = result }
        if let note { b["note"] = note }
        if let links { b["links"] = links }
        return try decode(AgentTask.self, try send("POST", orgURL(org, ["agent-tasks", id, "report"]), try json(b)))
    }

    func cancelTask(org: String, id: String) throws -> AgentTask {
        try decode(AgentTask.self, try send("POST", orgURL(org, ["agent-tasks", id, "cancel"]), try json([:])))
    }

    func requeueTask(org: String, id: String) throws -> AgentTask {
        try decode(AgentTask.self, try send("POST", orgURL(org, ["agent-tasks", id, "requeue"]), try json([:])))
    }

    // MARK: 版本

    /// GET /api/version(同步、不用登入;給 MCP 的 ops_server 用)
    func version() throws -> ServerVersion {
        var req = URLRequest(url: config.url.appendingPathComponent("api").appendingPathComponent("version"), timeoutInterval: timeout)
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        let (status, data) = try perform(req)
        guard status == 200 else { throw IssueRemoteError(message: "查不到版本(HTTP \(status))") }
        return try decode(ServerVersion.self, data)
    }

    /// GET /api/version(不用登入)
    static func fetchVersion(_ base: URL, timeout: TimeInterval = 5) async throws -> ServerVersion {
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = timeout
        if !protocolClasses.isEmpty { c.protocolClasses = protocolClasses }
        let session = URLSession(configuration: c)
        var req = URLRequest(url: base.appendingPathComponent("api").appendingPathComponent("version"), timeoutInterval: timeout)
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        let (data, resp) = try await session.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else { throw IssueRemoteError(message: "查不到版本(HTTP \(status))") }
        return try JSONDecoder().decode(ServerVersion.self, from: data)
    }
}
