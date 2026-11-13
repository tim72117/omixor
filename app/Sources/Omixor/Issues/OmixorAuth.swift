import Foundation

// 在 app 裡登入遠端 omixor(和 omixor CLI 的 omixor login 一樣):
//   1. 瀏覽器開 evoleap 的核准頁(ApprovalConnector,和 evoleap 連線同一套),拿到一次性的 evoleap CLI token
//   2. 交給 omixor server 的 POST /api/auth/evoleap,換 omixor 自己的 token(omx_…)
//   3. 不管換成功或失敗,立刻撤銷那把 evoleap token(只用來證明一次身分)
//   4. omixor token 存在鑰匙圈的 remote/<伺服器>/omixor-issues(每台伺服器各一把、不分組織;IssueRemote 讀寫 Issue 時帶它)
// evoleap 只證明身分;能不能用、是管理員還是成員,由 omixor 的使用者名單決定。
// 什麼時候登入、登入哪一台,見 OmixorMode.swift 與 AppState.connect(Features/Login/LoginGate.swift)。

struct OmixorAccount: Equatable {
    var email: String
    var name: String = ""
    var role: String
    var device: String
    var expiresAt: Date?
}

/// omixor server 上的組織(GET /api/orgs;myRole 是我在這個組織的角色)
struct ServerOrg: Codable, Equatable, Identifiable {
    var id: String
    var name: String
    var symbol: String?
    var color: String?
    var note: String?
    var createdAt: String?
    var myRole: String?

    /// 換成 app 的組織(id 加這台伺服器的前綴 "~<伺服器>~";圖示、顏色沒填的用預設)
    func organization(server: URL) -> Organization {
        Organization(id: OmixorMode.orgPrefix(for: server) + id, name: name.isEmpty ? id : name,
                     symbol: (symbol ?? "").isEmpty ? "building.2" : symbol!, color: (color ?? "").isEmpty ? "blue" : color!,
                     note: note ?? "", createdAt: OmixorAuth.date(createdAt) ?? .distantPast)
    }
}

/// 遠端 omixor 的登入狀態
enum OmixorLoginState: Equatable {
    case checking                    // 正在確認存著的登入
    case needLogin                   // 沒登入或登入失效
    case unreachable(String)         // 有存著的登入,但連不到伺服器
    case loggedIn(OmixorAccount)
    case skipped                     // 本機模式(不用登入)

    var account: OmixorAccount? {
        if case .loggedIn(let a) = self { return a }
        return nil
    }
}

enum OmixorAuth {
    static var tokenId: String { IssueServerConfig.omixorTokenId }

    /// 這台伺服器存著的 omixor token(每台伺服器各一把,不分組織);第一個遠端沒有時沿用舊格式的(複製過去,舊的不刪)
    static func token(server: URL) -> TokenSet? {
        if let t = TokenVault.tokens(tokenId, scope: OmixorMode.tokenScope(for: server)) { return t }
        if OmixorMode.isLegacyServer(server), let old = TokenVault.tokens(tokenId, scope: OmixorMode.legacyTokenScope) {
            setToken(old, server: server)
            return old
        }
        return nil
    }

    /// 存這台伺服器的 omixor token(nil = 刪掉)。回傳是否真的寫進鑰匙圈;寫不進去時這次執行仍用新的值(見 TokenVault.set),
    /// 不會再讀到鑰匙圈裡已撤銷的舊 token
    @discardableResult
    static func setToken(_ t: TokenSet?, server: URL) -> Bool { TokenVault.setTokens(t, tokenId, scope: OmixorMode.tokenScope(for: server)) }

    /// 更舊的版本把 omixor token 存在預設組織底下:搬到舊格式的遠端位置(之後再由 token(server:) 搬給第一個遠端;只搬一次)
    static func migrateLegacyToken() {
        guard TokenVault.tokens(tokenId, scope: OmixorMode.legacyTokenScope) == nil, let old = TokenVault.tokens(tokenId, scope: "") else { return }
        TokenVault.setTokens(old, tokenId, scope: OmixorMode.legacyTokenScope)
        TokenVault.setTokens(nil, tokenId, scope: "")
    }

    /// 確認存著的登入
    static func check(server: URL) async -> OmixorLoginState {
        guard let t = token(server: server) else { return .needLogin }
        do {
            let r = try await ConnHTTP.send("GET", server.appendingPathComponent("api/auth/me"),
                                            headers: ["Authorization": "Bearer \(t.accessToken)"])
            if r.status == 401 || r.status == 403 { return .needLogin }
            guard r.ok, let a = account(r.json) else { return .unreachable("伺服器回應不對(HTTP \(r.status))") }
            return .loggedIn(a)
        } catch {
            return .unreachable(error.localizedDescription)
        }
    }

    private static func account(_ json: Any) -> OmixorAccount? {
        guard let obj = json as? [String: Any], let user = obj["user"] as? [String: Any] else { return nil }
        let token = obj["token"] as? [String: Any]
        return OmixorAccount(email: user["email"] as? String ?? "", name: user["name"] as? String ?? "", role: user["role"] as? String ?? "",
                             device: token?["device"] as? String ?? "", expiresAt: date(token?["expiresAt"] as? String))
    }

    /// 用來證明身分的 evoleap 連線:用 app 裡設定的(可能改過網址),沒有就用預設的正式站
    @MainActor
    static func evoleapConnection(_ app: AppState) -> Connection? {
        app.connections.first { $0.id == IssueServerConfig.evoleapConnection }
            ?? Connection.defaults.first { $0.id == IssueServerConfig.evoleapConnection }
    }

    static var deviceName: String { "omixor app(\(Host.current().localizedName ?? "Mac"))" }

    private static func decoder() -> ISO8601DateFormatter {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }

    /// 伺服器回的時間可能有小數秒,也可能沒有
    static func date(_ s: String?) -> Date? {
        guard let s else { return nil }
        return decoder().date(from: s) ?? ISO8601DateFormatter().date(from: s)
    }

    /// 瀏覽器登入:回傳登入的帳號
    @MainActor
    static func login(_ app: AppState, server: URL) async throws -> OmixorAccount {
        guard let evo = evoleapConnection(app) else { throw ConnectionError("找不到 evoleap 連線設定") }
        let evoToken = try await ApprovalConnector.login(evo)
        return try await exchange(evo, evoToken, server: server)
    }

    /// 撤銷 evoleap token。另開不受取消影響的 Task:使用者按「取消」時登入的 Task 已被取消,
    /// 直接在裡面送請求會立刻失敗,token 就留在 evoleap 沒撤銷
    private static func revokeEvoleap(_ evo: Connection, _ t: TokenSet) async {
        await Task.detached { await ApprovalConnector.logout(evo, t) }.value
    }

    /// 拿 evoleap token 換 omixor token,換完(成功或失敗)撤銷 evoleap token
    @MainActor
    static func exchange(_ evo: Connection, _ evoToken: TokenSet, server: URL) async throws -> OmixorAccount {
        let r: ConnHTTP.Response
        do {
            r = try await ConnHTTP.json("POST", server.appendingPathComponent("api/auth/evoleap"),
                                        ["token": evoToken.accessToken, "device": deviceName])
        } catch {
            await revokeEvoleap(evo, evoToken)
            throw error
        }
        // evoleap 的 token 只用來證明一次身分:換成功或失敗都撤銷
        await revokeEvoleap(evo, evoToken)
        let obj = r.json as? [String: Any]
        guard r.ok, let tok = obj?["token"] as? String, !tok.isEmpty else {
            throw ConnectionError("omixor 拒絕登入:\(ConnHTTP.errorMessage(r))", status: r.status)
        }
        let exp = date(obj?["expiresAt"] as? String)
        // 寫不進鑰匙圈(舊的 token 刪不掉)時這次執行仍用新的 token;原因在 TokenVault.lastWriteError,登入畫面會提示
        if !setToken(TokenSet(accessToken: tok, expiresAt: exp, tokenType: "Bearer"), server: server) {
            keychainWarning = TokenVault.lastWriteError
        }
        let user = obj?["user"] as? [String: Any]
        return OmixorAccount(email: user?["email"] as? String ?? "", name: user?["name"] as? String ?? "", role: user?["role"] as? String ?? "",
                             device: deviceName, expiresAt: exp)
    }

    /// 上次登入時新的 omixor token 寫不進鑰匙圈的原因(畫面提示一次就清掉)
    nonisolated(unsafe) private static var keychainWarning: String?
    static func takeKeychainWarning() -> String? {
        defer { keychainWarning = nil }
        return keychainWarning
    }

    /// 目前登入的帳號;沒登入或 token 失效回傳 nil
    static func me(server: URL) async -> OmixorAccount? {
        if case .loggedIn(let a) = await check(server: server) { return a }
        return nil
    }

    private static func authed(_ server: URL, _ method: String, _ url: URL, _ body: [String: Any]? = nil) async throws -> ConnHTTP.Response {
        guard let t = token(server: server) else { throw ConnectionError("還沒登入 omixor") }
        let h = ["Authorization": "Bearer \(t.accessToken)"]
        if let body { return try await ConnHTTP.json(method, url, body, headers: h) }
        return try await ConnHTTP.send(method, url, headers: h)
    }

    /// 讀 {"result": …};失敗時丟出看得懂的原因(HTTP 狀態與伺服器的訊息、或哪個欄位解不開)
    private static func result<T: Decodable>(_ r: ConnHTTP.Response, _ type: T.Type) throws -> T {
        guard r.ok else {
            let msg = r.status == 401 ? "omixor 的登入無效或已撤銷,請重新登入(\(ConnHTTP.errorMessage(r)))" : ConnHTTP.errorMessage(r)
            throw ConnectionError("\(msg)(HTTP \(r.status))", status: r.status)
        }
        guard let obj = r.json as? [String: Any], let res = obj["result"] else {
            throw ConnectionError("伺服器回應的格式不對(HTTP \(r.status)):\(String(decoding: r.data.prefix(200), as: UTF8.self))", status: r.status)
        }
        do {
            return try JSONDecoder().decode(T.self, from: try JSONSerialization.data(withJSONObject: res, options: .fragmentsAllowed))
        } catch let e as DecodingError {
            throw ConnectionError("伺服器回應的資料解不開:\(describe(e))", status: r.status)
        }
    }

    private static func describe(_ e: DecodingError) -> String {
        func path(_ c: DecodingError.Context) -> String {
            let p = c.codingPath.map { $0.intValue.map { "[\($0)]" } ?? $0.stringValue }.joined(separator: ".")
            return p.isEmpty ? c.debugDescription : "\(p):\(c.debugDescription)"
        }
        switch e {
        case .typeMismatch(_, let c), .valueNotFound(_, let c), .dataCorrupted(let c): return path(c)
        case .keyNotFound(let k, let c): return "少了欄位 \(k.stringValue)(\(path(c)))"
        @unknown default: return e.localizedDescription
        }
    }

    /// 向伺服器取得我能用的組織;失敗回傳 nil(畫面照用快取)
    static func fetchOrgs(server: URL) async -> [ServerOrg]? {
        try? await listOrgs(server: server)
    }

    /// 同 fetchOrgs,但失敗時丟出原因(畫面要顯示為什麼讀不到)
    static func listOrgs(server: URL) async throws -> [ServerOrg] {
        let r = try await authed(server, "GET", server.appendingPathComponent("api/orgs"))
        // Go 的空切片可能編成 null:當作沒有組織
        if r.ok, let obj = r.json as? [String: Any], obj["result"] is NSNull { return [] }
        return try result(r, [ServerOrg].self)
    }

    /// 在伺服器建立組織(要伺服器管理員)
    static func createOrg(server: URL, id: String, name: String, symbol: String, color: String, note: String = "") async throws -> ServerOrg {
        let r = try await authed(server, "POST", server.appendingPathComponent("api/orgs"),
                                 ["id": id, "name": name, "symbol": symbol, "color": color, "note": note])
        return try result(r, ServerOrg.self)
    }

    /// 修改伺服器上的組織名稱、圖示、顏色、備註(要伺服器管理員)
    static func updateOrg(server: URL, _ org: Organization) async throws -> ServerOrg {
        let id = OmixorMode.serverOrgId(org.id)
        let r = try await authed(server, "PUT", server.appendingPathComponent("api/orgs").appendingPathComponent(id),
                                 ["name": org.name, "symbol": org.symbol, "color": org.color, "note": org.note])
        return try result(r, ServerOrg.self)
    }

    /// 登出:伺服器上撤銷這個 token,再從鑰匙圈刪掉(伺服器連不上也照樣刪)
    static func logout(server: URL) async {
        if token(server: server) != nil {
            _ = try? await authed(server, "POST", server.appendingPathComponent("api/auth/logout"), [:])
        }
        setToken(nil, server: server)
        if OmixorMode.isLegacyServer(server) { TokenVault.setTokens(nil, tokenId, scope: OmixorMode.legacyTokenScope) } // 免得又被沿用
    }
}
