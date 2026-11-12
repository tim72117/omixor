import AppKit
import CryptoKit
import Foundation

/// 開瀏覽器的方式;測試時換成直接打回呼網址
@MainActor
enum Browser {
    static var open: (URL) -> Void = { _ = NSWorkspace.shared.open($0) }
}

// MARK: - OAuth 2.0 / OIDC(通用)

/// 標準的授權碼流程 + PKCE(RFC 6749、7636、8252),只給網址也能用:
///   1. 探索:連線網址的 /.well-known/oauth-protected-resource(RFC 9728)找到授權伺服器,
///      再讀它的 /.well-known/openid-configuration 或 /.well-known/oauth-authorization-server(RFC 8414)
///   2. 沒有 client_id 但伺服器支援動態註冊(RFC 7591)時,自動註冊一個公開用戶端
///   3. 開瀏覽器授權,導回 127.0.0.1 的回呼,用 code + code_verifier 換 token
///   4. token 快過期時用 refresh_token 換新
enum OAuthConnector {
    struct Metadata: Equatable {
        var issuer: String
        var authorizationEndpoint: String
        var tokenEndpoint: String
        var registrationEndpoint: String
        var resource: String
    }

    // MARK: 探索

    /// 依序嘗試的中繼資料網址(RFC 8414 的路徑插在 /.well-known/ 後面;OIDC 接在 issuer 後面)
    static func metadataURLs(issuer: String) -> [String] {
        let iss = issuer.hasSuffix("/") ? String(issuer.dropLast()) : issuer
        guard let u = URL(string: iss), let scheme = u.scheme, let host = u.host else { return [] }
        let origin = "\(scheme)://\(host)" + (u.port.map { ":\($0)" } ?? "")
        let path = u.path == "/" ? "" : u.path
        var out = [iss + "/.well-known/openid-configuration"]
        if !path.isEmpty {
            out.append(origin + "/.well-known/oauth-authorization-server" + path)
            out.append(origin + "/.well-known/openid-configuration" + path)
        }
        out.append(origin + "/.well-known/oauth-authorization-server")
        return Array(NSOrderedSet(array: out)) as? [String] ?? out
    }

    static func discover(_ c: Connection) async throws -> Metadata {
        var issuer = c.oauth.issuer.trimmingCharacters(in: .whitespaces)
        var resource = c.oauth.resource
        if issuer.isEmpty {
            // RFC 9728:受保護資源告訴我們該找哪個授權伺服器
            for url in [c.origin + "/.well-known/oauth-protected-resource" + (URL(string: c.baseURL)?.path ?? ""),
                        c.origin + "/.well-known/oauth-protected-resource"] {
                guard let u = URL(string: url), let r = try? await ConnHTTP.send("GET", u), r.ok,
                      let d = r.json as? [String: Any] else { continue }
                if let servers = d["authorization_servers"] as? [String], let first = servers.first { issuer = first }
                if resource.isEmpty, let res = d["resource"] as? String { resource = res }
                break
            }
            if issuer.isEmpty { issuer = c.origin }
        }
        for url in metadataURLs(issuer: issuer) {
            guard let u = URL(string: url), let r = try? await ConnHTTP.send("GET", u), r.ok,
                  let d = r.json as? [String: Any],
                  let auth = d["authorization_endpoint"] as? String, let token = d["token_endpoint"] as? String else { continue }
            return Metadata(issuer: d["issuer"] as? String ?? issuer, authorizationEndpoint: auth, tokenEndpoint: token,
                            registrationEndpoint: d["registration_endpoint"] as? String ?? "", resource: resource)
        }
        throw ConnectionError("在 \(issuer) 找不到 OAuth 設定(/.well-known/openid-configuration 或 oauth-authorization-server)。請手動填授權與 Token 端點")
    }

    /// 已填的端點優先,缺的再探索
    static func endpoints(_ c: Connection) async throws -> Metadata {
        let o = c.oauth
        if !o.authorizationEndpoint.isEmpty && !o.tokenEndpoint.isEmpty {
            return Metadata(issuer: o.issuer, authorizationEndpoint: o.authorizationEndpoint, tokenEndpoint: o.tokenEndpoint,
                            registrationEndpoint: o.registrationEndpoint, resource: o.resource)
        }
        var m = try await discover(c)
        if !o.registrationEndpoint.isEmpty { m.registrationEndpoint = o.registrationEndpoint }
        return m
    }

    // MARK: PKCE

    static func base64url(_ d: Data) -> String {
        d.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    static func randomString(_ bytes: Int = 32) -> String {
        var b = [UInt8](repeating: 0, count: bytes)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes, &b)
        return base64url(Data(b))
    }

    static func challenge(for verifier: String) -> String {
        base64url(Data(SHA256.hash(data: Data(verifier.utf8))))
    }

    static func authorizationURL(_ m: Metadata, clientId: String, redirect: String, scopes: String,
                                 state: String, challenge: String, extra: String) -> URL? {
        guard var comps = URLComponents(string: m.authorizationEndpoint) else { return nil }
        var items = comps.queryItems ?? []
        items += [URLQueryItem(name: "response_type", value: "code"),
                  URLQueryItem(name: "client_id", value: clientId),
                  URLQueryItem(name: "redirect_uri", value: redirect),
                  URLQueryItem(name: "state", value: state),
                  URLQueryItem(name: "code_challenge", value: challenge),
                  URLQueryItem(name: "code_challenge_method", value: "S256")]
        let scope = scopes.split(whereSeparator: { $0 == " " || $0 == "," }).joined(separator: " ")
        if !scope.isEmpty { items.append(URLQueryItem(name: "scope", value: scope)) }
        if !m.resource.isEmpty { items.append(URLQueryItem(name: "resource", value: m.resource)) }
        for pair in extra.split(separator: "&") where !pair.isEmpty {
            let kv = pair.split(separator: "=", maxSplits: 1).map(String.init)
            items.append(URLQueryItem(name: kv[0], value: kv.count > 1 ? (kv[1].removingPercentEncoding ?? kv[1]) : ""))
        }
        comps.queryItems = items
        return comps.url
    }

    static func parseTokens(_ obj: Any, previous: TokenSet? = nil) throws -> TokenSet {
        // GitHub 換 token 失敗時回 200 加上 error
        if let d = obj as? [String: Any], d["access_token"] == nil, let e = d["error"] as? String {
            throw ConnectionError(d["error_description"] as? String ?? e)
        }
        guard let d = obj as? [String: Any], let access = d["access_token"] as? String else {
            throw ConnectionError("Token 回應裡沒有 access_token")
        }
        let expires = (d["expires_in"] as? NSNumber)?.doubleValue ?? Double(d["expires_in"] as? String ?? "")
        return TokenSet(accessToken: access,
                        refreshToken: d["refresh_token"] as? String ?? previous?.refreshToken,
                        idToken: d["id_token"] as? String ?? previous?.idToken,
                        expiresAt: expires.map { Date().addingTimeInterval($0) },
                        tokenType: d["token_type"] as? String)
    }

    // MARK: 登入

    /// 回傳 token,以及要寫回連線設定的變動(探索到的端點、動態註冊拿到的 client_id)
    @MainActor
    static func login(_ c: Connection) async throws -> (TokenSet, OAuthSettings) {
        var settings = c.oauth
        let m = try await endpoints(c)
        // 探索到的結果寫回設定:之後換新 token 不用再探索,resource 也不會遺失
        if settings.issuer.isEmpty { settings.issuer = m.issuer }
        settings.authorizationEndpoint = m.authorizationEndpoint
        settings.tokenEndpoint = m.tokenEndpoint
        if settings.registrationEndpoint.isEmpty { settings.registrationEndpoint = m.registrationEndpoint }
        if settings.resource.isEmpty { settings.resource = m.resource }
        let callback = try await LoopbackCallback.start(preferredPort: settings.registeredPort)
        defer { callback.stop() }

        var clientId = settings.clientId
        if clientId.isEmpty || (settings.registeredPort != nil && settings.registeredPort != callback.port) {
            guard !m.registrationEndpoint.isEmpty, let reg = URL(string: m.registrationEndpoint) else {
                throw ConnectionError("這個伺服器不支援自動註冊,請在連線設定填 client_id(在服務的開發者主控台建立「桌面/原生」類型的 OAuth 用戶端)")
            }
            let r = try ConnHTTP.check(try await ConnHTTP.json("POST", reg, [
                "client_name": "omixor(\(Host.current().localizedName ?? "Mac"))",
                "redirect_uris": [callback.redirectURI],
                "grant_types": ["authorization_code", "refresh_token"],
                "response_types": ["code"],
                "token_endpoint_auth_method": "none",
            ]))
            guard let id = (r.json as? [String: Any])?["client_id"] as? String else { throw ConnectionError("自動註冊沒有回傳 client_id") }
            clientId = id
            settings.clientId = id
            settings.registeredPort = callback.port
        }

        let verifier = randomString(48)
        let state = randomString(16)
        callback.expect = ("state", state)
        guard let url = authorizationURL(m, clientId: clientId, redirect: callback.redirectURI, scopes: settings.scopes,
                                         state: state, challenge: challenge(for: verifier), extra: settings.extraParams)
        else { throw ConnectionError("授權網址格式錯誤:\(m.authorizationEndpoint)") }
        Browser.open(url)
        let code = try await callback.waitForCode(timeout: 600)

        guard let tokenURL = URL(string: m.tokenEndpoint) else { throw ConnectionError("Token 端點格式錯誤") }
        var fields = ["grant_type": "authorization_code", "code": code, "redirect_uri": callback.redirectURI,
                      "client_id": clientId, "code_verifier": verifier]
        if let secret = TokenVault.secret(c.id), !secret.isEmpty { fields["client_secret"] = secret }
        if !m.resource.isEmpty { fields["resource"] = m.resource }
        let r = try ConnHTTP.check(try await ConnHTTP.form(tokenURL, fields))
        return (try parseTokens(r.json), settings)
    }

    static func refresh(_ c: Connection, _ t: TokenSet) async throws -> TokenSet {
        guard let rt = t.refreshToken else { throw ConnectionError("登入已過期,請重新登入", status: 401) }
        let m = try await endpoints(c)
        guard let tokenURL = URL(string: m.tokenEndpoint) else { throw ConnectionError("Token 端點格式錯誤") }
        var fields = ["grant_type": "refresh_token", "refresh_token": rt, "client_id": c.oauth.clientId]
        if let secret = TokenVault.secret(c.id), !secret.isEmpty { fields["client_secret"] = secret }
        if !m.resource.isEmpty { fields["resource"] = m.resource }
        let r = try await ConnHTTP.form(tokenURL, fields)
        guard r.ok else { throw ConnectionError("登入已過期,請重新登入(\(ConnHTTP.errorMessage(r)))", status: 401) }
        return try parseTokens(r.json, previous: t)
    }
}

// MARK: - 瀏覽器核准(evoleap CLI 的登入方式)

/// POST start 登記回呼網址 → 瀏覽器開核准頁 → 回呼帶 id + code → POST exchange 換 token。
/// 路徑都可以在連線設定改,其他服務用同樣做法也能接。
enum ApprovalConnector {
    @MainActor
    static func login(_ c: Connection) async throws -> TokenSet {
        let a = c.approval
        let callback = try await LoopbackCallback.start()
        defer { callback.stop() }
        guard let start = URL(string: c.trimmedBase + a.startPath) else { throw ConnectionError("網址格式錯誤") }
        let r = try ConnHTTP.check(try await ConnHTTP.json("POST", start, [
            "redirect_uri": callback.redirectURI, "name": "omixor(\(Host.current().localizedName ?? "Mac"))"]))
        guard let id = (r.json as? [String: Any])?["id"] as? String else { throw ConnectionError("伺服器沒有回傳登入 id") }
        callback.expect = ("id", id)
        let console = a.consoleURL.isEmpty ? c.trimmedBase : (a.consoleURL.hasSuffix("/") ? String(a.consoleURL.dropLast()) : a.consoleURL)
        let approve = a.approvePath.replacingOccurrences(of: "{id}", with: id.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? id)
        guard let approveURL = URL(string: console + approve) else { throw ConnectionError("核准頁網址格式錯誤") }
        Browser.open(approveURL)
        let code = try await callback.waitForCode(timeout: 600)
        guard let ex = URL(string: c.trimmedBase + a.exchangePath) else { throw ConnectionError("網址格式錯誤") }
        let tr = try ConnHTTP.check(try await ConnHTTP.json("POST", ex, ["id": id, "code": code]))
        guard let token = (tr.json as? [String: Any])?["token"] as? String else { throw ConnectionError("換不到 token") }
        return TokenSet(accessToken: token, refreshToken: nil, idToken: nil, expiresAt: nil, tokenType: "Bearer")
    }

    static func logout(_ c: Connection, _ t: TokenSet) async {
        guard !c.approval.logoutPath.isEmpty, let u = URL(string: c.trimmedBase + c.approval.logoutPath) else { return }
        _ = try? await ConnHTTP.send("POST", u, headers: ["Authorization": "Bearer \(t.accessToken)"])
    }
}
