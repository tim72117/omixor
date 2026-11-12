import Foundation

// 在 app 裡登入 omixor(和 omixor CLI 的 omixor login 一樣):
//   1. 瀏覽器開 evoleap 的核准頁(ApprovalConnector,和 evoleap 連線同一套),拿到一次性的 evoleap CLI token
//   2. 交給 omixor-issues 的 POST /api/auth/evoleap,換 omixor 自己的 token(omx_…)
//   3. 不管換成功或失敗,立刻撤銷那把 evoleap token(只用來證明一次身分)
//   4. omixor token 存在鑰匙圈的 omixor-issues(IssueRemote 讀寫 Issue 時帶它)
// evoleap 只證明身分;能不能用、是管理員還是成員,由 omixor 的使用者名單決定。

struct OmixorAccount: Equatable {
    var email: String
    var name: String = ""
    var role: String
    var device: String
    var expiresAt: Date?
}

/// omixor server 上我能用的組織(app 只做快取:開啟時先讀快取,登入確認後向伺服器更新)
struct ServerOrg: Codable, Equatable, Identifiable {
    var id: String
    var name: String
    var myRole: String?
}

/// app 的登入狀態
enum OmixorLoginState: Equatable {
    case checking                    // 剛開啟,正在確認存著的登入
    case needLogin                   // 沒登入或登入失效:顯示登入畫面
    case unreachable(String)         // 有存著的登入,但連不到伺服器
    case loggedIn(OmixorAccount)
    case skipped                     // 離線繼續,或 OPS_SKIP_LOGIN=1(開發、比對用)

    var account: OmixorAccount? {
        if case .loggedIn(let a) = self { return a }
        return nil
    }
}

enum OmixorAuth {
    static var tokenId: String { IssueServerConfig.omixorTokenId }
    /// 正式的 omixor-issues
    static let defaultServer = URL(string: "https://omixor-issues-340121279179.asia-east1.run.app")!

    /// 登入用的伺服器:設定或環境變數指定的 Issue 伺服器,沒設就用正式站
    static var server: URL { IssueServerConfig.current?.url ?? defaultServer }

    /// 開啟時確認存著的登入
    static func check(server: URL) async -> OmixorLoginState {
        guard let t = TokenVault.tokens(tokenId) else { return .needLogin }
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
        TokenVault.setTokens(TokenSet(accessToken: tok, expiresAt: exp, tokenType: "Bearer"), tokenId)
        let user = obj?["user"] as? [String: Any]
        return OmixorAccount(email: user?["email"] as? String ?? "", name: user?["name"] as? String ?? "", role: user?["role"] as? String ?? "",
                             device: deviceName, expiresAt: exp)
    }

    /// 目前登入的帳號;沒登入或 token 失效回傳 nil
    static func me(server: URL) async -> OmixorAccount? {
        if case .loggedIn(let a) = await check(server: server) { return a }
        return nil
    }

    /// 組織清單的快取檔(資料根目錄,不分組織)
    static var orgsCacheURL: URL { OrgFiles.root.appendingPathComponent("omixor-orgs.json") }

    static func cachedOrgs() -> [ServerOrg] {
        guard let d = try? Data(contentsOf: orgsCacheURL) else { return [] }
        return (try? JSONDecoder().decode([ServerOrg].self, from: d)) ?? []
    }

    /// 向伺服器取得我能用的組織,成功就更新快取;失敗回傳 nil(畫面照用快取)
    static func fetchOrgs(server: URL) async -> [ServerOrg]? {
        guard let t = TokenVault.tokens(tokenId),
              let r = try? await ConnHTTP.send("GET", server.appendingPathComponent("api/orgs"),
                                               headers: ["Authorization": "Bearer \(t.accessToken)"]), r.ok,
              let obj = r.json as? [String: Any], let result = obj["result"],
              let data = try? JSONSerialization.data(withJSONObject: result),
              let orgs = try? JSONDecoder().decode([ServerOrg].self, from: data) else { return nil }
        try? JSONEncoder().encode(orgs).write(to: orgsCacheURL, options: .atomic)
        return orgs
    }

    /// 登出:伺服器上撤銷這個 token,再從鑰匙圈刪掉(伺服器連不上也照樣刪)
    static func logout(server: URL) async {
        if let t = TokenVault.tokens(tokenId) {
            _ = try? await ConnHTTP.send("POST", server.appendingPathComponent("api/auth/logout"),
                                         headers: ["Authorization": "Bearer \(t.accessToken)"])
        }
        TokenVault.setTokens(nil, tokenId)
    }
}
