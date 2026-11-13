import Foundation

// omixor core REST 的小用戶端(只打 core 的 /api/…,見 core/api/api.go 檔頭的路由說明)。
// 回應格式:成功 {"result": …}(登入、配對與 /api/auth/me 例外,欄位在最上層),失敗 {"error": "…"} 加狀態碼。

struct APIError: LocalizedError, Equatable {
    var message: String
    /// 伺服器回的 HTTP 狀態(0 = 沒連上)
    var status: Int = 0
    var errorDescription: String? { message }
    /// 登入失效(token 被撤銷、過期):要回配對畫面
    var isUnauthorized: Bool { status == 401 }
}

struct APIClient: Sendable {
    let baseURL: URL
    var token: String?
    var session: URLSession = .shared

    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    init(baseURL: URL, token: String? = nil, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.token = token
        self.session = session
    }

    /// /api/orgs/<組織>/<rest…>
    func orgPath(_ org: String, _ rest: String...) -> String {
        let enc = { (s: String) in s.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? s }
        return "/api/orgs/" + enc(org) + rest.map { "/" + enc($0) }.joined()
    }

    func request(_ method: String, _ path: String, query: [String: String] = [:], body: Any? = nil) throws -> URLRequest {
        guard var c = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else {
            throw APIError(message: "伺服器網址不對:\(baseURL.absoluteString)")
        }
        let base = c.percentEncodedPath
        c.percentEncodedPath = (base.hasSuffix("/") ? String(base.dropLast()) : base) + path
        let q = query.filter { !$0.value.isEmpty }.sorted { $0.key < $1.key }
        if !q.isEmpty { c.queryItems = q.map { URLQueryItem(name: $0.key, value: $0.value) } }
        guard let url = c.url else { throw APIError(message: "網址不對:\(path)") }
        var req = URLRequest(url: url, timeoutInterval: 15)
        req.httpMethod = method
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if let body {
            req.httpBody = try JSONSerialization.data(withJSONObject: body)
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        return req
    }

    /// 送出請求,回傳 result(或沒有 result 時整份內容)的 JSON
    func raw(_ method: String, _ path: String, query: [String: String] = [:], body: Any? = nil) async throws -> Data {
        let req = try request(method, path, query: query, body: body)
        let data: Data
        let resp: URLResponse
        do {
            (data, resp) = try await session.data(for: req)
        } catch is CancellationError {
            throw CancellationError()
        } catch let e as URLError where e.code == .cancelled {
            throw CancellationError()
        } catch {
            throw APIError(message: "連不到 omixor server(\(baseURL.host ?? baseURL.absoluteString)):\(error.localizedDescription)")
        }
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        return try Self.unwrap(data, status: status)
    }

    /// 解開 {"result": …} / {"error": …}
    static func unwrap(_ data: Data, status: Int) throws -> Data {
        let obj = try? JSONSerialization.jsonObject(with: data, options: .fragmentsAllowed)
        let dict = obj as? [String: Any]
        if let e = dict?["error"] as? String { throw APIError(message: e, status: status) }
        guard (200..<300).contains(status) else {
            throw APIError(message: "omixor server 回應不對(HTTP \(status))", status: status)
        }
        if let dict, let r = dict["result"] {
            if r is NSNull { return Data("null".utf8) }
            return try JSONSerialization.data(withJSONObject: r, options: .fragmentsAllowed)
        }
        return data
    }

    func send<T: Decodable>(_ method: String, _ path: String, query: [String: String] = [:], body: Any? = nil, as: T.Type = T.self) async throws -> T {
        let data = try await raw(method, path, query: query, body: body)
        do {
            return try Self.decoder.decode(T.self, from: data)
        } catch {
            throw APIError(message: "看不懂 omixor server 的回應:\(error.localizedDescription)")
        }
    }

    func get<T: Decodable>(_ path: String, query: [String: String] = [:]) async throws -> T {
        try await send("GET", path, query: query)
    }

    func post<T: Decodable>(_ path: String, _ body: [String: Any] = [:]) async throws -> T {
        try await send("POST", path, body: body)
    }

    // MARK: 授權

    /// 用 Mac App 顯示的配對碼換一把自己的 token(不用登入)
    func claimPair(code: String, device: String) async throws -> LoginResult {
        try await post("/api/auth/pair/claim", ["code": code, "device": device])
    }

    func me() async throws -> MeResponse { try await get("/api/auth/me") }

    func logout() async throws { _ = try await raw("POST", "/api/auth/logout", body: [String: Any]()) }

    // MARK: 組織

    func orgs() async throws -> [Org] { try await get("/api/orgs") }
    func members(org: String) async throws -> [Member] { try await get(orgPath(org, "members")) }
    func agents(org: String) async throws -> [Agent] { try await get(orgPath(org, "agents")) }

    // MARK: 頻道

    func channels(org: String) async throws -> [ChannelSummary] { try await get(orgPath(org, "channels")) }

    func messages(org: String, channel: String, since: String? = nil, before: String? = nil, limit: Int = 50) async throws -> MessagePage {
        try await get(orgPath(org, "channels", channel, "messages"),
                      query: ["since": since ?? "", "before": before ?? "", "limit": String(limit)])
    }

    func postMessage(org: String, channel: String, text: String) async throws -> ChannelMessage {
        try await post(orgPath(org, "channels", channel, "messages"), ["text": text])
    }

    func markRead(org: String, channel: String, upTo: String? = nil) async throws {
        var b: [String: Any] = [:]
        if let upTo { b["upTo"] = upTo }
        _ = try await raw("POST", orgPath(org, "channels", channel, "read"), body: b)
    }

    // MARK: Issue

    func issues(org: String) async throws -> [Issue] { try await get(orgPath(org, "issues")) }
    func issue(org: String, id: String) async throws -> Issue { try await get(orgPath(org, "issues", id)) }

    /// 留言:伺服器的修改是整份取代留言,所以先拿最新的再加一則(新留言的作者伺服器會設成自己)
    func addComment(org: String, issue id: String, text: String, author: String) async throws -> Issue {
        let cur = try await raw("GET", orgPath(org, "issues", id))
        let obj = try JSONSerialization.jsonObject(with: cur) as? [String: Any] ?? [:]
        var comments = obj["comments"] as? [[String: Any]] ?? []
        comments.append(["text": text, "author": author])
        return try await post(orgPath(org, "issues", id), ["comments": comments, "author": author])
    }

    // MARK: 派發

    /// status:open、all 或單一狀態
    func tasks(org: String, status: String = "all", agent: String = "") async throws -> [AgentTask] {
        try await get(orgPath(org, "agent-tasks"), query: ["status": status, "agent": agent])
    }

    func requeue(org: String, task: String) async throws -> AgentTask { try await post(orgPath(org, "agent-tasks", task, "requeue")) }
    func cancel(org: String, task: String) async throws -> AgentTask { try await post(orgPath(org, "agent-tasks", task, "cancel")) }
}
