import CryptoKit
import Foundation

// 遠端模式(OmixorMode.swift)時 Issue 不讀寫本機的 issues.json,改透過 omixor server(core)的
// /api/orgs/<組織>/issues 讀寫。本機模式一律存在這台 Mac 的 issues.json。
//
//   網址:目前連的遠端 omixor(啟動設定裡的,見 OmixorMode.servers)
//   token:環境變數 OPS_ISSUE_SERVER_TOKEN(固定的,開發用);沒設時用 omixor 自己的 token(omx_…,
//          鑰匙圈的 remote/omixor-issues,登入時存的):沒有或被拒絕(401)時,拿 evoleap 連線登入後的 token
//          去 /api/auth/evoleap 換一個(evoleap 只證明身分,能不能用由 omixor 的使用者名單決定),再重送一次
//
// IssueStore 的介面是同步的(畫面與 MCP 工具直接拿回傳值),所以這裡用同步請求(有逾時)。

struct IssueServerConfig: Equatable {
    var url: URL
    var token: String?

    static let defaultsKey = "issueServerURL"
    /// 換 omixor token 時用來證明身分的連線(evoleap 正式站)
    static let evoleapConnection = "evoleap"
    /// omixor 自己的 token 存在鑰匙圈的這個名稱
    static let omixorTokenId = "omixor-issues"

    /// 測試用:非 nil 時取代環境變數與設定
    nonisolated(unsafe) static var testOverride: IssueServerConfig?

    static var current: IssueServerConfig? {
        if let o = testOverride { return o }
        guard let srv = OmixorMode.remoteServer else { return nil }
        let tok = ProcessInfo.processInfo.environment["OPS_ISSUE_SERVER_TOKEN"].flatMap { $0.isEmpty ? nil : $0 }
        return IssueServerConfig(url: srv.url, token: tok)
    }
}

struct IssueRemoteError: LocalizedError {
    var message: String
    var errorDescription: String? { message }
}

/// omixor server(core)/api/orgs/<組織>/issues 的用戶端
final class IssueRemote {
    let config: IssueServerConfig
    let session: URLSession
    var timeout: TimeInterval = 8

    /// 測試用:攔截請求的 URLProtocol
    nonisolated(unsafe) static var protocolClasses: [AnyClass] = []

    init(config: IssueServerConfig) {
        self.config = config
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 8
        if !Self.protocolClasses.isEmpty { c.protocolClasses = Self.protocolClasses }
        session = URLSession(configuration: c)
    }

    private static func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }

    private static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    private func url(_ org: String, _ rest: [String]) -> URL {
        // 核心的路徑:/api/orgs/<組織>/issues[/<id>[/delete]](伺服器也還接受舊的 /api/issues/<組織>)
        // 遠端組織在 app 裡的 id 有 "~" 前綴,送給伺服器時去掉
        var u = config.url.appendingPathComponent("api").appendingPathComponent("orgs")
            .appendingPathComponent(OmixorMode.serverOrgId(org)).appendingPathComponent("issues")
        for r in rest { u = u.appendingPathComponent(r) }
        return u
    }

    /// 同步送出一個請求,回傳 (HTTP 狀態, 內容)
    private func perform(_ req: URLRequest) throws -> (Int, Data) {
        final class Box: @unchecked Sendable { var data: Data?; var resp: URLResponse?; var err: Error? }
        let box = Box()
        let sem = DispatchSemaphore(value: 0)
        let task = session.dataTask(with: req) { d, r, e in
            box.data = d; box.resp = r; box.err = e
            sem.signal()
        }
        task.resume()
        if sem.wait(timeout: .now() + timeout + 1) == .timedOut {
            task.cancel()
            throw IssueRemoteError(message: "Issue 伺服器沒有回應(\(config.url.absoluteString))")
        }
        if let e = box.err { throw IssueRemoteError(message: "連不到 Issue 伺服器:\(e.localizedDescription)") }
        return ((box.resp as? HTTPURLResponse)?.statusCode ?? 0, box.data ?? Data())
    }

    /// 測試用:取代鑰匙圈(omixor token 與 evoleap token 都從這裡讀寫)
    nonisolated(unsafe) static var testVault: [String: String]?

    /// omixor token 存在不分組織的 remote/;evoleap 連線的 token 照連線的規則存在目前組織底下
    private func scope(_ id: String) -> String { id == IssueServerConfig.omixorTokenId ? OmixorMode.tokenScope : TokenVault.scope }

    private func vaultGet(_ id: String) -> String? {
        if let v = Self.testVault { return v[id] }
        return TokenVault.tokens(id, scope: scope(id))?.accessToken
    }

    private func vaultSet(_ id: String, _ token: String?, expires: Date?) {
        if Self.testVault != nil { Self.testVault?[id] = token; return }
        TokenVault.setTokens(token.map { TokenSet(accessToken: $0, expiresAt: expires, tokenType: "Bearer") }, id, scope: scope(id))
    }

    /// 用 evoleap 連線的登入,向 omixor-issues 換一個 omixor 自己的 token(存進鑰匙圈)
    private func exchangeToken() throws -> String {
        guard let evo = vaultGet(IssueServerConfig.evoleapConnection), !evo.isEmpty else {
            throw IssueRemoteError(message: "還沒登入 omixor:請從左下角的帳號選單重新連接遠端 omixor(或先登入 evoleap 連線,omixor 會用它證明身分)")
        }
        var req = URLRequest(url: config.url.appendingPathComponent("api/auth/evoleap"), timeoutInterval: timeout)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        let device = "omixor app(\(Host.current().localizedName ?? "Mac"))"
        req.httpBody = try JSONSerialization.data(withJSONObject: ["token": evo, "device": device])
        let (status, data) = try perform(req)
        let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        if let e = obj?["error"] as? String { throw IssueRemoteError(message: "omixor 拒絕登入:\(e)") }
        guard status == 200, let tok = obj?["token"] as? String, !tok.isEmpty else {
            throw IssueRemoteError(message: "omixor 登入失敗(HTTP \(status))")
        }
        let exp = (obj?["expiresAt"] as? String).flatMap { ISO8601DateFormatter().date(from: $0) }
        vaultSet(IssueServerConfig.omixorTokenId, tok, expires: exp)
        return tok
    }

    /// 送出請求、等回應(同步),回傳 {"result": …} 的 result(JSON)。
    /// 沒設固定 token 時帶 omixor token;沒有或被拒絕(401)就換一個新的再送一次
    private func send(_ method: String, _ url: URL, _ body: Data?) throws -> Data {
        func request(_ token: String?) -> URLRequest {
            var req = URLRequest(url: url, timeoutInterval: timeout)
            req.httpMethod = method
            req.setValue("application/json", forHTTPHeaderField: "Accept")
            if let body {
                req.httpBody = body
                req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            }
            if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
            return req
        }
        var status: Int, data: Data
        if let fixed = config.token {
            (status, data) = try perform(request(fixed))
        } else {
            let saved = vaultGet(IssueServerConfig.omixorTokenId)
            (status, data) = try perform(request(saved ?? (try exchangeToken())))
            if status == 401, saved != nil {
                vaultSet(IssueServerConfig.omixorTokenId, nil, expires: nil)
                (status, data) = try perform(request(try exchangeToken()))
            }
        }
        let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        if let e = obj?["error"] as? String { throw IssueRemoteError(message: e) }
        guard (200..<300).contains(status), let obj, let result = obj["result"] else {
            throw IssueRemoteError(message: "Issue 伺服器回應不對(HTTP \(status))")
        }
        return try JSONSerialization.data(withJSONObject: result, options: .fragmentsAllowed)
    }

    private func json(_ dict: [String: Any]) throws -> Data { try JSONSerialization.data(withJSONObject: dict) }

    /// 留言 id 不是 UUID 的(例如 Go 版從 Jira 匯入的 jira-123):換成固定的 UUID 給 app 用,送回去時換回原本的 id
    private var foreignIds: [String: String] = [:]

    private func uuidFor(_ id: String) -> String {
        var b = Array(SHA256.hash(data: Data(id.utf8)).prefix(16))
        b[6] = b[6] & 0x0f | 0x50
        b[8] = b[8] & 0x3f | 0x80
        let u = UUID(uuid: (b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])).uuidString
        foreignIds[u] = id
        return u
    }

    private func fixComments(_ issue: Any) -> Any {
        guard var d = issue as? [String: Any], let cs = d["comments"] as? [[String: Any]] else { return issue }
        d["comments"] = cs.map { c -> [String: Any] in
            var c = c
            if let id = c["id"] as? String, UUID(uuidString: id) == nil { c["id"] = uuidFor(id) }
            return c
        }
        return d
    }

    private func decodeIssues(_ data: Data) throws -> [OpsIssue] {
        let arr = (try JSONSerialization.jsonObject(with: data) as? [Any] ?? []).map(fixComments)
        return try Self.decoder().decode([OpsIssue].self, from: try JSONSerialization.data(withJSONObject: arr))
    }

    private func decodeIssue(_ data: Data) throws -> OpsIssue {
        let obj = fixComments(try JSONSerialization.jsonObject(with: data))
        return try Self.decoder().decode(OpsIssue.self, from: try JSONSerialization.data(withJSONObject: obj))
    }

    func list(org: String) throws -> [OpsIssue] {
        try decodeIssues(try send("GET", url(org, []), nil))
    }

    func create(org: String, title: String, body: String, priority: IssuePriority, labels: [String],
                source: String, link: String, author: String) throws -> OpsIssue {
        let b = try json(["title": title, "body": body, "priority": priority.rawValue, "labels": labels,
                          "source": source, "link": link, "author": author])
        return try decodeIssue(try send("POST", url(org, []), b))
    }

    /// 修改:送出可以改的欄位(整個取代),伺服器處理更新時間、關閉時間與動態牆
    func update(org: String, _ i: OpsIssue, author: String) throws -> OpsIssue {
        let enc = try Self.encoder().encode(i)
        guard var d = try JSONSerialization.jsonObject(with: enc) as? [String: Any] else {
            throw IssueRemoteError(message: "Issue 編碼失敗")
        }
        for k in ["id", "createdAt", "createdBy", "updatedAt", "closedAt"] { d.removeValue(forKey: k) }
        if let cs = d["comments"] as? [[String: Any]] {
            d["comments"] = cs.map { c -> [String: Any] in
                var c = c
                if let id = c["id"] as? String, let orig = foreignIds[id] { c["id"] = orig }
                return c
            }
        }
        d["author"] = author
        return try decodeIssue(try send("POST", url(org, [i.id]), try json(d)))
    }

    func delete(org: String, id: String) throws {
        _ = try send("POST", url(org, [id, "delete"]), try json([:]))
    }
}
