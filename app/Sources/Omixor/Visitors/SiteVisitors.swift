import Foundation

// 網站訪客:evoleap 後台的會員與訪客統計(GET /api/admin/overview?range=…),透過「連線」以管理員身分讀取。
// 欄位對應 evoleap 的 server/internal/store/admin.go(AdminOverview);缺欄位時用預設值,伺服器加欄位也不會壞。

struct SiteCount: Codable, Hashable {
    var name = ""
    var count = 0
}

struct SiteGuestEvent: Codable, Hashable {
    var t = 0
    var e = ""
}

struct SiteLessonStat: Codable, Hashable, Identifiable {
    var lesson = ""
    var learners = 0
    var completed = 0
    var avgPercent = 0.0
    var guestViews = 0
    var guestVisitors = 0
    var guestWatchedMs: Int64 = 0
    var guestBots = 0
    var guestTracked = 0
    var guestReady = 0
    var guestFailed = 0
    var guestPlayed = 0
    var guestIdleVisibleMs: Int64 = 0
    var guestReadyAvgMs: Int64 = 0
    var id: String { lesson }
}

struct SiteGuestVisit: Codable, Hashable {
    var lesson = ""
    var device = ""
    var source = ""
    var readyMs = 0
    var loadError = ""
    var visibleMs = 0
    var events: [SiteGuestEvent] = []
    var openedAt: Date?
}

struct SitePathStep: Codable, Hashable {
    var lesson = ""
    var played = false
}

struct SitePathCount: Codable, Hashable {
    var steps: [SitePathStep] = []
    var count = 0
    var played = 0
}

struct SiteHomeStay: Codable, Hashable {
    var guest = ""
    var device = ""
    var source = ""
    var visits = 0
    var totalMs: Int64 = 0
    var longestMs = 0
    var maxScroll = 0
    var lastOpened: Date?
}

struct SiteLoginStats: Codable, Hashable {
    var visits = 0
    var outcomes: [SiteCount] = []
    var next: [SiteCount] = []
    var fails: [SiteCount] = []
    var googleReady = 0
    var googleReadyAvgMs: Int64 = 0
    var googleFailed = 0
    var googleErrors: [SiteCount] = []
}

struct SiteGuestBreakdown: Codable, Hashable {
    var devices: [SiteCount] = []
    var sources: [SiteCount] = []
    var errors: [SiteCount] = []
    var idleVisible: [SiteCount] = []
    var idleVisits: [SiteGuestVisit] = []
    var homePaths: [SitePathCount] = []
    var homeVisits = 0
    var homeStays: [SiteHomeStay] = []
    var login = SiteLoginStats()
}

struct SiteOverview: Codable, Hashable {
    var users = 0
    var activeUsers = 0
    var guests = 0
    var lessons: [SiteLessonStat] = []
    var guestStats = SiteGuestBreakdown()

    /// 有播放器的課(和 evoleap 後台一樣:登入門檻 gate:、一般頁面 page: 不是播放器,不進漏斗與各課概況)
    var playerLessons: [SiteLessonStat] {
        lessons.filter { !$0.lesson.hasPrefix("gate:") && !$0.lesson.hasPrefix("page:") }
    }

    /// 登入門檻被看到幾次(訪客打開要登入的課)
    var gates: [SiteCount] {
        lessons.filter { $0.lesson.hasPrefix("gate:") && $0.guestTracked > 0 }
            .map { SiteCount(name: String($0.lesson.dropFirst("gate:".count)), count: $0.guestTracked) }
            .sorted { $0.count > $1.count }
    }

    /// 訪客漏斗(有播放器的課加總):真人打開 → 載入完成 → 有播放;載入失敗、爬蟲另計
    var funnel: (tracked: Int, ready: Int, played: Int, failed: Int, bots: Int) {
        playerLessons.reduce((0, 0, 0, 0, 0)) { a, l in
            (a.0 + l.guestTracked, a.1 + l.guestReady, a.2 + l.guestPlayed, a.3 + l.guestFailed, a.4 + l.guestBots)
        }
    }

    static func decode(_ obj: Any) throws -> SiteOverview {
        let data = try JSONSerialization.data(withJSONObject: obj)
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { dec in
            let s = try dec.singleValueContainer().decode(String.self)
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = f.date(from: s) { return date }
            f.formatOptions = [.withInternetDateTime]
            if let date = f.date(from: s) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: dec.codingPath, debugDescription: "日期格式:\(s)"))
        }
        return try d.decode(SiteOverview.self, from: data)
    }
}

/// 時間篩選,對應 evoleap 的 ?range=
enum SiteRange: String, CaseIterable, Identifiable {
    case all = "", m30 = "30m", h12 = "12h", d1 = "1d", d7 = "7d"
    var id: String { rawValue.isEmpty ? "all" : rawValue }
    var label: String {
        switch self {
        case .all: return "全部"
        case .m30: return "30 分鐘"
        case .h12: return "12 小時"
        case .d1: return "1 天"
        case .d7: return "7 天"
        }
    }
    var path: String { "/api/admin/overview" + (rawValue.isEmpty ? "" : "?range=\(rawValue)") }
}

/// 讀網站訪客統計(畫面與 MCP 共用)
@MainActor
enum SiteVisitors {
    static let defaultsKey = "visitors.connection"

    static var connectionId: String {
        get { UserDefaults.standard.string(forKey: defaultsKey) ?? "evoleap" }
        set { UserDefaults.standard.set(newValue, forKey: defaultsKey) }
    }

    static func fetch(_ app: AppState, connection id: String? = nil, range: SiteRange) async throws -> SiteOverview {
        app.startConnections()
        guard let store = app.connStore else { throw ConnectionError("連線功能還沒啟動") }
        return try SiteOverview.decode(try await store.request(id ?? connectionId, "GET", range.path))
    }
}

extension Int {
    /// 百分比字串;分母為 0 時顯示 —
    func percent(of total: Int) -> String {
        total > 0 ? String(format: "%.0f%%", Double(self) / Double(total) * 100) : "—"
    }
}

enum SiteFmt {
    static func ms<T: BinaryInteger>(_ v: T) -> String {
        let s = Double(v) / 1000
        if s < 60 { return String(format: s < 10 ? "%.1f 秒" : "%.0f 秒", s) }
        let m = Int(s) / 60
        return m < 60 ? "\(m) 分 \(Int(s) % 60) 秒" : "\(m / 60) 小時 \(m % 60) 分"
    }
}

// MARK: - 寬鬆解碼
// Go 的 nil slice 會輸出 null、*time.Time 可能是 null;合成的 Decodable 遇到 null 或缺欄位會整個失敗。
// 這裡每個欄位都用 v(_:_:) 讀:缺欄位、null、型別不符都改用預設值。

private extension KeyedDecodingContainer {
    func v<T: Decodable>(_ k: Key, _ d: T) -> T { ((try? decodeIfPresent(T.self, forKey: k)) ?? nil) ?? d }
}

extension SiteCount {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(name: c.v(.name, ""), count: c.v(.count, 0))
    }
}

extension SiteGuestEvent {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(t: c.v(.t, 0), e: c.v(.e, ""))
    }
}

extension SiteLessonStat {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(lesson: c.v(.lesson, ""), learners: c.v(.learners, 0), completed: c.v(.completed, 0),
                  avgPercent: c.v(.avgPercent, 0), guestViews: c.v(.guestViews, 0), guestVisitors: c.v(.guestVisitors, 0),
                  guestWatchedMs: c.v(.guestWatchedMs, 0), guestBots: c.v(.guestBots, 0), guestTracked: c.v(.guestTracked, 0),
                  guestReady: c.v(.guestReady, 0), guestFailed: c.v(.guestFailed, 0), guestPlayed: c.v(.guestPlayed, 0),
                  guestIdleVisibleMs: c.v(.guestIdleVisibleMs, 0), guestReadyAvgMs: c.v(.guestReadyAvgMs, 0))
    }
}

extension SiteGuestVisit {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(lesson: c.v(.lesson, ""), device: c.v(.device, ""), source: c.v(.source, ""), readyMs: c.v(.readyMs, 0),
                  loadError: c.v(.loadError, ""), visibleMs: c.v(.visibleMs, 0), events: c.v(.events, []),
                  openedAt: c.v(.openedAt, nil))
    }
}

extension SitePathStep {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(lesson: c.v(.lesson, ""), played: c.v(.played, false))
    }
}

extension SitePathCount {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(steps: c.v(.steps, []), count: c.v(.count, 0), played: c.v(.played, 0))
    }
}

extension SiteHomeStay {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(guest: c.v(.guest, ""), device: c.v(.device, ""), source: c.v(.source, ""), visits: c.v(.visits, 0),
                  totalMs: c.v(.totalMs, 0), longestMs: c.v(.longestMs, 0), maxScroll: c.v(.maxScroll, 0),
                  lastOpened: c.v(.lastOpened, nil))
    }
}

extension SiteLoginStats {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(visits: c.v(.visits, 0), outcomes: c.v(.outcomes, []), next: c.v(.next, []), fails: c.v(.fails, []),
                  googleReady: c.v(.googleReady, 0), googleReadyAvgMs: c.v(.googleReadyAvgMs, 0),
                  googleFailed: c.v(.googleFailed, 0), googleErrors: c.v(.googleErrors, []))
    }
}

extension SiteGuestBreakdown {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(devices: c.v(.devices, []), sources: c.v(.sources, []), errors: c.v(.errors, []),
                  idleVisible: c.v(.idleVisible, []), idleVisits: c.v(.idleVisits, []), homePaths: c.v(.homePaths, []),
                  homeVisits: c.v(.homeVisits, 0), homeStays: c.v(.homeStays, []), login: c.v(.login, SiteLoginStats()))
    }
}

extension SiteOverview {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(users: c.v(.users, 0), activeUsers: c.v(.activeUsers, 0), guests: c.v(.guests, 0),
                  lessons: c.v(.lessons, []), guestStats: c.v(.guestStats, SiteGuestBreakdown()))
    }
}
