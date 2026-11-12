import Foundation

// 在 app 裡登入遠端 omixor(和 omixor CLI 的 omixor login 一樣):
//   1. 瀏覽器開 evoleap 的核准頁(ApprovalConnector,和 evoleap 連線同一套),拿到一次性的 evoleap CLI token
//   2. 交給 omixor server 的 POST /api/auth/evoleap,換 omixor 自己的 token(omx_…)
//   3. 不管換成功或失敗,立刻撤銷那把 evoleap token(只用來證明一次身分)
//   4. omixor token 存在鑰匙圈的 remote/omixor-issues(不分組織;IssueRemote 讀寫 Issue 時帶它)
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

    /// 換成 app 的組織(id 加遠端前綴;圖示、顏色沒填的用預設)
    var organization: Organization {
        Organization(id: OmixorMode.remoteOrgPrefix + id, name: name.isEmpty ? id : name,
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

    /// 存著的 omixor token(遠端共用,不分組織)
    static var token: TokenSet? { TokenVault.tokens(tokenId, scope: OmixorMode.tokenScope) }
    static func setToken(_ t: TokenSet?) { TokenVault.setTokens(t, tokenId, scope: OmixorMode.tokenScope) }

    /// 舊版把 omixor token 存在預設組織底下:搬到不分組織的位置(只搬一次)
    static func migrateLegacyToken() {
        guard token == nil, let old = TokenVault.tokens(tokenId, scope: "") else { return }
        setToken(old)
        TokenVault.setTokens(nil, tokenId, scope: "")
    }

    /// 確認存著的登入
    static func check(server: URL) async -> OmixorLoginState {
        guard let t = token else { return .needLogin }
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

    /// 拿 evoleap token 換 omixor token,換完(成功或失敗)撤銷 evoleap token
    @MainActor
    static func exchange(_ evo: Connection, _ evoToken: TokenSet, server: URL) async throws -> OmixorAccount {
        let r: ConnHTTP.Response
        do {
            r = try await ConnHTTP.json("POST", server.appendingPathComponent("api/auth/evoleap"),
                                        ["token": evoToken.accessToken, "device": deviceName])
        } catch {
            await ApprovalConnector.logout(evo, evoToken)
            throw error
        }
        // evoleap 的 token 只用來證明一次身分:換成功或失敗都撤銷
        await ApprovalConnector.logout(evo, evoToken)
        let obj = r.json as? [String: Any]
        guard r.ok, let tok = obj?["token"] as? String, !tok.isEmpty else {
            throw ConnectionError("omixor 拒絕登入:\(ConnHTTP.errorMessage(r))", status: r.status)
        }
        let exp = date(obj?["expiresAt"] as? String)
        setToken(TokenSet(accessToken: tok, expiresAt: exp, tokenType: "Bearer"))
        let user = obj?["user"] as? [String: Any]
        return OmixorAccount(email: user?["email"] as? String ?? "", name: user?["name"] as? String ?? "", role: user?["role"] as? String ?? "",
                             device: deviceName, expiresAt: exp)
    }

    /// 目前登入的帳號;沒登入或 token 失效回傳 nil
    static func me(server: URL) async -> OmixorAccount? {
        if case .loggedIn(let a) = await check(server: server) { return a }
        return nil
    }

    private static func authed(_ method: String, _ url: URL, _ body: [String: Any]? = nil) async throws -> ConnHTTP.Response {
        guard let t = token else { throw ConnectionError("還沒登入 omixor") }
        let h = ["Authorization": "Bearer \(t.accessToken)"]
        if let body { return try await ConnHTTP.json(method, url, body, headers: h) }
        return try await ConnHTTP.send(method, url, headers: h)
    }

    private static func result<T: Decodable>(_ r: ConnHTTP.Response, _ type: T.Type) throws -> T {
        guard r.ok, let obj = r.json as? [String: Any], let res = obj["result"] else {
            throw ConnectionError(ConnHTTP.errorMessage(r), status: r.status)
        }
        return try JSONDecoder().decode(T.self, from: try JSONSerialization.data(withJSONObject: res, options: .fragmentsAllowed))
    }

    /// 向伺服器取得我能用的組織;失敗回傳 nil(畫面照用快取)
    static func fetchOrgs(server: URL) async -> [ServerOrg]? {
        guard let r = try? await authed("GET", server.appendingPathComponent("api/orgs")) else { return nil }
        return try? result(r, [ServerOrg].self)
    }

    /// 在伺服器建立組織(要伺服器管理員)
    static func createOrg(server: URL, id: String, name: String, symbol: String, color: String, note: String = "") async throws -> ServerOrg {
        let r = try await authed("POST", server.appendingPathComponent("api/orgs"),
                                 ["id": id, "name": name, "symbol": symbol, "color": color, "note": note])
        return try result(r, ServerOrg.self)
    }

    /// 修改伺服器上的組織名稱、圖示、顏色、備註(要伺服器管理員)
    static func updateOrg(server: URL, _ org: Organization) async throws -> ServerOrg {
        let id = OmixorMode.serverOrgId(org.id)
        let r = try await authed("PUT", server.appendingPathComponent("api/orgs").appendingPathComponent(id),
                                 ["name": org.name, "symbol": org.symbol, "color": org.color, "note": org.note])
        return try result(r, ServerOrg.self)
    }

    /// 登出:伺服器上撤銷這個 token,再從鑰匙圈刪掉(伺服器連不上也照樣刪)
    static func logout(server: URL) async {
        if token != nil {
            _ = try? await authed("POST", server.appendingPathComponent("api/auth/logout"), [:])
        }
        setToken(nil)
    }
}
