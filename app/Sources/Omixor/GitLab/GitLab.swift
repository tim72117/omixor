import Foundation

// GitLab(公司自架)的設定,同 web 的 gitlab/gitlabSettings.tsx 與 web/internal/core/gitlab.go 的設定部分:
//   - 組織的設定(存在 core,成員讀、組織管理員改):GitLab 主機網址、追蹤的專案。讀寫走 IntegrationRemote.swift 的
//     /api/orgs/<組織>/integrations/gitlab,用目前連線的 core server(IssueServerConfig.current)
//   - 這台的設定(不進 core):「GitLab」連線(Connections/,id 固定 gitlab),網址跟著組織的主機網址;
//     登入用個人存取權杖(read_api),存在鑰匙圈(TokenVault)。網址換了 ConnectionStore.upsert 會清掉舊 token(要重新登入)
// App 目前只有設定,沒有讀 MR、Pipeline 的畫面(開發進度沒有 GitLab 分頁);Issue 的倉庫選單會列出追蹤的專案(gitlab: 前綴)。
// 和 web 的差異:App 不做 GitLab OAuth 應用程式登入、CA 憑證檔與「不驗證憑證」(只支援公開信任憑證的主機)。

enum GitLab {
    /// 這台的 GitLab 連線 id(同 web 的 GLConnectionID)
    static let connectionId = "gitlab"

    /// 預設的 GitLab 連線:個人存取權杖(Bearer),測試路徑 /api/v4/user(順便取得帳號名稱)
    static func presetConnection(base: String) -> Connection {
        Connection(id: connectionId, name: "GitLab", baseURL: base, isProduction: false, testPath: "/api/v4/user",
                   auth: .bearer, note: "設定 › 程式碼倉庫 的 GitLab 登入(個人存取權杖,只讀)")
    }

    /// 建立個人存取權杖的頁面(已帶好名稱與 read_api 範圍)
    static func tokenURL(base: String) -> URL? {
        base.isEmpty ? nil : URL(string: base + "/-/user_settings/personal_access_tokens?name=omixor&scopes=read_api")
    }

    /// 專案在 GitLab 的網頁(數字 id 用 /projects/<id>,GitLab 會轉到專案頁)
    static func projectURL(base: String, key: String) -> URL? {
        guard !base.isEmpty else { return nil }
        return URL(string: base + (Int(key) != nil ? "/projects/\(key)" : "/\(key)"))
    }

    /// Issue 倉庫標記用的寫法:gitlab:<專案>
    static func repoTag(_ project: String) -> String { "gitlab:" + project }
}

/// GitLab 的組織設定與這台的登入
@MainActor
final class GitLabStore {
    let app: AppState

    init(app: AppState) { self.app = app }

    var connection: Connection? { app.connections.first { $0.id == GitLab.connectionId } }
    /// 這台登入了(有連線、組織有主機網址、鑰匙圈有 token)
    var isLoggedIn: Bool {
        guard let c = connection, !c.baseURL.isEmpty else { return false }
        return app.connStore?.hasToken(c) == true
    }

    private var remoteClient: IssueRemote?
    /// 目前連的 core server 的用戶端(和 Issue 同一個設定;連線換了會換新的)
    var remote: IssueRemote {
        let cfg = IssueServerConfig.current
        if let c = remoteClient, c.matches(cfg) { return c }
        let c = IssueRemote(config: cfg)
        remoteClient = c
        return c
    }

    private func setError(_ msg: String?) {
        if let msg, msg != app.gitlabSettingsError { NSLog("GitLab 設定(omixor server):%@", msg) }
        if app.gitlabSettingsError != msg { app.gitlabSettingsError = msg }
    }

    /// 套用核心的設定;主機網址換了就讓這台的連線跟著換(舊 token 作廢)
    private func apply(_ x: CoreGitLabSettings) {
        if app.gitlabSettings != x { app.gitlabSettings = x }
        syncConnection()
    }

    /// 這台已經有 GitLab 連線時,網址跟著組織的主機網址(不一樣才改;ConnectionStore.upsert 會清掉舊 token)
    func syncConnection() {
        guard var c = connection, c.baseURL != app.gitlabSettings.baseUrl else { return }
        c.baseURL = app.gitlabSettings.baseUrl
        app.connStore?.upsert(c)
    }

    /// 從核心讀組織的 GitLab 設定(請求在背景跑)。App 沒有本機的舊 GitLab 設定,不用匯入
    /// 沒有程式碼倉庫模組(server 沒編進 integrations)時不讀,也不顯示錯誤
    func reload() async {
        guard app.hasModule(ServerModule.integrations) else { clearForOrgChange(); return }
        let org = app.currentOrgId, r = remote
        do {
            let x = try await r.run { try $0.gitlabSettings(org: org) }
            guard app.currentOrgId == org else { return }
            apply(x)
            setError(nil)
        } catch is IntegrationModuleMissing {
            guard app.currentOrgId == org else { return }
            app.noteModuleMissing(ServerModule.integrations)   // 這台伺服器沒有程式碼倉庫模組:隱藏,不當錯誤
            clearForOrgChange()
        } catch {
            guard app.currentOrgId == org else { return }
            setError(error.localizedDescription)
        }
    }

    /// 換組織:先清掉上一個組織的,再讀
    func clearForOrgChange() {
        app.gitlabSettings = CoreGitLabSettings()
        app.gitlabSettingsError = nil
    }

    /// 存組織設定(要組織管理員);失敗時丟出伺服器的說明(例如網址格式不對、不是管理員)
    private func save(baseUrl: String? = nil, add: [String] = [], remove: [String] = []) async throws {
        let org = app.currentOrgId, r = remote
        let x = try await r.run { try $0.saveGitLabSettings(org: org, baseUrl: baseUrl, add: add, remove: remove) }
        guard app.currentOrgId == org else { return }
        apply(x)
        setError(nil)
    }

    /// 設定主機網址(組織的設定)。網址換了,這台與其他裝置都要重新登入
    func setBaseURL(_ s: String) async throws {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { throw ConnectionError("請輸入 GitLab 網址,例如 https://gitlab.example.com") }
        try await save(baseUrl: t)
    }

    /// 追蹤專案(group/project、數字 id 或專案網址;正規化由核心做)
    func addProject(_ s: String) async throws {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return }
        guard !app.gitlabSettings.baseUrl.isEmpty else { throw ConnectionError("請先設定 GitLab 網址") }
        try await save(add: [t])
    }

    /// 不再追蹤
    func removeProject(_ key: String) async throws { try await save(remove: [key]) }

    /// 用個人存取權杖登入(存在這台的鑰匙圈):沒有連線就建一個,網址用組織的主機網址
    func signIn(token: String) {
        let t = token.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty, !app.gitlabSettings.baseUrl.isEmpty else { return }
        app.startConnections()
        var c = connection ?? GitLab.presetConnection(base: app.gitlabSettings.baseUrl)
        c.baseURL = app.gitlabSettings.baseUrl
        c.auth = .bearer
        app.connStore?.upsert(c)
        app.connStore?.setBearer(t, for: c.id)
    }

    /// 登出:只清掉這台鑰匙圈裡的 token(組織的網址與專案不動)
    func logout() async {
        await app.connStore?.logout(GitLab.connectionId)
    }
}
