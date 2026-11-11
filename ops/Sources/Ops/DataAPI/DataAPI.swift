import Foundation
import Network

// 唯讀資料 API:讓別台電腦(透過 `tailscale serve`)用 HTTP 讀 Ops 目前的資料。
// - 只綁 127.0.0.1(預設 3768),預設關閉;設定存資料夾根目錄的 data-api.json(全部組織共用),token 存鑰匙圈
// - 路由一律對應到 MCP 裡 readOnlyHint = true 的工具,透過 MCPHandler 執行;其他工具一律 403。不新增工具、不改 MCP 規格
// - 驗證擇一:`Tailscale-User-Login` 在白名單,或 `Authorization: Bearer <token>`;否則 401,不洩漏任何資料

struct DataAPISettings: Codable, Equatable {
    static let defaultPort = 3768

    var enabled = false
    var port = DataAPISettings.defaultPort
    /// 允許的 Tailscale 登入(例如 tim@example.com 或 tim72117@github)
    var allowedLogins: [String] = []

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = (try? c.decode(Bool.self, forKey: .enabled)) ?? false
        let p = (try? c.decode(Int.self, forKey: .port)) ?? Self.defaultPort
        port = (1..<65536).contains(p) ? p : Self.defaultPort
        allowedLogins = ((try? c.decode([String].self, forKey: .allowedLogins)) ?? [])
            .map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    func allows(_ login: String?) -> Bool {
        guard let l = login?.trimmingCharacters(in: .whitespaces).lowercased(), !l.isEmpty else { return false }
        return allowedLogins.contains { $0.lowercased() == l }
    }
}

/// 設定頁顯示的狀態(不存檔)
struct DataAPIStatus: Equatable {
    struct Rejected: Identifiable, Equatable {
        var id: String { login }
        let login: String
        var count: Int
        var lastAt: Date
        var lastPath: String
    }

    var server: LineStatus.ServerState = .stopped
    var hasToken = false
    var requests = 0
    var unauthorized = 0
    var lastRequestAt: Date?
    var lastCaller: String?
    var lastPath: String?
    var rejected: [Rejected] = []
}

enum DataAPI {
    static let prefix = "/api/v1"
    static let tokenAccount = "data-api:token"

    /// 便利路徑 → 工具(固定參數由路徑帶入)
    static let shortcuts: [(path: String, tool: String, description: String)] = [
        ("/overview", "ops_overview", "整體狀況"),
        ("/issues", "ops_issues", "Issue(?status=&label=&query=&limit=);/issues/<id> 看一筆"),
        ("/agents", "ops_agents", "虛擬角色"),
        ("/tasks", "ops_agent_tasks", "角色任務(?agent=&status=);/tasks/<id> 看一筆"),
        ("/docs", "ops_docs", "文件櫃文件(?section=&tab=&query=);/docs/<id> 看一筆"),
        ("/cabinet", "ops_cabinet", "文件櫃結構"),
        ("/orgs", "ops_orgs", "組織"),
        ("/list/<collection>", "ops_list", "列出資料集合(?search=&limit=&offset=&where={JSON})"),
        ("/get/<collection>/<id>", "ops_get", "依 id 取得一筆"),
        ("/github", "ops_github", "GitHub 概況"),
        ("/gcp", "ops_gcp", "GCP 概況"),
    ]

    // MARK: - token

    static func token() -> String? {
        TokenVault.data(tokenAccount).map { String(decoding: $0, as: UTF8.self) }.flatMap { $0.isEmpty ? nil : $0 }
    }

    static func setToken(_ t: String?) { TokenVault.set(t.map { Data($0.utf8) }, tokenAccount) }

    static func newToken() -> String { "ops_" + LineAPI.nonce() + LineAPI.nonce() }

    // MARK: - 唯讀工具

    @MainActor static var readOnlyTools: [MCPHandler.Tool] { MCPHandler.tools.filter(\.readOnly) }

    @MainActor static func tool(_ name: String) -> MCPHandler.Tool? { MCPHandler.tools.first { $0.name == name } }

    // MARK: - 路由

    enum Auth: Equatable { case tailscale(String), token }

    struct Route: Equatable {
        var tool: String
        var args: [String: String] = [:]
    }

    /// 路徑 → 工具;nil 表示找不到
    static func resolve(_ path: String) -> Route? {
        var p = path
        while p.count > 1, p.hasSuffix("/") { p.removeLast() }
        guard p.hasPrefix(prefix + "/") else { return nil }
        let parts = p.dropFirst(prefix.count + 1).split(separator: "/", omittingEmptySubsequences: false)
            .map { String($0).removingPercentEncoding ?? String($0) }
        switch (parts.first ?? "", parts.count) {
        case ("tools", 2): return Route(tool: parts[1])
        case ("overview", 1): return Route(tool: "ops_overview")
        case ("issues", 1): return Route(tool: "ops_issues")
        case ("issues", 2): return Route(tool: "ops_issues", args: ["id": parts[1]])
        case ("agents", 1): return Route(tool: "ops_agents")
        case ("agents", 2): return Route(tool: "ops_agents", args: ["id": parts[1]])
        case ("tasks", 1): return Route(tool: "ops_agent_tasks")
        case ("tasks", 2): return Route(tool: "ops_agent_tasks", args: ["id": parts[1]])
        case ("docs", 1): return Route(tool: "ops_docs")
        case ("docs", 2): return Route(tool: "ops_docs", args: ["id": parts[1]])
        case ("cabinet", 1): return Route(tool: "ops_cabinet")
        case ("orgs", 1): return Route(tool: "ops_orgs")
        case ("list", 2): return Route(tool: "ops_list", args: ["collection": parts[1]])
        case ("get", 3): return Route(tool: "ops_get", args: ["collection": parts[1], "id": parts[2]])
        case ("github", 1): return Route(tool: "ops_github")
        case ("gcp", 1): return Route(tool: "ops_gcp")
        default: return nil
        }
    }

    struct BadRequest: Error { let message: String }

    /// query 字串 → [名稱: [值]](+ 視為空白)
    static func queryItems(_ target: String?) -> [(String, String)] {
        guard let target, let q = target.split(separator: "?", maxSplits: 1).dropFirst().first else { return [] }
        return q.split(separator: "&").compactMap { pair in
            let kv = pair.split(separator: "=", maxSplits: 1).map {
                String($0).replacingOccurrences(of: "+", with: " ").removingPercentEncoding ?? String($0)
            }
            guard let k = kv.first, !k.isEmpty else { return nil }
            return (k, kv.count > 1 ? kv[1] : "")
        }
    }

    /// 依工具的 inputSchema 把字串參數轉成對的型別
    static func convert(_ items: [(String, String)], schema: [String: Any]) throws -> [String: Any] {
        var grouped: [String: [String]] = [:]
        var order: [String] = []
        for (k, v) in items {
            if grouped[k] == nil { order.append(k) }
            grouped[k, default: []].append(v)
        }
        var out: [String: Any] = [:]
        for k in order {
            let vals = grouped[k]!
            guard let prop = schema[k] as? [String: Any] else {
                let allowed = schema.keys.sorted().joined(separator: "、")
                throw BadRequest(message: "未知參數 \(k)" + (allowed.isEmpty ? "(這個工具沒有參數)" : "。可用:\(allowed)"))
            }
            let v = vals.last!
            switch prop["type"] as? String ?? "string" {
            case "integer":
                guard let n = Int(v.trimmingCharacters(in: .whitespaces)) else { throw BadRequest(message: "參數 \(k) 要是整數,收到「\(v)」") }
                out[k] = n
            case "number":
                guard let n = Double(v.trimmingCharacters(in: .whitespaces)) else { throw BadRequest(message: "參數 \(k) 要是數字,收到「\(v)」") }
                out[k] = n
            case "boolean":
                switch v.lowercased() {
                case "true", "1", "yes", "": out[k] = true
                case "false", "0", "no": out[k] = false
                default: throw BadRequest(message: "參數 \(k) 要是 true 或 false,收到「\(v)」")
                }
            case "array":
                if vals.count == 1, v.trimmingCharacters(in: .whitespaces).hasPrefix("[") {
                    guard let a = try? JSONSerialization.jsonObject(with: Data(v.utf8)) as? [Any] else {
                        throw BadRequest(message: "參數 \(k) 的 JSON 陣列格式錯誤")
                    }
                    out[k] = a
                } else {
                    out[k] = vals  // ?labels=a&labels=b
                }
            case "object":
                guard let o = try? JSONSerialization.jsonObject(with: Data(v.utf8)) as? [String: Any] else {
                    throw BadRequest(message: "參數 \(k) 要是 JSON 物件,例如 {\"state\":\"firing\"}")
                }
                out[k] = o
            default:
                out[k] = v
            }
        }
        return out
    }

    // MARK: - 回應

    static func json(_ status: Int, _ obj: Any) -> HTTPResponse {
        HTTPResponse(status: status, body: MCPJSON.data(obj), contentType: "application/json; charset=utf-8")
    }

    static func error(_ status: Int, _ message: String, extra: [String: String] = [:]) -> HTTPResponse {
        var r = json(status, ["error": message])
        r.extra = extra
        return r
    }

    @MainActor static func index(org: String?) -> [String: Any] {
        [
            "name": "Ops 唯讀資料 API",
            "version": "v1",
            "org": org as Any,
            "auth": "Tailscale-User-Login 在白名單,或 Authorization: Bearer <token>",
            "endpoints": [
                ["method": "GET", "path": prefix, "description": "這份說明"],
                ["method": "GET", "path": prefix + "/tools/<工具>?參數=值", "description": "呼叫唯讀工具;query 依 inputSchema 轉型,array / object 可傳 JSON 字串"],
                ["method": "POST", "path": prefix + "/tools/<工具>", "description": "呼叫唯讀工具;body 是 JSON 參數"],
            ] + shortcuts.map { ["method": "GET", "path": prefix + $0.path, "tool": $0.tool, "description": $0.description] },
            "tools": readOnlyTools.map { t in
                ["name": t.name, "title": t.title, "description": t.description,
                 "inputSchema": t.definition["inputSchema"] as Any]
            },
        ]
    }

    /// 處理一個請求。`call` 執行 MCP 工具,回傳 (輸出文字, 是否錯誤);`onAuth` 記錄誰通過 / 被拒
    @MainActor
    static func handle(_ req: HTTPRequest, settings: DataAPISettings, token: String?, org: String?,
                       call: (String, [String: Any]) async -> (String, Bool),
                       record: (Auth?, String?) -> Void) async -> HTTPResponse {
        // 瀏覽器帶來的非本機 Origin 一律擋(這是 API,不是網頁)
        if let origin = req.headers["origin"], !MCPServer.isLocalOrigin(origin) {
            return error(403, "Forbidden origin")
        }
        // 驗證:先 Tailscale 身分,再 Bearer token
        let login = req.headers["tailscale-user-login"]
        var auth: Auth?
        if settings.allows(login) {
            auth = .tailscale(login!)
        } else if let h = req.headers["authorization"], h.lowercased().hasPrefix("bearer "), let token, !token.isEmpty,
                  LineAPI.constantTimeEqual(Data(h.dropFirst(7).trimmingCharacters(in: .whitespaces).utf8), Data(token.utf8)) {
            auth = .token
        }
        record(auth, login)
        guard auth != nil else {
            return error(401, "Unauthorized", extra: ["WWW-Authenticate": "Bearer"])
        }

        var path = req.path
        while path.count > 1, path.hasSuffix("/") { path.removeLast() }
        if path == prefix {
            guard req.method == "GET" else { return error(405, "Method not allowed", extra: ["Allow": "GET"]) }
            return json(200, index(org: org))
        }
        guard let route = resolve(path) else { return error(404, "Not found。端點清單:GET \(prefix)") }
        let isToolPath = path.hasPrefix(prefix + "/tools/")
        let allowed = isToolPath ? ["GET", "POST"] : ["GET"]
        guard allowed.contains(req.method) else {
            return error(405, "Method not allowed", extra: ["Allow": allowed.joined(separator: ", ")])
        }
        guard let tool = tool(route.tool) else { return error(404, "沒有這個工具:\(route.tool)") }
        guard tool.readOnly else { return error(403, "\(tool.name) 不是唯讀工具,資料 API 不能呼叫") }

        var args: [String: Any]
        do {
            args = try convert(queryItems(req.rawTarget), schema: tool.properties)
        } catch let e as BadRequest {
            return error(400, e.message)
        } catch let e {
            return Self.error(400, "\(e)")
        }
        if req.method == "POST", !req.body.isEmpty {
            guard let body = try? JSONSerialization.jsonObject(with: req.body) as? [String: Any] else {
                return error(400, "body 要是 JSON 物件")
            }
            for (k, _) in body where tool.properties[k] == nil { return error(400, "未知參數 \(k)") }
            args.merge(body) { _, b in b }
        }
        args.merge(route.args) { _, path in path }

        let (text, isError) = await call(tool.name, args)
        if isError { return error(400, text) }
        if (try? JSONSerialization.jsonObject(with: Data(text.utf8), options: .fragmentsAllowed)) != nil {
            return HTTPResponse(status: 200, body: Data(text.utf8), contentType: "application/json; charset=utf-8")
        }
        return json(200, ["result": text])
    }
}

/// 只聽 127.0.0.1 的極簡 HTTP 伺服器(每個連線處理一個請求就關閉),重用 HTTPRequest / HTTPResponse
final class LoopbackHTTPServer: @unchecked Sendable {
    private let port: UInt16
    private let queue = DispatchQueue(label: "ops.data-api")
    private var listener: NWListener?
    private let handle: @Sendable (HTTPRequest) async -> HTTPResponse
    private let onState: @Sendable (LineStatus.ServerState) -> Void

    init(port: UInt16, handle: @escaping @Sendable (HTTPRequest) async -> HTTPResponse,
         onState: @escaping @Sendable (LineStatus.ServerState) -> Void) {
        self.port = port
        self.handle = handle
        self.onState = onState
    }

    func start() {
        onState(.starting)
        guard let nwPort = NWEndpoint.Port(rawValue: port) else { onState(.failed("無效的連接埠 \(port)")); return }
        do {
            let params = NWParameters.tcp
            params.allowLocalEndpointReuse = true
            params.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: nwPort)
            params.requiredInterfaceType = .loopback
            let l = try NWListener(using: params)
            l.stateUpdateHandler = { [weak self] st in
                switch st {
                case .ready: self?.onState(.listening)
                case .failed(let e):
                    if case .posix(let code) = e, code == .EADDRINUSE { self?.onState(.failed("連接埠已被占用")) }
                    else { self?.onState(.failed("\(e)")) }
                case .cancelled: self?.onState(.stopped)
                default: break
                }
            }
            l.newConnectionHandler = { [weak self] conn in
                guard let self else { conn.cancel(); return }
                conn.start(queue: self.queue)
                self.receive(conn, buffer: Data())
            }
            l.start(queue: queue)
            listener = l
        } catch {
            onState(.failed("\(error)"))
        }
    }

    func stop() {
        listener?.cancel()
        listener = nil
    }

    private func receive(_ conn: NWConnection, buffer: Data) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { [weak self] data, _, isComplete, error in
            guard let self else { conn.cancel(); return }
            var buf = buffer
            if let data { buf.append(data) }
            if error != nil { conn.cancel(); return }
            switch HTTPRequest.parse(buf, maxBody: 1 << 20) {
            case .incomplete:
                if isComplete { conn.cancel() } else { self.receive(conn, buffer: buf) }
            case .invalid(let status, let msg):
                self.send(conn, HTTPResponse(status: status, body: Data(msg.utf8), contentType: "text/plain; charset=utf-8"))
            case .complete(let req):
                Task { [handle] in
                    let resp = await handle(req)
                    self.queue.async { self.send(conn, resp) }
                }
            }
        }
    }

    private func send(_ conn: NWConnection, _ resp: HTTPResponse) {
        conn.send(content: resp.serialized(), completion: .contentProcessed { _ in conn.cancel() })
    }
}

/// 設定、伺服器生命週期與狀態
@MainActor
final class DataAPIStore {
    let app: AppState
    private lazy var mcp = MCPHandler(app: app)
    private var server: LoopbackHTTPServer?
    private var serverPort: Int?
    private var nextId = 0

    init(app: AppState) { self.app = app }

    private var fileURL: URL { OrgFiles.root.appendingPathComponent("data-api.json") }

    func load() {
        app.dataAPISettings = (try? Data(contentsOf: fileURL)).flatMap { try? JSONDecoder().decode(DataAPISettings.self, from: $0) }
            ?? DataAPISettings()
        app.dataAPI.hasToken = DataAPI.token() != nil
    }

    func save() {
        let e = JSONEncoder()
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let d = try? e.encode(app.dataAPISettings) { try? d.write(to: fileURL, options: .atomic) }
    }

    func update(_ change: (inout DataAPISettings) -> Void) {
        var s = app.dataAPISettings
        change(&s)
        guard s != app.dataAPISettings else { return }
        app.dataAPISettings = s
        save()
        apply()
    }

    /// 產生新 token(舊的立即失效),回傳讓畫面顯示一次
    func regenerateToken() -> String {
        let t = DataAPI.newToken()
        DataAPI.setToken(t)
        app.dataAPI.hasToken = true
        app.log(.person(app.me), "key", "產生資料 API 的 token", .settings)
        return t
    }

    func revokeToken() {
        DataAPI.setToken(nil)
        app.dataAPI.hasToken = false
    }

    func allow(_ login: String) {
        let l = login.trimmingCharacters(in: .whitespaces)
        guard !l.isEmpty else { return }
        update { s in if !s.allows(l) { s.allowedLogins.append(l) } }
        app.dataAPI.rejected.removeAll { $0.login.lowercased() == l.lowercased() }
    }

    /// 依設定開關伺服器(serve: false 時只更新設定,測試用)
    func apply(serve: Bool = true) {
        let s = app.dataAPISettings
        guard s.enabled, serve else {
            server?.stop(); server = nil; serverPort = nil
            app.dataAPI.server = .stopped
            return
        }
        if server != nil, serverPort == s.port { return }
        server?.stop()
        let srv = LoopbackHTTPServer(port: UInt16(s.port), handle: { [weak self] req in
            guard let self else { return DataAPI.error(503, "Unavailable") }
            return await self.handle(req)
        }, onState: { [weak self] st in Task { @MainActor in self?.app.dataAPI.server = st } })
        server = srv
        serverPort = s.port
        srv.start()
    }

    func handle(_ req: HTTPRequest) async -> HTTPResponse {
        app.dataAPI.requests += 1
        return await DataAPI.handle(req, settings: app.dataAPISettings, token: DataAPI.token(), org: app.currentOrg?.name,
                                    call: { name, args in await self.call(name, args) },
                                    record: { auth, login in self.record(auth, login, path: req.rawTarget ?? req.path) })
    }

    private func record(_ auth: DataAPI.Auth?, _ login: String?, path: String) {
        let shortPath = String(path.prefix(120))
        switch auth {
        case .tailscale(let l):
            app.dataAPI.lastCaller = l
        case .token:
            app.dataAPI.lastCaller = "Bearer token" + (login.map { "(\($0),不在白名單)" } ?? "")
        case nil:
            app.dataAPI.unauthorized += 1
            let who = login?.trimmingCharacters(in: .whitespaces).isEmpty == false ? login! : "(沒有 Tailscale 登入,token 不對或沒帶)"
            if let i = app.dataAPI.rejected.firstIndex(where: { $0.login == who }) {
                app.dataAPI.rejected[i].count += 1
                app.dataAPI.rejected[i].lastAt = .now
                app.dataAPI.rejected[i].lastPath = shortPath
            } else {
                app.dataAPI.rejected.insert(.init(login: who, count: 1, lastAt: .now, lastPath: shortPath), at: 0)
                if app.dataAPI.rejected.count > 20 { app.dataAPI.rejected.removeLast() }
            }
            return
        }
        app.dataAPI.lastRequestAt = .now
        app.dataAPI.lastPath = shortPath
    }

    /// 透過 MCPHandler 執行工具(組 JSON-RPC tools/call),回傳輸出文字與是否錯誤
    private func call(_ name: String, _ args: [String: Any]) async -> (String, Bool) {
        nextId += 1
        let msg: [String: Any] = ["jsonrpc": "2.0", "id": nextId, "method": "tools/call", "params": ["name": name, "arguments": args]]
        guard let raw = await mcp.handle(MCPJSON.data(msg)),
              let obj = try? JSONSerialization.jsonObject(with: raw) as? [String: Any] else { return ("工具沒有回應", true) }
        if let e = obj["error"] as? [String: Any] { return (e["message"] as? String ?? "工具錯誤", true) }
        let result = obj["result"] as? [String: Any] ?? [:]
        let text = (result["content"] as? [[String: Any]])?.first?["text"] as? String ?? ""
        return (text, result["isError"] as? Bool ?? false)
    }
}
