import Charts
import SwiftUI

/// 成效:AI 自動化幫團隊省了多少時間、花了多少錢、可不可靠。
struct AutoInsightsView: View {
    @EnvironmentObject var app: AppState
    var openAutomation: (String) -> Void
    @State private var sort: AutoInsightSort = .hours

    private struct DayCount: Identifiable {
        let id = UUID()
        let day: Date
        let name: String
        let count: Int
    }

    private struct DayCost: Identifiable {
        let id = UUID()
        let day: Date
        let cost: Double
    }

    private var totalRuns: Int { app.automations.map(\.runs30d).reduce(0, +) }
    private var totalHours: Double { app.automations.map(\.timeSavedHours).reduce(0, +) }
    private var weightedSuccess: Double {
        guard totalRuns > 0 else { return 0 }
        return app.automations.map { $0.successRate * Double($0.runs30d) }.reduce(0, +) / Double(totalRuns)
    }

    /// 每次執行的平均成本(有真實紀錄就用紀錄,沒有就用固定估計)
    private func avgCost(_ a: Automation) -> Double {
        let mine = app.automationRuns.filter { $0.automation == a.id }
        if mine.isEmpty { return 0.05 + AutoSeed.unit(a.id + "cost") * 0.3 }
        return mine.map(\.costUSD).reduce(0, +) / Double(mine.count)
    }

    private func cost30d(_ a: Automation) -> Double { avgCost(a) * Double(a.runs30d) }
    private var totalCost: Double { app.automations.map(cost30d).reduce(0, +) }

    private var days: [Date] {
        let cal = Calendar.current
        let today = cal.startOfDay(for: .now)
        return (0..<14).reversed().compactMap { cal.date(byAdding: .day, value: -$0, to: today) }
    }

    /// 前幾名分開顯示,其餘合併成「其他」,避免顏色太多
    private var topIds: [String] {
        app.automations.sorted { $0.runs30d > $1.runs30d }.prefix(5).map(\.id)
    }

    private func dailyRuns(_ a: Automation, dayIndex i: Int) -> Int {
        let base = Double(a.runs30d) / 30
        let noise = 0.55 + AutoSeed.unit("\(a.id)-\(i)") * 0.9
        let cal = Calendar.current
        let actual = app.automationRuns.filter { $0.automation == a.id && cal.isDate($0.startedAt, inSameDayAs: days[i]) }.count
        return max(actual, Int((base * noise).rounded()))
    }

    private var runsByDay: [DayCount] {
        var out: [DayCount] = []
        let top = Set(topIds)
        for i in days.indices {
            var other = 0
            for a in app.automations {
                let n = dailyRuns(a, dayIndex: i)
                if top.contains(a.id) {
                    out.append(DayCount(day: days[i], name: a.name, count: n))
                } else {
                    other += n
                }
            }
            if other > 0 { out.append(DayCount(day: days[i], name: "其他", count: other)) }
        }
        return out
    }

    private var costByDay: [DayCost] {
        days.indices.map { i in
            DayCost(day: days[i], cost: app.automations.map { Double(dailyRuns($0, dayIndex: i)) * avgCost($0) }.reduce(0, +))
        }
    }

    private var sorted: [Automation] {
        switch sort {
        case .hours: return app.automations.sorted { $0.timeSavedHours > $1.timeSavedHours }
        case .runs: return app.automations.sorted { $0.runs30d > $1.runs30d }
        case .success: return app.automations.sorted { $0.successRate > $1.successRate }
        case .cost: return app.automations.sorted { cost30d($0) > cost30d($1) }
        }
    }

    var body: some View {
        PageScroll {
            if app.automations.isEmpty {
                Card {
                    EmptyStateView(title: "還沒有成效資料", icon: "chart.bar.xaxis",
                                   message: "啟用 AI 自動化流程後,這裡會統計執行次數、成功率與省下的時間。")
                        .frame(maxWidth: .infinity, minHeight: 240)
                }
            } else {
                tiles
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 380), spacing: 16, alignment: .top)], spacing: 16) {
                    runsChart
                    hoursChart
                    successChart
                    costChart
                }
                table
            }
        }
    }

    // MARK: 指標

    private var tiles: some View {
        let decided = app.approvals.filter { $0.state != .pending }
        let approved = decided.filter { $0.state == .approved }.count
        let approvalRate = decided.isEmpty ? nil : Double(approved) / Double(decided.count)
        return LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), spacing: 12)], spacing: 12) {
            MetricTile(title: "30 天執行", value: "\(totalRuns)", icon: "play.circle", tint: .indigo,
                       series: days.indices.map { i in Double(app.automations.map { dailyRuns($0, dayIndex: i) }.reduce(0, +)) })
            MetricTile(title: "整體成功率", value: Fmt.percent(weightedSuccess * 100), icon: "checkmark.circle", tint: .green)
            MetricTile(title: "省下人力", value: String(format: "%.0f 小時", totalHours),
                       delta: String(format: "≈ %.1f 人日", totalHours / 8), icon: "clock.arrow.circlepath", tint: .purple)
            MetricTile(title: "AI 成本(30 天)", value: String(format: "$%.2f", totalCost),
                       delta: totalHours > 0 ? String(format: "每省 1 小時 $%.2f", totalCost / totalHours) : nil,
                       icon: "dollarsign.circle", tint: .orange)
            MetricTile(title: "人工核准通過率", value: approvalRate.map { Fmt.percent($0 * 100, digits: 0) } ?? "—",
                       delta: decided.isEmpty ? nil : "\(decided.count) 件已處理", icon: "hand.raised", tint: .purple)
        }
    }

    // MARK: 圖表

    private var runsChart: some View {
        Card("每日執行次數", subtitle: "近 14 天,依流程分色") {
            Chart(runsByDay) { d in
                BarMark(x: .value("日期", d.day, unit: .day), y: .value("次數", d.count))
                    .foregroundStyle(by: .value("流程", d.name))
            }
            .chartXAxis { AxisMarks(values: .stride(by: .day, count: 2)) { _ in
                AxisGridLine(); AxisValueLabel(format: .dateTime.month(.defaultDigits).day())
            } }
            .chartLegend(position: .bottom, alignment: .leading)
            .frame(height: 230)
        }
    }

    private var hoursChart: some View {
        let data = app.automations.sorted { $0.timeSavedHours > $1.timeSavedHours }.prefix(8)
        return Card("省下的時間", subtitle: "30 天,小時") {
            Chart(Array(data)) { a in
                BarMark(x: .value("小時", a.timeSavedHours), y: .value("流程", a.name))
                    .foregroundStyle(AutoTheme.gradient)
                    .cornerRadius(4)
                    .annotation(position: .trailing) {
                        Text(String(format: "%.1f", a.timeSavedHours)).font(.caption2).foregroundStyle(.secondary)
                    }
            }
            .chartXAxis { AxisMarks { _ in AxisGridLine(); AxisValueLabel() } }
            .frame(height: max(150, CGFloat(data.count) * 28))
        }
    }

    private var successChart: some View {
        let data = app.automations.sorted { $0.successRate < $1.successRate }.prefix(8)
        return Card("成功率", subtitle: "低於 90% 的流程建議改回「需核准」") {
            Chart {
                ForEach(Array(data)) { a in
                    BarMark(x: .value("成功率", a.successRate * 100), y: .value("流程", a.name))
                        .foregroundStyle(a.successRate >= 0.95 ? Color.green : a.successRate >= 0.9 ? Color.orange : Color.red)
                        .cornerRadius(4)
                        .annotation(position: .trailing) {
                            Text(Fmt.percent(a.successRate * 100, digits: 0)).font(.caption2).foregroundStyle(.secondary)
                        }
                }
                RuleMark(x: .value("門檻", 90))
                    .foregroundStyle(.secondary)
                    .lineStyle(StrokeStyle(lineWidth: 1, dash: [4, 3]))
            }
            .chartXScale(domain: 0...105)
            .chartXAxis { AxisMarks(values: [0, 25, 50, 75, 100]) { _ in AxisGridLine(); AxisValueLabel() } }
            .frame(height: max(150, CGFloat(data.count) * 28))
        }
    }

    private var costChart: some View {
        Card("每日 AI 成本", subtitle: "美元,依 tokens 用量估算") {
            Chart(costByDay) { d in
                AreaMark(x: .value("日期", d.day, unit: .day), y: .value("成本", d.cost))
                    .foregroundStyle(Color.purple.opacity(0.15).gradient)
                    .interpolationMethod(.monotone)
                LineMark(x: .value("日期", d.day, unit: .day), y: .value("成本", d.cost))
                    .foregroundStyle(.purple)
                    .interpolationMethod(.monotone)
                PointMark(x: .value("日期", d.day, unit: .day), y: .value("成本", d.cost))
                    .foregroundStyle(.purple)
                    .symbolSize(18)
            }
            .chartYAxis { AxisMarks { v in
                AxisGridLine()
                AxisValueLabel { if let d = v.as(Double.self) { Text(String(format: "$%.1f", d)) } }
            } }
            .chartXAxis { AxisMarks(values: .stride(by: .day, count: 2)) { _ in
                AxisGridLine(); AxisValueLabel(format: .dateTime.month(.defaultDigits).day())
            } }
            .frame(height: 230)
        }
    }

    // MARK: 排行

    private var table: some View {
        Card {
            HStack {
                Text("流程排行").font(.headline)
                Spacer()
                Picker("排序", selection: $sort) {
                    ForEach(AutoInsightSort.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 320)
            }
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 0) {
                GridRow {
                    Text("流程")
                    Text("自主程度")
                    Text("執行").gridColumnAlignment(.trailing)
                    Text("成功率").gridColumnAlignment(.trailing)
                    Text("省下").gridColumnAlignment(.trailing)
                    Text("成本").gridColumnAlignment(.trailing)
                }
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .padding(.vertical, 6)
                Divider()
                ForEach(sorted) { a in
                    GridRow {
                        Button { openAutomation(a.id) } label: {
                            HStack(spacing: 8) {
                                AutoIconTile(icon: a.icon, size: 22, dimmed: !a.enabled)
                                Text(a.name).lineLimit(1)
                                if !a.enabled { Text("已停用").font(.caption).foregroundStyle(.secondary) }
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        AutoAutonomyBadge(autonomy: a.autonomy)
                        Text("\(a.runs30d)").monospacedDigit()
                        Text(Fmt.percent(a.successRate * 100))
                            .monospacedDigit()
                            .foregroundStyle(a.successRate >= 0.95 ? Color.primary : a.successRate >= 0.9 ? .orange : .red)
                        Text(String(format: "%.1f h", a.timeSavedHours)).monospacedDigit()
                        Text(String(format: "$%.2f", cost30d(a))).monospacedDigit()
                    }
                    .padding(.vertical, 7)
                    if a.id != sorted.last?.id { Divider() }
                }
            }
        }
    }
}

enum AutoInsightSort: String, CaseIterable, Identifiable {
    case hours, runs, success, cost
    var id: String { rawValue }
    var label: String {
        switch self {
        case .hours: return "省下時間"
        case .runs: return "執行次數"
        case .success: return "成功率"
        case .cost: return "成本"
        }
    }
}
