import Foundation
import Network

/// 登入時接收瀏覽器導回的臨時伺服器:只綁 127.0.0.1,收到一次符合的回呼就結束。
/// OAuth 用 state 比對、evoleap 的瀏覽器核准用 id 比對;回呼帶 error 時直接回報錯誤。
final class LoopbackCallback: @unchecked Sendable {
    private let listener: NWListener
    private let queue = DispatchQueue(label: "ops.connection.callback")
    private var continuation: CheckedContinuation<String, Error>?
    private var received: Result<String, Error>?
    private var startResumed = false
    private(set) var port: UInt16 = 0
    /// 回呼必須帶的參數(例如 state=…),防止別人塞來的回呼
    var expect: (name: String, value: String) = ("", "")
    var successTitle = "登入完成"

    var redirectURI: String { "http://127.0.0.1:\(port)/callback" }

    private init(listener: NWListener) { self.listener = listener }

    /// preferredPort 有給就先試那個埠(動態註冊的用戶端登記過固定的回呼網址),被占用就改用隨機埠
    static func start(preferredPort: UInt16? = nil) async throws -> LoopbackCallback {
        if let p = preferredPort, let cb = try? await make(port: NWEndpoint.Port(rawValue: p) ?? .any) { return cb }
        return try await make(port: .any)
    }

    private static func make(port: NWEndpoint.Port) async throws -> LoopbackCallback {
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: port)
        params.requiredInterfaceType = .loopback
        let cb = LoopbackCallback(listener: try NWListener(using: params))
        try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Void, Error>) in
            // stateUpdateHandler 都在 cb.queue 上依序執行
            cb.listener.stateUpdateHandler = { state in
                guard !cb.startResumed else { return }
                switch state {
                case .ready:
                    cb.startResumed = true
                    cb.port = cb.listener.port?.rawValue ?? 0
                    cont.resume()
                case .failed(let e):
                    cb.startResumed = true
                    cont.resume(throwing: e)
                default: break
                }
            }
            cb.listener.newConnectionHandler = { [weak cb] conn in cb?.accept(conn) }
            cb.listener.start(queue: cb.queue)
        }
        return cb
    }

    func stop() { listener.cancel() }

    /// 等回呼帶來的 code
    func waitForCode(timeout: TimeInterval) async throws -> String {
        try await withCheckedThrowingContinuation { cont in
            queue.async {
                if let r = self.received { cont.resume(with: r); return }
                self.continuation = cont
                self.queue.asyncAfter(deadline: .now() + timeout) {
                    self.finish(.failure(ConnectionError("等太久了(\(Int(timeout / 60)) 分鐘),請重新登入")))
                }
            }
        }
    }

    private func finish(_ r: Result<String, Error>) {
        guard received == nil else { return }
        received = r
        continuation?.resume(with: r)
        continuation = nil
    }

    private func accept(_ conn: NWConnection) {
        conn.start(queue: queue)
        receive(conn, Data())
    }

    private func receive(_ conn: NWConnection, _ buffer: Data) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 16 * 1024) { [weak self] data, _, done, err in
            guard let self else { return }
            var buf = buffer
            if let data { buf.append(data) }
            switch HTTPRequest.parse(buf, maxBody: 1024) {
            case .incomplete where !done && err == nil: self.receive(conn, buf)
            case .complete(let req): self.handle(req, conn)
            default: conn.cancel()
            }
        }
    }

    private func handle(_ req: HTTPRequest, _ conn: NWConnection) {
        let comps = URLComponents(string: "http://127.0.0.1" + (req.rawTarget ?? req.path))
        let q = Dictionary((comps?.queryItems ?? []).map { ($0.name, $0.value ?? "") }, uniquingKeysWith: { a, _ in a })
        guard req.path == "/callback" else { respond(conn, 404, "找不到", "這不是登入回呼網址。"); return }
        guard q[expect.name] == expect.value else {
            respond(conn, 400, "登入失敗", "回呼的驗證參數不符,請回到 omixor 重新登入。")
            return
        }
        if let error = q["error"] {
            let msg = q["error_description"].map { "\(error):\($0)" } ?? error
            respond(conn, 400, "登入失敗", msg)
            finish(.failure(ConnectionError("授權被拒絕:\(msg)")))
            return
        }
        guard let code = q["code"], !code.isEmpty else { respond(conn, 400, "登入失敗", "回呼沒有帶授權碼。"); return }
        respond(conn, 200, successTitle, "omixor 已完成登入,可以關掉這個分頁。")
        finish(.success(code))
    }

    private func respond(_ conn: NWConnection, _ status: Int, _ title: String, _ msg: String) {
        let esc = { (s: String) in s.replacingOccurrences(of: "&", with: "&amp;").replacingOccurrences(of: "<", with: "&lt;") }
        let html = "<!doctype html><meta charset=\"utf-8\"><title>\(esc(title))</title><body style=\"font:16px -apple-system;padding:48px\"><h2>\(esc(title))</h2><p>\(esc(msg))</p>"
        let resp = HTTPResponse(status: status, body: Data(html.utf8), contentType: "text/html; charset=utf-8")
        conn.send(content: resp.serialized(), completion: .contentProcessed { _ in conn.cancel() })
    }
}
