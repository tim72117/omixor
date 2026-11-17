import AppKit
import SwiftUI

// 開發進度 › Repositories:組織追蹤的 repo(真實資料,功能 development.repos,屬於選用模組 integrations)。
// 每列:repo 名、平台、預設分支最近一次 Actions 的狀態、開著的 PR 數;點進去看該 repo 的 PR 與 Actions 執行(含失敗原因)。
// 原本獨立的 GitHub 分頁併進來了(元件在 DevGitHubTab.swift);原本範例資料版的 repo 畫面已移除。
// GitLab 追蹤的專案也列出來,但 App 目前只讀 GitHub:GitLab 的 MR 與 Pipeline 請在 web 版看。
// 連線、登入與追蹤的 repo 在 設定 › 程式碼倉庫(Features/Settings/SettingsRepos.swift)。

/// 一個追蹤中的 repo
struct RepoEntry: Identifiable, Hashable {
    enum Platform: String, Hashable {
        case github, gitlab
        var label: String { self == .github ? "GitHub" : "GitLab" }
    }
    let platform: Platform
    /// GitHub:owner/name;GitLab:group/sub/project 或數字 id
    let key: String
    /// 畫面與跳轉用的 id:GitHub 是 owner/name,GitLab 加 gitlab: 前綴(同 Issue 的倉庫標記)
    var id: String { platform == .gitlab ? GitLab.repoTag(key) : key }
}

/// Repositories 分頁的清單與每列摘要(純函式,方便測試)
enum RepoListing {
    /// 組織追蹤的 repo:GitHub 在前、GitLab 在後,各自照加入順序
    static func entries(github: [String], gitlab: [String]) -> [RepoEntry] {
        github.map { RepoEntry(platform: .github, key: $0) } + gitlab.map { RepoEntry(platform: .gitlab, key: $0) }
    }

    /// 跳轉用:focus 是 owner/name、github:owner/name 或 gitlab:<專案>(不分大小寫)時找出對應的 repo
    static func entry(matching focus: String, github: [String], gitlab: [String]) -> RepoEntry? {
        let f = focus.trimmingCharacters(in: .whitespaces)
        guard !f.isEmpty else { return nil }
        let all = entries(github: github, gitlab: gitlab)
        let lower = f.lowercased()
        if lower.hasPrefix("gitlab:") {
            let k = String(f.dropFirst("gitlab:".count))
            return all.first { $0.platform == .gitlab && $0.key.caseInsensitiveCompare(k) == .orderedSame }
        }
        let k = lower.hasPrefix("github:") ? String(f.dropFirst("github:".count)) : f
        return all.first { $0.platform == .github && $0.key.caseInsensitiveCompare(k) == .orderedSame }
    }

    /// 預設分支最近一次的 Actions 執行(runs 照建立時間新到舊;還不知道預設分支時 nil)
    static func latestDefaultBranchRun(repo: String, info: GHRepo?, runs: [GHRun]) -> GHRun? {
        guard let branch = info?.defaultBranch, !branch.isEmpty else { return nil }
        return runs.filter { $0.repo == repo && $0.branch == branch }
            .max { ($0.createdAt ?? .distantPast) < ($1.createdAt ?? .distantPast) }
    }

    /// 開著的 PR 數
    static func openPullCount(repo: String, pulls: [GHPull]) -> Int { pulls.filter { $0.repo == repo }.count }
}

struct DevRepositoriesTab: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel

    /// 不在畫面計算時啟動(會在更新畫面途中改 @Published);app 啟動時已經建好
    private var store: GitHubStore { app.ghStore ?? GitHubStore(app: app) }
    private var state: ConnectionState? { app.connectionStates[app.githubSettings.connection] }
    private var githubReady: Bool { store.connection != nil && store.isLoggedIn }
    private var entries: [RepoEntry] { RepoListing.entries(github: app.githubSettings.repos, gitlab: app.gitlabSettings.projects) }
    /// 在設定頁停止追蹤後,選的值可能已不在清單裡,當成沒選
    private var selected: RepoEntry? { entries.first { $0.id == model.selectedRepo } }

    var body: some View {
        Group {
            if !githubReady && app.gitlabSettings.projects.isEmpty {
                // 沒有 GitLab 專案時,沿用原本 GitHub 分頁的做法:先到設定頁連上 GitHub
                RepoSetupEmpty(title: store.connection == nil ? "還沒設定 GitHub" : "還沒登入 GitHub",
                               message: "GitHub 的連線、登入與要追蹤的 repo 在 設定 › 程式碼倉庫 設定。")
            } else if entries.isEmpty {
                RepoSetupEmpty(title: "還沒追蹤任何 repo", icon: "shippingbox",
                               message: "要追蹤的 repo 在 設定 › 程式碼倉庫 加入。")
            } else {
                main
            }
        }
        .onAppear {
            if githubReady, app.github.fetchedAt == nil { Task { await store.refresh() } }
        }
        .onChange(of: state?.account) { _, acc in
            if acc != nil, githubReady, app.github.fetchedAt == nil { Task { await store.refresh() } }
        }
        .onChange(of: app.currentOrgId) { model.selectedRepo = nil }
    }

    // MARK: 主畫面

    private var main: some View {
        VStack(spacing: 0) {
            toolbar
            banner
            Divider()
            HStack(spacing: 0) {
                List(entries, selection: $model.selectedRepo) { e in RepoListRow(entry: e).tag(e.id) }
                    .listStyle(.inset)
                    .frame(width: 300)
                Divider()
                Group {
                    if let e = selected {
                        switch e.platform {
                        case .github: RepoGitHubDetail(repo: e.key, ready: githubReady).id(e.id)
                        case .gitlab: RepoGitLabDetail(project: e.key).id(e.id)
                        }
                    } else {
                        EmptyStateView(title: "選一個 repo", icon: "shippingbox",
                                       message: "點 repo 看它開著的 PR 與 Actions 執行(含失敗原因)。資料每 5 分鐘自動更新。")
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            if githubReady {
                Image(systemName: "person.crop.circle.badge.checkmark").foregroundStyle(.green)
                Text(state?.account ?? "已登入 GitHub").font(.callout)
            } else {
                Image(systemName: "person.crop.circle").foregroundStyle(.secondary)
                Text(store.connection == nil ? "還沒設定 GitHub" : "還沒登入 GitHub").font(.callout).foregroundStyle(.secondary)
            }
            Divider().frame(height: 16)
            Text("追蹤 \(entries.count) 個 repo").font(.caption).foregroundStyle(.secondary)
            Spacer()
            if let t = app.github.fetchedAt {
                Text("更新於 " + Fmt.relative(t)).font(.caption).foregroundStyle(.secondary)
            }
            if app.github.busy { ProgressView().controlSize(.small) }
            ManageReposButton()
            Button { Task { await store.refresh() } } label: { Image(systemName: "arrow.clockwise") }
                .help("重新整理").disabled(app.github.busy || !githubReady)
        }
        .padding(.horizontal, 16).padding(.vertical, 8)
    }

    /// 讀取錯誤(整體、組織設定、各 repo)
    @ViewBuilder private var banner: some View {
        // 沒登入時 app.github.error 是「還沒登入 GitHub」,工具列已經顯示了
        let error = githubReady ? app.github.error : nil
        if !app.github.errors.isEmpty || error != nil || app.github.settingsError != nil || app.gitlabSettingsError != nil {
            VStack(alignment: .leading, spacing: 4) {
                if let error { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                // 追蹤的 repo 是組織的設定(存在 omixor server);讀寫不到時顯示原因
                if let e = app.github.settingsError { Label("組織設定(GitHub):\(e)", systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                if let e = app.gitlabSettingsError { Label("組織設定(GitLab):\(e)", systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
                ForEach(app.github.errors.sorted(by: { $0.key < $1.key }), id: \.key) { r, e in
                    Label("\(r):\(e)", systemImage: "exclamationmark.triangle").foregroundStyle(.orange)
                }
            }
            .font(.caption)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 16).padding(.vertical, 6)
            .background(.orange.opacity(0.08))
        }
    }
}

// MARK: - 清單的一列

private struct RepoListRow: View {
    @EnvironmentObject var app: AppState
    let entry: RepoEntry

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text(entry.key).lineLimit(1).truncationMode(.middle)
                Spacer()
                Pill(text: entry.platform.label, color: entry.platform == .github ? .primary : .orange)
            }
            switch entry.platform {
            case .github: githubSummary
            case .gitlab:
                Text("App 只讀 GitHub,MR 與 Pipeline 在 web 版看").font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .padding(.vertical, 3)
    }

    @ViewBuilder private var githubSummary: some View {
        let info = app.github.repos.first { $0.id.caseInsensitiveCompare(entry.key) == .orderedSame }
        let run = RepoListing.latestDefaultBranchRun(repo: entry.key, info: info, runs: app.github.runs)
        let prs = RepoListing.openPullCount(repo: entry.key, pulls: app.github.pulls)
        HStack(spacing: 8) {
            if let e = app.github.errors[entry.key] {
                Label("讀不到", systemImage: "exclamationmark.triangle").foregroundStyle(.orange).help(e)
            } else if let run {
                let (icon, color) = GHStyle.runIcon(run)
                Label(run.stateLabel, systemImage: icon).foregroundStyle(color)
                    .help("\(run.branch) 最近一次:\(run.workflow) #\(run.runNumber)")
            } else {
                Label(app.github.fetchedAt == nil ? "讀取中…" : "沒有執行紀錄", systemImage: "circle.dashed").foregroundStyle(.secondary)
            }
            if let b = info?.defaultBranch { Text(b).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1) }
            Spacer()
            Label("\(prs) 個 PR", systemImage: "arrow.triangle.pull").foregroundStyle(prs > 0 ? .primary : .secondary)
        }
        .font(.caption)
    }
}

// MARK: - GitHub repo 詳情:PR 與 Actions

private struct RepoGitHubDetail: View {
    @EnvironmentObject var app: AppState
    let repo: String
    let ready: Bool
    @State private var mode: Mode = .runs
    @State private var selectedRun: Int?
    @State private var selectedPull: String?
    @State private var failedOnly = false

    enum Mode: String, Hashable { case runs, pulls }

    private var info: GHRepo? { app.github.repos.first { $0.id.caseInsensitiveCompare(repo) == .orderedSame } }
    private var allRuns: [GHRun] { app.github.runs.filter { $0.repo == repo } }
    private var runs: [GHRun] { allRuns.filter { !failedOnly || $0.isFailure } }
    private var pulls: [GHPull] { app.github.pulls.filter { $0.repo == repo } }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if !ready {
                RepoSetupEmpty(title: "還沒登入 GitHub",
                               message: "登入 GitHub 後才讀得到 \(repo) 的 PR 與 Actions。連線與登入在 設定 › 程式碼倉庫。")
            } else {
                HStack(spacing: 0) {
                    list.frame(width: 400)
                    Divider()
                    Group {
                        if mode == .runs, let id = selectedRun, let r = allRuns.first(where: { $0.id == id }) {
                            GHRunDetail(run: r).id(r.id)
                        } else if mode == .pulls, let id = selectedPull, let p = pulls.first(where: { $0.id == id }) {
                            GHPullDetailView(pull: p).id(p.id)
                        } else {
                            EmptyStateView(title: mode == .runs ? "選一筆執行" : "選一個 PR", icon: "arrow.triangle.branch",
                                           message: mode == .runs ? "失敗的執行會列出失敗的步驟,也能看紀錄最後幾行。" : nil)
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(repo).font(.title3.weight(.semibold)).textSelection(.enabled)
            Pill(text: "GitHub")
            if let i = info {
                Pill(text: i.defaultBranch, icon: "arrow.triangle.branch")
                if i.isPrivate { Pill(text: "私有", icon: "lock") }
                if !i.description.isEmpty { Text(i.description).font(.caption).foregroundStyle(.secondary).lineLimit(1) }
            }
            Spacer()
            if let u = URL(string: info?.url ?? "https://github.com/\(repo)") { Button("在 GitHub 打開") { NSWorkspace.shared.open(u) } }
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
    }

    private var list: some View {
        VStack(spacing: 0) {
            Picker("", selection: $mode) {
                Text("Actions(\(allRuns.count))").tag(Mode.runs)
                Text("Pull Requests(\(pulls.count))").tag(Mode.pulls)
            }
            .pickerStyle(.segmented).labelsHidden()
            .padding(10)
            if mode == .runs {
                Toggle("只看失敗", isOn: $failedOnly).toggleStyle(.checkbox).font(.caption)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 12).padding(.bottom, 6)
            }
            Divider()
            if let e = app.github.errors[repo] {
                Label(e, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(10)
            }
            if mode == .runs {
                if runs.isEmpty {
                    EmptyStateView(title: app.github.fetchedAt == nil ? "讀取中…" : (failedOnly ? "沒有失敗的執行" : "沒有執行紀錄"),
                                   icon: "arrow.triangle.branch")
                        .frame(maxHeight: .infinity)
                } else {
                    List(runs, selection: $selectedRun) { r in GHRunRow(run: r, showRepo: false).tag(r.id) }
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

// MARK: - GitLab 專案:App 目前只支援 GitHub

private struct RepoGitLabDetail: View {
    @EnvironmentObject var app: AppState
    let project: String

    var body: some View {
        VStack(spacing: 0) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(project).font(.title3.weight(.semibold)).textSelection(.enabled)
                Pill(text: "GitLab", color: .orange)
                Spacer()
                if let u = GitLab.projectURL(base: app.gitlabSettings.baseUrl, key: project) {
                    Button("在 GitLab 打開") { NSWorkspace.shared.open(u) }
                }
            }
            .padding(.horizontal, 16).padding(.vertical, 10)
            Divider()
            EmptyStateView(title: "App 目前只支援 GitHub 的 PR 與建置", icon: "info.circle",
                           message: "GitLab 的 Merge Requests 與 Pipelines 請在 web 版的 開發進度 › GitLab 看。")
        }
    }
}
