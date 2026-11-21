import CryptoKit
import Foundation

// Issue 一律透過 core server 的 /api/orgs/<組織>/issues 讀寫(遠端是設定裡的 omixor server,本機是 App 啟動的本機守護服務,
// 見 IssueServerConfig.current 與 LocalCore.swift)。App 不再讀寫本機的 issues.json。
//
//   網址:IssueServerConfig.current(遠端:啟動設定裡的 omixor,見 OmixorMode.servers;本機:127.0.0.1 的守護服務)
//   token:環境變數 OMIXOR_SERVER_TOKEN(舊名稱 OPS_ISSUE_SERVER_TOKEN;固定的,開發用);沒設時用 omixor 自己的 token(omx_…,
//          鑰匙圈的 remote/<伺服器>/omixor-issues,每台伺服器各一把,登入時存的):沒有或被拒絕(401)時,拿 evoleap 連線登入後的 token
//          去 /api/auth/evoleap 換一個(evoleap 只證明身分,能不能用由 omixor 的使用者名單決定),再重送一次
//
// IssueStore 的介面是同步的(畫面與 MCP 工具直接拿回傳值),所以這裡用同步請求(有逾時)。
// 主執行緒不要直接呼叫同步請求:定時重讀、切換組織、MCP 轉送、按鈕等改用 run { … }(在背景執行緒做同步請求,
// 呼叫端 await 後回到 MainActor 更新畫面)。

struct IssueServerConfig: Equatable {
    var url: URL
    var token: String?
    /// 不是 nil:這台 server 現在不能用(例如本機守護服務還沒啟動),每個請求直接回這個錯誤
    var unavailable: String? = nil
    /// 這是 App 啟動的本機守護服務(LocalCore.swift;本機模式才會同步組織清單與匯入舊資料)
    var localCore = false

    static let defaultsKey = "issueServerURL"
    /// 換 omixor token 時用來證明身分的連線(evoleap 正式站)
    static let evoleapConnection = "evoleap"
    /// omixor 自己的 token 存在鑰匙圈的這個名稱
    static let omixorTokenId = "omixor-issues"

    /// 測試用:非 nil 時取代環境變數與設定
    nonisolated(unsafe) static var testOverride: IssueServerConfig?

    /// 目前連的 core server(唯一決定「連哪台、用哪把 token」的地方;各 Store 與 MCP 轉送都從這裡拿):
    ///   遠端模式:設定裡的 omixor server(token 用鑰匙圈的 omixor token,或開發用的 OMIXOR_SERVER_TOKEN)
    ///   本機模式:App 啟動的本機守護服務(LocalCore.swift,127.0.0.1 + 這次啟動的 token);還沒好時是 unavailable 的連線
    static var current: IssueServerConfig {
        if let o = testOverride { return o }
        if let srv = OmixorMode.remoteServer {
            let tok = OmixorEnv.get("OMIXOR_SERVER_TOKEN", legacy: "OPS_ISSUE_SERVER_TOKEN")
            return IssueServerConfig(url: srv.url, token: tok)
        }
        return LocalCore.shared.config ?? LocalCore.shared.unavailableConfig
    }
}

/// omixor token 自動換發的協調(每台伺服器一份,執行緒安全):
///   單一飛行:同一台伺服器同時只有一個 exchange(lock(for:)),其他請求等它做完再用它換到的 token
///   已登出:使用者登出後(markLoggedOut)任何背景請求遇到 401 或沒有 token 都不自動換,直到重新明確登入(markLoggedIn)
enum OmixorTokenGate {
    private static let guardLock = NSLock()
    nonisolated(unsafe) private static var locks: [String: NSLock] = [:]
    nonisolated(unsafe) private static var loggedOut: Set<String> = []
    nonisolated(unsafe) private static var epochs: [String: Int] = [:]

    private static func key(_ server: URL) -> String { OmixorMode.tokenScope(for: server) }

    /// 這台伺服器換 token 用的鎖
    static func lock(for server: URL) -> NSLock {
        guardLock.lock(); defer { guardLock.unlock() }
        if let l = locks[key(server)] { return l }
        let l = NSLock()
        locks[key(server)] = l
        return l
    }

    static func isLoggedOut(_ server: URL) -> Bool {
        guardLock.lock(); defer { guardLock.unlock() }
        return loggedOut.contains(key(server))
    }

    /// 登出、登入都會加一;換 token 前後比對,中間有登出就作廢換到的 token
    static func epoch(_ server: URL) -> Int {
        guardLock.lock(); defer { guardLock.unlock() }
        return epochs[key(server), default: 0]
    }

    static func markLoggedOut(_ server: URL) {
        guardLock.lock(); defer { guardLock.unlock() }
        loggedOut.insert(key(server))
        epochs[key(server), default: 0] += 1
    }

    static func markLoggedIn(_ server: URL) {
        guardLock.lock(); defer { guardLock.unlock() }
        if loggedOut.remove(key(server)) != nil { epochs[key(server), default: 0] += 1 }
    }
}

struct IssueRemoteError: LocalizedError {
    var message: String
    /// 伺服器回的 HTTP 狀態(0 = 沒連上或不是伺服器回的錯)
    var status = 0
    /// 回應不是 core 的 JSON 錯誤({"error": …}),例如路由不存在時 Go 的「404 page not found」
    /// (status 404 且 noRoute = 這台伺服器沒有這個端點:舊版 server,或沒編進那個選用模組)
    var noRoute = false
    var errorDescription: String? { message }
}

/// omixor server(core)/api/orgs/<組織>/issues 的用戶端。
/// 可以在任何執行緒用(run 會在背景執行緒呼叫同步方法);可變的狀態(留言 id 對照)有鎖保護
final class IssueRemote: @unchecked Sendable {
    let config: IssueServerConfig
    let session: URLSession
    var timeout: TimeInterval = 8

    /// 測試用:攔截請求的 URLProtocol
    nonisolated(unsafe) static var protocolClasses: [AnyClass] = []

    /// evoleap 連線 token 所在的組織前綴(建立用戶端時就決定,不在背景執行緒讀 TokenVault.scope;
    /// 也免得請求途中切換組織,換 token 時拿到另一個組織的 evoleap 登入)
    let evoleapScope: String

    /// scope:evoleap 連線 token 的組織前綴;沒給就用建立當下的 TokenVault.scope(請在主執行緒建立,或明確傳入)
    init(config: IssueServerConfig, scope: String? = nil) {
        self.config = config
        self.evoleapScope = scope ?? TokenVault.scope
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 8
        if !Self.protocolClasses.isEmpty { c.protocolClasses = Self.protocolClasses }
        session = URLSession(configuration: c)
    }

    /// 這個用戶端還能不能沿用:連線設定相同、而且 evoleap 的組織前綴還是目前的(切換組織後要換新的)。在主執行緒呼叫
    func matches(_ cfg: IssueServerConfig) -> Bool { config == cfg && evoleapScope == TokenVault.scope }

    static func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }

    static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    private func url(_ org: String, _ rest: [String]) -> URL {
        // 核心的路徑:/api/orgs/<組織>/issues[/<id>[/delete]](伺服器也還接受舊的 /api/issues/<組織>)
        orgURL(org, ["issues"] + rest)
    }

    /// /api/orgs/<組織>/<rest…>;遠端組織在 app 裡的 id 有 "~" 前綴,送給伺服器時去掉
    func orgURL(_ org: String, _ rest: [String]) -> URL {
        var u = config.url.appendingPathComponent("api").appendingPathComponent("orgs")
            .appendingPathComponent(OmixorMode.serverOrgId(org))
        for r in rest { u = u.appendingPathComponent(r) }
        return u
    }

    /// 同步送出一個請求,回傳 (HTTP 狀態, 內容)
    func perform(_ req: URLRequest) throws -> (Int, Data) {
        if let m = config.unavailable { throw IssueRemoteError(message: m) }
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
    private func scope(_ id: String) -> String { id == IssueServerConfig.omixorTokenId ? OmixorMode.tokenScope(for: config.url) : evoleapScope }

    private func vaultGet(_ id: String) -> String? {
        if let v = Self.testVault { return v[id] }
        if id == IssueServerConfig.omixorTokenId { return OmixorAuth.token(server: config.url)?.accessToken } // 含第一個遠端沿用舊格式
        return TokenVault.tokens(id, scope: scope(id))?.accessToken
    }

    private func vaultSet(_ id: String, _ token: String?, expires: Date?) {
        if Self.testVault != nil { Self.testVault?[id] = token; return }
        TokenVault.setTokens(token.map { TokenSet(accessToken: $0, expiresAt: expires, tokenType: "Bearer") }, id, scope: scope(id))
    }

    /// 用 evoleap 連線的登入,向 omixor 換一個 omixor 自己的 token(不存;由 freshToken 決定存不存)
    private func exchangeToken() throws -> (String, Date?) {
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
        return (tok, (obj?["expiresAt"] as? String).flatMap { ISO8601DateFormatter().date(from: $0) })
    }

    /// 撤銷一把 omixor token(同步;失敗就算了)
    private func revoke(_ token: String) {
        var req = URLRequest(url: config.url.appendingPathComponent("api/auth/logout"), timeoutInterval: timeout)
        req.httpMethod = "POST"
        req.httpBody = Data("{}".utf8)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        _ = try? perform(req)
    }

    private var loggedOutError: IssueRemoteError {
        IssueRemoteError(message: "已登出 omixor:請從左下角的帳號選單重新連接遠端 omixor", status: 401)
    }

    /// 取得一把新的 omixor token(單一飛行):同一台伺服器同時只有一個在換,其他排隊;輪到時先看別人是否已經換好,
    /// 換好了就直接用。stale 是剛被拒絕(401)的 token(nil = 本來就沒有)。
    /// 使用者登出後(OmixorTokenGate.markLoggedOut)不自動換,直接失敗;換到一半遇到登出,剛換到的 token 撤銷、不存
    private func freshToken(replacing stale: String?) throws -> String {
        let server = config.url
        if OmixorTokenGate.isLoggedOut(server) { throw loggedOutError }
        let l = OmixorTokenGate.lock(for: server)
        l.lock()
        defer { l.unlock() }
        if OmixorTokenGate.isLoggedOut(server) { throw loggedOutError }
        let current = vaultGet(IssueServerConfig.omixorTokenId)
        if let current, current != stale { return current }   // 排隊期間別人已經換好(或本來就有)
        if stale != nil, current != nil { vaultSet(IssueServerConfig.omixorTokenId, nil, expires: nil) }  // 被拒絕的舊 token 清掉
        let epoch = OmixorTokenGate.epoch(server)
        let (tok, exp) = try exchangeToken()
        if OmixorTokenGate.epoch(server) != epoch || OmixorTokenGate.isLoggedOut(server) {
            // 換的期間使用者登出了:不能偷偷重新登入
            revoke(tok)
            throw loggedOutError
        }
        vaultSet(IssueServerConfig.omixorTokenId, tok, expires: exp)
        return tok
    }

    /// 帶 omixor token 送出(同步):沒設固定 token 時用鑰匙圈的;沒有或被拒絕(401)就換一個新的再送一次(見 freshToken)
    private func authorized(_ make: (String?) -> URLRequest) throws -> (Int, Data) {
        if let m = config.unavailable { throw IssueRemoteError(message: m) }
        if let fixed = config.token { return try perform(make(fixed)) }
        let saved = vaultGet(IssueServerConfig.omixorTokenId)
        let first = try saved ?? freshToken(replacing: nil)
        var (status, data) = try perform(make(first))
        if status == 401, saved != nil {
            (status, data) = try perform(make(try freshToken(replacing: first)))
        }
        return (status, data)
    }

    /// 送出請求、等回應(同步),回傳 {"result": …} 的 result(JSON)。
    func send(_ method: String, _ url: URL, _ body: Data?) throws -> Data {
        let (status, data) = try authorized { token in
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
        let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        if let e = obj?["error"] as? String { throw IssueRemoteError(message: e, status: status) }
        guard (200..<300).contains(status), let obj, let result = obj["result"] else {
            throw IssueRemoteError(message: "Issue 伺服器回應不對(HTTP \(status))", status: (200..<300).contains(status) ? 0 : status,
                                   noRoute: obj == nil && status == 404)
        }
        return try JSONSerialization.data(withJSONObject: result, options: .fragmentsAllowed)
    }

    /// 伺服器的管理用 MCP(/mcp/org/<組織>):送一則 JSON-RPC,回傳 result(權限同登入的帳號)。
    /// App 內建 MCP 連 server 時,核心工具(Issue、角色、文件櫃、組織鏈、頻道)轉給這裡,工具定義與行為只有核心一份
    func mcp(org: String, method: String, params: [String: Any] = [:]) throws -> [String: Any] {
        let url = config.url.appendingPathComponent("mcp").appendingPathComponent("org")
            .appendingPathComponent(OmixorMode.serverOrgId(org))
        let body = try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": 1, "method": method, "params": params])
        let (status, data) = try authorized { token in
            var req = URLRequest(url: url, timeoutInterval: timeout)
            req.httpMethod = "POST"
            req.httpBody = body
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
            if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
            return req
        }
        guard (200..<300).contains(status) else { throw IssueRemoteError(message: "omixor server 的 MCP 回應不對(HTTP \(status))") }
        let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        if let e = obj?["error"] as? [String: Any] { throw IssueRemoteError(message: e["message"] as? String ?? "MCP 錯誤") }
        guard let r = obj?["result"] as? [String: Any] else { throw IssueRemoteError(message: "omixor server 的 MCP 回應不對") }
        return r
    }

    func json(_ dict: [String: Any]) throws -> Data { try JSONSerialization.data(withJSONObject: dict) }

    /// 送 JSON、回傳 result(已解析)
    func sendJSON(_ method: String, _ url: URL, _ body: [String: Any]?) throws -> Any {
        let data = try send(method, url, try body.map { try json($0) })
        return try JSONSerialization.jsonObject(with: data, options: .fragmentsAllowed)
    }

    /// 留言 id 不是 UUID 的(例如 Go 版從 Jira 匯入的 jira-123):換成固定的 UUID 給 app 用(留言不會整份送回去,
    /// 新增留言走 addComment,所以伺服器上的 id 不會被改掉)
    private func uuidFor(_ id: String) -> String {
        var b = Array(SHA256.hash(data: Data(id.utf8)).prefix(16))
        b[6] = b[6] & 0x0f | 0x50
        b[8] = b[8] & 0x3f | 0x80
        return UUID(uuid: (b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])).uuidString
    }

    func fixComments(_ issue: Any) -> Any {
        guard var d = issue as? [String: Any], let cs = d["comments"] as? [[String: Any]] else { return issue }
        d["comments"] = cs.map { c -> [String: Any] in
            var c = c
            if let id = c["id"] as? String, UUID(uuidString: id) == nil { c["id"] = uuidFor(id) }
            return c
        }
        return d
    }

    private func decodeIssues(_ data: Data) throws -> [Issue] {
        // result 是 null(Go 的 nil slice)時是空列表
        let arr = (try JSONSerialization.jsonObject(with: data, options: .fragmentsAllowed) as? [Any] ?? []).map(fixComments)
        return try Self.decoder().decodeLossyList(Issue.self, from: try JSONSerialization.data(withJSONObject: arr))
    }

    private func decodeIssue(_ data: Data) throws -> Issue {
        let obj = fixComments(try JSONSerialization.jsonObject(with: data))
        return try Self.decoder().decode(Issue.self, from: try JSONSerialization.data(withJSONObject: obj))
    }

    /// repo:只取標記這個倉庫的(伺服器端 ?repo= 篩選,規則同 IssueRepo.matches;nil 或空 = 全部)
    func list(org: String, repo: String? = nil) throws -> [Issue] {
        var u = url(org, [])
        if let r = repo?.trimmingCharacters(in: .whitespacesAndNewlines), !r.isEmpty,
           var c = URLComponents(url: u, resolvingAgainstBaseURL: false) {
            c.queryItems = [URLQueryItem(name: "repo", value: r)]
            u = c.url ?? u
        }
        return try decodeIssues(try send("GET", u, nil))
    }

    /// 依條件篩選、排序的 Issue(GET …/issues?q=&status=…;規則只在 core 的 model/issue_query.go,App 只顯示結果)
    func query(org: String, _ q: IssueQuery) throws -> [Issue] {
        try decodeIssues(try send("GET", Self.withQuery(url(org, []), q.queryItems), nil))
    }

    /// 看板(GET …/issues/board):core 分好的三欄(每欄已排序)與篩選選單用的標籤
    func board(org: String, _ q: IssueQuery) throws -> IssueBoardResult {
        var q = q
        q.status = ""; q.sort = ""
        let data = try send("GET", Self.withQuery(url(org, ["board"]), q.queryItems), nil)
        return try decodeBoard(data)
    }

    /// 看板回應的解碼(欄裡的 Issue 和 list 一樣先把不是 UUID 的留言 id 換掉)
    func decodeBoard(_ data: Data) throws -> IssueBoardResult {
        guard var d = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw IssueRemoteError(message: "看板回應不對")
        }
        if let cols = d["columns"] as? [[String: Any]] {
            d["columns"] = cols.map { c -> [String: Any] in
                var c = c
                if let l = c["issues"] as? [Any] { c["issues"] = l.map(fixComments) }
                return c
            }
        }
        return try Self.decoder().decode(IssueBoardResult.self, from: try JSONSerialization.data(withJSONObject: d))
    }

    static func withQuery(_ u: URL, _ items: [URLQueryItem]) -> URL {
        guard !items.isEmpty, var c = URLComponents(url: u, resolvingAgainstBaseURL: false) else { return u }
        c.queryItems = items
        // URLComponents 不會把 + 編碼,伺服器(Go 的 url.Values)會把 + 當成空白
        c.percentEncodedQuery = c.percentEncodedQuery?.replacingOccurrences(of: "+", with: "%2B")
        return c.url ?? u
    }

    /// assignee:指派給的角色 id(nil 或空 = 不指派);伺服器會自動替該角色排任務,角色不存在回 400。
    /// repo:所屬倉庫(nil 或空 = 不標記;格式不合格伺服器回 400)
    func create(org: String, title: String, body: String, priority: IssuePriority, labels: [String],
                source: String, link: String, repo: String? = nil, author: String, assignee: String? = nil) throws -> Issue {
        var d: [String: Any] = ["title": title, "body": body, "priority": priority.rawValue, "labels": labels,
                                "source": source, "link": link, "author": author]
        if let a = assignee, !a.isEmpty { d["assignee"] = a }
        if let r = repo, !r.isEmpty { d["repo"] = r }
        return try decodeIssue(try send("POST", url(org, []), try json(d)))
    }

    /// 修改:送出可以改的欄位(整個取代),伺服器處理更新時間、關閉時間與動態牆。
    /// 留言不送(整份送回會蓋掉別人同時留的言):新增留言用 addComment。
    /// assignee 只在 sendAssignee 時送(空字串 = 取消指派):伺服器每收到一次 assignee 就會同步任務,
    /// 前一筆任務已完成時會重新排一筆,所以指派沒變就不能送
    func update(org: String, _ i: Issue, author: String, sendAssignee: Bool = false) throws -> Issue {
        let enc = try Self.encoder().encode(i)
        guard var d = try JSONSerialization.jsonObject(with: enc) as? [String: Any] else {
            throw IssueRemoteError(message: "Issue 編碼失敗")
        }
        for k in ["id", "createdAt", "createdBy", "updatedAt", "closedAt", "assignee", "comments"] { d.removeValue(forKey: k) }
        if sendAssignee { d["assignee"] = i.assignee ?? "" }
        // repo 一律送(空字串 = 清除標記);沒變時伺服器存回同一個值
        d["repo"] = i.repo ?? ""
        d["author"] = author
        return try decodeIssue(try send("POST", url(org, [i.id]), try json(d)))
    }

    /// 新增一則留言(POST …/issues/<id>/comments):伺服器只附加這一則,同時多人留言不會互相蓋掉;回傳新增後的 Issue。
    /// author 一般成員與角色憑證一律記成登入的人(給了別人靜默忽略);伺服器管理員只能代角色或系統署名,給使用者或找不到的名字也是記成登入的人
    func addComment(org: String, id: String, text: String, author: String) throws -> Issue {
        try decodeIssue(try send("POST", url(org, [id, "comments"]), try json(["text": text, "author": author])))
    }

    func delete(org: String, id: String) throws {
        _ = try send("POST", url(org, [id, "delete"]), try json([:]))
    }
}

// MARK: - Issue 的查詢與看板(規則在 core)

/// GET /api/orgs/<組織>/issues 的 query 參數(同 core 的 model.IssueQuery);空字串 = 不限
struct IssueQuery: Equatable, Hashable {
    /// 搜尋:標題、內文、留言、編號、標籤;空白分隔的每個字都要有,不分大小寫(core 處理)
    var q = ""
    /// open、closed、all;"" = 全部
    var status = ""
    var priority = ""
    var label = ""
    /// 倉庫;IssueQuery.none = 未標記
    var repo = ""
    /// 角色 id;IssueQuery.none = 未指派
    var assignee = ""
    /// priority、status、updated;"" = 不排序(存放處的順序)
    var sort = ""

    /// 篩選「未標記/未指派」用的值,同 core 的 model.IssueFilterNone
    static let none = "(none)"

    var queryItems: [URLQueryItem] {
        [("q", q), ("status", status), ("priority", priority), ("label", label), ("repo", repo), ("assignee", assignee), ("sort", sort)]
            .filter { !$0.1.isEmpty }.map { URLQueryItem(name: $0.0, value: $0.1) }
    }
}

/// GET /api/orgs/<組織>/issues/board 的回應(同 core 的 model.IssueBoard)
struct IssueBoardResult: Decodable, Equatable {
    struct Column: Decodable, Equatable, Identifiable {
        /// todo、doing、done
        var key: String
        /// 待處理、進行中、已完成
        var title: String
        /// 拖進這一欄要套的狀態;和 Issue 現在的相同(或 nil)就不用改
        var dropStatus: IssueStatus?
        /// 這一欄的 Issue(core 已依優先度 → 最近更新 → 編號排好)
        var issues: [Issue]
        var id: String { key }

        /// 把 issue 拖進這一欄要改成的狀態;nil = 不用改(同一欄、或 待處理 ↔ 進行中)
        func status(forDropping issue: Issue) -> IssueStatus? {
            guard let s = dropStatus, s != issue.status else { return nil }
            return s
        }
    }
    var columns: [Column]
    /// 全部 Issue(篩選前)用到的標籤,篩選選單用
    var labels: [String]
    /// 篩選前、後的 Issue 數
    var total: Int
    var matched: Int

    /// 這張 Issue 現在在哪一欄
    func column(of id: String) -> Column? { columns.first { $0.issues.contains { $0.id == id } } }

    /// 拖曳中的 Issue 拖到某一欄的結果(拖曳游標、高亮、提示用;只看欄的 dropStatus 和 Issue 現在的狀態)
    enum DropCheck: Equatable {
        /// 可以放:放下後改成這個狀態
        case move(IssueStatus)
        /// 拖回原本那一欄:不能放,也不用提示
        case sameColumn
        /// 不能放、要提示(dropStatus 和 Issue 現在的狀態相同,例如 待處理 ↔ 進行中)
        case blocked
        /// 看板上找不到這張 Issue(或沒有在拖曳):不能放
        case unknown

        var canDrop: Bool { if case .move = self { return true } else { return false } }
    }

    /// id 這張 Issue 拖到 col 的結果
    func dropCheck(dragging id: String?, onto col: Column) -> DropCheck {
        guard let id, let from = column(of: id), let issue = from.issues.first(where: { $0.id == id }) else { return .unknown }
        if let s = col.status(forDropping: issue) { return .move(s) }
        return from.key == col.key ? .sameColumn : .blocked
    }
}

extension IssueBoardResult.Column {
    private enum LossyKeys: String, CodingKey { case key, title, dropStatus, issues }

    // 寬鬆解碼(LossyDecoding.swift):key 必填(缺了略過這一欄);issues 是 null → [],缺 id 的 Issue 只略過那一筆;
    // 不認得的 dropStatus → nil(拖進來不改狀態)
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: LossyKeys.self)
        key = try c.decode(String.self, forKey: .key)
        title = c.lossy(.title, default: key)
        dropStatus = c.lossyOptional(String.self, .dropStatus).flatMap(IssueStatus.init(rawValue:))
        issues = c.lossyArray(Issue.self, .issues)
    }
}

extension IssueBoardResult {
    private enum LossyKeys: String, CodingKey { case columns, labels, total, matched }

    // 寬鬆解碼:columns、labels 是 null → [],壞掉的欄略過;total、matched 缺時用各欄的 Issue 數
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: LossyKeys.self)
        columns = c.lossyArray(Column.self, .columns)
        labels = c.lossyArray(String.self, .labels)
        let count = columns.reduce(0) { $0 + $1.issues.count }
        matched = c.lossy(.matched, default: count)
        total = c.lossy(.total, default: matched)
    }
}

extension IssueStore {
    /// 在背景向 core 查詢目前組織的 Issue(不擋主執行緒);回傳時組織已經換了就丟出 CancellationError
    func query(_ q: IssueQuery) async throws -> [Issue] {
        let org = app.currentOrgId
        let l = try await remote.run { try $0.query(org: org, q) }
        guard app.currentOrgId == org else { throw CancellationError() }
        return l
    }

    /// 在背景向 core 讀看板(不擋主執行緒);回傳時組織已經換了就丟出 CancellationError
    func board(_ q: IssueQuery) async throws -> IssueBoardResult {
        let org = app.currentOrgId
        let b = try await remote.run { try $0.board(org: org, q) }
        guard app.currentOrgId == org else { throw CancellationError() }
        return b
    }
}

// MARK: - 背景執行(不擋主執行緒)

extension IssueRemote {
    /// 同步請求放在這裡跑(可同時跑多個;每個請求自己有逾時)
    private static let background = DispatchQueue(label: "omixor.core-remote", qos: .userInitiated, attributes: .concurrent)

    /// 在背景執行緒做同步請求,回傳結果(呼叫端 await 後回到原本的 actor,例如 MainActor 再更新畫面)
    func run<T>(_ work: @escaping (IssueRemote) throws -> T) async throws -> T {
        try await withCheckedThrowingContinuation { (cont: CheckedContinuation<T, Error>) in
            Self.background.async {
                do { cont.resume(returning: try work(self)) } catch { cont.resume(throwing: error) }
            }
        }
    }

    /// 同 run,但連線用指定的設定(一次性的用戶端)
    static func run<T>(_ config: IssueServerConfig, _ work: @escaping (IssueRemote) throws -> T) async throws -> T {
        try await IssueRemote(config: config).run(work)
    }
}
