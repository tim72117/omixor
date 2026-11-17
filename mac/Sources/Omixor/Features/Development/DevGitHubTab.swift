import AppKit
import SwiftUI

// 開發進度 › Repositories 用的 GitHub 元件:執行與 PR 的列、執行詳情(含失敗步驟與紀錄)、PR 詳情、前往設定的空狀態。
// 原本獨立的 GitHub 分頁(DevGitHubTab)併進 Repositories(DevRepositoriesTab.swift),這裡只留共用元件。
// 連線、登入與追蹤的 repo 在 設定 › 程式碼倉庫(Features/Settings/SettingsRepos.swift)。

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

struct GHRunRow: View {
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

struct GHPullRow: View {
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

struct GHRunDetail: View {
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

struct GHPullDetailView: View {
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

// MARK: - 程式碼倉庫設定的入口

/// 分頁頂端的小按鈕:到 設定 › 程式碼倉庫 管理連線與追蹤的 repo
struct ManageReposButton: View {
    @EnvironmentObject var app: AppState
    var body: some View {
        Button { app.go(.settings, focus: SettingsPane.repos.rawValue) } label: { Label("管理倉庫", systemImage: "gearshape") }
            .help("在 設定 › 程式碼倉庫 管理連線與追蹤的 repo")
    }
}

/// 還沒設定、沒登入或沒追蹤時的空狀態,附「前往 設定 › 程式碼倉庫」按鈕
struct RepoSetupEmpty: View {
    @EnvironmentObject var app: AppState
    let title: String
    var icon = "chevron.left.forwardslash.chevron.right"
    let message: String

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: icon)
        } description: {
            Text(message)
        } actions: {
            Button("前往 設定 › 程式碼倉庫") { app.go(.settings, focus: SettingsPane.repos.rawValue) }
                .buttonStyle(.borderedProminent)
        }
    }
}
