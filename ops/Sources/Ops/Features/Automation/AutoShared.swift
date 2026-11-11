import AppKit
import SwiftUI

// AI 自動化畫面內共用的小元件與輔助型別(只在 Features/Automation 內使用)。

enum AutoTab: String, CaseIterable, Identifiable {
    case inbox, agents, facebook, flows, runs, insights
    var id: String { rawValue }
    var label: String {
        switch self {
        case .inbox: return "待核准"
        case .agents: return "虛擬角色"
        case .facebook: return "粉絲專頁"
        case .flows: return "自動化流程"
        case .runs: return "執行紀錄"
        case .insights: return "成效"
        }
    }
    var icon: String {
        switch self {
        case .inbox: return "tray.full"
        case .agents: return "person.2"
        case .facebook: return "megaphone"
        case .flows: return "point.3.connected.trianglepath.dotted"
        case .runs: return "list.bullet.rectangle"
        case .insights: return "chart.bar.xaxis"
        }
    }
}

enum AutoTheme {
    static let ai = Color.indigo
    static let gradient = LinearGradient(colors: [.purple, .indigo], startPoint: .topLeading, endPoint: .bottomTrailing)
    static let codeBackground = Color(nsColor: .textBackgroundColor)
}

/// 穩定的雜湊(Swift 內建 hashValue 每次啟動不同,不能用來產生固定的模擬數字)
enum AutoSeed {
    static func value(_ s: String) -> Int {
        var h: UInt64 = 1469598103934665603
        for b in s.utf8 { h = (h ^ UInt64(b)) &* 1099511628211 }
        return Int(h % 1_000_000)
    }
    /// 0...1 的固定亂數
    static func unit(_ s: String) -> Double { Double(value(s) % 10_000) / 10_000 }
}

/// 等寬程式碼區塊,附複製按鈕
struct AutoCodeBlock: View {
    let text: String
    var maxHeight: CGFloat? = nil
    var tint: Color = .primary
    @State private var copied = false

    /// 粗估行數(含自動換行),超過才用捲動區,避免短指令也撐出很高的框
    private var needsScroll: Bool {
        let lines = text.split(separator: "\n", omittingEmptySubsequences: false)
        let visual = lines.reduce(0) { $0 + max(1, $1.count / 90 + 1) }
        return visual > 11
    }

    private var textBody: some View {
        Text(text)
            .font(.system(.callout, design: .monospaced))
            .foregroundStyle(tint)
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(10)
            .padding(.trailing, 28)
    }

    var body: some View {
        ZStack(alignment: .topTrailing) {
            if let maxHeight, needsScroll {
                ScrollView(.vertical) { textBody }
                    .frame(height: maxHeight)
            } else {
                textBody
            }

            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(text, forType: .string)
                copied = true
                DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { copied = false }
            } label: {
                Image(systemName: copied ? "checkmark" : "doc.on.doc")
                    .font(.caption)
                    .frame(width: 22, height: 22)
            }
            .buttonStyle(.borderless)
            .help(copied ? "已複製" : "複製")
            .padding(4)
        }
        .background(AutoTheme.codeBackground.opacity(0.7), in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.1)))
    }
}

struct AutoRiskBadge: View {
    let risk: RiskLevel
    var body: some View {
        Pill(text: risk.label, color: risk.color, icon: risk == .high ? "exclamationmark.triangle.fill" : "shield.lefthalf.filled")
    }
}

struct AutoAutonomyBadge: View {
    let autonomy: Autonomy
    var body: some View {
        Pill(text: autonomy.label, color: autonomy.color, icon: AutoAutonomyInfo.icon(autonomy))
    }
}

enum AutoAutonomyInfo {
    static func icon(_ a: Autonomy) -> String {
        switch a {
        case .suggest: return "lightbulb"
        case .approve: return "hand.raised"
        case .auto: return "bolt.fill"
        }
    }
}

/// AI 圖示方塊(紫靛漸層)
struct AutoIconTile: View {
    let icon: String
    var size: CGFloat = 34
    var dimmed = false
    var body: some View {
        RoundedRectangle(cornerRadius: size * 0.26)
            .fill(dimmed ? AnyShapeStyle(Color.gray.opacity(0.35)) : AnyShapeStyle(AutoTheme.gradient))
            .frame(width: size, height: size)
            .overlay(Image(systemName: icon).font(.system(size: size * 0.45, weight: .medium)).foregroundStyle(.white))
    }
}

struct AutoStepIcon: View {
    let status: AIStepStatus
    var body: some View {
        Group {
            switch status {
            case .done: Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
            case .running: ProgressView().controlSize(.small)
            case .pending: Image(systemName: "circle.dashed").foregroundStyle(.secondary)
            case .failed: Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
            case .skipped: Image(systemName: "arrow.uturn.right.circle").foregroundStyle(.secondary)
            }
        }
        .frame(width: 18, height: 18)
    }
}

/// 標籤列(會自動換行)
struct AutoFlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowH: CGFloat = 0, maxX: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > 0 && x + s.width > width { x = 0; y += rowH + spacing; rowH = 0 }
            x += s.width + spacing
            maxX = max(maxX, x - spacing)
            rowH = max(rowH, s.height)
        }
        return CGSize(width: proposal.width ?? maxX, height: y + rowH)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, rowH: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > bounds.minX && x + s.width > bounds.maxX { x = bounds.minX; y += rowH + spacing; rowH = 0 }
            v.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(s))
            x += s.width + spacing
            rowH = max(rowH, s.height)
        }
    }
}

struct AutoChip: View {
    let text: String
    var icon: String? = nil
    var mono = true
    var body: some View {
        HStack(spacing: 3) {
            if let icon { Image(systemName: icon).font(.caption2) }
            Text(text).font(mono ? .system(.caption, design: .monospaced) : .caption)
        }
        .padding(.horizontal, 6).padding(.vertical, 2)
        .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 5))
        .overlay(RoundedRectangle(cornerRadius: 5).strokeBorder(Color.primary.opacity(0.08)))
    }
}

/// 倒數(到期時間)
struct AutoCountdown: View {
    let expiresAt: Date
    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { ctx in
            let left = expiresAt.timeIntervalSince(ctx.date)
            let urgent = left < 15 * 60
            HStack(spacing: 4) {
                Image(systemName: left <= 0 ? "hourglass.bottomhalf.filled" : "hourglass")
                Text(left <= 0 ? "已逾時" : "剩 \(Self.format(left))")
                    .monospacedDigit()
            }
            .font(.caption.weight(.medium))
            .foregroundStyle(left <= 0 ? Color.secondary : urgent ? Color.red : Color.orange)
            .help("到期時間 \(expiresAt.formatted(date: .abbreviated, time: .shortened))")
        }
    }

    static func format(_ t: TimeInterval) -> String {
        let s = Int(t)
        if s >= 3600 { return "\(s / 3600) 小時 \(s % 3600 / 60) 分" }
        if s >= 60 { return String(format: "%d:%02d", s / 60, s % 60) }
        return "\(s) 秒"
    }
}

/// 安全護欄:目前模型沒有這些欄位,依自動化的範圍與工具推算出固定值。
struct AutoGuardrails {
    let environments: [String]
    let blastRadius: String
    let monthlyBudgetUSD: Double
    let spentUSD: Double
    let maxActionsPerHour: Int
    let requiresDryRun: Bool
    let autoRollback: Bool
    let forbidden: [String]

    static func of(_ a: Automation, runs: [AutomationRun]) -> AutoGuardrails {
        let text = (a.scope + a.allowedTools + [a.name, a.summary]).joined(separator: " ").lowercased()
        var envs: [String] = []
        if text.contains("prod") || text.contains("正式") { envs.append("正式環境") }
        if text.contains("staging") || text.contains("預備") { envs.append("預備環境") }
        if envs.isEmpty { envs = ["預備環境", "開發環境"] }
        let seed = AutoSeed.value(a.id)
        let mine = runs.filter { $0.automation == a.id }
        let avg = mine.isEmpty ? 0.12 + Double(seed % 30) / 100 : mine.map(\.costUSD).reduce(0, +) / Double(mine.count)
        let spent = avg * Double(max(a.runs30d, mine.count))
        let budget = max(20, (spent * 1.8 / 10).rounded(.up) * 10)
        let radius: String
        switch a.autonomy {
        case .suggest: radius = "不做變更"
        case .approve: radius = "每次最多 \(2 + seed % 4) 個資源"
        case .auto: radius = "每次最多 1 個服務、\(10 + seed % 4 * 5)% 流量"
        }
        let destructive = text.contains("terraform") || text.contains("kubectl") || text.contains("deploy")
        return AutoGuardrails(environments: envs,
                              blastRadius: radius,
                              monthlyBudgetUSD: budget,
                              spentUSD: spent,
                              maxActionsPerHour: 3 + seed % 8,
                              requiresDryRun: destructive,
                              autoRollback: a.autonomy == .auto || destructive,
                              forbidden: ["刪除資料庫", "變更 IAM 權限", "關閉告警規則"])
    }
}

/// 某個自動化目前待核准的請求
extension AppState {
    func autoPendingApproval(for automationId: String) -> ApprovalRequest? {
        pendingApprovals.first { $0.automation == automationId }
    }
}
