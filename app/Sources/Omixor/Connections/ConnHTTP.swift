import Foundation

/// 連線用的 HTTP。測試時可以塞 URLProtocol 攔截(protocolClasses)。
enum ConnHTTP {
    nonisolated(unsafe) static var protocolClasses: [AnyClass] = []

    static var session: URLSession {
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 60
        if !protocolClasses.isEmpty { c.protocolClasses = protocolClasses + (c.protocolClasses ?? []) }
        return URLSession(configuration: c)
    }

    struct Response {
        let status: Int
        let data: Data
        var headers: [String: String] = [:]
        /// 回應標頭(不分大小寫)
        func header(_ name: String) -> String? { headers.first { $0.key.caseInsensitiveCompare(name) == .orderedSame }?.value }
        var json: Any { (try? JSONSerialization.jsonObject(with: data, options: .fragmentsAllowed)) ?? NSNull() }
        var ok: Bool { (200..<300).contains(status) }
    }

    static func send(_ method: String, _ url: URL, headers: [String: String] = [:], body: Data? = nil) async throws -> Response {
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.httpBody = body
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
        let (data, resp) = try await session.data(for: req, delegate: RedirectGuard.shared)
        let http = resp as? HTTPURLResponse
        var h: [String: String] = [:]
        for (k, v) in http?.allHeaderFields ?? [:] { if let k = k as? String, let v = v as? String { h[k] = v } }
        return Response(status: http?.statusCode ?? 0, data: data, headers: h)
    }

    static func json(_ method: String, _ url: URL, _ obj: Any, headers: [String: String] = [:]) async throws -> Response {
        var h = headers
        h["Content-Type"] = "application/json"
        return try await send(method, url, headers: h, body: try JSONSerialization.data(withJSONObject: obj))
    }

    static func form(_ url: URL, _ fields: [String: String]) async throws -> Response {
        try await send("POST", url, headers: ["Content-Type": "application/x-www-form-urlencoded"],
                       body: Data(formEncode(fields).utf8))
    }

    static func formEncode(_ fields: [String: String]) -> String {
        var allowed = CharacterSet.alphanumerics
        allowed.insert(charactersIn: "-._~")
        return fields.sorted { $0.key < $1.key }
            .map { "\($0.key)=\($0.value.addingPercentEncoding(withAllowedCharacters: allowed) ?? $0.value)" }
            .joined(separator: "&")
    }

    /// 伺服器錯誤訊息:常見的 {error:{message}}、{message}、{error, error_description}
    static func errorMessage(_ r: Response) -> String {
        let o = r.json as? [String: Any]
        if let e = o?["error"] as? [String: Any], let m = e["message"] as? String { return m }
        if let d = o?["error_description"] as? String { return d }
        if let m = o?["message"] as? String { return m }
        if let e = o?["error"] as? String { return e }
        let text = String(decoding: r.data.prefix(300), as: UTF8.self)
        return text.isEmpty ? HTTPURLResponse.localizedString(forStatusCode: r.status) : text
    }

    static func check(_ r: Response) throws -> Response {
        guard r.ok else { throw ConnectionError(r.status == 401 ? "登入已失效,請重新登入" : errorMessage(r), status: r.status) }
        return r
    }

    /// 路徑的每一段各自編碼,保留斜線
    static func escapePath(_ path: String) -> String {
        path.split(separator: "/", omittingEmptySubsequences: false).map {
            String($0).addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? String($0)
        }.joined(separator: "/")
    }

    /// 從 JSON 找出可以顯示的帳號名稱(email、name、login、sub),也看 user 底下
    static func accountLabel(_ obj: Any) -> String? {
        guard let d = obj as? [String: Any] else { return nil }
        for k in ["email", "preferred_username", "login", "name", "username", "sub"] {
            if let s = d[k] as? String, !s.isEmpty { return s }
        }
        for k in ["user", "data", "account", "me"] {
            if let inner = d[k], let s = accountLabel(inner) { return s }
        }
        return nil
    }

    /// 解開 JWT 的 payload(不驗證簽章,只拿來顯示帳號)
    static func jwtClaims(_ jwt: String) -> [String: Any]? {
        let parts = jwt.split(separator: ".")
        guard parts.count >= 2 else { return nil }
        var b64 = String(parts[1]).replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while b64.count % 4 != 0 { b64 += "=" }
        guard let d = Data(base64Encoded: b64) else { return nil }
        return try? JSONSerialization.jsonObject(with: d) as? [String: Any]
    }
}

/// 轉址到別的網站時拿掉 Authorization,token 不會送到第三方(例如 GitHub 紀錄轉到暫時的下載網址)
final class RedirectGuard: NSObject, URLSessionTaskDelegate, Sendable {
    static let shared = RedirectGuard()

    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest) async -> URLRequest? {
        var r = request
        if request.url?.host != task.originalRequest?.url?.host { r.setValue(nil, forHTTPHeaderField: "Authorization") }
        return r
    }
}
