import XCTest
@testable import Ops

/// Facebook 粉絲專頁:用 StubProtocol 假裝 graph.facebook.com,絕不送出真的請求
@MainActor
final class FacebookTests: XCTestCase {
    private var app: AppState!
    private var fb: FacebookStore!
    private var mcp: MCPHandler!
    private var dataDir: URL!
    private var nextId = 0

    struct Req { let method: String; let url: URL; let headers: [String: String]; let body: String }
    nonisolated(unsafe) static var requests: [Req] = []

    static let page1 = "1001"
    static let page2 = "1002"
    static let pageToken = "EAAG-page-1001"
    static let page2Token = "EAAG-page-1002"
    static let userToken = "EAAG-user"
    static let longUser = "EAAG-long-user"

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-fb-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        ConnHTTP.protocolClasses = [StubProtocol.self]
        Self.requests = []
        StubProtocol.log = []
        StubProtocol.handler = Self.fakeGraph
        FacebookStore.confirmOwner = { _ in true }
        app = AppState()
        app.startTestAgents()
        fb = app.startFacebook()
        mcp = MCPHandler(app: app)
    }

    override func tearDown() async throws {
        for p in app.fbSettings.pages { FacebookStore.setPageToken(nil, p.id) }
        for c in app.connections { TokenVault.removeAll(c.id) }
        ConnHTTP.protocolClasses = []
        StubProtocol.handler = nil
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    // MARK: - 假的 Graph API

    nonisolated static let deprecated: Set<String> = ["post_impressions_unique", "page_impressions_unique", "page_impressions",
                                                      "page_fans", "page_post_engagements", "page_fan_adds_unique"]

    nonisolated static func graphError(_ status: Int, _ message: String, _ code: Int, sub: Int? = nil) -> (Int, Any) {
        var e: [String: Any] = ["message": message, "type": "OAuthException", "code": code, "fbtrace_id": "Abc"]
        if let sub { e["error_subcode"] = sub }
        return (status, ["error": e])
    }

    nonisolated static func fakeGraph(_ req: URLRequest, _ body: Data) -> (Int, Any) {
        let url = req.url!
        requests.append(Req(method: req.httpMethod ?? "GET", url: url, headers: req.allHTTPHeaderFields ?? [:],
                            body: String(decoding: body, as: UTF8.self)))
        guard url.host == "graph.facebook.com", url.path.hasPrefix("/v24.0/") else { return (404, ["error": "wrong host"]) }
        let path = String(url.path.dropFirst("/v24.0".count))
        let q = Dictionary(uniqueKeysWithValues: (URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") })
        let auth = req.value(forHTTPHeaderField: "Authorization") ?? ""

        if path == "/oauth/access_token" {
            guard q["grant_type"] == "fb_exchange_token", q["client_id"] == "APPID", q["client_secret"] == "APPSECRET",
                  q["fb_exchange_token"] == "SHORT" else { return graphError(400, "Invalid OAuth access token", 190) }
            return (200, ["access_token": longUser, "token_type": "bearer", "expires_in": 5_184_000])
        }
        if auth == "Bearer EXPIRED" { return graphError(400, "Error validating access token: Session has expired", 190, sub: 463) }

        let page1Info: [String: Any] = ["id": page1, "name": "evoleap", "category": "Education", "link": "https://facebook.com/evoleap",
                                        "fan_count": 120, "followers_count": 150, "picture": ["data": ["url": "https://img/p.png"]]]
        switch (req.httpMethod ?? "GET", path) {
        case ("GET", "/me"):
            if auth == "Bearer \(pageToken)" { return (200, page1Info) }
            if auth == "Bearer \(userToken)" { return (200, ["id": "u1", "name": "Tim"]) }
            return graphError(400, "Invalid OAuth access token", 190)
        case ("GET", "/me/accounts"):
            guard auth == "Bearer \(userToken)" || auth == "Bearer \(longUser)" else { return graphError(400, "bad", 190) }
            if q["after"] == "P2" {
                return (200, ["data": [["id": page2, "name": "evoleap 兒童", "category": "Education", "access_token": page2Token]]])
            }
            var p = page1Info
            p["access_token"] = pageToken
            return (200, ["data": [p], "paging": ["cursors": ["after": "P2"], "next": "https://graph.facebook.com/v24.0/me/accounts?after=P2"]])
        default: break
        }
        guard auth == "Bearer \(pageToken)" else { return graphError(400, "Invalid OAuth access token", 190) }

        switch (req.httpMethod ?? "GET", path) {
        case ("GET", "/\(page1)"):
            return (200, page1Info)
        case ("GET", "/\(page1)/posts"):
            func post(_ n: Int) -> [String: Any] {
                ["id": "\(page1)_\(n)", "message": "第 \(n) 篇", "created_time": "2026-11-0\(n)T08:00:00+0000",
                 "permalink_url": "https://facebook.com/\(n)", "is_published": true, "status_type": "mobile_status_update",
                 "reactions": ["data": [], "summary": ["total_count": 7]], "comments": ["data": [], "summary": ["total_count": 2]],
                 "shares": ["count": 1]]
            }
            if q["after"] == "X" { return (200, ["data": [post(3)]]) }
            return (200, ["data": [post(1), post(2)],
                          "paging": ["next": "https://graph.facebook.com/v24.0/\(page1)/posts?after=X"]])
        case ("GET", "/\(page1)/scheduled_posts"):
            return (200, ["data": [["id": "\(page1)_900", "message": "排程的", "scheduled_publish_time": 1_800_000_000, "is_published": false]]])
        case ("POST", "/\(page1)/feed"):
            if String(decoding: body, as: UTF8.self).contains("FAIL") {
                return graphError(403, "(#200) Requires pages_manage_posts permission", 200)
            }
            return (200, ["id": "\(page1)_555"])
        case ("POST", "/\(page1)/photos"):
            return (200, ["id": "777", "post_id": "\(page1)_777"])
        case ("DELETE", "/\(page1)_900"):
            return (200, ["success": true])
        default: break
        }
        if path.hasSuffix("/insights") {
            let metrics = (q["metric"] ?? "").split(separator: ",").map(String.init)
            if metrics.contains(where: deprecated.contains) {
                return graphError(400, "(#100) The value must be a valid insights metric", 100)
            }
            let data: [[String: Any]] = metrics.map { m in
                switch m {
                case "post_total_media_view_unique": return ["name": m, "period": "lifetime", "values": [["value": 345]]]
                case "post_clicks": return ["name": m, "period": "lifetime", "values": [["value": 12]]]
                case "post_reactions_by_type_total": return ["name": m, "period": "lifetime", "values": [["value": ["like": 5, "love": 2]]]]
                case "page_total_media_view_unique", "page_media_view":
                    return ["name": m, "period": "day", "values": [10, 20, 30].enumerated().map { i, v in
                        ["value": v, "end_time": "2026-11-0\(i + 1)T08:00:00+0000"] }]
                case "page_follows":
                    return ["name": m, "period": "day", "values": [150, 151, 152].enumerated().map { i, v in
                        ["value": v, "end_time": "2026-11-0\(i + 1)T08:00:00+0000"] }]
                default: return ["name": m, "period": "day", "values": []]
                }
            }
            return (200, ["data": data])
        }
        return graphError(400, "Unsupported get request", 100, sub: 33)
    }

    private var graphPosts: [Req] { Self.requests.filter { $0.method == "POST" } }

    private func connect() async throws {
        try await fb.addToken(Self.pageToken)
    }

    private func form(_ body: String) -> [String: String] {
        LineAPI.parseData(body)
    }

    // MARK: - 連線與權杖

    func testPastePageTokenStoresInKeychainNotJSON() async throws {
        try await connect()
        XCTAssertEqual(app.fbSettings.pages.map(\.id), [Self.page1])
        XCTAssertEqual(app.fbSettings.pages[0].followersCount, 150)
        XCTAssertEqual(FacebookStore.pageToken(Self.page1), Self.pageToken)
        XCTAssertTrue(fb.isConnected)
        let r = try XCTUnwrap(Self.requests.first)
        XCTAssertEqual(r.url.host, "graph.facebook.com")
        XCTAssertEqual(r.headers["Authorization"], "Bearer \(Self.pageToken)")
        XCTAssertFalse(r.url.absoluteString.contains(Self.pageToken), "權杖不放在網址裡")
        let file = try String(contentsOf: app.dataDir.appendingPathComponent("facebook.json"), encoding: .utf8)
        XCTAssertTrue(file.contains(Self.page1))
        XCTAssertFalse(file.contains("EAAG"))
        // 組織前綴
        TokenVault.scope = "other/"
        XCTAssertNil(FacebookStore.pageToken(Self.page1))
        TokenVault.scope = ""
    }

    func testPasteUserTokenListsPagesAcrossPaging() async throws {
        try await fb.addToken(Self.userToken)
        XCTAssertEqual(app.fbSettings.pages.map(\.id).sorted(), [Self.page1, Self.page2])
        XCTAssertEqual(FacebookStore.pageToken(Self.page2), Self.page2Token)
        XCTAssertEqual(Self.requests.filter { $0.url.path.hasSuffix("/me/accounts") }.count, 2)
    }

    func testMetaLoginExchangesLongLivedTokenThenPageTokens() async throws {
        fb.setApp(id: "APPID", secret: "APPSECRET")
        let c = try XCTUnwrap(app.connections.first { $0.id == FB.connectionId })
        XCTAssertEqual(c.oauth.scopes, "pages_show_list,pages_read_engagement,pages_manage_posts,read_insights")
        XCTAssertEqual(c.oauth.registeredPort, 3767)
        XCTAssertTrue(c.oauth.authorizationEndpoint.hasPrefix("https://www.facebook.com/v24.0/dialog/oauth"))
        try await fb.completeLogin(userToken: "SHORT")
        XCTAssertEqual(TokenVault.tokens(FB.connectionId)?.accessToken, Self.longUser)
        XCTAssertEqual(app.fbSettings.mode, "oauth")
        XCTAssertEqual(Set(app.fbSettings.pages.map(\.id)), [Self.page1, Self.page2])
        XCTAssertEqual(FacebookStore.pageToken(Self.page1), Self.pageToken)
        for f in ["facebook.json", "connections.json"] {
            let s = try String(contentsOf: app.dataDir.appendingPathComponent(f), encoding: .utf8)
            XCTAssertFalse(s.contains("APPSECRET") || s.contains("EAAG"), f)
        }
    }

    func testLenientDecoding() throws {
        let raw = #"{"apiVersion":"v18.0","pages":[{"id":"9","name":1},{"name":"沒有 id"}],"drafts":[{"id":"FD-2","status":"weird","message":"hi"}],"x":1}"#
        let s = try OrgFiles.decoder().decode(FBSettings.self, from: Data(raw.utf8))
        XCTAssertEqual(s.apiVersion, FBSettings.defaultVersion)
        XCTAssertEqual(s.pages.map(\.id), ["9"])
        XCTAssertEqual(s.drafts.first?.status, .draft)
        XCTAssertEqual(s.drafts.first?.message, "hi")
        XCTAssertNoThrow(try OrgFiles.decoder().decode(FBSettings.self, from: Data("{}".utf8)))
    }

    // MARK: - 錯誤

    func testGraphErrorAndExpiredToken() async throws {
        try await connect()
        FacebookStore.setPageToken("EXPIRED", Self.page1)
        do {
            _ = try await fb.posts(Self.page1)
            XCTFail("應該失敗")
        } catch let e as FBError {
            XCTAssertEqual(e.code, 190)
            XCTAssertEqual(e.subcode, 463)
            XCTAssertTrue(e.isTokenExpired)
            XCTAssertTrue(e.localizedDescription.contains("重新連線"))
        }
        await fb.refreshAll()
        XCTAssertTrue(app.facebook.tokenExpired)
        XCTAssertNotNil(app.facebook.errors[Self.page1])
    }

    func testPagingNextToOtherHostIsIgnored() async throws {
        StubProtocol.handler = { req, body in
            requestsAppend(req, body)
            return (200, ["data": [["id": "a"]], "paging": ["next": "https://evil.example/steal"]])
        }
        let list = try await FBGraph.getAll("v24.0", "/x/posts", token: "T")
        XCTAssertEqual(list.count, 1)
        XCTAssertFalse(Self.requests.contains { $0.url.host == "evil.example" })
    }

    // MARK: - 貼文與成效

    func testPostsWithInsightsAndDeprecatedFallback() async throws {
        try await connect()
        let posts = try await fb.posts(Self.page1, limit: 3)
        XCTAssertEqual(posts.map(\.id), ["1001_1", "1001_2", "1001_3"], "跟著 paging.next")
        let p = posts[0]
        XCTAssertEqual(p.reactions, 7)
        XCTAssertEqual(p.engagement, 10)
        let reach = try XCTUnwrap(p.insights.first { $0.key == "reach" })
        XCTAssertEqual(reach.status, .ok)
        XCTAssertEqual(reach.metric, "post_total_media_view_unique")
        XCTAssertEqual(reach.total, 345)
        XCTAssertEqual(p.insights.first { $0.key == "clicks" }?.total, 12)
        XCTAssertEqual(p.insights.first { $0.key == "reactions" }?.breakdown, ["like": 5, "love": 2])
        // 淘汰的指標記住了,之後的貼文不會再問
        let asked = Self.requests.filter { $0.url.query?.contains("post_impressions_unique") == true }.count
        XCTAssertEqual(asked, 2, "第一篇:一次全部問 + 單獨問一次;之後不再問")
    }

    func testPageInsightsShowDeprecatedAndMissingMetrics() async throws {
        try await connect()
        let r = try await fb.pageInsights(Self.page1, period: "day", days: 7)
        let m = Dictionary(uniqueKeysWithValues: r.metrics.map { ($0.key, $0) })
        XCTAssertEqual(m["reach"]?.status, .ok)
        XCTAssertEqual(m["reach"]?.total, 60, "day 期間加總")
        XCTAssertEqual(m["reach"]?.values.count, 3)
        XCTAssertEqual(m["followers"]?.total, 152, "追蹤數取最後一天")
        XCTAssertEqual(m["engagement"]?.status, .deprecated)
        XCTAssertNil(m["engagement"]?.total)
        XCTAssertTrue(m["engagement"]?.note?.contains("valid insights metric") == true)
        XCTAssertEqual(m["new_followers"]?.status, .deprecated)
        XCTAssertEqual(m["views"]?.status, .ok)
        XCTAssertEqual(r.metrics.count, FB.pageMetrics.count)
    }

    func testMissingMetricIsUnavailableNotError() async throws {
        try await connect()
        let defs = [FB.MetricDef(key: "x", label: "X", candidates: ["page_daily_follows_unique"])]
        let m = try await fb.insights(Self.page1, token: Self.pageToken, defs: defs, query: ["period": "day"])
        XCTAssertEqual(m.first?.status, .unavailable)
        XCTAssertEqual(m.first?.statusLabel, "無資料")
    }

    // MARK: - 排程時間

    func testScheduleBoundaries() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        XCTAssertNoThrow(try FB.validateSchedule(now.addingTimeInterval(600), now: now))
        XCTAssertThrowsError(try FB.validateSchedule(now.addingTimeInterval(599), now: now))
        XCTAssertNoThrow(try FB.validateSchedule(now.addingTimeInterval(30 * 86400), now: now))
        XCTAssertThrowsError(try FB.validateSchedule(now.addingTimeInterval(30 * 86400 + 1), now: now))
        XCTAssertThrowsError(try FB.validateSchedule(now.addingTimeInterval(-60), now: now))
        XCTAssertTrue(FB.taipeiString(Date(timeIntervalSince1970: 0)).hasPrefix("1970/01/01 08:00"), "台北時間 UTC+8")
    }

    // MARK: - 發佈(人)

    private func makeDraft(_ msg: String, link: String = "", image: String = "", local: String = "") -> FBDraft {
        var d = FBDraft(id: fb.newDraftId(), author: "agent:marketing")
        d.pageId = Self.page1
        d.message = msg; d.link = link; d.imageURL = image; d.localImage = local
        return fb.saveDraft(d, by: "agent:marketing")
    }

    func testPublishNowSignsAsPerson() async throws {
        try await connect()
        let d = makeDraft("新課上線", link: "https://evoleap.io/c/1")
        let id = try await fb.publish(d.id)
        XCTAssertEqual(id, "1001_555")
        let r = try XCTUnwrap(graphPosts.last)
        XCTAssertEqual(r.url.path, "/v24.0/1001/feed")
        XCTAssertEqual(r.headers["Authorization"], "Bearer \(Self.pageToken)")
        XCTAssertEqual(r.headers["Content-Type"], "application/x-www-form-urlencoded")
        let f = form(r.body)
        XCTAssertEqual(f["message"], "新課上線")
        XCTAssertEqual(f["link"], "https://evoleap.io/c/1")
        XCTAssertNil(f["published"])
        XCTAssertEqual(fb.draft(d.id)?.status, .published)
        XCTAssertEqual(fb.draft(d.id)?.publishedBy, "tim")
        XCTAssertEqual(app.activity[0].actor, .person("tim"))
        XCTAssertTrue(app.activity[0].text.contains("發佈 Facebook 貼文到「evoleap」"))
        XCTAssertTrue(app.activity[0].text.contains("行銷"))
        // 不能再發一次
        do { _ = try await fb.publish(d.id); XCTFail() } catch {}
    }

    func testScheduleSendsUnpublishedWithTime() async throws {
        try await connect()
        let d = makeDraft("下週見", image: "https://img.example/a.png")
        let when = Date().addingTimeInterval(3600)
        _ = try await fb.publish(d.id, schedule: when)
        let r = try XCTUnwrap(graphPosts.last)
        XCTAssertEqual(r.url.path, "/v24.0/1001/photos")
        let f = form(r.body)
        XCTAssertEqual(f["published"], "false")
        XCTAssertEqual(f["scheduled_publish_time"], String(Int(when.timeIntervalSince1970)))
        XCTAssertEqual(f["url"], "https://img.example/a.png")
        XCTAssertEqual(f["caption"], "下週見")
        XCTAssertEqual(fb.draft(d.id)?.status, .scheduled)
        XCTAssertEqual(fb.draft(d.id)?.postId, "1001_777")
        XCTAssertTrue(app.activity[0].text.contains("排程 Facebook 貼文"))
        XCTAssertTrue(app.activity[0].text.contains("台北"))
    }

    func testLocalImageUsesMultipart() async throws {
        try await connect()
        let img = dataDir.appendingPathComponent("pic.png")
        try Data([0x89, 0x50, 0x4E, 0x47]).write(to: img)
        let d = makeDraft("圖", link: "https://evoleap.io", local: img.path)
        _ = try await fb.publish(d.id)
        let r = try XCTUnwrap(graphPosts.last)
        XCTAssertEqual(r.url.path, "/v24.0/1001/photos")
        XCTAssertTrue(r.headers["Content-Type"]?.hasPrefix("multipart/form-data; boundary=") == true)
        XCTAssertTrue(r.body.contains("filename=\"pic.png\""))
        XCTAssertTrue(r.body.contains("圖\n\nhttps://evoleap.io"), "有圖時連結接在內文後")
    }

    func testPublishNeedsConfirmationAndValidTime() async throws {
        try await connect()
        let d = makeDraft("要確認")
        FacebookStore.confirmOwner = { _ in false }
        do { _ = try await fb.publish(d.id); XCTFail() } catch { XCTAssertTrue(error.localizedDescription.contains("取消")) }
        FacebookStore.confirmOwner = { _ in true }
        do { _ = try await fb.publish(d.id, schedule: Date().addingTimeInterval(300)); XCTFail() } catch {
            XCTAssertTrue(error.localizedDescription.contains("10 分鐘"))
        }
        do { _ = try await fb.publish(d.id, schedule: Date().addingTimeInterval(31 * 86400)); XCTFail() } catch {
            XCTAssertTrue(error.localizedDescription.contains("30 天"))
        }
        XCTAssertTrue(graphPosts.isEmpty)
        XCTAssertEqual(fb.draft(d.id)?.status, .draft)
    }

    func testPublishErrorKeepsDraft() async throws {
        try await connect()
        let d = makeDraft("FAIL")
        do { _ = try await fb.publish(d.id); XCTFail() } catch let e as FBError {
            XCTAssertEqual(e.code, 200)
            XCTAssertTrue(e.localizedDescription.contains("pages_manage_posts"))
        }
        XCTAssertEqual(fb.draft(d.id)?.status, .draft)
    }

    func testCancelScheduled() async throws {
        try await connect()
        var d = makeDraft("排程的")
        d.status = .scheduled
        d.postId = "1001_900"
        fb.saveDraft(d, by: "tim")
        _ = try await fb.scheduled(Self.page1)
        XCTAssertEqual(app.facebook.scheduled[Self.page1]?.first?.scheduledTime, Date(timeIntervalSince1970: 1_800_000_000))
        try await fb.cancelScheduled("1001_900", pageId: Self.page1)
        XCTAssertEqual(Self.requests.last?.method, "DELETE")
        XCTAssertEqual(fb.draft(d.id)?.status, .draft)
        XCTAssertTrue(app.facebook.scheduled[Self.page1]?.isEmpty == true)
    }

    func testDraftsFromCabinetDocAndTask() {
        let doc = CabinetDoc(id: "DOC-9", title: "文案", body: "**新課**上線", section: "docs", author: "tim")
        let a = fb.draftFromDoc(doc)
        XCTAssertEqual(a.message, "新課上線")
        XCTAssertEqual(a.source, "doc:DOC-9")
        var t = AgentTask(id: "T-9", title: "寫貼文", agentId: "marketing", createdBy: "tim")
        t.result = "週末一起學程式!"
        let b = fb.draftFromTask(t)
        XCTAssertEqual(b.author, "agent:marketing")
        XCTAssertEqual(b.source, "task:T-9")
    }

    // MARK: - MCP

    private func rpc(_ method: String, _ params: [String: Any] = [:]) async throws -> [String: Any] {
        nextId += 1
        let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": nextId, "method": method, "params": params]))
        return try XCTUnwrap(try JSONSerialization.jsonObject(with: XCTUnwrap(raw)) as? [String: Any])
    }

    private func tool(_ name: String, _ args: [String: Any] = [:]) async throws -> (Any, Bool) {
        let r = try await rpc("tools/call", ["name": name, "arguments": args])
        let result = try XCTUnwrap(r["result"] as? [String: Any], "\(r)")
        let text = try XCTUnwrap((result["content"] as? [[String: Any]])?.first?["text"] as? String)
        if result["isError"] as? Bool == true { return (text, true) }
        return (try JSONSerialization.jsonObject(with: Data(text.utf8), options: .fragmentsAllowed), false)
    }

    private func ok(_ name: String, _ args: [String: Any] = [:]) async throws -> [String: Any] {
        let (out, isError) = try await tool(name, args)
        XCTAssertFalse(isError, "\(name) 失敗:\(out)")
        return try XCTUnwrap(out as? [String: Any])
    }

    func testMCPToolDefinitionsHaveNoPublish() async throws {
        let r = try await rpc("tools/list")
        let tools = try XCTUnwrap((r["result"] as? [String: Any])?["tools"] as? [[String: Any]])
        let fbTools = tools.filter { ($0["name"] as? String)?.hasPrefix("ops_fb_") == true }
        XCTAssertEqual(fbTools.compactMap { $0["name"] as? String }.sorted(),
                       ["ops_fb_draft_save", "ops_fb_drafts", "ops_fb_insights", "ops_fb_pages", "ops_fb_posts"])
        for t in fbTools {
            let ro = (t["annotations"] as? [String: Any])?["readOnlyHint"] as? Bool
            XCTAssertEqual(ro, t["name"] as? String != "ops_fb_draft_save", "\(t["name"]!)")
        }
        XCTAssertFalse(tools.contains { ($0["name"] as? String ?? "").contains("fb_publish") || ($0["name"] as? String ?? "").contains("fb_schedule") })
        XCTAssertTrue(MCPHandler.instructions.contains("ops_fb_draft_save"))
    }

    func testMCPNotConnectedErrors() async throws {
        for name in ["ops_fb_pages", "ops_fb_posts", "ops_fb_insights"] {
            let (out, isError) = try await tool(name)
            XCTAssertTrue(isError, name)
            XCTAssertTrue((out as? String)?.contains("AI 自動化 › 粉絲專頁") == true, name)
        }
        // 草稿是本機資料,沒連線也能用
        let (_, isError) = try await tool("ops_fb_drafts")
        XCTAssertFalse(isError)
    }

    func testMCPReadTools() async throws {
        try await connect()
        let pages = try await ok("ops_fb_pages")
        XCTAssertEqual(pages["connected"] as? Bool, true)
        XCTAssertEqual(pages["mode"] as? String, "page_token")
        let p = try XCTUnwrap((pages["pages"] as? [[String: Any]])?.first)
        XCTAssertEqual(p["id"] as? String, Self.page1)
        XCTAssertEqual(p["followersCount"] as? Int, 150)
        XCTAssertEqual(p["hasToken"] as? Bool, true)
        XCTAssertNil(p["accessToken"])
        XCTAssertFalse(MCPJSON.string(pages).contains("EAAG"))

        let posts = try await ok("ops_fb_posts", ["page": "evoleap", "limit": 2])
        let pp = try XCTUnwrap((posts["pages"] as? [[String: Any]])?.first)
        let list = try XCTUnwrap(pp["posts"] as? [[String: Any]])
        XCTAssertEqual(list.count, 2)
        XCTAssertEqual(list[0]["engagement"] as? Int, 10)
        let ins = try XCTUnwrap(list[0]["insights"] as? [[String: Any]])
        XCTAssertEqual(ins.first { $0["key"] as? String == "reach" }?["status"] as? String, "ok")
        XCTAssertEqual((pp["scheduled"] as? [[String: Any]])?.first?["scheduledTime"] as? String, "2027-01-15T08:00:00Z")

        let insights = try await ok("ops_fb_insights", ["period": "day", "days": 7])
        let ip = try XCTUnwrap((insights["pages"] as? [[String: Any]])?.first)
        let metrics = try XCTUnwrap(ip["metrics"] as? [[String: Any]])
        let eng = try XCTUnwrap(metrics.first { $0["key"] as? String == "engagement" })
        XCTAssertEqual(eng["status"] as? String, "deprecated")
        XCTAssertTrue(eng["total"] is NSNull)
        XCTAssertEqual(metrics.first { $0["key"] as? String == "reach" }?["total"] as? Double, 60)
        let (bad, isError) = try await tool("ops_fb_insights", ["period": "month"])
        XCTAssertTrue(isError, "\(bad)")
        XCTAssertTrue(graphPosts.isEmpty, "讀取工具不會送出 POST")
    }

    func testMCPDraftSaveSignsAgentAndCannotPublish() async throws {
        try await connect()
        let d = try await ok("ops_fb_draft_save", ["message": "週末限定", "link": "https://evoleap.io",
                                                    "proposed_time": "2026-11-20T20:00:00+08:00", "agent": "marketing"])
        let id = try XCTUnwrap(d["id"] as? String)
        XCTAssertEqual(d["author"] as? String, "agent:marketing")
        XCTAssertEqual(d["authorName"] as? String, "行銷")
        XCTAssertEqual(d["status"] as? String, "draft")
        XCTAssertEqual(d["pageId"] as? String, Self.page1)
        XCTAssertEqual(d["proposedTime"] as? String, "2026-11-20T12:00:00Z")
        XCTAssertNotNil(d["note"])
        XCTAssertEqual(app.activity[0].actor, .ai("marketing"))
        XCTAssertTrue(app.activity[0].text.contains("新增 Facebook 草稿"))

        // 修改
        let u = try await ok("ops_fb_draft_save", ["id": id, "message": "週末限定!"])
        XCTAssertEqual(u["message"] as? String, "週末限定!")
        XCTAssertEqual(u["author"] as? String, "agent:marketing", "修改不換撰寫者")

        // 沒帶 agent 署名 MCP 用戶端
        let m = try await ok("ops_fb_draft_save", ["message": "另一篇"])
        XCTAssertEqual(m["author"] as? String, "mcp")

        // 錯誤:未知角色、空白、壞網址、已發佈的不能改
        var (out, isError) = try await tool("ops_fb_draft_save", ["message": "x", "agent": "nobody"])
        XCTAssertTrue(isError); XCTAssertTrue((out as? String)?.contains("找不到角色") == true)
        (out, isError) = try await tool("ops_fb_draft_save", [:])
        XCTAssertTrue(isError)
        (out, isError) = try await tool("ops_fb_draft_save", ["message": "x", "link": "javascript:alert(1)"])
        XCTAssertTrue(isError)
        _ = try await fb.publish(id)
        (out, isError) = try await tool("ops_fb_draft_save", ["id": id, "message": "改"])
        XCTAssertTrue(isError); XCTAssertTrue((out as? String)?.contains("不能修改") == true)

        // MCP 存草稿不會碰 Graph API 的寫入
        XCTAssertEqual(graphPosts.count, 1, "只有人按的那一次發佈")

        let list = try await ok("ops_fb_drafts")
        XCTAssertEqual(list["total"] as? Int, 1)
        let all = try await ok("ops_fb_drafts", ["status": "all"])
        XCTAssertEqual(all["total"] as? Int, 2)
        let one = try await ok("ops_fb_drafts", ["id": id])
        XCTAssertEqual(one["status"] as? String, "published")
        XCTAssertEqual(one["postId"] as? String, "1001_555")
    }
}

/// 給替換過的 handler 用的記錄
private func requestsAppend(_ req: URLRequest, _ body: Data) {
    FacebookTests.requests.append(.init(method: req.httpMethod ?? "GET", url: req.url!, headers: req.allHTTPHeaderFields ?? [:],
                                        body: String(decoding: body, as: UTF8.self)))
}
