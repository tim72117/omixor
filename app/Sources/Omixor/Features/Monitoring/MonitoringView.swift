import SwiftUI

/// 監控與告警:告警、儀表板、日誌、規則、網站訪客五個分頁。
struct MonitoringView: View {
    @EnvironmentObject var app: AppState
    @State private var tab: MonTab = .alerts
    @State private var selectedAlertId: String?
    @State private var selectedDashboardId: String?
    @State private var highlightedRuleId: String?

    var body: some View {
        VStack(spacing: 0) {
            header
                .padding(.horizontal, 20)
                .padding(.top, 18)
                .padding(.bottom, 12)
            Divider()
            Group {
                switch app.shownTab(tab) {
                case .alerts: MonAlertsTab(selectedId: $selectedAlertId)
                case .dashboards: MonDashboardsTab(selectedId: $selectedDashboardId)
                case .logs: MonLogsTab()
                case .rules: MonRulesTab(highlightedId: $highlightedRuleId)
                case .visitors: MonVisitorsTab()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        }
        .onAppear { consumeFocus(app.focusId) }
        .onChange(of: app.focusId) { _, id in consumeFocus(id) }
        // 系統設定關掉的分頁:切到第一個開著的並提示(例如從別頁跳過來)
        .onChange(of: tab) { _, t in if let r = app.redirectTab(t) { tab = r } }
    }

    private var header: some View {
        PageHeader("監控與告警", subtitle: subtitle) {
            Picker("分頁", selection: Binding(get: { app.shownTab(tab) }, set: { tab = $0 })) {
                ForEach(app.visibleTabs(MonTab.self)) { t in
                    Label(t.title, systemImage: t.icon).tag(t)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
        }
    }

    private var subtitle: String {
        let firing = app.alerts.filter { $0.state == .firing }
        if firing.isEmpty { return "目前沒有觸發中的告警 · 時間範圍 \(app.timeRange.label)" }
        let crit = firing.filter { $0.severity == .critical }.count
        return "\(firing.count) 則告警觸發中" + (crit > 0 ? "(\(crit) 則 Critical)" : "") + " · 時間範圍 \(app.timeRange.label)"
    }

    /// 從其他頁跳過來時,依 focusId 切到對應分頁並選中項目。
    private func consumeFocus(_ id: String?) {
        guard let id else { return }
        if id == "visitors" {
            tab = .visitors
        } else if app.alerts.contains(where: { $0.id == id }) {
            tab = .alerts
            selectedAlertId = id
        } else if app.alertRules.contains(where: { $0.id == id }) {
            tab = .rules
            highlightedRuleId = id
        } else if app.dashboards.contains(where: { $0.id == id }) {
            tab = .dashboards
            selectedDashboardId = id
        } else {
            return
        }
        app.focusId = nil
    }
}

enum MonTab: String, CaseIterable, Identifiable {
    case alerts, dashboards, logs, rules, visitors
    var id: String { rawValue }
    var title: String {
        switch self {
        case .alerts: return "告警"
        case .dashboards: return "儀表板"
        case .logs: return "日誌"
        case .rules: return "規則"
        case .visitors: return "網站訪客"
        }
    }
    var icon: String {
        switch self {
        case .alerts: return "bell.badge"
        case .dashboards: return "chart.xyaxis.line"
        case .logs: return "text.alignleft"
        case .rules: return "list.bullet.rectangle"
        case .visitors: return "person.3"
        }
    }
}

// MARK: - 共用小工具

extension Severity {
    var monIcon: String {
        switch self {
        case .critical: return "exclamationmark.octagon.fill"
        case .warning: return "exclamationmark.triangle.fill"
        case .info: return "info.circle.fill"
        }
    }
}

extension AlertState {
    var monColor: Color {
        switch self {
        case .firing: return .red
        case .acknowledged: return .orange
        case .resolved: return .green
        case .silenced: return .gray
        }
    }
    var monIcon: String {
        switch self {
        case .firing: return "flame.fill"
        case .acknowledged: return "eye.fill"
        case .resolved: return "checkmark.circle.fill"
        case .silenced: return "bell.slash.fill"
        }
    }
}

extension LogLevel {
    var monLabel: String { rawValue.uppercased() }
}

/// 紫色的 AI 判讀框
struct MonAICallout: View {
    let title: String
    let text: String
    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "sparkles").foregroundStyle(.purple).padding(.top, 1)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.caption.weight(.semibold)).foregroundStyle(.purple)
                Text(text).font(.callout).foregroundStyle(.primary.opacity(0.85))
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }
            Spacer(minLength: 0)
        }
        .padding(10)
        .background(Color.purple.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.purple.opacity(0.2)))
    }
}

/// 可切換的篩選膠囊
struct MonToggleChip: View {
    let title: String
    var count: Int?
    let color: Color
    let isOn: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 5) {
                Circle().fill(color).frame(width: 7, height: 7).opacity(isOn ? 1 : 0.35)
                Text(title)
                if let count { Text("\(count)").monospacedDigit().foregroundStyle(.secondary) }
            }
            .font(.caption.weight(.medium))
            .padding(.horizontal, 9).padding(.vertical, 4)
            .background(isOn ? color.opacity(0.14) : Color.primary.opacity(0.04), in: Capsule())
            .overlay(Capsule().strokeBorder(isOn ? color.opacity(0.35) : Color.primary.opacity(0.08)))
            .foregroundStyle(isOn ? .primary : .secondary)
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
    }
}
