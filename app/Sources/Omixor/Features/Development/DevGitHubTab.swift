import AppKit
import SwiftUI

/// 開發進度 › GitHub:追蹤中 repo 的真實 PR(含檢查)與 Actions 執行。只讀。
struct DevGitHubTab: View {
    @EnvironmentObject var app: AppState
    @State private var mode: Mode = .runs
    @State private var selectedRun: Int?
    @State private var selectedPull: String?
    @State private var failedOnly = false
    @State private var repoFilter: String?
    @State private var newRepo = ""
    @State private var repoError: String?

    enum Mode: String, CaseIterable, Identifiable { case runs, pulls; var id: String { rawValue } }

    /// 不在畫面計算時啟動(會在更新畫面途中改 @Published);app 啟動時已經建好
    private var store: GitHubStore { app.ghStore ?? GitHubStore(app: app) }
    private var state: ConnectionState? { app.connectionStates[app.githubSettings.connection] }

    var body: some View {
        Group {
            if store.connection == nil || !store.isLoggedIn {
                ScrollView { GitHubSetupView().padding(24).frame(maxWidth: 720) }
                    .frame(maxWidth: .infinity)
            } else {
                main
            }
        }
        .onAppear {
            if store.isLoggedIn, app.github.fetchedAt == nil { Task { await store.refresh() } }
        }
        .onChange(of: state?.account) { _, acc in
            if acc != nil, store.isLoggedIn, app.github.fetchedAt == nil { Task { await store.refresh() } }
        }
        .onChange(of: app.currentOrgId) { selectedRun = nil; selectedPull = nil; repoFilter = nil }
    }

    // MARK: 主畫面

    private var main: some View {
        VStack(spacing: 0) {
            toolbar
            if !app.github.errors.isEmpty || app.github.error != nil || app.github.settingsError != nil {
                VStack(alignment: .leading, spacing: 4) {
                    if let e = app.github.error { Label(e, systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                    // 追蹤的 repo 是組織的設定(存在 omixor server);讀寫不到時顯示原因
                    if let e = app.github.settingsError { Label("組織設定:\(e)", systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                    ForEach(app.github.errors.sorted(by: { $0.key < $1.key }), id: \.key) { r, e in
                        Label("\(r):\(e)", systemImage: "exclamationmark.triangle").foregroundStyle(.orange)
                    }
                }
                .font(.caption)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 16).padding(.vertical, 6)
                .background(.orange.opacity(0.08))
            }
            Divider()
            if app.githubSettings.repos.isEmpty {
                EmptyStateView(title: "還沒追蹤任何 repo", icon: "shippingbox",
                               message: "在上面輸入 owner/name(例如 tim72117/evoleap)按「加入」。")
            } else {
                HStack(spacing: 0) {
                    list.frame(width: 440)
                    Divider()
                    Group {
                        if mode == .runs, let id = selectedRun, let r = app.github.runs.first(where: { $0.id == id }) {
                            GHRunDetail(run: r).id(r.id)
                        } else if mode == .pulls, let id = selectedPull, let p = app.github.pulls.first(where: { $0.id == id }) {
                            GHPullDetailView(pull: p).id(p.id)
                        } else {
                            EmptyStateView(title: mode == .runs ? "選一筆執行" : "選一個 PR", icon: "arrow.triangle.branch",
                                           message: "資料每 5 分鐘自動更新,也可以按右上角重新整理。")
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            Image(systemName: "person.crop.circle.badge.checkmark").foregroundStyle(.green)
            Text(state?.account ?? "已登入").font(.callout)
            Divider().frame(height: 16)
            ForEach(app.githubSettings.repos, id: \.self) { r in
                Menu {
                    if let u = URL(string: "https://github.com/\(r)") { Button("在 GitHub 打開") { NSWorkspace.shared.open(u) } }
                    Button("停止追蹤", role: .destructive) { removeRepo(r) }
                } label: { Label(r, systemImage: "shippingbox") }
                .menuStyle(.borderlessButton).fixedSize()
            }
            TextField("owner/name", text: $newRepo).textFieldStyle(.roundedBorder).frame(width: 160)
                .onSubmit(addRepo)
            Button("加入", action: addRepo).disabled(newRepo.trimmingCharacters(in: .whitespaces).isEmpty)
            if let repoError { Text(repoError).font(.caption).foregroundStyle(.red).lineLimit(1) }
            Spacer()
            if let t = app.github.fetchedAt {
                Text("更新於 " + Fmt.relative(t)).font(.caption).foregroundStyle(.secondary)
            }
            if app.github.busy { ProgressView().controlSize(.small) }
            Button { Task { await store.refresh() } } label: { Image(systemName: "arrow.clockwise") }
                .help("重新整理").disabled(app.github.busy)
        }
        .padding(.horizontal, 16).padding(.vertical, 8)
    }

    /// 停止追蹤(存到組織的設定,要組織管理員)
    private func removeRepo(_ r: String) {
        do {
            try store.removeRepo(r)
            repoError = nil
            if repoFilter == r { repoFilter = nil }
        } catch { repoError = error.localizedDescription }
    }

    private func addRepo() {
        do {
            try store.addRepo(newRepo)
            newRepo = ""; repoError = nil
            Task { await store.refresh() }
        } catch { repoError = error.localizedDescription }
    }

    // MARK: 清單

    private var runs: [GHRun] {
        app.github.runs.filter { (repoFilter == nil || $0.repo == repoFilter) && (!failedOnly || $0.isFailure) }
    }
    private var pulls: [GHPull] { app.github.pulls.filter { repoFilter == nil || $0.repo == repoFilter } }

    private var list: some View {
        VStack(spacing: 0) {
            HStack {
                Picker("", selection: $mode) {
                    Text("Actions(\(app.github.runs.count))").tag(Mode.runs)
                    Text("Pull Requests(\(app.github.pulls.count))").tag(Mode.pulls)
                }
                .pickerStyle(.segmented).labelsHidden()
                if app.githubSettings.repos.count > 1 {
                    Picker("", selection: $repoFilter) {
                        Text("全部 repo").tag(String?.none)
                        ForEach(app.githubSettings.repos, id: \.self) { Text($0).tag(String?.some($0)) }
                    }
                    .labelsHidden().frame(width: 130)
                }
            }
            .padding(10)
            if mode == .runs {
                Toggle("只看失敗", isOn: $failedOnly).toggleStyle(.checkbox).font(.caption)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 12).padding(.bottom, 6)
            }
            Divider()
            if mode == .runs {
                if runs.isEmpty {
                    EmptyStateView(title: app.github.fetchedAt == nil ? "讀取中…" : "沒有執行紀錄", icon: "arrow.triangle.branch")
                        .frame(maxHeight: .infinity)
                } else {
                    List(runs, selection: $selectedRun) { r in GHRunRow(run: r, showRepo: app.githubSettings.repos.count > 1).tag(r.id) }
                        .listStyle(.inset)
                }
            } else {
                if pulls.isEmpty {
                    EmptyStateView(title: app.github.fetchedAt == nil ? "讀取中…" : "沒有開著的 PR", icon: "arrow.triangle.pull")
                        .frame(maxHeight: .infinity)
                } else {
                    List(pulls, selection: $selectedPull) { p in GHPullRow(pull: p).tag(p.id) }
                        .listStyle(.inset)
                }
            }
        }
        .frame(maxHeight: .infinity, alignment: .top)
    }
}

// MARK: - 狀態樣式

enum GHStyle {
    static func runIcon(_ r: GHRun) -> (String, Color) {
        if r.isRunning { return ("clock.arrow.circlepath", .orange) }
        switch r.conclusion ?? "" {
        case "success": return ("checkmark.circle.fill", .green)
        case "cancelled", "skipped": return ("slash.circle", .secondary)
        default: return r.isFailure ? ("xmark.circle.fill", .red) : ("questionmark.circle", .secondary)
        }
    }

    static func stepIcon(status: String, conclusion: String?) -> (String, Color) {
        if status != "completed" { return ("clock", .orange) }
        switch conclusion ?? "" {
        case "success": return ("checkmark.circle", .green)
        case "skipped", "cancelled": return ("slash.circle", .secondary)
        default: return ("xmark.circle.fill", .red)
        }
    }

    static func checks(_ c: GHChecks?) -> (String, Color) {
        switch c?.state {
        case "failure": return ("xmark.circle.fill", .red)
        case "pending": return ("clock", .orange)
        case "success": return ("checkmark.circle.fill", .green)
        default: return ("circle.dashed", .secondary)
        }
    }

    static func duration(_ t: TimeInterval?) -> String? {
        guard let t else { return nil }
        let s = Int(t)
        return s < 60 ? "\(s) 秒" : "\(s / 60) 分 \(s % 60) 秒"
    }
}

private struct GHRunRow: View {
    let run: GHRun
    let showRepo: Bool

    var body: some View {
        let (icon, color) = GHStyle.runIcon(run)
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: icon).foregroundStyle(color).padding(.top, 2)
            VStack(alignment: .leading, spacing: 3) {
                Text(run.title).lineLimit(2)
                HStack(spacing: 6) {
                    Text("\(run.workflow) #\(run.runNumber)").font(.caption).foregroundStyle(.secondary)
                    Text(run.branch).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1)
                    Text(GitHub.runEventLabel(run.event)).font(.caption).foregroundStyle(.secondary)
                    if showRepo { Text(run.repo).font(.caption).foregroundStyle(.tertiary).lineLimit(1) }
                    Spacer()
                    if let c = run.createdAt { Text(Fmt.relative(c)).font(.caption).foregroundStyle(.secondary) }
                }
            }
        }
        .padding(.vertical, 2)
    }
}

private struct GHPullRow: View {
    let pull: GHPull

    var body: some View {
        let (icon, color) = GHStyle.checks(pull.checks)
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "arrow.triangle.pull").foregroundStyle(pull.draft ? Color.secondary : .green).padding(.top, 2)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 4) {
                    Text(pull.title).lineLimit(2)
                    if pull.draft { Pill(text: "草稿") }
                }
                HStack(spacing: 6) {
                    Text("#\(pull.number)").font(.caption.monospaced()).foregroundStyle(.secondary)
                    Text(pull.author).font(.caption).foregroundStyle(.secondary)
                    Text(pull.branch).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1)
                    Image(systemName: icon).foregroundStyle(color).font(.caption).help(pull.checks?.label ?? "")
                    Spacer()
                    if let u = pull.updatedAt { Text(Fmt.relative(u)).font(.caption).foregroundStyle(.secondary) }
                }
            }
        }
        .padding(.vertical, 2)
    }
}

// MARK: - 執行詳情

private struct GHRunDetail: View {
    @EnvironmentObject var app: AppState
    let run: GHRun
    @State private var jobs: [GHJob]?
    @State private var error: String?
    @State private var logJob: Int?
    @State private var log: String?
    @State private var logError: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                let (icon, color) = GHStyle.runIcon(run)
                HStack(alignment: .firstTextBaseline) {
                    Text(run.title).font(.title3.weight(.semibold)).textSelection(.enabled)
                    Spacer()
                    if let u = URL(string: run.url) { Button("在 GitHub 打開") { NSWorkspace.shared.open(u) } }
                }
                HStack(spacing: 8) {
                    Pill(text: run.stateLabel, color: color, icon: icon)
                    Pill(text: "\(run.workflow) #\(run.runNumber)" + (run.attempt > 1 ? "(第 \(run.attempt) 次)" : ""), icon: "gearshape.2")
                    Pill(text: run.branch, icon: "arrow.triangle.branch")
                    Text(String(run.sha.prefix(7))).font(.caption.monospaced()).foregroundStyle(.secondary)
                    Text(run.actor).font(.caption).foregroundStyle(.secondary)
                    if let d = GHStyle.duration(run.duration) { Text(d).font(.caption).foregroundStyle(.secondary) }
                    if let c = run.createdAt { Text(Fmt.relative(c)).font(.caption).foregroundStyle(.secondary) }
                }
                Divider()
                if let error { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                if let jobs {
                    ForEach(jobs) { j in jobCard(j) }
                } else if error == nil {
                    ProgressView().frame(maxWidth: .infinity)
                }
            }
            .padding(20)
            .frame(maxWidth: 820, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task(id: run.id) { await loadJobs() }
    }

    private func jobCard(_ j: GHJob) -> some View {
        let (icon, color) = GHStyle.stepIcon(status: j.status, conclusion: j.conclusion)
        let failed = j.status == "completed" && !["success", "skipped", "cancelled"].contains(j.conclusion ?? "")
        return Card {
            HStack {
                Image(systemName: icon).foregroundStyle(color)
                Text(j.name).font(.headline)
                if let s = j.startedAt, let e = j.completedAt, let d = GHStyle.duration(e.timeIntervalSince(s)) {
                    Text(d).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                if failed {
                    Button(logJob == j.id && log != nil ? "重新讀紀錄" : "看紀錄最後幾行") { Task { await loadLog(j.id) } }
                }
            }
            ForEach(j.steps, id: \.number) { s in
                let (si, sc) = GHStyle.stepIcon(status: s.status, conclusion: s.conclusion)
                HStack(spacing: 6) {
                    Image(systemName: si).foregroundStyle(sc).font(.caption)
                    Text(s.name).font(.callout)
                        .foregroundStyle(s.conclusion == "skipped" ? .secondary : .primary)
                        .fontWeight(sc == .red ? .semibold : .regular)
                }
            }
            if logJob == j.id {
                if let logError { Text(logError).font(.caption).foregroundStyle(.orange) }
                if let log {
                    ScrollView(.horizontal) {
                        Text(log).font(.system(size: 11, design: .monospaced)).textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .padding(8)
                    .background(.black.opacity(0.05), in: RoundedRectangle(cornerRadius: 6))
                } else if logError == nil {
                    ProgressView()
                }
            }
        }
    }

    private func loadJobs() async {
        do { jobs = try await app.startGitHub().jobs(repo: run.repo, run: run.id); error = nil }
        catch { self.error = "讀不到工作:\(error.localizedDescription)" }
    }

    private func loadLog(_ id: Int) async {
        logJob = id; log = nil; logError = nil
        do { log = try await app.startGitHub().jobLogTail(repo: run.repo, job: id) }
        catch { logError = "讀不到紀錄:\(error.localizedDescription)" }
    }
}

// MARK: - PR 詳情

private struct GHPullDetailView: View {
    @EnvironmentObject var app: AppState
    let pull: GHPull
    @State private var detail: GHPullDetail?
    @State private var error: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .firstTextBaseline) {
                    Text(pull.title).font(.title3.weight(.semibold)).textSelection(.enabled)
                    Text("#\(pull.number)").font(.title3).foregroundStyle(.secondary)
                    Spacer()
                    if let u = URL(string: pull.url) { Button("在 GitHub 打開") { NSWorkspace.shared.open(u) } }
                }
                HStack(spacing: 8) {
                    let (icon, color) = GHStyle.checks(detail?.pull.checks ?? pull.checks)
                    Pill(text: (detail?.pull.checks ?? pull.checks)?.label ?? "沒有檢查", color: color, icon: icon)
                    if pull.draft { Pill(text: "草稿") }
                    Pill(text: "\(pull.branch) → \(pull.base)", icon: "arrow.triangle.branch")
                    Text(pull.author).font(.caption).foregroundStyle(.secondary)
                    ForEach(pull.labels, id: \.self) { Pill(text: $0, icon: "tag") }
                    if let d = detail {
                        Text("+\(d.additions) −\(d.deletions) · \(d.changedFiles) 個檔案").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    }
                }
                if !pull.reviewers.isEmpty {
                    Text("等待審查:" + pull.reviewers.joined(separator: "、")).font(.caption).foregroundStyle(.secondary)
                }
                Divider()
                if let error { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                if let d = detail {
                    if d.body.isEmpty { Text("沒有說明").foregroundStyle(.secondary) } else { MarkdownText(d.body) }
                    Card("變更的檔案") {
                        ForEach(d.files, id: \.filename) { f in
                            HStack {
                                Text(f.filename).font(.caption.monospaced()).lineLimit(1).truncationMode(.middle)
                                Spacer()
                                Text("+\(f.additions)").font(.caption.monospacedDigit()).foregroundStyle(.green)
                                Text("−\(f.deletions)").font(.caption.monospacedDigit()).foregroundStyle(.red)
                            }
                        }
                    }
                } else if error == nil {
                    ProgressView().frame(maxWidth: .infinity)
                }
            }
            .padding(20)
            .frame(maxWidth: 820, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task(id: pull.id) {
            do { detail = try await app.startGitHub().pullDetail(repo: pull.repo, number: pull.number) }
            catch { self.error = "讀不到 PR:\(error.localizedDescription)" }
        }
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
        VStack(alignment: .leading, spacing: 16) {
            Label("連上 GitHub", systemImage: "chevron.left.forwardslash.chevron.right").font(.title2.weight(.semibold))
            Text("連上後,這裡會列出追蹤中 repo 的 Actions 執行(含失敗的步驟與紀錄)和開著的 PR(含檢查結果);虛擬角色也能透過 MCP 讀。omixor 只讀,不會開 PR、留言或重跑。")
                .foregroundStyle(.secondary)

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
