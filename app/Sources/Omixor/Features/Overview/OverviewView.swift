import SwiftUI

// 總覽:一眼看完服務狀態、需要處理的事、交付進度與最近動態。

struct OverviewView: View {
    @EnvironmentObject var app: AppState
    @State private var width: CGFloat = 1000

    /// 內容寬度超過這個值就改成「主欄 + 側欄」兩欄
    private let twoColumnBreakpoint: CGFloat = 940

    var body: some View {
        PageScroll {
            PageHeader("總覽", subtitle: "\(app.env.label) 環境 · 最近 \(app.timeRange.label)") {
                HStack(spacing: 8) {
                    EnvBadge(env: app.env)
                    Button {
                        app.showCommandPalette = true
                    } label: {
                        Label("搜尋", systemImage: "magnifyingglass")
                    }
                    .help("開啟指令面板")
                }
            }
            OverviewStatusBanner()
            OverviewKPIRow()
            if width >= twoColumnBreakpoint {
                HStack(alignment: .top, spacing: 16) {
                    VStack(alignment: .leading, spacing: 16) {
                        OverviewServicesCard()
                        OverviewDeliveryCard()
                        HStack(alignment: .top, spacing: 16) {
                            OverviewSprintCard()
                            OverviewErrorBudgetCard()
                        }
                        OverviewActivityCard()
                    }
                    .frame(maxWidth: .infinity)
                    VStack(alignment: .leading, spacing: 16) {
                        OverviewApprovalsCard()
                        OverviewAlertsCard()
                        OverviewIncidentsCard()
                        OverviewOnCallCard()
                    }
                    .frame(width: min(420, max(320, width * 0.32)))
                }
            } else {
                OverviewAlertsCard()
                OverviewApprovalsCard()
                OverviewIncidentsCard()
                OverviewServicesCard()
                OverviewDeliveryCard()
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 280), spacing: 16, alignment: .top)], spacing: 16) {
                    OverviewSprintCard()
                    OverviewErrorBudgetCard()
                }
                OverviewOnCallCard()
                OverviewActivityCard()
            }
        }
        .background(
            GeometryReader { g in
                Color.clear
                    .onAppear { width = g.size.width - 40 }
                    .onChange(of: g.size.width) { _, w in width = w - 40 }
            }
        )
    }
}

// MARK: - 狀態橫幅

struct OverviewStatusBanner: View {
    @EnvironmentObject var app: AppState

    private enum Level { case unknown, ok, degraded, major }

    private var down: [Service] { app.services.filter { $0.health == .down } }
    private var degraded: [Service] { app.services.filter { $0.health == .degraded } }
    private var maintenance: [Service] { app.services.filter { $0.health == .maintenance } }
    private var criticalFiring: Int { app.firingAlerts.filter { $0.severity == .critical }.count }
    private var majorIncidents: Int { app.openIncidents.filter { $0.sev <= 2 }.count }

    private var level: Level {
        if app.services.isEmpty && app.alerts.isEmpty && app.incidents.isEmpty { return .unknown }
        if !down.isEmpty || criticalFiring > 0 || majorIncidents > 0 { return .major }
        if !degraded.isEmpty || !app.firingAlerts.isEmpty || !app.openIncidents.isEmpty { return .degraded }
        return .ok
    }

    private var color: Color {
        switch level {
        case .unknown: return .gray
        case .ok: return .green
        case .degraded: return .orange
        case .major: return .red
        }
    }

    private var icon: String {
        switch level {
        case .unknown: return "questionmark.circle.fill"
        case .ok: return "checkmark.seal.fill"
        case .degraded: return "exclamationmark.triangle.fill"
        case .major: return "xmark.octagon.fill"
        }
    }

    private var title: String {
        switch level {
        case .unknown: return "尚未取得監控資料"
        case .ok: return "所有系統運作正常"
        case .degraded:
            let n = degraded.count
            return n > 0 ? "\(n) 個服務效能下降" : "有待處理的告警"
        case .major:
            if !down.isEmpty { return "\(down.count) 個服務中斷" }
            if majorIncidents > 0 { return "重大事件處理中" }
            return "有 Critical 告警觸發中"
        }
    }

    private var detail: String {
        switch level {
        case .unknown: return "連上監控整合後,這裡會顯示所有服務的即時狀態。"
        case .ok:
            let extra = maintenance.isEmpty ? "" : ",\(maintenance.count) 個維護中"
            return "\(app.services.count) 個服務全部正常\(extra)。"
        case .degraded, .major:
            var parts: [String] = []
            let affected = (down + degraded).map(\.name)
            if !affected.isEmpty { parts.append("受影響:" + affected.prefix(4).joined(separator: "、") + (affected.count > 4 ? " 等" : "")) }
            if !app.firingAlerts.isEmpty { parts.append("\(app.firingAlerts.count) 則告警觸發中") }
            if !app.openIncidents.isEmpty { parts.append("\(app.openIncidents.count) 起事件未結案") }
            return parts.joined(separator: " · ")
        }
    }

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 26))
                .foregroundStyle(color)
                .symbolRenderingMode(.hierarchical)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.headline)
                Text(detail).font(.subheadline).foregroundStyle(.secondary).lineLimit(2)
            }
            Spacer(minLength: 12)
            if level == .major || level == .degraded {
                if !app.openIncidents.isEmpty {
                    Button("查看事件") { app.go(.incidents, focus: app.openIncidents.first?.id) }
                }
                Button("查看告警") { app.go(.monitoring, focus: app.firingAlerts.first?.id) }
                    .buttonStyle(.borderedProminent)
                    .tint(color)
            } else if level == .ok {
                Button("服務列表") { app.go(.infrastructure) }
            }
        }
        .padding(14)
        .background(color.opacity(0.09), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(color.opacity(0.25)))
    }
}

// MARK: - KPI

struct OverviewKPIRow: View {
    @EnvironmentObject var app: AppState

    private var avgUptime: Double? {
        guard !app.services.isEmpty else { return nil }
        return app.services.map(\.uptime30d).reduce(0, +) / Double(app.services.count)
    }

    private var sloTarget: Double {
        app.services.map(\.sloTarget).max() ?? 99.9
    }

    /// 依流量加權的 p95(沒有流量資料時用平均)
    private var p95: Double? {
        let s = app.services
        guard !s.isEmpty else { return nil }
        let total = s.map(\.rps).reduce(0, +)
        if total > 0 { return s.map { $0.latencyP95 * $0.rps }.reduce(0, +) / total }
        return s.map(\.latencyP95).reduce(0, +) / Double(s.count)
    }

    private var errorRate: Double? {
        let s = app.services
        guard !s.isEmpty else { return nil }
        let total = s.map(\.rps).reduce(0, +)
        if total > 0 { return s.map { $0.errorRate * $0.rps }.reduce(0, +) / total }
        return s.map(\.errorRate).reduce(0, +) / Double(s.count)
    }

    private var deploysToday: [Deployment] {
        app.deployments.filter { $0.env == app.env && Calendar.current.isDateInToday($0.startedAt) }
    }

    private var aiRunsToday: [AutomationRun] {
        app.automationRuns.filter { Calendar.current.isDateInToday($0.startedAt) }
    }

    /// 今天 AI 省下的時間:每個自動化以 30 天平均每次省下的時間估算
    private var aiHoursToday: Double {
        aiRunsToday.reduce(0) { sum, run in
            guard let a = app.automation(run.automation), a.runs30d > 0 else { return sum }
            return sum + a.timeSavedHours / Double(a.runs30d)
        }
    }

    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), spacing: 12)], spacing: 12) {
            OverviewKPITile(
                title: "可用性(30 天)",
                value: avgUptime.map { Fmt.percent($0, digits: 2) } ?? "—",
                caption: "SLO \(Fmt.percent(sloTarget, digits: 2))",
                captionColor: (avgUptime ?? 100) >= sloTarget ? .green : .red,
                icon: "checkmark.shield",
                tint: .green
            ) { app.go(.monitoring) }
            OverviewKPITile(
                title: "p95 延遲",
                value: p95.map { "\(Int($0.rounded())) ms" } ?? "—",
                caption: p95.map { $0 > 500 ? "高於 500 ms 目標" : "在 500 ms 目標內" } ?? "尚無資料",
                captionColor: (p95 ?? 0) > 500 ? .orange : .secondary,
                icon: "speedometer",
                tint: .blue
            ) { app.go(.monitoring) }
            OverviewKPITile(
                title: "錯誤率",
                value: errorRate.map { Fmt.percent($0, digits: 2) } ?? "—",
                caption: errorRate.map { $0 > 1 ? "超過 1% 警戒" : "低於 1% 警戒" } ?? "尚無資料",
                captionColor: (errorRate ?? 0) > 1 ? .red : .secondary,
                icon: "exclamationmark.bubble",
                tint: .orange
            ) { app.go(.monitoring) }
            OverviewKPITile(
                title: "今日部署",
                value: "\(deploysToday.count)",
                caption: deployCaption,
                captionColor: deploysToday.contains { $0.status == .failed } ? .red : .secondary,
                icon: "shippingbox",
                tint: .indigo
            ) { app.go(.pipelines) }
            OverviewKPITile(
                title: "進行中事件",
                value: "\(app.openIncidents.count)",
                caption: app.openIncidents.map(\.sev).min().map { "最高 SEV\($0)" } ?? "目前沒有事件",
                captionColor: app.openIncidents.isEmpty ? .secondary : .red,
                icon: "flame",
                tint: app.openIncidents.isEmpty ? .secondary : .red
            ) { app.go(.incidents, focus: app.openIncidents.first?.id) }
            OverviewKPITile(
                title: "AI 今日動作",
                value: "\(aiRunsToday.count)",
                caption: aiHoursToday > 0 ? "約省下 \(String(format: "%.1f", aiHoursToday)) 小時" : "\(app.pendingApprovals.count) 項等你決定",
                captionColor: .purple,
                icon: "sparkles",
                tint: .purple
            ) { app.go(.automation) }
        }
    }

    private var deployCaption: String {
        let failed = deploysToday.filter { $0.status == .failed }.count
        let running = deploysToday.filter { $0.status == .running }.count
        if failed > 0 { return "\(failed) 次失敗" }
        if running > 0 { return "\(running) 個進行中" }
        return deploysToday.isEmpty ? "今天還沒有部署" : "全部成功"
    }
}

struct OverviewKPITile: View {
    let title: String
    let value: String
    let caption: String
    var captionColor: Color = .secondary
    let icon: String
    let tint: Color
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    Image(systemName: icon).foregroundStyle(tint)
                    Text(title).font(.subheadline).foregroundStyle(.secondary).lineLimit(1)
                }
                Text(value)
                    .font(.system(size: 26, weight: .semibold, design: .rounded))
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
                Text(caption).font(.caption.weight(.medium)).foregroundStyle(captionColor).lineLimit(1)
            }
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .overviewSurface()
        }
        .buttonStyle(OverviewPressStyle())
    }
}

// MARK: - 共用小工具(只在總覽用)

struct OverviewPressStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        OverviewHoverBody(configuration: configuration)
    }

    private struct OverviewHoverBody: View {
        let configuration: ButtonStyleConfiguration
        @State private var hovering = false
        var body: some View {
            configuration.label
                .contentShape(Rectangle())
                .brightness(hovering ? 0.02 : 0)
                .opacity(configuration.isPressed ? 0.75 : 1)
                .onHover { hovering = $0 }
        }
    }
}

extension View {
    /// 和 Card 相同的底色與邊框
    func overviewSurface(cornerRadius: CGFloat = 12) -> some View {
        self
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: cornerRadius))
            .overlay(RoundedRectangle(cornerRadius: cornerRadius).strokeBorder(Color.primary.opacity(0.07)))
    }
}

/// 卡片標題列:標題、數量、右側「全部」連結
struct OverviewCardHeader: View {
    let title: String
    var count: Int?
    var countColor: Color = .secondary
    var linkTitle = "全部"
    var action: (() -> Void)?

    var body: some View {
        HStack(spacing: 8) {
            Text(title).font(.headline)
            if let count, count > 0 {
                Text("\(count)")
                    .font(.caption.weight(.semibold).monospacedDigit())
                    .padding(.horizontal, 6).padding(.vertical, 1)
                    .foregroundStyle(countColor == .secondary ? Color.secondary : Color.white)
                    .background(countColor == .secondary ? Color.primary.opacity(0.08) : countColor, in: Capsule())
            }
            Spacer()
            if let action {
                Button(action: action) {
                    HStack(spacing: 2) {
                        Text(linkTitle)
                        Image(systemName: "chevron.right").font(.caption2.weight(.semibold))
                    }
                    .font(.caption)
                }
                .buttonStyle(.link)
            }
        }
    }
}

/// 卡片內的空狀態(比 ContentUnavailableView 小)
struct OverviewInlineEmpty: View {
    let icon: String
    let text: String
    var color: Color = .secondary

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: icon).foregroundStyle(color)
            Text(text).foregroundStyle(.secondary)
        }
        .font(.subheadline)
        .frame(maxWidth: .infinity, alignment: .center)
        .padding(.vertical, 14)
    }
}

/// 環形進度
struct OverviewRing: View {
    let progress: Double
    let color: Color
    var lineWidth: CGFloat = 10

    var body: some View {
        ZStack {
            Circle().stroke(Color.primary.opacity(0.08), lineWidth: lineWidth)
            Circle()
                .trim(from: 0, to: min(1, max(0, progress)))
                .stroke(color.gradient, style: StrokeStyle(lineWidth: lineWidth, lineCap: .round))
                .rotationEffect(.degrees(-90))
        }
    }
}

func overviewSevColor(_ sev: Int) -> Color {
    switch sev {
    case 1: return .red
    case 2: return .orange
    case 3: return .yellow
    default: return .blue
    }
}
