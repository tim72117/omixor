import Charts
import SwiftUI

/// 儀表板分頁:選一個儀表板,畫出所有面板;依全域時間範圍裁切,滑鼠移上去看數值。
struct MonDashboardsTab: View {
    @EnvironmentObject var app: AppState
    @Binding var selectedId: String?

    private var dashboard: Dashboard? {
        app.dashboards.first { $0.id == selectedId } ?? app.dashboards.first
    }

    var body: some View {
        if app.dashboards.isEmpty {
            EmptyStateView(title: "沒有儀表板", icon: "chart.xyaxis.line", message: "尚未設定任何監控儀表板。")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    toolbar
                    if let d = dashboard { content(d) }
                }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            if app.dashboards.count <= 5 {
                Picker("儀表板", selection: Binding(get: { dashboard?.id ?? "" }, set: { selectedId = $0 })) {
                    ForEach(app.dashboards) { Text($0.name).tag($0.id) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
            } else {
                Picker("儀表板", selection: Binding(get: { dashboard?.id ?? "" }, set: { selectedId = $0 })) {
                    ForEach(app.dashboards) { Text($0.name).tag($0.id) }
                }
                .frame(maxWidth: 260)
            }
            Spacer()
            Label("最近 \(app.timeRange.label)", systemImage: "clock")
                .font(.callout).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private func content(_ d: Dashboard) -> some View {
        if d.panels.isEmpty {
            EmptyStateView(title: "這個儀表板沒有面板", icon: "square.dashed")
                .frame(maxWidth: .infinity, minHeight: 240)
        } else {
            let windowStart = Date().addingTimeInterval(-app.timeRange.seconds)
            let panels = d.panels.map { MonPanelData(series: $0, windowStart: windowStart) }
            if let main = panels.first {
                MonPanelCard(panel: main, height: 240, prominent: true)
            }
            if panels.count > 1 {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 340), spacing: 16)], spacing: 16) {
                    ForEach(panels.dropFirst()) { p in
                        MonPanelCard(panel: p, height: 150, prominent: false)
                    }
                }
            }
        }
    }
}

/// 已依時間範圍裁切的面板資料
struct MonPanelData: Identifiable {
    let series: MetricSeries
    let points: [MetricPoint]
    /// 時間範圍內沒資料時改用全部資料
    let usedFallback: Bool
    var id: String { series.id }

    init(series: MetricSeries, windowStart: Date) {
        self.series = series
        let inWindow = series.points.filter { $0.date >= windowStart }
        if inWindow.count >= 2 || series.points.isEmpty {
            points = inWindow
            usedFallback = false
        } else {
            points = series.points
            usedFallback = true
        }
    }

    var values: [Double] { points.map(\.value) }
    var latest: Double? { points.last?.value }
    var avg: Double? { values.isEmpty ? nil : values.reduce(0, +) / Double(values.count) }
    var max: Double? { values.max() }
    var min: Double? { values.min() }

    /// 依名稱與單位推測合理的門檻(只在資料接近門檻時顯示)
    var threshold: Double? {
        let name = series.name.lowercased() + " " + series.id.lowercased()
        let unit = series.unit.lowercased()
        var t: Double?
        if unit == "ms" { t = name.contains("p99") ? 1500 : 800 }
        else if unit == "%" {
            if name.contains("error") || name.contains("錯誤") || name.contains("5xx") { t = 1 }
            else if name.contains("cpu") || name.contains("記憶體") || name.contains("memory") || name.contains("mem") || name.contains("disk") || name.contains("磁碟") { t = 85 }
            else if name.contains("uptime") || name.contains("可用") || name.contains("成功") { t = 99.9 }
        }
        guard let t, let mx = max, let mn = min else { return nil }
        // 只有在門檻落在資料附近時才畫,避免把圖壓扁
        return (mx >= t * 0.5 && mn <= t * 1.5) ? t : nil
    }

    func format(_ v: Double) -> String {
        let u = series.unit
        let s: String
        if abs(v) >= 1000 { s = Fmt.number(v) }
        else if abs(v) >= 100 { s = String(format: "%.0f", v) }
        else if abs(v) >= 10 { s = String(format: "%.1f", v) }
        else { s = String(format: "%.2f", v) }
        if u.isEmpty { return s }
        return u == "%" ? s + "%" : "\(s) \(u)"
    }
}

struct MonPanelCard: View {
    let panel: MonPanelData
    let height: CGFloat
    let prominent: Bool

    @State private var hoverPoint: MetricPoint?

    private var color: Color {
        if let t = panel.threshold, let l = panel.latest, l >= t { return .red }
        return prominent ? .accentColor : .blue
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(panel.series.name).font(prominent ? .headline : .subheadline.weight(.semibold))
                    if panel.usedFallback {
                        Text("選取的時間範圍內沒有資料,顯示全部").font(.caption2).foregroundStyle(.orange)
                    }
                }
                Spacer()
                if let p = hoverPoint {
                    VStack(alignment: .trailing, spacing: 0) {
                        Text(panel.format(p.value)).font(.system(prominent ? .title3 : .body, design: .rounded).weight(.semibold)).monospacedDigit()
                        Text(p.date.formatted(date: .abbreviated, time: .shortened)).font(.caption2).foregroundStyle(.secondary)
                    }
                } else if let l = panel.latest {
                    VStack(alignment: .trailing, spacing: 0) {
                        Text(panel.format(l)).font(.system(prominent ? .title3 : .body, design: .rounded).weight(.semibold)).monospacedDigit()
                            .foregroundStyle(color == .red ? .red : .primary)
                        Text("目前").font(.caption2).foregroundStyle(.secondary)
                    }
                }
            }
            if panel.points.isEmpty {
                Text("沒有資料").font(.callout).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, minHeight: height)
            } else {
                chart
            }
            if prominent || !panel.points.isEmpty {
                stats
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.07)))
    }

    private var chart: some View {
        Chart {
            ForEach(panel.points) { p in
                AreaMark(x: .value("時間", p.date), y: .value(panel.series.unit, p.value))
                    .foregroundStyle(LinearGradient(colors: [color.opacity(0.22), color.opacity(0.02)], startPoint: .top, endPoint: .bottom))
                    .interpolationMethod(.monotone)
                LineMark(x: .value("時間", p.date), y: .value(panel.series.unit, p.value))
                    .foregroundStyle(color)
                    .lineStyle(StrokeStyle(lineWidth: prominent ? 2 : 1.5))
                    .interpolationMethod(.monotone)
            }
            if let t = panel.threshold {
                RuleMark(y: .value("門檻", t))
                    .foregroundStyle(.red.opacity(0.7))
                    .lineStyle(StrokeStyle(lineWidth: 1, dash: [4, 3]))
                    .annotation(position: .top, alignment: .leading) {
                        Text("門檻 \(panel.format(t))").font(.caption2).foregroundStyle(.red)
                    }
            }
            if let p = hoverPoint {
                RuleMark(x: .value("時間", p.date))
                    .foregroundStyle(Color.secondary.opacity(0.5))
                    .lineStyle(StrokeStyle(lineWidth: 1))
                PointMark(x: .value("時間", p.date), y: .value(panel.series.unit, p.value))
                    .foregroundStyle(color)
                    .symbolSize(prominent ? 60 : 40)
            }
        }
        .chartYScale(domain: .automatic(includesZero: panel.series.unit == "%" || (panel.min ?? 0) < (panel.max ?? 0) * 0.3))
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: prominent ? 8 : 4)) { _ in
                AxisGridLine().foregroundStyle(Color.primary.opacity(0.06))
                AxisValueLabel(format: xFormat)
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: .automatic(desiredCount: prominent ? 5 : 3)) { v in
                AxisGridLine().foregroundStyle(Color.primary.opacity(0.06))
                AxisValueLabel {
                    if let d = v.as(Double.self) { Text(axisLabel(d)) }
                }
            }
        }
        .chartOverlay { proxy in
            GeometryReader { geo in
                Rectangle().fill(.clear).contentShape(Rectangle())
                    .onContinuousHover { phase in
                        switch phase {
                        case .active(let loc):
                            guard let plot = proxy.plotFrame else { return }
                            let x = loc.x - geo[plot].origin.x
                            guard let date: Date = proxy.value(atX: x) else { return }
                            hoverPoint = nearest(to: date)
                        case .ended:
                            hoverPoint = nil
                        }
                    }
            }
        }
        .frame(height: height)
    }

    private var stats: some View {
        HStack(spacing: 18) {
            stat("平均", panel.avg)
            stat("最低", panel.min)
            stat("最高", panel.max)
            if let t = panel.threshold {
                let over = panel.values.filter { $0 >= t }.count
                VStack(alignment: .leading, spacing: 1) {
                    Text("超過門檻").font(.caption2).foregroundStyle(.secondary)
                    Text(panel.values.isEmpty ? "—" : Fmt.percent(Double(over) / Double(panel.values.count) * 100, digits: 0))
                        .font(.caption.monospacedDigit().weight(.medium))
                        .foregroundStyle(over > 0 ? .red : .primary)
                }
            }
            Spacer()
        }
    }

    private func stat(_ title: String, _ v: Double?) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title).font(.caption2).foregroundStyle(.secondary)
            Text(v.map(panel.format) ?? "—").font(.caption.monospacedDigit().weight(.medium))
        }
    }

    private var xFormat: Date.FormatStyle {
        guard let first = panel.points.first?.date, let last = panel.points.last?.date else {
            return .dateTime.hour().minute()
        }
        return last.timeIntervalSince(first) > 2 * 86400 ? .dateTime.month(.defaultDigits).day() : .dateTime.hour().minute()
    }

    private func axisLabel(_ v: Double) -> String {
        if abs(v) >= 1000 { return Fmt.number(v) }
        return v == v.rounded() ? String(format: "%.0f", v) : String(format: "%.1f", v)
    }

    private func nearest(to date: Date) -> MetricPoint? {
        let pts = panel.points
        guard !pts.isEmpty else { return nil }
        // points 依時間排序,用二分搜尋
        var lo = 0, hi = pts.count - 1
        while lo < hi {
            let mid = (lo + hi) / 2
            if pts[mid].date < date { lo = mid + 1 } else { hi = mid }
        }
        if lo > 0, abs(pts[lo - 1].date.timeIntervalSince(date)) < abs(pts[lo].date.timeIntervalSince(date)) {
            return pts[lo - 1]
        }
        return pts[lo]
    }
}
