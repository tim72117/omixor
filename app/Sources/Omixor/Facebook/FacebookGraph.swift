import Foundation

// Meta Graph API 的 HTTP:一律走 ConnHTTP(測試時用 URLProtocol 攔截),權杖放在 Authorization: Bearer 標頭,
// 只送到 graph.facebook.com;分頁的 paging.next 也只跟同一台主機。

enum FB {
    static let connectionId = "facebook"
    static let host = "graph.facebook.com"
    static let scopes = "pages_show_list,pages_read_engagement,pages_manage_posts,read_insights"
    /// OAuth 回呼固定用這個埠,Meta App 的「有效的 OAuth 重新導向 URI」要填 http://127.0.0.1:3767/callback
    static let callbackPort: UInt16 = 3767
    static var callbackURL: String { "http://127.0.0.1:\(callbackPort)/callback" }
    static let taipei = TimeZone(identifier: "Asia/Taipei")!

    /// 排程限制:10 分鐘到 30 天後
    static let minLead: TimeInterval = 10 * 60
    static let maxLead: TimeInterval = 30 * 86400

    static func isVersion(_ v: String) -> Bool {
        guard v.hasPrefix("v"), let n = Double(v.dropFirst()) else { return false }
        return n >= 21
    }

    static func validateSchedule(_ date: Date, now: Date = .now) throws {
        let lead = date.timeIntervalSince(now)
        if lead < minLead { throw FBError("排程時間至少要在 10 分鐘後(\(taipeiString(now.addingTimeInterval(minLead))) 之後)") }
        if lead > maxLead { throw FBError("排程時間最多只能到 30 天後(\(taipeiString(now.addingTimeInterval(maxLead))) 之前)") }
    }

    static func taipeiString(_ d: Date) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "zh_TW")
        f.timeZone = taipei
        f.dateFormat = "yyyy/MM/dd HH:mm"
        return f.string(from: d) + "(台北)"
    }

    static func presetConnection(version: String) -> Connection {
        var c = Connection(id: connectionId, name: "Facebook", baseURL: "https://\(host)", isProduction: true,
                           testPath: "", auth: .oauth, note: "AI 自動化 › 粉絲專頁:發文、排程與成效")
        c.oauth = OAuthSettings(issuer: "https://www.facebook.com",
                                authorizationEndpoint: "https://www.facebook.com/\(version)/dialog/oauth",
                                tokenEndpoint: "https://\(host)/\(version)/oauth/access_token",
                                scopes: scopes, registeredPort: callbackPort)
        return c
    }

    // MARK: 成效指標(依序嘗試,前面的被淘汰就用後面的)

    struct MetricDef {
        let key: String
        let label: String
        let candidates: [String]
        /// 期間合計用加總;追蹤數這種累計值用最後一天
        var latest = false
    }

    static let pageMetrics: [MetricDef] = [
        MetricDef(key: "reach", label: "觸及人數", candidates: ["page_impressions_unique", "page_total_media_view_unique"]),
        MetricDef(key: "views", label: "瀏覽次數", candidates: ["page_media_view", "page_impressions"]),
        MetricDef(key: "engagement", label: "貼文互動", candidates: ["page_post_engagements"]),
        MetricDef(key: "followers", label: "追蹤數", candidates: ["page_follows", "page_fans"], latest: true),
        MetricDef(key: "new_followers", label: "新增追蹤", candidates: ["page_daily_follows_unique", "page_fan_adds_unique"]),
    ]

    static let postMetrics: [MetricDef] = [
        MetricDef(key: "reach", label: "觸及人數", candidates: ["post_impressions_unique", "post_total_media_view_unique"]),
        MetricDef(key: "clicks", label: "點擊", candidates: ["post_clicks"]),
        MetricDef(key: "reactions", label: "心情", candidates: ["post_reactions_by_type_total"]),
    ]

    static let periods = ["day", "week", "days_28"]

    // MARK: 解析

    private nonisolated(unsafe) static let iso: ISO8601DateFormatter = ISO8601DateFormatter()
    /// Graph 的時間:2026-11-10T08:00:00+0000
    private nonisolated(unsafe) static let graphDate: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd'T'HH:mm:ssZ"
        return f
    }()

    static func date(_ v: Any?) -> Date? {
        if let n = v as? NSNumber { return Date(timeIntervalSince1970: n.doubleValue) }
        guard let s = v as? String else { return nil }
        return graphDate.date(from: s) ?? iso.date(from: s)
    }

    private static func str(_ v: Any?) -> String { v as? String ?? "" }
    private static func count(_ v: Any?, _ key: String = "total_count") -> Int? {
        if let d = v as? [String: Any] {
            if let s = d["summary"] as? [String: Any], let n = s[key] as? NSNumber { return n.intValue }
            if let n = d["count"] as? NSNumber { return n.intValue }
        }
        return nil
    }

    static func page(_ d: [String: Any], managed: Bool = true) -> FBPage {
        let pic = ((d["picture"] as? [String: Any])?["data"] as? [String: Any])?["url"] as? String ?? ""
        return FBPage(id: str(d["id"]), name: str(d["name"]), category: str(d["category"]), link: str(d["link"]),
                      picture: pic, fanCount: (d["fan_count"] as? NSNumber)?.intValue,
                      followersCount: (d["followers_count"] as? NSNumber)?.intValue, managed: managed)
    }

    static func post(_ d: [String: Any], pageId: String) -> FBPost {
        FBPost(id: str(d["id"]), pageId: pageId, message: str(d["message"]), createdTime: date(d["created_time"]),
               scheduledTime: date(d["scheduled_publish_time"]), permalink: str(d["permalink_url"]),
               picture: str(d["full_picture"]), isPublished: d["is_published"] as? Bool ?? true,
               statusType: str(d["status_type"]), reactions: count(d["reactions"]), comments: count(d["comments"]),
               shares: count(d["shares"]))
    }

    static let postFields = "id,message,created_time,permalink_url,full_picture,is_published,status_type,"
        + "reactions.summary(total_count).limit(0),comments.summary(total_count).limit(0),shares"
    static let scheduledFields = "id,message,created_time,scheduled_publish_time,permalink_url,full_picture,is_published"
    static let pageFields = "id,name,category,link,fan_count,followers_count,picture{url}"

    /// insights 的 data 陣列 → 指標名稱 → 每天的值(數字)或分類(心情)
    static func insightValues(_ data: [[String: Any]]) -> [String: [(Date?, Any)]] {
        var out: [String: [(Date?, Any)]] = [:]
        for m in data {
            let name = str(m["name"])
            let vals = (m["values"] as? [[String: Any]] ?? []).compactMap { v -> (Date?, Any)? in
                guard let value = v["value"] else { return nil }
                return (date(v["end_time"]), value)
            }
            out[name, default: []] += vals
        }
        return out
    }
}

/// Graph API 請求
enum FBGraph {
    static func url(_ version: String, _ path: String, _ query: [String: String] = [:]) -> URL {
        var comps = URLComponents()
        comps.scheme = "https"
        comps.host = FB.host
        comps.path = "/\(version)" + (path.hasPrefix("/") ? path : "/" + path)
        if !query.isEmpty { comps.queryItems = query.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) } }
        return comps.url!
    }

    /// 解析回應:錯誤是 {error:{message, type, code, error_subcode}}
    static func check(_ r: ConnHTTP.Response) throws -> [String: Any] {
        let obj = r.json as? [String: Any] ?? [:]
        if let e = obj["error"] as? [String: Any] {
            throw FBError(e["message"] as? String ?? "Graph API 錯誤", code: (e["code"] as? NSNumber)?.intValue ?? 0,
                          subcode: (e["error_subcode"] as? NSNumber)?.intValue ?? 0, status: r.status)
        }
        guard r.ok else { throw FBError("HTTP \(r.status):\(ConnHTTP.errorMessage(r))", status: r.status) }
        return obj
    }

    private static func headers(_ token: String) -> [String: String] { ["Authorization": "Bearer \(token)"] }

    static func send(_ method: String, _ url: URL, token: String?, body: Data? = nil, contentType: String? = nil) async throws -> [String: Any] {
        guard url.host?.lowercased() == FB.host else { throw FBError("不會把權杖送到 \(url.host ?? "?")") }
        if NSClassFromString("XCTestCase") != nil, ConnHTTP.protocolClasses.isEmpty {
            throw FBError("測試中沒有攔截器,不送出 Graph API 請求")
        }
        var h = token.map(headers) ?? [:]
        if let contentType { h["Content-Type"] = contentType }
        do {
            return try check(try await ConnHTTP.send(method, url, headers: h, body: body))
        } catch let e as FBError {
            throw e
        } catch {
            throw FBError("連不上 Facebook:\(error.localizedDescription)")
        }
    }

    static func get(_ version: String, _ path: String, token: String, query: [String: String] = [:]) async throws -> [String: Any] {
        try await send("GET", url(version, path, query), token: token)
    }

    /// 跟著 paging.next 讀完(最多 maxPages 頁);next 只接受 graph.facebook.com
    static func getAll(_ version: String, _ path: String, token: String, query: [String: String] = [:],
                       maxPages: Int = 5, limit: Int? = nil) async throws -> [[String: Any]] {
        var out: [[String: Any]] = []
        var next: URL? = url(version, path, query)
        var pages = 0
        while let u = next, pages < maxPages {
            pages += 1
            let obj = try await send("GET", u, token: token)
            out += obj["data"] as? [[String: Any]] ?? []
            if let limit, out.count >= limit { return Array(out.prefix(limit)) }
            next = ((obj["paging"] as? [String: Any])?["next"] as? String).flatMap(URL.init(string:))
            if let n = next, n.host?.lowercased() != FB.host { next = nil }
        }
        return out
    }

    static func postForm(_ version: String, _ path: String, token: String, _ fields: [String: String]) async throws -> [String: Any] {
        try await send("POST", url(version, path), token: token, body: Data(ConnHTTP.formEncode(fields).utf8),
                       contentType: "application/x-www-form-urlencoded")
    }

    static func postMultipart(_ version: String, _ path: String, token: String, fields: [String: String],
                              file: (name: String, filename: String, mime: String, data: Data)) async throws -> [String: Any] {
        let boundary = "ops-\(UUID().uuidString)"
        var body = Data()
        for (k, v) in fields.sorted(by: { $0.key < $1.key }) {
            body += Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"\(k)\"\r\n\r\n\(v)\r\n".utf8)
        }
        body += Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"\(file.name)\"; filename=\"\(file.filename)\"\r\nContent-Type: \(file.mime)\r\n\r\n".utf8)
        body += file.data
        body += Data("\r\n--\(boundary)--\r\n".utf8)
        return try await send("POST", url(version, path), token: token, body: body, contentType: "multipart/form-data; boundary=\(boundary)")
    }

    static func delete(_ version: String, _ path: String, token: String) async throws -> [String: Any] {
        try await send("DELETE", url(version, path), token: token)
    }
}
