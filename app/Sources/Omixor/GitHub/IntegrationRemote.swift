import Foundation

// 組織的 GitHub、GitLab 設定一律透過 core server(IssueServerConfig.current:遠端 omixor 或本機守護服務)的
// /api/orgs/<組織>/integrations/github、…/gitlab 讀寫(規則由伺服器強制:成員讀、組織管理員改,見 core/api/integration.go)。
// 只有非機密設定(追蹤的 repo、GitLab 主機網址與追蹤的專案);token 留在這台的鑰匙圈,用哪一個連線(connection)也只存在這台的 github.json。
// 網址、token 與重新登入都和 Issue 共用 IssueRemote(Issues/IssueRemote.swift)。
// 這些端點屬於選用模組 integrations(core/modules/integrations):server 沒編進這個模組時路由不存在(404),
// 這裡把它轉成 IntegrationModuleMissing(不是錯誤:畫面當作這台伺服器沒有程式碼倉庫模組,見 AppState.noteModuleMissing)。

/// 這台伺服器沒有編進程式碼倉庫模組(/integrations/github|gitlab 的路由不存在)
struct IntegrationModuleMissing: LocalizedError, Equatable {
    static let text = "這台伺服器沒有安裝程式碼倉庫模組"
    var errorDescription: String? { Self.text }
}

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
        repos = c.lossyArray(String.self, .repos)
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
        skipped = c.lossyArray(String.self, .skipped)
        settings = try c.decode(CoreGitHubSettings.self, forKey: .settings)
    }
}

extension IssueRemote {
    private func decodeIntegration<T: Decodable>(_ type: T.Type, _ data: Data, _ name: String = "GitHub") throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的 \(name) 設定格式看不懂:\(error.localizedDescription)")
        }
    }

    private func githubSettingsURL(_ org: String, _ rest: [String] = []) -> URL { orgURL(org, ["integrations", "github"] + rest) }

    /// 送 /integrations/… 的請求:路由不存在(404、不是 core 的 JSON 錯誤)轉成 IntegrationModuleMissing
    func sendIntegration(_ method: String, _ url: URL, _ body: Data?) throws -> Data {
        do { return try send(method, url, body) } catch let e as IssueRemoteError where e.status == 404 && e.noRoute {
            throw IntegrationModuleMissing()
        }
    }

    /// GET /integrations/github
    func githubSettings(org: String) throws -> CoreGitHubSettings {
        try decodeIntegration(CoreGitHubSettings.self, try sendIntegration("GET", githubSettingsURL(org), nil))
    }

    /// POST /integrations/github:repos 不是 nil 就整份取代,再套用 add、remove(要組織管理員)
    func saveGitHubSettings(org: String, repos: [String]? = nil, add: [String] = [], remove: [String] = []) throws -> CoreGitHubSettings {
        var b: [String: Any] = [:]
        if let repos { b["repos"] = repos }
        if !add.isEmpty { b["add"] = add }
        if !remove.isEmpty { b["remove"] = remove }
        return try decodeIntegration(CoreGitHubSettings.self, try sendIntegration("POST", githubSettingsURL(org), try json(b)))
    }

    /// POST /integrations/github/import:第一次匯入這台的本機設定(要組織管理員;核心存過就不動)
    func importGitHubSettings(org: String, repos: [String]) throws -> CoreGitHubImport {
        try decodeIntegration(CoreGitHubImport.self, try sendIntegration("POST", githubSettingsURL(org, ["import"]), try json(["repos": repos])))
    }
}

// MARK: - GitLab

/// 核心的 GitLab 設定(GET 的回應;正規化規則見 core/model/integration.go)
struct CoreGitLabSettings: Codable, Equatable {
    /// GitLab 主機網址(https://gitlab.example.com,不含 /api/v4;"" = 還沒設定)
    var baseUrl: String
    /// 追蹤的專案(group/subgroup/project 或數字 id,依加入順序)
    var projects: [String]
    /// 核心存過(false = 還沒設定過,是空白預設)
    var saved: Bool
    var updatedAt: Date?
    var updatedBy: String?

    init(baseUrl: String = "", projects: [String] = [], saved: Bool = false, updatedAt: Date? = nil, updatedBy: String? = nil) {
        self.baseUrl = baseUrl; self.projects = projects; self.saved = saved; self.updatedAt = updatedAt; self.updatedBy = updatedBy
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        baseUrl = (try? c.decode(String.self, forKey: .baseUrl)) ?? ""
        projects = c.lossyArray(String.self, .projects)
        saved = (try? c.decode(Bool.self, forKey: .saved)) ?? false
        updatedAt = try? c.decode(Date.self, forKey: .updatedAt)
        updatedBy = try? c.decode(String.self, forKey: .updatedBy)
    }
}

/// GitLab 匯入的結果(核心存過時 imported = false、settings 是核心目前的)
struct CoreGitLabImport: Codable, Equatable {
    var imported: Bool
    var skipped: [String]
    var settings: CoreGitLabSettings

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        imported = (try? c.decode(Bool.self, forKey: .imported)) ?? false
        skipped = c.lossyArray(String.self, .skipped)
        settings = try c.decode(CoreGitLabSettings.self, forKey: .settings)
    }
}

extension IssueRemote {
    private func gitlabSettingsURL(_ org: String, _ rest: [String] = []) -> URL { orgURL(org, ["integrations", "gitlab"] + rest) }

    /// GET /integrations/gitlab(成員可讀)
    func gitlabSettings(org: String) throws -> CoreGitLabSettings {
        try decodeIntegration(CoreGitLabSettings.self, try sendIntegration("GET", gitlabSettingsURL(org), nil), "GitLab")
    }

    /// POST /integrations/gitlab(要組織管理員):baseUrl 不是 nil 就換網址("" = 清掉);
    /// projects 不是 nil 就整份取代,再套用 add、remove。格式不對時伺服器回 400 與說明
    func saveGitLabSettings(org: String, baseUrl: String? = nil, projects: [String]? = nil,
                            add: [String] = [], remove: [String] = []) throws -> CoreGitLabSettings {
        var b: [String: Any] = [:]
        if let baseUrl { b["baseUrl"] = baseUrl }
        if let projects { b["projects"] = projects }
        if !add.isEmpty { b["add"] = add }
        if !remove.isEmpty { b["remove"] = remove }
        return try decodeIntegration(CoreGitLabSettings.self, try sendIntegration("POST", gitlabSettingsURL(org), try json(b)), "GitLab")
    }

    /// POST /integrations/gitlab/import:第一次匯入這台的本機設定(要組織管理員;核心存過就不動)。
    /// App 目前沒有本機的舊 GitLab 設定,留給之後用
    func importGitLabSettings(org: String, baseUrl: String, projects: [String]) throws -> CoreGitLabImport {
        try decodeIntegration(CoreGitLabImport.self,
                              try sendIntegration("POST", gitlabSettingsURL(org, ["import"]), try json(["baseUrl": baseUrl, "projects": projects])), "GitLab")
    }
}
