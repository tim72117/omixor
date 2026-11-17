import Foundation

// LINE 官方帳號(Messaging API)的設定與狀態。
// 機密(channel access token、channel secret)存在鑰匙圈,帳號名稱帶組織前綴(TokenVault.scope);
// 其他設定存在目前組織資料夾的 line.json,解碼寬鬆:缺欄位或型別不對就用預設值。

/// 推播的接收者:LINE userId(U…)、groupId(C…)或 roomId(R…)
struct LineRecipient: Codable, Hashable, Identifiable {
    var id: String
    var label = ""

    init(id: String, label: String = "") {
        self.id = id
        self.label = label
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = (try? c.decode(String.self, forKey: .id)) ?? ""
        label = (try? c.decode(String.self, forKey: .label)) ?? ""
    }
}

/// 允許在 LINE 核准與提問的人
struct LineAllowedUser: Codable, Hashable, Identifiable {
    var userId: String
    var name = ""
    /// 在 Ops 裡以誰署名(Person.id);空白表示目前使用者
    var person = ""
    var id: String { userId }

    init(userId: String, name: String = "", person: String = "") {
        self.userId = userId
        self.name = name
        self.person = person
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        userId = (try? c.decode(String.self, forKey: .userId)) ?? ""
        name = (try? c.decode(String.self, forKey: .name)) ?? ""
        person = (try? c.decode(String.self, forKey: .person)) ?? ""
    }
}

struct LineSettings: Codable, Equatable {
    static let defaultPort = 3766

    /// 推播總開關
    var pushEnabled = true
    var recipients: [LineRecipient] = []
    var allowed: [LineAllowedUser] = []

    var notifyAlerts = true
    /// 告警的嚴重度門檻:critical、warning、info(含以上)
    var alertMinSeverity = Severity.warning.rawValue
    var notifyIncidents = true
    var notifyApprovals = true
    var notifyVoice = true
    var notifyAgentBlocked = true

    /// 本機 webhook 伺服器
    var webhookEnabled = true
    var port = LineSettings.defaultPort
    /// Cloudflare Tunnel / ngrok 給的公開網址(只用來在設定頁組出 Webhook URL)
    var publicURL = ""

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let d = LineSettings()
        pushEnabled = (try? c.decode(Bool.self, forKey: .pushEnabled)) ?? d.pushEnabled
        recipients = ((try? c.decode([LineRecipient].self, forKey: .recipients)) ?? []).filter { !$0.id.isEmpty }
        allowed = ((try? c.decode([LineAllowedUser].self, forKey: .allowed)) ?? []).filter { !$0.userId.isEmpty }
        notifyAlerts = (try? c.decode(Bool.self, forKey: .notifyAlerts)) ?? d.notifyAlerts
        let sev = (try? c.decode(String.self, forKey: .alertMinSeverity)) ?? d.alertMinSeverity
        alertMinSeverity = Severity(rawValue: sev) != nil ? sev : d.alertMinSeverity
        notifyIncidents = (try? c.decode(Bool.self, forKey: .notifyIncidents)) ?? d.notifyIncidents
        notifyApprovals = (try? c.decode(Bool.self, forKey: .notifyApprovals)) ?? d.notifyApprovals
        notifyVoice = (try? c.decode(Bool.self, forKey: .notifyVoice)) ?? d.notifyVoice
        notifyAgentBlocked = (try? c.decode(Bool.self, forKey: .notifyAgentBlocked)) ?? d.notifyAgentBlocked
        webhookEnabled = (try? c.decode(Bool.self, forKey: .webhookEnabled)) ?? d.webhookEnabled
        let p = (try? c.decode(Int.self, forKey: .port)) ?? d.port
        port = (1..<65536).contains(p) ? p : d.port
        publicURL = (try? c.decode(String.self, forKey: .publicURL)) ?? ""
    }

    var minSeverity: Severity { Severity(rawValue: alertMinSeverity) ?? .warning }

    func isAllowed(_ userId: String?) -> LineAllowedUser? {
        guard let userId, !userId.isEmpty else { return nil }
        return allowed.first { $0.userId == userId }
    }

    /// 要填到 LINE Developers 的 Webhook URL
    var webhookURL: String? {
        var base = publicURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !base.isEmpty else { return nil }
        if !base.lowercased().hasPrefix("http") { base = "https://" + base }
        while base.hasSuffix("/") { base.removeLast() }
        return base + LineWebhookServer.path
    }

    /// userId / groupId / roomId 的格式:U、C、R 開頭加 32 個十六進位字元
    static func isValidId(_ s: String) -> Bool {
        let s = s.trimmingCharacters(in: .whitespaces)
        guard s.count == 33, let f = s.first, "UCR".contains(f) else { return false }
        return s.dropFirst().allSatisfy { $0.isHexDigit }
    }
}

/// 顯示在設定頁的 LINE 狀態(不存檔)
struct LineStatus: Equatable {
    enum ServerState: Equatable { case stopped, starting, listening, failed(String) }

    struct LogLine: Identifiable, Equatable {
        let id = UUID()
        let date: Date
        let ok: Bool
        let text: String
    }

    /// 收到訊息但不在白名單的人,方便加進白名單
    struct Sender: Identifiable, Equatable {
        var id: String { userId }
        let userId: String
        var lastText: String
        var date: Date
    }

    var server: ServerState = .stopped
    var hasToken = false
    var hasSecret = false
    var pushed = 0
    var failed = 0
    var lastError: String?
    var lastErrorAt: Date?
    var webhookRequests = 0
    var rejectedSignatures = 0
    var log: [LogLine] = []
    var unknownSenders: [Sender] = []

    mutating func note(_ ok: Bool, _ text: String) {
        log.insert(LogLine(date: .now, ok: ok, text: text), at: 0)
        if log.count > 30 { log.removeLast(log.count - 30) }
        if !ok {
            lastError = text
            lastErrorAt = .now
        }
    }
}

/// 待核准請求推播時發的一次性 nonce,存在 line-state.json(重開 app 後按鈕仍有效)
struct LineNonce: Codable, Hashable {
    var approvalId: String
    var createdAt: Date
    var usedAt: Date?
}

struct LineState: Codable {
    var nonces: [String: LineNonce] = [:]

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        nonces = (try? c.decode([String: LineNonce].self, forKey: .nonces)) ?? [:]
    }
}

struct LineError: Error, CustomStringConvertible {
    let description: String
    init(_ d: String) { description = d }
}
