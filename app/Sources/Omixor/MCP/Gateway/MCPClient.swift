import Foundation

// 外部 MCP 伺服器的用戶端(App 當 MCP 閘道用,見 MCPGateway.swift)。
//
//   stdio:啟動子程序,JSON-RPC 一行一則走標準輸入輸出。密鑰(環境變數的值)只放進子程序的環境變數,不放命令列;
//          子程序拿不到 App 自己的 token 類環境變數(只帶 PATH、HOME 這些基本的)。
//   http :Streamable HTTP:POST JSON-RPC,回應是 application/json 或 text/event-stream(SSE,取對應 id 的那則);
//          記住伺服器給的 Mcp-Session-Id。密鑰(標頭的值)只放在請求標頭。
//
// 流程:initialize → notifications/initialized → tools/list(有 nextCursor 就繼續)→ tools/call。
// 每個請求都有逾時;子程序結束時所有等待中的請求回錯誤;close() 停掉子程序(App 結束時 MCPGateway.stopAll 會呼叫)。
// 錯誤訊息裡如果出現密鑰的值一律換成 ***(見 MCPClientError.masked)。

struct MCPClientError: LocalizedError {
    var message: String
    var errorDescription: String? { message }

    /// 把訊息裡的密鑰值換成 ***
    static func masked(_ s: String, _ secrets: [String]) -> String {
        var out = s
        for v in secrets where v.count >= 4 { out = out.replacingOccurrences(of: v, with: "***") }
        return out
    }
}

/// 外部伺服器的一個工具
struct MCPRemoteTool: Identifiable, Equatable {
    var name: String
    var title: String
    var description: String
    /// inputSchema(JSON);沒有時是空物件 schema
    var schemaJSON: Data
    var id: String { name }

    var inputSchema: [String: Any] {
        (try? JSONSerialization.jsonObject(with: schemaJSON)) as? [String: Any] ?? ["type": "object", "properties": [String: Any]()]
    }

    init(name: String, title: String = "", description: String = "", schema: [String: Any]? = nil) {
        self.name = name
        self.title = title
        self.description = description
        let s = schema ?? ["type": "object", "properties": [String: Any]()]
        schemaJSON = (try? JSONSerialization.data(withJSONObject: s, options: [.sortedKeys])) ?? Data("{}".utf8)
    }
}

/// 傳輸層:送一則請求等回應、送通知、關閉
protocol MCPTransport: AnyObject {
    func start() throws
    func request(_ msg: [String: Any], id: Int, timeout: TimeInterval) async throws -> [String: Any]
    func notify(_ msg: [String: Any]) async throws
    func close()
}

// MARK: - stdio

final class MCPStdioTransport: MCPTransport, @unchecked Sendable {
    let command: String
    let args: [String]
    let cwd: String?
    let env: [String: String]
    private let secrets: [String]
    private var process: Process?
    private var stdin: FileHandle?
    private let lock = NSLock()
    private var buffer = Data()
    private var stderrTail = Data()
    private var pending: [Int: CheckedContinuation<[String: Any], Error>] = [:]
    private var exited: String?

    /// 子程序可以拿到的 App 環境變數(其他的,例如 App 自己的 token,一律不帶)
    static let passEnv = ["PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "SHELL", "TERM"]

    init(command: String, args: [String], cwd: String?, secretEnv: [String: String]) {
        self.command = command
        self.args = args
        self.cwd = cwd.flatMap { $0.isEmpty ? nil : ($0 as NSString).expandingTildeInPath }
        var e: [String: String] = [:]
        let pe = ProcessInfo.processInfo.environment
        for k in Self.passEnv { if let v = pe[k] { e[k] = v } }
        if !(e["PATH"] ?? "").contains("/opt/homebrew/bin") { e["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:" + (e["PATH"] ?? "") }
        for (k, v) in secretEnv { e[k] = v }
        env = e
        secrets = Array(secretEnv.values)
    }

    var pid: Int32? { lock.lock(); defer { lock.unlock() }; return process?.isRunning == true ? process?.processIdentifier : nil }

    func start() throws {
        let p = Process()
        if command.contains("/") {
            p.executableURL = URL(fileURLWithPath: (command as NSString).expandingTildeInPath)
            p.arguments = args
        } else {
            // 指令名稱(例如 python3、npx):用 PATH 找
            p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
            p.arguments = [command] + args
        }
        p.environment = env
        if let cwd { p.currentDirectoryURL = URL(fileURLWithPath: cwd) }
        let inPipe = Pipe(), outPipe = Pipe(), errPipe = Pipe()
        p.standardInput = inPipe
        p.standardOutput = outPipe
        p.standardError = errPipe
        outPipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            if d.isEmpty { h.readabilityHandler = nil; return }
            self?.received(d)
        }
        errPipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            if d.isEmpty { h.readabilityHandler = nil; return }
            guard let self else { return }
            self.lock.lock()
            self.stderrTail.append(d)
            if self.stderrTail.count > 4096 { self.stderrTail = self.stderrTail.suffix(4096) }
            self.lock.unlock()
        }
        p.terminationHandler = { [weak self] proc in
            self?.failAll("外部 MCP 伺服器的子程序結束了(代碼 \(proc.terminationStatus))")
        }
        do { try p.run() } catch {
            throw MCPClientError(message: "啟動不了「\(command)」:\(error.localizedDescription)")
        }
        lock.lock()
        process = p
        stdin = inPipe.fileHandleForWriting
        exited = nil
        lock.unlock()
    }

    private func stderrText() -> String {
        lock.lock(); defer { lock.unlock() }
        let s = String(decoding: stderrTail, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        return MCPClientError.masked(String(s.suffix(500)), secrets)
    }

    private func failAll(_ why: String) {
        let tail = stderrText()
        let msg = tail.isEmpty ? why : "\(why):\(tail)"
        lock.lock()
        exited = msg
        let ps = pending
        pending = [:]
        lock.unlock()
        for (_, c) in ps { c.resume(throwing: MCPClientError(message: msg)) }
    }

    private func received(_ d: Data) {
        lock.lock()
        buffer.append(d)
        var lines: [Data] = []
        while let nl = buffer.firstIndex(of: 0x0A) {
            lines.append(buffer[buffer.startIndex..<nl])
            buffer.removeSubrange(buffer.startIndex...nl)
        }
        lock.unlock()
        for line in lines where !line.allSatisfy({ $0 == 0x20 || $0 == 0x0D }) {
            guard let obj = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else { continue }
            handle(obj)
        }
    }

    private func handle(_ obj: [String: Any]) {
        // 伺服器送來的請求(ping 等):ping 回空結果,其他回不支援
        if let method = obj["method"] as? String {
            guard let id = obj["id"] else { return }   // 通知:不理
            let reply: [String: Any] = method == "ping"
                ? ["jsonrpc": "2.0", "id": id, "result": [String: Any]()]
                : ["jsonrpc": "2.0", "id": id, "error": ["code": -32601, "message": "omixor 閘道不支援 \(method)"]]
            try? write(reply)
            return
        }
        guard let id = (obj["id"] as? Int) ?? (obj["id"] as? NSNumber)?.intValue else { return }
        lock.lock()
        let c = pending.removeValue(forKey: id)
        lock.unlock()
        c?.resume(returning: obj)
    }

    private func write(_ msg: [String: Any]) throws {
        var d = try JSONSerialization.data(withJSONObject: msg)
        d.append(0x0A)
        lock.lock()
        let h = stdin, gone = exited
        lock.unlock()
        if let gone { throw MCPClientError(message: gone) }
        guard let h else { throw MCPClientError(message: "外部 MCP 伺服器還沒啟動") }
        do { try h.write(contentsOf: d) } catch { throw MCPClientError(message: "寫不進外部 MCP 伺服器:\(error.localizedDescription)") }
    }

    func request(_ msg: [String: Any], id: Int, timeout: TimeInterval) async throws -> [String: Any] {
        try await withCheckedThrowingContinuation { (c: CheckedContinuation<[String: Any], Error>) in
            lock.lock()
            if let gone = exited { lock.unlock(); c.resume(throwing: MCPClientError(message: gone)); return }
            pending[id] = c
            lock.unlock()
            do { try write(msg) } catch {
                lock.lock()
                let p = pending.removeValue(forKey: id)
                lock.unlock()
                p?.resume(throwing: error)
                return
            }
            DispatchQueue.global().asyncAfter(deadline: .now() + timeout) { [weak self] in
                guard let self else { return }
                self.lock.lock()
                let p = self.pending.removeValue(forKey: id)
                self.lock.unlock()
                p?.resume(throwing: MCPClientError(message: "外部 MCP 伺服器沒有在 \(Int(timeout)) 秒內回應"))
            }
        }
    }

    func notify(_ msg: [String: Any]) async throws { try write(msg) }

    func close() {
        lock.lock()
        let p = process, h = stdin
        process = nil
        stdin = nil
        lock.unlock()
        try? h?.close()
        if let p, p.isRunning {
            p.terminate()
            // 不肯結束就強制停
            let pid = p.processIdentifier
            DispatchQueue.global().asyncAfter(deadline: .now() + 2) { if p.isRunning { kill(pid, SIGKILL) } }
        }
        failAll("外部 MCP 連線已關閉")
    }

    deinit { if let p = process, p.isRunning { p.terminate() } }
}

// MARK: - HTTP(Streamable HTTP)

final class MCPHTTPTransport: MCPTransport, @unchecked Sendable {
    let url: URL
    private let headers: [String: String]
    private let secrets: [String]
    private let session: URLSession
    private let lock = NSLock()
    private var sessionId: String?
    var protocolVersion: String?

    init(url: URL, secretHeaders: [String: String]) {
        self.url = url
        headers = secretHeaders
        secrets = Array(secretHeaders.values)
        let c = URLSessionConfiguration.ephemeral
        c.httpCookieStorage = nil
        c.urlCache = nil
        session = URLSession(configuration: c)
    }

    func start() throws {}

    private func makeRequest(_ msg: [String: Any], timeout: TimeInterval) throws -> URLRequest {
        var req = URLRequest(url: url, timeoutInterval: timeout)
        req.httpMethod = "POST"
        req.httpBody = try JSONSerialization.data(withJSONObject: msg)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
        for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
        lock.lock()
        if let s = sessionId { req.setValue(s, forHTTPHeaderField: "Mcp-Session-Id") }
        if let v = protocolVersion { req.setValue(v, forHTTPHeaderField: "MCP-Protocol-Version") }
        lock.unlock()
        return req
    }

    private func send(_ msg: [String: Any], timeout: TimeInterval) async throws -> (Int, String, Data) {
        let req = try makeRequest(msg, timeout: timeout)
        let (data, resp): (Data, URLResponse)
        do { (data, resp) = try await session.data(for: req) } catch {
            throw MCPClientError(message: "連不到外部 MCP 伺服器(\(url.host ?? "")):\(MCPClientError.masked(error.localizedDescription, secrets))")
        }
        let h = resp as? HTTPURLResponse
        if let s = h?.value(forHTTPHeaderField: "Mcp-Session-Id"), !s.isEmpty {
            lock.withLock { sessionId = s }
        }
        return (h?.statusCode ?? 0, h?.value(forHTTPHeaderField: "Content-Type") ?? "", data)
    }

    func request(_ msg: [String: Any], id: Int, timeout: TimeInterval) async throws -> [String: Any] {
        let (status, type, data) = try await send(msg, timeout: timeout)
        guard (200..<300).contains(status) else {
            let body = MCPClientError.masked(String(decoding: data.prefix(300), as: UTF8.self), secrets)
            throw MCPClientError(message: status == 401 || status == 403
                ? "外部 MCP 伺服器拒絕(HTTP \(status)):請確認這台裝置輸入的密鑰"
                : "外部 MCP 伺服器回應不對(HTTP \(status)):\(body)")
        }
        if type.lowercased().contains("text/event-stream") {
            for obj in Self.sseMessages(data) {
                if let rid = (obj["id"] as? Int) ?? (obj["id"] as? NSNumber)?.intValue, rid == id, obj["method"] == nil { return obj }
            }
            throw MCPClientError(message: "外部 MCP 伺服器的 SSE 回應裡沒有這個請求的結果")
        }
        if let obj = try? JSONSerialization.jsonObject(with: data) {
            if let d = obj as? [String: Any] { return d }
            if let arr = obj as? [[String: Any]], let d = arr.first(where: { ($0["id"] as? Int) == id }) { return d }
        }
        throw MCPClientError(message: "外部 MCP 伺服器的回應不是 JSON")
    }

    func notify(_ msg: [String: Any]) async throws {
        let (status, _, _) = try await send(msg, timeout: 10)
        guard (200..<300).contains(status) else { throw MCPClientError(message: "外部 MCP 伺服器不接受通知(HTTP \(status))") }
    }

    /// SSE:每個事件的 data 行合起來是一則 JSON
    static func sseMessages(_ data: Data) -> [[String: Any]] {
        var out: [[String: Any]] = []
        var cur: [String] = []
        func flush() {
            if !cur.isEmpty, let d = cur.joined(separator: "\n").data(using: .utf8),
               let o = try? JSONSerialization.jsonObject(with: d) as? [String: Any] { out.append(o) }
            cur = []
        }
        for raw in String(decoding: data, as: UTF8.self).components(separatedBy: "\n") {
            let line = raw.hasSuffix("\r") ? String(raw.dropLast()) : raw
            if line.isEmpty { flush(); continue }
            if line.hasPrefix("data:") { cur.append(String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)) }
        }
        flush()
        return out
    }

    func close() {
        // 有 session 時告訴伺服器結束(失敗不管)
        lock.lock()
        let sid = sessionId
        sessionId = nil
        lock.unlock()
        guard let sid else { return }
        var req = URLRequest(url: url, timeoutInterval: 3)
        req.httpMethod = "DELETE"
        req.setValue(sid, forHTTPHeaderField: "Mcp-Session-Id")
        for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
        session.dataTask(with: req).resume()
    }
}

// MARK: - 用戶端

final class MCPClient: @unchecked Sendable {
    let transport: MCPTransport
    private let lock = NSLock()
    private var nextId = 1
    private let secrets: [String]
    private(set) var serverName = ""
    private(set) var protocolVersion = ""
    static let clientVersion = "2025-06-18"
    var timeout: TimeInterval = 30

    init(transport: MCPTransport, secrets: [String] = []) {
        self.transport = transport
        self.secrets = secrets
    }

    private func newId() -> Int {
        lock.lock(); defer { lock.unlock() }
        nextId += 1
        return nextId
    }

    /// 送一個請求,回傳 result(JSON-RPC 的 error 轉成 MCPClientError)
    func call(_ method: String, _ params: [String: Any] = [:], timeout: TimeInterval? = nil) async throws -> [String: Any] {
        let id = newId()
        let resp = try await transport.request(["jsonrpc": "2.0", "id": id, "method": method, "params": params], id: id, timeout: timeout ?? self.timeout)
        if let e = resp["error"] as? [String: Any] {
            throw MCPClientError(message: MCPClientError.masked("外部 MCP 伺服器回錯誤:\(e["message"] as? String ?? "未知")", secrets))
        }
        return resp["result"] as? [String: Any] ?? [:]
    }

    /// 啟動、握手
    func connect() async throws {
        try transport.start()
        let r = try await call("initialize", [
            "protocolVersion": Self.clientVersion,
            "capabilities": [String: Any](),
            "clientInfo": ["name": "omixor-gateway", "title": "omixor 閘道", "version": MCPHandler.serverVersion],
        ])
        protocolVersion = r["protocolVersion"] as? String ?? Self.clientVersion
        serverName = (r["serverInfo"] as? [String: Any])?["name"] as? String ?? ""
        (transport as? MCPHTTPTransport)?.protocolVersion = protocolVersion
        try await transport.notify(["jsonrpc": "2.0", "method": "notifications/initialized"])
    }

    /// tools/list(有分頁就讀完,最多 20 頁)
    func listTools() async throws -> [MCPRemoteTool] {
        var out: [MCPRemoteTool] = []
        var cursor: String?
        for _ in 0..<20 {
            let r = try await call("tools/list", cursor.map { ["cursor": $0] } ?? [:])
            for t in r["tools"] as? [[String: Any]] ?? [] {
                guard let n = t["name"] as? String, !n.isEmpty else { continue }
                out.append(MCPRemoteTool(name: n, title: t["title"] as? String ?? (t["annotations"] as? [String: Any])?["title"] as? String ?? "",
                                         description: t["description"] as? String ?? "", schema: t["inputSchema"] as? [String: Any]))
            }
            cursor = r["nextCursor"] as? String
            if cursor?.isEmpty ?? true { break }
        }
        return out
    }

    /// tools/call:回傳 result(content、isError…;內容是外部的,不可信)
    func callTool(_ name: String, _ args: [String: Any], timeout: TimeInterval? = nil) async throws -> [String: Any] {
        try await call("tools/call", ["name": name, "arguments": args], timeout: timeout)
    }

    func close() { transport.close() }
}
