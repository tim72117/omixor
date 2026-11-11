import Foundation

// 連線:Ops 要呼叫的外部服務(網址 + 認證方式)。設定存在 Application Support/Ops/connections.json,
// 密碼類的東西(token、client secret)存在鑰匙圈(TokenVault),不寫進設定檔。

enum AuthKind: String, Codable, CaseIterable, Identifiable {
    case oauth, approval, bearer, none
    var id: String { rawValue }
    var label: String {
        switch self {
        case .oauth: return "OAuth 2.0 / OIDC"
        case .approval: return "瀏覽器核准"
        case .bearer: return "固定 Token"
        case .none: return "不用認證"
        }
    }
    var detail: String {
        switch self {
        case .oauth: return "標準 OAuth 授權碼流程 + PKCE,從網址自動探索端點;伺服器支援時自動註冊用戶端。Google、Auth0、Keycloak、Okta、MCP 伺服器等都適用"
        case .approval: return "Ops 在本機開回呼網址,瀏覽器裡由管理員按核准後換 token(evoleap CLI 的登入方式)"
        case .bearer: return "貼上 API key 或個人存取權杖,每次請求帶 Authorization: Bearer"
        case .none: return "公開的 API,不帶任何認證"
        }
    }
}

struct OAuthSettings: Codable, Hashable {
    /// 授權伺服器;空白時從連線網址探索(RFC 9728 → RFC 8414 / OIDC Discovery)
    var issuer = ""
    var authorizationEndpoint = ""
    var tokenEndpoint = ""
    var registrationEndpoint = ""
    var clientId = ""
    /// client secret 存在鑰匙圈;這裡只記有沒有
    var hasClientSecret = false
    var scopes = "openid email offline_access"
    /// RFC 8707 resource indicator:token 只給這個服務用
    var resource = ""
    /// 額外的授權參數,例如 Google 要 refresh token:access_type=offline&prompt=consent
    var extraParams = ""
    /// 呼叫 API 時送 id_token 而不是 access_token(Google IAP、Cloud Run 需要)
    var sendIdToken = false
    /// 動態註冊時登記的回呼連接埠;之後登入盡量用同一個
    var registeredPort: UInt16?
}

struct ApprovalSettings: Codable, Hashable {
    /// 核准頁所在的網站;空白時和連線網址相同
    var consoleURL = ""
    var startPath = "/api/cli-auth/start"
    var approvePath = "/login/cli?id={id}"
    var exchangePath = "/api/cli-auth/exchange"
    var logoutPath = "/api/cli-auth/logout"
}

struct Connection: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var baseURL: String
    var isProduction = false
    /// 測試連線與取得帳號名稱用的路徑(GET),例如 /api/me;空白表示不測
    var testPath = ""
    var auth: AuthKind = .oauth
    var oauth = OAuthSettings()
    var approval = ApprovalSettings()
    var note = ""

    var origin: String {
        guard let u = URL(string: baseURL), let scheme = u.scheme, let host = u.host else { return baseURL }
        return "\(scheme)://\(host)" + (u.port.map { ":\($0)" } ?? "")
    }

    var trimmedBase: String { baseURL.hasSuffix("/") ? String(baseURL.dropLast()) : baseURL }

    /// 連線的 token 可以送到哪些主機:連線網址本身,或同一個上層網域(www.googleapis.com → *.googleapis.com)
    func allowsHost(_ host: String?) -> Bool {
        guard let host = host?.lowercased(), let base = URL(string: baseURL)?.host?.lowercased() else { return false }
        if host == base { return true }
        let parts = base.split(separator: ".")
        guard parts.count >= 3 else { return false }
        return host.hasSuffix("." + parts.dropFirst().joined(separator: "."))
    }

    static let defaults: [Connection] = [
        Connection(id: "evoleap", name: "evoleap 正式站", baseURL: "https://evoleap.io", isProduction: true,
                   testPath: "/api/me", auth: .approval, note: "課程資料、旁白發佈"),
        Connection(id: "evoleap-local", name: "evoleap 本機", baseURL: "http://localhost:8190", isProduction: false,
                   testPath: "/api/me", auth: .approval,
                   approval: ApprovalSettings(consoleURL: "http://localhost:5190"), note: "本機開發用(go run ./cmd/server + npm run dev)"),
    ]
}

/// 存在鑰匙圈的登入結果
struct TokenSet: Codable, Equatable {
    var accessToken: String
    var refreshToken: String?
    var idToken: String?
    var expiresAt: Date?
    var tokenType: String?

    var isExpiring: Bool { expiresAt.map { $0.timeIntervalSinceNow < 60 } ?? false }
}

/// 畫面上顯示的連線狀態
struct ConnectionState: Equatable {
    var account: String?
    var busy = false
    var error: String?
    var lastTest: String?
}

struct ConnectionError: LocalizedError {
    let status: Int
    let message: String
    init(_ message: String, status: Int = 0) { self.message = message; self.status = status }
    var errorDescription: String? { status > 0 ? "HTTP \(status):\(message)" : message }
    var isUnauthorized: Bool { status == 401 }
}
