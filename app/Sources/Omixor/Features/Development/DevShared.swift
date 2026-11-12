import SwiftUI

// 開發進度頁內部共用:分頁、跨分頁狀態、計算指標、小元件。

enum DevTab: String, CaseIterable, Identifiable {
    case overview, board, issues, pulls, repos, github
    var id: String { rawValue }
    var label: String {
        switch self {
        case .overview: return "概覽"
        case .board: return "Sprint 看板"
        case .issues: return "Issue"
        case .pulls: return "Pull Requests"
        case .repos: return "Repositories"
        case .github: return "GitHub"
        }
    }
    var icon: String {
        switch self {
        case .overview: return "chart.bar.xaxis"
        case .board: return "rectangle.split.3x1"
        case .issues: return "exclamationmark.bubble"
        case .pulls: return "arrow.triangle.pull"
        case .repos: return "shippingbox"
        case .github: return "chevron.left.forwardslash.chevron.right"
        }
    }
}

/// 開發頁各分頁之間共用的狀態(例如從概覽點 PR → 切到 PR 分頁並選中)
@MainActor
final class DevModel: ObservableObject {
    @Published var tab: DevTab = .overview
    @Published var selectedPR: PullRequest.ID?
    @Published var prRepoFilter: String?          // nil = 全部
    @Published var highlightedItem: WorkItem.ID?
    @Published var selectedIssue: OpsIssue.ID?

    func showIssue(_ id: String) {
        selectedIssue = id
        tab = .issues
    }

    func showPR(_ id: Int) {
        selectedPR = id
        tab = .pulls
    }

    func showRepoPRs(_ repo: String) {
        prRepoFilter = repo
        selectedPR = nil
        tab = .pulls
    }

    func showItem(_ id: String) {
        highlightedItem = id
        tab = .board
    }
}

// MARK: - 指標

enum DevMetrics {
    static func isActive(_ pr: PullRequest) -> Bool {
        pr.status != .merged && pr.status != .closed
    }

    static func sprintPoints(_ items: [WorkItem]) -> (done: Int, total: Int) {
        let active = items.filter { $0.status != .backlog }
        return (active.filter { $0.status == .done }.reduce(0) { $0 + $1.points },
                active.reduce(0) { $0 + $1.points })
    }

    /// 已合併 PR 的平均開啟到合併時間(小時)
    static func cycleTimeHours(_ prs: [PullRequest]) -> Double? {
        let merged = prs.filter { $0.status == .merged }
        guard !merged.isEmpty else { return nil }
        let total = merged.reduce(0.0) { $0 + $1.updatedAt.timeIntervalSince($1.createdAt) }
        return total / Double(merged.count) / 3600
    }

    /// 待審 PR 平均已等待時間(小時)
    static func reviewWaitHours(_ prs: [PullRequest]) -> Double? {
        let waiting = prs.filter { $0.status == .open }
        guard !waiting.isEmpty else { return nil }
        let total = waiting.reduce(0.0) { $0 + Date.now.timeIntervalSince($1.createdAt) }
        return total / Double(waiting.count) / 3600
    }

    static func aiShare(_ prs: [PullRequest]) -> Double? {
        guard !prs.isEmpty else { return nil }
        return Double(prs.filter { $0.author.devIsAI }.count) / Double(prs.count)
    }

    static func avgCoverage(_ repos: [Repository]) -> Double? {
        guard !repos.isEmpty else { return nil }
        return repos.reduce(0) { $0 + $1.coverage } / Double(repos.count)
    }

    static func hours(_ h: Double?) -> String {
        guard let h else { return "—" }
        if h < 1 { return "\(Int(h * 60)) 分" }
        if h < 48 { return String(format: "%.1f 小時", h) }
        return String(format: "%.1f 天", h / 24)
    }

    static func age(_ date: Date) -> String {
        let s = Date.now.timeIntervalSince(date)
        if s < 3600 { return "\(max(1, Int(s / 60))) 分" }
        if s < 86400 { return "\(Int(s / 3600)) 小時" }
        return "\(Int(s / 86400)) 天"
    }
}

extension Actor {
    var devIsAI: Bool { if case .ai = self { return true } else { return false } }
    var devPersonId: String? { if case .person(let id) = self { return id } else { return nil } }
}

// MARK: - 小元件

struct DevChecksIcon: View {
    let status: CheckStatus
    var showLabel = false
    var label: String {
        switch status {
        case .pending: return "等待中"
        case .running: return "執行中"
        case .passed: return "通過"
        case .failed: return "失敗"
        }
    }
    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: status.icon).foregroundStyle(status.color)
            if showLabel { Text(label).foregroundStyle(status.color) }
        }
        .help("檢查:\(label)")
    }
}

struct DevDiffStat: View {
    let additions: Int
    let deletions: Int
    var body: some View {
        HStack(spacing: 6) {
            Text("+\(additions)").foregroundStyle(.green)
            Text("−\(deletions)").foregroundStyle(.red)
        }
        .font(.callout.monospacedDigit())
    }
}

struct DevAvatarStack: View {
    let ids: [String]
    var size: CGFloat = 20
    var body: some View {
        HStack(spacing: -6) {
            ForEach(ids.prefix(4), id: \.self) { id in
                ActorAvatar(actor: .person(id), size: size)
                    .overlay(Circle().stroke(Color(nsColor: .controlBackgroundColor), lineWidth: 1.5))
            }
            if ids.count > 4 {
                Text("+\(ids.count - 4)").font(.caption2).foregroundStyle(.secondary).padding(.leading, 8)
            }
        }
    }
}

/// 覆蓋率條:越高越好(與 UsageBar 的顏色意義相反)
struct DevCoverageBar: View {
    let value: Double
    var color: Color { value >= 80 ? .green : value >= 60 ? .orange : .red }
    var body: some View {
        HStack(spacing: 8) {
            GeometryReader { g in
                ZStack(alignment: .leading) {
                    Capsule().fill(Color.primary.opacity(0.08))
                    Capsule().fill(color.gradient).frame(width: g.size.width * min(1, max(0, value / 100)))
                }
            }
            .frame(height: 6)
            Text(Fmt.percent(value, digits: 0)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                .frame(width: 36, alignment: .trailing)
        }
    }
}

struct DevTypeTag: View {
    let type: WorkType
    var body: some View {
        HStack(spacing: 4) {
            RoundedRectangle(cornerRadius: 2).fill(type.color).frame(width: 8, height: 8)
            Text(type.label)
        }
        .font(.caption)
        .foregroundStyle(.secondary)
    }
}

extension WorkStatus {
    var devIcon: String {
        switch self {
        case .backlog: return "tray"
        case .todo: return "circle"
        case .inProgress: return "circle.lefthalf.filled"
        case .review: return "eye"
        case .done: return "checkmark.circle.fill"
        }
    }
    var devColor: Color {
        switch self {
        case .backlog: return .gray
        case .todo: return .secondary
        case .inProgress: return .blue
        case .review: return .purple
        case .done: return .green
        }
    }
}
