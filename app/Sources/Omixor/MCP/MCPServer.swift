import Foundation
import Network

/// 顯示在設定頁的 MCP 伺服器狀態
struct MCPStatus: Equatable {
    enum State: Equatable { case stopped, starting, listening, failed(String) }
    var state: State = .stopped
    var port: UInt16 = MCPServer.defaultPort
    var requests = 0
    var lastRequestAt: Date?
    var lastClient: String?
    var lastTool: String?

    var url: String { "http://127.0.0.1:\(port)/mcp" }
}

/// MCP 的 Streamable HTTP 傳輸:只聽 127.0.0.1,`POST /mcp` 收 JSON-RPC、回 application/json。
/// 不開 SSE 串流(GET 回 405),也不用 session;規格允許這樣的最小實作。
/// 每個連線處理一個請求就關閉(Connection: close)。
final class MCPServer: @unchecked Sendable {
    static let defaultPort: UInt16 = 3765
    static let maxBody = 4 << 20

    /// 連接埠:環境變數 OPS_MCP_PORT 優先,其次是 UserDefaults 的 mcp.port
    static var configuredPort: UInt16 {
        if let s = ProcessInfo.processInfo.environment["OPS_MCP_PORT"], let p = UInt16(s) { return p }
        let p = UserDefaults.standard.integer(forKey: "mcp.port")
        return p > 0 && p < 65536 ? UInt16(p) : defaultPort
    }

    private let port: UInt16
    private let queue = DispatchQueue(label: "ops.mcp.server")
    private var listener: NWListener?
    private let handle: @Sendable (Data) async -> Data?
    private let onState: @Sendable (MCPStatus.State) -> Void

    init(port: UInt16,
         handle: @escaping @Sendable (Data) async -> Data?,
         onState: @escaping @Sendable (MCPStatus.State) -> Void) {
        self.port = port
        self.handle = handle
        self.onState = onState
    }

    func start() {
        onState(.starting)
        do {
            let params = NWParameters.tcp
            params.allowLocalEndpointReuse = true
            guard let nwPort = NWEndpoint.Port(rawValue: port) else { throw MCPToolError("無效的連接埠 \(port)") }
            // 只綁 127.0.0.1,區網連不進來
            params.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: nwPort)
            params.requiredInterfaceType = .loopback
            let l = try NWListener(using: params)
            l.stateUpdateHandler = { [weak self] st in
                switch st {
                case .ready: self?.onState(.listening)
                case .failed(let e): self?.onState(.failed(Self.describe(e)))
                case .cancelled: self?.onState(.stopped)
                default: break
                }
            }
            l.newConnectionHandler = { [weak self] conn in self?.accept(conn) }
            l.start(queue: queue)
            listener = l
        } catch let e as MCPToolError {
            onState(.failed(e.message))
        } catch {
            onState(.failed(Self.describe(error)))
        }
    }

    func stop() {
        listener?.cancel()
        listener = nil
    }

    private static func describe(_ error: Error) -> String {
        if let e = error as? NWError, case .posix(let code) = e, code == .EADDRINUSE {
            return "連接埠已被占用"
        }
        return "\(error)"
    }

    // MARK: - 連線

    private func accept(_ conn: NWConnection) {
        // 雙重保險:只處理來自 loopback 的連線
        if case .hostPort(let host, _) = conn.endpoint, !Self.isLoopback(host) {
            conn.cancel()
            return
        }
        conn.start(queue: queue)
        receive(conn, buffer: Data())
    }

    private static func isLoopback(_ host: NWEndpoint.Host) -> Bool {
        switch host {
        case .ipv4(let a): return a.isLoopback
        case .ipv6(let a): return a.isLoopback || a.asIPv4?.isLoopback == true
        case .name(let n, _): return n == "localhost"
        @unknown default: return false
        }
    }

    private func receive(_ conn: NWConnection, buffer: Data) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { [weak self] data, _, isComplete, error in
            guard let self else { conn.cancel(); return }
            var buf = buffer
            if let data { buf.append(data) }
            if error != nil { conn.cancel(); return }
            switch HTTPRequest.parse(buf, maxBody: Self.maxBody) {
            case .incomplete:
                if isComplete { conn.cancel() } else { self.receive(conn, buffer: buf) }
            case .invalid(let status, let msg):
                self.send(conn, HTTPResponse(status: status, body: Data(msg.utf8), contentType: "text/plain; charset=utf-8"))
            case .complete(let req):
                Task { [handle] in
                    let resp = await Self.route(req, handle: handle)
                    self.queue.async { self.send(conn, resp) }
                }
            }
        }
    }

    private func send(_ conn: NWConnection, _ resp: HTTPResponse) {
        conn.send(content: resp.serialized(), completion: .contentProcessed { _ in conn.cancel() })
    }

    // MARK: - 路由

    static func route(_ req: HTTPRequest, handle: @Sendable (Data) async -> Data?) async -> HTTPResponse {
        // 防 DNS rebinding:瀏覽器送來的請求帶 Origin,只接受本機來源
        if let origin = req.headers["origin"], !isLocalOrigin(origin) {
            return HTTPResponse(status: 403, body: Data("Forbidden origin".utf8), contentType: "text/plain")
        }
        guard req.path == "/mcp" else {
            return HTTPResponse(status: 404, body: Data("Not found. MCP endpoint is /mcp".utf8), contentType: "text/plain")
        }
        switch req.method {
        case "POST":
            guard let out = await handle(req.body) else { return HTTPResponse(status: 202, body: Data(), contentType: nil) }
            return HTTPResponse(status: 200, body: out, contentType: "application/json")
        case "GET", "DELETE":
            return HTTPResponse(status: 405, body: Data(), contentType: nil, extra: ["Allow": "POST"])
        default:
            return HTTPResponse(status: 405, body: Data(), contentType: nil, extra: ["Allow": "POST"])
        }
    }

    static func isLocalOrigin(_ origin: String) -> Bool {
        guard let host = URLComponents(string: origin)?.host?.lowercased() else { return false }
        return ["localhost", "127.0.0.1", "::1", "[::1]"].contains(host)
    }
}

// MARK: - 極簡 HTTP/1.1

struct HTTPRequest {
    let method: String
    let path: String
    let headers: [String: String]  // key 一律小寫
    let body: Data
    /// 原始的請求目標(含 query),例如 /callback?id=…
    var rawTarget: String? = nil

    enum ParseResult {
        case incomplete
        case invalid(Int, String)
        case complete(HTTPRequest)
    }

    static func parse(_ buf: Data, maxBody: Int) -> ParseResult {
        let sep = Data("\r\n\r\n".utf8)
        guard let headerEnd = buf.range(of: sep) else {
            return buf.count > 64 * 1024 ? .invalid(431, "Headers too large") : .incomplete
        }
        guard let head = String(data: buf[buf.startIndex..<headerEnd.lowerBound], encoding: .utf8) else {
            return .invalid(400, "Bad request")
        }
        let lines = head.components(separatedBy: "\r\n")
        let parts = lines[0].split(separator: " ")
        guard parts.count >= 2 else { return .invalid(400, "Bad request line") }
        var headers: [String: String] = [:]
        for line in lines.dropFirst() {
            guard let c = line.firstIndex(of: ":") else { continue }
            headers[line[..<c].trimmingCharacters(in: .whitespaces).lowercased()] =
                line[line.index(after: c)...].trimmingCharacters(in: .whitespaces)
        }
        if headers["transfer-encoding"]?.lowercased().contains("chunked") == true {
            return .invalid(411, "Chunked bodies are not supported; send Content-Length")
        }
        let length = Int(headers["content-length"] ?? "0") ?? -1
        guard length >= 0 else { return .invalid(400, "Bad Content-Length") }
        guard length <= maxBody else { return .invalid(413, "Body too large") }
        let bodyStart = headerEnd.upperBound
        guard buf.count - (bodyStart - buf.startIndex) >= length else { return .incomplete }
        let body = buf[bodyStart..<(bodyStart + length)]
        let target = String(parts[1])
        let path = target.split(separator: "?", maxSplits: 1).first.map(String.init) ?? target
        return .complete(HTTPRequest(method: String(parts[0]).uppercased(), path: path, headers: headers, body: Data(body),
                                     rawTarget: target))
    }
}

struct HTTPResponse {
    let status: Int
    let body: Data
    let contentType: String?
    var extra: [String: String] = [:]

    private var reason: String {
        [200: "OK", 202: "Accepted", 400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed",
         411: "Length Required", 413: "Payload Too Large", 431: "Request Header Fields Too Large"][status] ?? "Error"
    }

    func serialized() -> Data {
        var head = "HTTP/1.1 \(status) \(reason)\r\nContent-Length: \(body.count)\r\nConnection: close\r\n"
        if let contentType { head += "Content-Type: \(contentType)\r\n" }
        for (k, v) in extra { head += "\(k): \(v)\r\n" }
        head += "\r\n"
        return Data(head.utf8) + body
    }
}

// MARK: - 啟動

extension AppState {
    /// 啟動內建 MCP 伺服器。app 開著時,MCP 用戶端就能讀寫這裡的資料。
    func startMCPServer() {
        guard mcpServer == nil else { return }
        let port = MCPServer.configuredPort
        mcp.port = port
        let handler = MCPHandler(app: self)
        let server = MCPServer(
            port: port,
            handle: { data in await handler.handle(data) },
            onState: { [weak self] state in Task { @MainActor in self?.mcp.state = state } }
        )
        server.start()
        mcpServer = server
    }
}
