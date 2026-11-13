import Foundation

/// 攔截 URLSession 請求的假伺服器(測試不連網路)
final class StubProtocol: URLProtocol {
    struct Reply { var status: Int; var body: String }
    nonisolated(unsafe) static var handler: ((URLRequest, Data?) -> Reply)?
    nonisolated(unsafe) static var requests: [(URLRequest, Data?)] = []

    static func session() -> URLSession {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [StubProtocol.self]
        return URLSession(configuration: c)
    }

    static func reset(_ h: @escaping (URLRequest, Data?) -> Reply) {
        handler = h
        requests = []
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody
        if body == nil, let s = request.httpBodyStream {
            s.open()
            var d = Data()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable {
                let n = s.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                d.append(buf, count: n)
            }
            s.close()
            body = d
        }
        Self.requests.append((request, body))
        let r = Self.handler?(request, body) ?? Reply(status: 404, body: #"{"error":"沒有設定假回應"}"#)
        let resp = HTTPURLResponse(url: request.url!, statusCode: r.status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(r.body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

func json(_ d: Data?) -> [String: Any] {
    (d.flatMap { try? JSONSerialization.jsonObject(with: $0) } as? [String: Any]) ?? [:]
}
