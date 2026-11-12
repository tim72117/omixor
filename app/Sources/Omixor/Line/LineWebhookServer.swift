import Foundation
import Network

/// 接 LINE webhook 的本機 HTTP 伺服器:只聽 127.0.0.1(Cloudflare Tunnel / ngrok 在本機把公開 https 轉進來),
/// 只有 `POST /line/webhook`。HTTP 解析沿用 MCP 伺服器的 HTTPRequest / HTTPResponse;每個連線處理一個請求就關閉。
final class LineWebhookServer: @unchecked Sendable {
    static let path = "/line/webhook"
    static let maxBody = 1 << 20

    private let port: UInt16
    private let queue = DispatchQueue(label: "ops.line.webhook")
    private var listener: NWListener?
    private let handle: @Sendable (HTTPRequest) async -> HTTPResponse
    private let onState: @Sendable (LineStatus.ServerState) -> Void

    init(port: UInt16,
         handle: @escaping @Sendable (HTTPRequest) async -> HTTPResponse,
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
            l.newConnectionHandler = { [weak self] conn in self?.accept(conn) }
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

    private func accept(_ conn: NWConnection) {
        conn.start(queue: queue)
        receive(conn, buffer: Data())
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
                    let resp: HTTPResponse
                    if req.path != Self.path {
                        resp = HTTPResponse(status: 404, body: Data("Not found".utf8), contentType: "text/plain")
                    } else if req.method != "POST" {
                        resp = HTTPResponse(status: 405, body: Data(), contentType: nil, extra: ["Allow": "POST"])
                    } else {
                        resp = await handle(req)
                    }
                    self.queue.async { self.send(conn, resp) }
                }
            }
        }
    }

    private func send(_ conn: NWConnection, _ resp: HTTPResponse) {
        conn.send(content: resp.serialized(), completion: .contentProcessed { _ in conn.cancel() })
    }
}
