import Foundation
import LocalAuthentication

// Facebook 粉絲專頁:連線(Meta 登入或貼上權杖)、讀貼文與成效、草稿、由人發佈與排程。

@MainActor
final class FacebookStore {
    let app: AppState
    init(app: AppState) { self.app = app }

    /// 發佈與排程前確認是本人(Touch ID 或電腦密碼,和旁白發佈一樣)。測試會換掉
    nonisolated(unsafe) static var confirmOwner: @MainActor (String) async -> Bool = { reason in
        let ctx = LAContext()
        var err: NSError?
        guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: &err) else { return true }
        return (try? await ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)) ?? false
    }

    static let notConnected = "omixor 還沒連上 Facebook 粉絲專頁。請人在 omixor 的 AI 自動化 › 粉絲專頁 設定連線(Meta 登入或貼上 Page access token),並勾選要管理的專頁。"

    private var fileURL: URL { app.dataDir.appendingPathComponent("facebook.json") }
    private var version: String { app.fbSettings.apiVersion }
    /// 這次執行中確認過已淘汰或不能用的指標,之後就不再問
    private(set) var deadMetrics: [String: FBError] = [:]

    // MARK: - 存檔

    func load() {
        app.fbSettings = (try? Data(contentsOf: fileURL)).flatMap { try? OrgFiles.decoder().decode(FBSettings.self, from: $0) } ?? FBSettings()
        app.facebook = FBSnapshot()
    }

    func save() {
        if let d = try? OrgFiles.encoder().encode(app.fbSettings) { try? d.write(to: fileURL, options: .atomic) }
    }

    // MARK: - 權杖(鑰匙圈,帶組織前綴)

    private static func pageAccount(_ id: String) -> String { "\(TokenVault.scope)facebook:page:\(id)" }

    static func pageToken(_ pageId: String) -> String? {
        TokenVault.data(pageAccount(pageId)).map { String(decoding: $0, as: UTF8.self) }.flatMap { $0.isEmpty ? nil : $0 }
    }

    static func setPageToken(_ token: String?, _ pageId: String) {
        TokenVault.set(token.map { Data($0.utf8) }, pageAccount(pageId))
    }

    var isConnected: Bool { app.fbSettings.managedPages.contains { Self.pageToken($0.id) != nil } }

    func page(_ ref: String?) -> FBPage? {
        guard let ref, !ref.isEmpty else { return nil }
        return app.fbSettings.pages.first { $0.id == ref } ?? app.fbSettings.pages.first { $0.name.caseInsensitiveCompare(ref) == .orderedSame }
    }

    /// 連線用的 Connection(Meta App 的 App ID 與 App secret 存在這裡,secret 在鑰匙圈)
    @discardableResult
    func ensureConnection() -> Connection {
        app.startConnections()
        if let c = app.connections.first(where: { $0.id == FB.connectionId }) { return c }
        let c = FB.presetConnection(version: version)
        app.connStore?.upsert(c)
        return c
    }

    func setApp(id: String, secret: String?) {
        var c = ensureConnection()
        c.oauth.clientId = id.trimmingCharacters(in: .whitespacesAndNewlines)
        c.oauth.authorizationEndpoint = "https://www.facebook.com/\(version)/dialog/oauth"
        c.oauth.tokenEndpoint = "https://\(FB.host)/\(version)/oauth/access_token"
        c.oauth.registeredPort = FB.callbackPort
        app.connStore?.upsert(c)
        if let secret, !secret.isEmpty { app.connStore?.setClientSecret(secret, for: c.id) }
    }

    /// 中斷連線:清掉所有權杖(專頁清單與草稿保留)
    func disconnect() {
        for p in app.fbSettings.pages { Self.setPageToken(nil, p.id) }
        TokenVault.setTokens(nil, FB.connectionId)
        app.fbSettings.mode = ""
        save()
        app.facebook = FBSnapshot()
        app.log(.person(app.me), "xmark.circle", "中斷 Facebook 粉絲專頁連線", .automation)
    }

    // MARK: - 連線

    /// (a) Meta 登入:OAuth 授權碼 → 長期使用者權杖 → 各專頁的 Page access token
    func loginWithMeta() async throws {
        let c = ensureConnection()
        guard !c.oauth.clientId.isEmpty, TokenVault.secret(c.id) != nil else { throw FBError("先填 Meta App 的 App ID 與 App secret") }
        let (tokens, settings) = try await OAuthConnector.login(c)
        var n = c
        n.oauth = settings
        app.connStore?.upsert(n)
        try await completeLogin(userToken: tokens.accessToken)
    }

    /// 短期使用者權杖換長期,再讀管理的專頁與各自的權杖
    func completeLogin(userToken: String) async throws {
        let c = ensureConnection()
        guard let secret = TokenVault.secret(c.id) else { throw FBError("鑰匙圈裡沒有 App secret") }
        let r = try await FBGraph.send("GET", FBGraph.url(version, "/oauth/access_token", [
            "grant_type": "fb_exchange_token", "client_id": c.oauth.clientId, "client_secret": secret, "fb_exchange_token": userToken,
        ]), token: nil)
        guard let long = r["access_token"] as? String else { throw FBError("換長期權杖失敗:回應沒有 access_token") }
        let expires = (r["expires_in"] as? NSNumber).map { Date().addingTimeInterval($0.doubleValue) }
        TokenVault.setTokens(TokenSet(accessToken: long, expiresAt: expires, tokenType: "bearer"), c.id)
        try await loadAccounts(userToken: long)
        app.fbSettings.mode = "oauth"
        save()
        app.log(.person(app.me), "person.badge.key", "連線 Facebook 粉絲專頁(Meta 登入,\(app.fbSettings.pages.count) 個專頁)", .automation)
    }

    private func loadAccounts(userToken: String) async throws {
        let list = try await FBGraph.getAll(version, "/me/accounts", token: userToken,
                                            query: ["fields": FB.pageFields + ",access_token", "limit": "50"])
        guard !list.isEmpty else { throw FBError("這個帳號沒有可以管理的粉絲專頁(或沒有授權 pages_show_list)") }
        mergePages(list.map { d -> (FBPage, String?) in (FB.page(d), d["access_token"] as? String) })
    }

    private func mergePages(_ found: [(FBPage, String?)]) {
        var pages = app.fbSettings.pages
        for (p, token) in found where !p.id.isEmpty {
            var p = p
            if let old = pages.first(where: { $0.id == p.id }) { p.managed = old.managed }
            pages.removeAll { $0.id == p.id }
            pages.append(p)
            if let token { Self.setPageToken(token, p.id) }
        }
        app.fbSettings.pages = pages
        app.facebook.tokenExpired = false
        app.facebook.error = nil
    }

    /// (b) 直接貼權杖:Page access token 就是那個專頁;貼的是使用者權杖就讀它管理的專頁
    func addToken(_ raw: String) async throws {
        let token = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !token.isEmpty else { throw FBError("權杖是空的") }
        let me = try await FBGraph.get(version, "/me", token: token, query: ["fields": "id,name,category,link,fan_count,followers_count,picture{url}"])
        if me["category"] != nil {
            mergePages([(FB.page(me), token)])
        } else {
            try await loadAccounts(userToken: token)
        }
        app.fbSettings.mode = app.fbSettings.mode.isEmpty ? "page_token" : app.fbSettings.mode
        save()
        app.log(.person(app.me), "key", "設定 Facebook 粉絲專頁權杖(\(me["name"] as? String ?? "?"))", .automation)
    }

    func setManaged(_ pageId: String, _ on: Bool) {
        guard let i = app.fbSettings.pages.firstIndex(where: { $0.id == pageId }) else { return }
        app.fbSettings.pages[i].managed = on
        save()
    }

    // MARK: - 讀資料

    private func token(_ pageId: String) throws -> String {
        guard let t = Self.pageToken(pageId) else { throw FBError(Self.notConnected) }
        return t
    }

    private func noteError(_ e: Error, page: String) {
        if let fe = e as? FBError, fe.isTokenExpired { app.facebook.tokenExpired = true }
        app.facebook.errors[page] = e.localizedDescription
    }

    func refreshPage(_ pageId: String) async throws -> FBPage {
        let d = try await FBGraph.get(version, "/\(pageId)", token: try token(pageId), query: ["fields": FB.pageFields])
        var p = FB.page(d)
        if let i = app.fbSettings.pages.firstIndex(where: { $0.id == pageId }) {
            p.managed = app.fbSettings.pages[i].managed
            app.fbSettings.pages[i] = p
            save()
        }
        return p
    }

    /// 最近貼文(含心情 / 留言 / 分享數);withInsights 時另外讀前幾篇的觸及、點擊、心情分類
    func posts(_ pageId: String, limit: Int = 10, withInsights: Bool = true) async throws -> [FBPost] {
        let t = try token(pageId)
        var posts = try await FBGraph.getAll(version, "/\(pageId)/posts", token: t,
                                             query: ["fields": FB.postFields, "limit": String(min(limit, 100))],
                                             maxPages: 3, limit: limit).map { FB.post($0, pageId: pageId) }
        if withInsights {
            for i in posts.indices.prefix(10) {
                posts[i].insights = try await insights(posts[i].id, token: t, defs: FB.postMetrics, query: ["period": "lifetime"])
            }
        }
        app.facebook.posts[pageId] = posts
        app.facebook.errors[pageId] = nil
        return posts
    }

    func scheduled(_ pageId: String) async throws -> [FBPost] {
        let list = try await FBGraph.getAll(version, "/\(pageId)/scheduled_posts", token: try token(pageId),
                                            query: ["fields": FB.scheduledFields, "limit": "50"], maxPages: 3)
            .map { d -> FBPost in var p = FB.post(d, pageId: pageId); p.isPublished = false; return p }
            .sorted { ($0.scheduledTime ?? .distantFuture) < ($1.scheduledTime ?? .distantFuture) }
        app.facebook.scheduled[pageId] = list
        return list
    }

    func pageInsights(_ pageId: String, period: String = "day", days: Int = 28) async throws -> FBPageInsights {
        let days = min(max(days, 1), 90)
        let until = Date()
        let since = until.addingTimeInterval(-Double(days) * 86400)
        let m = try await insights(pageId, token: try token(pageId), defs: FB.pageMetrics, query: [
            "period": FB.periods.contains(period) ? period : "day",
            "since": String(Int(since.timeIntervalSince1970)), "until": String(Int(until.timeIntervalSince1970)),
        ], sumDaily: period == "day")
        let r = FBPageInsights(pageId: pageId, period: period, since: since, until: until, metrics: m)
        app.facebook.insights[pageId] = r
        return r
    }

    /// 讀 insights:先一次問全部候選指標;Meta 說有指標無效(code 100)就逐一問,問不到的標成已淘汰,不讓整頁壞掉
    func insights(_ objectId: String, token: String, defs: [FB.MetricDef], query: [String: String],
                  sumDaily: Bool = false) async throws -> [FBMetric] {
        let all = Array(NSOrderedSet(array: defs.flatMap(\.candidates))) as? [String] ?? []
        let ask = all.filter { deadMetrics[$0] == nil }
        var values: [String: [(Date?, Any)]] = [:]
        var failed = deadMetrics
        if !ask.isEmpty {
            do {
                var q = query
                q["metric"] = ask.joined(separator: ",")
                values = FB.insightValues(try await FBGraph.get(version, "/\(objectId)/insights", token: token, query: q)["data"] as? [[String: Any]] ?? [])
            } catch let e as FBError where !e.isTokenExpired {
                // 有指標被淘汰(code 100)或沒權限:逐一問,能拿到的照樣顯示
                for m in ask {
                    var q = query
                    q["metric"] = m
                    do {
                        let data = try await FBGraph.get(version, "/\(objectId)/insights", token: token, query: q)["data"] as? [[String: Any]] ?? []
                        values.merge(FB.insightValues(data)) { a, _ in a }
                    } catch let e as FBError where !e.isTokenExpired {
                        failed[m] = e
                        if e.isInvalidMetric { deadMetrics[m] = e }
                    }
                }
            }
        }
        return defs.map { def in
            if let name = def.candidates.first(where: { values[$0]?.isEmpty == false }), let vals = values[name] {
                var metric = FBMetric(key: def.key, label: def.label, metric: name, status: .ok)
                var breakdown: [String: Double] = [:]
                for (d, v) in vals {
                    if let n = v as? NSNumber {
                        if let d { metric.values.append(.init(date: d, value: n.doubleValue)) } else { metric.values.append(.init(date: .now, value: n.doubleValue)) }
                    } else if let dict = v as? [String: Any] {
                        for (k, x) in dict { if let n = x as? NSNumber { breakdown[k, default: 0] += n.doubleValue } }
                    }
                }
                if !breakdown.isEmpty {
                    metric.breakdown = breakdown
                    metric.total = breakdown.values.reduce(0, +)
                } else if def.latest || !sumDaily {
                    metric.total = metric.values.last?.value
                } else {
                    metric.total = metric.values.reduce(0) { $0 + $1.value }
                }
                return metric
            }
            let errs = def.candidates.compactMap { failed[$0] }
            if let e = errs.first {
                return FBMetric(key: def.key, label: def.label, metric: nil, status: e.isInvalidMetric ? .deprecated : .unavailable,
                                total: nil, note: e.message)
            }
            return FBMetric(key: def.key, label: def.label, metric: nil, status: .unavailable, total: nil, note: "Meta 沒有回傳這個指標的資料")
        }
    }

    /// 畫面用:重新讀所有管理中專頁的資料;單一專頁失敗不影響其他
    func refreshAll() async {
        guard !app.facebook.busy else { return }
        app.facebook.busy = true
        defer { app.facebook.busy = false; app.facebook.fetchedAt = .now }
        app.facebook.error = isConnected ? nil : Self.notConnected
        for p in app.fbSettings.managedPages where Self.pageToken(p.id) != nil {
            do {
                _ = try await refreshPage(p.id)
                _ = try await posts(p.id)
                _ = try await scheduled(p.id)
                _ = try await pageInsights(p.id)
            } catch {
                noteError(error, page: p.id)
            }
        }
    }

    // MARK: - 草稿

    func draft(_ id: String) -> FBDraft? { app.fbSettings.drafts.first { $0.id.caseInsensitiveCompare(id) == .orderedSame } }

    func newDraftId() -> String { "FD-\((app.fbSettings.drafts.map(\.number).max() ?? 0) + 1)" }

    @discardableResult
    func saveDraft(_ d: FBDraft, by author: String) -> FBDraft {
        var d = d
        d.updatedAt = .now
        if let i = app.fbSettings.drafts.firstIndex(where: { $0.id == d.id }) {
            app.fbSettings.drafts[i] = d
        } else {
            d.author = author
            d.createdAt = .now
            app.fbSettings.drafts.insert(d, at: 0)
        }
        save()
        return d
    }

    func deleteDraft(_ id: String) {
        app.fbSettings.drafts.removeAll { $0.id == id }
        save()
    }

    /// 從文件櫃的文件帶入
    func draftFromDoc(_ doc: CabinetDoc) -> FBDraft {
        var d = FBDraft(id: newDraftId(), author: app.me)
        d.message = LineAPI.plainText(doc.body.isEmpty ? doc.title : doc.body)
        d.source = "doc:\(doc.id)"
        d.pageId = app.fbSettings.managedPages.first?.id ?? ""
        return saveDraft(d, by: app.me)
    }

    /// 從角色任務的結果帶入
    func draftFromTask(_ t: AgentTask) -> FBDraft {
        var d = FBDraft(id: newDraftId(), author: IssueStore.agentPrefix + t.agentId)
        d.message = LineAPI.plainText(t.result.isEmpty ? t.title : t.result)
        d.source = "task:\(t.id)"
        d.pageId = app.fbSettings.managedPages.first?.id ?? ""
        return saveDraft(d, by: IssueStore.agentPrefix + t.agentId)
    }

    // MARK: - 發佈(只有人在 app 裡按)

    /// 立即發佈(schedule = nil)或排程。發佈前一定要過 confirmOwner
    @discardableResult
    func publish(_ draftId: String, schedule: Date? = nil, now: Date = .now) async throws -> String {
        guard let d = draft(draftId) else { throw FBError("找不到草稿 \(draftId)") }
        guard d.status == .draft else { throw FBError("這份草稿已經\(d.status.label)") }
        guard !d.isEmpty else { throw FBError("草稿是空的:至少要有文字、連結或圖片") }
        guard let page = page(d.pageId) else { throw FBError("先選要發到哪個粉絲專頁") }
        let token = try token(page.id)
        if let schedule { try FB.validateSchedule(schedule, now: now) }
        let reason = schedule.map { "排程 Facebook 貼文到「\(page.name)」,\(FB.taipeiString($0))" } ?? "發佈 Facebook 貼文到「\(page.name)」"
        guard await Self.confirmOwner(reason) else { throw FBError("取消(未通過身分確認)") }

        var fields: [String: String] = [:]
        if let schedule {
            fields["published"] = "false"
            fields["scheduled_publish_time"] = String(Int(schedule.timeIntervalSince1970))
        }
        let caption = [d.message, d.imageURL.isEmpty && d.localImage.isEmpty ? "" : d.link]
            .filter { !$0.isEmpty }.joined(separator: "\n\n")
        let result: [String: Any]
        if !d.localImage.isEmpty {
            let url = URL(fileURLWithPath: d.localImage)
            guard let data = try? Data(contentsOf: url) else { throw FBError("讀不到圖片 \(url.lastPathComponent)") }
            fields["caption"] = caption
            let mime = ["png": "image/png", "gif": "image/gif", "heic": "image/heic"][url.pathExtension.lowercased()] ?? "image/jpeg"
            result = try await FBGraph.postMultipart(version, "/\(page.id)/photos", token: token, fields: fields,
                                                     file: ("source", url.lastPathComponent, mime, data))
        } else if !d.imageURL.isEmpty {
            fields["url"] = d.imageURL
            fields["caption"] = caption
            result = try await FBGraph.postForm(version, "/\(page.id)/photos", token: token, fields)
        } else {
            if !d.message.isEmpty { fields["message"] = d.message }
            if !d.link.isEmpty { fields["link"] = d.link }
            result = try await FBGraph.postForm(version, "/\(page.id)/feed", token: token, fields)
        }
        let postId = result["post_id"] as? String ?? result["id"] as? String ?? ""
        var n = d
        n.postId = postId
        n.status = schedule == nil ? .published : .scheduled
        n.scheduledFor = schedule
        n.publishedAt = schedule == nil ? .now : nil
        n.publishedBy = app.me
        saveDraft(n, by: app.me)
        let by = d.author == app.me ? "" : "(草稿:\(authorName(d.author)))"
        if let schedule {
            app.log(.person(app.me), "calendar.badge.clock", "排程 Facebook 貼文到「\(page.name)」:\(FB.taipeiString(schedule))「\(d.titleLine)」\(by)", .automation)
        } else {
            app.log(.person(app.me), "paperplane", "發佈 Facebook 貼文到「\(page.name)」「\(d.titleLine)」\(by)", .automation)
        }
        return postId
    }

    /// 取消排程:刪掉還沒發佈的排程貼文
    func cancelScheduled(_ postId: String, pageId: String) async throws {
        _ = try await FBGraph.delete(version, "/\(postId)", token: try token(pageId))
        app.facebook.scheduled[pageId]?.removeAll { $0.id == postId }
        if let i = app.fbSettings.drafts.firstIndex(where: { $0.postId == postId }) {
            app.fbSettings.drafts[i].status = .draft
            app.fbSettings.drafts[i].postId = nil
            app.fbSettings.drafts[i].scheduledFor = nil
            save()
        }
        app.log(.person(app.me), "calendar.badge.minus", "取消 Facebook 排程貼文 \(postId)(\(page(pageId)?.name ?? pageId))", .automation)
    }

    func authorName(_ author: String) -> String {
        if author == IssueStore.mcpAuthor { return "MCP 用戶端" }
        if author.hasPrefix(IssueStore.agentPrefix) {
            let id = String(author.dropFirst(IssueStore.agentPrefix.count))
            return app.agents.first { $0.id == id }?.name ?? id
        }
        return app.person(author)?.name ?? author
    }

    func actor(_ author: String) -> Actor {
        if author == IssueStore.mcpAuthor { return .mcp }
        if author.hasPrefix(IssueStore.agentPrefix) { return .ai(String(author.dropFirst(IssueStore.agentPrefix.count))) }
        return .person(author)
    }
}
