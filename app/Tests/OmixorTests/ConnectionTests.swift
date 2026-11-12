import XCTest
@testable import Ops

/// 假的服務:攔截 ConnHTTP 的請求,依路徑回應
final class StubProtocol: URLProtocol {
    nonisolated(unsafe) static var handler: ((URLRequest, Data) -> (Int, Any))?
    nonisolated(unsafe) static var log: [(String, String, Data)] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if body.isEmpty, let s = request.httpBodyStream {
            s.open()
            var buf = [UInt8](repeating: 0, count: 4096)
            while s.hasBytesAvailable { let n = s.read(&buf, maxLength: buf.count); if n <= 0 { break }; body.append(buf, count: n) }
            s.close()
        }
        Self.log.append((request.httpMethod ?? "GET", request.url?.absoluteString ?? "", body))
        var (status, obj) = Self.handler?(request, body) ?? (404, ["error": "no handler"])
        var fields = ["Content-Type": "application/json"]
        if let r = obj as? StubReply { fields.merge(r.headers) { $1 }; obj = r.body }
        let data = obj is NSNull ? Data() : (try? JSONSerialization.data(withJSONObject: obj, options: .fragmentsAllowed)) ?? Data()
        let resp = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: fields)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

/// handler 回傳這個型別時加上回應標頭(例如 ETag);body 是 NSNull 時不送內容
struct StubReply {
    var headers: [String: String]
    var body: Any
}

@MainActor
final class ConnectionTests: XCTestCase {
    private var apps: [AppState] = []
    private var dataDir: URL!

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-conn-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        ConnHTTP.protocolClasses = [StubProtocol.self]
        StubProtocol.log = []
        StubProtocol.handler = nil
    }

    override func tearDown() async throws {
        for app in apps { for c in app.connections { TokenVault.removeAll(c.id) } }
        Browser.open = { _ in }
        ConnHTTP.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func makeApp() -> (AppState, ConnectionStore) {
        let app = AppState()
        apps.append(app)
        app.startConnections()
        return (app, app.connStore!)
    }

    /// 模擬瀏覽器:使用者按同意後,授權伺服器把瀏覽器導回 redirect_uri?code=…&state=…
    private func browserApproves(code: String, param: String = "state", extra: [String: String] = [:]) {
        Browser.open = { url in
            let q = Dictionary(uniqueKeysWithValues: (URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") })
            Task.detached {
                // 瀏覽器核准:回呼網址在登記時就給了,從 StubProtocol 記下的 start 請求裡拿
                let redirect = q["redirect_uri"] ?? StubProtocol.log.compactMap { entry -> String? in
                    guard entry.1.hasSuffix("/api/cli-auth/start"),
                          let o = try? JSONSerialization.jsonObject(with: entry.2) as? [String: Any] else { return nil }
                    return o["redirect_uri"] as? String
                }.last ?? ""
                var comps = URLComponents(string: redirect)!
                comps.queryItems = [URLQueryItem(name: "code", value: code),
                                    URLQueryItem(name: param, value: q[param] ?? extra[param] ?? "")]
                _ = try? await URLSession.shared.data(from: comps.url!)
            }
        }
    }

    // MARK: - 純函式

    func testPKCEMatchesRFC7636Vector() {
        XCTAssertEqual(OAuthConnector.challenge(for: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"),
                       "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
        XCTAssertGreaterThanOrEqual(OAuthConnector.randomString(48).count, 43)
    }

    func testMetadataURLs() {
        XCTAssertEqual(OAuthConnector.metadataURLs(issuer: "https://accounts.google.com"),
                       ["https://accounts.google.com/.well-known/openid-configuration",
                        "https://accounts.google.com/.well-known/oauth-authorization-server"])
        let tenant = OAuthConnector.metadataURLs(issuer: "https://auth.example.com/realms/ops/")
        XCTAssertEqual(tenant.first, "https://auth.example.com/realms/ops/.well-known/openid-configuration")
        XCTAssertTrue(tenant.contains("https://auth.example.com/.well-known/oauth-authorization-server/realms/ops"))
    }

    func testAuthorizationURL() throws {
        let m = OAuthConnector.Metadata(issuer: "i", authorizationEndpoint: "https://a.example/authorize?x=1",
                                        tokenEndpoint: "t", registrationEndpoint: "", resource: "https://api.example")
        let url = try XCTUnwrap(OAuthConnector.authorizationURL(m, clientId: "cid", redirect: "http://127.0.0.1:5000/callback",
                                                                scopes: "openid, email", state: "st", challenge: "ch",
                                                                extra: "access_type=offline&prompt=consent"))
        let q = Dictionary(uniqueKeysWithValues: URLComponents(url: url, resolvingAgainstBaseURL: false)!.queryItems!.map { ($0.name, $0.value!) })
        XCTAssertEqual(q["x"], "1")
        XCTAssertEqual(q["response_type"], "code")
        XCTAssertEqual(q["code_challenge_method"], "S256")
        XCTAssertEqual(q["scope"], "openid email")
        XCTAssertEqual(q["resource"], "https://api.example")
        XCTAssertEqual(q["access_type"], "offline")
        XCTAssertEqual(q["prompt"], "consent")
    }

    func testAccountLabelAndJWT() {
        XCTAssertEqual(ConnHTTP.accountLabel(["user": ["email": "tim@example.com", "id": "1"]]), "tim@example.com")
        XCTAssertEqual(ConnHTTP.accountLabel(["login": "octocat"]), "octocat")
        let payload = OAuthConnector.base64url(try! JSONSerialization.data(withJSONObject: ["email": "a@b.c", "sub": "1"]))
        XCTAssertEqual(ConnHTTP.jwtClaims("h.\(payload).s")?["email"] as? String, "a@b.c")
    }

    func testDefaultsSeededAndPersisted() {
        let (app, store) = makeApp()
        XCTAssertEqual(app.connections.map(\.id), ["evoleap", "evoleap-local"])
        var c = Connection(id: store.newId(for: "Payments API"), name: "Payments API", baseURL: "https://pay.example", auth: .bearer)
        XCTAssertEqual(c.id, "payments-api")
        c.testPath = "/me"
        store.upsert(c)
        let (app2, _) = makeApp()
        XCTAssertEqual(app2.connections.last?.testPath, "/me")
    }

    // MARK: - OAuth 全流程(只給網址)

    func testOAuthDiscoveryRegistrationLoginRefreshAndRequest() async throws {
        var tokenCalls = 0
        StubProtocol.handler = { req, body in
            let url = req.url!.absoluteString
            let form = Dictionary(uniqueKeysWithValues: String(decoding: body, as: UTF8.self).split(separator: "&").map {
                let kv = $0.split(separator: "=", maxSplits: 1).map { String($0).removingPercentEncoding ?? String($0) }
                return (kv[0], kv.count > 1 ? kv[1] : "")
            })
            switch url {
            case "https://api.example/.well-known/oauth-protected-resource":
                return (200, ["resource": "https://api.example", "authorization_servers": ["https://auth.example"]])
            case "https://auth.example/.well-known/openid-configuration":
                return (200, ["issuer": "https://auth.example", "authorization_endpoint": "https://auth.example/authorize",
                              "token_endpoint": "https://auth.example/token", "registration_endpoint": "https://auth.example/register"])
            case "https://auth.example/register":
                return (201, ["client_id": "dyn-123"])
            case "https://auth.example/token":
                tokenCalls += 1
                XCTAssertEqual(form["client_id"], "dyn-123")
                XCTAssertEqual(form["resource"], "https://api.example")
                if form["grant_type"] == "authorization_code" {
                    XCTAssertEqual(form["code"], "the-code")
                    XCTAssertNotNil(form["code_verifier"])
                    return (200, ["access_token": "at-1", "refresh_token": "rt-1", "expires_in": 1, "token_type": "Bearer"])
                }
                XCTAssertEqual(form["refresh_token"], "rt-1")
                return (200, ["access_token": "at-2", "expires_in": 3600])
            case "https://api.example/me":
                let auth = req.value(forHTTPHeaderField: "Authorization")
                return auth == "Bearer at-1" || auth == "Bearer at-2" ? (200, ["email": "tim@example.com"]) : (401, ["error": "unauthorized"])
            default:
                return (404, ["error": "not found: \(url)"])
            }
        }
        let (app, store) = makeApp()
        store.upsert(Connection(id: "api", name: "API", baseURL: "https://api.example", testPath: "/me", auth: .oauth))
        browserApproves(code: "the-code")

        await store.login("api")
        XCTAssertNil(app.connectionStates["api"]?.error)
        XCTAssertEqual(app.connectionStates["api"]?.account, "tim@example.com")
        let c = try XCTUnwrap(store.connection("api"))
        XCTAssertEqual(c.oauth.clientId, "dyn-123", "動態註冊拿到的 client_id 要存回設定")
        XCTAssertEqual(c.oauth.issuer, "https://auth.example")
        XCTAssertNotNil(c.oauth.registeredPort)
        XCTAssertEqual(TokenVault.tokens("api")?.refreshToken, "rt-1")

        // access token 1 秒後過期:下一次請求自動換新
        try await Task.sleep(nanoseconds: 1_100_000_000)
        let me = try await store.request("api", "GET", "/me")
        XCTAssertEqual((me as? [String: Any])?["email"] as? String, "tim@example.com")
        XCTAssertEqual(TokenVault.tokens("api")?.accessToken, "at-2")
        XCTAssertEqual(TokenVault.tokens("api")?.refreshToken, "rt-1", "沒給新的 refresh token 時沿用舊的")
        XCTAssertEqual(tokenCalls, 2)

        await store.logout("api")
        XCTAssertNil(TokenVault.tokens("api"))
        do { _ = try await store.request("api", "GET", "/me"); XCTFail("登出後應該不能呼叫") } catch {}
    }

    func testOAuthWithoutRegistrationNeedsClientId() async {
        StubProtocol.handler = { req, _ in
            req.url!.absoluteString == "https://auth.example/.well-known/openid-configuration"
                ? (200, ["issuer": "https://auth.example", "authorization_endpoint": "https://auth.example/a", "token_endpoint": "https://auth.example/t"])
                : (404, [:])
        }
        let (app, store) = makeApp()
        var c = Connection(id: "g", name: "G", baseURL: "https://svc.example", auth: .oauth)
        c.oauth.issuer = "https://auth.example"
        store.upsert(c)
        await store.login("g")
        XCTAssertTrue(app.connectionStates["g"]?.error?.contains("client_id") == true)
    }

    // MARK: - 瀏覽器核准(evoleap)

    func testApprovalFlowLikeEvoleap() async throws {
        StubProtocol.handler = { req, body in
            switch req.url!.path {
            case "/api/cli-auth/start":
                let o = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
                XCTAssertTrue((o?["redirect_uri"] as? String ?? "").hasPrefix("http://127.0.0.1:"))
                return (200, ["id": "req-1"])
            case "/api/cli-auth/exchange":
                let o = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any]
                return o?["id"] as? String == "req-1" && o?["code"] as? String == "c-9" ? (200, ["token": "cli-token"]) : (400, ["error": ["message": "bad"]])
            case "/api/me":
                return req.value(forHTTPHeaderField: "Authorization") == "Bearer cli-token"
                    ? (200, ["user": ["email": "admin@evoleap.io"]]) : (200, ["user": NSNull()])
            case "/api/cli-auth/logout":
                return (200, ["ok": true])
            default: return (404, [:])
            }
        }
        var opened: URL?
        let (app, store) = makeApp()
        browserApproves(code: "c-9", param: "id", extra: ["id": "req-1"])
        let approve = Browser.open
        Browser.open = { opened = $0; approve($0) }

        await store.login("evoleap-local")
        XCTAssertNil(app.connectionStates["evoleap-local"]?.error)
        XCTAssertEqual(app.connectionStates["evoleap-local"]?.account, "admin@evoleap.io")
        XCTAssertEqual(opened?.absoluteString, "http://localhost:5190/login/cli?id=req-1")
        XCTAssertEqual(TokenVault.tokens("evoleap-local")?.accessToken, "cli-token")
        await store.logout("evoleap-local")
        XCTAssertTrue(StubProtocol.log.contains { $0.1.hasSuffix("/api/cli-auth/logout") })
    }
}
