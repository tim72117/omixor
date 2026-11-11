import Foundation

/// 管理連線設定、登入、帶認證的請求。畫面與其他功能(例如旁白發佈)都透過這裡呼叫外部服務。
@MainActor
final class ConnectionStore {
    /// 強參照:登入、確認帳號這些非同步工作可能在 await 期間跨過 app 的生命週期(測試裡尤其常見),
    /// unowned 會在那時崩潰。AppState 本來就和 app 同生共死,這個循環參照不會造成實際的洩漏。
    let app: AppState

    init(app: AppState) { self.app = app }

    private var fileURL: URL { app.dataDir.appendingPathComponent("connections.json") }

    // MARK: - 設定

    func load() {
        if let d = try? Data(contentsOf: fileURL), let list = try? JSONDecoder().decode([Connection].self, from: d) {
            app.connections = list
        } else {
            app.connections = Connection.defaults
            save()
        }
        // 背景確認登入還有效;用 weak self,app 已經不在時(例如測試結束)就不做
        Task { [weak self] in
            guard let ids = self?.app.connections.filter({ self?.hasToken($0) == true }).map(\.id) else { return }
            for id in ids { await self?.refreshAccount(id) }
        }
    }

    func save() {
        let e = JSONEncoder()
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let d = try? e.encode(app.connections) { try? d.write(to: fileURL, options: .atomic) }
    }

    func connection(_ id: String) -> Connection? { app.connections.first { $0.id == id } }

    func upsert(_ c: Connection) {
        if let i = app.connections.firstIndex(where: { $0.id == c.id }) {
            let old = app.connections[i]
            app.connections[i] = c
            // 網址或認證方式換了,舊的 token 不能再用
            if old.baseURL != c.baseURL || old.auth != c.auth || old.oauth.clientId != c.oauth.clientId {
                TokenVault.setTokens(nil, c.id)
                app.connectionStates[c.id] = ConnectionState()
            }
        } else {
            app.connections.append(c)
        }
        save()
    }

    func delete(_ id: String) {
        TokenVault.removeAll(id)
        app.connections.removeAll { $0.id == id }
        app.connectionStates[id] = nil
        save()
    }

    func newId(for name: String) -> String {
        let base = name.lowercased().map { $0.isLetter || $0.isNumber ? String($0) : "-" }.joined()
            .split(separator: "-").joined(separator: "-")
        var id = base.isEmpty ? "conn" : base
        var n = 2
        while app.connections.contains(where: { $0.id == id }) { id = "\(base.isEmpty ? "conn" : base)-\(n)"; n += 1 }
        return id
    }

    func setClientSecret(_ secret: String?, for id: String) {
        TokenVault.setSecret(secret?.isEmpty == true ? nil : secret, id)
        if let i = app.connections.firstIndex(where: { $0.id == id }) {
            app.connections[i].oauth.hasClientSecret = TokenVault.secret(id) != nil
            save()
        }
    }

    func setBearer(_ token: String, for id: String) {
        let t = token.trimmingCharacters(in: .whitespacesAndNewlines)
        TokenVault.setTokens(t.isEmpty ? nil : TokenSet(accessToken: t, tokenType: "Bearer"), id)
        app.connectionStates[id] = ConnectionState()
        Task { [weak self] in await self?.refreshAccount(id) }
    }

    func hasToken(_ c: Connection) -> Bool { c.auth == .none || TokenVault.tokens(c.id) != nil }

    /// OAuth:從網址探索端點,填進設定
    func discover(_ id: String) async {
        guard let c = connection(id) else { return }
        setState(id) { $0.busy = true; $0.error = nil }
        defer { setState(id) { $0.busy = false } }
        do {
            let m = try await OAuthConnector.discover(c)
            var n = c
            n.oauth.issuer = m.issuer
            n.oauth.authorizationEndpoint = m.authorizationEndpoint
            n.oauth.tokenEndpoint = m.tokenEndpoint
            n.oauth.registrationEndpoint = m.registrationEndpoint
            if n.oauth.resource.isEmpty { n.oauth.resource = m.resource }
            upsert(n)
            setState(id) { $0.lastTest = "找到授權伺服器 \(m.issuer)" + (m.registrationEndpoint.isEmpty ? "(不支援自動註冊,需要 client_id)" : "(支援自動註冊)") }
        } catch {
            setState(id) { $0.error = error.localizedDescription }
        }
    }

    // MARK: - 登入

    func login(_ id: String) async {
        guard let c = connection(id) else { return }
        setState(id) { $0.busy = true; $0.error = nil; $0.lastTest = nil }
        defer { setState(id) { $0.busy = false } }
        do {
            switch c.auth {
            case .oauth:
                let (tokens, settings) = try await OAuthConnector.login(c)
                var n = c
                n.oauth = settings
                if let i = app.connections.firstIndex(where: { $0.id == id }) { app.connections[i] = n; save() }
                TokenVault.setTokens(tokens, id)
            case .approval:
                TokenVault.setTokens(try await ApprovalConnector.login(c), id)
            case .bearer, .none:
                break
            }
            await refreshAccount(id)
            app.log(.person(app.me), "person.badge.key", "Ops 登入「\(c.name)」" + (app.connectionStates[id]?.account.map { "(\($0))" } ?? ""), .settings)
        } catch {
            setState(id) { $0.error = error.localizedDescription }
        }
    }

    func logout(_ id: String) async {
        guard let c = connection(id) else { return }
        if c.auth == .approval, let t = TokenVault.tokens(id) { await ApprovalConnector.logout(c, t) }
        TokenVault.setTokens(nil, id)
        app.connectionStates[id] = ConnectionState()
    }

    /// 用測試路徑確認登入還有效,順便取得帳號名稱
    func refreshAccount(_ id: String) async {
        guard let c = connection(id) else { return }
        guard hasToken(c) else { setState(id) { $0.account = nil }; return }
        var label: String?
        if let idt = TokenVault.tokens(id)?.idToken, let claims = ConnHTTP.jwtClaims(idt) {
            label = ConnHTTP.accountLabel(claims)
        }
        if !c.testPath.isEmpty {
            do {
                let obj = try await request(id, "GET", c.testPath)
                label = ConnHTTP.accountLabel(obj) ?? label
                setState(id) { $0.lastTest = "測試連線成功(GET \(c.testPath))"; $0.error = nil }
            } catch let e as ConnectionError where e.isUnauthorized {
                setState(id) { $0.account = nil; $0.error = e.message }
                return
            } catch {
                setState(id) { $0.error = "連不到 \(c.name):\(error.localizedDescription)" }
            }
        }
        setState(id) { $0.account = label ?? (c.auth == .none ? "不用登入" : "已登入") }
    }

    // MARK: - 請求

    /// 取得要帶的 token;快過期時自動換新
    func bearer(_ c: Connection) async throws -> String? {
        if c.auth == .none { return nil }
        guard var t = TokenVault.tokens(c.id) else { throw ConnectionError("還沒登入「\(c.name)」", status: 401) }
        if c.auth == .oauth, t.isExpiring {
            t = try await OAuthConnector.refresh(c, t)
            TokenVault.setTokens(t, c.id)
        }
        if c.auth == .oauth, c.oauth.sendIdToken {
            guard let idt = t.idToken else { throw ConnectionError("登入結果沒有 id_token,請在 scopes 加上 openid") }
            return idt
        }
        return t.accessToken
    }

    /// 帶認證呼叫連線上的 API。path 以 / 開頭(接在連線網址後面);回傳解析後的 JSON
    func request(_ id: String, _ method: String, _ path: String, body: Data? = nil) async throws -> Any {
        try ConnHTTP.check(try await requestRaw(id, method, path, body: body)).json
    }

    /// 同 request,但回傳原始回應(不是 JSON 的內容,例如 GitHub Actions 的紀錄文字)
    func requestRaw(_ id: String, _ method: String, _ path: String, body: Data? = nil) async throws -> ConnHTTP.Response {
        guard let c = connection(id) else { throw ConnectionError("找不到連線 \(id)") }
        // 完整網址:只允許和連線網址同一個網域(例如 www.googleapis.com 的 token 打 run.googleapis.com),token 不會送到別處
        let full = path.hasPrefix("https://") ? path : c.trimmedBase + path
        guard let url = URL(string: full) else { throw ConnectionError("網址格式錯誤:\(path)") }
        guard path.hasPrefix("/") || c.allowsHost(url.host) else { throw ConnectionError("這個連線不能呼叫 \(url.host ?? path)") }
        var headers: [String: String] = [:]
        if let token = try await bearer(c) { headers["Authorization"] = "Bearer \(token)" }
        if body != nil { headers["Content-Type"] = "application/json" }
        var r = try await ConnHTTP.send(method, url, headers: headers, body: body)
        // OAuth 的 token 被提早撤銷:換新一次再試
        if r.status == 401, c.auth == .oauth, var t = TokenVault.tokens(id), t.refreshToken != nil {
            t = try await OAuthConnector.refresh(c, t)
            TokenVault.setTokens(t, id)
            headers["Authorization"] = "Bearer \(c.oauth.sendIdToken ? (t.idToken ?? t.accessToken) : t.accessToken)"
            r = try await ConnHTTP.send(method, url, headers: headers, body: body)
        }
        return r
    }

    private func setState(_ id: String, _ change: (inout ConnectionState) -> Void) {
        var s = app.connectionStates[id] ?? ConnectionState()
        change(&s)
        app.connectionStates[id] = s
    }
}
