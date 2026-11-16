import AppKit
import SwiftUI

// 設定 › 程式碼倉庫:GitHub 的連線、登入與追蹤的 repo(原本在開發進度的 GitHub 分頁裡,同 web 的 SettingsRepos.tsx)。
// 開發進度 › Repositories 只看資料(Actions 執行、PR),沒設定好時用「前往 設定 › 程式碼倉庫」跳到這裡(app.go(.settings, focus: "repos"))。
// GitLab(公司自架)在下面的 GitLabSettingsSections:主機網址與追蹤的專案是組織的設定(core),登入(個人存取權杖)只在這台。
// App 沒有開發進度 › GitLab 分頁(MR、Pipeline 只在 web 看);Issue 的倉庫選單會列出追蹤的專案(gitlab: 前綴)。

struct SettingsReposPane: View {
    @EnvironmentObject var app: AppState
    @State private var newRepo = ""
    @State private var repoError: String?
    @State private var loggingOut = false

    /// 不在畫面計算時啟動(會在更新畫面途中改 @Published);app 啟動時已經建好
    private var store: GitHubStore { app.ghStore ?? GitHubStore(app: app) }
    private var conn: Connection? { store.connection }
    private var state: ConnectionState? { conn.flatMap { app.connectionStates[$0.id] } }
    private var loggedIn: Bool { conn != nil && store.isLoggedIn }

    var body: some View {
        Form {
            Section {
                if loggedIn, let c = conn {
                    accountRow(c)
                } else {
                    HStack {
                        Image(systemName: "person.crop.circle").foregroundStyle(.secondary)
                        Text(conn == nil ? "還沒設定" : "還沒登入").foregroundStyle(.secondary)
                        Spacer()
                    }
                }
            } header: {
                Label("GitHub", systemImage: "chevron.left.forwardslash.chevron.right")
            } footer: {
                Text("omixor 只讀 GitHub:看追蹤中 repo 的 Actions 執行與開著的 PR,不會開 PR、留言或重跑。資料在 開發進度 › Repositories。")
                    .font(.caption).foregroundStyle(.secondary)
            }

            if !loggedIn {
                Section { GitHubSetupView().padding(.vertical, 4) }
            } else {
                trackedRepos
            }

            GitLabSettingsSections()
        }
        .formStyle(.grouped)
    }

    /// 已登入時的帳號列:帳號、登入方式與登出(登出只清掉這台鑰匙圈裡的 token;追蹤的 repo 是組織設定,不會動)
    private func accountRow(_ c: Connection) -> some View {
        HStack(spacing: 10) {
            Image(systemName: "person.crop.circle.badge.checkmark").foregroundStyle(.green)
            VStack(alignment: .leading, spacing: 2) {
                Text(state?.account ?? "已登入")
                Text("登入方式:" + (c.auth == .bearer ? "個人存取權杖" : "GitHub OAuth")).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Button("登出") {
                loggingOut = true
                Task {
                    await app.connStore?.logout(c.id)
                    loggingOut = false
                }
            }
            .disabled(loggingOut)
            .help("登出 GitHub(清掉這台 Mac 上的 token)")
        }
    }

    /// 追蹤的 repo:清單(在 GitHub 打開、停止追蹤)與新增。repo 清單是組織的設定
    private var trackedRepos: some View {
        Section {
            if app.githubSettings.repos.isEmpty {
                Text("還沒追蹤任何 repo").foregroundStyle(.secondary)
            }
            ForEach(app.githubSettings.repos, id: \.self) { r in
                HStack {
                    Image(systemName: "shippingbox").foregroundStyle(.secondary)
                    Text(r).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    if let u = URL(string: "https://github.com/\(r)") {
                        Button("在 GitHub 打開") { NSWorkspace.shared.open(u) }.buttonStyle(.borderless)
                    }
                    Button("停止追蹤", role: .destructive) { removeRepo(r) }
                }
            }
            HStack {
                TextField("owner/name", text: $newRepo).textFieldStyle(.roundedBorder).onSubmit(addRepo)
                Button("加入", action: addRepo).disabled(newRepo.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            if let repoError {
                Text(repoError).font(.caption).foregroundStyle(.red)
            } else if let e = app.github.settingsError {
                Text("組織設定:\(e)").font(.caption).foregroundStyle(.orange)
            }
        } header: {
            Label("追蹤的 repo", systemImage: "shippingbox")
        } footer: {
            Text("輸入 owner/name(例如 tim72117/evoleap)。追蹤的 repo 是組織的設定,組織內的人都會看到;token 只存在這台。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    /// 停止追蹤(存到組織的設定,要組織管理員)
    private func removeRepo(_ r: String) {
        do {
            try store.removeRepo(r)
            repoError = nil
        } catch { repoError = error.localizedDescription }
    }

    private func addRepo() {
        do {
            try store.addRepo(newRepo)
            newRepo = ""; repoError = nil
            Task { await store.refresh() }
        } catch { repoError = error.localizedDescription }
    }
}

// MARK: - 登入引導

/// 還沒登入時:建連線 → 註冊 OAuth App → 填 client ID / secret → 瀏覽器登入。也可以改貼個人存取權杖
struct GitHubSetupView: View {
    @EnvironmentObject var app: AppState
    @State private var clientId = ""
    @State private var secret = ""
    @State private var useToken = false
    @State private var token = ""

    /// 不在畫面計算時啟動(會在更新畫面途中改 @Published);app 啟動時已經建好
    private var store: GitHubStore { app.ghStore ?? GitHubStore(app: app) }
    private var conn: Connection? { store.connection }
    private var state: ConnectionState? { conn.flatMap { app.connectionStates[$0.id] } }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("連上後,開發進度 › Repositories 會列出追蹤中 repo 的 Actions 執行(含失敗的步驟與紀錄)和開著的 PR(含檢查結果);虛擬角色也能透過 MCP 讀。")
                .font(.caption).foregroundStyle(.secondary)

            if conn == nil {
                Button("開始設定") { store.ensureConnection() }.buttonStyle(.borderedProminent)
            } else if let c = conn, c.auth == .oauth {
                if c.oauth.clientId.isEmpty || !c.oauth.hasClientSecret { registerSteps(c) } else { loginStep(c) }
            } else if let c = conn {
                // 已改成固定 Token 但 token 失效或還沒貼
                tokenStep(c)
            }

            if let e = state?.error { Label(e, systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }

            if let c = conn, c.auth == .oauth {
                DisclosureGroup("改用個人存取權杖", isExpanded: $useToken) { tokenStep(c).padding(.top, 6) }
                    .font(.callout)
            }
        }
        .onAppear { clientId = conn?.oauth.clientId ?? "" }
    }

    private func registerSteps(_ c: Connection) -> some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                Text("1. 在 GitHub 註冊一個 OAuth App(只要做一次)").font(.headline)
                Text("按下面的按鈕會打開 GitHub 的「New OAuth App」頁。名稱和網址會自動帶入;如果沒帶到,照下面填:")
                    .font(.callout).foregroundStyle(.secondary)
                copyRow("Application name", "omixor")
                copyRow("Homepage URL", GitHub.homepage)
                copyRow("Authorization callback URL", GitHub.callback)
                Text("「Enable Device Flow」不用勾。按 Register application 後,在 App 頁按「Generate a new client secret」。")
                    .font(.caption).foregroundStyle(.secondary)
                Button("打開 GitHub 註冊頁") { NSWorkspace.shared.open(GitHub.registerURL) }
                Divider()
                Text("2. 把 Client ID 和 Client secret 貼到這裡").font(.headline)
                TextField("Client ID", text: $clientId).textFieldStyle(.roundedBorder)
                SecureField("Client secret(存在鑰匙圈)", text: $secret).textFieldStyle(.roundedBorder)
                Button("儲存並用瀏覽器登入") {
                    var n = c
                    n.oauth.clientId = clientId.trimmingCharacters(in: .whitespacesAndNewlines)
                    app.connStore?.upsert(n)
                    app.connStore?.setClientSecret(secret.trimmingCharacters(in: .whitespacesAndNewlines), for: c.id)
                    secret = ""
                    Task { await app.connStore?.login(c.id) }
                }
                .buttonStyle(.borderedProminent)
                .disabled(clientId.trimmingCharacters(in: .whitespaces).isEmpty || (secret.isEmpty && !c.oauth.hasClientSecret))
            }
        }
    }

    private func loginStep(_ c: Connection) -> some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                Text("用瀏覽器登入 GitHub").font(.headline)
                Text("會打開 GitHub 的授權頁,按 Authorize 後回到 omixor 就完成了。權限只要 read:user;公開 repo 的 PR 與 Actions 不用額外權限。")
                    .font(.callout).foregroundStyle(.secondary)
                HStack {
                    Button("用瀏覽器登入") { Task { await app.connStore?.login(c.id) } }
                        .buttonStyle(.borderedProminent)
                        .disabled(state?.busy == true)
                    if state?.busy == true {
                        ProgressView().controlSize(.small); Text("等瀏覽器授權…").font(.caption)
                        Button("取消") { app.connStore?.cancelLogin(c.id) }
                    }
                    Spacer()
                    Button("重新設定 OAuth App") {
                        var n = c; n.oauth.clientId = ""
                        app.connStore?.upsert(n)
                        app.connStore?.setClientSecret(nil, for: c.id)
                        clientId = ""
                    }
                    .buttonStyle(.borderless)
                }
            }
        }
    }

    private func tokenStep(_ c: Connection) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("在 GitHub › Settings › Developer settings › Fine-grained tokens 建一個 token,Repository access 選要追蹤的 repo,權限給 Actions、Pull requests、Checks、Metadata 的 Read-only。")
                .font(.caption).foregroundStyle(.secondary)
            HStack {
                SecureField("github_pat_…(存在鑰匙圈)", text: $token).textFieldStyle(.roundedBorder)
                Button("使用這個 token") {
                    var n = c; n.auth = .bearer
                    app.connStore?.upsert(n)
                    app.connStore?.setBearer(token, for: c.id)
                    token = ""
                }
                .disabled(token.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            if c.auth == .bearer {
                Button("改回網頁 OAuth 登入") {
                    var n = c; n.auth = .oauth
                    app.connStore?.upsert(n)
                }
                .buttonStyle(.borderless)
            }
        }
    }

    private func copyRow(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label).font(.caption).foregroundStyle(.secondary).frame(width: 180, alignment: .leading)
            Text(value).font(.callout.monospaced()).textSelection(.enabled)
            Button { NSPasteboard.general.clearContents(); NSPasteboard.general.setString(value, forType: .string) } label: {
                Image(systemName: "doc.on.doc")
            }
            .buttonStyle(.borderless).help("複製")
        }
    }
}

// MARK: - GitLab

/// GitLab 的設定區塊(同 web 的 SettingsRepos.tsx GitLabPane,不含 OAuth 應用程式、CA 憑證檔與不驗證憑證):
/// 組織的主機網址與追蹤的專案(存在 core;組織管理員可改,成員唯讀)、這台的登入(個人存取權杖存在鑰匙圈)
struct GitLabSettingsSections: View {
    @EnvironmentObject var app: AppState
    @State private var base = ""
    @State private var baseError: String?
    @State private var savingBase = false
    @State private var token = ""
    @State private var newProject = ""
    @State private var projectError: String?
    @State private var busy = false
    @State private var loggingOut = false
    /// nil = 還不知道(當作可以改,伺服器會擋);false = 確定不是組織管理員(唯讀)
    @State private var canEdit: Bool?

    private var store: GitLabStore { app.glStore ?? app.startGitLab() }
    private var settings: CoreGitLabSettings { app.gitlabSettings }
    private var conn: Connection? { app.connections.first { $0.id == GitLab.connectionId } }
    private var state: ConnectionState? { conn.flatMap { app.connectionStates[$0.id] } }
    private var configured: Bool { !settings.baseUrl.isEmpty }
    private var loggedIn: Bool {
        guard let c = conn, configured, c.baseURL == settings.baseUrl else { return false }
        return app.connStore?.hasToken(c) == true
    }
    private var readOnly: Bool { canEdit == false }

    var body: some View {
        Group {
            statusSection
            serverSection
            if configured { loginSection }
            if configured { projectsSection }
        }
        .task(id: app.currentOrgId) { await reload() }
        .onChange(of: app.gitlabSettings.baseUrl) { _, v in base = v; baseError = nil }
    }

    private func reload() async {
        await store.reload()
        base = settings.baseUrl
        let org = app.currentOrgId, r = store.remote
        let role = try? await r.run { try $0.myRole(org: org) }
        guard app.currentOrgId == org else { return }
        // 拿不到角色(例如本機模式)時不擋,寫入由伺服器判斷
        if let role = role ?? nil { canEdit = role == "admin" || app.omixorLogin.account?.role == "admin" } else { canEdit = nil }
    }

    /// 狀態列:這台有沒有登入、組織設定讀取的錯誤
    private var statusSection: some View {
        Section {
            if loggedIn, let c = conn {
                HStack(spacing: 10) {
                    Image(systemName: "person.crop.circle.badge.checkmark").foregroundStyle(.green)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(state?.account ?? "已登入")
                        Text("\(c.baseURL) · 登入方式:個人存取權杖").font(.caption).foregroundStyle(.secondary)
                            .lineLimit(1).truncationMode(.middle)
                    }
                    Spacer()
                    Button("登出") {
                        loggingOut = true
                        Task { await store.logout(); loggingOut = false }
                    }
                    .disabled(loggingOut)
                    .help("登出 GitLab(清掉這台 Mac 上的 token;組織的網址與專案不動)")
                }
            } else {
                HStack {
                    Image(systemName: "person.crop.circle").foregroundStyle(.secondary)
                    Text(configured ? "還沒登入" : "組織還沒設定 GitLab 網址").foregroundStyle(.secondary)
                    Spacer()
                }
            }
            if let e = app.gitlabSettingsError {
                Label("組織設定:\(e)", systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
            }
            if let e = state?.error {
                Label(e, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
            }
        } header: {
            Label("GitLab(公司自架)", systemImage: "chevron.left.forwardslash.chevron.right")
        } footer: {
            Text("omixor 只讀 GitLab,不會開 MR、留言或重跑 pipeline。App 目前只有設定;Merge Requests 與 Pipelines 請在 web 版的 開發進度 › GitLab 看。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    /// 主機網址(組織的設定)
    private var serverSection: some View {
        Section {
            HStack {
                TextField("https://gitlab.corp.example.com", text: $base)
                    .textFieldStyle(.roundedBorder)
                    .disabled(readOnly)
                    .onSubmit(saveBase)
                Button("儲存", action: saveBase)
                    .disabled(readOnly || savingBase || base.trimmingCharacters(in: .whitespaces).isEmpty
                              || base.trimmingCharacters(in: .whitespaces) == settings.baseUrl)
            }
            if configured, !base.trimmingCharacters(in: .whitespaces).isEmpty,
               base.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "/")) != settings.baseUrl {
                Text("換網址後,這台與組織內其他裝置都要重新登入。").font(.caption).foregroundStyle(.orange)
            }
            if let baseError { Text(baseError).font(.caption).foregroundStyle(.red) }
        } header: {
            Label("GitLab 網址", systemImage: "server.rack")
        } footer: {
            Text(readOnly
                 ? "GitLab 網址是組織的設定,只有組織管理員可以修改。"
                 : "只要網址(不用 /api/v4)。GitLab 網址是組織的設定,組織內的人都會看到;只有組織管理員可以修改。App 只支援公開信任憑證的主機(自簽憑證請用 web 版設定 CA 憑證檔)。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    /// 這台的登入:貼個人存取權杖(存在鑰匙圈)
    private var loginSection: some View {
        Section {
            if loggedIn {
                Text("token 存在這台 Mac 的鑰匙圈,不會送到 omixor。要換 token 先登出再貼新的。").font(.caption).foregroundStyle(.secondary)
            } else {
                Text("在 GitLab 的「使用者設定 › 個人存取權杖」建一個 token,範圍只勾 read_api,設好到期日。")
                    .font(.caption).foregroundStyle(.secondary)
                if let u = GitLab.tokenURL(base: settings.baseUrl) {
                    Button("打開建立權杖頁(已帶好 read_api)") { NSWorkspace.shared.open(u) }
                }
                HStack {
                    SecureField("glpat-…(存在鑰匙圈)", text: $token).textFieldStyle(.roundedBorder)
                    Button("使用這個 token") {
                        store.signIn(token: token)
                        token = ""
                    }
                    .disabled(token.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        } header: {
            Label("這台的登入", systemImage: "key")
        }
    }

    /// 追蹤的專案(組織的設定)
    private var projectsSection: some View {
        Section {
            if settings.projects.isEmpty {
                Text("還沒追蹤任何專案").foregroundStyle(.secondary)
            }
            ForEach(settings.projects, id: \.self) { p in
                HStack {
                    Image(systemName: "shippingbox").foregroundStyle(.secondary)
                    Text(p).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    if let u = GitLab.projectURL(base: settings.baseUrl, key: p) {
                        Button("在 GitLab 打開") { NSWorkspace.shared.open(u) }.buttonStyle(.borderless)
                    }
                    if !readOnly {
                        Button("停止追蹤", role: .destructive) { removeProject(p) }.disabled(busy)
                    }
                }
            }
            if !readOnly {
                HStack {
                    TextField("group/project 或 id", text: $newProject).textFieldStyle(.roundedBorder).onSubmit(addProject)
                    Button("加入", action: addProject).disabled(busy || newProject.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
            if let projectError { Text(projectError).font(.caption).foregroundStyle(.red) }
        } header: {
            Label("追蹤的專案", systemImage: "shippingbox")
        } footer: {
            Text(readOnly
                 ? "追蹤的專案是組織的設定,只有組織管理員可以修改。Issue 的「所屬倉庫」會列出這些專案。"
                 : "可以有子群組,也可以貼專案網址或數字 id。追蹤的專案是組織的設定,組織內的人都會看到(Issue 的「所屬倉庫」也會列出);token 只存在這台。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func saveBase() {
        let b = base
        savingBase = true
        Task {
            do { try await store.setBaseURL(b); baseError = nil } catch { baseError = error.localizedDescription }
            savingBase = false
        }
    }

    private func addProject() {
        let p = newProject
        guard !p.trimmingCharacters(in: .whitespaces).isEmpty else { return }
        busy = true
        Task {
            do { try await store.addProject(p); newProject = ""; projectError = nil } catch { projectError = error.localizedDescription }
            busy = false
        }
    }

    private func removeProject(_ p: String) {
        busy = true
        Task {
            do { try await store.removeProject(p); projectError = nil } catch { projectError = error.localizedDescription }
            busy = false
        }
    }
}
