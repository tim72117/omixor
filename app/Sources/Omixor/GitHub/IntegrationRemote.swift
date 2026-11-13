import Foundation

// 組織的 GitHub 設定一律透過 core server(IssueServerConfig.current:遠端 omixor 或本機守護服務)的
// /api/orgs/<組織>/integrations/github 讀寫(規則由伺服器強制:成員讀、組織管理員改,見 core/api/integration.go)。
// 只有非機密設定(追蹤的 repo);token 留在這台的鑰匙圈,用哪一個連線(connection)也只存在這台的 github.json。
// 網址、token 與重新登入都和 Issue 共用 IssueRemote(Issues/IssueRemote.swift)。

/// 核心的 GitHub 設定(GET 的回應)
struct CoreGitHubSettings: Codable, Equatable {
    var repos: [String]
    /// 核心存過(false = 還沒設定過,是空白預設;第一次連上時看這個決定要不要匯入本機舊設定)
    var saved: Bool
    var updatedAt: Date?
    var updatedBy: String?

    init(repos: [String] = [], saved: Bool = false, updatedAt: Date? = nil, updatedBy: String? = nil) {
        self.repos = repos; self.saved = saved; self.updatedAt = updatedAt; self.updatedBy = updatedBy
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        repos = (try? c.decode([String].self, forKey: .repos)) ?? []
        saved = (try? c.decode(Bool.self, forKey: .saved)) ?? false
        updatedAt = try? c.decode(Date.self, forKey: .updatedAt)
        updatedBy = try? c.decode(String.self, forKey: .updatedBy)
    }
}

/// 匯入的結果(核心存過時 imported = false、settings 是核心目前的)
struct CoreGitHubImport: Codable, Equatable {
    var imported: Bool
    var skipped: [String]
    var settings: CoreGitHubSettings

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        imported = (try? c.decode(Bool.self, forKey: .imported)) ?? false
        skipped = (try? c.decode([String].self, forKey: .skipped)) ?? []
        settings = try c.decode(CoreGitHubSettings.self, forKey: .settings)
    }
}

extension IssueRemote {
    private func decodeIntegration<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的 GitHub 設定格式看不懂:\(error.localizedDescription)")
        }
    }

    private func githubSettingsURL(_ org: String, _ rest: [String] = []) -> URL { orgURL(org, ["integrations", "github"] + rest) }

    /// GET /integrations/github
    func githubSettings(org: String) throws -> CoreGitHubSettings {
        try decodeIntegration(CoreGitHubSettings.self, try send("GET", githubSettingsURL(org), nil))
    }

    /// POST /integrations/github:repos 不是 nil 就整份取代,再套用 add、remove(要組織管理員)
    func saveGitHubSettings(org: String, repos: [String]? = nil, add: [String] = [], remove: [String] = []) throws -> CoreGitHubSettings {
        var b: [String: Any] = [:]
        if let repos { b["repos"] = repos }
        if !add.isEmpty { b["add"] = add }
        if !remove.isEmpty { b["remove"] = remove }
        return try decodeIntegration(CoreGitHubSettings.self, try send("POST", githubSettingsURL(org), try json(b)))
    }

    /// POST /integrations/github/import:第一次匯入這台的本機設定(要組織管理員;核心存過就不動)
    func importGitHubSettings(org: String, repos: [String]) throws -> CoreGitHubImport {
        try decodeIntegration(CoreGitHubImport.self, try send("POST", githubSettingsURL(org, ["import"]), try json(["repos": repos])))
    }
}
